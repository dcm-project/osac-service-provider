# Specification: OSAC Service Provider — Milestone 5 (Status Reporting)

## 1. Overview

Milestone 5 per [issue #1](https://github.com/dcm-project/osac-service-provider/issues/1)'s
suggested delivery milestones: report Cluster/VM status changes to
`control-plane` asynchronously, via the messaging-system + CloudEvents
mechanism defined by the
[Service Provider Status Reporting enhancement](https://github.com/dcm-project/enhancements/blob/main/enhancements/state-management/service-provider-status-reporting.md),
rather than any synchronous HTTP callback.

Two new packages, delivered together as one milestone (not split across
phases — see DD-075):

- **`internal/statuspublisher`** — builds a CloudEvents v1.0 envelope for a
  given resource's status and publishes it to NATS JetStream, with
  indefinite, non-blocking, coalescing retry.
- **`internal/statuspoll`** — a periodic loop that lists all
  DCM-owned Clusters and VMs from OSAC, maps their status via Milestone 3/4's
  `cluster.MapStatus`/`vm.MapStatus`, derives a human-readable message, diffs
  against a local cache, and calls `statuspublisher` for anything new or
  changed.

**This spec covers status publishing and polling only.** Explicitly out of
scope:

- `Events`/`Watch`-based push notification from OSAC — the enhancement's own
  [Status Polling](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#status-polling)
  section treats it as an optional, undelivery-guaranteed latency
  optimization layered on top of polling, not a replacement for it (already
  the basis for Milestone 2's DD-010, which deferred vendoring
  `events_service.proto` for this exact reason).
- Any change to Milestone 3/4's REST response schemas (`Cluster.status_message`,
  currently defined in `openapi.yaml` but never populated by either
  milestone — see SC-M5-003) or to `cluster.MapStatus`/`vm.MapStatus`
  themselves. Both are consumed here exactly as Milestone 3/4 built and
  exported them ("kept and independently tested for a future async polling
  caller" — verbatim from both functions' doc comments).
- Fixing `osac-mock-provider`'s `List()` CEL-filter gap (SC-M5-001) or adding
  e2e coverage for status reporting — both flagged as known limitations,
  neither blocks this milestone's unit/integration-tier scope.
- Any change to `control-plane`'s consumer-side handling, including the
  dispatch-before-persist race traced during this milestone's design (see
  DD-074) — filed upstream as
  [control-plane#44](https://github.com/dcm-project/control-plane/issues/44),
  not fixed here.

**Reference documents:**

- [Service Provider Status Reporting enhancement](https://github.com/dcm-project/enhancements/blob/main/enhancements/state-management/service-provider-status-reporting.md) — the canonical CloudEvents contract (subject hierarchy, envelope attributes, `data` schema, status vocabularies) this spec implements
- [OSAC SP Enhancement](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md) — [Status Polling](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#status-polling)
- [Milestone 3 spec](./osac-sp-m3-cluster-crud.spec.md) (`internal/cluster.MapStatus`) and [Milestone 4 spec](./osac-sp-m4-vm-crud.spec.md) (`internal/vm.MapStatus`) — both consumed unchanged (§ above)
- OSAC public protos, vendored in Milestone 2: [`cluster_type.proto`](../../proto/osac/public/v1/cluster_type.proto) (`ClusterCondition`, `ClusterConditionType`), [`compute_instance_type.proto`](../../proto/osac/public/v1/compute_instance_type.proto) (`ComputeInstanceCondition`, `ComputeInstanceConditionType`) — conditions this milestone reads for message derivation
- `fulfillment-service`'s `docs/FILTER.md` — CEL filter syntax for the ownership-label filter reused from `internal/cluster/list.go`'s/`internal/vm/list.go`'s `ownershipFilter` constant
- [Design Decisions](../decisions/osac-sp.decisions.md) — DD-071..DD-075 (this milestone; DD-071..DD-073 ratify [PR #16](https://github.com/dcm-project/osac-service-provider/pull/16)'s proposed DD-200..DD-202, renumbered per that PR's own instruction now that this spec formally starts)

---

## 2. Architecture

```
cmd/osac-service-provider/main.go run()
  |
  +-- internal/osac.Bootstrap (M1/M2, unchanged)
  +-- internal/apiserver.Server (M1, unchanged)
  +-- internal/registration.Registrar (M1, unchanged)
  +-- internal/cluster.Service / internal/vm.Service (M3/M4, unchanged)
  |
  +-- internal/statuspublisher.Publisher            (NEW)
  |     NewPublisher(cfg.DCM.NATSURL, logger) -> dials NATS + JetStream
  |     Start(ctx)  -- background worker, indefinite retry, coalescing
  |     Publish(serviceType, id, status, message) -- non-blocking enqueue
  |
  +-- internal/statuspoll.Poller                    (NEW)
        New(clustersClient, computeInstancesClient, publisher, cfg, logger)
        Start(ctx) -- ticks every cfg.Status.PollInterval
          |
          +-- Clusters/List (ownership filter) --> cluster.MapStatus (M3)
          +-- ComputeInstances/List (ownership filter) --> vm.MapStatus (M4)
          |
          +-- diff against in-memory cache --> Publisher.Publish(...)
          +-- every N cycles: unconditional full resync --> Publisher.Publish(...)
```

`statuspublisher` has no import dependency on `statuspoll`, `internal/cluster`,
or `internal/vm` — it is a generic "CloudEvents-over-JetStream" transport,
parameterized by a `ServiceType{Subject, Type, Source}` value the caller
supplies. `statuspoll` is the only new package that imports Milestone 3/4's
`cluster`/`vm` packages, confining this milestone's build dependency on those
still-unmerged branches (#13/#14) to as small a surface as practical — see
DD-075's PR/validation strategy.

Both `Publisher.Start(ctx)` and `Poller.Start(ctx)` are non-blocking,
mirroring `internal/osac.Bootstrap.Start()` and
`internal/registration.Registrar.Start()`'s established "return immediately,
retry indefinitely in the background, never crash the process" convention
(`CLAUDE.md`).

---

## 3. Topic Dependency Graph

| # | Topic | Prefix | Depends On |
|---|-------|--------|------------|
| 1 | Status Event Publishing | PUBLISH | none new (uses `DCMConfig`) |
| 2 | Status Poll Loop | POLL | Topic 1; Milestone 2 (`Bootstrap.Conn()`); Milestone 3 (`cluster.MapStatus`); Milestone 4 (`vm.MapStatus`) |

```
Topic 1: Status Event Publishing  --->  Topic 2: Status Poll Loop
                                              (also depends on M2/M3/M4)
```

---

## 4. Topic Specifications

### 4.1 Status Event Publishing

#### Overview

`internal/statuspublisher.Publisher` builds and delivers CloudEvents v1.0
envelopes over NATS JetStream, matching the canonical spec's worked example
byte-for-byte (confirmed by an empirical round-trip spike against
`control-plane`'s own consumer-side `StatusEvent` struct — see DD-071).
`Publish` is fire-and-forget from the caller's perspective: it records the
*latest* known value for a given resource and returns immediately; a
background worker goroutine (`Start(ctx)`) drains and delivers these,
retrying indefinitely with backoff on failure and always delivering the most
recently enqueued value for a resource, never a stale superseded one.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-PUBLISH-010 | `DCMConfig` MUST gain a new field `NATSURL` (`env:"NATS_URL,notEmpty"` → `DCM_NATS_URL`) | MUST | DD-071 |
| REQ-PUBLISH-020 | `statuspublisher.NewPublisher(natsURL string, logger *slog.Logger, opts ...Option) (*Publisher, error)` MUST establish a NATS connection configured to retry indefinitely on connection loss (`nats.RetryOnFailedConnect(true)`, `nats.MaxReconnects(-1)`) and MUST NOT block indefinitely or crash the process if the broker is unreachable at startup | MUST | mirrors `Bootstrap`/`Registrar`'s non-blocking convention |
| REQ-PUBLISH-030 | The CloudEvents envelope MUST set: `id` = a fresh UUID per event (not the resource id); `source` = `dcm/providers/{provider_name}` (caller-supplied per service type); `type` = `dcm.status.{service_type}`; `subject` = `dcm.{service_type}`; `time` = current UTC time; `datacontenttype` = `application/json`; `data` = exactly `{"id": <resource id>, "status": <string>, "message": <string>}` — no `timestamp` field in `data` | MUST | DD-071; canonical spec §3 |
| REQ-PUBLISH-040 | The event MUST be published via JetStream (`js.Publish`), not core NATS (`nc.Publish`), to the subject `dcm.{service_type}` — identical to the envelope's own `subject` attribute (REQ-PUBLISH-030) | MUST | DD-072 |
| REQ-PUBLISH-050 | `Publisher.Publish(st ServiceType, resourceID, status, message string)` MUST be non-blocking: it records the given value as the latest pending update for the key `(st.Subject, resourceID)` and returns immediately, without waiting for network I/O | MUST | |
| REQ-PUBLISH-060 | `Publisher.Start(ctx)` MUST launch exactly one background worker goroutine, idempotently (repeated calls are no-ops beyond the first); `Publisher.Done()` MUST return a channel closed once that worker returns (on `ctx` cancellation) | MUST | mirrors `Registrar.Start`/`Done` |
| REQ-PUBLISH-070 | On a JetStream publish failure, the worker MUST retry with exponential backoff (configurable initial/max, default `1s`/`60s`) before its next attempt; no enqueued update may be silently dropped | MUST | DD-072 |
| REQ-PUBLISH-080 | If a newer update for the same `(serviceType, resourceID)` key is enqueued while an older one for that same key is still pending or being retried, only the newer value MUST ultimately be delivered — the worker MUST NOT deliver a stale value after a newer one was already recorded | MUST | coalescing; prevents out-of-order status regressions |
| REQ-PUBLISH-090 | `Publisher.Close()` MUST close the underlying NATS connection | MUST | |

#### Configuration Introduced

| Env Var | Field | Default | Notes |
|---------|-------|---------|-------|
| `DCM_NATS_URL` | `DCMConfig.NATSURL` | none (required) | DD-071: lives on `DCMConfig`, not `SP_`-prefixed, since the NATS broker is a shared DCM-wide backend like `DCM_REGISTRATION_URL`, not provider-specific |

#### Acceptance Criteria

##### AC-PUBLISH-010: Envelope attributes match the canonical spec exactly

- **Validates:** REQ-PUBLISH-030
- **Given** a `ServiceType{Subject: "dcm.vm", Type: "dcm.status.vm", Source: "dcm/providers/osac-sp-vm"}` and a status update `(resourceID="vm-1", status="RUNNING", message="instance is running")`
- **When** the envelope is built
- **Then** the marshaled JSON MUST have `source="dcm/providers/osac-sp-vm"`, `type="dcm.status.vm"`, `subject="dcm.vm"`, a non-empty `time`, `datacontenttype="application/json"`, and `data={"id":"vm-1","status":"RUNNING","message":"instance is running"}` exactly (no extra or missing keys in `data`)
- **And** `id` (the envelope attribute) MUST NOT equal `"vm-1"` (it is a distinct, fresh event identifier, not the resource id)

##### AC-PUBLISH-020: Publish is non-blocking and delivered via JetStream to the correct subject

- **Validates:** REQ-PUBLISH-040, REQ-PUBLISH-050
- **Given** a `Publisher` wired to a fake `jsPublisher` collaborator (hand-written fake, not a mocking framework) whose `Publish` call blocks until signaled
- **When** `Publisher.Publish(...)` is called
- **Then** it MUST return before the fake's `Publish` call is signaled to unblock (proving non-blocking enqueue)
- **And** once the worker delivers it, the fake MUST have received the call on subject `dcm.{service_type}` (matching the envelope's `subject`)

##### AC-PUBLISH-030: Failed publishes retry with backoff and are never dropped

- **Validates:** REQ-PUBLISH-060, REQ-PUBLISH-070
- **Given** a fake `jsPublisher` that fails the first two calls and succeeds on the third
- **When** `Publisher.Publish(...)` is called once and `Start(ctx)` is running
- **Then** the fake MUST eventually record exactly one successful publish of that value (after two failed attempts, each separated by at least the configured initial backoff)

##### AC-PUBLISH-040: A newer update supersedes an older one still pending delivery

- **Validates:** REQ-PUBLISH-080
- **Given** a fake `jsPublisher` whose first call blocks until signaled, and `Publisher.Publish(...)` is called twice in quick succession for the same `(serviceType, resourceID)` with different `status` values (`"PROVISIONING"` then `"RUNNING"`) before the worker's first attempt completes
- **When** the fake is allowed to proceed
- **Then** the fake MUST NOT ever be called with the stale `"PROVISIONING"` payload as the final delivered value for that resource — the last observed successful publish for that key MUST carry `"RUNNING"`

##### AC-PUBLISH-050: `Start`/`Done` are idempotent and mirror `Registrar`'s lifecycle shape

- **Validates:** REQ-PUBLISH-060
- **Given** a constructed `Publisher`
- **When** `Start(ctx)` is called twice, then `ctx` is cancelled
- **Then** `Done()` MUST close exactly once, and no test observes two worker goroutines running concurrently (e.g. via a call counter with `-race` clean)

#### Dependencies

None new — uses the existing `DCMConfig` struct and this repo's established
`Option`/backoff conventions.

---

### 4.2 Status Poll Loop

#### Overview

`internal/statuspoll.Poller` periodically lists every DCM-owned Cluster and
VM from OSAC, computes each one's current `(status, message)`, and reports
any new/changed/disappeared resource to `statuspublisher`. It is the only
new component in this milestone that imports Milestone 3's `internal/cluster`
and Milestone 4's `internal/vm` packages (for `MapStatus`), and therefore the
only part of this milestone's own package that cannot compile until those
two milestones land on `main` — see DD-075.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-POLL-010 | A new `StatusConfig` (`envPrefix:"SP_STATUS_"`) MUST provide `PollInterval time.Duration` (`env:"POLL_INTERVAL"`, default `30s`) and `ResyncEvery int` (`env:"RESYNC_EVERY"`, default `10`) | MUST | |
| REQ-POLL-020 | Each tick MUST list all Clusters (`publicv1.ClustersClient.List`) and all ComputeInstances (`publicv1.ComputeInstancesClient.List`) using the ownership CEL filter `this.metadata.labels["dcm.io/managed-by"] == "dcm"` (identical string to `internal/cluster/list.go`'s/`internal/vm/list.go`'s `ownershipFilter`), paging via `offset`/`limit` until every page (`total`) has been retrieved — not just the first page | MUST | |
| REQ-POLL-030 | For each listed Cluster/ComputeInstance, the Poller MUST compute its status via `cluster.MapStatus(nil, item.GetStatus())` / `vm.MapStatus(nil, item.GetStatus())` and a message via REQ-POLL-060/REQ-POLL-070 | MUST | |
| REQ-POLL-040 | The Poller MUST maintain an in-memory cache of the last-known `(status, message)` per resource id, scoped independently per service type; for any listed item whose current `(status, message)` differs from the cache (or that is absent from the cache), it MUST call `Publisher.Publish(...)` immediately and then update the cache to the new value | MUST | |
| REQ-POLL-050 | For any resource id present in the cache but absent from the current cycle's listing, the Poller MUST call `Publisher.Publish(...)` exactly once with status `DELETED` and a synthesized "resource no longer found" message, then remove that id from the cache | MUST | no live gRPC error exists for this case — DELETED is constructed directly, not derived via `MapStatus` |
| REQ-POLL-060 | **Cluster message derivation:** map the computed `v1alpha1.ClusterStatus` to the corresponding `ClusterConditionType` (`ACTIVE`→`READY`, `PROGRESSING`→`PROGRESSING`, `DEGRADED`→`DEGRADED`, `FAILED`→`FAILED`); if `status.GetConditions()` contains that type, use its `GetMessage()` if non-empty, else `GetReason()` if non-empty, else a synthesized default string of the form `"cluster is <lowercase status>"`; `DELETING`, `UNAVAILABLE`, and `DELETED` MUST always use the synthesized default (no corresponding condition type exists in `ClusterConditionType`) | MUST | grounded in `cluster_type.proto` lines 292-338 |
| REQ-POLL-070 | **VM message derivation:** use a synthesized default string of the form `"vm is <lowercase status>"` for the computed `v1alpha1.VMStatus`; additionally, if `status.GetConditions()` contains a `COMPUTE_INSTANCE_CONDITION_TYPE_RESTART_FAILED` condition with `status == CONDITION_STATUS_TRUE`, the reported message MUST incorporate that condition's `GetMessage()` (fallback `GetReason()`) regardless of the primary derived status | MUST | grounded in `compute_instance_type.proto` lines 290-358; VM's own `MapStatus` doc comment confirms conditions are otherwise not consulted for VM status (asymmetric by design, not a gap) |
| REQ-POLL-080 | Counting the very first tick as cycle 0, every `ResyncEvery`-th cycle (cycle 0, `ResyncEvery`, `2*ResyncEvery`, ...) MUST unconditionally call `Publisher.Publish(...)` for every currently-listed resource's current `(status, message)`, regardless of whether it differs from the cache | MUST | DD-074; cycle 0 being a resync subsumes cold start as this rule's natural first case — no separate code path |
| REQ-POLL-090 | If a `List` RPC fails for one service type in a given cycle, the Poller MUST log the error and skip only that service type's processing for that cycle; the other service type's processing (if its own `List` succeeded) MUST still proceed, and the loop MUST continue to its next scheduled tick regardless | MUST | one bad cycle never stops the loop |
| REQ-POLL-100 | `Poller.Start(ctx)` MUST be non-blocking and called from `cmd/osac-service-provider/main.go`'s `run()` alongside `Bootstrap.Start()`/`Registrar.Start()`/`Publisher.Start()`, without waiting for HTTP server readiness | MUST | unlike `Registrar` (DD-091), the poll loop has no dependency on this SP's own HTTP endpoint being reachable |

#### Configuration Introduced

| Env Var | Field | Default | Notes |
|---------|-------|---------|-------|
| `SP_STATUS_POLL_INTERVAL` | `StatusConfig.PollInterval` | `30s` | |
| `SP_STATUS_RESYNC_EVERY` | `StatusConfig.ResyncEvery` | `10` | ~5 min at the default interval |

#### Acceptance Criteria

##### AC-POLL-010: List uses the ownership filter and pages through all results

- **Validates:** REQ-POLL-020
- **Given** a fake `ClustersClient`/`ComputeInstancesClient` (hand-written, bufconn-free per this repo's unit-tier convention) serving 3 pages of results (`limit=2`, `total=5`)
- **When** one poll cycle runs
- **Then** the fake MUST have received exactly 3 `List` calls, each with `Filter == "this.metadata.labels[\"dcm.io/managed-by\"] == \"dcm\""`, and the Poller MUST have observed all 5 items

##### AC-POLL-020: A changed status is published immediately; an unchanged one is not

- **Validates:** REQ-POLL-040
- **Given** a cached resource with status `PROGRESSING`, and a poll cycle in which OSAC now reports it as `ACTIVE`
- **When** the cycle runs
- **Then** a fake `Publisher` MUST record exactly one `Publish` call for that resource with `status="ACTIVE"`
- **And** given a second cycle in which the status is unchanged, the fake MUST record zero additional `Publish` calls for that resource

##### AC-POLL-030: A disappeared resource is reported as DELETED exactly once

- **Validates:** REQ-POLL-050
- **Given** a cached resource observed in cycle 1, absent from cycle 2's listing
- **When** cycle 2 runs
- **Then** the fake `Publisher` MUST record exactly one `Publish` call for that resource with `status="DELETED"`
- **And** cycle 3 (also without that resource) MUST record zero further calls for it

##### AC-POLL-040: Cluster message derivation pulls from the matching condition

- **Validates:** REQ-POLL-060
- **Given** a `ClusterStatus` with `state=CLUSTER_STATE_READY` and a `conditions` entry `{type: READY, status: TRUE, message: "control plane healthy"}`
- **When** the message is derived
- **Then** it MUST equal `"control plane healthy"`
- **And given** the same state but the `READY` condition's `message` is empty and `reason="AllNodesReady"`, the derived message MUST equal `"AllNodesReady"`
- **And given** no matching condition at all, the derived message MUST equal the synthesized default (`"cluster is active"`)

##### AC-POLL-050: VM message derivation synthesizes a default and opportunistically surfaces RESTART_FAILED

- **Validates:** REQ-POLL-070
- **Given** a `ComputeInstanceStatus` with `state=COMPUTE_INSTANCE_STATE_RUNNING` and no conditions
- **When** the message is derived
- **Then** it MUST equal the synthesized default (`"vm is running"`)
- **And given** the same state plus a `RESTART_FAILED` condition with `status=TRUE, message="ssh key rotation failed"`
- **Then** the derived message MUST incorporate `"ssh key rotation failed"`

##### AC-POLL-060: Periodic full resync republishes every resource regardless of cache state

- **Validates:** REQ-POLL-080
- **Given** `ResyncEvery=3` and a resource whose status has not changed across cycles 0-3
- **When** cycles 0, 1, 2, 3 each run
- **Then** the fake `Publisher` MUST record `Publish` calls for that resource on cycles 0 and 3 only (the resync cycles), not on cycles 1 or 2

##### AC-POLL-070: A `List` failure for one service type does not stop the loop or the other service type

- **Validates:** REQ-POLL-090
- **Given** a fake `ClustersClient.List` that returns an error on cycle 1, while `ComputeInstancesClient.List` succeeds
- **When** cycles 1 and 2 run
- **Then** VM processing MUST still occur (and publish as appropriate) on cycle 1
- **And** cycle 2 MUST run normally for both service types (the loop was not stopped)

#### Dependencies

Depends on Topic 4.1 (Status Event Publishing), Milestone 2
(`Bootstrap.Conn()`), Milestone 3 (`cluster.MapStatus`), Milestone 4
(`vm.MapStatus`).

---

## 5. Cross-Cutting Concerns

**Debounce (enhancement doc's Risks/Mitigations table):** the enhancement
doc calls for "debounce logic to avoid sending updates for rapid status
oscillation ... within milliseconds." This milestone's fixed poll interval
(default 30s) is itself a structural debounce for a poll-based design — any
oscillation faster than one interval is invisible to the Poller by
construction (it only ever observes OSAC's state at tick boundaries). No
separate debounce timer is needed; this is noted here rather than left
implicit, since the enhancement doc phrases the requirement generically for
both push- and poll-based producers.

**Logging:** publish failures/retries (REQ-PUBLISH-070) and per-cycle `List`
failures (REQ-POLL-090) are logged at `Warn`; no new structured-logging
mechanism beyond the `*slog.Logger` already threaded through every other
component.

---

## 6. Consolidated Configuration Reference

| Env Var | Struct.Field | Default | Required |
|---------|--------------|---------|----------|
| `DCM_NATS_URL` | `DCMConfig.NATSURL` | — | Yes |
| `SP_STATUS_POLL_INTERVAL` | `StatusConfig.PollInterval` | `30s` | No |
| `SP_STATUS_RESYNC_EVERY` | `StatusConfig.ResyncEvery` | `10` | No |

See `osac-sp.spec.md` §6 for all prior configuration (unchanged).

---

## 7. Design Decisions

See `.ai/decisions/osac-sp.decisions.md` for the full text of:

- **DD-071** — `DCM_NATS_URL` on `DCMConfig`, CloudEvents envelope built via
  `cloudevents-sdk-go`, and the resolved `data={id,status,message}` shape
  (ratifies PR #16's proposed DD-200, folded together with the `data`-shape
  correction found while drafting this spec — see SC-M5-002).
- **DD-072** — JetStream (`js.Publish`) over core NATS, wrapped in an
  indefinite-retry, coalescing background worker (ratifies PR #16's proposed
  DD-201, refined from "indefinite retry loop" to the concrete
  coalescing-worker design in §4.1).
- **DD-073** — pinned dependency versions (`nats.go v1.50.0`,
  `nats-server/v2 v2.12.5` test-only, `cloudevents/sdk-go/v2 v2.16.2`)
  (ratifies PR #16's proposed DD-202).
- **DD-074** — periodic full resync (every `ResyncEvery` cycles) as the
  mitigation for `control-plane`'s traced dispatch-before-persist race
  (upstream issue [control-plane#44](https://github.com/dcm-project/control-plane/issues/44)).
- **DD-075** — single-PR delivery strategy for this milestone (why the
  publisher and poll loop are not split across two phases/PRs).

---

## 8. Spec Clarifications

### SC-M5-001: `osac-mock-provider`'s `List()` ignores the CEL `filter` field — known limitation, not fixed here

**Related requirements:** REQ-POLL-020

`internal/mockprovider/clusters.go`'s (and the equivalent ComputeInstances
mock's) `List()` implementation ignores the `filter` field entirely and
returns all stored items unconditionally. This milestone's unit/integration
tests assert the filter *string sent* by the Poller (AC-POLL-010), not real
server-side CEL evaluation, so the gap does not affect this milestone's test
coverage. It becomes relevant only if/when e2e coverage for status reporting
is added in a future milestone (tracked as a known limitation, not filed
upstream since `osac-mock-provider` is this repo's own test double, not a
real OSAC component).

### SC-M5-002: The canonical spec's own `VmStatus` type definition omits `id`, contradicting its own worked example and `control-plane`'s real consumer struct — resolved by following the real code

**Related requirements:** REQ-PUBLISH-030

The canonical spec's §3 defines `type VmStatus struct { Status string; Message string }` (no `id`) immediately above a worked example that constructs
`VmStatus{Id, "123-123", Status: "Running", Message: "VM is running."}` (which
also has a self-contradictory, unparseable field list, apparently a copy-paste
error from `ContainerStatus`/`StorageStatus`/`NetworkStatus`, all three of
which *do* declare `Id` in their type definitions just above it). Without an
`id`, `control-plane` could not attribute a `dcm.vm` status event to any
specific instance — the `subject`/`type` CloudEvents attributes identify only
the *service type*, never a specific resource. `control-plane`'s actual
running consumer code
(`internal/sp/consumer/consumer.go`'s `StatusEvent{Id, Status, Message,
Timestamp}`, confirmed via direct trace, not the enhancement doc) requires
`Id`. Per this repo's established precedent for resolving doc/code
conflicts in favor of real, running code (DD-010's "Phase 1 confirmation"),
this spec's `data` schema (REQ-PUBLISH-030) includes `id` for **both**
Cluster and VM, treating the doc's `VmStatus` field list as the error and its
own worked example (plus `control-plane`'s real struct) as authoritative.

### SC-M5-003: Milestone 3's `Cluster.status_message` REST field is unrelated to and unpopulated by this milestone

**Related requirements:** none (informational)

`api/v1alpha1/openapi.yaml` (Milestone 3, unmerged) defines a
`Cluster.status_message` REST response field, but `internal/cluster`'s
`toAPICluster` never sets it — confirmed by direct search, an
existing, unaddressed Milestone 3 gap. This milestone's `message` (in the
CloudEvents `data` payload) is a **separate** concept computed independently
by `internal/statuspoll` (REQ-POLL-060/070) for the async status-reporting
channel; it does not read from, write to, or otherwise interact with the
REST schema's `status_message` field. Fixing Milestone 3's gap (if desired)
is out of scope here.

---

## 9. Requirement ID Index

| Prefix | Topic | Count |
|--------|-------|-------|
| REQ-PUBLISH-NNN | 4.1: Status Event Publishing | 9 |
| REQ-POLL-NNN | 4.2: Status Poll Loop | 10 |
| **Total** | | **19** |

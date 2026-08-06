# Test Plan: OSAC Service Provider — Milestone 5 (Status Reporting)

Scope: unit and integration tests for Milestone 5 ("Status Reporting") as
specified in
[`.ai/specs/osac-sp-m5-status-reporting.spec.md`](../specs/osac-sp-m5-status-reporting.spec.md).
One file for both tiers, matching Milestone 3/4's convention.

**"Integration" tier for this milestone** means a real, unstubbed
collaborator crossing a protocol boundary this SP does not control — for
`internal/statuspublisher` that is a real embedded NATS/JetStream broker
(`nats-server/v2`, in-process, per DD-073); for `internal/statuspoll` that is
a real `bufconn`-backed gRPC server (mirroring Milestone 2's `SC-M2-003`
technique), since this milestone has no REST/HTTP surface of its own to
stand in for that role the way Milestone 1/3/4's "real HTTP server" tier
does.

**Framework:** Ginkgo v2 + Gomega. Unit tests:
`internal/statuspublisher/*_unit_test.go`,
`internal/statuspoll/*_unit_test.go` — hand-written fakes (no mocking
framework), no real network I/O. Integration tests:
`internal/statuspublisher/*_integration_test.go` (real embedded
`nats-server`), `internal/statuspoll/*_integration_test.go` (real
`bufconn` gRPC server + hand-written fake `Publisher`). Run with:

```bash
go run github.com/onsi/ginkgo/v2/ginkgo -r --race -focus "TC-U-4" ./internal/...
go run github.com/onsi/ginkgo/v2/ginkgo -r --race -focus "TC-I-4" ./internal/...
```

## Enforcement rules (gate before implementation, re-checked at REFACTOR)

Same four binding rules as Milestone 3/4's test plans (AC-first
Given/When/Then; no existence-only assertions; pyramid invariant — every
`REQ-*`/`AC-*` has both a `TC-U-*` and a `TC-I-*`; 100% coverage of new
testable code in `internal/statuspublisher/` and `internal/statuspoll/`).
One addition specific to this milestone:

5. **Golden-JSON contract test is mandatory, not optional (DD-073).** At
   least one test (TC-I-400) MUST assert the producer's real marshaled
   `data` JSON against the canonical schema byte-for-byte (exact key set,
   exact values), publishing and consuming through a real broker — not a
   hand-rolled struct asserted against itself. This is the test DD-073
   requires to exist "from day one," per the org-wide contract-test gap
   traced while drafting the spec (`kubevirt-service-provider#35`).

---

## 1. Unit tests: Envelope construction (`internal/statuspublisher`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-400 | Envelope attributes are set exactly per the canonical spec | REQ-PUBLISH-030, AC-PUBLISH-010 | Build an envelope for `ServiceType{Subject:"dcm.vm", Type:"dcm.status.vm", Source:"dcm/providers/osac-sp-vm"}` and `(resourceID="vm-1", status="RUNNING", message="instance is running")`; unmarshal the result and assert `source`, `type`, `subject`, `datacontenttype` equal the exact expected strings, `time` is non-empty and parses as RFC 3339, and `data` equals exactly `{"id":"vm-1","status":"RUNNING","message":"instance is running"}` (no extra/missing keys, verified via exact map equality, not substring checks). |
| TC-U-401 | The envelope `id` is a fresh, non-resource identifier on every call | REQ-PUBLISH-030, AC-PUBLISH-010 | Build two envelopes for the same resource with different status values; assert both envelopes' `id` attributes are non-empty, valid UUIDs, and differ from each other and from the resource id `"vm-1"`. |
| TC-U-402 | Cluster and VM service types produce the documented distinct subject/type/source | REQ-PUBLISH-030 | Table-driven over `{Subject:"dcm.cluster",...}` and `{Subject:"dcm.vm",...}`; assert each produces its own documented `subject`/`type`/`source` triple, never the other's. |

**Coverage note:** `buildEnvelope`'s two error-wrap branches
(`ev.SetData`'s and `json.Marshal`'s — [publisher.go](../../internal/statuspublisher/publisher.go))
and `deliver`'s corresponding `buildEnvelope` error branch are not exercised
by any TC here — `StatusPayload`'s fields are all plain strings, so no input
reachable from this codebase can make `json.Marshal` fail today. Documented
as accepted coverage exceptions in the code rather than tested with a
fabricated failing fake, consistent with this suite's "test real production
types" convention (see `.ai/test-plans/osac-sp-unit.test-plan.md`, section
4's coverage note, and `registration.go`'s matching exception).

---

## 2. Unit tests: Publish lifecycle — non-blocking, retry, coalescing (`internal/statuspublisher`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-410 | `Publish` returns before delivery completes | REQ-PUBLISH-050, AC-PUBLISH-020 | Inject a fake `jsPublisher` whose `Publish` method blocks on a channel until signaled; call `Publisher.Publish(...)`; assert the call returns immediately (bounded by a short test timeout) without the fake having been signaled yet. |
| TC-U-411 | The correct subject is used for delivery | REQ-PUBLISH-040, AC-PUBLISH-020 | Fake `jsPublisher` records the subject of each call; call `Publish` for `ServiceType{Subject:"dcm.cluster",...}`; once `Start(ctx)` delivers it, assert the fake recorded exactly `"dcm.cluster"`. |
| TC-U-412 | Failed publishes retry with exponential backoff, never silently dropped | REQ-PUBLISH-070, AC-PUBLISH-030 | Fake `jsPublisher` fails the first two calls, succeeds on the third; call `Publish` once, run `Start(ctx)`; assert the fake eventually records exactly one successful call carrying the original value, and that at least `initialBackoff` elapsed between the first and second attempts (test uses a short configured backoff, e.g. `WithInitialBackoff(5*time.Millisecond)`). |
| TC-U-413 | A newer update is the final delivered value even if a stale one was already in flight | REQ-PUBLISH-080, AC-PUBLISH-040 | Fake `jsPublisher`'s first call blocks on a channel; call `Publish(st, "vm-1", "PROVISIONING", ...)`, then before unblocking the fake, call `Publish(st, "vm-1", "RUNNING", ...)` again; unblock the fake; assert the *last* observed successful publish for `vm-1` carries `status=="RUNNING"` (per AC-PUBLISH-040's exact wording, the already in-flight `"PROVISIONING"` call may still land on the wire — its bytes were built and handed to the fake before it blocked — but it must never be the final one). |
| TC-U-414 | Two different resources are delivered independently (no cross-key coalescing) | REQ-PUBLISH-080 | Call `Publish` for `("vm-1", "RUNNING")` and `("vm-2", "STOPPED")`; assert the fake eventually records one delivery for each id with its own distinct status — proving coalescing is scoped per `(serviceType, resourceID)` key, not global. |
| TC-U-415 | `Start`/`Done` are idempotent and mirror `Registrar`'s shape | REQ-PUBLISH-060, AC-PUBLISH-050 | Call `Start(ctx)` twice; assert (via an internal call counter guarded by `-race`) only one worker goroutine ever runs; cancel `ctx`; assert `Done()`'s channel closes exactly once. |
| TC-U-416 | `Close` closes the underlying NATS connection | REQ-PUBLISH-090 | Construct a `Publisher` against a fake/real connection wrapper recording `Close()` calls; call `Publisher.Close()`; assert the underlying connection's `Close` was called exactly once. |
| TC-U-417 | `NewPublisher` wraps and returns a NATS connect failure | REQ-PUBLISH-020 | Call `NewPublisher("://not-a-valid-url", logger)` — `nats.Connect` fails synchronously on URL parsing, no live broker needed; assert a non-nil error wrapping "connecting to NATS" and a nil `*Publisher`. |

**Coverage note:** `NewPublisher`'s `jetstream.New(nc)` error-wrap branch is
not exercised by any TC here — `jetstream.New` is called with no
`JetStreamOpt`s, and only such an option's own error can make it fail, so no
input reachable from this codebase can trigger it today. Documented as an
accepted coverage exception in the code, consistent with this suite's "test
real production types" convention (see `.ai/test-plans/osac-sp-unit.test-plan.md`,
section 4's coverage note).

---

## 3. Unit tests: Poll cycle — listing, diffing, resync (`internal/statuspoll`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-450 | A poll cycle pages through all results using the ownership filter | REQ-POLL-020, AC-POLL-010 | Fake `ClustersClient`/`ComputeInstancesClient` (hand-written, in-process — no `bufconn` needed for pure logic tests) serve 3 pages (`limit=2, total=5`) each; run one cycle; assert each fake recorded exactly 3 `List` calls, each with `Filter=="this.metadata.labels[\"dcm.io/managed-by\"] == \"dcm\""`, and the Poller observed all 5 items per service type. |
| TC-U-451 | A changed status is published immediately; an unchanged one is not | REQ-POLL-040, AC-POLL-020 | Cycle 1: fake List returns a cluster with `state=PROGRESSING`. Cycle 2: same cluster now `state=READY`. Cycle 3: unchanged. Run all three; assert the fake `Publisher` recorded exactly one `Publish` call for that resource, on cycle 2, with `status=="ACTIVE"`. |
| TC-U-452 | A newly observed resource is published on first sight | REQ-POLL-040 | Cycle 1: fake List returns nothing. Cycle 2: fake List returns a new VM with `state=RUNNING`. Assert the fake `Publisher` recorded exactly one call, on cycle 2, for that VM. |
| TC-U-453 | A disappeared resource is reported DELETED exactly once | REQ-POLL-050, AC-POLL-030 | Cycle 1: resource present. Cycles 2, 3: absent. Assert the fake `Publisher` recorded exactly one call, on cycle 2, with `status=="DELETED"`, and zero further calls for that id on cycle 3. |
| TC-U-454 | Periodic full resync republishes every resource regardless of cache state | REQ-POLL-080, AC-POLL-060 | `ResyncEvery=3`; a resource's status is unchanged across cycles 0-3; assert the fake `Publisher` recorded calls for that resource on cycles 0 and 3 only. |
| TC-U-455 | A `List` failure for one service type does not stop the loop or block the other | REQ-POLL-090, AC-POLL-070 | Fake `ClustersClient.List` errors on cycle 1; fake `ComputeInstancesClient.List` succeeds; assert VM processing still occurs on cycle 1, and cycle 2 runs normally for both. |

---

## 4. Unit tests: Message derivation (`internal/statuspoll`, pure functions)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-460 | Cluster message pulls from the matching condition's `message`, falls back to `reason`, falls back to a synthesized default | REQ-POLL-060, AC-POLL-040 | Table-driven: (a) `ACTIVE` + `READY` condition with `message="control plane healthy"` → that string; (b) same but `message=""`, `reason="AllNodesReady"` → `"AllNodesReady"`; (c) `ACTIVE` with no matching condition → synthesized default `"cluster is active"`; (d) `DELETING`/`UNAVAILABLE`/`DELETED` → always the synthesized default regardless of any conditions present. |
| TC-U-461 | Cluster message derivation maps each non-error status to its own condition type | REQ-POLL-060 | Table-driven over `PROGRESSING`→`CLUSTER_CONDITION_TYPE_PROGRESSING`, `DEGRADED`→`CLUSTER_CONDITION_TYPE_DEGRADED`, `FAILED`→`CLUSTER_CONDITION_TYPE_FAILED`; assert each looks up the correspondingly-typed condition only, ignoring a differently-typed condition present in the same list. |
| TC-U-462 | VM message uses a synthesized default per status when no `RESTART_FAILED` condition is present | REQ-POLL-070, AC-POLL-050 | Table-driven over all 8 `v1alpha1.VMStatus` values with an empty condition list; assert each produces its own distinct synthesized default (e.g. `"vm is running"`, `"vm is stopped"`) — not a single generic string reused for every status. |
| TC-U-463 | A `TRUE` `RESTART_FAILED` condition's message is surfaced regardless of primary status | REQ-POLL-070, AC-POLL-050 | Status `RUNNING` plus a `RESTART_FAILED` condition with `status=TRUE, message="ssh key rotation failed"`; assert the derived message incorporates `"ssh key rotation failed"`. A `RESTART_FAILED` condition with `status=FALSE` present alongside MUST NOT be surfaced (assert the synthesized default is used instead). |

---

## 5. Integration test: golden-JSON contract (`internal/statuspublisher`, real embedded broker) — DD-073 mandatory

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-400 | Real publish-to-consume round trip produces the exact canonical `data` schema | REQ-PUBLISH-030, REQ-PUBLISH-040, DD-073 | Start a real embedded `nats-server` (JetStream enabled, per the spike's pinned `v2.12.5`); create a stream/durable consumer mirroring `control-plane`'s own config (`dcm-status` stream, subjects `dcm.*`, durable `control-plane`); construct a real `Publisher` against it; call `Publish` for a Cluster and a VM update; fetch the raw messages from the consumer; `json.Unmarshal` each into a `cloudevents.Event`, then `event.Data()` into `control-plane`'s exact real `StatusEvent{Id, Status, Message, Timestamp}` struct (vendored verbatim into the test file's fixtures, sourced from `control-plane`'s `internal/sp/consumer/consumer.go`, with a comment citing the exact file/commit); assert `Id`/`Status`/`Message` equal the published values exactly and `Timestamp` is the zero value (proving `timestamp` is never present in `data`, matching SC-M5-002 / DD-071). |
| TC-I-401 | A publish issued before the stream exists still eventually succeeds once created | REQ-PUBLISH-070 | Start the `Publisher`/`Start(ctx)` against a broker with no stream yet; call `Publish`; assert (via the fake's own retry-count instrumentation, or a short `Eventually`) at least one failed attempt is logged; create the stream; assert the message is eventually delivered and consumable. |

---

## 6. Integration test: end-to-end poll wiring (`internal/statuspoll`, real `bufconn` gRPC)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-450 | A full poll cycle reaches through real gRPC serialization to a real fake `Publisher` | REQ-POLL-020, REQ-POLL-030, REQ-POLL-040 | Stand up real `bufconn`-backed `ClustersServer`/`ComputeInstancesServer` fakes (mirroring `SC-M2-003`'s dial technique) seeded with one Cluster (`state=READY`, a `READY` condition with a known `message`) and one VM (`state=RUNNING`); construct a real `Poller` using `publicv1.NewClustersClient`/`NewComputeInstancesClient` dialed against those fakes, and a hand-written fake `Publisher`; run one cycle; assert the fake `Publisher` recorded exactly one call for the Cluster with `status=="ACTIVE"` and the condition's exact message, and exactly one call for the VM with `status=="RUNNING"` and the synthesized default message — proving the full chain (real gRPC wire round trip → `MapStatus` → message derivation → `Publisher.Publish`) end-to-end, not just direct Go calls between fakes. |

---

## Coverage Matrix

| REQ/AC | TC-U | TC-I |
|--------|------|------|
| REQ-PUBLISH-010 (config) | — (covered by `config` package's existing env-parsing tests; no new logic to unit-test) | — |
| REQ-PUBLISH-020 (connect, non-blocking) | TC-U-415, TC-U-417 | TC-I-401 |
| REQ-PUBLISH-030 / AC-PUBLISH-010 (envelope) | TC-U-400, TC-U-401, TC-U-402 | TC-I-400 |
| REQ-PUBLISH-040 / AC-PUBLISH-020 (JetStream subject) | TC-U-411 | TC-I-400 |
| REQ-PUBLISH-050 / AC-PUBLISH-020 (non-blocking) | TC-U-410 | TC-I-400 |
| REQ-PUBLISH-060 / AC-PUBLISH-050 (lifecycle) | TC-U-415 | TC-I-401 |
| REQ-PUBLISH-070 / AC-PUBLISH-030 (retry) | TC-U-412 | TC-I-401 |
| REQ-PUBLISH-080 / AC-PUBLISH-040 (coalescing) | TC-U-413, TC-U-414 | TC-I-400 |
| REQ-PUBLISH-090 (Close) | TC-U-416 | TC-I-400 (implicit via suite teardown) |
| REQ-POLL-010 (config) | — (env-parsing, no new logic) | — |
| REQ-POLL-020 / AC-POLL-010 (list+filter+paging) | TC-U-450 | TC-I-450 |
| REQ-POLL-030 (MapStatus call) | TC-U-451..455 (exercised via cycle outcomes) | TC-I-450 |
| REQ-POLL-040 / AC-POLL-020 (diff+publish) | TC-U-451, TC-U-452 | TC-I-450 |
| REQ-POLL-050 / AC-POLL-030 (disappeared→DELETED) | TC-U-453 | TC-I-450 (single-resource case implicitly covered by the same wiring proof) |
| REQ-POLL-060 / AC-POLL-040 (cluster message) | TC-U-460, TC-U-461 | TC-I-450 |
| REQ-POLL-070 / AC-POLL-050 (VM message) | TC-U-462, TC-U-463 | TC-I-450 |
| REQ-POLL-080 / AC-POLL-060 (resync) | TC-U-454 | TC-I-450 (single-cycle proof; multi-cycle resync timing is a unit-tier concern) |
| REQ-POLL-090 / AC-POLL-070 (partial failure) | TC-U-455 | TC-I-450 (single-cycle proof; failure-injection timing is a unit-tier concern) |
| REQ-POLL-100 (main.go wiring) | — (wiring-only; no branching logic to unit-test beyond what `TestMain`-style smoke coverage in `cmd/` already provides once M3/M4 land) | — |

Two `REQ-*` rows (`REQ-PUBLISH-010`, `REQ-POLL-010`) and one (`REQ-POLL-100`)
have no dedicated `TC-*`: they are pure struct-tag configuration (already
covered by `internal/config`'s existing generic env-parsing tests, which
exercise every `env:"..."` tag in the `Config` tree structurally) or
pure wiring with no conditional logic of its own. This mirrors Milestone
1/2's own treatment of `ServerConfig`/`OSACConfig` fields — not a pyramid
gap, since there is no independent business-logic branch in those rows to
cover twice.

---

## Requirement ID Index

| Prefix | Topic | TC Count |
|--------|-------|----------|
| TC-U-4xx | Unit: envelope, publish lifecycle, poll cycle, message derivation | 20 |
| TC-I-4xx | Integration: golden-JSON contract, poll wiring | 3 |
| **Total** | | **23** |

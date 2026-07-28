# Specification: OSAC Service Provider — Milestone 2 (gRPC Client Generation)

## 1. Overview

Milestone 2 per [issue #1](https://github.com/dcm-project/osac-service-provider/issues/1)'s
suggested delivery milestones: extend the `buf`/`protoc` pipeline introduced
in Milestone 1 (DD-020 in
[`osac-sp.spec.md`](./osac-sp.spec.md#dd-020-minimal-capabilities-only-grpc-client-for-milestone-1))
to vendor and generate Go client stubs for the OSAC services the SP will
actually call once Cluster CRUD (Milestone 3), VM CRUD (Milestone 4), and
default network provisioning (also Milestone 4) land:
`osac.public.v1.Clusters`, `osac.public.v1.ComputeInstances`,
`osac.public.v1.Subnets`, `osac.public.v1.VirtualNetworks`.

**This spec covers Milestone 2 only.** No cluster/VM REST endpoints, no
business logic consuming these new clients, and no new HTTP or registration
wiring are in scope — those land in Milestones 3–5. This milestone's only
observable output is: (a) new vendored `.proto` files and their generated Go
stubs, and (b) one new public accessor — `internal/osac.Bootstrap.Conn()
*grpc.ClientConn` — provably reachable over the same `ClientConn`/auth path
Milestone 1 already established and tested. Milestones 3–4's handler code
constructs each typed client (`publicv1.NewClustersClient(bootstrap.Conn())`,
etc.) directly at the point of use, matching the pattern established below.

**Version scope (Milestone 2):**

- Vendor 10 additional `.proto` files from
  `osac-project/fulfillment-service`, pinned to the **same commit** already
  used for Milestone 1's `Capabilities` vendoring
  ([`73ae26e`](https://github.com/osac-project/fulfillment-service/tree/73ae26e8cb0a476d4b035b18776603f60a361ed9/proto/public/osac/public/v1)) —
  re-verified directly against that commit while drafting this spec, so the
  file list and RPC method names below are confirmed, not inferred from the
  enhancement doc's prose citations.
- Add `buf.build/bufbuild/protovalidate` as a new `buf.yaml` dependency
  (`metadata_type.proto` imports `buf/validate/validate.proto`, which
  Milestone 1's `buf.yaml` does not yet depend on).
- Add one new public accessor, `Bootstrap.Conn() *grpc.ClientConn`, exposing
  the exact `ClientConn` already established in Milestone 1 — no new dial
  options, no new auth/TLS logic, no new configuration keys. Milestones 3–4
  construct `Clusters`/`ComputeInstances`/`Subnets`/`VirtualNetworks` typed
  clients directly from it at each call site, per DD-020's ecosystem-precedent
  rationale.
- Explicitly **not** vendored this milestone: `events_service.proto`/
  `event_type.proto`, `host_types_service.proto`/`host_type_type.proto`,
  `instance_types_service.proto`/`instance_type_type.proto` — see DD-010 for
  why.

**Reference documents:**

- [OSAC SP Enhancement](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md) — [Integration Points](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#integration-points), [Node Sizing](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#node-sizing), [VM Sizing](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#vm-sizing) (both cited in DD-010 below)
- [Milestone 1 spec](./osac-sp.spec.md), specifically DD-020 (why only `Capabilities` was generated in M1) and the `internal/osac.Bootstrap` component it defines
- [Implementation plan (issue #1)](https://github.com/dcm-project/osac-service-provider/issues/1)
- OSAC public protos: [`osac-project/fulfillment-service/proto/public/osac/public/v1/`](https://github.com/osac-project/fulfillment-service/tree/73ae26e8cb0a476d4b035b18776603f60a361ed9/proto/public/osac/public/v1) — the specific service/RPC/import listings in this spec were read directly from this commit
- `proto/README.md` (Milestone 1) — vendoring convention and pinned-commit rationale this milestone extends, not replaces

---

## 2. Architecture

Extends Milestone 1's `internal/osac.Bootstrap` component — no other
component changes. Milestone 1 never exposed a public accessor for its one
client: the `Capabilities` client is a private `capClient` field, constructed
once in `Bootstrap.Start()` and used only internally by `Probe()` — there was
no reason to export it since M1 has no business logic that calls OSAC
directly. Milestone 2 introduces exactly one new public method,
`Bootstrap.Conn() *grpc.ClientConn`, exposing the same private connection
Milestone 1 already dials and authenticates. Milestone 3–4 handler code then
constructs each typed client directly at its point of use —
`publicv1.NewClustersClient(bootstrap.Conn())`, etc. — rather than the
`Bootstrap` type growing a named wrapper method per OSAC service. This
mirrors, exhaustively and without exception, how
`osac-project/fulfillment-service`'s own CLI consumes these same generated
types (~40 call sites across `create`/`get`/`describe`/`console` commands,
all `publicv1.NewXClient(conn)` at the call site, none through a per-service
wrapper) and how the two DCM sibling SPs facing an analogous "one shared
client, many resource kinds" shape do it too
(`k8s-container-service-provider` returns the raw `client-go`
`kubernetes.Interface`; `acm-cluster-service-provider` passes the raw
`controller-runtime` `client.Client`) — see DD-020.

```
+------------------------------------------------------------------+
|                    internal/osac.Bootstrap                       |
|                                                                    |
|   OIDC token source (unchanged, M1)      *grpc.ClientConn (M1)    |
|          |                                        |                |
|          +--------- PerRPCCredentials ------------+                |
|                                                    |                |
|                        (private, M1)               |  Conn() (public, M2)
|                        capClient                   |  -- returns the
|                     (used by Probe())               \\    same *grpc.ClientConn
|                                                       v
|                                    (Milestone 3/4, not this milestone)
|                     publicv1.NewClustersClient(bootstrap.Conn())
|                     publicv1.NewComputeInstancesClient(bootstrap.Conn())
|                     publicv1.NewSubnetsClient(bootstrap.Conn())
|                     publicv1.NewVirtualNetworksClient(bootstrap.Conn())
+------------------------------------------------------------------+
```

All five client types (`Capabilities` plus the four new ones) are generated
into the same `internal/osacpb/osac/public/v1` Go package (`publicv1`, per
M1's `buf.gen.yaml`).

No new inbound wiring (HTTP routes, registration payloads) changes in this
milestone.

---

## 3. Topic Dependency Graph

| # | Topic                          | Prefix | Depends On             |
|---|----------------------------------|--------|-------------------------|
| 1 | Proto Vendoring & Codegen         | PROTO  | Milestone 1 (buf pipeline) |
| 2 | Shared Connection Accessor        | GRPC   | Topic 1; Milestone 1 Topic 4.2 (OSAC Client Bootstrap) |

```
Topic 1: Proto Vendoring & Codegen  --->  Topic 2: Shared Connection Accessor
                                                (also depends on M1's Bootstrap)
```

---

## 4. Topic Specifications

### 4.1 Proto Vendoring & Codegen

#### Overview

Vendor the `.proto` files needed to later call `Clusters`, `ComputeInstances`,
`Subnets`, and `VirtualNetworks`, and regenerate Go stubs alongside the
existing `Capabilities` stubs via the same `buf` pipeline. This is a
code-generation-only topic: no runtime behavior changes, no business logic.

Out of scope (see DD-010): `Events`/`event_type.proto` (optional streaming
supplement to polling, not required for Milestone 5's polling-based status
reporting, and its transitive import closure pulls in unrelated admin-plane
types), `HostTypes`, `InstanceTypes` (not called by this SP in v1 per the
enhancement's own Node Sizing / VM Sizing resolutions — see DD-010).

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-PROTO-010 | The SP MUST vendor the following files from `osac-project/fulfillment-service` at commit `73ae26e8cb0a476d4b035b18776603f60a361ed9` (the same commit already pinned for Milestone 1): `clusters_service.proto`, `cluster_type.proto`, `compute_instances_service.proto`, `compute_instance_type.proto`, `subnets_service.proto`, `subnet_type.proto`, `virtual_networks_service.proto`, `virtual_network_type.proto`, `metadata_type.proto`, `condition_status_type.proto` | MUST | DD-010 |
| REQ-PROTO-020 | Vendored files MUST be copied byte-for-byte verbatim from the pinned commit (same discipline as Milestone 1) | MUST | |
| REQ-PROTO-030 | `proto/README.md` MUST be updated to list every newly vendored file under the same "pinned to this exact commit" convention established in Milestone 1, and MUST correct (not merely append to) Milestone 1's forward-looking "Milestone 2 replaces this..." paragraph, which is stale in three ways per direct comparison against this spec — see SC-M2-002 | MUST | SC-M2-002 |
| REQ-PROTO-040 | `buf.yaml`'s `deps` MUST include `buf.build/bufbuild/protovalidate`, required transitively by `metadata_type.proto`'s `buf/validate/validate.proto` import | MUST | SC-M2-001 |
| REQ-PROTO-050 | `make generate-proto` MUST regenerate Go client and message stubs for `Clusters`, `ComputeInstances`, `Subnets`, and `VirtualNetworks` into `internal/osacpb/osac/public/v1/`, without altering the previously-generated `Capabilities`/`authn_capabilities` output (regeneration MUST be idempotent for unrelated files) | MUST | |
| REQ-PROTO-060 | `make check-generate-proto` MUST pass in CI after this milestone's vendored files and generated code are committed (extends the existing CI check — no new workflow needed) | MUST | |

#### Configuration Introduced

None.

#### Acceptance Criteria

##### AC-PROTO-010: New proto files vendored at the pinned commit

- **Validates:** REQ-PROTO-010, REQ-PROTO-020
- **Given** the file list in REQ-PROTO-010
- **When** each vendored file under `proto/osac/public/v1/` is diffed against the same path at commit `73ae26e8cb0a476d4b035b18776603f60a361ed9` in `osac-project/fulfillment-service`
- **Then** the diff MUST be empty (byte-for-byte identical)

##### AC-PROTO-020: `proto/README.md` documents the new files

- **Validates:** REQ-PROTO-030
- **Given** `proto/README.md`
- **When** its vendored-file list is read
- **Then** it MUST list all 10 newly vendored files (in addition to Milestone 1's 2) alongside the same pinned-commit reference

##### AC-PROTO-030: Generated code matches committed code

- **Validates:** REQ-PROTO-050, REQ-PROTO-060
- **Given** a clean checkout of this milestone's branch
- **When** `make generate-proto` is run and its output is diffed against the committed `internal/osacpb/` tree
- **Then** the diff MUST be empty (`make check-generate-proto` passes)

#### Dependencies

Depends on Milestone 1's `buf.yaml`/`buf.gen.yaml` pipeline.

---

### 4.2 Shared Connection Accessor

#### Overview

Add one new **public** method to `internal/osac.Bootstrap` — `Conn()
*grpc.ClientConn` — the first public accessor `Bootstrap` exposes (Milestone
1's `Capabilities` client remains a private field used only by `Probe()`; see
§2 Architecture). It returns the exact same `*grpc.ClientConn` Milestone 1
already dials and authenticates (REQ-OSAC-030/040/050/020 in
`osac-sp.spec.md`). No new `ClientConn`, no new dial options, no new per-RPC
credentials logic, no new configuration keys — see DD-020.

This milestone deliberately does **not** add per-service wrapper methods
(e.g. no `ClustersClient()`). DD-020 documents why: it would be inventing a
pattern with no precedent anywhere in the evidence this project checked,
including the upstream project that defines these exact generated types.

Out of scope: any HTTP handler, business logic, or REST endpoint consuming
`Conn()` (Milestones 3–4, which construct
`publicv1.NewClustersClient(bootstrap.Conn())` etc. directly at the call
site); pagination/field-mapping/error-translation logic for the eventual CRUD
handlers; anything involving `Events`, `HostTypes`, or `InstanceTypes`
(Topic 4.1 out-of-scope note).

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-GRPC-010 | `internal/osac.Bootstrap` MUST expose `Conn() *grpc.ClientConn`, returning the same connection field already used internally by the `Capabilities` client — MUST NOT dial a new connection, apply different TLS/insecure credentials, or bypass the existing bearer-token `PerRPCCredentials` | MUST | DD-020 |
| REQ-GRPC-020 | Callers constructing a typed client from `Conn()` (e.g. `publicv1.NewClustersClient(bootstrap.Conn())`) MUST be able to complete a real gRPC call (full marshal/unmarshal round trip) against a running OSAC-compatible server using only the bootstrap already configured in Milestone 1 — no additional setup | MUST | |

#### Configuration Introduced

None — reuses Milestone 1's `osac.fulfillmentAddress`, `osac.tlsEnabled`,
`osac.tlsCertFile`.

#### Acceptance Criteria

##### AC-GRPC-010: `Conn()` returns the exact shared, authenticated connection

- **Validates:** REQ-GRPC-010
- **Given** a `Bootstrap` constructed with a known `*grpc.ClientConn` (e.g. dialed against an in-test `bufconn.Listener`, per Milestone 1's existing test pattern)
- **When** `Conn()` is called
- **Then** it MUST return that exact `*grpc.ClientConn` value (pointer-identity or equivalent-behavior check)
- **And** issuing a call through a client constructed from it (e.g. `publicv1.NewClustersClient(bootstrap.Conn())`) MUST reach the same `bufconn` server the existing internal `Capabilities` client reaches (proving no second connection is dialed)

##### AC-GRPC-020: Clients built from `Conn()` round-trip real data, not just "no error"

- **Validates:** REQ-GRPC-020
- **Given** a fake `bufconn`-backed server implementing `List` for `Clusters`, `ComputeInstances`, `Subnets`, and `VirtualNetworks`, each returning a canned response with specific, known field values (e.g. a cluster with `id="c1"`, `status.state=CLUSTER_STATE_READY` — `status` is the nested `ClusterStatus` message, `state` its `ClusterState` enum field, per `cluster_type.proto`'s `Cluster`/`ClusterStatus` definitions)
- **When** `publicv1.NewClustersClient(bootstrap.Conn())` (and the analogous constructor for each of the other three) is used to call `List`
- **Then** the decoded response's fields MUST equal the canned values exactly (not merely `len(results) > 0` or `err == nil`)

##### AC-GRPC-030: Clients built from `Conn()` inherit the shared bearer-token interceptor

- **Validates:** REQ-GRPC-010
- **Given** the bootstrap holds a known cached token value (e.g. `"tok-xyz"`)
- **And** the fake `bufconn` server records the `authorization` gRPC metadata it receives per call
- **When** a call is made via `publicv1.NewClustersClient(bootstrap.Conn())` (representative of all four)
- **Then** the recorded metadata MUST equal exactly `"Bearer tok-xyz"` — the same value/format Milestone 1 already proved the `Capabilities` client sends, confirming clients built from `Conn()` are not bypassing it

#### Dependencies

Depends on Topic 4.1 (Proto Vendoring & Codegen) and Milestone 1 Topic 4.2
(OSAC Client Bootstrap).

---

## 5. Cross-Cutting Concerns

None new. Logging and configuration-management requirements (`osac-sp.spec.md`
§5) are unchanged and already satisfied — this milestone introduces no new
configuration and no new operations requiring distinct log statements beyond
what Milestone 1's bootstrap already logs for connection/auth state.

---

## 6. Consolidated Configuration Reference

No new configuration keys. See `osac-sp.spec.md` §6 for the full table
(unchanged by this milestone).

---

## 7. Design Decisions

### DD-010: Vendor only what Cluster/VM CRUD and default networking need — defer `Events`, exclude `HostTypes`/`InstanceTypes`

**Decision:** Vendor exactly the 10 files listed in REQ-PROTO-010. Do **not**
vendor `events_service.proto`/`event_type.proto`,
`host_types_service.proto`/`host_type_type.proto`, or
`instance_types_service.proto`/`instance_type_type.proto` in this milestone.

**Rationale:**

- **`Events`/`event_type.proto` deferred, not required:** the enhancement's
  [Status Polling](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#status-polling)
  section states OSAC's `Events/Watch` "may supplement polling for lower
  latency" but "explicitly states 'the server doesn't make any guarantee
  about the delivery or order of these events' and recommends combining
  watch with periodic reconciliation" — polling (`Clusters/List` +
  `ComputeInstances/List`, Milestone 5) is the required mechanism; `Watch` is
  an optional latency optimization the team can add later. Vendoring it now
  would also be a poor size trade: verified directly against
  `event_type.proto` at the pinned commit, it imports
  `baremetal_instance_type.proto`, `cluster_template_type.proto`,
  `cluster_version_type.proto`, `compute_instance_template_type.proto`,
  `host_type_type.proto`, `instance_type_type.proto`,
  `role_binding_type.proto`, `project_type.proto`, `secret_type.proto`,
  `role_type.proto`, and `tenant_type.proto` — an `EventsWatchResponse`
  wrapper fanning out into every resource type OSAC exposes, including
  admin-plane concerns (projects, tenants, roles, secrets) this SP never
  touches. Revisit if/when the team decides polling latency is a real
  problem worth the added surface.
- **`HostTypes`/`InstanceTypes` excluded — the SP doesn't call them in v1:**
  the enhancement's own resolved open questions rule these out. [Node
  Sizing](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#node-sizing)'s
  resolution: "The OSAC SP does **not** maintain an internal size-tier
  matrix — it is a pass-through: whatever `template_id` arrives ... is sent
  to OSAC as-is" (no `HostTypes` lookup needed — `host_type` is
  template-fixed server-side, per the same section's validation-behavior
  citation). [VM
  Sizing](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#vm-sizing)'s
  resolution: "reviewers agreed to keep the direct mapping for v1 ...
  rather than resolving a best-fit `instance_type` from DCM's raw values" —
  best-fit matching against `InstanceTypes/List` is explicitly called
  "technically feasible today, but `OSAC-46` is still In Progress ... isn't
  worth the churn risk yet." Vendoring services the SP will never invoke in
  v1 would be unused generated code with no test coverage to justify it.

**Related requirements:** REQ-PROTO-010

### DD-020: Expose the raw shared `ClientConn` via `Conn()` — no per-service wrapper methods

**Decision:** `Bootstrap` exposes exactly one new public method, `Conn()
*grpc.ClientConn`, returning the exact same connection Milestone 1's
`Bootstrap` already holds. Milestone 3/4 handler code constructs each typed
client directly at its call site (`publicv1.NewClustersClient(bootstrap.Conn())`,
etc.) — `Bootstrap` does **not** grow a named wrapper method per OSAC service
(no `ClustersClient()`, `ComputeInstancesClient()`, and so on).

**Rationale — sharing one connection:** gRPC's `ClientConn` is designed to
back multiple generated service clients against the same server address, and
OSAC's fulfillment service exposes `Clusters`, `ComputeInstances`, `Subnets`,
`VirtualNetworks`, and `Capabilities` from a single gRPC endpoint
(`fulfillmentAddress`) — there is exactly one connection to make regardless
of how many service clients sit on top of it. Sharing one `ClientConn` also
means this milestone introduces zero new configuration keys:
`fulfillmentAddress`, `tlsEnabled`, and `tlsCertFile` are already loaded by
Milestone 1's `internal/config`.

**Rationale — no per-service wrapper methods, revised from an earlier draft
of this spec:** An earlier draft of this decision had `Bootstrap` expose four
named accessor methods instead. Checking for ecosystem precedent before
finalizing it turned up a unanimous counter-example. Every one of the ~40
call sites across `osac-project/fulfillment-service`'s own CLI
(`internal/cmd/cli/{create,get,describe,console}/...`) that needs a typed
client from this exact `publicv1` package constructs it inline —
`publicv1.NewClustersClient(conn)`, `publicv1.NewComputeInstancesClient(conn)`,
`publicv1.NewSubnetsClient(conn)`, `publicv1.NewVirtualNetworksClient(conn)`,
and a dozen others — from a connection already held by the calling code, with
no wrapper type exposing one named method per service anywhere in that
project. The two DCM sibling SPs facing the same "one shared client, many
resource kinds" shape agree: `k8s-container-service-provider`'s
`internal/kubernetes.NewClient` returns the stock `client-go`
`kubernetes.Interface` untouched, and callers use `client-go`'s own
`client.AppsV1().Deployments(ns)`-style accessors; `acm-cluster-service-provider`'s
`internal/cluster/dispatcher.New` takes the raw `sigs.k8s.io/controller-runtime/pkg/client.Client`
interface directly, with no per-resource wrapper. (The remaining sibling,
`kubevirt-service-provider`, wraps a single resource type in business-verb
methods — not directly comparable, since it never faced the "one client,
many independent service types" question this milestone does.) Building a
custom per-service wrapper here would have been introducing a pattern with
no precedent anywhere checked, including the upstream project that defines
these exact generated types — the same category of unforced, unverified
invention this project has already corrected twice on OIDC discovery
(`osac-sp.spec.md` DD-060) and once already in this milestone's own package
naming (see this spec's commit history).

**Related requirements:** REQ-GRPC-010, REQ-GRPC-020

---

## 8. Spec Clarifications

### SC-M2-001: `protovalidate`'s effect on `buf.gen.yaml`'s `managed.disable` list is unverified — confirm at implementation time

**Related requirements:** REQ-PROTO-040

Milestone 1 needed a `managed.disable` entry for `buf.build/googleapis/googleapis`
in `buf.gen.yaml` to stop the `go_package_prefix` override from rewriting
`google/api/*.proto`'s Go package to one this repo never generates (see the
`go mod tidy` failure documented in Milestone 1's implementation notes).
`metadata_type.proto` (newly vendored here) imports
`buf/validate/validate.proto`, which requires adding
`buf.build/bufbuild/protovalidate` as a `buf.yaml` dependency (REQ-PROTO-040)
— but whether that dependency's generated/resolved Go package needs the same
`managed.disable` treatment is **not yet verified**, since `protovalidate`'s
file is consumed primarily as message-option extensions (field constraints)
rather than as a service with its own generated client, and buf's handling
of option-only imports may differ from `googleapis`'s handling. Confirm by
running `make generate-proto` and checking whether `go build ./...` /
`go mod tidy` succeed without a `managed.disable` addition before assuming
one is needed; add the exclusion (mirroring the `googleapis` entry) only if
generation actually produces a broken import.

### SC-M2-002: Milestone 1's `proto/README.md` forward-reference is stale against this spec in three ways — must be corrected, not appended to

**Related requirements:** REQ-PROTO-030

Milestone 1's `proto/README.md` (in `feat/milestone-1-scaffold`, not yet on
`main`) ends with a forward-looking paragraph written before this spec
existed: *"Milestone 2 replaces this with the full proto set
(`clusters_service.proto`, `compute_instances_service.proto`,
`subnet_type.proto`, `virtual_network_type.proto`, `events_service.proto`)
... at that point, re-evaluate whether `fulfillment-service`'s BSR module has
been published and switch to a real `deps:` reference instead of vendoring."*
Verified directly against that text while drafting this spec — it is
inaccurate in three ways an implementer must not just leave in place:

1. **"Replaces" is wrong.** This spec's REQ-PROTO-010 vendors 10 *additional*
   files; the two Milestone 1 files (`capabilities_service.proto`,
   `authn_capabilities_type.proto`) stay vendored, since `Capabilities` is
   still used by the health check's connectivity probe (Milestone 1 DD-020).
2. **Incomplete file list.** It names the `*_type.proto` files for
   Subnets/VirtualNetworks but omits the corresponding `*_service.proto`
   files (`subnets_service.proto`, `virtual_networks_service.proto`) —
   REQ-PROTO-010 vendors both.
3. **`events_service.proto` is explicitly out of scope here** (DD-010) — the
   Milestone 1 author's assumption that M2 would need it did not hold up
   once the enhancement's own Status Polling section was checked directly.

The "re-evaluate BSR/`deps:`" idea is a legitimate open question this spec
does **not** resolve — REQ-PROTO-020 continues Milestone 1's byte-for-byte
vendoring discipline without re-checking whether
`buf.build/osac-project/public-api` has since had commits pushed. Whoever
implements Milestone 2 should do that check (`buf build
buf.build/osac-project/public-api` or equivalent) before writing the updated
`proto/README.md`, and note the outcome either way — don't silently
perpetuate an unverified assumption from Milestone 1 into Milestone 2's own
vendoring rationale.

---

## 9. Requirement ID Index

| Prefix | Topic | Count |
|--------|-------|-------|
| REQ-PROTO-NNN | 4.1: Proto Vendoring & Codegen | 6 |
| REQ-GRPC-NNN | 4.2: Shared Connection Accessor | 2 |
| **Total** | | **8** |

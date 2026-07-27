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
stubs, and (b) four new typed accessor methods on the existing
`internal/osac.Bootstrap` component, each provably reachable over the same
`ClientConn`/auth path Milestone 1 already established and tested.

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
- Extend `internal/osac.Bootstrap` with four new typed client accessors,
  reusing the exact `ClientConn` already established in Milestone 1 — no new
  dial options, no new auth/TLS logic, no new configuration keys.
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
component changes. The single `ClientConn` established in Milestone 1 now
backs five typed clients instead of one:

```
+------------------------------------------------------------------+
|                    internal/osac.Bootstrap                       |
|                                                                    |
|   OIDC token source (unchanged, M1)      *grpc.ClientConn (M1)    |
|          |                                        |                |
|          +--------- PerRPCCredentials ------------+                |
|                                                    |                |
|          +----------------+----------------+------+------+-----+  |
|          |                |                |             |     |  |
|          v                v                v             v     v  |
|   Capabilities()   ClustersClient()  ComputeInstances  Subnets  VirtualNetworks
|      (M1)               (M2)           Client() (M2)  Client()  Client()
|                                                          (M2)     (M2)
+------------------------------------------------------------------+
```

No new inbound wiring (HTTP routes, registration payloads) changes in this
milestone.

---

## 3. Topic Dependency Graph

| # | Topic                          | Prefix | Depends On             |
|---|----------------------------------|--------|-------------------------|
| 1 | Proto Vendoring & Codegen         | PROTO  | Milestone 1 (buf pipeline) |
| 2 | Generated Client Accessors        | GRPC   | Topic 1; Milestone 1 Topic 4.2 (OSAC Client Bootstrap) |

```
Topic 1: Proto Vendoring & Codegen  --->  Topic 2: Generated Client Accessors
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
| REQ-PROTO-030 | `proto/README.md` MUST be updated to list every newly vendored file under the same "pinned to this exact commit" convention established in Milestone 1 | MUST | |
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

### 4.2 Generated Client Accessors

#### Overview

Extend `internal/osac.Bootstrap` with four new typed accessor methods —
`ClustersClient()`, `ComputeInstancesClient()`, `SubnetsClient()`,
`VirtualNetworksClient()` — each wrapping the exact same `*grpc.ClientConn`
Milestone 1 already dials and authenticates (REQ-OSAC-030/040/050/020 in
`osac-sp.spec.md`). No new `ClientConn`, no new dial options, no new
per-RPC credentials logic, no new configuration keys — see DD-020.

Out of scope: any HTTP handler, business logic, or REST endpoint consuming
these clients (Milestones 3–4); pagination/field-mapping/error-translation
logic for the eventual CRUD handlers; anything involving `Events`,
`HostTypes`, or `InstanceTypes` (Topic 4.1 out-of-scope note).

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-GRPC-010 | `internal/osac.Bootstrap` MUST expose `ClustersClient() clusterspb.ClustersClient`, `ComputeInstancesClient() computeinstancespb.ComputeInstancesClient`, `SubnetsClient() subnetspb.SubnetsClient`, and `VirtualNetworksClient() virtualnetworkspb.VirtualNetworksClient` (package names indicative — exact generated package names depend on `buf.gen.yaml`'s `go_package_prefix`, matching the existing `internal/osacpb/osac/public/v1` layout) | MUST | |
| REQ-GRPC-020 | Each accessor MUST construct its client from the same `*grpc.ClientConn` field already used by the existing `Capabilities` client — no accessor MUST dial a new connection, apply different TLS/insecure credentials, or bypass the existing bearer-token `PerRPCCredentials` | MUST | DD-020 |
| REQ-GRPC-030 | Each new client type MUST be able to complete a real gRPC call (full marshal/unmarshal round trip) against a running OSAC-compatible server using only the bootstrap already configured in Milestone 1 — no additional setup | MUST | |

#### Configuration Introduced

None — reuses Milestone 1's `osac.fulfillmentAddress`, `osac.tlsEnabled`,
`osac.tlsCertFile`.

#### Acceptance Criteria

##### AC-GRPC-010: Accessors return correctly-typed, connection-sharing clients

- **Validates:** REQ-GRPC-010, REQ-GRPC-020
- **Given** a `Bootstrap` constructed with a known `*grpc.ClientConn` (e.g. dialed against an in-test `bufconn.Listener`, per Milestone 1's existing test pattern)
- **When** each of the four new accessor methods is called
- **Then** each MUST return a non-nil client of the correct generated interface type
- **And** issuing a call through any of them MUST reach the same `bufconn` server the existing `Capabilities` client reaches (proving no second connection is dialed)

##### AC-GRPC-020: New clients round-trip real data, not just "no error"

- **Validates:** REQ-GRPC-030
- **Given** a fake `bufconn`-backed server implementing `List` for `Clusters`, `ComputeInstances`, `Subnets`, and `VirtualNetworks`, each returning a canned response with specific, known field values (e.g. a cluster with `id="c1"`, `status=CLUSTER_STATE_READY`)
- **When** each new client's `List` method is called
- **Then** the decoded response's fields MUST equal the canned values exactly (not merely `len(results) > 0` or `err == nil`)

##### AC-GRPC-030: New clients inherit the shared bearer-token interceptor

- **Validates:** REQ-GRPC-020
- **Given** the bootstrap holds a known cached token value (e.g. `"tok-xyz"`)
- **And** the fake `bufconn` server records the `authorization` gRPC metadata it receives per call
- **When** a call is made via `ClustersClient()` (representative of all four)
- **Then** the recorded metadata MUST equal exactly `"Bearer tok-xyz"` — the same value/format Milestone 1 already proved the `Capabilities` client sends, confirming the new clients are not bypassing it

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

### DD-020: Reuse the single shared `ClientConn` — no per-service dial/auth logic

**Decision:** The four new accessor methods construct their generated
clients from the exact same `*grpc.ClientConn` Milestone 1's `Bootstrap`
already holds — not new connections, and not new TLS/auth construction.

**Rationale:** gRPC's `ClientConn` is designed to back multiple generated
service clients against the same server address, and OSAC's fulfillment
service exposes `Clusters`, `ComputeInstances`, `Subnets`,
`VirtualNetworks`, and `Capabilities` from a single gRPC endpoint
(`fulfillmentAddress`) — there is exactly one connection to make regardless
of how many service clients sit on top of it. Duplicating dial/TLS/backoff
construction per service would risk a new client silently missing the
bearer-token interceptor if the duplication drifted from Milestone 1's
implementation — the same class of hallucination risk this project has
already hit twice on OIDC discovery (`osac-sp.spec.md` DD-060). Sharing one
`ClientConn` also means this milestone introduces zero new configuration
keys: `fulfillmentAddress`, `tlsEnabled`, and `tlsCertFile` are already
loaded by Milestone 1's `internal/config`.

**Related requirements:** REQ-GRPC-010, REQ-GRPC-020, REQ-GRPC-030

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

---

## 9. Requirement ID Index

| Prefix | Topic | Count |
|--------|-------|-------|
| REQ-PROTO-NNN | 4.1: Proto Vendoring & Codegen | 6 |
| REQ-GRPC-NNN | 4.2: Generated Client Accessors | 3 |
| **Total** | | **9** |

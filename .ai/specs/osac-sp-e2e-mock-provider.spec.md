# Specification: `osac-mock-provider` — Phase 1 of the kind-based e2e infra

## 1. Overview

Phase 1 of [osac-service-provider#17](https://github.com/dcm-project/osac-service-provider/issues/17)
(FLPATH-4759): a standalone binary, `cmd/osac-mock-provider`, that fakes the
**OSAC backend side** of the gRPC contract `osac-sp` dials — a real
`net.Listen`-backed `grpc.Server` implementing `osac.public.v1`'s
`Capabilities`, `Clusters`, `ComputeInstances`, `Subnets`, and
`VirtualNetworks` services, plus a real HTTP OIDC discovery-and-token stub
satisfying `internal/osac.Bootstrap`'s client-credentials flow.

This is what makes the rest of FLPATH-4759 (a `kind` cluster running real
`control-plane` + real `osac-sp` + this mock, in a GitHub Actions job)
possible without needing OSAC's real `fulfillment-service` or Keycloak.

**This spec covers the mock-provider binary only.** Explicitly out of scope
(deferred to a follow-up, Phase 2 of issue #17):

- `kind` cluster manifests, the GitHub Actions e2e workflow, and
  sparse-checking-out `control-plane`'s `deploy/helm/dcm/` chart.
- Simulating realistic provisioning *delays* (e.g. `STARTING` →
  `RUNNING` over time) — `Create` resolves straight to a terminal "ready"
  status. Phase 1's purpose is proving the SP↔mock **dispatch contract**
  round-trips correctly, not backend timing; revisit only if a later e2e
  scenario (e.g. Milestone 5 status-polling assertions) needs an observable
  transition.
- CEL `filter` / `order` support on `List` (real OSAC supports both per
  `compute_instances_service.proto`; this mock does not evaluate them).
- `Update` on any of the four CRUD-shaped services, and `Clusters`'
  `GetKubeconfig(ViaHttp)`/`GetPassword(ViaHttp)` — none of these are called
  by `osac-sp` today, so they are left on the generated
  `Unimplemented*Server` default (gRPC `UNIMPLEMENTED`), which is itself an
  accurate mock of "not part of this contract." **Correction (see
  REQ-MOCK-120):** this originally also listed plain `GetKubeconfig` as
  out of scope on the assumption that "Milestone 3/4's architecture
  diagrams only ever invoke Create/Get/List/Delete" — confirmed false once
  Milestone 3's actual `internal/cluster.Service.Get` was read: it calls
  `Clusters/GetKubeconfig` whenever the mapped status is `ACTIVE`
  (`osac-sp-m3-cluster-crud.spec.md`'s REQ-GET-020), which every `Get` of a
  mock-created cluster immediately is (REQ-MOCK-030 sets terminal
  `CLUSTER_STATE_READY` right away). Leaving it `UNIMPLEMENTED` made every
  such `Get` fail with a mapped `500` — caught empirically while building
  M3/M4 e2e coverage on top of this mock, not from re-reading the
  architecture diagrams alone. `GetKubeconfig(ViaHttp)`/`GetPassword*` stay
  out of scope; only plain `GetKubeconfig` is corrected. See DD-143.
- Real JWT signing/validation for the OIDC stub — the mock's own gRPC server
  never enforces auth (there is nothing downstream of the mock to protect),
  so the token only needs to satisfy `osac-sp`'s client, not a resource
  server.

**Reference documents:**

- [osac-service-provider#17](https://github.com/dcm-project/osac-service-provider/issues/17) —
  the parent proposal (architecture, ownership, GitHub Actions job outline)
- [`internal/osac/bootstrap.go`](../../internal/osac/bootstrap.go) — the
  real client this binary exists to satisfy: OIDC discovery order
  (`oauth-authorization-server` then `openid-configuration`), then
  `clientcredentials.Config`-backed token fetch, then a gRPC `ClientConn`
  with a per-RPC bearer-token interceptor (DD-060)
- OSAC public protos, already vendored in Milestone 2 and unchanged by this
  work: [`compute_instances_service.proto`](../../proto/osac/public/v1/compute_instances_service.proto),
  [`clusters_service.proto`](../../proto/osac/public/v1/clusters_service.proto),
  [`subnets_service.proto`](../../proto/osac/public/v1/subnets_service.proto),
  [`virtual_networks_service.proto`](../../proto/osac/public/v1/virtual_networks_service.proto),
  [`capabilities_service.proto`](../../proto/osac/public/v1/capabilities_service.proto)
- [Milestone 1 spec](./osac-sp.spec.md) (`cmd/osac-service-provider/main.go`'s
  graceful-shutdown shape, `internal/config`'s `caarlos0/env` pattern) and
  [Milestone 4 spec](./osac-sp-m4-vm-crud.spec.md) (REQ-VMCREATE-070's
  idempotent-create-retry contract, which REQ-MOCK-020 below exists to make
  testable) — both **structural** references only; this package has no code
  dependency on `internal/vm`, `internal/cluster`, or either milestone's
  handler packages, and branches from `main` (independent of the still
  -unmerged Milestone 3/4 PRs), per the same rationale as DD-126.
- [Design Decisions](../decisions/osac-sp.decisions.md) — new decisions for
  this work start at `DD-130` (next available on `main`); the same
  numbering-collision-until-merge caveat DD-126 already documents applies.

---

## 2. Architecture

```
                     osac-sp (real, cmd/osac-service-provider)
                              |
                internal/osac.Bootstrap
                (OIDC discovery + client-credentials + gRPC ClientConn)
                     |                          |
                     | HTTP                     | gRPC
                     v                          v
        +--------------------------------------------------------+
        |                 cmd/osac-mock-provider                  |
        |                                                          |
        |  test/mockprovider/oidc.go (HTTP)                    |
        |    GET  /.well-known/oauth-authorization-server          |
        |    GET  /.well-known/openid-configuration                |
        |    POST /token                                           |
        |                                                          |
        |  test/mockprovider/{clusters,computeinstances,       |
        |    subnets,virtualnetworks,capabilities}.go (gRPC)       |
        |    each backed by a resourceStore[T] (store.go) —        |
        |    thread-safe, insertion-ordered, ID-keyed map          |
        +--------------------------------------------------------+
```

Two independent listeners in one process: one `net.Listener` for the
`grpc.Server` (all five services registered), one `net.Listener` for the
OIDC `http.Server`. Both addresses are configured independently
(`internal/config`-style env vars), since `osac-sp`'s own config already
requires `SP_OSAC_OIDC_ISSUER_URL` and `SP_OSAC_FULFILLMENT_ADDRESS` to be
distinct URLs/addresses.

### New package: `test/mockprovider`

- `store.go` — `resourceStore[T]`, a generic, mutex-protected, ID-keyed,
  insertion-ordered map. Shared by all four CRUD-shaped services so the
  `AlreadyExists`/`NotFound`/list-ordering semantics are implemented exactly
  once (REQ-MOCK-020/030/040/050), not duplicated four times.
- `clusters.go`, `computeinstances.go` — SP-supplied-ID services: `Create`
  requires and uses the caller's `object.id` (REQ-MOCK-020), matching how
  `osac-sp` itself sets `ComputeInstance.id`/`Cluster.id` for idempotent
  create-retry (M3 DD-100 / M4 REQ-VMCREATE-070).
- `subnets.go`, `virtualnetworks.go` — server-generated-ID services:
  `Create` always assigns a fresh ID (REQ-MOCK-021), matching real OSAC
  (`osac-sp` never supplies a `Subnet`/`VirtualNetwork` ID on create either
  — see M4 spec §4.5, Default Network Provisioning).
- `capabilities.go` — trivial `Get`, no state (REQ-MOCK-060).
- `oidc.go` — the HTTP discovery + token stub (REQ-MOCK-070/080).

### New binary: `cmd/osac-mock-provider`

Config (`test/mockprovider/config.go` or inlined in `main.go` — decided
at implementation time), two `net.Listen` calls, `signal.NotifyContext`
-driven graceful shutdown — same shape as
[`cmd/osac-service-provider/main.go`](../../cmd/osac-service-provider/main.go).

---

## 3. Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-MOCK-010 | The binary MUST run a real `net.Listen("tcp", ...)`-backed `grpc.Server` exposing `osac.public.v1`'s `Capabilities`, `Clusters`, `ComputeInstances`, `Subnets`, and `VirtualNetworks` services, generated from the same vendored protos (`internal/osacpb`) the real SP already uses — no new proto surface | MUST | |
| REQ-MOCK-020 | For `Clusters`/`ComputeInstances` (SP-supplied-ID services), `Create` MUST reject an empty `object.id` with gRPC `INVALID_ARGUMENT` and MUST reject a second `Create` for an `id` that already exists with gRPC `ALREADY_EXISTS`, without mutating the stored object | MUST | Mirrors M3 DD-100 / M4 REQ-VMCREATE-070's precondition |
| REQ-MOCK-021 | For `Subnets`/`VirtualNetworks` (server-generated-ID services), `Create` MUST always assign a fresh, unique server-generated `id` — any caller-supplied `object.id` MUST be ignored/overwritten | MUST | |
| REQ-MOCK-030 | A successful `Create` on any of the four CRUD-shaped services MUST store the object with a terminal "ready" status set by the mock itself (`ComputeInstance`→`COMPUTE_INSTANCE_STATE_RUNNING`, `Cluster`→`CLUSTER_STATE_READY`, `Subnet`/`VirtualNetwork`→`{SUBNET,VIRTUAL_NETWORK}_STATE_READY`), regardless of what (if anything) the caller set on `object.status` | MUST | Out-of-scope note in §1: no simulated delay |
| REQ-MOCK-040 | `Get` MUST return the exact stored object for a known `id`, and gRPC `NOT_FOUND` for an unknown one, on all four CRUD-shaped services | MUST | |
| REQ-MOCK-050 | `List` MUST return every stored object for that service in creation order, honoring `offset`/`limit` when set (0 offset / unset limit = all); `filter`/`order` MUST be accepted but ignored | MUST | |
| REQ-MOCK-060 | `Delete` MUST remove a known `id` and return an empty response, and MUST return gRPC `NOT_FOUND` for an unknown `id` | MUST | Makes `osac-sp`'s own Delete-tolerates-404 behavior (M3/M4) testable against a real backend |
| REQ-MOCK-070 | `Capabilities/Get` MUST always return a successful (non-error) response | MUST | Backs the health-check connectivity probe only; no capability content required |
| REQ-MOCK-080 | The binary MUST serve `GET /.well-known/oauth-authorization-server` and `GET /.well-known/openid-configuration`, both returning `200` with a JSON body whose `token_endpoint` field points at the mock's own token endpoint | MUST | Matches `internal/osac/bootstrap.go`'s `oidcWellKnownEndpoints` discovery order |
| REQ-MOCK-090 | The token endpoint MUST accept `POST` with `grant_type=client_credentials` (client_id/secret via HTTP Basic auth or form body) and respond `200` with a JSON body containing non-empty `access_token`, `token_type="Bearer"`, and a positive `expires_in` | MUST | |
| REQ-MOCK-100 | The token endpoint MUST reject any request whose `grant_type` is missing or not `client_credentials` with HTTP `400` and an RFC 6749 §5.2-shaped `{"error": "..."}` body | MUST | |
| REQ-MOCK-110 | The binary MUST load its gRPC and HTTP listen addresses from environment variables, failing fast (matching `internal/config.Load()`'s convention) when a required value is missing/empty, and MUST shut down both listeners gracefully on `SIGTERM`/`SIGINT` | MUST | |
| REQ-MOCK-120 | `Clusters/GetKubeconfig` MUST return a non-empty, base64-encoded stub kubeconfig for a known `id`, and gRPC `NOT_FOUND` for an unknown one — mirroring the other four CRUD-shaped services' `Get` semantics (REQ-MOCK-040) | MUST | Correction to §1's original scope (see DD-143); backs Milestone 3's `internal/cluster.Service.Get` (REQ-GET-020) |

---

## 4. Acceptance Criteria

##### AC-MOCK-010: Create rejects an empty id (SP-supplied-ID services)

- **Validates:** REQ-MOCK-020
- **Given** a `Create` request for `Clusters` (and, separately,
  `ComputeInstances`) with `object.id == ""`
- **When** `Create` is called
- **Then** it returns gRPC `INVALID_ARGUMENT` and nothing is stored (a
  subsequent `List` is empty)

##### AC-MOCK-020: Create rejects a duplicate id (SP-supplied-ID services)

- **Validates:** REQ-MOCK-020
- **Given** `Create` has already succeeded once for `id="x"`
- **When** `Create` is called again with `id="x"`
- **Then** it returns gRPC `ALREADY_EXISTS` and `List` still shows exactly
  one stored object with `id="x"`

##### AC-MOCK-030: Create sets a terminal ready status and round-trips via Get/List (SP-supplied-ID services)

- **Validates:** REQ-MOCK-020, REQ-MOCK-030, REQ-MOCK-040, REQ-MOCK-050
- **Given** a fresh `Create` request with `id="x"` and no `status` set
- **When** `Create` succeeds, then `Get("x")` and `List()` are called
- **Then** all three responses show `status.state` as the service's ready
  state, and `Get`/`List`'s returned object is identical to `Create`'s

##### AC-MOCK-040: Get of an unknown id is NotFound (all four CRUD services)

- **Validates:** REQ-MOCK-040
- **Given** no object with `id="missing"` was ever created
- **When** `Get("missing")` is called
- **Then** it returns gRPC `NOT_FOUND`

##### AC-MOCK-050: Delete removes a known id; a second Delete is NotFound (all four CRUD services)

- **Validates:** REQ-MOCK-060
- **Given** `id="x"` exists
- **When** `Delete("x")` is called, then `Delete("x")` is called again
- **Then** the first call returns an empty success response and `x` no
  longer appears in `List`; the second call returns gRPC `NOT_FOUND`

##### AC-MOCK-060: List honors offset/limit in creation order (all four CRUD services)

- **Validates:** REQ-MOCK-050
- **Given** three objects created in order `A`, `B`, `C`
- **When** `List(offset=1, limit=1)` is called
- **Then** `items == [B]`, `size == 1`, `total == 3`

##### AC-MOCK-070: Create always generates a fresh id, ignoring any caller-supplied value (server-generated-ID services)

- **Validates:** REQ-MOCK-021, REQ-MOCK-030
- **Given** two `Create` calls to `Subnets` (and, separately,
  `VirtualNetworks`) — one with `object.id == ""`, one with
  `object.id == "caller-supplied"`
- **When** both are called
- **Then** both responses have a non-empty, mutually distinct `id`, neither
  equal to `"caller-supplied"`, and both have `status.state` `READY`

##### AC-MOCK-080: Capabilities/Get always succeeds

- **Validates:** REQ-MOCK-070
- **Given** a fresh mock server, no state
- **When** `Capabilities/Get` is called
- **Then** it returns a non-error response

##### AC-MOCK-090: Both OIDC discovery documents resolve to the token endpoint

- **Validates:** REQ-MOCK-080
- **Given** the mock's HTTP server running at base URL `B`
- **When** `GET B/.well-known/oauth-authorization-server` and, separately,
  `GET B/.well-known/openid-configuration` are called
- **Then** both return `200` with a JSON body whose `token_endpoint` field
  is the mock's real token URL

##### AC-MOCK-100: Token endpoint issues a bearer token for a client_credentials grant

- **Validates:** REQ-MOCK-090
- **Given** a `POST` to the token endpoint with `grant_type=client_credentials`
- **When** called (client_id/secret supplied via HTTP Basic auth)
- **Then** the response is `200` JSON with a non-empty `access_token`,
  `token_type == "Bearer"`, and `expires_in > 0`

##### AC-MOCK-110: Token endpoint rejects a non-client_credentials or missing grant type

- **Validates:** REQ-MOCK-100
- **Given**, in turn, a `POST` with `grant_type=authorization_code` and a
  `POST` with no `grant_type` at all
- **When** each is called
- **Then** each returns HTTP `400` with a JSON `error` field

##### AC-MOCK-120: A real `osac.Bootstrap` fetches a token and probes successfully against the real mock binary

- **Validates:** REQ-MOCK-010, REQ-MOCK-080, REQ-MOCK-090, REQ-MOCK-070, REQ-MOCK-110
- **Given** the real `cmd/osac-mock-provider` binary's `test/mockprovider`
  gRPC server and OIDC HTTP server both running on real, ephemeral
  `net.Listen` ports (in-process, not `bufconn`)
- **When** a real `osac.Bootstrap`, constructed via the production `osac.New()`
  (not the package's own unit-test fixtures), is pointed at those two
  addresses and started
- **Then** `Bootstrap.TokenStatus().Valid` becomes `true` and
  `Bootstrap.Probe(ctx).Connected` is `true` within a bounded wait — proving
  the mock is a genuine, real-transport substitute for OSAC before it is
  ever wired into `kind`

##### AC-MOCK-130: GetKubeconfig round-trips for a known id, NotFound for an unknown one

- **Validates:** REQ-MOCK-120
- **Given** a cluster already created via `Clusters/Create`
- **When** `Clusters/GetKubeconfig` is called with that cluster's `id`
- **Then** the response's `kubeconfig` field is non-empty and valid base64
- **And** calling it with an unknown `id` instead returns gRPC `NOT_FOUND`

---

## 5. Dependencies

Depends on Milestone 2's vendored/generated `internal/osacpb` (already on
`main`) only. No dependency on Milestone 1's `internal/config`/`apiserver`
code (the mock binary has its own minimal config, per REQ-MOCK-110, since
its concerns — two listen addresses, no HTTP router/middleware chain — don't
warrant reusing `internal/config.Config`'s full shape) and no dependency on
Milestone 3/4's `internal/cluster`/`internal/vm`/handler packages.

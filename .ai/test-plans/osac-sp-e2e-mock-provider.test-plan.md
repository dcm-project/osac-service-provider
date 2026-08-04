# Test Plan: `osac-mock-provider` (Phase 1 of the kind-based e2e infra)

Scope: unit and integration tests for
[`osac-sp-e2e-mock-provider.spec.md`](../specs/osac-sp-e2e-mock-provider.spec.md).
Continues numbering from `main`'s current maximums (`TC-U-113`, `TC-I-030`)
— same rebase-later caveat already documented by the M3/M4 branches' own
numbering (DD-086).

**Framework:** Ginkgo v2 + Gomega. Files use the `_unit_test.go` /
`_integration_test.go` suffix. Run a single case with:

```bash
go run github.com/onsi/ginkgo/v2/ginkgo -r -v -focus "TC-U-114" ./internal/mockprovider/... ./cmd/osac-mock-provider/...
```

**Assertion discipline:** assert actual values (exact IDs, exact status
enum values, exact response bodies), not existence-only checks.

**What's a "fake" here:** `internal/mockprovider` itself *is* the fake (it
plays the role OSAC's real `fulfillment-service` plays for `osac-sp`). Its
own unit tests therefore call its exported `grpc.Server`-implementing types
directly (`Create`/`Get`/`List`/`Delete`) — real production code under
test, no further fake/mock layer needed underneath it.

---

## 1. `internal/mockprovider` — `Clusters` and `ComputeInstances` (SP-supplied-ID services)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-114 | `Clusters.Create` rejects an empty `id` | REQ-MOCK-020, AC-MOCK-010 | Call `Create` with `object.id == ""`; assert the returned error's gRPC status code is `INVALID_ARGUMENT`; assert a subsequent `List` returns zero items. |
| TC-U-115 | `ComputeInstances.Create` rejects an empty `id` | REQ-MOCK-020, AC-MOCK-010 | Same as TC-U-114 for `ComputeInstances`. |
| TC-U-116 | `Clusters.Create` rejects a duplicate `id` | REQ-MOCK-020, AC-MOCK-020 | `Create` succeeds once with `id="x"`; call `Create` again with `id="x"` (different `spec`); assert the second call's error code is `ALREADY_EXISTS`; assert `List` shows exactly one item with `id="x"` (the original `spec`, not the second). |
| TC-U-117 | `ComputeInstances.Create` rejects a duplicate `id` | REQ-MOCK-020, AC-MOCK-020 | Same as TC-U-116 for `ComputeInstances`. |
| TC-U-118 | `Clusters.Create` sets `CLUSTER_STATE_READY` and round-trips via `Get`/`List` | REQ-MOCK-020, REQ-MOCK-030, REQ-MOCK-040, REQ-MOCK-050, AC-MOCK-030 | `Create` with `id="x"`, `status` unset; assert the `Create` response's `status.state == CLUSTER_STATE_READY`; call `Get("x")` and assert the returned object is `proto.Equal` to `Create`'s; call `List()` and assert it contains exactly that one object. |
| TC-U-119 | `ComputeInstances.Create` sets `COMPUTE_INSTANCE_STATE_RUNNING` and round-trips via `Get`/`List` | REQ-MOCK-020, REQ-MOCK-030, REQ-MOCK-040, REQ-MOCK-050, AC-MOCK-030 | Same as TC-U-118 for `ComputeInstances`, asserting `COMPUTE_INSTANCE_STATE_RUNNING`. |
| TC-U-120 | `Clusters.Get` of an unknown `id` is `NotFound` | REQ-MOCK-040, AC-MOCK-040 | Call `Get("missing")` on an empty store; assert the error code is `NOT_FOUND`. |
| TC-U-121 | `ComputeInstances.Get` of an unknown `id` is `NotFound` | REQ-MOCK-040, AC-MOCK-040 | Same as TC-U-120 for `ComputeInstances`. |
| TC-U-122 | `Clusters.List` honors `offset`/`limit` in creation order | REQ-MOCK-050, AC-MOCK-060 | `Create` three clusters in order `id="a","b","c"`; call `List(offset=1, limit=1)`; assert `items == [the "b" object]`, `Size == 1`, `Total == 3`. |
| TC-U-123 | `ComputeInstances.List` honors `offset`/`limit` in creation order | REQ-MOCK-050, AC-MOCK-060 | Same as TC-U-122 for `ComputeInstances`. |
| TC-U-124 | `Clusters.Delete` removes a known `id`; a second `Delete` is `NotFound` | REQ-MOCK-060, AC-MOCK-050 | `Create` with `id="x"`; call `Delete("x")` — assert no error and `List` no longer contains `"x"`; call `Delete("x")` again — assert error code `NOT_FOUND`. |
| TC-U-125 | `ComputeInstances.Delete` removes a known `id`; a second `Delete` is `NotFound` | REQ-MOCK-060, AC-MOCK-050 | Same as TC-U-124 for `ComputeInstances`. |

---

## 2. `internal/mockprovider` — `Subnets` and `VirtualNetworks` (server-generated-ID services)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-126 | `Subnets.Create` always generates a fresh `id`, ignoring any caller-supplied value, and sets `SUBNET_STATE_READY` | REQ-MOCK-021, REQ-MOCK-030, AC-MOCK-070 | Call `Create` twice: once with `object.id == ""`, once with `object.id == "caller-supplied"`; assert both responses have non-empty `id`, the two IDs differ from each other, neither equals `"caller-supplied"`, and both have `status.state == SUBNET_STATE_READY`. |
| TC-U-127 | `VirtualNetworks.Create` always generates a fresh `id`, ignoring any caller-supplied value, and sets `VIRTUAL_NETWORK_STATE_READY` | REQ-MOCK-021, REQ-MOCK-030, AC-MOCK-070 | Same as TC-U-126 for `VirtualNetworks`, asserting `VIRTUAL_NETWORK_STATE_READY`. |
| TC-U-128 | `Subnets.Get` round-trips a created object; unknown `id` is `NotFound` | REQ-MOCK-040, AC-MOCK-040 | `Create` one subnet, capture its generated `id`; call `Get(id)` and assert it's `proto.Equal` to the created object; call `Get("missing")` and assert error code `NOT_FOUND`. |
| TC-U-129 | `VirtualNetworks.Get` round-trips a created object; unknown `id` is `NotFound` | REQ-MOCK-040, AC-MOCK-040 | Same as TC-U-128 for `VirtualNetworks`. |
| TC-U-130 | `Subnets.List` reflects all created objects | REQ-MOCK-050 | `Create` two subnets; call `List()`; assert `Size == 2`, `Total == 2`, both generated IDs appear. |
| TC-U-131 | `VirtualNetworks.List` reflects all created objects | REQ-MOCK-050 | Same as TC-U-130 for `VirtualNetworks`. |
| TC-U-132 | `Subnets.Delete` removes a known `id`; a second `Delete` is `NotFound` | REQ-MOCK-060, AC-MOCK-050 | `Create` one subnet, capture its `id`; `Delete(id)` succeeds and `List` no longer contains it; `Delete(id)` again returns `NOT_FOUND`. |
| TC-U-133 | `VirtualNetworks.Delete` removes a known `id`; a second `Delete` is `NotFound` | REQ-MOCK-060, AC-MOCK-050 | Same as TC-U-132 for `VirtualNetworks`. |

---

## 3. `internal/mockprovider` — `Capabilities`

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-134 | `Capabilities.Get` always succeeds | REQ-MOCK-070, AC-MOCK-080 | Call `Get` on a fresh server with no prior state; assert no error is returned. |

---

## 4. `internal/mockprovider` — OIDC discovery + token stub

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-135 | `oauth-authorization-server` discovery document resolves to the token endpoint | REQ-MOCK-080, AC-MOCK-090 | Start the OIDC HTTP handler on an `httptest.Server`; `GET /.well-known/oauth-authorization-server`; assert `200` and the decoded JSON's `token_endpoint` field equals exactly `<base>/token`. |
| TC-U-136 | `openid-configuration` discovery document resolves to the token endpoint | REQ-MOCK-080, AC-MOCK-090 | Same as TC-U-135 for `GET /.well-known/openid-configuration`. |
| TC-U-137 | Token endpoint issues a bearer token for a `client_credentials` grant | REQ-MOCK-090, AC-MOCK-100 | `POST /token` with `grant_type=client_credentials` and HTTP Basic auth credentials; assert `200`, decoded JSON has non-empty `access_token`, `token_type == "Bearer"`, `expires_in > 0`. |
| TC-U-138 | Token endpoint rejects a non-`client_credentials` grant type | REQ-MOCK-100, AC-MOCK-110 | `POST /token` with `grant_type=authorization_code`; assert `400` and decoded JSON's `error` field is non-empty. |
| TC-U-139 | Token endpoint rejects a request with no `grant_type` at all | REQ-MOCK-100, AC-MOCK-110 | `POST /token` with an empty form body; assert `400` and decoded JSON's `error` field is non-empty. |

---

## 5. `cmd/osac-mock-provider` — integration

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-031 | A real `osac.Bootstrap` authenticates and probes against the real mock binary over real listeners | REQ-MOCK-010, REQ-MOCK-070, REQ-MOCK-080, REQ-MOCK-090, REQ-MOCK-110, AC-MOCK-120 | Start `internal/mockprovider`'s gRPC server and OIDC HTTP server each on a real `net.Listen("tcp", "127.0.0.1:0")` (not `bufconn`), in-process; construct an `osac.Bootstrap` via the production `osac.New(cfg, logger)` pointed at those two real addresses; call `Start(ctx)`; poll (bounded wait) until `TokenStatus().Valid == true`; assert `Probe(ctx).Connected == true`. |

---

## 6. Coverage Matrix

| Spec Topic | REQ Count | AC Count | TC-U | TC-I | Notes |
|---|---|---|---|---|---|
| `Clusters`/`ComputeInstances` CRUD | REQ-MOCK-020, 030, 040, 050, 060 | AC-MOCK-010, 020, 030, 040, 050, 060 | 12 (TC-U-114..125) | — | Pyramid invariant: every REQ/AC pair here has a dedicated TC-U; no dedicated TC-I since TC-I-031 already proves the wire-level round trip for the shared gRPC/HTTP plumbing, and the CRUD semantics are pure in-memory logic with no networking edge cases left to add at the integration tier. |
| `Subnets`/`VirtualNetworks` CRUD | REQ-MOCK-021, 030, 040, 050, 060 | AC-MOCK-040, 050, 070 | 8 (TC-U-126..133) | — | |
| `Capabilities` | REQ-MOCK-070 | AC-MOCK-080 | 1 (TC-U-134) | — | |
| OIDC discovery + token | REQ-MOCK-080, 090, 100 | AC-MOCK-090, 100, 110 | 5 (TC-U-135..139) | — | |
| Binary wiring (real listeners, real `osac.Bootstrap`) | REQ-MOCK-010, 070, 080, 090, 110 | AC-MOCK-120 | — | 1 (TC-I-031) | Closes the pyramid invariant for every REQ above by proving the real transport, not just the in-memory logic each REQ's TC-U already covers. |
| **Total** | 11 | 12 | **26** | **1** | |

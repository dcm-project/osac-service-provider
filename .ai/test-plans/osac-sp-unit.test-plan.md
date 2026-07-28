# Test Plan: OSAC Service Provider — Milestone 1 (Unit Tests)

Scope: unit tests for Milestone 1 ("Scaffold + registration + health") as
specified in [`.ai/specs/osac-sp.spec.md`](../specs/osac-sp.spec.md). Unit
tests exercise a single package in isolation with fakes/mocks for
collaborators — no real network, no real Kubernetes/HTTP servers, no
`bufconn`/`httptest` listeners spanning multiple packages (those are
integration-test scope, see
[`osac-sp-integration.test-plan.md`](./osac-sp-integration.test-plan.md)).

**Framework:** Ginkgo v2 + Gomega. Files use the `_unit_test.go` suffix.
Run a single case with:

```bash
go run github.com/onsi/ginkgo/v2/ginkgo -r -v -focus "TC-U-010" ./internal/...
```

**Assertion discipline:** assert actual values (exact header content, exact
struct fields, exact backoff durations), not existence-only checks
(`NotTo(BeNil())`, `NotTo(BeEmpty())` used as the entire assertion).

---

## 1. `internal/config`

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-001 | Loads all values from environment variables | REQ-XC-CFG-010, AC-XC-CFG-010 | Set every env var (`SP_SERVER_ADDRESS`, `SP_OSAC_*`, `DCM_REGISTRATION_URL`, `SP_ENDPOINT`, `SP_PROVIDER_*`) to distinct non-default values; assert the parsed `Config` struct's fields equal those exact values field-by-field. |
| TC-U-002 | Applies documented defaults when optional vars are unset | REQ-XC-CFG-010 | Unset all optional vars; assert `server.address == ":8080"`, `server.shutdownTimeout == 15s`, `server.requestTimeout == 30s`, `osac.tlsEnabled == false`, `osac.probeTimeout == 5s`, `provider.clusterName == "osac-sp-cluster"`, `provider.vmName == "osac-sp-vm"`. |
| TC-U-003 | Fails fast when a required field is missing (table-driven) | REQ-XC-CFG-020, AC-XC-CFG-020 | Table over `{SP_OSAC_FULFILLMENT_ADDRESS, SP_OSAC_OIDC_ISSUER_URL, SP_OSAC_OIDC_CLIENT_ID, SP_OSAC_OIDC_CLIENT_SECRET, DCM_REGISTRATION_URL, SP_ENDPOINT}`; for each, unset only that var (others valid) and assert `Load()` returns a non-nil error whose message contains that exact env var name. |
| TC-U-004 | Fails fast when a required field is empty string | REQ-XC-CFG-020 | Set `SP_ENDPOINT=""` explicitly (as opposed to unset); assert `Load()` returns an error naming `SP_ENDPOINT`. |

---

## 2. `internal/osac` (OSAC client bootstrap)

Collaborators faked: an OIDC issuer (`httptest.Server` serving canned
`.well-known/oauth-authorization-server` (RFC 8414, tried first per
REQ-OSAC-011) and `.well-known/openid-configuration` (OpenID Connect
Discovery, fallback) documents, independently configurable so tests can
exercise either the primary path or the fallback, plus a token endpoint at
the URL whichever document advertises — the fake must NOT accept token
requests at the issuer URL itself, so tests fail loudly if the code
regresses to treating the issuer as the token endpoint), OSAC `Capabilities`
service (in-memory `bufconn`-free stub satisfying the generated client
interface directly, or a hand-rolled fake implementing the same method
signature — no real gRPC dialing in unit scope; that's covered by TC-I
cases).

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-010 | Fetches OIDC token on `Start`, via the discovered token endpoint | REQ-OSAC-010, REQ-OSAC-011, AC-OSAC-010 | Fake discovery document at `{issuer}/.well-known/oauth-authorization-server` advertises `token_endpoint: "{issuer}/protocol/openid-connect/token"`; that exact URL returns `{access_token: "tok-abc", expires_in: 300}`; call `Start`; assert the bootstrap's cached token value equals exactly `"tok-abc"`. |
| TC-U-011 | Refreshes token before expiry | REQ-OSAC-020, AC-OSAC-020 | Fake token endpoint returns token A (expires in 1s), then token B on second call; advance a fake clock past the refresh margin; assert the bearer credential attached to a subsequent gRPC call equals token B's exact value, not token A's. |
| TC-U-012 | Builds insecure transport credentials when TLS disabled | REQ-OSAC-050, AC-OSAC-050 | `osac.tlsEnabled=false`; call the internal dial-options builder; assert the returned `grpc.DialOption` set is equal to (or wraps) `grpc.WithTransportCredentials(insecure.NewCredentials())` — assert on the credential's `Info().SecurityProtocol` value, not just "no error". |
| TC-U-013 | Builds TLS transport credentials when TLS enabled | REQ-OSAC-040, AC-OSAC-040 | `osac.tlsEnabled=true`, `tlsCertFile` points to a test CA PEM fixture; assert the returned credentials' `Info().SecurityProtocol == "tls"` and the loaded cert pool contains exactly the test CA's subject. |
| TC-U-014 | Token fetch failure does not panic or block `Start` | REQ-OSAC-060, AC-OSAC-060 | Discovery succeeds, but the discovered token endpoint returns `500` on every call; call `Start` with a bounded test timeout; assert `Start` returns (does not hang) and returns no fatal error — background retry goroutine is started. |
| TC-U-015 | Token fetch retries with exponential backoff | REQ-OSAC-060 | Fake token endpoint fails 3 times then succeeds; using an injected fake clock/backoff, assert the recorded retry delays are non-decreasing and match the configured backoff multiplier exactly (e.g., 1s, 2s, 4s). |
| TC-U-023 | Discovers token endpoint via RFC 8414 (`oauth-authorization-server`) before fetching a token | REQ-OSAC-011, AC-OSAC-011 | Fake `oauth-authorization-server` discovery document's `token_endpoint` differs from the issuer URL (e.g. issuer `https://kc.example.com/realms/osac`, `token_endpoint` `https://kc.example.com/realms/osac/protocol/openid-connect/token`); call `Start`; assert the fake HTTP server recorded the token POST at the discovered URL, recorded zero token POSTs at the bare issuer URL, and recorded zero requests to `.well-known/openid-configuration` (RFC 8414 succeeded, so no fallback was needed). |
| TC-U-024 | Discovery failure does not panic or block `Start`, retried with backoff | REQ-OSAC-011, REQ-OSAC-060, AC-OSAC-012 | Both fake discovery endpoints (`.well-known/oauth-authorization-server` and `.well-known/openid-configuration`) return `500` on every call; call `Start` with a bounded test timeout; assert `Start` returns without hanging, `TokenStatus().Valid == false` throughout, and discovery is retried using the same exponential backoff sequence as TC-U-015. |
| TC-U-025 | Falls back to OpenID Connect discovery (`openid-configuration`) when RFC 8414 discovery fails | REQ-OSAC-011, AC-OSAC-013 | Fake `.well-known/oauth-authorization-server` returns `404` (as on a Keycloak realm that doesn't expose it); fake `.well-known/openid-configuration` returns a valid document; call `Start`; assert the fake HTTP server recorded exactly one request to each well-known path (RFC 8414 tried first and failed, OIDC fallback tried second and succeeded) and the token POST landed at the fallback-discovered `token_endpoint`. |
| TC-U-016 | Token validity query — valid | REQ-OSAC-070, AC-OSAC-070 | Seed the bootstrap with a cached token expiring in 10 minutes; call `TokenStatus()`; assert `valid == true` and `expiresAt` equals the exact seeded expiry time. |
| TC-U-017 | Token validity query — never obtained | REQ-OSAC-070, AC-OSAC-071 | Construct the bootstrap with no token ever fetched; call `TokenStatus()`; assert `valid == false`. |
| TC-U-018 | Token validity query — expired | REQ-OSAC-070 | Seed a cached token with `expiresAt` in the past; call `TokenStatus()`; assert `valid == false`. |
| TC-U-019 | Connectivity probe success | REQ-OSAC-080, AC-OSAC-080 | Fake `Capabilities` client returns success within timeout; call `Probe(ctx)`; assert `connected == true` and `err == nil`. |
| TC-U-020 | Connectivity probe failure — unreachable | REQ-OSAC-080, AC-OSAC-081 | Fake `Capabilities` client returns a gRPC `Unavailable` error; call `Probe(ctx)`; assert `connected == false` and the returned error wraps a gRPC status with code `Unavailable`. |
| TC-U-021 | Connectivity probe failure — timeout | REQ-OSAC-080, AC-OSAC-081 | Fake `Capabilities` client blocks longer than `osac.probeTimeout`; call `Probe(ctx)` with that timeout; assert `connected == false` and the error is (or wraps) `context.DeadlineExceeded`. |
| TC-U-022 | Connectivity probe does not trigger token refresh | REQ-OSAC-090, AC-OSAC-090 | Seed a token fetch call counter; call `Probe(ctx)` several times; assert the token-fetch call counter is unchanged (still its pre-probe value) after all calls. |

---

## 3. `internal/health`

Collaborator faked: an `OSACStatus` interface (satisfied by
`internal/osac`'s bootstrap) with `TokenStatus()` and `Probe(ctx)` — injected
as a hand-written function-field mock (`mockOSACStatus{TokenStatusFunc,
ProbeFunc}`), per the sibling repos' "no mocking framework" convention. Per
DD-010, the `StrictServerInterface` exposes two generated methods
(`GetClustersHealth`, `GetVMsHealth`) that both delegate to the same shared
internal status-computation logic; cases below exercise that shared logic
once (via either entry point) except where the test is specifically about
the two-entry-point relationship (TC-U-039).

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-030 | Healthy when token valid and OSAC reachable | REQ-HLT-030, AC-HLT-020 | Mock returns `valid=true`, `connected=true`; call the health handler's `CheckHealth`; assert the returned status string equals exactly `"healthy"`. |
| TC-U-031 | Unhealthy when token invalid, OSAC reachable | REQ-HLT-030, AC-HLT-025 | Mock returns `valid=false`, `connected=true`; assert status equals exactly `"unhealthy"`. |
| TC-U-032 | Unhealthy when token valid, OSAC unreachable | REQ-HLT-030, AC-HLT-026 | Mock returns `valid=true`, `connected=false`; assert status equals exactly `"unhealthy"`. |
| TC-U-033 | Unhealthy when both token invalid and OSAC unreachable | REQ-HLT-030 | Mock returns `valid=false`, `connected=false`; assert status equals exactly `"unhealthy"` and (per AC-HLT-060) the `detail` field mentions both conditions or a combined message, not just one. |
| TC-U-034 | Response fields populated | REQ-HLT-020, AC-HLT-020 | Any healthy case; assert `type == "osac-service-provider.dcm.io/health"`, `path == "health"`, `version` equals the build-injected version string exactly, `uptime` is a non-negative integer equal to (now - recorded start time) within a small tolerance. |
| TC-U-035 | Does not force token refresh | REQ-HLT-050, AC-HLT-040 | Mock's `TokenStatusFunc` asserts it was called (reads cache) and a separate injected "force refresh" spy is asserted to have zero invocations after `CheckHealth`. |
| TC-U-036 | Probe invoked exactly once per health call | REQ-HLT-060, AC-HLT-050 | Mock's `ProbeFunc` increments a counter; call `CheckHealth` once; assert the counter equals exactly `1` (not 0, not 2+). |
| TC-U-037 | Unhealthy detail names token cause | REQ-HLT-070, AC-HLT-060 | Mock returns `valid=false`, `connected=true`; assert the `detail` string contains `"token"` (case-insensitive) and does not claim a connectivity failure. |
| TC-U-038 | Unhealthy detail names connectivity cause | REQ-HLT-070 | Mock returns `valid=true`, `connected=false`; assert the `detail` string contains `"osac"` or `"connect"` (case-insensitive) and does not claim a token failure. |
| TC-U-039 | Both StrictServerInterface entry points report identical status | REQ-HLT-015, AC-HLT-011 | Fixed mock state (`valid=true`, `connected=false`); call `GetClustersHealth` and `GetVMsHealth` independently; assert both returned bodies have identical `status` and `detail` values. |

---

## 4. `internal/registration`

Collaborator faked: `control-plane`'s `pkg/sp/client/provider` HTTP
round-tripper — injected as a fake `http.RoundTripper` returning canned
status codes/bodies per call, so payload construction and retry/backoff
logic are tested without a real registration server (end-to-end wiring is
TC-I scope).

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-050 | Cluster registration payload fields | REQ-REG-020, REQ-REG-030, REQ-REG-040, AC-REG-020 | Trigger the cluster registration call; capture the request body sent to the fake round-tripper; assert `name == "osac-sp-cluster"`, `service_type == "cluster"`, `endpoint == "<provider.endpoint>/api/v1alpha1/clusters"`, `schema_version == "v1alpha1"`, and within `metadata`: `supported_platforms == ["baremetal"]`, `supported_provisioning_types == ["hypershift"]`, `len(kubernetes_supported_versions) > 0`. |
| TC-U-051 | VM registration payload fields | REQ-REG-020, REQ-REG-030, AC-REG-021 | Trigger the vm registration call; assert `name == "osac-sp-vm"`, `service_type == "vm"`, `endpoint == "<provider.endpoint>/api/v1alpha1/vms"`, `schema_version == "v1alpha1"`. |
| TC-U-052 | Two independent registration calls are issued | REQ-REG-010, AC-REG-010 | Trigger `Start()`; assert the fake round-tripper recorded exactly 2 initial requests, with distinct `name` values `osac-sp-cluster` and `osac-sp-vm`. |
| TC-U-053 | 409 Conflict is non-retryable, same as other 4xx | REQ-REG-090, AC-REG-060 | Fake round-tripper returns `409` for every `vm` call, `201` for `cluster`; assert (a) `vm`'s registration is logged at ERROR level and marked non-retryable — same handling as a `400` — (b) no further `vm` registration requests are sent after the first `409` (advance a fake clock past several backoff intervals to confirm), (c) `cluster`'s state is `registered` and unaffected. Supersedes the pre-pivot "409 is retryable" test design — see DD-050. |
| TC-U-054 | Non-retryable 4xx stops retries | REQ-REG-090, AC-REG-070 | Fake round-tripper returns `400` for the `cluster` call; advance the fake clock past several backoff intervals; assert no further `cluster` registration requests were sent after the first `400`. |
| TC-U-055 | Retryable failure uses exponential backoff | REQ-REG-070, AC-REG-050 | Fake round-tripper returns connection-refused-equivalent errors 3 times then succeeds for `cluster`; assert the recorded retry delays match the configured exponential sequence exactly. |
| TC-U-056 | Registration does not block on construction | REQ-REG-050, AC-REG-030 | Fake round-tripper's handler blocks until explicitly released; call `Start()`; assert `Start()` returns before the round-tripper is released (i.e., registration runs in a goroutine, not synchronously in `Start`). |
| TC-U-057 | Cluster failure does not affect VM registration | REQ-REG-060, AC-REG-040 | Fake round-tripper returns `500` for every `cluster` call and `201` for the first `vm` call; assert `vm`'s state reaches `registered` regardless of `cluster`'s ongoing retries. |
| TC-U-058 | Idempotent re-registration reuses same name | REQ-REG-100, AC-REG-080 | Call the registration path twice (simulating a restart); assert both calls send the same `name`/`service_type` pair (no suffix/uniqueness token appended) so `control-plane`'s idempotency-on-`name` behavior is preserved. |
| TC-U-059 | No Authorization header on registration requests | REQ-REG-115, AC-REG-095 | Trigger both registration calls; inspect the requests recorded by the fake round-tripper; assert neither request has an `Authorization` header set. |

---

## 5. `internal/apiserver`

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-070 | Panic in handler returns RFC 7807 INTERNAL | REQ-HTTP-070, AC-HTTP-070 | Register a handler that panics with a string value; send a request via `httptest`; assert response status `== 500`, `Content-Type == "application/problem+json"`, and the decoded body's `type` field equals exactly `"INTERNAL"`. |
| TC-U-071 | Request logging captures method/path/status/duration | REQ-HTTP-060, AC-HTTP-060 | Inject a test log handler capturing structured attributes; send a `GET /api/v1alpha1/clusters/health` request; assert the captured log record has `method == "GET"`, `path == "/api/v1alpha1/clusters/health"`, `status` equal to the actual response status code, and a `duration` attribute present with a non-negative value. |
| TC-U-072 | Request timeout cancels context | REQ-HTTP-090, AC-HTTP-090 | Configure `requestTimeout=10ms`; register a handler that sleeps 100ms and then checks `ctx.Err()`; assert the handler observes `context.DeadlineExceeded` (not nil). |
| TC-U-073 | Recovery middleware is outermost | REQ-HTTP-070 | Register a second middleware that also panics; assert the outer recovery middleware still produces the RFC 7807 INTERNAL response rather than an unhandled panic escaping the test. |

---

## 6. `internal/osac` — Milestone 2 (gRPC Client Generation)

Scope: unit tests for
[`osac-sp-m2-grpc-client-generation.spec.md`](../specs/osac-sp-m2-grpc-client-generation.spec.md).
Milestone 2 introduces no new HTTP or registration wiring, so all of its
coverage is unit-scope (single-package, `bufconn`-faked OSAC server) — the
same classification Milestone 1 already used for the equivalent
`Capabilities`-client tests (TC-U-010..025 above), not integration-scope.
No corresponding `osac-sp-integration.test-plan.md` section is needed for
this milestone.

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-100 | `ClustersClient()` shares the existing connection | REQ-GRPC-010, REQ-GRPC-020, AC-GRPC-010 | Construct `Bootstrap` with a known `*grpc.ClientConn` dialed against an in-test `bufconn.Listener` (same pattern as TC-U-019); call `ClustersClient()`; assert it returns a non-nil `Clusters` client and that a call through it reaches the same `bufconn` listener the `Capabilities` client already reaches — no second connection is dialed. |
| TC-U-101 | `ComputeInstancesClient()` shares the existing connection | REQ-GRPC-010, REQ-GRPC-020, AC-GRPC-010 | Same pattern as TC-U-100 for `ComputeInstancesClient()`. |
| TC-U-102 | `SubnetsClient()` shares the existing connection | REQ-GRPC-010, REQ-GRPC-020, AC-GRPC-010 | Same pattern as TC-U-100 for `SubnetsClient()`. |
| TC-U-103 | `VirtualNetworksClient()` shares the existing connection | REQ-GRPC-010, REQ-GRPC-020, AC-GRPC-010 | Same pattern as TC-U-100 for `VirtualNetworksClient()`. |
| TC-U-104 | `Clusters.List` round-trips real data | REQ-GRPC-030, AC-GRPC-020 | Fake `bufconn` server's `Clusters.List` handler returns a canned response containing a cluster with `id="c1"`, `status.state=CLUSTER_STATE_READY` (`status` is the nested `ClusterStatus` message; `state` its enum field); call `ClustersClient().List(ctx, ...)`; assert the decoded response's entry equals those exact field values, not merely `len(results) > 0`. |
| TC-U-105 | `ComputeInstances.List` round-trips real data | REQ-GRPC-030, AC-GRPC-020 | Same pattern as TC-U-104 for `ComputeInstancesClient().List`, with a compute instance carrying known `id`/`status` values. |
| TC-U-106 | `Subnets.List` round-trips real data | REQ-GRPC-030, AC-GRPC-020 | Same pattern as TC-U-104 for `SubnetsClient().List`, with a subnet carrying a known `id`/state. |
| TC-U-107 | `VirtualNetworks.List` round-trips real data | REQ-GRPC-030, AC-GRPC-020 | Same pattern as TC-U-104 for `VirtualNetworksClient().List`, with a virtual network carrying a known `id`. |
| TC-U-108 | New clients inherit the shared bearer-token interceptor | REQ-GRPC-020, AC-GRPC-030 | Seed the bootstrap with a known cached token (`"tok-xyz"`); fake `bufconn` server's `Clusters.List` handler records the `authorization` gRPC metadata it receives; call `ClustersClient().List(...)`; assert the recorded metadata equals exactly `"Bearer tok-xyz"` — the same value/format already proved for the `Capabilities` client in Milestone 1. |

---

## Coverage Matrix

| Spec Section | REQ Count | AC Count | TC Count (this file) | Notes |
|---|---|---|---|---|
| 4.1 HTTP Server | 9 | 9 | 4 (TC-U-070..073) | Remaining HTTP-server ACs (startup, shutdown signals, route registration) are integration-scope — see `osac-sp-integration.test-plan.md`. |
| 4.2 OSAC Client Bootstrap | 10 | 14 | 16 (TC-U-010..025) | Full unit coverage; real-dial-over-the-wire cases are TC-I scope. |
| 4.3 Health Service | 8 | 10 | 10 (TC-U-030..039) | Full unit coverage. |
| 4.4 SP Registration (`control-plane`) | 12 | 10 | 10 (TC-U-050..059) | Full unit coverage of payload/backoff/independence logic; live-server wiring is TC-I scope. |
| 5.1 Logging | 2 | 2 | (covered incidentally by TC-U-014, TC-U-053, TC-U-054 asserting log level/content) | |
| 5.2 Configuration Management | 2 | 2 | 4 (TC-U-001..004) | Full unit coverage. |
| M2 4.1 Proto Vendoring & Codegen | 6 | 3 | (verified via `make check-generate-proto` in CI + file diff against the pinned commit, not a Ginkgo spec) | See `osac-sp-m2-grpc-client-generation.spec.md`. |
| M2 4.2 Generated Client Accessors | 3 | 3 | 9 (TC-U-100..108) | Full unit coverage. |

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
| TC-U-023 | Discovers token endpoint via RFC 8414 (`oauth-authorization-server`) before fetching a token | REQ-OSAC-011, REQ-OSAC-012, AC-OSAC-011 | Fake `oauth-authorization-server` discovery document's `token_endpoint` differs from the issuer URL (e.g. issuer `https://kc.example.com/realms/osac`, `token_endpoint` `https://kc.example.com/realms/osac/protocol/openid-connect/token`); call `Start`; assert the fake HTTP server recorded the token POST at the discovered URL, recorded zero token POSTs at the bare issuer URL, and recorded zero requests to `.well-known/openid-configuration` (RFC 8414 succeeded, so no fallback was needed). |
| TC-U-024 | Discovery failure does not panic or block `Start`, retried with backoff | REQ-OSAC-011, REQ-OSAC-060, AC-OSAC-012 | Both fake discovery endpoints (`.well-known/oauth-authorization-server` and `.well-known/openid-configuration`) return `500` on every call; call `Start` with a bounded test timeout; assert `Start` returns without hanging, `TokenStatus().Valid == false` throughout, and discovery is retried using the same exponential backoff sequence as TC-U-015. |
| TC-U-025 | Falls back to OpenID Connect discovery (`openid-configuration`) when RFC 8414 discovery fails | REQ-OSAC-011, REQ-OSAC-012, AC-OSAC-013 | Fake `.well-known/oauth-authorization-server` returns `404` (as on a Keycloak realm that doesn't expose it); fake `.well-known/openid-configuration` returns a valid document; call `Start`; assert the fake HTTP server recorded exactly one request to each well-known path (RFC 8414 tried first and failed, OIDC fallback tried second and succeeded) and the token POST landed at the fallback-discovered `token_endpoint`. |
| TC-U-016 | Token validity query — valid | REQ-OSAC-070, AC-OSAC-070 | Seed the bootstrap with a cached token expiring in 10 minutes; call `TokenStatus()`; assert `valid == true` and `expiresAt` equals the exact seeded expiry time. |
| TC-U-017 | Token validity query — never obtained | REQ-OSAC-070, AC-OSAC-071 | Construct the bootstrap with no token ever fetched; call `TokenStatus()`; assert `valid == false`. |
| TC-U-018 | Token validity query — expired | REQ-OSAC-070 | Seed a cached token with `expiresAt` in the past; call `TokenStatus()`; assert `valid == false`. |
| TC-U-019 | Connectivity probe success | REQ-OSAC-080, AC-OSAC-080 | Fake `Capabilities` client returns success within timeout; call `Probe(ctx)`; assert `connected == true` and `err == nil`. |
| TC-U-020 | Connectivity probe failure — unreachable | REQ-OSAC-080, AC-OSAC-081 | Fake `Capabilities` client returns a gRPC `Unavailable` error; call `Probe(ctx)`; assert `connected == false` and the returned error wraps a gRPC status with code `Unavailable`. |
| TC-U-021 | Connectivity probe failure — timeout | REQ-OSAC-080, AC-OSAC-081 | Fake `Capabilities` client blocks longer than `osac.probeTimeout`; call `Probe(ctx)` with that timeout; assert `connected == false` and the error is (or wraps) `context.DeadlineExceeded`. |
| TC-U-022 | Connectivity probe does not trigger token refresh | REQ-OSAC-090, AC-OSAC-090 | Seed a token fetch call counter; call `Probe(ctx)` several times; assert the token-fetch call counter is unchanged (still its pre-probe value) after all calls. |
| TC-U-106 | Discovery request construction failure | REQ-OSAC-011 | Call `fetchWellKnownTokenEndpoint` directly (white-box) with an issuer URL containing an invalid control character, making `http.NewRequestWithContext` fail; assert the returned error wraps a message containing `"building discovery request"`. |
| TC-U-107 | Discovery document fetch transport failure | REQ-OSAC-011, AC-OSAC-012 | Call `fetchWellKnownTokenEndpoint` directly with an `http.Client` pointed at a closed (never-listening) loopback address, so `httpClient.Do` fails with a connection error rather than returning any HTTP status; assert the returned error wraps a message containing `"fetching discovery document"`. |
| TC-U-108 | Discovery document decode failure | REQ-OSAC-011 | Fake discovery endpoint returns `200` with a non-JSON body; call `fetchWellKnownTokenEndpoint` directly; assert the returned error wraps a message containing `"decoding discovery document"`. |
| TC-U-109 | Discovery document missing `token_endpoint` | REQ-OSAC-011 | Fake discovery endpoint returns `200` with valid JSON but an empty/absent `token_endpoint` field; call `fetchWellKnownTokenEndpoint` directly; assert the returned error wraps a message containing `"has no token_endpoint"`. |
| TC-U-110 | `dialOptions` propagates TLS credential construction failure | REQ-OSAC-040 | `osac.tlsEnabled=true`, `tlsCertFile` points at a nonexistent file; call `dialOptions` directly; assert it returns a non-nil error and a nil dial-option slice (the same underlying failure TC-U-013 already proves for `transportCredentials`, now proven not swallowed one level up). |
| TC-U-111 | `New` surfaces dial-options construction failure | REQ-OSAC-040, AC-OSAC-040 | Same broken TLS config as TC-U-110, fed to `New(cfg, logger)`; assert `New` returns a non-nil error wrapping `"building gRPC dial options"` and a nil `*Bootstrap`. |
| TC-U-112 | `New` surfaces gRPC client construction failure | REQ-OSAC-030, AC-OSAC-030 | `osac.fulfillmentAddress` contains an invalid control character, making `grpc.NewClient` fail synchronously (confirmed by spike: most malformed targets dial lazily and don't error here, but an invalid-character target does); call `New(cfg, logger)`; assert it returns a non-nil error wrapping `"creating gRPC client"` and a nil `*Bootstrap`. |
| TC-U-113 | `Close` is a no-op when no connection was ever dialed | REQ-OSAC-030 | Construct a `Bootstrap` via `newBootstrap` (which never assigns `conn`, unlike `New`); call `Close()`; assert it returns `nil` without panicking. |

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
| TC-U-035 | Does not force token refresh | REQ-HLT-050, REQ-HLT-055, AC-HLT-040 | Mock's `TokenStatusFunc` asserts it was called (reads cache) and a separate injected "force refresh" spy is asserted to have zero invocations after `CheckHealth`. |
| TC-U-036 | Probe invoked exactly once per health call | REQ-HLT-060, AC-HLT-050 | Mock's `ProbeFunc` increments a counter; call `CheckHealth` once; assert the counter equals exactly `1` (not 0, not 2+). |
| TC-U-037 | Unhealthy detail names token cause | REQ-HLT-070, AC-HLT-060 | Mock returns `valid=false`, `connected=true`; assert the `detail` string contains `"token"` (case-insensitive) and does not claim a connectivity failure. |
| TC-U-038 | Unhealthy detail names connectivity cause | REQ-HLT-070 | Mock returns `valid=true`, `connected=false`; assert the `detail` string contains `"osac"` or `"connect"` (case-insensitive) and does not claim a token failure. |
| TC-U-039 | Both StrictServerInterface entry points report identical status | REQ-HLT-015, AC-HLT-011 | Fixed mock state (`valid=true`, `connected=false`); call `GetClustersHealth` and `GetVMsHealth` independently; assert both returned bodies have identical `status` and `detail` values. |

---

## 4. `internal/registration`

Collaborator faked: `environment-agent`'s `pkg/client` HTTP round-tripper
(DD-203) — injected as a fake `http.RoundTripper` returning canned status
codes/bodies per call, so payload construction and retry/backoff logic are
tested without a real registration server (end-to-end wiring is TC-I
scope).

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-050 | Cluster registration payload fields | REQ-REG-020, REQ-REG-030, REQ-REG-040, AC-REG-020 | Trigger the cluster registration call; capture the request body sent to the fake round-tripper; assert `name == "osac-sp-cluster"`, `service_type == "cluster"`, `endpoint == "<provider.endpoint>/api/v1alpha1/clusters"`, `schema_version == "v1alpha1"`, and within `metadata`: `supported_platforms == ["baremetal"]`, `supported_provisioning_types == ["hypershift"]`, `len(kubernetes_supported_versions) > 0`. |
| TC-U-051 | VM registration payload fields | REQ-REG-020, REQ-REG-030, AC-REG-021 | Trigger the vm registration call; assert `name == "osac-sp-vm"`, `service_type == "vm"`, `endpoint == "<provider.endpoint>/api/v1alpha1/vms"`, `schema_version == "v1alpha1"`. |
| TC-U-052 | Two independent registration calls are issued | REQ-REG-010, AC-REG-010 | Trigger `Start()`; assert the fake round-tripper recorded exactly 2 initial requests, with distinct `name` values `osac-sp-cluster` and `osac-sp-vm`. |
| TC-U-053 | 409 Conflict is retried on the re-registration cadence, not treated as fatal | REQ-REG-080, AC-REG-060 | Fake round-tripper returns `409` for every `vm` call, `201` for `cluster`; assert (a) `vm`'s `409` is logged at WARN level, (b) `vm` keeps sending further registration requests on the re-registration cadence (not exponential backoff) rather than stopping after the first `409`, (c) `cluster`'s state is `registered` and unaffected throughout. Restores the pre-Phase-1 design DD-050 had replaced — see DD-203. |
| TC-U-054 | Non-retryable 4xx stops retries | REQ-REG-090, AC-REG-070 | Fake round-tripper returns `400` for the `cluster` call; advance the fake clock past several backoff intervals; assert no further `cluster` registration requests were sent after the first `400`. |
| TC-U-055 | Retryable failure uses exponential backoff | REQ-REG-070, AC-REG-050 | Fake round-tripper returns connection-refused-equivalent errors 3 times then succeeds for `cluster`; assert the recorded retry delays match the configured exponential sequence exactly. |
| TC-U-056 | Registration does not block on construction | REQ-REG-050, AC-REG-030 | Fake round-tripper's handler blocks until explicitly released; call `Start()`; assert `Start()` returns before the round-tripper is released (i.e., registration runs in a goroutine, not synchronously in `Start`). |
| TC-U-057 | Cluster failure does not affect VM registration | REQ-REG-060, AC-REG-040 | Fake round-tripper returns `500` for every `cluster` call and `201` for the first `vm` call; assert `vm`'s state reaches `registered` regardless of `cluster`'s ongoing retries. |
| TC-U-058 | Idempotent re-registration reuses same name | REQ-REG-100, AC-REG-080 | Call the registration path twice (simulating a restart); assert both calls send the same `name`/`service_type` pair (no suffix/uniqueness token appended) so `environment-agent`'s idempotency-on-`name` behavior is preserved. |
| TC-U-059 | No Authorization header on registration requests | REQ-REG-115, AC-REG-095 | Trigger both registration calls; inspect the requests recorded by the fake round-tripper; assert neither request has an `Authorization` header set. |
| TC-U-060 | Backoff growth is capped at `maxBackoff` | REQ-REG-070 | Configure a small `initialBackoff`/`maxBackoff` (e.g. 1ms/4ms); fake round-tripper fails 8 consecutive times then succeeds; assert total elapsed time until success is bounded consistently with a *capped* geometric sum (well under what an uncapped doubling sequence over 8 retries would require), proving the cap actually bounds retry growth rather than merely being computed and discarded. |

**Coverage note:** `NewRegistrar`'s `agentclient.NewClientWithResponses` error-wrap branch ([registration.go](../../internal/registration/registration.go)) is not exercised by any TC here — it is currently unreachable given `environment-agent`'s generated client (same oapi-codegen shape as `control-plane`'s formerly did): `NewClient` stores the server string as-is (no parsing) and `WithHTTPClient` never returns an error, so no input this code can construct causes that branch to fire. Documented as an accepted coverage exception in the client code rather than tested with a fabricated failing fake, consistent with this suite's "test real production types" convention.

---

## 5. `internal/apiserver`

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-070 | Panic in handler returns RFC 9457 INTERNAL | REQ-HTTP-070, AC-HTTP-070 | Register a handler that panics with a string value; send a request via `httptest`; assert response status `== 500`, `Content-Type == "application/problem+json"`, and the decoded body's `type` field equals exactly `"https://dcm-project.github.io/problems/internal"`. |
| TC-U-071 | Request logging captures method/path/status/duration | REQ-HTTP-060, AC-HTTP-060 | Inject a test log handler capturing structured attributes; send a `GET /api/v1alpha1/clusters/health` request; assert the captured log record has `method == "GET"`, `path == "/api/v1alpha1/clusters/health"`, `status` equal to the actual response status code, and a `duration` attribute present with a non-negative value. |
| TC-U-072 | Request timeout cancels context | REQ-HTTP-090, AC-HTTP-090 | Configure `requestTimeout=10ms`; register a handler that sleeps 100ms and then checks `ctx.Err()`; assert the handler observes `context.DeadlineExceeded` (not nil). |
| TC-U-073 | Recovery middleware is outermost | REQ-HTTP-070 | Register a second middleware that also panics; assert the outer recovery middleware still produces the RFC 9457 INTERNAL response rather than an unhandled panic escaping the test. |
| TC-U-074 | `WithOnReady`'s callback fires exactly once, after real readiness | REQ-REG-050, AC-REG-030 | Register an `onReady` spy via `WithOnReady`; call `Run`; assert the spy was invoked exactly once, only after the readiness probe's first successful response. |
| TC-U-075 | A panicking `onReady` callback is recovered; the server keeps serving | REQ-HTTP-070 (defensive robustness, same principle applied to internal callbacks) | `onReady` panics; assert `Run` does not crash/return early and a subsequent request against the running server still succeeds normally. |
| TC-U-076 | A failed readiness probe skips `onReady` without stopping the server | REQ-REG-050 (negative case) | Cancel the context passed to `Run` immediately so `waitForReady` fails fast; assert the `onReady` spy was never invoked and `Run` returns without hanging. |
| TC-U-077 | `waitForReady` succeeds once the server starts accepting connections | REQ-HTTP-030 | Real loopback listener with a running `Serve` goroutine; call `waitForReady` directly; assert it returns `nil` promptly. |
| TC-U-078 | `waitForReady` returns an error when the server never becomes reachable | REQ-HTTP-030 (negative case) | Use `WithReadinessTiming` to shrink the timeout/interval to milliseconds; call `waitForReady` against an address nothing is listening on; assert it returns a non-nil timeout error. |
| TC-U-079 | `waitForReady` returns the context's error when cancelled mid-poll | REQ-HTTP-030 (negative case) | Cancel the context shortly after calling `waitForReady` against an unreachable address; assert the returned error is (or wraps) `context.Canceled`. |
| TC-U-080 | A genuine `Serve` error is surfaced as `Run`'s return error | REQ-HTTP-030 | Pass an already-closed `net.Listener` to `Run`; assert it returns a non-nil error (not silently swallowed like the expected `http.ErrServerClosed` case). |
| TC-U-081 | A `Shutdown` that exceeds `ShutdownTimeout` is surfaced as `Run`'s return error | REQ-HTTP-040 | Configure a 1ms `ShutdownTimeout` with a handler still sleeping when `ctx` is cancelled; assert `Run` returns a non-nil error wrapping the shutdown deadline error. |
| TC-U-082 | `newBadRequestHandler`/`NewRequestErrorHandler` writes an RFC 9457 `INVALID_ARGUMENT` response | REQ-HTTP-070 | Call the returned handler directly against an `httptest.ResponseRecorder`; assert status `400`, `Content-Type: application/problem+json`, decoded `type == v1alpha1.INVALIDARGUMENT`, and `instance` equals the request's exact URI. |
| TC-U-083 | `NewResponseErrorHandler` writes an RFC 9457 `INTERNAL` response without leaking the error | REQ-HTTP-070 | Call the returned handler directly with a deliberately sensitive error value; assert the decoded body's `detail` equals the generic `httperror.InternalDetail` constant, never the raw error string. |
| TC-U-084 | `requestInstance` returns `nil` for a `nil` request | REQ-HTTP-070 (defensive/nil-safety) | Call `requestInstance(nil)` directly; assert the result is `nil`. |
| TC-U-085 | `statusRecordingResponseWriter.Unwrap` returns the original writer | REQ-HTTP-070 (net/http `http.ResponseController` compatibility) | Wrap a recorder, call `Unwrap()`; assert the returned value is the exact same recorder instance. |
| TC-U-086 | `statusRecordingResponseWriter.Write` records status 200 when called without a preceding `WriteHeader` | REQ-HTTP-070 (net/http's documented implicit-200 behavior) | Call `Write` directly on a freshly constructed wrapper (no `WriteHeader` call first); assert the recorded `statusCode` is `200`. |
| TC-U-087 | `http.ErrAbortHandler` is re-panicked, not converted into an RFC 9457 response | REQ-HTTP-070 (net/http `ErrAbortHandler` convention) | Handler panics with `http.ErrAbortHandler`; assert the client observes an aborted connection (not a well-formed response) and the server keeps serving subsequent requests normally. |
| TC-U-088 | A panic after headers were already sent logs a warning instead of double-writing | REQ-HTTP-070 (negative case) | Handler calls `WriteHeader(200)` then panics; assert the client still receives the already-sent `200` (not rewritten to `500`) and a warning log records "headers already sent". |
| TC-U-089 | `waitForReady` returns an error if the probe request itself cannot be constructed | REQ-HTTP-030 (negative case) | Call `waitForReady` with a deliberately malformed address (a raw control character, rejected by `net/url`); assert it returns a non-nil error mentioning "creating readiness probe request", rather than looping forever. |

---

## 6. `internal/httperror`

Collaborator faked: an `http.ResponseWriter` whose `Write` deliberately
returns an error, for TC-U-092 only — everything else uses a real
`httptest.NewRecorder()`. This package's `WriteResponse` is already
exercised indirectly through `internal/apiserver`'s TC-U-070/073 (a real
panic response); the cases below test it directly and in isolation,
including branches those indirect paths never reach (e.g. a `nil` instance,
an encode failure).

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-090 | Writes exact status/headers/body fields | REQ-HTTP-070 | Call `WriteResponse` with concrete `errType`/`title`/`detail`/`instance` values; assert the recorder's status code, `Content-Type: application/problem+json` header, and every decoded body field equal those exact values. |
| TC-U-091 | A `nil` instance is omitted from the encoded body | REQ-HTTP-070 | Call `WriteResponse` with `instance == nil`; assert the decoded body's `instance` field is absent/null, not an empty string. |
| TC-U-092 | An encode failure is logged, not panicked | REQ-HTTP-070 (defensive robustness) | Inject a fake `http.ResponseWriter` whose `Write` always errors; call `WriteResponse`; assert it returns normally (no panic) and the injected logger recorded an error-level entry. |

---

## 7. `internal/util`

`Ptr[T]` has no independent business requirement of its own — it exists so
OpenAPI-generated pointer-typed struct fields (used throughout
`internal/httperror` and the generated `api/v1alpha1` types to distinguish
"absent" from "zero value") have one tested, safe way to take an address.
Covered here once, directly, rather than re-verified indirectly everywhere
it's called.

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-093 | Returns a pointer that dereferences to the input, for multiple types | N/A (generic language-level helper, no REQ) | Call `Ptr(v)` for a `string`, an `int`, and a struct value; assert `*Ptr(v) == v` in each case, and that two separate calls with equal values return distinct addresses (a real pointer, not a shared/cached one). |

---

## 8. `cmd/osac-service-provider`

Prior to this addition, this package had unit-level coverage only for
`run`'s happy-path wiring indirectly via `main_integration_test.go` (see
`osac-sp-integration.test-plan.md`); its top-level error-wrapping branches
and `mainRun`'s exit-code mapping were untested. TC-U-094..097 below call
`run`/`mainRun` directly and in-process — no real OSAC/Keycloak/
control-plane fakes needed, since each case fails before reaching those
collaborators.

TC-U-099 (Milestone 4) covers a different concern introduced once VM CRUD
landed: `apiHandler` now composes `internal/health.Handler` with
`internal/handlers/vm.Handler` to satisfy the full, larger
`oapigen.StrictServerInterface` — this proves that composition's 4 new
forwarding methods are wired to the real `internal/vm.Service`, using a
minimal `bufconn` fake (distinct from `internal/handlers/vm`'s own, richer
fixture, which already exhaustively covers the CRUD behavior itself).

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-094 | `run` wraps and returns a config-load failure | REQ-XC-CFG-020 | Leave a required env var unset; call `run` directly; assert it returns a non-nil error mentioning "initializing" and does not attempt to bind a listener. |
| TC-U-095 | `run` wraps and returns a listener-bind failure | REQ-HTTP-030 (negative case) | Set `SP_SERVER_ADDRESS` to an address already bound by a test-held listener; call `run`; assert it returns a non-nil error mentioning "listening". |
| TC-U-096 | `run` wraps and returns an OSAC bootstrap construction failure | REQ-OSAC-040 (negative case) | Set `SP_OSAC_TLS_ENABLED=true` and `SP_OSAC_TLS_CERT_FILE` to a nonexistent path; call `run`; assert it returns a non-nil error mentioning "creating OSAC client bootstrap". |
| TC-U-097 | `mainRun` returns exit code `1` when `run` fails | REQ-XC-CFG-020 | Leave a required env var unset (same trigger as TC-U-094); call `mainRun` directly; assert it returns exactly `1`, in-process, without invoking `os.Exit`. |
| TC-U-098 | `apiHandler`'s 4 Cluster forwarding methods each reach the wired `internal/cluster.Service` (Milestone 3) | REQ-CREATE-010, REQ-GET-010, REQ-LIST-010, REQ-DELETE-010 (wiring only — exhaustive CRUD business logic is `internal/cluster`'s and `internal/handlers/cluster`'s own scope, already 100%-covered there) | Construct a real `apiHandler` wrapping a real `clusterhandlers.Handler`/`cluster.Service` dialed against a minimal `bufconn` fake OSAC `Clusters`/`ClusterTemplates` server pair; call each of `ListClusters`/`CreateCluster`/`GetCluster`/`DeleteCluster` on `apiHandler` directly; assert the fake's respective call counter is exactly `1` after each — proving `main.go`'s composition actually reaches `internal/cluster`, not a re-test of what it does once there. |
| TC-U-099 | `apiHandler`'s 4 VM forwarding methods each reach the wired `internal/vm.Service` (Milestone 4) | REQ-VMCREATE-010, REQ-VMGET-010, REQ-VMLIST-010, REQ-VMDELETE-010 (wiring only — exhaustive CRUD business logic is `internal/vm`'s and `internal/handlers/vm`'s own scope, already 100%-covered there) | Construct a real `apiHandler` wrapping a real `vmhandlers.Handler`/`vm.Service` dialed against a minimal `bufconn` fake OSAC server trio; call each of `ListVMs`/`CreateVM`/`GetVM`/`DeleteVM` on `apiHandler` directly; assert the fake's respective call counter is exactly `1` after each — proving `main.go`'s composition actually reaches `internal/vm`, not a re-test of what it does once there. |
| TC-U-114 | `run` wraps and returns a status-publisher construction failure (Milestone 5) | REQ-PUBLISH-020 (negative case) | Set `DCM_NATS_URL` to a syntactically-invalid URL (`"://not-a-valid-url"`, same value as `internal/statuspublisher`'s own TC-U-417) so `statuspublisher.NewPublisher` fails synchronously on `nats.Connect`'s URL parsing, before any live broker is needed; call `run`; assert it returns a non-nil error mentioning "creating status publisher". Uses the next free ID above M2's `TC-U-113` since `TC-U-098`/`TC-U-099` above already claim the ones this case's own note originally reserved. |

**Coverage exceptions (documented in-code, not tested):**
- `main()`'s single `os.Exit(mainRun())` statement — observing it would
  require a subprocess/exec harness this repo doesn't have, since calling it
  in-process terminates the test binary itself.
- `mainRun`'s happy path — the `signal.NotifyContext(...)` call through its
  final `return 0` — whose effect (graceful shutdown on cancellation) is
  already fully proven by `main_integration_test.go`'s TC-I-003/004 via
  direct `ctx` cancellation against `run` directly; unit-testing `mainRun`
  itself reaching `return 0` would require delivering a real OS signal to
  the test process, precisely what that design avoids.
- `run`'s `registration.NewRegistrar` error-wrap branch — transitively
  unreachable for the same reason as `NewRegistrar`'s own documented
  exception (section 4's coverage note): no input reachable from this
  codebase can make `NewRegistrar` return a non-nil error today.

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

Milestone 2 exposes one new method, `Bootstrap.Conn() *grpc.ClientConn` — no
per-service wrapper methods (see DD-020 in the M2 spec for the ecosystem
precedent behind that choice). Tests construct each typed client directly
from `Conn()` via `publicv1.NewXClient(...)`, matching how Milestones 3/4's
handler code will call it.

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-100 | `Conn()` returns the exact shared, authenticated connection | REQ-GRPC-010, AC-GRPC-010 | Construct `Bootstrap` with a known `*grpc.ClientConn` dialed against an in-test `bufconn.Listener` (same pattern as TC-U-019); call `Conn()`; assert it returns that exact connection value and that a call made via `publicv1.NewClustersClient(bootstrap.Conn())` reaches the same `bufconn` listener the existing internal `Capabilities` client already reaches — no second connection is dialed. |
| TC-U-101 | `Clusters.List` round-trips real data via `Conn()` | REQ-GRPC-020, AC-GRPC-020 | Fake `bufconn` server's `Clusters.List` handler returns a canned response containing a cluster with `id="c1"`, `status.state=CLUSTER_STATE_READY` (`status` is the nested `ClusterStatus` message; `state` its enum field); call `publicv1.NewClustersClient(bootstrap.Conn()).List(ctx, ...)`; assert the decoded response's entry equals those exact field values, not merely `len(results) > 0`. |
| TC-U-102 | `ComputeInstances.List` round-trips real data via `Conn()` | REQ-GRPC-020, AC-GRPC-020 | Same pattern as TC-U-101 for `publicv1.NewComputeInstancesClient(bootstrap.Conn()).List`, with a compute instance carrying known `id`/`status` values. |
| TC-U-103 | `Subnets.List` round-trips real data via `Conn()` | REQ-GRPC-020, AC-GRPC-020 | Same pattern as TC-U-101 for `publicv1.NewSubnetsClient(bootstrap.Conn()).List`, with a subnet carrying a known `id`/state. |
| TC-U-104 | `VirtualNetworks.List` round-trips real data via `Conn()` | REQ-GRPC-020, AC-GRPC-020 | Same pattern as TC-U-101 for `publicv1.NewVirtualNetworksClient(bootstrap.Conn()).List`, with a virtual network carrying a known `id`. |
| TC-U-105 | Clients built from `Conn()` inherit the shared bearer-token interceptor | REQ-GRPC-010, AC-GRPC-030 | Seed the bootstrap with a known cached token (`"tok-xyz"`); fake `bufconn` server's `Clusters.List` handler records the `authorization` gRPC metadata it receives; call `publicv1.NewClustersClient(bootstrap.Conn()).List(...)`; assert the recorded metadata equals exactly `"Bearer tok-xyz"` — the same value/format already proved for the `Capabilities` client in Milestone 1. |

---

## Coverage Matrix

| Spec Section | REQ Count | AC Count | TC Count (this file) | Notes |
|---|---|---|---|---|
| 4.1 HTTP Server | 10 | 10 | 20 (TC-U-070..089) | Remaining HTTP-server ACs (startup, shutdown signals, route registration) are integration-scope — see `osac-sp-integration.test-plan.md`. |
| 4.2 OSAC Client Bootstrap | 11 | 14 | 24 (TC-U-010..025, TC-U-106..113) | Full unit coverage, including error-branch closure (TC-U-106..113, Milestone 2); real-dial-over-the-wire cases are TC-I scope. |
| 4.3 Health Service | 9 | 10 | 10 (TC-U-030..039) | Full unit coverage. |
| 4.4 SP Registration (`environment-agent`, DD-203) | 11 | 11 | 11 (TC-U-050..060) | Full unit coverage of payload/backoff/independence/409-retry logic (one branch documented as currently unreachable given `environment-agent`'s client — see section 4's coverage note); live-server wiring is TC-I scope. |
| 5.1 Logging | 2 | 2 | (covered incidentally by TC-U-014, TC-U-053, TC-U-054 asserting log level/content) | |
| 5.2 Configuration Management | 2 | 2 | 4 (TC-U-001..004) | Full unit coverage. |
| M2 4.1 Proto Vendoring & Codegen | 6 | 3 | (verified via `make check-generate-proto` in CI + file diff against the pinned commit, not a Ginkgo spec) | See `osac-sp-m2-grpc-client-generation.spec.md`. |
| M2 4.2 Shared Connection Accessor | 2 | 3 | 6 (TC-U-100..105) | Full unit coverage. |
| N/A `internal/httperror` (cross-cutting, REQ-HTTP-070) | 0 (no dedicated section; implements REQ-HTTP-070) | 0 | 3 (TC-U-090..092) | Direct/isolated coverage of the RFC 9457 response writer shared by all error paths above. |
| N/A `internal/util` (generic helper, no REQ) | 0 | 0 | 1 (TC-U-093) | Coverage-completeness only. |
| N/A `cmd/osac-service-provider` unit-level (REQ-XC-CFG-020, REQ-HTTP-030, REQ-OSAC-040, REQ-CREATE-010, REQ-GET-010, REQ-LIST-010, REQ-DELETE-010, REQ-VMCREATE-010, REQ-VMGET-010, REQ-VMLIST-010, REQ-VMDELETE-010, REQ-PUBLISH-020) | 0 (no dedicated section; covers existing REQs at a new layer) | 0 | 7 (TC-U-094..099, TC-U-114) | `run`/`mainRun`'s own error-wrapping branches (TC-U-094..097), `apiHandler`'s Cluster (TC-U-098, Milestone 3) and VM (TC-U-099, Milestone 4) CRUD forwarding wiring, plus `run`'s `statuspublisher.NewPublisher` error-wrap branch (TC-U-114, Milestone 5). Two lines remain a documented, in-code exception — see section 8. |

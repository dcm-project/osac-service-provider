# Test Plan: OSAC Service Provider — Milestone 1 (Integration Tests)

Scope: integration tests for Milestone 1 ("Scaffold + registration +
health") as specified in
[`.ai/specs/osac-sp.spec.md`](../specs/osac-sp.spec.md). Integration tests
wire multiple real components together — a real HTTP server bound to a
loopback port, a real gRPC server over `bufconn` implementing the
`Capabilities` service, and a real `httptest.Server` standing in for
Keycloak's token endpoint and `control-plane`'s SP registration
endpoint — with only the genuinely external systems (real OSAC, real
Keycloak, real `control-plane`) replaced by fakes.

**Framework:** Ginkgo v2 + Gomega. Files use the `_integration_test.go`
suffix. Run with:

```bash
go run github.com/onsi/ginkgo/v2/ginkgo -r --race -focus "TC-I-010" ./internal/...
```

**Assertion discipline:** assert actual observed values end-to-end (the
real HTTP response body/headers, the real requests received by the fake
registration server, real signal-triggered shutdown timing) — not just
"no error" / "server started".

---

## 1. Server lifecycle

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-001 | Server starts and listens on configured address | REQ-HTTP-010, AC-HTTP-010 | Start the SP with `SP_SERVER_ADDRESS=127.0.0.1:0` (OS-assigned port) and all other required config pointed at fakes; assert a TCP dial to the resolved address succeeds within a short deadline. |
| TC-I-002 | Both health routes are reachable end-to-end | REQ-HTTP-020, REQ-HTTP-025, AC-HTTP-020, AC-HTTP-025 | With the server running, issue a real `GET /api/v1alpha1/clusters/health` and a real `GET /api/v1alpha1/vms/health` over HTTP; assert response status `200` for both. |
| TC-I-003 | Graceful shutdown on SIGTERM drains in-flight request | REQ-HTTP-030, AC-HTTP-030 | Start the server with `shutdownTimeout=2s`; begin a request to a handler that sleeps 500ms; send SIGTERM to the process/goroutine group immediately after; assert the in-flight request still completes with its original response, and the server's listener is closed within `shutdownTimeout` of the signal. |
| TC-I-004 | Graceful shutdown on SIGINT behaves identically | REQ-HTTP-040, AC-HTTP-040 | Same as TC-I-003 but sending SIGINT; assert identical drain/exit behavior. |
| TC-I-005 | New connections rejected after shutdown begins | REQ-HTTP-030 | After triggering shutdown, attempt a new `GET /api/v1alpha1/clusters/health`; assert the connection is refused or the request fails (server no longer accepting new connections). |
| TC-I-006 | Startup fails fast with missing required config | REQ-XC-CFG-020, AC-XC-CFG-020 | Start the SP binary/entrypoint with `SP_ENDPOINT` unset and everything else valid; assert the process exits with a non-zero status before the HTTP listener is opened (assert no successful dial to the configured address occurs within a bounded wait). |

---

## 2. Health endpoints against real (fake) OSAC + Keycloak

Test harness: an `httptest.Server` serving a real
`.well-known/oauth-authorization-server` discovery document (RFC 8414 — the
primary discovery path per REQ-OSAC-011/REQ-OSAC-012/DD-060) plus a canned OAuth2
token-endpoint response at the URL that document advertises (token requests
sent directly to the issuer URL, bypassing discovery, MUST NOT succeed
against this fake). The RFC 8414 -> OpenID Connect Discovery fallback
ordering itself is unit-test scope (TC-U-023/024/025); this harness only
needs to serve one working discovery document to exercise the end-to-end
health/token-caching behavior below, and a real gRPC server bound via
`bufconn` implementing
`osac.public.v1.Capabilities` (generated client dials through a
`bufconn`-backed `grpc.ClientConn`). Unless noted, cases assert against
`/api/v1alpha1/clusters/health`; TC-I-016 additionally checks
`/api/v1alpha1/vms/health` reports the same result (DD-010).

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-010 | Health reports healthy end-to-end | REQ-HLT-030, AC-HLT-020 | Fake Keycloak's discovery + token endpoints succeed; fake `Capabilities/Get` succeeds; start the full SP; `GET /api/v1alpha1/clusters/health`; assert response body's `status == "healthy"` and `Content-Type == "application/json"` exactly. |
| TC-I-011 | Health reports unhealthy when Keycloak is down at startup | REQ-HLT-030, AC-HLT-025 | Fake Keycloak server is closed/unreachable from the start; fake `Capabilities/Get` succeeds; `GET /api/v1alpha1/clusters/health`; assert `status == "unhealthy"` while the HTTP status code is still `200`. |
| TC-I-012 | Health reports unhealthy when OSAC gRPC is unreachable | REQ-HLT-030, AC-HLT-026 | Fake Keycloak succeeds; the `bufconn` gRPC server is not started (or closed); `GET /api/v1alpha1/clusters/health`; assert `status == "unhealthy"` with HTTP `200`. |
| TC-I-013 | Health recovers after OSAC becomes reachable | REQ-HLT-060, AC-HLT-050 | Start with the gRPC server down (unhealthy, per TC-I-012); start the gRPC server; `GET /api/v1alpha1/clusters/health` again; assert `status == "healthy"` on this subsequent call, proving the probe is re-evaluated per request rather than cached. |
| TC-I-014 | Health does not re-fetch token once cached, even under repeated polling | REQ-HLT-050, REQ-HLT-055, AC-HLT-040 | Fake Keycloak counts hits to its token endpoint (not its discovery endpoint); issue 10 consecutive `GET /api/v1alpha1/clusters/health` calls within the token's validity window; assert the token-endpoint hit counter equals exactly `1` (the initial fetch only). |
| TC-I-015 | Health probe respects configured timeout | REQ-OSAC-080, AC-OSAC-081 | Configure `SP_OSAC_PROBE_TIMEOUT=200ms`; `bufconn` `Capabilities/Get` handler sleeps 1s before responding; `GET /api/v1alpha1/clusters/health`; assert the HTTP response returns within ~200-400ms (not ~1s+) and reports `status == "unhealthy"`. |
| TC-I-016 | Both health paths agree end-to-end | REQ-HLT-015, AC-HLT-011 | With the SP in a fixed healthy or unhealthy state (fakes configured either way), call `GET /api/v1alpha1/clusters/health` and `GET /api/v1alpha1/vms/health`; assert both return the same `status` and HTTP code. |
| TC-I-017 | Token fetch uses the discovered endpoint, not the configured issuer URL | REQ-OSAC-011, REQ-OSAC-012, AC-OSAC-011 | Fake Keycloak's `oauth-authorization-server` discovery document advertises a token endpoint at a *different path* than the issuer URL itself; assert the recorded requests show the token POST landing on the discovered path, zero POSTs landing on the bare issuer URL, and zero requests to `.well-known/openid-configuration` (RFC 8414 succeeded, so the fallback was never exercised). |

---

## 3. SP registration against a fake `control-plane`

Test harness: an `httptest.Server` implementing `control-plane`'s current
`api/sp/v1alpha1/provider/openapi.yaml` contract for
`POST /api/v1alpha1/providers` (pinned to the same commit SHA as the
`go.mod` dependency, per DD-050), with a handler that records every request
body and can be configured to return specific status codes per call. This
mirrors `control-plane`'s actual, implemented `RegisterOrUpdateProvider`
conflict semantics (name/ID-based only, no per-service-type exclusivity —
see DD-050) rather than faking a live instance.

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-020 | Both registrations are sent on startup | REQ-REG-010, AC-REG-010 | Fake `control-plane` returns `201` for all requests; start the SP; within a bounded wait, assert the fake recorded exactly 2 distinct requests with `name` values `"osac-sp-cluster"` and `"osac-sp-vm"` respectively. |
| TC-I-021 | Cluster registration payload matches contract exactly | REQ-REG-020, REQ-REG-030, REQ-REG-040, AC-REG-020 | Inspect the recorded cluster registration request body; assert every field listed in AC-REG-020 matches exactly (not just "field present"), including `supported_platforms`/`supported_provisioning_types`/`kubernetes_supported_versions` nested under `metadata` rather than top-level. |
| TC-I-022 | Registration does not block server readiness | REQ-REG-050, AC-REG-030 | Fake `control-plane`'s handler blocks (does not respond) until explicitly released; assert `GET /api/v1alpha1/clusters/health` still succeeds with a normal HTTP response while registration is still pending. |
| TC-I-023 | VM 409 Conflict is non-retryable, cluster registration still succeeds | REQ-REG-060, REQ-REG-090, AC-REG-040, AC-REG-060 | Fake `control-plane` returns `409` for `vm` requests and `201` for `cluster`; assert (via the fake's recorded request log, polled with a bounded wait) that `cluster` registration completed successfully, that exactly one `vm` request was recorded (no retry after the `409`), and that the `vm` failure is logged at ERROR level. Supersedes the pre-pivot "409 is retryable" test design — see DD-050. |
| TC-I-024 | Non-retryable 4xx on cluster does not crash the process or block vm registration | REQ-REG-090, AC-REG-070 | Fake `control-plane` returns `400` for `cluster`, `201` for `vm`; assert the SP process/goroutines remain alive (health endpoint still responds) and `vm` registration recorded a successful request. |
| TC-I-025 | Retry backoff observed against a flaky fake `control-plane` | REQ-REG-070, AC-REG-050 | Fake fails the first 2 requests per service type (connection reset) then succeeds; assert both service types eventually reach a recorded successful request, and the elapsed wall-clock time between attempts is consistent with the configured initial backoff (bounded assertion, e.g. `>= initialBackoff` and `< initialBackoff * 4`). |
| TC-I-026 | Idempotent re-registration on simulated restart | REQ-REG-100, AC-REG-080 | Run the registration flow twice against the same fake `control-plane` instance (simulating a process restart) without changing configuration; assert both runs send identical `name`/`service_type` pairs, consistent with `control-plane`'s documented idempotency-on-`name` behavior (no client-side dedup logic needed, but no drift in identifying fields either). |
| TC-I-027 | No Authorization header sent to the fake `control-plane` | REQ-REG-115, AC-REG-095 | Fake handler records all request headers; start the SP and let both registrations fire; assert neither recorded request has an `Authorization` header. |

---

## 4. Full-stack smoke test

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-030 | Cold start reaches healthy + fully registered | REQ-HTTP-010, REQ-OSAC-010, REQ-OSAC-011, REQ-OSAC-030, REQ-HLT-030, REQ-REG-010, AC-HLT-020, AC-REG-010 | With all fakes healthy (Keycloak discovery + token endpoints valid, `bufconn` Capabilities up, fake `control-plane` returning 201), start the SP from cold (fresh config, no pre-seeded state); poll `GET /api/v1alpha1/clusters/health` until `status == "healthy"` (bounded wait); assert `GET /api/v1alpha1/vms/health` reports the same status; assert the fake `control-plane` independently recorded both `osac-sp-cluster` and `osac-sp-vm` registrations by the same deadline. |

---

## Coverage Matrix

| Spec Section | REQ Count | AC Count | TC Count (this file) | Notes |
|---|---|---|---|---|
| 4.1 HTTP Server | 10 | 10 | 6 (TC-I-001..006) | Lifecycle/signal-handling ACs not practical to unit test are covered here. |
| 4.2 OSAC Client Bootstrap | 11 | 14 | 2 dedicated (TC-I-015, TC-I-017) + covered via Health tests (TC-I-010..014) | Real `bufconn` dial path and real discovery-document fetch exercised only here; RFC 8414/OIDC fallback ordering itself is unit-test scope (TC-U-023..025). |
| 4.3 Health Service | 9 | 10 | 8 (TC-I-010..017) | End-to-end status derivation against real (fake) dependencies, including both-paths agreement. |
| 4.4 SP Registration (`control-plane`) | 10 | 10 | 8 (TC-I-020..027) | End-to-end wiring against a fake HTTP server implementing `control-plane`'s current (implemented server-side) SP API contract. |
| Full-stack | - | - | 1 (TC-I-030) | Cross-cutting cold-start smoke test. |

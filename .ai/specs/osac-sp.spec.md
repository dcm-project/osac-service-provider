# Specification: OSAC Service Provider — Milestone 1 (Scaffold + Registration + Health)

## 1. Overview

The OSAC Service Provider (OSAC SP) is a DCM Service Provider that integrates
the Open Sovereign AI Cloud (OSAC) platform with DCM, provisioning OpenShift
clusters and VMs via OSAC's fulfillment service gRPC API. **Delivery is
two-phase: Phase 1 registers with and dispatches through `control-plane`'s SP
API; migration to the `environment-agent` model is deferred to Phase 2, once
that project matures** — see DD-050 and
[enhancements#95](https://github.com/dcm-project/enhancements/issues/95).

**This spec covers Milestone 1 only** — the first independently reviewable
slice per [issue #1](https://github.com/dcm-project/osac-service-provider/issues/1)'s
suggested delivery milestones: repo skeleton, Phase 1 (`control-plane`)
registration (two service types), and a health endpoint backed by real OSAC
connectivity and OIDC token validity. **No cluster or VM CRUD endpoints are
in scope for this spec** — those land in later milestones (2–6) with their
own spec additions.

**Version scope (Milestone 1):**

- HTTP server foundation, following the same middleware chain pattern as
  sibling SPs (recovery → request logging → request timeout)
- OIDC client-credentials bootstrap against OSAC's Keycloak and a gRPC
  connection to OSAC's fulfillment service, sufficient to back a real health
  check (full CRUD-service gRPC stub generation is Milestone 2)
- `GET /api/v1alpha1/clusters/health` and `GET /api/v1alpha1/vms/health`
  (one per registered provider — see DD-010) reporting real OSAC connectivity
  + OIDC token health (not a stub that always reports healthy)
- Two-name registration with `control-plane`'s SP API (`osac-sp-cluster` /
  `osac-sp-vm`), each independently retried
- No cluster/VM REST endpoints, no gRPC CRUD calls, no status polling, no
  CloudEvents publishing — all deferred to later milestones

**Reference documents:**

- [Design Decisions](../decisions/osac-sp.decisions.md) (`DD-NNN`, referenced throughout this spec)
- [OSAC SP Enhancement](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md) (source of truth for design decisions; Phase 1/2 update tracked in [enhancements#95](https://github.com/dcm-project/enhancements/issues/95))
- [Implementation plan (issue #1)](https://github.com/dcm-project/osac-service-provider/issues/1)
- [SP Registration Flow](https://github.com/dcm-project/enhancements/blob/main/enhancements/sp-registration-flow/sp-registration-flow.md)
- [SP Health Check](https://github.com/dcm-project/enhancements/blob/main/enhancements/service-provider-health-check/service-provider-health-check.md)
- OSAC public protos: [`osac-project/fulfillment-service/proto/public/osac/public/v1/`](https://github.com/osac-project/fulfillment-service/tree/main/proto/public/osac/public/v1)
- Reference implementations (structure/conventions template): [`k8s-container-service-provider`](https://github.com/dcm-project/k8s-container-service-provider), [`acm-cluster-service-provider`](https://github.com/dcm-project/acm-cluster-service-provider), [`kubevirt-service-provider`](https://github.com/dcm-project/kubevirt-service-provider) — **note:** these also register against `control-plane`'s SP API, via the archived `service-provider-manager` client rather than `control-plane`'s newer `pkg/sp/client/provider`; OSAC SP now targets the same backend as its siblings, just on the newer client (see DD-050)
- [`dcm-project/control-plane`](https://github.com/dcm-project/control-plane) (`api/sp/v1alpha1/provider/openapi.yaml`) — authoritative Phase 1 registration contract this SP integrates with (see DD-050)
- [`dcm-project/environment-agent`](https://github.com/dcm-project/environment-agent) — Phase 2 target, deferred pending maturity (see DD-050)
- OpenAPI Spec: `api/v1alpha1/openapi.yaml` (source of truth for the API contract, once authored)

---

## 2. Architecture

```
                                     +------------------+
                                     |  control-plane   |
                                     |   (SP + SPRM)    |
                                     +--------+---------+
                                              |
                          +-------------------+-------------------+
                          ^                                       |
                          |                                       v
                   Registration (x2)                   Health Poll (x2)
              POST /api/v1alpha1/providers        GET {provider.endpoint}/health
                          |                    (healthcheck.Monitor, once per
                          |                        registered provider)
+-------------------------+---------------------------------------+--------+
|                       OSAC Service Provider                              |
|                                                                          |
|  +-------------+  +----------------+  +------------------------------+  |
|  | HTTP Server |--| Health Handler |--| OSAC Client Bootstrap         |  |
|  | (router)    |  | (cluster + vm  |  | - OIDC token source (OAuth2   |  |
|  |             |  | health, DD-010)|  |   client-credentials, issuer   |  |
|  +------+------+  +----------------+  |   discovery + token endpoint) |  |
|         |                             | - gRPC ClientConn + auth      |  |
|  +------+------+                      |   interceptor (bearer token)  |  |
|  | Registrar   |                      +---------------+----------------+  |
|  | (control-   |                                      |                   |
|  |  plane      |                                      |                   |
|  |  client)    |                                      |                   |
|  +-------------+                                      |                   |
+--------------------------------------------------------+-----------------+
                                                          |
                                              +-----------+-----------+
                                              |   OSAC Fulfillment    |
                                              |   Service (gRPC) +    |
                                              |   Keycloak (OIDC)     |
                                              +-----------------------+
```

---

## 3. Topic Dependency Graph

| # | Topic                              | Prefix | Depends On |
|---|-------------------------------------|--------|------------|
| 1 | HTTP Server                         | HTTP   | -          |
| 2 | OSAC Client Bootstrap               | OSAC   | -          |
| 3 | Health Service                      | HLT    | 1, 2       |
| 4 | SP Registration (`control-plane`)   | REG    | 1          |

```
Topic 1: HTTP Server              (independent)
Topic 2: OSAC Client Bootstrap    (independent)
  |         |
  |         +---> Topic 3: Health Service               (depends on 1, 2)
  +---> Topic 4: SP Registration (`control-plane`)       (depends on 1)
```

Topics 1 and 2 can be delivered in parallel. Topics 3 and 4 depend on their
respective prerequisites.

> **Note:** Health handler tests mock the OSAC client bootstrap interface;
> OSAC client bootstrap tests use a fake/mock Keycloak token endpoint and a
> local in-process gRPC server (`google.golang.org/grpc/test/bufconn`).

---

## 4. Topic Specifications

### 4.1 HTTP Server

#### Overview

Foundation layer: HTTP server with graceful shutdown, signal handling,
configuration loading from environment variables. Route registration for
this milestone is limited to the health endpoint; later milestones add
cluster/VM routes generated from the OpenAPI spec.

Out of scope: TLS termination (handled by infrastructure/ingress),
authentication/authorization middleware on the DCM-facing API, rate limiting.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-HTTP-010 | The SP MUST start an HTTP server on the configured address | MUST | |
| REQ-HTTP-020 | The SP MUST register `GET /api/v1alpha1/clusters/health` | MUST | DD-010 |
| REQ-HTTP-025 | The SP MUST register `GET /api/v1alpha1/vms/health` | MUST | DD-010 |
| REQ-HTTP-030 | The SP MUST initiate graceful shutdown on SIGTERM: stop new connections, drain in-flight requests within configured timeout, exit cleanly | MUST | |
| REQ-HTTP-040 | The SP MUST initiate graceful shutdown on SIGINT, behaving identically to REQ-HTTP-030 | MUST | |
| REQ-HTTP-050 | The SP MUST load configuration values from environment variables | MUST | |
| REQ-HTTP-060 | The SP MUST log each HTTP request at INFO level including method, path, response status code, and duration | MUST | |
| REQ-HTTP-070 | The SP MUST catch panics in HTTP handlers and return an RFC 9457 error response with `type=https://dcm-project.github.io/problems/internal`. Recovery middleware MUST be applied as the outermost middleware layer | MUST | DD-070 |
| REQ-HTTP-080 | The SP MUST log server lifecycle events including listen address on startup | MUST | |
| REQ-HTTP-090 | The SP SHOULD enforce a configurable per-request timeout, cancelling the request context after the deadline | SHOULD | |

#### Configuration Introduced

| Config Key | Env Var | Default | Description |
|------------|---------|---------|-------------|
| server.address | SP_SERVER_ADDRESS | :8080 | Listen address (host:port) |
| server.shutdownTimeout | SP_SERVER_SHUTDOWN_TIMEOUT | 15s | Graceful shutdown drain timeout |
| server.requestTimeout | SP_SERVER_REQUEST_TIMEOUT | 30s | Per-request context timeout |

#### Acceptance Criteria

##### AC-HTTP-010: Server starts on configured address

- **Validates:** REQ-HTTP-010
- **Given** valid configuration is provided
- **When** the SP starts
- **Then** the HTTP server MUST begin listening on the configured address

##### AC-HTTP-020: Cluster health route registration

- **Validates:** REQ-HTTP-020
- **Given** the HTTP server has started
- **When** a GET request is made to `/api/v1alpha1/clusters/health`
- **Then** the request MUST be routed to the health handler

##### AC-HTTP-025: VM health route registration

- **Validates:** REQ-HTTP-025
- **Given** the HTTP server has started
- **When** a GET request is made to `/api/v1alpha1/vms/health`
- **Then** the request MUST be routed to the health handler

##### AC-HTTP-030: Graceful shutdown on SIGTERM

- **Validates:** REQ-HTTP-030
- **Given** the HTTP server is running
- **When** SIGTERM is received
- **Then** the server MUST stop accepting new connections
- **And** the server MUST drain in-flight requests within the configured shutdown timeout
- **And** the server MUST exit cleanly after draining or timeout

##### AC-HTTP-040: Graceful shutdown on SIGINT

- **Validates:** REQ-HTTP-040
- **Given** the HTTP server is running
- **When** SIGINT is received
- **Then** the server MUST behave identically to REQ-HTTP-030

##### AC-HTTP-050: Configuration from environment variables

- **Validates:** REQ-HTTP-050
- **Given** environment variables are set (e.g., SP_SERVER_ADDRESS=:9090)
- **When** the SP starts
- **Then** the SP MUST use the values from the environment variables

##### AC-HTTP-060: Request logging

- **Validates:** REQ-HTTP-060
- **Given** any HTTP request is processed
- **When** the response is sent
- **Then** the SP MUST log at INFO level with method, path, status code, and duration

##### AC-HTTP-070: Panic recovery

- **Validates:** REQ-HTTP-070
- **Given** a handler panics during request processing
- **When** the panic is caught
- **Then** the response MUST be HTTP 500 with RFC 9457 body (`type=https://dcm-project.github.io/problems/internal`)
- **And** the panic and stack trace MUST be logged at ERROR level

##### AC-HTTP-080: Lifecycle logging

- **Validates:** REQ-HTTP-080
- **Given** the SP starts or stops
- **When** the server begins listening or initiates shutdown
- **Then** the SP MUST log the event including the listen address on startup

##### AC-HTTP-090: Request timeout

- **Validates:** REQ-HTTP-090
- **Given** a configurable request timeout is set (default 30s)
- **When** a request exceeds the timeout
- **Then** the request context MUST be cancelled

#### Dependencies

None - independently deliverable.

---

### 4.2 OSAC Client Bootstrap

#### Overview

Establishes the SP's connection to OSAC: an OIDC client-credentials token
source against OSAC's Keycloak, and a gRPC `ClientConn` to the fulfillment
service with a per-RPC bearer-token interceptor. `oidcIssuerUrl` is an OIDC
**issuer** identifier, not a token endpoint — the actual token endpoint MUST
be resolved via standard OIDC discovery before any token request is made
(see DD-060). This is the minimum needed to back a real health check in this
milestone. Full generated CRUD stubs
(`Clusters`, `ComputeInstances`, `Subnets`, `VirtualNetworks`) are generated
in Milestone 2 via a `buf`/`protoc` pipeline against
[`osac-project/fulfillment-service`](https://github.com/osac-project/fulfillment-service)'s
public protos — see DD-020 for why this milestone only generates the minimal
`Capabilities` client instead of the full set.

Out of scope: token exchange (RFC 8693, confirmed unsupported by OSAC),
per-tenant credentials (v1 is single shared service account), retry/circuit
breaking beyond token refresh and connection backoff.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-OSAC-010 | The SP MUST obtain an OIDC access token via OAuth2 client-credentials grant against the token endpoint resolved per REQ-OSAC-011/REQ-OSAC-012, using `oidcClientId`/`oidcClientSecret` | MUST | DD-060 |
| REQ-OSAC-011 | The SP MUST resolve the token endpoint from `oidcIssuerUrl` by trying `GET {oidcIssuerUrl}/.well-known/oauth-authorization-server` (RFC 8414) first and, only if that fails, falling back to `GET {oidcIssuerUrl}/.well-known/openid-configuration` (OpenID Connect Discovery 1.0) | MUST | DD-060 |
| REQ-OSAC-012 | The SP MUST extract `token_endpoint` from whichever discovery document succeeds, rather than treating `oidcIssuerUrl` itself as the token endpoint or querying only one of the two documents | MUST | DD-060 |
| REQ-OSAC-020 | The SP MUST refresh the OIDC token before expiry and supply it as a gRPC bearer credential (`PerRPCCredentials`) on every call to the fulfillment service | MUST | |
| REQ-OSAC-030 | The SP MUST establish a gRPC `ClientConn` to `fulfillmentAddress` | MUST | |
| REQ-OSAC-040 | When `tlsEnabled=true`, the gRPC connection MUST use TLS, loading a CA certificate from `tlsCertFile` if set | MUST | |
| REQ-OSAC-050 | When `tlsEnabled=false` (default), the gRPC connection MUST use insecure transport credentials | MUST | |
| REQ-OSAC-060 | OIDC discovery failures and token fetch failures MUST be retried with exponential backoff and MUST NOT crash the SP or block server startup | MUST | |
| REQ-OSAC-070 | The bootstrap component MUST expose a query method reporting current OIDC token validity (has a non-expired cached token) for use by the health handler | MUST | |
| REQ-OSAC-080 | The bootstrap component MUST expose a query method reporting gRPC connectivity to the fulfillment service, using a lightweight, unauthenticated probe (`osac.public.v1.Capabilities/Get`) with a short timeout | MUST | DD-020 |
| REQ-OSAC-090 | The connectivity probe (REQ-OSAC-080) MUST NOT be invoked more often than once per health check request and MUST NOT itself force an OIDC token refresh | MUST | |

#### Configuration Introduced

| Config Key | Env Var | Default | Required | Description |
|------------|---------|---------|----------|-------------|
| osac.fulfillmentAddress | SP_OSAC_FULFILLMENT_ADDRESS | - | Yes | OSAC fulfillment service gRPC address |
| osac.oidcIssuerUrl | SP_OSAC_OIDC_ISSUER_URL | - | Yes | Keycloak OIDC issuer URL |
| osac.oidcClientId | SP_OSAC_OIDC_CLIENT_ID | - | Yes | OAuth 2.0 client ID registered in Keycloak |
| osac.oidcClientSecret | SP_OSAC_OIDC_CLIENT_SECRET | - | Yes | OAuth 2.0 client secret |
| osac.tlsEnabled | SP_OSAC_TLS_ENABLED | false | No | Enable TLS for fulfillment service connection |
| osac.tlsCertFile | SP_OSAC_TLS_CERT_FILE | - | No | Path to TLS CA certificate file |
| osac.probeTimeout | SP_OSAC_PROBE_TIMEOUT | 5s | No | Timeout for the health-check connectivity probe |

#### Acceptance Criteria

##### AC-OSAC-010: OIDC token fetch on startup

- **Validates:** REQ-OSAC-010
- **Given** valid OIDC issuer URL, client ID, and client secret
- **When** the SP starts
- **Then** the bootstrap component MUST first discover the token endpoint (AC-OSAC-011), then obtain an access token from it

##### AC-OSAC-011: Token endpoint discovered from issuer, not assumed

- **Validates:** REQ-OSAC-011, REQ-OSAC-012
- **Given** `oidcIssuerUrl=https://keycloak.example.com/realms/osac` and a discovery document at `{oidcIssuerUrl}/.well-known/oauth-authorization-server` whose `token_endpoint` is `https://keycloak.example.com/realms/osac/protocol/openid-connect/token`
- **When** the bootstrap component fetches a token
- **Then** the token request MUST be sent to the discovered `token_endpoint`, not to `oidcIssuerUrl` directly
- **And** `{oidcIssuerUrl}/.well-known/openid-configuration` MUST NOT be queried, since the first (RFC 8414) attempt already succeeded

##### AC-OSAC-012: Discovery failure is retried, non-fatal

- **Validates:** REQ-OSAC-011, REQ-OSAC-060
- **Given** both `{oidcIssuerUrl}/.well-known/oauth-authorization-server` and `{oidcIssuerUrl}/.well-known/openid-configuration` are unreachable or return a non-2xx status
- **When** the SP starts
- **Then** the SP MUST continue starting the HTTP server
- **And** discovery MUST be retried with exponential backoff in the background, using the same backoff sequence as token fetch retries (AC-OSAC-060)

##### AC-OSAC-013: Falls back to OpenID Connect discovery when RFC 8414 discovery fails

- **Validates:** REQ-OSAC-011, REQ-OSAC-012
- **Given** `{oidcIssuerUrl}/.well-known/oauth-authorization-server` returns a non-2xx status (e.g. `404`, matching a Keycloak realm that doesn't expose that document) but `{oidcIssuerUrl}/.well-known/openid-configuration` returns a valid discovery document
- **When** the bootstrap component fetches a token
- **Then** the SP MUST fall back to querying `{oidcIssuerUrl}/.well-known/openid-configuration` and use its `token_endpoint`
- **And** the token request MUST succeed using that fallback-discovered endpoint

##### AC-OSAC-020: Token refresh before expiry

- **Validates:** REQ-OSAC-020
- **Given** a cached token is within its configured refresh margin of expiring
- **When** the next gRPC call is made (or the refresh timer fires)
- **Then** a new token MUST be fetched and used for subsequent calls
- **And** the bearer token attached to the gRPC call MUST equal the current cached token's value

##### AC-OSAC-030: gRPC connection established

- **Validates:** REQ-OSAC-030
- **Given** a valid `fulfillmentAddress`
- **When** the SP starts
- **Then** a gRPC `ClientConn` MUST be created targeting that address

##### AC-OSAC-040: TLS enabled

- **Validates:** REQ-OSAC-040
- **Given** `osac.tlsEnabled=true` and a valid `tlsCertFile`
- **When** the gRPC connection is created
- **Then** the connection MUST use TLS transport credentials loaded from the CA file

##### AC-OSAC-050: TLS disabled (default)

- **Validates:** REQ-OSAC-050
- **Given** `osac.tlsEnabled=false`
- **When** the gRPC connection is created
- **Then** the connection MUST use insecure transport credentials

##### AC-OSAC-060: Token fetch retry, non-fatal

- **Validates:** REQ-OSAC-060
- **Given** the discovered Keycloak token endpoint is unreachable (discovery itself having already succeeded)
- **When** the SP starts
- **Then** the SP MUST continue starting the HTTP server
- **And** token fetch MUST be retried with exponential backoff in the background

##### AC-OSAC-070: Token validity query — valid

- **Validates:** REQ-OSAC-070
- **Given** a non-expired token is cached
- **When** the token-validity query is called
- **Then** it MUST report valid=true with the token's expiry time

##### AC-OSAC-071: Token validity query — invalid/absent

- **Validates:** REQ-OSAC-070
- **Given** no token has been successfully obtained yet
- **When** the token-validity query is called
- **Then** it MUST report valid=false

##### AC-OSAC-080: Connectivity probe — success

- **Validates:** REQ-OSAC-080
- **Given** the OSAC fulfillment service is reachable
- **When** the connectivity probe is invoked
- **Then** `osac.public.v1.Capabilities/Get` MUST be called and succeed within `osac.probeTimeout`
- **And** the probe MUST report connected=true

##### AC-OSAC-081: Connectivity probe — failure

- **Validates:** REQ-OSAC-080
- **Given** the OSAC fulfillment service is unreachable or the probe times out
- **When** the connectivity probe is invoked
- **Then** the probe MUST report connected=false with the underlying error recorded

##### AC-OSAC-090: Probe does not force token refresh

- **Validates:** REQ-OSAC-090
- **Given** the connectivity probe is invoked (Capabilities/Get requires no auth per the OSAC proto contract)
- **When** the probe executes
- **Then** it MUST NOT trigger an OIDC token fetch as a side effect

#### Dependencies

None - independently deliverable.

---

### 4.3 Health Service

#### Overview

Implementation of `GET /api/v1alpha1/clusters/health` and
`GET /api/v1alpha1/vms/health`, reporting real OSAC connectivity and OIDC
token health — not a stub that always reports healthy. **Two paths, not
one:** `control-plane`'s `internal/sp/healthcheck.Monitor` health-checks
each registered provider row independently by polling
`GET {provider.Endpoint}/health` on an interval (verified directly in its
implementation, not just inferred from a spec — see DD-010); since Topic 4.4
registers `cluster` at `{provider.endpoint}/api/v1alpha1/clusters` and `vm`
at `{provider.endpoint}/api/v1alpha1/vms` (REQ-REG-030), this yields
`/api/v1alpha1/clusters/health` and `/api/v1alpha1/vms/health` polled
independently. Both paths report identical status: this SP's health (OIDC
token validity + OSAC gRPC connectivity) is a single, global condition, not
per-service-type. `control-plane`'s monitor derives its own three-state model
(Ready, Unhealthy, Unavailable) from this SP's two-state
`{"status": "healthy"|"unhealthy"}` response body plus HTTP-level
reachability (non-2xx/timeout escalates to `Unavailable` after
`MaxConsecutiveFailures`) — this SP only needs to produce the two-state body
correctly; the three-state derivation is `control-plane`'s responsibility.

Out of scope: readiness vs. liveness distinction, hub-availability reporting
(`At least one hub is registered` — deferred until cluster/VM milestones
where hub-related failures are actually observable).

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-HLT-010 | The SP MUST expose `GET /api/v1alpha1/clusters/health` and `GET /api/v1alpha1/vms/health`, each returning HTTP 200 OK | MUST | DD-010 |
| REQ-HLT-015 | Both health endpoints MUST report identical status, derived from the same underlying OIDC/OSAC health check (this SP has one global health condition, not one per service type) | MUST | |
| REQ-HLT-020 | Each health response body MUST include `status`, `type`, `path`, `version`, and `uptime` fields | MUST | DD-030 |
| REQ-HLT-030 | `status` MUST be `"healthy"` only when both the cached OIDC token is valid (REQ-OSAC-070) AND the OSAC connectivity probe succeeds (REQ-OSAC-080); otherwise `"unhealthy"` | MUST | |
| REQ-HLT-040 | The response MUST set `Content-Type: application/json` | MUST | |
| REQ-HLT-050 | The health handler MUST NOT force a new OIDC token fetch on every poll | MUST | |
| REQ-HLT-055 | The health handler MUST query the cached OIDC token validity (REQ-OSAC-070) on every poll | MUST | |
| REQ-HLT-060 | The health handler MUST invoke the connectivity probe (REQ-OSAC-080) on every call, bounded by `osac.probeTimeout` | MUST | |
| REQ-HLT-070 | When unhealthy, the response body SHOULD include a `detail` field distinguishing "token invalid" from "OSAC unreachable" (both may be true simultaneously) | SHOULD | |

#### Acceptance Criteria

##### AC-HLT-010: Health endpoint availability

- **Validates:** REQ-HLT-010
- **Given** the HTTP server is running
- **When** a GET request is made to `/api/v1alpha1/clusters/health` or to `/api/v1alpha1/vms/health`
- **Then** the SP MUST return HTTP 200 OK from either path

##### AC-HLT-011: Both health paths report identical status

- **Validates:** REQ-HLT-015
- **Given** a fixed OIDC token validity and OSAC connectivity state
- **When** `/api/v1alpha1/clusters/health` and `/api/v1alpha1/vms/health` are each called
- **Then** both responses MUST report the same `status` value

##### AC-HLT-020: Health response body — healthy

- **Validates:** REQ-HLT-020, REQ-HLT-030
- **Given** a valid cached OIDC token and a reachable OSAC fulfillment service
- **When** GET `/api/v1alpha1/clusters/health` (or `/api/v1alpha1/vms/health`) is called
- **Then** the response body MUST contain:
  - `status`: `"healthy"`
  - `type`: `"osac-service-provider.dcm.io/health"`
  - `path`: `"health"`
  - `version`: SP build version (string)
  - `uptime`: seconds since SP started (integer)

##### AC-HLT-025: Health response body — unhealthy, token invalid

- **Validates:** REQ-HLT-030
- **Given** no valid cached OIDC token (e.g., Keycloak unreachable at startup)
- **And** the OSAC fulfillment service is reachable
- **When** GET `/api/v1alpha1/clusters/health` (or `/api/v1alpha1/vms/health`) is called
- **Then** the response MUST be HTTP 200 OK with `status: "unhealthy"`

##### AC-HLT-026: Health response body — unhealthy, OSAC unreachable

- **Validates:** REQ-HLT-030
- **Given** a valid cached OIDC token
- **And** the OSAC fulfillment service is unreachable
- **When** GET `/api/v1alpha1/clusters/health` (or `/api/v1alpha1/vms/health`) is called
- **Then** the response MUST be HTTP 200 OK with `status: "unhealthy"`

##### AC-HLT-030: Health response content type

- **Validates:** REQ-HLT-040
- **Given** any call to either health endpoint
- **When** the response is returned
- **Then** the `Content-Type` header MUST be `application/json`

##### AC-HLT-040: No forced token refresh on poll

- **Validates:** REQ-HLT-050, REQ-HLT-055
- **Given** a valid cached token exists
- **When** either health endpoint is called repeatedly
- **Then** the OIDC token endpoint MUST NOT be called as part of serving the health request

##### AC-HLT-050: Connectivity probe invoked per request

- **Validates:** REQ-HLT-060
- **Given** either health endpoint is called
- **When** the request is processed
- **Then** the OSAC connectivity probe MUST be invoked and its result MUST determine part of the reported status

##### AC-HLT-060: Unhealthy detail distinguishes cause

- **Validates:** REQ-HLT-070
- **Given** the cached token is invalid but OSAC is reachable
- **When** GET `/api/v1alpha1/clusters/health` (or `/api/v1alpha1/vms/health`) is called
- **Then** the `detail` field, if present, MUST reference token/authentication rather than connectivity

#### Dependencies

Depends on Topic 1 (HTTP Server) and Topic 2 (OSAC Client Bootstrap).

---

### 4.4 SP Registration (`environment-agent`, Phase 2 — DD-203)

#### Overview

**Status: draft, in progress (issue [#33](https://github.com/dcm-project/osac-service-provider/issues/33)) — this section describes the target design, not yet the state of `main`.** Self-register with
`dcm-project/environment-agent`'s SP API on startup, using **two** distinct
provider names — `osac-sp-cluster` (service_type `cluster`) and `osac-sp-vm`
(service_type `vm`) — following the same `name`-as-natural-key idempotency
structure as the enhancement's
[Registration Flow](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#registration-flow).
This supersedes DD-050's Phase 1 target (`control-plane`), forced back to
the originally-planned `environment-agent` destination by
[`control-plane#51`](https://github.com/dcm-project/control-plane/pull/51)
deleting `control-plane`'s SP registration API outright in favor of an
agent-routed model — see DD-203. The two registrations are independent:
neither's success or failure affects the other. Unlike `control-plane`,
`environment-agent` enforces **per-service-type exclusivity** — only one
SP (embedded or external) may serve a given `service_type` per agent — so a
`409` on `POST /providers` signals that scenario (another SP currently holds
the slot), not a name/ID data conflict, and is retried rather than treated
as fatal (REQ-REG-090).

Out of scope: de-registration on shutdown, registration status surfaced in
the health check (deferred — see Topic 4.3 out-of-scope note).

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-REG-010 | The SP MUST register with `environment-agent` on startup via two independent calls: one for `service_type=cluster` (name `osac-sp-cluster`), one for `service_type=vm` (name `osac-sp-vm`) | MUST | DD-203 |
| REQ-REG-020 | Each registration payload MUST include `name`, `service_type`, `endpoint`, and `schema_version` | MUST | |
| REQ-REG-030 | The cluster registration `endpoint` MUST be `{provider.endpoint}/api/v1alpha1/clusters`; the vm registration `endpoint` MUST be `{provider.endpoint}/api/v1alpha1/vms` | MUST | |
| REQ-REG-040 | The cluster registration payload MUST advertise `supported_platforms=["baremetal"]`, `supported_provisioning_types=["hypershift"]`, and a hardcoded `kubernetes_supported_versions` list, carried as additional keys inside the `metadata` object | MUST | DD-203 — `environment-agent`'s `Provider`/`ProviderMetadata` resource has no top-level fields for these either; carried via its `additionalProperties` catch-all shape alongside its own known fields (`region_code`/`zone`/`status`/`resources`) |
| REQ-REG-050 | Both registrations MUST execute asynchronously and MUST NOT block server startup | MUST | |
| REQ-REG-052 | The internal readiness self-probe that gates when registration starts (REQ-REG-050) MUST keep retrying if a single probing window elapses without a successful response, rather than permanently abandoning registration; it MUST give up only when the server's shutdown context is cancelled | MUST | DD-141 |
| REQ-REG-060 | The two registrations MUST be independent: a failure (including non-retryable 4xx) on one MUST NOT stop or delay the other | MUST | |
| REQ-REG-070 | Registration MUST retry with exponential backoff on retryable failures (connection errors, 5xx) | MUST | |
| REQ-REG-080 | A `409 Conflict` response (service type already served by another provider — `environment-agent`'s per-service-type exclusivity) MUST NOT be treated as fatal: it MUST be logged at WARN level and retried on the same cadence as periodic re-registration (REQ-REG-100), not exponential backoff, so this SP can acquire the slot later if the incumbent is displaced | MUST | DD-203 — restores the pre-Phase-1 design DD-050 had replaced; see REQ-REG-090's "not `409`" carve-out |
| REQ-REG-090 | Non-retryable 4xx responses **other than `409 Conflict`** (see REQ-REG-080) MUST stop retries for that registration immediately and be logged at ERROR level | MUST | DD-203 |
| REQ-REG-100 | Registration MUST be idempotent: periodic re-registration updates/refreshes the entry (by `name`) rather than duplicating it. `environment-agent`'s OpenAPI describes a "200: lease renewal" response, but no lease/TTL enforcement was found in its current `internal/provider/service` implementation for external providers — periodic re-registration is therefore not proven necessary to retain the slot today, but remains valuable both to keep capability metadata (REQ-REG-040) fresh and as the retry cadence for REQ-REG-080 | MUST | DD-203 |
| REQ-REG-115 | Registration requests MUST NOT set an `Authorization` header or any bearer credential. `environment-agent`'s API declares its `401` response as "reserved; authentication deferred to future version" — unauthenticated is the currently correct, documented behavior, not merely an unenforced no-op as it was under `control-plane` (DD-050's Authentication Gap) | MUST | DD-203 |

#### Configuration Introduced

| Config Key | Env Var | Default | Required | Description |
|------------|---------|---------|----------|-------------|
| dcm.registrationUrl | DCM_REGISTRATION_URL | - | Yes | `environment-agent` base URL (e.g. `https://environment-agent.example.com/api/v1alpha1`), passed to the generated client's `NewClient`. Env var name is unchanged from Phase 1's `control-plane` target (DD-203) — it already described "the DCM-side registration endpoint" generically, not `control-plane` by name, so this is a config *value* change for operators, not a schema change |
| provider.endpoint | SP_ENDPOINT | - | Yes | Externally reachable base URL for this SP |
| provider.clusterName | SP_PROVIDER_CLUSTER_NAME | osac-sp-cluster | No | Registered name for the `cluster` service type |
| provider.vmName | SP_PROVIDER_VM_NAME | osac-sp-vm | No | Registered name for the `vm` service type |

#### Acceptance Criteria

##### AC-REG-010: Dual registration on startup

- **Validates:** REQ-REG-010
- **Given** the SP starts with valid registration configuration
- **When** the HTTP server is ready
- **Then** two registration requests MUST be sent: one with `service_type=cluster` and name `osac-sp-cluster`, one with `service_type=vm` and name `osac-sp-vm`

##### AC-REG-020: Cluster registration payload

- **Validates:** REQ-REG-020, REQ-REG-030, REQ-REG-040
- **Given** a cluster registration request is sent
- **When** the payload is constructed
- **Then** it MUST include:
  - `name`: `"osac-sp-cluster"`
  - `service_type`: `"cluster"`
  - `endpoint`: `"{provider.endpoint}/api/v1alpha1/clusters"`
  - `schema_version`: `"v1alpha1"`
  - `metadata.supported_platforms`: `["baremetal"]`
  - `metadata.supported_provisioning_types`: `["hypershift"]`
  - `metadata.kubernetes_supported_versions`: non-empty list

##### AC-REG-021: VM registration payload

- **Validates:** REQ-REG-020, REQ-REG-030
- **Given** a VM registration request is sent
- **When** the payload is constructed
- **Then** it MUST include:
  - `name`: `"osac-sp-vm"`
  - `service_type`: `"vm"`
  - `endpoint`: `"{provider.endpoint}/api/v1alpha1/vms"`
  - `schema_version`: `"v1alpha1"`

##### AC-REG-030: Non-blocking registration

- **Validates:** REQ-REG-050
- **Given** the HTTP server has started
- **When** registration is initiated
- **Then** the server MUST already be accepting HTTP requests
- **And** both registrations MUST run concurrently with request handling

##### AC-REG-031: Resilient readiness gating

- **Validates:** REQ-REG-052
- **Given** the HTTP server is slow to confirm its own readiness (e.g. transient CPU contention during pod startup) and one internal probing window elapses without a successful response
- **When** the shutdown context has not been cancelled
- **Then** the self-probe MUST retry a fresh window rather than giving up
- **And** registration MUST still start once the server does confirm readiness, however many windows that takes
- **And** the self-probe MUST permanently stop only if the shutdown context is cancelled before readiness is ever confirmed

##### AC-REG-040: Independent registration outcomes

- **Validates:** REQ-REG-060
- **Given** the `vm` registration fails immediately (e.g., non-retryable 4xx)
- **When** the failure occurs
- **Then** the `cluster` registration MUST proceed unaffected and MUST succeed if `environment-agent` accepts it

##### AC-REG-050: Exponential backoff on retryable failure

- **Validates:** REQ-REG-070
- **Given** `control-plane` is unreachable
- **When** a registration attempt fails
- **Then** the SP MUST retry with exponential backoff

##### AC-REG-060: 409 Conflict is retried on the re-registration cadence, not treated as fatal (restores the pre-Phase-1 design; DD-203 supersedes DD-050 here)

- **Validates:** REQ-REG-080
- **Given** a registration receives `409 Conflict` from `environment-agent` (service type already served by another provider)
- **When** the response is handled
- **Then** the SP MUST log at WARN level and retry that registration on the same cadence as periodic re-registration (not exponential backoff)
- **And** MUST NOT stop that registration's loop, and MUST NOT let it affect the other service type's registration

##### AC-REG-070: Non-retryable 4xx (other than 409) stops retries

- **Validates:** REQ-REG-090
- **Given** a registration receives a non-retryable 4xx response other than `409 Conflict` (e.g., 400 Bad Request, 422 Unprocessable Entity)
- **When** the response is handled
- **Then** the SP MUST NOT retry that registration
- **And** MUST log the error at ERROR level
- **And** MUST continue running and serving requests

##### AC-REG-080: Idempotent re-registration

- **Validates:** REQ-REG-100
- **Given** the SP was previously registered for both service types
- **When** the SP restarts and re-registers
- **Then** the existing registrations MUST be updated (not duplicated)

##### AC-REG-095: No authentication on registration requests

- **Validates:** REQ-REG-115
- **Given** a registration request is constructed
- **When** it is sent to `environment-agent`
- **Then** the request MUST NOT carry an `Authorization` header or any bearer token

#### Dependencies

Depends on Topic 1 (HTTP Server).

---

## 5. Cross-Cutting Concerns

### 5.1 Logging

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-XC-LOG-010 | Structured logging (`log/slog`) MUST be used throughout the application | MUST | |
| REQ-XC-LOG-020 | Log levels MUST follow the defined convention: ERROR (unrecoverable failures), WARN (recoverable issues, e.g. registration retries and 409 conflicts), INFO (lifecycle events), DEBUG (detailed data) | MUST | |

#### Acceptance Criteria

##### AC-XC-LOG-010: Structured logging

- **Validates:** REQ-XC-LOG-010
- **Given** any operation occurs in the SP
- **When** the operation is logged
- **Then** the log output MUST use structured logging format

##### AC-XC-LOG-020: Log level usage

- **Validates:** REQ-XC-LOG-020
- **Given** different types of events occur (e.g., a token refresh failure vs. a 409 on `vm` registration)
- **When** they are logged
- **Then** ERROR, WARN, INFO, and DEBUG levels MUST be used according to the defined convention

### 5.2 Configuration Management

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-XC-CFG-010 | All configuration MUST be loadable from environment variables | MUST | |
| REQ-XC-CFG-020 | The SP MUST fail fast on startup when required configuration values are absent or empty, returning an error before starting any subsystem | MUST | |

#### Acceptance Criteria

##### AC-XC-CFG-010: Environment variable configuration

- **Validates:** REQ-XC-CFG-010
- **Given** any configuration value
- **When** the corresponding environment variable is set
- **Then** the SP MUST use the value from the environment variable

##### AC-XC-CFG-020: Fail-fast on missing required config

- **Validates:** REQ-XC-CFG-020
- **Given** a required config value (`SP_OSAC_FULFILLMENT_ADDRESS`, `SP_OSAC_OIDC_ISSUER_URL`, `SP_OSAC_OIDC_CLIENT_ID`, `SP_OSAC_OIDC_CLIENT_SECRET`, `DCM_REGISTRATION_URL`, or `SP_ENDPOINT`) is absent or empty
- **When** the SP starts
- **Then** the SP MUST return an error identifying the missing field
- **And** MUST exit before starting the HTTP server or any subsystem

---

## 6. Consolidated Configuration Reference

All configuration is loaded from environment variables.

| Config Key | Env Var | Default | Required | Topic |
|------------|---------|---------|----------|-------|
| server.address | SP_SERVER_ADDRESS | :8080 | No | 1 |
| server.shutdownTimeout | SP_SERVER_SHUTDOWN_TIMEOUT | 15s | No | 1 |
| server.requestTimeout | SP_SERVER_REQUEST_TIMEOUT | 30s | No | 1 |
| osac.fulfillmentAddress | SP_OSAC_FULFILLMENT_ADDRESS | - | Yes | 2 |
| osac.oidcIssuerUrl | SP_OSAC_OIDC_ISSUER_URL | - | Yes | 2 |
| osac.oidcClientId | SP_OSAC_OIDC_CLIENT_ID | - | Yes | 2 |
| osac.oidcClientSecret | SP_OSAC_OIDC_CLIENT_SECRET | - | Yes | 2 |
| osac.tlsEnabled | SP_OSAC_TLS_ENABLED | false | No | 2 |
| osac.tlsCertFile | SP_OSAC_TLS_CERT_FILE | - | No | 2 |
| osac.probeTimeout | SP_OSAC_PROBE_TIMEOUT | 5s | No | 2 |
| dcm.registrationUrl | DCM_REGISTRATION_URL | - | Yes | 4 |
| provider.endpoint | SP_ENDPOINT | - | Yes | 4 |
| provider.clusterName | SP_PROVIDER_CLUSTER_NAME | osac-sp-cluster | No | 4 |
| provider.vmName | SP_PROVIDER_VM_NAME | osac-sp-vm | No | 4 |

---

## 7. Design Decisions

Design decisions (`DD-NNN`) referenced throughout this spec have moved to
their own living document:
[`.ai/decisions/osac-sp.decisions.md`](../decisions/osac-sp.decisions.md).
Keeping decisions there — separate from milestone specs — lets the record
grow across milestones without being tied to any one spec's lifecycle, and
lets reviewers keep it open in a second tab alongside the spec.

---

## 8. Spec Clarifications

### SC-001: `kubernetes_supported_versions` content for Milestone 1

**Related requirements:** REQ-REG-040

The full version-translation compatibility matrix (mapping DCM K8s versions
to OSAC `release_image` values) is Milestone 6 scope per issue #1. For this
milestone, `kubernetes_supported_versions` MUST be a non-empty, hardcoded
placeholder list (e.g., the versions already known to be supported by OSAC's
available cluster templates at implementation time) — it does not need to be
sourced from the full matrix yet, since no cluster-create endpoint consumes
it in this milestone.

**Superseded by Milestone 6** (`internal/versionmatrix` —
see `osac-sp-m6-version-matrix.spec.md` REQ-VERSION-050): once that
milestone lands, `kubernetes_supported_versions` is derived directly from
the shared matrix's own keys (`matrix.SupportedVersions()`), not a
separately hand-maintained list — this SC's placeholder-list allowance no
longer applies from that point forward.

### SC-002: Unhealthy status is never an HTTP error

**Related requirements:** REQ-HLT-010, REQ-HLT-030

Per the sibling SPs' established pattern (DD-070 in
`k8s-container-service-provider`), an `"unhealthy"` status is still returned
as **HTTP 200 OK** with the unhealthy body — it is not a 5xx response.
`control-plane`'s health-check monitor (`internal/sp/healthcheck.Monitor`,
see DD-010/DD-050) distinguishes healthy/unhealthy (read from this SP's JSON
body) from unavailable (non-2xx/timeout at the transport level, escalated
after `MaxConsecutiveFailures`) — the same two-level distinction this
sentence originally attributed to `environment-agent`, now confirmed against
`control-plane`'s actual implementation.

---

## 9. Requirement ID Index

| Prefix | Topic | Count |
|--------|-------|-------|
| REQ-HTTP-NNN | 4.1: HTTP Server | 10 |
| REQ-OSAC-NNN | 4.2: OSAC Client Bootstrap | 11 |
| REQ-HLT-NNN | 4.3: Health Service | 9 |
| REQ-REG-NNN | 4.4: SP Registration (`environment-agent`, Phase 2 — DD-203) | 11 |
| REQ-XC-LOG-NNN | 5.1: Logging | 2 |
| REQ-XC-CFG-NNN | 5.2: Configuration Management | 2 |
| **Total** | | **45** |

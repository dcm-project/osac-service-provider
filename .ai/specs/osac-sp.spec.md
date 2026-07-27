# Specification: OSAC Service Provider — Milestone 1 (Scaffold + Registration + Health)

## 1. Overview

The OSAC Service Provider (OSAC SP) is a DCM Service Provider that integrates
the Open Sovereign AI Cloud (OSAC) platform with DCM through the environment
agent model, provisioning OpenShift clusters and VMs via OSAC's fulfillment
service gRPC API.

**This spec covers Milestone 1 only** — the first independently reviewable
slice per [issue #1](https://github.com/dcm-project/osac-service-provider/issues/1)'s
suggested delivery milestones: repo skeleton, environment-agent registration
(two service types), and a health endpoint backed by real OSAC connectivity
and OIDC token validity. **No cluster or VM CRUD endpoints are in scope for
this spec** — those land in later milestones (2–6) with their own spec
additions.

**Version scope (Milestone 1):**

- HTTP server foundation (chi-based), following the same middleware chain
  pattern as sibling SPs (recovery → request logging → request timeout)
- OIDC client-credentials bootstrap against OSAC's Keycloak and a gRPC
  connection to OSAC's fulfillment service, sufficient to back a real health
  check (full CRUD-service gRPC stub generation is Milestone 2)
- `GET /api/v1alpha1/health` reporting real OSAC connectivity + OIDC token
  health (not a stub that always reports healthy)
- Two-name registration with the environment agent (`osac-sp-cluster` /
  `osac-sp-vm`), each independently retried
- No cluster/VM REST endpoints, no gRPC CRUD calls, no status polling, no
  CloudEvents publishing — all deferred to later milestones

**Reference documents:**

- [OSAC SP Enhancement](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md) (source of truth for design decisions)
- [Implementation plan (issue #1)](https://github.com/dcm-project/osac-service-provider/issues/1)
- [SP Registration Flow](https://github.com/dcm-project/enhancements/blob/main/enhancements/sp-registration-flow/sp-registration-flow.md)
- [Environment Agent Enhancement](https://github.com/dcm-project/enhancements/blob/main/enhancements/environment-agent/environment-agent.md)
- [SP Health Check](https://github.com/dcm-project/enhancements/blob/main/enhancements/service-provider-health-check/service-provider-health-check.md)
- OSAC public protos: [`osac-project/fulfillment-service/proto/public/osac/public/v1/`](https://github.com/osac-project/fulfillment-service/tree/main/proto/public/osac/public/v1)
- Reference implementations (structure/conventions template): [`k8s-container-service-provider`](https://github.com/dcm-project/k8s-container-service-provider), [`acm-cluster-service-provider`](https://github.com/dcm-project/acm-cluster-service-provider), [`kubevirt-service-provider`](https://github.com/dcm-project/kubevirt-service-provider) — **note:** these register directly against `control-plane`'s (formerly `service-provider-manager`'s) SP API, not against `environment-agent`; OSAC SP does not follow their registration wiring (see DD-050)
- [`dcm-project/environment-agent`](https://github.com/dcm-project/environment-agent) (`api/v1alpha1/openapi.yaml`) — authoritative registration contract this SP integrates with (see DD-050)
- OpenAPI Spec: `api/v1alpha1/openapi.yaml` (source of truth for the API contract, once authored)

---

## 2. Architecture

```
                                     +------------------+
                                     |   Environment    |
                                     |      Agent       |
                                     +--------+---------+
                                              |
                          +-------------------+-------------------+
                          ^                                       |
                          |                                       v
                   Registration (x2)                        Health Poll
              POST /api/v1alpha1/providers                  GET /health
                          |                                       |
+-------------------------+---------------------------------------+--------+
|                       OSAC Service Provider                              |
|                                                                          |
|  +-------------+  +----------------+  +------------------------------+  |
|  | HTTP Server |--| Health Handler |--| OSAC Client Bootstrap         |  |
|  | (chi)       |  | (/health)      |  | - OIDC token source (OAuth2   |  |
|  +------+------+  +----------------+  |   client-credentials)         |  |
|         |                             | - gRPC ClientConn + auth      |  |
|  +------+------+                      |   interceptor (bearer token)  |  |
|  | Registrar   |                      +---------------+----------------+  |
|  | (env-agent  |                                      |                   |
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
| 4 | Environment Agent Registration      | REG    | 1          |

```
Topic 1: HTTP Server              (independent)
Topic 2: OSAC Client Bootstrap    (independent)
  |         |
  |         +---> Topic 3: Health Service       (depends on 1, 2)
  +---> Topic 4: Environment Agent Registration  (depends on 1)
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

Foundation layer: chi-based HTTP server with graceful shutdown, signal
handling, configuration loading from environment variables. Route
registration for this milestone is limited to the health endpoint; later
milestones add cluster/VM routes generated from the OpenAPI spec.

Out of scope: TLS termination (handled by infrastructure/ingress),
authentication/authorization middleware on the DCM-facing API, rate limiting.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-HTTP-010 | The SP MUST start an HTTP server on the configured address | MUST | |
| REQ-HTTP-020 | The SP MUST register `GET /api/v1alpha1/health` | MUST | DD-010 |
| REQ-HTTP-030 | The SP MUST initiate graceful shutdown on SIGTERM: stop new connections, drain in-flight requests within configured timeout, exit cleanly | MUST | |
| REQ-HTTP-040 | The SP MUST initiate graceful shutdown on SIGINT, behaving identically to REQ-HTTP-030 | MUST | |
| REQ-HTTP-050 | The SP MUST load configuration values from environment variables | MUST | |
| REQ-HTTP-060 | The SP MUST log each HTTP request at INFO level including method, path, response status code, and duration | MUST | |
| REQ-HTTP-070 | The SP MUST catch panics in HTTP handlers and return an RFC 7807 INTERNAL error response. Recovery middleware MUST be applied as the outermost middleware layer | MUST | |
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

##### AC-HTTP-020: Health route registration

- **Validates:** REQ-HTTP-020
- **Given** the HTTP server has started
- **When** a GET request is made to `/api/v1alpha1/health`
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
- **Then** the response MUST be HTTP 500 with RFC 7807 body (type=INTERNAL)
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
service with a per-RPC bearer-token interceptor. This is the minimum needed
to back a real health check in this milestone. Full generated CRUD stubs
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
| REQ-OSAC-010 | The SP MUST obtain an OIDC access token via OAuth2 client-credentials grant against the configured `oidcIssuerUrl`, using `oidcClientId`/`oidcClientSecret` | MUST | |
| REQ-OSAC-020 | The SP MUST refresh the OIDC token before expiry and supply it as a gRPC bearer credential (`PerRPCCredentials`) on every call to the fulfillment service | MUST | |
| REQ-OSAC-030 | The SP MUST establish a gRPC `ClientConn` to `fulfillmentAddress` | MUST | |
| REQ-OSAC-040 | When `tlsEnabled=true`, the gRPC connection MUST use TLS, loading a CA certificate from `tlsCertFile` if set | MUST | |
| REQ-OSAC-050 | When `tlsEnabled=false` (default), the gRPC connection MUST use insecure transport credentials | MUST | |
| REQ-OSAC-060 | OIDC token fetch failures MUST be retried with exponential backoff and MUST NOT crash the SP or block server startup | MUST | |
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
- **Then** the bootstrap component MUST obtain an access token from Keycloak

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
- **Given** the Keycloak token endpoint is unreachable
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

Implementation of `GET /api/v1alpha1/health`, reporting real OSAC
connectivity and OIDC token health — not a stub that always reports healthy.
Polled by the environment agent per the three-state health model (Ready,
Unhealthy, Unavailable).

Out of scope: readiness vs. liveness distinction, hub-availability reporting
(`At least one hub is registered` — deferred until cluster/VM milestones
where hub-related failures are actually observable).

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-HLT-010 | The SP MUST expose `GET /api/v1alpha1/health` and return HTTP 200 OK | MUST | |
| REQ-HLT-020 | The health response body MUST include `status`, `type`, `path`, `version`, and `uptime` fields | MUST | DD-030 |
| REQ-HLT-030 | `status` MUST be `"healthy"` only when both the cached OIDC token is valid (REQ-OSAC-070) AND the OSAC connectivity probe succeeds (REQ-OSAC-080); otherwise `"unhealthy"` | MUST | |
| REQ-HLT-040 | The response MUST set `Content-Type: application/json` | MUST | |
| REQ-HLT-050 | The health handler MUST NOT force a new OIDC token fetch on every poll; it MUST query cached token validity (REQ-OSAC-070) | MUST | |
| REQ-HLT-060 | The health handler MUST invoke the connectivity probe (REQ-OSAC-080) on every call, bounded by `osac.probeTimeout` | MUST | |
| REQ-HLT-070 | When unhealthy, the response body SHOULD include a `detail` field distinguishing "token invalid" from "OSAC unreachable" (both may be true simultaneously) | SHOULD | |

#### Acceptance Criteria

##### AC-HLT-010: Health endpoint availability

- **Validates:** REQ-HLT-010
- **Given** the HTTP server is running
- **When** a GET request is made to `/api/v1alpha1/health`
- **Then** the SP MUST return HTTP 200 OK

##### AC-HLT-020: Health response body — healthy

- **Validates:** REQ-HLT-020, REQ-HLT-030
- **Given** a valid cached OIDC token and a reachable OSAC fulfillment service
- **When** GET `/api/v1alpha1/health` is called
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
- **When** GET `/api/v1alpha1/health` is called
- **Then** the response MUST be HTTP 200 OK with `status: "unhealthy"`

##### AC-HLT-026: Health response body — unhealthy, OSAC unreachable

- **Validates:** REQ-HLT-030
- **Given** a valid cached OIDC token
- **And** the OSAC fulfillment service is unreachable
- **When** GET `/api/v1alpha1/health` is called
- **Then** the response MUST be HTTP 200 OK with `status: "unhealthy"`

##### AC-HLT-030: Health response content type

- **Validates:** REQ-HLT-040
- **Given** any call to the health endpoint
- **When** the response is returned
- **Then** the `Content-Type` header MUST be `application/json`

##### AC-HLT-040: No forced token refresh on poll

- **Validates:** REQ-HLT-050
- **Given** a valid cached token exists
- **When** the health endpoint is called repeatedly
- **Then** the OIDC token endpoint MUST NOT be called as part of serving the health request

##### AC-HLT-050: Connectivity probe invoked per request

- **Validates:** REQ-HLT-060
- **Given** the health endpoint is called
- **When** the request is processed
- **Then** the OSAC connectivity probe MUST be invoked and its result MUST determine part of the reported status

##### AC-HLT-060: Unhealthy detail distinguishes cause

- **Validates:** REQ-HLT-070
- **Given** the cached token is invalid but OSAC is reachable
- **When** GET `/api/v1alpha1/health` is called
- **Then** the `detail` field, if present, MUST reference token/authentication rather than connectivity

#### Dependencies

Depends on Topic 1 (HTTP Server) and Topic 2 (OSAC Client Bootstrap).

---

### 4.4 Environment Agent Registration

#### Overview

Self-register with the environment agent on startup, using **two** distinct
provider names — `osac-sp-cluster` (service_type `cluster`) and
`osac-sp-vm` (service_type `vm`) — per the enhancement's
[Registration Flow](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#registration-flow).
The two registrations are independent: neither's success or failure affects
the other, since `vm` may legitimately be rejected with `409 Conflict` if
another SP (e.g., `kubevirt-service-provider`) already holds that service
type slot.

Out of scope: de-registration on shutdown, registration status surfaced in
the health check (deferred — see Topic 4.3 out-of-scope note).

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-REG-010 | The SP MUST register with the environment agent on startup via two independent calls: one for `service_type=cluster` (name `osac-sp-cluster`), one for `service_type=vm` (name `osac-sp-vm`) | MUST | |
| REQ-REG-020 | Each registration payload MUST include `name`, `service_type`, `endpoint`, and `schema_version` | MUST | |
| REQ-REG-030 | The cluster registration `endpoint` MUST be `{provider.endpoint}/api/v1alpha1/clusters`; the vm registration `endpoint` MUST be `{provider.endpoint}/api/v1alpha1/vms` | MUST | |
| REQ-REG-040 | The cluster registration payload MUST advertise `supported_platforms=["baremetal"]`, `supported_provisioning_types=["hypershift"]`, and a hardcoded `kubernetes_supported_versions` list, carried as additional keys inside the `metadata` object (the environment agent's `Provider` resource has no top-level fields for these) | MUST | DD-050 |
| REQ-REG-050 | Both registrations MUST execute asynchronously and MUST NOT block server startup | MUST | |
| REQ-REG-060 | The two registrations MUST be independent: a failure (including non-retryable 4xx) on one MUST NOT stop or delay the other | MUST | |
| REQ-REG-070 | Registration MUST retry with exponential backoff on retryable failures (connection errors, 5xx) | MUST | |
| REQ-REG-080 | A `409 Conflict` response on the `vm` registration MUST be treated as retryable — logged, not fatal — and retried on the same periodic cadence as lease renewal, so the SP can acquire the slot later if the incumbent's lease expires | MUST | |
| REQ-REG-090 | Non-retryable 4xx responses (other than `409` on `vm`, per REQ-REG-080) MUST stop retries for that registration immediately and be logged | MUST | |
| REQ-REG-100 | Registration MUST be idempotent: periodic re-registration renews the lease rather than duplicating the entry | MUST | |
| REQ-REG-110 | The SP MUST use the `environment-agent` project's generated client library (`github.com/dcm-project/environment-agent/pkg/client`) for registration, depended on via a pinned Go module commit SHA (pseudo-version) rather than a tagged release, since none exist yet | MUST | DD-050 |
| REQ-REG-115 | Registration requests MUST NOT set an `Authorization` header or any bearer credential; the environment agent's registration endpoint does not require authentication in its current contract | MUST | DD-050 |

#### Configuration Introduced

| Config Key | Env Var | Default | Required | Description |
|------------|---------|---------|----------|-------------|
| agent.registrationUrl | SP_AGENT_REGISTRATION_URL | - | Yes | Environment agent base URL (e.g. `https://agent.example.com/api/v1alpha1`) passed to the generated client's `NewClient` |
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

##### AC-REG-040: Independent registration outcomes

- **Validates:** REQ-REG-060
- **Given** the `vm` registration fails immediately (e.g., non-retryable 4xx other than 409)
- **When** the failure occurs
- **Then** the `cluster` registration MUST proceed unaffected and MUST succeed if the agent accepts it

##### AC-REG-050: Exponential backoff on retryable failure

- **Validates:** REQ-REG-070
- **Given** the environment agent is unreachable
- **When** a registration attempt fails
- **Then** the SP MUST retry with exponential backoff

##### AC-REG-060: VM registration 409 is non-fatal and retried

- **Validates:** REQ-REG-080
- **Given** the `vm` registration receives `409 Conflict` (another SP holds the slot)
- **When** the response is handled
- **Then** the SP MUST log the conflict at INFO/WARN level (not ERROR)
- **And** MUST NOT mark the SP as failed
- **And** MUST retry the `vm` registration on the same cadence as lease renewal

##### AC-REG-070: Non-retryable 4xx stops retries

- **Validates:** REQ-REG-090
- **Given** a registration receives a non-retryable 4xx response (e.g., 400 Bad Request)
- **When** the response is handled
- **Then** the SP MUST NOT retry that registration
- **And** MUST log the error at ERROR level
- **And** MUST continue running and serving requests

##### AC-REG-080: Idempotent re-registration

- **Validates:** REQ-REG-100
- **Given** the SP was previously registered for both service types
- **When** the SP restarts and re-registers
- **Then** the existing registrations MUST be updated (not duplicated)

##### AC-REG-090: Registration client library

- **Validates:** REQ-REG-110
- **Given** the registration subsystem is implemented
- **When** a registration request is sent
- **Then** it MUST use `github.com/dcm-project/environment-agent/pkg/client`, imported via a `go.mod` entry pinned to a specific commit SHA (pseudo-version)

##### AC-REG-095: No authentication on registration requests

- **Validates:** REQ-REG-115
- **Given** a registration request is constructed
- **When** it is sent to the environment agent
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
| REQ-XC-CFG-010 | All configuration MUST be loadable from environment variables via `caarlos0/env` | MUST | |
| REQ-XC-CFG-020 | The SP MUST fail fast on startup when required configuration values are absent or empty, returning an error before starting any subsystem | MUST | |

#### Acceptance Criteria

##### AC-XC-CFG-010: Environment variable configuration

- **Validates:** REQ-XC-CFG-010
- **Given** any configuration value
- **When** the corresponding environment variable is set
- **Then** the SP MUST use the value from the environment variable

##### AC-XC-CFG-020: Fail-fast on missing required config

- **Validates:** REQ-XC-CFG-020
- **Given** a required config value (`SP_OSAC_FULFILLMENT_ADDRESS`, `SP_OSAC_OIDC_ISSUER_URL`, `SP_OSAC_OIDC_CLIENT_ID`, `SP_OSAC_OIDC_CLIENT_SECRET`, `SP_AGENT_REGISTRATION_URL`, or `SP_ENDPOINT`) is absent or empty
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
| agent.registrationUrl | SP_AGENT_REGISTRATION_URL | - | Yes | 4 |
| provider.endpoint | SP_ENDPOINT | - | Yes | 4 |
| provider.clusterName | SP_PROVIDER_CLUSTER_NAME | osac-sp-cluster | No | 4 |
| provider.vmName | SP_PROVIDER_VM_NAME | osac-sp-vm | No | 4 |

---

## 7. Design Decisions

### DD-010: Health endpoint path scoped to `/api/v1alpha1/health`

**Decision:** Serve health only at the resource-relative path
`/api/v1alpha1/health` in this milestone (not also at root `/health`).

**Rationale:** Per issue #1's REST API contract table, `/api/v1alpha1/health`
is the SP's advertised health path. Sibling repos additionally serve a root
`/health` for infrastructure readiness probing; revisit adding that alongside
this path once a Containerfile/Deployment manifest exists for this repo
(tracked as a follow-up, not blocking this milestone).

**Related requirements:** REQ-HTTP-020, REQ-HLT-010

### DD-020: Minimal `Capabilities`-only gRPC client for Milestone 1

**Decision:** This milestone generates (via `buf`) only the
`osac.public.v1.Capabilities` client — the smallest, explicitly
unauthenticated proto service — to back the connectivity probe. The full
`Clusters`/`ComputeInstances`/`Subnets`/`VirtualNetworks` stub generation
pipeline is Milestone 2's deliverable per issue #1's suggested milestones.

**Rationale:** Issue #1 places "gRPC client generation" in Milestone 2, but
also requires Milestone 1's health check to report "real OSAC gRPC
connectivity" — these two constraints are reconciled by scoping Milestone
1's codegen to the one service that (a) requires no auth per its own proto
comment ("This endpoint does not require authentication") and (b) is
purpose-built for capability/connectivity discovery, making it accidentally
the ideal minimal probe target. The `buf.gen.yaml`/`buf.yaml` files
introduced here are extended, not replaced, in Milestone 2.

**Related requirements:** REQ-OSAC-080, REQ-OSAC-090

### DD-030: Health response schema

**Decision:** Health response uses `status: "healthy"` or `status:
"unhealthy"` with fields `type`, `path`, `version`, `uptime`, matching the
schema convention already used by sibling SPs' health endpoints, plus an
optional `detail` field for this SP's two-part health condition (token +
connectivity).

**Rationale:** Consistency with the DCM three-state health model
([enhancements#47](https://github.com/dcm-project/enhancements/pull/47)) and
with how sibling SPs shape their health payloads, while accommodating that
this SP's health depends on two independent external systems (Keycloak,
OSAC fulfillment service) rather than one (a local Kubernetes API server).

**Related requirements:** REQ-HLT-020, REQ-HLT-030, REQ-HLT-070

### DD-040: Registration retry independence for `cluster` vs. `vm`

**Decision:** Implement the two registrations as fully independent
goroutines/retry loops, not a single loop iterating over two service types.

**Rationale:** The enhancement doc is explicit that a `vm` registration
`409 Conflict` (another SP already holds the slot) must not be fatal to the
process and must keep retrying on the lease-renewal cadence, while `cluster`
registration must proceed and succeed independently. Sharing a retry loop
would risk one service type's backoff/failure state leaking into the other.

**Related requirements:** REQ-REG-060, REQ-REG-080

### DD-050: Register with `environment-agent`'s pre-release API and client, not `control-plane`

**Decision:** OSAC SP registers with the standalone
[`dcm-project/environment-agent`](https://github.com/dcm-project/environment-agent)
service (`POST /api/v1alpha1/providers`), using its generated
`github.com/dcm-project/environment-agent/pkg/client`, pinned by commit SHA
(no tagged releases exist). This is a different service and wire contract
than `control-plane`'s `api/sp/v1alpha1/provider` (the successor to the now
archived `service-provider-manager`, which all 5 existing sibling SPs still
use directly).

**Rationale:** OSAC SP's own enhancement doc and issue #1 describe
registering with "the environment agent," and `environment-agent`'s README
confirms it is purpose-built for exactly this — "External SPs: Standalone SP
processes that register to the agent via the REST API." The team confirmed
this direction during the enhancement review, explicitly choosing it over
`control-plane`'s direct-registration path. No existing SP repo actually
depends on `environment-agent` today: `k8s-container`, `acm-cluster`, and
`kubevirt` register directly with `control-plane`/the archived SPM, and
`environment-agent`'s own spec only lists them as candidates for future
in-process embedding (not yet implemented). OSAC SP will be the first SP to
exercise this integration path in either direction.

**Maturity risk (accepted, tracked):** As of this writing, `environment-agent`
is 18 days old, has no tagged releases, and its `POST /api/v1alpha1/providers`
handler exists only as generated stubs (`server.gen.go`) — there is no
`internal/handlers`/`internal/service` implementation yet, and `main()` is a
no-op. Concretely, this means:
- The Go dependency MUST be pinned to a specific commit SHA and bumped
  deliberately, not tracked via `@latest` or a floating branch.
- Milestone 1's registration integration tests (Topic 4, `osac-sp-integration.test-plan.md`
  §3) exercise a **fake HTTP server implementing the current OpenAPI
  contract**, not a live `environment-agent` instance — this was already the
  planned test design and needs no rework.
- The OpenAPI contract (`api/v1alpha1/openapi.yaml`, merged on `main`) could
  still change before `environment-agent`'s own registration handler lands,
  since nothing about it is released or frozen. Re-validating OSAC SP's
  registration assumptions (schema fields, 409-conflict semantics,
  idempotency-on-`name`) against a real running `environment-agent` is a
  tracked follow-up once that work ships there — not a Milestone 1 blocker.
- No authentication is required on this call today (`401` is explicitly
  "reserved; authentication deferred to future version" in
  `environment-agent`'s spec) — unlike `control-plane`'s equivalent API,
  which requires a bearer JWT. See REQ-REG-115.

**Schema consequence:** `environment-agent`'s generated `Provider` struct has
no `supported_platforms`/`supported_provisioning_types`/
`kubernetes_supported_versions` fields — these OSAC-specific values MUST be
carried as additional keys inside `metadata` (`ProviderMetadata`'s
`additionalProperties: true` catch-all, which flattens to sibling JSON keys
alongside `region_code`/`zone`/`status`/`resources` on marshal). See
REQ-REG-040.

**Related requirements:** REQ-REG-040, REQ-REG-110, REQ-REG-115

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

### SC-002: Unhealthy status is never an HTTP error

**Related requirements:** REQ-HLT-010, REQ-HLT-030

Per the sibling SPs' established pattern (DD-070 in
`k8s-container-service-provider`), an `"unhealthy"` status is still returned
as **HTTP 200 OK** with the unhealthy body — it is not a 5xx response. The
environment agent's polling logic distinguishes healthy/unhealthy from
unavailable (non-200/timeout) at the transport level.

---

## 9. Requirement ID Index

| Prefix | Topic | Count |
|--------|-------|-------|
| REQ-HTTP-NNN | 4.1: HTTP Server | 9 |
| REQ-OSAC-NNN | 4.2: OSAC Client Bootstrap | 9 |
| REQ-HLT-NNN | 4.3: Health Service | 7 |
| REQ-REG-NNN | 4.4: Environment Agent Registration | 12 |
| REQ-XC-LOG-NNN | 5.1: Logging | 2 |
| REQ-XC-CFG-NNN | 5.2: Configuration Management | 2 |
| **Total** | | **41** |

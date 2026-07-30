# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

DCM (Data Center Management) service provider that integrates the
[Open Sovereign AI Cloud (OSAC)](https://github.com/osac-project/) platform
with DCM. It provisions OpenShift clusters and VMs by translating
agent-routed requests into OSAC fulfillment service gRPC API calls.

**Module:** `github.com/dcm-project/osac-service-provider`

**Authoritative design:** the
[OSAC Service Provider enhancement](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md).
If an implementation decision conflicts with the enhancement doc, open a PR
against the enhancement first.

## DCM Ecosystem

Like its sibling service providers, this SP registers with **`control-plane`'s
SP API** (`dcm-project/control-plane`, `pkg/sp/client/provider`) — this is a
**Phase 1** decision, not the original one: the enhancement's initial design
targeted the standalone **environment agent** (`dcm-project/environment-agent`)
instead, but the team pivoted to `control-plane` for the first release on
delivery-risk/maturity grounds (environment-agent's registration handler is
still unimplemented). See `DD-050` in `.ai/specs/osac-sp.spec.md` for the full
rationale and
[enhancements#95](https://github.com/dcm-project/enhancements/issues/95) for
the tracked enhancement-doc update (two-phase delivery: `control-plane` now,
`environment-agent` later once mature). `control-plane` also has no tagged
releases yet, hence the same commit-SHA pin in `go.mod`.

| Component | Interaction | Config |
|---|---|---|
| [control-plane](https://github.com/dcm-project/control-plane) | Registers two independent entries on startup — one `cluster` service type, one `vm` service type — using its generated Go client library (`pkg/sp/client/provider`). Periodically re-registers to refresh capability metadata (no lease/TTL to renew, unlike environment-agent's model). | `DCM_REGISTRATION_URL` |
| [osac-project/fulfillment-service](https://github.com/osac-project/fulfillment-service) | gRPC API for cluster/VM CRUD. OAuth2/OIDC client-credentials auth against OSAC's Keycloak. | `SP_OSAC_FULFILLMENT_ADDRESS`, `SP_OSAC_OIDC_*` |

**Milestone 1 scope** (this repo's current state): scaffold, HTTP server,
health check, and registration only — no cluster/VM CRUD endpoints yet. See
issue [#1](https://github.com/dcm-project/osac-service-provider/issues/1)
for the full milestone breakdown.

## Commands

```bash
make build               # Build binary to bin/osac-service-provider
make test                # Run all tests (Ginkgo v2, race detector)
make test-cover          # Run tests with coverage
make lint                # Run golangci-lint
make check               # fmt + vet + lint + test
make generate-api        # Regenerate code from api/v1alpha1/openapi.yaml (oapi-codegen)
make check-generate-api  # Verify generated OpenAPI code is up to date (CI check)
make generate-proto      # Regenerate OSAC gRPC client from proto/ (buf; requires buf CLI)
make check-generate-proto # Verify generated proto code is up to date (CI check)
make generate            # generate-api + generate-proto
make check-aep           # Validate OpenAPI spec against AEP standards (requires spectral)
```

### Running specific tests

```bash
# Single package
go run github.com/onsi/ginkgo/v2/ginkgo -r internal/registration

# Single test by name/TC-ID
go run github.com/onsi/ginkgo/v2/ginkgo -r -v -focus "TC-U-050" internal/registration
```

## API Endpoints

Milestone 1 defines only:

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1alpha1/clusters/health` | Health check for the `cluster` provider registration. Reflects real OSAC gRPC connectivity and OIDC token validity — `status` in the body (not the HTTP code) indicates health. |
| `GET` | `/api/v1alpha1/vms/health` | Health check for the `vm` provider registration. Reports identical status to the endpoint above — this SP has one global health condition, not one per service type. |

There are two health endpoints (not one) because `control-plane`'s
`healthcheck.Monitor` health-checks each independently-registered provider at
`{provider.endpoint}/health` (see DD-010 in `.ai/specs/osac-sp.spec.md`).

All error responses use RFC 9457 Problem Details (`application/problem+json`).

## Architecture

### OpenAPI-first code generation

`api/v1alpha1/openapi.yaml` is the source of truth for the REST API. Run
`make generate-api` after editing it; CI enforces the generated code is up to
date.

Generated files (do not edit manually):
- `api/v1alpha1/types.gen.go` — data models
- `api/v1alpha1/spec.gen.go` — embedded OpenAPI spec
- `internal/api/server/server.gen.go` — Chi router + strict server interface
- `pkg/client/client.gen.go` — HTTP client (unused by this repo itself in M1; generated for consistency with sibling SPs and for future consumers)

### Proto-first gRPC client generation (DD-020)

`proto/osac/public/v1/*.proto` are **vendored** (copied, not a live `buf`
dependency — see `proto/README.md`) from `osac-project/fulfillment-service`,
since that repo's BSR module isn't published yet. Only the `Capabilities`
service is vendored/generated in Milestone 1 (used for the health check's
connectivity probe); the full CRUD services (`Clusters`, `ComputeInstances`,
...) are added in Milestone 2. Run `make generate-proto` after editing
`proto/`, `buf.yaml`, or `buf.gen.yaml`.

Generated files (do not edit manually):
- `internal/osacpb/osac/public/v1/*.pb.go` — protobuf messages + gRPC client

### Request flow

`cmd/osac-service-provider/main.go` wires everything together:
1. `internal/config` loads and validates env vars (fails fast on missing required values).
2. `internal/osac.Bootstrap` starts an async OIDC token fetch/refresh loop and creates a lazy gRPC `ClientConn` to OSAC's fulfillment service.
3. `internal/apiserver.Server` starts the HTTP server (middleware: Recovery → Request Logging → Request Timeout) serving `internal/health.Handler`, which implements `StrictServerInterface` and queries the bootstrap's cached token/probe state.
4. Once the HTTP server confirms it is accepting connections (self-probed via its own `WithOnReady` hook), `internal/registration.Registrar` starts two independent, indefinitely-retrying registration loops (cluster, vm) against `control-plane`.

### Internal packages

| Package | Purpose |
|---|---|
| `internal/apiserver/` | HTTP server setup, middleware chain (recovery, logging, timeout), readiness probing |
| `internal/config/` | Environment variable parsing via `caarlos0/env`. Prefixes: `SP_SERVER_*`, `SP_OSAC_*`, `DCM_*`, `SP_*` (provider identity) |
| `internal/osac/` | `Bootstrap` — OIDC client-credentials token source + gRPC `ClientConn` to the fulfillment service; exposes `TokenStatus()` and `Probe()` for the health check |
| `internal/health/` | Health check handler implementing `StrictServerInterface` |
| `internal/registration/` | `Registrar` — async self-registration with `control-plane` (two independent service types: cluster, vm); exponential backoff on retryable failures, immediate stop on non-retryable 4xx (including 409), periodic re-registration to refresh capability metadata on success |
| `internal/httperror/` | RFC 9457 `application/problem+json` response writing |
| `internal/util/` | Generic helpers (e.g., `Ptr[T]`) |
| `internal/osacpb/` | **Generated** — OSAC `Capabilities` gRPC client (DD-020) |
| `internal/api/server/` | **Generated** — Chi router and `StrictServerInterface` |

### Key patterns

- **Strict server interface**: oapi-codegen generates a `StrictServerInterface` with typed request/response objects. Handlers implement this interface — no manual HTTP parsing.
- **RFC 9457 errors**: all error responses use Problem Details format (RFC 9457 obsoletes RFC 7807). `type` values are project-controlled URIs under `https://dcm-project.github.io/problems/*` (e.g. `.../internal`, `.../invalid-argument`), matching the ecosystem-wide RFC 9457 migration ([FLPATH-4719](https://redhat.atlassian.net/browse/FLPATH-4719), see DD-070 in `.ai/decisions/osac-sp.decisions.md`) — not the bare short codes (`INTERNAL`, `INVALID_ARGUMENT`) this repo originally used.
- **Fail-fast config**: `internal/config.Load()` returns an error immediately if any required env var is missing/empty, before any subsystem starts.
- **Non-blocking bootstrap**: neither the OIDC token loop nor gRPC dial ever block HTTP server startup or crash the process on failure — both retry indefinitely with backoff.
- **Independent registration loops**: cluster and vm registration succeed/fail/retry completely independently of each other.
- **Idempotent registration**: successful registrations are periodically renewed (re-POSTed) rather than sent once; `control-plane`'s contract treats this as idempotent on `name`, with no lease/TTL to expire.

## Testing

- **Framework**: Ginkgo v2 (BDD) + Gomega assertions
- **Test naming**: files use `_unit_test.go` / `_integration_test.go` suffixes. Test cases carry `TC-U-XXX` / `TC-I-XXX` identifiers.
- **Mocks**: hand-written fakes (e.g., a fake `oauth2.TokenSource`, a fake `publicv1.CapabilitiesClient`, a fake `http.RoundTripper` for the `control-plane` client) — no mocking framework.
- **Spec first**: new requirements (REQ-*) and acceptance criteria (AC-*) MUST be added to `.ai/specs/osac-sp.spec.md` before any implementation or test planning begins.
- **Test plan first**: new test cases (TC-*) MUST be registered in `.ai/test-plans/` with mappings to REQ-*/AC-* before being implemented in code.

## .ai/ conventions

```
.ai/
├── specs/              # Specifications with REQ-* and AC-* (git-tracked)
├── test-plans/         # Test plans with TC-* IDs (git-tracked)
├── decisions/          # Trust-boundary and design decision logs (local only)
├── plans/              # Implementation plans (local only)
├── checkpoints/        # Session state snapshots (local only)
├── exploration/        # Codebase analysis and research (local only)
└── reviews/            # Code review findings (local only)
```

Only `specs/` and `test-plans/` are committed to git. All other subdirectories
are gitignored and remain local.

**Gate enforcement**: spec (REQ + AC) must be complete before test plan (TC);
test plan must be complete before implementation.

## Linting

golangci-lint excludes generated code directories (`api/v1alpha1/`,
`pkg/client/`). See `.golangci.yml` for enabled linters. `internal/osacpb/`
(generated proto) is not yet excluded — if golangci-lint starts flagging it,
add it to `.golangci.yml`'s exclusion list alongside the OpenAPI-generated
directories.

## Commit format

```
<type>(<scope>): <subject>
```

Use `git commit -s` to add sign-off. Types: `feat`, `fix`, `refactor`, `test`, `docs`, `chore`.

**Peer review**: all changes that address a requirement land via PR, never
pushed directly to `main` — see `.github/CODEOWNERS`.

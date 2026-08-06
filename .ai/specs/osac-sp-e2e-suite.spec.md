# Specification: kind-based e2e CI — real `control-plane` + real `osac-sp` + `osac-mock-provider`

## 1. Overview

Phase 2 of [osac-service-provider#17](https://github.com/dcm-project/osac-service-provider/issues/17)
(FLPATH-4759): a `kind` cluster, built and torn down inside a GitHub Actions
job owned by this repo, running real `control-plane` (pulled as a published
upstream image + chart), real `osac-sp` (built from this repo), and
`osac-mock-provider` (Phase 1, [#18](https://github.com/dcm-project/osac-service-provider/pull/18)) —
proving the `osac-sp` ↔ `control-plane` contract round-trips against a real,
independently-built control-plane, not `wiremock`/bufconn fakes of it.

This closes the specific gap [control-plane#40](https://github.com/dcm-project/control-plane/issues/40)
identified: no SP repo has any e2e tier that runs its own binary against a
real `control-plane`.

**Scope boundary (unchanged from issue #17):** this is SP-repo-owned e2e with
a *mocked provider backend only*. It validates the `osac-sp` ↔
`control-plane` contract, not real-OSAC integration — that heavier tier
(`FLPATH-4760`, OCP + real `fulfillment-service`) is `control-plane`'s /
QE's, tracked separately, and gated on Milestone 4/5 landing.

**What's asserted in this phase, concretely:** registration success,
health-check propagation, Cluster and VM CRUD dispatch (Milestone 3/4,
REQ-E2E-090/100), and — as of REQ-E2E-110 below — version-matrix
enforcement over real HTTP (Milestone 6). NATS status-event round-trips
(Milestone 5) are **still not yet assertable**, since that milestone's own
async publish path has no `osac-sp`-side REST surface to assert against
over HTTP (§6 continues to track it as an explicit follow-up — Milestone 5
was, however, included in the throwaway combined-branch validation run
below, proving it doesn't crash-loop or otherwise destabilize the stack
this suite's other assertions depend on; see DD-146 for the
`DCM_NATS_URL` manifest gap that run surfaced and fixed).

**Note on Milestone 3/4/6 sequencing:** REQ-E2E-090/100's test cases were
validated against a throwaway local branch merging this branch with
[#13](https://github.com/dcm-project/osac-service-provider/pull/13)/[#14](https://github.com/dcm-project/osac-service-provider/pull/14)
before being added here (evidence: the passing run linked from the PR that
introduced these requirements) — `osac-sp`'s own REST CRUD handlers aren't
on `main` yet, so this branch's *own* CI cannot exercise them until those
two PRs merge. REQ-E2E-110/111 (Milestone 6, version matrix) follow the
same pattern: validated against a second throwaway branch additionally
combining [#26](https://github.com/dcm-project/osac-service-provider/pull/26)
(M6, which itself already contains M3) and
[#25](https://github.com/dcm-project/osac-service-provider/pull/25) (M5) —
see DD-146 for the passing run. The requirements/test cases are specified
now regardless, per this repo's spec-first convention, rather than waiting
for the review backlog to clear.

**Reference documents:**

- [osac-service-provider#17](https://github.com/dcm-project/osac-service-provider/issues/17) —
  architecture, ownership, GitHub Actions job outline (this spec implements
  its "GitHub Actions job outline" section)
- [`osac-sp-e2e-mock-provider.spec.md`](./osac-sp-e2e-mock-provider.spec.md) —
  Phase 1, the binary this phase deploys as the faked OSAC backend
- `dcm-project/control-plane`'s [`deploy/helm/dcm/`](https://github.com/dcm-project/control-plane/tree/main/deploy/helm/dcm) —
  the chart this phase sparse-checks-out and installs (verified against the
  chart's `values.yaml`/`templates/` on `main` at spec-writing time: chart
  name `dcm`, `controlPlane`/`dcmUi`/`postgres`/`nats` sections, per-SP
  sections for the 4 SPs it already knows about — `osac-sp` is deliberately
  not one of them yet, see REQ-E2E-030)
- `internal/config/config.go` — the exact env-var contract this phase's
  `osac-sp` manifest must satisfy (`SP_SERVER_*`, `SP_OSAC_*`, `DCM_*`, `SP_*`)
- `internal/mockprovider/config.go` — the exact env-var contract this phase's
  `osac-mock-provider` manifest must satisfy (`MOCK_GRPC_ADDRESS`,
  `MOCK_OIDC_ADDRESS`)
- [`osac-sp-m6-version-matrix.spec.md`](./osac-sp-m6-version-matrix.spec.md) —
  Milestone 6, the source of REQ-E2E-110/111's underlying behavior
  (REQ-VERSION-080)
- [Design Decisions](../decisions/osac-sp.decisions.md) — new decisions for
  this work continue from wherever Phase 1's `DD-13x` left off on this branch

---

## 2. Architecture

```
kind cluster (GH-hosted ubuntu-latest runner: 4 vCPU / 16 GB RAM)
├── dcm-postgres              (control-plane's own chart, StatefulSet+PVC)
├── dcm-nats                  (control-plane's own chart, StatefulSet+PVC)
├── dcm-control-plane         (control-plane's own chart; PULLED image quay.io/dcm-project/control-plane:main)
├── osac-service-provider     (this repo's own manifest; BUILT image, kind-loaded)
└── osac-mock-provider        (this repo's own manifest; BUILT image, kind-loaded)
```

- `control-plane` + Postgres + NATS come from `control-plane`'s own
  `deploy/helm/dcm/` chart (Helm), sparse-checked-out at a pinned ref —
  **not built from source**, mirroring how a real deployment consumes
  `control-plane` (issue #17, "Ownership & artifact sourcing"). Its `Route`/
  `Ingress`/`dcmUi` resources are disabled via `--set` overrides (REQ-E2E-020)
  since `kind` has neither OpenShift's `Route` CRD nor any need for the UI in
  an assertions-only e2e run.
- `osac-service-provider` and `osac-mock-provider` are **this repo's own
  plain Kubernetes manifests** (`Deployment`+`Service`, no Helm) — issue #17
  explicitly scopes them as "this repo's own manifests," and `osac-sp` isn't
  in the upstream chart's `values.yaml` yet (REQ-E2E-030).
- Both this repo's images are built locally in the job and `kind load
  docker-image`d — no registry push needed for a PR-triggered run.

### Component wiring (env vars → Service DNS names)

Given Helm release name `dcm` (so `{{ dcm.fullname }}` resolves to `dcm`,
confirmed against `_helpers.tpl`'s `contains $chartName $releaseName` branch):

| Component | Env var | Value |
|---|---|---|
| `osac-service-provider` | `DCM_REGISTRATION_URL` | `http://dcm-control-plane:8080/api/v1alpha1` |
| `osac-service-provider` | `DCM_NATS_URL` | `nats://dcm-nats:4222` (unused until Milestone 5 merges, see DD-146) |
| `osac-service-provider` | `SP_ENDPOINT` | `http://osac-service-provider:8080` |
| `osac-service-provider` | `SP_OSAC_FULFILLMENT_ADDRESS` | `osac-mock-provider:9090` |
| `osac-service-provider` | `SP_OSAC_OIDC_ISSUER_URL` | `http://osac-mock-provider:9091` |
| `osac-service-provider` | `SP_OSAC_OIDC_CLIENT_ID`/`_SECRET` | any non-empty value (mock never validates, [DD-132](../decisions/osac-sp.decisions.md)) |
| `osac-mock-provider` | `MOCK_GRPC_ADDRESS` / `MOCK_OIDC_ADDRESS` | `:9090` / `:9091` |

---

## 3. Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-E2E-010 | The GitHub Actions job MUST create a `kind` cluster, build+load `osac-service-provider` and `osac-mock-provider` images (no push), and pull `quay.io/dcm-project/control-plane:main` (no build) | MUST | |
| REQ-E2E-020 | The job MUST install `control-plane`'s `deploy/helm/dcm/` chart (sparse-checkout at a pinned ref) with `dcmUi.enabled=false`, `controlPlane.route.enabled=false`, `controlPlane.ingress.enabled=false` | MUST | `kind` has no `Route` CRD; UI is irrelevant to assertions |
| REQ-E2E-030 | The job MUST apply this repo's own plain-manifest `Deployment`+`Service` pair for `osac-service-provider` and for `osac-mock-provider`, wired per §2's table | MUST | Not part of the upstream chart (yet) |
| REQ-E2E-040 | The job MUST wait, with a bounded timeout, for every component's readiness (control-plane's `/health`-equivalent, `osac-sp`'s `GET /api/v1alpha1/clusters/health` and `/vms/health` both reporting `status: healthy`) before running assertions, and MUST fail the job (not hang) if the timeout is exceeded | MUST | Reuses each component's existing health endpoint — no new one invented |
| REQ-E2E-050 | The e2e suite MUST assert that `osac-sp`, running for real against real `control-plane`, successfully self-registers **both** the `cluster` and `vm` service types (2 independent registrations, per `internal/registration.Registrar`'s existing design) | MUST | First real cross-repo assertion of the SP↔control-plane registration contract |
| REQ-E2E-060 | The e2e suite MUST assert that `osac-sp`'s own two health endpoints report `status: healthy` with `connected: true` against the real `osac-mock-provider` (real gRPC dial + real OIDC token fetch, not bufconn) | MUST | Exercises `internal/osac.Bootstrap` end-to-end for the first time outside its own unit/integration tests |
| REQ-E2E-070 | The job MUST tear down the `kind` cluster on both success and failure, and MUST upload each component's logs as a build artifact on failure | MUST | Matches `fulfillment-service`'s own IT harness convention (verified in the Tier B spike, [#19](https://github.com/dcm-project/osac-service-provider/pull/19)) |
| REQ-E2E-080 | The e2e suite MUST run as a separate nested Go module (own `go.mod`) so its dependencies (a `control-plane` REST client, `k8s.io/client-go` if used for readiness polling) never enter the main module's `go.mod`/`go.sum` | MUST | Same isolation rationale as the Tier B spike, [#19](https://github.com/dcm-project/osac-service-provider/pull/19) |
| REQ-E2E-090 | The e2e suite MUST exercise a full Cluster CRUD lifecycle (`Create` → `Get` → `List` → `Delete`) directly against real `osac-sp`'s REST API, dispatching into the real `osac-mock-provider` gRPC backend (not the bufconn fakes Milestone 3's own unit/integration tests use), asserting `Create`'s response reaches the mock's terminal ready status (`ACTIVE`) and the subsequent `Get`'s response additionally carries a non-empty `kubeconfig` — `Create` itself never populates `kubeconfig` (`osac-sp-m3-cluster-crud.spec.md` REQ-CREATE-050; only `Get` does, conditionally on `ACTIVE`, REQ-GET-020) — without any polling for convergence, since the real mock resolves `Create` synchronously to `CLUSTER_STATE_READY` (`osac-sp-e2e-mock-provider.spec.md` REQ-MOCK-030) | MUST | Milestone 3 (Cluster CRUD); see sequencing note above |
| REQ-E2E-091 | Cluster `Delete` MUST be idempotent when exercised over real HTTP against the real mock backend: a `Delete` of an id that was already deleted MUST still return `204`, mirroring `osac-sp-m3-cluster-crud.spec.md` REQ-DELETE-020 | MUST | Milestone 3 |
| REQ-E2E-100 | The e2e suite MUST exercise a full VM CRUD lifecycle (`Create` → `Get` → `List` → `Delete`) directly against real `osac-sp`'s REST API, dispatching into the real `osac-mock-provider` gRPC backend, asserting the created object reaches the mock's terminal ready status (`RUNNING`) without any polling for convergence — the real mock resolves `Create` synchronously to `COMPUTE_INSTANCE_STATE_RUNNING` (`osac-sp-e2e-mock-provider.spec.md` REQ-MOCK-030) | MUST | Milestone 4 (VM CRUD); see sequencing note above |
| REQ-E2E-101 | VM `Delete` MUST be idempotent when exercised over real HTTP against the real mock backend: a `Delete` of an id that was already deleted MUST still return `204`, mirroring `osac-sp-m4-vm-crud.spec.md` REQ-VMDELETE-020 | MUST | Milestone 4 |
| REQ-E2E-110 | Cluster `Create` MUST reject a request whose `spec.version` is absent from the advertised version-translation matrix with `400`/`INVALID_ARGUMENT`, over real HTTP against the real mock backend, and the rejected request MUST never reach `osac-mock-provider` (no partial side effect) | MUST | Milestone 6 (version matrix); mirrors `osac-sp-m6-version-matrix.spec.md` REQ-VERSION-080; see sequencing note above |
| REQ-E2E-111 | An explicit `provider_hints.osac.release_image` override on Cluster `Create` MUST bypass version-matrix validation, over real HTTP against the real mock backend, even for a `spec.version` otherwise absent from the matrix | MUST | Milestone 6; mirrors `osac-sp-m6-version-matrix.spec.md` REQ-VERSION-080's bypass clause |

---

## 4. Acceptance Criteria

##### AC-E2E-010: The stack comes up healthy within the bounded wait

- **Validates:** REQ-E2E-010, REQ-E2E-020, REQ-E2E-030, REQ-E2E-040
- **Given** a freshly created `kind` cluster with the chart and manifests
  applied per §2
- **When** the job polls each component's readiness/health endpoint
- **Then** every component reports ready/healthy before the bounded timeout
  elapses, and the job proceeds to run the e2e suite

##### AC-E2E-020: `osac-sp` registers both service types with real `control-plane`

- **Validates:** REQ-E2E-050
- **Given** the healthy stack from AC-E2E-010
- **When** the e2e suite queries `control-plane`'s real REST API for
  registered providers
- **Then** it finds exactly one `cluster`-type and one `vm`-type provider
  entry, both pointing at `osac-service-provider`'s real `SP_ENDPOINT`

##### AC-E2E-030: `osac-sp`'s health reflects real mock-provider connectivity

- **Validates:** REQ-E2E-060
- **Given** the healthy stack from AC-E2E-010
- **When** the e2e suite calls `osac-sp`'s own
  `GET /api/v1alpha1/clusters/health` and `GET /api/v1alpha1/vms/health`
  directly
- **Then** both report `status: healthy` with the OIDC-token and gRPC-probe
  sub-fields both indicating success

##### AC-E2E-040: A component failure produces retrievable logs, not a silent hang

- **Validates:** REQ-E2E-040, REQ-E2E-070
- **When** any component fails to become healthy within the bounded timeout
- **Then** the job fails (does not hang to its outer CI timeout) and uploads
  `kubectl logs`/`describe` output for every component as a build artifact

##### AC-E2E-050: Cluster CRUD round-trips end-to-end against the real mock backend

- **Validates:** REQ-E2E-090, REQ-E2E-091
- **Given** the healthy stack from AC-E2E-010, running `osac-sp` built with
  Milestone 3's Cluster CRUD handlers
- **When** the e2e suite `POST`s a valid Cluster create request to real
  `osac-sp`, then `GET`s it, `LIST`s it, and `DELETE`s it — all over real
  HTTP, no bufconn
- **Then** `Create`/`Get` both report `status: ACTIVE` with a non-empty
  `kubeconfig`, `List` includes the created object, `Delete` returns `204`,
  a subsequent `Get` returns `404`, and a second `Delete` of the same id
  still returns `204`

##### AC-E2E-060: VM CRUD round-trips end-to-end against the real mock backend

- **Validates:** REQ-E2E-100, REQ-E2E-101
- **Given** the healthy stack from AC-E2E-010, running `osac-sp` built with
  Milestone 4's VM CRUD handlers
- **When** the e2e suite `POST`s a valid VM create request to real
  `osac-sp`, then `GET`s it, `LIST`s it, and `DELETE`s it — all over real
  HTTP, no bufconn
- **Then** `Create`/`Get` both report `status: RUNNING`, `List` includes
  the created object, `Delete` returns `204`, a subsequent `Get` returns
  `404`, and a second `Delete` of the same id still returns `204`

##### AC-E2E-070: Version-matrix enforcement round-trips over real HTTP

- **Validates:** REQ-E2E-110, REQ-E2E-111
- **Given** the healthy stack from AC-E2E-010, running `osac-sp` built with
  Milestone 6's version-matrix wiring (which itself requires Milestone 3's
  Cluster CRUD handlers)
- **When** the e2e suite `POST`s a Cluster create request whose `spec.version`
  is absent from the advertised matrix and carries no `release_image`
  override
- **Then** the response is `400` with an RFC 7807 body whose `type` is
  `INVALID_ARGUMENT`, and a subsequent `List` never contains the rejected
  id (proving the mock backend was never called)
- **And when** the same request instead carries an explicit
  `provider_hints.osac.release_image` override
- **Then** `Create` succeeds (`201`, `status: ACTIVE`) despite the
  otherwise-unsupported version, proving the override bypasses the matrix
  check end-to-end, not just in `internal/cluster`'s own unit tests

---

## 5. Non-Functional Requirements

| ID | Requirement |
|----|-------------|
| NFR-E2E-010 | Total job wall-clock time (cluster create → teardown) SHOULD stay well under GitHub Actions' free-tier job timeout, budgeted at 20 minutes per issue #17's resource estimate (~3–4.5 GB RAM, comfortably inside 16 GB) |
| NFR-E2E-020 | The workflow MUST NOT require any credentials/secrets beyond what's already public (pulling a public `quay.io` image, sparse-checking-out a public repo) |

---

## 6. Explicitly out of scope (this phase)

- **Subnet/VirtualNetwork CRUD assertions as their own top-level
  scenarios** — REQ-E2E-090/100's VM lifecycle already exercises
  `Subnets/List` and `VirtualNetworks/Create` indirectly (M4's default
  network provisioning, REQ-VMNET-010..050), but no e2e case asserts
  those two services' CRUD endpoints directly the way Cluster/VM's own are
  asserted — no `osac-sp` REST surface exposes them (they're OSAC-internal
  to VM's network resolution), so there's nothing over HTTP to assert
  against.
- **NATS status-event round-trip** — Milestone 5 ([#25](https://github.com/dcm-project/osac-service-provider/pull/25)) is implemented, but publishes async CloudEvents onto a shared NATS subject with no `osac-sp`-side REST surface to assert delivery against; a real assertion belongs to whichever component subscribes and renders status (likely `control-plane` or a future consumer), not this suite. Included in the throwaway combined-branch validation (DD-146) only as a "doesn't destabilize the stack" smoke check, not as a behavioral TC.
- **Real `fulfillment-service` + real Keycloak** (Tier B, closing the
  auth-claims-fidelity gap identified after Phase 1 — see
  [#19](https://github.com/dcm-project/osac-service-provider/pull/19)'s
  spike) — a deliberate, separate enhancement to this workflow once this
  MVP is green; not a blocker for it.
- **Real OSAC backend** (`FLPATH-4760`) — `control-plane`/QE's tier, OCP-gated.
- **Multi-SP generalization docs** — issue #17's "Generalization for other
  SP teams" section is written up once this reference implementation is
  proven, not before.

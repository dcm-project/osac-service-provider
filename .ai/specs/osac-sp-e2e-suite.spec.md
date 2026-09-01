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
health-check propagation, and Cluster/VM CRUD dispatch (Milestone 3/4,
REQ-E2E-090..102) — exercised over real HTTP into the real
`osac-mock-provider` gRPC backend, not the bufconn fakes those milestones'
own unit/integration tests use. NATS status-event round-trips (Milestone 5)
remain **not yet assertable**, since that milestone's own async publish
path has no `osac-sp`-side REST surface to assert against over HTTP; §6
tracks it as an explicit follow-up rather than silently deferring it.

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
- `test/mockprovider/config.go` — the exact env-var contract this phase's
  `osac-mock-provider` manifest must satisfy (`MOCK_GRPC_ADDRESS`,
  `MOCK_OIDC_ADDRESS`)
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
| `osac-service-provider` | `SP_ENDPOINT` | `http://osac-service-provider:8080` |
| `osac-service-provider` | `SP_OSAC_FULFILLMENT_ADDRESS` | `osac-mock-provider:9090` |
| `osac-service-provider` | `SP_OSAC_OIDC_ISSUER_URL` | `http://osac-mock-provider:9091` |
| `osac-service-provider` | `SP_OSAC_OIDC_CLIENT_ID`/`_SECRET` | any non-empty value (mock never validates, [DD-207](../decisions/osac-sp.decisions.md)) |
| `osac-mock-provider` | `MOCK_GRPC_ADDRESS` / `MOCK_OIDC_ADDRESS` | `:9090` / `:9091` |

---

## 3. Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-E2E-010 | The GitHub Actions job MUST create a `kind` cluster, build+load `osac-service-provider` and `osac-mock-provider` images (no push), and pull `quay.io/dcm-project/control-plane:main` (no build) | MUST | |
| REQ-E2E-020 | The job MUST install `control-plane`'s `deploy/helm/dcm/` chart (sparse-checkout at a pinned ref) with `dcmUi.enabled=false`, `controlPlane.route.enabled=false`, `controlPlane.ingress.enabled=false` | MUST | `kind` has no `Route` CRD; UI is irrelevant to assertions |
| REQ-E2E-030 | The job MUST apply this repo's own plain-manifest `Deployment`+`Service` pair for `osac-service-provider` and for `osac-mock-provider`, wired per §2's table | MUST | Not part of the upstream chart (yet) |
| REQ-E2E-040 | The job MUST wait, with a bounded timeout, for every component's Kubernetes readiness (`kubectl wait --for=condition=Available`) before starting the e2e suite, and MUST fail the job (not hang) if the timeout is exceeded. Where the suite separately polls `osac-sp`'s own `GET /api/v1alpha1/clusters/health` and `/vms/health` for `status: healthy` (see AC-E2E-030), that polling is deliberately not gated by this job-level wait, to avoid a hidden test-ordering assumption (DD-142) | MUST | Reuses each component's existing health endpoint — no new one invented. As of DD-212 (#28), the health-polling `Describe` block is `Label("tier-b-only")`, so `e2e.yaml` (Phase A) no longer does this at all — only `e2e-tierb.yaml`'s run of the suite does; this job-level readiness wait itself still applies to both |
| REQ-E2E-050 | The e2e suite MUST assert that `osac-sp`, running for real against real `control-plane`, successfully self-registers **both** the `cluster` and `vm` service types (2 independent registrations, per `internal/registration.Registrar`'s existing design) | MUST | First real cross-repo assertion of the SP↔control-plane registration contract. As of DD-212 (#28), this only runs in `e2e-tierb.yaml`, not `e2e.yaml` — neither the OSAC backend nor its mock is relevant to this assertion at all |
| REQ-E2E-060 | The e2e suite MUST assert that `osac-sp`'s own two health endpoints report `status: healthy` against a real backend (real gRPC dial + real OIDC token fetch, not bufconn) | MUST | Exercises `internal/osac.Bootstrap` end-to-end for the first time outside its own unit/integration tests; the `Health` schema (`api/v1alpha1/openapi.yaml`) has no `connected` field — `status`/`detail` are the only signal. As of DD-212 (#28), this only runs in `e2e-tierb.yaml` against Tier B's real `fulfillment-service`, not `e2e.yaml`/`osac-mock-provider` — the mock case added no signal beyond `internal/osac`'s own bufconn tests |
| REQ-E2E-070 | The job MUST tear down the `kind` cluster on both success and failure, and MUST upload each component's logs as a build artifact on failure | MUST | Matches `fulfillment-service`'s own IT harness convention (verified in the Tier B spike, [#19](https://github.com/dcm-project/osac-service-provider/pull/19)) |
| REQ-E2E-080 | The e2e suite MUST run as a separate nested Go module (own `go.mod`) so its dependencies (a `control-plane` REST client, `k8s.io/client-go` if used for readiness polling) never enter the main module's `go.mod`/`go.sum` | MUST | Same isolation rationale as the Tier B spike, [#19](https://github.com/dcm-project/osac-service-provider/pull/19) |
| REQ-E2E-090 | The e2e suite MUST exercise a full Cluster CRUD lifecycle (`Create` → `Get` → `List` → `Delete`) directly against real `osac-sp`'s REST API, dispatching into the real `osac-mock-provider` gRPC backend (not the bufconn fakes Milestone 3's own unit/integration tests use), asserting `Create`'s response reaches the mock's terminal ready status (`ACTIVE`) and the subsequent `Get`'s response additionally carries a non-empty `kubeconfig` — `Create` itself never populates `kubeconfig` (`osac-sp-m3-cluster-crud.spec.md` REQ-CREATE-050; only `Get` does, conditionally on `ACTIVE`, REQ-GET-020) — without any polling for convergence, since the real mock resolves `Create` synchronously to `CLUSTER_STATE_READY` (`osac-sp-e2e-mock-provider.spec.md` REQ-MOCK-030) | MUST | Milestone 3 (Cluster CRUD) |
| REQ-E2E-091 | Cluster `Delete` MUST be idempotent when exercised over real HTTP against the real mock backend: a `Delete` of an id that was already deleted MUST still return `204`, mirroring `osac-sp-m3-cluster-crud.spec.md` REQ-DELETE-020 | MUST | Milestone 3 |
| REQ-E2E-092 | Cluster `Create` MUST be idempotent when exercised over real HTTP against the real mock backend: a second `Create` for an `id` that already exists MUST return `201` with the existing cluster's current state (not an error), mirroring `osac-sp-m3-cluster-crud.spec.md` REQ-CREATE-040/DD-100 — this is the one scenario `osac-mock-provider`'s own `ALREADY_EXISTS` behavior (`osac-sp-e2e-mock-provider.spec.md` REQ-MOCK-020) was purpose-built to make testable against a real backend, not a bufconn fake | MUST | Milestone 3 |
| REQ-E2E-100 | The e2e suite MUST exercise a full VM CRUD lifecycle (`Create` → `Get` → `List` → `Delete`) directly against real `osac-sp`'s REST API, dispatching into the real `osac-mock-provider` gRPC backend, asserting the created object reaches the mock's terminal ready status (`RUNNING`) without any polling for convergence — the real mock resolves `Create` synchronously to `COMPUTE_INSTANCE_STATE_RUNNING` (`osac-sp-e2e-mock-provider.spec.md` REQ-MOCK-030) | MUST | Milestone 4 (VM CRUD) |
| REQ-E2E-101 | VM `Delete` MUST be idempotent when exercised over real HTTP against the real mock backend: a `Delete` of an id that was already deleted MUST still return `204`, mirroring `osac-sp-m4-vm-crud.spec.md` REQ-VMDELETE-020 | MUST | Milestone 4 |
| REQ-E2E-102 | VM `Create` MUST be idempotent when exercised over real HTTP against the real mock backend: a second `Create` for an `id` that already exists MUST return `201` with the existing VM's current state (not an error), mirroring `osac-sp-m4-vm-crud.spec.md` REQ-VMCREATE-070 (itself mirroring Milestone 3's DD-100) — same `osac-mock-provider` `ALREADY_EXISTS` contract as REQ-E2E-092, exercised through the VM surface instead of Cluster's | MUST | Milestone 4 |

---

## 4. Acceptance Criteria

##### AC-E2E-010: The stack comes up healthy within the bounded wait

- **Validates:** REQ-E2E-010, REQ-E2E-020, REQ-E2E-030, REQ-E2E-040
- **Given** a freshly created `kind` cluster with the chart and manifests
  applied per §2
- **When** the job waits on each component's Kubernetes `Available` condition
- **Then** every component reaches `Available` before the bounded timeout
  elapses, and the job proceeds to run the e2e suite — `osac-sp`'s own
  business-level `healthy` status is confirmed separately by the suite
  itself (AC-E2E-030), not by this job-level wait

##### AC-E2E-020: `osac-sp` registers both service types with real `control-plane`

- **Validates:** REQ-E2E-050
- **Given** the healthy stack from AC-E2E-010
- **When** the e2e suite queries `control-plane`'s real REST API for
  registered providers
- **Then** it finds exactly one `cluster`-type and one `vm`-type provider
  entry, both pointing at `osac-service-provider`'s real `SP_ENDPOINT`

##### AC-E2E-030: `osac-sp`'s health reflects real backend connectivity

- **Validates:** REQ-E2E-060
- **Given** the healthy stack from AC-E2E-010
- **When** the e2e suite calls `osac-sp`'s own
  `GET /api/v1alpha1/clusters/health` and `GET /api/v1alpha1/vms/health`
  directly
- **Then** both report `status: healthy` with an empty `detail` — a
  non-empty `detail` would indicate the OIDC token fetch or the OSAC gRPC
  probe failed (`internal/osac.Bootstrap`)

##### AC-E2E-040: A component failure produces retrievable logs, not a silent hang

- **Validates:** REQ-E2E-040, REQ-E2E-070
- **When** any component fails to become healthy within the bounded timeout
- **Then** the job fails (does not hang to its outer CI timeout) and uploads
  `kubectl logs`/`describe` output for every component as a build artifact

##### AC-E2E-050: Cluster CRUD round-trips end-to-end against the real mock backend

- **Validates:** REQ-E2E-090, REQ-E2E-091
- **Given** the healthy stack from AC-E2E-010
- **When** the e2e suite `POST`s a Cluster Create, then `Get`s, `List`s, and
  `Delete`s it, then repeats `Delete` on the same (now-deleted) id
- **Then** `Create`/`Get` report `ACTIVE` with `Get`'s response additionally
  carrying a non-empty, valid-base64 `kubeconfig`; `List` contains the id
  before deletion; both `Delete` calls return `204`; a `Get` after deletion
  returns `404`

##### AC-E2E-051: Cluster `Create` is idempotent against the real mock backend's `ALREADY_EXISTS`

- **Validates:** REQ-E2E-092
- **Given** the healthy stack from AC-E2E-010
- **When** the e2e suite `POST`s a Cluster Create twice for the same id
- **Then** both calls return `201` with identical `id`/`status`, and a
  subsequent `List` shows exactly one entry for that id

##### AC-E2E-060: VM CRUD round-trips end-to-end against the real mock backend

- **Validates:** REQ-E2E-100, REQ-E2E-101
- **Given** the healthy stack from AC-E2E-010
- **When** the e2e suite `POST`s a VM Create, then `Get`s, `List`s, and
  `Delete`s it, then repeats `Delete` on the same (now-deleted) id
- **Then** `Create`/`Get` report `RUNNING`; `List` contains the id before
  deletion; both `Delete` calls return `204`; a `Get` after deletion returns
  `404`

##### AC-E2E-061: VM `Create` is idempotent against the real mock backend's `ALREADY_EXISTS`

- **Validates:** REQ-E2E-102
- **Given** the healthy stack from AC-E2E-010
- **When** the e2e suite `POST`s a VM Create twice for the same id
- **Then** both calls return `201` with identical `id`/`status` — same
  `ALREADY_EXISTS` contract as AC-E2E-051, exercised through the VM surface

---

## 5. Non-Functional Requirements

| ID | Requirement |
|----|-------------|
| NFR-E2E-010 | Total job wall-clock time (cluster create → teardown) SHOULD stay well under GitHub Actions' free-tier job timeout, budgeted at 20 minutes per issue #17's resource estimate (~3–4.5 GB RAM, comfortably inside 16 GB) |
| NFR-E2E-020 | The workflow MUST NOT require any credentials/secrets beyond what's already public (pulling a public `quay.io` image, sparse-checking-out a public repo) |

---

## 6. Explicitly out of scope (this phase)

- **Subnet/VirtualNetwork CRUD dispatch assertions** — Cluster/VM CRUD
  (REQ-E2E-090..102) covers Milestone 3/4's own directly-exposed REST
  surfaces; `Subnet`/`VirtualNetwork` are only ever created indirectly as
  part of VM Default Network Provisioning (M4 §4.5), with no dedicated
  `osac-sp` REST endpoint of their own to assert against here.
- **NATS status-event round-trip** — Milestone 5, not yet designed.
- **Version-matrix (Milestone 6) validation over real HTTP** — a separate
  follow-up; not part of this phase's REQ-E2E-090..102 CRUD coverage.
- **Real `fulfillment-service` + real Keycloak** (Tier B, closing the
  auth-claims-fidelity gap identified after Phase 1 — see
  [#19](https://github.com/dcm-project/osac-service-provider/pull/19)'s
  spike) — a deliberate, separate enhancement to this workflow once this
  MVP is green; not a blocker for it.
- **Real OSAC backend** (`FLPATH-4760`) — `control-plane`/QE's tier, OCP-gated.
- **Multi-SP generalization docs** — issue #17's "Generalization for other
  SP teams" section is written up once this reference implementation is
  proven, not before.

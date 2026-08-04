# Test Plan: kind-based e2e CI (Phase 2 of the e2e infra)

Scope: the e2e assertions for
[`osac-sp-e2e-suite.spec.md`](../specs/osac-sp-e2e-suite.spec.md), run by
`.github/workflows/e2e.yaml` against a live `kind` cluster. New ID space —
`TC-E2E-*` — since these run against a real cluster in CI, not `go test`
locally; they are not part of the `TC-U-*`/`TC-I-*` pyramid tiers and are not
counted toward the repo's 100%-unit-coverage gate.

**Framework:** Ginkgo v2 + Gomega, same as the rest of the repo, but in a
**separate nested Go module** (`test/e2e/go.mod`, per REQ-E2E-080) so a
`control-plane` REST client and any Kubernetes client-go dependency never
enter the main module. Run locally against a running cluster with:

```bash
cd test/e2e && KIND_KUBECONFIG=<path> CONTROL_PLANE_URL=http://localhost:<port> OSAC_SP_URL=http://localhost:<port> \
  go run github.com/onsi/ginkgo/v2/ginkgo -r -v
```

(the workflow itself sets these via `kubectl port-forward` or `NodePort`s —
finalized in the workflow, not fixed by this test plan).

**Assertion discipline:** assert actual response fields (exact service-type
values, exact `status` strings), not existence-only/200-only checks — same
discipline as the rest of the repo's test plans.

**What's real here, what's not:** `control-plane` and `osac-sp` are the real
built/pulled artifacts (§2 of the spec) — nothing under test is a fake.
`osac-mock-provider` (Phase 1) stands in for the actual external OSAC
backend; that substitution is the one documented, deliberate scope boundary
(spec §1/§6), not a gap in this tier's own fidelity.

---

## 1. Infra readiness

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-E2E-010 | All 5 pods reach `Ready` within the bounded wait | REQ-E2E-010, REQ-E2E-020, REQ-E2E-030, REQ-E2E-040, AC-E2E-010 | Before the Ginkgo suite's specs run, its `SynchronizedBeforeSuite` (or the workflow's own pre-step — decided at implementation time) polls `kubectl get pods` / each health endpoint until `dcm-postgres`, `dcm-nats`, `dcm-control-plane`, `osac-service-provider`, and `osac-mock-provider` are all `Ready`; assert this completes before the bounded timeout (NFR-E2E-010) and fail with the last-observed pod statuses on timeout (not a bare "context deadline exceeded"). |

---

## 2. Registration contract (`osac-sp` ↔ real `control-plane`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-E2E-020 | `osac-sp` registers a `cluster`-type provider with real `control-plane` | REQ-E2E-050, AC-E2E-020 | `GET` `control-plane`'s real provider-listing endpoint; assert exactly one entry has `serviceType == "cluster"` (or the real API's equivalent enum/string — confirmed against `control-plane`'s actual REST schema at implementation time) and `endpoint` equal to `osac-service-provider`'s real in-cluster `SP_ENDPOINT`. |
| TC-E2E-030 | `osac-sp` registers a `vm`-type provider with real `control-plane` | REQ-E2E-050, AC-E2E-020 | Same as TC-E2E-020, asserting the independent `vm`-type entry — proves the two registration loops are genuinely independent against a real backend, not just in `internal/registration`'s own fakes. |
| TC-E2E-040 | Both registrations persist across a re-registration cycle (no duplicates) | REQ-E2E-050, AC-E2E-020 | Wait past `internal/registration.Registrar`'s periodic re-registration interval; re-`GET` the provider listing; assert still exactly one `cluster` and one `vm` entry each (idempotent re-POST, DD-established behavior, now proven against a real, independently-built `control-plane` rather than the repo's own `fakeControlPlaneServer`). |

---

## 3. Health-check propagation (real gRPC + real OIDC, no bufconn)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-E2E-050 | `osac-sp`'s cluster health endpoint reports healthy against the real mock backend | REQ-E2E-060, AC-E2E-030 | `GET osac-service-provider`'s `/api/v1alpha1/clusters/health` directly (via port-forward/NodePort); assert HTTP `200`-class response body's `status == "healthy"` and its connectivity/token sub-fields indicate a real, successful OIDC token fetch and gRPC probe — not the bufconn-backed fakes `internal/health`'s own tests use. |
| TC-E2E-060 | `osac-sp`'s vm health endpoint reports the identical global health condition | REQ-E2E-060, AC-E2E-030 | Same as TC-E2E-050 for `/api/v1alpha1/vms/health`; assert its body matches TC-E2E-050's (one global health condition per CLAUDE.md's documented API design, now proven over a real wire, not just asserted from a single in-process handler test). |
| TC-E2E-070 | `control-plane`'s own health monitor reflects `osac-sp` as healthy | REQ-E2E-060, AC-E2E-030 | Query `control-plane`'s real health-monitor state (its REST API or, if none is exposed read-side, its logs/metrics — confirmed against the real API at implementation time) for both registered providers; assert both show the healthy status `osac-sp` itself reports, closing the full real, cross-repo propagation loop, not just `osac-sp`'s own view of itself. |

---

## 4. Failure-mode / CI-hygiene checks

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-E2E-080 | A deliberately broken mock-provider address fails the readiness wait, not the whole job silently | REQ-E2E-040, REQ-E2E-070, AC-E2E-040 | Exercised as a one-off manual/local workflow-input variant (e.g. a `workflow_dispatch` boolean), not on every PR run: point `osac-sp`'s `SP_OSAC_FULFILLMENT_ADDRESS` at a nonexistent Service; assert the readiness wait times out with a clear pod-status message and the workflow's log-upload step still runs (`if: always()`). |

---

## 5. Coverage Matrix

| Spec Topic | REQ Count | AC Count | TC-E2E | Notes |
|---|---|---|---|---|
| Infra bring-up/readiness | REQ-E2E-010, 020, 030, 040 | AC-E2E-010 | 1 (TC-E2E-010) | Infra preconditions gate every other TC in this plan; not itself a behavioral assertion about `osac-sp`. |
| Registration contract | REQ-E2E-050 | AC-E2E-020 | 3 (TC-E2E-020..040) | |
| Health-check propagation | REQ-E2E-060 | AC-E2E-030 | 3 (TC-E2E-050..070) | |
| CI failure-mode hygiene | REQ-E2E-040, 070 | AC-E2E-040 | 1 (TC-E2E-080) | Manual/opt-in variant, not run on every PR (would otherwise double the job's steady-state runtime for a check that doesn't need re-proving every merge). |
| **Total** | 8 | 4 | **8** | This tier is deliberately thin (2 behaviors — registration, health — per spec §1's "what exists today" scope) and additive: TC-E2E-090+ is reserved for CRUD/NATS assertions once Milestone 3/4/5 land on `main` (spec §6). |

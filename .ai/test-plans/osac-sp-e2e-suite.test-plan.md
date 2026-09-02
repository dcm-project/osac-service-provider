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

**E2E disposition invariant (DD-230):** every `REQ-*`/`AC-*` this test plan
or its milestone specs define must carry an explicit disposition —
**e2e-covered** (a `TC-E2E-*` entry exists), deliberately **deferred**
(named follow-up, owning issue), or **integration-tier-sufficient** (pure
translation logic already proven by a `TC-I-*`/`TC-U-*` over real HTTP or a
`bufconn` fake). This is not the same invariant as the `TC-U`+`TC-I`
pyramid — e2e coverage is a judgment call about real cross-process/timing/
wire risk, not a hard per-REQ requirement — but a `REQ-*`/`AC-*` with *no*
disposition recorded anywhere is an undocumented gap and blocks merge on
review. See the Coverage Matrix (§6) "Notes" column for where each
disposition is recorded.

---

## 1. Infra readiness

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-E2E-010 | All 5 pods reach `Ready` within the bounded wait | REQ-E2E-010, REQ-E2E-020, REQ-E2E-030, REQ-E2E-040, AC-E2E-010 | Before the Ginkgo suite's specs run, its `SynchronizedBeforeSuite` (or the workflow's own pre-step — decided at implementation time) polls `kubectl get pods` / each health endpoint until `dcm-postgres`, `dcm-nats`, `dcm-control-plane`, `osac-service-provider`, and `osac-mock-provider` are all `Ready`; assert this completes before the bounded timeout (NFR-E2E-010) and fail with the last-observed pod statuses on timeout (not a bare "context deadline exceeded"). |

---

## 2. Registration contract (`osac-sp` ↔ real `control-plane`)

**As of DD-212 (#28):** `registration_test.go`'s `Describe` block is
`Label("tier-b-only")` — these specs run only against Tier B's real
backend (`e2e-tierb.yaml`), no longer against `osac-mock-provider`
(`e2e.yaml`). None of them ever exercised the OSAC backend at all, so
mock-vs-real made no difference; running them in both jobs was pure
duplication once Tier B existed to run them for real.

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-E2E-020 | `osac-sp` registers a `cluster`-type provider with real `control-plane`, advertising `kubernetes_supported_versions` | REQ-E2E-050, REQ-E2E-051, AC-E2E-020, AC-E2E-021 | `GET` `control-plane`'s real provider-listing endpoint; assert exactly one entry has `serviceType == "cluster"` (or the real API's equivalent enum/string — confirmed against `control-plane`'s actual REST schema at implementation time) and `endpoint` equal to `osac-service-provider`'s real in-cluster `SP_ENDPOINT`; assert its `metadata.kubernetes_supported_versions` contains `"1.31"` — a key from `osac-sp`'s real, uninjected `DefaultMatrix` (closes DD-230's REQ-VERSION-050 disposition gap). |
| TC-E2E-030 | `osac-sp` registers a `vm`-type provider with real `control-plane` | REQ-E2E-050, AC-E2E-020 | Same as TC-E2E-020, asserting the independent `vm`-type entry — proves the two registration loops are genuinely independent against a real backend, not just in `internal/registration`'s own fakes. |
| TC-E2E-040 | Both registrations persist across a re-registration cycle (no duplicates) | REQ-E2E-050, AC-E2E-020 | Wait past `internal/registration.Registrar`'s periodic re-registration interval; re-`GET` the provider listing; assert still exactly one `cluster` and one `vm` entry each (idempotent re-POST, DD-established behavior, now proven against a real, independently-built `control-plane` rather than the repo's own `fakeControlPlaneServer`). |

---

## 3. Health-check propagation (real gRPC + real OIDC, no bufconn)

**As of DD-212 (#28):** `health_test.go`'s happy-path `Describe` block is
`Label("tier-b-only")` — same rationale as §2 above. These specs proved
"a backend that always says yes makes `osac-sp` report healthy," already
covered in-process by `internal/osac`'s own bufconn tests, and covered
more rigorously by Tier B's real Keycloak/`fulfillment-service` saying
yes for real reasons — running them against the mock too added no
signal.

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-E2E-050 | `osac-sp`'s cluster health endpoint reports healthy against the real backend | REQ-E2E-060, AC-E2E-030 | `GET osac-service-provider`'s `/api/v1alpha1/clusters/health` directly (via port-forward/NodePort), **polling (`Eventually`, 30s/500ms) rather than a single one-shot request** — `osac-sp`'s real OIDC token fetch + gRPC probe run asynchronously and are not gated by the pod's `Available` condition (DD-010: status is in the body, not the HTTP code), so a single-shot check would only pass reliably by accident of which other spec happened to run first (DD-142); assert the body's `status == "healthy"` once converged and its connectivity/token sub-fields indicate a real, successful OIDC token fetch and gRPC probe — not the bufconn-backed fakes `internal/health`'s own tests use. |
| TC-E2E-060 | `osac-sp`'s vm health endpoint reports the identical global health condition | REQ-E2E-060, AC-E2E-030 | Same polling discipline as TC-E2E-050, independently applied to `/api/v1alpha1/vms/health` (DD-142); assert its body matches TC-E2E-050's (one global health condition per CLAUDE.md's documented API design, now proven over a real wire, not just asserted from a single in-process handler test). |
| TC-E2E-070 | `control-plane`'s own health monitor reflects `osac-sp` as healthy | REQ-E2E-060, AC-E2E-030 | Query `control-plane`'s real `ListProviders` REST endpoint's `health_status` field for both registered providers; assert both equal `"ready"` — `control-plane`'s own vocabulary (`internal/sp/store/model.HealthStatusReady`) for "my last poll of this provider's `/health` succeeded," confirmed against the real API/source at implementation time. This is deliberately not the string `"healthy"` `osac-sp`'s own `/health` response uses (TC-E2E-050/060) — the two are independent layers' vocabularies, not one contract; this TC closes the full real, cross-repo propagation loop, not just `osac-sp`'s own view of itself. |

---

## 4. Failure-mode / CI-hygiene checks

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-E2E-080 | A deliberately broken mock-provider address fails the readiness wait, not the whole job silently | REQ-E2E-040, REQ-E2E-070, AC-E2E-040 | Exercised as a one-off manual/local workflow-input variant (e.g. a `workflow_dispatch` boolean), not on every PR run: point `osac-sp`'s `SP_OSAC_FULFILLMENT_ADDRESS` at a nonexistent Service; assert the readiness wait times out with a clear pod-status message and the workflow's log-upload step still runs (`if: always()`). |

---

## 5. Cluster/VM CRUD (real dispatch into the real mock backend)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-E2E-090 | Cluster Create → Get → List → Delete round-trips over real HTTP into the real mock backend | REQ-E2E-090, AC-E2E-050 | `POST /api/v1alpha1/clusters?id=<unique>` with a valid body; assert `201` with `status == "ACTIVE"` (no polling — the real mock resolves synchronously, REQ-MOCK-030); Create's own response never carries `kubeconfig` (REQ-CREATE-050); `GET` the same id and assert `status == "ACTIVE"` **and** a non-empty, valid-base64 `kubeconfig` (REQ-GET-020); `GET` (list) and assert the id appears; `DELETE` and assert `204`; `GET` again and assert `404`. |
| TC-E2E-091 | Cluster Delete tolerates an already-deleted id | REQ-E2E-091, AC-E2E-050 | `POST` a cluster, `DELETE` it (`204`), `DELETE` it again; assert the second call also returns `204`, not `404`. |
| TC-E2E-092 | Cluster Create is idempotent against the real mock's `ALREADY_EXISTS` | REQ-E2E-092, AC-E2E-051 | `POST` a cluster with a unique `id`, record its `id`/`status`; `POST` the identical create request again with the same `id`; assert `201` (not an error), the returned `id`/`status` match the first response exactly, and `List` shows exactly one entry for that `id` — proves the real mock's `ALREADY_EXISTS` (REQ-MOCK-020) round-trips through DD-100's handling, not just a bufconn fake's simulation. |
| TC-E2E-100 | VM Create → Get → List → Delete round-trips over real HTTP into the real mock backend | REQ-E2E-100, AC-E2E-060 | Same shape as TC-E2E-090 against `/api/v1alpha1/vms`, asserting `status == "RUNNING"` (no polling — `COMPUTE_INSTANCE_STATE_RUNNING` is set synchronously on `Create`, REQ-MOCK-030). |
| TC-E2E-101 | VM Delete tolerates an already-deleted id | REQ-E2E-101, AC-E2E-060 | Same shape as TC-E2E-091 against `/api/v1alpha1/vms`. |
| TC-E2E-102 | VM Create is idempotent against the real mock's `ALREADY_EXISTS` | REQ-E2E-102, AC-E2E-061 | Same shape as TC-E2E-092 against `/api/v1alpha1/vms` (REQ-VMCREATE-070). |
| TC-E2E-103 | Cluster Create rejects an unknown `template_id` as 400, not 404 | REQ-E2E-103, AC-E2E-052 | `POST` a Create body whose `template_id` doesn't match the real mock's fixture default; assert `400` with RFC 9457 `type` exactly `.../invalid-argument`, and the id absent from a subsequent `List` — proves the real mock's own, unmodified `ClusterTemplatesServer.Get` NotFound (not a bufconn fake's simulation of it) round-trips through REQ-CREATE-100's mapping (closes DD-230's disposition gap). |

---

## 6. Coverage Matrix

| Spec Topic | REQ Count | AC Count | TC-E2E | Notes |
|---|---|---|---|---|
| Infra bring-up/readiness | REQ-E2E-010, 020, 030, 040 | AC-E2E-010 | 1 (TC-E2E-010) | Infra preconditions gate every other TC in this plan; not itself a behavioral assertion about `osac-sp`. |
| Registration contract | REQ-E2E-050, 051 | AC-E2E-020, 021 | 3 (TC-E2E-020..040) | REQ-E2E-051/AC-E2E-021 (kubernetes_supported_versions) ride TC-E2E-020's existing assertion, not a new TC. |
| Health-check propagation | REQ-E2E-060 | AC-E2E-030 | 3 (TC-E2E-050..070) | |
| CI failure-mode hygiene | REQ-E2E-040, 070 | AC-E2E-040 | 1 (TC-E2E-080) | Manual/opt-in variant, not run on every PR (would otherwise double the job's steady-state runtime for a check that doesn't need re-proving every merge). |
| Cluster CRUD (Milestone 3) | REQ-E2E-090, 091, 092, 103 | AC-E2E-050, 051, 052 | 4 (TC-E2E-090, 091, 092, 103) | |
| VM CRUD (Milestone 4) | REQ-E2E-100, 101, 102 | AC-E2E-060, 061 | 3 (TC-E2E-100, 101, 102) | |
| **Total** | 16 | 10 | **15** | NATS status-event round-trips (Milestone 5) remain a deliberately-untested-here follow-up (spec §6) — implemented, but with no `osac-sp`-side REST surface for this suite to assert delivery against. REQ/AC counts sum each row's literal count (not deduplicated across rows). |

---

## 7. E2E disposition for milestone-spec `REQ-*`/`AC-*` outside this suite's own `REQ-E2E-*` scope (DD-230)

The Coverage Matrix above only tracks this suite's *own* `REQ-E2E-*`/
`AC-E2E-*` IDs. DD-230 requires every `REQ-*`/`AC-*` defined by the
milestone specs (`osac-sp.spec.md`, `osac-sp-m3-cluster-crud.spec.md`,
`osac-sp-m4-vm-crud.spec.md`, `osac-sp-m5-status-reporting.spec.md`,
`osac-sp-m6-version-matrix.spec.md`) to carry an explicit disposition
somewhere — this table is that record for the ones with no `TC-E2E-*`/
`TC-TB-*` coverage of their own.

| Milestone REQ group | Disposition | Rationale |
|---|---|---|
| `REQ-CREATE-100` (unknown `template_id` → `400`) | **e2e-covered** | `TC-E2E-103` (§5). |
| `REQ-VERSION-050` (registration metadata carries `kubernetes_supported_versions`) | **e2e-covered** | `TC-E2E-020`'s updated assertion (§2). |
| `REQ-STATUS-020` (10-rule status precedence table) | integration-tier-sufficient | Pure translation logic; `TC-I-240` already proves the same mapper over real HTTP against the real router — a live cluster adds no new signal, only CI time. |
| `REQ-ERR-010/020/030`, `REQ-VMERR-010/020/030` (gRPC-code→HTTP tables) | integration-tier-sufficient | `TC-I-250`/`TC-I-360` already exercise the full tables over real HTTP. |
| `REQ-LIST-020/040` (pagination round-trip, `Size`/`Total` mismatch) | integration-tier-sufficient | `TC-I-221`/`TC-I-223` already prove this via two real, sequential HTTP requests. |
| `REQ-VMSTATUS-020` (8-value VM status precedence) | integration-tier-sufficient | `TC-I-350` already proves it over real HTTP. |
| `REQ-VMNET-020/030/040` (default-network provision/poll/timeout) | **deferred** | Genuinely untested at any e2e tier today — the mock always resolves `READY` synchronously, so `osac-sp`'s poll loop never iterates in a live run. Closing this needs a mock-side delayed-ready simulation plus an order-independent reset hook (the shared default network is provisioned once per pod lifetime); tracked as a follow-up rather than closed here. |
| `REQ-HTTP-030/040` (graceful shutdown on SIGTERM/SIGINT) | integration-tier-sufficient | `TC-I-003`/`TC-I-004` already drain a real in-flight request against a real signal; process-lifecycle behavior, not a cross-process/wire-contract risk this tier's live cluster would add signal to. |
| `REQ-HTTP-070` (panic recovery → RFC 9457) | integration-tier-sufficient | Unusually well-tested already at the unit tier (`TC-U-070/073/075/087/088`); a live-cluster panic would just re-prove the same in-process recovery middleware. |
| `REQ-OSAC-011/012` (RFC 8414-then-OIDC-discovery fallback order) | integration-tier-sufficient | `TC-U-023/024/025` force both the success and fallback branches deterministically; Tier B's real Keycloak (`TC-TB-030`) only ever proves *a* real token is issued, not that the fallback branch specifically fired — forcing that branch e2e would need a deliberately-RFC-8414-less Keycloak variant for marginal added confidence. |
| `REQ-REG-080/090` (409-not-fatal / other-4xx-fatal registration branching) | integration-tier-sufficient | `TC-I-023/024` prove both branches against a fake backend; `TC-I-029` additionally proves the 409 branch against a **real** `environment-agent` build (`make test-realbackend-environment-agent`). Deliberately forcing a 409/4xx from a live e2e `environment-agent` pod would require racing two registrations for the same slot — high flakiness risk for coverage already proven twice over. |
| `REQ-PUBLISH-*`, `REQ-POLL-*` (Milestone 5, NATS status-event publish) | **deferred** | See this table's own header note above — no `osac-sp`-side REST surface exists to assert delivery against yet. |
| `REQ-VERSION-010..040, 060..090` (Milestone 6, version-matrix internals beyond the registration metadata field) | integration-tier-sufficient | `Lookup`/`SupportedVersions`/`Load`/pre-flight-400 are pure config/translation logic, already fully unit/integration-tested (`osac-sp-m6-version-matrix.test-plan.md`); no cross-process/real-infra risk a live cluster would add signal to. |

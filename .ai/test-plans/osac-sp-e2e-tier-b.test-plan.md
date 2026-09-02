# Test Plan: Tier B e2e — real OSAC stack (Phase 1)

Scope: the e2e assertions for
[`osac-sp-e2e-tier-b.spec.md`](../specs/osac-sp-e2e-tier-b.spec.md) Phase 1,
run by a Tier B variant of the `kind`-based e2e workflow. New ID space —
`TC-TB-*` — since, like `TC-E2E-*`, these run against a live `kind` cluster
in CI, not `go test` locally; they are not part of the `TC-U-*`/`TC-I-*`
pyramid tiers and are not counted toward the repo's 100%-unit-coverage gate.
Phase 2's `TC-TB-*` range is reserved, not allocated here — REQ-TB-090 gates
its implementation on `osac-sp` M2+ landing first.

**Framework:** same `test/e2e` nested Go module (own `go.mod`, REQ-E2E-080)
as `osac-sp-e2e-suite.test-plan.md` — Tier B is a variant of that same suite
(a different backend stood up, same Ginkgo binary), not a separate module.

**Assertion discipline:** assert actual response fields and body details
(exact health sub-field values, exact RFC 9457 `type`), not
existence-only/200-only checks — same discipline as the rest of the repo's
test plans.

**What's real here, what's not:** everything in Phase 1's stack is a real,
independently-built or upstream-pinned artifact — real Postgres, real
Keycloak (official image), real `fulfillment-service` (pinned image/chart).
`osac-mock-provider` is not present in a Tier B run at all (spec §2, Phase
1) — it is fully replaced, not layered alongside.

**E2E disposition invariant (DD-230):** same invariant as
`osac-sp-e2e-suite.test-plan.md` — every `REQ-*`/`AC-*` relevant to this
tier must carry an explicit disposition (**e2e-covered** via a `TC-TB-*`
entry, **deferred**, or **integration-tier-sufficient**) recorded in the
Coverage Matrix (§7) "Notes" column. A `REQ-*`/`AC-*` with no disposition
recorded in either this file or `osac-sp-e2e-suite.test-plan.md` is an
undocumented gap and blocks merge on review.

---

## 1. Infra readiness

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-TB-010 | `ffs-postgres`, `ffs-keycloak`, and `ffs-fulfillment-service` all reach `Ready` within the bounded wait | REQ-TB-010, AC-TB-010 (given clause) | Same readiness-polling discipline as TC-E2E-010, extended to the three new Tier B pods; assert this completes before NFR-TB-010's resource/timeout budget and fail with last-observed pod statuses (not a bare timeout) if any pod doesn't converge. |

---

## 2. Real Keycloak issues correctly-claimed tokens

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-TB-020 | The vendored realm's `client_credentials` client issues a token carrying `username` and `osac-api` audience claims (not `organization`/`groups`/`realm_access.roles` — an earlier, unverified assumption corrected by DD-150) | REQ-TB-020 | Call `ffs-keycloak`'s real token endpoint directly (`client_credentials` grant, the vendored client/secret from `test/e2e/tierb-config/realm.json`) — independent of `osac-sp` — decode the returned JWT's payload (base64, no signature verification needed for this assertion) and assert `username` equals the expected service-account principal and `osac-api` is present as an audience claim. `groups` is deliberately NOT asserted: the realm's `groups` scope/mapper is present (REQ-TB-020), but Keycloak omits the claim entirely for a service account with no group memberships, per DD-150's addendum — asserting its presence would fail against correct, expected behavior. Proves the realm config itself is correct in isolation, before involving `osac-sp` at all. |

---

## 3. `osac-sp` is genuinely authenticated by real OSAC

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-TB-030 | `osac-sp`'s health endpoints report real, successful OIDC token acquisition and gRPC `Capabilities` connectivity against real OSAC | REQ-TB-030, REQ-TB-040, AC-TB-010 | No new spec: `health_test.go`'s existing "osac-sp health, against the real backend" `Describe` block (TC-E2E-050/060 — `status == "healthy"`, empty `Detail`) already runs against whatever `OSAC_SP_URL` points at; `.github/workflows/e2e-tierb.yaml` points it at `ffs-keycloak`/`ffs-fulfillment-service`, closing the auth-fidelity gap DD-132 documented as structurally untestable in Phase A, with no new assertion code needed (see `tierb_test.go`'s file-level doc comment). As of DD-212 (#28), this `Describe` block is `Label("tier-b-only")` and no longer runs in Phase A's `e2e.yaml` at all. |

---

## 4. CI-hygiene: image/chart tags are pinned

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-TB-040 | No Tier B manifest/workflow step references a floating (`main`/`latest`) OSAC tag | REQ-TB-050 | Static check, not a Ginkgo spec — a workflow step (or a small script invoked by `make check`) greps every Tier B manifest and the workflow's own `image:`/chart-version fields for `osac-project`/`ghcr.io/osac-project` references and fails the job if any resolves to `main`/`latest` rather than a `vX.Y.Z` tag. Mirrors upstream's own `check-floating-tags.yaml` guard (spec §3 rationale) on our side of the dependency. |

---

## 5. A real auth failure is genuinely detectable

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-TB-050 | A deliberately wrong client secret makes `osac-sp` report `unhealthy` with an auth-failure detail | REQ-TB-060, AC-TB-020 | Exercised as an opt-in `workflow_dispatch` variant, not on every PR run — same precedent as TC-E2E-080: a second `osac-sp` pod/config variant is deployed with `SP_OSAC_OIDC_CLIENT_SECRET` set to a value that matches no vendored Keycloak client; assert its health endpoint converges to `status: "unhealthy"` with a body detail identifying the failure as an auth/token error (not a generic/opaque `"connection failed"` string) — this is the core deliverable this tier exists for: proving Tier B can catch what Phase A's permissive mock (DD-132) structurally cannot. |

---

## 6. OSAC unreachable is genuinely detectable, distinct from an auth failure

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-TB-130 | An unroutable OSAC address makes `osac-sp` report `unhealthy` with a connectivity-only detail, distinct from TC-TB-050's auth-failure detail | REQ-TB-065, AC-TB-025 | Exercised as an opt-in `workflow_dispatch` variant, same precedent as TC-TB-050 — a third `osac-sp` pod/config variant is deployed with real, correct OIDC credentials but `SP_OSAC_FULFILLMENT_ADDRESS` pointed at an unroutable target; assert its health endpoint converges to `status: "unhealthy"` with body detail equal to exactly `"OSAC fulfillment service unreachable"` (`internal/health`'s exact string, TC-U-032/038) — never combined with or confused for the token-invalid detail. Closes DD-230's e2e disposition gap for the connectivity half of `osac-sp.spec.md`'s AC-HLT-060. |

---

## 7. Coverage Matrix

| Spec Topic | REQ Count | AC Count | TC-TB | Notes |
|---|---|---|---|---|
| Infra bring-up/readiness | REQ-TB-010 | AC-TB-010 (given) | 1 (TC-TB-010) | Gates every other TC in this plan. |
| Realm/claim correctness | REQ-TB-020 | — | 1 (TC-TB-020) | Verified directly against Keycloak, independent of `osac-sp`, before the harder end-to-end assertion. |
| Real auth success (osac-sp) | REQ-TB-030, REQ-TB-040 | AC-TB-010 | 1 (TC-TB-030) | The primary positive-path deliverable — closes DD-132's gap. |
| Pinned-tag CI hygiene | REQ-TB-050 | — | 1 (TC-TB-040) | Static/lint-shaped, not a runtime Ginkgo spec. |
| Real auth failure detection | REQ-TB-060 | AC-TB-020 | 1 (TC-TB-050) | Opt-in `workflow_dispatch` variant, matching TC-E2E-080's precedent (avoids doubling steady-state PR runtime). |
| "OSAC unreachable" health branch | REQ-TB-065 | AC-TB-025 | 1 (TC-TB-130) | Mirrors TC-TB-050's opt-in-variant pattern; closes the other half of `osac-sp.spec.md`'s AC-HLT-060 that TC-TB-050 doesn't reach — "OSAC unreachable, token valid" distinctly from "token invalid" (DD-230). |
| **Total** | 7 | 3 | **6** | Phase 1 is deliberately thin (one behavior — real auth fidelity — per spec §1's motivation); `TC-TB-060`+ is reserved for Phase 2 (`osac-operator`/BMFO/`osac-aap-mock` provisioning-fidelity assertions) once REQ-TB-090's gate opens. REQ/AC counts sum each row's literal count. |

---

## 8. E2E disposition for milestone-spec `REQ-*`/`AC-*` outside this tier's own scope (DD-230)

| Milestone REQ group | Disposition | Rationale |
|---|---|---|
| `REQ-OSAC-040` (custom `TLSCertFile` CA vs. system root pool, as distinct branches) | integration-tier-sufficient | `TC-U-012`/`TC-U-013` already force both branches deterministically; Tier B's real Keycloak/`fulfillment-service` only ever exercises whichever branch this tier's own manifest configures (currently the custom-CA path, DD-151/cert-manager) — exercising the other branch e2e would just mean deploying a second variant to prove config plumbing already covered by config-loading unit tests. |

All other Milestone 5/6 dispositions are recorded once, in
`osac-sp-e2e-suite.test-plan.md` §7 (not duplicated here) — they apply
identically regardless of which tier's backend is running underneath.

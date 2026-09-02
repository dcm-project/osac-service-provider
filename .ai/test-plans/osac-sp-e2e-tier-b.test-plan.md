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

## 6. Phase 2: `osac-aap-mock` unit tests

Scope: `test/cmd/osac-aap-mock`'s own `go test` unit coverage — same `TC-U-*` ID
space and pyramid-invariant/100%-unit-coverage discipline as the rest of the
repo (unlike `TC-TB-*`, these count toward that gate). Same testing pattern
as `test/mockprovider`'s own unit tests (`httptest.Server`, table-driven
where the response shape repeats across endpoints).

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-560 | `GetTemplate` (job template lookup by name) returns a real `{count, results}` body for any requested name | REQ-TB-080 | `GET /v2/job_templates/?name={name}` returns `200` with `count: 1`, `results: [{id, name}]` — the mock accepts any name (from-scratch fake, DD-213), no fixture template list to keep in sync. |
| TC-U-561 | The `workflow_job_templates` lookup endpoint independently returns the same real `{count, results}` shape for any name | REQ-TB-080 | Direct HTTP call to `GET /v2/workflow_job_templates/?name={name}` (not via the Go client's fallback logic — since the mock's `job_templates` endpoint always matches by design, `aap.Client.GetTemplateByName` never actually falls through to this one in practice) — proves the handler itself is correct and available for any caller that does address it directly, even though `LaunchWorkflowTemplate` is consequently never exercised by this phase's `ClusterOrder` reconciliation path. |
| TC-U-562 | `LaunchJobTemplate`/`LaunchWorkflowTemplate` each return a unique, incrementing job ID | REQ-TB-080 | `POST /v2/{job_templates\|workflow_job_templates}/{id}/launch/` with an `extra_vars` body returns `200`/`201` with `{"id": N}`; two successive launches never reuse an ID (matters for `GetJob`/`CancelJob` to unambiguously address one job). |
| TC-U-563 | `LaunchJobTemplate` accepts an arbitrary `extra_vars` payload without validating its shape | REQ-TB-080, NFR-TB-030 | POSTs a body containing the real `osac_job_vars.resource`-shaped payload `osac-operator`'s `extractExtraVars` sends — asserts `200`, not a schema-validation rejection; the mock is not a schema validator (NFR-TB-030's "not a thin wrapper" framing). |
| TC-U-564 | `GetJob` reports a launched job as `status: "successful"` immediately, with `started`/`finished` both populated | REQ-TB-080, REQ-TB-100, DD-214 | `GET /v2/jobs/{id}/` on a job launched via TC-U-562 returns `200` with `status: "successful"` on the very first call — no pending/running transition (DD-214) — matching the exact string `osac-operator/pkg/provisioning/aap_provider.go`'s `mapAAPStatusToJobState` maps to `JobStateSucceeded`. |
| TC-U-565 | `GetJob` on an unknown job ID returns a real `404` | REQ-TB-080 | Proves the real client's `NotFoundError` path (`doRequest`'s `resp.StatusCode == 404` branch) is genuinely exercised, not just assumed — mirrors this plan's general "real failure paths must be detectable" discipline (echoes AC-TB-020's spirit for the AAP layer). |
| TC-U-566 | `CanCancelJob` reports `can_cancel: true` for a just-launched (non-terminal) job | REQ-TB-080 | `GET /v2/jobs/{id}/cancel/` → `200` `{"can_cancel": true}` before any cancel/terminal-status transition. |
| TC-U-567 | `CancelJob` on a non-terminal job returns `202` with an empty body, and the job's subsequent `GetJob` reports `status: "canceled"` | REQ-TB-080 | `POST /v2/jobs/{id}/cancel/` → `202`; a follow-up `GetJob` reflects the cancellation — needed for `AAPProvider.cancelProvisionJob`'s 202/405 branching (`pkg/provisioning/aap_provider.go`) to be exercised both ways. |
| TC-U-568 | `CancelJob` on an already-terminal job returns `405`, not a silent success | REQ-TB-080, DD-214 | Calling cancel twice: second call returns `405` (mirrors `MethodNotAllowedError`) — deliberately fail-safe rather than permissive, since a silently-succeeding cancel would make the mock more forgiving than real AAP and lose fidelity value for `AAPProvider.isReadyForDeprovision`'s 405-detection branch. |
| TC-U-569 | Every endpoint rejects a request with no `Authorization` header at all, with a real `401` | REQ-TB-080, NFR-TB-030, DD-225 | A request with no `Authorization` header gets `401`, not a silent pass-through — this mock enforces a shared-secret Bearer token (DD-225) rather than accepting anything, so a missing/misconfigured token fails the same way it would against real AAP. |
| TC-U-570 | `LoadConfig` loads `MOCK_AAP_ADDRESS` from the environment | REQ-TB-080 | Same `caarlos0/env`-based config-loading pattern as `test/mockprovider.Config`. |
| TC-U-571 | `LoadConfig` fails fast when `MOCK_AAP_ADDRESS` is missing | REQ-TB-080 | Matches `internal/config.Load()`'s fail-fast convention. |
| TC-U-572 | `GetJob`/`CanCancelJob`/`CancelJob` all reject a non-numeric job ID with `400` | REQ-TB-080 | Table-driven across all 3 job-scoped endpoints — a malformed ID must not panic or silently match an unintended route. |
| TC-U-573 | `CanCancelJob`/`CancelJob` both return `404` for an unknown job ID | REQ-TB-080 | Matches `GetJob`'s own not-found behavior (TC-U-565), table-driven across both endpoints. |
| TC-U-574 | Every endpoint rejects a request whose Bearer token doesn't exactly match the configured one, with a real `401` | REQ-TB-080, NFR-TB-030, DD-225 | Proves the mock validates the token's *content*, not just its presence — a wrong token gets the same `401` as a missing one. |
| TC-U-575 | `LoadConfig` loads `MOCK_AAP_TOKEN` from the environment | REQ-TB-080, DD-225 | Same config-loading pattern as `MOCK_AAP_ADDRESS` (TC-U-570). |
| TC-U-576 | `LoadConfig` fails fast when `MOCK_AAP_TOKEN` is missing | REQ-TB-080, DD-225 | Matches `internal/config.Load()`'s fail-fast convention. |

`test/cmd/osac-aap-mock`'s own `main.go` wiring (config-load/listener-bind
error wrapping, `serveUntilDone`'s shutdown/failure branches) is covered by
TC-U-580..585, and a real-listener end-to-end lifecycle smoke test by
TC-I-090 — same split and same techniques as
`osac-sp-e2e-mock-provider.test-plan.md`'s own `test/cmd/osac-mock-provider`
coverage (TC-U-144..151, TC-I-031), not re-tabulated here in full since the
pattern is identical; see `test/cmd/osac-aap-mock/main_unit_test.go` and
`main_integration_test.go` directly.

---

## 7. Phase 2: real provisioning fidelity (`ClusterOrder` via real `osac-operator` + `osac-aap-mock`; `BareMetalInstance` via real BMFO)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-TB-060 | `osac-operator`, BMFO, and `osac-aap-mock` all reach `Ready` within the bounded wait, on top of Phase 1's already-`Ready` stack | REQ-TB-070, AC-TB-030 (given clause) | Same readiness-polling discipline as TC-TB-010, extended to the 3 new Phase 2 pods plus all 8 vendored CRDs' existence (`kubectl get crd`) — the workflow installs all 8 (the original 4 plus `BareMetalPool`/`ComputeInstance`/`NodePool`/`BareMetalHost`, added once startup broke without them, DD-220/222) and this asserts every one of them, not just the original 4 — assert this completes within NFR-TB-010's overall 25-minute budget. |
| TC-TB-080 | The e2e suite creates a `ClusterOrder` CR directly against the kind cluster's own API server | REQ-TB-070, REQ-TB-100 | Not yet routed through `osac-sp`/`fulfillment-service`'s dispatch chain — see DD-218, tracked in [#47](https://github.com/dcm-project/osac-service-provider/issues/47). Asserts the CR is accepted (the fixture-grade CRD, DD-213-adjacent precedent, has no schema to reject it) and osac-operator's `ClusterOrderReconciler` picks it up (a non-empty `.status.phase` appears) before the terminal-state wait begins. |
| TC-TB-090 | That `ClusterOrder` CR reaches a real terminal `Ready` phase, driven by real `osac-operator` reconciliation through `osac-aap-mock` | REQ-TB-080, REQ-TB-100, AC-TB-030 | The core Phase 2 deliverable: polls the CR's `.status.phase` until `Ready` (bounded wait) or fails with the CR's full `.status` (conditions, `provisioningJobs`) for CI triage — not a bare timeout. Real reconciliation, real HyperShift-shaped `HostedCluster` CR creation (fixture-grade per issue #44's comment), real AAP dispatch to `osac-aap-mock`, all exercised for real; only the literal AAP job execution is faked (NFR-TB-030). Beyond the phase check, also asserts the exact `Progressing=False`/reason `"AsExpected"` condition and a `.status.provisioningJobs` entry with `type: "provision"`/`state: "Succeeded"` — both set only by osac-operator's real `provisioningCallbacks.OnSuccess` path (verified against real upstream source, DD-230), so a reconciler that flipped `.status.phase` to `Ready` via some other path would fail these even though it'd pass the bare phase check. |
| TC-TB-110 | A `BareMetalInstance` with `runStrategy` unset, backed by a static `BareMetalHost` fixture, reaches a real terminal `Ready` phase | REQ-TB-110, AC-TB-040 | Mirrors TC-TB-080/090's two-step pattern (apply fixture, then poll `.status.phase`) as a single `It` — no separate "reconciler picked it up" intermediate assertion is needed here since the bounded `Ready`-or-timeout wait already fully covers it. Proves BMFO's `metal3` backend allocates the fixture host and drives the CR to `Ready` with zero real hardware/BMC simulation (DD-226/227). |
| TC-TB-120 | A `BareMetalInstance` with `runStrategy: Always`, backed by its own static `BareMetalHost` fixture, reaches `Ready` only after the suite patches that host's `status.poweredOn` | REQ-TB-110, AC-TB-040 | Exercises the power-synced condition path (`reconcilePower`/`SetPowerState`) that TC-TB-110 never touches. First asserts the CR is genuinely stuck in `Progressing` (`PowerSynced=False`, "node power state is transitioning") *before* the patch — proving the power-sync condition actually gates `Ready`, not a vestigial/never-blocking check — then patches the fixture `BareMetalHost`'s `status.poweredOn: true` via `--subresource=status` (simulating a real `baremetal-operator`'s completed power-on) and asserts the CR converges to `Ready` on the next reconcile (DD-226/227). Once `Ready`, also asserts the exact `PowerSynced=True`/reason `"PowerOn"` condition and `.status.runStrategy: "Always"` — both set only by BMFO's real `syncBareMetalInstanceStatus` once it has actually re-read the host's converged power state (verified against real upstream source, DD-230), not just inferred from the bare phase transition. |
| TC-TB-130 | A `BareMetalInstance` whose `hostType` matches zero `BareMetalHost` fixtures converges to a terminal `Failed` phase, not an indefinite `Progressing`/`Allocating` | REQ-TB-120, AC-TB-050 | Asserts the exact `Allocated=False`/reason `"Failed"`/message `"No matching hosts available"` condition BMFO's `reconcileInventory` sets on its zero-candidates branch (`internal/controller/baremetalinstance_controller.go`, verified against real upstream source, DD-229) — not just "never became Ready", which could also pass for a hung reconciler. |
| TC-TB-140 | A `BareMetalInstance` whose only matching `BareMetalHost` has a non-`OK` `operationalStatus` converges to the same terminal `Failed` phase as TC-TB-130, never allocated | REQ-TB-120, AC-TB-050 | Distinct regression class from TC-TB-130: a host of the right type *exists* but must still be excluded — proves BMFO's `FindFreeHost` candidate filter (`OperationalStatus == OK`), not just the "zero hosts of this type at all" path. A BMFO regression that dropped this filter would pass TC-TB-130 but fail this one. |
| TC-TB-150 | Two `BareMetalInstance`s racing for one available `BareMetalHost` converge to exactly one `Ready` and one `Failed` — never both `Ready` | REQ-TB-120, AC-TB-050 | Both instances' `runStrategy` is left unset (mirrors TC-TB-110, zero power steps) so the only variable under test is host contention. Grounded directly in `AssignHost`'s real double-claim guard (`bmh.Spec.ConsumerRef != nil && ...Name != bareMetalInstanceID` returns `nil, nil`, DD-229) — the loser clears its own `ExternalHostID` and retries `FindFreeHost`, which now excludes the claimed host, converging to the same zero-candidates `Failed` path as TC-TB-130. Polls both CRs' `.status.phase` together rather than asserting a specific one wins (which instance wins the race is intentionally not deterministic). |
| TC-TB-160 | Deleting a `Ready` `BareMetalInstance` releases its `BareMetalHost` (`spec.consumerRef` cleared) so it becomes reassignable again | REQ-TB-120, AC-TB-050 | Uses its own dedicated fixture pair, independent of TC-TB-110/120/150, so Ginkgo's randomized spec order can't make this delete race with another spec's still-in-use instance. `kubectl delete` blocks until BMFO's `handleDeletion` finalizer cleanup (`UnassignHost`) actually completes (DD-229), so the release assertion needs no extra `Eventually`. |

Note: an earlier draft of this plan (superseded) had a `TC-TB-100` ("BMFO
deploys and stays healthy with no `BareMetalInstance` CR present") as a
deploy-only regression check, placeholder for the fact that nothing in the
suite could yet drive `BareMetalInstance` reconciliation (DD-216). It is
retired, not renumbered, now that TC-TB-110/120 exist: its "zero CRs
present" premise is no longer true once those specs run, and its
`deploymentReady("bmf-operator-controller-manager")` half was already fully
duplicated by TC-TB-060. A passing TC-TB-110/120 already implies BMFO
deployed and stayed healthy — a strictly stronger claim than TC-TB-100 ever
made.

---

## 8. Coverage Matrix

| Spec Topic | REQ Count | AC Count | TC Count | Notes |
|---|---|---|---|---|
| Infra bring-up/readiness (Phase 1) | REQ-TB-010 | AC-TB-010 (given) | 1 (TC-TB-010) | Gates every other TC in this plan. |
| Realm/claim correctness | REQ-TB-020 | — | 1 (TC-TB-020) | Verified directly against Keycloak, independent of `osac-sp`, before the harder end-to-end assertion. |
| Real auth success (osac-sp) | REQ-TB-030, REQ-TB-040 | AC-TB-010 | 1 (TC-TB-030) | The primary positive-path deliverable — closes DD-132's gap. |
| Pinned-tag CI hygiene | REQ-TB-050 | — | 1 (TC-TB-040) | Static/lint-shaped, not a runtime Ginkgo spec. |
| Real auth failure detection | REQ-TB-060 | AC-TB-020 | 1 (TC-TB-050) | Opt-in `workflow_dispatch` variant, matching TC-E2E-080's precedent (avoids doubling steady-state PR runtime). |
| `osac-aap-mock` unit coverage | REQ-TB-080 | — | 17 (TC-U-560..576) | Counts toward the repo's 100%-unit-coverage gate, unlike the `TC-TB-*` rows below. |
| Phase 2 infra/terminal-state (`ClusterOrder` + `BareMetalInstance`, direct CR create) | REQ-TB-070, REQ-TB-080, REQ-TB-100, REQ-TB-110 | AC-TB-030, AC-TB-040 | 5 (TC-TB-060/080/090/110/120) | The Phase 2 deliverable — real reconciliation through a real AAP-layer fake (`ClusterOrder`) and real BMFO against a static host fixture (`BareMetalInstance`, DD-226/227), both via a direct CR create rather than an `osac-sp`-driven one (DD-218, #47). Supersedes the retired `TC-TB-100` deploy-only placeholder (DD-216 is now fully resolved, not just partially). |
| `BareMetalInstance` fail-safe/release paths | REQ-TB-120 | AC-TB-050 | 4 (TC-TB-130/140/150/160) | Negative-path complement to TC-TB-110/120's happy path — no host, ineligible host, contended host, and delete-time release — all verified against BMFO's real upstream source before being written (DD-229), not assumed from the happy-path behavior. |
| **Total** | 10 | 5 | **31** | |

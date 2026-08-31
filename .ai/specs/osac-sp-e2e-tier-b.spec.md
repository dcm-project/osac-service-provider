# Specification: Tier B e2e — real OSAC stack, phased through full provisioning fidelity

## 1. Overview

Follow-up to [osac-service-provider#17](https://github.com/dcm-project/osac-service-provider/issues/17)
/ [`osac-sp-e2e-suite.spec.md`](./osac-sp-e2e-suite.spec.md) ("Phase A" e2e,
now merged as [#20](https://github.com/dcm-project/osac-service-provider/pull/20)),
which validates the `osac-sp` ↔ `control-plane` contract against real
`control-plane` but a **hand-written mock** (`osac-mock-provider`) standing
in for the entire OSAC backend. That mock never validates auth at all
([DD-132](../decisions/osac-sp.decisions.md)) — a real, deliberate gap for
Phase A's scope, but one that means Phase A's green CI proves nothing about
whether `osac-sp`'s real OIDC client-credentials flow and gRPC calls would
actually be *accepted* by real OSAC.

This spec defines **Tier B**: closing that gap by running real OSAC
components instead of our own mock, phased to land incrementally alongside
`osac-sp`'s own milestone roadmap rather than built all at once, but
**designed end-to-end now** so later phases don't require re-architecting
earlier ones. This was explicitly requested: the goal is for the e2e
infrastructure to already cover real OSAC provisioning fidelity by the time
`osac-sp`'s own CRUD milestones (M3–M5) complete, not to bolt that on
afterward.

**Relationship to [#19](https://github.com/dcm-project/osac-service-provider/pull/19)'s spike:**
that spike proved `osac-project/fulfillment-service`'s own `it` Go package
(a live external dependency importing their internal integration-test
harness) is technically importable. This spec deliberately does **not**
build on that approach — see DD-149 for why — and instead vendors the
specific, minimal pieces needed (a static Keycloak realm config; pinned,
published image/chart tags) so this repo owns and controls its own e2e
infrastructure rather than depending on upstream's internal test tooling,
which can change out from under us without notice.

**Relationship to issue #17's stated scope boundary:** issue #17 explicitly
scoped Phase A as "mocked provider backend only," noting *"confirmed with
the team: `control-plane` will own the e2e integration matrix against real
providers across all SPs... this issue is explicitly not attempting to take
on that broader, heavier responsibility."* Tier B is a narrower thing than
that broader cross-SP matrix: it's `osac-sp`-repo-owned, `osac-sp`↔OSAC-pair-specific,
stays entirely within `kind` (no OCP, no nested virtualization), and doesn't
block on or duplicate `FLPATH-4760` (`control-plane`/QE's OCP-gated,
cross-SP real-provider tier, still unscheduled at spec-writing time). It's
scoped narrowly enough to be this repo's own reasonable extension of Phase
A, not a preemption of that broader initiative — worth flagging explicitly
to `control-plane`/QE once built, so the two efforts stay aware of each
other rather than silently diverging.

**Time-sensitive fact this spec depends on:** `osac-project/fulfillment-service`,
`osac-operator`, `bare-metal-fulfillment-operator` (BMFO), and `osac-aap`
were all archived on 2026-08-15 (corrected — a prior draft of this note
said 2026-08-04; re-verified directly via `gh api /repos/osac-project/<repo>`,
all four show `archived: true`, `pushed_at` within the same
2026-08-15T17:54–18:06Z window) and merged into a new monorepo,
**`osac-project/osac`**, as subdirectories of the same name. Content was
byte-identical to the archived repos at merge time (verified). All source
references, sparse-checkouts, and image/chart names below already assume
the monorepo location; nothing in this spec depends on the archived repos
still being reachable.

**Also confirmed (2026-08-26):** the monorepo's own CI publishes to the
*same* registry coordinates the archived repos used
(`ghcr.io/osac-project/osac-operator`,
`ghcr.io/osac-project/bare-metal-fulfillment-operator`,
`oci://ghcr.io/osac-project/charts/*` — hardcoded literals in the
monorepo's workflows, not derived from its own repo name), and keeps
releasing new versions there (e.g. `bare-metal-fulfillment-operator/v0.0.12`,
2026-08-21 — genuinely post-archival). Phase 2 implementation MUST pin
against the monorepo's own releases, not whatever version the archived
repos last published before the freeze — those are permanently frozen at
`v0.0.10` for both operators and will never receive another update.

**Reference documents:**

- [osac-service-provider#17](https://github.com/dcm-project/osac-service-provider/issues/17) —
  original e2e scope boundary this spec extends
- [`osac-sp-e2e-suite.spec.md`](./osac-sp-e2e-suite.spec.md) — Phase A,
  already merged; Tier B replaces only `osac-mock-provider`'s role, keeping
  `control-plane`+`osac-sp` deployment as-is
- [PR #19](https://github.com/dcm-project/osac-service-provider/pull/19) —
  the spike that established `it.NewTool()` is importable (informational;
  not depended on by this spec's chosen approach, see DD-149)
- `osac-project/osac`'s `fulfillment-service/it/charts/keycloak/files/realm.json` —
  source of the vendored realm config (§2, Phase 1)
- `osac-project/osac`'s `fulfillment-service/docs/INSTALL.md` — authoritative
  production wiring reference for `charts/service`'s `auth`/`idp`/`database` values
- `osac-project/osac`'s `osac-operator/pkg/provisioning/{provider,aap}.go` —
  the `ProvisioningProvider`/`AAPClient` interfaces Phase 2's `aap-mock`
  must satisfy (public, cross-repo-stable; concrete fakes are private
  `_test.go`-only and not reusable — confirmed, nothing upstream to import)

---

## 2. Architecture

Two phases, gated on `osac-sp`'s own milestone completion. **Phase 1 is
fully implemented.** Phase 2 has landed real `ClusterOrder` reconciliation
(`osac-operator` + `osac-aap-mock`, per REQ-TB-070/080/100) but is still
ongoing — `BareMetalInstance`'s terminal-state proof is deferred to
[#46](https://github.com/dcm-project/osac-service-provider/issues/46), and
routing creation through `osac-sp`'s own `Create` +
`fulfillment-service`'s `Hub` dispatch (rather than this phase's direct
`ClusterOrder` CR create) is deferred to
[#47](https://github.com/dcm-project/osac-service-provider/issues/47).

### Phase 1 (buildable now — no `osac-sp` code changes needed, M1 scope)

```
kind cluster
├── dcm-postgres, dcm-nats, dcm-control-plane   (unchanged from Phase A)
├── osac-service-provider                       (unchanged from Phase A, this repo's own manifest)
├── cert-manager        (NEW — upstream release manifest; hard prerequisite of fulfillment-service's own chart, all variants — see DD-151)
├── ffs-postgres        (NEW — plain manifest, this repo's own; 2 DBs: keycloak, service)
├── ffs-keycloak        (NEW — plain manifest; official Keycloak image + vendored realm.json)
└── ffs-fulfillment-service (NEW — real published chart, `oci://ghcr.io/osac-project/charts/fulfillment-service`, pinned `--version`, `variant: kind`; replaces osac-mock-provider — see DD-151)
```

- `osac-mock-provider` (Phase A) is **removed** from the stack in Tier B
  runs; `osac-sp`'s `SP_OSAC_*` env vars point at `ffs-fulfillment-service`
  and `ffs-keycloak` instead.
- No `osac-operator`/BMFO/AAP anywhere yet — matches upstream's own `it`
  package's scope exactly (their controller reconciles `ClusterOrder`/
  `BareMetalInstance` CRs for real, but nothing downstream watches them).
  Since `osac-sp` at M1 only calls `Capabilities` (health-check probe), it
  never creates those CRs at all — this phase's absence of provisioning has
  zero effect on what's assertable.
- Closes the auth-fidelity gap for exactly what M1 exercises: `osac-sp`'s
  real OIDC client-credentials token fetch and gRPC dial must be **accepted**
  by real Keycloak + real OSAC gRPC auth interceptor, not a mock that never
  checks either (DD-132).

### Phase 2 (specified now, built once `osac-sp` M2+ CRUD lands)

```
kind cluster
├── (everything from Phase 1)
├── ffs-osac-operator      (NEW — pinned image/chart, real reconciliation of ClusterOrder)
├── ffs-bmfo               (NEW — pinned image/chart, real reconciliation of BareMetalInstance)
└── ffs-aap-mock           (NEW — this repo's own binary, test/cmd/osac-aap-mock/, analogous to osac-mock-provider)
```

- `osac-operator`/BMFO run **for real**, unmodified — their AAP client is
  wired at runtime from env vars (`createAAPProviderFromEnv`), not compiled
  in, so pointing it at our own fake requires zero upstream code changes.
- `ffs-aap-mock` implements just enough of AAP's REST surface
  (`GetTemplate`, `LaunchJobTemplate`/`LaunchWorkflowTemplate`, `GetJob`,
  `CancelJob`) for real reconciliation loops to reach a terminal
  `Ready`/`Complete` state — the literal hardware/cloud-provisioning
  boundary is the *only* thing faked; every DCM-and-OSAC-owned business
  decision above it runs for real.
- Closes full provisioning fidelity: `osac-sp`'s eventual `Create`/`Get`
  calls (M3/M4) can be asserted all the way through to a real CR reaching
  `Ready`, not just "the gRPC call was accepted."

### Vendoring plan (Phase 1, applies to Phase 2's new components too)

| Item | Approach | Rationale |
|---|---|---|
| Keycloak realm/clients (`organization`/`groups`/`realm_access.roles` claim mappers, `osac-admin`/`osac-controller` client-credentials clients) | **Copy** the static `realm.json` (or equivalent `KeycloakRealmImport` YAML) into this repo, `test/e2e/tierb-config/realm.json` | Small, static, exactly what's needed; avoids any live dependency on upstream's chart/templating |
| Keycloak deployment | Official Keycloak image, our own plain manifest, `--import-realm` against the vendored file | Upstream's own chart is unpublished and explicitly dev-only (own README's disclaimer); the Operator-based production path is unnecessary weight for a throwaway CI cluster |
| Postgres (for Keycloak + fulfillment-service DBs) | Our own manifest, same pattern as `control-plane`'s Postgres | Upstream's own IT-tier Postgres chart is generic/disposable — nothing OSAC-specific to vendor |
| `fulfillment-service`, `osac-operator`, BMFO | Pin real, versioned images (`ghcr.io/osac-project/fulfillment-service:vX.Y.Z`, etc.) and their published OCI Helm charts directly — **no source build, no `it` package Go dependency** | These are genuine, stable, versioned upstream artifacts (confirmed: 80+ real semver tags on GHCR) — treating them as external dependencies is the same posture already used for `control-plane`'s image in Phase A |
| `osac-aap-mock` (Phase 2 only) | New, hand-written binary in this repo, `test/cmd/osac-aap-mock/` (DD-224: test-only binaries stay out of repo-root `cmd/`) | No reusable upstream artifact exists (confirmed — §4/DD-152) |

---

## 3. Requirements

### Phase 1

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-TB-010 | The Tier B workflow MUST deploy real Postgres, real Keycloak (official image + vendored realm import), and real `fulfillment-service` (pinned image + chart) in place of `osac-mock-provider`, leaving `control-plane`+`osac-sp` deployment unchanged from Phase A | MUST | |
| REQ-TB-020 | The vendored Keycloak realm config MUST define `client_credentials`-capable clients (`osac-admin`, `osac-controller`) with the same custom `clientScopes` (`osac-api`, `username`, `groups`) real OSAC's own production install doc defines. Issued tokens for these clients MUST carry a `username` claim and an `osac-api` audience claim; the `groups` scope/mapper MUST be present in the realm config, but is not itself a guaranteed claim on every issued token — a service account with no group memberships MUST NOT be expected to carry a `groups` claim (Keycloak's `oidc-group-membership-mapper` omits the claim entirely, not an empty array, in that case) | MUST | Source: `fulfillment-service/docs/INSTALL.md`'s `KeycloakRealmImport` example — corrected from an earlier, unverified assumption (`organization`/`realm_access.roles`); further corrected (empty-`groups` semantics) after a live spike, see DD-150 |
| REQ-TB-030 | `osac-sp`'s `SP_OSAC_OIDC_ISSUER_URL`/`SP_OSAC_OIDC_CLIENT_ID`/`_SECRET`/`SP_OSAC_FULFILLMENT_ADDRESS` MUST point at the real `ffs-keycloak`/`ffs-fulfillment-service` services, with credentials matching a real vendored client | MUST | |
| REQ-TB-040 | The e2e suite MUST assert `osac-sp`'s health endpoints report real, successful OIDC token acquisition and gRPC `Capabilities` connectivity against real OSAC — not just against the Phase A mock | MUST | Same assertions as `AC-E2E-030`, re-run against the real backend |
| REQ-TB-050 | The workflow MUST pin exact `vX.Y.Z` image/chart tags for every OSAC component (never `main`/`latest`) | MUST | Upstream's own `check-floating-tags.yaml` CI guard confirms `main`/`latest` are untrusted as "current" |
| REQ-TB-060 | A deliberately wrong/missing client credential MUST result in `osac-sp` reporting `unhealthy` with an auth-failure detail — proving Tier B can actually detect what Phase A's permissive mock structurally cannot | MUST | The core deliverable this tier exists for |

### Phase 2 (REQ-TB-090's M2+ gate satisfied; implementation landing, ongoing — see #46, #47)

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-TB-070 | The Tier B workflow MUST additionally deploy real `osac-operator` and real BMFO (pinned images/charts) on the same `kind` cluster's own API server. `osac-operator` MUST be configured to reconcile `ClusterOrder` CRs for real, through `osac-aap-mock`. BMFO's deployment MUST succeed (proving no CRD/RBAC/chart-install regressions), but its `BareMetalInstance` reconciliation is deploy-only for this phase — see the REQ-TB-100 note | MUST | Requires installing the 4 CRD schemas `fulfillment-service`'s own `it` package uses (`ClusterOrder`, `HostedCluster`, `Tenant`, `BareMetalInstance`), plus 4 more (`BareMetalPool`, `ComputeInstance`, `NodePool`, `BareMetalHost`) added once `osac-operator`/BMFO startup broke without them — 8 total, see `test/e2e/manifests-tierb/crds/README.md` (DD-220/222) |
| REQ-TB-080 | `test/cmd/osac-aap-mock/` MUST implement `GetTemplate`, `LaunchJobTemplate`/`LaunchWorkflowTemplate`, `GetJob`, and `CancelJob` against `osac-operator/pkg/aap.Client`'s real REST contract, sufficient for real reconciliation loops to drive a `ClusterOrder` to a terminal success state | MUST | Exact request/response shapes resolved directly from source before implementation — see DD-213..215 |
| REQ-TB-090 | Phase 2 implementation MUST NOT begin until `osac-sp` itself has real `Create`/`Get` CRUD dispatch for at least one resource type (M2+) — there is nothing for Phase 2's assertions to exercise before then | MUST | Gate, not a deferral-without-reason |
| REQ-TB-100 | The e2e suite MUST create a `ClusterOrder` CR and assert it reaches a real terminal `Ready`/`Complete` status, driven entirely by real OSAC reconciliation logic through `osac-aap-mock` | MUST | **Scope correction (this landing, DD-216/DD-218):** originally worded to (a) cover `BareMetalInstance` too, and (b) drive the CR via an `osac-sp`-initiated `Create` through `fulfillment-service`'s real dispatch chain (its own `Hub` mechanism). (a) is split to [#46](https://github.com/dcm-project/osac-service-provider/issues/46) — `BareMetalInstance` needs a real host-allocation backend `osac-aap-mock` can't substitute for. (b) is split to [#47](https://github.com/dcm-project/osac-service-provider/issues/47) — `fulfillment-service`'s `Hub` registration is a stateful, multi-step CLI flow with several details (TLS mode, image/tag, cross-invocation config persistence) that need their own live-CI verification pass rather than a first attempt inside this already-large PR. This phase creates the `ClusterOrder` CR directly against the cluster's own API server instead, proving real `osac-operator` reconciliation + `osac-aap-mock` reach `Ready` — the actually-novel thing this phase introduces |

---

## 4. Acceptance Criteria

##### AC-TB-010: `osac-sp` is genuinely authenticated by real OSAC, not a permissive mock

- **Validates:** REQ-TB-010, REQ-TB-020, REQ-TB-030, REQ-TB-040
- **Given** the Phase 1 stack (real Postgres+Keycloak+fulfillment-service in
  place of `osac-mock-provider`)
- **When** `osac-sp` performs its real OIDC client-credentials token fetch
  and gRPC `Capabilities` probe
- **Then** both succeed against the real backend, and `osac-sp`'s own health
  endpoints report `status: healthy` reflecting that real success

##### AC-TB-020: A real auth failure is genuinely detectable

- **Validates:** REQ-TB-060
- **Given** the Phase 1 stack, but `osac-sp` configured with a client secret
  that doesn't match any vendored Keycloak client
- **When** `osac-sp` attempts its token fetch
- **Then** it fails, and `osac-sp`'s health endpoint reports `status:
  unhealthy` with an auth-failure detail — proving this tier can catch what
  Phase A structurally cannot

##### AC-TB-030 (Phase 2): A real `ClusterOrder` reaches a real terminal state

- **Validates:** REQ-TB-070, REQ-TB-080, REQ-TB-100
- **Given** the Phase 2 stack (real `osac-operator` + `osac-aap-mock`; BMFO
  also deployed per REQ-TB-070 but not exercised to a terminal state this
  phase — see REQ-TB-100's note and [#46](https://github.com/dcm-project/osac-service-provider/issues/46))
- **When** the e2e suite creates a `ClusterOrder` CR directly against the
  `kind` cluster's own API server (not yet routed through an
  `osac-sp`-initiated `Create` + `fulfillment-service`'s `Hub` dispatch —
  see REQ-TB-100's note and [#47](https://github.com/dcm-project/osac-service-provider/issues/47))
- **Then** real `osac-operator` reconciliation and `osac-aap-mock` cooperate
  to drive that CR to a real terminal `Ready` status, observable via the
  Kind cluster's own API server

---

## 5. Non-Functional Requirements

| ID | Requirement |
|----|-------------|
| NFR-TB-010 | Phase 1's additional components (Postgres, Keycloak, fulfillment-service) MUST keep total stack resource usage comfortably inside the free-tier runner's 16 GB RAM / 4 vCPU budget, alongside Phase A's existing components (NFR-E2E-010) |
| NFR-TB-020 | No new secrets/credentials beyond what's already public MUST be required — Keycloak realm/client secrets are test-only, checked into this repo, never used for anything but this throwaway `kind` cluster |
| NFR-TB-030 | Phase 2's `osac-aap-mock` MUST NOT depend on any real Ansible/AAP/hardware access — it is the literal replacement for that boundary, not a thin wrapper around it |

---

## 6. Open items (remaining before Phase 2 is fully complete)

- ~~**Exact `aap.Client` REST contract**~~ — resolved: read directly from
  `osac-operator/pkg/aap/client.go` and `pkg/provisioning/aap_provider.go`
  (job-status-to-`JobState` mapping) in the monorepo; `osac-aap-mock`
  implements that contract (DD-213/214).
- ~~**CRD schema sourcing**~~ — resolved, though the final count grew from 4
  to 8: the original 4 are vendored verbatim from `fulfillment-service/it/crds/`,
  the other 4 (`BareMetalPool`/`ComputeInstance`/`NodePool`/`BareMetalHost`)
  were added once `osac-operator`/BMFO startup broke without them
  (DD-220/222) — all in `test/e2e/manifests-tierb/crds/` (see that
  directory's own `README.md` for which are real vs. fixture-grade).
- **`BareMetalInstance`'s terminal-state proof** — deferred to
  [#46](https://github.com/dcm-project/osac-service-provider/issues/46); this
  landing only proves BMFO deploys and stays healthy (TC-TB-100), not that it
  can drive a `BareMetalInstance` CR to `Ready` (see REQ-TB-100's note).
- **`osac-sp`-initiated `Create` → `fulfillment-service` `Hub` dispatch** —
  deferred to [#47](https://github.com/dcm-project/osac-service-provider/issues/47);
  this landing creates the `ClusterOrder` CR directly against the cluster's
  own API server instead (see REQ-TB-100's note).
- **Realm secret rotation risk**: the vendored `realm.json`'s client
  secrets are static and checked into git — acceptable for a throwaway
  `kind` cluster (NFR-TB-020), but worth a one-line comment at the vendor
  site making that explicit so it's never mistaken for a real-world secret
  pattern.

## 7. Explicitly out of scope

- **`FLPATH-4760`** (`control-plane`/QE's OCP-gated, cross-SP real-provider
  integration matrix) — this spec's Tier B is `osac-sp`-repo-owned and
  `kind`-only; it doesn't block on, replace, or duplicate that initiative.
  Worth surfacing this spec to `control-plane`/QE once Phase 1 lands so the
  two stay aware of each other.
- **Real hardware/cloud provisioning** — genuinely impossible in CI;
  `osac-aap-mock` (Phase 2) is the permanent stand-in for this boundary, not
  a temporary one to later replace with the real thing.
- **NATS status-event round-trip** (Milestone 5) — tracked in
  [`osac-sp-e2e-suite.spec.md`](./osac-sp-e2e-suite.spec.md) §6, orthogonal
  to this spec.

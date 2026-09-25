# Specification: Tier B e2e — real OSAC stack, phased through full provisioning fidelity

> **Status: Canonical active E2E specification (DD-237).** The former Phase A
> control-plane/mock-provider workflow is retired; this Tier B topology is the
> only E2E workflow maintained by this repository.

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
- [`osac-sp-e2e-suite.spec.md`](./osac-sp-e2e-suite.spec.md) — shared e2e
  requirements and Phase A baseline; Tier B replaces `osac-mock-provider` and
  the legacy registration target with real OSAC and environment-agent
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
fully implemented.** Phase 2 has landed real `ClusterOrder` **and
`BareMetalInstance`** reconciliation (`osac-operator` + BMFO +
`osac-aap-mock`, per REQ-TB-070/080/100/110), including both direct CR
creation and the `osac-sp` `Create` → `fulfillment-service` Hub dispatch
path covered by TC-TB-200.

### Phase 1 (buildable now — no `osac-sp` code changes needed, M1 scope)

```
kind cluster
├── cert-manager              (upstream release manifest; hard prerequisite of fulfillment-service's own chart — see DD-151)
├── ffs-postgres              (plain manifest; 2 DBs: keycloak, service)
├── ffs-keycloak              (official Keycloak image + vendored realm.json)
├── ffs-fulfillment-service   (real published chart, `oci://ghcr.io/osac-project/charts/fulfillment-service`, pinned `--version`)
├── environment-agent         (real binary, built from the pinned e2e module dependency)
├── nats                      (JetStream broker required by environment-agent and osac-sp)
├── osac-service-provider     (this repo's own manifest; wired to environment-agent + ffs stack)
└── (Phase 2 additions below)
```

**Phase 1 to Phase 2 migration (DD-203, PR #59):**
- `dcm-control-plane` removed from workflow (replaced by environment-agent as registration target)
- `osac-mock-provider` removed (replaced by real fulfillment-service)
- `environment-agent` and NATS run inside the kind cluster as real Deployments;
  the agent can therefore resolve and health-check `osac-service-provider`'s
  in-cluster Service endpoint
- `osac-sp` registration now targets `environment-agent` at
  `http://environment-agent:8090/api/v1alpha1`; the agent API is
  port-forwarded to the host only for test assertions
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
| REQ-TB-010 | The Tier B workflow MUST deploy real Postgres, real Keycloak (official image + vendored realm import), and real `fulfillment-service` (pinned image + chart) in place of `osac-mock-provider`. Phase 2 (DD-203): registration target migrated from `control-plane` to a real `environment-agent` Deployment in the kind cluster, with NATS available as its messaging dependency; `osac-sp` deployment wiring updated to match | MUST | Phase 2 per issue #44 |
| REQ-TB-020 | The vendored Keycloak realm config MUST define `client_credentials`-capable clients (`osac-admin`, `osac-controller`) with the same custom `clientScopes` (`osac-api`, `username`, `groups`) real OSAC's own production install doc defines, and the `tenant-admin` and `tenant-idp-manager` realm roles required by real Fulfillment Service tenant lifecycle operations. Issued tokens for these clients MUST carry a `username` claim and an `osac-api` audience claim; the `groups` scope/mapper MUST be present in the realm config, but is not itself a guaranteed claim on every issued token — a service account with no group memberships MUST NOT be expected to carry a `groups` claim (Keycloak's `oidc-group-membership-mapper` omits the claim entirely, not an empty array, in that case) | MUST | Source: `fulfillment-service/docs/INSTALL.md`'s `KeycloakRealmImport` example and `fulfillment-service/docs/AUTH.md`'s required realm roles — corrected from an earlier, unverified assumption (`organization`/`realm_access.roles`); further corrected (empty-`groups` semantics) after a live spike, see DD-150 |
| REQ-TB-030 | `osac-sp`'s `SP_OSAC_OIDC_ISSUER_URL`/`SP_OSAC_OIDC_CLIENT_ID`/`_SECRET`/`SP_OSAC_FULFILLMENT_ADDRESS` MUST point at the real `ffs-keycloak`/`ffs-fulfillment-service` services, with credentials matching a real vendored client; when the SP exercises private Secret retrieval (REQ-TB-130), its gRPC address MUST expose Fulfillment Service's private API | MUST | Phase 2 points `SP_OSAC_FULFILLMENT_ADDRESS` at the chart's internal gRPC endpoint, which exposes public and private methods; the external endpoint filters private methods |
| REQ-TB-040 | The e2e suite MUST assert `osac-sp`'s health endpoints report real, successful OIDC token acquisition and gRPC `Capabilities` connectivity against real OSAC — not just against the Phase A mock | MUST | Same assertions as `AC-E2E-030`, re-run against the real backend |
| REQ-TB-050 | The workflow MUST pin exact `vX.Y.Z` image/chart tags for every OSAC component (never `main`/`latest`) | MUST | Upstream's own `check-floating-tags.yaml` CI guard confirms `main`/`latest` are untrusted as "current" |
| REQ-TB-060 | The integration test suite MUST prove that an OIDC token endpoint rejection results in `osac-sp` reporting `unhealthy` with an auth-failure detail while OSAC remains reachable | MUST | Integration-tier-sufficient via TC-I-018; no separate real-Keycloak kind variant is required |
| REQ-TB-065 | The integration test suite MUST prove that valid OIDC credentials plus an unreachable OSAC gRPC endpoint result in `osac-sp` reporting `unhealthy` with a connectivity-only detail, distinct from an auth-failure detail | MUST | Integration-tier-sufficient via TC-I-012; no separate kind deployment is required |

### Phase 2 (REQ-TB-090's M2+ gate satisfied; implementation landing, ongoing — see #47)

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-TB-070 | The Tier B workflow MUST additionally deploy real `osac-operator` and real BMFO (pinned images/charts) on the same `kind` cluster's own API server. `osac-operator` MUST be configured to reconcile `ClusterOrder` CRs for real, through `osac-aap-mock`. BMFO's deployment MUST succeed (proving no CRD/RBAC/chart-install regressions) and MUST be configured to reconcile `BareMetalInstance` CRs for real — see REQ-TB-110 | MUST | Requires installing the 4 CRD schemas `fulfillment-service`'s own `it` package uses (`ClusterOrder`, `HostedCluster`, `Tenant`, `BareMetalInstance`), plus 4 more (`BareMetalPool`, `ComputeInstance`, `NodePool`, `BareMetalHost`) added once `osac-operator`/BMFO startup broke without them — 8 total, see `test/e2e/manifests-tierb/crds/README.md` (DD-220/222) |
| REQ-TB-080 | `test/cmd/osac-aap-mock/` MUST implement `GetTemplate`, `LaunchJobTemplate`/`LaunchWorkflowTemplate`, `GetJob`, and `CancelJob` against `osac-operator/pkg/aap.Client`'s real REST contract, sufficient for real reconciliation loops to drive a `ClusterOrder` to a terminal success state | MUST | Exact request/response shapes resolved directly from source before implementation — see DD-213..215 |
| REQ-TB-090 | Phase 2 implementation MUST NOT begin until `osac-sp` itself has real `Create`/`Get` CRUD dispatch for at least one resource type (M2+) — there is nothing for Phase 2's assertions to exercise before then | MUST | Gate, not a deferral-without-reason |
| REQ-TB-100 | The e2e suite MUST create a `ClusterOrder` CR and assert it reaches a real terminal `Ready`/`Complete` status, driven entirely by real OSAC reconciliation logic through `osac-aap-mock` | MUST | **Scope correction (DD-216/DD-218):** originally worded to (a) cover `BareMetalInstance` too, and (b) drive the CR via an `osac-sp`-initiated `Create` through `fulfillment-service`'s real dispatch chain (its own `Hub` mechanism). (a) is now covered by REQ-TB-110 — a live spike (DD-226/227) found #46's original premise wrong: `BareMetalInstance` does not need a real host-allocation backend, a static `BareMetalHost` fixture is sufficient. The direct-CR path remains covered by TC-TB-080/090, while TC-TB-200 covers the routed path with the `v0.0.107` CLI and its OSAC-4826 fix. |
| REQ-TB-110 | The e2e suite MUST create two `BareMetalInstance` CRs — one with `runStrategy` unset, one with `runStrategy: Always` — each backed by its own static `BareMetalHost` fixture, and assert each reaches a real terminal `Ready` status, driven entirely by real BMFO reconciliation | MUST | No real Metal3/Ironic/virtual-BMC infrastructure is required: BMFO's `metal3` inventory/management backends operate purely on Kubernetes-object fields they own (`BareMetalHost.status`/`spec.online`/the `reboot.metal3.io` annotation), never on a live BMC/Ironic endpoint — proven via live spike, see DD-226/227. Originally assumed to need real host-allocation infrastructure and split out to [#46](https://github.com/dcm-project/osac-service-provider/issues/46); that assumption was wrong |
| REQ-TB-120 | The e2e suite MUST also prove BMFO's `BareMetalInstance` allocation fails safe (never silently allocates an ineligible host, never double-allocates a contended one) and correctly releases its host on deletion — all driven by real BMFO reconciliation against static fixtures, no new mock/product code | MUST | Verified directly against BMFO's real upstream source (`internal/inventory/metal3.go`, `internal/controller/baremetalinstance_controller.go`) before writing any assertion — see DD-229 |
| REQ-TB-130 | The Tier B e2e suite MUST exercise `GET /api/v1alpha1/clusters/{id}` through real `osac-sp` and real Fulfillment Service v0.0.107, with an `ACTIVE` cluster whose `status.kubeconfig_secret` points to a real private Secret; it MUST assert the returned `kubeconfig` exactly equals the standard-base64 encoding of that Secret's non-empty `data["kubeconfig"]` bytes | MUST | Uses a test-created, IDP-synced non-shared tenant/Secret and private `Clusters/Update` to establish the Ready-state fixture; does not depend on provisioning a live OpenShift cluster or Vault |

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

##### AC-TB-020: A real auth failure is genuinely detectable (integration-tier sufficient)

- **Validates:** REQ-TB-060
- **Disposition:** integration-tier-sufficient via TC-I-018; the full-stack
  integration harness uses a real SP process and HTTP server, a token endpoint
  that rejects credentials, and a reachable loopback gRPC endpoint.
- **When** `osac-sp` attempts its token fetch
- **Then** it fails, and `osac-sp`'s health endpoint reports `status:
  unhealthy` with exactly `"OIDC token invalid"` and no connectivity detail

##### AC-TB-025: OSAC unreachable is genuinely detectable, distinct from an auth failure (integration-tier sufficient)

- **Validates:** REQ-TB-065
- **Disposition:** integration-tier-sufficient via TC-I-012; the full-stack
  integration harness uses a real SP process and HTTP server, a successful
  fake OIDC token fetch, and an unreachable loopback gRPC endpoint.
- **When** `osac-sp` fetches its OIDC token (succeeds) and probes the
  unreachable OSAC gRPC endpoint
- **Then** its health endpoint reports `status: unhealthy` with a detail
  equal to exactly `"OSAC fulfillment service unreachable"`, never combined
  with or confused for the AC-TB-020 token-invalid detail

##### AC-TB-030 (Phase 2): A real `ClusterOrder` reaches a real terminal state

- **Validates:** REQ-TB-070, REQ-TB-080, REQ-TB-100
- **Given** the Phase 2 stack (real `osac-operator` + `osac-aap-mock`)
- **When** the e2e suite creates a `ClusterOrder` CR directly against the
  `kind` cluster's own API server (the direct-reconciliation path, distinct
  from TC-TB-200's routed `osac-sp`-initiated `Create`)
- **Then** real `osac-operator` reconciliation and `osac-aap-mock` cooperate
  to drive that CR to a real terminal `Ready` status, observable via the
  Kind cluster's own API server

##### AC-TB-040 (Phase 2): A real `BareMetalInstance` reaches a real terminal state, with and without an active `RunStrategy`

- **Validates:** REQ-TB-070, REQ-TB-110
- **Given** the Phase 2 stack (real BMFO) and two static `BareMetalHost`
  fixtures, one per `BareMetalInstance` variant
- **When** the e2e suite creates both `BareMetalInstance` CRs directly
  against the `kind` cluster's own API server, and — for the
  `runStrategy: Always` variant only — patches that variant's
  `BareMetalHost.status.poweredOn` to `true` once, simulating a real
  `baremetal-operator`'s completed power-on action
- **Then** both CRs reach a real terminal `status.phase: Ready`, driven
  entirely by real BMFO reconciliation logic — no real Metal3/Ironic/
  virtual-BMC infrastructure involved

##### AC-TB-050 (Phase 2): BMFO's `BareMetalInstance` allocation fails safe, and releases its host on deletion

- **Validates:** REQ-TB-070, REQ-TB-120
- **Given** the Phase 2 stack (real BMFO) and static `BareMetalHost`
  fixtures crafted to exercise each failure/release path (absent, present
  but ineligible, singly contended)
- **When** the e2e suite creates `BareMetalInstance` CRs against: (a) a
  `hostType` with no matching `BareMetalHost` at all, (b) a `hostType`
  whose only `BareMetalHost` has a non-`OK` `operationalStatus`, (c) a
  single available `BareMetalHost` claimed by two competing
  `BareMetalInstance`s, and separately deletes a `Ready`
  `BareMetalInstance` created for this purpose
- **Then** (a) and (b) both converge to a real terminal `status.phase:
  Failed` with an `Allocated=False`/`"No matching hosts available"`
  condition — never silently `Ready` and never stuck retrying forever
  without a terminal status; (c) converges to exactly one instance
  `Ready` and the other `Failed`, never both `Ready` (no double
  allocation); and the deleted instance's `BareMetalHost` has its
  `spec.consumerRef` cleared, making it reassignable again

##### AC-TB-060 (Phase 2): osac-sp-initiated Create routes through fulfillment-service Hub dispatch

- **Validates:** REQ-TB-100
- **Given** a registered fulfillment-service Hub (registered by the pinned `osac` CLI in the Tier B workflow)
- **When** the e2e suite calls osac-sp's `POST /api/v1alpha1/clusters?id=...` (not direct CR creation)
- **Then** the request routes through fulfillment-service's dispatch layer, creates a real ClusterOrder CR on the Hub, and osac-operator + osac-aap-mock drive it to Ready

##### AC-TB-070 (Phase 2): Cluster Get retrieves the kubeconfig through the real Secret API

- **Validates:** REQ-TB-030, REQ-TB-130, REQ-GET-020
- **Given** the Tier B stack, an IDP-synced test tenant with a unique DNS domain, a Cluster created in that tenant through the pinned Fulfillment Service CLI, a real kubeconfig Secret in that tenant, and the Cluster updated through the private API to `CLUSTER_STATE_READY` with that Secret's ID in `status.kubeconfig_secret`
- **When** the e2e suite calls `GET /api/v1alpha1/clusters/{id}` through real `osac-sp`
- **Then** the response is `200 OK`, its status is exactly `ACTIVE`, and its `kubeconfig` is exactly the standard-base64 encoding of the test Secret's non-empty `data["kubeconfig"]` bytes

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
- ~~**`BareMetalInstance`'s terminal-state proof**~~ — resolved: a live
  spike found #46's original premise wrong — BMFO's `metal3` backend
  requires zero real hardware/BMC/Ironic simulation, since every operation
  it performs is a plain Kubernetes API read/patch on the `BareMetalHost`
  object itself (DD-226/227). Implemented as REQ-TB-110/AC-TB-040 using two
  static `BareMetalHost` fixtures, covering both `runStrategy` unset and
  `Always` (TC-TB-110/120).
- ~~**`osac-sp`-initiated `Create` → `fulfillment-service` `Hub` dispatch**~~ —
  resolved by TC-TB-200 using the `v0.0.107` fulfillment-service CLI, which
  includes the OSAC-4826 `--name` fix.
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

# Tier B Phase 2 CRDs (`osac-sp-e2e-tier-b.spec.md` §3, REQ-TB-070)

The first 4 are verbatim copies of the CRD YAMLs `fulfillment-service`'s own
`it` (Go integration-test) package vendors for its tests, sourced from
[`osac-project/osac/fulfillment-service/it/crds/`](https://github.com/osac-project/osac/tree/main/fulfillment-service/it/crds)
(see issue #44's research comment for how this was confirmed). The remaining
4 (DD-220/DD-221-adjacent) were added after a live spike and this repo's own
CI both hit startup-time gaps DD-219's original 4 didn't cover — either
copied from `osac-operator`'s/BMFO's own `config/crd/bases/` (real,
`controller-gen`-generated schemas) where no `it/crds/` fixture existed, or
authored from scratch in the same fixture-grade style where neither existed.

| File | CRD `kind` | Real or fixture-grade? |
|---|---|---|
| `clusterorders.osac.openshift.io.yaml` | `ClusterOrder` | Fixture-grade — upstream's own comment: "a fake CRD, it can contain any thing, it is not validated. We use it just for tests." (`x-kubernetes-preserve-unknown-fields: true`, no schema) |
| `hostedclusters.hypershift.openshift.io.yaml` | `HostedCluster` | Fixture-grade, same as above |
| `nodepools.hypershift.openshift.io.yaml` | `NodePool` | Fixture-grade, authored here (no upstream `it/crds/` copy exists) — see the file's own header comment for why it's required even with most osac-operator controllers disabled |
| `tenants.osac.openshift.io.yaml` | `Tenant` | Real, `controller-gen`-generated production schema |
| `osac.openshift.io_baremetalinstances.yaml` | `BareMetalInstance` | Real, `controller-gen`-generated production schema |
| `osac.openshift.io_baremetalpools.yaml` | `BareMetalPool` | Real, `controller-gen`-generated production schema — BMFO's manager fails `unable to start manager` at startup without it, regardless of which controllers are enabled |
| `osac.openshift.io_computeinstances.yaml` | `ComputeInstance` | Real, `controller-gen`-generated production schema — `osac-operator`'s startup migration (`migrate-subnetrefs`) unconditionally lists `ComputeInstance`s and fails hard without this CRD, even with `controllers.computeInstance: false` |
| `baremetalhosts.metal3.io.yaml` | `BareMetalHost` | Fixture-grade, authored here — BMFO's `metal3` inventory backend (selected in `bmfo-secrets.yaml`) does a CRD-discovery check at startup for this API group; no `BareMetalHost` objects are ever created this phase |

All 8 are installed regardless of which osac-operator/BMFO controllers are
actually enabled (DD-215) — `osac-operator`'s and BMFO's managers reference
these types even for controllers this phase leaves disabled, and a missing
CRD kind is a harder failure mode to diagnose than one extra unused CRD (in
the `NodePool`/`ComputeInstance` cases, "harder to diagnose" is literal: the
former fails silently — pod stays `Running`, forever-retrying its EventSource
in the background — and only the latter crash-loops visibly).

The upstream `it/crds/` directory also has 3 more (`securitygroups`,
`subnets`, `virtualnetworks`) not vendored here — `osac-mock-provider`'s own
gRPC layer already covers those for Tier B's Phase 1, and Phase 2 doesn't
touch them.

Not regenerated/kept in sync automatically — these are point-in-time copies,
same posture as this repo's other vendored fixtures (e.g.
`test/e2e/tierb-config/realm.json`).

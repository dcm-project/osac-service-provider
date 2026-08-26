# Tier B Phase 2 CRDs (`osac-sp-e2e-tier-b.spec.md` §3, REQ-TB-070)

Verbatim copies of the 4 CRD YAMLs `fulfillment-service`'s own `it` (Go
integration-test) package vendors for its tests, sourced from
[`osac-project/osac/fulfillment-service/it/crds/`](https://github.com/osac-project/osac/tree/main/fulfillment-service/it/crds)
(see issue #44's research comment for how this was confirmed).

| File | CRD `kind` | Real or fixture-grade? |
|---|---|---|
| `clusterorders.osac.openshift.io.yaml` | `ClusterOrder` | Fixture-grade — upstream's own comment: "a fake CRD, it can contain any thing, it is not validated. We use it just for tests." (`x-kubernetes-preserve-unknown-fields: true`, no schema) |
| `hostedclusters.hypershift.openshift.io.yaml` | `HostedCluster` | Fixture-grade, same as above |
| `tenants.osac.openshift.io.yaml` | `Tenant` | Real, `controller-gen`-generated production schema |
| `osac.openshift.io_baremetalinstances.yaml` | `BareMetalInstance` | Real, `controller-gen`-generated production schema |

All 4 are installed regardless of which osac-operator/BMFO controllers are
actually enabled (DD-214) — `osac-operator`'s and BMFO's managers reference
these types even for controllers this phase leaves disabled, and a missing
CRD kind is a harder failure mode to diagnose than one extra unused CRD.

The upstream `it/crds/` directory also has 3 more (`securitygroups`,
`subnets`, `virtualnetworks`) not vendored here — `osac-mock-provider`'s own
gRPC layer already covers those for Tier B's Phase 1, and Phase 2 doesn't
touch them.

Not regenerated/kept in sync automatically — these are point-in-time copies,
same posture as this repo's other vendored fixtures (e.g.
`test/e2e/tierb-config/realm.json`).

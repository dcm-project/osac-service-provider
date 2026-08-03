# Vendored OSAC protos

Per [DD-020](../.ai/decisions/osac-sp.decisions.md#dd-020-minimal-capabilities-only-grpc-client-for-milestone-1),
Milestone 1 generated a client for only the `osac.public.v1.Capabilities`
service — the smallest, explicitly unauthenticated proto service — to back
the health check's connectivity probe. Milestone 2
([spec](../.ai/specs/osac-sp-m2-grpc-client-generation.spec.md)) adds the
`Clusters`, `ComputeInstances`, `Subnets`, and `VirtualNetworks` services (and
their supporting type/metadata files) needed for cluster/VM CRUD. Both sets
of files stay vendored side by side — Milestone 2 does not replace or remove
the Milestone 1 files, since `Capabilities` is still used by the health
check.

All files below are vendored (copied) verbatim, byte-for-byte, from
`osac-project/fulfillment-service` at commit
[`73ae26e`](https://github.com/osac-project/fulfillment-service/tree/73ae26e8cb0a476d4b035b18776603f60a361ed9/proto/public/osac/public/v1) —
**pinned to that exact commit, not `main`**, since "verbatim" is a claim
about one point in time and `main` will have moved by the time anyone reads
this. This mirrors the same reproducibility concern DD-050 addresses for the
`control-plane` Go module dependency (pinned by commit SHA in `go.mod`
for the same reason: no tagged release to pin to instead). If you update
these files, update the pinned commit reference here too.

Milestone 1:

- `osac/public/v1/capabilities_service.proto`
- `osac/public/v1/authn_capabilities_type.proto`

Milestone 2:

- `osac/public/v1/clusters_service.proto`
- `osac/public/v1/cluster_type.proto`
- `osac/public/v1/compute_instances_service.proto`
- `osac/public/v1/compute_instance_type.proto`
- `osac/public/v1/subnets_service.proto`
- `osac/public/v1/subnet_type.proto`
- `osac/public/v1/virtual_networks_service.proto`
- `osac/public/v1/virtual_network_type.proto`
- `osac/public/v1/metadata_type.proto`
- `osac/public/v1/condition_status_type.proto`

`events_service.proto` is explicitly **out of scope** for Milestone 2 (see
`.ai/specs/osac-sp-m2-grpc-client-generation.spec.md`'s DD-010) — status
reporting back to DCM goes through `control-plane`'s direct-REST dispatch
model (Phase 1), not CloudEvents, so this SP has no current consumer for it.

**Vendoring vs. a live `deps:` reference:** `fulfillment-service`'s proto
module (`buf.build/osac-project/public-api`) is confirmed live and publicly
readable, pushed on every tagged release (e.g. `v0.0.79`) by
`fulfillment-service`'s `publish-proto.yaml` — but only under version-tag
labels, not `main` (checking the default `main` label alone will incorrectly
suggest the module has no commits). Byte-for-byte vendoring is kept anyway,
deliberately, not because the module is immature: pinning to an explicit
commit gives this SP controlled, reviewed adoption of upstream interface
changes rather than exposure to unreviewed regressions from a floating live
dependency. Revisit if vendoring's manual-sync cost grows.

Regenerate with `make generate-proto` after editing `buf.gen.yaml`/`buf.yaml`
or bumping the vendored files.

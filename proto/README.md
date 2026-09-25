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

The original Milestone 1 files below were vendored (copied) verbatim,
byte-for-byte, from `osac-project/fulfillment-service` at commit
[`73ae26e`](https://github.com/osac-project/fulfillment-service/tree/73ae26e8cb0a476d4b035b18776603f60a361ed9/proto/public/osac/public/v1).
The cluster CRUD files and their supporting types were originally synced to
the public FFS API schema used by the pinned
[`fulfillment-service/v0.0.107`](https://github.com/osac-project/osac/tree/fulfillment-service/v0.0.107/proto/public/osac/public/v1)
release tag (monorepo commit
[`bff38394`](https://github.com/osac-project/osac/tree/bff38394f1ad724c1b0b17fd655480c2c202ea11/proto/public/osac/public/v1)).
The commit pin is deliberate: upstream API changes must be reviewed and
regenerated explicitly rather than arriving through a floating `main`
dependency.

Milestone 1:

- `osac/public/v1/capabilities_service.proto`
- `osac/public/v1/authn_capabilities_type.proto`

Milestone 3 additionally vendors, at the same pinned commit:

- `osac/public/v1/clusters_service.proto` and `cluster_type.proto` replace the
  earlier Milestone 2 versions for the Cluster Get migration: `GetKubeconfig`
  is removed and `ClusterStatus.kubeconfig_secret` is included.
- `osac/public/v1/secret_type.proto`
- `osac/public/v1/cluster_catalog_item_type.proto`
- `osac/public/v1/cluster_common_type.proto`
- `osac/public/v1/cluster_templates_service.proto`
- `osac/public/v1/cluster_template_type.proto`
- `osac/public/v1/cluster_version_type.proto`
- `osac/public/v1/cluster_versions_service.proto`
- `osac/public/v1/field_definition_type.proto`
- `osac/public/v1/host_type_type.proto`
- `osac/public/v1/security_rule_type.proto`

The Secret-backed Cluster Get path also needs the private `Secrets/Get` client.
The following wire-compatible `osac.private.v1` schema subset is sourced from
the same `bff38394` release commit: `metadata_type.proto`, `secret_type.proto`,
`secrets_service.proto`, and the `Cluster`/`Clusters.Get`/`Clusters.Update`
messages needed by the Tier B regression fixture. `tenant_type.proto` and the
`Tenants.Create`/`Tenants.Get` subset let that fixture create and await a
tenant-scoped test Secret.
Production uses only `Secrets/Get`; private Cluster and Tenant clients are for
test setup, not production provider operations. `buf.build/cleanapi/cleanapi:v0.0.12`
is pinned in `buf.yaml` for the private API annotations.

Needed by Create's node-set-key resolution: `Cluster.spec.node_sets`' keys
are per-template and not derivable from `template_id`, so the SP calls
`ClusterTemplates/Get` to discover them (see
[#12](https://github.com/dcm-project/osac-service-provider/pull/12)'s
DD-110/SC-M3-004 for the full rationale — this branch predates that fix and
will pick up its own copy of the decision on rebase).

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
`.ai/specs/osac-sp-m2-grpc-client-generation.spec.md`'s DD-010) — **not**
because status reporting uses REST (it doesn't: `control-plane`'s SP-facing
REST surface,
[`api/sp/v1alpha1/resource_manager/openapi.yaml`](https://github.com/dcm-project/control-plane/blob/main/api/sp/v1alpha1/resource_manager/openapi.yaml),
exposes only `GET`/`POST /service-type-instances` and
`GET`/`DELETE /service-type-instances/{id}` — no status-update endpoint
exists there at all), but because status reporting (Milestone 5, not yet
implemented) is designed to be **polling**-based per the
[SP Status Reporting](https://github.com/dcm-project/enhancements/blob/main/enhancements/state-management/service-provider-status-reporting.md)
and [OSAC SP](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#status-reporting)
enhancements, publishing CloudEvents directly over NATS JetStream — consumed
by `control-plane`'s
[`internal/sp/consumer.StatusConsumer`](https://github.com/dcm-project/control-plane/blob/main/internal/sp/consumer/consumer.go).
`Events`/`Watch` is a real, working gRPC-streaming RPC (verified against
`fulfillment-service`'s
[`internal/servers/events_server.go`](https://github.com/osac-project/osac/blob/main/fulfillment-service/internal/servers/events_server.go)
— `fulfillment-service` archived as a standalone repo ~2026-08-15, now a
subdirectory of the `osac-project/osac` monorepo),
but DD-010 treats it as an optional latency supplement to polling, not the
primary mechanism — see DD-010's rationale for why. This SP has no current
consumer for `events_service.proto` simply because Milestone 5 hasn't been
implemented yet.

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

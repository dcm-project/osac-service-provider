# Vendored OSAC protos (Milestone 1 — minimal set)

Per [DD-020](../.ai/specs/osac-sp.spec.md#dd-020-minimal-capabilities-only-grpc-client-for-milestone-1),
this milestone generates a client for only the `osac.public.v1.Capabilities`
service — the smallest, explicitly unauthenticated proto service — to back
the health check's connectivity probe.

`osac-project/fulfillment-service`'s proto module
(`buf.build/osac-project/public-api`) is declared but has no commits pushed
to the Buf Schema Registry yet, so it cannot be depended on remotely. The two
files below are vendored (copied) directly from
[`osac-project/fulfillment-service/proto/public/osac/public/v1/`](https://github.com/osac-project/fulfillment-service/tree/main/proto/public/osac/public/v1)
verbatim:

- `osac/public/v1/capabilities_service.proto`
- `osac/public/v1/authn_capabilities_type.proto`

**Milestone 2** replaces this with the full proto set
(`clusters_service.proto`, `compute_instances_service.proto`,
`subnet_type.proto`, `virtual_network_type.proto`, `events_service.proto`)
needed for cluster/VM CRUD — at that point, re-evaluate whether
`fulfillment-service`'s BSR module has been published and switch to a real
`deps:` reference instead of vendoring, per issue #1's guidance not to vendor
another repo's *generated* code (vendoring the proto *source* here is an
interim measure, not the target state).

Regenerate with `make generate-proto` after editing `buf.gen.yaml`/`buf.yaml`
or bumping the vendored files.

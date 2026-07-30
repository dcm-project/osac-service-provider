# Vendored OSAC protos (Milestone 1 — minimal set)

Per [DD-020](../.ai/specs/osac-sp.spec.md#dd-020-minimal-capabilities-only-grpc-client-for-milestone-1),
this milestone generates a client for only the `osac.public.v1.Capabilities`
service — the smallest, explicitly unauthenticated proto service — to back
the health check's connectivity probe.

`osac-project/fulfillment-service`'s proto module
(`buf.build/osac-project/public-api`) is declared but has no commits pushed
to the Buf Schema Registry yet, so it cannot be depended on remotely. The two
files below are vendored (copied) verbatim, byte-for-byte, from
`osac-project/fulfillment-service` at commit
[`73ae26e`](https://github.com/osac-project/fulfillment-service/tree/73ae26e8cb0a476d4b035b18776603f60a361ed9/proto/public/osac/public/v1) —
**pinned to that exact commit, not `main`**, since "verbatim" is a claim
about one point in time and `main` will have moved by the time anyone reads
this. This mirrors the same reproducibility concern DD-050 addresses for the
`control-plane` Go module dependency (pinned by commit SHA in `go.mod`
for the same reason: no tagged release to pin to instead). If you update
these files, update the pinned commit reference here too.

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

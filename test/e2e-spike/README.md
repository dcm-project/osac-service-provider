# Spike: real `fulfillment-service` + real Keycloak for e2e (Tier B)

**Throwaway — not for merging.** This directory exists only to answer one
question: can `osac-sp`'s e2e suite import and drive
`osac-project/fulfillment-service`'s own `it` package (its kind-based
integration-test harness — real `fulfillment-service` + real Keycloak + real
Postgres, no AAP/no real hub) instead of hand-rolling an equivalent?

Deliberately a **separate nested Go module** (own `go.mod`/`go.sum`), so
`fulfillment-service`'s heavy transitive dependencies (Postgres/Keycloak
clients, `k8s.io/client-go`, `controller-runtime`, OPA/Rego, ...) never touch
the main module's `go.mod`/`go.sum` — same reasoning as other DCM repos'
`tests/e2e/go.mod` split.

Not wired into `make check`/CI. Not spec'd, not test-planned. If the spike
findings below are acted on, the real implementation lands as a proper
spec/TDD-gated change under `.ai/`; this code is not that change.

## Findings

### ✅ Step 1: `it.NewTool()...Build()` resolves and compiles as an external dependency

```console
$ go get github.com/osac-project/fulfillment-service@v0.0.79
go: downloading github.com/osac-project/fulfillment-service v0.0.79
go: github.com/osac-project/fulfillment-service@v0.0.79 requires go >= 1.26.3; switching to go1.26.5
go: added github.com/osac-project/fulfillment-service v0.0.79

$ go mod tidy && go build ./...
# (succeeds — see go.sum for the full resolved transitive graph)

$ go run .
=== Step 1: does `it.NewTool()...Build()` construct without error? ===
PASS: it.NewTool()...Build() constructed a *it.Tool successfully.
```

Confirmed by reading the resolved dependency graph along the way:
- `internal/auth/grpc_authz_interceptor.go` imports `github.com/open-policy-agent/opa/v1/rego`
  — the OPA/Rego policy evaluation the DCM docs bank described is a real,
  present code path in the exact version we'd pin against, not something that
  atrophied.
- `it/it_tool.go` imports `k8s.io/client-go`, `sigs.k8s.io/controller-runtime`,
  and (via `internal/testing/kind.go`) the CRD-type packages of both
  `osac-project/osac-operator` and `osac-project/bare-metal-fulfillment-operator`
  — confirming the harness registers those CRDs in its kind cluster (for
  fulfillment-service's own internal reconciler to write into) without
  actually running those operators' controllers.

### ⚠️ Step 1a: `it.Tool` requires the caller to pass `SetProjectDir` explicitly

`ToolBuilder.Build()` falls back to `findProjectDir()`, which walks **up from
the current working directory** looking for the nearest `go.mod`. Inside
`fulfillment-service`'s own repo (running `go test ./it/...`), that correctly
lands on their repo root, so relative paths like `it/charts/keycloak` resolve.
For an **external** caller like us, that walk would find *our* `go.mod`
instead — so any real (non-spike) use of this package must call
`.SetProjectDir(<path to a real fulfillment-service checkout>)` explicitly.
This isn't a blocker, just a concrete integration detail: the real
implementation needs a `git clone` of `fulfillment-service` at a pinned tag
(mirrors this repo's existing commit-pin precedent for `control-plane`) as a
setup step, with `SetProjectDir` pointed at that clone.

### ⏸️ Step 2: actually running `tool.Setup(ctx)` — not exercised on this laptop

This machine's local Podman machine (`podman-machine-default`) would not
reliably start during this spike — `podman machine start` reports "started
successfully" but `podman machine inspect` immediately shows `state: stopped`
and the API socket refuses connections. This is a **local environment issue
on this laptop**, unrelated to `fulfillment-service`'s own tooling.

This is not a meaningful gap: `fulfillment-service`'s own
[`.github/workflows/integration-tests.yml`](https://github.com/osac-project/fulfillment-service/blob/main/.github/workflows/integration-tests.yml)
already runs this exact `Setup(ctx)` path (kind cluster + real Keycloak + real
Postgres + real service) successfully on a plain `runs-on: ubuntu-latest`
GitHub-hosted free-tier runner, on every PR and nightly. The remaining risk is
best derisked in an actual GitHub Actions run of *our* repo, not on this
laptop's flaky Podman VM.

## What this means for the real implementation

- Tier B (real `fulfillment-service` + real Keycloak, no real hub/AAP) is
  **buildable, not just theoretical** — confirmed by successfully compiling
  against the real `it` package at its latest tag (`v0.0.79`).
- The real work is: (1) a CI step that `git clone`s `fulfillment-service` at a
  pinned tag, (2) a small Go helper (in a proper `.ai`-spec'd package, not
  this throwaway) that calls `it.NewTool().SetProjectDir(<clone>)...Build()`
  and `tool.Setup(ctx)`/`tool.Cleanup(ctx)`, and (3) our own Ginkgo assertions
  (structurally like `TC-I-031`, but pointed at the real backend) proving our
  real OIDC client credentials/claims are accepted end-to-end by the real
  interceptor + OPA policy.
- Confirm actual runtime behavior in GitHub Actions (`ubuntu-latest`) as the
  very first step of that work, since it's the one thing this spike couldn't
  exercise locally.

## Running it

```bash
cd test/e2e-spike
go build ./...              # proves import + compile
go run . -runsetup -projectdir=/path/to/a/real/fulfillment-service/checkout
```

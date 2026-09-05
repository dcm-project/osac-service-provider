# The kind-based e2e CI pattern for DCM service providers

**Status:** proven reference implementation, this repo. Copyable by any
other `dcm-project` service provider (SP) repo today.

**Origin:** [osac-service-provider#17](https://github.com/dcm-project/osac-service-provider/issues/17)
(FLPATH-4759), tracing back to
[control-plane#40](https://github.com/dcm-project/control-plane/issues/40):
no SP repo had **any** e2e/subsystem CI tier of its own, and no test
anywhere in the DCM project ran a real, independently-built SP against a
real `control-plane` — every "subsystem" suite (`control-plane`'s
`auth-subsystem`, `sp-subsystem`, etc.) stubbed out every DCM-owned sibling
with `wiremock`. That gap is how `kubevirt-service-provider`'s VM status
payload once diverged from the canonical spec with nothing catching it.
`osac-sp` built the reference implementation first (cheapest real backend
to fake — a plain gRPC service, no cluster/nested-virt needed) specifically
so other SP teams wouldn't have to invent this from scratch.

This document is the write-up issue #17 promised ("document this pattern so
other SP teams can replicate it without reinventing the approach"), now
that the pattern has shipped, been fixed under real CI failures, and been
independently re-verified outside CI (see §7).

---

## 1. The core idea

**Own your mock-provider binary in your own repo. Own your own e2e GitHub
Actions workflow in your own repo. Pull `control-plane` as a published
upstream artifact (image + chart directory) rather than building it from
source.**

```
kind cluster (GH-hosted ubuntu-latest runner: 4 vCPU / 16 GB RAM)
├── dcm-postgres              (control-plane's own chart, StatefulSet+PVC)
├── dcm-nats                  (control-plane's own chart, StatefulSet+PVC)
├── dcm-control-plane         (control-plane's own chart; PULLED image, pinned — see §5)
├── <your-sp>                 (your repo's own manifest; BUILT image, kind-loaded)
└── <your-sp>-mock-provider   (your repo's own manifest; BUILT image, kind-loaded)
```

Only the actual external backend your SP talks to (OSAC's
`fulfillment-service` for `osac-sp`; the Kubernetes API for
`kubevirt-service-provider`/`k8s-container-service-provider`; ACM's CRDs
for `acm-cluster-service-provider`; etc.) is mocked. Everything else —
`control-plane`, Postgres, NATS, your own SP binary — is the real thing,
running as real pods in a real (if ephemeral) cluster. This validates the
`<your-sp>` ↔ `control-plane` contract end-to-end; it is explicitly **not**
real-backend integration testing (that's `control-plane`'s own future
cross-SP matrix, or your own Tier B — see §6).

## 2. What to copy, file by file

Everything below is this repo's own plain Kubernetes manifests / Go code —
no shared library, no `dcm-project/utilities` dependency required (issue
#17 originally proposed the latter; DD-202 explains why it turned out
unnecessary once `control-plane` started shipping a real image+chart other
repos can pull directly).

| This repo's file/dir | What it is | What differs per SP |
|---|---|---|
| `test/cmd/osac-mock-provider/main.go` | Real `net.Listen`-backed gRPC + HTTP server, no `bufconn` | Which services you register — see §3 |
| `test/mockprovider/*.go` | The actual fake service implementations + resource stores | Entirely SP-specific: fakes whatever your backend's API looks like |
| `test/e2e/` (own `go.mod`) | The e2e assertions themselves (Ginkgo v2), plus `kind-config.yaml` | Assertions are SP-specific; the nested-module isolation trick (own `go.mod` so `k8s.io/client-go`/a control-plane REST client never enters your main module) is universal |
| `test/e2e/manifests/` | Plain `Deployment`+`Service` YAML for your SP + your mock provider | Env-var wiring only |
| `.github/workflows/e2e.yaml` | The GitHub Actions job itself | Copy near-verbatim; adjust image names and env vars |

## 3. Building your own mock-provider binary

The insight from issue #17 that made this cheap: **your existing unit-test
fakes are already real, wire-level server implementations** — they're just
wired over `bufconn` for in-process test speed instead of a real TCP
listener. Converting one into a standalone binary is a small step, not a
rewrite. This repo's version:

```55:86:test/cmd/osac-mock-provider/main.go
func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := mockprovider.LoadConfig()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	grpcLn, err := net.Listen("tcp", cfg.GRPCAddress)
	if err != nil {
		return fmt.Errorf("listening for gRPC on %s: %w", cfg.GRPCAddress, err)
	}
	defer func() { _ = grpcLn.Close() }()

	oidcLn, err := net.Listen("tcp", cfg.OIDCAddress)
	if err != nil {
		return fmt.Errorf("listening for OIDC HTTP on %s: %w", cfg.OIDCAddress, err)
	}
	defer func() { _ = oidcLn.Close() }()

	grpcSrv := grpc.NewServer()
	publicv1.RegisterCapabilitiesServer(grpcSrv, mockprovider.NewCapabilitiesServer())
	publicv1.RegisterClustersServer(grpcSrv, mockprovider.NewClustersServer())
	publicv1.RegisterComputeInstancesServer(grpcSrv, mockprovider.NewComputeInstancesServer())
	publicv1.RegisterSubnetsServer(grpcSrv, mockprovider.NewSubnetsServer())
	publicv1.RegisterVirtualNetworksServer(grpcSrv, mockprovider.NewVirtualNetworksServer())

	// OIDCHandler derives each response's token_endpoint from that
	// request's own Host header (DD-139), so it needs no address
	// computed from oidcLn here.
	oidcSrv := &http.Server{Handler: mockprovider.NewOIDCHandler(logger)}

	return serveUntilDone(ctx, logger, shutdownTimeout, grpcSrv, grpcLn, oidcSrv, oidcLn)
}
```

Design points worth copying regardless of your specific backend shape:

- **A single, mutex-protected in-memory `resourceStore[T]`** (generic, one
  instance per CRUD-shaped resource) rather than bespoke storage per
  service — see `test/mockprovider/store.go`.
- **A minimal fake OIDC token endpoint** if your SP does client-credentials
  auth: derive `token_endpoint` from the request's own `Host` header rather
  than a hardcoded address, so the same binary works whether it's dialed by
  hostname (in-cluster) or `localhost:<port>` (local dev) without
  reconfiguration.
- **Flat, binary-specific env-var config** (`MOCK_`-prefixed here), not a
  nested struct shared with your production SP's config — this binary's
  config surface is deliberately tiny and independent.
- **No mocking framework** — hand-written fakes throughout, consistent with
  this project's broader testing convention.

## 4. The GitHub Actions job

The full annotated version is `.github/workflows/e2e.yaml` in this repo.
The shape, condensed:

1. `docker build` your SP's image and your mock-provider's image (no
   push needed for a PR-triggered run — `kind load docker-image` both).
2. `kind create cluster` (via `helm/kind-action`).
3. **Sparse-checkout** `dcm-project/control-plane`'s `deploy/helm/dcm/`
   directory (not a full clone, not a build) and `helm install` it for
   `control-plane`+Postgres+NATS, with `dcmUi.enabled=false`,
   `controlPlane.route.enabled=false`, `controlPlane.ingress.enabled=false`
   (`kind` has no `Route` CRD; the UI is irrelevant to assertions).
4. `kubectl apply -f test/e2e/manifests/` — your own plain manifests for
   your SP + your mock provider.
5. `kubectl wait --for=condition=Available` on everything, then
   `kubectl port-forward` `control-plane` and your SP to fixed local ports.
6. Run your e2e suite (`go run github.com/onsi/ginkgo/v2/ginkgo -r -v`)
   from the nested `test/e2e` module, pointed at the port-forwarded URLs.
7. **Always** (`if: always()`) stop port-forwards and `kind delete cluster`.
8. **On failure** (`if: failure()`), collect and upload `kubectl
   get pods/events`, per-component `describe` + logs as a build artifact —
   this is what makes a red run in CI actually debuggable instead of a dead
   end. See `e2e.yaml`'s "Collect diagnostics" step for the exact commands.

## 5. The two mistakes we made so you don't have to

Both of these caused real, multi-day-visible CI breakage in this repo.
Copy the *fix*, not just the pattern, from day one:

### 5a. Pin `control-plane`'s ref **and** its image tag — never float on `main`

The obvious approach — `ref: main` for the sparse-checkout, no explicit
image tag — works fine until it doesn't:
[`control-plane#51`](https://github.com/dcm-project/control-plane/pull/51)
deleted an API this suite's registration flow depended on, silently
breaking every downstream SP's e2e the moment it merged (DD-147 in this
repo's `.ai/decisions/osac-sp.decisions.md`). Two things had to be pinned,
not one:

```yaml
env:
  # Pin the chart ref itself...
  CONTROL_PLANE_REF: c04802d05ecc4fd11e070f3bc604ef9e8671a09b
  # ...AND the deployed image tag — the chart's values.yaml hardcodes
  # global.imageTag: main as its own default *regardless* of which chart
  # commit is checked out, so pinning CONTROL_PLANE_REF alone still pulls
  # the floating (and by then already-broken) quay.io/.../control-plane:main.
  CONTROL_PLANE_IMAGE_TAG: c04802d
```
```yaml
      - uses: actions/checkout@v7
        with:
          repository: dcm-project/control-plane
          ref: ${{ env.CONTROL_PLANE_REF }}
          sparse-checkout: deploy/helm/dcm
      - run: |
          helm install dcm control-plane/deploy/helm/dcm \
            --set global.imageTag="${{ env.CONTROL_PLANE_IMAGE_TAG }}" \
            --set dcmUi.enabled=false \
            --set controlPlane.route.enabled=false \
            --set controlPlane.ingress.enabled=false
```

**If you add a second workflow variant** (e.g. a Tier B stack, see §6),
pin it there too, independently — this repo initially pinned only the
primary `e2e.yaml` and missed the second `e2e-tierb.yaml` workflow, which
kept floating on `main` and stayed silently broken until caught during
this pattern's own write-up review.

`shared-workflows`' `build-push-quay.yaml` publishes both a floating
`main` tag and the immutable `${GITHUB_SHA:0:7}` short-SHA tag on every
push — always pin to the short-SHA tag for anything you depend on being
stable, and verify that tag's `build-push` check actually succeeded on
quay.io before relying on it.

### 5b. New required config surfaces later milestones add WILL crash-loop your e2e manifest if you forget to update it

Your SP's own `internal/config.Load()`-style fail-fast validation is a
feature in production — but it means every time a later milestone adds a
new *required* env var (e.g. this repo's Milestone 5 added a required
`DCM_NATS_URL`), every e2e manifest that predates that milestone needs the
same field added, or the pod crash-loops on startup with a clear but
easy-to-miss log line:

```
{"level":"ERROR","msg":"fatal error","error":"initializing: loading configuration: env: environment variable \"DCM_NATS_URL\" should not be empty"}
```

This bit us twice in this repo specifically because we had **two** e2e
manifests (`test/e2e/manifests/osac-service-provider.yaml` for Phase A,
`test/e2e/manifests-tierb/osac-service-provider.yaml` for Tier B) and only
remembered to update one of them when M5 landed — the fix had to be
reapplied to the second manifest independently, weeks later, the next time
someone touched that file. **If you have more than one e2e manifest for
the same binary, grep all of them whenever your own `internal/config`
gains a new required field.**

## 6. Two-tier model: mocked backend now, real backend later (optional)

Phase A (§1–§5) is the mandatory baseline: fast, deterministic, no external
service dependency beyond `control-plane` itself. Once your SP's own
Milestone/CRUD work has landed and you want to close the
"mock accurately models the real API" gap, a second **Tier B** workflow
variant can swap only the mocked backend for the real one — everything
else (kind, `control-plane`, your SP's own manifest wiring) stays
identical. This repo's Tier B
(`.github/workflows/e2e-tierb.yaml`, `.ai/specs/osac-sp-e2e-tier-b.spec.md`)
replaces `osac-mock-provider` with:

- A real, vendored Keycloak realm (own repo's `test/e2e/tierb-config/realm.json`).
- The real, published upstream backend chart, installed via `helm
  template | grep -v <noise> | yq filter | kubectl apply` rather than
  `helm install` directly, when the chart renders resources (e.g. Gateway
  API `TLSRoute`) your cluster has no controller for — see
  `e2e-tierb.yaml`'s "Install fulfillment-service" step for the exact,
  heavily-commented workaround.
- `cert-manager` as a hard prerequisite for the real backend's own
  TLS-issued certs, plus a self-signed CA your own SP's container trusts
  by installing it into the container's system trust store at startup
  (`update-ca-trust`) — needed because not every HTTP client your SP uses
  necessarily has its own CA-override knob (a real, narrow gap documented
  rather than silently worked around in code).

This tier is genuinely optional and only worth building once you have a
real backend cheap enough to run in `kind` (a plain container-based
service, not something needing nested virtualization or a full external
hub product) — assess this the same way issue #17 assessed `osac-sp` vs.
`kubevirt-service-provider`/`acm-cluster-service-provider` before deciding
whether it's worth it for your SP.

## 7. How this was verified (do this for your own copy too)

Two independent layers, not just one:

1. **The actual GitHub Actions workflow, watched to completion** (`gh run
   watch --exit-status`), not just a green checkmark glanced at after the
   fact — real `kind` clusters on GitHub-hosted runners, real images built
   from the PR's own code.
2. **A from-scratch manual run on a persistent lab host** (this repo used
   `helios08`), replicating the workflow's steps outside GitHub's ephemeral
   runner environment, with the cluster torn down cleanly afterward and no
   state left behind for other users of a shared host. This catches
   anything runner-environment-specific that might otherwise hide a real
   bug (or, symmetrically, a runner-specific flake that isn't a real bug).

Both layers caught real bugs during this pattern's own hardening (§5) —
neither alone would have been sufficient.

## 8. Checklist for adopting this in a new SP repo

- [ ] Convert your existing unit-test gRPC/HTTP fakes into a real
      `net.Listen`-backed `test/cmd/<your-sp>-mock-provider/` binary (§3) —
      not repo-root `cmd/`, which is reserved for binaries you actually
      ship (DD-224).
- [ ] Write `test/e2e/` as its own nested Go module (own `go.mod`) so its
      test-only dependencies never enter your main module.
- [ ] Write plain `Deployment`+`Service` manifests for your SP + your mock
      provider (`test/e2e/manifests/`).
- [ ] Copy `.github/workflows/e2e.yaml`'s structure; adjust image names,
      manifest paths, and your suite's env vars.
- [ ] **Pin `CONTROL_PLANE_REF` to a short-SHA, and `CONTROL_PLANE_IMAGE_TAG`
      to the matching quay.io tag** — never `main` (§5a). Verify the pinned
      commit's own `build-push` check succeeded before relying on it.
- [ ] Confirm `quay.io/dcm-project/<your-sp>` already exists as a
      repository before your first `build-push-quay.yaml` run — org secrets
      (`QUAY_TOKEN`/`QUAY_USERNAME`) authenticate fine even against a
      nonexistent repo (`docker login` succeeds), but the push itself 401s
      if the repo was never provisioned and your account lacks
      create-repository permission on the org. This is an org-admin action
      (create the repo, or grant create permission), not a workflow fix —
      see [osac-service-provider#39](https://github.com/dcm-project/osac-service-provider/issues/39)
      for the exact symptom this produces if you hit it.
- [ ] Add the "collect diagnostics on failure" step from day one — don't
      wait until your first hard-to-reproduce red run to add it.
- [ ] Whenever your SP's own config gains a new *required* field, grep
      every e2e manifest you have (plural, if you build a Tier B later) and
      update all of them, not just the one you're currently looking at.
- [ ] Watch your workflow run to completion at least once
      (`gh run watch --exit-status`) rather than trusting a checkmark, and
      consider one from-scratch manual run outside CI before calling it done.

## References

- [osac-service-provider#17](https://github.com/dcm-project/osac-service-provider/issues/17) — original architecture/design issue this document fulfills
- [control-plane#40](https://github.com/dcm-project/control-plane/issues/40) — the org-wide gap this closes for one SP, as a template for the rest
- `.ai/specs/osac-sp-e2e-suite.spec.md`, `.ai/specs/osac-sp-e2e-mock-provider.spec.md`, `.ai/specs/osac-sp-e2e-tier-b.spec.md` — this repo's own REQ-*/AC-* for the reference implementation
- `.ai/decisions/osac-sp.decisions.md` — DD-130..153, DD-202 for the specific decisions/fixes referenced above

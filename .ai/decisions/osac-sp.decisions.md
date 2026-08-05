# Design Decisions: OSAC Service Provider

This document records architectural and design decisions for the OSAC
Service Provider, referenced by ID (`DD-NNN`) from the specs in
`.ai/specs/`. New decisions are appended here as implementation surfaces
them, so this file stays open across milestones rather than being tied to
any single spec document's lifecycle.

**Related Specs:** `.ai/specs/osac-sp.spec.md` (Milestone 1)

---

## DD-010: One health endpoint per registered provider, not a single unified path

**Decision:** Serve health at `GET /api/v1alpha1/clusters/health` and
`GET /api/v1alpha1/vms/health` — one path per independently-registered
provider (Topic 4.4) — rather than a single unified
`GET /api/v1alpha1/health`. Both paths report identical status, since this
SP's OIDC/OSAC health is one global condition.

**Rationale (superseding the original decision in this slot, which was
wrong):** `environment-agent`'s own detailed spec
(`.ai/specs/environment-agent.spec.md`) is explicit and concrete about how
health polling addresses a registered SP — `AC-HMN-010`: *"Given an external
SP is registered with endpoint `https://sp.example.com:8080`... When the
health check polls `GET https://sp.example.com:8080/health`"* — i.e. the
agent polls **`{registered endpoint}/health`**, literally appending `/health`
to whatever `endpoint` value the SP registered. This is reinforced by
`REQ-RTE-040` in the same spec: *"the agent MUST forward creation requests
via `POST {endpoint}`"* with no additional path segments added by the agent
— which only works if `endpoint` is already the full resource-collection
URL. Since Topic 4.4 (REQ-REG-030) registers `cluster` at
`{provider.endpoint}/api/v1alpha1/clusters` and `vm` at
`{provider.endpoint}/api/v1alpha1/vms`, applying `{endpoint}/health`
per-registration yields `/api/v1alpha1/clusters/health` and
`/api/v1alpha1/vms/health` — not a third, unrelated unified path. This also
matches the real, shipped convention in every sibling SP checked
(`k8s-container-service-provider` → `/api/v1alpha1/containers/health`,
`acm-cluster-service-provider` → `/api/v1alpha1/clusters/health`,
`k8s-storage-service-provider` → `/api/v1alpha1/volumes/health`,
`three-tier-app-demo-service-provider` → `/api/v1alpha1/three-tier-apps/health`):
health always nests under the SP's own registered resource path, never a
bare top-level path.

The original version of this decision cited issue #1's REST API contract
table for a single `/api/v1alpha1/health` path without verifying it against
`environment-agent`'s actual polling contract — a hallucination corrected
here before implementation.

**Phase 1 (`control-plane`) confirmation:** unlike the `environment-agent`
citations above (which were spec-only, since that project's own health
checker is unimplemented), `control-plane`'s health-check monitor is real,
running code, and confirms the identical convention directly:
`internal/sp/healthcheck/monitor.go`'s `performHealthCheck` builds
`strings.TrimRight(provider.Endpoint, "/") + "/health"` — the same
`{registered endpoint}/health` construction, appended per-`Provider`-row.
This decision needed no change for the Phase 1 pivot (DD-050); if anything,
it is now verified against running code rather than an unimplemented spec.
The three-state (Ready/Unhealthy/Unavailable) derivation this confirmation
refers to — already documented here and in the enhancement doc's own "SP
Health Check" section — got its first *live* (not just source-read)
confirmation via the kind-based e2e infra; see DD-140.

**Related requirements:** REQ-HTTP-020, REQ-HTTP-025, REQ-HLT-010, REQ-HLT-015

---

## DD-020: Minimal `Capabilities`-only gRPC client for Milestone 1

**Decision:** This milestone generates (via `buf`) only the
`osac.public.v1.Capabilities` client — the smallest, explicitly
unauthenticated proto service — to back the connectivity probe. The full
`Clusters`/`ComputeInstances`/`Subnets`/`VirtualNetworks` stub generation
pipeline is Milestone 2's deliverable per issue #1's suggested milestones.

**Rationale:** Issue #1 places "gRPC client generation" in Milestone 2, but
also requires Milestone 1's health check to report "real OSAC gRPC
connectivity" — these two constraints are reconciled by scoping Milestone
1's codegen to the one service that (a) requires no auth per its own proto
comment ("This endpoint does not require authentication") and (b) is
purpose-built for capability/connectivity discovery, making it accidentally
the ideal minimal probe target. The `buf.gen.yaml`/`buf.yaml` files
introduced here are extended, not replaced, in Milestone 2.

**Related requirements:** REQ-OSAC-080, REQ-OSAC-090

---

## DD-030: Health response schema

**Decision:** Health response uses `status: "healthy"` or `status:
"unhealthy"` with fields `type`, `path`, `version`, `uptime`, matching the
schema convention already used by sibling SPs' health endpoints, plus an
optional `detail` field for this SP's two-part health condition (token +
connectivity).

**Rationale:** Consistency with the DCM three-state health model
([enhancements#47](https://github.com/dcm-project/enhancements/pull/47)) and
with how sibling SPs shape their health payloads, while accommodating that
this SP's health depends on two independent external systems (Keycloak,
OSAC fulfillment service) rather than one (a local Kubernetes API server).

**Related requirements:** REQ-HLT-020, REQ-HLT-030, REQ-HLT-070

---

## DD-040: Registration retry independence for `cluster` vs. `vm`

**Decision:** Implement the two registrations as fully independent
goroutines/retry loops, not a single loop iterating over two service types.

**Rationale (updated for Phase 1/`control-plane` — see DD-050):** originally
justified by `environment-agent`'s `vm`-registration `409`/slot-contention
scenario (kept not fatal, retried on the lease-renewal cadence) needing to
coexist with `cluster` succeeding independently. That specific scenario no
longer applies (see DD-050) — `control-plane` has no per-service-type
slot contention. The decision itself still holds on general grounds: any
independent, non-transient failure on one service type's registration (a
validation bug in the `vm` metadata payload, a `control-plane`-side outage
affecting only one in-flight request, etc.) must not block, delay, or leak
backoff/failure state into the other's retry loop (REQ-REG-060). Sharing a
retry loop would couple that state unnecessarily.

**Related requirements:** REQ-REG-060, REQ-REG-090

---

## DD-050: Two-phase registration target — `control-plane` for Phase 1, `environment-agent` deferred to Phase 2

**Decision (supersedes the original single-phase decision in this slot):**
OSAC SP's first release (Phase 1, this milestone) registers with
[`dcm-project/control-plane`](https://github.com/dcm-project/control-plane)'s
SP API (`POST /api/v1alpha1/providers`, per
`api/sp/v1alpha1/provider/openapi.yaml`), using its generated
`github.com/dcm-project/control-plane/pkg/sp/client/provider`, pinned by
commit SHA (no tagged releases exist for `control-plane` either). Migration
to `dcm-project/environment-agent` (the originally chosen target) is
deferred to a future Phase 2, once that project reaches sufficient maturity.
This reverses the direction taken during the original enhancement review —
tracked for the enhancement doc update in
[enhancements#95](https://github.com/dcm-project/enhancements/issues/95).

**Rationale — maturity comparison (why the reversal):** at
`control-plane`@[`6c16c06`](https://github.com/dcm-project/control-plane/commit/6c16c0654018cd779a7c3ad8739427644732c41b),
`POST /api/v1alpha1/providers` is a **complete, wired** implementation —
`internal/sp/handlers/provider/handler.go` implements the generated
`StrictServerInterface` end-to-end (List/Create/Get/Apply/Delete) against a
real store, mounted in `internal/app/run.go`. `environment-agent`'s
equivalent handler, by contrast, exists only as generated stubs
(`server.gen.go`) with no `internal/handlers`/`internal/service`
implementation and a no-op `main()` — unchanged from the original writeup of
this decision. Neither project has tagged releases, but `control-plane`'s SP
registration path is functionally complete today; `environment-agent`'s is
not. `k8s-container`, `acm-cluster`, and `kubevirt` already register
directly with `control-plane` (via the archived `service-provider-manager`
client rather than `control-plane`'s newer `pkg/sp/client/provider`) — OSAC
SP now targets the same backend as its siblings for Phase 1, rather than
diverging from them, and is the first SP to exercise the newer client.

**Authentication:** `control-plane`'s SP registration and CRUD-dispatch APIs
(`api/sp/v1alpha1/provider/openapi.yaml`,
`api/sp/v1alpha1/resource_manager/openapi.yaml`) declare
`security: [bearerAuth: []]`, enforced by in-app JWT bearer validation
([`internal/auth`](https://github.com/dcm-project/control-plane/blob/main/internal/auth),
added in
[`control-plane#24`](https://github.com/dcm-project/control-plane/pull/24)
as part of the
[authentication enhancement](https://github.com/dcm-project/enhancements/blob/main/enhancements/authentication/authentication.md)).
That check is currently a no-op: `AUTH_DISABLED` defaults to `true`
([`internal/app/config.go`](https://github.com/dcm-project/control-plane/blob/main/internal/app/config.go)),
and `control-plane`'s own outbound call to the SP's endpoint sets no
`Authorization` header either. Sending no bearer credential from this SP
(REQ-REG-115) is therefore correct behavior today, but is a **known,
tracked gap, not a permanent architectural guarantee** — see the
Authentication Gap paragraph below.

**Authentication Gap (open, not scheduled):** the authentication
enhancement documents `AUTH_DISABLED=true` as a transitional mitigation and
states it "must not be used in production deployments." The ticket meant to
give SPs a real, authenticated path to `control-plane` —
[FLPATH-4196](https://redhat.atlassian.net/browse/FLPATH-4196) and its
enhancement-doc story
[FLPATH-4455](https://redhat.atlassian.net/browse/FLPATH-4455) — was closed
Won't-Do ("will be handled in the agents epic as the architecture has
changed"). The successor,
[FLPATH-4622](https://redhat.atlassian.net/browse/FLPATH-4622), has not
started and is blocked on the Phase 2 `environment-agent` implementation
itself
([FLPATH-4486](https://redhat.atlassian.net/browse/FLPATH-4486)). So Phase 1
currently has **no active, unblocked implementation path** to
production-safe authentication against `control-plane` — closing this gap
may require the Phase 2 migration this decision already defers to. Treat
this as a Phase 1 production blocker to track, not a cosmetic doc gap;
re-evaluate the Phase 2 migration trigger (maturity bar, above) if it
remains unaddressed by the time this SP approaches a production rollout.
Full writeup: [enhancements#96](https://github.com/dcm-project/enhancements/pull/96)
("Authentication" and "Risks and Mitigations" sections).

**Second consumer of the same `AUTH_DISABLED=true` default, found via the
kind-based e2e infra:** `test/e2e`'s registration/health test files
(`osac-sp-e2e-suite`, DD-139/090) call `control-plane`'s real
`GET /api/v1alpha1/providers` with no `Authorization` header at all and get
`200`s — confirmed this is the *same* transitional default, not a separate
gap: `control-plane`'s OpenAPI spec declares `bearerAuth` security on every
path (including these GETs) via a single global `auth.Middleware`
(`internal/auth/middleware.go`), but that middleware is a no-op whenever
`AUTH_DISABLED=true`. If `control-plane`'s chart/deployment ever flips that
default (e.g. by setting `AUTH_ISSUER_URL`), this e2e suite's unauthenticated
`cpclient` calls will start failing with `401`s alongside `osac-sp`'s own
registration POSTs — one fix (giving this SP a real bearer credential to
send) would need to cover both call sites, not just the registration path
this decision originally scoped.

**This is not just a client-library swap — `control-plane`'s CRUD-dispatch
model differs materially from `environment-agent`'s, which matters for
future milestones even though it doesn't block this one:**
`control-plane`'s `internal/sp/service/resource_manager.InstanceService`
dispatches **synchronously over plain REST** directly to
`provider.Endpoint` (`POST {endpoint}?id={instanceId}` with body
`{"spec": {...}}`; `DELETE {endpoint}/{instanceId}`) — looked up from the
same `Provider` row `POST /providers` writes — rather than
`environment-agent`'s CloudEvent-through-a-messaging-topic model with a
DCM-generated `resourceId` forwarded in the event body. Milestone 1 (this
spec) only covers registration and health, so this doesn't change anything
implemented here, but it means Milestone 2+'s CRUD/idempotent-creation
design (currently written against the CloudEvent model elsewhere in the
enhancement doc) will need real rework, not a find-replace, when that work
starts — tracked as open questions in enhancements#95, not resolved here.

**Confirmed favorably while researching this update:** `control-plane` has
its own real health-check poller,
`internal/sp/healthcheck.Monitor` (see the Phase 1 confirmation added to
DD-010) — it polls `GET {provider.Endpoint}/health` on an interval and
expects exactly the `{"status": "healthy"|"unhealthy"}` body this SP already
implements (Topic 4.3), escalating to its own `Unavailable` state via
consecutive-failure counting rather than reading a three-state body from the
SP. **No change was needed to Topic 4.3's design or its already-implemented
health handler for this pivot** — DD-010's two-endpoint-per-provider decision
holds, now confirmed against running code instead of an unimplemented spec.

**Lease/TTL consequence:** `control-plane`'s `Provider` row (see
`internal/sp/store/model/provider.go`) has no lease/TTL/expiry field — a
registered entry persists until explicitly deleted; health is tracked
independently by the monitor above via `ConsecutiveFailures`, not by
whether the SP keeps re-registering. This removes the "the SP loses its slot
if it stops renewing" mechanic entirely, which is why the pre-pivot design's
"409 is retryable, retry on the lease-renewal cadence" behavior was replaced
by REQ-REG-090's non-retryable handling rather than adapted — see DD-040.

**Maturity risk (still accepted, now against a different target):**
`control-plane` also has no tagged releases, so the Go dependency MUST still
be pinned to a specific commit SHA and bumped deliberately, not tracked via
`@latest` or a floating branch. Milestone 1's registration integration tests
(Topic 4, `osac-sp-integration.test-plan.md` §3) exercise a **fake HTTP
server implementing `control-plane`'s current OpenAPI contract**, not a live
`control-plane` instance — consistent with the existing test-design
philosophy (a fake was always the plan; only what it fakes has changed).

**Schema consequence (unchanged by the pivot):** `control-plane`'s generated
`Provider`/`ProviderMetadata` struct has no `supported_platforms`/
`supported_provisioning_types`/`kubernetes_supported_versions` fields either
— these OSAC-specific values MUST be carried as additional keys inside
`metadata` (`ProviderMetadata`'s `additionalProperties: true` catch-all,
which flattens to sibling JSON keys alongside `region_code`/`zone`/`status`/
`resources` on marshal). See REQ-REG-040.

**Related requirements:** REQ-REG-040, REQ-REG-090, REQ-REG-100, REQ-REG-115

---

## DD-060: Resolve the OIDC token endpoint via discovery, not by treating the issuer URL as the token endpoint

**Decision:** Before requesting an access token, the SP MUST perform
discovery — trying `GET {oidcIssuerUrl}/.well-known/oauth-authorization-server`
(RFC 8414) first, then falling back to
`GET {oidcIssuerUrl}/.well-known/openid-configuration` (OpenID Connect
Discovery 1.0) only if the first request fails — and use the `token_endpoint`
from whichever document was successfully retrieved for the client-credentials
grant. `oidcIssuerUrl` MUST NOT be passed directly as the OAuth2 `TokenURL`,
and discovery MUST NOT be narrowed to only one of the two well-known
documents.

**Rationale:** An OIDC *issuer* URL (e.g.
`https://keycloak.example.com/realms/osac`) and its *token endpoint* (e.g.
`https://keycloak.example.com/realms/osac/protocol/openid-connect/token`) are
different URLs in every real deployment; treating them as interchangeable
was a hallucination in the original implementation, caught by the same
"verify against an authoritative source" review that caught DD-010/SC-001.

The *first* corrected implementation (this decision's original text) only
queried `.well-known/openid-configuration` — itself a second, more subtle
hallucination, caught by the same review pass while independently verifying
the vendored `authn_capabilities_type.proto` (DD-020): that proto's own
doc comment on `trusted_token_issuers` documents discovery via
`.well-known/oauth-authorization-server` and cites RFC 8414, not OpenID
Connect Discovery. Tracing this to the ecosystem's actual client code
confirms which pattern applies to *this* SP's flow specifically:
- `osac-project/fulfillment-service`'s own `internal/oauth.TokenSource` —
  the thing that authenticates `fulfillment-service`'s **own**
  client-credentials grants (`CredentialsFlow`) against the same class of
  Keycloak issuer this SP authenticates against — calls
  `DiscoveryTool.Discover()`, which tries `oauth-authorization-server`
  first and falls back to `openid-configuration` second
  (`internal/oauth/oauth_discovery_tool.go`). This is the correct thing to
  mirror: same grant type, same issuer, same authoritative project.
- `osac-project/osac-ux`'s `proxy/auth/oidc.go` queries only
  `openid-configuration`, with no RFC 8414 attempt — but it backs a
  *human* browser authorization-code login flow, not client-credentials.
  Citing it as precedent for this SP's machine-to-machine flow (as the
  original version of this decision did) was itself a category error:
  superficially similar code, wrong flow to mirror.
- `dcm-project/control-plane` depends on `github.com/coreos/go-oidc/v3`
  for OIDC concerns elsewhere in the DCM ecosystem; that library's own
  provider discovery also tries `oauth-authorization-server` before falling
  back to `openid-configuration`, consistent with `fulfillment-service`'s
  behavior.

**Consequence:** the OIDC bootstrap can no longer build a static
`clientcredentials.Config` at construction time (`New()`); discovery must
happen lazily, inside the same retryable/non-blocking loop as token fetch
(REQ-OSAC-060), since either discovery document may be transiently
unreachable at startup just like the token endpoint itself. Discovery logic
must attempt both well-known paths, in order, before treating discovery as
failed for that attempt.

**Related requirements:** REQ-OSAC-010, REQ-OSAC-011, REQ-OSAC-012, REQ-OSAC-060

---

## DD-070: Error `type` URIs use RFC 9457 with project-controlled URIs, not bare short codes

**Decision:** `Error.type` values are full, project-controlled URIs under
`https://dcm-project.github.io/problems/*` (e.g.
`https://dcm-project.github.io/problems/internal`), per RFC 9457 (*Problem
Details for HTTP APIs*, which obsoletes RFC 7807). This replaces the bare
short-code convention (`INTERNAL`, `INVALID_ARGUMENT`, etc.) this spec
originally adopted to match `k8s-container-service-provider` and
`acm-cluster-service-provider`.

**Rationale:** this is a tracked, org-wide initiative, not a one-off ask —
Jira epic [FLPATH-4719](https://redhat.atlassian.net/browse/FLPATH-4719)
("Align existing repositories to use RFC 9457 instead of the now obsolete
RFC 7807") was opened off audit
[FLPATH-4722](https://redhat.atlassian.net/browse/FLPATH-4722), which found
both sibling SPs cited above still on RFC 7807. Migration tasks exist for
`k8s-container-service-provider`
([FLPATH-4720](https://redhat.atlassian.net/browse/FLPATH-4720), in
progress), `acm-cluster-service-provider`
([FLPATH-4721](https://redhat.atlassian.net/browse/FLPATH-4721), in code
review as
[acm-cluster-service-provider#33](https://github.com/dcm-project/acm-cluster-service-provider/pull/33)),
and `control-plane` / `kubevirt-service-provider` /
`k8s-network-service-provider` / `cnpg-database-service-provider`
(FLPATH-4728..4731, not started). No OSAC SP-specific ticket exists yet, but
since this milestone hasn't merged, there's no reason to ship on the
now-superseded convention and immediately need a follow-up migration.

`acm-cluster-service-provider#33` (open, unmerged as of this decision) is
the concrete technical reference for the target shape:
`https://dcm-project.github.io/problems/{slug}` — a real, project-owned
GitHub Pages domain — replacing both this SP's bare-code convention and the
placeholder `dcm.example.com` domain ACM/k8s-container used as an
intermediate step (an IANA-reserved example domain per RFC 2606, never
project-controlled).

**Consequence:** this SP has no CRUD endpoints yet (Milestone 1), so the
blast radius is small — only `REQ-HTTP-070`/`AC-HTTP-070` (panic recovery)
reference a concrete `type` value today. Handlers reference the generated Go
constants (`v1alpha1.INTERNAL`, etc.) symbolically rather than the
underlying string literal, so only `api/v1alpha1/openapi.yaml`'s enum
values (and regenerated code) change — no handler logic changes.

**Related requirements:** REQ-HTTP-070

---

## DD-130: Single `internal/mockprovider` package, not one sub-package per service

**Decision:** `cmd/osac-mock-provider`'s five fake gRPC services
(`Capabilities`, `Clusters`, `ComputeInstances`, `Subnets`,
`VirtualNetworks`) and its OIDC discovery+token stub all live directly in
one flat package, `internal/mockprovider` — one Go file per
service/concern (`clusters.go`, `computeinstances.go`, `subnets.go`,
`virtualnetworks.go`, `capabilities.go`, `oidc.go`, `store.go`,
`config.go`), not `internal/mockprovider/clusters/`,
`internal/mockprovider/oidc/`, etc.

**Rationale:** every file in this package shares one concern — faking
OSAC's backend surface for `osac-sp`'s own client code to dial — and all
five gRPC services share the exact same generic, unexported storage engine
(`resourceStore[T]`, see DD-131), which would otherwise need to be exported
(or duplicated) to cross sub-package boundaries for no benefit: nothing
outside this mock ever needs to depend on, say, `mockprovider/clusters`
without also needing the other four services and the OIDC stub to form a
working substitute. This mirrors the existing repo's own flat-package
convention for single-concern internal packages (e.g. `internal/osac`,
`internal/registration`) rather than the multi-file-but-nested-directory
shape of, say, `internal/api/server` (which is generated, not
hand-authored).

**Note on DD numbering:** this decision (and DD-131..083 below) start at
DD-130 on a branch cut directly from `main` while `main`'s own decisions
file still ends at DD-070. The still-unmerged M3/M4 branches independently
also claim `DD-130`+ for unrelated decisions of their own — an accepted,
temporary numbering collision until whichever branch merges first, same
already-established pattern as this repo's other concurrently-developed
milestone branches. Whichever of this branch/M3/M4 merges last renumbers its
own new entries to continue after the numbers already merged.

**Related requirements:** REQ-MOCK-010, REQ-MOCK-070, REQ-MOCK-080

---

## DD-131: Generic, mutex-protected in-memory `resourceStore[T]`, not bespoke per-service storage

**Decision:** All four CRUD-capable fake services (`Clusters`,
`ComputeInstances`, `Subnets`, `VirtualNetworks`) share one generic
`resourceStore[T]` type (`internal/mockprovider/store.go`) — a
`sync.Mutex`-protected, `map[string]T`-backed, insertion-ordered store with
`create`/`insert`/`get`/`list`/`delete` methods — rather than each service
hand-rolling its own map/mutex pair. `create` performs the duplicate-`id`
check `Clusters`/`ComputeInstances` need (`ALREADY_EXISTS` on a second
`Create` for the same caller-supplied `id`, REQ-MOCK-020); `insert` skips
that check unconditionally, for `Subnets`/`VirtualNetworks`' always-fresh,
server-generated `id`s (REQ-MOCK-021), where a duplicate-`id` branch would
be dead code (a `google/uuid` v4 collision is not a realistically testable
condition).

**Rationale:** the four services' CRUD semantics are otherwise identical
(insertion-ordered `List` with `offset`/`limit` clamping, `NOT_FOUND` on
unknown `id` for `Get`/`Delete`) and differ only in (a) whether `Create`
accepts a caller-supplied `id` or always generates one and (b) which
protobuf message type and status-enum value each wraps around the stored
object. Centralizing the storage engine keeps that duplication to a single
`switch`-free generic type instead of four near-identical hand-rolled
implementations, while still keeping each service's own file focused on its
service-specific translation logic (building the right typed
request/response, setting the right initial `status.state`).

**Consequence:** `resourceStore[T]` is unexported — it is purely an
implementation detail of this package's five services, never referenced by
`cmd/osac-mock-provider` or any external caller, so it carries no API
stability obligation of its own.

**Related requirements:** REQ-MOCK-020, REQ-MOCK-021, REQ-MOCK-030,
REQ-MOCK-040, REQ-MOCK-050, REQ-MOCK-060

---

## DD-132: No real JWT signing for the OIDC token stub

**Decision:** `internal/mockprovider.OIDCHandler`'s `/token` endpoint issues
a static, opaque bearer token string (not a real, cryptographically signed
JWT) for a valid `client_credentials` grant, and never validates the
`client_id`/`client_secret` credentials presented against anything.

**Rationale:** the mock's own gRPC server (the thing that token is actually
*for*) doesn't enforce auth either — `internal/mockprovider`'s five gRPC
services accept every request unconditionally, regardless of what (if any)
bearer metadata is attached — so a real, verifiable JWT would be signing a
promise nothing on either side of this mock ever checks. The only real
production code this mock needs to satisfy end-to-end is `osac-sp`'s own
`internal/osac.discoveringTokenSource`/`clientcredentials.Config`-backed
token fetch (REQ-OSAC-010/011), which only requires a syntactically valid
OAuth2 token response body (`access_token`/`token_type`/`expires_in`) — it
never inspects the token's own contents. Matches
`cmd/osac-service-provider/main_integration_test.go`'s own fake Keycloak,
which takes the identical shortcut for the same reason.

**Related requirements:** REQ-MOCK-090

---

## DD-133: Flat `MOCK_`-prefixed env vars for the mock's own config, not a nested `internal/config`-shaped struct

**Decision:** `internal/mockprovider.Config` is a flat, two-field struct
(`GRPCAddress`, `OIDCAddress`, both required/fail-fast) read via
`MOCK_GRPC_ADDRESS`/`MOCK_OIDC_ADDRESS` — a new, independent env-var
namespace, not a reuse of `internal/config.Config`'s shape or any of its
existing `SP_`/`DCM_` prefixes.

**Rationale:** this binary is not a service provider registering with
`control-plane` and has no OSAC client of its own to configure — the only
two things it needs are its own two listen addresses — so
`internal/config.Config`'s `Server`/`OSAC`/`DCM`/`Provider` sub-structs
would each be either entirely unused or actively misleading (e.g. an `OSAC`
sub-struct on the binary that *is* the OSAC stand-in). A fresh, minimal,
purpose-built `Config` avoids importing meaning (and required env vars)
that don't apply, while still reusing the same `caarlos0/env` loading
convention (`LoadConfig`, fail-fast via the `notEmpty` tag) as
`internal/config.Load`. The `MOCK_` prefix (rather than reusing `SP_`)
keeps this binary's env vars unambiguously distinct from the real SP's own,
since both binaries may run side by side in the same `kind` pod/namespace
once Phase 2 wires them together.

**Related requirements:** REQ-MOCK-110

---

## DD-134: `osac-sp`/`osac-mock-provider` as this repo's own plain manifests, not a `control-plane` chart contribution

**Decision:** Phase 2's `kind` cluster deploys `osac-service-provider` and
`osac-mock-provider` via plain `Deployment`+`Service` YAML owned by this
repo (`test/e2e/manifests/`), applied with `kubectl apply` alongside a
`helm install` of `control-plane`'s own unmodified
[`deploy/helm/dcm/`](https://github.com/dcm-project/control-plane/tree/main/deploy/helm/dcm)
chart — not a PR adding `osacServiceProvider`/`osacMockProvider` sections to
that chart's `values.yaml`/`templates/` (the pattern its 4 existing
`*ServiceProvider` sections use for `kubevirt`/`k8s-container`/`acm-cluster`/
`three-tier-demo`).

**Rationale:** issue #17 ("Ownership & artifact sourcing") explicitly scopes
this: each SP team owns its own mock-provider binary *and* its own e2e
workflow in its own repo, consuming `control-plane` purely as a published
upstream artifact (image + chart directory), not by forking or PRing into
it. Contributing an `osac-sp` chart section would (a) require a
`control-plane` PR and review cycle before this repo's own e2e could ever
go green, directly conflicting with "ready to run asap," and (b) couple
this repo's CI to `control-plane`'s release cadence for a resource this
repo already fully owns and controls the shape of. Upstreaming a chart
section remains a reasonable follow-up once this pattern is proven (issue
#17's "Generalization for other SP teams"), but is explicitly not a
blocker for it.

**Related requirements:** REQ-E2E-030

---

## DD-135: Route/Ingress/`dcmUi` disabled when installing `control-plane`'s chart into `kind`

**Decision:** `helm install` overrides `dcmUi.enabled=false`,
`controlPlane.route.enabled=false`, `controlPlane.ingress.enabled=false`
against `control-plane`'s chart defaults (`dcmUi.enabled: true`,
`controlPlane.route.enabled: true`).

**Rationale:** `controlPlane.route.enabled`'s `Route` resource is an
OpenShift-only CRD (`route.openshift.io/v1`) that plain `kind` doesn't
install — leaving it enabled would fail the `helm install --wait` step
outright. `dcmUi` has no bearing on any REQ-E2E-* assertion (this tier
asserts the REST/gRPC contract, not a browser-driven UI journey) and adds a
pod + image pull for no verification value, so disabling it keeps the job
closer to its `NFR-E2E-010` time budget.

**Related requirements:** REQ-E2E-020

---

## DD-136: `kubectl port-forward` (not `NodePort`/`Ingress`) for the e2e suite's cluster access

**Decision:** `.github/workflows/e2e.yaml` reaches `dcm-control-plane` and
`osac-service-provider` from the GitHub Actions runner host via two
background `kubectl port-forward` processes (`18080`→`control-plane:8080`,
`18081`→`osac-service-provider:8080`), not `NodePort` Services or an
`Ingress`/`Route`.

**Rationale:** the e2e suite (`test/e2e`) runs as a plain `go run` process
on the runner host, outside the `kind` cluster's pod network, so it needs
*some* host-reachable path in. `NodePort` would require picking/coordinating
fixed ports across both this repo's manifests and `kind-config.yaml`'s
`extraPortMappings`; `Ingress`/`Route` needs an ingress controller neither
chart installs here. `port-forward` needs zero manifest/kind-config changes
beyond the `Service`s Phase 1/this phase already create, at the cost of two
background processes the workflow must remember to stop (`if: always()`) —
an acceptable, purely-CI-internal tradeoff with no bearing on the
assertions themselves.

**Related requirements:** REQ-E2E-050, REQ-E2E-060

---

## DD-137: `test/e2e` imports `control-plane`'s own generated REST types directly, not a hand-rolled JSON struct

**Decision:** `test/e2e`'s registration assertions (TC-E2E-020..040)
import `github.com/dcm-project/control-plane/api/sp/v1alpha1/provider` and
`.../pkg/sp/client/provider` directly — the exact generated
OpenAPI client/types `internal/registration/registration.go` itself uses to
*write* — rather than decoding `GET /providers` responses into a
locally-defined struct.

**Rationale:** unlike the `osac-sp` health response (where `test/e2e`
deliberately hand-decodes into its own minimal struct — see the test plan's
"What's real here" note — to avoid a `replace`-directive dependency on the
parent module from this nested one), `control-plane`'s provider-types
package is an independent, already-external, already-pinned dependency
(`go.mod`'s existing `v0.0.0-20260629133201-6c16c0654018`) with a
deliberately minimal transitive footprint (confirmed empirically: `go mod
tidy` in `test/e2e` pulled in only `oapi-codegen/runtime` and small
`go-openapi`/testify-adjacent packages, no `k8s.io/client-go` or similar) —
so it carries none of the "heavy transitive deps" risk REQ-E2E-080 exists to
avoid, while giving exact field-name fidelity (`health_status`,
`service_type`, etc.) for free instead of risking drift from a hand-copied
struct.

**Related requirements:** REQ-E2E-050, REQ-E2E-060, REQ-E2E-080

---

## DD-138: Patch around control-plane#42 instead of forking the chart or blocking on it

**Decision:** the workflow installs `control-plane`'s chart with
`helm install` (no `--wait`), then `kubectl rollout status`-waits on
Postgres/NATS, then `kubectl patch`es `dcm-control-plane`'s
`wait-for-postgres` init container's `securityContext.runAsNonRoot` to
`false` (strategic merge, only that one field), then `kubectl
rollout status`-waits on `dcm-control-plane` itself.

**Rationale:** empirically, `control-plane`'s own chart's
`dcm.waitForPostgres` helper sets `runAsNonRoot: true` with no `runAsUser`
against the official `postgres:16-alpine` image (root-by-default), which the
kubelet permanently refuses to start on plain Kubernetes (`kind` has no
OpenShift-SCC-style automatic non-root UID injection) —
`CreateContainerConfigError`, confirmed via a real run of this workflow
(first real evidence anyone in DCM has produced that this chart doesn't
install on non-OpenShift Kubernetes at all). Filed upstream as
[control-plane#42](https://github.com/dcm-project/control-plane/issues/42)
per this project's standing practice of flagging cross-repo inconsistencies
to their owning team rather than silently working around them. Forking the
chart was rejected for the same reason as DD-134 (this repo consumes
`control-plane` as a published artifact, not a fork); blocking this PR on
`control-plane#42` landing and releasing was rejected as directly
contradicting "ready to run asap." The patch touches only the one field
actually causing the failure, leaving the chart's other hardening
(`allowPrivilegeEscalation`, `capabilities.drop`, `seccompProfile`) intact.
**This step should be deleted once control-plane#42 is fixed and released**
— it is not a permanent feature of this workflow.

**Related requirements:** REQ-E2E-020

## DD-139: `osac-mock-provider`'s OIDC discovery documents derive `token_endpoint` from the request's `Host` header, not the listener's bind address

**Decision:** `internal/mockprovider.OIDCHandler`'s discovery-document
handler builds `token_endpoint` as `"http://" + r.Host + "/token"` per
request, computed at request time from the incoming `http.Request`'s own
`Host` field. `NewOIDCHandler` no longer takes a `tokenURL` parameter;
`cmd/osac-mock-provider/main.go` no longer computes one from
`oidcLn.Addr().String()`.

**Rationale:** the first real run of the kind-based e2e infra
(`osac-sp-e2e-suite`, [#20](https://github.com/dcm-project/osac-service-provider/pull/20))
caught a genuine bug the mock's own unit/integration tests couldn't: TC-E2E-050
and TC-E2E-070 failed because `osac-sp`'s OIDC token fetch got
`dial tcp [::]:9091: connect: connection refused`. Root cause — the pre-fix
`NewOIDCHandler(tokenURL, logger)` baked `tokenURL` from
`oidcLn.Addr().String()` once at startup in `main.go`. That's correct for a
loopback-bound listener (`127.0.0.1:0`, what TC-I-031's integration test and
all prior unit tests used) but wrong for a wildcard bind (`:9091`, what
`test/e2e/manifests/osac-mock-provider.yaml`'s `MOCK_OIDC_ADDRESS` uses so the
Service can reach the pod at all): `net.Listener.Addr().String()` on a
wildcard bind reports `[::]:9091`, which is the local kernel's own unspecified
-address representation, not a routable address any other pod can dial. No
existing test exercised a wildcard bind's discovery document, so this shipped
undetected until a real cross-pod network call in CI exposed it — exactly the
class of bug this e2e tier exists to catch (spec's §1 "closes the specific gap
control-plane#40 identified").

The fix (deriving `token_endpoint` from each request's `Host` header instead)
is the standard technique real OIDC providers use to self-reference correctly
regardless of how a client reached them (a wildcard-bound server has no way to
know an externally-reachable address for itself in advance; the request that
just arrived does). It requires no new configuration and is not a special
case for e2e/Kubernetes: it also transparently keeps working for
TC-I-031's loopback-address integration test (`r.Host` there is whatever
`127.0.0.1:<port>` the test's own client dialed) and for the unit suite's
`httptest.Server`-backed tests (`r.Host` is `httptest.Server`'s own
`127.0.0.1:<port>`).

Added TC-U-152 as the regression test: two requests carrying different `Host`
headers to the same handler instance must get back two different
`token_endpoint` values matching each request's own `Host` — this is what the
pre-fix, construction-time-fixed `doc` value could never satisfy.

**Related requirements:** REQ-MOCK-080

## DD-140: `test/e2e`'s TC-E2E-070 asserts `control-plane`'s real `"ready"` vocabulary, not `osac-sp`'s `"healthy"`

**Decision:** TC-E2E-070's assertion on `control-plane`'s `ListProviders`
`health_status` field checks for the literal string `"ready"`
(`healthStatusReady`, a local constant mirroring `control-plane`'s own
unexported `internal/sp/store/model.HealthStatusReady`), not `"healthy"`.

**Rationale:** the test-plan's original TC-E2E-070 description assumed
`control-plane` would echo whatever status string the polled provider's own
`/health` endpoint returns. The first real e2e run proved that assumption
wrong: `control-plane`'s `healthcheck.Monitor.performHealthCheck` reads the
polled provider's `status` field and translates it into its **own**
three-value vocabulary (`model.HealthStatusReady = "ready"` /
`HealthStatusUnhealthy = "unhealthy"` / `HealthStatusUnavailable =
"unavailable"`, confirmed directly against `control-plane`'s source in the
Go module cache) — it does not pass the polled string through verbatim.
`osac-sp`'s own `/health` reporting `"healthy"` and `control-plane` recording
`"ready"` for that same provider are therefore both correct, independent
statements at two different layers, not a contract mismatch. This is exactly
the class of cross-repo wire-contract fact this e2e tier exists to pin down
empirically rather than assume (spec's `AC-E2E-030` note: "confirmed against
the real API at implementation time").

**Related requirements:** REQ-E2E-060

## DD-141: `Server.Run`'s internal readiness self-probe retries indefinitely (gated only by ctx cancellation), not just once per a fixed timeout

**Decision:** `internal/apiserver.Server.Run` now gates its `onReady`
callback (which starts registration, REQ-REG-050) on a new
`waitForReadyUntilCancelled` method, not a single call to `waitForReady`.
`waitForReadyUntilCancelled` repeatedly runs `waitForReady`'s existing
bounded polling window (unchanged: still `readinessProbeTimeout` = 5s,
`readinessProbeInterval` = 50ms, still fully covered by TC-U-077/078/079/089
exactly as before) and, whenever one window elapses without success, retries
a fresh window — paced by `readinessInterval` between attempts — instead of
giving up. The **only** way `waitForReadyUntilCancelled` ever returns an
error (and thus permanently skips `onReady`) is if the server's shutdown
context is cancelled before a window ever succeeds.

**Rationale:** a real run of the kind-based e2e infra
(`osac-sp-e2e-suite`, [#20](https://github.com/dcm-project/osac-service-provider/pull/20),
[failed CI run](https://github.com/dcm-project/osac-service-provider/actions/runs/30958037842/job/92155524084))
caught a genuine production reliability bug in `osac-sp` itself — not the
mock or the e2e infra — that no unit or integration test had exercised: the
`kind` node was running Postgres, NATS, `control-plane`, `osac-sp`, and
`osac-mock-provider` simultaneously, and under that real CPU contention the
pod's own `/health` responses slowed to ~1s each (visible in
`osac-service-provider`'s logs). The pre-fix `Run` called `waitForReady`
exactly once with its single 5-second window; that window elapsed with zero
successful responses, so `Run` logged `"readiness probe failed, skipping
onReady callback"` and **never called `onReady` again for the rest of the
process's life** — `internal/registration.Registrar` never started, so
`osac-sp` never registered with `control-plane` at all. The pod otherwise
ran fine: `/health` kept returning 200 OK moments later once contention
eased, and the pod's own Kubernetes readinessProbe (a separate, external,
repeatedly-retried check — unaffected by this bug) considered it `Ready`
throughout. Only the one-shot internal self-probe's single window mattered,
and it had already permanently given up before the node settled down. All
four e2e specs downstream of registration (TC-E2E-020/030/040/070) then
correctly failed with "expected 1 provider, got 0" / "control-plane never
recorded ready" after their own 60s polling windows — the e2e assertions
were right; `osac-sp`'s own startup resilience was the actual bug.

This directly contradicts this codebase's own stated resilience philosophy
(CLAUDE.md's "Non-blocking bootstrap": OIDC token fetch and the OSAC gRPC
dial both "retry indefinitely with backoff" and never permanently give up)
— the readiness self-probe was the one bootstrap step that *did* permanently
give up, and no REQ/AC ever specified that one-shot behavior; it was purely
an implementation artifact never deliberately specified (TC-U-078's mapped
REQ-HTTP-030 is actually about SIGTERM graceful shutdown, reused loosely for
lack of a dedicated readiness REQ at the time). A pod that is merely slow to
start — not permanently broken — should not be permanently prevented from
ever registering; requiring a full pod restart to recover from a transient
startup hiccup is a worse outcome than simply retrying, since retrying costs
nothing (the probe target is the process's own loopback listener, not a
remote/rate-limited service, so no backoff is needed the way the OIDC/gRPC
loops need one against a real remote server).

The fix deliberately leaves `waitForReady` itself, and its three existing
unit tests (TC-U-078's timeout-error contract, TC-U-079's
context-cancellation contract, TC-U-089's malformed-address contract), fully
unchanged — the bug was in `Run`'s one-shot *use* of `waitForReady`'s
result, not in `waitForReady`'s own per-window behavior. Added REQ-REG-052 /
AC-REG-031 to codify the required resilient-gating behavior, and TC-U-153
(retries past one elapsed window and still succeeds) / TC-U-154 (context
cancellation, not an elapsed window, is what actually stops retrying) as
regression coverage — see `.ai/test-plans/osac-sp-unit.test-plan.md`.

**Related requirements:** REQ-REG-052

## DD-142: TC-E2E-050/060 poll (`Eventually`) for `osac-sp`'s health status instead of asserting it in a single request

**Decision:** `test/e2e/health_test.go`'s `eventuallyHealthy` helper wraps
`GET /api/v1alpha1/{clusters,vms}/health` in `Eventually(..., 30*time.Second,
500*time.Millisecond).Should(Equal("healthy"))`, returning the converged
response for further field assertions. TC-E2E-050 and TC-E2E-060 now both
call it independently, rather than each calling the prior single-shot
`getHealth` directly.

**Rationale:** prompted directly by the user questioning a suspiciously fast
(~1.8s) `kubectl wait --for=condition=Available` in a
[passing run](https://github.com/dcm-project/osac-service-provider/actions/runs/30959010814/job/92158554429)
right after DD-141 landed — "are you sure osac was deployed?" Investigating
that question surfaced a real, separate latent flakiness risk (not the
question's literal premise: the deployment *was* genuine — image already
`kind load`-ed, `Deployment`/`Service` objects created, pod scheduled — but
the reasoning for *why* "Available" in ~1.8s is unsurprising, and *not*
strong evidence of full OSAC connectivity, exposed the actual bug below).

`kubectl wait --for=condition=Available` and Kubernetes' own `readinessProbe`
both only check the HTTP status code (2xx) of `GET
/api/v1alpha1/clusters/health`. Per `internal/health.Handler.checkHealth`
(and CLAUDE.md's documented API design, DD-010), that endpoint **always**
returns HTTP `200` — the real health verdict is only in the JSON body's
`status` field (`"healthy"`/`"unhealthy"`), which is deliberately invisible
to a plain `httpGet` probe. So "Available" in ~1.8s only proves the
container started and its HTTP server bound — not that `osac-sp`'s
`internal/osac.Bootstrap` had actually finished its real OIDC token fetch +
gRPC probe against `osac-mock-provider` yet.

Auditing what *does* prove that led to the actual finding: TC-E2E-050/060
(pre-fix) called `getHealth` exactly once and asserted `status ==
"healthy"` directly, with no retry. In the run under discussion, this
"worked" only because Ginkgo v2 by default randomizes the order of
*top-level containers* between runs (a fresh, printed "Random Seed" each
run — confirmed non-deterministic), and this run happened to schedule the
"registration" `Describe` block — whose TC-E2E-040 spec unconditionally
`Consistently`-waits a fixed 90 seconds — before the "health" `Describe`
block, incidentally giving `Bootstrap` 90+ seconds of real wall-clock time
to converge before the health assertions ever ran. Nothing guarantees that
ordering: a run whose seed schedules "health" first could hit the
single-shot check within ~1-2s of pod start, before `Bootstrap`'s
async OIDC/gRPC handshake against `osac-mock-provider` (itself racing
against `osac-mock-provider`'s own pod becoming `Ready` and its Service
gaining endpoints) necessarily completes — the exact same class of
startup-timing race DD-141 fixed in `Server.Run`'s own internal readiness
gate, just on this suite's side instead of `osac-sp`'s.

The fix makes each `It` self-sufficient rather than implicitly depending on
another, unrelated `Describe` block's incidental duration — matching the
polling discipline TC-E2E-070 already used for exactly the same class of
async-convergence reason. Priority given to per-spec independence (each
`It` converges on its own) over relying on Ginkgo's within-container
declaration-order guarantee (which does hold, but is a more fragile,
implicit inter-spec dependency to lean on than making every spec correct in
isolation, including under `--focus`/individual execution).

No REQ/AC change: `.ai/specs/osac-sp-e2e-suite.spec.md`'s AC-E2E-030 already
just requires the body to report healthy, not a specific
single-shot-vs-polling implementation strategy. `.ai/test-plans/
osac-sp-e2e-suite.test-plan.md`'s TC-E2E-050/060 descriptions were updated
to document the polling discipline explicitly.

**Related requirements:** REQ-E2E-060

---

## DD-145: `osac-mock-provider`'s `Clusters/GetKubeconfig` is implemented, correcting Phase 1's original out-of-scope call

**Decision:** `internal/mockprovider/clusters.go`'s `ClustersServer` now
implements `GetKubeconfig` (REQ-MOCK-120): for a known `id` it returns a
deterministic, non-functional stub kubeconfig (base64-encoded YAML with the
cluster `id` as its context name); for an unknown `id` it returns gRPC
`NOT_FOUND`, mirroring the other four CRUD-shaped services' `Get` semantics
(REQ-MOCK-040). `GetKubeconfigViaHttp`/`GetPassword(ViaHttp)` remain
unimplemented (still genuinely uncalled by `osac-sp`).

**Rationale:** found while building M3/M4 CRUD e2e coverage on top of this
mock, not from re-reading the original architecture diagrams. Phase 1's
spec (`.ai/specs/osac-sp-e2e-mock-provider.spec.md` §1) had explicitly
scoped plain `GetKubeconfig` out, reasoning "none of these are called by
`osac-sp` today (Milestone 3/4's architecture diagrams only ever invoke
Create/Get/List/Delete)" — true of the diagrams, false of the actual M3
implementation: `internal/cluster.Service.Get` calls
`Clusters/GetKubeconfig` whenever the mapped status is `ACTIVE`
(`osac-sp-m3-cluster-crud.spec.md` REQ-GET-020). Since `Clusters/Create`
sets a terminal `CLUSTER_STATE_READY` immediately (REQ-MOCK-030, no
simulated `PROGRESSING` delay), *every* `Get` of a mock-created cluster
maps to `ACTIVE` and hits this path — so leaving `GetKubeconfig`
`UNIMPLEMENTED` would have made the very first `cluster_crud_test.go` e2e
spec's `Get` call fail with a mapped `500`, not the `200` it exercises.
This is exactly the class of gap this M3/M4 e2e validation work exists to
catch before it reaches a real deployment.

**Related requirements:** REQ-MOCK-120, REQ-GET-020 (M3)

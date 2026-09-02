# Design Decisions: OSAC Service Provider

This document records architectural and design decisions for the OSAC
Service Provider, referenced by ID (`DD-NNN`) from the specs in
`.ai/specs/`. New decisions are appended here as implementation surfaces
them, so this file stays open across milestones rather than being tied to
any single spec document's lifecycle.

**Related Specs:** `.ai/specs/osac-sp.spec.md` (Milestone 1),
`.ai/specs/osac-sp-m3-cluster-crud.spec.md` (Milestone 3),
`.ai/specs/osac-sp-m4-vm-crud.spec.md` (Milestone 4),
`.ai/specs/osac-sp-m5-status-reporting.spec.md` (Milestone 5),
`.ai/specs/osac-sp-m6-version-matrix.spec.md` (Milestone 6)

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

**Superseded by DD-203**, once `control-plane#51` deleted the Phase 1 SP
registration API this decision depended on and forced the Phase 2 migration
this decision had deferred — see #33. Kept here for historical record per
this project's DD-numbering discipline — do not re-litigate; the two-phase
framing and the maturity-comparison rationale below remain useful context
for *why* Phase 1 targeted `control-plane` in the first place, even though
the registration target itself has moved on.

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

which key to size — multi-node-set templates are out of scope for this
milestone's single `nodes.worker.count` sizing dimension.

**Rationale:** The M3 spec originally assumed `node_sets[key]`'s `key`
equals `provider_hints.osac.template_id`. Verified directly against
[`cluster_template_type.proto`](https://github.com/osac-project/fulfillment-service/blob/73ae26e8cb0a476d4b035b18776603f60a361ed9/proto/public/osac/public/v1/cluster_template_type.proto)
and `private_clusters_server.go`'s node-set validation
(osac-project/fulfillment-service): node-set keys are arbitrary,
per-template strings chosen by whoever authored the template (a test
fixture defines template `"my-template-id"` with node-set keys
`"compute"`/`"gpu"` — the key is never the template's own `id`), and
OSAC validates `Cluster.spec.node_sets` keys against exactly the map
`ClusterTemplates/Get`/`List` return. This is the same discovery path
OSAC's own CLI (`osac scale --node-set <name>`) and UI
(`useClusterTemplate`) use before referencing a node-set by name — there
is no `template_id`-based shortcut.

This also surfaced an open sizing-model question: a template may define
more than one node-set key (e.g. separate `"compute"`/`"gpu"` worker
pools), but DCM's generic Cluster schema carries only a single
`nodes.worker.count`. The `osac-sp` enhancement doc never introduces a
second sizing dimension or a `provider_hints.osac.node_set` hint — its
Drawbacks section frames sizing as one coarse dimension tied to whichever
discrete host types the provisioned OSAC templates expose, implying DCM
catalog admins are expected to select single-worker-node-set templates.
Rejecting multi-key templates outright (rather than guessing, or silently
sizing only one key) enforces that assumption instead of merely hoping for
it, consistent with this repo's error-on-ambiguity convention (DD-100).
Revisit if a real DCM catalog item needs a genuinely heterogeneous
(multi-node-set) template — that requires an enhancement-doc change (a new
provider hint), not a unilateral SP-side guess.

**Consequence:** `osac.public.v1.ClusterTemplates` (`cluster_template_type.proto`/
`cluster_templates_service.proto`) must be vendored and generated alongside
the already-vendored `Clusters` service (M3 spec §1) — `internal/cluster`'s
Create path now depends on two OSAC clients, not one.

**Related requirements:** REQ-CREATE-080, REQ-CREATE-090

---

## DD-111: Unknown `template_id` maps to `400 Bad Request` (`InvalidArgument`), not `404`

**Decision:** When `ClusterTemplates/Get(template_id)` returns gRPC
`NotFound`, the SP returns `400 Bad Request` — not `404` — without calling
`Clusters/Create`.

**Rationale:** `template_id` is a value the caller supplied inside the
request body, not a path-addressed resource the caller is directly
operating on. `REQ-CREATE-060` already treats an absent/empty `template_id`
as a `400`-worthy request-validation failure; treating a *present but
nonexistent* `template_id` as a `404` instead would be an inconsistent split
of the same underlying problem (a bad value in the caller's own request)
across two different HTTP semantics depending on whether the value is empty
or merely wrong. `404` stays reserved for `GET`/`DELETE` operating directly
on a `Cluster` resource by its own `id` (REQ-GET-040), where the missing
resource *is* the thing being addressed.

**Related requirements:** REQ-CREATE-100

---

## DD-113: `POST /clusters` is schema-optional on `id` and its body is the `Cluster` resource itself, to satisfy AEP-133

> Renumbered from this branch's original DD-110 — [#12](https://github.com/dcm-project/osac-service-provider/pull/12)
> claimed DD-110 for the (unrelated) node-set-key-resolution decision while
> this branch was in flight; see `proto/README.md`.

**Decision:** The `id` query parameter on `POST /api/v1alpha1/clusters` is
`required: false` in the OpenAPI schema, and the request body schema is
`$ref: '#/components/schemas/Cluster'` (with a new optional `spec` property
added to that shared resource schema) rather than the previous
`ClusterCreateRequest` wrapper. REQ-CREATE-010/060's actual runtime
contract — `id` and `spec` are both effectively required, and their absence
is a `400` — is unchanged; it is now enforced entirely by
`internal/handlers/cluster`'s own `validateCreateRequest`, not by the
OpenAPI `required` keyword.

**Rationale:** CI's `check-aep` job flagged two `aep-133` violations once
the first `POST` landed in this repo's schema: a required `id` query param,
and a request body that isn't an AEP resource (`ClusterCreateRequest`).
Both are schema-level lint rules, not a wire-format constraint —
`control-plane`'s real dispatch envelope (DD-080) doesn't need either.
Matches the sibling SPs'
([`acm-cluster-service-provider`](https://github.com/dcm-project/acm-cluster-service-provider),
[`k8s-container-service-provider`](https://github.com/dcm-project/k8s-container-service-provider))
existing shape for `Create`, so this isn't a new pattern.

`Cluster`'s `required` list is `[id, status, spec]` — `spec` was added
alongside the pre-existing `id`/`status` rather than replacing them, since
OpenAPI 3.0 scopes `readOnly`+`required` to responses only and
`writeOnly`+`required` to requests only; a single bidirectional AEP-133
resource schema can express both per-direction requirements in the same
`required` array. Verified this against actual `oapi-codegen` output before
relying on it (an earlier version of this decision assumed the opposite,
incorrectly — see PR #13 review thread `discussion_r3767520271`): both
`Spec *ClusterSpec` and `Status *ClusterStatus` generate as pointers with
`omitempty` regardless of `required` membership, driven entirely by their
own `readOnly`/`writeOnly` flags. So adding `spec` to `required` does not
make it non-pointer and does not force a spurious `"spec":{}` on Get/List
responses. Also didn't copy the siblings' server-side UUID generation when
`id` is omitted — REQ-CREATE-010 already guarantees `control-plane` always
supplies one.

**Related requirements:** REQ-CREATE-010, REQ-CREATE-060

---

## DD-114: `check-aep` is now part of `make check`, invoked via `npx` instead of requiring a global `spectral` install

> Renumbered from this branch's original DD-111 for the same reason as
> DD-113 above.

**Decision:** `make check`'s prerequisite list is now `fmt vet lint check-aep
test` (previously omitted `check-aep`), and the `check-aep` target itself now
runs `npx --yes @stoplight/spectral-cli lint ...` instead of assuming a
bare `spectral` binary is already on `PATH`.

**Rationale:** `check-aep` was a CI gate since Milestone 1 but never a
prerequisite of `make check` — the local pre-push command `CLAUDE.md`
documents — and its local invocation assumed a bare `spectral` binary on
`PATH`, which nothing in this repo provisions (CI self-installs it fresh
every run). AEP-133's create-specific rules had also never had a chance to
fire before PR #13's Create endpoint (Milestones 1-2 had no `POST`s), so
this was a dormant gap, not a regression. `npx` removes the missing-binary
friction; adding `check-aep` to `make check`'s prerequisite list removes the
"forgot to run the separate target" failure mode.

**Related requirements:** REQ-CREATE-010, REQ-CREATE-060 (DD-113); process
fix has no REQ-* of its own — it is tooling/workflow, not product behavior.

---

## DD-112: `CLUSTER_STATE_UNSPECIFIED` maps to `PROGRESSING`, not `FAILED`

**Context:** ported from Milestone 4 (PR #14, `internal/vm/status.go`
DD-129), found while recording the DCM-first demo-journey against real
infrastructure. Milestone 4's VM mapper showed `FAILED` for several
seconds immediately after every VM creation, self-correcting to
`PROVISIONING`/`RUNNING` shortly after; `internal/cluster/status.go` has
the identical unhandled-default gap for `CLUSTER_STATE_UNSPECIFIED`.

**Root cause:** `REQ-STATUS-020`'s original rule (9) mapped "anything
else, including `CLUSTER_STATE_UNSPECIFIED`" to `FAILED` as a "defensive
default," per
[`service-provider-status-reporting.md#cluster-status`](https://github.com/dcm-project/enhancements/blob/main/enhancements/state-management/service-provider-status-reporting.md#cluster-status)'s
either/or guidance for ambiguous states. This was deliberate and tested
(`TC-U-240`), but — like VM's identical mapper — written and verified
only against hand-written fakes, never against a real
`fulfillment-service`/`osac-operator` pair. `CLUSTER_STATE_UNSPECIFIED`
is proto3's structural zero-value (`cluster_type.proto`'s own comment:
"Unspecified indicates that the state is unknown"), and is the normal
state every `Cluster` briefly holds between creation and
`osac-operator`'s first reconcile pass — not a genuine anomaly. This
milestone's Cluster CRUD isn't exercised in the demo directly, but the
same live-verified reasoning from DD-129 applies identically here.

**Decision:** `REQ-STATUS-020` now maps `CLUSTER_STATE_UNSPECIFIED` →
`PROGRESSING` (new rule 3, ahead of the `FAILED`/`DELETING`/
`DELETE_FAILED` checks), matching the "closest active state" half of the
upstream guidance instead of the `FAILED` half — mirroring
`CLUSTER_STATE_PROGRESSING`'s own mapping. `internal/cluster/status.go`
gained an explicit early-return for it. The `default` branch remains,
now scoped to genuinely future/unmodeled enum values only.

**Upstream gap flagged:** same two issues as DD-129 — filed against
`osac-project` (proto `*_STATE_UNSPECIFIED` enum comments don't document
temporal semantics) and `dcm-project/enhancements`
(`service-provider-status-reporting.md`'s ambiguous-state guidance
conflates "not yet reported" with "genuinely anomalous" under one
either/or).

**Related requirements:** `REQ-STATUS-020`, `AC-STATUS-010`.

---

# Milestone 5 (Status Reporting) — pre-resolved recommendations

**Status: proposed, not yet ratified.** Milestone 5 has not started — no
spec exists yet, and per this repo's own spec-first/test-plan-first gate, no
`REQ-*`/`AC-*` or `TC-*` exist for it either. The two entries below
(`DD-200`–`DD-201`) are numbered in a block well clear of Milestone 3's
(`DD-080`..`DD-111`, on `feat/milestone-3-cluster-crud`, unmerged as of this
writing) and Milestone 4's (`DD-080`..`DD-086`, on
`feat/milestone-4-vm-crud`, unmerged as of this writing) independently-
numbered, not-yet-merged decisions, specifically to avoid a numbering
collision when those two branches eventually land. **Renumber into the
normal sequence (and drop "proposed" framing) once M5's spec formally
starts** — these are not a substitute for that gate.

Two further research findings (dependency versions to pin, and a
contract-test gap for the CloudEvents `data` payload) came out of the same
research pass but are implementation guidance, not durable architectural
decisions — they live in `.ai/exploration/m5-status-reporting-research.md`
(local-only) instead, for whoever writes M5's actual spec to verify against
current reality at that time.

## DD-120: VM CRUD dispatch contract mirrors Cluster's; `ComputeInstances/Delete` is fully implemented today, not gated on OSAC

**Decision:** Milestone 4 (VM CRUD) reuses Milestone 3's dispatch contract
verbatim — `control-plane` calls `POST /api/v1alpha1/vms?id=X`,
`DELETE /api/v1alpha1/vms/{vmId}`, and never calls `GET`/`List` (it serves
those from its own store) — with `osac.public.v1.ComputeInstances` standing
in for `osac.public.v1.Clusters`. No new dispatch envelope is introduced.

**Rationale:** verified directly against OSAC's actual backend source
(`osac-project/fulfillment-service` at
[`c4110b2`](https://github.com/osac-project/fulfillment-service/blob/c4110b28a14d4a3b3926ae5360e2cd59c15430d5)),
not inferred from the proto alone, in response to the user's question of
whether `ComputeInstances/Delete` might still be unimplemented pending OSAC:
it is **not**. Both the public
[`compute_instances_server.go#L327-L342`](https://github.com/osac-project/fulfillment-service/blob/c4110b28a14d4a3b3926ae5360e2cd59c15430d5/internal/servers/compute_instances_server.go#L327-L342)
and private
[`private_compute_instances_server.go#L305-L309`](https://github.com/osac-project/fulfillment-service/blob/c4110b28a14d4a3b3926ae5360e2cd59c15430d5/internal/servers/private_compute_instances_server.go#L305-L309)
`Delete` methods are implemented today via the same generic,
type-parameterized DAO server (`GenericServer[*privatev1.ComputeInstance]`)
that backs `Clusters/Delete` — no `TODO`, no stub, no feature flag. VM
deletion has **no** analog of Cluster's tracked teardown-ambiguity gap
(`OSAC-1586`/`OSAC-1391`): the `computeinstance` reconciler's `delete()`
waits for the underlying Kubernetes object to be confirmed gone before
clearing its finalizer, so a VM's `404` reliably means it is actually torn
down — VM delete is, if anything, more reliable than Cluster's today, not
less.

**Consequence:** VM Delete needs no gating, feature-flag, or "wait for OSAC"
caveat — REQ-VMDELETE-* below is implemented unconditionally, same as
Cluster's REQ-DELETE-*.

**Related requirements:** REQ-VMDELETE-010, REQ-VMDELETE-020

---

## DD-121: VM status uses its own 8-value DCM vocabulary with a direct, condition-free state mapping

**Decision:** The VM status mapper returns one of DCM's VM-specific
lifecycle phases — `PROVISIONING | RUNNING | STOPPED | FAILED | DELETING |
STOPPING | PAUSED | DELETED` — per
[`service-provider-status-reporting.md#vm-status`](https://github.com/dcm-project/enhancements/blob/main/enhancements/state-management/service-provider-status-reporting.md#vm-status).
This is a **different** enum from Cluster's 7-value vocabulary (DD-090) —
reusing Cluster's enum for VMs, or vice versa, would be wrong for either
resource type. Mapping is a direct 1:1 translation of
`ComputeInstanceState` (`STARTING`→`PROVISIONING`, `RUNNING`→`RUNNING`,
`FAILED`→`FAILED`, `DELETING`→`DELETING`, `STOPPING`→`STOPPING`,
`STOPPED`→`STOPPED`, `PAUSED`→`PAUSED`), plus the same `NotFound`→`DELETED`
API-response rule Cluster uses.

**Rationale:** verified directly against `ComputeInstanceCondition`/
`ComputeInstanceConditionType` in the vendored `compute_instance_type.proto`
— unlike Cluster's `CLUSTER_CONDITION_TYPE_DEGRADED`, none of VM's six
condition types (`CONFIGURATION_APPLIED`, `READY`, `RESTART_IN_PROGRESS`,
`RESTART_FAILED`, `PROVISIONED`, `RESTART_REQUIRED`) correspond to a
`DEGRADED`-like concept, and DCM's VM vocabulary has no `DEGRADED` value to
map one to even if it existed. The VM mapper therefore has no
condition-precedence step at all — a meaningful simplification versus
Cluster's mapper (DD-090), not an oversight.

Also unlike Cluster's vocabulary, VM's has no `UNAVAILABLE` value. When the
gRPC call itself fails with `Unavailable`/`DeadlineExceeded` (OSAC
unreachable), the mapper returns `FAILED` — the closest fit per
`service-provider-status-reporting.md`'s own guidance ("if a provider has a
state that is ambiguous, they should default to... `FAILED` if
functionality is impaired"). This is a real, deliberate divergence from
Cluster's precedent (which has a dedicated `UNAVAILABLE` bucket to use
instead) — not an inconsistency to reconcile later.

**Consequence:** `internal/vm`'s status mapper is a separate implementation
from `internal/cluster`'s (different input enum, different output
vocabulary, no shared logic to extract) — no code-sharing attempt is made
between the two, and none is warranted.

**Related requirements:** REQ-VMSTATUS-010, REQ-VMSTATUS-020

---

## DD-122: `provider_hints.osac.instance_type` is required on every VM Create — no direct `cores`/`memory_gib` fallback exists

**Decision:** VM Create requires `spec.provider_hints.osac.instance_type`
on every request; a request omitting it is rejected `400 Bad Request`
before any OSAC call is made. `spec.vcpu.count`/`spec.memory.size` are
accepted (DCM's generic `VMSpec` schema requires them) but are
**informational only** on this SP — never translated to any OSAC field.
Best-fit resolution of an `instance_type` from raw `vcpu`/`memory` via
`InstanceTypes/List` was considered and explicitly rejected for v1.

**Rationale:** the enhancement doc's original "VM Sizing" resolution (keep
`vcpu.count`/`memory.size` mapped directly to OSAC's `cores`/`memory_gib`,
accepting a deprecation warning) turned out to be based on a stale
understanding of OSAC's contract. Verified directly against the vendored
`compute_instance_type.proto` and confirmed identical on
`fulfillment-service`'s current `main`
([`c4110b2`](https://github.com/osac-project/fulfillment-service/blob/c4110b28a14d4a3b3926ae5360e2cd59c15430d5/proto/public/osac/public/v1/compute_instance_type.proto#L134-L136)):
`cores`/`memory_gib` are `reserved` (removed) fields, not merely
deprecated-with-warning — there is no code path left that accepts them.
Pushed a corrective PR against the enhancement
([dcm-project/enhancements#100](https://github.com/dcm-project/enhancements/pull/100))
before drafting this spec, per this project's existing precedent
(`OSAC-1586` correction landed the same way ahead of Milestone 3) and per
`CLAUDE.md`'s "open a PR against the enhancement first" rule. Presented two
options to the user (best-fit `InstanceTypes` matching vs. requiring the
hint explicitly) — the user chose the latter: simpler, and pushes the
sizing decision to the DCM caller rather than the SP guessing a catalog
entry on their behalf.

**Consequence:** no `InstanceTypes` client is needed in Milestone 4 (no
`InstanceTypes/List` call, no best-fit matching logic, no GPU-exclusion
logic that best-fit matching would have required). `provider_hints.osac`
for VMs now has two required fields (`template_id`, `instance_type`), not
one.

**Related requirements:** REQ-VMCREATE-020, REQ-VMCREATE-060

---

## DD-123: Disk capacity strings are parsed as GiB-colloquial, not strict SI/IEC units

**Decision:** `spec.storage.disks[*].capacity` (DCM strings like `"100GB"`,
`"2TB"`) are parsed into OSAC's `size_gib` (`int32`) by treating the numeric
prefix as already being in GiB when the unit is `GB`/`GiB` (i.e. `"100GB"`
→ `100`, not `100 × 10^9 / 2^30 ≈ 93`), multiplying by 1024 for `TB`/`TiB`,
and dividing by 1024 (rounding up) for `MB`/`MiB`. Units are matched
case-insensitively; anything else (unparseable string, unrecognized unit,
non-positive value) is rejected as `400 Bad Request` before calling OSAC.

**Rationale:** OSAC's own field is explicitly named `size_gib` (binary
GiB), and colloquial infrastructure/VM sizing usage (this SP's own
`ComputeInstanceDisk.size_gib` field comment, cloud VM catalog listings,
etc.) treats "100GB of disk" as meaning 100 GiB in practice, not a
decimal-SI 100×10⁹-byte quantity that would need lossy conversion to a
GiB integer. Treating DCM's `GB` unit as GiB directly avoids inventing an
unrequested decimal-to-binary rounding policy for a distinction no caller
in this schema is likely to intend precisely.

**Consequence:** this parser is the only unit-conversion logic Milestone 4
needs — `spec.memory.size` needs no parsing at all per DD-122 (never sent to
OSAC).

**Related requirements:** REQ-VMCREATE-030, REQ-VMCREATE-040

---

## DD-124: Default Network Provisioning is stateless (List-then-create-on-miss every Create), not a cached per-tenant mapping store

**Decision:** On every VM Create, the SP calls `Subnets/List` filtered by
`dcm.io/managed-by == "dcm" && dcm.io/service-type == "vm-default-network"`
(both labels, not managed-by alone — see the note appended below). If a
matching subnet exists, its `id` is reused. If not, the SP creates a
`VirtualNetwork` (fixed CIDR `10.200.0.0/16`, IPv4-only, `network_class`
omitted so the platform default is used) and a `Subnet` under it (fixed
CIDR `10.200.1.0/24`), both tagged with the same ownership labels as
Cluster/ComputeInstance resources, then polls both (`Get`, 500ms interval,
15s total timeout) until `READY` before using the new subnet's `id`. No
local ID-mapping/cache store is introduced.

**Rationale:** the enhancement doc's "Default Network Provisioning"
section describes caching the resolved subnet ID in "the SP's local mapping
store" per tenant. This SP has been deliberately stateless through
Milestones 1-3 (no ID-mapping store exists — DCM's identifier and OSAC's
`id` are always the same value, per the enhancement's own "ID Mapping"
section, and SC-M3-002 explicitly chose not to introduce a store for a
related VM-sizing/versioning concern). Introducing the SP's first-ever
persistence layer solely to cache one subnet ID — which the enhancement
doc itself admits resolves to exactly one shared subnet for all tenants in
v1, since `metadata.tenant` resolves to the SP's single assigned
Organization (see the enhancement's Non-Goals) — is not a good trade:
persistence adds a new failure mode (store unavailable, stale/orphaned
entries after a subnet is manually deleted in OSAC) to solve a problem an
extra `List` RPC already solves adequately at v1's scale.

**Known limitation (accepted for v1, not solved here):** two concurrent
first-ever VM Create calls can both observe "no subnet exists" and both
provision a `VirtualNetwork`/`Subnet` pair — there is no OSAC-side
uniqueness constraint on "the default one" to arbitrate this, unlike
`Cluster`/`ComputeInstance`'s `id`-based `AlreadyExists` idempotency (DD-100),
because no DCM-issued identifier is available to key a default network on.
A cached-mapping-store design has the identical race on its very first
write, so this is not a limitation caching would have actually avoided —
see [SC-M4-001](../specs/osac-sp-m4-vm-crud.spec.md#sc-m4-001) for the full
analysis. Acceptable for v1's expected low VM-creation concurrency; revisit
if it proves not to be.

**Consequence:** every VM Create makes at least one extra `Subnets/List`
RPC (and, only on the very first Create ever, two `Create` + polling
round-trips) versus the enhancement doc's cached design. No new
configuration keys are introduced for the poll interval/timeout — both are
hardcoded constants, consistent with `Bootstrap`'s existing
`initialBackoff`/`maxBackoff` pattern (DD-* not assigned — see
`internal/osac/bootstrap.go`).

**Related requirements:** REQ-VMNET-010 through REQ-VMNET-050

**Update (review finding):** the original filter checked only
`dcm.io/managed-by == "dcm"`, reusing the same `ownershipFilter` constant
`ComputeInstances/List` uses. That's too broad for this specific lookup —
it's meant to find one particular shared subnet, not just any DCM-managed
one — so a future DCM-managed subnet created for an unrelated purpose could
have been mistaken for the default network. Fixed to also require
`dcm.io/service-type == "vm-default-network"`, its own filter distinct from
`ComputeInstances/List`'s.

---

## DD-125: `POST /api/v1alpha1/vms` is AEP-133-compliant from the start

**Decision:** the `id` query parameter is schema-optional (`required:
false`) and the request body is the `VirtualMachine` resource itself
(`{"spec": {...}}`) rather than a dedicated `*CreateRequest` wrapper schema
— applying Milestone 3's DD-110 fix pattern proactively, on day one, rather
than discovering the same `aep-133-required-params`/`aep-133-request-body`
violations in CI a second time.

**Rationale:** DD-111 root-caused Milestone 3's PR #13 CI failure to
`check-aep` never having been run locally before that PR (no `POST`
endpoint existed before it to trigger AEP-133's create-specific rules) —
that process gap is now fixed (`check-aep` is part of `make check`,
`internal/handlers`/`internal/vm` implementation ordering below always runs
`make check` before opening the PR), but there is no reason to also
re-litigate the *design* question DD-110 already answered for Cluster.
"Required" `id`/`spec` semantics remain enforced at the runtime/behavioral
level (REQ-VMCREATE-060), exactly as DD-110 established for Cluster.

**Consequence:** `CreateVMParams.Id` and `CreateVMJSONRequestBody.Spec`
generate as pointer types (`*string`, `*v1alpha1.VMSpec`), same as
Cluster's equivalents — handler code must nil-check both before use, same
as `internal/handlers/cluster/create.go` already does.

**Related requirements:** REQ-VMCREATE-010, REQ-VMCREATE-070

---

## DD-126: gRPC-to-HTTP error classification is extracted into a shared `internal/grpcerror` package

**Decision:** the gRPC-code → HTTP-status/`v1alpha1.ErrorType`/title
mapping table (REQ-VMERR-010) lives in a new `internal/grpcerror` package
(`Classify(err error) (status int, errType v1alpha1.ErrorType, title
string)`), used by `internal/handlers/vm`. It is not duplicated
package-locally the way `internal/handlers/cluster/error.go` (Milestone 3,
PR #13, not yet merged at the time of this decision) implements the
identical table.

**Rationale:** this is the second time this exact mapping table needs to
exist. Extracting it now costs nothing and creates zero conflict risk with
PR #13 (`internal/grpcerror` is a new file `internal/handlers/cluster`
never touches), unlike attempting to retrofit `internal/handlers/cluster`
itself from this branch, which does not have that package's code to modify
(Milestone 4 branched from `main`, before Milestone 3 merged — see the M4
kickoff discussion). `internal/handlers/cluster/error.go` should adopt
`internal/grpcerror.Classify` in a follow-up cleanup once both Milestone 3
and 4 have landed on `main` — tracked informally here, not as a new `REQ-*`,
since it's a refactor with no behavioral effect.

**Consequence:** `internal/handlers/vm/error.go` is a thin adapter that
calls `internal/grpcerror.Classify` and constructs the operation-specific
`StrictServerInterface` response type — it does not reimplement the
switch statement.

**Related requirements:** REQ-VMERR-010, REQ-VMERR-020, REQ-VMERR-030

---

## DD-127: `ComputeInstance`/`Subnet`/`VirtualNetwork` reference fields must be `Reference` messages populated by `id`, not bare strings or `name`

**Context:** found live, against real infra (`fulfillment-service`
`v0.0.83`, real `osac-operator`, real AAP), while validating this
milestone's code end-to-end for a demo recording (see the sibling
`osac-sp` decisions/exploration around a scratch `scratch/m3-m4-m5-demo`
branch — not part of this repo's git history on this branch, but the
source of this fix). Two independent bugs, both in the same call path,
surfaced back-to-back:

**Bug 1 — wire-format shape.** `internal/vm/{translate,network,service}.go`
populated `ComputeInstanceSpec.template`/`.instance_type`,
`SubnetSpec.virtual_network`, and `NetworkAttachment.subnet` with bare Go
strings. The proto contract for all four fields is actually a `Reference`
message type (`ComputeInstanceTemplateReference`,
`InstanceTypeReference`, `VirtualNetworkLocalReference`,
`SubnetLocalReference`) — confirmed via `grpcurl describe` against a live
`fulfillment-service` and by diffing this repo's vendored
`proto/osac/public/v1/*.proto` files against the `fulfillment-service`
`v0.0.83` tag, which showed this repo's copies were stale relative to
that tag. Sending a bare string for a message-typed field fails
`grpcurl`/proto decoding outright; through `osac-sp`'s own generated
client it instead silently produced a wire payload
`fulfillment-service` couldn't unmarshal correctly. Fixed by re-vendoring
`compute_instance_type.proto`, `subnet_type.proto`,
`virtual_network_type.proto` (plus two newly-required transitive
dependencies, `instance_type_type.proto` and `security_group_type.proto`)
from the `fulfillment-service` `v0.0.83` tag, regenerating the Go stubs,
and wrapping every affected string in its `Reference` type
(`&publicv1.VirtualNetworkLocalReference{Id: vnetID}` etc.) at the three
call sites.

**Bug 2 — `Id` vs `Name`.** The initial fix for Bug 1 populated
`ComputeInstanceTemplateReference{Name: ...}` /
`InstanceTypeReference{Name: ...}` from
`spec.ProviderHints.Osac.{TemplateId,InstanceType}`. This is wrong:
those DCM-supplied strings (e.g. `osac.templates.ocp_virt_vm`,
`demo-small`) are OSAC's **`id`** field, not `metadata.name` — proven
against a live server (`ComputeInstanceTemplates/List` returned
`{"id": "osac.templates.ocp_virt_vm", "metadata": {"name":
"ocp-virt-vm"}}` — `id` and `metadata.name` differ for this resource
type; `InstanceTypes/List` happens to have them equal, which is why only
the template half of this bug was initially visible). Sending `Name:`
caused fulfillment-service's reference resolution to silently fail to
find any object (`reference validation failed: object.spec.template:
ComputeInstanceTemplate "osac.templates.ocp_virt_vm" not found`) — a
400, not Bug 1's 500, so it read as a distinct issue until traced back to
the same reference-message change. DCM's own field naming
(`OSACVMProviderHints.template_id`/`.instance_type` in `openapi.yaml` —
literally named "_id") was already the semantic hint this should have
used from the start. Fixed by switching both fields to `Id:` in
`internal/vm/translate.go`.

**Decision:** `internal/vm/translate.go`, `internal/vm/network.go`, and
`internal/vm/service.go` now populate `ComputeInstanceSpec.template`/
`.instance_type` by `Id` (DCM-supplied provider-hint strings are OSAC
ids, never names), and `SubnetSpec.virtual_network`/
`NetworkAttachment.subnet` by `Id` (OSAC-assigned ids returned from this
package's own prior `Create` calls). Updated the corresponding unit/
integration test assertions (`internal/vm/{network,create}_unit_test.go`,
`internal/handlers/vm/{create,crosscutting}_integration_test.go`) from
bare-string/`.GetName()` comparisons to `.GetId()`. No test *behavior*
changed — only the accessor path each assertion uses.

While diagnosing the live 500 this bug produced, also found and fixed a
related observability gap: `internal/handlers/vm/error.go`'s 500-class
error handling replaced the detail message with a generic string
(correctly, to avoid leaking internals to the caller) but never logged
the original error anywhere, making it impossible to see the real cause
from the server side either. Added a `slog.Error` call before masking.

**Verified (live, real infra, not mocked):** direct `grpcurl` calls
against a live `fulfillment-service` (`ComputeInstances/Create` with
`template: {id: "osac.templates.ocp_virt_vm"}`, `instance_type: {id:
"demo-small"}`) progressed cleanly past reference resolution into the
next, unrelated validation stage (`network_attachments` required) —
proof the fix is correct independent of `osac-sp` itself. Full suite
(11 suites, this branch) plus `make lint` pass clean after the fix.

**Related requirements:** none new — this is a correctness fix to this
milestone's existing `REQ-VMCREATE-*`/`REQ-VMNET-*` implementation, which
was developed and merged against a proto snapshot that predates
`fulfillment-service` `v0.0.83`. Every real `ComputeInstance`/`Subnet`/
`VirtualNetwork` `Create` call through this code, before this fix, hit
this class of failure (masked as an opaque `500` to the DCM caller).

---

## DD-128: `imageSourceType = "catalog"` (SC-M4-002) is rejected by OSAC's real `ComputeInstance` CRD — only `"registry"` validates

**Context:** found live, immediately after DD-127's fixes, during the same
end-to-end validation session: a `ComputeInstance` create returned
`HTTP 201` from `osac-sp` but the underlying provider resource settled
into `status: FAILED`. Querying the live `fulfillment-service` directly
(`ComputeInstances/Get` via `grpcurl`) showed
`COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED = False`, `reason:
"ReconciliationFailed"`, `message: "ComputeInstance.osac.openshift.io
\"vm-h5hff\" is invalid: [spec.image.sourceType: Unsupported value:
\"catalog\": supported values: \"registry\"]"` — `osac-operator`'s
underlying Kubernetes CRD rejected the object outright at admission/
reconciliation time.

**Root cause:** `internal/vm/translate.go`'s `imageSourceType` constant
was hardcoded to `"catalog"`, per SC-M4-002's spike finding that
`ComputeInstanceImage.source_type` is an untyped `string` at the
**proto** layer with no enum (still true — `compute_instance_type.proto`'s
`source_type` field has no enum, and its only doc-comment example is,
adding to the irony, `"registry"`). SC-M4-002's conclusion ("no correct
value to derive, so any non-breaking choice is fine") was correct about
the proto but was never verified against the real CRD's admission
validation, which **does** enforce an enum with (as of this OSAC
version) exactly one accepted value.

**Decision:** changed `imageSourceType` from `"catalog"` to `"registry"`.
No test assertions needed updating (none asserted on `SourceType`'s
value).

**Verified (live, real infra):** re-ran a `ComputeInstance` create (after
rebuilding/redeploying `osac-sp` with this fix) and confirmed the
resulting provider resource reconciled past the image-validation stage.

**Related requirements:** correctness fix to this milestone's
`REQ-VMCREATE-*` (SC-M4-002's spike conclusion was incomplete, not the
implementation — no REQ/AC text changes needed).

---

## DD-129: `COMPUTE_INSTANCE_STATE_UNSPECIFIED` maps to `PROVISIONING`, not `FAILED`

**Context:** the DCM-first demo-journey recording showed `dcm sp resource
list` reporting `FAILED` for a VM immediately after a successful `Create`,
self-correcting to `PROVISIONING`/`RUNNING` a few seconds later.

**Root cause:** `REQ-VMSTATUS-020`'s original rule (10) mapped "anything
else, including `COMPUTE_INSTANCE_STATE_UNSPECIFIED`" to `FAILED` as a
"defensive default," per
[`service-provider-status-reporting.md#vm-status`](https://github.com/dcm-project/enhancements/blob/main/enhancements/state-management/service-provider-status-reporting.md#vm-status)'s
either/or guidance for ambiguous states ("default to the closest active
state **or** `FAILED` if functionality is impaired"). This was a
deliberate, spec'd, and tested choice (`TC-U-350`), not an oversight —
but it was written and verified entirely against hand-written fakes
(this milestone's Tier A methodology), never against a real
`fulfillment-service`/`osac-operator` pair.

**Verified (live, real infra):** timed the transition on a real
`ComputeInstance` create — `dcm sp resource list` showed `FAILED` at
T+0.1s and T+2.6s, then self-corrected to `PROVISIONING` by T+5.2s.
`COMPUTE_INSTANCE_STATE_UNSPECIFIED` is proto3's structural zero-value
(`compute_instance_type.proto`'s own comment: "Unspecified indicates
that the state is unknown"), and is the normal, universal state every
`ComputeInstance` briefly holds between creation and `osac-operator`'s
first reconcile pass — not a genuine anomaly. Mapping it to `FAILED`
therefore produced a false failure on every single VM creation, not just
a rare edge case.

**Also confirmed systemic, not VM-specific:** `internal/cluster/status.go`
(Milestone 3, PR #13) has the identical unhandled-default gap for
`CLUSTER_STATE_UNSPECIFIED` (same proto zero-value comment, same
either/or guidance, same `FAILED` resolution) — ported the equivalent fix
there too (`CLUSTER_STATE_UNSPECIFIED` → `PROGRESSING`).

**Decision:** `REQ-VMSTATUS-020` now maps `COMPUTE_INSTANCE_STATE_UNSPECIFIED`
→ `PROVISIONING` (rule 3, ahead of `STARTING`), matching the "closest
active state" half of the upstream guidance instead of the `FAILED` half.
`internal/vm/status.go` gained an explicit case for it, plus an explicit
(previously default-only) case for `COMPUTE_INSTANCE_STATE_FAILED` for
clarity. The `default` branch remains, now scoped to genuinely
future/unmodeled enum values only.

**Upstream gap flagged:** filed issues against `osac-project` (proto
`*_STATE_UNSPECIFIED` enum comments don't document temporal semantics —
"unknown" doesn't say whether it's transient-at-creation or a genuine
anomaly) and `dcm-project/enhancements` (`service-provider-status-reporting.md`'s
ambiguous-state guidance conflates the two cases under one either/or with
no criteria for which applies).

**Related requirements:** `REQ-VMSTATUS-020`, `AC-VMSTATUS-010`.

---

# Milestone 6 (Version-Translation Compatibility Matrix)

## DD-130: A new standalone `internal/versionmatrix` package, not a shared field on an existing type

**Decision:** The version-translation compatibility matrix lives in a new,
standalone top-level package, `internal/versionmatrix`, exposing a `Matrix`
type, a `DefaultMatrix` value, and a `Load(path string) (Matrix, error)`
function. It depends on nothing else in this repo (not even
`api/v1alpha1`). `internal/registration.Registrar` and
`internal/cluster.Service` each hold their own copy of the single `Matrix`
value constructed once in `main.go` and passed to both.

**Rationale:** Before this milestone, `internal/registration` and
`internal/cluster` each maintained their own version list with no shared
type between them, and — critically — **neither package currently imports
the other**. Putting the shared `Matrix` type inside either package (e.g.
exporting `cluster.ReleaseImageByVersion` for `registration` to import, or
vice versa) would introduce a new, artificial coupling between two packages
whose only real relationship is "both need to agree on the same set of
supported Kubernetes versions" — not "one depends on the other's business
logic." A new standalone package makes that shared dependency explicit and
avoids having to pick an arbitrary "owning" side. `Matrix` is a plain
`map[string]string`-backed value type (not a pointer, not a struct wrapping
a mutex) specifically so both consumers can hold their own independent copy
of the same immutable data with no shared-mutable-state/locking concern —
the matrix is loaded exactly once at startup (`main.go`) and never mutated
afterward by either consumer.

**Related requirements:** REQ-VERSION-010, REQ-VERSION-020, REQ-VERSION-030

---

## DD-131: Hard rejection of unsupported versions, at the existing `validateCreateRequest` pre-flight layer — not a silent fallback, and not a new error type

**Decision:** When `provider_hints.osac.release_image` is absent and
`spec.version` has no matrix entry, Create MUST be rejected with `400 Bad
Request` (`codes.InvalidArgument`) **before ever calling OSAC** —
superseding the pre-Milestone-6 behavior of `releaseImage()` silently
returning `nil` and letting OSAC's per-template default `release_image`
take effect unannounced. The check itself is added as one more `case` in
`internal/handlers/cluster.Handler.validateCreateRequest` (Milestone 3's
existing pre-flight validation function, REQ-CREATE-060), querying a new
`Service.SupportsVersion(version string) bool` method rather than the
`Handler` importing/holding its own copy of the matrix directly. No new
`v1alpha1.ErrorType` enum value or OpenAPI schema change was needed:
`INVALIDARGUMENT` already exists and already means exactly this ("bad
input, rejected before ever reaching OSAC") per REQ-CREATE-060's existing
precedent for the four structural validation failures `validateCreateRequest`
already checks.

Separately, `internal/versionmatrix.Load` itself fails fast — rather than,
say, silently ignoring a malformed override file and falling back to
`DefaultMatrix` — on three conditions when `path != ""`: the file is
missing, the file is not valid JSON, or the file parses to zero entries.
The third condition (valid-but-empty) is deliberately treated as an error
rather than a legal (if useless) configuration: an operator-supplied
override file that resolves to zero supported versions would silently
brick every future Create call (every version request would be rejected)
with no diagnostic pointing at the actual cause — the same
fail-fast-over-silently-degrading philosophy REQ-XC-CFG-020 already applies
to missing required env vars is extended here to a malformed *optional*
one's referenced file, once that variable is actually set.

**Amendment (review finding on PR #26):** the original "decodes to zero
entries" check (`len(m)==0`) only catches a wholly empty `{}`; a file like
`{"1.29":""}` or `{"":"<image>"}` has exactly one key and passed the check
unchanged, silently loading a matrix with one *unusable* entry (an empty
version can never match a `Lookup` call; an empty `release_image` would be
wired straight into an OSAC `Create` call). `Load` now additionally rejects
any entry whose version key or `release_image` value is the empty string,
for the same brick-every-future-Create-call rationale above — a matrix
whose only entries are blank is functionally the zero-entries case with
extra steps.

**Rationale:** Mirrors the sibling `acm-cluster-service-provider`'s
established, already-reviewed pattern for the identical problem (translating
a DCM-facing Kubernetes version into a platform-specific release artifact) —
hard rejection over silent fallback, and a full-replace (not merge) JSON
override that itself fails fast on malformed input. Reusing
`validateCreateRequest`/`mapError`/`internal/grpcerror` rather than
inventing a new error path keeps this milestone's blast radius small: no
`api/v1alpha1/openapi.yaml` change, no `make generate-api` diff, and every
existing `TC-U-205`/`TC-U-206`/`AC-CREATE-040`/`AC-CREATE-050` test pattern
for "reject before calling OSAC" is directly reusable as a template for
this milestone's own `AC-VERSION-080`.

**Related requirements:** REQ-VERSION-040, REQ-VERSION-070, REQ-VERSION-080

---

## DD-132: Optional `SP_VERSION_MATRIX_PATH` env var, full-replace JSON override semantics

**Decision:** `internal/config.Config` gains one new optional field,
`SP_VERSION_MATRIX_PATH` (empty/unset is valid — means "use
`DefaultMatrix`"). When set, `versionmatrix.Load` reads it as a JSON object
and that object **entirely replaces** `DefaultMatrix` — it is not merged
key-by-key with the default table.

**Rationale:** Full-replace (not merge) was chosen because a merge
semantic's behavior on key collision is inherently ambiguous (does the
override file's `"1.29"` win, or does an operator have to repeat every
entry they *don't* want to change just to be sure?) and because a partial
merge silently keeps hardcoded defaults in effect that an operator
overriding the file most likely intended to fully take over — matching the
sibling SP precedent this milestone's plan explicitly adopted (DD-131).
Making the var optional (rather than required, or defaulting to a
committed-to-the-repo file path) avoids forcing every deployment to ship
and mount a JSON file just to reproduce the same 5 entries `DefaultMatrix`
already hardcodes — the override exists specifically for operators who need
to diverge from those 5 entries (e.g. a newer OSAC catalog template
becoming available before a new osac-sp release ships), not for normal
operation.

**Related requirements:** REQ-VERSION-040, REQ-VERSION-090

---

## DD-133: Stack this milestone's branch/PR directly on `feat/milestone-3-cluster-crud`, not on `main`

**Decision:** Unlike Milestone 5 (DD-075: a self-contained PR off `main`,
validated against M3/M4 only in a throwaway merge worktree, since M5 only
needed M3/M4's *types*), this milestone's branch was created directly from
`feat/milestone-3-cluster-crud`'s tip and its PR originally targeted that
branch, not `main`. It was retargeted to `main` (this merge commit) once
`#13` (the M3 PR) merged, exactly like this repo's own existing e2e PR
chain already does (`#24` on `#23` on `#20` on `#18`).

**Rationale:** This milestone edits `internal/cluster/translate.go` and
`internal/registration/registration.go` directly — both are Milestone 3
files (the latter dating to Milestone 1, but modified during M3's
development), not merely types M3 introduced. There was no self-contained
way to implement this milestone against `main` alone at the time: `main`
did not yet have Cluster CRUD at all, so "make this build standalone
against `main`" would have been a fabricated constraint, not a real one —
stacking directly said plainly that this milestone genuinely could not
exist independently of M3, the same way the e2e PR chain already stacks
for the same structural reason. Stacking also kept the PR diff small and
reviewable (just this milestone's actual changes against M3's tip) with no
throwaway-merge-worktree dance needed to validate it, unlike M5's
situation. This milestone's own `DD-110`/`DD-111` (this branch's original,
pre-merge numbering for what `main` already carries as `DD-113`/`DD-114`)
were dropped as duplicates during the merge that retargeted this branch to
`main` — same underlying decisions, same content, no independent
substance to preserve twice under two numbers.

**Related requirements:** none (process decision, not a REQ/AC).

---

# Milestone 5 (Status Reporting) — pre-resolved recommendations

**Status: proposed, not yet ratified.** Milestone 5 has not started — no
spec exists yet, and per this repo's own spec-first/test-plan-first gate, no
`REQ-*`/`AC-*` or `TC-*` exist for it either. The two entries below
(`DD-200`–`DD-201`) are numbered in a block well clear of Milestone 3's
(`DD-080`..`DD-111`, on `feat/milestone-3-cluster-crud`, unmerged as of this
writing) and Milestone 4's (`DD-080`..`DD-086`, on
`feat/milestone-4-vm-crud`, unmerged as of this writing) independently-
numbered, not-yet-merged decisions, specifically to avoid a numbering
collision when those two branches eventually land. **Renumber into the
normal sequence (and drop "proposed" framing) once M5's spec formally
starts** — these are not a substitute for that gate.

Two further research findings (dependency versions to pin, and a
contract-test gap for the CloudEvents `data` payload) came out of the same
research pass but are implementation guidance, not durable architectural
decisions — they live in `.ai/exploration/m5-status-reporting-research.md`
(local-only) instead, for whoever writes M5's actual spec to verify against
current reality at that time.

## DD-071: `DCM_NATS_URL` on `DCMConfig`; CloudEvents envelope via the SDK; `data` includes `id` for both Cluster and VM

**Decision:** name the NATS broker URL config field `DCM_NATS_URL` (a new
field on the existing `DCMConfig` struct), build the CloudEvents envelope
with `github.com/cloudevents/sdk-go/v2` rather than a hand-rolled struct, and
report `data` as `{"id": <resource id>, "status": <string>, "message":
<string>}` for **both** Cluster and VM — not the two-field `{status,
message}` the canonical spec's `VmStatus` type declaration literally shows.

**Rationale (config placement):** `DCMConfig` already uses the unprefixed
`DCM_` prefix specifically for backends that are DCM-wide, not
provider-specific — `DCM_REGISTRATION_URL` is the same URL every SP and
`control-plane` must agree on. The NATS broker is structurally identical
(one shared, DCM-operated instance), so it belongs on `DCMConfig` under the
same reasoning, not a provider-specific `SP_NATS_URL` (the two sibling SPs
that already publish status disagree with each other on this exact point —
`acm-cluster-service-provider` uses `SP_NATS_URL`, `kubevirt-service-provider`
uses bare `NATS_URL` — so nose-counting sibling precedent doesn't resolve it;
breaking the tie on `DCMConfig`'s own underlying placement principle does).

**Rationale (SDK, not hand-rolled):** `github.com/cloudevents/sdk-go/v2`
guarantees envelope-level spec compliance (correct `specversion`, attribute
serialization) for free, and `control-plane` (the consumer) already depends
on it for parsing — zero net-new dependency risk to the ecosystem. This only
protects the envelope shape, not the `data` payload shape, which is a
project-specific contract the SDK knows nothing about (see below and
DD-073's contract-test requirement).

**Rationale (`data` includes `id` for VM too — corrects a doc
inconsistency):** the canonical spec's §3 defines `type VmStatus struct {
Status string; Message string }` (no `id`) directly above a worked example
that constructs a *different*, self-contradictory `VmStatus{Id, "123-123",
Status: "Running", Message: "VM is running."}` literal (unparseable Go —
appears to be a copy/paste artifact from the `ContainerStatus`/
`StorageStatus`/`NetworkStatus` definitions immediately above it, all three
of which do declare `Id`). Without an `id` in `data`, `control-plane` has no
way to attribute a `dcm.vm` event to a specific instance — `subject`
identifies only the *service type* (`dcm.vm`), never a resource. Confirmed
directly against `control-plane`'s real, running consumer code
(`internal/sp/consumer/consumer.go`'s `StatusEvent{Id, Status, Message,
Timestamp}`), which requires `Id`. Per this repo's established precedent for
resolving doc/code conflicts in favor of real running code over a doc's own
internally-inconsistent prose (see DD-010's "Phase 1 confirmation" for the
same class of resolution), this SP always includes `id` in `data`, for both
service types.

**Confirmed by spike** (2026-08-05): a throwaway module built the exact
envelope this decision describes with `cloudevents-sdk-go v2.16.2`, published
it to a real embedded JetStream stream/consumer configured identically to
`control-plane`'s own (`consumer.go:90-121`), and round-tripped it through
`control-plane`'s real `StatusEvent` struct end-to-end. Wire bytes:
`{"specversion":"1.0","id":"evt-abc","source":"dcm/providers/osac-sp-vm","type":"dcm.status.vm","subject":"dcm.vm","datacontenttype":"application/json","time":"...","data":{"id":"vm-123","status":"RUNNING","message":"instance is running"}}`.

**Related requirements:** REQ-PUBLISH-010, REQ-PUBLISH-030

---

## DD-072: JetStream (`js.Publish`) over core NATS, wrapped in an indefinite-retry, coalescing background worker

**Decision:** publish status events via the JetStream API (`js.Publish`),
never plain core NATS (`nc.Publish`), from a single background worker
goroutine (`Publisher.Start(ctx)`) that retries indefinitely with
exponential backoff on failure and always delivers the *latest* known value
for a given resource — never a stale one superseded by a newer update still
queued behind a slow/failing retry.

**Rationale (JetStream over core):** `js.Publish` fails loudly (a retryable
error) if the target stream isn't ready yet; `nc.Publish` silently drops the
message with no error in that same case. Confirmed empirically (2026-08-05
spike): `js.Publish` against a stream-less embedded `nats-server` returned a
real error (`nats: no response from stream`) — not just inferred from
`control-plane`/`kubevirt-service-provider` source. This repo already has an
established, documented resilience convention for exactly this class of
"dependency not ready yet" condition (`CLAUDE.md`'s "Non-blocking bootstrap"
and "Independent registration loops": the OIDC token loop, gRPC dial, and
both registration loops all retry indefinitely with backoff rather than
silently dropping work) — `js.Publish` is the only one of the two transports
that can participate in that convention at all.

**Rationale (coalescing background worker, not a bounded per-call retry):**
an earlier design considered a synchronous `Publish` method with a small
bounded retry (matching `acm-cluster-service-provider`'s
`SP_NATS_PUBLISH_RETRY_MAX`/`_INTERVAL` knobs), reasoning that indefinite
*synchronous* retry would stall the poll loop's processing of every other
resource behind one failing publish. Resolved by decoupling: `Publish`
records the latest value for `(serviceType, resourceID)` in an in-memory map
and returns immediately (never blocks the poll loop); a single background
worker drains that map, retrying failed deliveries indefinitely. Because the
worker always re-reads the *current* map value for a key before each
attempt (not a value captured when first enqueued), a newer status arriving
while an older delivery for the same resource is still retrying always wins
— the worker can never deliver the older one after the newer one has been
recorded. This is a coalescing work-queue pattern (conceptually the same
technique `client-go`'s controller-runtime workqueue uses to deduplicate
reconcile keys), not an original invention for this project — chosen because
it satisfies "indefinite retry" and "never blocks the caller" and "never
reorders/regresses a resource's reported status" simultaneously, which a
simple bounded synchronous retry cannot.

**Related requirements:** REQ-PUBLISH-040, REQ-PUBLISH-050, REQ-PUBLISH-060,
REQ-PUBLISH-070, REQ-PUBLISH-080

---

## DD-073: Pin `nats.go`/`nats-server`/`cloudevents-sdk-go` to versions already used by `control-plane`

**Decision:** pin `github.com/nats-io/nats.go` (+ its `jetstream`
subpackage) to `v1.50.0`, `github.com/nats-io/nats-server/v2` to `v2.12.5`
(test-only — the contract test's embedded broker, DD-074's cross-reference),
and `github.com/cloudevents/sdk-go/v2` to `v2.16.2`.

**Rationale:** `v1.50.0`/`v2.16.2` match `control-plane` (the consumer) and
`acm-cluster-service-provider` exactly; `kubevirt-service-provider` is one
minor version behind (`nats.go v1.49.0`) with no reason to match the stale
one. `v2.12.5` matches `control-plane`'s own test-time `nats-server`
version. Confirmed by two independent spikes (2026-08-05): a throwaway
module using exactly these versions, and a second drop-in check directly
against this repo's real `go.mod` (`go get` the three pins, then
`go build ./...`, `go vet ./...`, and the full Ginkgo suite — 10 suites, 156
specs, all green, zero transitive-dependency conflicts with the existing
gRPC/protobuf/OIDC stack; the `go.mod`/`go.sum` change was reverted after,
since it was purely a compatibility probe run ahead of this spec).

**Related requirements:** REQ-PUBLISH-020

---

## DD-074: Periodic full resync mitigates `control-plane`'s dispatch-before-persist race (filed upstream, not fixed here)

**Decision:** in addition to publishing immediately on a detected diff
(REQ-POLL-040), the Poller unconditionally republishes every currently
observed resource's status every `ResyncEvery` poll cycles (default 10,
~5 minutes at the default 30s interval), regardless of whether the local
cache thinks it changed.

**Rationale:** tracing `control-plane`'s actual `UpdateStatus` SQL
(`internal/sp/store/resource_manager/service_instance.go:160-175`, a plain
`UPDATE ... WHERE id=?` checking `RowsAffected == 0` → `ErrInstanceNotFound`)
confirms repeated identical status updates are safe/idempotent — so a
resync costs nothing extra on the happy path. But the same trace surfaced a
real gap a pure diff-only design does not handle: `control-plane`'s
`CreateInstance`
(`internal/sp/service/resource_manager/service_type_instance.go:86-105`)
dispatches to the provider's REST endpoint **before** persisting its own
`ServiceTypeInstance` DB row. If this SP's Poller observes and publishes a
newly-created resource's status during that window, `control-plane`'s
consumer receives it, calls `UpdateStatus`, gets `ErrInstanceNotFound`, and
unconditionally `Ack()`s the message (dropped, no redelivery — confirmed no
`MaxDeliver`/backoff is configured on their consumer) — while this SP's own
local cache has already recorded that status as "delivered," so a pure
diff-based design would never naturally retry it, permanently losing that
update. This race is structurally present on every create, org-wide, not an
osac-sp-specific edge case (confirmed via `control-plane`'s own test suite,
where this exact path is untested beyond "doesn't panic":
`internal/sp/consumer/consumer_test.go:153`).

This is `control-plane`'s bug to fix, not something to fully absorb here —
filed as
[control-plane#44](https://github.com/dcm-project/control-plane/issues/44),
presenting two remediation options (reorder persist-before-dispatch, or a
bounded-retry `Nak` instead of unconditional `Ack` on `ErrInstanceNotFound`)
without prescribing which. The periodic resync mitigation ships regardless
of that issue's resolution, both because this SP cannot wait on their
fix/timeline and because it is generic defense-in-depth against any class of
transient consumer-side loss, not just this one race. It also subsumes the
original cold-start design (first poll = empty cache = every resource looks
new = already a de facto full resync) as cycle 0's natural case — no
separate cold-start code path is needed.

**Related requirements:** REQ-POLL-080

---

## DD-075: Deliver the publisher and poll loop as a single milestone/PR, not split across two phases

**Decision:** `internal/statuspublisher` and `internal/statuspoll` are
specified, implemented, and landed together in one PR, validated the same
way as [PR #24](https://github.com/dcm-project/osac-service-provider/pull/24)
(E2E CRUD coverage) — on a throwaway branch merging Milestone 3 + Milestone
4 + this milestone's code, then as a single small draft PR off `main`,
explicitly flagged blocked on Milestone 3/4 (#13/#14) merging first.

**Rationale:** an earlier draft of this plan split delivery into an
"unblocked" publisher-only phase (no import dependency on M3/M4) and a
"blocked" poll-loop phase, reasoning the publisher could land and be
reviewed independently. Reassessed and reversed for two reasons: (1) a
standalone publisher with no caller delivers no working capability — nothing
in this repo invokes `internal/statuspublisher` until the poll loop exists,
making a publisher-only PR a "why does this exist yet" review smell rather
than real progress; (2) it would introduce a *second*, different
unblocked/blocked delivery shape when PR #24 already established and
proved — via actual review — that the single-PR/draft/blocked-on-#13/#14
pattern works and is reviewer-legible for exactly this class of "depends on
an unmerged milestone" work. Introducing a new pattern here for no real
unblocking benefit (the actual outcome — status gets reported — is blocked
on M3/M4 either way) adds review overhead without upside.

**Related requirements:** none (process decision, not a functional one)

---

## DD-076: Review-found fixes — coalescing worker re-reads latest value on retry, per-`List` timeout, `len(items)`-based pagination, caller-supplied `Source`

**Decision:** four fixes made during review of [PR #25](https://github.com/dcm-project/osac-service-provider/pull/25), all in `internal/statuspoll`/`internal/statuspublisher`:

1. `Publisher`'s delivery worker (`deliverLatest`, formerly `deliver`) now
 re-reads the current pending value for its key from the map before
 *every* retry attempt, instead of retrying a value captured once when
 first popped. The entry is removed from the pending map only once
 delivered, and only if unchanged since (`removeIfUnchanged`) — never
 pre-emptively at pop time. This was a real correctness gap: the old
 `deliver` already violated REQ-PUBLISH-080/DD-072's own documented
 "worker always re-reads the current map value before each attempt"
 guarantee for exactly the case that matters most — an update superseding
 another one *while it is being retried* (as opposed to superseding one
 still in its very first, not-yet-failed attempt, which the pre-existing
 TC-U-413 did cover). TC-U-418 is the regression test; confirmed to fail
 against the pre-fix code (3 delivery attempts, stale value resent) and
 pass against the fix (2 attempts, latest value only).
2. `listClusters`/`listComputeInstances` (`internal/statuspoll/poller.go`)
 now advance pagination `offset` by `len(resp.GetItems())`, not
 `resp.GetSize()`, and terminate the page loop outright once a page
 returns zero items. The prior `Size`-based advancement could loop
 forever if a response ever reported `Size=0` while `Total>0` (a
 buggy/inconsistent server response) — trusting the peer's self-reported
 size field for loop-termination progress is less robust than trusting
 what was actually received.
3. Each individual `List` call is now bounded by a new
 `StatusConfig.ListTimeout` (`SP_STATUS_LIST_TIMEOUT`, default `10s`,
 REQ-POLL-025), applied per-page (not once for the whole paginated
 sequence, since a large listing needs one fresh deadline per page, not
 one shared budget). A timeout is treated identically to any other
 `List` error (REQ-POLL-090's existing "log and skip this service type"
 path) — no new error-handling branch needed. Without this, a hung OSAC
 backend could wedge the poll loop indefinitely, the same failure class
 DD-091 already fixed for the registration self-probe elsewhere in this
 codebase.
4. `statuspoll.New` now takes `clusterProviderName`/`vmProviderName`
 parameters (wired from `cfg.Provider.ClusterName`/`VMName` in
 `cmd/osac-service-provider/main.go`) and builds each `ServiceType.Source`
 from them, rather than two package-level `var`s hardcoding
 `"osac-sp-cluster"`/`"osac-sp-vm"` regardless of config. Those literals
 happened to match `ProviderConfig`'s own defaults, masking the gap until
 someone actually overrides `SP_PROVIDER_CLUSTER_NAME`/`SP_PROVIDER_VM_NAME`
 (already a supported, real config knob used by `internal/registration`) —
 at which point the registered provider identity and the reported
 CloudEvents `source` would silently diverge. REQ-PUBLISH-030 already
 specified `source` as "caller-supplied per service type"; this package is
 that caller, and REQ-POLL-015 makes the obligation explicit on this side
 too.

**Rationale:** none of these are new features — all four are the
implementation catching up to guarantees already promised either by this
milestone's own spec (REQ-PUBLISH-080, REQ-PUBLISH-030) or by an established
codebase-wide resilience convention (DD-091's "no unbounded wait on a
dependency"). Filed as one DD since all four were found in the same review
pass and share the same theme: a documented guarantee that the first
implementation didn't fully satisfy.

**Related requirements:** REQ-POLL-015, REQ-POLL-020, REQ-POLL-025,
REQ-PUBLISH-030, REQ-PUBLISH-080

---

## DD-077: `Publisher`'s single worker delivers in per-round sweeps, not one key retried to exhaustion, to prevent head-of-line blocking

**Decision:** `Publisher.run`'s single background worker (REQ-PUBLISH-060) no
longer picks one pending key and retries it with backoff until it succeeds
before ever looking at another key. Instead, each round
(`attemptRound`) makes exactly one delivery attempt for *every* pending key
that isn't currently cooling down from a prior failure, then sleeps only
until the next key becomes ready (or a fresh `Publish` wakes it early via
the existing `wake` channel). Per-key backoff state (`retryState`: current
backoff, next-eligible-attempt time) is tracked in a new `Publisher.retry`
map, populated on failure (`bumpRetry`) and cleared on success or a
malformed-envelope drop (`clearRetry`). `deliverLatest` (the old
retry-to-exhaustion loop) is replaced by `attemptOnce`, a single-shot
version of the same re-read-current-value-before-every-attempt logic
(DD-072/DD-076 item 1) — that guarantee is unchanged, just now evaluated
once per round instead of once per backoff sleep.

**Problem:** found while reassessing merged M5 PRs (#25) for latent issues
before opening further work — not from a bug report. With exactly one
worker (REQ-PUBLISH-060, a deliberate constraint so no two deliveries for
different keys could ever race) previously bound to the *old* exhaustive
retry-one-key loop, a single persistently-failing resource (e.g. a bad
provider ID, or a subject the broker permanently rejects) could occupy the
worker for its entire backoff sequence — indefinitely, per REQ-PUBLISH-070's
"retry indefinitely" requirement — starving every *other* resource's status
updates for as long as that one key kept failing. Two updates racing at the
JetStream/NATS layer was never the concern (JetStream's own per-subject
ordering already handles that); the concern was purely one Go-level worker
loop's scheduling starving unrelated work it had no reason to block on.

**Why per-round sweeps, not multiple workers:** REQ-PUBLISH-060 fixes
"exactly one worker goroutine" as a MUST, so running one worker per subject
(or per key) was not an option without a spec change. Round-robin
scheduling within the single worker satisfies the same requirement
unchanged while still eliminating the head-of-line block: a key's backoff
now only delays *that key's* next attempt, never the worker's ability to
service other keys in between.

**Regression test:** TC-U-419 publishes a permanently-failing key (`vm-1`)
followed by an unrelated always-succeeding key (`c-1` on a different
subject), and asserts `c-1` is delivered within a window far shorter than
`vm-1`'s configured initial backoff — confirmed to fail against the
pre-fix code (times out waiting for `c-1`, which the old loop could not
reach until `vm-1`'s retry loop happened to succeed or `vm-1`'s backoff
between attempts left a gap) and pass against the fix.

**Related requirements:** REQ-PUBLISH-060, REQ-PUBLISH-070, REQ-PUBLISH-080

---

## Validation evidence: M3+M4+M5 merged worktree (DD-075)

Per DD-075, the full stack was validated on a throwaway worktree merging all
three still-independent branches, at these SHAs:

- `feat/milestone-3-cluster-crud` @ `640caaa`
- `feat/milestone-4-vm-crud` @ `0afb49d`
- `feat/milestone-5-status-reporting` @ `1adc5ec`

merged (in that order) onto a scratch branch (`tmp/m5-validate-merge2`) in a
disposable `git worktree`, discarded after this evidence was captured — no
artifact of it is committed to any real branch. Conflicts were mechanical and
expected for two independently-developed OpenAPI branches sharing a base:
`openapi.yaml`/generated code needed a structural (not textual) merge,
`oapi-codegen`'s collision-avoidance then prefixes overlapping enum names
(e.g. `VMStatusDELETED`/`ClusterStatusDELETED` instead of bare `DELETED`)
requiring a handful of call-site updates in M3/M4's own pre-existing code,
and a few test fixtures needed the analogous stub method for the
sibling milestone's now-larger `StrictServerInterface`. None of this touches
M5's own logic.

Results on the merged tree:

- `go build ./...`, `go vet ./...`: clean
- `gofmt -l`: no files
- `golangci-lint run ./...`: 0 issues
- `make generate-api` against the merged `openapi.yaml`: byte-identical
  output to what was already merged (no generator drift)
- `ginkgo -r --race --cover`: 15 suites, 338 specs, all green; composite
  98.7%. The two suites below 100% are both pre-existing, in-code documented
  coverage exceptions, not artifacts of the merge: `Registration` (98.4%,
  predates M5) and `StatusPublisher` (88.4%, `buildEnvelope`'s
  `SetData`/`json.Marshal` branches and `NewPublisher`'s `jetstream.New`
  branch — see the M5 test plan's coverage notes).

Conclusion: M5's own branch is merge-clean against M3+M4 as of the above
SHAs. The M5 PR can be opened now per DD-075, with this note (and these
SHAs) linked as evidence, flagged blocked on #13/#14 for actual merge.

### Re-confirmation (2026-08-06): PR #25's own `ci/build`/`lint` failures are this same, expected DD-075 state, not a regression

`ci/build` ([run 31049778266](https://github.com/dcm-project/osac-service-provider/actions/runs/31049778266))
and `lint` ([run 31049776667](https://github.com/dcm-project/osac-service-provider/actions/runs/31049776667))
both fail on this PR's own branch, exactly as DD-075 predicted:
`internal/statuspoll/poller.go` directly imports `internal/cluster`/
`internal/vm` (M3/M4 packages), which simply don't exist on `main` — this
branch is deliberately *not* stacked on M3/M4 (DD-075's own rationale), so
these two checks cannot go green until `#13`/`#14` merge, full stop. This
is the identical failure mode already reasoned through above; it is not a
new bug and no code change to M5 fixes it.

Re-confirmed today, independently of the merge worktree above, by
re-checking out this branch's exact evidence commit
(`1169b82`, from the M6-adjacent throwaway validation branch
`scratch/e2e-m6-all-prs`, which additionally layered M6 — `#26` — on top of
this same M3+M4+M5 base): `make build`, `make vet`, and `make lint`
(`golangci-lint run ./...`) all pass with **0 issues**, and
`ginkgo -r --race` is green across all 19 non-e2e suites (only the `test/e2e`
suite itself fails locally, and only because `CONTROL_PLANE_URL` isn't set
outside a real `kind` run — not a code defect). This is the same
`ci/build`/`lint` job definition PR #25 itself runs, just executed against
the merged tree instead of the standalone branch — proving the failing
checks are 100% attributable to merge order, not to any defect introduced
by M5.

## DD-200: NATS broker URL env var — recommend `DCM_NATS_URL`, not `SP_NATS_URL`

**Superseded by DD-071**, which ratifies this exact recommendation against
what Milestone 5 actually built, rather than a pre-implementation proposal.
Kept here for historical record per this project's DD-numbering
discipline — do not re-litigate.

**Decision (proposed):** name the NATS broker URL config field
`DCM_NATS_URL` (a new field on the existing `DCMConfig` struct in
`internal/config/config.go`), not `SP_NATS_URL`.

**Rationale:** the two sibling SPs that already publish status disagree
with each other — `acm-cluster-service-provider` uses `SP_NATS_URL`,
`kubevirt-service-provider` uses bare `NATS_URL` — so this repo's own
established rule for `DCMConfig` ("match the sibling convention already
used for this backend," per `DCMConfig`'s doc comment and DD-050) doesn't
resolve cleanly by nose-count. Breaking the tie on that rule's underlying
*principle* instead: `DCMConfig` uses the unprefixed `DCM_` specifically
because `REGISTRATION_URL` names a DCM-wide, not-provider-specific backend
endpoint — every SP and `control-plane` must agree on the same URL. The
NATS broker is structurally identical (one shared, DCM-operated instance),
so it belongs on `DCMConfig` under the same reasoning, not on the
provider-specific `SP_` prefix.

**Related requirements:** none yet — M5 not started.

---

## DD-201: NATS publish transport — recommend JetStream (`js.Publish`), not core (`nc.Publish`)

**Superseded by DD-072**, which ratifies this exact recommendation against
what Milestone 5 actually built (the coalescing indefinite-retry background
worker), rather than a pre-implementation proposal. Kept here for
historical record per this project's DD-numbering discipline — do not
re-litigate.

**Decision (proposed):** publish status events via the JetStream API
(`js.Publish`), not plain core NATS (`nc.Publish`).

**Rationale:** `js.Publish` fails loudly (a retryable error) if the target
stream isn't ready yet; `nc.Publish` silently drops the message with no
error in that same case. This repo already has an established, documented
resilience convention for exactly this class of "dependency not ready yet"
condition (per the "Non-blocking bootstrap" and "Independent registration
loops" patterns in `CLAUDE.md`: the OIDC token loop, gRPC dial, and both
registration loops all retry indefinitely with backoff rather than
silently dropping work). Recommend `js.Publish` wrapped in the same kind of
indefinite-retry loop, matching `kubevirt-service-provider`'s transport
choice (not `acm-cluster-service-provider`'s) for consistency with this
repo's own convention, not that sibling's.

**Related requirements:** none yet — M5 not started.

---

## DD-205: Single `test/mockprovider` package, not one sub-package per service

**Decision:** `cmd/osac-mock-provider`'s five fake gRPC services
(`Capabilities`, `Clusters`, `ComputeInstances`, `Subnets`,
`VirtualNetworks`) and its OIDC discovery+token stub all live directly in
one flat package, `test/mockprovider` — one Go file per
service/concern (`clusters.go`, `computeinstances.go`, `subnets.go`,
`virtualnetworks.go`, `capabilities.go`, `oidc.go`, `store.go`,
`config.go`), not `test/mockprovider/clusters/`,
`test/mockprovider/oidc/`, etc.

**Rationale:** every file in this package shares one concern — faking
OSAC's backend surface for `osac-sp`'s own client code to dial — and all
five gRPC services share the exact same generic, unexported storage engine
(`resourceStore[T]`, see DD-206), which would otherwise need to be exported
(or duplicated) to cross sub-package boundaries for no benefit: nothing
outside this mock ever needs to depend on, say, `mockprovider/clusters`
without also needing the other four services and the OIDC stub to form a
working substitute. This mirrors the existing repo's own flat-package
convention for single-concern internal packages (e.g. `internal/osac`,
`internal/registration`) rather than the multi-file-but-nested-directory
shape of, say, `internal/api/server` (which is generated, not
hand-authored).

**Note on DD numbering:** this decision (and DD-206..208 below) were
originally numbered DD-130+ on a branch cut directly from `main` while
`main`'s own decisions file still ended at DD-070, on the (correct, at cut
time) assumption that M3/M4 (Cluster/VM CRUD, which had already claimed
DD-080..129) left DD-130+ clear. It didn't stay clear: M6
(version-translation matrix, DD-130..133 above) merged first and claimed
the same range independently, on its own branch, unaware of this one — a
genuine duplicate-numbering collision, caught and fixed here by renumbering
this block to DD-205..208. Kept as a cautionary note for the numbering
discipline this project otherwise relies on: "next available on a branch
cut from `main`" is only actually safe once merged, not at cut time, when
two branches claim the same range concurrently.

**Related requirements:** REQ-MOCK-010, REQ-MOCK-070, REQ-MOCK-080

---

## DD-206: Generic, mutex-protected in-memory `resourceStore[T]`, not bespoke per-service storage

**Decision:** All four CRUD-capable fake services (`Clusters`,
`ComputeInstances`, `Subnets`, `VirtualNetworks`) share one generic
`resourceStore[T]` type (`test/mockprovider/store.go`) — a
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

## DD-207: No real JWT signing for the OIDC token stub

**Decision:** `test/mockprovider.OIDCHandler`'s `/token` endpoint issues
a static, opaque bearer token string (not a real, cryptographically signed
JWT) for a valid `client_credentials` grant, and never validates the
`client_id`/`client_secret` credentials presented against anything.

**Rationale:** the mock's own gRPC server (the thing that token is actually
*for*) doesn't enforce auth either — `test/mockprovider`'s five gRPC
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

## DD-208: Flat `MOCK_`-prefixed env vars for the mock's own config, not a nested `internal/config`-shaped struct

**Decision:** `test/mockprovider.Config` is a flat, two-field struct
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

## DD-134: `List` pagination advances by `len(results)` actually received, not the server-reported `Size` field

**Decision:** `internal/cluster.List` and `internal/vm.List` compute the next
`page_token`'s offset from `len(results)` (the number of items this SP
actually received and mapped), not from OSAC's `resp.GetSize()` field. An
empty page (`len(results) == 0`) never emits a `next_page_token`, regardless
of what `resp.GetTotal()` reports.

**Rationale:** found during review of [PR #25](https://github.com/dcm-project/osac-service-provider/pull/25) (`internal/statuspoll`, which copied this exact pagination shape from these two functions' own doc comments — "mirroring `internal/cluster/list.go`'s own offset/limit pagination math exactly"). The original `nextOffset := offset + resp.GetSize()` trusts OSAC's self-reported `Size` field for computing forward progress; if `Size` and `Total` are ever inconsistent (e.g. `Size=0` while `Total>offset` — a malformed/buggy upstream response), `nextOffset` never advances past the current `offset`, so `List` would keep reissuing the *exact same* `page_token` it just consumed. Unlike `internal/statuspoll`'s own internal pagination loop (which could spin forever inside this SP's process), this doesn't hang *this* SP — each `List` call is one bounded HTTP request — but it pushes the same failure onto whichever caller (`control-plane`) faithfully follows `next_page_token`: that caller would loop forever refetching an empty page it already has. `len(results)` is what this SP actually received and can prove happened; it cannot be inconsistent with itself the way a separate self-reported counter can be with `Total`.

**Related requirements:** REQ-LIST-040, REQ-VMLIST-040

---

## DD-204: `osac-sp`/`osac-mock-provider` as this repo's own plain manifests, not a `control-plane` chart contribution

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
chart was rejected for the same reason as DD-204 (this repo consumes
`control-plane` as a published artifact, not a fork); blocking this PR on
`control-plane#42` landing and releasing was rejected as directly
contradicting "ready to run asap." The patch touches only the one field
actually causing the failure, leaving the chart's other hardening
(`allowPrivilegeEscalation`, `capabilities.drop`, `seccompProfile`) intact.
**This step should be deleted once control-plane#42 is fixed and released**
— it is not a permanent feature of this workflow.

**Related requirements:** REQ-E2E-020

## DD-139: `osac-mock-provider`'s OIDC discovery documents derive `token_endpoint` from the request's `Host` header, not the listener's bind address

**Decision:** `test/mockprovider.OIDCHandler`'s discovery-document
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

## DD-149: Tier B vendors specific OSAC config/artifacts rather than importing `fulfillment-service`'s `it` Go package

**Decision:** `.ai/specs/osac-sp-e2e-tier-b.spec.md` ("Tier B") deploys real
Postgres, real Keycloak (official image + a vendored, static realm-config
file copied into this repo), and real `fulfillment-service`/`osac-operator`/
BMFO (pinned, published `vX.Y.Z` image and OCI chart tags) — not by adding a
live Go module dependency on `osac-project/fulfillment-service`'s own `it`
integration-test package (which [#19](https://github.com/dcm-project/osac-service-provider/pull/19)'s
spike proved is technically importable), and not by building any OSAC
component from source.

**Rationale:** requested directly — depending on another team's internal
test-harness *code* (as opposed to their published, versioned artifacts)
means this repo's own CI can break for reasons entirely outside its
control, on a schedule it doesn't own, with no version pin protecting it
(the `it` package has no stability contract; it's `fulfillment-service`'s
own test-only tooling, not a public API). Concretely, this repo would
inherit: `it.Tool`'s exact function signatures, its own Kind/Helm/podman
orchestration assumptions, and its full transitive dependency graph
(confirmed non-trivial in the spike — `osac-operator`/BMFO CRD types, etc.),
none of which this repo has any influence over.

The chosen alternative — vendor the *minimum static config* actually
needed (Keycloak's realm/client/claim-mapper definitions, which are what
determine JWT-claim-shape fidelity) and reference *published, versioned*
images/charts for everything else — mirrors this repo's own existing
precedent for depending on other DCM/OSAC components: `control-plane` is
already consumed as a pulled image + sparse-checked-out chart at a pinned
ref in Phase A (`osac-sp-e2e-suite.spec.md` §2), never built from source or
imported as a Go dependency; DD-020 vendors (copies) OSAC's protos rather
than living off an unpublished BSR module for the identical reason. Tier B
extends that same posture to Keycloak/`fulfillment-service`/`osac-operator`/
BMFO instead of introducing a new, inconsistent pattern.

Confirmed as a real, live risk while researching this decision (not
theoretical): `osac-project/fulfillment-service` (along with `osac-operator`,
BMFO, and `osac-aap`) was archived and merged into a new monorepo,
`osac-project/osac`. This entry originally said that move happened on
2026-08-04, the day before this decision was written — it hadn't actually
happened yet at that point; the real archival date, re-verified directly
against each repo's `pushed_at` via the GitHub API on 2026-08-26, is
**2026-08-15**. Corrected here rather than left standing, per this repo's
own convention for decision-log entries whose stated facts didn't hold up
(see [PR #41](https://github.com/dcm-project/osac-service-provider/pull/41)'s
DD numbering fix for the same treatment applied to a different kind of
miss). The conclusion this decision reached is unaffected
either way: a live Go dependency on the old repo's `it` package would still
need remediation once the repo moved/archived; a pinned image/chart tag and
a vendored static file remain unaffected by the repo move, whenever it
actually happened. Also confirmed 2026-08-26: the monorepo's CI publishes
new releases to the *same* registry coordinates the archived repos used, so
"pinned image/chart tag" for Tier B Phase 2 (issue #44) means pinning
against the monorepo's own releases going forward, not whatever the
archived repos last published before the freeze.

**Related requirements:** REQ-TB-010, REQ-TB-050

---

## DD-150: Vendored realm built from `INSTALL.md`'s authoritative `KeycloakRealmImport`, not the `it` package's test-fixture realm — corrects REQ-TB-020

**Decision:** `test/e2e/tierb-config/realm.json` is a minimal Keycloak
realm-export JSON assembled directly from
`osac-project/osac`'s `fulfillment-service/docs/INSTALL.md`'s
`KeycloakRealmImport` example (the `spec.realm` field there is a
`RealmRepresentation` — the same schema a plain `--import-realm` file uses,
confirmed by inspecting the CR), not derived from
`fulfillment-service/it/charts/keycloak/files/realm.json` as DD-149/REQ-TB-020
originally assumed.

**Correction to REQ-TB-020:** verified directly against the real
`it/charts/keycloak/files/realm.json` (2209 lines, fetched from `main`) that
it contains **no** `client_credentials` service-account client at all — only
the interactive `osac-cli` public client. `osac-admin`/`osac-controller` are
real client IDs (confirmed via `it_tool.go`'s `adminClientId`/
`controllerClientId` constants and independently via `INSTALL.md`'s own
`osac login --client-id osac-admin` example), but the `it` package creates
them **programmatically** against Keycloak's admin API at test-setup time —
they are not in the static file, so vendoring that file alone (REQ-TB-020's
original plan) would not have produced a usable client-credentials login.

**Further correction — the actual claims OSAC checks:** REQ-TB-020 guessed
`organization`/`groups`/`realm_access.roles` (pattern-matched from
generic Keycloak+OPA blog content during spec-writing, not confirmed against
OSAC's own docs). `INSTALL.md` §"Configure the Keycloak realm" states
plainly: *"The realm must also be configured so that access tokens include
the `username` and `groups` claims"*, via three custom `clientScopes`
(`osac-api` — an `oidc-audience-mapper` audience claim, not a role claim;
`username`; `groups`) assigned as `defaultDefaultClientScopes`. No
`organization` or `realm_access.roles` claim is mentioned anywhere in the
production install doc. REQ-TB-020 is corrected accordingly.

**Vendored realm contents** (`test/e2e/tierb-config/realm.json`), copied
near-verbatim from `INSTALL.md`'s CR example with test-only static secrets
substituted for the doc's `openssl rand` placeholders (NFR-TB-020):

- `clientScopes`: `osac-api` (audience mapper, `included.custom.audience:
  osac-api`), `username`, `groups` — assigned via `defaultDefaultClientScopes`
- `clients`: `osac-cli` (public, unused by this suite but included for
  parity with the real realm shape), `osac-admin` (confidential,
  `serviceAccountsEnabled`, the client `osac-sp` itself authenticates as —
  same one `INSTALL.md`'s own `osac login` example uses), `osac-controller`
  (confidential, `realm-management` roles — required by
  `fulfillment-service`'s own chart's `auth.controllerCredentials`/`idp.credentials`,
  not by `osac-sp`)
- `users`: `service-account-osac-admin`, `service-account-osac-controller`

**Addendum (confirmed via live `kind` spike, same day):** a real
`client_credentials` grant against the deployed realm returns `aud:
"osac-api"` and `username: "service-account-osac-admin"` exactly as
expected, but **no `groups` claim at all** — Keycloak's
`oidc-group-membership-mapper` omits the claim entirely (not an empty
array) when the subject has zero group memberships, and this vendored
realm's `users` entries never assign `service-account-osac-admin`/
`service-account-osac-controller` to any group, matching `INSTALL.md`'s own
reference realm (which does the same). `INSTALL.md`'s own verification
steps only check the `username` claim for exactly this reason. TC-TB-020
was corrected to not assert `groups` presence; REQ-TB-020's "carry...a
`groups` claim" wording describes the *mapper being configured* (which it
is, and would emit the claim for any principal that actually has group
memberships), not a claim guaranteed present on every token this specific
realm can issue.

**Related requirements:** REQ-TB-020 (corrected wording), REQ-TB-030

---

## DD-151: `fulfillment-service` is installed via its real, published OCI chart (`variant: kind`), not a hand-written manifest — and requires cert-manager

**Decision:** `ffs-fulfillment-service` is installed with
`helm install ... oci://ghcr.io/osac-project/charts/fulfillment-service --version vX.Y.Z`
(pinned, REQ-TB-050), using the chart's built-in `variant: kind` mode — not a
hand-written plain `Deployment`/`Service` manifest as originally implied by
this spec's Phase 1 architecture diagram's `ffs-fulfillment-service (NEW —
pinned image/chart from ghcr.io/osac-project/*, replaces osac-mock-provider)`
line, which undersold how much the chart itself already handles.

**New, previously-undocumented prerequisite:** the chart's own README lists
_cert-manager_ as a hard prerequisite regardless of `variant`
(`certs.issuerRef`/`certs.caBundle.configMap` have no default — "Required" in
the chart's own parameter table). This was not called out anywhere in
`osac-sp-e2e-tier-b.spec.md`'s original architecture (§2) — a genuine spec
gap, not a Phase-1-vs-Phase-2 scoping choice. NFR-TB-010's resource budget
must additionally account for cert-manager's 3 pods (controller, webhook,
cainjector), still comfortably within the 16 GB/4 vCPU free-tier budget.

**Rationale for the chart over a hand-written manifest:** matches this
spec's own §2 vendoring-plan table (`fulfillment-service` row: "Pin real,
versioned images... and their published OCI Helm charts directly"), and the
`control-plane` precedent already established in Phase A
(`osac-sp-e2e-suite.spec.md` §2) — pulled chart, not hand-authored manifest,
for anything with its own official chart.

**Resolved via live `kind` spike (this same day):** `fulfillment-grpc-server`
hard-rejects a plain-HTTP issuer at JWKS-cache construction time —
`failed to create JWKS cache: issuer URL '...' must use the HTTPS scheme` —
so `ffs-keycloak` was switched to also terminate TLS, using a `cert-manager`
`Certificate` issued by the same self-signed `osac-ca` `ClusterIssuer` the
chart's own components trust via `certs.caBundle`. `auth.issuerUrl`/`idp.url`
now both point at `https://ffs-keycloak:8443/realms/osac`.

**Second-order finding, same spike:** enabling `KC_HTTPS_CERTIFICATE_FILE`/
`KC_HTTPS_CERTIFICATE_KEY_FILE` on Keycloak 26.3 also switches its
_management interface_ (port 9000 — `/health/ready`, `/health/live`) from
HTTP to HTTPS, undocumented in the parameter's own description. The
`readinessProbe`/`livenessProbe` in `test/e2e/manifests-tierb/keycloak.yaml`
must set `scheme: HTTPS`, or the pod flips permanently `Unready` with
"connection refused" once TLS is turned on — this is not optional plumbing,
it's a hard coupling in Keycloak's own Quarkus runtime between "any HTTPS
cert configured" and "management interface moves to HTTPS too."

**Third finding, from the `e2e-tierb.yaml` workflow's own first CI runs**
(not reproducible in the earlier interactive spike, which happened to
have leftover Gateway API CRDs registered on that cluster from prior
troubleshooting): `helm install` fails outright in a clean cluster with
`no matches for kind "TLSRoute" in version "gateway.networking.k8s.io/v1alpha3"`,
so installation was switched to
`helm template | yq filter (drop TLSRoute) | kubectl apply`. That filter
must also drop null documents (`select(. != null)`) — some of the chart's
templates emit a bare `---` separator with no body for a
conditionally-skipped block, which `kubectl apply` otherwise rejects
outright with `apiVersion not set, kind not set` (reproduced in
isolation to confirm root cause).

**Actual root cause of the persistent CI failure** (took 5 CI iterations
to isolate, including three disproven theories along the way — yq
version, kubectl 1.35-vs-1.36 client-side validation strictness, and
"stderr redirection alone fixes it" — each ruled out or falsified by a
subsequent CI run): `helm template`'s OCI pull status (`Pulled: ...`,
`Digest: ...`) lands on **stdout or stderr depending on the installed
Helm build** — this repo's local Helm v4.1.1 writes it to stderr (why
every interactive local repro looked clean), but `azure/setup-helm@v4`'s
unpinned `version: latest` in CI resolves to a build that writes it to
stdout, where it prepends a bogus first YAML document with no
`apiVersion`/`kind` that `kubectl apply` rejects outright. A stdout/stderr
redirect fix (the natural first attempt) is therefore *not* portable
across Helm versions/environments. Fixed by stripping both lines by
content (`grep -Ev '^(Pulled|Digest): '`) after merging stdout+stderr,
which is invariant to which stream a given Helm build chooses.

**Process lesson for this repo:** a fix validated only by reasoning
about "which stream should X go to" and confirming via local
reproduction, without accounting for tool-version drift between the
local dev machine and CI's freshly-resolved `latest` action inputs, can
look conclusively correct locally and still fail in CI for an unrelated
reason. Prefer content-based filtering over stream-based redirection
when post-processing third-party CLI output whose stream discipline
isn't part of its documented/stable contract.

**Related requirements:** REQ-TB-010, REQ-TB-050

---

## DD-152: `osac-aap-mock` (Phase 2) is a new, hand-written fake — no reusable upstream AAP-layer test double exists

**Decision:** Tier B's Phase 2 (`.ai/specs/osac-sp-e2e-tier-b.spec.md` §3)
will introduce a new binary, `test/cmd/osac-aap-mock/` (see DD-224 for why
it lives under `test/` rather than the repo-root `cmd/`), implementing
enough of
AAP's REST surface (`GetTemplate`, `LaunchJobTemplate`/
`LaunchWorkflowTemplate`, `GetJob`, `CancelJob`) for real `osac-operator`/
BMFO reconciliation to reach a terminal state — built from scratch, the
same way `osac-mock-provider` was for OSAC's own gRPC/OIDC surface
(DD-130–133), not adapted from any existing OSAC-provided fake.

**Rationale:** confirmed by direct source investigation across
`osac-operator`, BMFO, and the wider `osac-project` org that no reusable
provisioning-layer test double exists anywhere upstream:

- The `ProvisioningProvider`/`AAPClient` interfaces
  (`osac-operator/pkg/provisioning`) are genuinely public and cross-repo
  (BMFO imports them as a real Go module dependency) — so the *interface
  contract* `osac-aap-mock` must satisfy is stable and well-defined.
- Every concrete fake implementing those interfaces
  (`noopProvisioningProvider`, `mockProvisioningProvider`,
  `mockAAPClient`, etc.) is unexported and defined inline in `_test.go`
  files, scattered and duplicated per controller test — Go tooling
  excludes `_test.go` files from normal builds, so none of these are
  importable by any external module regardless of intent.
- No runtime dry-run/test-mode config flag exists in either operator's
  production code — `osac-operator/cmd/main.go`'s
  `createAAPProviderFromEnv` unconditionally constructs a real AAP client
  from env vars pointing at a URL.
- No dedicated AAP/Ansible-mock repo or component exists anywhere in the
  `osac-project` org; the only place real AAP is ever exercised is the
  separate, heavyweight `osac-test-infra` full-stack pipelines (real
  KVM/libvirt), which is precisely the "genuinely impossible in CI" tier
  Tier B's Phase 2 is designed to never require.

Because `createAAPProviderFromEnv` wires the AAP client from a URL at
runtime (not compiled in), real `osac-operator`/BMFO can run completely
unmodified against `osac-aap-mock` — no upstream code changes needed, only
a config value pointing at our own component instead of a real AAP
instance.

**Related requirements:** REQ-TB-080

---

## DD-153: `e2e-tierb.yaml`/`manifests-tierb` needed the same two Phase-A CI fixes (DD-144, DD-147) independently applied

**Decision:** after retargeting this PR from the now-squash-merged
`e2e/kind-control-plane-infra` (#29) directly to `main`, `e2e-tierb`'s CI
run was still red on its pre-existing head commit, for the exact two
reasons DD-144 and DD-147 already fixed in the sibling `e2e.yaml` (Phase
A) — but never ported to this PR's own Tier B-specific files, since
`manifests-tierb/osac-service-provider.yaml` and `e2e-tierb.yaml` are
separate files from Phase A's, unaffected by a `main`-merge alone:

1. Both `osac-service-provider` Deployments in
   `manifests-tierb/osac-service-provider.yaml` now set
   `DCM_NATS_URL=nats://dcm-nats:4222` (DD-144's exact fix, reused
   verbatim) — confirmed via the failing run's captured pod log
   (`"env: environment variable \"DCM_NATS_URL\" should not be empty"`),
   same M5 fail-fast gap, same missing manifest field.
2. `e2e-tierb.yaml`'s `CONTROL_PLANE_REF`/`CONTROL_PLANE_IMAGE_TAG` are now
   pinned to DD-147's exact commit/tag (`c04802d0`/`c04802d`), since this
   workflow installs `control-plane`'s chart independently of `e2e.yaml`
   and was still floating on `main` (i.e. still exposed to control-plane#51).

**Rationale:** these are config-only ports of already-validated fixes, not
new decisions — recorded here (rather than amending DD-144/147 in place)
so the CI history for *why this PR's checks went from red to green* stays
traceable to this branch's own timeline.

**Related requirements:** REQ-TB-030, REQ-PUBLISH-010 (M5)

---

## DD-143: `osac-mock-provider`'s `Clusters/GetKubeconfig` is implemented, correcting Phase 1's original out-of-scope call

**Decision:** `test/mockprovider/clusters.go`'s `ClustersServer` now
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

---

## DD-144: `test/e2e/manifests/osac-service-provider.yaml` gains `DCM_NATS_URL`, required now that M5 has merged

**Decision:** the e2e Deployment manifest now sets
`DCM_NATS_URL=nats://dcm-nats:4222`, pointing at the `dcm-nats` StatefulSet
this workflow's own `helm install dcm control-plane/deploy/helm/dcm` step
already deploys and waits on (`.github/workflows/e2e.yaml`'s
`kubectl rollout status statefulset/dcm-nats`).

**Rationale:** Milestone 5 (REQ-PUBLISH-010, now merged to `main` via #25)
made `DCM_NATS_URL` a required, fail-fast `DCMConfig` field —
`internal/config.Load()` errors out before any subsystem starts if it's
empty. With `main` now merged into this branch (bringing M5/M6 in), the
mock e2e's `osac-service-provider` Deployment would crash-loop on startup
without this, since no manifest here previously set it (this pod had
nothing to gate on before M5 existed). Caught by this branch's own `e2e`
CI job going red (`kubectl wait --for=condition=Available` timing out on
both `osac-service-provider`/`osac-mock-provider` Deployments) immediately
after the M5/M6 merge — not from a design review. The exact value was
already known and pre-validated on a separate, ahead-of-time branch (see
`e2e/crud-coverage`'s own `DD-146`, which added this same fix proactively
before M5 had even landed, anticipating exactly this gap) — reused
verbatim here since this branch is the one that actually now needs it.

**Related requirements:** REQ-PUBLISH-010 (M5)

---

## DD-147: `control-plane`#51 forces Phase 2 (`environment-agent`) migration — deferred until current PR stack lands

**Decision:** `control-plane`'s `main` deleted `api/sp/v1alpha1/provider`
([control-plane#51](https://github.com/dcm-project/control-plane/pull/51),
2026-08-19), permanently invalidating DD-050's Phase 1 target.
`environment-agent` has matured past the stub state DD-050 cited, so
Phase 2 is now mandatory, not optional — but is deliberately deferred
until the in-flight PR stack (#29, #22, #24, #27, #32) lands, to keep it a
clean refactor rather than mid-flight scope creep. `.github/workflows/e2e.yaml`'s
`CONTROL_PLANE_REF` is pinned to the last commit before #51 as a stopgap
in the meantime. Pinning the chart ref alone was not sufficient — the
chart's `values.yaml` hardcodes `global.imageTag: main` as its own default
regardless of which chart commit is checked out, so Helm kept pulling the
floating (already-broken) `:main` image. `CONTROL_PLANE_IMAGE_TAG` pins the
actual deployed image to the matching short-SHA tag
(`shared-workflows`' `build-push-quay.yaml` publishes both `main` and
`${GITHUB_SHA:0:7}` on every push), verified present on quay.io before use.
Full RCA, maturity evidence, and known implementation
deltas are tracked in
[#33](https://github.com/dcm-project/osac-service-provider/issues/33);
DD-050 will be formally superseded once that work actually starts.

**Related requirements:** REQ-REG-010, REQ-REG-090, REQ-REG-100 (all to be
revised when Phase 2 starts — see #33)

---

## DD-202: Milestone 7 (E2E suite) stays in `osac-service-provider`, not `dcm-project/utilities`, for now

**Decision:** Issue #1's original Milestone 7 plan pointed at
`dcm-project/utilities` (mirroring `kubevirt-service-provider`'s e2e
pattern). In practice, the kind-based e2e suite (#18, #20/#29, and the
Tier B stack #22/#27/#24) was built directly in this repo instead. Keep it
here for now; relocate to `dcm-project/utilities` once the Phase 2
`environment-agent` migration (#33) lands and the suite has stabilized.

**Rationale:** `control-plane` now ships a versioned image and Helm chart
that a self-contained in-repo `kind` job can pull directly (see the
Milestone 7 e2e suite's own e2e.yaml), so an in-repo job doesn't need
`utilities`' shared harness to stand up a realistic environment — the
original rationale for putting it there doesn't hold as strongly as it did
for the sibling SPs' earlier e2e work. Deferring the move also avoids
adding a cross-repo relocation to the already in-flight PR stack; issue
#1's Milestone 7 text is stale relative to this and will be updated to
match.

**Related:** #17, #21, #33

---

## DD-148: `.golangci.yml` hardened with 10 additional linters, evidence-tested before adoption

**Decision:** added `nestif`, `errorlint`, `forcetypeassert`, `predeclared`,
`perfsprint`, `intrange`, `gocyclo` (`min-complexity: 15`), `funlen`
(`lines: 80`, `statements: 50`), `goconst` (`min-occurrences: 3`), and
`exhaustive` (`default-signifies-exhaustive: true`) to the linter set this
repo shares verbatim with every sibling DCM Go service provider
(`control-plane`, `environment-agent`, `k8s-network-service-provider`,
`k8s-storage-service-provider` all carry the identical file). Explicitly
**not** added: `dupl`, `noctx` (see Rationale).

**Rationale:** triggered by two real review nits on [PR #25](https://github.com/dcm-project/osac-service-provider/pull/25) that manual review caught but no linter would have (`internal/statuspoll`: two guard-`if`s that should have been one condition; a repeated string literal that should have been a constant) — the concrete goal was closing exactly that gap, plus general AI-generated-code hardening, without adding noise.

Each candidate was run against this repo's actual code before being adopted, not chosen from a generic "best practices" list:
- `nestif`/`errorlint`/`forcetypeassert`/`predeclared`/`perfsprint`/`intrange`/`gocyclo`/`funlen`: **zero** current hits — pure forward-looking regression guards, free to adopt.
- `exhaustive`: naively fires on 5 switches over `codes.Code`/`ClusterState`, but 4 already have a deliberate `default:` catch-all (the documented `internal/grpcerror.Classify` pattern, e.g.). Setting `default-signifies-exhaustive: true` drops this to exactly 1 real hit — `internal/cluster/status.go`'s intentional partial-match-then-fallthrough switch (checks terminal states first, falls through to a `DEGRADED` condition check, falls through again to a final exhaustive switch) — annotated with a justified `//nolint:exhaustive` rather than restructured, since the fallthrough is the intended design. This is the single most valuable addition: a safety net against silently mishandling a future new OSAC proto enum value.
- `goconst` (`min-occurrences: 3`): 4 hits, all pre-existing test-only literals (`internal/osac/bootstrap_unit_test.go`'s repeated CA-file path, `internal/registration/registration_unit_test.go`'s repeated content-type/service-type strings) — extracted to fixture-local constants. This is the exact linter that would have auto-caught the PR #25 nit.
- `dupl`: 25 hits, but every one is the deliberate Cluster/VM/Subnet/VirtualNetwork structural mirroring already established as an intentional non-generic design elsewhere in this codebase (`test/mockprovider`'s per-resource-type servers, `internal/cluster`/`internal/vm`'s parallel service packages). Enabling it would force either an unwanted generics refactor or ~12 blanket `//nolint`s — noise, not signal. Left off.
- `noctx`: 6 hits, all `net.Listen`/`net.DialTimeout` at process-startup or test-probe call sites with no real cancellation need. Stylistic modernization, not a correctness or AI-slop risk. Left off.

Deliberately scoped to this repo first, not simultaneously rolled out to sibling SPs — this repo's `.golangci.yml` already functions as the de facto shared template (byte-identical across 4 other repos), so validating the change here first, then propagating, is lower-risk than a coordinated multi-repo change.

**Related requirements:** none (tooling/process decision, no REQ/AC).

---

## DD-203: Registration target flips back to `environment-agent` (DD-050 superseded) — forced by `control-plane#51`, drafted before either project's PR stack has landed

**Decision:** `internal/registration.Registrar` now targets
[`dcm-project/environment-agent`](https://github.com/dcm-project/environment-agent)'s
`POST /api/v1alpha1/providers` (`api/v1alpha1/openapi.yaml`), using its
generated `github.com/dcm-project/environment-agent/pkg/client`, pinned by
commit SHA
[`8e638b2`](https://github.com/dcm-project/environment-agent/commit/8e638b289d670b85c323f90442e916503fa0f54d)
(no tagged releases exist for `environment-agent` either — same treatment
as `control-plane` under DD-050). This supersedes DD-050's Phase 1 target
choice; DD-040's rationale about independent per-service-type retry loops
is otherwise unaffected and still holds.

**Rationale — why now, and why this isn't optional:** tracked in
[#33](https://github.com/dcm-project/osac-service-provider/issues/33).
`control-plane`'s `main` merged
[`control-plane#51`](https://github.com/dcm-project/control-plane/pull/51)
on 2026-08-19, deleting `api/sp/v1alpha1/provider` (the API DD-050's Phase 1
target depended on) in favor of an agent-routed dispatch model
(`api/agent/v1alpha1`) — a deliberate, reviewed architectural move (per
`control-plane#51`'s own reviewer), not a bug to wait out. DD-050's Phase 2
trigger — "`environment-agent` maturity" — is also now satisfied: as of
`environment-agent@8e638b2`, `internal/handler/handler.go` has a real, wired
`CreateProvider`, `internal/provider/service` has a real service+store, and
`cmd/environment-agent/main.go` is real wiring, not the "stub-only, no-op
`main()`" state DD-050 was written against.

**Explicit gate this decision does not wait on:** issue #33 itself states
"do not start until #29/#22/#24/#27/#32 land" (the current Milestone 7 e2e
PR stack). This branch/PR is drafted anyway, ahead of that gate, at the
user's explicit request to keep moving on Phase 2 groundwork in parallel —
it targets `main` directly (not the e2e stack's branches) precisely so it
doesn't entangle with or block that stack. **Do not merge this to `main`
before the e2e stack lands**, per issue #33's own reasoning: landing this
first would mean the e2e stack's `test/e2e/registration_test.go` (which
exercises the real `internal/registration` package against `control-plane`,
pinned per DD-147/DD-148) breaks or needs its own parallel rework, which is
exactly the entanglement issue #33 was trying to avoid by sequencing these.

**No environment-agent image needed for this work:** `environment-agent` has
no Helm chart and no tagged release/published image (`build-push-quay.yaml`
only publishes on `v*` tags), so a `kind`-based e2e run against a real
`environment-agent` deployment is **not** part of this change — same as
`control-plane`'s own Go client dependency, `environment-agent/pkg/client`
is importable straight from its `main` branch source by commit SHA, with no
image required. Unit and integration tests here use hand-written fakes
(`http.RoundTripper` / `httptest.Server`), per this repo's established
testing convention — identical in kind to how DD-050's `control-plane`
integration was itself tested before any real `control-plane` e2e existed.
Deploying a real `environment-agent` in `kind` remains a separate, larger
follow-up (its own item under #33), not bundled here.

**`409` semantics flip back to retryable (REQ-REG-080, new):**
`environment-agent`'s `POST /providers` enforces "only one SP — embedded or
external — may serve a given service type per agent" (409 on conflict),
restoring the per-service-type exclusivity DD-050 said didn't apply under
`control-plane`. REQ-REG-090's "409 is non-retryable" and its superseding
tests (`TC-I-023`, `TC-U-053`) — both of which explicitly documented
themselves as superseding "the pre-pivot 409-is-retryable design (see
DD-050)" — flip back to that pre-pivot design: log at WARN, retry on the
re-registration cadence, do not escalate backoff, do not stop the loop.

**Deliberate divergence from the pre-pivot design:** the original
pre-Phase-1 code (commit `3c49de7`) only applied 409-retry treatment to the
`vm` registration loop, leaving `cluster`'s 409 non-retryable. Re-examining
`environment-agent@8e638b2`'s `internal/provider/service.RegisterEmbedded`,
per-service-type exclusivity (including embedded-provider collisions) is
generic across whichever `service_type`s an agent deployment enables — there
is no `environment-agent`-side reason `cluster` couldn't hit the same 409
scenario `vm` could. This decision generalizes 409-retry handling to both
service types symmetrically (`runLoop` no longer takes a
`treat409AsLeaseCadence` flag), rather than reproducing the original
asymmetry without justification. `TC-U-053`/`TC-I-023` still only exercise
the `vm` case (matching the original test IDs/scope), since `cluster`'s
symmetric handling is exercised by the same shared `runLoop` code path.

**Schema consequence (same shape as DD-050 described, now on the other
side):** `environment-agent`'s `Provider`/`ProviderMetadata` types
(`api/v1alpha1/types.gen.go`) are field-compatible with what
`internal/registration/registration.go` already sends —
`name`/`endpoint`/`service_type`/`schema_version` are all direct string
fields, and `ProviderMetadata` keeps an `AdditionalProperties` catch-all
alongside its own named fields (`region_code`/`zone`/`status`/`resources`),
so the existing `supported_platforms`/`supported_provisioning_types`/
`kubernetes_supported_versions` metadata-nesting approach (REQ-REG-040)
carries over with no payload-construction changes beyond the import swap.

**Lease/TTL:** unlike DD-050's confirmation that `control-plane`'s `Provider`
row has no lease/TTL at all, `environment-agent`'s OpenAPI documents a
"200: lease renewal" response for re-registration, but no actual lease/TTL
*enforcement* was found in `internal/provider/service/service.go` for
external providers as of `8e638b2`. REQ-REG-100 is worded to not overclaim
slot-retention behavior that isn't actually implemented upstream — periodic
re-registration is kept for capability-metadata freshness and as the
REQ-REG-080 retry cadence, not because losing the slot on silence is a
proven risk today.

**Authentication:** unchanged from a behavioral standpoint —
`environment-agent`'s `401` response is documented as "reserved;
authentication deferred to future version," so sending no `Authorization`
header (REQ-REG-115) remains correct, and — unlike DD-050's Authentication
Gap under `control-plane` — this isn't an unenforced no-op sitting on top of
a declared `security: bearerAuth` scheme; `environment-agent`'s OpenAPI
doesn't declare that requirement at all yet, so there's no gap to track here
the way there was under `control-plane`.

**Health-check mechanism:** unchanged, no code changes needed —
`environment-agent`'s `internal/health/monitor/checker.go` polls
`{provider.endpoint}/health` via HTTP, the same mechanism `control-plane`'s
own `healthcheck.Monitor` used (DD-010).

**Not yet addressed by this decision (tracked separately in #33):**
Milestone 2+'s CRUD/status-dispatch design (`internal/cluster`,
`internal/vm`, `internal/statuspublisher`) — `control-plane`'s synchronous
direct-REST dispatch model differs materially from `environment-agent`'s
CloudEvent-through-a-messaging-topic model, and reworking that is separate,
larger scope than this registration-client swap, exactly as DD-050 itself
anticipated back when it deferred Phase 2. This decision covers Topic 4.4
(registration) only.

**Related requirements:** REQ-REG-010, REQ-REG-040, REQ-REG-080 (new),
REQ-REG-090, REQ-REG-100, REQ-REG-115

**Verified against a real local build (2026-08-21):** manually, then as a
committed Tier B suite — `internal/registration/registration_realbackend_test.go`
(TC-I-028/029, gated behind the `realbackend` build tag) plus
`.github/workflows/environment-agent-registration.yaml` — built
`environment-agent` from source at the exact `go.mod`-pinned commit and ran
the real `Registrar` against it (real NATS/JetStream broker, no fakes).
Confirms both idempotent create-or-update-on-name (`201` then `200`,
`update_time` advancing on each re-registration) and per-service-type `409`
exclusivity being retried on the re-registration cadence rather than fatal —
i.e. the fake harness in section 3 of the integration test plan accurately
models the real implementation, not just its documented OpenAPI contract.

**Related:** #33, DD-050 (superseded), DD-040 (unaffected), DD-145/DD-147/DD-148 (the e2e stack's `control-plane` pin — unaffected, separate module)

---

## DD-209: `osac-mock-provider`'s `ClusterTemplates/Get` is implemented, correcting Phase 1's original out-of-scope call

**Decision:** `test/mockprovider/clustertemplates.go` adds a trivial,
stateless `ClusterTemplatesServer` recognizing exactly one well-known
template id (`default-hcp`, matching the e2e suite's own
`validClusterCreateBody`), registered on `cmd/osac-mock-provider`'s
`grpc.Server` alongside the other five fakes (REQ-MOCK-130).

**Rationale:** same category of gap as DD-143's `Clusters/GetKubeconfig`
correction, found the same way — while building this suite's own Cluster/VM
CRUD coverage (Milestone 3/4, REQ-E2E-090..102) against a combined branch
before either milestone's PR merged. `osac-mock-provider` (Phase 1 of the
e2e infra) was built before Milestone 3 existed, so it had no way to
anticipate that `internal/cluster.Service.Create`'s `resolveNodeSetKey`
(M3 REQ-CREATE-080) would call `ClusterTemplates/Get` on every real Cluster
Create. Without this fix, every real e2e Cluster Create against the mock
fails gRPC `UNIMPLEMENTED`, surfaced by `osac-sp` as a generic
500/`INTERNAL` — invisible from `internal/cluster`'s own bufconn-backed
unit/integration tests, which fake `ClusterTemplates` directly and so never
exercise the mock binary's actual (missing) implementation.

**Related requirements:** REQ-MOCK-130, REQ-E2E-090, REQ-E2E-100

---

## DD-210: Default VM network resources (`VirtualNetwork`/`Subnet`) must set a non-empty `metadata.name`

**Decision:** `internal/vm/network.go`'s `provisionDefaultVirtualNetwork`/
`provisionDefaultSubnet` now set `metadata.name` to a fixed constant
(`dcm-default-network` / `dcm-default-subnet`) on the objects they create,
in addition to the ownership labels they already set (REQ-VMNET-020/030).

**Rationale:** discovered the same way as DD-209 and DD-143 before it —
running this suite's own Cluster/VM CRUD coverage against a real backend
for the first time surfaced a gap invisible to both unit tests (bufconn
fakes, which never validate field presence) and `osac-mock-provider`
(Tier A, which also never validates it). Real fulfillment-service rejects
an empty `metadata.name` (`must be at least 1 characters`, plus a
`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$` regex failure for the same empty
value) — the very first VM Create in a job always hits this, since it's
the one that provisions the shared default network. This is a real,
latent product bug independent of the Tier B scoping question in DD-211:
it would also affect a genuine Phase 2 real-backend deployment, not just
this test suite.

**Related requirements:** REQ-VMNET-020, REQ-VMNET-030

---

## DD-211: Cluster/VM CRUD e2e specs are Tier-A-only (`Label("tier-a-only")`), excluded from Tier B's real-backend run

**Decision:** `cluster_crud_test.go`/`vm_crud_test.go`'s `Describe` blocks
carry Ginkgo `Label("tier-a-only")`. A follow-up change to
`.github/workflows/e2e-tierb.yaml` (on `main`, since that file doesn't
exist on this branch) adds `--label-filter='!tier-a-only'` to its `ginkgo`
invocation, so these specs run against `osac-mock-provider` (Phase A,
`e2e.yaml`) only, never against Tier B's real fulfillment-service.
`e2e.yaml` itself passes no filter — Tier A explicitly wants every spec
that's backend-agnostic or mock-only.

**Rationale:** `e2e-tierb.yaml`'s `ginkgo -r -v` runs the entire
`test/e2e` module unfiltered (by design, per DD-153 — `health_test.go`/
`registration_test.go` are intentionally backend-agnostic and meant to run
against both tiers). Once this PR added Cluster/VM CRUD specs to that same
module, they were swept into Tier B's real-backend run for the first time
and failed on `template "default-hcp" not found` — real fulfillment-service
has no such template; these specs hardcode `osac-mock-provider`-only
fixture IDs (`default-hcp`, `default-vm`, `standard-4-16`) and rely on the
mock's synchronous, non-validating Create. Making CRUD genuinely work
against Tier B requires real OSAC templates/instance types, which is
explicitly out of scope until Phase 2's `osac-aap-mock` (DD-152) exists —
so for now, scope these specs out via a label rather than either (a)
fabricating fake-but-real-looking template IDs that would silently break
again the moment Tier B's fixtures change, or (b) leaving Tier B red.

**Related requirements:** REQ-E2E-090..102, REQ-MOCK-130

---

## DD-212: `registration_test.go`/`health_test.go`'s happy path are `Label("tier-b-only")`, pruned from Phase A's mock job

**Decision:** `registration_test.go`'s `Describe("osac-sp registration
with real control-plane", ...)` and `health_test.go`'s
`Describe("osac-sp health, against the real backend", ...)` now carry
Ginkgo `Label("tier-b-only")`. `e2e.yaml` (Phase A) adds
`--label-filter='!tier-b-only'` to its `ginkgo` invocation, so these
specs no longer run against `osac-mock-provider` at all — only against
Tier B's real backend (`e2e-tierb.yaml`, unfiltered on this label,
mirroring `DD-211`'s symmetric `tier-a-only` exclusion in the other
direction).

**Rationale:** implements issue #28, gated on both its dependencies
(`#27`/Tier B, `#24`→`#37`/CRUD e2e) now being merged to `main`. Neither
Describe block ever exercised anything mock-vs-real-specific:
`registration_test.go`'s specs never call the OSAC backend at all — they
only assert what `osac-sp` registered with `control-plane` — and
`health_test.go`'s happy path only proves "a backend that always says
yes makes `osac-sp` report healthy," already covered in-process by
`internal/registration`/`internal/osac`'s own bufconn integration tests,
and covered more rigorously by Tier B's real Keycloak/`fulfillment-service`
saying yes for real reasons. Running both in `e2e.yaml` too was pure
duplication that only earned its keep before Tier B existed. This
supersedes `DD-153`'s note that these two files are "intentionally
backend-agnostic and meant to run against both tiers" — that was true
only for as long as Tier B didn't exist yet to give them non-duplicated
signal on its own.

`osac-mock-provider` itself is unaffected and still required: Cluster/VM
CRUD's idempotency/404-after-delete contracts are only assertable against
a backend with deterministic, synchronous completion (`REQ-MOCK-030`),
which is exactly what real infrastructure structurally cannot offer in CI
— the mock's value was never about registration/health duplication, only
about CRUD.

**Related requirements:** REQ-E2E-020..070

**Related:** #28, DD-153 (refined, not reversed — its readiness-race
rationale for why health checks need polling, not a single-shot check,
still holds; only the "runs in both tiers" claim changes)

---

## DD-213: `osac-aap-mock` hand-rolls its own response structs, not an import of `osac-operator/pkg/aap`

**Decision:** `test/aapmock/` defines its own request/response types
matching the JSON shapes `osac-operator/pkg/aap.Client` sends/expects
(confirmed by reading `client.go` directly — issue #44's own comment
already did this research), rather than importing
`github.com/osac-project/osac/osac-operator/pkg/aap` for its `Job`/
`Template`/`Launch*Response` struct definitions.

**Rationale:** matches `DD-152`'s already-recorded posture for
`osac-mock-provider` ("built from scratch... not adapted from any existing
OSAC-provided fake") and this repo's general stance of not taking a live Go
dependency on OSAC's internal types beyond the vendored proto layer
(`DD-020`). The import-path alternative would also mean pinning to the
monorepo's multi-module tag scheme
(`github.com/osac-project/osac/osac-operator@osac-operator/vX.Y.Z`, `DD-149`)
purely for struct shapes this mock already needs to hand-verify against
source regardless — no meaningful risk reduction for a new, non-trivial
dependency.

**Related requirements:** REQ-TB-080

---

## DD-214: `osac-aap-mock`'s jobs are always immediately `"successful"` — no pending/running simulation

**Decision:** `GetJob` on `osac-aap-mock` always reports a launched job's
status as `"successful"` from the very first poll — there is no
`pending`/`waiting`/`running` transition window, and no artificial delay.

**Rationale:** `osac-operator/pkg/provisioning/aap_provider.go`'s
`mapAAPStatusToJobState` maps AAP's `"successful"` string directly to
`v1alpha1.JobStateSucceeded` (terminal) — reporting this on the first poll
is the minimum needed to satisfy REQ-TB-100's terminal-state proof, and
keeps the CI-side polling loop (`ClusterOrderReconciler.StatusPollInterval`)
from adding wall-clock time against NFR-TB-010's 25-minute budget for no
test-value gain: AC-TB-030 exists to prove real OSAC reconciliation logic
runs correctly against a real terminal AAP outcome, not to exercise AAP's
own job-lifecycle *timing* (never a stated goal of Phase 2, and NFR-TB-030
already scopes `osac-aap-mock` as a hardware/Ansible boundary replacement,
not a fidelity simulation of AAP itself). Mirrors `osac-mock-provider`'s own
precedent of a synchronous, non-validating `Create` (`DD-211`'s framing).

**Related requirements:** REQ-TB-080, REQ-TB-100, NFR-TB-010, NFR-TB-030

---

## DD-215: Phase 2 Helm values explicitly disable unused `osac-operator` controllers and retarget its `ClusterIssuer`

**Decision:** `osac-operator`'s chart install for Phase 2 explicitly sets:

```yaml
controllers:
  clusterOrder: true # REQ-TB-070 scope
  computeInstance: false # avoids kubevirt.io CRD requirement
  tenant: false # avoids k8s.ovn.org CRD requirement
  networking: false
  bareMetalInstance: false # BMFO owns BareMetalInstance reconciliation, not osac-operator
  storage: false # also skips the label-storageclass pre-install hook (avoids pulling quay.io/openshift/origin-cli:4.20.0, ~164 MB)
certs:
  issuerRef:
    name: osac-ca # chart default `default-ca` doesn't exist; Phase 1 already defines `osac-ca` (DD-151)
```

**Rationale:** read `osac-operator/cmd/main.go` directly — each controller
independently gates its own scheme registration
(`hypershiftv1beta1`/`kubevirtv1`/`ovnv1.AddToScheme` only run if the
matching flag is enabled), but `enableAllIfNoneSet()` defaults every
controller to `true` when none are explicitly set, and the chart's own
`values.yaml` defaults match that. Leaving any of these implicit would pull
in CRD requirements (KubeVirt, OVN-K) this phase has no use for and no
CRDs vendored to satisfy — explicit `false` keeps the CRD surface to exactly
the 4 already identified (`ClusterOrder`, `HostedCluster`, `Tenant`,
`BareMetalInstance`), no HyperShift/KubeVirt/OVN-K operators needed. The
chart also unconditionally renders a `cert-manager.io/v1 Certificate` for
its console-proxy `APIService` (not gated by any values flag) — a new
dependency not previously documented, but already satisfied by Phase 1's
existing cert-manager install and `osac-ca` `ClusterIssuer`
(`test/e2e/manifests-tierb/cert-manager-ca.yaml`, `DD-151`); only the
issuer name needs overriding.

**Related requirements:** REQ-TB-070

---

## DD-216: `BareMetalInstance`'s terminal-state proof is out of scope this phase — split to #46

**Decision:** `REQ-TB-100`/`AC-TB-030`'s real-terminal-state proof covers
`ClusterOrder` only in this landing. BMFO is still deployed (satisfying
`REQ-TB-070`'s "deploy real BMFO" half — proves no CRD/RBAC/chart-install
regression), but no `BareMetalInstance` CR is created and no AAP/inventory
backend is wired to it.

**Rationale:** spiked directly against
`bare-metal-fulfillment-operator/internal/controller/baremetalinstance_controller.go`:
a `BareMetalInstance` only reaches AAP *after* an `Allocating` phase driven
by a separate host-management/inventory backend
(`internal/management`/`internal/inventory`). The only two backends BMFO
registers are `openstack` (real Ironic via `gophercloud`) and `metal3`
(reads/writes real `BareMetalHost` CRs, but power operations — confirmed in
`internal/management/metal3.go`'s reboot-annotation handling — depend on the
actual `metal3-io/baremetal-operator` driving real Ironic + a BMC). No
lightweight/fake backend type is registered anywhere in BMFO
(`NewClientForTest`/`NewMetal3ClientForTest` are Go unit-test helpers, not a
runtime-selectable config option). Standing up either real OpenStack Ironic
or Metal3+Ironic+virtual-BMC (e.g. `sushy-tools`) inside `kind` is
substantial, separate scope from an AAP-layer fake — `osac-aap-mock` cannot
substitute for infrastructure that sits entirely upstream of where it's
invoked. Tracked as [#46](https://github.com/dcm-project/osac-service-provider/issues/46).

**Related requirements:** REQ-TB-070, REQ-TB-100

---

## DD-217: BMFO's two non-optional chart secrets (`osac-inventory-config`, `osac-management-config`) are stubbed empty

**Decision:** Phase 2 creates two placeholder Secrets,
`osac-inventory-config` and `osac-management-config` (the chart's default
names, `values.yaml`'s `secrets.inventoryConfig`/`secrets.managementConfig`),
before installing the BMFO chart.

**Rationale:** read the chart's `templates/deployment.yaml` directly — its
`inventory-config`/`management-config` volumes reference these two Secrets
without `optional: true` (unlike `clouds`/`profiles`/`bcm-certs`, which are
all marked optional). Without them, the controller-manager pod fails at
mount time and never starts, regardless of whether `BareMetalInstance`
reconciliation is exercised (`DD-216`) — this is a hard pod-start
requirement, not a lazy-read dependency.

**Correction (2026-08-26):** this entry originally said the Secrets could be
left fully empty because "content is irrelevant for this phase's scope."
That was wrong — passing the mount doesn't mean passing BMFO's own startup
code, which reads and parses these files by name (`cmd/main.go`) and then
constructs a real inventory/management client from their `type` field before
the manager can start at all. An empty Secret satisfies the volume mount but
not the file-read/parse step one layer up; DD-222 covers the actual fix.

**Related requirements:** REQ-TB-070

---

## DD-218: `fulfillment-service` `Hub` registration researched but deferred to #47 — this phase creates `ClusterOrder` directly instead

**Decision:** getting `fulfillment-service` to create real `ClusterOrder` CRs
on this same `kind` cluster (the link a fully-faithful `REQ-TB-100`/
`AC-TB-030` would need) requires registering a `Hub` via
`fulfillment-service`'s own CLI — researched and documented below, but not
implemented in this phase. This phase's e2e suite creates the `ClusterOrder`
CR directly instead; the Hub/CLI mechanism is handed off to
[#47](https://github.com/dcm-project/osac-service-provider/issues/47).

**What was confirmed (for #47's head start):**
`fulfillment-service/it/it_tool.go` (upstream's own integration-test
harness) registers a same-cluster Hub by loading a kubeconfig and rewriting
every cluster entry's `Server` field to `https://kubernetes.default.svc`
(the in-cluster API server address) before registering it — so
`fulfillment-service`'s own reconciliation loop
(`internal/controllers/cluster/cluster_reconciler_function.go`'s
`buildSpec`, which constructs and creates the real `ClusterOrder` CR from a
DB-backed `Cluster` record) targets the cluster it's already running in.
The equivalent, non-`it`-package way to do this is `fulfillment-service`'s
own CLI (`internal/cmd/cli/login`, `internal/cmd/cli/create/hub`): a
stateful, two-step flow (`login --address ... --private --issuer ...
--flow credentials --client-id osac-admin --client-secret ...` persists
connection/auth config, then `create hub --id ... --kubeconfig ...
--namespace ...` uses it to call the private `Hubs` gRPC API).

**Why deferred rather than attempted here:** several details need live-CI
verification before they can be trusted blind in a single PR — whether
`--plaintext` is required for this cluster's in-cluster gRPC (no TLS
termination configured anywhere in Phase 1's wiring), which image/tag runs
the CLI from (a one-off Job vs. `kubectl exec` into the running server
pod), and whether the CLI's config persists correctly across two separate
invocations. Given how many new moving pieces that is on top of #44's
already-large scope (a new binary, 4 new CRDs, two new operators), this is
lower-risk to verify iteratively in its own smaller, focused PR — same
judgment call as `DD-216`'s `BareMetalInstance` split, applied here to a
different (dispatch-chain, not infra-availability) kind of gap.

**Related requirements:** REQ-TB-100

---

## DD-219: Phase 2's CRDs are `kubectl apply`'d directly; `osac-operator-crds`/BMFO's own CRD charts are not used

**Decision:** `e2e-tierb.yaml` installs the 4 vendored CRD YAMLs
(`test/e2e/manifests-tierb/crds/`, DD-149-adjacent vendoring precedent) via
a plain `kubectl apply -f`, rather than `helm install`ing the monorepo's own
separately-published `osac-operator-crds`/`bare-metal-fulfillment-operator-crds`
charts (confirmed to exist on `ghcr.io/osac-project/charts/`, same
`0.0.12` release line as the operator charts themselves).

**Rationale:** the vendored copies were already sourced from
`fulfillment-service/it/crds/` for their fixture-grade `ClusterOrder`/
`HostedCluster` variants (no schema, deliberately loose — see
`manifests-tierb/crds/README.md`), and reusing the same file set for all 4
(including the real, `controller-gen`-generated `Tenant`/`BareMetalInstance`
schemas) avoids depending on two additional chart installs whose only
content is CRD manifests anyway. Confirmed via `helm pull` that neither the
`osac-operator` nor `bare-metal-fulfillment-operator` chart bundles CRDs
itself (no `crds/` directory in either), so there's no double-install/schema-
drift risk from skipping the dedicated CRD charts.

**Related requirements:** REQ-TB-070

## DD-220: Three more CRDs vendored after a live spike found `osac-operator`/BMFO fail to reconcile or even start without them

**Decision:** `test/e2e/manifests-tierb/crds/` gains `osac.openshift.io_computeinstances.yaml`,
`nodepools.hypershift.openshift.io.yaml`, and `osac.openshift.io_baremetalpools.yaml`,
on top of DD-219's original 4.

**Rationale:** a live spike for issue #47 (building the full Phase 2 stack by
hand on a real cluster) and this PR's own CI run both hit the same three
gaps independently:

- `osac-operator`'s startup migration (`migrate-subnetrefs`) unconditionally
  lists `ComputeInstance`s and crash-loops without the CRD present, even
  with `controllers.computeInstance: false` — DD-215's controller-disable
  flags don't prevent this because it runs before the controller-manager
  even starts controllers.
- `osac-operator`'s `ClusterOrderReconciler` always registers a watch on
  `NodePool` (again, regardless of `controllers.*` flags), and without that
  CRD present the watch's `EventSource` never finishes starting — this is
  the most dangerous of the three because it's *silent*: the pod stays
  `Running`, and the main `clusterorder` controller (as opposed to the
  separate `clusterorder-feedback` one) simply never begins reconciling
  anything. No upstream fixture exists for this one (neither
  `fulfillment-service/it/crds/` nor `osac-operator`'s own repo vendor it),
  so it's authored here from scratch, same fixture-grade
  (`x-kubernetes-preserve-unknown-fields`) posture as the existing
  `hostedclusters.hypershift.openshift.io.yaml`.
- BMFO's manager does a hard `unable to start manager` failure at startup
  without the `BareMetalPool` CRD present — a real, `controller-gen`-generated
  schema, sourced the same way `osac.openshift.io_baremetalinstances.yaml`
  was (BMFO's own `config/crd/bases/`).

**Related requirements:** REQ-TB-070, REQ-TB-100

## DD-221: `hub-access-hosted-clusters` ClusterRole gap is a known, unresolved risk for AC-TB-030's terminal-state assertion

**Finding:** even with DD-220's three CRDs applied, a live spike found
`osac-operator`'s `ClusterOrderReconciler` gets stuck indefinitely retrying
`rolebindings.rbac.authorization.k8s.io "{namespace}-hub-access-hosted-clusters"
not found` on every reconcile of a freshly-created `ClusterOrder` — traced to
`clusterorder_controller.go`'s `newHubAccessRoleBinding` component and
`clusterorder_names.go`'s `hubAccessClusterRoleName()`, whose own doc comment
says the `{namespace}-` prefix "account[s] for the kustomize prefix
transformer ... in CI/production overlays" — i.e. this `ClusterRole` is
expected to be pre-created by a kustomize overlay used in upstream's own
CI/production, and isn't shipped by the public Helm chart at all.

**Resolved (2026-08-26):** see DD-223 — root-caused and fixed via a second
live repro, same day. Left this entry as-is (rather than deleting it) since
it captures the original discovery accurately; DD-223 has the fix.

**Related requirements:** REQ-TB-100, AC-TB-030

## DD-222: BMFO's stub inventory/management config (DD-217) selects the `metal3` backend type, not truly empty content

**Decision:** `bmfo-secrets.yaml`'s two Secrets use keys named exactly
`inventory.yaml`/`management.yaml` (not an arbitrary key name) containing
`type: metal3` config pointing at the `default` namespace, plus a new
fixture-grade `baremetalhosts.metal3.io.yaml` CRD.

**Rationale:** this repo's own CI run (not just the #47 spike) hit DD-217's
gap directly — `cmd/main.go` reads these files from hardcoded default paths
(`/etc/osac/inventory/inventory.yaml`, `/etc/osac/management/
management.yaml`) regardless of the Secret's own key name, so DD-217's
original `config.yaml` key was never actually read. Once read, the content
is unmarshalled into a `Config{Type, Options, ...}` struct and fed to a
per-backend client factory — `metal3` was chosen over BMFO's other two
backends (`bcm`, `openstack`) because it's the only one whose client
constructor doesn't also require a live external endpoint to succeed. Its
inventory-side constructor does a CRD-discovery check for
`metal3.io/v1alpha1` (not an actual object read), satisfied by the new
fixture-grade `BareMetalHost` CRD; its management-side constructor has no
such check at all. No `BareMetalHost` objects are ever created this phase,
so `FindFreeHost`/`AssignHost`/power-control calls are never exercised —
this only gets BMFO's manager past startup into a `Ready` pod for
TC-TB-100, consistent with DD-216's terminal-state deferral to #46.

**Related requirements:** REQ-TB-070, REQ-TB-100

## DD-223: DD-221's RoleBinding gap is a genuine chart omission, fixed with a fixture ClusterRole + a scoped `bind` grant

**Decision:** `test/e2e/manifests-tierb/hub-access-hosted-clusters-rbac.yaml`
adds, after `osac-operator` is installed: (1) a `default-hub-access-hosted-clusters`
ClusterRole granting `get/list/watch` on `hypershift.openshift.io`
`hostedclusters`/`nodepools`, and (2) a second ClusterRole + ClusterRoleBinding
granting `osac-operator`'s own ServiceAccount the `bind` verb, scoped via
`resourceNames` to just that one ClusterRole.

**Root cause (confirmed via a second live repro, a minimal single-operator
cluster built specifically to isolate this):** the chart's
`templates/hub-access-clusterrole.yaml` (gated by `.Values.hubAccess.enabled`)
only creates a `{namespace}-hub-access` ClusterRole (`osac.openshift.io`
CRUD) — a different ClusterRole, for a different purpose, than the
`{namespace}-hub-access-hosted-clusters` one the controller code actually
references. The chart never ships the latter at all; this is a genuine
upstream gap, not a version-skew or fixture-vendoring artifact.

**Why both pieces were needed, not just the ClusterRole:** creating only the
ClusterRole (repro'd first) reproduced the *identical* error unchanged —
including with a ruleset that's an exact subset of `osac-operator-manager`'s
own existing `hostedclusters`/`nodepools` permissions (confirmed by reading
that ClusterRole directly off the repro cluster). Kubernetes' RBAC
escalation check did not treat that pre-existing coverage as sufficient in
practice; only adding it back after granting `osac-operator`'s ServiceAccount
`cluster-admin` (isolating the RBAC hypothesis) or, more narrowly, an
explicit `bind` verb on that specific ClusterRole (the actual fix shipped
here) made the `RoleBinding` create succeed. After that, reconciliation
progressed past this component entirely, into real AAP-dispatch behavior
(confirmed by the next error changing to a template-lookup failure — expected,
since the repro cluster had no `osac-aap-mock` running).

**Related requirements:** REQ-TB-100, AC-TB-030

---

## DD-224: test-only binaries live under `test/cmd/`, not the repo-root `cmd/`

**Decision:** Both e2e mock binaries — `osac-aap-mock` and
`osac-mock-provider` — live at `test/cmd/osac-aap-mock/` and
`test/cmd/osac-mock-provider/` respectively, not repo-root `cmd/`.
Repo-root `cmd/` is reserved for binaries this repo actually ships as
product — currently just `cmd/osac-service-provider/`.
`osac-mock-provider` predates this decision (Phase 1) and was moved
alongside `osac-aap-mock` in this same PR rather than deferred, once the
general principle was raised (originally tracked as a separate follow-up in
[#49](https://github.com/dcm-project/osac-service-provider/issues/49),
closed as done-here).

**Rationale:** neither binary has any purpose outside e2e testing; keeping
them out of `cmd/` avoids either being mistaken for production code. Both
binaries' own implementation packages (`test/aapmock/`, `test/mockprovider/`)
were already under `test/` — only the `package main` wrappers needed a new
home, and Go doesn't require `cmd/` at the repo root, so nesting them as
`test/cmd/<binary>/` keeps the familiar `cmd/<binary>/main.go` shape while
making the test-only scope obvious from the path.

**Related requirements:** REQ-TB-080, REQ-MOCK-010

---

## DD-225: `osac-aap-mock` enforces exact-match Bearer token auth, not "any/no header accepted"

**Decision:** every request to `osac-aap-mock` must present
`Authorization: Bearer <token>`, where `<token>` exactly matches the value
the mock was started with (`MOCK_AAP_TOKEN`) — a shared secret with
`osac-operator`'s own `aap.token` Helm value
(`test/e2e/tierb-config/osac-operator-values.yaml`). A missing header, wrong
scheme, or mismatched token gets a real `401`, checked once in `Handler`'s
`ServeHTTP` (`test/aapmock/handler.go`) ahead of every route, not per-handler.

**Rationale:** supersedes this PR's own earlier posture — `TC-U-569`
originally asserted the opposite (any/no `Authorization` header succeeded),
reasoning by analogy to `DD-132`'s permissive OIDC stub. Reconsidered: a mock
that's permissive-by-default on auth risks masking a real production
misconfiguration — a test suite would pass against the mock and only fail
once pointed at real AAP, the opposite of what an e2e suite is for.
`NFR-TB-030`'s actual scope ("no real Ansible/hardware access") never
required this permissiveness in the first place; enforcing a shared-secret
check costs nothing in fidelity terms and closes the gap. `TC-U-569` now
asserts the missing-header case; `TC-U-574` is new, asserting the
wrong-token case.

**Related requirements:** REQ-TB-080, NFR-TB-030

---

## DD-226: `BareMetalInstance`'s `metal3` backend requires zero real hardware/BMC/Ironic simulation — supersedes DD-216's scope-out

**Decision:** `REQ-TB-110`/`AC-TB-040` prove a real `BareMetalInstance`
reaches a real terminal `Ready` phase, driven by real BMFO reconciliation,
using only a static, hand-authored `BareMetalHost` fixture — no OpenStack
Ironic, no Metal3, no virtual BMC (`sushy-tools`), no real
`baremetal-operator`.

**Rationale:** a live spike on a real cluster, reading BMFO's actual source
(`internal/inventory/metal3.go`, `internal/management/metal3.go`,
`internal/controller/baremetalinstance_controller.go`) line by line, found
every single operation the `metal3` inventory/management backends perform
is a plain Kubernetes API read/patch on the `BareMetalHost` object itself —
label list, `consumerRef` set/clear, `spec.online` patch, the
`reboot.metal3.io` annotation set/check. BMFO never talks to Ironic, a BMC,
or the real `baremetal-operator` directly; that component is what would
*eventually* react to those same fields in production, but nothing in
BMFO's own code requires it to be present for BMFO's reconciler to observe
the expected end-state. Proven twice: once with `runStrategy` unset (reaches
`Ready` with zero extra steps), once with `runStrategy: Always` (gets stuck
in `Progressing`, `PowerSynced=False`, until the fixture's
`status.poweredOn` is patched once by hand — the one point where a real or
fake BMO's reaction genuinely matters).

`DD-216` correctly scoped `BareMetalInstance` out of the prior PR given the
information available then — `internal/management/metal3.go`'s
reboot-annotation handling was read as implying a live BMH controller must
exist for BMFO's reconciler to make progress. This DD corrects that
inference with empirical evidence: a live BMH controller is what
*eventually* acts on the fields BMFO writes, not a precondition for BMFO's
own reconcile loop to reach its own terminal state.

**Related requirements:** REQ-TB-070, REQ-TB-110

---

## DD-227: Static `BareMetalHost` fixture shape — two upstream field-naming gotchas, one config-schema gotcha

**Decision:** the `BareMetalHost`/`BareMetalInstance` fixtures added for
`REQ-TB-110` (`test/e2e/manifests-tierb/`) use the exact field names/shapes
below, confirmed against real upstream source rather than inferred from
BMFO's own Go field names.

**Rationale — three real gotchas hit during the spike:**

1. **`status.hardware`, not `status.hardwareDetails`.** BMFO's own log
   message ("NIC inventory is missing") and internal Go field are named
   `HardwareDetails`, but the real upstream `metal3-io/baremetal-operator`
   JSON tag for that field is `hardware` (`HardwareDetails *HardwareDetails
   \`json:"hardware,omitempty"\``), with `nics` (plural) as the NIC list
   key inside it. The vendored fixture CRD's
   `x-kubernetes-preserve-unknown-fields: true` schema doesn't validate
   this — a fixture with the wrong key silently round-trips through
   `kubectl get` (both keys persist side by side) while BMFO just never
   sees NIC data and reports "no matching hosts."
2. **Status must be set via `--subresource=status`, not the create/apply
   body.** The fixture CRD declares `subresources: {status: {}}` (matching
   the real upstream CRD), so any `status:` block in a plain `kubectl
   apply` is silently dropped.
3. **`osac-inventory-config`/`osac-management-config`'s YAML schema**: the
   `Config` struct (`internal/inventory/client.go`) is `{name, type,
   options: map[string]any, hostClass}` — the per-backend block (e.g.
   `metal3: {namespace: ...}`) must be nested under a top-level `options:`
   key, and `hostClass` is a top-level field, not nested inside the
   backend's own options block. `DD-217`/`DD-222`'s existing stub secrets
   happened to already have the right shape (never exercised against a
   real host before now, so this went unverified) — confirmed correct as
   part of this spike, not a new fix to those secrets.

**Related requirements:** REQ-TB-070, REQ-TB-110

---

## DD-228: `bmfo-secrets.yaml`'s `osac-inventory-config` needs a non-empty `hostClass` — corrects a gap in DD-227's own verification

**Decision:** `test/e2e/manifests-tierb/bmfo-secrets.yaml`'s `inventory.yaml`
sets `hostClass: tierb-fixture-hostclass` at the top level (sibling to
`type`/`options`, per DD-227 point 3's own schema description). The checked-in
fixture never actually set it — a real CI run of `TC-TB-110`/`TC-TB-120`
(post-merge, not part of the spike itself) timed out on both specs, stuck at
`Phase: Progressing` indefinitely, `BareMetalInstance`'s own `Allocated`
condition already `True` for both.

**Rationale — the actual mechanism, confirmed against real upstream BMFO
source (`internal/inventory/metal3.go`, `internal/controller/
baremetalinstance_controller.go`) post-merge:** `Metal3Client.hostClass` is
set once, at client construction, straight from `cfg.HostClass` — every
`FindFreeHost`/`AssignHost` call returns a `Host` whose `HostClass` field is
that same (in this case empty) string, never derived from the
`BareMetalHost` object itself. `BareMetalInstanceReconciler.reconcileInventory`
writes that value straight into `bareMetalInstance.Spec.HostClass` on every
successful assignment — and `handleUpdate` picks `reconcileInventory` vs.
`reconcileManagement` based on `bareMetalInstance.Spec.HostClass == ""`. An
empty `hostClass` in the inventory config means that check is never
satisfied: the reconciler re-runs the (idempotent) assign-host steps forever,
setting `Phase=Progressing`/`Allocated=True` each time, but never once
reaches `reconcileManagement` — the function that would eventually set
`Phase=Ready`. This is indistinguishable, from the reconciler's own logs
alone, from a slow-but-progressing power-management flow (both show
"Successfully fulfilled BareMetalInstance" repeating), which is why it wasn't
caught by re-reading those logs alone; it only became clear by tracing
`Spec.HostClass`'s value through `bmhToHost` back to the empty config field.

**Why DD-227 didn't already catch this**: DD-227 point 3 asserted the
existing stub secrets' *shape* (nesting) was "confirmed correct... not a new
fix to those secrets" — true for the YAML structure, but that check verified
nesting, not that every field the schema allows was actually populated with
a working value. The interactive spike session that produced DD-226/227 most
likely ran against a `hostClass` set some other way (e.g. edited directly on
the live cluster) that was never round-tripped back into this checked-in
fixture — the gap was in what the spike verified, not in the schema
description itself.

**Related requirements:** REQ-TB-070, REQ-TB-110

---

## DD-229: `BareMetalInstance` fail-safe/release paths (TC-TB-130..160) — verified against real upstream BMFO source before writing any assertion, one candidate scenario ruled out

**Decision:** REQ-TB-120/AC-TB-050's four negative/release cases
(no-matching-host, ineligible-host, contended-host, delete-time release)
are all grounded directly in `bare-metal-fulfillment-operator`'s real
`main`-branch source (`internal/inventory/metal3.go`'s `FindFreeHost`/
`AssignHost`, `internal/controller/baremetalinstance_controller.go`'s
`reconcileInventory`/`handleDeletion`), read before any fixture or
assertion was written — not inferred from the happy-path behavior
DD-226/227 already proved:

- **No matching host / ineligible host**: `FindFreeHost`'s candidate filter
  requires `OperationalStatus == OK`, `Provisioning.State == Available`,
  and `ConsumerRef == nil` — a host failing any of these is silently
  excluded from the candidate list, indistinguishable (by the reconciler)
  from that `hostType` having zero hosts at all. Both converge to the same
  `reconcileInventory` zero-candidates branch: `Phase = Failed`,
  `Allocated` condition `False`/reason `"Failed"`/message `"No matching
  hosts available"`, requeued indefinitely (never a silent `Ready`, never
  an un-requeued dead end).
- **Contended host**: `AssignHost` guards against double-claim —
  `bmh.Spec.ConsumerRef != nil && ...Name != bareMetalInstanceID` returns
  `nil, nil` (not an error) to the loser, which clears its own
  `Spec.ExternalHostID` and retries `FindFreeHost`. Once the winner's claim
  is visible, the loser's retry sees zero candidates (the one host is now
  `ConsumerRef != nil`) and converges to the identical `Failed` path above.
  No locking bug risk of both reaching `Ready`: `TryLock`/`AssignHost`'s
  own re-check is what prevents it, not test-side timing.
- **Delete-time release**: `handleDeletion`'s inventory-finalizer cleanup
  calls `UnassignHost`, which clears `Spec.ConsumerRef` — and `kubectl
  delete` (no `--wait=false`) blocks until that finalizer cleanup has
  actually completed, so the release assertion needs no `Eventually`.

**One candidate case deliberately dropped**: reverting a `Ready`,
`runStrategy: Always` instance's host back to `status.poweredOn: false`
post-`Ready`, to prove BMFO detects the drift. Ruled out by reading the
same source: `SetupWithManager` only watches `BareMetalInstance` itself
(no `Watches()` on `BareMetalHost`), and `reconcileManagement`'s own
`Ready` branch returns `ctrl.Result{}` with no `RequeueAfter` — there is
no trigger, periodic or event-driven, that would ever cause BMFO to
re-examine a `BareMetalHost` after its owning instance reaches `Ready`.
This is a genuine gap in BMFO itself (drift from an already-`Ready` state
is silently missed), not a gap in this suite's test design — fixing it
means changing BMFO, out of scope for `osac-service-provider`'s own e2e
suite. Recorded here so a future reader doesn't re-propose this exact test
without re-deriving the same finding.

**Related requirements:** REQ-TB-070, REQ-TB-120

---
## DD-230: TC-TB-090/TC-TB-120 deepened beyond `.status.phase == Ready` — exact condition/field values verified against real upstream source

**Decision:** in response to PR review feedback that TC-TB-090 and
TC-TB-120 asserted only `.status.phase == "Ready"` (which a reconciler bug
that set `Ready` via the wrong path would still satisfy), both were
extended to assert the specific condition and status-field values real
`osac-operator`/BMFO only set once the underlying work has actually
happened — each value read directly from the real upstream source before
being asserted, same discipline as DD-229:

- **TC-TB-090** (`ClusterOrder`, `osac-operator`
  `internal/controller/clusterorder_controller.go`'s
  `provisioningCallbacks.OnSuccess`, `api/v1alpha1/conditions.go`,
  `api/v1alpha1/job_types.go`): asserts `Progressing` condition
  `False`/reason `"AsExpected"`, and the last `.status.provisioningJobs`
  entry has `type: "provision"`/`state: "Succeeded"` (`JobTypeProvision`/
  `JobStateSucceeded`). Both are set only inside the AAP-poll-succeeded
  branch, not by any other code path that could reach `Phase: Ready`.
- **TC-TB-120** (`BareMetalInstance`, BMFO
  `internal/controller/baremetalinstance_controller.go`'s
  `syncBareMetalInstanceStatus`): asserts `PowerSynced` condition
  `True`/reason `"PowerOn"`, and `.status.runStrategy: "Always"` — both
  set only after the reconciler has re-read the host's power state
  post-patch and found it converged (`poweredOn == true` branch), not
  merely inferred from `Phase` flipping to `Ready`.

Deliberately did **not** extend TC-TB-110 (the `runStrategy` unset sibling
of TC-TB-120) — the reviewer's comment named only TC-TB-090/TC-TB-120, and
TC-TB-110's `PowerSynced`/`runStrategy` values (`PowerOff`/`Halted`, since
the fixture host is never powered on) aren't a meaningfully different
regression class from what TC-TB-120's new assertions already cover for
the power-sync path. Also deliberately did **not** assert on
`ClusterOrder`'s `ControlPlaneAvailable`/`ClusterAvailable` conditions
(`clusterorder_controller.go`'s `handleHostedCluster`) — those gate on the
real `HostedCluster` CR's own status becoming available, which never
happens in this suite (the vendored `HostedCluster` CRD is fixture-grade
with no real HyperShift operator reconciling it, DD-218/DD-220), so
asserting them would either hang or assert a value the suite's own
architecture makes permanently unreachable.

**Related requirements:** REQ-TB-080, REQ-TB-100, REQ-TB-110, AC-TB-030,
AC-TB-040

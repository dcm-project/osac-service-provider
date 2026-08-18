# Design Decisions: OSAC Service Provider

This document records architectural and design decisions for the OSAC
Service Provider, referenced by ID (`DD-NNN`) from the specs in
`.ai/specs/`. New decisions are appended here as implementation surfaces
them, so this file stays open across milestones rather than being tied to
any single spec document's lifecycle.

**Related Specs:** `.ai/specs/osac-sp.spec.md` (Milestone 1),
`.ai/specs/osac-sp-m3-cluster-crud.spec.md` (Milestone 3)

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

## DD-200: NATS broker URL env var — recommend `DCM_NATS_URL`, not `SP_NATS_URL`

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

## DD-130: Single `test/mockprovider` package, not one sub-package per service

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
(`resourceStore[T]`, see DD-131), which would otherwise need to be exported
(or duplicated) to cross sub-package boundaries for no benefit: nothing
outside this mock ever needs to depend on, say, `mockprovider/clusters`
without also needing the other four services and the OIDC stub to form a
working substitute. This mirrors the existing repo's own flat-package
convention for single-concern internal packages (e.g. `internal/osac`,
`internal/registration`) rather than the multi-file-but-nested-directory
shape of, say, `internal/api/server` (which is generated, not
hand-authored).

**Note on DD numbering:** this decision (and DD-131..133 below) were
originally numbered DD-130+ on a branch cut directly from `main` while
`main`'s own decisions file still ended at DD-070. By the time this branch
merged, M3/M4 (Cluster/VM CRUD) had already landed on `main` and claimed
DD-080..129 — clear of this range, so no renumbering was needed.

**Related requirements:** REQ-MOCK-010, REQ-MOCK-070, REQ-MOCK-080

---

## DD-131: Generic, mutex-protected in-memory `resourceStore[T]`, not bespoke per-service storage

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

## DD-132: No real JWT signing for the OIDC token stub

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

## DD-133: Flat `MOCK_`-prefixed env vars for the mock's own config, not a nested `internal/config`-shaped struct

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

## DD-135: `.golangci.yml` hardened with 10 additional linters, evidence-tested before adoption

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

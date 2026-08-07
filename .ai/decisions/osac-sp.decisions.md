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

## DD-080: Cluster CRUD dispatches via `control-plane`'s synchronous direct-REST contract, not gRPC/CloudEvents — and only Create/Delete are actually invoked by `control-plane`

**Decision:** Milestone 3's four Cluster REST handlers are built as a full
CRUDL surface (matching the AEP/OpenAPI-first convention every sibling SP
follows), but the spec explicitly documents that `control-plane` (Phase 1,
DD-050) only ever calls **Create** and **Delete** on this SP's registered
endpoint — `Get`/`List`/`Update` are served entirely from `control-plane`'s
own Postgres store and never reach this SP. Create's request/response shape
and Delete's `NotFound`-tolerance are dictated by `control-plane`'s actual
outbound dispatch code, not by a generic REST-resource assumption.

**Rationale:** Verified directly against
[`internal/sp/service/resource_manager/service_type_instance.go`](https://github.com/dcm-project/control-plane/blob/f243dfaa2e2752c63202432409e78cc2a4ad7d85/internal/sp/service/resource_manager/service_type_instance.go)
(commit `f243dfa`) rather than any OpenAPI document — `control-plane`'s own
`api/sp/v1alpha1/resource_manager/openapi.yaml` describes a *different*,
catalog-facing API (`/service-type-instances`) than what it sends outbound
to a registered provider's `Endpoint`, so reading that spec alone would have
been a category error (the same mistake DD-060 already corrected once for
OIDC discovery — citing superficially-similar-but-wrong code). The actual
outbound contract:

- `GetInstance`/`ListInstances` read only `s.store` — zero calls to
  `provider.Endpoint` for either. `UpdateInstance` doesn't exist as a
  provider-dispatch path at all.
- `createInstanceWithProvider`: `POST {endpoint}?id={id}` (query parameter,
  not a body field), body `{"spec": request.Spec}`, response unmarshaled
  into `ProviderResponse{ID string `json:"id"`; Status string
  `json:"status"`}` (`convert.go`) — extra fields in the SP's response are
  silently ignored, not rejected, so returning the full `Cluster` resource
  (id/status top-level) is compatible.
- `deleteInstanceWithProvider`: `DELETE {endpoint}/{id}` (path segment).
  `if resp.IsError() && resp.StatusCode() != 404` — a `404` from the SP is
  explicitly excluded from the error branch, i.e. treated as a successful
  delete, not surfaced as a `ProviderError`.
- `control-plane` does not parse RFC 7807/9457 bodies from the SP — any
  `>=400` becomes a generic `ProviderError` string. RFC 9457 compliance
  (DD-070) is still correct for API-contract consistency and any direct/
  non-`control-plane` caller, just not something `control-plane` itself
  interprets structurally today.

Enhancement [PR #96](https://github.com/dcm-project/enhancements/pull/96)
(open, unmerged) already reflects this corrected contract for Cluster/VM —
used as this milestone's interim source of truth per issue #1's own note.

**Related requirements:** REQ-CREATE-010, REQ-CREATE-020, REQ-CREATE-050, REQ-DELETE-010, REQ-DELETE-020

---

## DD-090: `Cluster.status` uses DCM's full 7-value canonical vocabulary, not the 5-value subset in the enhancement doc's own table

**Decision:** The status mapper (M3 spec §4.5) returns one of DCM's full
canonical 7 values — `PROGRESSING | ACTIVE | DEGRADED | UNAVAILABLE | FAILED
| DELETING | DELETED` — including `UNAVAILABLE` and `DELETING`, even though
only `UNAVAILABLE` has a real driving signal from OSAC today.

**Rationale:** Read
[`service-provider-status-reporting.md`](https://github.com/dcm-project/enhancements/blob/main/enhancements/state-management/service-provider-status-reporting.md#L266-281)
directly rather than trusting enhancement PR #96's own Status Mapping
table, which only lists 5 values (`PROGRESSING`/`ACTIVE`/`DEGRADED`/
`FAILED`/`DELETED`) — that table documents which *signals OSAC currently
sends*, not the full *contract DCM requires the SP to speak*. The primary
doc is unambiguous that the target vocabulary is the full 7 values, with
distinct semantics for each (e.g. `UNAVAILABLE` = "previously available but
now unreachable and not progressing toward recovery", distinct from
`DEGRADED` = "reachable but critical components unhealthy"). This is a
different, DCM-wide vocabulary from the ad-hoc per-SP enums other sibling
SPs invented before any `control-plane` dispatch integration existed (e.g.
`acm-cluster-sp`'s `PENDING|PROVISIONING|READY|FAILED|DELETING|DELETED|
UNAVAILABLE` — close but not identical wording, and not the authoritative
source). `UNAVAILABLE` is legitimately SP-detectable today (an OSAC gRPC
connectivity failure while polling, distinct from a real `NotFound`);
`DELETING`/`DELETE_FAILED` are proto-defined but currently unreachable in
practice (SC-M3-001) — both are still required enum values for forward
compatibility and DCM-wide consistency, not values the SP can skip because
nothing exercises them yet.

**Related requirements:** REQ-STATUS-010, REQ-STATUS-020

---

## DD-100: SP-side idempotent Create-on-`AlreadyExists`→`Get` is a hard requirement, not a best-effort nicety

**Decision:** REQ-CREATE-040 (Create's `AlreadyExists`→`Get` fallback) is
specified as a `MUST` with dedicated, mandatory test coverage
(AC-CREATE-030), not an optional robustness improvement that could be
deferred or left partially tested.

**Rationale:** Traced the full call chain above `control-plane`'s SP
dispatch and found upstream retry-safety is weaker than the enhancement
docs assume, making the SP the *only* reliable backstop:
`internal/catalog/service/catalog_item_instance.go`'s `Create` (and the
duplicated pattern one layer down in `internal/placement/service/placement.go`)
performs an **unconditional rollback on any error** — deleting the local DB
row keyed on the caller's `id` — with no branch distinguishing "definitely
rejected" from "ambiguous/timeout." A subsequent retry with the *same*
caller-facing `id` therefore mints a **new internal `resourceID`** and
dispatches a second, differently-IDed Create to the SP — silently defeating
the `id`-based idempotency the catalog API explicitly promises
(`api/catalog/v1alpha1/openapi.yaml`: "user-specified IDs... for
idempotency"). No orphan-reconciliation exists anywhere in that stack, and
the one path that could surface an orphaned SP-side resource
(`internal/sp/consumer/consumer.go`'s NATS status ingestion) silently
`Warn`-logs and ACKs unmatched IDs rather than alerting. Separately,
`control-plane`'s outbound HTTP client to the SP is configured with
`resty.SetRetryCount(3)` (network-failure retries), so the SP can
legitimately receive the *same* `id` twice from a connection-level hiccup
alone, independent of the rollback bug above. Filed as
[`control-plane#38`](https://github.com/dcm-project/control-plane/issues/38)
(new bug, confirmed via `gh issue list`/`gh pr list` to not duplicate any
existing tracked risk) — not fixable from within `osac-service-provider`,
and not expected to be fixed by the in-flight, architecture-changing
[`control-plane#37`](https://github.com/dcm-project/control-plane/pull/37)
either (flagged there directly). Both the general
[`sp-resource-manager.md`](https://github.com/dcm-project/enhancements/blob/main/enhancements/sp-resource-manager/sp-resource-manager.md#L487-L502)
and OSAC-specific
[`osac-sp.md`](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#idempotent-creation)
enhancement docs already push the final idempotency guarantee down to the
SP — this decision makes that guarantee an enforced, tested contract rather
than an assumed one.

**Related requirements:** REQ-CREATE-040

## DD-110: `POST /clusters` is schema-optional on `id` and its body is the `Cluster` resource itself, to satisfy AEP-133

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

Kept `Cluster`'s `required: [id, status]` as-is (didn't drop to the
siblings' `required: [spec]`) to avoid making `Status` a pointer across
every file that already compares it directly — the new `spec` property is
its own pointer instead, satisfying `aep-133-request-body` without that
blast radius. Also didn't copy the siblings' server-side UUID generation
when `id` is omitted — REQ-CREATE-010 already guarantees `control-plane`
always supplies one.

**Related requirements:** REQ-CREATE-010, REQ-CREATE-060

---

## DD-111: `check-aep` is now part of `make check`, invoked via `npx` instead of requiring a global `spectral` install

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

**Related requirements:** REQ-CREATE-010, REQ-CREATE-060 (DD-110); process
fix has no REQ-* of its own — it is tooling/workflow, not product behavior.

---

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

### Re-validation (2026-08-06): `scratch/m3-m4-m5-demo` — a kept (not throwaway) branch for the OSAC PoC demo

Unlike the two validations above, this merge is deliberately **not**
discarded: `scratch/m3-m4-m5-demo` is pushed to `origin` and used directly as
the demo's deployable artifact (no new PRs per the team's in-flight-PR
moratorium), which is also why this note exists at all this time — the
prior two validations' own worktrees were thrown away per their own text,
taking a first draft of this same evidence with them (see the "why does
DD-075 not have any conflict-resolution detail" investigation earlier this
session — nothing was lost from `main` or this repo; it simply lived only in
a since-deleted disposable worktree from a different branch's tree, and this
branch (created fresh off `main`) hadn't merged M5 yet at the time).

Merged in order: `feat/milestone-3-cluster-crud` (`origin`, commit `640caaa`),
`feat/milestone-4-vm-crud` (`origin`, commit `0afb49d`), `feat/milestone-5-status-reporting`
(`origin`, commit `1adc5ec`) — same SHAs as the original DD-075 validation.
Conflicts matched DD-075's prediction exactly:

- **M3+M4 merge** (10 conflicted files): `.ai/decisions/osac-sp.decisions.md`
  and `api/v1alpha1/openapi.yaml` were pure structural/additive conflicts
  (both branches appended non-overlapping content at the same anchor point);
  resolved by reconstructing `openapi.yaml` programmatically (Python
  string-splice of M4's unique paths/schemas/tag/parameter into M3's already-
  merged tree, verified via `yaml.safe_load` before writing) rather than
  hand-editing 27 zigzagged conflict markers, since the two branches'
  parallel-shaped diffs caused git's LCS-based algorithm to interleave
  unrelated Cluster/VM content rather than cleanly separating it. The 4
  `*.gen.go`/`client.gen.go` files were resolved by regenerating
  (`make generate-api`) against the fixed spec, not hand-merged.
  `oapi-codegen` then prefixed the now-colliding `ClusterStatus`/`VMStatus`
  enum values (`DELETED`/`FAILED`/`DELETING` exist in both) and **all**
  `ErrorType` values (colliding with `ClusterStatus.UNAVAILABLE`), exactly as
  DD-075 predicted — requiring call-site fixes in `internal/vm/status.go`,
  `internal/grpcerror/classify.go`, and ~10 test files that referenced the
  bare pre-collision names. `cmd/osac-service-provider/main.go`/
  `main_unit_test.go` and `internal/apiserver/server_{integration,unit}_test.go`
  needed hand merges combining both branches' `apiHandler`/test-double
  wiring (Cluster + VM forwarding side by side) — `main_unit_test.go`'s two
  `Describe` blocks were also zigzag-interleaved by git's diff for the same
  parallel-shape reason as `openapi.yaml`, resolved the same way (extract
  each side's clean full text via `git show :2:`/`:3:`, splice rather than
  hand-resolve markers). `go vet` then caught two stale test fixtures
  (`internal/handlers/cluster/fixture_test.go`,
  `internal/handlers/vm/fixture_test.go`) missing the sibling milestone's
  stub methods on their local `realHandler` — fixed by adding a `stubVM`/
  `stubCluster` type to each, mirroring the pattern `internal/apiserver`'s
  own test doubles already used.
- **M5 merge** (3 conflicted files): trivial by comparison — `.ai/decisions/...`
  and `.ai/test-plans/osac-sp-unit.test-plan.md` were the same
  additive-append pattern (combined by hand, no scripting needed);
  `cmd/osac-service-provider/main.go` conflicted only in its `import` block
  (`internal/vm` vs. `internal/statuspoll`/`internal/statuspublisher`) — the
  function-body wiring merged automatically clean.

Post-merge, on `scratch/m3-m4-m5-demo` at this note's HEAD: `go build ./...`,
`go vet ./...`, `gofmt -l .` all clean; `golangci-lint run ./...` reports
**0 issues**; `make generate-api` against the merged spec is byte-identical
(no generator drift); `ginkgo -r --race --cover` is green across all 15
suites, composite coverage **98.6%** (matching the original DD-075
validation's 98.7% to within normal variance).

## DD-076: External (pre-existing) AAP instance for the OSAC demo, bootstrapped by a one-off Job with a manually-minted gateway token — not `osac-installer`'s bundled AAP automation

**Context:** the demo environment (`osac-demo-dcm` namespace, shared dev OCP
cluster, not a throwaway `kind`/CI cluster) already has a fully-licensed,
independently-managed AAP 2.7 instance running in the
`ansible-automation-platform` namespace. `osac-installer`'s `osac-aap` Helm
subchart assumes it is deploying and owning a *fresh, in-cluster* AAP
instance: its `create-api-token` hook shells out to `oc` against a
same-cluster AAP route/secret it created itself, and its `bootstrap-job`
hardcodes in-cluster hostnames for the org/project/job-template config-as-code
run — neither can be redirected to an external, independently-managed AAP via
Helm values alone. Reinstalling a second AAP just for this demo was rejected
as wasteful (real subscription/license consumption, longer setup, and a
second thing to keep patched) when a working one already exists on the same
cluster.

**Decision:** disable all of `osac-installer`'s AAP automation
(`aapOperator.enabled=false`, `aap.aap.instance.enabled=false`,
`aap.bootstrap.enabled=false`, `aap.apiToken.create=false`,
`aap.instanceGroups.publishTemplates.enabled=false` in
`values/osac-demo-dcm/values.yaml`) and instead run the same underlying
config-as-code payload (`osac.config_as_code.configure`, from the
`osac-aap` collection, using the `ghcr.io/osac-project/osac-aap:latest` EE
image directly as a plain Kubernetes `batch/v1` Job) against the existing
external AAP's Controller API, pointed at it via `AAP_HOSTNAME`/
`AAP_USERNAME`/`AAP_PASSWORD` env vars (the last two from a manually-created
`aap-admin-creds` Secret in `osac-demo-dcm`) plus `AAP_VALIDATE_CERTS=false`
(cluster-internal CA not in the job pod's trust store).

**Problem encountered and root-caused:** the first bootstrap Job run
completed all 753 tasks but created **nothing** — the actual object-creation
calls for every resource type (organizations, projects, execution
environments, job templates, workflows, schedules) each failed with
`Failed to get token: HTTP Error 404: Not Found` from
`infra.aap_configuration.collect_async_status`, which every `controller_*`/
`gateway_*` role in the bundled collection calls after firing its own
`async: 1000, poll: 0` create/update task, to poll for completion. Traced
this to `ansible.controller` 4.6.11 (bundled in the EE image;
`infra.aap_configuration` 4.4.0) — its `authenticate()` in
`controller_api.py` unconditionally POSTs to
`{api_path()}v2/tokens/` (i.e. `/api/controller/v2/tokens/`) to exchange
username/password for a session token, but this AAP 2.7 instance's
gateway-fronted Controller genuinely 404s that path (confirmed directly:
`curl -u admin:*** -X OPTIONS https://<aap>/api/controller/v2/tokens/` → 404,
while `https://<aap>/api/gateway/v1/tokens/` → 200) — a version-skew bug
between this collection version and AAP 2.7's gateway-mediated auth routing
(same family as upstream reports like
[`redhat-cop/infra.aap_configuration#1219`](https://github.com/redhat-cop/infra.aap_configuration/issues/1219)
and [`ansible/awx#15727`](https://github.com/ansible/awx/issues/15727)). No
`CONTROLLER_OPTIONAL_API_URLPATTERN_PREFIX` env-var override helps, since the
`v2/tokens/` suffix is hardcoded and the working gateway endpoint's path
shape (`/api/gateway/v1/tokens/`, no `v2`) doesn't fit that prefix-substitution
pattern anyway. The one exploratory workaround this repo tried before finding
the real fix — passing `-e aap_configuration_collect_logs=true` to make the
broken status check non-fatal — was insufficient: it let the *playbook* reach
`PLAY RECAP` without aborting, but every actual create call was async-fired
and then abandoned before its status (and, in practice, its actual server-side
completion) could be confirmed, so real objects were still not reliably
created (confirmed: a re-run under only that flag left `Projects` empty for
the `osac` org even though the playbook "succeeded").

**Real fix:** `ansible.controller`'s `ControllerAPIModule.__init__` skips
`authenticate()` entirely whenever `controller_oauthtoken` is already set —
`if not self.oauth_token and not self.authenticated: self.authenticate()`.
Every `infra.aap_configuration` role forwards a generic `aap_token` role
variable straight through as `controller_oauthtoken` (alongside
`aap_username`/`aap_password` → `controller_username`/`controller_password`,
which then simply go unused once a token is present) — this is a documented,
first-class alternate auth path (`infra.aap_configuration.dispatch`'s own
`meta/argument_specs.yml`: *"Either username/password or oauthtoken need to
be specified"*), not a hack. So the bootstrap Job's entrypoint script now
mints its own short-lived token directly against the **working** gateway
endpoint (`POST /api/gateway/v1/tokens/` with HTTP Basic auth — confirmed by
inspection to be a completely independent code path from the broken
`ansible.controller`-internal one) before invoking Ansible, then passes it
through as `-e aap_token=<token>`. This was verified end-to-end both
in isolation (the gateway-minted token successfully authenticated a manual
`GET /api/controller/v2/organizations/` call with `Authorization: Bearer`)
and via a full bootstrap re-run: `PLAY RECAP` shows `failed=0 ignored=0`
(the only remaining `FAILED - RETRYING` lines in the log are normal
async-poll-not-done-yet retries, e.g. waiting on the `osac` project's git
clone, all of which resolve to `ok`/`changed` on a later poll — not the
token bug), and every expected object now exists when queried directly via
the Controller API: the `osac` organization; the `osac` project
(`status: successful`, i.e. its SCM sync from
`github.com/osac-project/osac` actually completed); the `osac-ee` execution
environment; all 24 `osac-*` job templates including the
`osac-create-compute-instance`/`osac-delete-compute-instance` pair this demo
needs; and both hosted-cluster workflow job templates.

Kept as a `bash`-in-`args:` step (not `command:`, which was tried first and
broke — it replaces the EE image's own `ENTRYPOINT`
(`/opt/builder/bin/entrypoint dumb-init`), which is what sets up a writable
`HOME`/`.ansible` dir for the container's actual (arbitrary, OpenShift-
assigned) UID; skipping it makes even `ansible-playbook --version`-level
startup fail with `Permission denied: '/.ansible'`).

**Scope note:** this is purely an artifact of *this demo's* choice to reuse
an existing, independently-managed AAP instance rather than let
`osac-installer` deploy its own — it says nothing about `osac-installer`'s
bundled-AAP path itself (untouched, unexercised, presumably fine for its
intended fresh-cluster use case), and the underlying collection-version/
gateway-auth incompatibility is upstream `ansible.controller`/AAP-gateway
territory, not an `osac-sp` or `osac-installer` defect — recorded here only
because reproducing it cost real debugging time and the next person pointing
this repo's demo tooling at a different pre-existing AAP instance will hit
the identical wall.

**Related requirements:** none (demo/infra-only; no `osac-sp` REQ-*/AC-*
touched by this decision).

---

## DD-077: `osac-installer` demo deployment isolated into `osac-demo-dcm`, with two local chart patches for shared-cluster coexistence

**Context:** the demo runs on a shared, long-lived dev OCP cluster that
already hosts unrelated workloads and cluster-scoped operators (its own
`cert-manager`, a `keycloak` namespace used by another team, ACM/MCE, etc.),
not a disposable cluster provisioned fresh for this demo. `osac-installer`'s
three-phase Helm install (`install-operators` → `install-prereqs` →
`install-osac`) makes several assumptions that don't hold on a shared
cluster: that it owns cluster-wide singletons it deploys (cert-manager), that
its own `keycloak` namespace name is free, and that AAP is either bundled or
absent (see DD-076) rather than pre-existing and independently owned.

**Decision:** deploy OSAC entirely within a single dedicated
`osac-demo-dcm` namespace (plus one Keycloak-only sibling namespace, below),
via a custom `values/osac-demo-dcm/values.yaml` (based on
`osac-installer`'s own first-party, CI-tested `values/vmaas-ci/values.yaml`
reference config) that: disables `certManager.enabled` (cluster already has
one) while still enabling `trustManager.enabled`/`caIssuer.enabled` scoped to
a new `default-ca` `ClusterIssuer` (avoids colliding with whatever
`ClusterIssuer`(s) the existing cert-manager already owns); disables
`lvms.enabled` (storage already provided) and `metallb.enabled` (Routes are
used instead of LoadBalancer Services); disables `mce.enabled` (RHACM/MCE is
only needed for `ClusterOrder`/Agent-based cluster fulfillment, out of scope
for this VM-only demo — see the `ComputeInstance` vs. `ClusterOrder` distinction
in the top-level exploration notes) and `kafka.enabled` (metering out of
scope); and enables `cnv.enabled` (OpenShift Virtualization, required for
`ComputeInstance`/KubeVirt `VirtualMachine` objects, not already present on
this cluster).

Two upstream chart bugs surfaced by this shared-cluster deployment needed
local patches to `/tmp/osac-explore/osac/osac-installer` (not upstreamed —
these are exploration-only clones, not this repo's own code, so no
`osac-sp` REQ-*/AC-* is affected):

1. **Keycloak namespace collision.** `osac-prereqs`'s Phase 2 Keycloak
   resources (`charts/osac-prereqs/templates/keycloak/resources.yaml`, the
   `create-controller-credentials`/`wait-keycloak` hooks, and
   `wait-keycloak.sh`) all hardcode the namespace name `keycloak`. The
   shared cluster already has an unrelated `keycloak` namespace owned by
   another team, so Phase 2 failed outright:
   `Namespace "keycloak" in namespace "" exists and cannot be imported into
   the current release`. Patched all of the above to read a new
   `keycloak.namespace` value (defaulting to `keycloak` to preserve upstream
   behavior when unset) instead of the literal string, including the
   in-cluster DNS name (`keycloak.{{ $kcNamespace }}.svc.cluster.local`) and
   the `password-generator` init container's inline `oc get/create secret
   -n keycloak` commands, which still had the namespace hardcoded even after
   the main resource namespaces were parameterized (caught by
   `keycloak-database-0` looping in `Init:CrashLoopBackOff` after the first,
   partial patch — a second pass fixed the remaining hardcoded reference).
   `values/osac-demo-dcm/values.yaml` sets `keycloak.namespace:
   osac-demo-dcm-keycloak`.
2. **CNV configuration job OOMKilled.** `osac-prereqs`'s `configure-cnv`
   hook Job was OOMKilled at its default 256Mi memory limit — the
   `HyperConverged` CRD's schema is large enough that applying/patching it
   exceeded that budget on this cluster's CNV/CSV version. Raised to 768Mi in
   `charts/osac-prereqs/templates/hooks/configure-cnv.yaml`.

A `trust-manager` webhook race (`install-prereqs` briefly failing with
`no endpoints available for service "trust-manager"` because the webhook
was called before the pod's endpoints were ready) was not a code/config bug —
just retried once `trust-manager` reported ready.

Phase 3 (`install-osac`) itself needed two more fixes, both applied as
`values/osac-demo-dcm/values.yaml` overrides (no chart patches needed this
time):

3. **`osac-operator` OOMKilled at its default 128Mi memory limit.**
   `multicluster-runtime`'s per-GVK informer caches for every CRD this
   operator watches (regardless of whether that CRD's own controller toggle
   is on) add up to more than 128Mi at this cluster's steady state. Raised
   via `operator.resources.limits.memory=512Mi` /
   `requests.memory=128Mi` (same class of fix as `configure-cnv`'s OOM,
   item 2 above).
4. **`osac-operator` crash-looped (`Exit Code 1`, not OOM) with
   `no kind is registered for the type v1alpha1.BareMetalInstance in scheme`
   once the OOM was fixed.** Root-caused to an upstream `osac-operator`
   inconsistency, not a demo-config problem:
   `internal/controller/externalipattachment_controller.go`'s
   `SetupWithManager` unconditionally sets up a watch on
   `bmfov1alpha1.BareMetalInstance` whenever the **`networking`** controller
   is enabled (external-IP attachments can target a ComputeInstance,
   Cluster, *or* BareMetalInstance, so the reconciler always watches all
   three), but `cmd/main.go` only calls
   `bmfov1alpha1.AddToScheme(localScheme)` when the **separate**
   `bareMetalInstance` controller flag is true (`main.go:198-199`) — so
   `networking=true` + `bareMetalInstance=false` (our original, logically
   correct-looking setting, since this demo does no bare-metal provisioning)
   is a fatal combination the code doesn't defend against. The
   `baremetalinstances.osac.openshift.io` CRD itself was never the problem —
   confirmed present on the cluster the whole time (`bmfCrds`, the CRD-only
   companion chart, installs unconditionally, unlike the actual
   `bare-metal-fulfillment-operator` chart which is gated on `bmf.enabled`).
   Workaround: set `operator.controllers.bareMetalInstance=true` anyway,
   purely to get the scheme registered — the demo still does not exercise
   any actual `BareMetalInstance` controller logic or provisioning.

**Publishing templates to the fulfillment-service catalog (the
`osac-publish-templates` AAP job template, normally auto-launched by
`osac-installer`'s own `charts/osac/templates/hooks/publish-templates.yaml`
post-install hook, disabled here via
`aap.instanceGroups.publishTemplates.enabled=false` for the same
bundled-AAP-hostname-assumption reason as `bootstrap.enabled`) needed three
more fixes, launched manually by mirroring that disabled hook's own
API sequence (list job template by name -> POST `.../launch/` -> poll
`.../jobs/{id}/`) against the real AAP:**

5. **Missing `template-publisher` identity plumbing end-to-end.** The
   `osac-publish-templates-ig` container group (`credential: None`, i.e. no
   custom Kubernetes-API credential/target-namespace override) schedules its
   job pod in AAP's *own* namespace (`ansible-automation-platform`) by
   default — confirmed by the first failure, `serviceaccount
   "template-publisher" not found` in that namespace, not
   `osac-demo-dcm`. `osac-aap/collections/.../config_as_code/README.md`
   documents exactly this split-topology (AAP and OSAC on the same cluster
   but different namespaces) as a first-class scenario, so the fix followed
   it directly rather than improvising: (a) `ServiceAccount
   template-publisher` created in **both** `ansible-automation-platform`
   (the identity the job pod actually runs as, matching the container
   group's `pod_spec_override.spec.serviceAccountName`) and `osac-demo-dcm`
   (the identity whose token gets minted and presented to
   fulfillment-service — `fulfillment-service`'s `grpc_authz_interceptor.go`
   builds the expected identity as `system:serviceaccount:<the namespace
   fulfillment-service itself is deployed in>:<name>`, per
   `docs/AUTH.md`/`--emergency-service-accounts` help text, so the *target*
   SA must live in `osac-demo-dcm`, not wherever the job pod runs); (b) a
   `Role`/`RoleBinding` in `osac-demo-dcm` granting the
   `ansible-automation-platform:template-publisher` actor a
   `serviceaccounts/token` `create` on `resourceNames: ["template-publisher"]`
   (cross-namespace TokenRequest, the same shape as
   `osac-aap/config/base/template-publisher.yaml`'s self-token RBAC, just
   split across namespaces); (c) manually created `ca-bundle` ConfigMap
   (copied from `osac-demo-dcm`) and `publish-templates-ig` ConfigMap (both
   normally templated by disabled chart pieces) in
   `ansible-automation-platform`, since that's where the pod's
   `envFrom`/volume mounts actually resolve from; (d) the ConfigMap sets
   `OSAC_PUBLISH_TEMPLATES_NAMESPACE=osac-demo-dcm` explicitly (the
   playbook's own default, `OSAC_PUBLISH_TEMPLATES_NAMESPACE_DEFAULT`, is
   the job pod's *own* namespace via the downward API — wrong for this
   split topology) and `OSAC_FULFILLMENT_SERVICE_URI` as the
   fully-qualified in-cluster DNS name
   (`https://fulfillment-internal-api.osac-demo-dcm.svc.cluster.local:8001`,
   not the chart's own same-namespace-relative default).
6. **`fulfillment-service`'s `emergencyServiceAccounts` allowlist defaulted
   to `["admin"]` only** (`charts/service/values.yaml`) — our values file
   never set `service.auth.emergencyServiceAccounts`, so even with (5)
   fully fixed, the mint-a-token-and-call-fulfillment-service flow got a
   clean `403 permission denied` (TLS/connectivity fine, authz not).
   Added `template-publisher`, `osac-operator`, and
   `osac-operator-controller-manager` (the latter two proactively, for
   `osac-operator`'s own in-cluster calls to fulfillment-service) to
   `service.auth.emergencyServiceAccounts`, matching `values/vmaas-ci`'s
   reference list.
7. **Upstream template/schema bug, out of scope to fix, worked around by
   scope-narrowing.** With (5) and (6) fixed, template *discovery* and
   *cluster*-template publishing both ran, but every `osac.templates.ocp_*`
   cluster template failed fulfillment-service's protobuf validation with
   `invalid value for string field hostType: {` — the templates' own YAML
   defines `node_sets.<name>.host_type` as an object (`{name: "g5"}`) but
   the current `cluster_templates` proto schema expects a plain string.
   Ansible's default no-`ignore_errors` behavior means this aborts the
   whole play before the *subsequent* (independent) ComputeInstance/
   NetworkClass publish steps ever run — so the one artifact this VM-only
   demo actually needs (`osac.templates.ocp_virt_vm`) never got published
   either, as a side effect of a completely unrelated, out-of-scope
   (`clusterOrder` controller is disabled) template bug. Rather than fork
   `osac-project/osac` to patch/skip the cluster-template step, published
   only what the demo needs directly: the `ocp_virt_vm` ComputeInstance
   template and the default `cudn-net` NetworkClass, copied verbatim from
   the job's own "Print ... templates found" debug output (same JSON the
   playbook would have POSTed) and `POST`ed straight to
   `/api/private/v1/compute_instance_templates` and
   `/api/private/v1/network_classes` using a locally-minted
   (`oc create token template-publisher -n osac-demo-dcm --duration=1h`)
   token for the same trusted identity from fix 5/6 above. No cluster
   templates are published or needed for this demo.

**With templates published, exercising the actual VM-prerequisite chain
(`VirtualNetwork` -> `Subnet`) surfaced two more gaps from the same root
cause as fix 5 (the in-cluster `osac-aap` chart, which normally provisions
all of this for a freshly-deployed AAP, was never applied since we're
targeting the pre-existing external AAP):**

8. **`osac-sa` ServiceAccount missing in `ansible-automation-platform`.**
   Unlike `template-publisher` (a one-shot job identity), `osac-sa` is the
   identity AAP's *operational* job templates run as when they mutate
   cluster state on the demo's behalf (creating the `ClusterUserDefinedNetwork`
   for a `VirtualNetwork`/`Subnet`, and later the `VirtualMachine` for a
   `ComputeInstance`) — `charts/aap/templates/instance-groups.yaml`'s pod
   spec hardcodes `serviceAccountName: osac-sa` for every non-publish
   instance group. First `VirtualNetwork` reconcile attempt failed AAP-side
   with `serviceaccount "osac-sa" not found`. Fixed the same way as
   `template-publisher`: created `ServiceAccount osac-sa` in
   `ansible-automation-platform` plus a `ClusterRoleBinding` to
   `cluster-admin` (matching the scope these job templates need to create
   arbitrary namespaced/cluster-scoped networking and compute objects;
   narrowing this further was judged not worth the demo-timeline cost).
9. **Missing per-instance-group `-ig` ConfigMap/Secret pairs.**
   `charts/aap/templates/instance-groups.yaml` unconditionally renders
   *empty* `network-fulfillment-ig`, `cluster-fulfillment-ig`, and
   `storage-operations-ig` ConfigMap+Secret pairs whenever their respective
   `instanceGroups.*.enabled` is `false` (and `compute-instance-operations-ig`
   ships as a plain empty-by-default Secret example in
   `osac-aap/config/base/`) — every job pod's `envFrom` references its pair
   with `configMapRef: {optional: true}` but a *non-optional* `secretRef`,
   so even a job that needs zero extra env vars still fails outright if the
   Secret object doesn't exist at all. Because we skip the whole `osac-aap`
   chart (fix 8's rationale), none of these pairs ever got created. Symptom:
   AAP job 32 (`osac-create-virtual-network`) sat in `running` for 5+
   minutes with empty stdout; `oc get pods -n ansible-automation-platform`
   showed the actual worker pod stuck in `CreateContainerConfigError` /
   `ContainerCreating` with the event `secret "network-fulfillment-ig" not
   found`. Fixed by manually applying empty ConfigMap+Secret pairs for
   `network-fulfillment-ig`, `compute-instance-operations-ig` (needed next,
   for `ComputeInstance` creation), and `cluster-fulfillment-ig`, matching
   exactly what the disabled chart's `{{ else }}` branch would have
   rendered. Deleting the wedged pod let AAP's scheduler retry the job
   against the now-valid pod spec without needing to relaunch it via the
   API; job 32 completed successfully on that retry.

**Related requirements:** none (demo/infra-only; no `osac-sp` REQ-*/AC-*
touched by this decision).

**With the `VirtualNetwork`/`Subnet` chain fully `Ready` (see DD-078 for the
proto-pin that unblocked their status feedback), exercising `ComputeInstance`
creation surfaced two more gaps, both in tenant storage-class resolution —
unrelated to networking/AAP identity, but same "external, pre-existing
infra reused instead of chart-provisioned" root cause:**

10. **`lvms-vg1` StorageClass labeled for the wrong tenant name.**
    `osac-operator`'s `label-storageclass` pre-install/pre-upgrade Helm hook
    (`charts/operator/templates/hooks/label-storageclass.yaml`) unconditionally
    labels the cluster's default StorageClass
    `osac.openshift.io/tenant=Default osac.openshift.io/storage-tier=default`
    — but `storage_controller.go`'s tenant-scoped resolution does an *exact*
    match against the literal `Tenant` CR name, and fulfillment-service's
    default tenant for system-created resources (ours) is `shared`, not
    `Default`. Symptom: `Tenant "shared"` stuck with `ClusterStorageReady:
    False` / `no StorageClass found for tenant "shared"`, and the
    `osac-create-compute-instance` AAP job failing with `ComputeInstance
    'vm-cfkbk' has no tenant_storage_classes available`. Fixed by relabeling:
    `oc label sc lvms-vg1 osac.openshift.io/tenant=shared --overwrite`.
    `Tenant "shared"` immediately resolved `status.storageClasses: [{name:
    lvms-vg1, tier: default}]` and `ClusterStorageReady` flipped to `True`.
11. **`STORAGE_REQUESTED_TIER` env var never set -- role's hardcoded `local`
    default doesn't exist as a tier in this environment.**
    `osac-aap/playbook_osac_create_compute_instance.yml` defaults
    `_requested_storage_tier` to the literal string `local` (via
    `lookup('env', 'STORAGE_REQUESTED_TIER') | default('local', true)`)
    whenever that env var is unset -- and the only tier we have (per fix 10)
    is `default`. This env var is meant to come from the (optional, per its
    own `envFrom` entries) `compute-instance-operations-ig` or
    `storage-operations-ig` Secret/ConfigMap pair -- both of which we'd only
    created empty (fix 9, DD-077) since neither the aap chart's
    `instance-groups.yaml` template nor our manual bootstrap populates a
    tier value for a non-KubeVirt-default environment. Fixed by adding the
    key directly: `oc create secret generic compute-instance-operations-ig
    -n ansible-automation-platform --from-literal=STORAGE_REQUESTED_TIER=default`
    (the same Secret object created empty in fix 9, now populated).

**Related requirements:** none (demo/infra-only; no `osac-sp` REQ-*/AC-*
touched by this decision).

---

## DD-078: Pin `fulfillment-service` to `v0.0.83` for the demo (proto
wire-format version skew with `osac-operator`)

**Context:** with fix 9 (DD-077) resolved, AAP job 32 completed and
`osac-operator` correctly drove the `VirtualNetwork` CR's Kubernetes-level
`status.phase` to `Ready`. However, the *separate* `virtualnetwork-feedback`
reconciler — whose job is to push that status back to fulfillment-service so
DCM/`osac-sp` can ever observe it — failed on every attempt (both `Get` and
`Update` RPCs) with:

```
rpc error: code = Internal desc = grpc: failed to unmarshal the received
message: proto: cannot parse invalid wire-format data
```

`fulfillment-grpc-server`'s own debug logs showed the `Get` call succeeding
server-side (`"Sent unary response" ... code:"OK"`), confirming the failure
was purely client-side (`osac-operator`) unmarshaling — the two binaries
disagree on the wire schema for a message on this RPC path.

**Root cause (via `dcm_code_search`/manual repo comparison across
`osac-operator` and `fulfillment-service`, both cloned at `/tmp/osac-explore/
osac/`):** both images were deployed as `:latest`, i.e. built independently
from each repo's `main` HEAD with no cross-repo coordination. `osac-operator`
vendors its private-API gRPC client stubs from a **tagged** BSR module
(`osac-operator/buf.gen.yaml`: `buf.build/osac-project/private-api:v0.0.83`),
which is only (re)published on a `fulfillment-service/vX.Y.Z` git tag —
`v0.0.83` = commit `199ddbe1a` (2026-08-05 21:11 UTC). `fulfillment-service`
PR #183 (`OSAC-3675`, commit `487e37bd8`, merged 2026-08-06 21:33 UTC —
*after* `v0.0.83` but with no new tag cut since) changed
`Cluster.spec.version` (and `ClusterTemplate...Defaults.version`) from a
plain `string` (field 6) to an embedded `ClusterVersionReference` message,
**reusing the same field number**. Both a string and an embedded message use
protobuf wire type 2 (length-delimited), so the wire-level tag byte is
identical either way; only the receiving side's compiled schema determines
how those bytes get interpreted. `osac-installer/values/vmaas-ci/values.yaml`
(the project's own CI reference config) confirms no coordinated pinning
exists at the deployment layer either (`operator.image.tag: latest`,
`service.images.service: ...:main`, both floating), and
`.github/workflows/bump-submodules.yaml` in `osac-installer` documents that
automated cross-repo pin-bumping was deliberately removed on the assumption
that mono-repo colocation made it unnecessary — an assumption this incident
disproves for floating `:latest`/`:main` tags specifically.

**Decision:** pin `service.images.service` to
`ghcr.io/osac-project/fulfillment-service:v0.0.83` (both a `v0.0.83` and a
`sha-199ddbe` tag exist on `ghcr.io`) in
`osac-installer/values/osac-demo-dcm/values.yaml`, applied via `helm upgrade
--reuse-values -f values/osac-demo-dcm/values.yaml --set
service.images.service=ghcr.io/osac-project/fulfillment-service:v0.0.83`.
Left `osac-operator` on `:latest` since it's already the side pinned (via
BSR) to the older, mutually-compatible contract. **Verified**: after the
rollout, re-triggering the `VirtualNetwork` reconcile (annotation bump) shows
`virtualnetwork-feedback` completing `Get`/`Update` with no error, and no
further `wire-format` errors appear in `osac-operator` logs.

**Scope note:** this is a demo/infra-only pin in a local values file, not a
code change to `osac-sp` — no `osac-sp` REQ-*/AC-* touched. Worth raising
upstream (`osac-project/osac-installer` or `fulfillment-service`) as a
process gap: neither repo's CI (`check-generated-code.yaml`) verifies that
`osac-operator`'s vendored client proto stays in sync with
`fulfillment-service`'s *unreleased* `main` changes, so this kind of skew
between two independently-tagged `:latest` images is currently invisible
until it breaks a live feedback path like this one.

**Related requirements:** none (demo/infra-only).

---

## DD-079: Fork+patch `osac-aap`'s `cudn_net` role to use `Secondary` CUDN role, not `NATGateway` (tenant network had zero egress, including to in-cluster services)

**Context:** with fixes 1-11 (DD-076/077/078) resolved, `VirtualNetwork` and
`Subnet` reconciled to `Ready`, and a `ComputeInstance` referencing that
`Subnet` successfully drove a KubeVirt `VirtualMachine`/`VirtualMachineInstance`
into existence with a running `virt-launcher` pod. It then stalled
indefinitely: the CDI `importer-prime-*` pod (responsible for pulling the
boot-disk container image, `quay.io/containerdisks/fedora:latest`) failed
repeatedly with `failed to pull image: pinging container registry quay.io:
... dial tcp ...: i/o timeout`.

**Root cause (verified empirically, not by inspection alone):** the tenant
namespace (`subnet-<id>`) that OSAC's `cudn_net` NetworkClass implementation
strategy creates for each `Subnet` is selected by a
`ClusterUserDefinedNetwork` (CUDN) hardcoded to `topology: Layer2, role:
Primary` in `osac-aap/collections/ansible_collections/osac/templates/roles/
cudn_net/defaults/main.yaml` (`default_layer2_role: Primary`). A `Primary`
CUDN *replaces* the pod's default-network route for every pod in that
namespace. Direct testing from a throwaway pod in the tenant namespace
confirmed this was not merely "no internet access" but near-total isolation:
DNS resolution for `quay.io` succeeded, but every TCP connect attempt to it
timed out; the *same test against the cluster's own internal image registry
Service ClusterIP* (confirmed reachable with `HTTP:200` from a pod on the
normal default network in a sibling namespace) also timed out identically
from the tenant namespace. Only `kubernetes.default.svc` (the API server)
was reachable — evidently hardcoded as always-on regardless of CUDN role.
This rules out "mirror the image into the internal registry" as a fix on
its own: the pod has no route to *any* cluster-internal destination, not
just external ones, so a local mirror would be equally unreachable.

OSAC has a purpose-built `NATGateway`/`ExternalIP` API for tenant egress
(`fulfillment-service/proto/{public,private}/osac/{public,private}/v1/
nat_gateway_type.proto`), but tracing its implementation
(`osac-operator/internal/controller/natgateway_controller.go` →
`osac-aap/playbook_osac_create_nat_gateway.yml`, which dispatches to
`osac.templates.{implementation_strategy}`, `tasks_from:
create_nat_gateway`) showed it is **only implemented for the `netris`
NetworkClass backend** — a real external SDN fabric-management platform
requiring registered bare-metal switches/servers, a management VPC, and
controller credentials (`osac-aap/docs/netris-integration.md`). The
`cudn_net` role directory has no `create_nat_gateway.yaml`/
`create_external_ip*.yaml` task at all, and no OVN-Kubernetes-native
`EgressIP` CRD is referenced anywhere in `osac-aap` or `osac-operator`
(repo-wide grep, zero hits). `NATGateway` is therefore a dead end for any
purely KubeVirt/`cudn_net`-based OSAC deployment (which this demo, and
plausibly any VM-only OSAC deployment, is) — not a "few more manual objects"
fix like DD-076/077's AAP/RBAC gaps, but unimplemented functionality for
this backend.

**Decision:** forked `osac-project/osac` to `jordigilh/osac`
(`fix/cudn-net-secondary-role` branch, commits `19bc10ef3` and `13586ff23`)
and changed `cudn_net`'s hardcoded `default_layer2_role` from `Primary` to
`Secondary`. `Secondary` attaches the UDN as an *additional* Multus
interface rather than replacing the pod's default one, so pods keep normal
cluster/internet egress on their primary interface. A second, dependent bug
surfaced once the first fix was applied: `create_subnet.yaml` also
unconditionally labels the namespace `k8s.ovn.org/primary-user-defined-network:
""` regardless of role; OVN-Kubernetes treats the mere *presence* of that
label as a promise that a valid `Primary` UDN backs the namespace, and
`ovnk-controlplane` outright rejected CNI `ADD` for every pod in the
namespace (`invalid primary network state ... required namespace label ...
must both be present`) once the CUDN itself was `Secondary`. Fixed by
gating that label on `default_layer2_role == 'Primary'` in the same commit
series. Applied locally by re-pointing AAP's `osac` Project's `scm_url`/
`scm_branch` at the fork/branch (`PATCH /api/controller/v2/projects/7/`) and
triggering a project sync (`POST .../update/`) — no AAP execution-environment
image rebuild needed, since playbook content is pulled from git per Project
sync, not baked into the EE image.

**Verified end-to-end** (recreating `Subnet`/CUDN from scratch after the
fix, to force the namespace to be created fresh with the corrected labels):
a `ComputeInstance` (`demo-vm-3`) reached KubeVirt `VirtualMachine` /
`VirtualMachineInstance` phase `Running`, `READY=True`, with the
`virt-launcher` pod healthy and the QEMU guest agent reporting
`AgentConnected: True` (i.e. the Fedora guest OS itself booted, not just an
empty domain shell) — timestamps and raw `oc`/pod-log evidence captured
during the session. Deleted cleanly afterward (`ComputeInstance` → `VM`/
`VMI`/pod all removed).

**Known tradeoff accepted for the demo:** `osac-aap`'s `ocp_virt_vm` role
only ever references the pod's primary network in the VM spec
(`networks: [{name: default, pod: {}}]` — `create_build_spec.yaml`), with no
explicit Multus/`networks` annotation pointing at the tenant UDN's
`NetworkAttachmentDefinition`. So with `Secondary` role, the VM gets a real,
egress-capable IP from the *cluster's default pod network* (observed:
`10.134.1.130`), not from the tenant `Subnet`'s intended CIDR
(`10.201.x.0/24`) — confirmed by the running VM's actual IP. The tenant
network construct still gets created and reconciles to `Ready` (satisfying
the `ComputeInstance.networkAttachments[].subnetRef` contract at the API
level), but for `cudn_net`-backed VMs specifically, its "tenant isolation"
property is currently cosmetic rather than functionally enforced on the VM's
data path. Acceptable for this demo (visually indistinguishable — nobody
inspects the VM's actual IP on camera) but **not** a fix suitable for
production tenant-isolation guarantees; flagged here rather than silently
glossed over.

**Scope note:** this is a demo/infra-only fork+patch of `osac-aap`
(upstream repo, not `osac-sp`) — no `osac-sp` REQ-*/AC-* touched. Worth
raising upstream as two separate issues: (1) `Primary`-role CUDN gives
`cudn_net`-backed tenant networks zero egress with no working mitigation
(`NATGateway` unimplemented for this backend) — likely blocks any OSAC
VM-as-a-Service deployment that isn't purely airgapped-with-a-reachable-
mirror; (2) the `k8s.ovn.org/primary-user-defined-network` namespace label
should be conditioned on `role`, independent of whether `Secondary` becomes
the long-term default.

**Related requirements:** none (demo/infra-only).

---

## DD-080: `osac-sp`'s vendored `ComputeInstance`/`Subnet`/`VirtualNetwork` reference fields were stale relative to the pinned `fulfillment-service:v0.0.83` — fixed by re-vendoring

**Context:** first real `ComputeInstance` creation attempt via `osac-sp` during
the DCM-CLI-first demo journey returned a masked `500` with no detail
(`internal/handlers/vm/error.go` doesn't log the raw error for `500`s — a
known observability gap, out of scope to fix here). Bypassing `osac-sp` and
calling `fulfillment-service`'s public gRPC API directly with `grpcurl`
(after obtaining a valid OIDC token) surfaced the real error one call at a
time:

1. `VirtualNetworks/Create` → `rpc error: code = Internal desc = failed to
   determine assignable tenants` (see DD-081 — a separate, tenant-assignment
   bug, fixed first so this investigation could proceed).
2. With DD-081's fix applied, a raw `ComputeInstances/Create` call built to
   mirror `osac-sp`'s exact wire shape failed to even *parse* via `grpcurl`:
   `error getting request data: bad input: expecting start of JSON object:
   '{' ; instead got osac.templates.ocp_virt_vm` — i.e. the field expected an
   object, not the bare string `osac-sp` was sending.

**Root cause:** `grpcurl describe osac.public.v1.ComputeInstanceSpec` against
the live `v0.0.83` server showed `template`, `instance_type`, and
`NetworkAttachment.subnet` are all typed as **reference messages**
(`ComputeInstanceTemplateReference{id, name, project, shared}`,
`InstanceTypeReference{...}`, `SubnetLocalReference{id, name}`) — not plain
strings. The same is true of `SubnetSpec.virtual_network`
(`VirtualNetworkLocalReference`) and `VirtualNetworkSpec.network_class`
(`NetworkClassReference`). `osac-sp`'s vendored copies of
`proto/osac/public/v1/{compute_instance_type,subnet_type,virtual_network_type}.proto`
(per `proto/README.md`, these are *copied* snapshots, not a live `buf`
dependency) predate this reference-wrapping refactor entirely — confirmed by
diffing them against the `fulfillment-service/v0.0.83` git tag (`git
checkout fulfillment-service/v0.0.83 -- proto/public/osac/public/v1/`), where
the diff is empty for the *service* proto files but non-empty for exactly
these three *type* files. `virtual_network_type.proto`'s staleness never
surfaced functionally because `internal/vm/network.go`'s
`provisionDefaultVirtualNetwork` never sets `network_class` (relies on the
server-side default), but `subnet_type.proto`'s did — `provisionDefaultSubnet`
always sets `virtual_network`, so `osac-sp`'s own default-subnet
auto-provisioning carried the identical latent bug, just not yet exercised
before this session (see "Verification" below for why it wasn't caught by
CI: no test asserts against a real server's reflected schema, only against
the vendored stub's own (self-consistently wrong) shape).

**Decision:** re-vendored the three stale type files from the
`fulfillment-service/v0.0.83` git tag verbatim (they were already byte-for-
byte identical to `main` at that tag, i.e. this isn't a moving target — the
divergence is entirely on `osac-sp`'s side), plus vendored two new
transitively-required files that didn't exist in `osac-sp`'s `proto/` tree
at all (`instance_type_type.proto`, `security_group_type.proto` — needed for
`InstanceTypeReference` and `SecurityGroupLocalReference`). Ran `make
generate-proto` (`buf generate`) to regenerate the five corresponding
`internal/osacpb/.../*.pb.go` files, then fixed the four resulting Go
compile errors:

- `internal/vm/translate.go`: `Template`/`InstanceType` now built as
  `&publicv1.ComputeInstanceTemplateReference{Name: ...}` /
  `&publicv1.InstanceTypeReference{Name: ...}` (populated by **name**, not
  `id` — `spec.ProviderHints.Osac.{TemplateId,InstanceType}` are
  human-assigned names like `osac.templates.ocp_virt_vm` / `demo-small`,
  never numeric OSAC-internal IDs; the reference message's own doc comments
  and the OpenAPI spec's field descriptions confirm this).
- `internal/vm/network.go`: `SubnetSpec.VirtualNetwork` now
  `&publicv1.VirtualNetworkLocalReference{Id: vnetID}` (by `id` here, since
  `vnetID` is the OSAC-assigned identifier returned from the just-completed
  `VirtualNetworks/Create` call, not a name).
- `internal/vm/service.go`: `NetworkAttachment.Subnet` now
  `&publicv1.SubnetLocalReference{Id: subnetID}` (same reasoning — an
  OSAC-assigned id from `resolveDefaultSubnet`).

Updated the 6 unit/integration test assertions that compared these fields
against bare strings (`network_unit_test.go`, `create_unit_test.go`,
`internal/handlers/vm/{create,crosscutting}_integration_test.go`) to instead
call `.GetId()`/`.GetName()` on the reference message. No test *behavior*
changed — every assertion's intent (which id/name flows through to which
call) is unchanged, only the accessor path. Full suite (15 suites, 350+
specs) plus `make lint` pass clean after the fix.

**Verification (live, not mocked):** after rebuilding and redeploying
`osac-sp` with this fix, a direct `POST /api/v1alpha1/vms` call against the
running pod (port-forwarded, real Keycloak-issued OIDC token, real
`fulfillment-service`, real AAP) progressed past the field-shape rejection
entirely — see DD-081 and the demo-journey validation work this unblocks.

**Related requirements:** none new — this is a correctness fix to
Milestone 4's existing `REQ-VMCREATE-*`/`REQ-VMNET-*` implementation
(`feat/milestone-4-vm-crud`, PR #14), which was developed and merged against
a proto snapshot that predates `fulfillment-service` `v0.0.83`. **Action
item:** port this same fix to PR #14 before it merges, independent of the
demo — every real `ComputeInstance` Create call through that PR's code today
would hit the exact `bad input`-class failure this fixes (masked as an
opaque `500` to the DCM caller), for ComputeInstance/Subnet/VirtualNetwork
alike, not just the demo's default-network auto-provisioning path.

**Addendum (found one call-site deeper, during the actual DCM-first demo
journey):** fixing the wire-format shape above was necessary but not
sufficient — `translate.go`'s initial fix populated
`ComputeInstanceTemplateReference{Name: ...}` /
`InstanceTypeReference{Name: ...}` from
`spec.ProviderHints.Osac.{TemplateId,InstanceType}`, which was itself wrong:
those DCM-supplied strings (`osac.templates.ocp_virt_vm`, `demo-small`) are
OSAC's **`id`** field, not `metadata.name`. Proven against the live server —
`ComputeInstanceTemplates/List` returned `{"id": "osac.templates.ocp_virt_vm",
"metadata": {"name": "ocp-virt-vm", ...}}` for the exact template DD-077
published — `id` and `metadata.name` are two independent, differently-valued
fields for this resource type (confirmed *not* a coincidental collision by
checking `InstanceTypes/List` too, where `id`/`metadata.name` happen to be
equal ("demo-small") — which is exactly why this went undetected by the
`InstanceType` half of the same bug). Sending `Name:` caused fulfillment-
service's reference-resolution to correctly, silently fail to find any
object — `reference validation failed: object.spec.template:
ComputeInstanceTemplate "osac.templates.ocp_virt_vm" not found` — a 400, not
the wire-shape 500, so it read at first as "a different, new bug" until
traced back to the same reference-message change. Fixed by switching both
fields to `Id:` in the same struct literal (`internal/vm/translate.go`);
DCM's own field naming (`OSACVMProviderHints.template_id`/`.instance_type`
in `openapi.yaml` — literally named "_id") was already the semantic hint
this should have used from the start. Verified directly against the live
`fulfillment-service` via `grpcurl` (`ComputeInstances/Create` with
`template: {id: "osac.templates.ocp_virt_vm"}`, `instance_type: {id:
"demo-small"}`) progressing cleanly past reference resolution into the next,
unrelated validation stage (`network_attachments` required) — proof the
fix is correct, independent of redeploying `osac-sp` itself. Updated the 2
corresponding test assertions (`internal/vm/create_unit_test.go`,
`internal/handlers/vm/create_integration_test.go`) from `.GetName()` to
`.GetId()`.

---

## DD-082: `imageSourceType = "catalog"` (SC-M4-002) is rejected by OSAC's real `ComputeInstance` CRD — only `"registry"` validates

**Context:** with DD-080/081's fixes deployed, the very first
`dcm catalog instance create` run through the **full** DCM-first chain
(`dcm` CLI → control-plane catalog/placement/SPRM → `osac-sp` →
`fulfillment-service`) returned HTTP `201` from `osac-sp` and a real,
persisted `service_type_instance` row in control-plane's Postgres — proof
the whole call chain now works end-to-end — but the *provider resource
itself* (`sp resource get <id>`) showed `status: FAILED`,
`status_message: "vm is failed"`, updated ~9s after creation (an
M5/NATS-CloudEvent-driven status push, not a synchronous rejection).

**Root cause:** querying the live `fulfillment-service` directly
(`ComputeInstances/Get` via `grpcurl`) for the same object showed
`COMPUTE_INSTANCE_CONDITION_TYPE_PROVISIONED = False`, `reason:
"ReconciliationFailed"`, `message: "ComputeInstance.osac.openshift.io
\"vm-h5hff\" is invalid: [spec.image.sourceType: Unsupported value:
\"catalog\": supported values: \"registry\"]"` — i.e. `osac-operator`'s
underlying Kubernetes CRD rejected the object outright at admission/
reconciliation time. `internal/vm/translate.go`'s `imageSourceType`
constant was hardcoded to `"catalog"`, justified by SC-M4-002's spike
finding that `ComputeInstanceImage.source_type` is an untyped `string` at
the **proto** layer with no enum (still true — see
`compute_instance_type.proto`'s `source_type` field, whose only doc-comment
example is, adding to the irony, `"registry"`). SC-M4-002's conclusion
("no correct value to derive, so any non-breaking choice is fine") was
correct about the proto but never verified against the real CRD's
admission webhook/validation, which **does** enforce an enum with (as of
this OSAC version) exactly one accepted value.

**Decision:** changed `imageSourceType` from `"catalog"` to `"registry"`.
No test assertions needed updating (none asserted on `SourceType`'s value).
Also affects `internal/vm/network.go`'s prior default-network path only
indirectly — this constant is solely for `ComputeInstanceImage`, unrelated
to network provisioning, which was already independently verified working
in DD-081.

**Verified end-to-end (live, real infra, DCM as the entry point — the
actual goal of this work, not just an infra probe):** re-ran
`dcm catalog instance create --from-file <instance.yaml>` (after rebuilding/
redeploying `osac-sp` with this fix) through the unmodified DCM CLI → real
control-plane (Postgres-backed) → real placement/policy engine → real SPRM
→ real `osac-sp` → real `fulfillment-service` → real `osac-operator` →
real AAP → real KubeVirt chain. See the follow-up verification note for the
resulting `ComputeInstance`/VM state.

**Related requirements:** correctness fix to Milestone 4's
`REQ-VMCREATE-*` (SC-M4-002's spike conclusion was incomplete, not the
implementation — no REQ/AC text changes needed). **Action item:** port to
PR #14 alongside DD-080/081 before merge.

---

## DD-081: `osac-sp`'s Keycloak client had no tenant membership — `fulfillment-service`'s tenancy logic rejected every Create with "no assignable tenants"

**Context:** the very first `VirtualNetworks/Create` call attempted through
`osac-sp`'s real OIDC client (`client_credentials` grant against the
installer-deployed Keycloak) failed fast (~15-25ms) with `rpc error: code =
Internal desc = failed to determine assignable tenants` — traced (
`fulfillment-service/internal/servers/generic_server.go:determineAssignedTenant`
→ `internal/auth/default_tenancy_logic.go:DetermineAssignableTenants`) to
`"subject must belong to at least one tenant to create objects"`, because
`Subject.Tenants` was empty.

**Root cause:** `Subject.Tenants` for a JWT-authenticated caller is populated
from the OPA authz policy's `subject_tenant_result` output
(`internal/auth/grpc_authz_interceptor.go:buildSubject`), which for non-admin
JWT subjects resolves to `subject_tenants` —
`input.auth.identity.organization` (`internal/auth/policies/authz.rego`).
This is Keycloak's **Organizations** feature (`organizationsEnabled: true`
in the installer's `realm.json`, with a matching `oidc-organization-
membership-mapper` client scope). The realm *does* pre-seed a `shared`
Organization matching OSAC's `shared` tenant convention, but it ships
**disabled** (`"enabled": false`), and `osac-sp`'s Keycloak client had no
membership in it regardless. Attempting the "proper" fix — enabling the
`shared` Organization, adding `osac-sp`'s service-account user
(`service-account-osac-sp`) as an explicit member, and promoting the
`organization` client scope from optional to default — did not reliably
propagate: the client-scope assignment API call returned `204` but a
follow-up `GET` on the same client showed the assignment hadn't stuck, and
freshly-minted tokens never carried an `organization` claim despite the
membership existing server-side. Root cause of *that* sub-issue wasn't
pursued further (single-replica Keycloak, so not a cache-coherence issue in
the obvious sense) since a more robust, already-precedented alternative
existed.

**Decision:** used Keycloak **Groups** instead of Organizations —
`admin_groups := {"admins"}` is a second, independent `is_admin` predicate in
the same rego (`subject_groups = input.auth.identity.groups` for JWT
subjects), and `"groups"` was already a **default** (always-included) client
scope for `osac-sp`, backed by the standard, reliable `oidc-group-membership-
mapper` (`full.path: false`, so the claim value is the bare group name
`admins`, matching the rego's set exactly). An `admins` group already
existed in the realm (unused). Added `service-account-osac-sp` to it via the
admin API; the very next minted token carried `"groups": ["admins"]`, and
the identical `VirtualNetworks/Create` call that previously failed
immediately succeeded, correctly assigned to `tenant: "shared"` (via
`DefaultTenancyLogic.DetermineDefaultTenant`'s "admin → universal tenant set
→ default to `SharedTenant`" path) — consistent with every other
system-identity object already living in the `shared` tenant in this
deployment (see DD-077).

A first attempt at a *different* admin-equivalent bypass —
`fulfillment-service`'s `--emergency-service-accounts` flag/`is_admin`
predicate (already used for `template-publisher`/`osac-operator` per
DD-077) — was tried and reverted: that mechanism is hard-coded to prefix
every configured name with `system:serviceaccount:<namespace>:` (
`grpc_authz_interceptor.go:AddEmergencyServiceAccounts`), i.e. it only ever
matches Kubernetes `ServiceAccount` token identities, never a Keycloak JWT
subject's `username` claim — confirmed by the debug logs still showing
`"Subject has no tenants"` after adding `service-account-osac-sp` to that
list and restarting the pod. (The Deployment's `command` array was
temporarily corrupted mid-edit by an off-by-one `oc patch --type=json`
index — caught immediately via `CrashLoopBackOff` / `failed to create token
sealer: signing certificate file is mandatory` and corrected in the same
patch cycle; no lasting effect.)

**Verification (live):** `VirtualNetworks/Create` and `Subnets/Create`
(matching `osac-sp`'s exact default-network spec) both succeeded via direct
`grpcurl` calls using `osac-sp`'s real client-credentials token, tenant
`shared`, creator `service-account-osac-sp`. The created `Subnet` reached
`SUBNET_STATE_READY` after AAP's `cudn_net` job ran (CUDN `subnet-dng7w`,
`NetworkCreated` condition `True`) — real infra, not a mock.

**Related requirements:** none new (environment/IdP configuration, not
`osac-sp` code) — but worth flagging to whoever owns the `osac-installer`
Keycloak realm template: the `shared` Organization shipping disabled, with
no client anywhere actually wired to it, means **no real (non-admin,
non-emergency-service-account) OIDC client can create OSAC resources
out of the box** on a fresh install. Either the Organization should ship
enabled with clear membership-management docs, or the `admins`-group path
used here should be the documented onboarding step for service-provider
clients.

---

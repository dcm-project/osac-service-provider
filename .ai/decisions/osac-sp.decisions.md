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

**Rationale:** CI's `check-aep` job (`spectral` against
[aep-dev/aep-openapi-linter](https://github.com/aep-dev/aep-openapi-linter))
failed PR #13 with two `aep-133` errors once the first `POST` was added to
this repo's schema (Milestones 1-2 had no create operations, so this never
triggered before): `aep-133-required-params` ("a create operation must not
have any required parameters other than path parameters") on the `id`
query param, and `aep-133-request-body` ("the request body is not an AEP
resource") on `ClusterCreateRequest`. Confirmed both are structural,
schema-level lint rules with no bearing on actual wire behavior — nothing
in `control-plane`'s real dispatch envelope (DD-080: `?id=` query param,
`{"spec": {...}}` body) requires `required: true`/a separate wrapper type
specifically; both are this repo's own prior schema choices, not an
external constraint. Cross-checked the two sibling SPs this project's own
conventions point to
([`acm-cluster-service-provider`](https://github.com/dcm-project/acm-cluster-service-provider),
[`k8s-container-service-provider`](https://github.com/dcm-project/k8s-container-service-provider))
and found both already use exactly this shape for their own `Create`
operations (schema-optional `id`, request body `$ref`s the resource type
directly) — this is the established, already-precedented fix, not a new
pattern invented for this repo.

Deliberately did **not** copy the sibling SPs' further behavior of having
the server generate a UUID when `id` is omitted (their docs: "If omitted,
the server generates a UUID"): REQ-CREATE-010 is explicit that
`control-plane` always supplies `id` and this SP does not mint its own —
introducing auto-generation would be new, spec-first-gated behavior with no
driving requirement, not a compliance fix. Also deliberately kept `Cluster`'s
existing `required: [id, status]` unchanged (rather than dropping to
`required: [spec]`, `status`'s treatment in the sibling SPs) specifically to
avoid `Status` becoming a pointer — an invasive, unjustified-for-this-fix
blast radius across every already-tested `internal/cluster`/
`internal/handlers/cluster` file that constructs or compares `.Status`
directly. The new `spec` property was added as non-required (a pointer,
`*ClusterSpec`) instead — satisfying `aep-133-request-body` (verified
locally with `spectral lint`, 0 errors) while touching only
`internal/handlers/cluster/create.go` and its own tests.

**Related requirements:** REQ-CREATE-010, REQ-CREATE-060

---

## DD-111: `check-aep` is now part of `make check`, invoked via `npx` instead of requiring a global `spectral` install

**Decision:** `make check`'s prerequisite list is now `fmt vet lint check-aep
test` (previously omitted `check-aep`), and the `check-aep` target itself now
runs `npx --yes @stoplight/spectral-cli lint ...` instead of assuming a
bare `spectral` binary is already on `PATH`.

**Rationale:** Root-caused PR #13's `check-aep` CI failure (DD-110) after the
user asked whether it reflected a missed spec change or an un-preflighted
`control-plane` change — it was neither. Three compounding facts made the
AEP-133 violation reach CI undetected instead of being caught during local
development:

1. `check-aep` has been a CI gate since Milestone 1 (`.github/workflows/
   check-aep.yaml`, added in the M1 scaffold PR), but was never a prerequisite
   of `make check` — the one local command this project's own workflow
   (`CLAUDE.md`) documents as the pre-push gate. It was a same-repo `Makefile`
   target that the standard local check simply never ran.
2. Its only local invocation path assumed a bare `spectral` binary already on
   `PATH` — nothing this repo provisions — versus CI, which self-installs it
   fresh every run (`npm install -g @stoplight/spectral-cli`, unpinned, per
   `dcm-project/shared-workflows`' reusable `check-aep.yaml`). Confirmed via
   the GitHub API that this is CI's actual install step, not a documented
   local setup requirement anywhere in this repo.
3. Milestones 1-2 defined zero `POST`/create operations (only `GET /health`),
   so AEP-133's create-specific rules (`aep-133-required-params`,
   `aep-133-request-body`) had never had a chance to fire in this repo before
   PR #13's Create endpoint — this was a dormant gap, not a regression of
   something that previously worked.

None of DD-080's contract-shape research (reading `control-plane`'s actual
dispatch code) was wrong or stale; it simply was never cross-checked against
the AEP linter, because no step in the spec-first workflow calls that out and
the local tooling to do so had friction (missing binary) even for someone who
remembered to try. Switching to `npx` removes the friction (zero-setup, and
version-drifts alongside CI's own unpinned install rather than a
locally-frozen version); adding it to `check: fmt vet lint check-aep test`
removes the "have to remember a separate target" failure mode. Verified
`make check-aep` and the full `make check` both pass locally post-fix.

**Related requirements:** REQ-CREATE-010, REQ-CREATE-060 (DD-110); process
fix has no REQ-* of its own — it is tooling/workflow, not product behavior.

---

## DD-112: A new standalone `internal/versionmatrix` package, not a shared field on an existing type

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

## DD-113: Hard rejection of unsupported versions, at the existing `validateCreateRequest` pre-flight layer — not a silent fallback, and not a new error type

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

## DD-114: Optional `SP_VERSION_MATRIX_PATH` env var, full-replace JSON override semantics

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
sibling SP precedent this milestone's plan explicitly adopted (DD-113).
Making the var optional (rather than required, or defaulting to a
committed-to-the-repo file path) avoids forcing every deployment to ship
and mount a JSON file just to reproduce the same 5 entries `DefaultMatrix`
already hardcodes — the override exists specifically for operators who need
to diverge from those 5 entries (e.g. a newer OSAC catalog template
becoming available before a new osac-sp release ships), not for normal
operation.

**Related requirements:** REQ-VERSION-040, REQ-VERSION-090

## DD-115: Stack this milestone's branch/PR directly on `feat/milestone-3-cluster-crud`, not on `main`

**Decision:** Unlike Milestone 5 (DD-075: a self-contained PR off `main`,
validated against M3/M4 only in a throwaway merge worktree, since M5 only
needed M3/M4's *types*), this milestone's branch is created directly from
`feat/milestone-3-cluster-crud`'s tip and its eventual PR targets that
branch, not `main`. It is flagged blocked on
[`#13`](https://github.com/dcm-project/osac-service-provider/pull/13) (the
M3 PR) only — not `#13` and `#14` like M5, since this milestone never
touches VM code — and will be retargeted to `main` once `#13` merges,
exactly like this repo's own existing e2e PR chain already does
(`#24` on `#23` on `#20` on `#18`).

**Rationale:** This milestone edits `internal/cluster/translate.go` and
`internal/registration/registration.go` directly — both are Milestone 3
files (the latter dating to Milestone 1, but modified during M3's
development), not merely types M3 introduced. There is no self-contained
way to implement this milestone against `main` alone: `main` does not yet
have Cluster CRUD at all, so "make this build standalone against `main`"
would be a fabricated constraint, not a real one — stacking directly says
plainly that this milestone genuinely cannot exist independently of M3, the
same way the e2e PR chain already stacks for the same structural reason.
Stacking also keeps the PR diff small and reviewable (just this milestone's
actual changes against M3's tip) with no throwaway-merge-worktree dance
needed to validate it, unlike M5's situation.

**Related requirements:** None directly — process/workflow decision.

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

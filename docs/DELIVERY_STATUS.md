# OSAC Service Provider — Delivery Status

**Last Updated:** 2026-09-18
**Status:** ✅ **Implementation milestones delivered** (live deployment validation ongoing)

---

## Summary

All planned M1-M7 milestones and the Phase 2 registration migration have been
delivered, tested, and merged to `main`. The active Tier B workflow now runs
against the real OSAC stack and environment-agent; the remaining Hub-dispatch
E2E coverage is in PR #58 and is not yet merged.

**Test Results:** 469/469 specs passing across 20 test suites on `main`.

---

## Delivery Milestones

| Milestone | Title | PR | Date | Status | Coverage |
|-----------|-------|----|----|--------|----------|
| **M1** | Scaffold + Registration + Health | #3 | 2026-07-27 | ✅ Merged | HTTP server, OIDC bootstrap, health checks |
| **M2** | gRPC Client Generation | #7 | 2026-07-31 | ✅ Merged | Clusters, ComputeInstances, Subnets, VirtualNetworks services |
| **M3** | Cluster CRUD | #13 | 2026-08-03 | ✅ Merged | Create/Get/List/Delete for clusters (50 service tests + 46 handler tests) |
| **M4** | VM CRUD | #14 | 2026-08-05 | ✅ Merged | Create/Get/List/Delete for VMs + default network provisioning (61 + 37 tests) |
| **M5** | Status Polling + CloudEvents | #25 | 2026-08-05 | ✅ Merged | Async polling (30s interval), change detection, NATS publishing (36 tests) |
| **M6** | Version Translation Matrix | #26 | 2026-08-05 | ✅ Merged | K8s version → OCP release image mapping (14 tests) |
| **M7** | E2E Infrastructure (kind CI) | #29, #59 | 2026-08-18 / 2026-09-15 | ✅ Merged | Active Tier B stack with real fulfillment-service, Keycloak, environment-agent, NATS, osac-operator, and BMFO |
| **Phase 2** | Environment-Agent Registration | #38 | 2026-08-21 | ✅ Merged | Migrated registration target from control-plane to environment-agent |
| **TLS/AuthN** | Mandatory TLS + AuthN/Z Delegation | #52 | 2026-09-02 | ✅ Merged | Real TLS to fulfillment-service, delegate authN/Z to OSAC |
| **Follow-up** | Hub Dispatch E2E | #58 | 2026-09-18 | In review | Enables TC-TB-200 from osac-sp Create through fulfillment-service Hub dispatch |

---

## Test Coverage

### Unit & Integration Tests (Running on Every PR)

```
Total Suites: 20
Total Specs: 469
Status: ✅ ALL PASSING

Test Suite Breakdown:
├── Config Suite: 12/12 ✅
├── API Server Suite: 27/27 ✅
├── OSAC Bootstrap Suite: 32/32 ✅
├── Registration Suite: 20/20 ✅
├── Health Suite: 10/10 ✅
├── HTTP Error Suite: 3/3 ✅
├── GRPC Error Classification Suite: 10/10 ✅
├── Cluster Suite (Service Layer): 50/50 ✅
├── Cluster Handlers Suite: 46/46 ✅
├── VM Suite (Service Layer): 61/61 ✅
├── VM Handlers Suite: 37/37 ✅
├── StatusPoll Suite: 36/36 ✅
├── StatusPublisher Suite: 16/16 ✅
├── VersionMatrix Suite: 14/14 ✅
├── Util Suite: 1/1 ✅
├── Main Integration Suite: 24/24 ✅
├── AAP Mock Suite: 19/19 ✅
├── AAP Mock Main Integration Suite: 7/7 ✅
├── Mock Provider Suite: 35/35 ✅
└── Mock Provider Main Integration Suite: 9/9 ✅

Execution Time: 42.94 seconds (representative `main` run)
Race Detector: Enabled
```

### E2E Test Coverage (Tier B kind CI)

**PR #59** made the real environment-agent-backed Tier B workflow canonical:
- TC-TB-020: real Keycloak token claims
- TC-E2E-020/030/040: environment-agent registration, metadata, and renewal
- TC-TB-030: health against the real fulfillment-service stack
- TC-TB-060: Phase 2 CRDs registered before reconciliation
- TC-TB-080/090: direct ClusterOrder creation and terminal Ready state
- TC-TB-110/120: BareMetalInstance Ready paths
- TC-TB-130/140/150/160: BMFO failure, contention, and release paths
- TC-TB-200: osac-sp Create through fulfillment-service Hub dispatch, pending merge of #58

**Tier B Stack:**
- Real cert-manager + self-signed CA
- Real PostgreSQL + Keycloak (with vendored realm)
- Real fulfillment-service (published Helm chart)
- Real osac-operator + BMFO (via Helm)
- Real osac-aap-mock (Docker image built locally)
- Real osac-service-provider (Docker image built locally)
- environment-agent (built from pinned go.mod commit)

---

## Feature Completeness

### Cluster Management

✅ **Create** (`POST /api/v1alpha1/clusters?id=...`)
- Idempotent on `id`: retry with same ID returns existing resource
- DCM spec translation → OSAC gRPC Clusters/Create
- Template resolution via template_id in provider_hints
- Ownership labels: `dcm.io/managed-by=dcm`
- Returns 201 with cluster in initial PROGRESSING state

✅ **Get** (`GET /api/v1alpha1/clusters/{clusterId}`)
- Kubeconfig populated only when status == ACTIVE
- Otherwise empty string
- Full cluster status including node counts

✅ **List** (`GET /api/v1alpha1/clusters?max_page_size=50&page_token=...`)
- AEP-132 pagination wrapper
- Filters by ownership label (`dcm.io/managed-by=dcm`)
- Never populates kubeconfig in list entries
- Offset/limit pagination translated from OSAC

✅ **Delete** (`DELETE /api/v1alpha1/clusters/{clusterId}`)
- Idempotent: treats NotFound as success (204)
- Async at API level: returns 204, reconciliation continues in background
- Removal from local cache when 404 observed

### VM Management

✅ **Create** (`POST /api/v1alpha1/vms?id=...`)
- Idempotent on `id`: retry with same ID returns existing resource
- DCM spec translation → OSAC gRPC ComputeInstances/Create
- Default network provisioning: resolves or creates shared VNet+Subnet
- Template + instance_type resolution
- SSH key, boot disk, additional disks all supported
- Returns 201 with VM in initial PROVISIONING state

✅ **Get** (`GET /api/v1alpha1/vms/{vmId}`)
- Internal/external IP addresses echoed from OSAC
- Full VM status (PROVISIONING, RUNNING, STOPPED, FAILED, DELETING, STOPPING, PAUSED, DELETED)

✅ **List** (`GET /api/v1alpha1/vms?max_page_size=50&page_token=...`)
- AEP-132 pagination wrapper
- Filters by ownership label
- IP addresses echoed in each entry

✅ **Delete** (`DELETE /api/v1alpha1/vms/{vmId}`)
- Idempotent: treats NotFound as success
- Async deletion with local cache removal on 404 observation

### Status & Health

✅ **Status Polling** (M5)
- Async poller: every 30 seconds (configurable)
- Change detection via per-service-type caches
- Publishes CloudEvents to NATS JetStream on status change
- Subjects: `dcm.cluster` / `dcm.vm`
- Types: `dcm.status.cluster` / `dcm.status.vm`

✅ **Health Checks** (M1)
- Two endpoints: `/api/v1alpha1/clusters/health` and `/api/v1alpha1/vms/health`
- Report identical status (single global condition)
- Reflects: OIDC token validity + OSAC gRPC connectivity
- Uptime, version, status detail included

### Error Handling

✅ **RFC 9457 Problem Details** (M3+)
- All errors use `application/problem+json` content type
- Type URIs: `https://dcm-project.github.io/problems/{error-type}`
- Supported types: `invalid-argument`, `not-found`, `already-exists`, `permission-denied`, `unauthenticated`, `internal`, `unavailable`
- HTTP status codes: 400, 401, 403, 404, 409, 422, 500, 502

✅ **gRPC Error Classification** (M3+)
- Automatic translation from gRPC codes (codes.InvalidArgument, codes.NotFound, etc.)
- Preserves original error message detail

### Version Translation (M6)

✅ **Kubernetes → OCP Release Image**
- Hardcoded compatibility matrix (configurable)
- Fallback to provider_hints.osac.release_image override
- 14 mapping test cases

---

## Known Limitations & Deferred Features

| Item | Status | Reason |
|------|--------|--------|
| **UPDATE/PATCH** | Out of Scope | DCM SP API doesn't standardize UPDATE yet |
| **Day 2 Operations** (scale, upgrade, hibernate) | Out of Scope | Requires UPDATE support |
| **Direct `vcpu`/`memory` VM sizing** | Deprecated in OSAC | Use `provider_hints.osac.instance_type` instead |
| **Hub dispatch e2e test** (TC-TB-200) | In review in #58; skipped on `main` | #58 validates the private CLI path against fulfillment-service `v0.0.107`; keep this pending until that PR merges |
| **Per-DCM-tenant OSAC isolation** | Single-tenant v1 | Pending FLPATH-4115 (DCM multi-tenancy) |

---

## Architecture Decisions (Logged as DD-*)

Key decisions documented in `.ai/decisions/osac-sp.decisions.md`:

- **DD-050:** Phase 1 (control-plane) vs Phase 2 (environment-agent) split
- **DD-080:** Cluster CRUD field mapping and ID handling
- **DD-090:** Cluster status vocabulary (7 states)
- **DD-100:** Idempotent create pattern (AlreadyExists handling)
- **DD-110:** List filtering by ownership labels
- **DD-120:** VM CRUD field mapping
- **DD-121:** VM status vocabulary (8 states)
- **DD-122:** VM sizing via instance_type (cores/memory deprecated)
- **DD-123:** Disk capacity parsing (GB/GiB/TB/TiB)
- **DD-125:** AEP-133 schema compliance (Create must have no required body params)
- **DD-203:** Phase 2 migration: control-plane → environment-agent
- **DD-232:** AuthN/Z delegation to OSAC (no bearer token forwarding from DCM)
- **DD-233:** TLS mandatory to fulfillment-service
- **DD-235:** Tier B runs environment-agent and JetStream NATS inside kind
- **DD-237:** Retire Phase A control-plane/mock-provider E2E; Tier B is canonical

---

## Production Readiness Checklist

| Aspect | Status | Evidence |
|--------|--------|----------|
| **Code Quality** | ✅ | golangci-lint (11 linters), gosec, go vet all passing |
| **Unit Tests** | ✅ | 469/469 specs passing across 20 suites; race detector enabled |
| **E2E Tests** | ⚠️ | Tier B kind CI real stack passes; Hub-dispatch TC-TB-200 is in #58 |
| **Error Handling** | ✅ | RFC 9457 all response codes, comprehensive error classification |
| **Idempotency** | ✅ | Retry-safe creates, AlreadyExists handling, NotFound tolerance on delete |
| **Pagination** | ✅ | AEP-132 wrapper, max_page_size + opaque page_token |
| **TLS** | ✅ | Mandatory TLS to fulfillment-service (DD-233) |
| **AuthN/Z** | ✅ | OIDC client-credentials against OSAC's Keycloak, delegation to OSAC authorization (DD-232) |
| **Observability** | ✅ | Request logging, health checks, status polling with change detection |
| **Documentation** | ✅ | README, CLAUDE.md, active E2E specs, and delivery status are aligned |

---

## Next Steps (Post-Delivery)

1. **Validate against Real OSAC** (Issue #11 DoD)
   - Deploy against live OSAC dev or MOC environment
   - Verify CRUD against real clusters/VMs
   - Test status polling accuracy and latency

2. **Merge Hub-dispatch coverage**
   - Merge [#58](https://github.com/dcm-project/osac-service-provider/pull/58)
   - Keep TC-TB-200 and the workflow's private ClusterVersion setup aligned
   - Update this document from "in review" to delivered after the merge

3. **Performance & Hardening**
   - Profile status polling under load (large cluster/VM counts)
   - Test resilience: fulfillment-service outages, Keycloak restarts
   - Measure gRPC client pool efficiency

4. **Optional Enhancements**
   - Events/Watch streaming (alternative to polling, lower latency)
   - Instance type best-fit matcher (once OSAC-46 stabilizes)
   - Pre-provisioned default subnets per tenant (mitigate first-VM latency)

---

## References

- **Main Design:** [OSAC Service Provider Enhancement](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md)
- **Implementation Plan:** [Issue #1](https://github.com/dcm-project/osac-service-provider/issues/1)
- **Architecture Guide:** [CLAUDE.md](./CLAUDE.md)
- **Milestone Specs:** `.ai/specs/osac-sp-m{1..7}-*.spec.md`
- **Decision Log:** `.ai/decisions/osac-sp.decisions.md`
- **E2E CI Pattern:** [docs/e2e-ci-pattern-for-service-providers.md](./docs/e2e-ci-pattern-for-service-providers.md)

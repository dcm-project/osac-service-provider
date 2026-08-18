# Specification: OSAC Service Provider — Milestone 3 (Cluster CRUD)

## 1. Overview

Milestone 3 per [issue #1](https://github.com/dcm-project/osac-service-provider/issues/1)'s
suggested delivery milestones: implement the four Cluster CRUD REST
endpoints, translating `control-plane`-dispatched requests into
`osac.public.v1.Clusters` gRPC calls.

- `POST /api/v1alpha1/clusters` — Create (idempotent on `id`)
- `GET /api/v1alpha1/clusters` — List
- `GET /api/v1alpha1/clusters/{clusterId}` — Get (incl. kubeconfig once `ACTIVE`)
- `DELETE /api/v1alpha1/clusters/{clusterId}` — Delete (404-tolerant)

**This spec covers Cluster CRUD only.** Explicitly out of scope, per
confirmed scoping:

- Async status-polling + CloudEvents/NATS publishing back to `control-plane`
  — deferred to a new **Milestone 5** (no messaging dependency exists in this
  repo yet; issue #1's Milestone 3 line does not mention it).
- VM CRUD — Milestone 4.
- Default network/subnet provisioning — VM-only concern (`ComputeInstances`
  require `network_attachments`; `Clusters` do not — see §4.1), Milestone 4.
- The full DCM-K8s-version-to-OSAC-`release_image` compatibility matrix —
  Milestone 6 per issue #1 and `osac-sp.spec.md`'s SC-001. This milestone's
  Create endpoint is the **first actual consumer** of that placeholder
  concern (SC-001 originally noted "no cluster-create endpoint consumes it
  yet" — this milestone resolves that premise; see §4.1's translation table
  for the placeholder this milestone uses in its place).
- Templates whose `node_sets` map doesn't define exactly one key — rejected
  with `400` (REQ-CREATE-090); sizing a multi-node-set template needs a new
  provider hint the enhancement doc doesn't define yet (SC-M3-004, DD-110).

**Reference documents:**

- [OSAC SP Enhancement](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md) — [API Endpoints](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#api-endpoints), [Idempotent Creation](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#idempotent-creation), [Ownership Tracking](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#ownership-tracking), [Status Mapping](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#status-mapping-osac-to-dcm) — merged via [PR #96](https://github.com/dcm-project/enhancements/pull/96), the current source of truth for the Phase 1 (`control-plane`) contract; see DD-080/090/100 and SC-M3-001/002 for corrections/extensions found during independent verification
- [`dcm-project/control-plane`](https://github.com/dcm-project/control-plane)'s actual outbound dispatch code — [`internal/sp/service/resource_manager/service_type_instance.go`](https://github.com/dcm-project/control-plane/blob/f243dfaa2e2752c63202432409e78cc2a4ad7d85/internal/sp/service/resource_manager/service_type_instance.go) (`createInstanceWithProvider`/`deleteInstanceWithProvider`) and [`convert.go`](https://github.com/dcm-project/control-plane/blob/f243dfaa2e2752c63202432409e78cc2a4ad7d85/internal/sp/service/resource_manager/convert.go) (`ProviderResponse`) — read directly, not inferred from any OpenAPI doc, since `control-plane`'s own inbound `resource_manager/openapi.yaml` describes a *different* API (catalog-facing) than what it sends to this SP
- [Generic Service Type Schema](https://github.com/dcm-project/enhancements/blob/main/enhancements/service-type-definitions/service-type-definitions.md#generic-service) and [Kubernetes Cluster Schema](https://github.com/dcm-project/enhancements/blob/main/enhancements/service-type-definitions/service-type-definitions.md#kubernetes-cluster)
- [Service Provider Status Reporting — Cluster status](https://github.com/dcm-project/enhancements/blob/main/enhancements/state-management/service-provider-status-reporting.md#cluster-status) — the canonical 7-value status vocabulary (§4.5)
- OSAC public protos, vendored in Milestone 2: [`clusters_service.proto`/`cluster_type.proto`](https://github.com/osac-project/fulfillment-service/tree/73ae26e8cb0a476d4b035b18776603f60a361ed9/proto/public/osac/public/v1) — plus `cluster_template_type.proto`/`cluster_templates_service.proto` (`osac.public.v1.ClusterTemplates`), newly vendored **this** milestone (DD-110)
- [Milestone 1 spec](./osac-sp.spec.md) (`internal/httperror`, RFC 9457 error writing — DD-070) and [Milestone 2 spec](./osac-sp-m2-grpc-client-generation.spec.md) (`Bootstrap.Conn()`) — both extended, not replaced
- [Design Decisions](../decisions/osac-sp.decisions.md) — DD-080/090/100/110/111/112/113/114 (new, this milestone)

---

## 2. Architecture

Extends Milestone 1's `internal/apiserver`/`internal/httperror` and
Milestone 2's `internal/osac.Bootstrap.Conn()`. Two new packages:

- **`internal/cluster`** — business logic. Constructs
  `publicv1.NewClustersClient(bootstrap.Conn())` and
  `publicv1.NewClusterTemplatesClient(bootstrap.Conn())` (per M2's DD-020
  pattern — no new accessor added to `Bootstrap`) and exposes `Create`/`Get`/
  `List`/`Delete` methods operating on the SP's own `Cluster` type,
  encapsulating DCM<->OSAC field translation, the node-set key resolution
  (DD-110), the idempotent-create retry, and the shared status mapper
  (§4.5).
- **`internal/handlers/cluster`** — thin `StrictServerInterface`
  implementations for the 4 REST operations, delegating to `internal/cluster`
  and reusing `internal/httperror` for every non-2xx response (§4.6) — no new
  error-writing mechanism.

```
control-plane (synchronous, direct REST — DD-080)
        |
        | POST /api/v1alpha1/clusters?id=X   {"spec": {...}}
        | DELETE /api/v1alpha1/clusters/X
        | (GET/List/Update never called — control-plane serves those
        |  from its own Postgres store; see DD-080)
        v
+--------------------------------------------------------------+
|              internal/handlers/cluster                       |
|   (StrictServerInterface: Create/Get/List/Delete)             |
|         |                                                     |
|         v                                                     |
|              internal/cluster                                |
|   Create: ClusterTemplates/Get -> resolve node_sets key       |
|           (DD-110), translate spec -> ClusterSpec, set        |
|           ownership labels, Clusters/Create,                  |
|           AlreadyExists->Get (DD-100)                         |
|   Get:    Clusters/Get, status mapper (4.5), conditional      |
|           Clusters/GetKubeconfig                              |
|   List:   Clusters/List (CEL ownership filter, offset/limit)  |
|   Delete: Clusters/Delete, NotFound treated as success        |
|         |                                                     |
|         v                                                     |
|   publicv1.NewClustersClient(bootstrap.Conn())  <-- M2        |
|   publicv1.NewClusterTemplatesClient(bootstrap.Conn())        |
+--------------------------------------------------------------+
        |
        v
   osac.public.v1.Clusters (OSAC fulfillment service)
```

No changes to `internal/config`, `internal/registration`, or `internal/osac`
— this milestone only adds business-logic and handler packages on top of
Milestone 2's already-authenticated shared connection.

---

## 3. Topic Dependency Graph

| # | Topic | Prefix | Depends On |
|---|-------|--------|------------|
| 1 | Cluster Create | CREATE | Topic 5 (Status Mapping), Topic 6 (Error Mapping), Milestone 2 `Bootstrap.Conn()` (also backing `ClusterTemplatesClient`, DD-110) |
| 2 | Cluster Get | GET | Topic 5, Topic 6, Milestone 2 |
| 3 | Cluster List | LIST | Topic 5, Topic 6, Milestone 2 |
| 4 | Cluster Delete | DELETE | Topic 6, Milestone 2 |
| 5 | Status Mapping | STATUS | Milestone 2 (`publicv1.ClusterState`/`ClusterCondition` types) |
| 6 | Error Mapping | ERR | Milestone 1 Topic 4.1 (`internal/httperror`, DD-070) |

```
Topic 5: Status Mapping  ---+--> Topic 1: Create
Topic 6: Error Mapping   ---+--> Topic 2: Get
                          \--+--> Topic 3: List
                           \----> Topic 4: Delete (Topic 6 only)
```

---

## 4. Topic Specifications

### 4.1 Cluster Create

#### Overview

`POST /api/v1alpha1/clusters` accepts a required `id` query parameter (set
by `control-plane` — DD-080) and a JSON body `{"spec": {...}}` where `spec`
follows the [generic Cluster schema](https://github.com/dcm-project/enhancements/blob/main/enhancements/service-type-definitions/service-type-definitions.md#kubernetes-cluster)
(`version`, `nodes.control_plane`, `nodes.worker`) plus the common
`metadata`/`provider_hints` fields. The handler translates this into OSAC's
`ClusterSpec` and calls `Clusters/Create`.

**Field Mapping (DCM request → OSAC `ClusterSpec`):**

| DCM Field | OSAC Field | Notes |
|-----------|------------|-------|
| `id` (query param) | `Cluster.id` | Sets OSAC's own identifier — see REQ-CREATE-040 (idempotency) |
| `spec.version` | `spec.release_image` | Translated via a hardcoded placeholder table (REQ-CREATE-025) — full matrix is Milestone 6 |
| `spec.nodes.control_plane.*` | *(not sent)* | Hosted Control Planes manage the control plane internally — OSAC's `ClusterSpec` has no control-plane node-set concept |
| `spec.nodes.worker.count` | `spec.node_sets[key].size` | `key` comes from `ClusterTemplates/Get(template_id).node_sets` (DD-110) — never derived from `template_id` itself; the template MUST define exactly one node-set key (REQ-CREATE-090) |
| `spec.nodes.worker.cpu`/`memory`/`storage` | *(not sent)* | Informational only — `host_type` is fixed by the template (REQ-CREATE-070) |
| `spec.metadata.name` | `spec.metadata.name` | |
| `spec.metadata.labels` | `spec.metadata.labels` (merged with ownership labels, REQ-CREATE-030) | |
| `spec.provider_hints.osac.template_id` | `spec.template` | Required — see Provider Hints below |
| `spec.provider_hints.osac.base_domain`/`pull_secret`/`ssh_key` | `spec.network.*`/`pull_secret`/`ssh_public_key` | Optional passthrough |

**Provider Hints (`provider_hints.osac`):** `template_id` (string, required);
`base_domain`, `pull_secret`, `ssh_key`, `release_image` (string, optional —
`release_image` overrides the `version`-derived translation when present).

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-CREATE-010 | The SP MUST implement `POST /api/v1alpha1/clusters`, accepting a required `id` query parameter and a request body `{"spec": {...}}` — matching `control-plane`'s actual outbound dispatch shape, not a hypothetical/generic REST-resource shape. "Required" here is a runtime/behavioral requirement (REQ-CREATE-060) enforced by request validation, not the OpenAPI schema's `required` keyword — both `id` and the body's `spec` property are schema-optional for AEP-133 compliance | MUST | DD-080, DD-113 |
| REQ-CREATE-020 | The SP MUST translate the request per the Field Mapping table above and call `osac.public.v1.Clusters/Create` with `Cluster.id` set to the `id` query parameter's exact value | MUST | |
| REQ-CREATE-025 | `spec.version` MUST be translated to `release_image` via a hardcoded placeholder table (e.g. the same versions already used for `kubernetes_supported_versions`, REQ-REG-040) when `provider_hints.osac.release_image` is not supplied; this table does not need to be the full compatibility matrix (Milestone 6) | MUST | SC-001 (`osac-sp.spec.md`) |
| REQ-CREATE-030 | The SP MUST set three ownership labels on `Cluster.metadata.labels` for every Create call: `dcm.io/managed-by="dcm"`, `dcm.io/instance-id="<id>"`, `dcm.io/service-type="cluster"` — merged with, not replacing, any caller-supplied `spec.metadata.labels` | MUST | |
| REQ-CREATE-040 | If `Clusters/Create` returns gRPC `AlreadyExists` for the given `id`, the SP MUST call `Clusters/Get(id)` and return **that** object's current state as a successful Create response — MUST NOT surface `AlreadyExists` to the caller as an error | MUST | DD-100 |
| REQ-CREATE-050 | A successful Create response MUST be `201 Created` with a body whose top-level `id` and `status` fields are always populated (never omitted/null) — `control-plane` persists these two fields with no presence validation of its own | MUST | |
| REQ-CREATE-060 | A request missing the `id` query parameter, missing the body's `spec` property entirely, or whose `spec` fails required-field validation (`spec.version`, `spec.nodes.worker.count`, `spec.metadata.name`, or `spec.provider_hints.osac.template_id` absent/empty), MUST return `400 Bad Request` via the shared error-mapping topic (§4.6) without calling OSAC | MUST | DD-113 |
| REQ-CREATE-070 | The SP MUST NOT compute or send a `host_type` derived from `spec.nodes.worker.cpu`/`memory`/`storage` — those fields MUST be treated as informational only, with no corresponding OSAC field set from them | MUST | Node Sizing resolution |
| REQ-CREATE-080 | Before dispatching `Clusters/Create`, the SP MUST call `ClusterTemplates/Get(template_id)` and use the returned `node_sets` map's key — never `template_id` itself, never a DCM-chosen name — to construct `spec.node_sets[key].size` from `nodes.worker.count` | MUST | DD-110 |
| REQ-CREATE-090 | If `ClusterTemplates/Get(template_id)`'s `node_sets` map does not contain exactly one key (zero, or more than one), the SP MUST reject the request with `400 Bad Request` via the shared error-mapping topic (§4.6), without calling `Clusters/Create` — multi-node-set (and node-set-less) templates are out of scope for this milestone's single `nodes.worker.count` sizing dimension | MUST | DD-110 |
| REQ-CREATE-100 | If `ClusterTemplates/Get(template_id)` returns gRPC `NotFound` (an unknown `template_id`), the SP MUST reject the request with `400 Bad Request` (`InvalidArgument`) via the shared error-mapping topic (§4.6), **not** `404` — an unresolvable value inside the caller's own request body is a request-validation failure (the same category as REQ-CREATE-060), not evidence of a missing SP-managed resource | MUST | DD-111 |

#### Configuration Introduced

None — reuses Milestone 2's `Bootstrap.Conn()` (now also backing a
`ClusterTemplatesClient`, DD-110).

#### Acceptance Criteria

##### AC-CREATE-010: Create translates and dispatches the full field set correctly

- **Validates:** REQ-CREATE-010, REQ-CREATE-020, REQ-CREATE-025, REQ-CREATE-080
- **Given** a request `POST /api/v1alpha1/clusters?id=X` with body `{"spec":{"version":"1.29","nodes":{"worker":{"count":3}},"metadata":{"name":"foo"},"provider_hints":{"osac":{"template_id":"default-hcp"}}}}`, and a fake `ClusterTemplatesServer` whose `Get("default-hcp")` returns a template with `node_sets={"compute":{}}` (a key deliberately distinct from the template ID, to prove the SP doesn't assume `key == template_id`)
- **When** the handler processes it against fake `bufconn`-backed `ClusterTemplatesServer`/`ClustersServer`
- **Then** the fake's recorded `Clusters/Create` call has `Cluster.id` exactly `"X"`, `spec.template` exactly `"default-hcp"`, `spec.node_sets["compute"].size` exactly `3` (the key discovered from `ClusterTemplates/Get`, not `"default-hcp"`), `spec.metadata.name` exactly `"foo"`, and `spec.release_image` equal to the placeholder table's mapped value for `"1.29"` (not empty, not the literal string `"1.29"`)

##### AC-CREATE-020: Ownership labels are set exactly, merged with caller labels

- **Validates:** REQ-CREATE-030
- **Given** a Create request with `id=X` and `spec.metadata.labels={"team":"platform"}`
- **When** processed
- **Then** the fake's recorded `Cluster.metadata.labels` equals exactly `{"team":"platform","dcm.io/managed-by":"dcm","dcm.io/instance-id":"X","dcm.io/service-type":"cluster"}`

##### AC-CREATE-030: Idempotent Create — retried request with the same `id` returns the existing resource, not a duplicate or an error

- **Validates:** REQ-CREATE-040
- **Given** a Create request with `id=X` has already succeeded once (fake OSAC now holds exactly one Cluster with `id=X`, `status.state=PROGRESSING`)
- **When** a second Create request arrives with the same `id=X` and an equivalent `spec`
- **Then** the fake's second `Clusters/Create` call returns `AlreadyExists`, the SP calls `Clusters/Get("X")`, the HTTP response is `201 Created` with `id` exactly `"X"` and `status` exactly `"PROGRESSING"` (the Get's value), and the fake OSAC server's cluster count remains exactly `1`

##### AC-CREATE-040: Missing `id` query parameter is rejected before calling OSAC

- **Validates:** REQ-CREATE-060
- **Given** a Create request with no `id` query parameter
- **When** processed
- **Then** the response is `400 Bad Request` (RFC 9457, `type` exactly `.../invalid-argument`) and the fake OSAC server recorded **zero** `Clusters/Create` calls

##### AC-CREATE-050: Missing required spec field is rejected before calling OSAC

- **Validates:** REQ-CREATE-060
- **Given** a Create request body missing `spec.provider_hints.osac.template_id`
- **When** processed
- **Then** the response is `400 Bad Request` and the fake OSAC server recorded zero `Clusters/Create` calls

##### AC-CREATE-060: Worker sizing hints are never translated into a `host_type` override

- **Validates:** REQ-CREATE-070
- **Given** a Create request with `spec.nodes.worker.cpu=8`, `memory="32GB"`, `storage="250GB"`
- **When** processed
- **Then** the fake's recorded `Clusters/Create` call's `node_sets[key].host_type` field is the empty string (the SP never sets it — OSAC's template fills it server-side)

##### AC-CREATE-070: Templates without exactly one node-set key are rejected without calling OSAC's Create

- **Validates:** REQ-CREATE-090
- **Given** two cases: (a) `template_id="multi-nodeset-template"` whose fake `ClusterTemplatesServer.Get` returns `node_sets={"compute":{},"gpu":{}}` (two keys); (b) `template_id="empty-nodeset-template"` whose `Get` returns `node_sets={}` (zero keys)
- **When** each is processed
- **Then** both responses are `400 Bad Request` (RFC 9457, `type` exactly `.../invalid-argument`) and the fake OSAC server recorded **zero** `Clusters/Create` calls in either case

##### AC-CREATE-080: An unknown `template_id` surfaces as 400 (InvalidArgument), not swallowed, not 404

- **Validates:** REQ-CREATE-100, REQ-ERR-010
- **Given** a Create request referencing `template_id="nonexistent"`, and a fake `ClusterTemplatesServer` whose `Get("nonexistent")` returns gRPC `NotFound`
- **When** processed
- **Then** the response is `400 Bad Request` (RFC 9457, `type` exactly `.../invalid-argument`) — not `404` — and the fake OSAC server recorded **zero** `Clusters/Create` calls

#### Dependencies

Depends on Topic 5 (Status Mapping, for the response `status` field),
Topic 6 (Error Mapping), and Milestone 2's `Bootstrap.Conn()` (backing both
`ClustersClient` and the new `ClusterTemplatesClient`, DD-110).

---

### 4.2 Cluster Get

#### Overview

`GET /api/v1alpha1/clusters/{clusterId}` calls `Clusters/Get(clusterId)`
(the same value as `id` — see DD-080's ID Mapping note: DCM's identifier and
OSAC's `Cluster.id` are always the same value, so no local mapping store is
needed), maps the result via the shared status mapper (§4.5), and
conditionally fetches the kubeconfig.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-GET-010 | The SP MUST implement `GET /api/v1alpha1/clusters/{clusterId}`, calling `Clusters/Get(clusterId)` and mapping the result per the shared status mapper (§4.5) | MUST | |
| REQ-GET-020 | When the mapped status is exactly `ACTIVE`, the SP MUST call `Clusters/GetKubeconfig` and populate the response's `kubeconfig` field with its (base64) value | MUST | |
| REQ-GET-030 | When the mapped status is anything other than `ACTIVE`, the response's `kubeconfig` field MUST be the empty string, and `Clusters/GetKubeconfig` MUST NOT be called | MUST | Avoids an unnecessary/premature OSAC call |
| REQ-GET-040 | `Clusters/Get` returning gRPC `NotFound` MUST map to HTTP `404` via the shared error-mapping topic (§4.6) | MUST | |

#### Configuration Introduced

None.

#### Acceptance Criteria

##### AC-GET-010: `ACTIVE` cluster returns its kubeconfig, fetched exactly once

- **Validates:** REQ-GET-010, REQ-GET-020
- **Given** a fake `Clusters/Get` returning `status.state=CLUSTER_STATE_READY` and a fake `Clusters/GetKubeconfig` returning `"kubeconfig-abc"`
- **When** `GET /api/v1alpha1/clusters/{id}` is called
- **Then** the response is `200 OK` with `status` exactly `"ACTIVE"`, `kubeconfig` exactly `"kubeconfig-abc"`, and the fake's `GetKubeconfig` call counter equals exactly `1`

##### AC-GET-020: Non-`ACTIVE` cluster never triggers a kubeconfig fetch

- **Validates:** REQ-GET-030
- **Given** a fake `Clusters/Get` returning `status.state=CLUSTER_STATE_PROGRESSING`
- **When** `GET` is called
- **Then** the response's `status` is exactly `"PROGRESSING"`, `kubeconfig` is exactly `""`, and the fake's `GetKubeconfig` call counter equals exactly `0`

##### AC-GET-030: Nonexistent cluster returns 404

- **Validates:** REQ-GET-040
- **Given** a fake `Clusters/Get` returning gRPC `NotFound`
- **When** `GET` is called
- **Then** the response is `404 Not Found` (RFC 9457, `type` exactly `.../not-found`)

#### Dependencies

Depends on Topic 5 (Status Mapping), Topic 6 (Error Mapping), Milestone 2.

---

### 4.3 Cluster List

#### Overview

`GET /api/v1alpha1/clusters` calls `Clusters/List` with a CEL filter that
scopes results to this SP's own managed resources, and translates DCM's
`max_page_size`/`page_token` query parameters to/from OSAC's `limit`/`offset`
pagination.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-LIST-010 | The SP MUST call `Clusters/List` with CEL filter `this.metadata.labels["dcm.io/managed-by"] == "dcm"` on every call — this filter is always applied and is not caller-configurable (the REST API exposes no raw-filter passthrough) | MUST | Ownership Tracking |
| REQ-LIST-020 | The SP MUST translate `max_page_size` (query, default `50` when omitted) to OSAC's `limit`, and encode/decode `page_token` as an opaque wrapper around OSAC's `offset` | MUST | |
| REQ-LIST-030 | The response MUST be the AEP-132 pagination wrapper `{"results": [...], "next_page_token": "..."}`, with each entry mapped via the same status mapper as Get (§4.5), omitting the `kubeconfig` field entirely (List never fetches it — see AC-LIST-030) | MUST | |
| REQ-LIST-040 | `next_page_token` MUST be empty/absent exactly when OSAC's `List` response indicates no further results (not merely when the current page is short). The next offset MUST be computed from the number of results actually received (`len(results)`), not `resp.GetSize()`; an empty page (zero results) MUST NOT emit a `next_page_token`, regardless of what `Total` reports | MUST | DD-134 — a `Size`/`Total` mismatch must never cause the same `page_token` to be reissued, which would make a faithfully-paginating caller loop forever |

#### Configuration Introduced

None.

#### Acceptance Criteria

##### AC-LIST-010: List applies the ownership filter and default page size, returns exact field values

- **Validates:** REQ-LIST-010, REQ-LIST-030
- **Given** a fake `Clusters/List` that records its request and returns 2 clusters with known `id`/`status.state` values
- **When** `GET /api/v1alpha1/clusters` is called with no query parameters
- **Then** the fake recorded `filter` exactly equal to `this.metadata.labels["dcm.io/managed-by"] == "dcm"` and `limit` exactly `50`, and the response's `results` array has exactly 2 entries whose `id`/`status` fields equal the fake's canned values exactly

##### AC-LIST-020: `page_token` round-trips through OSAC's `offset` correctly

- **Validates:** REQ-LIST-020, REQ-LIST-040
- **Given** a fake `Clusters/List` that returns a response indicating a next `offset` of `50` on the first call
- **When** the response's `next_page_token` is fed back into a second `GET /api/v1alpha1/clusters?page_token=...` call
- **Then** the fake's second recorded `List` call has `offset` exactly `50`

##### AC-LIST-030: List entries never populate `kubeconfig`

- **Validates:** REQ-LIST-030
- **Given** a fake `Clusters/List` returning a cluster with `status.state=CLUSTER_STATE_READY` (which would trigger a kubeconfig fetch under Get, per AC-GET-010) and a fake `GetKubeconfig` that would fail the test if called
- **When** `GET /api/v1alpha1/clusters` is called
- **Then** the response entry has no `kubeconfig` field populated, and the fake `GetKubeconfig` call counter equals exactly `0`

##### AC-LIST-040: A `page_token` this SP never issued (not valid base64, or not numeric once decoded) is rejected as `400 Bad Request`, without calling `Clusters/List`

- **Validates:** REQ-LIST-020, REQ-ERR-010
- **Given** no fake `Clusters/List` behavior is configured (any call would panic/fail the test)
- **When** `GET /api/v1alpha1/clusters?page_token=not-valid-base64!!!` is called
- **Then** the response is `400 Bad Request` with `type` exactly `INVALIDARGUMENT`, and the fake's `List` call counter is exactly `0` — proving the token is rejected during request parsing, before any OSAC RPC

##### AC-LIST-050: A `Size`/`Total` mismatch never reissues the same `page_token` (regression)

- **Validates:** REQ-LIST-040
- **Given** a fake `Clusters/List` that returns zero items with `Size=0` while `Total=5` (a buggy/inconsistent upstream response) for a request at `offset=0`
- **When** `GET /api/v1alpha1/clusters` is called
- **Then** the response's `next_page_token` MUST be absent — never a token that would decode back to `offset=0` and reissue the exact same page

#### Dependencies

Depends on Topic 5 (Status Mapping), Topic 6 (Error Mapping), Milestone 2.

---

### 4.4 Cluster Delete

#### Overview

`DELETE /api/v1alpha1/clusters/{clusterId}` calls `Clusters/Delete`. Per
`control-plane`'s own dispatcher (`deleteInstanceWithProvider` treats a `404`
from the SP as success — DD-080), and per the corrected understanding of
OSAC's deletion model (SC-M3-001 — deletion is asynchronous and eventually
returns `NotFound` even mid-teardown), the SP mirrors that same tolerance
internally: a `NotFound` from OSAC is success, not an error.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-DELETE-010 | The SP MUST implement `DELETE /api/v1alpha1/clusters/{clusterId}`, calling `Clusters/Delete(clusterId)` | MUST | |
| REQ-DELETE-020 | `Clusters/Delete` returning gRPC `NotFound` MUST be treated as success — response `204 No Content`, **not** `404` — mirroring `control-plane`'s own tolerance for this exact case | MUST | DD-080 |
| REQ-DELETE-030 | Any other gRPC error from `Clusters/Delete` MUST map through the shared error-mapping topic (§4.6), producing a non-2xx response | MUST | |
| REQ-DELETE-040 | The SP MUST return `204` as soon as OSAC acknowledges the delete request — it MUST NOT poll/wait for the cluster to actually disappear (`Get`/`List` may continue returning the object briefly per SC-M3-001) | MUST | |

#### Configuration Introduced

None.

#### Acceptance Criteria

##### AC-DELETE-010: Successful delete returns 204

- **Validates:** REQ-DELETE-010, REQ-DELETE-040
- **Given** a fake `Clusters/Delete` that succeeds
- **When** `DELETE /api/v1alpha1/clusters/{id}` is called
- **Then** the response is `204 No Content` with an empty body, returned without any subsequent `Clusters/Get` call being made to confirm teardown

##### AC-DELETE-020: Deleting an already-deleted cluster is idempotent over two real HTTP requests

- **Validates:** REQ-DELETE-020
- **Given** a fake OSAC server holding exactly one cluster with `id=X`
- **When** `DELETE /api/v1alpha1/clusters/X` is issued twice in sequence over real HTTP (not two direct package-level calls)
- **Then** **both** requests receive `204 No Content` — the first because OSAC really deletes it, the second because OSAC's second `Delete` call returns `NotFound` and the SP maps that to `204` rather than `404`

##### AC-DELETE-030: A genuine OSAC failure during delete is not swallowed

- **Validates:** REQ-DELETE-030
- **Given** a fake `Clusters/Delete` returning gRPC `Unavailable`
- **When** `DELETE` is called
- **Then** the response is `502 Bad Gateway` (per §4.6's mapping table), not `204` — the `NotFound`-tolerance carve-out does not apply to unrelated error codes

#### Dependencies

Depends on Topic 6 (Error Mapping), Milestone 2.

---

### 4.5 Status Mapping

#### Overview

A single mapping function, shared identically by Create's response, Get,
and List (REQ-STATUS-030), translating OSAC's `ClusterStatus` (`state` enum
plus `conditions[]`) and the outcome of the gRPC call itself into DCM's
canonical **7-value** Cluster status vocabulary, per
[`service-provider-status-reporting.md`](https://github.com/dcm-project/enhancements/blob/main/enhancements/state-management/service-provider-status-reporting.md#cluster-status):
`PROGRESSING | ACTIVE | DEGRADED | UNAVAILABLE | FAILED | DELETING | DELETED`.

This is the full canonical set, not the 5-value subset the enhancement
doc's own Status Mapping table lists — see DD-090 for why the full set
applies here.

No existing document specifies a precedence order for when multiple OSAC
signals could theoretically coexist (e.g. a `DEGRADED` condition alongside a
`FAILED` state). This spec defines one (REQ-STATUS-020), since a mapper
function must return exactly one value.

Rules 1–2 below (gRPC-outcome-driven `UNAVAILABLE`/`DELETED`) are part of
the mapper's contract but are reachable only via Milestone 5's async
status-polling path, not via this milestone's synchronous Create/Get/List —
see SC-M3-003.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-STATUS-010 | The status mapper MUST return exactly one of the 7 canonical values listed above for every Create/Get/List call | MUST | DD-090 |
| REQ-STATUS-020 | The mapper MUST apply this precedence, in order, stopping at the first match: (1) the gRPC call itself failed with `Unavailable`/`DeadlineExceeded` (OSAC unreachable during polling) → `UNAVAILABLE`; (2) the gRPC call returned `NotFound` → `DELETED`; (3) `status.state == CLUSTER_STATE_UNSPECIFIED` → `PROGRESSING`; (4) `status.state == CLUSTER_STATE_FAILED` → `FAILED`; (5) `status.state == CLUSTER_STATE_DELETING` → `DELETING`; (6) `status.state == CLUSTER_STATE_DELETE_FAILED` → `FAILED`; (7) any condition with `type == CLUSTER_CONDITION_TYPE_DEGRADED` and `status == CONDITION_STATUS_TRUE` → `DEGRADED`; (8) `status.state == CLUSTER_STATE_READY` → `ACTIVE`; (9) `status.state == CLUSTER_STATE_PROGRESSING` → `PROGRESSING`; (10) anything else (a future, not-yet-modeled enum value) → `FAILED` (defensive default) | MUST | SC-M3-001 (rules 5/6/7 unreachable in practice, kept for forward compatibility); SC-M3-003 (rules 1/2 are reachable only via the future M5 async polling path — REQ-GET-040/REQ-ERR-010 already intercept these same gRPC outcomes as sync HTTP errors); DD-112 (rule 3 — `UNSPECIFIED` is OSAC's proto3 zero-value, the normal state for a fresh Cluster before `osac-operator`'s first reconcile pass, not a genuine anomaly) |
| REQ-STATUS-030 | Create's response `status`, Get's `status`, and each List entry's `status` MUST all be computed by the same mapper implementation (no per-handler duplication) | MUST | |

#### Configuration Introduced

None.

#### Acceptance Criteria

Table-driven: one row per REQ-STATUS-020 precedence rule, each asserting the
mapper's exact return value (not "no error") for a specific input.

##### AC-STATUS-010: Each individual signal maps to its documented value

- **Validates:** REQ-STATUS-010, REQ-STATUS-020
- **Given** each of the following inputs, in turn: gRPC `Unavailable`; gRPC `NotFound`; `state=UNSPECIFIED`; `state=FAILED`; `state=DELETING`; `state=DELETE_FAILED`; `state=READY` with no conditions; `state=PROGRESSING`; a `DEGRADED` condition `TRUE` with `state=READY`
- **When** the mapper is called with each
- **Then** it returns exactly `UNAVAILABLE`, `DELETED`, `PROGRESSING`, `FAILED`, `DELETING`, `FAILED`, `ACTIVE`, `PROGRESSING`, `DEGRADED` respectively (one assertion per input)

##### AC-STATUS-020: `FAILED` state takes precedence over a simultaneous `DEGRADED` condition

- **Validates:** REQ-STATUS-020 (precedence ordering)
- **Given** `state=CLUSTER_STATE_FAILED` **and** a `DEGRADED` condition `TRUE` present simultaneously
- **When** the mapper is called
- **Then** it returns exactly `FAILED` — state-level failure is checked before the condition

##### AC-STATUS-030: Connectivity failure is never conflated with a real 404

- **Validates:** REQ-STATUS-020 (rules 1 vs 2)
- **Given** two separate calls — one where the gRPC call itself fails with `Unavailable`, one where it succeeds and returns `NotFound`
- **When** the mapper is called for each
- **Then** the first returns exactly `UNAVAILABLE` and the second returns exactly `DELETED` — distinct values, not the same fallback

#### Dependencies

Depends on Milestone 2's vendored `publicv1.ClusterState`/`ClusterCondition`
types (no new proto vendoring).

---

### 4.6 Error Mapping

#### Overview

Every gRPC error surfaced by any `Clusters` or `ClusterTemplates` RPC
(except the three carve-outs below) MUST be mapped to an HTTP status and an
RFC 9457 (`application/problem+json`, DD-070) body, reusing
`internal/httperror.WriteResponse` — no new error-writing mechanism.
Milestone 1's `v1alpha1.ErrorType` enum already
defines all 7 codes this milestone needs
(`ALREADYEXISTS`/`INTERNAL`/`INVALIDARGUMENT`/`NOTFOUND`/`PERMISSIONDENIED`/
`UNAUTHENTICATED`/`UNAVAILABLE`); this milestone is the first to actually
exercise most of them (Milestone 1 only ever produced `INTERNAL`, per DD-070's
"Consequence" note) — no OpenAPI schema change needed for the enum itself.

**Carve-outs (handler-specific, override this table):** Create's `AlreadyExists`
is intercepted per REQ-CREATE-040 and never reaches this mapping (never
surfaces as `409` to the caller). Delete's `NotFound` is intercepted per
REQ-DELETE-020 and returns `204`, not `404`. Create's `ClusterTemplates/Get`
`NotFound` (unknown `template_id`) is intercepted per REQ-CREATE-100/DD-111
and returns `400` (`InvalidArgument`), not `404` — a bad value in the
caller's own request, not a missing SP-managed resource.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-ERR-010 | gRPC codes MUST map to HTTP status + `v1alpha1.ErrorType` as follows (except the carve-outs above): `InvalidArgument`→`400`/`INVALIDARGUMENT`; `Unauthenticated`→`401`/`UNAUTHENTICATED`; `PermissionDenied`→`403`/`PERMISSIONDENIED`; `NotFound`→`404`/`NOTFOUND`; `AlreadyExists`→`409`/`ALREADYEXISTS`; `Unavailable`/`DeadlineExceeded`→`502`/`UNAVAILABLE`; `Internal`/`Unknown`/anything else→`500`/`INTERNAL` | MUST | |
| REQ-ERR-020 | Every error response MUST use `internal/httperror.WriteResponse`, producing `Content-Type: application/problem+json` and a body matching the `v1alpha1.Error` schema | MUST | DD-070 |
| REQ-ERR-030 | The mapping function MUST be shared across all 4 handlers (one implementation, not per-handler duplication) | MUST | |

#### Configuration Introduced

None.

#### Acceptance Criteria

##### AC-ERR-010: Each gRPC code maps to its documented HTTP status and `type`

- **Validates:** REQ-ERR-010, REQ-ERR-020
- **Given**, in turn, fake OSAC responses of gRPC `InvalidArgument`, `Unauthenticated`, `PermissionDenied`, `NotFound` (on Get, not Delete), `Unavailable`, and `Internal`
- **When** each is returned from a `Clusters/Get` call
- **Then** the HTTP responses are exactly `400`/`401`/`403`/`404`/`502`/`500` respectively, each with `Content-Type: application/problem+json` and a decoded body's `type` field matching the documented `v1alpha1.ErrorType` constant exactly

##### AC-ERR-020: The same mapping function produces identical results across handlers

- **Validates:** REQ-ERR-030
- **Given** a fake OSAC `PermissionDenied` error returned from `Clusters/Get`, `Clusters/List`, and `Clusters/Delete` in three separate calls
- **When** each handler processes its respective request
- **Then** all three produce an identical HTTP status (`403`) and identical `type` value (one shared implementation, not three independently-drifting ones)

#### Dependencies

Depends on Milestone 1's `internal/httperror` (DD-070).

---

## 5. Cross-Cutting Concerns

No new cross-cutting concerns. Logging conventions (`osac-sp.spec.md` §5.1)
are unchanged — handlers log at the same levels/fields already established
(structured `slog`, request method/path/status already captured by the
existing middleware chain). No new configuration is introduced (§6).

---

## 6. Consolidated Configuration Reference

No new configuration keys. See `osac-sp.spec.md` §6 for the full table
(unchanged by this milestone) and `osac-sp-m2-grpc-client-generation.spec.md`
§6 (also unchanged).

---

## 7. Design Decisions

Design decisions (`DD-NNN`) live in
[`.ai/decisions/osac-sp.decisions.md`](../decisions/osac-sp.decisions.md),
per the convention established in `osac-sp.spec.md` §7. This milestone adds
**DD-080** (Cluster CRUD dispatch contract), **DD-090** (7-value canonical
status vocabulary), **DD-100** (SP-side idempotent-create is a hard
requirement, not best-effort), **DD-110** (node-set key resolved via
`ClusterTemplates/Get`, single-node-set templates only), **DD-111** (unknown
`template_id` maps to `400`, not `404`), **DD-112** (`CLUSTER_STATE_UNSPECIFIED`
maps to `PROGRESSING`, not `FAILED`), **DD-113** (`POST /clusters` is
schema-optional on `id`/`spec` for AEP-133 compliance), and **DD-114**
(`check-aep` added to `make check`'s prerequisites).

---

## 8. Spec Clarifications

### SC-M3-001: `ClusterState` now defines `DELETING`/`DELETE_FAILED`, but nothing upstream sets them yet — 404-based `DELETED` detection remains correct in practice

**Related requirements:** REQ-STATUS-020, REQ-DELETE-040

[`cluster_type.proto`](https://github.com/osac-project/fulfillment-service/blob/73ae26e8cb0a476d4b035b18776603f60a361ed9/proto/public/osac/public/v1/cluster_type.proto#L237-L253)
defines `CLUSTER_STATE_DELETING = 4` and `CLUSTER_STATE_DELETE_FAILED = 5`,
but `osac-operator`'s
[`feedback_controller.go`](https://github.com/osac-project/osac-operator/blob/065c4fd420e367ddb54bf0f63c64315c27fd87a9/internal/controller/feedback_controller.go#L262-L272,347-350)
has literal `// TODO` stubs for both, and `private_clusters_server.go`'s
`Delete()` performs no state transition before the generic DAO delete.
`Clusters/Get`/`List` continue returning the object until OSAC's DAO
archives the record once no finalizers remain — deletion is asynchronous at
the API level, and a cluster can disappear from OSAC's API before the
underlying teardown actually finishes. REQ-STATUS-020's
`DELETING`/`DELETE_FAILED` mapping rules are therefore forward-compatible
dead code today — do not expect a `TC-I-*` to observe them without a fake
OSAC server that fabricates a state no real deployment produces yet.

### SC-M3-002: Cluster/generic schema verified directly — no conflicts with PR #96's field-mapping table; response schema deliberately does not copy the enhancement doc's illustrative example verbatim

**Related requirements:** REQ-CREATE-010, REQ-CREATE-020, REQ-GET-010

[`service-type-definitions.md`](https://github.com/dcm-project/enhancements/blob/main/enhancements/service-type-definitions/service-type-definitions.md#L103-L114,386-427)
marks `id`/`status`/`path`/`status_message`/`create_time`/`update_time` as
`ReadOnly: Yes` on the generic schema — consistent with `control-plane`
forwarding `id` as a query parameter rather than a request-body field
(DD-080).

The enhancement doc's example response shows `nodes: {control_plane:
{ready, total}, worker: {ready, total}}`, but OSAC's actual
[`ClusterStatus`/`ClusterNodeSet`](https://github.com/osac-project/fulfillment-service/blob/73ae26e8cb0a476d4b035b18776603f60a361ed9/proto/public/osac/public/v1/cluster_type.proto)
proto types have no `ready` count field and no control-plane node-set
bucket (Hosted Control Planes hide it entirely). This milestone's `Cluster`
response schema echoes OSAC's `status.node_sets` map directly instead
(`{<key>: {host_type, size}}`).

`version` is echoed on Create's response only (from the request's own
`spec.version`, no OSAC round-trip) and omitted on Get/List — the
enhancement's [Version Translation](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#version-translation)
section documents only a one-directional DCM→OSAC mapping, with no reverse
translation from `release_image` required in scope.

### SC-M3-003: Status mapper's `Unavailable`/`NotFound` rules are async-only in this milestone — synchronous Create/Get/List always resolve those two gRPC outcomes as HTTP errors first

**Related requirements:** REQ-STATUS-010, REQ-STATUS-020, REQ-GET-040, REQ-ERR-010

REQ-STATUS-020's rules 1 (`Unavailable`/`DeadlineExceeded` → `UNAVAILABLE`)
and 2 (`NotFound` → `DELETED`) are part of the mapper's own contract,
exercised directly as pure-function unit tests (AC-STATUS-010/AC-STATUS-030,
TC-U-240/242), but are not reachable through this milestone's synchronous
Create/Get/List handlers: REQ-GET-040 intercepts a `NotFound` from
`Clusters/Get` into HTTP `404`, and REQ-ERR-010 intercepts `Unavailable`
into HTTP `502`, before either outcome would reach the mapper —
AC-GET-030 already proves this (the response is `404`, never `200` with
`status: "DELETED"`). This mirrors the established DCM-wide convention that
the canonical status vocabulary's `DELETED`/`UNAVAILABLE` values belong to
the async status-polling/CloudEvents channel, not synchronous request
bodies: [`service-provider-status-reporting.md`](https://github.com/dcm-project/enhancements/blob/main/enhancements/state-management/service-provider-status-reporting.md)
describes only an async publish model, `osac-sp.md` documents synchronous
API errors and status-polling in separate sections, and
`acm-cluster-service-provider`'s spec states the rule explicitly for this
exact case ("CloudEvents only — API returns 404"). Rules 1/2 become
reachable only once Milestone 5 adds the async status-polling loop; until
then they are forward-compatible dead code for the synchronous surface —
the same treatment SC-M3-001 already gives `DELETING`/`DELETE_FAILED`.

### SC-M3-004: `node_sets` keys are per-template, not `template_id` — and this milestone only supports single-node-set templates

**Related requirements:** REQ-CREATE-080, REQ-CREATE-090, REQ-CREATE-100

The Field Mapping table originally assumed `spec.node_sets[key]`'s `key`
equals `provider_hints.osac.template_id`. Direct verification against
[`cluster_template_type.proto`](https://github.com/osac-project/fulfillment-service/blob/73ae26e8cb0a476d4b035b18776603f60a361ed9/proto/public/osac/public/v1/cluster_template_type.proto)
and `private_clusters_server.go`'s node-set validation
(osac-project/fulfillment-service) shows this is wrong: node-set keys are
arbitrary, per-template strings defined by whoever authored the template
(a test fixture uses template ID `"my-template-id"` with node-set keys
`"compute"`/`"gpu"` — never the template's own `id`), discoverable only via
`ClusterTemplates/Get`/`List`. OSAC's own CLI (`osac scale --node-set
<name>`) and UI (`useClusterTemplate`) resolve the key the same way before
referencing a node-set by name — there is no shortcut.

This also surfaced an unresolved sizing-model question: DCM's generic
schema (§1) carries a single `nodes.worker.count`, but a template may
define more than one node-set key (e.g. a template with separate
`"compute"`/`"gpu"` worker pools). The `osac-sp` enhancement never
introduces a second sizing dimension or a `provider_hints.osac.node_set`
hint — its Drawbacks section frames sizing as one coarse dimension tied to
whichever discrete host types the provisioned OSAC templates expose,
implying DCM catalog admins are expected to select single-worker-node-set
templates. Per DD-110, this milestone enforces that assumption rather than
guessing: the SP resolves the node-set key via `ClusterTemplates/Get` and
rejects (`400`) any template whose `node_sets` map doesn't have exactly one
key. Multi-node-set templates require an enhancement-doc change (a new
provider hint) to size correctly — out of scope here. A `template_id` that
doesn't resolve at all (`ClusterTemplates/Get` returns `NotFound`) is a
related but separate failure mode, also `400` rather than `404` — see
DD-111.

---

## 9. Requirement ID Index

| Prefix | Topic | Count |
|--------|-------|-------|
| REQ-CREATE-NNN | 4.1: Cluster Create | 10 |
| REQ-GET-NNN | 4.2: Cluster Get | 4 |
| REQ-LIST-NNN | 4.3: Cluster List | 4 |
| REQ-DELETE-NNN | 4.4: Cluster Delete | 4 |
| REQ-STATUS-NNN | 4.5: Status Mapping | 3 |
| REQ-ERR-NNN | 4.6: Error Mapping | 3 |
| **Total** | | **28** |

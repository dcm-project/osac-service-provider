# Specification: OSAC Service Provider — Milestone 4 (VM CRUD)

## 1. Overview

Milestone 4 per [issue #1](https://github.com/dcm-project/osac-service-provider/issues/1)'s
suggested delivery milestones: implement the four VM CRUD REST endpoints,
translating `control-plane`-dispatched requests into
`osac.public.v1.ComputeInstances` gRPC calls — the VM counterpart to
Milestone 3's Cluster CRUD.

- `POST /api/v1alpha1/vms` — Create (idempotent on `id`)
- `GET /api/v1alpha1/vms` — List
- `GET /api/v1alpha1/vms/{vmId}` — Get
- `DELETE /api/v1alpha1/vms/{vmId}` — Delete (404-tolerant)

**This spec covers VM CRUD only.** Explicitly out of scope:

- Async status-polling + CloudEvents/NATS publishing back to `control-plane`
  — Milestone 5 (same deferral Milestone 3 made; no messaging dependency
  exists in this repo yet).
- Best-fit `instance_type` resolution from raw `vcpu`/`memory` values — a
  required `provider_hints.osac.instance_type` is a hard requirement instead
  (DD-122); revisit only if a documented need emerges.
- `InstanceTypes`/`SecurityGroups` CRUD or admin management — this SP only
  ever *references* an `instance_type` by name and *creates* the one default
  `VirtualNetwork`/`Subnet` pair it needs (§4.5); it never manages the
  `InstanceTypes` catalog itself.
- Multi-NIC / caller-supplied network configuration — DCM's VM schema has no
  networking concept at all (confirmed directly against
  [`service-type-definitions.md`](https://github.com/dcm-project/enhancements/blob/main/enhancements/service-type-definitions/service-type-definitions.md#virtual-machine));
  every VM gets exactly one, SP-managed default NIC (§4.5).

**Reference documents:**

- [OSAC SP Enhancement](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md) —
  [API Endpoints](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#api-endpoints),
  [VM Sizing](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#vm-sizing),
  [Default Network Provisioning](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#default-network-provisioning),
  [Idempotent Creation](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#idempotent-creation),
  [Ownership Tracking](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#ownership-tracking),
  [Status Mapping — VM Status](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md#status-mapping-osac-to-dcm) —
  **PR [#100](https://github.com/dcm-project/enhancements/pull/100)**
  (corrective fix to VM Sizing, landed ahead of this spec — see DD-122)
  supersedes the merged [PR #96](https://github.com/dcm-project/enhancements/pull/96)'s
  "keep direct `cores`/`memory_gib` mapping" resolution.
- [`dcm-project/control-plane`](https://github.com/dcm-project/control-plane)'s
  actual outbound dispatch code — same source Milestone 3 verified against
  (`service_type_instance.go`/`convert.go`) — unchanged for `vm` vs.
  `cluster` service types; both dispatch identically.
- [Virtual Machine Schema](https://github.com/dcm-project/enhancements/blob/main/enhancements/service-type-definitions/service-type-definitions.md#virtual-machine)
  and [Generic Service Schema](https://github.com/dcm-project/enhancements/blob/main/enhancements/service-type-definitions/service-type-definitions.md#generic-service)
- [Service Provider Status Reporting — VM Status](https://github.com/dcm-project/enhancements/blob/main/enhancements/state-management/service-provider-status-reporting.md#vm-status) —
  the canonical 8-value status vocabulary (§4.6), **distinct** from Cluster's
  7-value vocabulary (DD-121)
- OSAC public protos, already vendored in Milestone 2:
  [`compute_instances_service.proto`/`compute_instance_type.proto`](https://github.com/osac-project/fulfillment-service/tree/73ae26e8cb0a476d4b035b18776603f60a361ed9/proto/public/osac/public/v1),
  [`subnets_service.proto`/`subnet_type.proto`](https://github.com/osac-project/fulfillment-service/tree/73ae26e8cb0a476d4b035b18776603f60a361ed9/proto/public/osac/public/v1),
  [`virtual_networks_service.proto`/`virtual_network_type.proto`](https://github.com/osac-project/fulfillment-service/tree/73ae26e8cb0a476d4b035b18776603f60a361ed9/proto/public/osac/public/v1) —
  no new vendoring needed. Every field/behavior claim below was re-verified
  directly against `fulfillment-service`'s current `main`
  ([`c4110b2`](https://github.com/osac-project/fulfillment-service/blob/c4110b28a14d4a3b3926ae5360e2cd59c15430d5)),
  not the vendored commit alone, given DD-122's finding that the vendored
  commit already lagged the enhancement doc's own prose in one respect.
- [Milestone 1 spec](./osac-sp.spec.md) (`internal/httperror`, DD-070) and
  [Milestone 2 spec](./osac-sp-m2-grpc-client-generation.spec.md)
  (`Bootstrap.Conn()`) — both extended, not replaced. Milestone 3's spec
  (`./osac-sp-m3-cluster-crud.spec.md`) is a **structural** reference (same
  patterns applied to a different resource type) but not a code dependency —
  Milestone 4 branched from `main` before Milestone 3 merged (see DD-126),
  so `internal/cluster`/`internal/handlers/cluster` do not exist on this
  branch.
- [Design Decisions](../decisions/osac-sp.decisions.md) — DD-120 through
  DD-126 (new, this milestone)

---

## 2. Architecture

Extends Milestone 1's `internal/apiserver`/`internal/httperror` and
Milestone 2's `internal/osac.Bootstrap.Conn()`. Three new packages:

- **`internal/vm`** — business logic. Constructs
  `publicv1.NewComputeInstancesClient(bootstrap.Conn())`,
  `publicv1.NewSubnetsClient(bootstrap.Conn())`, and
  `publicv1.NewVirtualNetworksClient(bootstrap.Conn())` (per M2's DD-020
  pattern) and exposes `Create`/`Get`/`List`/`Delete` methods operating on
  the SP's own `VirtualMachine` type, encapsulating DCM↔OSAC field
  translation, the idempotent-create retry, default network provisioning
  (§4.5), and the VM-specific status mapper (§4.6).
- **`internal/handlers/vm`** — thin `StrictServerInterface` implementations
  for the 4 REST operations, delegating to `internal/vm` and reusing
  `internal/httperror`/`internal/grpcerror` for every non-2xx response
  (§4.7).
- **`internal/grpcerror`** (new, shared) — `Classify(err error) (status
  int, errType v1alpha1.ErrorType, title string)`, extracted per DD-126 so
  `internal/handlers/vm` does not duplicate the gRPC-code mapping table a
  second time. `internal/handlers/cluster` (Milestone 3, landing separately)
  should adopt this in a follow-up.

```
control-plane (synchronous, direct REST — same contract as Milestone 3, DD-120)
        |
        | POST /api/v1alpha1/vms?id=X   {"spec": {...}}
        | DELETE /api/v1alpha1/vms/X
        | (GET/List/Update never called — control-plane serves those
        |  from its own Postgres store)
        v
+------------------------------------------------------------+
|              internal/handlers/vm                           |
|   (StrictServerInterface: Create/Get/List/Delete)            |
|         |                                                     |
|         v                                                     |
|              internal/vm                                     |
|   Create: resolve default subnet (4.5), translate spec ->     |
|           ComputeInstanceSpec, set ownership labels,          |
|           ComputeInstances/Create, AlreadyExists->Get         |
|   Get:    ComputeInstances/Get, status mapper (4.6)            |
|   List:   ComputeInstances/List (CEL ownership filter,         |
|           offset/limit)                                       |
|   Delete: ComputeInstances/Delete, NotFound treated as success |
|         |                                                     |
|         v                                                     |
|   publicv1.New{ComputeInstances,Subnets,VirtualNetworks}      |
|   Client(bootstrap.Conn())  <-- M2                            |
+------------------------------------------------------------+
        |
        v
   osac.public.v1.{ComputeInstances,Subnets,VirtualNetworks}
   (OSAC fulfillment service)
```

No changes to `internal/config`, `internal/registration`, or `internal/osac`
— this milestone only adds business-logic and handler packages on top of
Milestone 2's already-authenticated shared connection.

---

## 3. Topic Dependency Graph

| # | Topic                        | Prefix  | Depends On                                                             |
| - | ----------------------------- | ------- | ------------------------------------------------------------------------ |
| 1 | VM Create                    | VMCREATE | Topic 5 (Default Network), Topic 6 (Status Mapping), Topic 7 (Error Mapping), Milestone 2 |
| 2 | VM Get                       | VMGET    | Topic 6, Topic 7, Milestone 2                                            |
| 3 | VM List                      | VMLIST   | Topic 6, Topic 7, Milestone 2                                            |
| 4 | VM Delete                    | VMDELETE | Topic 7, Milestone 2                                                     |
| 5 | Default Network Provisioning | VMNET    | Topic 7, Milestone 2 (`Subnets`/`VirtualNetworks` clients)                |
| 6 | Status Mapping                | VMSTATUS | Milestone 2 (`publicv1.ComputeInstanceState` type)                        |
| 7 | Error Mapping                 | VMERR    | Milestone 1 Topic 4.1 (`internal/httperror`, DD-070); introduces `internal/grpcerror` (DD-126) |

```
Topic 7: Error Mapping   ---+--> Topic 1: Create (via Topic 5)
Topic 5: Default Network -->/
Topic 6: Status Mapping  ---+--> Topic 2: Get
                          \--+--> Topic 3: List
                           \----> Topic 4: Delete (Topic 7 only)
```

---

## 4. Topic Specifications

### 4.1 VM Create

#### Overview

`POST /api/v1alpha1/vms` accepts an optional-schema `id` query parameter
(runtime-required, DD-125) and a JSON body `{"spec": {...}}` where `spec`
follows the
[VM schema](https://github.com/dcm-project/enhancements/blob/main/enhancements/service-type-definitions/service-type-definitions.md#virtual-machine)
(`vcpu.count`, `memory.size`, `storage.disks[]`, `guest_os.type`,
`access.ssh_public_key`) plus the common `metadata`/`provider_hints`
fields. The handler translates this into OSAC's `ComputeInstanceSpec` and
calls `ComputeInstances/Create`.

**Field Mapping (DCM request → OSAC `ComputeInstanceSpec`):**

| DCM Field                                | OSAC Field                          | Notes                                                                          |
| ------------------------------------------ | -------------------------------------- | --------------------------------------------------------------------------------- |
| `id` (query param)                        | `ComputeInstance.id`                  | Sets OSAC's own identifier — REQ-VMCREATE-070 (idempotency)                      |
| `spec.vcpu.count`                         | *(not sent)*                          | Informational only — DD-122                                                      |
| `spec.memory.size`                        | *(not sent)*                          | Informational only — DD-122                                                      |
| `spec.storage.disks[]` (`name == "boot"`) | `spec.boot_disk.size_gib`             | Parsed per DD-123. Exactly one disk named `boot` is required (REQ-VMCREATE-060)  |
| `spec.storage.disks[]` (all others)       | `spec.additional_disks[].size_gib`    | Parsed per DD-123. Disk `name` is **not** preserved — OSAC's `ComputeInstanceDisk` has no name field (SC-M4-002) |
| `spec.guest_os.type`                      | `spec.image.source_ref`               | `image.source_type` is a fixed constant, `"registry"` — see SC-M4-002/DD-128      |
| `spec.access.ssh_public_key`               | `spec.ssh_public_key`                 | Optional passthrough                                                             |
| `spec.metadata.name`                       | `metadata.name`                       |                                                                                     |
| `spec.metadata.labels`                     | `metadata.labels` (merged with ownership labels, REQ-VMCREATE-050) |                                                            |
| `spec.provider_hints.osac.template_id`     | `spec.template`                       | Required                                                                          |
| `spec.provider_hints.osac.instance_type`   | `spec.instance_type`                  | **Required** — DD-122, no fallback                                               |
| `spec.provider_hints.osac.windows`         | `spec.is_windows`                     | Optional, defaults to `false`/omitted. Named `windows`, not `is_windows`, per AEP-140 (boolean properties omit the `is` prefix) — this is the SP's own hint schema, not a literal passthrough of OSAC's field name |
| *(SP-resolved, §4.5)*                      | `spec.network_attachments`            | Always exactly one entry — REQ-VMNET-040                                          |

**Provider Hints (`provider_hints.osac`):** `template_id` (string,
required); `instance_type` (string, required — DD-122); `windows`
(bool, optional — AEP-140 naming).

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-VMCREATE-010 | The SP MUST implement `POST /api/v1alpha1/vms`, accepting an optional-schema `id` query parameter and a request body `{"spec": {...}}` — matching `control-plane`'s actual outbound dispatch shape. "Required" here is a runtime/behavioral requirement (REQ-VMCREATE-060), not the OpenAPI schema's `required` keyword — both `id` and the body's `spec` property are schema-optional for AEP-133 compliance | MUST | DD-125 |
| REQ-VMCREATE-020 | The SP MUST translate the request per the Field Mapping table above and call `osac.public.v1.ComputeInstances/Create` with `ComputeInstance.id` set to the `id` query parameter's exact value | MUST | |
| REQ-VMCREATE-030 | Exactly one entry of `spec.storage.disks[]` MUST have `name == "boot"`; its `capacity` (parsed per REQ-VMCREATE-040) MUST become `spec.boot_disk.size_gib`. Every other entry's parsed `capacity` MUST become one `spec.additional_disks[]` entry, in the same relative order as the request, with the boot disk excluded | MUST | |
| REQ-VMCREATE-040 | Disk `capacity` strings MUST be parsed per DD-123 (`GB`/`GiB` treated as GiB directly, `TB`/`TiB` ×1024, `MB`/`MiB` ÷1024 rounded up, case-insensitive unit matching); an unparseable string, unrecognized unit, or non-positive value MUST be rejected per REQ-VMCREATE-060 | MUST | DD-123 |
| REQ-VMCREATE-050 | The SP MUST set three ownership labels on `metadata.labels` for every Create call: `dcm.io/managed-by="dcm"`, `dcm.io/instance-id="<id>"`, `dcm.io/service-type="vm"` — merged with, not replacing, any caller-supplied `spec.metadata.labels` | MUST | |
| REQ-VMCREATE-060 | A request missing the `id` query parameter, missing the body's `spec` property entirely, missing `spec.provider_hints.osac.template_id` or `spec.provider_hints.osac.instance_type`, missing a disk named `boot`, or containing an unparseable disk `capacity` (REQ-VMCREATE-040), MUST return `400 Bad Request` via the shared error-mapping topic (§4.7) without calling OSAC | MUST | DD-122 |
| REQ-VMCREATE-070 | If `ComputeInstances/Create` returns gRPC `AlreadyExists` for the given `id`, the SP MUST call `ComputeInstances/Get(id)` and return **that** object's current state as a successful Create response — MUST NOT surface `AlreadyExists` to the caller as an error | MUST | Mirrors DD-100 (Milestone 3) |
| REQ-VMCREATE-080 | A successful Create response MUST be `201 Created` with a body whose top-level `id` and `status` fields are always populated (never omitted/null) | MUST | |
| REQ-VMCREATE-090 | `spec.network_attachments` MUST always be set to exactly the single entry resolved by §4.5 (Default Network Provisioning) — the SP MUST NOT accept or forward any caller-supplied network configuration (DCM's VM schema defines none) | MUST | REQ-VMNET-040 |

#### Configuration Introduced

None — reuses Milestone 2's `Bootstrap.Conn()`.

#### Acceptance Criteria

##### AC-VMCREATE-010: Create translates and dispatches the full field set correctly

- **Validates:** REQ-VMCREATE-010, REQ-VMCREATE-020
- **Given** a request `POST /api/v1alpha1/vms?id=X` with body
  `{"spec":{"vcpu":{"count":4},"memory":{"size":"8GB"},"storage":{"disks":[{"name":"boot","capacity":"100GB"}]},"guest_os":{"type":"rhel-9"},"metadata":{"name":"foo"},"provider_hints":{"osac":{"template_id":"default-vm","instance_type":"standard-4-16"}}}}`
- **When** the handler processes it against a fake `bufconn`-backed
  `ComputeInstancesServer` (and fake `Subnets`/`VirtualNetworks` servers
  already holding a `READY` default subnet, §4.5)
- **Then** the fake's recorded `ComputeInstances/Create` call has
  `ComputeInstance.id` exactly `"X"`, `spec.template` exactly
  `"default-vm"`, `spec.instance_type` exactly `"standard-4-16"`,
  `spec.image.source_ref` exactly `"rhel-9"`, `spec.boot_disk.size_gib`
  exactly `100`, `spec.cores`/`spec.memory_gib` **unset** (proving
  `vcpu.count`/`memory.size` were never translated), and
  `metadata.name` exactly `"foo"`

##### AC-VMCREATE-020: Ownership labels are set exactly, merged with caller labels

- **Validates:** REQ-VMCREATE-050
- **Given** a Create request with `id=X` and `spec.metadata.labels={"team":"platform"}`
- **When** processed
- **Then** the fake's recorded `metadata.labels` equals exactly `{"team":"platform","dcm.io/managed-by":"dcm","dcm.io/instance-id":"X","dcm.io/service-type":"vm"}`

##### AC-VMCREATE-030: Non-boot disks translate to `additional_disks`, boot disk to `boot_disk`, both size-parsed

- **Validates:** REQ-VMCREATE-030, REQ-VMCREATE-040
- **Given** `spec.storage.disks = [{"name":"data","capacity":"2TB"},{"name":"boot","capacity":"100GB"}]`
- **When** processed
- **Then** the fake's recorded `spec.boot_disk.size_gib` is exactly `100`
  and `spec.additional_disks` has exactly one entry with `size_gib` exactly
  `2048` (`2TB` × 1024) — proving both the boot/non-boot split and the unit
  conversion, regardless of the disks' order in the request

##### AC-VMCREATE-040: Missing boot disk is rejected before calling OSAC

- **Validates:** REQ-VMCREATE-060
- **Given** `spec.storage.disks = [{"name":"data","capacity":"100GB"}]` (no disk named `boot`)
- **When** processed
- **Then** the response is `400 Bad Request` and the fake OSAC server recorded zero `ComputeInstances/Create` calls

##### AC-VMCREATE-050: Unparseable disk capacity is rejected before calling OSAC

- **Validates:** REQ-VMCREATE-040, REQ-VMCREATE-060
- **Given**, in turn, `capacity` values `"100"` (no unit), `"100XB"` (unrecognized unit), and `"-5GB"` (non-positive)
- **When** each is submitted on the boot disk
- **Then** each returns `400 Bad Request` with zero `ComputeInstances/Create` calls made

##### AC-VMCREATE-060: Missing `provider_hints.osac.instance_type` is rejected before calling OSAC

- **Validates:** REQ-VMCREATE-060, DD-122
- **Given** a Create request with `provider_hints.osac.template_id` set but `instance_type` omitted
- **When** processed
- **Then** the response is `400 Bad Request` and the fake OSAC server recorded zero `ComputeInstances/Create` calls — proving the SP never falls back to a direct `cores`/`memory_gib` mapping

##### AC-VMCREATE-070: Idempotent Create — retried request with the same `id` returns the existing resource, not a duplicate or an error

- **Validates:** REQ-VMCREATE-070
- **Given** a Create request with `id=X` has already succeeded once (fake OSAC now holds exactly one `ComputeInstance` with `id=X`, `status.state=COMPUTE_INSTANCE_STATE_STARTING`)
- **When** a second Create request arrives with the same `id=X` and an equivalent `spec`
- **Then** the fake's second `ComputeInstances/Create` call returns `AlreadyExists`, the SP calls `ComputeInstances/Get("X")`, the HTTP response is `201 Created` with `id` exactly `"X"` and `status` exactly `"PROVISIONING"` (from the Get), and the fake OSAC server's instance count remains exactly `1`

##### AC-VMCREATE-080: Every Create sets exactly one network attachment, resolved by §4.5

- **Validates:** REQ-VMCREATE-090
- **Given** a fake `Subnets/List` returning one `READY` subnet with `id="subnet-abc"`
- **When** a Create request is processed with no networking fields in the DCM request body at all
- **Then** the fake's recorded `ComputeInstances/Create` call has `spec.network_attachments` with exactly one entry, `subnet` exactly `"subnet-abc"`, `security_groups` empty

#### Dependencies

Depends on Topic 5 (Default Network Provisioning), Topic 6 (Status
Mapping, for the response `status` field), Topic 7 (Error Mapping), and
Milestone 2's `Bootstrap.Conn()`.

---

### 4.2 VM Get

#### Overview

`GET /api/v1alpha1/vms/{vmId}` calls `ComputeInstances/Get(vmId)` (the same
value as `id` — no local mapping store needed, same as Cluster) and maps
the result via the shared VM status mapper (§4.6).

**Response extension (SP-specific, not part of DCM's generic VM
contract):** the response echoes `status.internal_ip_address`/
`external_ip_address` directly from OSAC when known (empty string
otherwise) — informational for callers that query the SP directly, same
rationale as Milestone 3's Cluster response echoing `status.node_sets`
(SC-M3-002) rather than fabricating a DCM-schema field that doesn't exist.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-VMGET-010 | The SP MUST implement `GET /api/v1alpha1/vms/{vmId}`, calling `ComputeInstances/Get(vmId)` and mapping the result per the shared status mapper (§4.6) | MUST | |
| REQ-VMGET-020 | `ComputeInstances/Get` returning gRPC `NotFound` MUST map to HTTP `404` via the shared error-mapping topic (§4.7) | MUST | |
| REQ-VMGET-030 | The response MUST include `internal_ip_address`/`external_ip_address` fields echoing `status.internal_ip_address`/`status.external_ip_address` exactly (empty string when OSAC returns empty) | MUST | SP-specific extension |

#### Configuration Introduced

None.

#### Acceptance Criteria

##### AC-VMGET-010: A running VM's IP addresses are echoed exactly

- **Validates:** REQ-VMGET-010, REQ-VMGET-030
- **Given** a fake `ComputeInstances/Get` returning `status.state=COMPUTE_INSTANCE_STATE_RUNNING`, `status.internal_ip_address="10.200.1.5"`, `status.external_ip_address=""`
- **When** `GET /api/v1alpha1/vms/{id}` is called
- **Then** the response is `200 OK` with `status` exactly `"RUNNING"`, `internal_ip_address` exactly `"10.200.1.5"`, `external_ip_address` exactly `""`

##### AC-VMGET-020: Nonexistent VM returns 404

- **Validates:** REQ-VMGET-020
- **Given** a fake `ComputeInstances/Get` returning gRPC `NotFound`
- **When** `GET` is called
- **Then** the response is `404 Not Found` (RFC 9457, `type` exactly `.../not-found`)

#### Dependencies

Depends on Topic 6 (Status Mapping), Topic 7 (Error Mapping), Milestone 2.

---

### 4.3 VM List

#### Overview

`GET /api/v1alpha1/vms` calls `ComputeInstances/List` with a CEL filter
scoping results to this SP's own managed resources, and translates DCM's
`max_page_size`/`page_token` query parameters to/from OSAC's
`limit`/`offset` pagination — identical mechanics to Milestone 3's Cluster
List.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-VMLIST-010 | The SP MUST call `ComputeInstances/List` with CEL filter `this.metadata.labels["dcm.io/managed-by"] == "dcm"` on every call | MUST | Ownership Tracking |
| REQ-VMLIST-020 | The SP MUST translate `max_page_size` (query, default `50` when omitted) to OSAC's `limit`, and encode/decode `page_token` as an opaque wrapper around OSAC's `offset` | MUST | |
| REQ-VMLIST-030 | The response MUST be the AEP-132 pagination wrapper `{"results": [...], "next_page_token": "..."}`, with each entry mapped via the same status mapper as Get (§4.6), including the same `internal_ip_address`/`external_ip_address` echo as Get (REQ-VMGET-030) — unlike Cluster's `kubeconfig`, this costs no extra RPC since it's already present on each `ComputeInstancesListResponse` item | MUST | |
| REQ-VMLIST-040 | `next_page_token` MUST be empty/absent exactly when OSAC's `List` response indicates no further results | MUST | |

#### Configuration Introduced

None.

#### Acceptance Criteria

##### AC-VMLIST-010: List applies the ownership filter and default page size, returns exact field values

- **Validates:** REQ-VMLIST-010, REQ-VMLIST-030
- **Given** a fake `ComputeInstances/List` that records its request and returns 2 instances with known `id`/`status.state`/IP values
- **When** `GET /api/v1alpha1/vms` is called with no query parameters
- **Then** the fake recorded `filter` exactly equal to `this.metadata.labels["dcm.io/managed-by"] == "dcm"` and `limit` exactly `50`, and the response's `results` array has exactly 2 entries whose `id`/`status`/`internal_ip_address` fields equal the fake's canned values exactly

##### AC-VMLIST-020: `page_token` round-trips through OSAC's `offset` correctly

- **Validates:** REQ-VMLIST-020, REQ-VMLIST-040
- **Given** a fake `ComputeInstances/List` that returns a response indicating a next `offset` of `50` on the first call
- **When** the response's `next_page_token` is fed back into a second `GET /api/v1alpha1/vms?page_token=...` call
- **Then** the fake's second recorded `List` call has `offset` exactly `50`

#### Dependencies

Depends on Topic 6 (Status Mapping), Topic 7 (Error Mapping), Milestone 2.

---

### 4.4 VM Delete

#### Overview

`DELETE /api/v1alpha1/vms/{vmId}` calls `ComputeInstances/Delete`. Per
`control-plane`'s own dispatcher tolerance for a `404` (same as Cluster,
DD-120) — and per DD-120's direct verification that `ComputeInstances/Delete`
is fully implemented today with **no** teardown-ambiguity gap (unlike
Cluster's tracked `OSAC-1586`/`OSAC-1391`) — the SP mirrors that tolerance:
a `NotFound` from OSAC is success, not an error.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-VMDELETE-010 | The SP MUST implement `DELETE /api/v1alpha1/vms/{vmId}`, calling `ComputeInstances/Delete(vmId)` | MUST | DD-120 |
| REQ-VMDELETE-020 | `ComputeInstances/Delete` returning gRPC `NotFound` MUST be treated as success — response `204 No Content`, **not** `404` | MUST | Mirrors REQ-DELETE-020 (Milestone 3) |
| REQ-VMDELETE-030 | Any other gRPC error from `ComputeInstances/Delete` MUST map through the shared error-mapping topic (§4.7) | MUST | |
| REQ-VMDELETE-040 | The SP MUST return `204` as soon as OSAC acknowledges the delete request — it MUST NOT poll/wait for the VM to actually disappear | MUST | |

#### Configuration Introduced

None.

#### Acceptance Criteria

##### AC-VMDELETE-010: Successful delete returns 204

- **Validates:** REQ-VMDELETE-010, REQ-VMDELETE-040
- **Given** a fake `ComputeInstances/Delete` that succeeds
- **When** `DELETE /api/v1alpha1/vms/{id}` is called
- **Then** the response is `204 No Content` with an empty body, returned without any subsequent `ComputeInstances/Get` call being made to confirm teardown

##### AC-VMDELETE-020: Deleting an already-deleted VM is idempotent over two real HTTP requests

- **Validates:** REQ-VMDELETE-020
- **Given** a fake OSAC server holding exactly one instance with `id=X`
- **When** `DELETE /api/v1alpha1/vms/X` is issued twice in sequence over real HTTP
- **Then** both requests receive `204 No Content`

##### AC-VMDELETE-030: A genuine OSAC failure during delete is not swallowed

- **Validates:** REQ-VMDELETE-030
- **Given** a fake `ComputeInstances/Delete` returning gRPC `Unavailable`
- **When** `DELETE` is called
- **Then** the response is `502 Bad Gateway`, not `204`

#### Dependencies

Depends on Topic 7 (Error Mapping), Milestone 2.

---

### 4.5 Default Network Provisioning

#### Overview

`osac.public.v1.ComputeInstances/Create` requires at least one entry in
`spec.network_attachments`, each referencing a `Subnet` in `READY` state.
DCM's VM schema has no networking concept, so on every Create the SP
resolves (or provisions) a default subnet and attaches it automatically —
statelessly, per DD-124, not via the enhancement doc's cached per-tenant
mapping store.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-VMNET-010 | Before every `ComputeInstances/Create` call, the SP MUST call `Subnets/List` with CEL filter `this.metadata.labels["dcm.io/managed-by"] == "dcm" && this.metadata.labels["dcm.io/service-type"] == "vm-default-network"` — scoped to both labels, not managed-by alone, so a differently-purposed DCM-managed subnet is never mistaken for the shared default one. If at least one result is returned, the SP MUST use the first result's `id` as the resolved subnet and MUST NOT create a new `VirtualNetwork`/`Subnet` | MUST | DD-124 |
| REQ-VMNET-020 | If `Subnets/List` returns zero results, the SP MUST call `VirtualNetworks/Create` with `spec.ipv4_cidr="10.200.0.0/16"`, `spec.capabilities.enable_ipv4=true`, `spec.network_class` omitted, and **two** ownership labels: `dcm.io/managed-by="dcm"` and `dcm.io/service-type="vm-default-network"`. **No** `dcm.io/instance-id` label is set — unlike Create's per-VM ownership labels (REQ-VMCREATE-050), this network is shared across every VM, not owned by the one whose Create call happened to trigger its provisioning, so there is no single VM id to attribute it to (see SC-M4-001) | MUST | DD-124 |
| REQ-VMNET-030 | After a successful `VirtualNetworks/Create`, the SP MUST call `Subnets/Create` with `spec.virtual_network` set to the new `VirtualNetwork`'s `id`, `spec.ipv4_cidr="10.200.1.0/24"`, the same two ownership labels as REQ-VMNET-020, and `metadata.annotations["osac.openshift.io/owner-reference"]` set to the `VirtualNetwork`'s `id` | MUST | DD-124 |
| REQ-VMNET-040 | The SP MUST poll (`Get`, `500ms` interval, `15s` total timeout, bounded by the request's context) both the new `VirtualNetwork` and `Subnet` until both report `READY`, before using the `Subnet`'s `id`. If the timeout elapses first, the SP MUST return an error that maps to `502 Bad Gateway` (§4.7) and MUST NOT call `ComputeInstances/Create` | MUST | DD-124 |
| REQ-VMNET-050 | The resolved subnet `id` (whether reused via REQ-VMNET-010 or newly provisioned via REQ-VMNET-020..040) MUST be set as `spec.network_attachments[0].subnet` on the subsequent `ComputeInstances/Create` call, with `security_groups` left empty | MUST | REQ-VMCREATE-090 |

#### Configuration Introduced

None — poll interval/timeout are hardcoded constants (DD-124), not new
environment variables.

#### Acceptance Criteria

##### AC-VMNET-010: An existing default subnet is reused, no new network is created

- **Validates:** REQ-VMNET-010
- **Given** a fake `Subnets/List` returning one subnet `id="subnet-existing"`, `status.state=SUBNET_STATE_READY`
- **When** a VM Create request is processed
- **Then** the fake's `VirtualNetworks/Create` and `Subnets/Create` call counters are both exactly `0`, and the eventual `ComputeInstances/Create` call's `network_attachments[0].subnet` is exactly `"subnet-existing"`

##### AC-VMNET-020: No existing subnet — a new VirtualNetwork and Subnet are provisioned with the exact documented shape

- **Validates:** REQ-VMNET-020, REQ-VMNET-030
- **Given** a fake `Subnets/List` returning zero results
- **When** a VM Create request is processed
- **Then** the fake's recorded `VirtualNetworks/Create` call has `spec.ipv4_cidr` exactly `"10.200.0.0/16"`, `spec.network_class` exactly `""` (unset), and `metadata.labels["dcm.io/managed-by"]` exactly `"dcm"`; and the fake's recorded `Subnets/Create` call has `spec.virtual_network` exactly equal to the `VirtualNetwork`'s returned `id`, `spec.ipv4_cidr` exactly `"10.200.1.0/24"`, and `metadata.annotations["osac.openshift.io/owner-reference"]` exactly equal to that same `VirtualNetwork` `id`

##### AC-VMNET-030: The SP polls until both resources report READY before creating the VM

- **Validates:** REQ-VMNET-040
- **Given** a fake `VirtualNetworks/Get` that returns `PENDING` on its first call and `READY` on its second, and a fake `Subnets/Get` that returns `READY` immediately
- **When** a VM Create request is processed (no existing subnet)
- **Then** the fake's `VirtualNetworks/Get` call counter is exactly `2`, and `ComputeInstances/Create` is called only after both report `READY` — proving the SP does not attach a not-yet-`READY` subnet

##### AC-VMNET-040: Provisioning timeout is surfaced as 502, `ComputeInstances/Create` is never called

- **Validates:** REQ-VMNET-040
- **Given** a fake `Subnets/Get` that always returns `PENDING`, with the poll timeout overridden to a short test value (e.g. `50ms`/`10ms` interval, via a constructor option — not the real 15s/500ms constants)
- **When** a VM Create request is processed
- **Then** the response is `502 Bad Gateway` and the fake's `ComputeInstances/Create` call counter is exactly `0`

#### Dependencies

Depends on Topic 7 (Error Mapping), Milestone 2's `Bootstrap.Conn()`.

---

### 4.6 Status Mapping

#### Overview

A single mapping function, shared identically by Create's response, Get,
and List (REQ-VMSTATUS-030), translating OSAC's `ComputeInstanceState` and
the outcome of the gRPC call itself into DCM's canonical **8-value** VM
status vocabulary (DD-121), per
[`service-provider-status-reporting.md#vm-status`](https://github.com/dcm-project/enhancements/blob/main/enhancements/state-management/service-provider-status-reporting.md#vm-status):
`PROVISIONING | RUNNING | STOPPED | FAILED | DELETING | STOPPING | PAUSED |
DELETED`. This is a **separate** vocabulary from Cluster's 7-value one
(DD-121) — do not conflate the two mappers.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-VMSTATUS-010 | The status mapper MUST return exactly one of the 8 canonical values listed above for every Create/Get/List call | MUST | DD-121 |
| REQ-VMSTATUS-020 | The mapper MUST apply this precedence, in order, stopping at the first match: (1) the gRPC call itself failed with `Unavailable`/`DeadlineExceeded` → `FAILED`; (2) the gRPC call returned `NotFound` → `DELETED`; (3) `state == COMPUTE_INSTANCE_STATE_UNSPECIFIED` → `PROVISIONING`; (4) `state == COMPUTE_INSTANCE_STATE_STARTING` → `PROVISIONING`; (5) `state == COMPUTE_INSTANCE_STATE_RUNNING` → `RUNNING`; (6) `state == COMPUTE_INSTANCE_STATE_FAILED` → `FAILED`; (7) `state == COMPUTE_INSTANCE_STATE_DELETING` → `DELETING`; (8) `state == COMPUTE_INSTANCE_STATE_STOPPING` → `STOPPING`; (9) `state == COMPUTE_INSTANCE_STATE_STOPPED` → `STOPPED`; (10) `state == COMPUTE_INSTANCE_STATE_PAUSED` → `PAUSED`; (11) anything else (a future, not-yet-modeled enum value) → `FAILED` (defensive default) | MUST | DD-121, DD-129 — `UNSPECIFIED` is OSAC's proto3 zero-value, observed live to be the normal state for ~3-5s between `ComputeInstance` creation and `osac-operator`'s first reconcile pass, not a genuine anomaly; mapping it to `FAILED` produced a false failure on every VM creation |
| REQ-VMSTATUS-030 | Create's response `status`, Get's `status`, and each List entry's `status` MUST all be computed by the same mapper implementation (no per-handler duplication) | MUST | |

#### Configuration Introduced

None.

#### Acceptance Criteria

##### AC-VMSTATUS-010: Each individual signal maps to its documented value

- **Validates:** REQ-VMSTATUS-010, REQ-VMSTATUS-020
- **Given** each of the following inputs, in turn: gRPC `Unavailable`; gRPC `NotFound`; `state=UNSPECIFIED`; `state=STARTING`; `state=RUNNING`; `state=FAILED`; `state=DELETING`; `state=STOPPING`; `state=STOPPED`; `state=PAUSED`
- **When** the mapper is called with each
- **Then** it returns exactly `FAILED`, `DELETED`, `PROVISIONING`, `PROVISIONING`, `RUNNING`, `FAILED`, `DELETING`, `STOPPING`, `STOPPED`, `PAUSED` respectively — one assertion per input

##### AC-VMSTATUS-020: Connectivity failure and a real 404 are never conflated

- **Validates:** REQ-VMSTATUS-020 (rules 1 vs 2)
- **Given** two separate calls — one where the gRPC call itself fails with `Unavailable`, one where it succeeds and returns `NotFound`
- **When** the mapper is called for each
- **Then** the first returns exactly `FAILED` and the second returns exactly `DELETED` — distinct values, proving `Unavailable` is not silently treated as "gone"

#### Dependencies

Depends on Milestone 2's vendored `publicv1.ComputeInstanceState` type (no
new proto vendoring).

---

### 4.7 Error Mapping

#### Overview

Every gRPC error surfaced by any `ComputeInstances`/`Subnets`/
`VirtualNetworks` RPC (except the two carve-outs below) MUST be mapped to
an HTTP status and an RFC 9457 body, via a new shared package,
`internal/grpcerror` (DD-126), reusing `internal/httperror.WriteResponse`
for the actual write (unchanged from Milestone 1, DD-070).

**Carve-outs (handler-specific, override this table):** Create's
`AlreadyExists` is intercepted per REQ-VMCREATE-070 and never reaches this
mapping. Delete's `NotFound` is intercepted per REQ-VMDELETE-020 and
returns `204`, not `404`. Default Network Provisioning's poll timeout
(REQ-VMNET-040) is surfaced as `502` directly, not via a gRPC error code.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-VMERR-010 | `internal/grpcerror.Classify` MUST map gRPC codes to HTTP status + `v1alpha1.ErrorType` identically to Milestone 3's table: `InvalidArgument`→`400`/`INVALIDARGUMENT`; `Unauthenticated`→`401`/`UNAUTHENTICATED`; `PermissionDenied`→`403`/`PERMISSIONDENIED`; `NotFound`→`404`/`NOTFOUND`; `AlreadyExists`→`409`/`ALREADYEXISTS`; `Unavailable`/`DeadlineExceeded`→`502`/`UNAVAILABLE`; `Internal`/`Unknown`/anything else→`500`/`INTERNAL` | MUST | DD-126 |
| REQ-VMERR-020 | Every error response MUST use `internal/httperror.WriteResponse`, producing `Content-Type: application/problem+json` and a body matching the `v1alpha1.Error` schema | MUST | DD-070 |
| REQ-VMERR-030 | `internal/grpcerror.Classify` MUST be the single implementation consumed by all 4 VM handlers (no per-handler duplication) | MUST | DD-126 |

#### Configuration Introduced

None.

#### Acceptance Criteria

##### AC-VMERR-010: Each gRPC code maps to its documented HTTP status and `type`

- **Validates:** REQ-VMERR-010, REQ-VMERR-020
- **Given**, in turn, fake OSAC responses of gRPC `InvalidArgument`, `Unauthenticated`, `PermissionDenied`, `NotFound` (on Get, not Delete), `Unavailable`, and `Internal`
- **When** each is returned from a `ComputeInstances/Get` call
- **Then** the HTTP responses are exactly `400`/`401`/`403`/`404`/`502`/`500` respectively, each with `Content-Type: application/problem+json` and a decoded body's `type` field matching the documented `v1alpha1.ErrorType` constant exactly

##### AC-VMERR-020: `internal/grpcerror.Classify` produces identical results across handlers

- **Validates:** REQ-VMERR-030
- **Given** a fake OSAC `PermissionDenied` error returned from `ComputeInstances/Get`, `ComputeInstances/List`, and `ComputeInstances/Delete` in three separate calls
- **When** each handler processes its respective request
- **Then** all three produce an identical HTTP status (`403`) and identical `type` value

#### Dependencies

Depends on Milestone 1's `internal/httperror` (DD-070).

---

## 5. Cross-Cutting Concerns

No new cross-cutting concerns. Logging conventions (`osac-sp.spec.md`
§5.1) are unchanged. No new configuration is introduced (§6).

---

## 6. Consolidated Configuration Reference

No new configuration keys. See `osac-sp.spec.md` §6 for the full table
(unchanged by this milestone).

---

## 7. Design Decisions

Design decisions (`DD-NNN`) live in
[`.ai/decisions/osac-sp.decisions.md`](../decisions/osac-sp.decisions.md).
This milestone adds **DD-120** (VM CRUD dispatch contract; `Delete`
verified fully implemented), **DD-121** (8-value VM status vocabulary,
condition-free mapping), **DD-122** (`instance_type` required, no direct
sizing), **DD-123** (disk capacity unit parsing), **DD-124** (stateless
default network provisioning), **DD-125** (AEP-133-compliant `POST
/api/v1alpha1/vms` from the start), and **DD-126** (shared
`internal/grpcerror` package).

---

## 8. Spec Clarifications

### SC-M4-001: Concurrent first-ever VM creates can race on default network provisioning — accepted for v1, not solved here

**Related requirements:** REQ-VMNET-010, REQ-VMNET-020

Unlike `ComputeInstances`/`Clusters`' `id`-based `AlreadyExists` idempotency
(DD-100, Milestone 3), which works because DCM issues a stable identifier
the SP can key a retry-safe create on, `Subnets`/`VirtualNetworks` have no
DCM-issued identifier at all — the SP invents these resources unprompted.
Two concurrent VM Create requests, both arriving before any default subnet
exists yet, can both observe zero results from `Subnets/List`
(REQ-VMNET-010) and both proceed to create a `VirtualNetwork`/`Subnet`
pair, resulting in two "default" networks instead of one. Neither ends up
orphaned or broken — each VM Create that raced simply attaches to whichever
subnet its own provisioning call produced — but subsequent `Subnets/List`
calls will return two results instead of one, and REQ-VMNET-010 picks
"the first result" arbitrarily from then on.

This is a known, accepted limitation for v1, not a defect to fix in this
milestone: DCM's current VM-creation concurrency is low (no evidence of
concurrent-create load in any milestone through M4), and — critically — a
cached-mapping-store design (the enhancement doc's original proposal) has
the *exact same* race on its first-ever write, since the cache itself
starts empty and two concurrent first-requests would both find it empty
before either one populates it. Caching does not solve this race; it only
defers the same race to "first ever call after each process restart" if
the cache is process-local, or introduces a *new* problem (distributed
lock/coordination) if the cache is meant to be durable across restarts.
Revisit with an explicit distributed-lock or OSAC-side uniqueness
constraint if concurrent-create load materializes in practice.

### SC-M4-002: `ComputeInstanceImage.source_type` has no documented enum or server-side validation at the **proto** layer — spike originally set it to a fixed `"catalog"` constant; **superseded by DD-128**, which found the real CRD *does* enforce one and only `"registry"` validates

**Related requirements:** REQ-VMCREATE-020

`ComputeInstanceImage.source_type` (`compute_instance_type.proto`) is a
plain `string` with only an illustrative comment (`"e.g. registry"`) — no
enum, no `buf.validate` constraint, and (confirmed directly against
`private_compute_instances_server.go`) no server-side validation or
interpretation of its value at all. DCM's `spec.guest_os.type` values
(`rhel-9`, `ubuntu-22.04`, `windows-server-2022`) look like OS/template
catalog identifiers, not container-registry image references, so the
proto comment's own example (`"registry"`) originally looked like a
misleading value to hardcode, leading this spike to choose `"catalog"`
instead.

**Correction (DD-128):** that conclusion was only verified against the
proto layer, not the real OSAC `ComputeInstance` CRD's admission
validation, which does enforce an enum — and rejects `"catalog"`. The
implementation now sets `source_type` to `"registry"` (see the Field
Mapping row above); this section's original reasoning is kept for
context, but `"catalog"` is no longer the value in effect anywhere.

Also note: `spec.storage.disks[]` entries other than the required `boot`
disk lose their DCM `name` on translation — `ComputeInstanceDisk` has only
a `size_gib` field, no name/identifier of any kind (verified directly
against `compute_instance_type.proto`). This is a real, permanent
information loss on the OSAC side, not an SP oversight; there is no OSAC
field to preserve it in.

### SC-M4-003: Status mapper's `Unavailable`/`NotFound` rules are async-only in this milestone — synchronous Get/Create/Delete always resolve those two gRPC outcomes as HTTP errors first

**Related requirements:** REQ-VMSTATUS-020, REQ-VMGET-020, REQ-VMERR-010

REQ-VMSTATUS-020's rules 1 (`Unavailable`/`DeadlineExceeded` → `FAILED`)
and 2 (`NotFound` → `DELETED`) are part of the mapper's own contract,
exercised directly as pure-function unit tests, but are not reachable
through this milestone's synchronous Get/Create/Delete handlers:
REQ-VMGET-020 intercepts a `NotFound` from `ComputeInstances/Get` into HTTP
`404`, and REQ-VMERR-010 intercepts `Unavailable`/`DeadlineExceeded` into
HTTP `502`, before either outcome would reach the mapper. This is the same
treatment Cluster's mapper gets in M3 (SC-M3-003) for the identical
reason: both rules become reachable only once Milestone 5 adds the async
status-polling loop; until then they are forward-compatible dead code for
the synchronous surface.

---

## 9. Requirement ID Index

| Prefix | Topic | Count |
|--------|-------|-------|
| REQ-VMCREATE-NNN | 4.1: VM Create | 9 |
| REQ-VMGET-NNN | 4.2: VM Get | 3 |
| REQ-VMLIST-NNN | 4.3: VM List | 4 |
| REQ-VMDELETE-NNN | 4.4: VM Delete | 4 |
| REQ-VMNET-NNN | 4.5: Default Network Provisioning | 5 |
| REQ-VMSTATUS-NNN | 4.6: Status Mapping | 3 |
| REQ-VMERR-NNN | 4.7: Error Mapping | 3 |
| **Total** | | **31** |

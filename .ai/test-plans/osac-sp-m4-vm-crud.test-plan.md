# Test Plan: OSAC Service Provider — Milestone 4 (VM CRUD)

Scope: unit **and** integration tests for Milestone 4 ("VM CRUD") as
specified in
[`.ai/specs/osac-sp-m4-vm-crud.spec.md`](../specs/osac-sp-m4-vm-crud.spec.md).
One file for both tiers, same convention Milestone 3 established (the
pyramid-invariant rule below requires reading a `REQ`/`AC`'s unit and
integration case side by side). No e2e tier — scope is CRUD-only; deferred
NATS/status-polling work is Milestone 5.

**TC ID range:** `TC-U-300`..`TC-U-3xx` / `TC-I-300`..`TC-I-3xx` — a fresh
block distinct from Milestone 1/2 (`0xx`/`1xx`) and Milestone 3 (`2xx`),
chosen because Milestone 4 branched from `main` before Milestone 3 merged
(see the M4 spec's Reference Documents note) — the two milestones' TC IDs
must not collide once both land on `main`.

**Framework:** Ginkgo v2 + Gomega. Unit tests: `internal/vm/*_unit_test.go`,
`internal/handlers/vm/*_unit_test.go`, `internal/grpcerror/*_unit_test.go` —
pure business logic against `bufconn`-backed fake `ComputeInstancesServer`/
`SubnetsServer`/`VirtualNetworksServer` implementations, no real HTTP.
Integration tests: `internal/handlers/vm/*_integration_test.go` — a real
HTTP server (loopback listener, same pattern as M1's
`server_integration_test.go`) with the real router/`StrictServerInterface`
wiring, backed by the same `bufconn` fakes. Run with:

```bash
go run github.com/onsi/ginkgo/v2/ginkgo -r --race -focus "TC-U-3" ./internal/...
go run github.com/onsi/ginkgo/v2/ginkgo -r --race -focus "TC-I-3" ./internal/...
```

## Enforcement rules (gate before implementation, re-checked at REFACTOR)

Identical to Milestone 3's rules — binding, not advisory:

1. **AC-first, Given/When/Then.** Every `AC-*` below was written as an
   observable business outcome (see the spec) before any `TC-*` here was
   drafted against it.
2. **No existence-only/implementation-shape assertions.** Banned:
   `err == nil`, `response != nil`, "`Create` was called". Required: exact
   field-value assertions, exact call counts.
3. **Pyramid invariant.** Every `REQ-*`/`AC-*` pair has at least one
   `TC-U-*` **and** at least one `TC-I-*`. `AC-VMCREATE-070` (idempotent
   Create) and `AC-VMDELETE-020` (404-tolerant Delete) each require their
   `TC-I-*` to issue **two real, sequential HTTP requests** — not two direct
   package-level calls.
4. **100% coverage of new testable code** (non-generated) in `internal/vm/`,
   `internal/handlers/vm/`, and `internal/grpcerror/`, verified via
   `go test -cover` / `make test-cover`.

---

## 1. Unit tests: VM Create (`internal/vm`, `internal/handlers/vm`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-300 | Create translates the full field set and dispatches exact values | REQ-VMCREATE-010, REQ-VMCREATE-020, AC-VMCREATE-010 | Call `internal/vm.Create` with `id="X"`, a full valid spec (`template_id="default-vm"`, `instance_type="standard-4-16"`, `guest_os.type="rhel-9"`, boot disk `"100GB"`), against `bufconn` fakes (with an existing `READY` default subnet); assert the fake `ComputeInstances/Create` request has `ComputeInstance.id=="X"`, `spec.template=="default-vm"`, `spec.instance_type=="standard-4-16"`, `spec.image.source_ref=="rhel-9"`, `spec.boot_disk.size_gib==100`, and both `spec.cores`/`spec.memory_gib` unset. |
| TC-U-301 | Ownership labels are set exactly, merged with caller labels | REQ-VMCREATE-050, AC-VMCREATE-020 | Same call with `spec.metadata.labels={"team":"platform"}`; assert the fake's recorded `metadata.labels` equals exactly `{"team":"platform","dcm.io/managed-by":"dcm","dcm.io/instance-id":"X","dcm.io/service-type":"vm"}`. |
| TC-U-302 | Boot/non-boot disk split and unit conversion | REQ-VMCREATE-030, REQ-VMCREATE-040, AC-VMCREATE-030 | Call `Create` with `disks=[{"data","2TB"},{"boot","100GB"}]` (non-boot listed first); assert `spec.boot_disk.size_gib==100` and `spec.additional_disks` has exactly one entry with `size_gib==2048`. |
| TC-U-303 | Missing boot disk is rejected before calling OSAC | REQ-VMCREATE-060, AC-VMCREATE-040 | Call the Create handler with `disks=[{"data","100GB"}]` (no `"boot"`); assert a `400`-mapped error and zero fake `ComputeInstances/Create` calls. |
| TC-U-304 | Unparseable/invalid disk capacity is rejected before calling OSAC | REQ-VMCREATE-040, REQ-VMCREATE-060, AC-VMCREATE-050 | Table-driven over capacities `"100"`, `"100XB"`, `"-5GB"` on the boot disk; assert each returns a `400`-mapped error and zero fake `ComputeInstances/Create` calls. |
| TC-U-305 | Disk capacity unit table covers GB/GiB/TB/TiB/MB/MiB, case-insensitively | REQ-VMCREATE-040 | Table-driven: `"100gb"`→`100`, `"100GiB"`→`100`, `"2TB"`→`2048`, `"1TiB"`→`1024`, `"512MB"`→`1` (rounded up), `"2048MiB"`→`2`; assert the parser's exact return value for each. |
| TC-U-306 | Missing `provider_hints.osac.instance_type` is rejected before calling OSAC | REQ-VMCREATE-060, AC-VMCREATE-060 | Call the Create handler with `template_id` set, `instance_type` omitted; assert a `400`-mapped error and zero fake `ComputeInstances/Create` calls — proving no direct `cores`/`memory_gib` fallback exists. |
| TC-U-307 | `AlreadyExists` on Create triggers a Get and returns the existing resource, not a new one | REQ-VMCREATE-070, AC-VMCREATE-070 | Fake `ComputeInstances/Create` returns `codes.AlreadyExists` for `id="X"`; fake `ComputeInstances/Get("X")` returns a canned instance with `status.state=STARTING`; call `Create`; assert the returned resource's `id=="X"`, `status=="PROVISIONING"`, and the fake's `Get` call counter equals exactly `1`. |
| TC-U-308 | Every Create sets exactly one network attachment resolved from the default-network step | REQ-VMCREATE-090, AC-VMCREATE-080 | Fake `Subnets/List` returns one `READY` subnet `id="subnet-abc"`; call `Create` with no networking fields in the request; assert the fake's recorded `ComputeInstances/Create` call has `spec.network_attachments` with exactly one entry, `subnet=="subnet-abc"`. |

---

## 2. Unit tests: VM Get (`internal/vm`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-310 | IP addresses are echoed exactly from OSAC's status | REQ-VMGET-010, REQ-VMGET-030, AC-VMGET-010 | Fake `ComputeInstances/Get` returns `status.state=RUNNING`, `internal_ip_address="10.200.1.5"`, `external_ip_address=""`; call `internal/vm.Get`; assert returned `status=="RUNNING"`, `internal_ip_address=="10.200.1.5"`, `external_ip_address==""`. |
| TC-U-311 | Nonexistent VM maps to a not-found result | REQ-VMGET-020, AC-VMGET-020 | Fake `ComputeInstances/Get` returns `codes.NotFound`; call `Get`; assert the returned error, through the shared classifier, produces HTTP `404`. |

---

## 3. Unit tests: VM List (`internal/vm`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-320 | List applies the ownership filter, default page size, and exact field values including IP echo | REQ-VMLIST-010, REQ-VMLIST-030, AC-VMLIST-010 | Fake `ComputeInstances/List` records its request, returns 2 instances with known `id`/`status.state`/`internal_ip_address`; call `internal/vm.List` with no page params; assert the fake recorded `filter=="this.metadata.labels[\"dcm.io/managed-by\"] == \"dcm\""` and `limit==50`, and returned entries' fields equal the fake's canned values exactly. |
| TC-U-321 | `page_token` round-trips through OSAC's `offset` | REQ-VMLIST-020, REQ-VMLIST-040, AC-VMLIST-020 | Fake `ComputeInstances/List` returns a response with next `offset=50`; decode the returned `next_page_token` and feed it into a second `List` call; assert the fake's second recorded request has `offset==50` exactly. |

---

## 4. Unit tests: VM Delete (`internal/vm`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-330 | Successful delete does not poll for confirmation | REQ-VMDELETE-010, REQ-VMDELETE-040, AC-VMDELETE-010 | Fake `ComputeInstances/Delete` succeeds; call `internal/vm.Delete`; assert success and the fake's `ComputeInstances/Get` call counter equals exactly `0`. |
| TC-U-331 | `NotFound` on Delete is treated as success | REQ-VMDELETE-020, AC-VMDELETE-020 | Fake `ComputeInstances/Delete` returns `codes.NotFound`; call `Delete`; assert success (no error). |
| TC-U-332 | A genuine Delete failure is surfaced, not swallowed | REQ-VMDELETE-030, AC-VMDELETE-030 | Fake `ComputeInstances/Delete` returns `codes.Unavailable`; call `Delete`; assert the returned error, through the shared classifier, produces HTTP `502`. |

---

## 5. Unit tests: Default Network Provisioning (`internal/vm`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-340 | An existing default subnet is reused, no new network is created | REQ-VMNET-010, AC-VMNET-010 | Fake `Subnets/List` returns one `READY` subnet `id="subnet-existing"`; resolve the default subnet; assert the fake's `VirtualNetworks/Create`/`Subnets/Create` counters are both `0` and the resolved subnet id is exactly `"subnet-existing"`. |
| TC-U-341 | No existing subnet — a new VirtualNetwork and Subnet are provisioned with the documented shape | REQ-VMNET-020, REQ-VMNET-030, AC-VMNET-020 | Fake `Subnets/List` returns zero results; resolve the default subnet; assert `VirtualNetworks/Create`'s recorded `spec.ipv4_cidr=="10.200.0.0/16"`, `spec.network_class==""`, ownership labels present; and `Subnets/Create`'s recorded `spec.virtual_network` equals the new VirtualNetwork's `id` exactly, `spec.ipv4_cidr=="10.200.1.0/24"`, `metadata.annotations["osac.openshift.io/owner-reference"]` equals that same `id`. |
| TC-U-342 | The resolver polls until both resources report READY | REQ-VMNET-040, AC-VMNET-030 | Fake `VirtualNetworks/Get` returns `PENDING` then `READY`; fake `Subnets/Get` returns `READY` immediately; resolve the default subnet; assert `VirtualNetworks/Get`'s call counter equals exactly `2` and the resolver only returns after both are `READY`. |
| TC-U-343 | Provisioning timeout returns an error without ever calling `ComputeInstances/Create` | REQ-VMNET-040, AC-VMNET-040 | Fake `Subnets/Get` always returns `PENDING`; construct the resolver with a short test timeout/interval (constructor option, not the real 15s/500ms constants); assert resolving the default subnet returns an error, and `Create`'s caller-level test confirms `ComputeInstances/Create`'s call counter is exactly `0`. |

---

## 6. Unit tests: Status Mapping (`internal/vm`, pure function)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-350 | Each precedence-rule input maps to its documented value | REQ-VMSTATUS-010, REQ-VMSTATUS-020, AC-VMSTATUS-010 | Table-driven, one row per rule: `Unavailable`→`FAILED`; `NotFound`→`DELETED`; `state=STARTING`→`PROVISIONING`; `state=RUNNING`→`RUNNING`; `state=FAILED`→`FAILED`; `state=DELETING`→`DELETING`; `state=STOPPING`→`STOPPING`; `state=STOPPED`→`STOPPED`; `state=PAUSED`→`PAUSED`; `state=UNSPECIFIED`→`FAILED`. Each row asserts the mapper's exact return value. |
| TC-U-351 | Connectivity failure and a real 404 are never conflated | REQ-VMSTATUS-020, AC-VMSTATUS-020 | Call the mapper once with a gRPC `Unavailable` outcome and once with `NotFound`; assert the first returns exactly `FAILED` and the second exactly `DELETED`. |

---

## 7. Unit tests: Error Mapping (`internal/grpcerror`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-360 | Each gRPC code maps to its documented HTTP status and `type` | REQ-VMERR-010, REQ-VMERR-020, AC-VMERR-010 | Table-driven: `Classify` called with `InvalidArgument`/`Unauthenticated`/`PermissionDenied`/`NotFound`/`AlreadyExists`/`Unavailable`/`Internal`; assert the returned status is exactly `400`/`401`/`403`/`404`/`409`/`502`/`500` respectively and the returned `v1alpha1.ErrorType` matches the documented constant exactly. |
| TC-U-361 | `Classify` is the single implementation consumed by all 4 VM handlers | REQ-VMERR-030, AC-VMERR-020 | Fake OSAC returns `PermissionDenied` from `ComputeInstances/Get`, `ComputeInstances/List`, and `ComputeInstances/Delete` in three separate calls; assert all three handlers produce identical HTTP status (`403`) and identical `type` value, both derived from `internal/grpcerror.Classify`. |

---

## 8. Integration tests: VM Create (real HTTP + router + `bufconn` OSAC fakes)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-300 | Create succeeds end-to-end over real HTTP | REQ-VMCREATE-010, REQ-VMCREATE-020, REQ-VMCREATE-050, REQ-VMCREATE-080, AC-VMCREATE-010, AC-VMCREATE-020 | Start the real HTTP server wired to `bufconn` fakes (with an existing `READY` default subnet); issue a real `POST /api/v1alpha1/vms?id=X` with a full valid body; assert the real HTTP response is `201` with `id=="X"` and `status=="PROVISIONING"` exactly, and the fake OSAC server independently recorded the Create call with the exact translated fields (mirrors TC-U-300/301). |
| TC-I-301 | Idempotent Create: two real, sequential HTTP requests with the same `id` return the same resource | REQ-VMCREATE-070, AC-VMCREATE-070 | Issue `POST /api/v1alpha1/vms?id=X` twice in sequence over real HTTP; assert **both** responses are `201` with `id=="X"`, the second response's `status` reflects the existing (not re-created) instance's state, and the fake OSAC server's instance count is exactly `1` after both requests. |
| TC-I-302 | Request validation is enforced at the real HTTP boundary | REQ-VMCREATE-060, AC-VMCREATE-040, AC-VMCREATE-050, AC-VMCREATE-060 | Issue real Create requests missing, in turn: the `id` query parameter; a boot disk; a parseable disk capacity; `provider_hints.osac.instance_type`; assert each real response is `400` (RFC 9457) and the fake OSAC server recorded zero Create calls in every case. |
| TC-I-303 | Disk translation and network attachment are correct over real HTTP | REQ-VMCREATE-030, REQ-VMCREATE-090, AC-VMCREATE-030, AC-VMCREATE-080 | Issue a real Create request with a non-boot data disk and a boot disk; assert the fake's recorded `boot_disk`/`additional_disks`/`network_attachments` match TC-U-302/308's expectations exactly, proven reachable through the real router. |

---

## 9. Integration tests: VM Get (real HTTP + router + `bufconn` OSAC fake)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-310 | Get returns exact status and IP fields over real HTTP | REQ-VMGET-010, REQ-VMGET-030, AC-VMGET-010 | Fake OSAC `Get` returns `RUNNING` with known IP fields; real `GET /api/v1alpha1/vms/{id}`; assert `200`, `status=="RUNNING"`, `internal_ip_address` equals the fake's exact value. |
| TC-I-311 | Get returns 404 for a nonexistent VM over real HTTP | REQ-VMGET-020, AC-VMGET-020 | Fake OSAC `Get` returns `NotFound`; real `GET`; assert `404` with RFC 9457 `type` exactly `.../not-found`. |

---

## 10. Integration tests: VM List (real HTTP + router + `bufconn` OSAC fake)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-320 | List returns exact entries with the ownership filter applied, over real HTTP | REQ-VMLIST-010, REQ-VMLIST-030, AC-VMLIST-010 | Fake OSAC `List` records its request, returns 2 known instances; real `GET /api/v1alpha1/vms`; assert the fake recorded the exact CEL filter, and the real response body's `results` match the canned values exactly. |
| TC-I-321 | Pagination round-trips across two real, sequential HTTP requests | REQ-VMLIST-020, REQ-VMLIST-040, AC-VMLIST-020 | First real `GET /api/v1alpha1/vms` triggers a fake response with next `offset=50`; feed the returned `next_page_token` into a second real `GET .../vms?page_token=...`; assert the fake's second recorded request has `offset==50`. |

---

## 11. Integration tests: VM Delete (real HTTP + router + `bufconn` OSAC fake)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-330 | Delete succeeds over real HTTP | REQ-VMDELETE-010, REQ-VMDELETE-040, AC-VMDELETE-010 | Fake OSAC `Delete` succeeds; real `DELETE /api/v1alpha1/vms/{id}`; assert `204` with empty body, and no `ComputeInstances/Get` call was made afterward. |
| TC-I-331 | Deleting an already-deleted VM is idempotent across two real, sequential HTTP requests | REQ-VMDELETE-020, AC-VMDELETE-020 | Fake OSAC holds exactly one instance `id=X`; issue real `DELETE /api/v1alpha1/vms/X` twice in sequence; assert **both** real responses are `204`. |
| TC-I-332 | A genuine Delete failure surfaces over real HTTP | REQ-VMDELETE-030, AC-VMDELETE-030 | Fake OSAC `Delete` returns `Unavailable`; real `DELETE`; assert `502`, not `204`. |

---

## 12. Integration tests: Default Network Provisioning, Status, and Error mapping, proven through the real router

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-340 | A new default network is provisioned end-to-end over real HTTP on the very first VM Create | REQ-VMNET-020, REQ-VMNET-030, REQ-VMNET-040, AC-VMNET-020, AC-VMNET-030 | Fake `Subnets/List` returns zero results initially; fake `VirtualNetworks/Get`/`Subnets/Get` report `READY` immediately after their respective `Create` calls; issue a real `POST /api/v1alpha1/vms?id=X`; assert `201`, and the fake's `ComputeInstances/Create` call's `network_attachments[0].subnet` equals the newly-created Subnet's `id` exactly. |
| TC-I-341 | Network provisioning timeout is surfaced as 502 over real HTTP | REQ-VMNET-040, AC-VMNET-040 | Fake `Subnets/Get` always returns `PENDING` (poll timeout overridden to a short test value via server construction option); real `POST /api/v1alpha1/vms?id=X`; assert `502` and zero fake `ComputeInstances/Create` calls. |
| TC-I-342 | An existing default subnet is reused over real HTTP — no new network is created | REQ-VMNET-010, AC-VMNET-010 | Fake `Subnets/List` returns one `READY` subnet `id="subnet-existing"` (the fixture default); real `POST /api/v1alpha1/vms?id=X`; assert `201`, the fake's `VirtualNetworks/Create` and `Subnets/Create` call counters are both exactly `0`, and the recorded `ComputeInstances/Create` call's `network_attachments[0].subnet` is exactly `"subnet-existing"`. |
| TC-I-350 | Status precedence is observable over real HTTP | REQ-VMSTATUS-020, AC-VMSTATUS-020 | Fake OSAC `Get` returns gRPC `Unavailable` on one real `GET` call and `NotFound` on another; assert the first real response is `502`/mapped-`FAILED`-equivalent status per §4.7 and the second is `404`. |
| TC-I-360 | Each gRPC error code maps to its documented HTTP status over real HTTP, identically across handlers | REQ-VMERR-010, REQ-VMERR-020, REQ-VMERR-030, AC-VMERR-010, AC-VMERR-020 | Table-driven over real HTTP: fake OSAC returns each of the 7 codes from `ComputeInstances/Get`, and `PermissionDenied` additionally from `ComputeInstances/List` and `ComputeInstances/Delete`; assert each real response's HTTP status/`type` matches the documented mapping exactly, and the `PermissionDenied` case is identical across all three handlers. |

---

## Coverage Matrix

| Spec Section | REQ Count | AC Count | TC-U (this file) | TC-I (this file) | Pyramid complete? |
|---|---|---|---|---|---|
| 4.1 VM Create | 9 | 8 | 9 (TC-U-300..308) | 4 (TC-I-300..303) | Yes — every AC has both tiers; AC-VMCREATE-070 covered by TC-U-307 (unit) + TC-I-301 (2 real sequential HTTP requests, per rule 3) |
| 4.2 VM Get | 3 | 2 | 2 (TC-U-310..311) | 2 (TC-I-310..311) | Yes |
| 4.3 VM List | 4 | 2 | 2 (TC-U-320..321) | 2 (TC-I-320..321) | Yes |
| 4.4 VM Delete | 4 | 3 | 3 (TC-U-330..332) | 3 (TC-I-330..332) | Yes — AC-VMDELETE-020 covered by TC-U-331 (unit) + TC-I-331 (2 real sequential HTTP requests, per rule 3) |
| 4.5 Default Network Provisioning | 5 | 4 | 4 (TC-U-340..343) | 3 dedicated (TC-I-340..342) | Yes — AC-VMNET-010 covered by TC-U-340 (unit) + TC-I-342 (integration, explicit zero-call-count assertion, not merely incidental reuse via other Create tests) |
| 4.6 Status Mapping | 3 | 2 | 2 (TC-U-350..351) | 1 dedicated (TC-I-350) + incidentally via TC-I-310/311/300 | Yes |
| 4.7 Error Mapping | 3 | 2 | 2 (TC-U-360..361) | 1 dedicated (TC-I-360) + incidentally via TC-I-302/311/332 | Yes |
| **Total** | **31** | **23** | **24** | **16** | |

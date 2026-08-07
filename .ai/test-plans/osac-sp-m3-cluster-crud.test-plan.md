# Test Plan: OSAC Service Provider — Milestone 3 (Cluster CRUD)

Scope: unit **and** integration tests for Milestone 3 ("Cluster CRUD") as
specified in
[`.ai/specs/osac-sp-m3-cluster-crud.spec.md`](../specs/osac-sp-m3-cluster-crud.spec.md).
Unlike Milestones 1/2 (separate `-unit`/`-integration` files), this
milestone uses one file for both tiers, since the pyramid-invariant rule
below requires reading a `REQ`/`AC`'s unit and integration case side by side
to verify the pyramid is actually complete for it. There is no e2e tier for
this milestone — scope is CRUD-only; the deferred NATS/status-polling work
(Milestone 5) is where an e2e-style tier applies.

**Framework:** Ginkgo v2 + Gomega. Unit tests: `internal/cluster/*_unit_test.go`,
`internal/handlers/cluster/*_unit_test.go` — pure business logic against a
`bufconn`-backed fake `publicv1.ClustersServer` (same technique as M2's
`conn_unit_test.go`), no real HTTP. Integration tests:
`internal/handlers/cluster/*_integration_test.go` — a real HTTP server
(loopback listener, same pattern as M1's `server_integration_test.go`) with
the real router/`StrictServerInterface` wiring, backed by the same
`bufconn` fake OSAC server. Run with:

```bash
go run github.com/onsi/ginkgo/v2/ginkgo -r --race -focus "TC-U-2" ./internal/...
go run github.com/onsi/ginkgo/v2/ginkgo -r --race -focus "TC-I-2" ./internal/...
```

## Enforcement rules (gate before implementation, re-checked at REFACTOR)

These are binding, not advisory — a PR that violates any of them is not
done, regardless of coverage percentage:

1. **AC-first, Given/When/Then.** Every `AC-*` below was written as an
   observable business outcome (see the spec) before any `TC-*` here was
   drafted against it. A `TC-*` that only asserts "no error"/"response not
   nil" without asserting the specific returned value fails this rule — see
   rule 2.
2. **No existence-only/implementation-shape assertions.** Banned:
   "`err == nil`", "`response != nil`", "`Create` was called". Required:
   "response `id` equals exactly `X`", "response `status` equals exactly
   `ACTIVE`", "fake recorded exactly 1 call with these exact field values."
3. **Pyramid invariant.** Every `REQ-*`/`AC-*` pair has at least one `TC-U-*`
   (business logic in isolation against the `bufconn` fake) **and** at least
   one `TC-I-*` (same business outcome, reachable through the real HTTP
   router). See the Coverage Matrix for the explicit pairing — any `AC-*`
   with only one tier listed is an incomplete pyramid and blocks merge.
   `AC-CREATE-030` (idempotent Create) and `AC-DELETE-020` (404-tolerant
   Delete) each require their `TC-I-*` to issue **two real, sequential HTTP
   requests** — not two direct package-level calls — since the guarantee
   being proven is about what a caller observes over the wire across
   retries.
4. **100% coverage of new testable code** (non-generated) in
   `internal/cluster/` and `internal/handlers/cluster/`, verified via
   `go test -cover` / `make test-cover`, the same way Milestones 1/2 closed
   their gaps. Coverage is a floor, not a substitute for rules 1-2 — a line
   can be executed by a test that still only checks "no error."

---

## 1. Unit tests: Cluster Create (`internal/cluster`, `internal/handlers/cluster`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-200 | Create translates the full field set and dispatches exact values | REQ-CREATE-010, REQ-CREATE-020, REQ-CREATE-025, AC-CREATE-010 | Call `internal/cluster.Create` with `id="X"`, `spec={version:"1.29", nodes.worker.count:3, metadata.name:"foo", provider_hints.osac.template_id:"default-hcp"}` against a `bufconn` fake `ClustersServer`; assert the fake's recorded `Clusters/Create` request has `Cluster.id=="X"`, `spec.template=="default-hcp"`, `spec.node_sets["default-hcp"].size==3`, `spec.metadata.name=="foo"`, and `spec.release_image` equal to the placeholder table's mapped value for `"1.29"` (a specific non-empty string, not `"1.29"` itself). |
| TC-U-201 | Ownership labels are set exactly, merged with caller labels | REQ-CREATE-030, AC-CREATE-020 | Same call with `spec.metadata.labels={"team":"platform"}`; assert the fake's recorded `Cluster.metadata.labels` equals exactly `{"team":"platform","dcm.io/managed-by":"dcm","dcm.io/instance-id":"X","dcm.io/service-type":"cluster"}`. |
| TC-U-202 | `AlreadyExists` on Create triggers a Get and returns the existing resource, not a new one | REQ-CREATE-040, AC-CREATE-030 | Fake `Clusters/Create` returns `codes.AlreadyExists` for `id="X"`; fake `Clusters/Get("X")` returns a canned Cluster with `status.state=PROGRESSING`; call `Create`; assert the returned resource's `id=="X"` and `status=="PROGRESSING"` (the Get's values, not fabricated), and that the fake's `Get` call counter equals exactly `1`. |
| TC-U-203 | Worker CPU/memory/storage hints never become a `host_type` override | REQ-CREATE-070, AC-CREATE-060 | Call `Create` with `spec.nodes.worker.{cpu:8,memory:"32GB",storage:"250GB"}`; assert the fake's recorded `node_sets[key].host_type` is the empty string. |
| TC-U-204 | Version translation table covers each supported placeholder version | REQ-CREATE-025 | Table-driven: for each version in the placeholder table (e.g. `"1.28"`, `"1.29"`, `"1.30"`), call `Create`; assert the fake's recorded `spec.release_image` equals that version's specific documented mapped value (not merely non-empty) — and that an explicit `provider_hints.osac.release_image` override, when present, is used verbatim instead of the table lookup. |
| TC-U-205 | Missing `id` query parameter is rejected before calling OSAC | REQ-CREATE-060, AC-CREATE-040 | Invoke the `Create` handler (StrictServerInterface layer) with no `id` param; assert it returns a `400`-mapped error and the fake OSAC server recorded zero `Clusters/Create` calls. |
| TC-U-206 | Missing required spec field is rejected before calling OSAC | REQ-CREATE-060, AC-CREATE-050 | Invoke the `Create` handler with `spec.provider_hints.osac.template_id` absent; assert `400`-mapped error and zero fake OSAC calls. |

---

## 2. Unit tests: Cluster Get (`internal/cluster`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-210 | `ACTIVE` cluster fetches kubeconfig exactly once | REQ-GET-010, REQ-GET-020, AC-GET-010 | Fake `Clusters/Get` returns `status.state=READY`; fake `Clusters/GetKubeconfig` returns `"kubeconfig-abc"`; call `internal/cluster.Get`; assert returned `status=="ACTIVE"`, `kubeconfig=="kubeconfig-abc"`, and the fake's `GetKubeconfig` call counter equals exactly `1`. |
| TC-U-211 | Non-`ACTIVE` cluster never triggers a kubeconfig fetch | REQ-GET-030, AC-GET-020 | Fake `Clusters/Get` returns `status.state=PROGRESSING`; call `Get`; assert `status=="PROGRESSING"`, `kubeconfig==""`, and `GetKubeconfig`'s call counter equals exactly `0`. |
| TC-U-212 | Nonexistent cluster maps to a not-found result | REQ-GET-040, AC-GET-030 | Fake `Clusters/Get` returns `codes.NotFound`; call `Get`; assert the returned error, when passed through the shared error mapper, produces HTTP `404`. |

---

## 3. Unit tests: Cluster List (`internal/cluster`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-220 | List applies the ownership filter, default page size, and exact field values | REQ-LIST-010, REQ-LIST-030, AC-LIST-010 | Fake `Clusters/List` records its request and returns 2 clusters with known `id`/`status.state`; call `internal/cluster.List` with no page params; assert the fake recorded `filter=="this.metadata.labels[\"dcm.io/managed-by\"] == \"dcm\""` and `limit==50`, and the returned entries' `id`/`status` equal the fake's canned values exactly. |
| TC-U-221 | `page_token` round-trips through OSAC's `offset` | REQ-LIST-020, REQ-LIST-040, AC-LIST-020 | Fake `Clusters/List` returns a response with next `offset=50`; decode the returned `next_page_token` and feed it into a second `List` call; assert the fake's second recorded request has `offset==50` exactly. |
| TC-U-222 | List entries never populate `kubeconfig` | REQ-LIST-030, AC-LIST-030 | Fake `Clusters/List` returns a `status.state=READY` cluster; fake `GetKubeconfig` fails the test if called; call `List`; assert the returned entry has no populated `kubeconfig` and `GetKubeconfig`'s call counter equals exactly `0`. |

---

## 4. Unit tests: Cluster Delete (`internal/cluster`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-230 | Successful delete does not poll for confirmation | REQ-DELETE-010, REQ-DELETE-040, AC-DELETE-010 | Fake `Clusters/Delete` succeeds; call `internal/cluster.Delete`; assert it returns success and the fake's `Clusters/Get` call counter equals exactly `0` (no confirmation poll). |
| TC-U-231 | `NotFound` on Delete is treated as success | REQ-DELETE-020, AC-DELETE-020 | Fake `Clusters/Delete` returns `codes.NotFound`; call `Delete`; assert it returns success (no error), not a not-found error. |
| TC-U-232 | A genuine Delete failure is surfaced, not swallowed | REQ-DELETE-030, AC-DELETE-030 | Fake `Clusters/Delete` returns `codes.Unavailable`; call `Delete`; assert the returned error, through the shared error mapper, produces HTTP `502` — proving the `NotFound` carve-out does not apply here. |

---

## 5. Unit tests: Status Mapping (`internal/cluster`, pure function)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-240 | Each precedence-rule input maps to its documented value | REQ-STATUS-010, REQ-STATUS-020, AC-STATUS-010 | Table-driven, one row per rule in REQ-STATUS-020: `Unavailable`→`UNAVAILABLE`; `NotFound`→`DELETED`; `state=UNSPECIFIED`→`PROGRESSING`; `state=FAILED`→`FAILED`; `state=DELETING`→`DELETING`; `state=DELETE_FAILED`→`FAILED`; `state=READY` (no conditions)→`ACTIVE`; `state=PROGRESSING`→`PROGRESSING`; `DEGRADED` condition `TRUE` + `state=READY`→`DEGRADED`. Each row asserts the mapper's exact return value. |
| TC-U-241 | `FAILED` state takes precedence over a simultaneous `DEGRADED` condition | REQ-STATUS-020, AC-STATUS-020 | Call the mapper with `state=FAILED` **and** a `DEGRADED` condition `TRUE` present together; assert it returns exactly `FAILED`. |
| TC-U-242 | Connectivity failure (`Unavailable`) is never conflated with a real `NotFound` | REQ-STATUS-020, AC-STATUS-030 | Call the mapper once with a gRPC `Unavailable` outcome and once with `NotFound`; assert the first returns exactly `UNAVAILABLE` and the second exactly `DELETED`. |

---

## 6. Unit tests: Error Mapping (`internal/handlers/cluster` or shared helper)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-250 | Each gRPC code maps to its documented HTTP status and `type` | REQ-ERR-010, REQ-ERR-020, AC-ERR-010 | Table-driven via the Get handler: fake `Clusters/Get` returns, in turn, `InvalidArgument`/`Unauthenticated`/`PermissionDenied`/`NotFound`/`Unavailable`/`Internal`; assert the resulting HTTP status is exactly `400`/`401`/`403`/`404`/`502`/`500` respectively and the decoded body's `type` matches the documented `v1alpha1.ErrorType` constant exactly. |
| TC-U-251 | The same error-mapping function is used by Get, List, and Delete | REQ-ERR-030, AC-ERR-020 | Fake OSAC returns `PermissionDenied` from `Clusters/Get`, `Clusters/List`, and `Clusters/Delete` in three separate calls; assert all three handlers produce identical HTTP status (`403`) and identical `type` value. |

---

## 7. Integration tests: Cluster Create (real HTTP + router + `bufconn` OSAC fake)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-200 | Create succeeds end-to-end over real HTTP | REQ-CREATE-010, REQ-CREATE-020, REQ-CREATE-030, REQ-CREATE-050, AC-CREATE-010, AC-CREATE-020 | Start the real HTTP server wired to a `bufconn` fake OSAC; issue a real `POST /api/v1alpha1/clusters?id=X` with a full valid body; assert the real HTTP response is `201` with `id=="X"` and `status=="PROGRESSING"` exactly, and the fake OSAC server independently recorded the Create call with the exact translated fields (mirrors TC-U-200/201, proven reachable through the real router). |
| TC-I-201 | Idempotent Create: two real, sequential HTTP requests with the same `id` return the same resource | REQ-CREATE-040, AC-CREATE-030 | Issue `POST /api/v1alpha1/clusters?id=X` twice in sequence over real HTTP (not direct package calls); assert **both** responses are `201` with `id=="X"`, the second response's `status` reflects the existing (not re-created) cluster's state, and the fake OSAC server's cluster count is exactly `1` after both requests. |
| TC-I-202 | Request validation is enforced at the real HTTP boundary | REQ-CREATE-060, AC-CREATE-040, AC-CREATE-050 | Issue a real `POST /api/v1alpha1/clusters` with no `id` query parameter, and separately one with a body missing `spec.provider_hints.osac.template_id`; assert both real responses are `400` (RFC 9457) and the fake OSAC server recorded zero Create calls for either. |
| TC-I-203 | Host-sizing hints are not forwarded as a `host_type` override, over real HTTP | REQ-CREATE-070, AC-CREATE-060 | Issue a real Create request with `spec.nodes.worker.cpu=8`; assert the fake OSAC server's recorded `node_sets[key].host_type` is empty. |

---

## 8. Integration tests: Cluster Get (real HTTP + router + `bufconn` OSAC fake)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-210 | Get returns kubeconfig for an `ACTIVE` cluster over real HTTP | REQ-GET-010, REQ-GET-020, AC-GET-010 | Fake OSAC `Get` returns `READY`, fake `GetKubeconfig` returns a known value; issue a real `GET /api/v1alpha1/clusters/{id}`; assert `200`, `status=="ACTIVE"` exactly, `kubeconfig` equals the fake's exact value. |
| TC-I-211 | Get omits kubeconfig for a non-`ACTIVE` cluster over real HTTP | REQ-GET-030, AC-GET-020 | Fake OSAC `Get` returns `PROGRESSING`; real `GET`; assert `status=="PROGRESSING"`, `kubeconfig==""` exactly. |
| TC-I-212 | Get returns 404 for a nonexistent cluster over real HTTP | REQ-GET-040, AC-GET-030 | Fake OSAC `Get` returns `NotFound`; real `GET`; assert `404` with RFC 9457 `type` exactly `.../not-found`. |

---

## 9. Integration tests: Cluster List (real HTTP + router + `bufconn` OSAC fake)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-220 | List returns exact entries with the ownership filter applied, over real HTTP | REQ-LIST-010, REQ-LIST-030, AC-LIST-010 | Fake OSAC `List` records its request, returns 2 known clusters; real `GET /api/v1alpha1/clusters`; assert the fake recorded the exact CEL filter, and the real response body's `results` match the canned values exactly. |
| TC-I-221 | Pagination round-trips across two real, sequential HTTP requests | REQ-LIST-020, REQ-LIST-040, AC-LIST-020 | First real `GET /api/v1alpha1/clusters` triggers a fake response with next `offset=50`; feed the returned `next_page_token` into a second real `GET .../clusters?page_token=...`; assert the fake's second recorded request has `offset==50`. |
| TC-I-222 | List responses never include `kubeconfig`, over real HTTP | REQ-LIST-030, AC-LIST-030 | Fake OSAC `List` returns a `READY` cluster; real `GET`; assert the response entry has no `kubeconfig` field and the fake `GetKubeconfig` was never called. |

---

## 10. Integration tests: Cluster Delete (real HTTP + router + `bufconn` OSAC fake)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-230 | Delete succeeds over real HTTP | REQ-DELETE-010, REQ-DELETE-040, AC-DELETE-010 | Fake OSAC `Delete` succeeds; real `DELETE /api/v1alpha1/clusters/{id}`; assert `204` with empty body, and no `Clusters/Get` call was made by the SP afterward. |
| TC-I-231 | Deleting an already-deleted cluster is idempotent across two real, sequential HTTP requests | REQ-DELETE-020, AC-DELETE-020 | Fake OSAC holds exactly one cluster `id=X`; issue real `DELETE /api/v1alpha1/clusters/X` twice in sequence; assert **both** real responses are `204` (the second because the fake's second `Delete` call returns `NotFound`, mapped to `204` not `404`). |
| TC-I-232 | A genuine Delete failure surfaces over real HTTP, is not swallowed by the 404-tolerance carve-out | REQ-DELETE-030, AC-DELETE-030 | Fake OSAC `Delete` returns `Unavailable`; real `DELETE`; assert `502`, not `204`. |

---

## 11. Integration tests: Status precedence and Error mapping, proven through the real router

Status Mapping's and Error Mapping's own `REQ`/`AC` pairs are cross-cutting
(§4.5/§4.6 of the spec) and have no dedicated HTTP surface of their own —
their pyramid `TC-I-*` requirement is satisfied primarily by the Create/Get/
List/Delete integration tests above, each of which already exercises a
specific mapped status or error code end-to-end (e.g. TC-I-210/211
exercise `ACTIVE`/`PROGRESSING`, TC-I-212 exercises the `NotFound`→`404`
error mapping). The two cases below cover the remaining business-observable
behaviors (precedence ordering, cross-handler mapper sharing) that no single
CRUD happy-path test would incidentally prove.

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-240 | Status precedence is observable over real HTTP, not just at the unit level | REQ-STATUS-020, AC-STATUS-020 | Fake OSAC `Get` returns `state=FAILED` **and** a `DEGRADED` condition `TRUE` simultaneously; real `GET /api/v1alpha1/clusters/{id}`; assert the real response body's `status` is exactly `"FAILED"`. |
| TC-I-250 | Each gRPC error code maps to its documented HTTP status over real HTTP, identically across handlers | REQ-ERR-010, REQ-ERR-020, REQ-ERR-030, AC-ERR-010, AC-ERR-020 | Table-driven over real HTTP: fake OSAC returns each of the 6 codes from `Clusters/Get`, and `PermissionDenied` additionally from `Clusters/List` and `Clusters/Delete`; assert each real response's HTTP status/`type` matches the documented mapping exactly, and the `PermissionDenied` case is identical (`403`, same `type`) across all three handlers. |

---

## Coverage Matrix

| Spec Section | REQ Count | AC Count | TC-U (this file) | TC-I (this file) | Pyramid complete? |
|---|---|---|---|---|---|
| 4.1 Cluster Create | 7 | 6 | 7 (TC-U-200..206) | 4 (TC-I-200..203) | Yes — every AC has both tiers; AC-CREATE-030 covered by TC-U-202 (unit) + TC-I-201 (2 real sequential HTTP requests, per rule 3) |
| 4.2 Cluster Get | 4 | 3 | 3 (TC-U-210..212) | 3 (TC-I-210..212) | Yes |
| 4.3 Cluster List | 4 | 3 | 3 (TC-U-220..222) | 3 (TC-I-220..222) | Yes |
| 4.4 Cluster Delete | 4 | 3 | 3 (TC-U-230..232) | 3 (TC-I-230..232) | Yes — AC-DELETE-020 covered by TC-U-231 (unit) + TC-I-231 (2 real sequential HTTP requests, per rule 3) |
| 4.5 Status Mapping | 3 | 3 | 3 (TC-U-240..242) | 1 dedicated (TC-I-240) + incidentally via TC-I-210/211/220 | Yes |
| 4.6 Error Mapping | 3 | 2 | 2 (TC-U-250..251) | 1 dedicated (TC-I-250) + incidentally via TC-I-202/212/232 | Yes |
| **Total** | **25** | **20** | **21** | **15** | |

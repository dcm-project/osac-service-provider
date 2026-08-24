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
   retries. The only sanctioned exception is a sub-case the spec proves
   unreachable through any real HTTP handler in this milestone (documented
   via an `SC-M3-*` clarification, e.g. `SC-M3-001`/`SC-M3-003` — see
   §4.5's Coverage Matrix row); an untested `TC-I-*` with no such
   documented reason is still an incomplete pyramid.
4. **100% coverage of new testable code** (non-generated) in
   `internal/cluster/` and `internal/handlers/cluster/`, verified via
   `go test -cover` / `make test-cover`, the same way Milestones 1/2 closed
   their gaps. Coverage is a floor, not a substitute for rules 1-2 — a line
   can be executed by a test that still only checks "no error."

---

## 1. Unit tests: Cluster Create (`internal/cluster`, `internal/handlers/cluster`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-200 | Create translates the full field set and dispatches exact values | REQ-CREATE-010, REQ-CREATE-020, REQ-CREATE-025, REQ-CREATE-080, AC-CREATE-010 | Exercises AC-CREATE-010 via `internal/cluster.Create` against `bufconn` fakes for both `ClusterTemplatesServer` (single node-set key, deliberately distinct from `template_id`) and `ClustersServer` (see spec §4.1 for the exact field values asserted, including the resolved `node_sets` key). |
| TC-U-201 | Ownership labels are set exactly, merged with caller labels | REQ-CREATE-030, AC-CREATE-020 | Exercises AC-CREATE-020 via `internal/cluster.Create` against the bufconn fake. |
| TC-U-202 | `AlreadyExists` on Create triggers a Get and returns the existing resource, not a new one | REQ-CREATE-040, AC-CREATE-030 | Exercises AC-CREATE-030's Create-then-Get-fallback path directly via `internal/cluster.Create` against the bufconn fake (the real-HTTP two-request guarantee is TC-I-201, per Rule 3). |
| TC-U-203 | Worker CPU/memory/storage hints never become a `host_type` override | REQ-CREATE-070, AC-CREATE-060 | Exercises AC-CREATE-060 via `internal/cluster.Create` against the bufconn fake. |
| TC-U-204 | Version translation table covers each supported placeholder version | REQ-CREATE-025 | Table-driven over the placeholder version table (spec §4.1); also asserts an explicit `provider_hints.osac.release_image` override is used verbatim instead of the table lookup. No dedicated AC — covered by AC-CREATE-010's general translation assertion. |
| TC-U-205 | Missing `id` query parameter is rejected before calling OSAC | REQ-CREATE-060, AC-CREATE-040 | Exercises AC-CREATE-040 at the `StrictServerInterface` handler layer (no OSAC call reached). |
| TC-U-206 | Missing required spec field is rejected before calling OSAC | REQ-CREATE-060, AC-CREATE-050 | Exercises AC-CREATE-050 at the handler layer. |
| TC-U-207 | Multi-node-set template is rejected before calling `Clusters/Create` | REQ-CREATE-090, AC-CREATE-070 | Exercises AC-CREATE-070's multi-key case via `internal/cluster.Create` against a `bufconn` fake `ClusterTemplatesServer` returning two node-set keys; asserts `Clusters/Create` is never called. |
| TC-U-208 | Unknown `template_id` maps `ClusterTemplates/Get`'s `NotFound` to 400, not 404 | REQ-CREATE-100, REQ-ERR-010, AC-CREATE-080 | Exercises AC-CREATE-080 via `internal/cluster.Create` against a `bufconn` fake `ClusterTemplatesServer` returning `NotFound`; asserts `Clusters/Create` is never called. |
| TC-U-209 | Zero-node-set template is rejected before calling `Clusters/Create` | REQ-CREATE-090, AC-CREATE-070 | Exercises AC-CREATE-070's zero-key case via `internal/cluster.Create` against a `bufconn` fake `ClusterTemplatesServer` returning an empty `node_sets` map; asserts `Clusters/Create` is never called. |

---

## 2. Unit tests: Cluster Get (`internal/cluster`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-210 | `ACTIVE` cluster fetches kubeconfig exactly once | REQ-GET-010, REQ-GET-020, AC-GET-010 | Exercises AC-GET-010 via `internal/cluster.Get` against the bufconn fake. |
| TC-U-211 | Non-`ACTIVE` cluster never triggers a kubeconfig fetch | REQ-GET-030, AC-GET-020 | Exercises AC-GET-020 via `internal/cluster.Get` against the bufconn fake. |
| TC-U-212 | Nonexistent cluster maps to a not-found result | REQ-GET-040, AC-GET-030 | Exercises AC-GET-030's mapper-level outcome directly via `internal/cluster.Get` (the HTTP-level `404` assertion is TC-I-212). |

---

## 3. Unit tests: Cluster List (`internal/cluster`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-220 | List applies the ownership filter, default page size, and exact field values | REQ-LIST-010, REQ-LIST-030, AC-LIST-010 | Exercises AC-LIST-010 via `internal/cluster.List` against the bufconn fake. |
| TC-U-221 | `page_token` round-trips through OSAC's `offset` | REQ-LIST-020, REQ-LIST-040, AC-LIST-020 | Exercises AC-LIST-020 via two sequential `internal/cluster.List` calls against the bufconn fake. |
| TC-U-222 | List entries never populate `kubeconfig` | REQ-LIST-030, AC-LIST-030 | Exercises AC-LIST-030 via `internal/cluster.List` against the bufconn fake. |
| TC-U-223 | A `Size`/`Total` mismatch never reissues the same `page_token` (regression) | REQ-LIST-040, AC-LIST-050 | Fake `Clusters/List` returns `Items: nil, Size: 0, Total: 5` at `offset=0`; call `internal/cluster.List`; assert `NextPageToken` is nil. |

---

## 4. Unit tests: Cluster Delete (`internal/cluster`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-230 | Successful delete does not poll for confirmation | REQ-DELETE-010, REQ-DELETE-040, AC-DELETE-010 | Exercises AC-DELETE-010 via `internal/cluster.Delete` against the bufconn fake. |
| TC-U-231 | `NotFound` on Delete is treated as success | REQ-DELETE-020, AC-DELETE-020 | Exercises AC-DELETE-020's single-call outcome directly via `internal/cluster.Delete` (the real, sequential two-HTTP-request guarantee is TC-I-231, per Rule 3). |
| TC-U-232 | A genuine Delete failure is surfaced, not swallowed | REQ-DELETE-030, AC-DELETE-030 | Exercises AC-DELETE-030 via `internal/cluster.Delete` against the bufconn fake. |

---

## 5. Unit tests: Status Mapping (`internal/cluster`, pure function)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-240 | Each precedence-rule input maps to its documented value | REQ-STATUS-010, REQ-STATUS-020, AC-STATUS-010 | Exercises AC-STATUS-010, table-driven over all 10 REQ-STATUS-020 rules, calling the mapper directly (pure unit, no bufconn needed). Rules 1/2 (`Unavailable`/`NotFound`) and 5/6 (`DELETING`/`DELETE_FAILED`) are unit-only by design — see SC-M3-003/SC-M3-001. |
| TC-U-241 | `FAILED` state takes precedence over a simultaneous `DEGRADED` condition | REQ-STATUS-020, AC-STATUS-020 | Exercises AC-STATUS-020 by calling the mapper directly with both signals present. |
| TC-U-242 | Connectivity failure (`Unavailable`) is never conflated with a real `NotFound` | REQ-STATUS-020, AC-STATUS-030 | Exercises AC-STATUS-030 by calling the mapper directly for each outcome. Per SC-M3-003, this rule has no `TC-I` counterpart in M3 by design — REQ-GET-040/REQ-ERR-010 resolve both outcomes as sync HTTP errors before the mapper runs. |

---

## 6. Unit tests: Error Mapping (`internal/handlers/cluster` or shared helper)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-250 | Each gRPC code maps to its documented HTTP status and `type` | REQ-ERR-010, REQ-ERR-020, AC-ERR-010 | Exercises AC-ERR-010 via the Get handler, table-driven over the 6 gRPC codes (spec §4.6). |
| TC-U-251 | The same error-mapping function is used by Get, List, and Delete | REQ-ERR-030, AC-ERR-020 | Exercises AC-ERR-020 via the Get/List/Delete handlers against the bufconn fake. |

---

## 7. Integration tests: Cluster Create (real HTTP + router + `bufconn` OSAC fake)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-200 | Create succeeds end-to-end over real HTTP | REQ-CREATE-010, REQ-CREATE-020, REQ-CREATE-030, REQ-CREATE-050, REQ-CREATE-080, AC-CREATE-010, AC-CREATE-020 | Real-HTTP counterpart of TC-U-200/201 — same field-value assertions (incl. the resolved `node_sets` key), reached through the real router. |
| TC-I-201 | Idempotent Create: two real, sequential HTTP requests with the same `id` return the same resource | REQ-CREATE-040, AC-CREATE-030 | Real-HTTP counterpart of TC-U-202; issues two sequential `POST` requests (not direct package calls), per Rule 3. |
| TC-I-202 | Request validation is enforced at the real HTTP boundary | REQ-CREATE-060, AC-CREATE-040, AC-CREATE-050 | Real-HTTP counterpart of TC-U-205/206. |
| TC-I-203 | Host-sizing hints are not forwarded as a `host_type` override, over real HTTP | REQ-CREATE-070, AC-CREATE-060 | Real-HTTP counterpart of TC-U-203. |
| TC-I-204 | Multi-node-set template is rejected over real HTTP | REQ-CREATE-090, AC-CREATE-070 | Real-HTTP counterpart of TC-U-207. |
| TC-I-205 | Unknown `template_id` returns 400, not 404, over real HTTP | REQ-CREATE-100, REQ-ERR-010, AC-CREATE-080 | Real-HTTP counterpart of TC-U-208. |

---

## 8. Integration tests: Cluster Get (real HTTP + router + `bufconn` OSAC fake)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-210 | Get returns kubeconfig for an `ACTIVE` cluster over real HTTP | REQ-GET-010, REQ-GET-020, AC-GET-010 | Real-HTTP counterpart of TC-U-210. |
| TC-I-211 | Get omits kubeconfig for a non-`ACTIVE` cluster over real HTTP | REQ-GET-030, AC-GET-020 | Real-HTTP counterpart of TC-U-211. |
| TC-I-212 | Get returns 404 for a nonexistent cluster over real HTTP | REQ-GET-040, AC-GET-030 | Real-HTTP counterpart of TC-U-212, asserting the HTTP-level `404`/RFC 9457 `type` that TC-U-212 doesn't cover. |

---

## 9. Integration tests: Cluster List (real HTTP + router + `bufconn` OSAC fake)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-220 | List returns exact entries with the ownership filter applied, over real HTTP | REQ-LIST-010, REQ-LIST-030, AC-LIST-010 | Real-HTTP counterpart of TC-U-220. |
| TC-I-221 | Pagination round-trips across two real, sequential HTTP requests | REQ-LIST-020, REQ-LIST-040, AC-LIST-020 | Real-HTTP counterpart of TC-U-221; issues two sequential `GET` requests. |
| TC-I-222 | List responses never include `kubeconfig`, over real HTTP | REQ-LIST-030, AC-LIST-030 | Real-HTTP counterpart of TC-U-222. |
| TC-I-223 | A `page_token` this SP never issued is rejected as `400`, without calling `Clusters/List` | REQ-LIST-020, REQ-ERR-010, AC-LIST-040 | No fake `Clusters/List` behavior configured (any call fails the test); real `GET /api/v1alpha1/clusters?page_token=not-valid-base64!!!`; assert `400` with `type` exactly `INVALIDARGUMENT`, and the fake's `List` call counter is exactly `0`. |

---

## 10. Integration tests: Cluster Delete (real HTTP + router + `bufconn` OSAC fake)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-230 | Delete succeeds over real HTTP | REQ-DELETE-010, REQ-DELETE-040, AC-DELETE-010 | Real-HTTP counterpart of TC-U-230. |
| TC-I-231 | Deleting an already-deleted cluster is idempotent across two real, sequential HTTP requests | REQ-DELETE-020, AC-DELETE-020 | Real-HTTP counterpart of TC-U-231; issues two sequential `DELETE` requests (not direct package calls), per Rule 3. |
| TC-I-232 | A genuine Delete failure surfaces over real HTTP, is not swallowed by the 404-tolerance carve-out | REQ-DELETE-030, AC-DELETE-030 | Real-HTTP counterpart of TC-U-232. |

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
| TC-I-240 | Status precedence is observable over real HTTP, not just at the unit level | REQ-STATUS-020, AC-STATUS-020 | Real-HTTP counterpart of TC-U-241, via `GET /api/v1alpha1/clusters/{id}`. |
| TC-I-250 | Each gRPC error code maps to its documented HTTP status over real HTTP, identically across handlers | REQ-ERR-010, REQ-ERR-020, REQ-ERR-030, AC-ERR-010, AC-ERR-020 | Real-HTTP counterpart of TC-U-250/251. |

---

## Coverage Matrix

| Spec Section | REQ Count | AC Count | TC-U (this file) | TC-I (this file) | Pyramid complete? |
|---|---|---|---|---|---|
| 4.1 Cluster Create | 10 | 8 | 10 (TC-U-200..209) | 6 (TC-I-200..205) | Yes — every AC has both tiers; AC-CREATE-030 covered by TC-U-202 (unit) + TC-I-201 (2 real sequential HTTP requests, per rule 3); AC-CREATE-070's zero-key case (TC-U-209) is unit-only, same tier-split rationale as its multi-key case |
| 4.2 Cluster Get | 4 | 3 | 3 (TC-U-210..212) | 3 (TC-I-210..212) | Yes |
| 4.3 Cluster List | 4 | 4 | 3 (TC-U-220..222) | 4 (TC-I-220..223) | Yes — AC-LIST-040 covered by a pre-existing, untagged unit test in `list_unit_test.go` (base64/non-numeric `page_token` rejection) plus dedicated TC-I-223 for the real-HTTP boundary |
| 4.4 Cluster Delete | 4 | 3 | 3 (TC-U-230..232) | 3 (TC-I-230..232) | Yes — AC-DELETE-020 covered by TC-U-231 (unit) + TC-I-231 (2 real sequential HTTP requests, per rule 3) |
| 4.5 Status Mapping | 3 | 3 | 3 (TC-U-240..242) | 1 dedicated (TC-I-240) + incidentally via TC-I-210/211/220 | Yes — AC-STATUS-020 has both tiers (TC-U-241 + TC-I-240); AC-STATUS-010's rules 1/2/5/6 and all of AC-STATUS-030 are unit-only by design, not an incomplete pyramid (SC-M3-001/SC-M3-003 — those gRPC outcomes are resolved as sync HTTP errors before the mapper runs in M3) |
| 4.6 Error Mapping | 3 | 2 | 2 (TC-U-250..251) | 1 dedicated (TC-I-250) + incidentally via TC-I-202/212/232 | Yes |
| **Total** | **28** | **23** | **24** | **18** | |

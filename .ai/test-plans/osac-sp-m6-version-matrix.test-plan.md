# Test Plan: OSAC Service Provider — Milestone 6 (Version-Translation Compatibility Matrix)

Scope: unit **and** integration tests for Milestone 6 as specified in
[`.ai/specs/osac-sp-m6-version-matrix.spec.md`](../specs/osac-sp-m6-version-matrix.spec.md).
Follows Milestone 3's precedent of one combined file for both tiers (not
separate `-unit`/`-integration` files), since the pyramid-invariant rule
below requires reading a `REQ`/`AC`'s unit and integration case side by
side.

**Placement note (deliberate, deviates from a plausible alternative):**
this milestone touches `internal/config` (a new optional field) and
`cmd/osac-service-provider` (new startup wiring/fail-fast behavior) —
packages whose *existing* tests live in the shared, cross-cutting
`osac-sp-unit.test-plan.md`/`osac-sp-integration.test-plan.md` files. This
milestone's own cases for those two packages are kept here instead, in this
milestone's self-contained file, because: (a) they validate new
`REQ-VERSION-*` requirements authored in this milestone's own spec, not a
modification of an existing `REQ-XC-CFG-*`/`REQ-HTTP-*` requirement's
behavior; (b) this milestone's branch stacks on the *unmerged*
`feat/milestone-3-cluster-crud` (DD-133) — appending to the shared files
here risks a numbering collision with Milestone 4/5's own unmerged,
independent edits to those same files (the same category of pre-existing
concern already flagged for `DD-080`..`086` in Milestones 4/5); (c) it keeps
this milestone's PR diff self-contained and reviewable against M3's tip
alone, matching DD-133's rationale.

**Framework:** Ginkgo v2 + Gomega. Unit tests:
`internal/versionmatrix/*_unit_test.go`, `internal/registration/*_unit_test.go`,
`internal/cluster/*_unit_test.go`, `internal/handlers/cluster/*_unit_test.go`,
`internal/config/*_unit_test.go`, `cmd/osac-service-provider/*_unit_test.go`
— pure business logic against fakes (`bufconn` OSAC server, fake
`http.RoundTripper` for `control-plane`, in-process `run`/`mainRun` calls),
no real HTTP/network. Integration tests:
`internal/handlers/cluster/*_integration_test.go`,
`internal/registration/*_integration_test.go` (or a shared fixture,
implementation's choice),
`cmd/osac-service-provider/*_integration_test.go` — real HTTP
server/router and a real `httptest.Server` fake `control-plane`, same
techniques Milestones 1 and 3 already established. Run with:

```bash
go run github.com/onsi/ginkgo/v2/ginkgo -r --race -focus "TC-U-5" ./internal/... ./cmd/...
go run github.com/onsi/ginkgo/v2/ginkgo -r --race -focus "TC-I-5" ./internal/... ./cmd/...
```

## Enforcement rules (gate before implementation, re-checked at REFACTOR)

Binding, not advisory — same 4 rules Milestone 3 established:

1. **AC-first, Given/When/Then.** Every `AC-VERSION-*` below was written
   against the spec before any `TC-*` here was drafted.
2. **No existence-only/implementation-shape assertions.** Banned: `err ==
   nil`, `response != nil`, `Load was called`. Required: exact returned
   values (`Lookup("1.29")` returns this exact image string, the response's
   `type` equals exactly `INVALIDARGUMENT`, the fake recorded exactly this
   `release_image`).
3. **Pyramid invariant.** Every `REQ-VERSION-*`/`AC-VERSION-*` pair has at
   least one `TC-U-*` and at least one `TC-I-*` — either dedicated, or
   (matching Milestone 3's own precedent for its Status/Error Mapping
   topics, which also have no independent HTTP surface of their own)
   proven incidentally through another dedicated integration case. See the
   Coverage Matrix for the explicit pairing.
4. **100% coverage of new testable code** (non-generated) in
   `internal/versionmatrix/` and every touched line in `internal/registration/`,
   `internal/cluster/`, `internal/handlers/cluster/`, `internal/config/`,
   `cmd/osac-service-provider/`, verified via `go test -cover`/`make
   test-cover`.

---

## 1. Unit tests: Version Matrix Package (`internal/versionmatrix`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-500 | `DefaultMatrix` resolves each documented version to its exact release image; an undocumented version misses | REQ-VERSION-010, REQ-VERSION-020, AC-VERSION-010 | Table-driven: call `DefaultMatrix.Lookup(v)` for each of `"1.29"`, `"1.30"`, `"1.31"`, `"1.32"`, `"1.33"`; assert each returns `ok==true` and the exact documented OCP `4.16`-`4.20` `-multi` release image string. Separately call `Lookup("1.99")`; assert `ok==false`. |
| TC-U-501 | `SupportedVersions` returns exactly the matrix's keys, sorted ascending | REQ-VERSION-030, AC-VERSION-020 | Construct `Matrix{"1.31":"x","1.29":"y","1.33":"z"}` (deliberately non-sorted insertion order); call `SupportedVersions()`; assert the returned slice equals exactly `[]string{"1.29","1.31","1.33"}`. |
| TC-U-502 | `Load("")` returns `DefaultMatrix` unchanged | REQ-VERSION-040 | Call `Load("")`; assert the returned `Matrix` equals `DefaultMatrix` field-for-field (`Equal(versionmatrix.DefaultMatrix)`). |
| TC-U-503 | `Load(path)` with a valid override file fully replaces, not merges with, the default | REQ-VERSION-040, AC-VERSION-030 | Write a temp file containing `{"1.34":"quay.io/openshift-release-dev/ocp-release:4.21.0-multi"}`; call `Load(path)`; assert `Lookup("1.34")` returns that exact image with `ok==true`, and `Lookup("1.29")` (a `DefaultMatrix`-only entry) returns `ok==false`. |
| TC-U-504 | `Load(path)` fails fast on a missing, malformed, or empty override file (table-driven) | REQ-VERSION-040, AC-VERSION-040 | Table over: (a) a path that does not exist, (b) a temp file containing `not json`, (c) a temp file containing `{}`; for each, call `Load(path)`; assert a non-nil error and a nil `Matrix` in every case. |
| TC-U-505 | `Load(path)` fails fast on an override file with an empty version key or an empty `release_image` value (table-driven, regression) | REQ-VERSION-040, AC-VERSION-040 | Table over: (a) a temp file containing `{"":"quay.io/openshift-release-dev/ocp-release:4.16.0-multi"}`, (b) a temp file containing `{"1.29":""}`; for each, call `Load(path)`; assert a non-nil error and a nil `Matrix` in every case. Regression for a review finding on PR #26: `len(m)==0` alone only rejects a wholly empty `{}`, not a one-entry map with a blank key or blank value. |

---

## 2. Unit tests: Registration Consumes the Matrix (`internal/registration`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-510 | Cluster registration payload's `kubernetes_supported_versions` is derived from the injected matrix, not a hardcoded list | REQ-VERSION-050, AC-VERSION-050 | Construct a `Registrar` with a 3-entry test `Matrix` (values/keys distinct from `DefaultMatrix`) via the updated `NewRegistrar(cfg, logger, matrix, ...)`; trigger the cluster registration call; capture the request body sent to the fake round-tripper; assert `metadata.kubernetes_supported_versions` equals exactly that test matrix's `SupportedVersions()` (proving it is not `DefaultMatrix`'s keys, and not the old removed package-level variable). |

---

## 3. Unit tests: Cluster Service Consumes the Matrix (`internal/cluster`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-520 | Create dispatches the injected matrix's `release_image`, per version | REQ-VERSION-060, AC-VERSION-060 | Construct a `Service` via `New(client, matrix)` with a test `Matrix` whose values differ from `DefaultMatrix`; table-driven over each of its entries, call `Create`; assert the fake's recorded `Cluster.spec.release_image` equals exactly that entry's mapped value. |
| TC-U-521 | An explicit `release_image` override bypasses the injected matrix entirely, even for an unmapped version | REQ-VERSION-060, AC-VERSION-070 | Construct a `Service` with a test `Matrix` that has no entry for `"9.99"`; call `Create` with `spec.version="9.99"` and `provider_hints.osac.release_image="custom-image"`; assert the fake's recorded `release_image` equals exactly `"custom-image"` (not empty, not a matrix miss). |
| TC-U-522 | `SupportsVersion` reports matrix membership exactly | REQ-VERSION-070 | Construct a `Service` with a known 2-entry test `Matrix`; call `SupportsVersion` for one of its keys (assert `true`) and for a key absent from it (assert `false`). |

---

## 4. Unit tests: Create Validation Rejects Unsupported Versions (`internal/handlers/cluster`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-530 | An unsupported version with no override is rejected before calling OSAC | REQ-VERSION-080, AC-VERSION-080 | Construct a `Handler` wrapping a `Service` whose injected matrix has no entry for `"9.99"`; invoke `CreateCluster` (`StrictServerInterface` layer, same white-box technique as M3's `TC-U-205`/`206`) with `spec.version="9.99"` and no `release_image` hint; assert the response is `400`-mapped (`v1alpha1.ErrorTypeINVALIDARGUMENT`) and the fake OSAC server recorded zero `Clusters/Create` calls. |
| TC-U-531 | An explicit `release_image` override bypasses the unsupported-version rejection | REQ-VERSION-080, AC-VERSION-070 | Same fixture as TC-U-530, but the request additionally sets `provider_hints.osac.release_image="custom-image"`; invoke `CreateCluster`; assert it is **not** rejected (no synthetic validation error is returned) and the fake OSAC server recorded exactly one `Clusters/Create` call with `release_image=="custom-image"`. |

---

## 5. Unit tests: Config Surface (`internal/config`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-540 | `SP_VERSION_MATRIX_PATH` loads exactly when set | REQ-VERSION-090 | Set `SP_VERSION_MATRIX_PATH=/tmp/matrix.json` (plus every other required var, same pattern as `osac-sp-unit.test-plan.md`'s `TC-U-001`); call `Load()`; assert `Config.VersionMatrix.Path` equals exactly that value. |
| TC-U-541 | `SP_VERSION_MATRIX_PATH` defaults to the empty string when unset | REQ-VERSION-090 | Leave `SP_VERSION_MATRIX_PATH` unset (all other required vars valid); call `Load()`; assert `Config.VersionMatrix.Path == ""`. |

---

## 6. Unit tests: `main.go` Wiring (`cmd/osac-service-provider`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-U-550 | `run` wraps and returns a version-matrix load failure, before binding a listener | REQ-VERSION-090, AC-VERSION-090 | Set `SP_VERSION_MATRIX_PATH` to a nonexistent file path (all other required vars valid, same in-process technique as `main_unit_test.go`'s existing `TC-U-094`..`096`); call `run` directly; assert it returns a non-nil error mentioning "version matrix" and that no listener was bound (mirrors `TC-U-094`'s config-load-failure assertion style). |

`run`'s happy path (successful matrix load, correct wiring into both
`registration.NewRegistrar` and `cluster.New`) is integration-tier scope
only (§9 below, `TC-I-521`) — same convention `main_unit_test.go` already
documents for `mainRun`'s own happy path (proven only via
`main_integration_test.go`).

---

## 7. Integration tests: Create over Real HTTP (`internal/handlers/cluster`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-500 | Create rejects an unsupported version with no override, over real HTTP | REQ-VERSION-080, AC-VERSION-080 | Start the real HTTP server wired to a `Handler`/`Service` constructed with a test matrix lacking an entry for `"9.99"`; issue a real `POST /api/v1alpha1/clusters?id=X` with `spec.version="9.99"` and no `release_image` hint; assert the real response is `400` (RFC 9457, `type` exactly `INVALIDARGUMENT`) and the fake OSAC server recorded zero `Clusters/Create` calls. |
| TC-I-501 | An explicit `release_image` override bypasses matrix validation, over real HTTP | REQ-VERSION-070, AC-VERSION-070 | Same fixture as TC-I-500, but the request additionally sets `provider_hints.osac.release_image="custom-image"`; issue the real request; assert `201 Created` and the fake OSAC server's recorded `Cluster.spec.release_image` equals exactly `"custom-image"`. |
| TC-I-502 | Create dispatches an injected, non-default matrix's `release_image`, over real HTTP | REQ-VERSION-060, AC-VERSION-010, AC-VERSION-030, AC-VERSION-060 | Construct the fixture with a test matrix `{"1.40":"custom-release-image"}` (a version/image pair absent from `DefaultMatrix`); issue a real Create request with `spec.version="1.40"` and no override; assert `201 Created` and the fake OSAC server's recorded `release_image` equals exactly `"custom-release-image"` — proving the real HTTP path consults the actually-injected matrix, not a hardcoded default (incidental integration-tier proof for Topic 4.1's lookup/full-replace behavior, mirroring M3's Status/Error Mapping precedent). |

---

## 8. Integration tests: Registration against a Fake `control-plane` (`internal/registration`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-510 | Cluster registration's advertised versions equal the injected matrix's keys, against a fake `control-plane` | REQ-VERSION-050, AC-VERSION-020, AC-VERSION-050 | Using the same `httptest.Server` fake-`control-plane` harness as `osac-sp-integration.test-plan.md`'s `TC-I-020`/`021`, construct a `Registrar` with a distinct test matrix; call `Start()`; assert the fake's recorded cluster registration request body's `metadata.kubernetes_supported_versions` equals exactly that test matrix's `SupportedVersions()`. |

---

## 9. Integration tests: Full-Stack Startup (`cmd/osac-service-provider`)

| TC ID | Test Name | Validates | Description |
|-------|-----------|-----------|-------------|
| TC-I-520 | Startup fails fast when `SP_VERSION_MATRIX_PATH` is set but invalid | REQ-VERSION-090, AC-VERSION-040, AC-VERSION-090 | Start the SP binary/entrypoint (same technique as `osac-sp-integration.test-plan.md`'s `TC-I-006`) with `SP_VERSION_MATRIX_PATH` pointing at a nonexistent file and all other required config valid; assert the process exits with a non-zero status before the HTTP listener is opened, and before any registration request reaches the fake `control-plane`. |
| TC-I-521 | Cold start advertises `DefaultMatrix`'s versions when `SP_VERSION_MATRIX_PATH` is unset | REQ-VERSION-090, AC-VERSION-100 | With `SP_VERSION_MATRIX_PATH` unset and all fakes healthy (mirrors `osac-sp-integration.test-plan.md`'s `TC-I-030` full-stack cold-start smoke harness), start the SP from cold; assert the fake `control-plane` eventually records a cluster registration whose `kubernetes_supported_versions` equals exactly `versionmatrix.DefaultMatrix.SupportedVersions()`. |

---

## Coverage Matrix

| Spec Section | REQ Count | AC Count | TC-U (this file) | TC-I (this file) | Pyramid complete? |
|---|---|---|---|---|---|
| 4.1 Version Matrix Package | 4 | 4 | 5 (TC-U-500..504) | 0 dedicated + incidentally via TC-I-502 (AC-010/030) and TC-I-510 (AC-020) and TC-I-520 (AC-040) | Yes — no HTTP surface of its own, same treatment as M3's Status/Error Mapping topics |
| 4.2 Matrix Consumption | 5 | 6 | 9 (TC-U-510, 520..522, 530..531, 540..541, 550) | 6 (TC-I-500..502, 510, 520..521) | Yes — every AC has both tiers |
| **Total** | **9** | **10** | **14** | **6** | |

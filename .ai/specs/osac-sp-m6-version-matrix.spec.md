# Specification: OSAC Service Provider — Milestone 6 (Version Compatibility and Catalog Resolution)

## 1. Overview

Milestone 6 defines a single, shared, validated version-support matrix for
the Kubernetes minors this provider advertises and accepts.

The current OSAC public API does not accept a container `release_image` on
`Clusters/Create`. It accepts a typed `ClusterVersionReference`. Therefore
Create uses the shared matrix only to decide whether a requested Kubernetes
minor is supported, then queries OSAC's `ClusterVersions/List` catalog and
selects the newest matching SemVer z-stream. The matrix's legacy image values
remain part of the JSON/configuration shape for compatibility, but are not sent
to OSAC.

The delivered behavior has one source of truth for supported Kubernetes minor
versions. Registration advertises its keys, Create validates against the same
keys, and Create resolves the concrete OSAC version reference from the live
catalog. Unsupported versions and the obsolete non-empty
`provider_hints.osac.release_image` field are rejected before OSAC mutation.

**This spec covers version support and catalog resolution.** It does not
expand the set of supported versions: the same 5 Kubernetes minor versions
(`1.29`-`1.33`, mapping to OpenShift `4.16`-`4.20`) are carried in the
matrix's hardcoded default. The matrix is a single shared, testable component;
it does not add version support beyond those five minors.
OSAC catalog lookup is in scope because the current public API exposes
`ClusterVersions/List`; the matrix remains the operator-controlled admission
policy while the catalog supplies the concrete resource reference.

**Reference documents:**

- [OSAC SP Enhancement](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md) — the enhancement's own "the SP maintains an internal compatibility matrix" language
- [Milestone 1 spec](./osac-sp.spec.md) — SC-001 (superseded by this milestone, see below), REQ-REG-040
- [Milestone 3 spec](./osac-sp-m3-cluster-crud.spec.md) — REQ-CREATE-025, REQ-CREATE-060 (this milestone's `validateCreateRequest` extension reuses the exact same `InvalidArgument`/`mapError` path that requirement already established)
- [Design Decisions](../decisions/osac-sp.decisions.md) — DD-130 through DD-133 and DD-238 (catalog resolution)
- `acm-cluster-service-provider` (sibling SP, same OpenShift-provisioning problem) — precedent for the hardcoded-default-plus-optional-JSON-override pattern and hard-rejection-of-unsupported-versions behavior adopted below

---

## 2. Architecture

New package: **`internal/versionmatrix`** — a small, dependency-free package
(no import of `api/v1alpha1` or any other `internal/*` package) exposing a
`Matrix` type, a hardcoded `DefaultMatrix`, and a `Load` function. Placed as
a new top-level `internal/` package rather than inside either existing
consumer, specifically to avoid an awkward cross-import between
`internal/registration` and `internal/cluster` in either direction (neither
currently imports the other, and this milestone must not introduce that
coupling just to share one map) — see DD-130.

Two existing packages become **consumers**, each holding their own
`versionmatrix.Matrix` value (the same value, since both are constructed
from the same `main.go`-loaded instance — a `Matrix` is an immutable value
type, so sharing it by value across two owners is safe and requires no
synchronization):

- **`internal/registration`** — `Registrar` gains a `matrix` field; its
  cluster registration payload's `kubernetes_supported_versions` is now
  `matrix.SupportedVersions()` instead of a separately hand-typed slice.
- **`internal/cluster`** — `Service` gains a `matrix` field and a
  `ClusterVersionsClient`; `SupportsVersion` checks matrix membership, while
  Create lists OSAC `ClusterVersion` resources and selects the newest matching
  SemVer z-stream. The obsolete `provider_hints.osac.release_image` override
  is rejected.

`internal/handlers/cluster`'s `Handler.validateCreateRequest` (existing,
Milestone 3) gains one more pre-flight case, querying `Service.SupportsVersion`
— see §4.2.

```
                        cmd/osac-service-provider/main.go
                                       |
                      versionmatrix.Load(cfg.VersionMatrix.Path)
                       (empty path -> DefaultMatrix; non-empty
                        path -> JSON file, full replace, fail-fast)
                                       |
                    +------------------+------------------+
                    |                                     |
                    v                                     v
      registration.NewRegistrar(cfg, logger,   cluster.New(client, matrix)
                     matrix, ...)                          |
                    |                                     |
                     v                                     v
      kubernetes_supported_versions          ClusterVersions/List -> newest matching
      = matrix.SupportedVersions()           SemVer ClusterVersionReference
                                                             |
                                                             v
                                           internal/handlers/cluster.validateCreateRequest:
                                           release_image present? -> 400 InvalidArgument
                                           unsupported version? -> 400 InvalidArgument
```

No changes to `internal/osac`, `internal/apiserver`, `internal/httperror`, or
the OpenAPI schema — no new error `type`/schema is introduced (§4.2).

---

## 3. Topic Dependency Graph

| # | Topic | Prefix | Depends On |
|---|-------|--------|------------|
| 1 | Version Matrix Package | VERSION | None (new, standalone package) |
| 2 | Matrix Consumption (registration, translation, validation, config) | VERSION | Topic 1; Milestone 1 `internal/registration`/`internal/config`; Milestone 3 `internal/cluster`/`internal/handlers/cluster` |

```
Topic 1: Version Matrix Package  --->  Topic 2: Matrix Consumption
```

---

## 4. Topic Specifications

### 4.1 Version Matrix Package (`internal/versionmatrix`)

#### Overview

A new, standalone package providing the `Matrix` type (a Kubernetes minor
version -> retained compatibility image metadata mapping), a hardcoded default instance
carrying forward the same 5 entries Milestones 1/3 already established, and
a loader that optionally replaces the default entirely from an external
JSON file.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-VERSION-010 | The package MUST expose a `Matrix` type mapping a Kubernetes minor version string (e.g. `"1.29"`) to retained compatibility image metadata, with a `Lookup(version string) (string, bool)` method returning the metadata and whether `version` is present | MUST | Matrix keys, not image values, control support admission |
| REQ-VERSION-020 | The package MUST expose a hardcoded `DefaultMatrix` value containing exactly the five supported minor versions (`"1.29"`-`"1.33"`) and their retained OpenShift image metadata (`4.16.0`-`4.20.0`, `-multi`) | MUST | The metadata is retained for configuration compatibility; Create resolves a live `ClusterVersionReference` |
| REQ-VERSION-030 | `Matrix` MUST expose a `SupportedVersions() []string` method returning the matrix's keys sorted in ascending lexical order, for deterministic consumption by registration payloads and tests | MUST | |
| REQ-VERSION-040 | The package MUST expose `Load(path string) (Matrix, error)`: when `path == ""`, it MUST return `DefaultMatrix` unchanged; when `path != ""`, it MUST read `path` as a JSON object (`{"<k8s-version>": "<release_image>", ...}`) and, on success, return **that file's content alone** as the resulting `Matrix` — fully replacing, not merging with, `DefaultMatrix`. `Load` MUST return a non-nil error (and a nil `Matrix`) if `path != ""` and the file is missing, unreadable, not valid JSON, decodes to zero entries, or contains any entry with an empty version key or an empty `release_image` value | MUST | Full-replace (not merge) and fail-fast-on-empty (including blank-key/blank-value entries) are both deliberate — see DD-131 |

#### Configuration Introduced

None directly (the env var consuming `Load`'s `path` argument is introduced
in Topic 2, §4.2, since it is `internal/config`'s concern, not this
package's).

#### Acceptance Criteria

##### AC-VERSION-010: `DefaultMatrix` resolves each of the 5 documented versions to its exact release image

- **Validates:** REQ-VERSION-010, REQ-VERSION-020
- **Given** `DefaultMatrix` (used whenever `Load` is called with `path == ""`)
- **When** `Lookup` is called, table-driven, for each of `"1.29"`, `"1.30"`, `"1.31"`, `"1.32"`, `"1.33"`
- **Then** each call MUST return `ok == true` and the exact documented release image for that version (e.g. `"1.29"` -> `"quay.io/openshift-release-dev/ocp-release:4.16.0-multi"`) — and `Lookup` for an undocumented version (e.g. `"1.99"`) MUST return `ok == false`

##### AC-VERSION-020: `SupportedVersions` returns exactly the matrix's keys, sorted

- **Validates:** REQ-VERSION-030
- **Given** a `Matrix` with a known, non-alphabetically-inserted set of keys
- **When** `SupportedVersions()` is called
- **Then** the returned slice MUST equal exactly the matrix's keys in ascending sorted order (not insertion order, not map iteration order)

##### AC-VERSION-030: A JSON override file fully replaces the default table, it does not merge with it

- **Validates:** REQ-VERSION-040
- **Given** a JSON file containing only `{"1.34": "quay.io/openshift-release-dev/ocp-release:4.21.0-multi"}` (a version absent from `DefaultMatrix`)
- **When** `Load(path)` is called with that file's path
- **Then** the returned `Matrix`'s `Lookup("1.34")` MUST return `ok == true` with that exact image, **and** `Lookup("1.29")` (a `DefaultMatrix`-only entry) MUST return `ok == false` — proving the override replaced, rather than merged with, the default

##### AC-VERSION-040: A missing, malformed, or empty override file fails fast

- **Validates:** REQ-VERSION-040
- **Given**, in turn: (a) a `path` pointing to a nonexistent file, (b) a `path` pointing to a file containing invalid JSON, (c) a `path` pointing to a file containing valid JSON that decodes to an empty object `{}`, (d) a file with an empty version key (e.g. `{"": "quay.io/openshift-release-dev/ocp-release:4.16.0-multi"}`), (e) a file with an empty `release_image` value (e.g. `{"1.29": ""}`)
- **When** `Load(path)` is called for each case
- **Then** each call MUST return a non-nil error and a nil `Matrix`

#### Dependencies

None — standalone new package.

---

### 4.2 Matrix Consumption (Registration, Catalog Resolution, Validation, Config)

#### Overview

Wires the Topic 4.1 package into registration's capability advertisement and
Create's pre-flight request validation. Create also resolves supported minors
against OSAC's live `ClusterVersions/List` catalog and selects the newest valid
SemVer z-stream. The optional configuration surface controlling `Load`'s
`path` argument is included here.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-VERSION-050 | `internal/registration.Registrar` MUST accept a `versionmatrix.Matrix` at construction and its cluster registration payload's `kubernetes_supported_versions` MUST be exactly `matrix.SupportedVersions()` — the package-level `kubernetesSupportedVersions` variable (M1) is removed | MUST | Supersedes `osac-sp.spec.md` SC-001; single source of truth, no possible drift between registration and translation |
| REQ-VERSION-060 | `internal/cluster.Service` MUST accept a `versionmatrix.Matrix` and `ClusterVersionsClient` at construction; Create MUST list OSAC `ClusterVersion` resources and resolve `spec.version` to a `ClusterVersionReference` for the newest valid SemVer candidate whose version equals the requested minor or begins with that minor followed by a dot | MUST | The live catalog, not a local `release_image`, supplies the concrete OSAC version reference |
| REQ-VERSION-070 | `internal/cluster.Service` MUST expose `SupportsVersion(version string) bool`, reporting whether `version` has a matrix entry, for `internal/handlers/cluster`'s pre-flight validation (REQ-VERSION-080) to query without duplicating or directly importing the matrix | MUST | Keeps the matrix instance itself owned by exactly one component (`Service`) even though two packages need to consult it |
| REQ-VERSION-080 | `internal/handlers/cluster.Handler`'s existing `validateCreateRequest` (M3, REQ-CREATE-060) MUST reject a non-empty legacy `provider_hints.osac.release_image` and MUST reject a `spec.version` absent from the injected matrix, using the same synthetic `codes.InvalidArgument` status mapped to `400 Bad Request` before ever calling `Service.Create`/dispatching to OSAC | MUST | The current OSAC API uses `ClusterVersionReference`; no image override bypass exists |
| REQ-VERSION-090 | `internal/config.Config` MUST add an optional `SP_VERSION_MATRIX_PATH` environment variable (empty/unset is valid and MUST result in `DefaultMatrix` being used); `cmd/osac-service-provider`'s `run` MUST call `versionmatrix.Load(cfg.VersionMatrix.Path)` once at startup, before starting any subsystem, and MUST fail fast (return a non-nil error, causing `mainRun` to exit non-zero) if `Load` returns an error | MUST | Mirrors REQ-XC-CFG-020's existing fail-fast convention (`osac-sp.spec.md`) for the case where the var *is* set but its file is missing/malformed |

#### Configuration Introduced

| Env Var | Required | Default | Description |
|---|---|---|---|
| `SP_VERSION_MATRIX_PATH` | No | *(empty — use `DefaultMatrix`)* | Optional path to a JSON file fully replacing the default supported-version policy and retained image metadata. If set, the file MUST exist and contain a valid, non-empty `{"<k8s-version>": "<release_image>", ...}` object, or the service fails to start (REQ-VERSION-090). |

#### Acceptance Criteria

##### AC-VERSION-050: Registration's advertised versions are exactly the matrix's keys — no possible drift

- **Validates:** REQ-VERSION-050
- **Given** a `Registrar` constructed with a known `Matrix` (e.g. a 3-entry test matrix, deliberately different from `DefaultMatrix` to prove it is not hardcoded)
- **When** the cluster registration payload is built
- **Then** its `metadata.kubernetes_supported_versions` MUST equal exactly `matrix.SupportedVersions()` for that same matrix — proving the value is derived, not separately maintained

##### AC-VERSION-060: Create resolves each supported version through the OSAC catalog

- **Validates:** REQ-VERSION-060
- **Given** a `Service` constructed with a known `Matrix` and an OSAC catalog containing a matching `ClusterVersion`
- **When** `Create` is called for a supported version
- **Then** the dispatched `Cluster.spec.version` MUST contain a `ClusterVersionReference` with the selected catalog item's exact `id` and metadata name

##### AC-VERSION-070: The obsolete `release_image` override is rejected

- **Validates:** REQ-VERSION-060, REQ-VERSION-080
- **Given** a Create request with a non-empty `provider_hints.osac.release_image`
- **When** the request is validated
- **Then** the response MUST be `400 Bad Request` (`INVALIDARGUMENT`) and the fake OSAC server MUST record zero `Clusters/Create` calls

##### AC-VERSION-080: An unsupported version with no override is rejected before ever calling OSAC

- **Validates:** REQ-VERSION-080
- **Given** a Create request with `spec.version` set to a value absent from the injected matrix and no `provider_hints.osac.release_image` set
- **When** the request is validated
- **Then** the response MUST be `400 Bad Request` (RFC 9457, `type` exactly `INVALIDARGUMENT`) and the fake OSAC server MUST record zero `Clusters/Create` calls

##### AC-VERSION-090: The service fails to start when `SP_VERSION_MATRIX_PATH` is set but invalid

- **Validates:** REQ-VERSION-090
- **Given** `SP_VERSION_MATRIX_PATH` set to a path that does not exist
- **When** `run(ctx, logger)` is invoked
- **Then** it MUST return a non-nil error identifying the version matrix, and MUST NOT start the HTTP listener, `osac.Bootstrap`, or the registrar

##### AC-VERSION-100: The service uses `DefaultMatrix` when `SP_VERSION_MATRIX_PATH` is unset

- **Validates:** REQ-VERSION-090
- **Given** `SP_VERSION_MATRIX_PATH` unset (all other required config valid)
- **When** `run(ctx, logger)` is invoked
- **Then** it MUST start successfully, and the resulting registrar's advertised `kubernetes_supported_versions` MUST equal `versionmatrix.DefaultMatrix.SupportedVersions()` exactly

##### AC-VERSION-110: Create selects the newest matching SemVer z-stream

- **Validates:** REQ-VERSION-060
- **Given** an OSAC catalog containing matching versions `1.29.2` and `1.29.10`, plus an unrelated `1.30.99`, in that order
- **When** Create is called for Kubernetes minor `1.29`
- **Then** the dispatched `Cluster.spec.version.name` MUST be the catalog item's name for `1.29.10`, regardless of catalog order

#### Dependencies

Depends on Topic 4.1 (Version Matrix Package); Milestone 1's
`internal/registration`/`internal/config` (REQ-REG-040, REQ-XC-CFG-020);
Milestone 3's `internal/cluster`/`internal/handlers/cluster` (REQ-CREATE-025,
REQ-CREATE-060).

---

## 5. Cross-Cutting Concerns

None new. Logging and configuration-management requirements
(`osac-sp.spec.md` §5) are unchanged; `SP_VERSION_MATRIX_PATH` follows the
same fail-fast-on-invalid-required-input logging convention already
established for every other required-when-set config value.

---

## 6. Consolidated Configuration Reference

| Env Var | Required | Default | Introduced |
|---|---|---|---|
| `SP_VERSION_MATRIX_PATH` | No | *(empty — use `DefaultMatrix`)* | Milestone 6, §4.2 |

See `osac-sp.spec.md` §6 for the full table of pre-existing configuration
(unchanged by this milestone).

---

## 7. Design Decisions

See `.ai/decisions/osac-sp.decisions.md` DD-130 through DD-133.

---

## 8. Requirement ID Index

| Prefix | Topic | Count |
|--------|-------|-------|
| REQ-VERSION-0NN (010-040) | 4.1: Version Matrix Package | 4 |
| REQ-VERSION-0NN (050-090) | 4.2: Matrix Consumption | 5 |
| **Total** | | **9** |

# Specification: OSAC Service Provider — Milestone 6 (Version-Translation Compatibility Matrix)

## 1. Overview

Milestone 6 per [issue #1](https://github.com/dcm-project/osac-service-provider/issues/1)'s
suggested delivery milestones ("kubeconfig retrieval + version translation
matrix" — kubeconfig retrieval already landed in Milestone 3, `REQ-GET-020`/
`REQ-GET-030`): replace the two independently hardcoded, duplicated
Kubernetes-version lists introduced as explicit placeholders in Milestones 1
and 3 with a single, shared, validated version-translation compatibility
matrix.

Today, on the branch this milestone stacks on
(`feat/milestone-3-cluster-crud`), there are two hand-maintained lists that
are supposed to stay in sync but have no shared source of truth:

- `internal/registration/registration.go`'s `kubernetesSupportedVersions` —
  advertised to `control-plane` as cluster-service-type capability metadata
  (`osac-sp.spec.md` REQ-REG-040, SC-001).
- `internal/cluster/translate.go`'s `releaseImageByVersion` — a 5-entry map
  used to translate `spec.version` into an OSAC `release_image` on Create
  (`osac-sp-m3-cluster-crud.spec.md` REQ-CREATE-025).

Both variables' own comments already say the full compatibility matrix is
Milestone 6 scope. Additionally, `releaseImage()` currently **silently falls
back** to OSAC's template default `release_image` when a requested version
has no table entry — no validation, no error surfaced to the caller
(§2 of the OSAC SP enhancement's own design describes an internal
compatibility matrix, not a silent-fallback one).

**This spec covers the version-translation matrix only.** It does not
expand the set of supported versions: the same 5 Kubernetes minor versions
(`1.29`-`1.33`, mapping to OpenShift `4.16`-`4.20`) that Milestones 1/3
already established are carried forward verbatim as this matrix's hardcoded
default — this milestone formalizes and validates that data through a
single shared, testable component; it does not add new version support.
Also out of scope: any dynamic version-discovery call to OSAC — confirmed
directly against `osac-project/fulfillment-service`'s
`cluster_template_type.proto` that `ClusterTemplateSpecDefaults` only
exposes a single per-template *default* `release_image`, with no API to
enumerate supported Kubernetes/OpenShift versions, matching the enhancement
doc's own language that "the SP maintains an internal compatibility
matrix." No dynamic-query shortcut exists; the matrix must stay
self-maintained.

**Branch stacking:** unlike Milestone 5 (whose spec/implementation only
needed Milestone 3/4's *types* and was delivered as a self-contained PR off
`main`, validated via a throwaway merge worktree — DD-075), this milestone
edits Milestone 3's own files directly (`internal/registration/registration.go`,
`internal/cluster/translate.go`, `internal/handlers/cluster/create.go`).
Consequently this milestone's branch/PR stacks directly on top of
`feat/milestone-3-cluster-crud` (mirroring this repo's own e2e PR chain
precedent: `#24` on `#23` on `#20` on `#18`), not on `main` — see DD-133.

**Reference documents:**

- [OSAC SP Enhancement](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md) — the enhancement's own "the SP maintains an internal compatibility matrix" language
- [Milestone 1 spec](./osac-sp.spec.md) — SC-001 (superseded by this milestone, see below), REQ-REG-040
- [Milestone 3 spec](./osac-sp-m3-cluster-crud.spec.md) — REQ-CREATE-025, REQ-CREATE-060 (this milestone's `validateCreateRequest` extension reuses the exact same `InvalidArgument`/`mapError` path that requirement already established)
- [Design Decisions](../decisions/osac-sp.decisions.md) — DD-130 through DD-133 (new, this milestone)
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
- **`internal/cluster`** — `Service` gains a `matrix` field; `releaseImage()`
  looks up `spec.Version` in the injected matrix (with the existing
  `provider_hints.osac.release_image` override still taking precedence);
  a new `Service.SupportsVersion(version string) bool` method lets
  `internal/handlers/cluster` query matrix membership without importing
  `internal/versionmatrix` itself or duplicating the matrix.

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
      kubernetes_supported_versions          releaseImage(): matrix.Lookup(version)
      = matrix.SupportedVersions()           SupportsVersion(): matrix.Lookup(version) ok?
                                                            |
                                                            v
                                          internal/handlers/cluster.validateCreateRequest:
                                          override present? -> skip check
                                          else -> svc.SupportsVersion(version) required,
                                                  else 400 InvalidArgument, pre-flight
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
version -> OSAC `release_image` mapping), a hardcoded default instance
carrying forward the same 5 entries Milestones 1/3 already established, and
a loader that optionally replaces the default entirely from an external
JSON file.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-VERSION-010 | The package MUST expose a `Matrix` type mapping a Kubernetes minor version string (e.g. `"1.29"`) to an OSAC `release_image` string, with a `Lookup(version string) (string, bool)` method returning the mapped image and whether `version` is present | MUST | |
| REQ-VERSION-020 | The package MUST expose a hardcoded `DefaultMatrix` value containing exactly the same 5 entries as Milestone 3's `releaseImageByVersion` (`"1.29"`-`"1.33"` -> the OpenShift `4.16.0`-`4.20.0` `-multi` release images) — this milestone carries the data forward unchanged, it does not add or remove entries | MUST | Data continuity with M1 SC-001 / M3 REQ-CREATE-025 |
| REQ-VERSION-030 | `Matrix` MUST expose a `SupportedVersions() []string` method returning the matrix's keys sorted in ascending lexical order, for deterministic consumption by registration payloads and tests | MUST | |
| REQ-VERSION-040 | The package MUST expose `Load(path string) (Matrix, error)`: when `path == ""`, it MUST return `DefaultMatrix` unchanged; when `path != ""`, it MUST read `path` as a JSON object (`{"<k8s-version>": "<release_image>", ...}`) and, on success, return **that file's content alone** as the resulting `Matrix` — fully replacing, not merging with, `DefaultMatrix`. `Load` MUST return a non-nil error (and a nil `Matrix`) if `path != ""` and the file is missing, unreadable, not valid JSON, or decodes to zero entries | MUST | Full-replace (not merge) and fail-fast-on-empty are both deliberate — see DD-131 |

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
- **Given**, in turn: (a) a `path` pointing to a nonexistent file, (b) a `path` pointing to a file containing invalid JSON, (c) a `path` pointing to a file containing valid JSON that decodes to an empty object `{}`
- **When** `Load(path)` is called for each case
- **Then** each call MUST return a non-nil error and a nil `Matrix`

#### Dependencies

None — standalone new package.

---

### 4.2 Matrix Consumption (Registration, Translation, Validation, Config)

#### Overview

Wires the Topic 4.1 package into the three places that currently either
hand-maintain their own version list or silently fall back on an unmapped
version: registration's capability advertisement, Create's `release_image`
translation, and Create's pre-flight request validation. Also adds the
optional configuration surface controlling `Load`'s `path` argument.

#### Requirements

| ID | Requirement | Priority | Notes |
|----|-------------|----------|-------|
| REQ-VERSION-050 | `internal/registration.Registrar` MUST accept a `versionmatrix.Matrix` at construction and its cluster registration payload's `kubernetes_supported_versions` MUST be exactly `matrix.SupportedVersions()` — the package-level `kubernetesSupportedVersions` variable (M1) is removed | MUST | Supersedes `osac-sp.spec.md` SC-001; single source of truth, no possible drift between registration and translation |
| REQ-VERSION-060 | `internal/cluster.Service` MUST accept a `versionmatrix.Matrix` at construction (`New(client, matrix)`); `releaseImage()` MUST consult the injected matrix's `Lookup` instead of the package-level `releaseImageByVersion` map (M3) — the existing override precedence (`provider_hints.osac.release_image`, when non-empty, always wins over any matrix lookup) MUST be unchanged | MUST | Supersedes `osac-sp-m3-cluster-crud.spec.md` REQ-CREATE-025's "hardcoded placeholder table" wording |
| REQ-VERSION-070 | `internal/cluster.Service` MUST expose `SupportsVersion(version string) bool`, reporting whether `version` has a matrix entry, for `internal/handlers/cluster`'s pre-flight validation (REQ-VERSION-080) to query without duplicating or directly importing the matrix | MUST | Keeps the matrix instance itself owned by exactly one component (`Service`) even though two packages need to consult it |
| REQ-VERSION-080 | `internal/handlers/cluster.Handler`'s existing `validateCreateRequest` (M3, REQ-CREATE-060) MUST gain one more pre-flight case: when the request's `provider_hints.osac.release_image` is absent or empty **and** `spec.version` is not supported (per REQ-VERSION-070's `SupportsVersion`), the handler MUST reject the request with the same synthetic `codes.InvalidArgument` gRPC status used for the function's other validation failures — mapped to `400 Bad Request` by the existing shared `mapError`/`internal/grpcerror` machinery (REQ-ERR-030) — before ever calling `Service.Create`/dispatching to OSAC. An explicit non-empty `release_image` override MUST bypass this check entirely, even for a `spec.version` with no matrix entry | MUST | Hard rejection, not silent fallback to OSAC's template default `release_image` — see DD-131. No new `v1alpha1.ErrorType`/schema value needed; `INVALIDARGUMENT` already fits, matching REQ-CREATE-060's existing precedent |
| REQ-VERSION-090 | `internal/config.Config` MUST add an optional `SP_VERSION_MATRIX_PATH` environment variable (empty/unset is valid and MUST result in `DefaultMatrix` being used); `cmd/osac-service-provider`'s `run` MUST call `versionmatrix.Load(cfg.VersionMatrix.Path)` once at startup, before starting any subsystem, and MUST fail fast (return a non-nil error, causing `mainRun` to exit non-zero) if `Load` returns an error | MUST | Mirrors REQ-XC-CFG-020's existing fail-fast convention (`osac-sp.spec.md`) for the case where the var *is* set but its file is missing/malformed |

#### Configuration Introduced

| Env Var | Required | Default | Description |
|---|---|---|---|
| `SP_VERSION_MATRIX_PATH` | No | *(empty — use `DefaultMatrix`)* | Optional path to a JSON file fully replacing the hardcoded default version-translation matrix. If set, the file MUST exist and contain a valid, non-empty `{"<k8s-version>": "<release_image>", ...}` object, or the service fails to start (REQ-VERSION-090). |

#### Acceptance Criteria

##### AC-VERSION-050: Registration's advertised versions are exactly the matrix's keys — no possible drift

- **Validates:** REQ-VERSION-050
- **Given** a `Registrar` constructed with a known `Matrix` (e.g. a 3-entry test matrix, deliberately different from `DefaultMatrix` to prove it is not hardcoded)
- **When** the cluster registration payload is built
- **Then** its `metadata.kubernetes_supported_versions` MUST equal exactly `matrix.SupportedVersions()` for that same matrix — proving the value is derived, not separately maintained

##### AC-VERSION-060: Create dispatches the injected matrix's `release_image`, per version

- **Validates:** REQ-VERSION-060
- **Given** a `Service` constructed with a known `Matrix`
- **When** `Create` is called, table-driven, once per matrix entry
- **Then** each call's dispatched `Cluster.spec.release_image` MUST equal exactly that entry's mapped value (same outcome Milestone 3's `TC-U-200`/`TC-U-204` already prove for the old hardcoded map, now proven against the injected matrix instead)

##### AC-VERSION-070: An explicit `release_image` override bypasses the matrix entirely, even for an unsupported version

- **Validates:** REQ-VERSION-060, REQ-VERSION-080
- **Given** a Create request with `spec.version` set to a value absent from the injected matrix, and `provider_hints.osac.release_image` set to an explicit non-empty override string
- **When** the request is validated and then processed
- **Then** `validateCreateRequest` MUST NOT reject the request, and the dispatched `Cluster.spec.release_image` MUST equal exactly the override string, not any matrix-derived value

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

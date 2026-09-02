// Package versionmatrix implements the version-translation compatibility
// matrix mapping DCM's Kubernetes minor versions to OSAC release_image
// values (Milestone 6). It is the single source of truth previously
// duplicated across internal/registration (kubernetesSupportedVersions)
// and internal/cluster (releaseImageByVersion) — see
// .ai/decisions/osac-sp.decisions.md DD-130.
//
// This package deliberately depends on nothing else in this repository,
// to avoid coupling internal/registration and internal/cluster (which do
// not otherwise import each other) through a shared type owned by either
// one.
package versionmatrix

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Matrix maps a Kubernetes minor version (e.g. "1.29") to the OSAC
// release_image it translates to. It is an immutable value once
// constructed — callers never mutate a Matrix after Load/DefaultMatrix
// returns it, so sharing one Matrix value across multiple owners (as
// internal/registration and internal/cluster do) requires no
// synchronization.
type Matrix map[string]string

// DefaultMatrix is the hardcoded default version-translation table,
// carrying forward the same 5 entries Milestones 1/3 originally
// hand-maintained separately (REQ-VERSION-020) — Kubernetes 1.29-1.33
// mapping to the OpenShift 4.16-4.20 release images OSAC's fulfillment
// service already has catalog item templates for.
var DefaultMatrix = Matrix{
	"1.29": "quay.io/openshift-release-dev/ocp-release:4.16.0-multi",
	"1.30": "quay.io/openshift-release-dev/ocp-release:4.17.0-multi",
	"1.31": "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
	"1.32": "quay.io/openshift-release-dev/ocp-release:4.19.0-multi",
	"1.33": "quay.io/openshift-release-dev/ocp-release:4.20.0-multi",
}

// Lookup returns the release_image mapped to version and whether version
// is present in m (REQ-VERSION-010).
func (m Matrix) Lookup(version string) (string, bool) {
	img, ok := m[version]
	return img, ok
}

// SupportedVersions returns m's keys, sorted in ascending lexical order,
// for deterministic consumption by registration payloads and tests
// (REQ-VERSION-030). Lexical order is a deliberate, spec'd choice, not an
// oversight — versions are opaque strings here, with no assumed numeric
// structure. Note the resulting caveat for callers: it can misorder
// multi-digit segments once they appear (e.g. "1.10" would sort before
// "1.9"); revisit if a future consumer needs true version-aware ordering.
func (m Matrix) SupportedVersions() []string {
	versions := make([]string, 0, len(m))
	for v := range m {
		versions = append(versions, v)
	}
	sort.Strings(versions)
	return versions
}

// Load returns DefaultMatrix when path is empty. When path is non-empty,
// it reads path as a JSON object and returns that file's content alone as
// the resulting Matrix — fully replacing, not merging with, DefaultMatrix.
// It returns a non-nil error (and a nil Matrix) if path is set and the
// file is missing, unreadable, not valid JSON, decodes to zero entries, or
// contains any entry with an empty version key or an empty release_image
// value (REQ-VERSION-040) — a zero-entry map and a map holding only
// blank-key/blank-value entries are both, in effect, zero *usable*
// versions, and are rejected for the same reason: an override that
// resolves to zero supported versions is a misconfiguration, not a legal
// (if useless) one, since it would silently reject every future Create
// call with no diagnostic pointing at the cause (see DD-131).
func Load(path string) (Matrix, error) {
	if path == "" {
		return DefaultMatrix, nil
	}

	data, err := os.ReadFile(path) //nolint:gosec // path is operator-controlled config (SP_VERSION_MATRIX_PATH), not user input
	if err != nil {
		return nil, fmt.Errorf("reading version matrix file %q: %w", path, err)
	}

	var m Matrix
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing version matrix file %q: %w", path, err)
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("version matrix file %q contains no entries", path)
	}
	for version, image := range m {
		if version == "" {
			return nil, fmt.Errorf("version matrix file %q contains an entry with an empty version key", path)
		}
		if image == "" {
			return nil, fmt.Errorf("version matrix file %q contains an empty release_image for version %q", path, version)
		}
	}

	return m, nil
}

// Package versionmatrix implements the version-translation compatibility
// matrix mapping DCM's Kubernetes minor versions to OSAC release_image
// values (Milestone 6). It is the single source of truth previously
// duplicated across internal/registration (kubernetesSupportedVersions)
// and internal/cluster (releaseImageByVersion) — see
// .ai/decisions/osac-sp.decisions.md DD-112.
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
// (REQ-VERSION-030).
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
// file is missing, unreadable, not valid JSON, or decodes to zero entries
// (REQ-VERSION-040) — an override that resolves to zero supported
// versions is treated as a misconfiguration, not a legal (if useless) one,
// since it would silently reject every future Create call with no
// diagnostic pointing at the cause (see DD-113).
func Load(path string) (Matrix, error) {
	if path == "" {
		return DefaultMatrix, nil
	}

	data, err := os.ReadFile(path)
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

	return m, nil
}

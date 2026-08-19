package versionmatrix_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dcm-project/osac-service-provider/internal/versionmatrix"
)

func strPtr(s string) *string { return &s }

// writeTempFile writes contents to a new temp file and returns its path.
// The file is removed automatically via Ginkgo's DeferCleanup.
func writeTempFile(contents string) string {
	f, err := os.CreateTemp("", "versionmatrix-*.json")
	Expect(err).NotTo(HaveOccurred())
	path := f.Name()
	DeferCleanup(func() { _ = os.Remove(path) })

	_, err = f.WriteString(contents)
	Expect(err).NotTo(HaveOccurred())
	Expect(f.Close()).NotTo(HaveOccurred())
	return path
}

var _ = Describe("DefaultMatrix (Topic 4.1 Version Matrix Package)", func() {
	// TC-U-500 (REQ-VERSION-010/020, AC-VERSION-010): DefaultMatrix
	// resolves each documented version to its exact release image; an
	// undocumented version misses.
	DescribeTable("resolves each documented version to its exact release image (TC-U-500)",
		func(version, wantImage string) {
			img, ok := versionmatrix.DefaultMatrix.Lookup(version)
			Expect(ok).To(BeTrue())
			Expect(img).To(Equal(wantImage))
		},
		Entry("1.29 -> OpenShift 4.16", "1.29", "quay.io/openshift-release-dev/ocp-release:4.16.0-multi"),
		Entry("1.30 -> OpenShift 4.17", "1.30", "quay.io/openshift-release-dev/ocp-release:4.17.0-multi"),
		Entry("1.31 -> OpenShift 4.18", "1.31", "quay.io/openshift-release-dev/ocp-release:4.18.0-multi"),
		Entry("1.32 -> OpenShift 4.19", "1.32", "quay.io/openshift-release-dev/ocp-release:4.19.0-multi"),
		Entry("1.33 -> OpenShift 4.20", "1.33", "quay.io/openshift-release-dev/ocp-release:4.20.0-multi"),
	)

	It("misses for an undocumented version (TC-U-500)", func() {
		_, ok := versionmatrix.DefaultMatrix.Lookup("1.99")
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("Matrix.SupportedVersions (Topic 4.1 Version Matrix Package)", func() {
	// TC-U-501 (REQ-VERSION-030, AC-VERSION-020): SupportedVersions
	// returns exactly the matrix's keys, sorted ascending — not insertion
	// or map-iteration order.
	It("returns exactly the matrix's keys, sorted ascending (TC-U-501)", func() {
		m := versionmatrix.Matrix{"1.31": "x", "1.29": "y", "1.33": "z"}
		Expect(m.SupportedVersions()).To(Equal([]string{"1.29", "1.31", "1.33"}))
	})
})

var _ = Describe("Load (Topic 4.1 Version Matrix Package)", func() {
	// TC-U-502 (REQ-VERSION-040): Load("") returns DefaultMatrix
	// unchanged.
	It("returns DefaultMatrix unchanged when path is empty (TC-U-502)", func() {
		m, err := versionmatrix.Load("")
		Expect(err).NotTo(HaveOccurred())
		Expect(m).To(Equal(versionmatrix.DefaultMatrix))
	})

	// TC-U-503 (REQ-VERSION-040, AC-VERSION-030): a valid override file
	// fully replaces, not merges with, the default.
	It("fully replaces, not merges with, the default when a valid override file is given (TC-U-503)", func() {
		path := writeTempFile(`{"1.34":"quay.io/openshift-release-dev/ocp-release:4.21.0-multi"}`)

		m, err := versionmatrix.Load(path)
		Expect(err).NotTo(HaveOccurred())

		img, ok := m.Lookup("1.34")
		Expect(ok).To(BeTrue())
		Expect(img).To(Equal("quay.io/openshift-release-dev/ocp-release:4.21.0-multi"))

		_, ok = m.Lookup("1.29")
		Expect(ok).To(BeFalse())
	})

	// TC-U-504 (REQ-VERSION-040, AC-VERSION-040): Load fails fast on a
	// missing, malformed, or empty override file. contents == nil means
	// "nonexistent path" (no file is written at all).
	//
	// TC-U-505 (REQ-VERSION-040, AC-VERSION-040, regression): an override
	// file with an empty version key or an empty release_image value is
	// rejected too — len(m)==0 alone only catches a wholly empty {}; a
	// file like {"1.29":""} or {"":"img"} has one key and would otherwise
	// load successfully as a matrix with zero *usable* versions (found in
	// review: https://github.com/dcm-project/osac-service-provider/pull/26).
	DescribeTable("fails fast on an invalid override file (TC-U-504, TC-U-505)",
		func(contents *string) {
			var path string
			if contents == nil {
				path = filepath.Join(os.TempDir(), "definitely-does-not-exist-versionmatrix.json")
			} else {
				path = writeTempFile(*contents)
			}

			m, err := versionmatrix.Load(path)
			Expect(err).To(HaveOccurred())
			Expect(m).To(BeNil())
		},
		Entry("nonexistent path", nil),
		Entry("malformed JSON", strPtr("not json")),
		Entry("empty object", strPtr("{}")),
		Entry("empty version key", strPtr(`{"":"quay.io/openshift-release-dev/ocp-release:4.16.0-multi"}`)),
		Entry("empty release_image value", strPtr(`{"1.29":""}`)),
	)
})

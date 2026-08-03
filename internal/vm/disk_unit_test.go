package vm_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dcm-project/osac-service-provider/internal/vm"
)

// TC-U-305 (REQ-VMCREATE-040): ParseDiskCapacityGiB is a pure function,
// tested directly against DD-083's unit table without needing the bufconn
// fixture.
var _ = Describe("ParseDiskCapacityGiB (Topic 4.1 disk capacity parsing, DD-083)", func() {
	DescribeTable("parses each supported unit, case-insensitively (TC-U-305)",
		func(capacity string, wantGiB int32) {
			got, err := vm.ParseDiskCapacityGiB(capacity)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(wantGiB))
		},
		Entry("GB treated as GiB directly", "100GB", int32(100)),
		Entry("gb lowercase", "100gb", int32(100)),
		Entry("GiB treated as GiB directly", "100GiB", int32(100)),
		Entry("TB multiplies by 1024", "2TB", int32(2048)),
		Entry("TiB multiplies by 1024", "1TiB", int32(1024)),
		Entry("MB divides by 1024, rounded up", "512MB", int32(1)),
		Entry("MiB divides by 1024, rounded up", "2048MiB", int32(2)),
	)

	DescribeTable("rejects unparseable, unrecognized-unit, or non-positive values",
		func(capacity string) {
			_, err := vm.ParseDiskCapacityGiB(capacity)
			Expect(err).To(HaveOccurred())
		},
		Entry("no unit", "100"),
		Entry("unrecognized unit", "100XB"),
		Entry("non-positive", "-5GB"),
		Entry("zero", "0GB"),
		Entry("empty string", ""),
		Entry("non-numeric prefix", "abcGB"),
	)
})

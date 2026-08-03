package util_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dcm-project/osac-service-provider/internal/util"
)

type ptrTestStruct struct {
	Name string
	N    int
}

var _ = Describe("Ptr", func() {
	// TC-U-093: Ptr returns a pointer that dereferences to the input, for
	// multiple types, and each call returns a distinct address (a real
	// pointer, not a shared/cached one).
	It("returns a pointer that dereferences to the input, for multiple types (TC-U-093)", func() {
		s := "osac-sp"
		sp := util.Ptr(s)
		Expect(*sp).To(Equal(s))

		n := 42
		np := util.Ptr(n)
		Expect(*np).To(Equal(n))

		st := ptrTestStruct{Name: "cluster", N: 1}
		stp := util.Ptr(st)
		Expect(*stp).To(Equal(st))

		otherSp := util.Ptr(s)
		Expect(sp).NotTo(BeIdenticalTo(otherSp), "each call must return a distinct address, not a shared/cached one")
	})
})

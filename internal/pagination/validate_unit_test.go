package pagination_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/dcm-project/osac-service-provider/internal/pagination"
	"github.com/dcm-project/osac-service-provider/internal/util"
)

var _ = Describe("NormalizeMaxPageSize", func() {
	DescribeTable("applies the AEP-158 page size contract",
		func(value *int32, wantLimit int32, wantCode codes.Code) {
			got, err := pagination.NormalizeMaxPageSize(value, 50)
			Expect(grpcstatus.Code(err)).To(Equal(wantCode))
			if wantCode == codes.OK {
				Expect(got).To(Equal(wantLimit))
			}
		},
		Entry("defaults an omitted value", nil, int32(50), codes.OK),
		Entry("defaults zero", util.Ptr(int32(0)), int32(50), codes.OK),
		Entry("accepts the minimum", util.Ptr(int32(1)), int32(1), codes.OK),
		Entry("accepts the maximum", util.Ptr(int32(100)), int32(100), codes.OK),
		Entry("clamps a value above the maximum", util.Ptr(int32(101)), int32(100), codes.OK),
		Entry("rejects a negative value", util.Ptr(int32(-1)), int32(0), codes.InvalidArgument),
	)
})

package pagination_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/dcm-project/osac-service-provider/internal/pagination"
	"github.com/dcm-project/osac-service-provider/internal/util"
)

var _ = Describe("ValidateMaxPageSize", func() {
	DescribeTable("validates the optional AEP-132 page size",
		func(value *int32, wantCode codes.Code) {
			err := pagination.ValidateMaxPageSize(value)
			Expect(grpcstatus.Code(err)).To(Equal(wantCode))
		},
		Entry("accepts an omitted value", nil, codes.OK),
		Entry("accepts the minimum", util.Ptr(int32(1)), codes.OK),
		Entry("accepts the maximum", util.Ptr(int32(100)), codes.OK),
		Entry("rejects a negative value", util.Ptr(int32(-1)), codes.InvalidArgument),
		Entry("rejects zero", util.Ptr(int32(0)), codes.InvalidArgument),
		Entry("rejects a value above the maximum", util.Ptr(int32(101)), codes.InvalidArgument),
	)
})

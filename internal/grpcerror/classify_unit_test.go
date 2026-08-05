package grpcerror_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	"github.com/dcm-project/osac-service-provider/internal/grpcerror"
)

// TC-U-360 (REQ-VMERR-010/020, AC-VMERR-010): Classify maps every
// documented gRPC code to its exact HTTP status and v1alpha1.ErrorType,
// identically to Milestone 3's table (DD-126).
var _ = Describe("Classify (Topic 7 VM Error Mapping)", func() {
	DescribeTable("maps each gRPC code to its documented HTTP status and type (TC-U-360)",
		func(code codes.Code, wantStatus int, wantType v1alpha1.ErrorType) {
			err := grpcstatus.Error(code, "boom")

			status, errType, title := grpcerror.Classify(err)

			Expect(status).To(Equal(wantStatus))
			Expect(errType).To(Equal(wantType))
			Expect(title).NotTo(BeEmpty())
		},
		Entry("InvalidArgument -> 400", codes.InvalidArgument, http.StatusBadRequest, v1alpha1.INVALIDARGUMENT),
		Entry("Unauthenticated -> 401", codes.Unauthenticated, http.StatusUnauthorized, v1alpha1.UNAUTHENTICATED),
		Entry("PermissionDenied -> 403", codes.PermissionDenied, http.StatusForbidden, v1alpha1.PERMISSIONDENIED),
		Entry("NotFound -> 404", codes.NotFound, http.StatusNotFound, v1alpha1.NOTFOUND),
		Entry("AlreadyExists -> 409", codes.AlreadyExists, http.StatusConflict, v1alpha1.ALREADYEXISTS),
		Entry("Unavailable -> 502", codes.Unavailable, http.StatusBadGateway, v1alpha1.UNAVAILABLE),
		Entry("DeadlineExceeded -> 502", codes.DeadlineExceeded, http.StatusBadGateway, v1alpha1.UNAVAILABLE),
		Entry("Internal -> 500", codes.Internal, http.StatusInternalServerError, v1alpha1.INTERNAL),
		Entry("Unknown -> 500 (catch-all)", codes.Unknown, http.StatusInternalServerError, v1alpha1.INTERNAL),
	)

	// Supplementary to TC-U-360: a plain (non-gRPC-status) error — e.g. one
	// synthesized by internal/vm's own request-shape validation, which is
	// never itself a *grpcstatus.Status — must still classify deterministically
	// rather than panicking. grpcstatus.Code on a non-status error returns
	// codes.Unknown, so this exercises the same catch-all row as above via
	// a distinct input shape.
	It("classifies a non-gRPC error via the codes.Unknown catch-all, without panicking", func() {
		status, errType, title := grpcerror.Classify(errPlain{})

		Expect(status).To(Equal(http.StatusInternalServerError))
		Expect(errType).To(Equal(v1alpha1.INTERNAL))
		Expect(title).NotTo(BeEmpty())
	})
})

// errPlain is a minimal error implementation carrying no gRPC status.
type errPlain struct{}

func (errPlain) Error() string { return "plain error" }

package cluster_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// decodeError decodes an application/problem+json body from rec into a
// v1alpha1.Error for assertion.
func decodeError(rec *httptest.ResponseRecorder) v1alpha1.Error {
	var body v1alpha1.Error
	Expect(json.Unmarshal(rec.Body.Bytes(), &body)).To(Succeed())
	return body
}

var _ = Describe("Error Mapping (Topic 6, shared across handlers)", func() {
	var f *fixture

	BeforeEach(func() {
		f = newFixture()
		DeferCleanup(f.Close)
	})

	// TC-U-250 (REQ-ERR-010/020, AC-ERR-010): each gRPC code maps to its
	// documented HTTP status and v1alpha1.ErrorType, driven through the Get
	// handler.
	DescribeTable("maps each gRPC code to its documented HTTP status and type (TC-U-250)",
		func(code codes.Code, wantStatus int, wantType v1alpha1.ErrorType) {
			f.fake.getFunc = func(*publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
				return nil, grpcstatus.Error(code, "boom")
			}

			resp, err := f.handler.GetCluster(context.Background(), oapigen.GetClusterRequestObject{ClusterId: "X"})
			Expect(err).NotTo(HaveOccurred())

			rec := httptest.NewRecorder()
			Expect(resp.VisitGetClusterResponse(rec)).To(Succeed())
			Expect(rec.Code).To(Equal(wantStatus))
			Expect(rec.Header().Get("Content-Type")).To(Equal("application/problem+json"))
			Expect(decodeError(rec).Type).To(Equal(wantType))
		},
		Entry("InvalidArgument -> 400", codes.InvalidArgument, http.StatusBadRequest, v1alpha1.ErrorTypeINVALIDARGUMENT),
		Entry("Unauthenticated -> 401", codes.Unauthenticated, http.StatusUnauthorized, v1alpha1.ErrorTypeUNAUTHENTICATED),
		Entry("PermissionDenied -> 403", codes.PermissionDenied, http.StatusForbidden, v1alpha1.ErrorTypePERMISSIONDENIED),
		Entry("NotFound -> 404", codes.NotFound, http.StatusNotFound, v1alpha1.ErrorTypeNOTFOUND),
		Entry("Unavailable -> 502", codes.Unavailable, http.StatusBadGateway, v1alpha1.ErrorTypeUNAVAILABLE),
		Entry("DeadlineExceeded -> 502", codes.DeadlineExceeded, http.StatusBadGateway, v1alpha1.ErrorTypeUNAVAILABLE),
		Entry("Internal -> 500", codes.Internal, http.StatusInternalServerError, v1alpha1.ErrorTypeINTERNAL),
		// AlreadyExists is a documented REQ-ERR-010 mapping row, even
		// though no current call site reaches mapError with it in practice
		// (Create's own AlreadyExists is intercepted earlier, per
		// REQ-CREATE-040) — kept and tested for forward compatibility, the
		// same convention as internal/cluster.MapStatus's SC-M3-001 rules.
		Entry("AlreadyExists -> 409", codes.AlreadyExists, http.StatusConflict, v1alpha1.ErrorTypeALREADYEXISTS),
	)

	// TC-U-251 (REQ-ERR-030, AC-ERR-020): the same mapping function
	// produces identical results across Get, List, and Delete.
	It("produces an identical status and type across Get, List, and Delete for the same error (TC-U-251)", func() {
		f.fake.getFunc = func(*publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
			return nil, grpcstatus.Error(codes.PermissionDenied, "denied")
		}
		f.fake.listFunc = func(*publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error) {
			return nil, grpcstatus.Error(codes.PermissionDenied, "denied")
		}
		f.fake.deleteFunc = func(*publicv1.ClustersDeleteRequest) (*publicv1.ClustersDeleteResponse, error) {
			return nil, grpcstatus.Error(codes.PermissionDenied, "denied")
		}

		getResp, err := f.handler.GetCluster(context.Background(), oapigen.GetClusterRequestObject{ClusterId: "X"})
		Expect(err).NotTo(HaveOccurred())
		getRec := httptest.NewRecorder()
		Expect(getResp.VisitGetClusterResponse(getRec)).To(Succeed())

		listResp, err := f.handler.ListClusters(context.Background(), oapigen.ListClustersRequestObject{})
		Expect(err).NotTo(HaveOccurred())
		listRec := httptest.NewRecorder()
		Expect(listResp.VisitListClustersResponse(listRec)).To(Succeed())

		deleteResp, err := f.handler.DeleteCluster(context.Background(), oapigen.DeleteClusterRequestObject{ClusterId: "X"})
		Expect(err).NotTo(HaveOccurred())
		deleteRec := httptest.NewRecorder()
		Expect(deleteResp.VisitDeleteClusterResponse(deleteRec)).To(Succeed())

		Expect(getRec.Code).To(Equal(http.StatusForbidden))
		Expect(listRec.Code).To(Equal(http.StatusForbidden))
		Expect(deleteRec.Code).To(Equal(http.StatusForbidden))

		wantType := v1alpha1.ErrorTypePERMISSIONDENIED
		Expect(decodeError(getRec).Type).To(Equal(wantType))
		Expect(decodeError(listRec).Type).To(Equal(wantType))
		Expect(decodeError(deleteRec).Type).To(Equal(wantType))
	})
})

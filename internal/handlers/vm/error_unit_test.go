package vm_test

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

var _ = Describe("Error Mapping (Topic 7, shared across VM handlers)", func() {
	var f *fixture

	BeforeEach(func() {
		f = newFixture()
		DeferCleanup(f.Close)
	})

	// TC-U-361 (REQ-VMERR-030, AC-VMERR-020): internal/grpcerror.Classify
	// is the single implementation consumed by all 4 VM handlers — a
	// PermissionDenied from Get, List, and Delete each produce an
	// identical HTTP status and type.
	It("produces an identical status and type across Get, List, and Delete for the same error (TC-U-361)", func() {
		f.fake.getFunc = func(*publicv1.ComputeInstancesGetRequest) (*publicv1.ComputeInstancesGetResponse, error) {
			return nil, grpcstatus.Error(codes.PermissionDenied, "denied")
		}
		f.fake.listFunc = func(*publicv1.ComputeInstancesListRequest) (*publicv1.ComputeInstancesListResponse, error) {
			return nil, grpcstatus.Error(codes.PermissionDenied, "denied")
		}
		f.fake.deleteFunc = func(*publicv1.ComputeInstancesDeleteRequest) (*publicv1.ComputeInstancesDeleteResponse, error) {
			return nil, grpcstatus.Error(codes.PermissionDenied, "denied")
		}

		getResp, err := f.handler.GetVM(context.Background(), oapigen.GetVMRequestObject{VmId: "X"})
		Expect(err).NotTo(HaveOccurred())
		getRec := httptest.NewRecorder()
		Expect(getResp.VisitGetVMResponse(getRec)).To(Succeed())

		listResp, err := f.handler.ListVMs(context.Background(), oapigen.ListVMsRequestObject{})
		Expect(err).NotTo(HaveOccurred())
		listRec := httptest.NewRecorder()
		Expect(listResp.VisitListVMsResponse(listRec)).To(Succeed())

		deleteResp, err := f.handler.DeleteVM(context.Background(), oapigen.DeleteVMRequestObject{VmId: "X"})
		Expect(err).NotTo(HaveOccurred())
		deleteRec := httptest.NewRecorder()
		Expect(deleteResp.VisitDeleteVMResponse(deleteRec)).To(Succeed())

		Expect(getRec.Code).To(Equal(http.StatusForbidden))
		Expect(listRec.Code).To(Equal(http.StatusForbidden))
		Expect(deleteRec.Code).To(Equal(http.StatusForbidden))

		wantType := v1alpha1.PERMISSIONDENIED
		Expect(decodeError(getRec).Type).To(Equal(wantType))
		Expect(decodeError(listRec).Type).To(Equal(wantType))
		Expect(decodeError(deleteRec).Type).To(Equal(wantType))
	})
})

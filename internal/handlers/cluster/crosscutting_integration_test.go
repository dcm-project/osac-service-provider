package cluster_test

import (
	"encoding/json"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

var _ = Describe("Status precedence and Error mapping (integration, cross-cutting)", func() {
	var f *integrationFixture

	BeforeEach(func() {
		f = newIntegrationFixture()
		DeferCleanup(f.Close)
	})

	// TC-I-240 (REQ-STATUS-020, AC-STATUS-020): status precedence
	// (FAILED beats a simultaneous DEGRADED condition) is observable over
	// real HTTP, not just at the unit level.
	It("resolves FAILED over a simultaneous DEGRADED condition, over real HTTP (TC-I-240)", func() {
		f.fake.getFunc = func(req *publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
			return &publicv1.ClustersGetResponse{Object: &publicv1.Cluster{
				Id: req.GetId(),
				Status: &publicv1.ClusterStatus{
					State: publicv1.ClusterState_CLUSTER_STATE_FAILED,
					Conditions: []*publicv1.ClusterCondition{
						{Type: publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_DEGRADED, Status: publicv1.ConditionStatus_CONDITION_STATUS_TRUE},
					},
				},
			}}, nil
		}

		resp := getCluster(f)
		defer func() { _ = resp.Body.Close() }()

		var cluster v1alpha1.Cluster
		Expect(json.NewDecoder(resp.Body).Decode(&cluster)).To(Succeed())
		Expect(*cluster.Status).To(Equal(v1alpha1.ClusterStatusFAILED))
	})

	// TC-I-250 (REQ-ERR-010/020/030, AC-ERR-010/020): each gRPC error code
	// maps to its documented HTTP status over real HTTP, identically across
	// handlers, driven through Get plus an extra PermissionDenied check on
	// List/Delete.
	DescribeTable("maps each gRPC code to its documented HTTP status over real HTTP (TC-I-250)",
		func(code codes.Code, wantStatus int, wantType v1alpha1.ErrorType) {
			f.fake.getFunc = func(*publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
				return nil, grpcstatus.Error(code, "boom")
			}

			resp := getCluster(f)
			defer func() { _ = resp.Body.Close() }()

			Expect(resp.StatusCode).To(Equal(wantStatus))
			var body v1alpha1.Error
			Expect(json.NewDecoder(resp.Body).Decode(&body)).To(Succeed())
			Expect(body.Type).To(Equal(wantType))
		},
		Entry("InvalidArgument -> 400", codes.InvalidArgument, http.StatusBadRequest, v1alpha1.ErrorTypeINVALIDARGUMENT),
		Entry("Unauthenticated -> 401", codes.Unauthenticated, http.StatusUnauthorized, v1alpha1.ErrorTypeUNAUTHENTICATED),
		Entry("PermissionDenied -> 403", codes.PermissionDenied, http.StatusForbidden, v1alpha1.ErrorTypePERMISSIONDENIED),
		Entry("NotFound -> 404", codes.NotFound, http.StatusNotFound, v1alpha1.ErrorTypeNOTFOUND),
		Entry("Unavailable -> 502", codes.Unavailable, http.StatusBadGateway, v1alpha1.ErrorTypeUNAVAILABLE),
		Entry("Internal -> 500", codes.Internal, http.StatusInternalServerError, v1alpha1.ErrorTypeINTERNAL),
	)

	It("produces an identical PermissionDenied mapping across List and Delete, over real HTTP (TC-I-250b)", func() {
		f.fake.listFunc = func(*publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error) {
			return nil, grpcstatus.Error(codes.PermissionDenied, "denied")
		}
		f.fake.deleteFunc = func(*publicv1.ClustersDeleteRequest) (*publicv1.ClustersDeleteResponse, error) {
			return nil, grpcstatus.Error(codes.PermissionDenied, "denied")
		}

		listResp := listClusters(f, "")
		defer func() { _ = listResp.Body.Close() }()
		deleteResp := deleteCluster(f)
		defer func() { _ = deleteResp.Body.Close() }()

		Expect(listResp.StatusCode).To(Equal(http.StatusForbidden))
		Expect(deleteResp.StatusCode).To(Equal(http.StatusForbidden))

		var listErr, deleteErr v1alpha1.Error
		Expect(json.NewDecoder(listResp.Body).Decode(&listErr)).To(Succeed())
		Expect(json.NewDecoder(deleteResp.Body).Decode(&deleteErr)).To(Succeed())
		Expect(listErr.Type).To(Equal(v1alpha1.ErrorTypePERMISSIONDENIED))
		Expect(deleteErr.Type).To(Equal(v1alpha1.ErrorTypePERMISSIONDENIED))
	})
})

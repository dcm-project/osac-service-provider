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

// getCluster issues a real GET for cluster "X" — every Get test in this
// package uses the same id, since the fake OSAC server's behavior (not the
// id) is what varies per test.
func getCluster(f *integrationFixture) *http.Response {
	resp, err := http.Get(f.URL("/api/v1alpha1/clusters/X")) //nolint:noctx // test helper
	Expect(err).NotTo(HaveOccurred())
	return resp
}

var _ = Describe("Cluster Get (integration, real HTTP + router + bufconn OSAC fake)", func() {
	var f *integrationFixture

	BeforeEach(func() {
		f = newIntegrationFixture()
		DeferCleanup(f.Close)
	})

	// TC-I-210 (REQ-GET-010/020, AC-GET-010): Get returns the kubeconfig
	// for an ACTIVE cluster over real HTTP.
	It("returns the kubeconfig for an ACTIVE cluster over real HTTP (TC-I-210)", func() {
		f.fake.getFunc = func(req *publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
			return &publicv1.ClustersGetResponse{Object: &publicv1.Cluster{
				Id:     req.GetId(),
				Status: &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_READY},
			}}, nil
		}
		f.fake.getKubeconfigFunc = func(*publicv1.ClustersGetKubeconfigRequest) (*publicv1.ClustersGetKubeconfigResponse, error) {
			return &publicv1.ClustersGetKubeconfigResponse{Kubeconfig: "kubeconfig-abc"}, nil
		}

		resp := getCluster(f)
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var cluster v1alpha1.Cluster
		Expect(json.NewDecoder(resp.Body).Decode(&cluster)).To(Succeed())
		Expect(*cluster.Status).To(Equal(v1alpha1.ClusterStatusACTIVE))
		Expect(*cluster.Kubeconfig).To(Equal("kubeconfig-abc"))
	})

	// TC-I-211 (REQ-GET-030, AC-GET-020): Get omits a real kubeconfig for a
	// non-ACTIVE cluster over real HTTP.
	It("returns an empty kubeconfig for a non-ACTIVE cluster over real HTTP (TC-I-211)", func() {
		f.fake.getFunc = func(req *publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
			return &publicv1.ClustersGetResponse{Object: &publicv1.Cluster{
				Id:     req.GetId(),
				Status: &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_PROGRESSING},
			}}, nil
		}

		resp := getCluster(f)
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var cluster v1alpha1.Cluster
		Expect(json.NewDecoder(resp.Body).Decode(&cluster)).To(Succeed())
		Expect(*cluster.Status).To(Equal(v1alpha1.ClusterStatusPROGRESSING))
		Expect(*cluster.Kubeconfig).To(Equal(""))
	})

	// TC-I-212 (REQ-GET-040, AC-GET-030): Get returns 404 for a nonexistent
	// cluster over real HTTP.
	It("returns 404 for a nonexistent cluster over real HTTP (TC-I-212)", func() {
		f.fake.getFunc = func(*publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
			return nil, grpcstatus.Error(codes.NotFound, "no such cluster")
		}

		resp := getCluster(f)
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		Expect(resp.Header.Get("Content-Type")).To(Equal("application/problem+json"))
		var body v1alpha1.Error
		Expect(json.NewDecoder(resp.Body).Decode(&body)).To(Succeed())
		Expect(body.Type).To(Equal(v1alpha1.ErrorTypeNOTFOUND))
	})
})

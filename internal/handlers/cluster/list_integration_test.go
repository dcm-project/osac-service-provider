package cluster_test

import (
	"encoding/json"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

func listClusters(f *integrationFixture, query string) *http.Response {
	resp, err := http.Get(f.URL("/api/v1alpha1/clusters" + query)) //nolint:noctx // test helper
	Expect(err).NotTo(HaveOccurred())
	return resp
}

var _ = Describe("Cluster List (integration, real HTTP + router + bufconn OSAC fake)", func() {
	var f *integrationFixture

	BeforeEach(func() {
		f = newIntegrationFixture()
		DeferCleanup(f.Close)
	})

	// TC-I-220 (REQ-LIST-010/030, AC-LIST-010): List returns exact entries
	// with the ownership filter applied, over real HTTP.
	It("returns exact entries with the ownership filter applied, over real HTTP (TC-I-220)", func() {
		var recordedFilter string
		f.fake.listFunc = func(req *publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error) {
			recordedFilter = req.GetFilter()
			return &publicv1.ClustersListResponse{
				Size:  2,
				Total: 2,
				Items: []*publicv1.Cluster{
					{Id: "c1", Status: &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_READY}},
					{Id: "c2", Status: &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_PROGRESSING}},
				},
			}, nil
		}

		resp := listClusters(f, "")
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var list v1alpha1.ClusterList
		Expect(json.NewDecoder(resp.Body).Decode(&list)).To(Succeed())

		Expect(recordedFilter).To(Equal(`this.metadata.labels["dcm.io/managed-by"] == "dcm"`))
		Expect(list.Results).To(HaveLen(2))
		Expect(*list.Results[0].Id).To(Equal("c1"))
		Expect(*list.Results[0].Status).To(Equal(v1alpha1.ClusterStatusACTIVE))
		Expect(*list.Results[1].Id).To(Equal("c2"))
		Expect(*list.Results[1].Status).To(Equal(v1alpha1.ClusterStatusPROGRESSING))
	})

	// TC-I-221 (REQ-LIST-020/040, AC-LIST-020): pagination round-trips
	// across two real, sequential HTTP requests.
	It("round-trips page_token through OSAC's offset across two real HTTP requests (TC-I-221)", func() {
		var recordedOffsets []int32
		f.fake.listFunc = func(req *publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error) {
			recordedOffsets = append(recordedOffsets, req.GetOffset())
			return &publicv1.ClustersListResponse{Size: 50, Total: 100}, nil
		}

		first := listClusters(f, "")
		Expect(first.StatusCode).To(Equal(http.StatusOK))
		var firstList v1alpha1.ClusterList
		Expect(json.NewDecoder(first.Body).Decode(&firstList)).To(Succeed())
		_ = first.Body.Close()
		Expect(firstList.NextPageToken).NotTo(BeNil())

		second := listClusters(f, "?page_token="+*firstList.NextPageToken)
		defer func() { _ = second.Body.Close() }()
		Expect(second.StatusCode).To(Equal(http.StatusOK))

		Expect(recordedOffsets).To(Equal([]int32{0, 50}))
	})

	// TC-I-222 (REQ-LIST-030, AC-LIST-030): List responses never include
	// kubeconfig, over real HTTP.
	It("never includes kubeconfig in List responses, over real HTTP (TC-I-222)", func() {
		f.fake.listFunc = func(*publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error) {
			return &publicv1.ClustersListResponse{
				Size: 1, Total: 1,
				Items: []*publicv1.Cluster{
					{Id: "c1", Status: &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_READY}},
				},
			}, nil
		}
		f.fake.getKubeconfigFunc = func(*publicv1.ClustersGetKubeconfigRequest) (*publicv1.ClustersGetKubeconfigResponse, error) {
			Fail("GetKubeconfig must never be called from List")
			return nil, nil
		}

		resp := listClusters(f, "")
		defer func() { _ = resp.Body.Close() }()

		var raw map[string]interface{}
		Expect(json.NewDecoder(resp.Body).Decode(&raw)).To(Succeed())
		results, ok := raw["results"].([]interface{})
		Expect(ok).To(BeTrue())
		Expect(results).To(HaveLen(1))
		entry, ok := results[0].(map[string]interface{})
		Expect(ok).To(BeTrue())
		Expect(entry).NotTo(HaveKey("kubeconfig"))
	})
})

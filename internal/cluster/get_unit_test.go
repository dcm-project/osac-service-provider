package cluster_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/util"
)

var _ = Describe("Service.Get (Topic 4.2 Cluster Get)", func() {
	var f *fixture

	BeforeEach(func() {
		f = newFixture()
		DeferCleanup(f.Close)
	})

	// TC-U-210 (REQ-GET-010/020, AC-GET-010): an ACTIVE cluster fetches its
	// kubeconfig exactly once.
	It("fetches the kubeconfig exactly once for an ACTIVE cluster (TC-U-210)", func() {
		f.fake.getFunc = func(req *publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
			return &publicv1.ClustersGetResponse{Object: &publicv1.Cluster{
				Id: req.GetId(),
				Status: &publicv1.ClusterStatus{
					State: publicv1.ClusterState_CLUSTER_STATE_READY,
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"default-hcp": {HostType: "acme_1tb", Size: 3},
					},
				},
			}}, nil
		}
		f.fake.getKubeconfigFunc = func(*publicv1.ClustersGetKubeconfigRequest) (*publicv1.ClustersGetKubeconfigResponse, error) {
			return &publicv1.ClustersGetKubeconfigResponse{Kubeconfig: "kubeconfig-abc"}, nil
		}

		result, err := f.svc.Get(context.Background(), "X")
		Expect(err).NotTo(HaveOccurred())

		Expect(*result.Status).To(Equal(v1alpha1.ClusterStatusACTIVE))
		Expect(*result.Kubeconfig).To(Equal("kubeconfig-abc"))
		Expect(f.fake.GetKubeconfigCallCount()).To(Equal(1))
		// SC-M3-002: node_sets echoes OSAC's status.node_sets map directly.
		Expect(*result.NodeSets).To(Equal(map[string]v1alpha1.ClusterNodeSet{
			"default-hcp": {HostType: util.Ptr("acme_1tb"), Size: util.Ptr(3)},
		}))
	})

	// TC-U-211 (REQ-GET-030, AC-GET-020): a non-ACTIVE cluster never
	// triggers a kubeconfig fetch.
	It("never fetches a kubeconfig for a non-ACTIVE cluster (TC-U-211)", func() {
		f.fake.getFunc = func(req *publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
			return &publicv1.ClustersGetResponse{Object: &publicv1.Cluster{
				Id:     req.GetId(),
				Status: &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_PROGRESSING},
			}}, nil
		}

		result, err := f.svc.Get(context.Background(), "X")
		Expect(err).NotTo(HaveOccurred())

		Expect(*result.Status).To(Equal(v1alpha1.ClusterStatusPROGRESSING))
		// REQ-GET-030: kubeconfig is the empty string, not omitted, for a
		// non-ACTIVE cluster.
		Expect(result.Kubeconfig).NotTo(BeNil())
		Expect(*result.Kubeconfig).To(Equal(""))
		Expect(f.fake.GetKubeconfigCallCount()).To(Equal(0))
	})

	// TC-U-212 (REQ-GET-040, AC-GET-030): a nonexistent cluster's NotFound
	// is propagated raw, for the shared error-mapping topic (§4.6) to turn
	// into HTTP 404 — exact gRPC code, not merely "an error occurred".
	It("propagates NotFound raw for a nonexistent cluster (TC-U-212)", func() {
		f.fake.getFunc = func(*publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
			return nil, grpcstatus.Error(codes.NotFound, "no such cluster")
		}

		_, err := f.svc.Get(context.Background(), "X")
		Expect(err).To(HaveOccurred())

		st, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.NotFound))
	})

	// Supplementary: a GetKubeconfig failure on an ACTIVE cluster is
	// propagated raw, not swallowed.
	It("propagates a GetKubeconfig error raw for an ACTIVE cluster", func() {
		f.fake.getFunc = func(req *publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
			return &publicv1.ClustersGetResponse{Object: &publicv1.Cluster{
				Id:     req.GetId(),
				Status: &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_READY},
			}}, nil
		}
		f.fake.getKubeconfigFunc = func(*publicv1.ClustersGetKubeconfigRequest) (*publicv1.ClustersGetKubeconfigResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "osac unreachable")
		}

		_, err := f.svc.Get(context.Background(), "X")
		Expect(err).To(HaveOccurred())

		st, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unavailable))
	})
})

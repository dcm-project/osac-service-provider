package cluster_test

import (
	"context"
	"encoding/base64"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	privatev1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/private/v1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/util"
)

var _ = Describe("Service.Get (Topic 4.2 Cluster Get)", func() {
	var f *fixture

	BeforeEach(func() {
		f = newFixture()
		DeferCleanup(f.Close)
	})

	// TC-U-210 (REQ-GET-010/020, AC-GET-010): an ACTIVE cluster resolves its
	// kubeconfig Secret exactly once and preserves the REST base64 contract.
	It("fetches the referenced kubeconfig Secret exactly once for an ACTIVE cluster (TC-U-210)", func() {
		kubeconfigBytes := []byte("apiVersion: v1")
		f.fake.getFunc = func(req *publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
			return &publicv1.ClustersGetResponse{Object: &publicv1.Cluster{
				Id: req.GetId(),
				Status: &publicv1.ClusterStatus{
					State:            publicv1.ClusterState_CLUSTER_STATE_READY,
					KubeconfigSecret: &publicv1.SecretLocalReference{Id: "secret-1"},
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"default-hcp": {HostType: &publicv1.HostTypeReference{Id: "acme_1tb"}, Size: 3},
					},
				},
			}}, nil
		}
		f.secrets.getFunc = func(req *privatev1.SecretsGetRequest) (*privatev1.SecretsGetResponse, error) {
			Expect(req.GetId()).To(Equal("secret-1"))
			return &privatev1.SecretsGetResponse{Object: &privatev1.Secret{
				Type: privatev1.SecretType_SECRET_TYPE_KUBECONFIG,
				Data: map[string][]byte{"kubeconfig": kubeconfigBytes},
			}}, nil
		}

		result, err := f.svc.Get(context.Background(), "X")
		Expect(err).NotTo(HaveOccurred())

		Expect(*result.Status).To(Equal(v1alpha1.ClusterStatusACTIVE))
		Expect(*result.Kubeconfig).To(Equal(base64.StdEncoding.EncodeToString(kubeconfigBytes)))
		Expect(f.secrets.GetCallCount()).To(Equal(1))
		// SC-M3-002: node_sets echoes OSAC's status.node_sets map directly.
		Expect(*result.NodeSets).To(Equal(map[string]v1alpha1.ClusterNodeSet{
			"default-hcp": {HostType: util.Ptr("acme_1tb"), Size: util.Ptr(3)},
		}))
	})

	// TC-U-211 (REQ-GET-030, AC-GET-020): a non-ACTIVE cluster never
	// triggers a Secret fetch.
	It("never fetches a kubeconfig Secret for a non-ACTIVE cluster (TC-U-211)", func() {
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
		Expect(f.secrets.GetCallCount()).To(Equal(0))
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
		Expect(f.secrets.GetCallCount()).To(Equal(0))
	})

	// TC-U-213 (REQ-GET-050, AC-GET-040): a broken backend-managed Secret is
	// an internal invariant failure, not a successful empty kubeconfig or a
	// cluster NotFound.
	DescribeTable("handles an ACTIVE cluster's kubeconfig Secret resolution errors (TC-U-213)",
		func(ref *publicv1.SecretLocalReference, secret *privatev1.Secret, secretErr error, wantCalls int, wantCode codes.Code) {
			f.fake.getFunc = func(req *publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
				return &publicv1.ClustersGetResponse{Object: &publicv1.Cluster{
					Id: req.GetId(),
					Status: &publicv1.ClusterStatus{
						State:            publicv1.ClusterState_CLUSTER_STATE_READY,
						KubeconfigSecret: ref,
					},
				}}, nil
			}
			f.secrets.getFunc = func(*privatev1.SecretsGetRequest) (*privatev1.SecretsGetResponse, error) {
				if secretErr != nil {
					return nil, secretErr
				}
				return &privatev1.SecretsGetResponse{Object: secret}, nil
			}

			_, err := f.svc.Get(context.Background(), "X")
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(wantCode))
			Expect(f.secrets.GetCallCount()).To(Equal(wantCalls))
		},
		Entry("missing reference", (*publicv1.SecretLocalReference)(nil), (*privatev1.Secret)(nil), nil, 0, codes.Internal),
		Entry("empty reference ID", &publicv1.SecretLocalReference{}, (*privatev1.Secret)(nil), nil, 0, codes.Internal),
		Entry("referenced Secret not found", &publicv1.SecretLocalReference{Id: "secret-1"}, (*privatev1.Secret)(nil), grpcstatus.Error(codes.NotFound, "no such secret"), 1, codes.Internal),
		Entry("nil Secret object", &publicv1.SecretLocalReference{Id: "secret-1"}, (*privatev1.Secret)(nil), nil, 1, codes.Internal),
		Entry("missing kubeconfig data key", &publicv1.SecretLocalReference{Id: "secret-1"}, &privatev1.Secret{Data: map[string][]byte{}}, nil, 1, codes.Internal),
		Entry("empty kubeconfig data", &publicv1.SecretLocalReference{Id: "secret-1"}, &privatev1.Secret{Data: map[string][]byte{"kubeconfig": {}}}, nil, 1, codes.Internal),
		Entry("transient Secrets/Get failure preserves its status", &publicv1.SecretLocalReference{Id: "secret-1"}, (*privatev1.Secret)(nil), grpcstatus.Error(codes.Unavailable, "osac unreachable"), 1, codes.Unavailable),
	)
})

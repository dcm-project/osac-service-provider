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

// baseSpec returns a fully-populated, valid ClusterSpec satisfying every
// REQ-CREATE-060 required field, matching TC-U-200's fixture values
// (id="X", version="1.29", nodes.worker.count=3, metadata.name="foo",
// provider_hints.osac.template_id="default-hcp"). Individual tests mutate a
// copy to exercise one dimension at a time.
func baseSpec() v1alpha1.ClusterSpec {
	return v1alpha1.ClusterSpec{
		Version: "1.29",
		Nodes: v1alpha1.ClusterNodes{
			Worker: v1alpha1.ClusterWorkerNodes{Count: 3},
		},
		Metadata: v1alpha1.ClusterMetadata{Name: "foo"},
		ProviderHints: v1alpha1.ClusterProviderHints{
			Osac: v1alpha1.OSACProviderHints{TemplateId: "default-hcp"},
		},
	}
}

var _ = Describe("Service.Create (Topic 4.1 Cluster Create)", func() {
	var f *fixture

	BeforeEach(func() {
		f = newFixture()
		DeferCleanup(f.Close)
	})

	// TC-U-200 (REQ-CREATE-010/020/025, AC-CREATE-010): Create translates
	// and dispatches the full field set with exact values.
	It("translates the full field set and dispatches exact values to Clusters/Create (TC-U-200)", func() {
		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).NotTo(HaveOccurred())

		Expect(f.fake.CreateCallCount()).To(Equal(1))
		req := f.fake.LastCreateCall()
		obj := req.GetObject()

		Expect(obj.GetId()).To(Equal("X"))
		Expect(obj.GetSpec().GetTemplate()).To(Equal("default-hcp"))
		Expect(obj.GetSpec().GetNodeSets()).To(HaveKey("default-hcp"))
		Expect(obj.GetSpec().GetNodeSets()["default-hcp"].GetSize()).To(Equal(int32(3)))
		Expect(obj.GetMetadata().GetName()).To(Equal("foo"))
		Expect(obj.GetSpec().GetReleaseImage()).To(Equal("quay.io/openshift-release-dev/ocp-release:4.16.0-multi"))
	})

	// TC-U-201 (REQ-CREATE-030, AC-CREATE-020): ownership labels are set
	// exactly, merged with (not replacing) caller-supplied labels.
	It("sets ownership labels exactly, merged with caller labels (TC-U-201)", func() {
		spec := baseSpec()
		spec.Metadata.Labels = &map[string]string{"team": "platform"}

		_, err := f.svc.Create(context.Background(), "X", spec)
		Expect(err).NotTo(HaveOccurred())

		labels := f.fake.LastCreateCall().GetObject().GetMetadata().GetLabels()
		Expect(labels).To(Equal(map[string]string{
			"team":                "platform",
			"dcm.io/managed-by":   "dcm",
			"dcm.io/instance-id":  "X",
			"dcm.io/service-type": "cluster",
		}))
	})

	// TC-U-202 (REQ-CREATE-040, AC-CREATE-030): AlreadyExists on Create
	// triggers a Get and returns the existing resource, not a new one.
	It("returns the existing resource via Get when Create reports AlreadyExists (TC-U-202)", func() {
		f.fake.createFunc = func(*publicv1.ClustersCreateRequest) (*publicv1.ClustersCreateResponse, error) {
			return nil, grpcstatus.Error(codes.AlreadyExists, "cluster X already exists")
		}
		f.fake.getFunc = func(req *publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
			return &publicv1.ClustersGetResponse{Object: &publicv1.Cluster{
				Id:     req.GetId(),
				Status: &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_PROGRESSING},
			}}, nil
		}

		result, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).NotTo(HaveOccurred())

		Expect(*result.Id).To(Equal("X"))
		Expect(result.Status).To(Equal(v1alpha1.ClusterStatusPROGRESSING))
		Expect(f.fake.GetCallCount()).To(Equal(1))
	})

	// TC-U-203 (REQ-CREATE-070, AC-CREATE-060): worker CPU/memory/storage
	// hints never become a host_type override.
	It("never translates worker sizing hints into a host_type override (TC-U-203)", func() {
		spec := baseSpec()
		spec.Nodes.Worker.Cpu = util.Ptr(8)
		spec.Nodes.Worker.Memory = util.Ptr("32GB")
		spec.Nodes.Worker.Storage = util.Ptr("250GB")

		_, err := f.svc.Create(context.Background(), "X", spec)
		Expect(err).NotTo(HaveOccurred())

		nodeSet := f.fake.LastCreateCall().GetObject().GetSpec().GetNodeSets()["default-hcp"]
		Expect(nodeSet.GetHostType()).To(Equal(""))
	})

	// TC-U-204 (REQ-CREATE-025): version translation table covers each
	// supported placeholder version, and an explicit release_image override
	// is used verbatim instead of the table lookup.
	DescribeTable("translates spec.version to the documented release_image (TC-U-204)",
		func(version, wantReleaseImage string) {
			spec := baseSpec()
			spec.Version = version

			_, err := f.svc.Create(context.Background(), "X", spec)
			Expect(err).NotTo(HaveOccurred())

			Expect(f.fake.LastCreateCall().GetObject().GetSpec().GetReleaseImage()).To(Equal(wantReleaseImage))
		},
		Entry("1.29 -> OpenShift 4.16", "1.29", "quay.io/openshift-release-dev/ocp-release:4.16.0-multi"),
		Entry("1.30 -> OpenShift 4.17", "1.30", "quay.io/openshift-release-dev/ocp-release:4.17.0-multi"),
		Entry("1.31 -> OpenShift 4.18", "1.31", "quay.io/openshift-release-dev/ocp-release:4.18.0-multi"),
		Entry("1.32 -> OpenShift 4.19", "1.32", "quay.io/openshift-release-dev/ocp-release:4.19.0-multi"),
		Entry("1.33 -> OpenShift 4.20", "1.33", "quay.io/openshift-release-dev/ocp-release:4.20.0-multi"),
	)

	It("leaves release_image unset when the version has no table entry and no override is given (TC-U-204)", func() {
		spec := baseSpec()
		spec.Version = "1.99" // not in the placeholder table

		_, err := f.svc.Create(context.Background(), "X", spec)
		Expect(err).NotTo(HaveOccurred())

		Expect(f.fake.LastCreateCall().GetObject().GetSpec().GetReleaseImage()).To(Equal(""))
	})

	It("uses an explicit provider_hints.osac.release_image override verbatim instead of the table lookup (TC-U-204)", func() {
		spec := baseSpec()
		spec.Version = "1.29" // has its own table entry, to prove the override wins
		spec.ProviderHints.Osac.ReleaseImage = util.Ptr("custom-registry.example.com/custom-image:latest")

		_, err := f.svc.Create(context.Background(), "X", spec)
		Expect(err).NotTo(HaveOccurred())

		Expect(f.fake.LastCreateCall().GetObject().GetSpec().GetReleaseImage()).To(Equal("custom-registry.example.com/custom-image:latest"))
	})

	// Supplementary (REQ-ERR-010/030 precondition): a Create failure other
	// than AlreadyExists is propagated raw, for the shared error-mapping
	// topic (§4.6) to translate — exact gRPC code, not merely "an error
	// occurred".
	It("propagates a non-AlreadyExists Create error raw", func() {
		f.fake.createFunc = func(*publicv1.ClustersCreateRequest) (*publicv1.ClustersCreateResponse, error) {
			return nil, grpcstatus.Error(codes.InvalidArgument, "bad template")
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).To(HaveOccurred())

		st, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.InvalidArgument))
	})

	// Supplementary: if the AlreadyExists-recovery Get itself fails, that
	// failure is propagated raw rather than being swallowed.
	It("propagates the recovery Get's error when it fails after Create reports AlreadyExists", func() {
		f.fake.createFunc = func(*publicv1.ClustersCreateRequest) (*publicv1.ClustersCreateResponse, error) {
			return nil, grpcstatus.Error(codes.AlreadyExists, "cluster X already exists")
		}
		f.fake.getFunc = func(*publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "osac unreachable")
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).To(HaveOccurred())

		st, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unavailable))
	})
})

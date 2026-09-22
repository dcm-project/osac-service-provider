package cluster_test

import (
	"bytes"
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/util"
	"github.com/dcm-project/osac-service-provider/internal/versionmatrix"
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
	// and dispatches the full field set with exact values. The fake
	// template's node-set key ("compute", fixture_test.go's
	// defaultNodeSetKey) is deliberately distinct from templateID
	// ("default-hcp") to prove Create uses the resolved key, not the
	// template ID.
	It("translates the full field set and dispatches exact values to Clusters/Create (TC-U-200)", func() {
		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).NotTo(HaveOccurred())

		Expect(f.fake.CreateCallCount()).To(Equal(1))
		req := f.fake.LastCreateCall()
		obj := req.GetObject()

		Expect(obj.GetId()).To(Equal("X"))
		Expect(obj.GetSpec().GetTemplate().GetId()).To(Equal("default-hcp"))
		Expect(obj.GetSpec().GetNodeSets()).To(HaveKey(defaultNodeSetKey))
		Expect(obj.GetSpec().GetNodeSets()[defaultNodeSetKey].GetSize()).To(Equal(int32(3)))
		Expect(obj.GetMetadata().GetName()).To(Equal("foo"))
		Expect(obj.GetSpec().GetVersion().GetName()).To(Equal("tierb-1-29"))

		wire, err := proto.Marshal(f.fake.LastCreateCall())
		Expect(err).NotTo(HaveOccurred())
		Expect(bytes.Contains(wire, []byte{0x2a, 0x03, 'f', 'o', 'o'})).To(BeTrue(),
			"metadata.name must use FFS v0.0.107's field 5 wire tag")
	})

	// TC-U-200b (REQ-CREATE-080): the node-set key comes from
	// ClusterTemplates/Get, not from templateID — proven here with a key
	// that looks nothing like a template ID at all.
	It("resolves the node-set key via ClusterTemplates/Get instead of assuming templateID (TC-U-200b)", func() {
		f.templates.getFunc = func(req *publicv1.ClusterTemplatesGetRequest) (*publicv1.ClusterTemplatesGetResponse, error) {
			Expect(req.GetId()).To(Equal("default-hcp"))
			return &publicv1.ClusterTemplatesGetResponse{Object: &publicv1.ClusterTemplate{
				NodeSets: map[string]*publicv1.ClusterTemplateNodeSet{"gpu-workers": {}},
			}}, nil
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).NotTo(HaveOccurred())

		nodeSets := f.fake.LastCreateCall().GetObject().GetSpec().GetNodeSets()
		Expect(nodeSets).To(HaveKey("gpu-workers"))
		Expect(nodeSets).NotTo(HaveKey("default-hcp"))
	})

	// TC-U-207 (REQ-CREATE-090): a template with more than one node-set
	// key is rejected before Clusters/Create is ever called.
	It("rejects a template with more than one node-set key (TC-U-207)", func() {
		f.templates.getFunc = func(*publicv1.ClusterTemplatesGetRequest) (*publicv1.ClusterTemplatesGetResponse, error) {
			return &publicv1.ClusterTemplatesGetResponse{Object: &publicv1.ClusterTemplate{
				NodeSets: map[string]*publicv1.ClusterTemplateNodeSet{"compute": {}, "gpu": {}},
			}}, nil
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(grpcstatus.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// TC-U-209 (REQ-CREATE-090): a template with zero node-set keys is
	// rejected the same way — there's nothing to apply nodes.worker.count
	// to.
	It("rejects a template with zero node-set keys (TC-U-209)", func() {
		f.templates.getFunc = func(*publicv1.ClusterTemplatesGetRequest) (*publicv1.ClusterTemplatesGetResponse, error) {
			return &publicv1.ClusterTemplatesGetResponse{Object: &publicv1.ClusterTemplate{}}, nil
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(grpcstatus.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// TC-U-208 (REQ-CREATE-100): an unknown template_id is a 400, not the
	// 404 a raw NotFound passthrough would produce.
	It("rejects an unknown template_id as InvalidArgument, not NotFound (TC-U-208)", func() {
		f.templates.getFunc = func(*publicv1.ClusterTemplatesGetRequest) (*publicv1.ClusterTemplatesGetResponse, error) {
			return nil, grpcstatus.Error(codes.NotFound, "template not found")
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(grpcstatus.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// TC-U-200c: unlike TC-U-208's NotFound-to-InvalidArgument remap, any
	// other gRPC error from ClusterTemplates/Get (e.g. a transient
	// Unavailable) is passed through unchanged — it's a real backend
	// failure, not a bad value in the caller's own request.
	It("passes through a non-NotFound ClusterTemplates/Get error unchanged (TC-U-200c)", func() {
		f.templates.getFunc = func(*publicv1.ClusterTemplatesGetRequest) (*publicv1.ClusterTemplatesGetResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "templates backend unreachable")
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(grpcstatus.Code(err)).To(Equal(codes.Unavailable))
		Expect(err).To(MatchError(ContainSubstring("templates backend unreachable")))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
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
		Expect(*result.Status).To(Equal(v1alpha1.ClusterStatusPROGRESSING))
		Expect(f.fake.GetCallCount()).To(Equal(1))
		// The retried path echoes version too — same request, same
		// spec.version, shouldn't return a different body shape than the
		// first-time path (SC-M3-002).
		Expect(result.Version).To(HaveValue(Equal("1.29")))
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

		nodeSet := f.fake.LastCreateCall().GetObject().GetSpec().GetNodeSets()[defaultNodeSetKey]
		Expect(nodeSet.GetHostType()).To(BeNil())
	})

	// TC-U-204 (REQ-CREATE-025): each supported DCM minor resolves to the
	// matching OSAC ClusterVersion reference.
	DescribeTable("resolves spec.version to an OSAC ClusterVersion reference (TC-U-204)",
		func(version, wantVersionName string) {
			spec := baseSpec()
			spec.Version = version

			_, err := f.svc.Create(context.Background(), "X", spec)
			Expect(err).NotTo(HaveOccurred())

			Expect(f.fake.LastCreateCall().GetObject().GetSpec().GetVersion().GetName()).To(Equal(wantVersionName))
		},
		Entry("1.29 -> tierb-1-29", "1.29", "tierb-1-29"),
		Entry("1.30 -> tierb-1-30", "1.30", "tierb-1-30"),
		Entry("1.31 -> tierb-1-31", "1.31", "tierb-1-31"),
		Entry("1.32 -> tierb-1-32", "1.32", "tierb-1-32"),
		Entry("1.33 -> tierb-1-33", "1.33", "tierb-1-33"),
	)

	It("rejects a version with no matching OSAC ClusterVersion (TC-U-204)", func() {
		spec := baseSpec()
		spec.Version = "1.99"

		_, err := f.svc.Create(context.Background(), "X", spec)
		Expect(grpcstatus.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// TC-U-523 (REQ-VERSION-060, AC-VERSION-110): the resolver compares
	// SemVer values instead of trusting OSAC catalog order.
	It("selects the latest matching OSAC ClusterVersion z-stream regardless of list order (TC-U-523)", func() {
		f.versions.listFunc = func(*publicv1.ClusterVersionsListRequest) (*publicv1.ClusterVersionsListResponse, error) {
			return &publicv1.ClusterVersionsListResponse{Items: []*publicv1.ClusterVersion{
				{
					Metadata: &publicv1.Metadata{Name: "tierb-1-29-2"},
					Spec:     &publicv1.ClusterVersionSpec{Version: "1.29.2"},
				},
				{
					Metadata: &publicv1.Metadata{Name: "tierb-1-30-99"},
					Spec:     &publicv1.ClusterVersionSpec{Version: "1.30.99"},
				},
				{
					Metadata: &publicv1.Metadata{Name: "tierb-1-29-10"},
					Spec:     &publicv1.ClusterVersionSpec{Version: "1.29.10"},
				},
			}}, nil
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).NotTo(HaveOccurred())
		Expect(f.fake.LastCreateCall().GetObject().GetSpec().GetVersion().GetName()).To(Equal("tierb-1-29-10"))
	})

	It("ignores malformed and unnamed catalog candidates and tie-breaks equal versions by name (TC-U-524)", func() {
		f.versions.listFunc = func(*publicv1.ClusterVersionsListRequest) (*publicv1.ClusterVersionsListResponse, error) {
			return &publicv1.ClusterVersionsListResponse{Items: []*publicv1.ClusterVersion{
				{
					Metadata: &publicv1.Metadata{},
					Spec:     &publicv1.ClusterVersionSpec{Version: "1.29.8"},
				},
				{
					Metadata: &publicv1.Metadata{Name: "tierb-invalid"},
					Spec:     &publicv1.ClusterVersionSpec{Version: "1.29.not-semver"},
				},
				{
					Metadata: &publicv1.Metadata{Name: "tierb-1-29-10-z"},
					Spec:     &publicv1.ClusterVersionSpec{Version: "1.29.10"},
				},
				{
					Metadata: &publicv1.Metadata{Name: "tierb-1-29-10-a"},
					Spec:     &publicv1.ClusterVersionSpec{Version: "1.29.10"},
				},
			}}, nil
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).NotTo(HaveOccurred())
		Expect(f.fake.LastCreateCall().GetObject().GetSpec().GetVersion().GetName()).To(Equal("tierb-1-29-10-a"))
	})

	It("propagates a ClusterVersions/List error before dispatching Create (TC-U-525)", func() {
		f.versions.listFunc = func(*publicv1.ClusterVersionsListRequest) (*publicv1.ClusterVersionsListResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "catalog unavailable")
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(grpcstatus.Code(err)).To(Equal(codes.Unavailable))
		Expect(grpcstatus.Convert(err).Message()).To(Equal("catalog unavailable"))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	It("rejects the legacy release_image override because OSAC now uses ClusterVersions", func() {
		spec := baseSpec()
		spec.Version = "1.29"
		spec.ProviderHints.Osac.ReleaseImage = util.Ptr("custom-registry.example.com/custom-image:latest")

		_, err := f.svc.Create(context.Background(), "X", spec)
		Expect(grpcstatus.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(grpcstatus.Convert(err).Message()).To(Equal("OSAC ClusterVersion selection does not support provider_hints.osac.release_image"))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// TC-U-520 (REQ-VERSION-060, AC-VERSION-060): Create resolves the
	// requested version against the live catalog rather than a hardcoded name.
	DescribeTable("resolves versions from the OSAC catalog (TC-U-520)",
		func(version, wantVersionName string) {
			testMatrix := versionmatrix.Matrix{
				"9.01": "quay.io/example/release:9.01",
				"9.02": "quay.io/example/release:9.02",
			}
			f := newFixtureWithMatrix(testMatrix)
			defer f.Close()

			spec := baseSpec()
			spec.Version = version

			_, err := f.svc.Create(context.Background(), "X", spec)
			Expect(err).NotTo(HaveOccurred())

			Expect(f.fake.LastCreateCall().GetObject().GetSpec().GetVersion().GetName()).To(Equal(wantVersionName))
		},
		Entry("9.01 -> tierb-9-01", "9.01", "tierb-9-01"),
		Entry("9.02 -> tierb-9-02", "9.02", "tierb-9-02"),
	)

	// TC-U-521 (REQ-VERSION-060, AC-VERSION-070): the legacy override is
	// rejected rather than silently changing the OSAC version selection.
	It("rejects an explicit release_image override", func() {
		testMatrix := versionmatrix.Matrix{"9.01": "quay.io/example/release:9.01"}
		f := newFixtureWithMatrix(testMatrix)
		defer f.Close()

		spec := baseSpec()
		spec.Version = "9.99" // absent from testMatrix
		spec.ProviderHints.Osac.ReleaseImage = util.Ptr("custom-image")

		_, err := f.svc.Create(context.Background(), "X", spec)
		Expect(grpcstatus.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// TC-U-522 (REQ-VERSION-070): SupportsVersion reports injected-matrix
	// membership exactly.
	It("reports matrix membership exactly via SupportsVersion (TC-U-522)", func() {
		testMatrix := versionmatrix.Matrix{
			"9.01": "quay.io/example/release:9.01",
			"9.02": "quay.io/example/release:9.02",
		}
		f := newFixtureWithMatrix(testMatrix)
		defer f.Close()

		Expect(f.svc.SupportsVersion("9.01")).To(BeTrue())
		Expect(f.svc.SupportsVersion("9.99")).To(BeFalse())
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

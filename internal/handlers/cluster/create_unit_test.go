package cluster_test

import (
	"context"
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

// validCreateBody is a fully-populated, valid CreateClusterJSONRequestBody
// satisfying every REQ-CREATE-060 required field. Individual tests mutate a
// copy to omit exactly one field.
func validCreateBody() v1alpha1.CreateClusterJSONRequestBody {
	return v1alpha1.CreateClusterJSONRequestBody{
		Spec: v1alpha1.ClusterSpec{
			Version: "1.29",
			Nodes: v1alpha1.ClusterNodes{
				Worker: v1alpha1.ClusterWorkerNodes{Count: 3},
			},
			Metadata: v1alpha1.ClusterMetadata{Name: "foo"},
			ProviderHints: v1alpha1.ClusterProviderHints{
				Osac: v1alpha1.OSACProviderHints{TemplateId: "default-hcp"},
			},
		},
	}
}

var _ = Describe("Handler.CreateCluster request validation (Topic 1 Cluster Create)", func() {
	var f *fixture

	BeforeEach(func() {
		f = newFixture()
		DeferCleanup(f.Close)
	})

	// TC-U-205 (REQ-CREATE-060, AC-CREATE-040): a missing id query
	// parameter is rejected before calling OSAC. The router itself already
	// rejects a fully-absent "id" (empty CreateClusterParams.Id here
	// stands in for that, at the StrictServerInterface layer this package
	// owns) — this proves the handler's own validation path additionally
	// guards against it reaching internal/cluster.
	It("rejects an empty id before calling OSAC (TC-U-205)", func() {
		req := oapigen.CreateClusterRequestObject{
			Params: v1alpha1.CreateClusterParams{Id: ""},
			Body:   ptrBody(validCreateBody()),
		}

		resp, err := f.handler.CreateCluster(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())

		rec := httptest.NewRecorder()
		Expect(resp.VisitCreateClusterResponse(rec)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// TC-U-206 (REQ-CREATE-060, AC-CREATE-050): a missing required spec
	// field is rejected before calling OSAC, table-driven across all 4
	// required fields.
	DescribeTable("rejects a request missing a required spec field, before calling OSAC (TC-U-206)",
		func(mutate func(*v1alpha1.CreateClusterJSONRequestBody)) {
			body := validCreateBody()
			mutate(&body)

			req := oapigen.CreateClusterRequestObject{
				Params: v1alpha1.CreateClusterParams{Id: "X"},
				Body:   &body,
			}

			resp, err := f.handler.CreateCluster(context.Background(), req)
			Expect(err).NotTo(HaveOccurred())

			rec := httptest.NewRecorder()
			Expect(resp.VisitCreateClusterResponse(rec)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
			Expect(f.fake.CreateCallCount()).To(Equal(0))
		},
		Entry("spec.version absent", func(b *v1alpha1.CreateClusterJSONRequestBody) { b.Spec.Version = "" }),
		Entry("spec.nodes.worker.count absent (zero)", func(b *v1alpha1.CreateClusterJSONRequestBody) { b.Spec.Nodes.Worker.Count = 0 }),
		Entry("spec.metadata.name absent", func(b *v1alpha1.CreateClusterJSONRequestBody) { b.Spec.Metadata.Name = "" }),
		Entry("spec.provider_hints.osac.template_id absent", func(b *v1alpha1.CreateClusterJSONRequestBody) { b.Spec.ProviderHints.Osac.TemplateId = "" }),
	)

	// Supplementary (REQ-ERR-010/030 precondition): a genuine OSAC Create
	// failure (not the AlreadyExists carve-out, already covered by
	// internal/cluster's own TC-U-202) is mapped through the shared
	// mapError, proving CreateCluster's error path actually reaches it.
	It("maps a genuine (non-AlreadyExists) OSAC Create failure through the shared error mapper", func() {
		f.fake.createFunc = func(*publicv1.ClustersCreateRequest) (*publicv1.ClustersCreateResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "osac unreachable")
		}

		req := oapigen.CreateClusterRequestObject{
			Params: v1alpha1.CreateClusterParams{Id: "X"},
			Body:   ptrBody(validCreateBody()),
		}
		resp, err := f.handler.CreateCluster(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())

		rec := httptest.NewRecorder()
		Expect(resp.VisitCreateClusterResponse(rec)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusBadGateway))
	})
})

func ptrBody(b v1alpha1.CreateClusterJSONRequestBody) *v1alpha1.CreateClusterJSONRequestBody {
	return &b
}

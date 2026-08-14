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

var _ = Describe("Service.List (Topic 4.3 Cluster List)", func() {
	var f *fixture

	BeforeEach(func() {
		f = newFixture()
		DeferCleanup(f.Close)
	})

	// TC-U-220 (REQ-LIST-010/030, AC-LIST-010): List applies the ownership
	// filter and default page size, and returns exact field values.
	It("applies the ownership filter, default page size, and exact field values (TC-U-220)", func() {
		f.fake.listFunc = func(*publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error) {
			return &publicv1.ClustersListResponse{
				Size:  2,
				Total: 2,
				Items: []*publicv1.Cluster{
					{Id: "c1", Status: &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_READY}},
					{Id: "c2", Status: &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_PROGRESSING}},
				},
			}, nil
		}

		result, err := f.svc.List(context.Background(), v1alpha1.ListClustersParams{})
		Expect(err).NotTo(HaveOccurred())

		calls := f.fake.ListCalls()
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].GetFilter()).To(Equal(`this.metadata.labels["dcm.io/managed-by"] == "dcm"`))
		Expect(calls[0].GetLimit()).To(Equal(int32(50)))

		Expect(result.Results).To(HaveLen(2))
		Expect(*result.Results[0].Id).To(Equal("c1"))
		Expect(*result.Results[0].Status).To(Equal(v1alpha1.ClusterStatusACTIVE))
		Expect(*result.Results[1].Id).To(Equal("c2"))
		Expect(*result.Results[1].Status).To(Equal(v1alpha1.ClusterStatusPROGRESSING))
	})

	// Supplementary (REQ-LIST-020): an explicit max_page_size overrides the
	// default page size.
	It("uses an explicit max_page_size instead of the default", func() {
		f.fake.listFunc = func(*publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error) {
			return &publicv1.ClustersListResponse{Size: 10, Total: 10}, nil
		}

		_, err := f.svc.List(context.Background(), v1alpha1.ListClustersParams{MaxPageSize: util.Ptr(int32(10))})
		Expect(err).NotTo(HaveOccurred())

		Expect(f.fake.ListCalls()[0].GetLimit()).To(Equal(int32(10)))
	})

	// TC-U-221 (REQ-LIST-020/040, AC-LIST-020): page_token round-trips
	// through OSAC's offset correctly, across two calls.
	It("round-trips page_token through OSAC's offset (TC-U-221)", func() {
		f.fake.listFunc = func(*publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error) {
			return &publicv1.ClustersListResponse{Size: 50, Total: 100}, nil
		}

		first, err := f.svc.List(context.Background(), v1alpha1.ListClustersParams{})
		Expect(err).NotTo(HaveOccurred())
		Expect(first.NextPageToken).NotTo(BeNil())
		Expect(*first.NextPageToken).NotTo(BeEmpty())

		_, err = f.svc.List(context.Background(), v1alpha1.ListClustersParams{PageToken: first.NextPageToken})
		Expect(err).NotTo(HaveOccurred())

		calls := f.fake.ListCalls()
		Expect(calls).To(HaveLen(2))
		Expect(calls[1].GetOffset()).To(Equal(int32(50)))
	})

	// TC-U-222 (REQ-LIST-030, AC-LIST-030): List entries never populate
	// kubeconfig, and never trigger a kubeconfig fetch.
	It("never populates kubeconfig on List entries (TC-U-222)", func() {
		f.fake.listFunc = func(*publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error) {
			return &publicv1.ClustersListResponse{
				Size: 1, Total: 1,
				Items: []*publicv1.Cluster{
					{Id: "c1", Status: &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_READY}},
				},
			}, nil
		}

		result, err := f.svc.List(context.Background(), v1alpha1.ListClustersParams{})
		Expect(err).NotTo(HaveOccurred())

		Expect(result.Results).To(HaveLen(1))
		Expect(result.Results[0].Kubeconfig).To(BeNil())
		Expect(f.fake.GetKubeconfigCallCount()).To(Equal(0))
	})

	// Supplementary (REQ-ERR-010/030 precondition): a List RPC failure is
	// propagated raw, for the shared error-mapping topic to translate.
	It("propagates a List error raw", func() {
		f.fake.listFunc = func(*publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "osac unreachable")
		}

		_, err := f.svc.List(context.Background(), v1alpha1.ListClustersParams{})
		Expect(err).To(HaveOccurred())

		st, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unavailable))
	})

	// Supplementary: a malformed page_token (not one this SP itself ever
	// issued) is rejected as InvalidArgument (400), not left to fall
	// through to a 500, covering both of decodePageToken's error returns.
	It("rejects a page_token that isn't valid base64", func() {
		_, err := f.svc.List(context.Background(), v1alpha1.ListClustersParams{PageToken: util.Ptr("not-valid-base64!!!")})
		Expect(grpcstatus.Code(err)).To(Equal(codes.InvalidArgument))
	})

	It("rejects a page_token that decodes to non-numeric content", func() {
		// base64 for the literal string "not-a-number".
		_, err := f.svc.List(context.Background(), v1alpha1.ListClustersParams{PageToken: util.Ptr("bm90LWEtbnVtYmVy")})
		Expect(grpcstatus.Code(err)).To(Equal(codes.InvalidArgument))
	})
})

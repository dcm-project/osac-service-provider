package vm_test

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

var _ = Describe("Service.List (Topic 4.3 VM List)", func() {
	var f *fixture

	BeforeEach(func() {
		f = newFixture()
		DeferCleanup(f.Close)
	})

	// TC-U-320 (REQ-VMLIST-010/030, AC-VMLIST-010): List applies the
	// ownership filter, default page size, and exact field values
	// including the IP echo.
	It("applies the ownership filter, default page size, and exact field values (TC-U-320)", func() {
		f.fake.listFunc = func(*publicv1.ComputeInstancesListRequest) (*publicv1.ComputeInstancesListResponse, error) {
			return &publicv1.ComputeInstancesListResponse{
				Size:  2,
				Total: 2,
				Items: []*publicv1.ComputeInstance{
					{Id: "v1", Status: &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, InternalIpAddress: "10.0.0.1"}},
					{Id: "v2", Status: &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING}},
				},
			}, nil
		}

		result, err := f.svc.List(context.Background(), v1alpha1.ListVMsParams{})
		Expect(err).NotTo(HaveOccurred())

		calls := f.fake.ListCalls()
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].GetFilter()).To(Equal(`this.metadata.labels["dcm.io/managed-by"] == "dcm"`))
		Expect(calls[0].GetLimit()).To(Equal(int32(50)))

		Expect(result.Results).To(HaveLen(2))
		Expect(*result.Results[0].Id).To(Equal("v1"))
		Expect(result.Results[0].Status).To(Equal(v1alpha1.VMStatusRUNNING))
		Expect(*result.Results[0].InternalIpAddress).To(Equal("10.0.0.1"))
		Expect(*result.Results[1].Id).To(Equal("v2"))
		Expect(result.Results[1].Status).To(Equal(v1alpha1.VMStatusPROVISIONING))
	})

	// TC-U-321 (REQ-VMLIST-020/040, AC-VMLIST-020): page_token round-trips
	// through OSAC's offset correctly, across two calls.
	It("round-trips page_token through OSAC's offset (TC-U-321)", func() {
		f.fake.listFunc = func(*publicv1.ComputeInstancesListRequest) (*publicv1.ComputeInstancesListResponse, error) {
			return &publicv1.ComputeInstancesListResponse{Size: 50, Total: 100}, nil
		}

		first, err := f.svc.List(context.Background(), v1alpha1.ListVMsParams{})
		Expect(err).NotTo(HaveOccurred())
		Expect(first.NextPageToken).NotTo(BeNil())
		Expect(*first.NextPageToken).NotTo(BeEmpty())

		_, err = f.svc.List(context.Background(), v1alpha1.ListVMsParams{PageToken: first.NextPageToken})
		Expect(err).NotTo(HaveOccurred())

		calls := f.fake.ListCalls()
		Expect(calls).To(HaveLen(2))
		Expect(calls[1].GetOffset()).To(Equal(int32(50)))
	})

	// Supplementary (REQ-VMLIST-020): an explicit max_page_size overrides
	// the default page size.
	It("uses an explicit max_page_size instead of the default", func() {
		f.fake.listFunc = func(*publicv1.ComputeInstancesListRequest) (*publicv1.ComputeInstancesListResponse, error) {
			return &publicv1.ComputeInstancesListResponse{Size: 10, Total: 10}, nil
		}

		_, err := f.svc.List(context.Background(), v1alpha1.ListVMsParams{MaxPageSize: util.Ptr(int32(10))})
		Expect(err).NotTo(HaveOccurred())

		Expect(f.fake.ListCalls()[0].GetLimit()).To(Equal(int32(10)))
	})

	// Supplementary (REQ-VMERR-010/030 precondition): a List RPC failure
	// is propagated raw, for the shared error-mapping topic to translate.
	It("propagates a List error raw", func() {
		f.fake.listFunc = func(*publicv1.ComputeInstancesListRequest) (*publicv1.ComputeInstancesListResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "osac unreachable")
		}

		_, err := f.svc.List(context.Background(), v1alpha1.ListVMsParams{})
		Expect(err).To(HaveOccurred())

		st, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unavailable))
	})

	// Supplementary: a malformed page_token (not one this SP itself ever
	// issued) is rejected with an error rather than silently defaulting to
	// offset 0.
	It("rejects a page_token that isn't valid base64", func() {
		_, err := f.svc.List(context.Background(), v1alpha1.ListVMsParams{PageToken: util.Ptr("not-valid-base64!!!")})
		Expect(err).To(HaveOccurred())
	})

	It("rejects a page_token that decodes to non-numeric content", func() {
		// base64 for the literal string "not-a-number".
		_, err := f.svc.List(context.Background(), v1alpha1.ListVMsParams{PageToken: util.Ptr("bm90LWEtbnVtYmVy")})
		Expect(err).To(HaveOccurred())
	})
})

package vm_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

var _ = Describe("Service.Delete (Topic 4.4 VM Delete)", func() {
	var f *fixture

	BeforeEach(func() {
		f = newFixture()
		DeferCleanup(f.Close)
	})

	// TC-U-330 (REQ-VMDELETE-010/040, AC-VMDELETE-010): a successful
	// delete does not poll for confirmation.
	It("succeeds without polling for confirmation (TC-U-330)", func() {
		err := f.svc.Delete(context.Background(), "X")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.fake.GetCallCount()).To(Equal(0))
	})

	// TC-U-331 (REQ-VMDELETE-020, AC-VMDELETE-020): NotFound on Delete is
	// treated as success, not a not-found error.
	It("treats NotFound as success (TC-U-331)", func() {
		f.fake.deleteFunc = func(*publicv1.ComputeInstancesDeleteRequest) (*publicv1.ComputeInstancesDeleteResponse, error) {
			return nil, grpcstatus.Error(codes.NotFound, "no such compute instance")
		}

		err := f.svc.Delete(context.Background(), "X")
		Expect(err).NotTo(HaveOccurred())
	})

	// TC-U-332 (REQ-VMDELETE-030, AC-VMDELETE-030): a genuine delete
	// failure is surfaced raw (exact gRPC code), not swallowed by the
	// NotFound carve-out.
	It("surfaces a genuine delete failure raw (TC-U-332)", func() {
		f.fake.deleteFunc = func(*publicv1.ComputeInstancesDeleteRequest) (*publicv1.ComputeInstancesDeleteResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "osac unreachable")
		}

		err := f.svc.Delete(context.Background(), "X")
		Expect(err).To(HaveOccurred())

		st, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unavailable))
	})
})

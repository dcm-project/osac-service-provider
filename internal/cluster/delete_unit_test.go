package cluster_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

var _ = Describe("Service.Delete (Topic 4.4 Cluster Delete)", func() {
	var f *fixture

	BeforeEach(func() {
		f = newFixture()
		DeferCleanup(f.Close)
	})

	// TC-U-230 (REQ-DELETE-010/040, AC-DELETE-010): a successful delete
	// does not poll for confirmation.
	It("succeeds without polling for confirmation (TC-U-230)", func() {
		err := f.svc.Delete(context.Background(), "X")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.fake.GetCallCount()).To(Equal(0))
	})

	// TC-U-231 (REQ-DELETE-020, AC-DELETE-020): NotFound on Delete is
	// treated as success, not a not-found error.
	It("treats NotFound as success (TC-U-231)", func() {
		f.fake.deleteFunc = func(*publicv1.ClustersDeleteRequest) (*publicv1.ClustersDeleteResponse, error) {
			return nil, grpcstatus.Error(codes.NotFound, "no such cluster")
		}

		err := f.svc.Delete(context.Background(), "X")
		Expect(err).NotTo(HaveOccurred())
	})

	// TC-U-232 (REQ-DELETE-030, AC-DELETE-030): a genuine delete failure is
	// surfaced raw (exact gRPC code), not swallowed by the NotFound
	// carve-out.
	It("surfaces a genuine delete failure raw (TC-U-232)", func() {
		f.fake.deleteFunc = func(*publicv1.ClustersDeleteRequest) (*publicv1.ClustersDeleteResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "osac unreachable")
		}

		err := f.svc.Delete(context.Background(), "X")
		Expect(err).To(HaveOccurred())

		st, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unavailable))
	})
})

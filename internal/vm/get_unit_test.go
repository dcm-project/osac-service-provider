package vm_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

var _ = Describe("Service.Get (Topic 4.2 VM Get)", func() {
	var f *fixture

	BeforeEach(func() {
		f = newFixture()
		DeferCleanup(f.Close)
	})

	// TC-U-310 (REQ-VMGET-010/030, AC-VMGET-010): IP addresses are echoed
	// exactly from OSAC's status.
	It("echoes IP addresses exactly for a running VM (TC-U-310)", func() {
		f.fake.getFunc = func(req *publicv1.ComputeInstancesGetRequest) (*publicv1.ComputeInstancesGetResponse, error) {
			return &publicv1.ComputeInstancesGetResponse{Object: &publicv1.ComputeInstance{
				Id: req.GetId(),
				Status: &publicv1.ComputeInstanceStatus{
					State:             publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
					InternalIpAddress: "10.200.1.5",
					ExternalIpAddress: "",
				},
			}}, nil
		}

		result, err := f.svc.Get(context.Background(), "X")
		Expect(err).NotTo(HaveOccurred())

		Expect(result.Status).To(Equal(v1alpha1.VMStatusRUNNING))
		Expect(*result.InternalIpAddress).To(Equal("10.200.1.5"))
		Expect(*result.ExternalIpAddress).To(Equal(""))
	})

	// TC-U-311 (REQ-VMGET-020, AC-VMGET-020): a nonexistent VM's NotFound
	// is propagated raw, for the shared error-mapping topic (§4.7) to turn
	// into HTTP 404.
	It("propagates NotFound raw for a nonexistent VM (TC-U-311)", func() {
		f.fake.getFunc = func(*publicv1.ComputeInstancesGetRequest) (*publicv1.ComputeInstancesGetResponse, error) {
			return nil, grpcstatus.Error(codes.NotFound, "no such compute instance")
		}

		_, err := f.svc.Get(context.Background(), "X")
		Expect(err).To(HaveOccurred())

		st, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.NotFound))
	})
})

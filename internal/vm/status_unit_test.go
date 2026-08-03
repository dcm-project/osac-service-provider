package vm_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/vm"
)

// TC-U-350..351 (REQ-VMSTATUS-010/020, AC-VMSTATUS-010/020): MapStatus is a
// pure function, tested directly against every REQ-VMSTATUS-020 precedence
// rule without needing the bufconn fixture. This is a separate 8-value
// vocabulary and a separate, condition-free implementation from Cluster's
// MapStatus (DD-081) — do not conflate the two.
var _ = Describe("MapStatus (Topic 4.6 Status Mapping)", func() {
	DescribeTable("maps each individual signal to its documented value (TC-U-350)",
		func(err error, status *publicv1.ComputeInstanceStatus, want v1alpha1.VMStatus) {
			Expect(vm.MapStatus(err, status)).To(Equal(want))
		},
		Entry("gRPC Unavailable", grpcstatus.Error(codes.Unavailable, "osac unreachable"), (*publicv1.ComputeInstanceStatus)(nil), v1alpha1.FAILED),
		Entry("gRPC DeadlineExceeded", grpcstatus.Error(codes.DeadlineExceeded, "timed out"), (*publicv1.ComputeInstanceStatus)(nil), v1alpha1.FAILED),
		Entry("gRPC NotFound", grpcstatus.Error(codes.NotFound, "no such compute instance"), (*publicv1.ComputeInstanceStatus)(nil), v1alpha1.DELETED),
		Entry("gRPC PermissionDenied (defensive default for any other error code)", grpcstatus.Error(codes.PermissionDenied, "denied"), (*publicv1.ComputeInstanceStatus)(nil), v1alpha1.FAILED),
		Entry("state=STARTING", nil, &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING}, v1alpha1.PROVISIONING),
		Entry("state=RUNNING", nil, &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING}, v1alpha1.RUNNING),
		Entry("state=FAILED", nil, &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED}, v1alpha1.FAILED),
		Entry("state=DELETING", nil, &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING}, v1alpha1.DELETING),
		Entry("state=STOPPING", nil, &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING}, v1alpha1.STOPPING),
		Entry("state=STOPPED", nil, &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED}, v1alpha1.STOPPED),
		Entry("state=PAUSED", nil, &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED}, v1alpha1.PAUSED),
		Entry("state=UNSPECIFIED", nil, &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED}, v1alpha1.FAILED),
	)

	// TC-U-351 (AC-VMSTATUS-020): a connectivity failure is never
	// conflated with a real NotFound — two separate calls, two distinct
	// values.
	It("never conflates a connectivity failure with a real NotFound (TC-U-351)", func() {
		unavailable := vm.MapStatus(grpcstatus.Error(codes.Unavailable, "osac unreachable"), nil)
		notFound := vm.MapStatus(grpcstatus.Error(codes.NotFound, "no such compute instance"), nil)

		Expect(unavailable).To(Equal(v1alpha1.FAILED))
		Expect(notFound).To(Equal(v1alpha1.DELETED))
	})
})

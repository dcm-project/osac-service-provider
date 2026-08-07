package cluster_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	"github.com/dcm-project/osac-service-provider/internal/cluster"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// TC-U-240..242 (REQ-STATUS-010/020, AC-STATUS-010/020/030): MapStatus is a
// pure function, tested directly against every REQ-STATUS-020 precedence
// rule without needing the bufconn fixture.
var _ = Describe("MapStatus (Topic 4.5 Status Mapping)", func() {
	degradedTrue := []*publicv1.ClusterCondition{
		{Type: publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_DEGRADED, Status: publicv1.ConditionStatus_CONDITION_STATUS_TRUE},
	}

	// TC-U-240: each precedence-rule input maps to its documented value.
	DescribeTable("maps each individual signal to its documented value (TC-U-240)",
		func(err error, status *publicv1.ClusterStatus, want v1alpha1.ClusterStatus) {
			Expect(cluster.MapStatus(err, status)).To(Equal(want))
		},
		Entry("gRPC Unavailable", grpcstatus.Error(codes.Unavailable, "osac unreachable"), (*publicv1.ClusterStatus)(nil), v1alpha1.ClusterStatusUNAVAILABLE),
		Entry("gRPC DeadlineExceeded", grpcstatus.Error(codes.DeadlineExceeded, "timed out"), (*publicv1.ClusterStatus)(nil), v1alpha1.ClusterStatusUNAVAILABLE),
		Entry("gRPC NotFound", grpcstatus.Error(codes.NotFound, "no such cluster"), (*publicv1.ClusterStatus)(nil), v1alpha1.ClusterStatusDELETED),
		Entry("gRPC PermissionDenied (defensive default for any other error code)", grpcstatus.Error(codes.PermissionDenied, "denied"), (*publicv1.ClusterStatus)(nil), v1alpha1.ClusterStatusFAILED),
		// UNSPECIFIED (proto3's zero-value) maps to PROGRESSING, not FAILED
		// (DD-129): it is the normal state for a freshly-created Cluster
		// before osac-operator's first reconcile pass, not a genuine
		// anomaly — see VM's identical, live-verified fix.
		Entry("state=UNSPECIFIED", nil, &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_UNSPECIFIED}, v1alpha1.ClusterStatusPROGRESSING),
		Entry("state=FAILED", nil, &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_FAILED}, v1alpha1.ClusterStatusFAILED),
		Entry("state=DELETING", nil, &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_DELETING}, v1alpha1.ClusterStatusDELETING),
		Entry("state=DELETE_FAILED", nil, &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_DELETE_FAILED}, v1alpha1.ClusterStatusFAILED),
		Entry("state=READY, no conditions", nil, &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_READY}, v1alpha1.ClusterStatusACTIVE),
		Entry("state=PROGRESSING", nil, &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_PROGRESSING}, v1alpha1.ClusterStatusPROGRESSING),
		Entry("DEGRADED condition TRUE + state=READY", nil, &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_READY, Conditions: degradedTrue}, v1alpha1.ClusterStatusDEGRADED),
	)

	// TC-U-241 (AC-STATUS-020): FAILED state takes precedence over a
	// simultaneous DEGRADED condition — proves ordering, not just that both
	// individually-tested cases pass in isolation.
	It("returns FAILED when state=FAILED and a DEGRADED condition are present simultaneously (TC-U-241)", func() {
		status := &publicv1.ClusterStatus{
			State:      publicv1.ClusterState_CLUSTER_STATE_FAILED,
			Conditions: degradedTrue,
		}
		Expect(cluster.MapStatus(nil, status)).To(Equal(v1alpha1.ClusterStatusFAILED))
	})

	// TC-U-242 (AC-STATUS-030): a connectivity failure is never conflated
	// with a real NotFound — two separate calls, two distinct values.
	It("never conflates a connectivity failure with a real NotFound (TC-U-242)", func() {
		unavailable := cluster.MapStatus(grpcstatus.Error(codes.Unavailable, "osac unreachable"), nil)
		notFound := cluster.MapStatus(grpcstatus.Error(codes.NotFound, "no such cluster"), nil)

		Expect(unavailable).To(Equal(v1alpha1.ClusterStatusUNAVAILABLE))
		Expect(notFound).To(Equal(v1alpha1.ClusterStatusDELETED))
	})
})

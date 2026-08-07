package cluster

import (
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// MapStatus translates OSAC's ClusterStatus, or the outcome of the gRPC
// call that produced it, into DCM's canonical 7-value Cluster status
// vocabulary, per REQ-STATUS-020's precedence order. When err is non-nil,
// status is ignored (and may be nil): the error's own gRPC code alone
// determines the result.
//
// The err branch is unreachable in this milestone's synchronous call sites
// — kept and tested for Milestone 5's async polling. See SC-M3-001/
// SC-M3-003 for why.
//
// CLUSTER_STATE_UNSPECIFIED (proto3's zero-value) maps to PROGRESSING, not
// the catch-all FAILED default (DD-129): it is the normal state for a
// freshly-created Cluster before osac-operator's first reconcile pass, not
// a genuine anomaly — see VM's identical fix (internal/vm/status.go) for
// the live evidence this was verified against.
func MapStatus(err error, status *publicv1.ClusterStatus) v1alpha1.ClusterStatus {
	if err != nil {
		switch grpcstatus.Code(err) {
		case codes.Unavailable, codes.DeadlineExceeded:
			return v1alpha1.ClusterStatusUNAVAILABLE
		case codes.NotFound:
			return v1alpha1.ClusterStatusDELETED
		default:
			return v1alpha1.ClusterStatusFAILED
		}
	}

	if status.GetState() == publicv1.ClusterState_CLUSTER_STATE_UNSPECIFIED {
		return v1alpha1.ClusterStatusPROGRESSING
	}

	switch status.GetState() {
	case publicv1.ClusterState_CLUSTER_STATE_FAILED:
		return v1alpha1.ClusterStatusFAILED
	case publicv1.ClusterState_CLUSTER_STATE_DELETING:
		return v1alpha1.ClusterStatusDELETING
	case publicv1.ClusterState_CLUSTER_STATE_DELETE_FAILED:
		return v1alpha1.ClusterStatusFAILED
	}

	for _, cond := range status.GetConditions() {
		if cond.GetType() == publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_DEGRADED &&
			cond.GetStatus() == publicv1.ConditionStatus_CONDITION_STATUS_TRUE {
			return v1alpha1.ClusterStatusDEGRADED
		}
	}

	switch status.GetState() {
	case publicv1.ClusterState_CLUSTER_STATE_READY:
		return v1alpha1.ClusterStatusACTIVE
	case publicv1.ClusterState_CLUSTER_STATE_PROGRESSING:
		return v1alpha1.ClusterStatusPROGRESSING
	default:
		return v1alpha1.ClusterStatusFAILED
	}
}

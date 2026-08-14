package vm

import (
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// MapStatus translates OSAC's ComputeInstanceState, or the outcome of the
// gRPC call that produced it, into DCM's canonical 8-value VM status
// vocabulary (DD-121), per REQ-VMSTATUS-020's precedence order. When err is
// non-nil, status is ignored (and may be nil): the error's own gRPC code
// alone determines the result.
//
// Unlike Cluster's MapStatus, there is no UNAVAILABLE or DEGRADED value in
// this 8-value vocabulary (no conditions are consulted) — any error other
// than NotFound (including Unavailable/DeadlineExceeded) maps to FAILED
// (AC-VMSTATUS-020). The err branch is unreachable in this milestone's
// synchronous call sites — kept and tested for Milestone 5's async
// polling. See SC-M4-003 for why.
//
// COMPUTE_INSTANCE_STATE_UNSPECIFIED (proto3's zero-value) maps to
// PROVISIONING, not the catch-all FAILED default (DD-129): verified live
// against a real fulfillment-service/osac-operator, it is the normal state
// for every ComputeInstance for several seconds between creation and
// osac-operator's first reconcile pass — not a genuine anomaly. Treating it
// as FAILED produced a false failure on every single VM creation.
//
// The generated constant names became VMStatus-prefixed (e.g.
// v1alpha1.VMStatusRUNNING) once the Milestone 3 Cluster CRUD spec merged
// into this one: oapi-codegen only prefixes on a genuine cross-schema
// collision, and ClusterStatus/VMStatus share several raw values
// (FAILED/DELETING/DELETED) — expected, not a regression.
func MapStatus(err error, status *publicv1.ComputeInstanceStatus) v1alpha1.VMStatus {
	if err != nil {
		if grpcstatus.Code(err) == codes.NotFound {
			return v1alpha1.VMStatusDELETED
		}
		return v1alpha1.VMStatusFAILED
	}

	switch status.GetState() {
	case publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_UNSPECIFIED:
		return v1alpha1.VMStatusPROVISIONING
	case publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING:
		return v1alpha1.VMStatusPROVISIONING
	case publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING:
		return v1alpha1.VMStatusRUNNING
	case publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_FAILED:
		return v1alpha1.VMStatusFAILED
	case publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING:
		return v1alpha1.VMStatusDELETING
	case publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING:
		return v1alpha1.VMStatusSTOPPING
	case publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED:
		return v1alpha1.VMStatusSTOPPED
	case publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED:
		return v1alpha1.VMStatusPAUSED
	default:
		return v1alpha1.VMStatusFAILED
	}
}

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
// (AC-VMSTATUS-020). No current call site invokes MapStatus with a non-nil
// err — Get/Create/Delete all intercept NotFound/AlreadyExists before
// reaching here — kept and independently tested (SC-M4-00x) for a future
// async polling caller.
//
// The generated constant names are currently bare (v1alpha1.RUNNING, not
// v1alpha1.VMStatusRUNNING) because oapi-codegen only prefixes enum
// constants with their type name on a genuine cross-schema collision, and
// today nothing else in the spec collides with VMStatus's 8 raw values —
// see ClusterStatus's own collision with ErrorType's UNAVAILABLE for a
// contrast. This SP's two branches (M3/M4) share several raw value
// strings (FAILED, DELETING, DELETED) despite having entirely separate
// vocabularies, so a future merge of both onto the same spec will very
// likely introduce that collision and force both enums to become
// prefixed — expected, not a regression, and every call site here already
// goes through the generated identifiers (not hardcoded strings) so it
// will pick up the rename automatically.
func MapStatus(err error, status *publicv1.ComputeInstanceStatus) v1alpha1.VMStatus {
	if err != nil {
		if grpcstatus.Code(err) == codes.NotFound {
			return v1alpha1.DELETED
		}
		return v1alpha1.FAILED
	}

	switch status.GetState() {
	case publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING:
		return v1alpha1.PROVISIONING
	case publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING:
		return v1alpha1.RUNNING
	case publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_DELETING:
		return v1alpha1.DELETING
	case publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPING:
		return v1alpha1.STOPPING
	case publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STOPPED:
		return v1alpha1.STOPPED
	case publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_PAUSED:
		return v1alpha1.PAUSED
	default:
		return v1alpha1.FAILED
	}
}

package statuspoll

import (
	"fmt"
	"strings"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// clusterConditionTypeFor returns the ClusterConditionType consulted for
// status's message, per REQ-POLL-060. DELETING, UNAVAILABLE, and DELETED
// have no corresponding condition type in ClusterConditionType and always
// fall back to the synthesized default (ok is false).
func clusterConditionTypeFor(status v1alpha1.ClusterStatus) (publicv1.ClusterConditionType, bool) {
	switch status {
	case v1alpha1.ClusterStatusACTIVE:
		return publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY, true
	case v1alpha1.ClusterStatusPROGRESSING:
		return publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING, true
	case v1alpha1.ClusterStatusDEGRADED:
		return publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_DEGRADED, true
	case v1alpha1.ClusterStatusFAILED:
		return publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_FAILED, true
	default:
		return publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_UNSPECIFIED, false
	}
}

// clusterMessage derives a Cluster status update's human-readable message
// (REQ-POLL-060): the matching condition's message, falling back to its
// reason, falling back to a synthesized default.
func clusterMessage(status v1alpha1.ClusterStatus, conditions []*publicv1.ClusterCondition) string {
	condType, ok := clusterConditionTypeFor(status)
	if ok {
		for _, c := range conditions {
			if c.GetType() != condType {
				continue
			}
			if msg := c.GetMessage(); msg != "" {
				return msg
			}
			if reason := c.GetReason(); reason != "" {
				return reason
			}
			break
		}
	}
	return fmt.Sprintf("cluster is %s", strings.ToLower(string(status)))
}

// vmMessage derives a VM status update's human-readable message
// (REQ-POLL-070): a synthesized default per status, opportunistically
// overridden by a TRUE RESTART_FAILED condition's message/reason regardless
// of the primary status — VM's MapStatus otherwise never consults
// conditions (asymmetric with Cluster by design, not a gap).
func vmMessage(status v1alpha1.VMStatus, conditions []*publicv1.ComputeInstanceCondition) string {
	for _, c := range conditions {
		if c.GetType() != publicv1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_RESTART_FAILED ||
			c.GetStatus() != publicv1.ConditionStatus_CONDITION_STATUS_TRUE {
			continue
		}
		if msg := c.GetMessage(); msg != "" {
			return msg
		}
		if reason := c.GetReason(); reason != "" {
			return reason
		}
	}
	return fmt.Sprintf("vm is %s", strings.ToLower(string(status)))
}

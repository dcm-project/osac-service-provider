package statuspoll

import (
	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func clusterCond(t publicv1.ClusterConditionType, message, reason string) *publicv1.ClusterCondition {
	c := &publicv1.ClusterCondition{Type: t, Status: publicv1.ConditionStatus_CONDITION_STATUS_TRUE}
	if message != "" {
		c.Message = &message
	}
	if reason != "" {
		c.Reason = &reason
	}
	return c
}

func vmCond(t publicv1.ComputeInstanceConditionType, status publicv1.ConditionStatus, message, reason string) *publicv1.ComputeInstanceCondition {
	c := &publicv1.ComputeInstanceCondition{Type: t, Status: status}
	if message != "" {
		c.Message = &message
	}
	if reason != "" {
		c.Reason = &reason
	}
	return c
}

var _ = Describe("Cluster message derivation", func() {
	// TC-U-460: message pulls from the matching condition's message, falls
	// back to reason, falls back to a synthesized default.
	DescribeTable("derives the message per status/condition combination (TC-U-460)",
		func(status v1alpha1.ClusterStatus, conditions []*publicv1.ClusterCondition, want string) {
			Expect(clusterMessage(status, conditions)).To(Equal(want))
		},
		Entry("message present on the matching condition",
			v1alpha1.ClusterStatusACTIVE,
			[]*publicv1.ClusterCondition{
				clusterCond(publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY, "control plane healthy", ""),
			},
			"control plane healthy",
		),
		Entry("message empty, falls back to reason",
			v1alpha1.ClusterStatusACTIVE,
			[]*publicv1.ClusterCondition{
				clusterCond(publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY, "", "AllNodesReady"),
			},
			"AllNodesReady",
		),
		Entry("no matching condition, synthesized default",
			v1alpha1.ClusterStatusACTIVE,
			nil,
			"cluster is active",
		),
		Entry("DELETING always uses the synthesized default regardless of conditions present",
			v1alpha1.ClusterStatusDELETING,
			[]*publicv1.ClusterCondition{
				clusterCond(publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY, "control plane healthy", ""),
			},
			"cluster is deleting",
		),
		Entry("UNAVAILABLE always uses the synthesized default regardless of conditions present",
			v1alpha1.ClusterStatusUNAVAILABLE,
			[]*publicv1.ClusterCondition{
				clusterCond(publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY, "control plane healthy", ""),
			},
			"cluster is unavailable",
		),
		Entry("DELETED always uses the synthesized default regardless of conditions present",
			v1alpha1.ClusterStatusDELETED,
			[]*publicv1.ClusterCondition{
				clusterCond(publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY, "control plane healthy", ""),
			},
			"cluster is deleted",
		),
		Entry("matching condition present but both message and reason are empty, synthesized default",
			v1alpha1.ClusterStatusACTIVE,
			[]*publicv1.ClusterCondition{
				clusterCond(publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY, "", ""),
			},
			"cluster is active",
		),
	)

	// TC-U-461: each non-error status maps to its own condition type, and
	// only that type is consulted, ignoring a differently-typed condition
	// present in the same list.
	DescribeTable("maps each non-error status to its own condition type, ignoring differently-typed ones (TC-U-461)",
		func(status v1alpha1.ClusterStatus, matchingType publicv1.ClusterConditionType) {
			otherType := publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY
			if matchingType == otherType {
				otherType = publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING
			}
			conditions := []*publicv1.ClusterCondition{
				clusterCond(otherType, "wrong condition", ""),
				clusterCond(matchingType, "right condition", ""),
			}
			Expect(clusterMessage(status, conditions)).To(Equal("right condition"))
		},
		Entry("PROGRESSING -> CLUSTER_CONDITION_TYPE_PROGRESSING", v1alpha1.ClusterStatusPROGRESSING, publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_PROGRESSING),
		Entry("DEGRADED -> CLUSTER_CONDITION_TYPE_DEGRADED", v1alpha1.ClusterStatusDEGRADED, publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_DEGRADED),
		Entry("FAILED -> CLUSTER_CONDITION_TYPE_FAILED", v1alpha1.ClusterStatusFAILED, publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_FAILED),
	)
})

var _ = Describe("VM message derivation", func() {
	// TC-U-462: a synthesized default per status when no RESTART_FAILED
	// condition is present — each status gets its own distinct string.
	DescribeTable("uses a distinct synthesized default per status with no conditions present (TC-U-462)",
		func(status v1alpha1.VMStatus, want string) {
			Expect(vmMessage(status, nil)).To(Equal(want))
		},
		Entry("PROVISIONING", v1alpha1.VMStatusPROVISIONING, "vm is provisioning"),
		Entry("RUNNING", v1alpha1.VMStatusRUNNING, "vm is running"),
		Entry("STOPPING", v1alpha1.VMStatusSTOPPING, "vm is stopping"),
		Entry("STOPPED", v1alpha1.VMStatusSTOPPED, "vm is stopped"),
		Entry("PAUSED", v1alpha1.VMStatusPAUSED, "vm is paused"),
		Entry("DELETING", v1alpha1.VMStatusDELETING, "vm is deleting"),
		Entry("DELETED", v1alpha1.VMStatusDELETED, "vm is deleted"),
		Entry("FAILED", v1alpha1.VMStatusFAILED, "vm is failed"),
	)

	// TC-U-463: a TRUE RESTART_FAILED condition's message is surfaced
	// regardless of primary status; a FALSE one must not be.
	It("surfaces a TRUE RESTART_FAILED condition's message regardless of primary status (TC-U-463)", func() {
		conditions := []*publicv1.ComputeInstanceCondition{
			vmCond(publicv1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_RESTART_FAILED, publicv1.ConditionStatus_CONDITION_STATUS_TRUE, "ssh key rotation failed", ""),
		}
		Expect(vmMessage(v1alpha1.VMStatusRUNNING, conditions)).To(ContainSubstring("ssh key rotation failed"))
	})

	It("ignores a condition of a different type entirely (TC-U-463)", func() {
		conditions := []*publicv1.ComputeInstanceCondition{
			vmCond(publicv1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_READY, publicv1.ConditionStatus_CONDITION_STATUS_TRUE, "ready message", ""),
		}
		Expect(vmMessage(v1alpha1.VMStatusRUNNING, conditions)).To(Equal("vm is running"))
	})

	It("does not surface a FALSE RESTART_FAILED condition (TC-U-463)", func() {
		conditions := []*publicv1.ComputeInstanceCondition{
			vmCond(publicv1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_RESTART_FAILED, publicv1.ConditionStatus_CONDITION_STATUS_FALSE, "ssh key rotation failed", ""),
		}
		Expect(vmMessage(v1alpha1.VMStatusRUNNING, conditions)).To(Equal("vm is running"))
	})

	It("falls back to the RESTART_FAILED condition's reason when its message is empty (TC-U-463)", func() {
		conditions := []*publicv1.ComputeInstanceCondition{
			vmCond(publicv1.ComputeInstanceConditionType_COMPUTE_INSTANCE_CONDITION_TYPE_RESTART_FAILED, publicv1.ConditionStatus_CONDITION_STATUS_TRUE, "", "SSHKeyRotationFailed"),
		}
		Expect(vmMessage(v1alpha1.VMStatusRUNNING, conditions)).To(ContainSubstring("SSHKeyRotationFailed"))
	})
})

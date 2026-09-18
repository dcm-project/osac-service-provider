package cluster

import (
	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/util"
)

const (
	labelManagedBy   = "dcm.io/managed-by"
	labelInstanceID  = "dcm.io/instance-id"
	labelServiceType = "dcm.io/service-type"

	managedByValue   = "dcm"
	serviceTypeValue = "cluster"
)

// ownershipLabels returns the three dcm.io/* labels this SP always sets on
// every Create call (REQ-CREATE-030).
func ownershipLabels(id string) map[string]string {
	return map[string]string{
		labelManagedBy:   managedByValue,
		labelInstanceID:  id,
		labelServiceType: serviceTypeValue,
	}
}

// mergeLabels merges the caller-supplied labels (if any) with the ownership
// labels, with ownership labels taking precedence on key collision.
func mergeLabels(caller *map[string]string, id string) map[string]string {
	merged := make(map[string]string)
	if caller != nil {
		for k, v := range *caller {
			merged[k] = v
		}
	}
	for k, v := range ownershipLabels(id) {
		merged[k] = v
	}
	return merged
}

// toOSACCluster translates a Create request's id/spec into the OSAC
// Cluster object sent to Clusters/Create, per the M3 spec's Field Mapping
// table (§4.1). nodeSetKey is resolved by the caller via
// ClusterTemplates/Get (REQ-CREATE-080/090) — never derived from
// templateID here. Node sizing hints (cpu/memory/storage) are deliberately
// never read here (REQ-CREATE-070) — host_type is fixed by the template.
//
// provider_hints.osac.base_domain has no corresponding OSAC field —
// verified directly against the vendored cluster_type.proto's
// ClusterNetwork message (pod_cidr/service_cidr only) — so it is accepted
// but not translated, the same "informational, no OSAC field" treatment as
// the worker cpu/memory/storage hints.
func (s *Service) toOSACCluster(
	id string,
	spec v1alpha1.ClusterSpec,
	nodeSetKey string,
	versionRef *publicv1.ClusterVersionReference,
) *publicv1.Cluster {
	templateID := spec.ProviderHints.Osac.TemplateId

	osacSpec := &publicv1.ClusterSpec{
		Template: &publicv1.ClusterTemplateReference{Id: templateID},
		NodeSets: map[string]*publicv1.ClusterNodeSet{
			nodeSetKey: {Size: int32(spec.Nodes.Worker.Count)}, //nolint:gosec // range-checked by validateCreateRequest before Create ever reaches here
		},
		Version:      versionRef,
		PullSecret:   spec.ProviderHints.Osac.PullSecret,
		SshPublicKey: spec.ProviderHints.Osac.SshKey,
	}

	return &publicv1.Cluster{
		Id: id,
		Metadata: &publicv1.Metadata{
			Name:   spec.Metadata.Name,
			Labels: mergeLabels(spec.Metadata.Labels, id),
		},
		Spec: osacSpec,
	}
}

// toAPICluster translates an OSAC Cluster object into the SP's REST
// response schema. version is non-nil only on a fresh Create response
// (SC-M3-002); Get/List pass nil, since the request version is only echoed
// on a fresh Create response.
func toAPICluster(osacCluster *publicv1.Cluster, version *string) v1alpha1.Cluster {
	return v1alpha1.Cluster{
		Id:       util.Ptr(osacCluster.GetId()),
		Path:     util.Ptr("clusters/" + osacCluster.GetId()),
		Status:   util.Ptr(MapStatus(nil, osacCluster.GetStatus())),
		NodeSets: toAPINodeSets(osacCluster.GetStatus().GetNodeSets()),
		Version:  version,
	}
}

// toAPINodeSets echoes OSAC's status.node_sets map directly — no
// ready/total computation, since OSAC's ClusterNodeSet has no such field
// (SC-M3-002).
func toAPINodeSets(raw map[string]*publicv1.ClusterNodeSet) *map[string]v1alpha1.ClusterNodeSet {
	if len(raw) == 0 {
		return nil
	}
	m := make(map[string]v1alpha1.ClusterNodeSet, len(raw))
	for k, v := range raw {
		var hostType *string
		if ref := v.GetHostType(); ref != nil {
			value := ref.GetId()
			if value == "" {
				value = ref.GetName()
			}
			if value != "" {
				hostType = util.Ptr(value)
			}
		}
		m[k] = v1alpha1.ClusterNodeSet{
			HostType: hostType,
			Size:     util.Ptr(int(v.GetSize())),
		}
	}
	return &m
}

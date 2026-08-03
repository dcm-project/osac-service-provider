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

// releaseImageByVersion is a hardcoded placeholder table (REQ-CREATE-025)
// translating DCM's Kubernetes minor version to an OSAC release_image —
// the same Kubernetes versions as registration.go's
// kubernetesSupportedVersions (SC-001), paired with the OpenShift 4.16-4.20
// releases that variable's own comment documents as backing them. The full
// compatibility matrix is Milestone 6.
var releaseImageByVersion = map[string]string{
	"1.29": "quay.io/openshift-release-dev/ocp-release:4.16.0-multi",
	"1.30": "quay.io/openshift-release-dev/ocp-release:4.17.0-multi",
	"1.31": "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
	"1.32": "quay.io/openshift-release-dev/ocp-release:4.19.0-multi",
	"1.33": "quay.io/openshift-release-dev/ocp-release:4.20.0-multi",
}

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

// releaseImage resolves spec's release_image: an explicit
// provider_hints.osac.release_image override always wins (REQ-CREATE-025);
// otherwise the placeholder table is consulted by spec.version. Returns nil
// if neither yields a value, leaving OSAC's template default release image
// in effect.
func releaseImage(spec v1alpha1.ClusterSpec) *string {
	if override := spec.ProviderHints.Osac.ReleaseImage; override != nil && *override != "" {
		return override
	}
	if img, ok := releaseImageByVersion[spec.Version]; ok {
		return util.Ptr(img)
	}
	return nil
}

// toOSACCluster translates a Create request's id/spec into the OSAC
// Cluster object sent to Clusters/Create, per the M3 spec's Field Mapping
// table (§4.1). Node sizing hints (cpu/memory/storage) are deliberately
// never read here (REQ-CREATE-070) — host_type is fixed by the template.
//
// provider_hints.osac.base_domain has no corresponding OSAC field —
// verified directly against the vendored cluster_type.proto's
// ClusterNetwork message (pod_cidr/service_cidr only) — so it is accepted
// but not translated, the same "informational, no OSAC field" treatment as
// the worker cpu/memory/storage hints.
func toOSACCluster(id string, spec v1alpha1.ClusterSpec) *publicv1.Cluster {
	templateID := spec.ProviderHints.Osac.TemplateId

	osacSpec := &publicv1.ClusterSpec{
		Template: templateID,
		NodeSets: map[string]*publicv1.ClusterNodeSet{
			templateID: {Size: int32(spec.Nodes.Worker.Count)},
		},
		ReleaseImage: releaseImage(spec),
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
// (SC-M3-002); Get/List pass nil, since no reverse release_image->version
// mapping exists.
func toAPICluster(osacCluster *publicv1.Cluster, version *string) v1alpha1.Cluster {
	return v1alpha1.Cluster{
		Id:       util.Ptr(osacCluster.GetId()),
		Path:     util.Ptr("clusters/" + osacCluster.GetId()),
		Status:   MapStatus(nil, osacCluster.GetStatus()),
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
		m[k] = v1alpha1.ClusterNodeSet{
			HostType: util.Ptr(v.GetHostType()),
			Size:     util.Ptr(int(v.GetSize())),
		}
	}
	return &m
}

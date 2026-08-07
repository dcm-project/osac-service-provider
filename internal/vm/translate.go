package vm

import (
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/util"
)

const (
	labelManagedBy   = "dcm.io/managed-by"
	labelInstanceID  = "dcm.io/instance-id"
	labelServiceType = "dcm.io/service-type"

	managedByValue   = "dcm"
	serviceTypeValue = "vm"

	// imageSourceType is a fixed constant (SC-M4-002):
	// ComputeInstanceImage.source_type has no enum or server-side
	// validation, so there is no "correct" value to derive from DCM's
	// spec.guest_os.type — this is a best-effort, non-breaking choice.
	imageSourceType = "catalog"
)

// ownershipLabels returns the three dcm.io/* labels this SP always sets on
// every VM Create call (REQ-VMCREATE-050).
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

// toOSACComputeInstance translates a Create request's id/spec into the
// OSAC ComputeInstance object sent to ComputeInstances/Create, per the M4
// spec's Field Mapping table (§4.1). spec.vcpu/spec.memory are
// deliberately never read here (DD-122) — they're informational only.
// network_attachments is left unset — Service.Create fills it in after
// resolving the default subnet (§4.5), since that step can itself fail and
// must not have already dispatched to OSAC.
//
// Returns a codes.InvalidArgument error (REQ-VMCREATE-060) if the disks
// don't contain exactly one disk named "boot", or if any disk's capacity
// fails to parse (REQ-VMCREATE-040) — both are translation-inherent
// validations, not simple field-presence checks, so they live here rather
// than in internal/handlers/vm's request-shape validation.
func toOSACComputeInstance(id string, spec v1alpha1.VMSpec) (*publicv1.ComputeInstance, error) {
	bootDisk, additionalDisks, err := splitDisks(spec.Storage.Disks)
	if err != nil {
		return nil, err
	}

	osacSpec := &publicv1.ComputeInstanceSpec{
		Template:        &publicv1.ComputeInstanceTemplateReference{Name: spec.ProviderHints.Osac.TemplateId},
		InstanceType:    &publicv1.InstanceTypeReference{Name: spec.ProviderHints.Osac.InstanceType},
		Image:           &publicv1.ComputeInstanceImage{SourceType: imageSourceType, SourceRef: spec.GuestOs.Type},
		BootDisk:        bootDisk,
		AdditionalDisks: additionalDisks,
	}
	if spec.Access != nil {
		osacSpec.SshPublicKey = spec.Access.SshPublicKey
	}
	if spec.ProviderHints.Osac.Windows != nil {
		osacSpec.IsWindows = spec.ProviderHints.Osac.Windows
	}

	return &publicv1.ComputeInstance{
		Id: id,
		Metadata: &publicv1.Metadata{
			Name:   spec.Metadata.Name,
			Labels: mergeLabels(spec.Metadata.Labels, id),
		},
		Spec: osacSpec,
	}, nil
}

// toAPIVM translates an OSAC ComputeInstance object into the SP's REST
// response schema, echoing internal/external IP addresses exactly
// (REQ-VMGET-030/REQ-VMLIST-030) regardless of caller (Create/Get/List all
// share this).
func toAPIVM(osacObj *publicv1.ComputeInstance) v1alpha1.VirtualMachine {
	status := osacObj.GetStatus()
	return v1alpha1.VirtualMachine{
		Id:                util.Ptr(osacObj.GetId()),
		Path:              util.Ptr("vms/" + osacObj.GetId()),
		Status:            MapStatus(nil, status),
		InternalIpAddress: util.Ptr(status.GetInternalIpAddress()),
		ExternalIpAddress: util.Ptr(status.GetExternalIpAddress()),
	}
}

// invalidArgument is a small helper matching internal/handlers/cluster's
// convention of representing request-validation failures as a synthetic
// gRPC InvalidArgument error, mapped to 400 by the shared error-mapping
// topic (§4.7) exactly like any OSAC-originated error.
func invalidArgument(msg string) error {
	return grpcstatus.Error(codes.InvalidArgument, msg)
}

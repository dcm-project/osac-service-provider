package vm

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/util"
)

const (
	// defaultNetworkServiceType is REQ-VMNET-020/030's dcm.io/service-type
	// value for the shared default VirtualNetwork/Subnet — distinct from
	// ownershipLabels' "vm" value used for per-VM ComputeInstance
	// resources, since this network is shared, not owned by any one VM
	// (SC-M4-001).
	defaultNetworkServiceType = "vm-default-network"

	defaultVNetCIDR   = "10.200.0.0/16"
	defaultSubnetCIDR = "10.200.1.0/24"

	ownerReferenceAnnotation = "osac.openshift.io/owner-reference"
)

// defaultNetworkLabels returns the two ownership labels REQ-VMNET-020/030
// document for the shared default network resources. No dcm.io/instance-id
// label is set here (see ownershipLabels for the per-VM equivalent).
func defaultNetworkLabels() map[string]string {
	return map[string]string{
		labelManagedBy:   managedByValue,
		labelServiceType: defaultNetworkServiceType,
	}
}

// resolveDefaultSubnet implements the Default Network Provisioning topic
// (§4.5, DD-124): reuse an existing default subnet if one exists
// (REQ-VMNET-010), otherwise provision a new VirtualNetwork/Subnet pair
// and wait for both to become READY (REQ-VMNET-020/030/040) before
// returning the resolved subnet id.
func (s *Service) resolveDefaultSubnet(ctx context.Context) (string, error) {
	listResp, err := s.subnets.List(ctx, &publicv1.SubnetsListRequest{Filter: util.Ptr(ownershipFilter)})
	if err != nil {
		return "", err
	}
	if items := listResp.GetItems(); len(items) > 0 {
		return items[0].GetId(), nil
	}

	vnetID, err := s.provisionDefaultVirtualNetwork(ctx)
	if err != nil {
		return "", err
	}
	return s.provisionDefaultSubnet(ctx, vnetID)
}

// provisionDefaultVirtualNetwork implements REQ-VMNET-020: create the
// shared default VirtualNetwork and poll until READY.
func (s *Service) provisionDefaultVirtualNetwork(ctx context.Context) (string, error) {
	resp, err := s.virtualNetworks.Create(ctx, &publicv1.VirtualNetworksCreateRequest{
		Object: &publicv1.VirtualNetwork{
			Metadata: &publicv1.Metadata{Labels: defaultNetworkLabels()},
			Spec: &publicv1.VirtualNetworkSpec{
				Ipv4Cidr:     util.Ptr(defaultVNetCIDR),
				Capabilities: &publicv1.VirtualNetworkCapabilities{EnableIpv4: true},
			},
		},
	})
	if err != nil {
		return "", err
	}

	vnetID := resp.GetObject().GetId()
	if err := s.pollUntilReady(ctx, func(ctx context.Context) (bool, error) {
		getResp, err := s.virtualNetworks.Get(ctx, &publicv1.VirtualNetworksGetRequest{Id: vnetID})
		if err != nil {
			return false, err
		}
		return getResp.GetObject().GetStatus().GetState() == publicv1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY, nil
	}); err != nil {
		return "", err
	}
	return vnetID, nil
}

// provisionDefaultSubnet implements REQ-VMNET-030: create the Subnet
// attached to vnetID (with the owner-reference annotation) and poll until
// READY.
func (s *Service) provisionDefaultSubnet(ctx context.Context, vnetID string) (string, error) {
	resp, err := s.subnets.Create(ctx, &publicv1.SubnetsCreateRequest{
		Object: &publicv1.Subnet{
			Metadata: &publicv1.Metadata{
				Labels:      defaultNetworkLabels(),
				Annotations: map[string]string{ownerReferenceAnnotation: vnetID},
			},
			Spec: &publicv1.SubnetSpec{
				VirtualNetwork: vnetID,
				Ipv4Cidr:       util.Ptr(defaultSubnetCIDR),
			},
		},
	})
	if err != nil {
		return "", err
	}

	subnetID := resp.GetObject().GetId()
	if err := s.pollUntilReady(ctx, func(ctx context.Context) (bool, error) {
		getResp, err := s.subnets.Get(ctx, &publicv1.SubnetsGetRequest{Id: subnetID})
		if err != nil {
			return false, err
		}
		return getResp.GetObject().GetStatus().GetState() == publicv1.SubnetState_SUBNET_STATE_READY, nil
	}); err != nil {
		return "", err
	}
	return subnetID, nil
}

// pollUntilReady implements REQ-VMNET-040's polling contract: call
// isReady repeatedly at s.networkPollInterval until it reports true, the
// request's context is done, or s.networkPollTimeout elapses — whichever
// comes first. A timeout is returned as a gRPC DeadlineExceeded error so
// the shared error-mapping topic (§4.7) maps it to 502, exactly like any
// OSAC-originated Unavailable/DeadlineExceeded error.
func (s *Service) pollUntilReady(ctx context.Context, isReady func(ctx context.Context) (bool, error)) error {
	deadline := time.Now().Add(s.networkPollTimeout)
	for {
		ready, err := isReady(ctx)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		if !time.Now().Before(deadline) {
			return grpcstatus.Error(codes.DeadlineExceeded, fmt.Sprintf("timed out after %s waiting for network resource to become ready", s.networkPollTimeout))
		}

		select {
		case <-ctx.Done():
			return grpcstatus.FromContextError(ctx.Err()).Err()
		case <-time.After(s.networkPollInterval):
		}
	}
}

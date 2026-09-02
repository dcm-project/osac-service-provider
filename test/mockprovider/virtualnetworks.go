package mockprovider

import (
	"context"

	"github.com/google/uuid"

	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// VirtualNetworksServer is a real, in-memory fake of
// osac.public.v1.VirtualNetworks (REQ-MOCK-010). Unlike
// Clusters/ComputeInstances, Create always assigns a fresh,
// server-generated id (REQ-MOCK-021) — real OSAC generates VirtualNetwork
// IDs server-side too; osac-sp never supplies one on create (M4 spec §4.5,
// Default Network Provisioning). Update is left on the embedded
// UnimplementedVirtualNetworksServer default (gRPC UNIMPLEMENTED) —
// osac-sp never calls it (see spec §1).
type VirtualNetworksServer struct {
	publicv1.UnimplementedVirtualNetworksServer

	store *resourceStore[*publicv1.VirtualNetwork]
}

// NewVirtualNetworksServer returns an empty VirtualNetworksServer ready to
// register on a grpc.Server.
func NewVirtualNetworksServer() *VirtualNetworksServer {
	return &VirtualNetworksServer{store: newResourceStore[*publicv1.VirtualNetwork]()}
}

func (s *VirtualNetworksServer) Create(_ context.Context, req *publicv1.VirtualNetworksCreateRequest) (*publicv1.VirtualNetworksCreateResponse, error) {
	obj := req.GetObject()
	obj.Id = uuid.NewString()
	obj.Status = &publicv1.VirtualNetworkStatus{State: publicv1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY}

	s.store.insert(obj.GetId(), obj)
	return &publicv1.VirtualNetworksCreateResponse{Object: obj}, nil
}

func (s *VirtualNetworksServer) Get(_ context.Context, req *publicv1.VirtualNetworksGetRequest) (*publicv1.VirtualNetworksGetResponse, error) {
	obj, err := s.store.get(req.GetId())
	if err != nil {
		return nil, err
	}
	return &publicv1.VirtualNetworksGetResponse{Object: obj}, nil
}

func (s *VirtualNetworksServer) List(_ context.Context, req *publicv1.VirtualNetworksListRequest) (*publicv1.VirtualNetworksListResponse, error) {
	items, total := s.store.list(int(req.GetOffset()), int(req.GetLimit()))
	return &publicv1.VirtualNetworksListResponse{Size: int32(len(items)), Total: int32(total), Items: items}, nil //nolint:gosec // in-memory test store, item count bounded by what the test itself creates
}

func (s *VirtualNetworksServer) Delete(_ context.Context, req *publicv1.VirtualNetworksDeleteRequest) (*publicv1.VirtualNetworksDeleteResponse, error) {
	if err := s.store.delete(req.GetId()); err != nil {
		return nil, err
	}
	return &publicv1.VirtualNetworksDeleteResponse{}, nil
}

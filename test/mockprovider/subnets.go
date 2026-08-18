package mockprovider

import (
	"context"

	"github.com/google/uuid"

	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// SubnetsServer is a real, in-memory fake of osac.public.v1.Subnets
// (REQ-MOCK-010). Unlike Clusters/ComputeInstances, Create always assigns
// a fresh, server-generated id (REQ-MOCK-021) — real OSAC generates Subnet
// IDs server-side too; osac-sp never supplies one on create (M4 spec §4.5,
// Default Network Provisioning). Update is left on the embedded
// UnimplementedSubnetsServer default (gRPC UNIMPLEMENTED) — osac-sp never
// calls it (see spec §1).
type SubnetsServer struct {
	publicv1.UnimplementedSubnetsServer

	store *resourceStore[*publicv1.Subnet]
}

// NewSubnetsServer returns an empty SubnetsServer ready to register on a
// grpc.Server.
func NewSubnetsServer() *SubnetsServer {
	return &SubnetsServer{store: newResourceStore[*publicv1.Subnet]()}
}

func (s *SubnetsServer) Create(_ context.Context, req *publicv1.SubnetsCreateRequest) (*publicv1.SubnetsCreateResponse, error) {
	obj := req.GetObject()
	obj.Id = uuid.NewString()
	obj.Status = &publicv1.SubnetStatus{State: publicv1.SubnetState_SUBNET_STATE_READY}

	s.store.insert(obj.GetId(), obj)
	return &publicv1.SubnetsCreateResponse{Object: obj}, nil
}

func (s *SubnetsServer) Get(_ context.Context, req *publicv1.SubnetsGetRequest) (*publicv1.SubnetsGetResponse, error) {
	obj, err := s.store.get(req.GetId())
	if err != nil {
		return nil, err
	}
	return &publicv1.SubnetsGetResponse{Object: obj}, nil
}

func (s *SubnetsServer) List(_ context.Context, req *publicv1.SubnetsListRequest) (*publicv1.SubnetsListResponse, error) {
	items, total := s.store.list(int(req.GetOffset()), int(req.GetLimit()))
	return &publicv1.SubnetsListResponse{Size: int32(len(items)), Total: int32(total), Items: items}, nil
}

func (s *SubnetsServer) Delete(_ context.Context, req *publicv1.SubnetsDeleteRequest) (*publicv1.SubnetsDeleteResponse, error) {
	if err := s.store.delete(req.GetId()); err != nil {
		return nil, err
	}
	return &publicv1.SubnetsDeleteResponse{}, nil
}

package mockprovider

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// ComputeInstancesServer is a real, in-memory fake of
// osac.public.v1.ComputeInstances (REQ-MOCK-010). Create requires and uses
// the caller-supplied object.id (REQ-MOCK-020), matching how osac-sp
// itself sets ComputeInstance.id for idempotent create-retry (M4
// REQ-VMCREATE-070). Update is left on the embedded
// UnimplementedComputeInstancesServer default (gRPC UNIMPLEMENTED) —
// osac-sp never calls it (see spec §1).
type ComputeInstancesServer struct {
	publicv1.UnimplementedComputeInstancesServer

	store *resourceStore[*publicv1.ComputeInstance]
}

// NewComputeInstancesServer returns an empty ComputeInstancesServer ready
// to register on a grpc.Server.
func NewComputeInstancesServer() *ComputeInstancesServer {
	return &ComputeInstancesServer{store: newResourceStore[*publicv1.ComputeInstance]()}
}

func (s *ComputeInstancesServer) Create(_ context.Context, req *publicv1.ComputeInstancesCreateRequest) (*publicv1.ComputeInstancesCreateResponse, error) {
	obj := req.GetObject()
	if obj.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "object.id is required")
	}
	obj.Status = &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING}

	created, err := s.store.create(obj.GetId(), obj)
	if err != nil {
		return nil, err
	}
	return &publicv1.ComputeInstancesCreateResponse{Object: created}, nil
}

func (s *ComputeInstancesServer) Get(_ context.Context, req *publicv1.ComputeInstancesGetRequest) (*publicv1.ComputeInstancesGetResponse, error) {
	obj, err := s.store.get(req.GetId())
	if err != nil {
		return nil, err
	}
	return &publicv1.ComputeInstancesGetResponse{Object: obj}, nil
}

func (s *ComputeInstancesServer) List(_ context.Context, req *publicv1.ComputeInstancesListRequest) (*publicv1.ComputeInstancesListResponse, error) {
	items, total := s.store.list(int(req.GetOffset()), int(req.GetLimit()))
	return &publicv1.ComputeInstancesListResponse{Size: int32(len(items)), Total: int32(total), Items: items}, nil //nolint:gosec // in-memory test store, item count bounded by what the test itself creates
}

func (s *ComputeInstancesServer) Delete(_ context.Context, req *publicv1.ComputeInstancesDeleteRequest) (*publicv1.ComputeInstancesDeleteResponse, error) {
	if err := s.store.delete(req.GetId()); err != nil {
		return nil, err
	}
	return &publicv1.ComputeInstancesDeleteResponse{}, nil
}

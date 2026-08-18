package mockprovider

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// ClustersServer is a real, in-memory fake of osac.public.v1.Clusters
// (REQ-MOCK-010). Create requires and uses the caller-supplied
// object.id (REQ-MOCK-020), matching how osac-sp itself sets Cluster.id
// for idempotent create-retry (M3 DD-100). Update and the kubeconfig/
// password RPCs are left on the embedded UnimplementedClustersServer
// default (gRPC UNIMPLEMENTED) — osac-sp never calls them (see spec §1).
type ClustersServer struct {
	publicv1.UnimplementedClustersServer

	store *resourceStore[*publicv1.Cluster]
}

// NewClustersServer returns an empty ClustersServer ready to register on a
// grpc.Server.
func NewClustersServer() *ClustersServer {
	return &ClustersServer{store: newResourceStore[*publicv1.Cluster]()}
}

func (s *ClustersServer) Create(_ context.Context, req *publicv1.ClustersCreateRequest) (*publicv1.ClustersCreateResponse, error) {
	obj := req.GetObject()
	if obj.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "object.id is required")
	}
	obj.Status = &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_READY}

	created, err := s.store.create(obj.GetId(), obj)
	if err != nil {
		return nil, err
	}
	return &publicv1.ClustersCreateResponse{Object: created}, nil
}

func (s *ClustersServer) Get(_ context.Context, req *publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
	obj, err := s.store.get(req.GetId())
	if err != nil {
		return nil, err
	}
	return &publicv1.ClustersGetResponse{Object: obj}, nil
}

func (s *ClustersServer) List(_ context.Context, req *publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error) {
	items, total := s.store.list(int(req.GetOffset()), int(req.GetLimit()))
	return &publicv1.ClustersListResponse{Size: int32(len(items)), Total: int32(total), Items: items}, nil
}

func (s *ClustersServer) Delete(_ context.Context, req *publicv1.ClustersDeleteRequest) (*publicv1.ClustersDeleteResponse, error) {
	if err := s.store.delete(req.GetId()); err != nil {
		return nil, err
	}
	return &publicv1.ClustersDeleteResponse{}, nil
}

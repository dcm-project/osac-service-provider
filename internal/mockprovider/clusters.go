package mockprovider

import (
	"context"
	"encoding/base64"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// ClustersServer is a real, in-memory fake of osac.public.v1.Clusters
// (REQ-MOCK-010). Create requires and uses the caller-supplied
// object.id (REQ-MOCK-020), matching how osac-sp itself sets Cluster.id
// for idempotent create-retry (M3 DD-100). Update and the
// GetKubeconfigViaHttp/password RPCs are left on the embedded
// UnimplementedClustersServer default (gRPC UNIMPLEMENTED) — osac-sp never
// calls them. Plain GetKubeconfig, by contrast, IS implemented below
// (REQ-MOCK-120): osac-sp's M3 Get handler calls it for every ACTIVE-status
// cluster, which every mock-created cluster immediately is (REQ-MOCK-030).
// See DD-095 for how this correction was found.
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

// GetKubeconfig returns a stub, non-functional kubeconfig for a known id,
// and gRPC NOT_FOUND for an unknown one (REQ-MOCK-120) — mirroring the
// other four CRUD-shaped services' Get semantics (REQ-MOCK-040). The
// content is never parsed by osac-sp (internal/cluster.Service.Get copies
// it through as an opaque base64 string), so a deterministic placeholder
// is sufficient; it only needs to be present and valid base64.
func (s *ClustersServer) GetKubeconfig(_ context.Context, req *publicv1.ClustersGetKubeconfigRequest) (*publicv1.ClustersGetKubeconfigResponse, error) {
	if _, err := s.store.get(req.GetId()); err != nil {
		return nil, err
	}
	stub := fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: %s\n  cluster:\n    server: https://mock-provider.invalid:6443\ncurrent-context: %s\n", req.GetId(), req.GetId())
	return &publicv1.ClustersGetKubeconfigResponse{Kubeconfig: base64.StdEncoding.EncodeToString([]byte(stub))}, nil
}

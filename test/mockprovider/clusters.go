package mockprovider

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/private/v1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// ClustersServer is a real, in-memory fake of osac.public.v1.Clusters
// (REQ-MOCK-010). Create requires and uses the caller-supplied
// object.id (REQ-MOCK-020), matching how osac-sp itself sets Cluster.id
// for create-retry (M3 DD-100). Update and the removed GetKubeconfig RPCs
// remain unimplemented; kubeconfig is served by the paired private Secrets
// service.
type ClustersServer struct {
	publicv1.UnimplementedClustersServer

	store   *resourceStore[*publicv1.Cluster]
	mu      sync.RWMutex
	secrets map[string]*privatev1.Secret
}

// NewClustersServer returns an empty ClustersServer ready to register on a
// grpc.Server.
func NewClustersServer() *ClustersServer {
	return &ClustersServer{
		store:   newResourceStore[*publicv1.Cluster](),
		secrets: make(map[string]*privatev1.Secret),
	}
}

func (s *ClustersServer) Create(_ context.Context, req *publicv1.ClustersCreateRequest) (*publicv1.ClustersCreateResponse, error) {
	obj := req.GetObject()
	if obj.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "object.id is required")
	}
	secretID := "mock-kubeconfig-" + obj.GetId()
	obj.Status = &publicv1.ClusterStatus{
		State: publicv1.ClusterState_CLUSTER_STATE_READY,
		KubeconfigSecret: &publicv1.SecretLocalReference{
			Id:   secretID,
			Name: obj.GetId() + "-kubeconfig",
		},
	}

	created, err := s.store.create(obj.GetId(), obj)
	if err != nil {
		return nil, err
	}
	stub := fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: %s\n  cluster:\n    server: https://mock-provider.invalid:6443\ncurrent-context: %s\n", obj.GetId(), obj.GetId())
	s.mu.Lock()
	s.secrets[secretID] = &privatev1.Secret{
		Id:   secretID,
		Type: privatev1.SecretType_SECRET_TYPE_KUBECONFIG,
		Data: map[string][]byte{"kubeconfig": []byte(stub)},
	}
	s.mu.Unlock()
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
	return &publicv1.ClustersListResponse{Size: int32(len(items)), Total: int32(total), Items: items}, nil //nolint:gosec // in-memory test store, item count bounded by what the test itself creates
}

func (s *ClustersServer) Delete(_ context.Context, req *publicv1.ClustersDeleteRequest) (*publicv1.ClustersDeleteResponse, error) {
	if err := s.store.delete(req.GetId()); err != nil {
		return nil, err
	}
	s.mu.Lock()
	delete(s.secrets, "mock-kubeconfig-"+req.GetId())
	s.mu.Unlock()
	return &publicv1.ClustersDeleteResponse{}, nil
}

// SecretsServer serves the inline kubeconfig Secrets attached by
// ClustersServer.Create (REQ-MOCK-120).
type SecretsServer struct {
	privatev1.UnimplementedSecretsServer
	clusters *ClustersServer
}

// NewSecretsServer returns a Secrets service backed by the given Clusters
// server's mock kubeconfig store.
func NewSecretsServer(clusters *ClustersServer) *SecretsServer {
	return &SecretsServer{clusters: clusters}
}

func (s *SecretsServer) Get(_ context.Context, req *privatev1.SecretsGetRequest) (*privatev1.SecretsGetResponse, error) {
	s.clusters.mu.RLock()
	secret := s.clusters.secrets[req.GetId()]
	if secret == nil {
		s.clusters.mu.RUnlock()
		return nil, status.Error(codes.NotFound, "secret not found")
	}
	data := make(map[string][]byte, len(secret.GetData()))
	for key, value := range secret.GetData() {
		data[key] = append([]byte(nil), value...)
	}
	responseSecret := &privatev1.Secret{Id: secret.GetId(), Type: secret.GetType(), Data: data}
	s.clusters.mu.RUnlock()
	return &privatev1.SecretsGetResponse{Object: responseSecret}, nil
}

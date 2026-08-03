// Package cluster implements the OSAC Service Provider's Cluster CRUD
// business logic (Milestone 3), translating between DCM's Cluster REST
// schema (api/v1alpha1) and osac.public.v1.Clusters gRPC calls.
//
// Implements .ai/specs/osac-sp-m3-cluster-crud.spec.md Topics 4.1-4.4.
package cluster

import (
	"context"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// Service implements Cluster Create/Get/List/Delete against OSAC's
// Clusters gRPC service. Constructed from a Bootstrap.Conn()-backed client
// (publicv1.NewClustersClient) per DD-020 — no new Bootstrap accessor is
// added.
type Service struct {
	client publicv1.ClustersClient
}

// New constructs a Service wrapping the given Clusters client.
func New(client publicv1.ClustersClient) *Service {
	return &Service{client: client}
}

// Create translates spec into OSAC's ClusterSpec and calls Clusters/Create
// with Cluster.id set to id (REQ-CREATE-020). If OSAC reports AlreadyExists
// for id, Create instead calls Clusters/Get(id) and returns that resource's
// current state — REQ-CREATE-040/DD-100: this SP is the real idempotency
// backstop, since upstream (control-plane) retry-safety has a known gap.
func (s *Service) Create(ctx context.Context, id string, spec v1alpha1.ClusterSpec) (v1alpha1.Cluster, error) {
	obj := toOSACCluster(id, spec)

	resp, err := s.client.Create(ctx, &publicv1.ClustersCreateRequest{Object: obj})
	if err != nil {
		if grpcstatus.Code(err) == codes.AlreadyExists {
			getResp, getErr := s.client.Get(ctx, &publicv1.ClustersGetRequest{Id: id})
			if getErr != nil {
				return v1alpha1.Cluster{}, getErr
			}
			return toAPICluster(getResp.GetObject(), nil), nil
		}
		return v1alpha1.Cluster{}, err
	}

	version := spec.Version
	return toAPICluster(resp.GetObject(), &version), nil
}

// Get calls Clusters/Get(id), maps the result via MapStatus, and — only
// when the mapped status is exactly ACTIVE (REQ-GET-020/030) — fetches the
// kubeconfig via Clusters/GetKubeconfig.
func (s *Service) Get(ctx context.Context, id string) (v1alpha1.Cluster, error) {
	resp, err := s.client.Get(ctx, &publicv1.ClustersGetRequest{Id: id})
	if err != nil {
		return v1alpha1.Cluster{}, err
	}

	result := toAPICluster(resp.GetObject(), nil)
	if result.Status != v1alpha1.ClusterStatusACTIVE {
		// REQ-GET-030: kubeconfig is the empty string (not omitted) for any
		// non-ACTIVE status — distinct from List, which omits it entirely
		// (REQ-LIST-030) since toAPICluster never sets it.
		result.Kubeconfig = new(string)
		return result, nil
	}

	kcResp, err := s.client.GetKubeconfig(ctx, &publicv1.ClustersGetKubeconfigRequest{Id: id})
	if err != nil {
		return v1alpha1.Cluster{}, err
	}
	kubeconfig := kcResp.GetKubeconfig()
	result.Kubeconfig = &kubeconfig
	return result, nil
}

// Delete calls Clusters/Delete(id). A NotFound response is treated as
// success (REQ-DELETE-020), mirroring control-plane's own tolerance for
// this exact case (DD-080).
func (s *Service) Delete(ctx context.Context, id string) error {
	_, err := s.client.Delete(ctx, &publicv1.ClustersDeleteRequest{Id: id})
	if err != nil && grpcstatus.Code(err) != codes.NotFound {
		return err
	}
	return nil
}

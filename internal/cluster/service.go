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
	"github.com/dcm-project/osac-service-provider/internal/versionmatrix"
)

// Service implements Cluster Create/Get/List/Delete against OSAC's
// Clusters gRPC service. Constructed from Bootstrap.Conn()-backed clients
// (publicv1.NewClustersClient, publicv1.NewClusterTemplatesClient) per
// DD-020 — no new Bootstrap accessor is added.
type Service struct {
	client    publicv1.ClustersClient
	templates publicv1.ClusterTemplatesClient
	matrix    versionmatrix.Matrix
}

// New constructs a Service wrapping the given Clusters/ClusterTemplates
// clients. matrix is consulted by Create's release_image translation
// (REQ-VERSION-060) and by SupportsVersion (REQ-VERSION-070) — the same
// instance main.go loads once at startup and also injects into
// internal/registration, so the two can never drift apart.
func New(client publicv1.ClustersClient, templates publicv1.ClusterTemplatesClient, matrix versionmatrix.Matrix) *Service {
	return &Service{client: client, templates: templates, matrix: matrix}
}

// SupportsVersion reports whether version has an entry in s's injected
// matrix, for internal/handlers/cluster's pre-flight validation
// (REQ-VERSION-070/080) to query without duplicating or directly importing
// the matrix.
func (s *Service) SupportsVersion(version string) bool {
	_, ok := s.matrix.Lookup(version)
	return ok
}

// Create translates spec into OSAC's ClusterSpec and calls Clusters/Create
// with Cluster.id set to id (REQ-CREATE-020). If OSAC reports AlreadyExists
// for id, Create instead calls Clusters/Get(id) and returns that resource's
// current state — REQ-CREATE-040/DD-100: this SP is the real idempotency
// backstop, since upstream (control-plane) retry-safety has a known gap.
func (s *Service) Create(ctx context.Context, id string, spec v1alpha1.ClusterSpec) (v1alpha1.Cluster, error) {
	nodeSetKey, err := s.resolveNodeSetKey(ctx, spec.ProviderHints.Osac.TemplateId)
	if err != nil {
		return v1alpha1.Cluster{}, err
	}

	obj := s.toOSACCluster(id, spec, nodeSetKey)
	version := spec.Version

	resp, err := s.client.Create(ctx, &publicv1.ClustersCreateRequest{Object: obj})
	if err != nil {
		if grpcstatus.Code(err) == codes.AlreadyExists {
			getResp, getErr := s.client.Get(ctx, &publicv1.ClustersGetRequest{Id: id})
			if getErr != nil {
				return v1alpha1.Cluster{}, getErr
			}
			// Same request, same spec.version — echo it here too so the
			// retried path doesn't return a different body shape than the
			// first-time path (SC-M3-002).
			return toAPICluster(getResp.GetObject(), &version), nil
		}
		return v1alpha1.Cluster{}, err
	}

	return toAPICluster(resp.GetObject(), &version), nil
}

// resolveNodeSetKey looks up templateID via ClusterTemplates/Get and
// returns its single node-set key (REQ-CREATE-080) — the key is per-template
// and never equal to templateID itself, so it can only be discovered this
// way (DD-110). A template with zero or more than one node-set key is
// rejected as InvalidArgument (REQ-CREATE-090): this milestone's single
// nodes.worker.count has no way to say which key it applies to. An unknown
// templateID is also InvalidArgument, not the NotFound a raw passthrough
// would produce (REQ-CREATE-100) — it's a bad value in the caller's own
// request, not a missing SP-managed resource.
func (s *Service) resolveNodeSetKey(ctx context.Context, templateID string) (string, error) {
	resp, err := s.templates.Get(ctx, &publicv1.ClusterTemplatesGetRequest{Id: templateID})
	if err != nil {
		if grpcstatus.Code(err) == codes.NotFound {
			return "", grpcstatus.Errorf(codes.InvalidArgument, "template %q not found", templateID)
		}
		return "", err
	}

	nodeSets := resp.GetObject().GetNodeSets()
	if len(nodeSets) != 1 {
		return "", grpcstatus.Errorf(codes.InvalidArgument,
			"template %q defines %d node sets, expected exactly 1", templateID, len(nodeSets))
	}
	var key string
	for k := range nodeSets {
		key = k
	}
	return key, nil
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
	if *result.Status != v1alpha1.ClusterStatusACTIVE {
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

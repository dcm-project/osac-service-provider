package mockprovider

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// defaultHCPTemplateID is the only template this fake knows about, matching
// osac-sp's own e2e suite's validClusterCreateBody
// (provider_hints.osac.template_id) and internal/handlers/cluster's own
// integration-test fixtures.
const defaultHCPTemplateID = "default-hcp"

// ClusterTemplatesServer is a trivial, stateless fake of
// osac.public.v1.ClusterTemplates (REQ-MOCK-130) — added after the rest of
// this package, once Milestone 3's internal/cluster.Service.Create
// introduced a hard dependency on ClusterTemplates/Get (REQ-CREATE-080) to
// resolve a template's single node-set key. Without this, every real
// Cluster Create against this mock fails with gRPC UNIMPLEMENTED (surfaced
// by osac-sp as a generic 500/INTERNAL) — a gap only found by dry-running
// this suite against a combined M3+M4+this-branch tree before either PR
// merged.
type ClusterTemplatesServer struct {
	publicv1.UnimplementedClusterTemplatesServer
}

// NewClusterTemplatesServer returns a ClusterTemplatesServer ready to
// register on a grpc.Server.
func NewClusterTemplatesServer() *ClusterTemplatesServer {
	return &ClusterTemplatesServer{}
}

func (s *ClusterTemplatesServer) Get(_ context.Context, req *publicv1.ClusterTemplatesGetRequest) (*publicv1.ClusterTemplatesGetResponse, error) {
	if req.GetId() != defaultHCPTemplateID {
		return nil, status.Errorf(codes.NotFound, "cluster template %q not found", req.GetId())
	}
	return &publicv1.ClusterTemplatesGetResponse{
		Object: &publicv1.ClusterTemplate{
			Id:       defaultHCPTemplateID,
			NodeSets: map[string]*publicv1.ClusterTemplateNodeSet{"compute": {HostType: "compute", Size: 1}},
		},
	}, nil
}

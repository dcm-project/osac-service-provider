package mockprovider

import (
	"context"

	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// CapabilitiesServer is a trivial fake of osac.public.v1.Capabilities
// (REQ-MOCK-070) — backs only the health-check connectivity probe, so no
// capability content is required to be populated.
type CapabilitiesServer struct {
	publicv1.UnimplementedCapabilitiesServer
}

// NewCapabilitiesServer returns a CapabilitiesServer ready to register on
// a grpc.Server.
func NewCapabilitiesServer() *CapabilitiesServer {
	return &CapabilitiesServer{}
}

func (s *CapabilitiesServer) Get(_ context.Context, _ *publicv1.CapabilitiesGetRequest) (*publicv1.CapabilitiesGetResponse, error) {
	return &publicv1.CapabilitiesGetResponse{}, nil
}

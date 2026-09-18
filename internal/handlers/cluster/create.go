package cluster

import (
	"context"
	"math"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
)

// CreateCluster implements oapigen.StrictServerInterface.
//
// Implements REQ-CREATE-010 through REQ-CREATE-070.
func (h *Handler) CreateCluster(ctx context.Context, req oapigen.CreateClusterRequestObject) (oapigen.CreateClusterResponseObject, error) {
	if err := h.validateCreateRequest(req); err != nil {
		return h.mapError(err), nil
	}

	result, err := h.svc.Create(ctx, *req.Params.Id, *req.Body.Spec)
	if err != nil {
		return h.mapError(err), nil
	}
	return oapigen.CreateCluster201JSONResponse(result), nil
}

// validateCreateRequest implements REQ-CREATE-060's request validation,
// returning a synthetic gRPC InvalidArgument error — mapped to 400 by the
// same shared mapError as any OSAC-originated error (REQ-ERR-030) — before
// ever dispatching to OSAC. This is the sole enforcement point for "id"/
// "spec", which are schema-optional per DD-113 (AEP-133).
//
// The final cases reject unsupported versions and the removed release_image
// override before dispatching to the current OSAC API.
func (h *Handler) validateCreateRequest(req oapigen.CreateClusterRequestObject) error {
	switch {
	case req.Params.Id == nil || *req.Params.Id == "":
		return grpcstatus.Error(codes.InvalidArgument, "id query parameter must not be empty")
	case req.Body.Spec == nil:
		return grpcstatus.Error(codes.InvalidArgument, "spec is required")
	case req.Body.Spec.Version == "":
		return grpcstatus.Error(codes.InvalidArgument, "spec.version is required")
	case req.Body.Spec.Nodes.Worker.Count <= 0:
		return grpcstatus.Error(codes.InvalidArgument, "spec.nodes.worker.count must be greater than 0")
	case req.Body.Spec.Nodes.Worker.Count > math.MaxInt32:
		// translate.go's toOSACCluster narrows this to int32 for OSAC's
		// wire type (gosec G115) — reject out-of-range values here,
		// rather than let that conversion silently wrap (see DD-113's
		// fail-fast-validation precedent).
		return grpcstatus.Error(codes.InvalidArgument, "spec.nodes.worker.count must not exceed 2147483647")
	case req.Body.Spec.Metadata.Name == "":
		return grpcstatus.Error(codes.InvalidArgument, "spec.metadata.name is required")
	case req.Body.Spec.ProviderHints.Osac.TemplateId == "":
		return grpcstatus.Error(codes.InvalidArgument, "spec.provider_hints.osac.template_id is required")
	case hasReleaseImageOverride(req):
		return grpcstatus.Error(codes.InvalidArgument,
			"spec.provider_hints.osac.release_image is not supported by the current OSAC API")
	case !h.svc.SupportsVersion(req.Body.Spec.Version):
		return grpcstatus.Error(codes.InvalidArgument, "spec.version is not a supported Kubernetes version")
	default:
		return nil
	}
}

// hasReleaseImageOverride reports whether req sets a non-empty legacy
// provider_hints.osac.release_image field.
func hasReleaseImageOverride(req oapigen.CreateClusterRequestObject) bool {
	override := req.Body.Spec.ProviderHints.Osac.ReleaseImage
	return override != nil && *override != ""
}

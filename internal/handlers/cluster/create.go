package cluster

import (
	"context"

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
// The final case (REQ-VERSION-080) hard-rejects an unsupported
// spec.version instead of silently falling back to OSAC's template
// default release_image (DD-113) — an explicit, non-empty
// provider_hints.osac.release_image override bypasses this check
// entirely, even for a version absent from h.svc's injected matrix.
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
	case req.Body.Spec.Metadata.Name == "":
		return grpcstatus.Error(codes.InvalidArgument, "spec.metadata.name is required")
	case req.Body.Spec.ProviderHints.Osac.TemplateId == "":
		return grpcstatus.Error(codes.InvalidArgument, "spec.provider_hints.osac.template_id is required")
	case !hasReleaseImageOverride(req) && !h.svc.SupportsVersion(req.Body.Spec.Version):
		return grpcstatus.Error(codes.InvalidArgument, "spec.version is not a supported Kubernetes version")
	default:
		return nil
	}
}

// hasReleaseImageOverride reports whether req sets a non-empty
// provider_hints.osac.release_image, which bypasses the matrix entirely
// (REQ-VERSION-060/080).
func hasReleaseImageOverride(req oapigen.CreateClusterRequestObject) bool {
	override := req.Body.Spec.ProviderHints.Osac.ReleaseImage
	return override != nil && *override != ""
}

package vm

import (
	"context"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
)

// CreateVM implements oapigen.StrictServerInterface.
//
// Implements REQ-VMCREATE-010 through REQ-VMCREATE-090.
func (h *Handler) CreateVM(ctx context.Context, req oapigen.CreateVMRequestObject) (oapigen.CreateVMResponseObject, error) {
	if err := validateCreateRequest(req); err != nil {
		return h.mapError(err), nil
	}

	result, err := h.svc.Create(ctx, *req.Params.Id, *req.Body.Spec)
	if err != nil {
		return h.mapError(err), nil
	}
	return oapigen.CreateVM201JSONResponse(result), nil
}

// validateCreateRequest implements REQ-VMCREATE-060's request-shape
// validation, returning a synthetic gRPC InvalidArgument error — mapped to
// 400 by the same shared mapError as any OSAC-originated error
// (REQ-VMERR-030) — before ever dispatching to OSAC. Both req.Params.Id
// and req.Body.Spec are pointers that can genuinely be nil: the "id" query
// parameter and the body's "spec" property are schema-optional (AEP-133
// compliance, DD-085), so the router's generated ServerInterfaceWrapper
// does not reject a wholly-absent "id" (or an empty JSON object body)
// ahead of this handler ever running. This is the sole enforcement point
// for the id/spec/template_id/instance_type presence checks;
// boot-disk-presence and disk-capacity-parseability are translation-
// inherent validations that live in internal/vm instead (see
// internal/vm/translate.go's own comment) and are simply propagated raw
// through Create's error return below.
func validateCreateRequest(req oapigen.CreateVMRequestObject) error {
	switch {
	case req.Params.Id == nil || *req.Params.Id == "":
		return grpcstatus.Error(codes.InvalidArgument, "id query parameter must not be empty")
	case req.Body.Spec == nil:
		return grpcstatus.Error(codes.InvalidArgument, "spec is required")
	case req.Body.Spec.ProviderHints.Osac.TemplateId == "":
		return grpcstatus.Error(codes.InvalidArgument, "spec.provider_hints.osac.template_id is required")
	case req.Body.Spec.ProviderHints.Osac.InstanceType == "":
		return grpcstatus.Error(codes.InvalidArgument, "spec.provider_hints.osac.instance_type is required")
	default:
		return nil
	}
}

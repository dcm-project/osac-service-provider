package vm

import (
	"context"

	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
)

// ListVMs implements oapigen.StrictServerInterface.
//
// Implements REQ-VMLIST-010 through REQ-VMLIST-040.
func (h *Handler) ListVMs(ctx context.Context, req oapigen.ListVMsRequestObject) (oapigen.ListVMsResponseObject, error) {
	result, err := h.svc.List(ctx, req.Params)
	if err != nil {
		return h.mapError(err), nil
	}
	return oapigen.ListVMs200JSONResponse(result), nil
}

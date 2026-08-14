package vm

import (
	"context"

	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
)

// GetVM implements oapigen.StrictServerInterface.
//
// Implements REQ-VMGET-010 through REQ-VMGET-030.
func (h *Handler) GetVM(ctx context.Context, req oapigen.GetVMRequestObject) (oapigen.GetVMResponseObject, error) {
	result, err := h.svc.Get(ctx, req.VmId)
	if err != nil {
		return h.mapError(err), nil
	}
	return oapigen.GetVM200JSONResponse(result), nil
}

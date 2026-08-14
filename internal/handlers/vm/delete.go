package vm

import (
	"context"

	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
)

// DeleteVM implements oapigen.StrictServerInterface. The NotFound
// carve-out (REQ-VMDELETE-020) is internal/vm.Service.Delete's own
// responsibility — any error reaching here is already a genuine failure
// (REQ-VMDELETE-030).
//
// Implements REQ-VMDELETE-010 through REQ-VMDELETE-040.
func (h *Handler) DeleteVM(ctx context.Context, req oapigen.DeleteVMRequestObject) (oapigen.DeleteVMResponseObject, error) {
	if err := h.svc.Delete(ctx, req.VmId); err != nil {
		return h.mapError(err), nil
	}
	return oapigen.DeleteVM204Response{}, nil
}

package cluster

import (
	"context"

	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
)

// GetCluster implements oapigen.StrictServerInterface.
//
// Implements REQ-GET-010 through REQ-GET-040.
func (h *Handler) GetCluster(ctx context.Context, req oapigen.GetClusterRequestObject) (oapigen.GetClusterResponseObject, error) {
	result, err := h.svc.Get(ctx, req.ClusterId)
	if err != nil {
		return h.mapError(err), nil
	}
	return oapigen.GetCluster200JSONResponse(result), nil
}

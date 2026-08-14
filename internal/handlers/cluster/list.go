package cluster

import (
	"context"

	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
)

// ListClusters implements oapigen.StrictServerInterface.
//
// Implements REQ-LIST-010 through REQ-LIST-040.
func (h *Handler) ListClusters(ctx context.Context, req oapigen.ListClustersRequestObject) (oapigen.ListClustersResponseObject, error) {
	result, err := h.svc.List(ctx, req.Params)
	if err != nil {
		return h.mapError(err), nil
	}
	return oapigen.ListClusters200JSONResponse(result), nil
}

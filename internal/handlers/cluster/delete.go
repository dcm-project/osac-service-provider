package cluster

import (
	"context"

	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
)

// DeleteCluster implements oapigen.StrictServerInterface. The NotFound
// carve-out (REQ-DELETE-020) is internal/cluster.Service.Delete's own
// responsibility — any error reaching here is already a genuine failure
// (REQ-DELETE-030).
//
// Implements REQ-DELETE-010 through REQ-DELETE-040.
func (h *Handler) DeleteCluster(ctx context.Context, req oapigen.DeleteClusterRequestObject) (oapigen.DeleteClusterResponseObject, error) {
	if err := h.svc.Delete(ctx, req.ClusterId); err != nil {
		return h.mapError(err), nil
	}
	return oapigen.DeleteCluster204Response{}, nil
}

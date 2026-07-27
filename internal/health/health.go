// Package health implements the GET /api/v1alpha1/clusters/health and
// GET /api/v1alpha1/vms/health endpoints, reporting real OSAC connectivity
// and OIDC token health. Per DD-010, there are two endpoints (one per
// independently-registered provider) rather than one, but both report the
// same underlying, global health condition.
//
// Implements Topic 4.3 (Health Service) of the Milestone 1 spec.
package health

import (
	"context"
	"strings"
	"time"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
	"github.com/dcm-project/osac-service-provider/internal/osac"
	"github.com/dcm-project/osac-service-provider/internal/util"
)

// resourceType is the AEP resource type identifier for the Health resource.
const resourceType = "osac-service-provider.dcm.io/health"

// OSACStatus is the subset of internal/osac's Bootstrap this handler
// depends on. Satisfied by *osac.Bootstrap; faked in unit tests.
type OSACStatus interface {
	TokenStatus() osac.TokenStatus
	Probe(ctx context.Context) osac.ProbeResult
}

// Handler implements the health check.
type Handler struct {
	osacStatus OSACStatus
	startTime  time.Time
	version    string
}

// NewHandler creates a health Handler.
func NewHandler(osacStatus OSACStatus, startTime time.Time, version string) *Handler {
	return &Handler{osacStatus: osacStatus, startTime: startTime, version: version}
}

// GetClustersHealth implements oapigen.StrictServerInterface for the
// `cluster` provider's health endpoint.
//
// Implements REQ-HLT-010 through REQ-HLT-070.
func (h *Handler) GetClustersHealth(ctx context.Context, _ oapigen.GetClustersHealthRequestObject) (oapigen.GetClustersHealthResponseObject, error) {
	resp := h.checkHealth(ctx)
	return oapigen.GetClustersHealth200JSONResponse(resp), nil
}

// GetVMsHealth implements oapigen.StrictServerInterface for the `vm`
// provider's health endpoint. Per REQ-HLT-015, this reports identical
// status to GetClustersHealth — the same underlying health condition,
// computed by the shared checkHealth.
//
// Implements REQ-HLT-010 through REQ-HLT-070.
func (h *Handler) GetVMsHealth(ctx context.Context, _ oapigen.GetVMsHealthRequestObject) (oapigen.GetVMsHealthResponseObject, error) {
	resp := h.checkHealth(ctx)
	return oapigen.GetVMsHealth200JSONResponse(resp), nil
}

// checkHealth computes the SP's single, global health condition (OIDC token
// validity + OSAC gRPC connectivity), shared by both exposed endpoints
// (REQ-HLT-015).
func (h *Handler) checkHealth(ctx context.Context) v1alpha1.Health {
	tokenStatus := h.osacStatus.TokenStatus()
	probe := h.osacStatus.Probe(ctx)

	status := v1alpha1.Healthy
	var detail *string
	if !tokenStatus.Valid || !probe.Connected {
		status = v1alpha1.Unhealthy
		detail = util.Ptr(unhealthyDetail(tokenStatus.Valid, probe.Connected))
	}

	return v1alpha1.Health{
		Type:    util.Ptr(resourceType),
		Status:  status,
		Path:    util.Ptr("health"),
		Version: util.Ptr(h.version),
		Uptime:  util.Ptr(int(time.Since(h.startTime).Seconds())),
		Detail:  detail,
	}
}

// unhealthyDetail builds a message distinguishing token vs. connectivity
// causes (REQ-HLT-070); both may be true simultaneously.
func unhealthyDetail(tokenValid, connected bool) string {
	var parts []string
	if !tokenValid {
		parts = append(parts, "OIDC token invalid")
	}
	if !connected {
		parts = append(parts, "OSAC fulfillment service unreachable")
	}
	return strings.Join(parts, "; ")
}

// Package health implements the GET /api/v1alpha1/health endpoint, reporting
// real OSAC connectivity and OIDC token health.
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

// GetHealth implements oapigen.StrictServerInterface.
//
// Implements REQ-HLT-010 through REQ-HLT-070.
func (h *Handler) GetHealth(ctx context.Context, _ oapigen.GetHealthRequestObject) (oapigen.GetHealthResponseObject, error) {
	tokenStatus := h.osacStatus.TokenStatus()
	probe := h.osacStatus.Probe(ctx)

	status := v1alpha1.Healthy
	var detail *string
	if !tokenStatus.Valid || !probe.Connected {
		status = v1alpha1.Unhealthy
		detail = util.Ptr(unhealthyDetail(tokenStatus.Valid, probe.Connected))
	}

	resp := v1alpha1.Health{
		Type:    util.Ptr(resourceType),
		Status:  status,
		Path:    util.Ptr("health"),
		Version: util.Ptr(h.version),
		Uptime:  util.Ptr(int(time.Since(h.startTime).Seconds())),
		Detail:  detail,
	}

	return oapigen.GetHealth200JSONResponse(resp), nil
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

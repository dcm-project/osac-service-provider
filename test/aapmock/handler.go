package aapmock

import "net/http"

// Handler serves this mock's fake AAP REST surface (REQ-TB-080).
type Handler struct {
	mux       *http.ServeMux
	templates *templateRegistry
	jobs      *jobStore
}

// NewHandler returns a ready-to-serve Handler, backed by fresh in-memory
// state.
func NewHandler() *Handler {
	h := &Handler{
		templates: newTemplateRegistry(),
		jobs:      newJobStore(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v2/job_templates/", h.lookupTemplate)
	mux.HandleFunc("GET /v2/workflow_job_templates/", h.lookupTemplate)
	mux.HandleFunc("POST /v2/job_templates/{identifier}/launch/", h.launchTemplate)
	mux.HandleFunc("POST /v2/workflow_job_templates/{identifier}/launch/", h.launchTemplate)
	mux.HandleFunc("GET /v2/jobs/{id}/", h.getJob)
	mux.HandleFunc("GET /v2/jobs/{id}/cancel/", h.canCancelJob)
	mux.HandleFunc("POST /v2/jobs/{id}/cancel/", h.cancelJob)
	h.mux = mux
	return h
}

// ServeHTTP implements http.Handler. Deliberately does not validate the
// Authorization header's content (NFR-TB-030, TC-U-569) — mirrors DD-132's
// OIDC-stub precedent: this mock's fidelity boundary is "no real
// Ansible/hardware access," not AAP-layer auth.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

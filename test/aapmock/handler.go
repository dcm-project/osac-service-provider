package aapmock

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// Handler serves this mock's fake AAP REST surface (REQ-TB-080).
type Handler struct {
	mux       *http.ServeMux
	templates *templateRegistry
	jobs      *jobStore
	token     string
}

// NewHandler returns a ready-to-serve Handler, backed by fresh in-memory
// state. token is the exact Bearer value every request must present
// (DD-225) — see ServeHTTP's doc comment for why this mock enforces it
// rather than accepting anything.
func NewHandler(token string) *Handler {
	h := &Handler{
		templates: newTemplateRegistry(),
		jobs:      newJobStore(),
		token:     token,
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

// ServeHTTP implements http.Handler. Every request must present the exact
// Bearer token this mock was started with (DD-225, TC-U-569/574) —
// osac-operator is configured with the same shared secret via its own
// `aap.token` Helm value, so a missing/wrong token fails here the same way
// it would against real AAP, instead of only ever failing in production.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) authorized(r *http.Request) bool {
	const prefix = "Bearer "
	authz := r.Header.Get("Authorization")
	provided, ok := strings.CutPrefix(authz, prefix)
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(h.token)) == 1
}

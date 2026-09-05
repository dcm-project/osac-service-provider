package aapmock

import (
	"encoding/json"
	"net/http"
	"sync"
)

// templateResult mirrors osac-operator/pkg/aap.Template's wire shape
// (id/name only — Type is derived client-side from which endpoint
// answered, DD-213).
type templateResult struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type templateLookupResponse struct {
	Count   int              `json:"count"`
	Results []templateResult `json:"results"`
}

// templateRegistry assigns a stable, incrementing ID the first time a
// template name is looked up, and returns the same ID on every subsequent
// lookup — this mock accepts any name (DD-213: no fixture template list to
// keep in sync with osac-operator's resolveTemplateName), but real AAP
// template IDs are stable, so this mirrors that.
type templateRegistry struct {
	mu     sync.Mutex
	nextID int
	byName map[string]int
}

func newTemplateRegistry() *templateRegistry {
	return &templateRegistry{byName: make(map[string]int)}
}

func (r *templateRegistry) idFor(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	if id, ok := r.byName[name]; ok {
		return id
	}
	r.nextID++
	r.byName[name] = r.nextID
	return r.nextID
}

// lookupTemplate handles both `GET /v2/job_templates/?name=X` and
// `GET /v2/workflow_job_templates/?name=X` — REQ-TB-080's GetTemplate
// contract (aap.Client.getTemplateFromEndpoint). Always reports exactly one
// match for the requested name (DD-213).
func (h *Handler) lookupTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	id := h.templates.idFor(name)

	writeJSON(w, templateLookupResponse{
		Count:   1,
		Results: []templateResult{{ID: id, Name: name}},
	})
}

// writeJSON writes body as a 200 OK JSON response. Every endpoint in this
// mock responds 200 on success (failure paths use http.Error/http.NotFound
// directly instead), so status is not a parameter. Generic over a concrete
// response type (rather than `any`) so each call site's actual struct type
// stays statically JSON-safe.
func writeJSON[T any](w http.ResponseWriter, body T) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	//nolint:errchkjson // every call site passes one of this package's own hand-defined response structs (templateLookupResponse/launchResponse/jobResponse/canCancelResponse) — always JSON-safe, no channels/funcs/unsupported types possible.
	_ = json.NewEncoder(w).Encode(body)
}

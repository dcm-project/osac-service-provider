package aapmock

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type launchRequest struct {
	ExtraVars json.RawMessage `json:"extra_vars"`
}

type launchResponse struct {
	ID int `json:"id"`
}

// launchTemplate handles both
// `POST /v2/job_templates/{identifier}/launch/` and
// `POST /v2/workflow_job_templates/{identifier}/launch/` — REQ-TB-080's
// LaunchJobTemplate/LaunchWorkflowTemplate contract. Accepts any
// extra_vars payload without validating its shape (NFR-TB-030, TC-U-563):
// this mock is a hardware/Ansible-boundary replacement, not a schema
// validator.
func (h *Handler) launchTemplate(w http.ResponseWriter, r *http.Request) {
	var req launchRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	j := h.jobs.launch(string(req.ExtraVars))
	writeJSON(w, launchResponse{ID: j.id})
}

type jobResponse struct {
	ID              int    `json:"id"`
	Status          string `json:"status"`
	Started         string `json:"started"`
	Finished        string `json:"finished"`
	ExtraVars       string `json:"extra_vars"`
	ResultTraceback string `json:"result_traceback"`
}

// getJob handles `GET /v2/jobs/{id}/` — REQ-TB-080's GetJob contract.
// Reports "successful" from the very first poll for any non-canceled job
// (DD-214); unknown IDs return a real 404 (TC-U-565), matching
// aap.Client's NotFoundError branch.
func (h *Handler) getJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}

	j, ok := h.jobs.get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}

	writeJSON(w, jobResponse{
		ID:        j.id,
		Status:    j.status(),
		Started:   j.started.Format(time.RFC3339),
		Finished:  j.finished.Format(time.RFC3339),
		ExtraVars: j.extraVars,
	})
}

type canCancelResponse struct {
	CanCancel bool `json:"can_cancel"`
}

// canCancelJob handles `GET /v2/jobs/{id}/cancel/` — REQ-TB-080's
// CanCancelJob contract.
func (h *Handler) canCancelJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}

	j, ok := h.jobs.get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}

	writeJSON(w, canCancelResponse{CanCancel: !j.canceled})
}

// cancelJob handles `POST /v2/jobs/{id}/cancel/` — REQ-TB-080's CancelJob
// contract. Returns 202 on a real state transition, or 405 if the job was
// already canceled (DD-214, TC-U-568) — a fail-safe response, not a
// silent success, matching aap.Client's MethodNotAllowedError branch.
func (h *Handler) cancelJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}

	ok, found := h.jobs.cancel(id)
	if !found {
		http.NotFound(w, r)
		return
	}
	if !ok {
		http.Error(w, "job already in a terminal state", http.StatusMethodNotAllowed)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

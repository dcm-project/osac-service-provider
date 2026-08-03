package vm

import (
	"log/slog"
	"net/http"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	"github.com/dcm-project/osac-service-provider/internal/grpcerror"
	"github.com/dcm-project/osac-service-provider/internal/httperror"
)

// mappedError is a single response-object type satisfying all four VM
// operations' *ResponseObject interfaces simultaneously (each requires only
// its own Visit*Response method) — so Handler.mapError (REQ-VMERR-030) is
// the one shared implementation used by every handler, not four
// independently drifting ones.
type mappedError struct {
	logger  *slog.Logger
	status  int
	errType v1alpha1.ErrorType
	title   string
	detail  string
}

func (e mappedError) respond(w http.ResponseWriter) error {
	// REQ-VMERR-020: every error response reuses internal/httperror.WriteResponse.
	// instance is omitted (nil): unlike apiserver's middleware-level error
	// handlers, a StrictServerInterface method only receives (ctx, request)
	// — never the raw *http.Request needed to populate RFC 9457's optional
	// "instance" field.
	httperror.WriteResponse(w, e.logger, e.status, e.errType, e.title, e.detail, nil)
	return nil
}

func (e mappedError) VisitCreateVMResponse(w http.ResponseWriter) error { return e.respond(w) }
func (e mappedError) VisitGetVMResponse(w http.ResponseWriter) error    { return e.respond(w) }
func (e mappedError) VisitListVMsResponse(w http.ResponseWriter) error  { return e.respond(w) }
func (e mappedError) VisitDeleteVMResponse(w http.ResponseWriter) error { return e.respond(w) }

// mapError translates err — either a gRPC error surfaced by internal/vm,
// or a synthetic codes.InvalidArgument error from this package's own
// request validation — into the RFC 9457 status/type/title documented by
// REQ-VMERR-010, via the shared internal/grpcerror.Classify (DD-086, not
// duplicated per-handler). Callers implementing the AlreadyExists/NotFound
// carve-outs (REQ-VMCREATE-070, REQ-VMDELETE-020) MUST intercept those
// codes before reaching mapError; this function applies no carve-out of
// its own.
func (h *Handler) mapError(err error) mappedError {
	status, errType, title := grpcerror.Classify(err)
	detail := err.Error()
	if status == http.StatusInternalServerError {
		// Never leak a raw internal/gRPC error string for the catch-all
		// case, matching apiserver.NewResponseErrorHandler's convention.
		detail = httperror.InternalDetail
	}
	return mappedError{logger: h.logger, status: status, errType: errType, title: title, detail: detail}
}

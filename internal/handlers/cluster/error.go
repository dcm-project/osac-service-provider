package cluster

import (
	"log/slog"
	"net/http"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	"github.com/dcm-project/osac-service-provider/internal/httperror"
)

// mappedError is a single response-object type satisfying all four Cluster
// operations' *ResponseObject interfaces simultaneously (each requires only
// its own Visit*Response method) — so Handler.mapError (REQ-ERR-030) is the
// one shared implementation used by every handler, not four independently
// drifting ones.
type mappedError struct {
	logger  *slog.Logger
	status  int
	errType v1alpha1.ErrorType
	title   string
	detail  string
}

func (e mappedError) respond(w http.ResponseWriter) error {
	// REQ-ERR-020: every error response reuses internal/httperror.WriteResponse.
	// instance is omitted (nil): unlike apiserver's middleware-level error
	// handlers, a StrictServerInterface method only receives (ctx, request)
	// — never the raw *http.Request needed to populate RFC 9457's optional
	// "instance" field.
	httperror.WriteResponse(w, e.logger, e.status, e.errType, e.title, e.detail, nil)
	return nil
}

func (e mappedError) VisitCreateClusterResponse(w http.ResponseWriter) error { return e.respond(w) }
func (e mappedError) VisitGetClusterResponse(w http.ResponseWriter) error    { return e.respond(w) }
func (e mappedError) VisitListClustersResponse(w http.ResponseWriter) error  { return e.respond(w) }
func (e mappedError) VisitDeleteClusterResponse(w http.ResponseWriter) error { return e.respond(w) }

// mapError translates err — either a gRPC error surfaced by
// internal/cluster, or a synthetic codes.InvalidArgument error from this
// package's own request validation — into the RFC 9457 status/type/title
// documented by REQ-ERR-010. Callers implementing the AlreadyExists/NotFound
// carve-outs (REQ-CREATE-040, REQ-DELETE-020) MUST intercept those codes
// before reaching mapError; this function applies no carve-out of its own.
func (h *Handler) mapError(err error) mappedError {
	status, errType, title := classifyError(err)
	detail := err.Error()
	if status == http.StatusInternalServerError {
		// Never leak a raw internal/gRPC error string for the catch-all
		// case, matching apiserver.NewResponseErrorHandler's convention.
		detail = httperror.InternalDetail
	}
	return mappedError{logger: h.logger, status: status, errType: errType, title: title, detail: detail}
}

// classifyError implements REQ-ERR-010's gRPC-code -> (HTTP status,
// v1alpha1.ErrorType, title) mapping table.
func classifyError(err error) (int, v1alpha1.ErrorType, string) {
	switch grpcstatus.Code(err) {
	case codes.InvalidArgument:
		return http.StatusBadRequest, v1alpha1.ErrorTypeINVALIDARGUMENT, "Bad Request"
	case codes.Unauthenticated:
		return http.StatusUnauthorized, v1alpha1.ErrorTypeUNAUTHENTICATED, "Unauthorized"
	case codes.PermissionDenied:
		return http.StatusForbidden, v1alpha1.ErrorTypePERMISSIONDENIED, "Forbidden"
	case codes.NotFound:
		return http.StatusNotFound, v1alpha1.ErrorTypeNOTFOUND, "Not Found"
	case codes.AlreadyExists:
		return http.StatusConflict, v1alpha1.ErrorTypeALREADYEXISTS, "Conflict"
	case codes.Unavailable, codes.DeadlineExceeded:
		return http.StatusBadGateway, v1alpha1.ErrorTypeUNAVAILABLE, "Bad Gateway"
	default:
		return http.StatusInternalServerError, v1alpha1.ErrorTypeINTERNAL, httperror.InternalTitle
	}
}

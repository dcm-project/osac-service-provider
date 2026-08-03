// Package grpcerror provides the single, shared gRPC-code -> HTTP
// status/v1alpha1.ErrorType mapping used by every OSAC Service Provider
// REST handler. Extracted from Milestone 3's internal/handlers/cluster
// (DD-086) so Milestone 4's internal/handlers/vm does not duplicate the
// same table a second time; internal/handlers/cluster should adopt this in
// a follow-up.
//
// Implements .ai/specs/osac-sp-m4-vm-crud.spec.md Topic 4.7 (Error
// Mapping).
package grpcerror

import (
	"net/http"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	"github.com/dcm-project/osac-service-provider/internal/httperror"
)

// Classify maps err's gRPC status code to an HTTP status, a
// v1alpha1.ErrorType, and a human-readable title, per REQ-VMERR-010. err
// need not originate from a real gRPC call — grpcstatus.Code returns
// codes.Unknown for any error (including nil-adjacent synthetic ones) that
// isn't a *grpcstatus.Status, which this function treats identically to an
// OSAC-reported codes.Unknown via the catch-all case.
//
// Callers implementing a carve-out (e.g. Create's AlreadyExists
// interception per REQ-VMCREATE-070, Delete's NotFound tolerance per
// REQ-VMDELETE-020) MUST intercept those codes before calling Classify;
// this function applies no carve-out of its own.
func Classify(err error) (status int, errType v1alpha1.ErrorType, title string) {
	switch grpcstatus.Code(err) {
	case codes.InvalidArgument:
		return http.StatusBadRequest, v1alpha1.INVALIDARGUMENT, "Bad Request"
	case codes.Unauthenticated:
		return http.StatusUnauthorized, v1alpha1.UNAUTHENTICATED, "Unauthorized"
	case codes.PermissionDenied:
		return http.StatusForbidden, v1alpha1.PERMISSIONDENIED, "Forbidden"
	case codes.NotFound:
		return http.StatusNotFound, v1alpha1.NOTFOUND, "Not Found"
	case codes.AlreadyExists:
		return http.StatusConflict, v1alpha1.ALREADYEXISTS, "Conflict"
	case codes.Unavailable, codes.DeadlineExceeded:
		return http.StatusBadGateway, v1alpha1.UNAVAILABLE, "Bad Gateway"
	default:
		return http.StatusInternalServerError, v1alpha1.INTERNAL, httperror.InternalTitle
	}
}

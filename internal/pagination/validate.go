// Package pagination contains validation shared by the DCM-facing list
// endpoints.
package pagination

import (
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

const (
	minPageSize int32 = 1
	maxPageSize int32 = 100
)

// ValidateMaxPageSize validates the optional DCM/AEP-132 max_page_size
// parameter. An omitted parameter is valid and lets the caller apply its
// endpoint-specific default. Invalid values are returned as gRPC
// InvalidArgument so the existing REST error mappers produce HTTP 400.
func ValidateMaxPageSize(value *int32) error {
	if value == nil {
		return nil
	}
	if *value < minPageSize || *value > maxPageSize {
		return grpcstatus.Errorf(codes.InvalidArgument, "max_page_size must be between %d and %d", minPageSize, maxPageSize)
	}
	return nil
}

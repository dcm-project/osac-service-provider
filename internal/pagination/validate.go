// Package pagination contains validation shared by the DCM-facing list
// endpoints.
package pagination

import (
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

const maxPageSize int32 = 100

// NormalizeMaxPageSize applies the DCM list pagination contract from AEP-158.
// An omitted or zero value uses defaultPageSize, values above the maximum are
// coerced to the maximum, and negative values are returned as gRPC
// InvalidArgument so the existing REST error mappers produce HTTP 400.
func NormalizeMaxPageSize(value *int32, defaultPageSize int32) (int32, error) {
	if value == nil || *value == 0 {
		return defaultPageSize, nil
	}
	if *value < 0 {
		return 0, grpcstatus.Error(codes.InvalidArgument, "max_page_size must not be negative")
	}
	if *value > maxPageSize {
		return maxPageSize, nil
	}
	return *value, nil
}

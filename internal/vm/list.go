package vm

import (
	"context"
	"encoding/base64"
	"math"
	"strconv"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/util"
)

// ownershipFilter is the CEL filter always applied to ComputeInstances/List
// (REQ-VMLIST-010) — not caller-configurable.
const ownershipFilter = `this.metadata.labels["dcm.io/managed-by"] == "dcm"`

// defaultPageSize is used when max_page_size is omitted (REQ-VMLIST-020).
const defaultPageSize int32 = 50

// List calls ComputeInstances/List with the ownership filter and
// translates max_page_size/page_token to/from OSAC's limit/offset
// pagination (REQ-VMLIST-020), mirroring internal/cluster's List exactly
// (same pagination contract, same opaque token encoding).
func (s *Service) List(ctx context.Context, params v1alpha1.ListVMsParams) (v1alpha1.VirtualMachineList, error) {
	limit := defaultPageSize
	if params.MaxPageSize != nil {
		limit = *params.MaxPageSize
	}

	var offset int32
	if params.PageToken != nil && *params.PageToken != "" {
		var err error
		offset, err = decodePageToken(*params.PageToken)
		if err != nil {
			return v1alpha1.VirtualMachineList{}, err
		}
	}

	resp, err := s.computeInstances.List(ctx, &publicv1.ComputeInstancesListRequest{
		Filter: util.Ptr(ownershipFilter),
		Limit:  util.Ptr(limit),
		Offset: util.Ptr(offset),
	})
	if err != nil {
		return v1alpha1.VirtualMachineList{}, err
	}

	results := make([]v1alpha1.VirtualMachine, 0, len(resp.GetItems()))
	for _, item := range resp.GetItems() {
		results = append(results, toAPIVM(item))
	}

	list := v1alpha1.VirtualMachineList{Results: results}

	// REQ-VMLIST-040: next_page_token is present exactly when there are
	// further results beyond this page, not merely when this page is
	// short. Advances using len(results) actually received, not the
	// server-reported resp.GetSize() (DD-134) — see internal/cluster's
	// List (same fix, same rationale).
	if len(results) > 0 {
		nextOffset := offset + int32(len(results)) //nolint:gosec // see internal/cluster's List (same fix, same rationale): len(results) never exceeds the already-int32 requested limit
		if nextOffset < resp.GetTotal() {
			list.NextPageToken = util.Ptr(encodePageToken(nextOffset))
		}
	}
	return list, nil
}

// encodePageToken/decodePageToken wrap OSAC's offset as an opaque token
// (REQ-VMLIST-020) — callers must not infer meaning from its contents.
func encodePageToken(offset int32) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(int(offset))))
}

// decodePageToken returns a synthetic gRPC InvalidArgument error on a
// malformed token — mapError needs a real gRPC code to route this to 400
// rather than falling through to its default 500 case.
func decodePageToken(token string) (int32, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, grpcstatus.Errorf(codes.InvalidArgument, "invalid page_token: %v", err)
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil {
		return 0, grpcstatus.Errorf(codes.InvalidArgument, "invalid page_token: %v", err)
	}
	// Reject a decoded value outside int32 range explicitly rather than
	// let the narrowing conversion below silently wrap it (gosec G109) —
	// see internal/cluster's decodePageToken (same fix, same rationale).
	if n < 0 || n > math.MaxInt32 {
		return 0, grpcstatus.Errorf(codes.InvalidArgument, "invalid page_token: offset out of range")
	}
	return int32(n), nil //nolint:gosec // range-checked immediately above; gosec's G109 pattern match doesn't see the manual bounds check
}

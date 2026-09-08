package cluster

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

// ownershipFilter is the CEL filter always applied to Clusters/List
// (REQ-LIST-010) — not caller-configurable.
const ownershipFilter = `this.metadata.labels["dcm.io/managed-by"] == "dcm"`

// defaultPageSize is used when max_page_size is omitted (REQ-LIST-020).
const defaultPageSize int32 = 50

// List calls Clusters/List with the ownership filter and translates
// max_page_size/page_token to/from OSAC's limit/offset pagination
// (REQ-LIST-020). Entries never populate kubeconfig (REQ-LIST-030) — List
// never calls GetKubeconfig.
func (s *Service) List(ctx context.Context, params v1alpha1.ListClustersParams) (v1alpha1.ClusterList, error) {
	limit := defaultPageSize
	if params.MaxPageSize != nil {
		limit = *params.MaxPageSize
	}

	var offset int32
	if params.PageToken != nil && *params.PageToken != "" {
		var err error
		offset, err = decodePageToken(*params.PageToken)
		if err != nil {
			return v1alpha1.ClusterList{}, err
		}
	}

	resp, err := s.client.List(ctx, &publicv1.ClustersListRequest{
		Filter: util.Ptr(ownershipFilter),
		Limit:  util.Ptr(limit),
		Offset: util.Ptr(offset),
	})
	if err != nil {
		return v1alpha1.ClusterList{}, err
	}

	results := make([]v1alpha1.Cluster, 0, len(resp.GetItems()))
	for _, item := range resp.GetItems() {
		results = append(results, toAPICluster(item, nil))
	}

	list := v1alpha1.ClusterList{Results: results}

	// REQ-LIST-040: next_page_token is present exactly when there are
	// further results beyond this page, not merely when this page is
	// short. Advances using len(results) actually received, not the
	// server-reported resp.GetSize() (DD-134) — a Size/Total mismatch
	// (e.g. Size=0 while Total>offset) would otherwise reissue the exact
	// same page_token, and a caller faithfully following pagination would
	// loop forever refetching the same page. An empty page can never make
	// progress, so it never emits a next_page_token regardless of Total.
	if len(results) > 0 {
		nextOffset := offset + int32(len(results)) //nolint:gosec // len(results) never exceeds the already-int32 requested limit; overflow would need ~2^31 total OSAC records
		if nextOffset < resp.GetTotal() {
			list.NextPageToken = util.Ptr(encodePageToken(nextOffset))
		}
	}
	return list, nil
}

// encodePageToken/decodePageToken wrap OSAC's offset as an opaque token
// (REQ-LIST-020) — callers must not infer meaning from its contents.
func encodePageToken(offset int32) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(int(offset))))
}

// decodePageToken returns a synthetic gRPC InvalidArgument error on a
// malformed token — mapError (§4.6) needs a real gRPC code to route this to
// 400 rather than falling through to its default 500 case.
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
	// a malformed/tampered token should map to the same InvalidArgument
	// as any other unparseable one, not an undefined offset.
	if n < 0 || n > math.MaxInt32 {
		return 0, grpcstatus.Errorf(codes.InvalidArgument, "invalid page_token: offset out of range")
	}
	return int32(n), nil //nolint:gosec // range-checked immediately above; gosec's G109 pattern match doesn't see the manual bounds check
}

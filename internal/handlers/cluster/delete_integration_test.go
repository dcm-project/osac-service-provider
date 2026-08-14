package cluster_test

import (
	"io"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// deleteCluster issues a real DELETE for cluster "X" — every Delete test in
// this package uses the same id.
func deleteCluster(f *integrationFixture) *http.Response {
	req, err := http.NewRequest(http.MethodDelete, f.URL("/api/v1alpha1/clusters/X"), nil) //nolint:noctx // test helper
	Expect(err).NotTo(HaveOccurred())
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	return resp
}

var _ = Describe("Cluster Delete (integration, real HTTP + router + bufconn OSAC fake)", func() {
	var f *integrationFixture

	BeforeEach(func() {
		f = newIntegrationFixture()
		DeferCleanup(f.Close)
	})

	// TC-I-230 (REQ-DELETE-010/040, AC-DELETE-010): Delete succeeds over
	// real HTTP with an empty body, without polling for confirmation.
	It("succeeds over real HTTP with an empty body and no confirmation poll (TC-I-230)", func() {
		resp := deleteCluster(f)
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusNoContent))
		b, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(b).To(BeEmpty())
		Expect(f.fake.GetCallCount()).To(Equal(0))
	})

	// TC-I-231 (REQ-DELETE-020, AC-DELETE-020): deleting an already-deleted
	// cluster is idempotent across two real, sequential HTTP requests.
	It("is idempotent across two real, sequential HTTP DELETE requests (TC-I-231)", func() {
		first := deleteCluster(f)
		Expect(first.StatusCode).To(Equal(http.StatusNoContent))
		_ = first.Body.Close()

		f.fake.deleteFunc = func(*publicv1.ClustersDeleteRequest) (*publicv1.ClustersDeleteResponse, error) {
			return nil, grpcstatus.Error(codes.NotFound, "no such cluster")
		}

		second := deleteCluster(f)
		defer func() { _ = second.Body.Close() }()
		Expect(second.StatusCode).To(Equal(http.StatusNoContent))
	})

	// TC-I-232 (REQ-DELETE-030, AC-DELETE-030): a genuine OSAC failure
	// during delete is not swallowed by the NotFound-tolerance carve-out.
	It("surfaces a genuine OSAC failure as 502, not swallowed by NotFound-tolerance (TC-I-232)", func() {
		f.fake.deleteFunc = func(*publicv1.ClustersDeleteRequest) (*publicv1.ClustersDeleteResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "osac unreachable")
		}

		resp := deleteCluster(f)
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusBadGateway))
	})
})

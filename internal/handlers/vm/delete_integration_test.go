package vm_test

import (
	"io"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// deleteVM issues a real DELETE for VM "X" — every Delete test in this
// package uses the same id.
func deleteVM(f *integrationFixture) *http.Response {
	req, err := http.NewRequest(http.MethodDelete, f.URL("/api/v1alpha1/vms/X"), nil) //nolint:noctx // test helper
	Expect(err).NotTo(HaveOccurred())
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	return resp
}

var _ = Describe("VM Delete (integration, real HTTP + router + bufconn OSAC fake)", func() {
	var f *integrationFixture

	BeforeEach(func() {
		f = newIntegrationFixture()
		DeferCleanup(f.Close)
	})

	// TC-I-330 (REQ-VMDELETE-010/040, AC-VMDELETE-010): Delete succeeds
	// over real HTTP with an empty body, without polling for confirmation.
	It("succeeds over real HTTP with an empty body and no confirmation poll (TC-I-330)", func() {
		resp := deleteVM(f)
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusNoContent))
		b, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(b).To(BeEmpty())
		Expect(f.fake.GetCallCount()).To(Equal(0))
	})

	// TC-I-331 (REQ-VMDELETE-020, AC-VMDELETE-020): deleting an
	// already-deleted VM is idempotent across two real, sequential HTTP
	// requests.
	It("is idempotent across two real, sequential HTTP DELETE requests (TC-I-331)", func() {
		first := deleteVM(f)
		Expect(first.StatusCode).To(Equal(http.StatusNoContent))
		_ = first.Body.Close()

		f.fake.deleteFunc = func(*publicv1.ComputeInstancesDeleteRequest) (*publicv1.ComputeInstancesDeleteResponse, error) {
			return nil, grpcstatus.Error(codes.NotFound, "no such compute instance")
		}

		second := deleteVM(f)
		defer func() { _ = second.Body.Close() }()
		Expect(second.StatusCode).To(Equal(http.StatusNoContent))
	})

	// TC-I-332 (REQ-VMDELETE-030, AC-VMDELETE-030): a genuine OSAC failure
	// during delete is not swallowed by the NotFound-tolerance carve-out.
	It("surfaces a genuine OSAC failure as 502, not swallowed by NotFound-tolerance (TC-I-332)", func() {
		f.fake.deleteFunc = func(*publicv1.ComputeInstancesDeleteRequest) (*publicv1.ComputeInstancesDeleteResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "osac unreachable")
		}

		resp := deleteVM(f)
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusBadGateway))
	})
})

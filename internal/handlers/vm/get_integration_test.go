package vm_test

import (
	"encoding/json"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// getVM issues a real GET for VM "X" — every Get test in this package uses
// the same id, since the fake OSAC server's behavior (not the id) is what
// varies per test.
func getVM(f *integrationFixture) *http.Response {
	resp, err := http.Get(f.URL("/api/v1alpha1/vms/X")) //nolint:noctx // test helper
	Expect(err).NotTo(HaveOccurred())
	return resp
}

var _ = Describe("VM Get (integration, real HTTP + router + bufconn OSAC fake)", func() {
	var f *integrationFixture

	BeforeEach(func() {
		f = newIntegrationFixture()
		DeferCleanup(f.Close)
	})

	// TC-I-310 (REQ-VMGET-010/030, AC-VMGET-010): Get returns exact status
	// and IP fields for a running VM over real HTTP.
	It("returns exact status and IP fields for a running VM over real HTTP (TC-I-310)", func() {
		f.fake.getFunc = func(req *publicv1.ComputeInstancesGetRequest) (*publicv1.ComputeInstancesGetResponse, error) {
			return &publicv1.ComputeInstancesGetResponse{Object: &publicv1.ComputeInstance{
				Id: req.GetId(),
				Status: &publicv1.ComputeInstanceStatus{
					State:             publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
					InternalIpAddress: "10.200.1.5",
					ExternalIpAddress: "",
				},
			}}, nil
		}

		resp := getVM(f)
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var vmObj v1alpha1.VirtualMachine
		Expect(json.NewDecoder(resp.Body).Decode(&vmObj)).To(Succeed())
		Expect(vmObj.Status).To(Equal(v1alpha1.VMStatusRUNNING))
		Expect(*vmObj.InternalIpAddress).To(Equal("10.200.1.5"))
		Expect(*vmObj.ExternalIpAddress).To(Equal(""))
	})

	// TC-I-311 (REQ-VMGET-020, AC-VMGET-020): Get returns 404 for a
	// nonexistent VM over real HTTP.
	It("returns 404 for a nonexistent VM over real HTTP (TC-I-311)", func() {
		f.fake.getFunc = func(*publicv1.ComputeInstancesGetRequest) (*publicv1.ComputeInstancesGetResponse, error) {
			return nil, grpcstatus.Error(codes.NotFound, "no such compute instance")
		}

		resp := getVM(f)
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		Expect(resp.Header.Get("Content-Type")).To(Equal("application/problem+json"))
		var body v1alpha1.Error
		Expect(json.NewDecoder(resp.Body).Decode(&body)).To(Succeed())
		Expect(body.Type).To(Equal(v1alpha1.ErrorTypeNOTFOUND))
	})
})

package vm_test

import (
	"encoding/json"
	"net/http"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// postCreate issues a real POST /api/v1alpha1/vms?id=X (every test in this
// file uses the same id) with body as the JSON payload — a string so tests
// can send deliberately-malformed/incomplete bodies for validation cases.
func postCreate(f *integrationFixture, body string) *http.Response {
	url := f.URL("/api/v1alpha1/vms?id=X")
	resp, err := http.Post(url, "application/json", strings.NewReader(body)) //nolint:noctx,gosec // test helper
	Expect(err).NotTo(HaveOccurred())
	return resp
}

const validCreateJSON = `{"spec":{"storage":{"disks":[{"name":"boot","capacity":"100GB"}]},"guest_os":{"type":"rhel-9"},"metadata":{"name":"foo"},"provider_hints":{"osac":{"template_id":"default-vm","instance_type":"standard-4-16"}}}}`

var _ = Describe("VM Create (integration, real HTTP + router + bufconn OSAC fakes)", func() {
	var f *integrationFixture

	BeforeEach(func() {
		f = newIntegrationFixture()
		DeferCleanup(f.Close)
	})

	// TC-I-300 (REQ-VMCREATE-010/020/050/080, AC-VMCREATE-010/020): Create
	// succeeds end-to-end over real HTTP with the exact translated fields
	// independently observable both in the HTTP response and at the fake
	// OSAC server.
	It("succeeds end-to-end over real HTTP (TC-I-300)", func() {
		resp := postCreate(f, validCreateJSON)
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusCreated))
		var vmObj v1alpha1.VirtualMachine
		Expect(json.NewDecoder(resp.Body).Decode(&vmObj)).To(Succeed())
		Expect(*vmObj.Id).To(Equal("X"))
		Expect(vmObj.Status).To(Equal(v1alpha1.VMStatusPROVISIONING))

		Expect(f.fake.CreateCallCount()).To(Equal(1))
		req := f.fake.LastCreateCall()
		obj := req.GetObject()
		Expect(obj.GetId()).To(Equal("X"))
		Expect(obj.GetSpec().GetTemplate().GetId()).To(Equal("default-vm"))
		Expect(obj.GetSpec().GetInstanceType().GetId()).To(Equal("standard-4-16"))
		Expect(obj.GetSpec().GetImage().GetSourceRef()).To(Equal("rhel-9"))
		Expect(obj.GetSpec().GetBootDisk().GetSizeGib()).To(Equal(int32(100)))
		Expect(obj.GetMetadata().GetLabels()).To(Equal(map[string]string{
			"dcm.io/managed-by":   "dcm",
			"dcm.io/instance-id":  "X",
			"dcm.io/service-type": "vm",
		}))
	})

	// TC-I-301 (REQ-VMCREATE-070, AC-VMCREATE-070): two real, sequential
	// HTTP requests with the same id return the same resource, not a
	// duplicate.
	It("is idempotent across two real, sequential HTTP requests with the same id (TC-I-301)", func() {
		first := postCreate(f, validCreateJSON)
		Expect(first.StatusCode).To(Equal(http.StatusCreated))
		_ = first.Body.Close()

		// The fake's default Create behavior always succeeds; make the
		// second Create call for the same id fail with AlreadyExists,
		// exactly as a real OSAC server would for a retried id, and have
		// Get return the first call's now-persisted state.
		f.fake.createFunc = func(*publicv1.ComputeInstancesCreateRequest) (*publicv1.ComputeInstancesCreateResponse, error) {
			return nil, grpcstatus.Error(codes.AlreadyExists, "compute instance X already exists")
		}
		f.fake.getFunc = func(req *publicv1.ComputeInstancesGetRequest) (*publicv1.ComputeInstancesGetResponse, error) {
			return &publicv1.ComputeInstancesGetResponse{Object: &publicv1.ComputeInstance{
				Id:     req.GetId(),
				Status: &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING},
			}}, nil
		}

		second := postCreate(f, validCreateJSON)
		defer func() { _ = second.Body.Close() }()
		Expect(second.StatusCode).To(Equal(http.StatusCreated))

		var vmObj v1alpha1.VirtualMachine
		Expect(json.NewDecoder(second.Body).Decode(&vmObj)).To(Succeed())
		Expect(*vmObj.Id).To(Equal("X"))
		Expect(vmObj.Status).To(Equal(v1alpha1.VMStatusPROVISIONING))
		Expect(f.fake.GetCallCount()).To(Equal(1))
	})

	// TC-I-302 (REQ-VMCREATE-060, AC-VMCREATE-040/050/060): request
	// validation is enforced at the real HTTP boundary for each
	// independently-documented required field.
	It("rejects a missing id query parameter at the real HTTP boundary (TC-I-302a)", func() {
		resp, err := http.Post(f.URL("/api/v1alpha1/vms"), "application/json", strings.NewReader(validCreateJSON)) //nolint:noctx,gosec // test helper
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	It("rejects a missing boot disk at the real HTTP boundary (TC-I-302b)", func() {
		body := `{"spec":{"storage":{"disks":[{"name":"data","capacity":"100GB"}]},"guest_os":{"type":"rhel-9"},"metadata":{"name":"foo"},"provider_hints":{"osac":{"template_id":"default-vm","instance_type":"standard-4-16"}}}}`
		resp := postCreate(f, body)
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	It("rejects an unparseable disk capacity at the real HTTP boundary (TC-I-302c)", func() {
		body := `{"spec":{"storage":{"disks":[{"name":"boot","capacity":"100XB"}]},"guest_os":{"type":"rhel-9"},"metadata":{"name":"foo"},"provider_hints":{"osac":{"template_id":"default-vm","instance_type":"standard-4-16"}}}}`
		resp := postCreate(f, body)
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	It("rejects a missing provider_hints.osac.instance_type at the real HTTP boundary (TC-I-302d)", func() {
		body := `{"spec":{"storage":{"disks":[{"name":"boot","capacity":"100GB"}]},"guest_os":{"type":"rhel-9"},"metadata":{"name":"foo"},"provider_hints":{"osac":{"template_id":"default-vm"}}}}`
		resp := postCreate(f, body)
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// TC-I-303 (REQ-VMCREATE-030/090, AC-VMCREATE-030/080): disk
	// translation and network attachment are correct over real HTTP.
	It("translates non-boot/boot disks and resolves the network attachment correctly, over real HTTP (TC-I-303)", func() {
		body := `{"spec":{"storage":{"disks":[{"name":"data","capacity":"2TB"},{"name":"boot","capacity":"100GB"}]},"guest_os":{"type":"rhel-9"},"metadata":{"name":"foo"},"provider_hints":{"osac":{"template_id":"default-vm","instance_type":"standard-4-16"}}}}`
		resp := postCreate(f, body)
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		osacSpec := f.fake.LastCreateCall().GetObject().GetSpec()
		Expect(osacSpec.GetBootDisk().GetSizeGib()).To(Equal(int32(100)))
		Expect(osacSpec.GetAdditionalDisks()).To(HaveLen(1))
		Expect(osacSpec.GetAdditionalDisks()[0].GetSizeGib()).To(Equal(int32(2048)))

		attachments := osacSpec.GetNetworkAttachments()
		Expect(attachments).To(HaveLen(1))
		Expect(attachments[0].GetSubnet().GetId()).To(Equal("subnet-existing"))
		Expect(attachments[0].GetSecurityGroups()).To(BeEmpty())
	})
})

package vm_test

import (
	"context"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/util"
)

// validCreateBody is a fully-populated, valid CreateVMJSONRequestBody
// satisfying every REQ-VMCREATE-060 required field. Individual tests
// mutate a copy to omit exactly one field. Spec is a pointer (DD-125: the
// body's "spec" property is schema-optional per AEP-133, even though this
// SP treats an absent one as a validation failure, not silent success).
func validCreateBody() v1alpha1.CreateVMJSONRequestBody {
	return v1alpha1.CreateVMJSONRequestBody{
		Spec: &v1alpha1.VMSpec{
			Storage: v1alpha1.VMStorage{
				Disks: []v1alpha1.VMDisk{
					{Name: "boot", Capacity: "100GB"},
				},
			},
			GuestOs:  v1alpha1.VMGuestOS{Type: "rhel-9"},
			Metadata: v1alpha1.VMMetadata{Name: "foo"},
			ProviderHints: v1alpha1.VMProviderHints{
				Osac: v1alpha1.OSACVMProviderHints{
					TemplateId:   "default-vm",
					InstanceType: "standard-4-16",
				},
			},
		},
	}
}

var _ = Describe("Handler.CreateVM request validation (Topic 1 VM Create)", func() {
	var f *fixture

	BeforeEach(func() {
		f = newFixture()
		DeferCleanup(f.Close)
	})

	// TC-U-303 (REQ-VMCREATE-060): a missing id query parameter (nil) is
	// rejected before calling OSAC.
	It("rejects a wholly-absent id before calling OSAC (TC-U-303)", func() {
		req := oapigen.CreateVMRequestObject{
			Params: v1alpha1.CreateVMParams{Id: nil},
			Body:   ptrBody(validCreateBody()),
		}

		resp, err := f.handler.CreateVM(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())

		rec := httptest.NewRecorder()
		Expect(resp.VisitCreateVMResponse(rec)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// Supplementary to TC-U-303: an id present but empty (`?id=`) is a
	// distinct wire shape from a wholly-absent one and must be rejected
	// identically.
	It("rejects a present-but-empty id before calling OSAC", func() {
		req := oapigen.CreateVMRequestObject{
			Params: v1alpha1.CreateVMParams{Id: util.Ptr("")},
			Body:   ptrBody(validCreateBody()),
		}

		resp, err := f.handler.CreateVM(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())

		rec := httptest.NewRecorder()
		Expect(resp.VisitCreateVMResponse(rec)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// Supplementary to TC-U-303: a wholly-absent spec (nil) is rejected the
	// same as any other missing required field, not a nil-pointer panic.
	It("rejects a wholly-absent spec before calling OSAC", func() {
		req := oapigen.CreateVMRequestObject{
			Params: v1alpha1.CreateVMParams{Id: util.Ptr("X")},
			Body:   &v1alpha1.CreateVMJSONRequestBody{Spec: nil},
		}

		resp, err := f.handler.CreateVM(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())

		rec := httptest.NewRecorder()
		Expect(resp.VisitCreateVMResponse(rec)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// TC-U-303 (REQ-VMCREATE-060, AC-VMCREATE-040): a missing disk named
	// "boot" is rejected before calling OSAC, reachable through the
	// handler (internal/vm's own validation, proven again at this layer).
	It("rejects a spec with no disk named boot before calling OSAC (TC-U-303)", func() {
		body := validCreateBody()
		body.Spec.Storage.Disks = []v1alpha1.VMDisk{{Name: "data", Capacity: "100GB"}}

		req := oapigen.CreateVMRequestObject{
			Params: v1alpha1.CreateVMParams{Id: util.Ptr("X")},
			Body:   &body,
		}

		resp, err := f.handler.CreateVM(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())

		rec := httptest.NewRecorder()
		Expect(resp.VisitCreateVMResponse(rec)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// TC-U-304 (REQ-VMCREATE-040/060, AC-VMCREATE-050): an unparseable or
	// invalid boot disk capacity is rejected before calling OSAC, reachable
	// through the handler.
	DescribeTable("rejects an unparseable/invalid boot disk capacity before calling OSAC (TC-U-304)",
		func(capacity string) {
			body := validCreateBody()
			body.Spec.Storage.Disks = []v1alpha1.VMDisk{{Name: "boot", Capacity: capacity}}

			req := oapigen.CreateVMRequestObject{
				Params: v1alpha1.CreateVMParams{Id: util.Ptr("X")},
				Body:   &body,
			}

			resp, err := f.handler.CreateVM(context.Background(), req)
			Expect(err).NotTo(HaveOccurred())

			rec := httptest.NewRecorder()
			Expect(resp.VisitCreateVMResponse(rec)).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusBadRequest))
			Expect(f.fake.CreateCallCount()).To(Equal(0))
		},
		Entry("no unit", "100"),
		Entry("unrecognized unit", "100XB"),
		Entry("non-positive", "-5GB"),
	)

	// TC-U-306 (REQ-VMCREATE-060, AC-VMCREATE-060): a missing
	// provider_hints.osac.instance_type is rejected before calling OSAC —
	// proving no direct cores/memory_gib fallback exists (DD-122).
	It("rejects a request missing provider_hints.osac.instance_type before calling OSAC (TC-U-306)", func() {
		body := validCreateBody()
		body.Spec.ProviderHints.Osac.InstanceType = ""

		req := oapigen.CreateVMRequestObject{
			Params: v1alpha1.CreateVMParams{Id: util.Ptr("X")},
			Body:   &body,
		}

		resp, err := f.handler.CreateVM(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())

		rec := httptest.NewRecorder()
		Expect(resp.VisitCreateVMResponse(rec)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// Supplementary to TC-U-306 (REQ-VMCREATE-060): a missing
	// provider_hints.osac.template_id is rejected the same way.
	It("rejects a request missing provider_hints.osac.template_id before calling OSAC", func() {
		body := validCreateBody()
		body.Spec.ProviderHints.Osac.TemplateId = ""

		req := oapigen.CreateVMRequestObject{
			Params: v1alpha1.CreateVMParams{Id: util.Ptr("X")},
			Body:   &body,
		}

		resp, err := f.handler.CreateVM(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())

		rec := httptest.NewRecorder()
		Expect(resp.VisitCreateVMResponse(rec)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// Supplementary (REQ-VMERR-010/030 precondition): a genuine OSAC
	// Create failure (not the AlreadyExists carve-out, already covered by
	// internal/vm's own TC-U tests) is mapped through the shared
	// grpcerror.Classify, proving CreateVM's error path actually reaches
	// it.
	It("maps a genuine (non-AlreadyExists) OSAC Create failure through the shared error mapper", func() {
		f.fake.createFunc = func(*publicv1.ComputeInstancesCreateRequest) (*publicv1.ComputeInstancesCreateResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "osac unreachable")
		}

		req := oapigen.CreateVMRequestObject{
			Params: v1alpha1.CreateVMParams{Id: util.Ptr("X")},
			Body:   ptrBody(validCreateBody()),
		}
		resp, err := f.handler.CreateVM(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())

		rec := httptest.NewRecorder()
		Expect(resp.VisitCreateVMResponse(rec)).To(Succeed())
		Expect(rec.Code).To(Equal(http.StatusBadGateway))
	})
})

func ptrBody(b v1alpha1.CreateVMJSONRequestBody) *v1alpha1.CreateVMJSONRequestBody {
	return &b
}

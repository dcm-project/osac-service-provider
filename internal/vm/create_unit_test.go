package vm_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/util"
)

// baseSpec returns a fully-populated, valid VMSpec satisfying every
// REQ-VMCREATE-060 required field, matching AC-VMCREATE-010's fixture
// values (id="X", boot disk "100GB", guest_os.type="rhel-9",
// metadata.name="foo", provider_hints.osac.{template_id="default-vm",
// instance_type="standard-4-16"}). Individual tests mutate a copy to
// exercise one dimension at a time.
func baseSpec() v1alpha1.VMSpec {
	return v1alpha1.VMSpec{
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
	}
}

var _ = Describe("Service.Create (Topic 4.1 VM Create)", func() {
	var f *fixture

	BeforeEach(func() {
		f = newFixtureWithExistingSubnet()
		DeferCleanup(f.Close)
	})

	// TC-U-300 (REQ-VMCREATE-010/020, AC-VMCREATE-010): Create translates
	// and dispatches the full field set with exact values, and never
	// translates vcpu/memory (DD-122).
	It("translates the full field set and dispatches exact values to ComputeInstances/Create (TC-U-300)", func() {
		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).NotTo(HaveOccurred())

		Expect(f.fake.CreateCallCount()).To(Equal(1))
		req := f.fake.LastCreateCall()
		obj := req.GetObject()

		Expect(obj.GetId()).To(Equal("X"))
		Expect(obj.GetSpec().GetTemplate().GetId()).To(Equal("default-vm"))
		Expect(obj.GetSpec().GetInstanceType().GetId()).To(Equal("standard-4-16"))
		Expect(obj.GetSpec().GetImage().GetSourceRef()).To(Equal("rhel-9"))
		Expect(obj.GetSpec().GetBootDisk().GetSizeGib()).To(Equal(int32(100)))
		Expect(obj.GetMetadata().GetName()).To(Equal("foo"))
	})

	// TC-U-301 (REQ-VMCREATE-050, AC-VMCREATE-020): ownership labels are
	// set exactly, merged with (not replacing) caller-supplied labels.
	It("sets ownership labels exactly, merged with caller labels (TC-U-301)", func() {
		spec := baseSpec()
		spec.Metadata.Labels = &map[string]string{"team": "platform"}

		_, err := f.svc.Create(context.Background(), "X", spec)
		Expect(err).NotTo(HaveOccurred())

		labels := f.fake.LastCreateCall().GetObject().GetMetadata().GetLabels()
		Expect(labels).To(Equal(map[string]string{
			"team":                "platform",
			"dcm.io/managed-by":   "dcm",
			"dcm.io/instance-id":  "X",
			"dcm.io/service-type": "vm",
		}))
	})

	// TC-U-302 (REQ-VMCREATE-030/040, AC-VMCREATE-030): non-boot disks
	// translate to additional_disks, boot disk to boot_disk, both
	// size-parsed, regardless of the disks' order in the request.
	It("splits boot/non-boot disks and parses capacity units correctly (TC-U-302)", func() {
		spec := baseSpec()
		spec.Storage.Disks = []v1alpha1.VMDisk{
			{Name: "data", Capacity: "2TB"},
			{Name: "boot", Capacity: "100GB"},
		}

		_, err := f.svc.Create(context.Background(), "X", spec)
		Expect(err).NotTo(HaveOccurred())

		osacSpec := f.fake.LastCreateCall().GetObject().GetSpec()
		Expect(osacSpec.GetBootDisk().GetSizeGib()).To(Equal(int32(100)))
		Expect(osacSpec.GetAdditionalDisks()).To(HaveLen(1))
		Expect(osacSpec.GetAdditionalDisks()[0].GetSizeGib()).To(Equal(int32(2048)))
	})

	// TC-U-307 (REQ-VMCREATE-070, AC-VMCREATE-070): AlreadyExists on
	// Create triggers a Get and returns the existing resource, not a new
	// one.
	It("returns the existing resource via Get when Create reports AlreadyExists (TC-U-307)", func() {
		f.fake.createFunc = func(*publicv1.ComputeInstancesCreateRequest) (*publicv1.ComputeInstancesCreateResponse, error) {
			return nil, grpcstatus.Error(codes.AlreadyExists, "compute instance X already exists")
		}
		f.fake.getFunc = func(req *publicv1.ComputeInstancesGetRequest) (*publicv1.ComputeInstancesGetResponse, error) {
			return &publicv1.ComputeInstancesGetResponse{Object: &publicv1.ComputeInstance{
				Id:     req.GetId(),
				Status: &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING},
			}}, nil
		}

		result, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).NotTo(HaveOccurred())

		Expect(*result.Id).To(Equal("X"))
		Expect(result.Status).To(Equal(v1alpha1.PROVISIONING))
		Expect(f.fake.GetCallCount()).To(Equal(1))
	})

	// TC-U-308 (REQ-VMCREATE-090, AC-VMCREATE-080): every Create sets
	// exactly one network attachment, resolved from the default-network
	// step, with no caller-supplied networking fields at all.
	It("sets exactly one network attachment resolved by the default-network step (TC-U-308)", func() {
		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).NotTo(HaveOccurred())

		attachments := f.fake.LastCreateCall().GetObject().GetSpec().GetNetworkAttachments()
		Expect(attachments).To(HaveLen(1))
		Expect(attachments[0].GetSubnet().GetId()).To(Equal("subnet-existing"))
		Expect(attachments[0].GetSecurityGroups()).To(BeEmpty())
	})

	// Supplementary (REQ-VMERR-010/030 precondition): a Create failure
	// other than AlreadyExists is propagated raw, for the shared
	// error-mapping topic (§4.7) to translate.
	It("propagates a non-AlreadyExists Create error raw", func() {
		f.fake.createFunc = func(*publicv1.ComputeInstancesCreateRequest) (*publicv1.ComputeInstancesCreateResponse, error) {
			return nil, grpcstatus.Error(codes.InvalidArgument, "bad template")
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).To(HaveOccurred())

		st, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.InvalidArgument))
	})

	// Supplementary: if the AlreadyExists-recovery Get itself fails, that
	// failure is propagated raw rather than being swallowed.
	It("propagates the recovery Get's error when it fails after Create reports AlreadyExists", func() {
		f.fake.createFunc = func(*publicv1.ComputeInstancesCreateRequest) (*publicv1.ComputeInstancesCreateResponse, error) {
			return nil, grpcstatus.Error(codes.AlreadyExists, "compute instance X already exists")
		}
		f.fake.getFunc = func(*publicv1.ComputeInstancesGetRequest) (*publicv1.ComputeInstancesGetResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "osac unreachable")
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).To(HaveOccurred())

		st, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(st.Code()).To(Equal(codes.Unavailable))
	})

	// Supplementary (REQ-VMCREATE-060): a missing boot disk is rejected
	// before calling OSAC — the internal/vm-layer half of TC-U-303
	// (mirrored again at the handler layer against the full HTTP request
	// shape).
	It("rejects a spec with no disk named boot before calling OSAC", func() {
		spec := baseSpec()
		spec.Storage.Disks = []v1alpha1.VMDisk{{Name: "data", Capacity: "100GB"}}

		_, err := f.svc.Create(context.Background(), "X", spec)
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// Supplementary (Field Mapping table, §4.1): access.ssh_public_key and
	// provider_hints.osac.windows both pass through untranslated when set.
	It("passes through ssh_public_key and windows when set", func() {
		spec := baseSpec()
		spec.Access = &v1alpha1.VMAccess{SshPublicKey: util.Ptr("ssh-ed25519 AAAA...")}
		spec.ProviderHints.Osac.Windows = util.Ptr(true)

		_, err := f.svc.Create(context.Background(), "X", spec)
		Expect(err).NotTo(HaveOccurred())

		osacSpec := f.fake.LastCreateCall().GetObject().GetSpec()
		Expect(osacSpec.GetSshPublicKey()).To(Equal("ssh-ed25519 AAAA..."))
		Expect(osacSpec.GetIsWindows()).To(BeTrue())
	})

	// Supplementary (REQ-VMCREATE-040/060): an unparseable disk capacity
	// is rejected before calling OSAC — the internal/vm-layer half of
	// TC-U-304.
	DescribeTable("rejects an unparseable/invalid boot disk capacity before calling OSAC",
		func(capacity string) {
			spec := baseSpec()
			spec.Storage.Disks = []v1alpha1.VMDisk{{Name: "boot", Capacity: capacity}}

			_, err := f.svc.Create(context.Background(), "X", spec)
			Expect(err).To(HaveOccurred())
			Expect(grpcstatus.Code(err)).To(Equal(codes.InvalidArgument))
			Expect(f.fake.CreateCallCount()).To(Equal(0))
		},
		Entry("no unit", "100"),
		Entry("unrecognized unit", "100XB"),
		Entry("non-positive", "-5GB"),
	)
})

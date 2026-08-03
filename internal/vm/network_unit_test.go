package vm_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/vm"
)

// Default Network Provisioning (Topic 4.5, DD-084) has no dedicated public
// method — it's an internal step of Create (see the M4 spec §2 architecture
// diagram) — so these tests drive it exclusively through Create and assert
// against the fake Subnets/VirtualNetworks servers' recorded calls, exactly
// as each AC-VMNET-* is phrased ("When a VM Create request is processed").
var _ = Describe("Default Network Provisioning (Topic 4.5)", func() {
	// TC-U-340 (REQ-VMNET-010, AC-VMNET-010): an existing default subnet
	// is reused, no new network is created.
	It("reuses an existing default subnet without creating a new network (TC-U-340)", func() {
		f := newFixtureWithExistingSubnet()
		DeferCleanup(f.Close)

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).NotTo(HaveOccurred())

		Expect(f.vnets.CreateCallCount()).To(Equal(0))
		Expect(f.subnets.CreateCallCount()).To(Equal(0))
		attachments := f.fake.LastCreateCall().GetObject().GetSpec().GetNetworkAttachments()
		Expect(attachments).To(HaveLen(1))
		Expect(attachments[0].GetSubnet()).To(Equal("subnet-existing"))
	})

	// TC-U-341 (REQ-VMNET-020/030, AC-VMNET-020): no existing subnet — a
	// new VirtualNetwork and Subnet are provisioned with the exact
	// documented shape.
	It("provisions a new VirtualNetwork and Subnet with the documented shape when none exists (TC-U-341)", func() {
		f := newFixtureNoSubnet()
		DeferCleanup(f.Close)

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).NotTo(HaveOccurred())

		vnetReq := f.vnets.LastCreateCall()
		Expect(vnetReq.GetObject().GetSpec().GetIpv4Cidr()).To(Equal("10.200.0.0/16"))
		Expect(vnetReq.GetObject().GetSpec().GetNetworkClass()).To(Equal(""))
		Expect(vnetReq.GetObject().GetMetadata().GetLabels()).To(HaveKeyWithValue("dcm.io/managed-by", "dcm"))

		newVnetID := "vnet-new"
		subnetReq := f.subnets.LastCreateCall()
		Expect(subnetReq.GetObject().GetSpec().GetVirtualNetwork()).To(Equal(newVnetID))
		Expect(subnetReq.GetObject().GetSpec().GetIpv4Cidr()).To(Equal("10.200.1.0/24"))
		Expect(subnetReq.GetObject().GetMetadata().GetAnnotations()).To(HaveKeyWithValue("osac.openshift.io/owner-reference", newVnetID))
	})

	// TC-U-342 (REQ-VMNET-040, AC-VMNET-030): the SP polls (via Get) until
	// both resources report READY before creating the VM.
	It("polls until both new resources report READY before creating the VM (TC-U-342)", func() {
		f := newFixtureNoSubnet()
		DeferCleanup(f.Close)

		callCount := 0
		f.vnets.getFunc = func(req *publicv1.VirtualNetworksGetRequest) (*publicv1.VirtualNetworksGetResponse, error) {
			callCount++
			state := publicv1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING
			if callCount >= 2 {
				state = publicv1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY
			}
			return &publicv1.VirtualNetworksGetResponse{Object: &publicv1.VirtualNetwork{
				Id:     req.GetId(),
				Status: &publicv1.VirtualNetworkStatus{State: state},
			}}, nil
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).NotTo(HaveOccurred())

		Expect(f.vnets.GetCallCount()).To(Equal(2))
		Expect(f.fake.CreateCallCount()).To(Equal(1))
	})

	// TC-U-343 (REQ-VMNET-040, AC-VMNET-040): provisioning timeout is
	// surfaced as an error mapping to 502, ComputeInstances/Create is
	// never called. Poll interval/timeout overridden to short test values
	// via constructor options — not the real 15s/500ms constants.
	It("returns an error and never calls ComputeInstances/Create on provisioning timeout (TC-U-343)", func() {
		f := newFixtureNoSubnet(
			vm.WithNetworkPollInterval(2*time.Millisecond),
			vm.WithNetworkPollTimeout(20*time.Millisecond),
		)
		DeferCleanup(f.Close)

		f.subnets.getFunc = func(req *publicv1.SubnetsGetRequest) (*publicv1.SubnetsGetResponse, error) {
			return &publicv1.SubnetsGetResponse{Object: &publicv1.Subnet{
				Id:     req.GetId(),
				Status: &publicv1.SubnetStatus{State: publicv1.SubnetState_SUBNET_STATE_PENDING},
			}}, nil
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).To(HaveOccurred())
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// Supplementary (REQ-VMERR-010/030 precondition): every RPC error in
	// the §4.5 chain is propagated raw, without calling
	// ComputeInstances/Create, for the shared error-mapping topic to
	// translate.
	It("propagates a Subnets/List error raw without calling ComputeInstances/Create", func() {
		f := newFixtureNoSubnet()
		DeferCleanup(f.Close)
		f.subnets.listFunc = func(*publicv1.SubnetsListRequest) (*publicv1.SubnetsListResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "osac unreachable")
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).To(HaveOccurred())
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	It("propagates a VirtualNetworks/Create error raw without calling ComputeInstances/Create", func() {
		f := newFixtureNoSubnet()
		DeferCleanup(f.Close)
		f.vnets.createFunc = func(*publicv1.VirtualNetworksCreateRequest) (*publicv1.VirtualNetworksCreateResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "osac unreachable")
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).To(HaveOccurred())
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	It("propagates a VirtualNetworks/Get (poll) error raw without calling ComputeInstances/Create", func() {
		f := newFixtureNoSubnet()
		DeferCleanup(f.Close)
		f.vnets.getFunc = func(*publicv1.VirtualNetworksGetRequest) (*publicv1.VirtualNetworksGetResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "osac unreachable")
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).To(HaveOccurred())
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	It("propagates a Subnets/Create error raw without calling ComputeInstances/Create", func() {
		f := newFixtureNoSubnet()
		DeferCleanup(f.Close)
		f.subnets.createFunc = func(*publicv1.SubnetsCreateRequest) (*publicv1.SubnetsCreateResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "osac unreachable")
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).To(HaveOccurred())
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	It("propagates a Subnets/Get (poll) error raw without calling ComputeInstances/Create", func() {
		f := newFixtureNoSubnet()
		DeferCleanup(f.Close)
		f.subnets.getFunc = func(*publicv1.SubnetsGetRequest) (*publicv1.SubnetsGetResponse, error) {
			return nil, grpcstatus.Error(codes.Unavailable, "osac unreachable")
		}

		_, err := f.svc.Create(context.Background(), "X", baseSpec())
		Expect(err).To(HaveOccurred())
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})

	// Supplementary (REQ-VMNET-040's "bounded by the request's context"
	// clause): cancelling the caller's context while polling stops the
	// wait immediately, without waiting out the full poll timeout.
	It("stops polling immediately when the request's context is cancelled", func() {
		f := newFixtureNoSubnet(vm.WithNetworkPollInterval(time.Hour), vm.WithNetworkPollTimeout(time.Hour))
		DeferCleanup(f.Close)
		f.vnets.getFunc = func(req *publicv1.VirtualNetworksGetRequest) (*publicv1.VirtualNetworksGetResponse, error) {
			return &publicv1.VirtualNetworksGetResponse{Object: &publicv1.VirtualNetwork{
				Id:     req.GetId(),
				Status: &publicv1.VirtualNetworkStatus{State: publicv1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING},
			}}, nil
		}

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()

		_, err := f.svc.Create(ctx, "X", baseSpec())
		Expect(err).To(HaveOccurred())
		Expect(f.fake.CreateCallCount()).To(Equal(0))
	})
})

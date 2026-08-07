// Package vm implements the OSAC Service Provider's VM CRUD business logic
// (Milestone 4), translating between DCM's VirtualMachine REST schema
// (api/v1alpha1) and osac.public.v1.ComputeInstances gRPC calls — the VM
// counterpart to Milestone 3's internal/cluster.
//
// Implements .ai/specs/osac-sp-m4-vm-crud.spec.md Topics 4.1-4.6.
package vm

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// defaultNetworkPollInterval/Timeout are REQ-VMNET-040's hardcoded polling
// constants (DD-124 — no new configuration is introduced for these).
// Overridable per-Service via WithNetworkPollInterval/WithNetworkPollTimeout,
// exclusively so tests can exercise the timeout path (AC-VMNET-040) without
// a real 15-second wait.
const (
	defaultNetworkPollInterval = 500 * time.Millisecond
	defaultNetworkPollTimeout  = 15 * time.Second
)

// Service implements VM Create/Get/List/Delete against OSAC's
// ComputeInstances gRPC service, plus the Default Network Provisioning
// step (§4.5) against Subnets/VirtualNetworks. Constructed from
// Bootstrap.Conn()-backed clients per DD-020 — no new Bootstrap accessor is
// added.
type Service struct {
	computeInstances publicv1.ComputeInstancesClient
	subnets          publicv1.SubnetsClient
	virtualNetworks  publicv1.VirtualNetworksClient

	networkPollInterval time.Duration
	networkPollTimeout  time.Duration
}

// Option configures a Service constructed via New.
type Option func(*Service)

// WithNetworkPollInterval overrides REQ-VMNET-040's default poll interval
// (500ms). Intended for tests only (AC-VMNET-030/040) — production wiring
// never sets this.
func WithNetworkPollInterval(d time.Duration) Option {
	return func(s *Service) { s.networkPollInterval = d }
}

// WithNetworkPollTimeout overrides REQ-VMNET-040's default poll timeout
// (15s). Intended for tests only (AC-VMNET-040) — production wiring never
// sets this.
func WithNetworkPollTimeout(d time.Duration) Option {
	return func(s *Service) { s.networkPollTimeout = d }
}

// New constructs a Service wrapping the given clients.
func New(computeInstances publicv1.ComputeInstancesClient, subnets publicv1.SubnetsClient, virtualNetworks publicv1.VirtualNetworksClient, opts ...Option) *Service {
	s := &Service{
		computeInstances:    computeInstances,
		subnets:             subnets,
		virtualNetworks:     virtualNetworks,
		networkPollInterval: defaultNetworkPollInterval,
		networkPollTimeout:  defaultNetworkPollTimeout,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Create translates spec into OSAC's ComputeInstanceSpec, resolves (or
// provisions) the default network attachment (§4.5), and calls
// ComputeInstances/Create with ComputeInstance.id set to id
// (REQ-VMCREATE-020). If OSAC reports AlreadyExists for id, Create instead
// calls ComputeInstances/Get(id) and returns that resource's current state
// (REQ-VMCREATE-070, mirrors DD-100).
//
// Field translation (including disk validation, REQ-VMCREATE-030/040/060)
// happens before any network attachment resolution or OSAC call, so an
// invalid spec never triggers a wasted Subnets/List RPC.
func (s *Service) Create(ctx context.Context, id string, spec v1alpha1.VMSpec) (v1alpha1.VirtualMachine, error) {
	obj, err := toOSACComputeInstance(id, spec)
	if err != nil {
		return v1alpha1.VirtualMachine{}, err
	}

	subnetID, err := s.resolveDefaultSubnet(ctx)
	if err != nil {
		return v1alpha1.VirtualMachine{}, err
	}
	obj.Spec.NetworkAttachments = []*publicv1.NetworkAttachment{{Subnet: &publicv1.SubnetLocalReference{Id: subnetID}}}

	resp, err := s.computeInstances.Create(ctx, &publicv1.ComputeInstancesCreateRequest{Object: obj})
	if err != nil {
		if grpcstatus.Code(err) == codes.AlreadyExists {
			getResp, getErr := s.computeInstances.Get(ctx, &publicv1.ComputeInstancesGetRequest{Id: id})
			if getErr != nil {
				return v1alpha1.VirtualMachine{}, getErr
			}
			return toAPIVM(getResp.GetObject()), nil
		}
		return v1alpha1.VirtualMachine{}, err
	}

	return toAPIVM(resp.GetObject()), nil
}

// Get calls ComputeInstances/Get(id) and maps the result via MapStatus,
// echoing internal/external IP addresses exactly (REQ-VMGET-030).
func (s *Service) Get(ctx context.Context, id string) (v1alpha1.VirtualMachine, error) {
	resp, err := s.computeInstances.Get(ctx, &publicv1.ComputeInstancesGetRequest{Id: id})
	if err != nil {
		return v1alpha1.VirtualMachine{}, err
	}
	return toAPIVM(resp.GetObject()), nil
}

// Delete calls ComputeInstances/Delete(id). A NotFound response is treated
// as success (REQ-VMDELETE-020), mirroring control-plane's own tolerance
// for this exact case (DD-120).
func (s *Service) Delete(ctx context.Context, id string) error {
	_, err := s.computeInstances.Delete(ctx, &publicv1.ComputeInstancesDeleteRequest{Id: id})
	if err != nil && grpcstatus.Code(err) != codes.NotFound {
		return err
	}
	return nil
}

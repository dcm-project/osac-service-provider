package vm_test

import (
	"context"
	"net"
	"sync"

	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/vm"
)

// fakeComputeInstancesServer is a real, wire-level fake implementing the
// generated ComputeInstancesServer interface (same bufconn technique as
// internal/cluster/fixture_test.go, Milestone 3). Each RPC records its
// request and, when a per-test override function isn't set, returns a
// permissive default response so tests only need to configure the behavior
// they actually care about.
type fakeComputeInstancesServer struct {
	publicv1.UnimplementedComputeInstancesServer

	mu sync.Mutex

	createFunc func(*publicv1.ComputeInstancesCreateRequest) (*publicv1.ComputeInstancesCreateResponse, error)
	getFunc    func(*publicv1.ComputeInstancesGetRequest) (*publicv1.ComputeInstancesGetResponse, error)
	listFunc   func(*publicv1.ComputeInstancesListRequest) (*publicv1.ComputeInstancesListResponse, error)
	deleteFunc func(*publicv1.ComputeInstancesDeleteRequest) (*publicv1.ComputeInstancesDeleteResponse, error)

	createCalls []*publicv1.ComputeInstancesCreateRequest
	getCalls    []*publicv1.ComputeInstancesGetRequest
	listCalls   []*publicv1.ComputeInstancesListRequest
	deleteCalls []*publicv1.ComputeInstancesDeleteRequest
}

func (s *fakeComputeInstancesServer) Create(_ context.Context, req *publicv1.ComputeInstancesCreateRequest) (*publicv1.ComputeInstancesCreateResponse, error) {
	s.mu.Lock()
	s.createCalls = append(s.createCalls, req)
	fn := s.createFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	created := req.GetObject()
	created.Status = &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING}
	return &publicv1.ComputeInstancesCreateResponse{Object: created}, nil
}

func (s *fakeComputeInstancesServer) Get(_ context.Context, req *publicv1.ComputeInstancesGetRequest) (*publicv1.ComputeInstancesGetResponse, error) {
	s.mu.Lock()
	s.getCalls = append(s.getCalls, req)
	fn := s.getFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return &publicv1.ComputeInstancesGetResponse{}, nil
}

func (s *fakeComputeInstancesServer) List(_ context.Context, req *publicv1.ComputeInstancesListRequest) (*publicv1.ComputeInstancesListResponse, error) {
	s.mu.Lock()
	s.listCalls = append(s.listCalls, req)
	fn := s.listFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return &publicv1.ComputeInstancesListResponse{}, nil
}

func (s *fakeComputeInstancesServer) Delete(_ context.Context, req *publicv1.ComputeInstancesDeleteRequest) (*publicv1.ComputeInstancesDeleteResponse, error) {
	s.mu.Lock()
	s.deleteCalls = append(s.deleteCalls, req)
	fn := s.deleteFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return &publicv1.ComputeInstancesDeleteResponse{}, nil
}

func (s *fakeComputeInstancesServer) CreateCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.createCalls)
}

func (s *fakeComputeInstancesServer) LastCreateCall() *publicv1.ComputeInstancesCreateRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.createCalls) == 0 {
		return nil
	}
	return s.createCalls[len(s.createCalls)-1]
}

func (s *fakeComputeInstancesServer) GetCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.getCalls)
}

func (s *fakeComputeInstancesServer) ListCalls() []*publicv1.ComputeInstancesListRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*publicv1.ComputeInstancesListRequest(nil), s.listCalls...)
}

func (s *fakeComputeInstancesServer) DeleteCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deleteCalls)
}

// fakeSubnetsServer is the bufconn fake for osac.public.v1.Subnets, used by
// the Default Network Provisioning topic (§4.5).
type fakeSubnetsServer struct {
	publicv1.UnimplementedSubnetsServer

	mu sync.Mutex

	listFunc   func(*publicv1.SubnetsListRequest) (*publicv1.SubnetsListResponse, error)
	createFunc func(*publicv1.SubnetsCreateRequest) (*publicv1.SubnetsCreateResponse, error)
	getFunc    func(*publicv1.SubnetsGetRequest) (*publicv1.SubnetsGetResponse, error)

	listCalls   []*publicv1.SubnetsListRequest
	createCalls []*publicv1.SubnetsCreateRequest
	getCalls    []*publicv1.SubnetsGetRequest
}

func (s *fakeSubnetsServer) List(_ context.Context, req *publicv1.SubnetsListRequest) (*publicv1.SubnetsListResponse, error) {
	s.mu.Lock()
	s.listCalls = append(s.listCalls, req)
	fn := s.listFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return &publicv1.SubnetsListResponse{}, nil
}

func (s *fakeSubnetsServer) Create(_ context.Context, req *publicv1.SubnetsCreateRequest) (*publicv1.SubnetsCreateResponse, error) {
	s.mu.Lock()
	s.createCalls = append(s.createCalls, req)
	fn := s.createFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	created := req.GetObject()
	created.Id = "subnet-new"
	created.Status = &publicv1.SubnetStatus{State: publicv1.SubnetState_SUBNET_STATE_READY}
	return &publicv1.SubnetsCreateResponse{Object: created}, nil
}

func (s *fakeSubnetsServer) Get(_ context.Context, req *publicv1.SubnetsGetRequest) (*publicv1.SubnetsGetResponse, error) {
	s.mu.Lock()
	s.getCalls = append(s.getCalls, req)
	fn := s.getFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return &publicv1.SubnetsGetResponse{Object: &publicv1.Subnet{
		Id:     req.GetId(),
		Status: &publicv1.SubnetStatus{State: publicv1.SubnetState_SUBNET_STATE_READY},
	}}, nil
}

func (s *fakeSubnetsServer) ListCalls() []*publicv1.SubnetsListRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*publicv1.SubnetsListRequest(nil), s.listCalls...)
}

func (s *fakeSubnetsServer) CreateCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.createCalls)
}

func (s *fakeSubnetsServer) LastCreateCall() *publicv1.SubnetsCreateRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.createCalls) == 0 {
		return nil
	}
	return s.createCalls[len(s.createCalls)-1]
}

func (s *fakeSubnetsServer) GetCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.getCalls)
}

// fakeVirtualNetworksServer is the bufconn fake for
// osac.public.v1.VirtualNetworks, used by the Default Network Provisioning
// topic (§4.5).
type fakeVirtualNetworksServer struct {
	publicv1.UnimplementedVirtualNetworksServer

	mu sync.Mutex

	createFunc func(*publicv1.VirtualNetworksCreateRequest) (*publicv1.VirtualNetworksCreateResponse, error)
	getFunc    func(*publicv1.VirtualNetworksGetRequest) (*publicv1.VirtualNetworksGetResponse, error)

	createCalls []*publicv1.VirtualNetworksCreateRequest
	getCalls    []*publicv1.VirtualNetworksGetRequest
}

func (s *fakeVirtualNetworksServer) Create(_ context.Context, req *publicv1.VirtualNetworksCreateRequest) (*publicv1.VirtualNetworksCreateResponse, error) {
	s.mu.Lock()
	s.createCalls = append(s.createCalls, req)
	fn := s.createFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	created := req.GetObject()
	created.Id = "vnet-new"
	created.Status = &publicv1.VirtualNetworkStatus{State: publicv1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY}
	return &publicv1.VirtualNetworksCreateResponse{Object: created}, nil
}

func (s *fakeVirtualNetworksServer) Get(_ context.Context, req *publicv1.VirtualNetworksGetRequest) (*publicv1.VirtualNetworksGetResponse, error) {
	s.mu.Lock()
	s.getCalls = append(s.getCalls, req)
	fn := s.getFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return &publicv1.VirtualNetworksGetResponse{Object: &publicv1.VirtualNetwork{
		Id:     req.GetId(),
		Status: &publicv1.VirtualNetworkStatus{State: publicv1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY},
	}}, nil
}

func (s *fakeVirtualNetworksServer) CreateCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.createCalls)
}

func (s *fakeVirtualNetworksServer) LastCreateCall() *publicv1.VirtualNetworksCreateRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.createCalls) == 0 {
		return nil
	}
	return s.createCalls[len(s.createCalls)-1]
}

func (s *fakeVirtualNetworksServer) GetCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.getCalls)
}

// fixture bundles a real, in-process gRPC server (bufconn-bound) hosting
// fake ComputeInstances/Subnets/VirtualNetworks servers, and a vm.Service
// dialed against it through real generated clients — no hand-rolled
// client-interface fakes, per the established M2/M3 convention.
type fixture struct {
	svc     *vm.Service
	fake    *fakeComputeInstancesServer
	subnets *fakeSubnetsServer
	vnets   *fakeVirtualNetworksServer
	conn    *grpc.ClientConn
	server  *grpc.Server
}

// newFixtureWithSubnet seeds the fake Subnets server with one READY subnet
// (id="subnet-existing") so Create's default-network resolution short
// circuits to REQ-VMNET-010's reuse path without every Create-focused test
// needing to know about §4.5's provisioning mechanics.
func newFixtureWithExistingSubnet(opts ...vm.Option) *fixture {
	f := newFixtureNoSubnet(opts...)
	f.subnets.listFunc = func(*publicv1.SubnetsListRequest) (*publicv1.SubnetsListResponse, error) {
		return &publicv1.SubnetsListResponse{
			Size: 1, Total: 1,
			Items: []*publicv1.Subnet{{
				Id:     "subnet-existing",
				Status: &publicv1.SubnetStatus{State: publicv1.SubnetState_SUBNET_STATE_READY},
			}},
		}, nil
	}
	return f
}

// newFixtureNoSubnet leaves the fake Subnets server empty (Subnets/List
// returns zero results), exercising §4.5's provisioning path on Create.
func newFixtureNoSubnet(opts ...vm.Option) *fixture {
	lis := bufconn.Listen(1024 * 1024)
	grpcSrv := grpc.NewServer()

	fake := &fakeComputeInstancesServer{}
	subnets := &fakeSubnetsServer{}
	vnets := &fakeVirtualNetworksServer{}
	publicv1.RegisterComputeInstancesServer(grpcSrv, fake)
	publicv1.RegisterSubnetsServer(grpcSrv, subnets)
	publicv1.RegisterVirtualNetworksServer(grpcSrv, vnets)
	go func() { _ = grpcSrv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	)
	Expect(err).NotTo(HaveOccurred())

	svc := vm.New(
		publicv1.NewComputeInstancesClient(conn),
		publicv1.NewSubnetsClient(conn),
		publicv1.NewVirtualNetworksClient(conn),
		opts...,
	)

	return &fixture{
		svc:     svc,
		fake:    fake,
		subnets: subnets,
		vnets:   vnets,
		conn:    conn,
		server:  grpcSrv,
	}
}

// newFixture is an alias for newFixtureWithExistingSubnet — the common case
// for tests (Get/List/Delete/Status) that don't exercise §4.5 at all and
// need Create-adjacent setup to just work.
func newFixture() *fixture {
	return newFixtureWithExistingSubnet()
}

func (f *fixture) Close() {
	_ = f.conn.Close()
	f.server.Stop()
}

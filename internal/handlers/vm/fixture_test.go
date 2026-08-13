package vm_test

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
	"github.com/dcm-project/osac-service-provider/internal/apiserver"
	"github.com/dcm-project/osac-service-provider/internal/config"
	vmhandlers "github.com/dcm-project/osac-service-provider/internal/handlers/vm"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	vmservice "github.com/dcm-project/osac-service-provider/internal/vm"
)

// fakeComputeInstancesServer is a real, wire-level fake implementing the
// generated ComputeInstancesServer interface (same bufconn technique as
// internal/vm/fixture_test.go, Milestone 4). Each RPC records its request
// and, when a per-test override function isn't set, returns a permissive
// default response so tests only need to configure the behavior they
// actually care about.
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

// fakeSubnetsServer is the bufconn fake for osac.public.v1.Subnets, needed
// transitively by every VM Create call (§4.5's Default Network
// Provisioning). Defaults to reporting one READY subnet so Create-adjacent
// tests that don't care about §4.5 just work.
type fakeSubnetsServer struct {
	publicv1.UnimplementedSubnetsServer

	mu sync.Mutex

	listFunc   func(*publicv1.SubnetsListRequest) (*publicv1.SubnetsListResponse, error)
	createFunc func(*publicv1.SubnetsCreateRequest) (*publicv1.SubnetsCreateResponse, error)
	getFunc    func(*publicv1.SubnetsGetRequest) (*publicv1.SubnetsGetResponse, error)

	createCalls []*publicv1.SubnetsCreateRequest
}

func (s *fakeSubnetsServer) List(_ context.Context, req *publicv1.SubnetsListRequest) (*publicv1.SubnetsListResponse, error) {
	s.mu.Lock()
	fn := s.listFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return &publicv1.SubnetsListResponse{
		Size: 1, Total: 1,
		Items: []*publicv1.Subnet{{
			Id:     "subnet-existing",
			Status: &publicv1.SubnetStatus{State: publicv1.SubnetState_SUBNET_STATE_READY},
		}},
	}, nil
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

// CreateCallCount reports how many times Create has been invoked — used to
// assert the AC-VMNET-010 "no new network is created" negative outcome
// (TC-I-342), not just the positive "which subnet got attached" outcome.
func (s *fakeSubnetsServer) CreateCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.createCalls)
}

func (s *fakeSubnetsServer) Get(_ context.Context, req *publicv1.SubnetsGetRequest) (*publicv1.SubnetsGetResponse, error) {
	s.mu.Lock()
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

// fakeVirtualNetworksServer is the bufconn fake for
// osac.public.v1.VirtualNetworks, used by §4.5.
type fakeVirtualNetworksServer struct {
	publicv1.UnimplementedVirtualNetworksServer

	mu sync.Mutex

	createFunc func(*publicv1.VirtualNetworksCreateRequest) (*publicv1.VirtualNetworksCreateResponse, error)
	getFunc    func(*publicv1.VirtualNetworksGetRequest) (*publicv1.VirtualNetworksGetResponse, error)

	createCalls []*publicv1.VirtualNetworksCreateRequest
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

// CreateCallCount reports how many times Create has been invoked — used to
// assert the AC-VMNET-010 "no new network is created" negative outcome
// (TC-I-342), not just the positive "which subnet got attached" outcome.
func (s *fakeVirtualNetworksServer) CreateCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.createCalls)
}

func (s *fakeVirtualNetworksServer) Get(_ context.Context, req *publicv1.VirtualNetworksGetRequest) (*publicv1.VirtualNetworksGetResponse, error) {
	s.mu.Lock()
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

// discardLogger silences handler logging (e.g. httperror.WriteResponse's
// JSON-encode-failure branch) during tests.
var discardLogger = slog.New(slog.DiscardHandler)

// dialFakeOSAC starts a bufconn-bound gRPC server hosting fake
// ComputeInstances/Subnets/VirtualNetworks servers and returns a client
// connection dialed against it, plus the three fakes for assertions.
func dialFakeOSAC() (conn *grpc.ClientConn, server *grpc.Server, fake *fakeComputeInstancesServer, subnets *fakeSubnetsServer, vnets *fakeVirtualNetworksServer) {
	lis := bufconn.Listen(1024 * 1024)
	grpcSrv := grpc.NewServer()

	fake = &fakeComputeInstancesServer{}
	subnets = &fakeSubnetsServer{}
	vnets = &fakeVirtualNetworksServer{}
	publicv1.RegisterComputeInstancesServer(grpcSrv, fake)
	publicv1.RegisterSubnetsServer(grpcSrv, subnets)
	publicv1.RegisterVirtualNetworksServer(grpcSrv, vnets)
	go func() { _ = grpcSrv.Serve(lis) }()

	c, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	)
	Expect(err).NotTo(HaveOccurred())

	return c, grpcSrv, fake, subnets, vnets
}

// fixture bundles a bufconn-bound fake OSAC server trio, a real
// vmservice.Service dialed against it, and the real vmhandlers.Handler
// under test — for direct (non-HTTP) invocation of StrictServerInterface
// methods.
type fixture struct {
	handler *vmhandlers.Handler
	fake    *fakeComputeInstancesServer
	subnets *fakeSubnetsServer
	vnets   *fakeVirtualNetworksServer
	conn    *grpc.ClientConn
	server  *grpc.Server
}

func newFixture() *fixture {
	conn, server, fake, subnets, vnets := dialFakeOSAC()

	svc := vmservice.New(
		publicv1.NewComputeInstancesClient(conn),
		publicv1.NewSubnetsClient(conn),
		publicv1.NewVirtualNetworksClient(conn),
	)
	return &fixture{
		handler: vmhandlers.NewHandler(svc, discardLogger),
		fake:    fake,
		subnets: subnets,
		vnets:   vnets,
		conn:    conn,
		server:  server,
	}
}

func (f *fixture) Close() {
	_ = f.conn.Close()
	f.server.Stop()
}

// stubHealth stands in for internal/health.Handler's two StrictServerInterface
// methods so realHandler below satisfies the full oapigen.StrictServerInterface
// without pulling in internal/health — this package's tests don't exercise
// health at all.
type stubHealth struct{}

func (stubHealth) GetClustersHealth(context.Context, oapigen.GetClustersHealthRequestObject) (oapigen.GetClustersHealthResponseObject, error) {
	return nil, errors.New("not implemented")
}

func (stubHealth) GetVMsHealth(context.Context, oapigen.GetVMsHealthRequestObject) (oapigen.GetVMsHealthResponseObject, error) {
	return nil, errors.New("not implemented")
}

// stubCluster stands in for internal/handlers/cluster.Handler's 4
// StrictServerInterface methods (Cluster CRUD, added Milestone 3) so
// realHandler below satisfies the full interface without pulling in
// internal/handlers/cluster — this package's tests exercise only VM CRUD.
type stubCluster struct{}

func (stubCluster) ListClusters(context.Context, oapigen.ListClustersRequestObject) (oapigen.ListClustersResponseObject, error) {
	return nil, errors.New("not implemented")
}

func (stubCluster) CreateCluster(context.Context, oapigen.CreateClusterRequestObject) (oapigen.CreateClusterResponseObject, error) {
	return nil, errors.New("not implemented")
}

func (stubCluster) GetCluster(context.Context, oapigen.GetClusterRequestObject) (oapigen.GetClusterResponseObject, error) {
	return nil, errors.New("not implemented")
}

func (stubCluster) DeleteCluster(context.Context, oapigen.DeleteClusterRequestObject) (oapigen.DeleteClusterResponseObject, error) {
	return nil, errors.New("not implemented")
}

// realHandler combines the real vmhandlers.Handler with stubHealth and
// stubCluster so the result satisfies the full oapigen.StrictServerInterface,
// exactly as cmd/osac-service-provider/main.go's composite handler does.
type realHandler struct {
	*vmhandlers.Handler
	stubHealth
	stubCluster
}

// integrationFixture starts a real HTTP server (loopback listener, same
// pattern as internal/apiserver/server_integration_test.go) wired through
// the real chi router and strict adapter to the real vmhandlers.Handler,
// backed by the bufconn fake OSAC server trio — for TC-I-3xx.
type integrationFixture struct {
	addr    string
	fake    *fakeComputeInstancesServer
	subnets *fakeSubnetsServer
	vnets   *fakeVirtualNetworksServer
	conn    *grpc.ClientConn
	grpc    *grpc.Server
	cancel  context.CancelFunc
	done    <-chan error
}

func newIntegrationFixture(opts ...vmservice.Option) *integrationFixture {
	conn, grpcSrv, fake, subnets, vnets := dialFakeOSAC()

	svc := vmservice.New(
		publicv1.NewComputeInstancesClient(conn),
		publicv1.NewSubnetsClient(conn),
		publicv1.NewVirtualNetworksClient(conn),
		opts...,
	)
	h := &realHandler{Handler: vmhandlers.NewHandler(svc, discardLogger)}
	strict := oapigen.NewStrictHandlerWithOptions(h, nil, oapigen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  apiserver.NewRequestErrorHandler(discardLogger),
		ResponseErrorHandlerFunc: apiserver.NewResponseErrorHandler(discardLogger),
	})

	cfg := &config.Config{Server: config.ServerConfig{ShutdownTimeout: time.Second}}
	srv := apiserver.New(cfg, discardLogger, strict)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx, ln) }()

	addr := ln.Addr().String()
	Eventually(func() error {
		c, dialErr := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if dialErr == nil {
			_ = c.Close()
		}
		return dialErr
	}, "500ms", "5ms").Should(Succeed())

	return &integrationFixture{addr: addr, fake: fake, subnets: subnets, vnets: vnets, conn: conn, grpc: grpcSrv, cancel: cancel, done: done}
}

func (f *integrationFixture) URL(path string) string {
	return "http://" + f.addr + path
}

func (f *integrationFixture) Close() {
	f.cancel()
	Eventually(f.done, "2s").Should(Receive())
	_ = f.conn.Close()
	f.grpc.Stop()
}

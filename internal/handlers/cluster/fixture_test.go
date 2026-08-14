package cluster_test

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
	clusterservice "github.com/dcm-project/osac-service-provider/internal/cluster"
	"github.com/dcm-project/osac-service-provider/internal/config"
	clusterhandlers "github.com/dcm-project/osac-service-provider/internal/handlers/cluster"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/versionmatrix"
)

// fakeClustersServer is a real, wire-level fake implementing the generated
// ClustersServer interface (same bufconn technique as
// internal/cluster/fixture_test.go and Milestone 2's conn_unit_test.go).
type fakeClustersServer struct {
	publicv1.UnimplementedClustersServer

	mu sync.Mutex

	createFunc        func(*publicv1.ClustersCreateRequest) (*publicv1.ClustersCreateResponse, error)
	getFunc           func(*publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error)
	listFunc          func(*publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error)
	deleteFunc        func(*publicv1.ClustersDeleteRequest) (*publicv1.ClustersDeleteResponse, error)
	getKubeconfigFunc func(*publicv1.ClustersGetKubeconfigRequest) (*publicv1.ClustersGetKubeconfigResponse, error)

	createCalls []*publicv1.ClustersCreateRequest
	deleteCalls []*publicv1.ClustersDeleteRequest
	getCalls    []*publicv1.ClustersGetRequest
	listCalls   []*publicv1.ClustersListRequest
}

func (s *fakeClustersServer) Create(_ context.Context, req *publicv1.ClustersCreateRequest) (*publicv1.ClustersCreateResponse, error) {
	s.mu.Lock()
	s.createCalls = append(s.createCalls, req)
	fn := s.createFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	// A real OSAC server always reports a freshly created cluster as
	// PROGRESSING, never UNSPECIFIED — mirrored here so default-behavior
	// integration tests observe a realistic status without every test
	// needing its own createFunc override.
	created := req.GetObject()
	created.Status = &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_PROGRESSING}
	return &publicv1.ClustersCreateResponse{Object: created}, nil
}

func (s *fakeClustersServer) Get(_ context.Context, req *publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
	s.mu.Lock()
	s.getCalls = append(s.getCalls, req)
	fn := s.getFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return &publicv1.ClustersGetResponse{}, nil
}

func (s *fakeClustersServer) List(_ context.Context, req *publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error) {
	s.mu.Lock()
	s.listCalls = append(s.listCalls, req)
	fn := s.listFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return &publicv1.ClustersListResponse{}, nil
}

func (s *fakeClustersServer) Delete(_ context.Context, req *publicv1.ClustersDeleteRequest) (*publicv1.ClustersDeleteResponse, error) {
	s.mu.Lock()
	s.deleteCalls = append(s.deleteCalls, req)
	fn := s.deleteFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return &publicv1.ClustersDeleteResponse{}, nil
}

func (s *fakeClustersServer) GetKubeconfig(_ context.Context, req *publicv1.ClustersGetKubeconfigRequest) (*publicv1.ClustersGetKubeconfigResponse, error) {
	fn := s.getKubeconfigFunc
	if fn != nil {
		return fn(req)
	}
	return &publicv1.ClustersGetKubeconfigResponse{}, nil
}

func (s *fakeClustersServer) CreateCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.createCalls)
}

func (s *fakeClustersServer) LastCreateCall() *publicv1.ClustersCreateRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.createCalls) == 0 {
		return nil
	}
	return s.createCalls[len(s.createCalls)-1]
}

func (s *fakeClustersServer) DeleteCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deleteCalls)
}

func (s *fakeClustersServer) ListCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.listCalls)
}

func (s *fakeClustersServer) GetCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.getCalls)
}

// fakeClusterTemplatesServer backs Create's REQ-CREATE-080 node-set-key
// lookup for this package's handler-level tests, which don't otherwise
// care about node-set resolution — a single fixed key is enough here.
type fakeClusterTemplatesServer struct {
	publicv1.UnimplementedClusterTemplatesServer
}

func (s *fakeClusterTemplatesServer) Get(context.Context, *publicv1.ClusterTemplatesGetRequest) (*publicv1.ClusterTemplatesGetResponse, error) {
	return &publicv1.ClusterTemplatesGetResponse{Object: &publicv1.ClusterTemplate{
		NodeSets: map[string]*publicv1.ClusterTemplateNodeSet{"compute": {}},
	}}, nil
}

// discardLogger silences handler logging (e.g. httperror.WriteResponse's
// JSON-encode-failure branch) during tests.
var discardLogger = slog.New(slog.DiscardHandler)

// fixture bundles a bufconn-bound fake OSAC ClustersServer, a real
// cluster.Service dialed against it, and the real clusterhandlers.Handler
// under test — for direct (non-HTTP) invocation of StrictServerInterface
// methods (TC-U-205/206/250/251).
type fixture struct {
	handler *clusterhandlers.Handler
	fake    *fakeClustersServer
	conn    *grpc.ClientConn
	server  *grpc.Server
}

func newFixture() *fixture {
	return newFixtureWithMatrix(versionmatrix.DefaultMatrix)
}

// newFixtureWithMatrix is newFixture with an explicit matrix, for
// TC-U-530/531 (unsupported-version rejection against a matrix lacking the
// requested version).
func newFixtureWithMatrix(matrix versionmatrix.Matrix) *fixture {
	lis := bufconn.Listen(1024 * 1024)
	grpcSrv := grpc.NewServer()
	fake := &fakeClustersServer{}
	publicv1.RegisterClustersServer(grpcSrv, fake)
	publicv1.RegisterClusterTemplatesServer(grpcSrv, &fakeClusterTemplatesServer{})
	go func() { _ = grpcSrv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	)
	Expect(err).NotTo(HaveOccurred())

	svc := clusterservice.New(publicv1.NewClustersClient(conn), publicv1.NewClusterTemplatesClient(conn), matrix)
	return &fixture{
		handler: clusterhandlers.NewHandler(svc, discardLogger),
		fake:    fake,
		conn:    conn,
		server:  grpcSrv,
	}
}

func (f *fixture) Close() {
	_ = f.conn.Close()
	f.server.Stop()
}

// stubHealth stands in for internal/health.Handler's two StrictServerInterface
// methods so realHandler below satisfies the full interface without pulling
// in internal/health — this package's tests don't exercise health at all.
type stubHealth struct{}

func (stubHealth) GetClustersHealth(context.Context, oapigen.GetClustersHealthRequestObject) (oapigen.GetClustersHealthResponseObject, error) {
	return nil, errors.New("not implemented")
}

func (stubHealth) GetVMsHealth(context.Context, oapigen.GetVMsHealthRequestObject) (oapigen.GetVMsHealthResponseObject, error) {
	return nil, errors.New("not implemented")
}

// stubVM stands in for internal/handlers/vm.Handler's 4 StrictServerInterface
// methods (VM CRUD, added Milestone 4) so realHandler below satisfies the
// full interface without pulling in internal/handlers/vm — this package's
// tests exercise only Cluster CRUD.
type stubVM struct{}

func (stubVM) ListVMs(context.Context, oapigen.ListVMsRequestObject) (oapigen.ListVMsResponseObject, error) {
	return nil, errors.New("not implemented")
}

func (stubVM) CreateVM(context.Context, oapigen.CreateVMRequestObject) (oapigen.CreateVMResponseObject, error) {
	return nil, errors.New("not implemented")
}

func (stubVM) GetVM(context.Context, oapigen.GetVMRequestObject) (oapigen.GetVMResponseObject, error) {
	return nil, errors.New("not implemented")
}

func (stubVM) DeleteVM(context.Context, oapigen.DeleteVMRequestObject) (oapigen.DeleteVMResponseObject, error) {
	return nil, errors.New("not implemented")
}

// realHandler combines the real clusterhandlers.Handler with stubHealth and
// stubVM so the result satisfies the full oapigen.StrictServerInterface,
// exactly as cmd/osac-service-provider/main.go's composite handler does.
type realHandler struct {
	*clusterhandlers.Handler
	stubHealth
	stubVM
}

// integrationFixture starts a real HTTP server (loopback listener, same
// pattern as internal/apiserver/server_integration_test.go) wired through
// the real chi router and strict adapter to the real clusterhandlers.Handler,
// backed by a bufconn fake OSAC server — for TC-I-2xx.
type integrationFixture struct {
	addr   string
	fake   *fakeClustersServer
	conn   *grpc.ClientConn
	grpc   *grpc.Server
	cancel context.CancelFunc
	done   <-chan error
}

func newIntegrationFixture() *integrationFixture {
	return newIntegrationFixtureWithMatrix(versionmatrix.DefaultMatrix)
}

// newIntegrationFixtureWithMatrix is newIntegrationFixture with an
// explicit matrix, for TC-I-500/501/502.
func newIntegrationFixtureWithMatrix(matrix versionmatrix.Matrix) *integrationFixture {
	lis := bufconn.Listen(1024 * 1024)
	grpcSrv := grpc.NewServer()
	fake := &fakeClustersServer{}
	publicv1.RegisterClustersServer(grpcSrv, fake)
	publicv1.RegisterClusterTemplatesServer(grpcSrv, &fakeClusterTemplatesServer{})
	go func() { _ = grpcSrv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	)
	Expect(err).NotTo(HaveOccurred())

	svc := clusterservice.New(publicv1.NewClustersClient(conn), publicv1.NewClusterTemplatesClient(conn), matrix)
	h := &realHandler{Handler: clusterhandlers.NewHandler(svc, discardLogger)}
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

	return &integrationFixture{addr: addr, fake: fake, conn: conn, grpc: grpcSrv, cancel: cancel, done: done}
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

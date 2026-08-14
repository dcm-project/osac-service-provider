package cluster_test

import (
	"context"
	"net"
	"sync"

	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/dcm-project/osac-service-provider/internal/cluster"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/versionmatrix"
)

// fakeClustersServer is a real, wire-level fake implementing the generated
// ClustersServer interface (same bufconn technique as Milestone 2's
// internal/osac/conn_unit_test.go). Each RPC records its request and, when a
// per-test override function isn't set, returns a permissive default
// response so tests only need to configure the behavior they actually care
// about.
type fakeClustersServer struct {
	publicv1.UnimplementedClustersServer

	mu sync.Mutex

	createFunc        func(*publicv1.ClustersCreateRequest) (*publicv1.ClustersCreateResponse, error)
	getFunc           func(*publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error)
	listFunc          func(*publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error)
	deleteFunc        func(*publicv1.ClustersDeleteRequest) (*publicv1.ClustersDeleteResponse, error)
	getKubeconfigFunc func(*publicv1.ClustersGetKubeconfigRequest) (*publicv1.ClustersGetKubeconfigResponse, error)

	createCalls        []*publicv1.ClustersCreateRequest
	getCalls           []*publicv1.ClustersGetRequest
	listCalls          []*publicv1.ClustersListRequest
	deleteCalls        []*publicv1.ClustersDeleteRequest
	getKubeconfigCalls []*publicv1.ClustersGetKubeconfigRequest
}

func (s *fakeClustersServer) Create(_ context.Context, req *publicv1.ClustersCreateRequest) (*publicv1.ClustersCreateResponse, error) {
	s.mu.Lock()
	s.createCalls = append(s.createCalls, req)
	fn := s.createFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return &publicv1.ClustersCreateResponse{Object: req.GetObject()}, nil
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
	s.mu.Lock()
	s.getKubeconfigCalls = append(s.getKubeconfigCalls, req)
	fn := s.getKubeconfigFunc
	s.mu.Unlock()
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

func (s *fakeClustersServer) GetCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.getCalls)
}

func (s *fakeClustersServer) ListCalls() []*publicv1.ClustersListRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*publicv1.ClustersListRequest(nil), s.listCalls...)
}

func (s *fakeClustersServer) DeleteCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deleteCalls)
}

func (s *fakeClustersServer) GetKubeconfigCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.getKubeconfigCalls)
}

// fakeClusterTemplatesServer backs Create's REQ-CREATE-080 node-set-key
// lookup. Its default Get response defines a single node-set key
// ("compute") deliberately distinct from any templateID a test uses,
// proving Create never assumes key == templateID.
type fakeClusterTemplatesServer struct {
	publicv1.UnimplementedClusterTemplatesServer

	mu sync.Mutex

	getFunc func(*publicv1.ClusterTemplatesGetRequest) (*publicv1.ClusterTemplatesGetResponse, error)

	getCalls []*publicv1.ClusterTemplatesGetRequest
}

func (s *fakeClusterTemplatesServer) Get(_ context.Context, req *publicv1.ClusterTemplatesGetRequest) (*publicv1.ClusterTemplatesGetResponse, error) {
	s.mu.Lock()
	s.getCalls = append(s.getCalls, req)
	fn := s.getFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return &publicv1.ClusterTemplatesGetResponse{Object: &publicv1.ClusterTemplate{
		NodeSets: map[string]*publicv1.ClusterTemplateNodeSet{defaultNodeSetKey: {}},
	}}, nil
}

func (s *fakeClusterTemplatesServer) GetCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.getCalls)
}

// defaultNodeSetKey is the node-set key fixtures resolve to by default —
// deliberately not equal to any templateID value a test uses (e.g.
// "default-hcp"), so a test that asserted NodeSets["default-hcp"] would
// fail loudly instead of silently passing against the old key==templateID
// bug.
const defaultNodeSetKey = "compute"

// fixture bundles a real, in-process gRPC server (bufconn-bound) hosting a
// fakeClustersServer and a fakeClusterTemplatesServer, and a cluster.Service
// dialed against it through real publicv1 clients — no hand-rolled
// client-interface fake, per the established M2 convention of exercising
// the real generated client code.
type fixture struct {
	svc       *cluster.Service
	fake      *fakeClustersServer
	templates *fakeClusterTemplatesServer
	conn      *grpc.ClientConn
	server    *grpc.Server
}

func newFixture() *fixture {
	return newFixtureWithMatrix(versionmatrix.DefaultMatrix)
}

// newFixtureWithMatrix is newFixture with an explicit matrix, for
// TC-U-520/521/522 (proving Service.Create/SupportsVersion consult
// whatever matrix is injected, not DefaultMatrix specifically).
func newFixtureWithMatrix(matrix versionmatrix.Matrix) *fixture {
	lis := bufconn.Listen(1024 * 1024)
	grpcSrv := grpc.NewServer()
	fake := &fakeClustersServer{}
	templates := &fakeClusterTemplatesServer{}
	publicv1.RegisterClustersServer(grpcSrv, fake)
	publicv1.RegisterClusterTemplatesServer(grpcSrv, templates)
	go func() { _ = grpcSrv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
	)
	Expect(err).NotTo(HaveOccurred())

	return &fixture{
		svc:       cluster.New(publicv1.NewClustersClient(conn), publicv1.NewClusterTemplatesClient(conn), matrix),
		fake:      fake,
		templates: templates,
		conn:      conn,
		server:    grpcSrv,
	}
}

func (f *fixture) Close() {
	_ = f.conn.Close()
	f.server.Stop()
}

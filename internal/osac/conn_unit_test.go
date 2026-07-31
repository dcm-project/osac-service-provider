package osac

// TC-U-100..105 (Milestone 2, Topic 4.2 Shared Connection Accessor). Kept in
// its own file rather than folded into bootstrap_unit_test.go, since it
// introduces a materially different test harness (a real, in-process gRPC
// server bound to a bufconn.Listener hosting five services) that the rest of
// that file's tests don't need.
//
// Per SC-M2-003 (.ai/specs/osac-sp-m2-grpc-client-generation.spec.md, PR #5):
// no WithConn Option exists or is needed. This file is package osac
// (white-box), so it dials through the real, unexported dialOptions(cfg,
// &bearerCreds{b: b}) plus grpc-go's standard grpc.WithContextDialer
// bufconn-injection pattern, then assigns the resulting *grpc.ClientConn
// directly to a newBootstrap(...)-constructed Bootstrap's unexported conn
// field — genuinely exercising the bearer-token interceptor (REQ-GRPC-030),
// not stubbing it out.

import (
	"context"
	"net"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// fakeCapabilitiesServer is a minimal real Capabilities service
// implementation (not a hand-rolled client-interface fake like
// fakeCapabilitiesClient in bootstrap_unit_test.go) — it's registered on the
// same bufconn-backed grpc.Server as the four CRUD fakes below, so TC-U-100
// can prove a client built from Conn() reaches the exact same server the
// existing internal Capabilities client reaches.
type fakeCapabilitiesServer struct {
	publicv1.UnimplementedCapabilitiesServer
}

func (s *fakeCapabilitiesServer) Get(context.Context, *publicv1.CapabilitiesGetRequest) (*publicv1.CapabilitiesGetResponse, error) {
	return &publicv1.CapabilitiesGetResponse{}, nil
}

// fakeClustersServer is a real, wire-level fake implementing the generated
// ClustersServer interface: it records the authorization metadata each List
// call actually carried on the wire (TC-U-105) and returns a canned,
// test-configured response (TC-U-100/101).
type fakeClustersServer struct {
	publicv1.UnimplementedClustersServer

	mu       sync.Mutex
	resp     *publicv1.ClustersListResponse
	lastAuth string
}

func (s *fakeClustersServer) SetResponse(resp *publicv1.ClustersListResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resp = resp
}

func (s *fakeClustersServer) LastAuth() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastAuth
}

func (s *fakeClustersServer) List(ctx context.Context, _ *publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get("authorization"); len(v) > 0 {
			s.lastAuth = v[0]
		}
	}
	if s.resp == nil {
		return &publicv1.ClustersListResponse{}, nil
	}
	return s.resp, nil
}

// fakeComputeInstancesServer, fakeSubnetsServer, and fakeVirtualNetworksServer
// are the same pattern as fakeClustersServer, minus authorization recording
// — TC-U-105 only needs one representative service to prove the
// bearer-token interceptor is inherited (per AC-GRPC-030's own wording,
// "representative of all four").
type fakeComputeInstancesServer struct {
	publicv1.UnimplementedComputeInstancesServer
	resp *publicv1.ComputeInstancesListResponse
}

func (s *fakeComputeInstancesServer) List(context.Context, *publicv1.ComputeInstancesListRequest) (*publicv1.ComputeInstancesListResponse, error) {
	if s.resp == nil {
		return &publicv1.ComputeInstancesListResponse{}, nil
	}
	return s.resp, nil
}

type fakeSubnetsServer struct {
	publicv1.UnimplementedSubnetsServer
	resp *publicv1.SubnetsListResponse
}

func (s *fakeSubnetsServer) List(context.Context, *publicv1.SubnetsListRequest) (*publicv1.SubnetsListResponse, error) {
	if s.resp == nil {
		return &publicv1.SubnetsListResponse{}, nil
	}
	return s.resp, nil
}

type fakeVirtualNetworksServer struct {
	publicv1.UnimplementedVirtualNetworksServer
	resp *publicv1.VirtualNetworksListResponse
}

func (s *fakeVirtualNetworksServer) List(context.Context, *publicv1.VirtualNetworksListRequest) (*publicv1.VirtualNetworksListResponse, error) {
	if s.resp == nil {
		return &publicv1.VirtualNetworksListResponse{}, nil
	}
	return s.resp, nil
}

// bufconnGRPCFixture bundles a real, in-process gRPC server (bound to a
// bufconn.Listener) hosting the Capabilities service plus all four
// Milestone 2 CRUD services, and a Bootstrap dialed against it through the
// exact production dialOptions()+bearerCreds chain, per SC-M2-003.
type bufconnGRPCFixture struct {
	bootstrap        *Bootstrap
	clusters         *fakeClustersServer
	computeInstances *fakeComputeInstancesServer
	subnets          *fakeSubnetsServer
	virtualNetworks  *fakeVirtualNetworksServer
	conn             *grpc.ClientConn
	grpcServer       *grpc.Server
}

func newBufconnGRPCFixture() *bufconnGRPCFixture {
	lis := bufconn.Listen(1024 * 1024)
	grpcSrv := grpc.NewServer()

	clustersImpl := &fakeClustersServer{}
	ciImpl := &fakeComputeInstancesServer{}
	subnetsImpl := &fakeSubnetsServer{}
	vnImpl := &fakeVirtualNetworksServer{}

	publicv1.RegisterCapabilitiesServer(grpcSrv, &fakeCapabilitiesServer{})
	publicv1.RegisterClustersServer(grpcSrv, clustersImpl)
	publicv1.RegisterComputeInstancesServer(grpcSrv, ciImpl)
	publicv1.RegisterSubnetsServer(grpcSrv, subnetsImpl)
	publicv1.RegisterVirtualNetworksServer(grpcSrv, vnImpl)

	go func() { _ = grpcSrv.Serve(lis) }()

	b := newBootstrap(testCfg(), discardLogger, &fakeTokenSource{}, nil)

	dialOpts, err := dialOptions(testCfg(), &bearerCreds{b: b})
	Expect(err).NotTo(HaveOccurred())
	dialOpts = append(dialOpts, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}))

	conn, err := grpc.NewClient("passthrough:///bufnet", dialOpts...)
	Expect(err).NotTo(HaveOccurred())

	// Mirrors New()'s production wiring: capClient is built from the same
	// conn Conn() will expose, so TC-U-100 can prove both share one dial.
	b.conn = conn
	b.capClient = publicv1.NewCapabilitiesClient(conn)

	return &bufconnGRPCFixture{
		bootstrap:        b,
		clusters:         clustersImpl,
		computeInstances: ciImpl,
		subnets:          subnetsImpl,
		virtualNetworks:  vnImpl,
		conn:             conn,
		grpcServer:       grpcSrv,
	}
}

func (f *bufconnGRPCFixture) Close() {
	_ = f.conn.Close()
	f.grpcServer.Stop()
}

var _ = Describe("Conn (shared gRPC connection accessor, Milestone 2 DD-020)", func() {
	var fixture *bufconnGRPCFixture

	BeforeEach(func() {
		fixture = newBufconnGRPCFixture()
		DeferCleanup(fixture.Close)
	})

	// TC-U-100 (AC-GRPC-010): Conn() returns the exact connection, and a
	// client built from it reaches the same bufconn server the existing
	// internal Capabilities client reaches (there is only one listener in
	// this test's process, so both succeeding proves no second connection
	// was dialed).
	It("returns the exact shared connection, reachable by both the internal Capabilities client and a client built from it (TC-U-100)", func() {
		Expect(fixture.bootstrap.Conn()).To(BeIdenticalTo(fixture.conn))

		probeResult := fixture.bootstrap.Probe(context.Background())
		Expect(probeResult.Connected).To(BeTrue())
		Expect(probeResult.Err).NotTo(HaveOccurred())

		fixture.clusters.SetResponse(&publicv1.ClustersListResponse{
			Size: 1, Total: 1,
			Items: []*publicv1.Cluster{{Id: "c1", Status: &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_READY}}},
		})
		client := publicv1.NewClustersClient(fixture.bootstrap.Conn())
		resp, err := client.List(context.Background(), &publicv1.ClustersListRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Items).To(HaveLen(1))
		Expect(resp.Items[0].Id).To(Equal("c1"))
	})

	// TC-U-101 (AC-GRPC-020): Clusters.List round-trips real data via
	// Conn() — exact field equality, not len()>0.
	It("round-trips exact Clusters.List data via Conn() (TC-U-101)", func() {
		fixture.clusters.SetResponse(&publicv1.ClustersListResponse{
			Size: 1, Total: 1,
			Items: []*publicv1.Cluster{{Id: "c1", Status: &publicv1.ClusterStatus{State: publicv1.ClusterState_CLUSTER_STATE_READY}}},
		})

		client := publicv1.NewClustersClient(fixture.bootstrap.Conn())
		resp, err := client.List(context.Background(), &publicv1.ClustersListRequest{})
		Expect(err).NotTo(HaveOccurred())

		Expect(resp.Size).To(Equal(int32(1)))
		Expect(resp.Total).To(Equal(int32(1)))
		Expect(resp.Items).To(HaveLen(1))
		Expect(resp.Items[0].Id).To(Equal("c1"))
		Expect(resp.Items[0].Status.State).To(Equal(publicv1.ClusterState_CLUSTER_STATE_READY))
	})

	// TC-U-102 (AC-GRPC-020): ComputeInstances.List round-trips real data
	// via Conn() — same pattern as TC-U-101.
	It("round-trips exact ComputeInstances.List data via Conn() (TC-U-102)", func() {
		fixture.computeInstances.resp = &publicv1.ComputeInstancesListResponse{
			Size: 1, Total: 1,
			Items: []*publicv1.ComputeInstance{{Id: "ci1", Status: &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING}}},
		}

		client := publicv1.NewComputeInstancesClient(fixture.bootstrap.Conn())
		resp, err := client.List(context.Background(), &publicv1.ComputeInstancesListRequest{})
		Expect(err).NotTo(HaveOccurred())

		Expect(resp.Items).To(HaveLen(1))
		Expect(resp.Items[0].Id).To(Equal("ci1"))
		Expect(resp.Items[0].Status.State).To(Equal(publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING))
	})

	// TC-U-103 (AC-GRPC-020): Subnets.List round-trips real data via
	// Conn() — same pattern as TC-U-101.
	It("round-trips exact Subnets.List data via Conn() (TC-U-103)", func() {
		fixture.subnets.resp = &publicv1.SubnetsListResponse{
			Size: 1, Total: 1,
			Items: []*publicv1.Subnet{{Id: "sn1", Status: &publicv1.SubnetStatus{State: publicv1.SubnetState_SUBNET_STATE_READY}}},
		}

		client := publicv1.NewSubnetsClient(fixture.bootstrap.Conn())
		resp, err := client.List(context.Background(), &publicv1.SubnetsListRequest{})
		Expect(err).NotTo(HaveOccurred())

		Expect(resp.Items).To(HaveLen(1))
		Expect(resp.Items[0].Id).To(Equal("sn1"))
		Expect(resp.Items[0].Status.State).To(Equal(publicv1.SubnetState_SUBNET_STATE_READY))
	})

	// TC-U-104 (AC-GRPC-020): VirtualNetworks.List round-trips real data
	// via Conn() — same pattern as TC-U-101.
	It("round-trips exact VirtualNetworks.List data via Conn() (TC-U-104)", func() {
		fixture.virtualNetworks.resp = &publicv1.VirtualNetworksListResponse{
			Size: 1, Total: 1,
			Items: []*publicv1.VirtualNetwork{{Id: "vn1", Status: &publicv1.VirtualNetworkStatus{State: publicv1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY}}},
		}

		client := publicv1.NewVirtualNetworksClient(fixture.bootstrap.Conn())
		resp, err := client.List(context.Background(), &publicv1.VirtualNetworksListRequest{})
		Expect(err).NotTo(HaveOccurred())

		Expect(resp.Items).To(HaveLen(1))
		Expect(resp.Items[0].Id).To(Equal("vn1"))
		Expect(resp.Items[0].Status.State).To(Equal(publicv1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY))
	})

	// TC-U-105 (AC-GRPC-030): a client built from Conn() inherits the
	// shared bearer-token interceptor — the recorded authorization
	// metadata equals exactly "Bearer tok-xyz", the same value/format
	// Milestone 1 already proved for the Capabilities client.
	It("carries the shared bearer token on calls made via a client built from Conn() (TC-U-105)", func() {
		fixture.bootstrap.setToken("tok-xyz", time.Now().Add(time.Hour))

		client := publicv1.NewClustersClient(fixture.bootstrap.Conn())
		_, err := client.List(context.Background(), &publicv1.ClustersListRequest{})
		Expect(err).NotTo(HaveOccurred())

		Expect(fixture.clusters.LastAuth()).To(Equal("Bearer tok-xyz"))
	})
})

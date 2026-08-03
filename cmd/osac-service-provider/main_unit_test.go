package main

// Unit scope (per .ai/test-plans/osac-sp-unit.test-plan.md, section 8
// "cmd/osac-service-provider"): these cases call run/mainRun directly and
// in-process, each failing before reaching any real OSAC/Keycloak/
// control-plane collaborator — no fakes needed, unlike
// main_integration_test.go's full-stack happy-path suite.

import (
	"context"
	"log/slog"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
	"github.com/dcm-project/osac-service-provider/internal/cluster"
	clusterhandlers "github.com/dcm-project/osac-service-provider/internal/handlers/cluster"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// setValidEnv sets every required/commonly-used env var to a
// syntactically-valid placeholder value, using the given (already
// reserved-but-unbound) server address. Individual tests below override or
// omit specific vars to force one particular failure branch.
func setValidEnv(serverAddr string) {
	t := GinkgoT()
	t.Setenv("SP_SERVER_ADDRESS", serverAddr)
	t.Setenv("SP_SERVER_SHUTDOWN_TIMEOUT", "1s")
	t.Setenv("SP_OSAC_FULFILLMENT_ADDRESS", "127.0.0.1:1")
	t.Setenv("SP_OSAC_OIDC_ISSUER_URL", "https://keycloak.example.com/realms/osac")
	t.Setenv("SP_OSAC_OIDC_CLIENT_ID", "osac-sp")
	t.Setenv("SP_OSAC_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("SP_OSAC_TLS_ENABLED", "false")
	t.Setenv("SP_OSAC_PROBE_TIMEOUT", "1s")
	t.Setenv("DCM_REGISTRATION_URL", "https://control-plane.example.com/api/v1alpha1")
	t.Setenv("SP_ENDPOINT", "https://osac-sp.example.com")
	t.Setenv("SP_PROVIDER_CLUSTER_NAME", "osac-sp-cluster")
	t.Setenv("SP_PROVIDER_VM_NAME", "osac-sp-vm")
}

var _ = Describe("run's top-level error wrapping (unit)", func() {
	// TC-U-094: a config.Load failure is wrapped and returned, before any
	// listener is bound.
	It("wraps and returns a config-load failure (TC-U-094)", func() {
		t := GinkgoT()
		// Every required var except SP_ENDPOINT — deliberately left
		// unset so config.Load fails fast (REQ-XC-CFG-020).
		t.Setenv("SP_OSAC_FULFILLMENT_ADDRESS", "127.0.0.1:1")
		t.Setenv("SP_OSAC_OIDC_ISSUER_URL", "https://keycloak.example.com/realms/osac")
		t.Setenv("SP_OSAC_OIDC_CLIENT_ID", "osac-sp")
		t.Setenv("SP_OSAC_OIDC_CLIENT_SECRET", "secret")
		t.Setenv("DCM_REGISTRATION_URL", "https://control-plane.example.com/api/v1alpha1")

		err := run(context.Background(), slog.New(slog.DiscardHandler))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("initializing"))
	})

	// TC-U-095: a listener-bind failure (address already in use) is
	// wrapped and returned, before any OSAC/registration collaborator is
	// constructed.
	It("wraps and returns a listener-bind failure (TC-U-095)", func() {
		held, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = held.Close() }()

		setValidEnv(held.Addr().String()) // already bound by `held` above

		runErr := run(context.Background(), slog.New(slog.DiscardHandler))
		Expect(runErr).To(HaveOccurred())
		Expect(runErr.Error()).To(ContainSubstring("listening"))
	})

	// TC-U-096: an OSAC bootstrap construction failure (invalid TLS cert
	// file) is wrapped and returned, before registration starts.
	It("wraps and returns an OSAC bootstrap construction failure (TC-U-096)", func() {
		setValidEnv(reserveLoopbackAddr())
		t := GinkgoT()
		t.Setenv("SP_OSAC_TLS_ENABLED", "true")
		t.Setenv("SP_OSAC_TLS_CERT_FILE", "/nonexistent/path/ca.pem")

		runErr := run(context.Background(), slog.New(slog.DiscardHandler))
		Expect(runErr).To(HaveOccurred())
		Expect(runErr.Error()).To(ContainSubstring("creating OSAC client bootstrap"))
	})
})

// minimalClustersServer is a bufconn-backed fake OSAC ClustersServer (same
// technique as internal/cluster/fixture_test.go) used only to prove
// apiHandler's 4 forwarding methods actually reach internal/cluster —
// exhaustive Cluster CRUD business-logic behavior itself is
// internal/cluster's and internal/handlers/cluster's own test scope.
type minimalClustersServer struct {
	publicv1.UnimplementedClustersServer
	createCalls, getCalls, listCalls, deleteCalls int
}

func (s *minimalClustersServer) Create(context.Context, *publicv1.ClustersCreateRequest) (*publicv1.ClustersCreateResponse, error) {
	s.createCalls++
	return &publicv1.ClustersCreateResponse{Object: &publicv1.Cluster{Id: "X", Status: &publicv1.ClusterStatus{}}}, nil
}

func (s *minimalClustersServer) Get(context.Context, *publicv1.ClustersGetRequest) (*publicv1.ClustersGetResponse, error) {
	s.getCalls++
	return &publicv1.ClustersGetResponse{Object: &publicv1.Cluster{Id: "X", Status: &publicv1.ClusterStatus{}}}, nil
}

func (s *minimalClustersServer) List(context.Context, *publicv1.ClustersListRequest) (*publicv1.ClustersListResponse, error) {
	s.listCalls++
	return &publicv1.ClustersListResponse{}, nil
}

func (s *minimalClustersServer) Delete(context.Context, *publicv1.ClustersDeleteRequest) (*publicv1.ClustersDeleteResponse, error) {
	s.deleteCalls++
	return &publicv1.ClustersDeleteResponse{}, nil
}

var _ = Describe("apiHandler's Cluster CRUD forwarding (unit)", func() {
	// TC-U-098: each of apiHandler's 4 forwarding methods reaches the real
	// internal/cluster.Service (through clusterhandlers.Handler), proving
	// cmd/main's wiring itself — not a re-test of CRUD business logic,
	// which is internal/cluster's and internal/handlers/cluster's own
	// exhaustive scope (100% covered there).
	It("routes all 4 Cluster operations through to the wired cluster.Service (TC-U-098)", func() {
		lis := bufconn.Listen(1024 * 1024)
		grpcSrv := grpc.NewServer()
		fake := &minimalClustersServer{}
		publicv1.RegisterClustersServer(grpcSrv, fake)
		go func() { _ = grpcSrv.Serve(lis) }()
		defer grpcSrv.Stop()

		conn, err := grpc.NewClient("passthrough:///bufnet",
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
		)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = conn.Close() }()

		svc := cluster.New(publicv1.NewClustersClient(conn))
		h := &apiHandler{cluster: clusterhandlers.NewHandler(svc, slog.New(slog.DiscardHandler))}
		ctx := context.Background()

		_, err = h.ListClusters(ctx, oapigen.ListClustersRequestObject{})
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.listCalls).To(Equal(1))

		_, err = h.CreateCluster(ctx, oapigen.CreateClusterRequestObject{
			Params: v1alpha1.CreateClusterParams{Id: "X"},
			Body: &v1alpha1.CreateClusterJSONRequestBody{Spec: v1alpha1.ClusterSpec{
				Version:       "1.29",
				Nodes:         v1alpha1.ClusterNodes{Worker: v1alpha1.ClusterWorkerNodes{Count: 1}},
				Metadata:      v1alpha1.ClusterMetadata{Name: "foo"},
				ProviderHints: v1alpha1.ClusterProviderHints{Osac: v1alpha1.OSACProviderHints{TemplateId: "default-hcp"}},
			}},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.createCalls).To(Equal(1))

		_, err = h.GetCluster(ctx, oapigen.GetClusterRequestObject{ClusterId: "X"})
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.getCalls).To(Equal(1))

		_, err = h.DeleteCluster(ctx, oapigen.DeleteClusterRequestObject{ClusterId: "X"})
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.deleteCalls).To(Equal(1))
	})
})

var _ = Describe("mainRun (unit)", func() {
	// TC-U-097: mainRun maps a run failure to exit code 1, in-process,
	// without invoking os.Exit — proving the exit-code contract without
	// needing a subprocess harness (main()'s actual os.Exit call is a
	// documented coverage exception; see the test plan).
	It("returns exit code 1 when run fails (TC-U-097)", func() {
		// Same trigger as TC-U-094: leave SP_ENDPOINT unset.
		t := GinkgoT()
		t.Setenv("SP_OSAC_FULFILLMENT_ADDRESS", "127.0.0.1:1")
		t.Setenv("SP_OSAC_OIDC_ISSUER_URL", "https://keycloak.example.com/realms/osac")
		t.Setenv("SP_OSAC_OIDC_CLIENT_ID", "osac-sp")
		t.Setenv("SP_OSAC_OIDC_CLIENT_SECRET", "secret")
		t.Setenv("DCM_REGISTRATION_URL", "https://control-plane.example.com/api/v1alpha1")

		Expect(mainRun()).To(Equal(1))
	})
})

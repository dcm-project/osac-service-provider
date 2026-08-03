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
	vmhandlers "github.com/dcm-project/osac-service-provider/internal/handlers/vm"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/util"
	"github.com/dcm-project/osac-service-provider/internal/vm"
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

// minimalComputeInstancesServer is a bufconn-backed fake OSAC
// ComputeInstancesServer (same technique as
// internal/vm/fixture_test.go) used only to prove apiHandler's 4
// forwarding methods actually reach internal/vm — exhaustive VM CRUD
// business-logic behavior itself is internal/vm's and
// internal/handlers/vm's own test scope.
type minimalComputeInstancesServer struct {
	publicv1.UnimplementedComputeInstancesServer
	createCalls, getCalls, listCalls, deleteCalls int
}

func (s *minimalComputeInstancesServer) Create(context.Context, *publicv1.ComputeInstancesCreateRequest) (*publicv1.ComputeInstancesCreateResponse, error) {
	s.createCalls++
	return &publicv1.ComputeInstancesCreateResponse{Object: &publicv1.ComputeInstance{Id: "X", Status: &publicv1.ComputeInstanceStatus{}}}, nil
}

func (s *minimalComputeInstancesServer) Get(context.Context, *publicv1.ComputeInstancesGetRequest) (*publicv1.ComputeInstancesGetResponse, error) {
	s.getCalls++
	return &publicv1.ComputeInstancesGetResponse{Object: &publicv1.ComputeInstance{Id: "X", Status: &publicv1.ComputeInstanceStatus{}}}, nil
}

func (s *minimalComputeInstancesServer) List(context.Context, *publicv1.ComputeInstancesListRequest) (*publicv1.ComputeInstancesListResponse, error) {
	s.listCalls++
	return &publicv1.ComputeInstancesListResponse{}, nil
}

func (s *minimalComputeInstancesServer) Delete(context.Context, *publicv1.ComputeInstancesDeleteRequest) (*publicv1.ComputeInstancesDeleteResponse, error) {
	s.deleteCalls++
	return &publicv1.ComputeInstancesDeleteResponse{}, nil
}

// minimalSubnetsServer always reports one READY default subnet, so
// Create's default-network resolution (§4.5) short-circuits without this
// test needing to know anything about its provisioning mechanics.
type minimalSubnetsServer struct {
	publicv1.UnimplementedSubnetsServer
}

func (minimalSubnetsServer) List(context.Context, *publicv1.SubnetsListRequest) (*publicv1.SubnetsListResponse, error) {
	return &publicv1.SubnetsListResponse{
		Size: 1, Total: 1,
		Items: []*publicv1.Subnet{{Id: "subnet-existing", Status: &publicv1.SubnetStatus{State: publicv1.SubnetState_SUBNET_STATE_READY}}},
	}, nil
}

// minimalVirtualNetworksServer is never called given minimalSubnetsServer
// above always reports an existing default subnet; embedding the
// Unimplemented type alone satisfies publicv1.VirtualNetworksServer.
type minimalVirtualNetworksServer struct {
	publicv1.UnimplementedVirtualNetworksServer
}

var _ = Describe("apiHandler's VM CRUD forwarding (unit)", func() {
	// TC-U-099: each of apiHandler's 4 forwarding methods reaches the real
	// internal/vm.Service (through vmhandlers.Handler), proving cmd/main's
	// wiring itself — not a re-test of CRUD business logic, which is
	// internal/vm's and internal/handlers/vm's own exhaustive scope (100%
	// covered there).
	It("routes all 4 VM operations through to the wired vm.Service (TC-U-099)", func() {
		lis := bufconn.Listen(1024 * 1024)
		grpcSrv := grpc.NewServer()
		fake := &minimalComputeInstancesServer{}
		publicv1.RegisterComputeInstancesServer(grpcSrv, fake)
		publicv1.RegisterSubnetsServer(grpcSrv, minimalSubnetsServer{})
		publicv1.RegisterVirtualNetworksServer(grpcSrv, minimalVirtualNetworksServer{})
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

		svc := vm.New(
			publicv1.NewComputeInstancesClient(conn),
			publicv1.NewSubnetsClient(conn),
			publicv1.NewVirtualNetworksClient(conn),
		)
		h := &apiHandler{vm: vmhandlers.NewHandler(svc, slog.New(slog.DiscardHandler))}
		ctx := context.Background()

		_, err = h.ListVMs(ctx, oapigen.ListVMsRequestObject{})
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.listCalls).To(Equal(1))

		_, err = h.CreateVM(ctx, oapigen.CreateVMRequestObject{
			Params: v1alpha1.CreateVMParams{Id: util.Ptr("X")},
			Body: &v1alpha1.CreateVMJSONRequestBody{Spec: &v1alpha1.VMSpec{
				Storage:  v1alpha1.VMStorage{Disks: []v1alpha1.VMDisk{{Name: "boot", Capacity: "100GB"}}},
				GuestOs:  v1alpha1.VMGuestOS{Type: "rhel-9"},
				Metadata: v1alpha1.VMMetadata{Name: "foo"},
				ProviderHints: v1alpha1.VMProviderHints{Osac: v1alpha1.OSACVMProviderHints{
					TemplateId:   "default-vm",
					InstanceType: "standard-4-16",
				}},
			}},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.createCalls).To(Equal(1))

		_, err = h.GetVM(ctx, oapigen.GetVMRequestObject{VmId: "X"})
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.getCalls).To(Equal(1))

		_, err = h.DeleteVM(ctx, oapigen.DeleteVMRequestObject{VmId: "X"})
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

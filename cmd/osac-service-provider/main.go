// Command osac-service-provider runs the OSAC Service Provider.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
	"github.com/dcm-project/osac-service-provider/internal/apiserver"
	"github.com/dcm-project/osac-service-provider/internal/cluster"
	"github.com/dcm-project/osac-service-provider/internal/config"
	clusterhandlers "github.com/dcm-project/osac-service-provider/internal/handlers/cluster"
	vmhandlers "github.com/dcm-project/osac-service-provider/internal/handlers/vm"
	"github.com/dcm-project/osac-service-provider/internal/health"
	"github.com/dcm-project/osac-service-provider/internal/osac"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/registration"
	"github.com/dcm-project/osac-service-provider/internal/statuspoll"
	"github.com/dcm-project/osac-service-provider/internal/statuspublisher"
	"github.com/dcm-project/osac-service-provider/internal/vm"
)

// apiHandler combines Milestone 1's health.Handler with Milestone 3's
// clusterhandlers.Handler and Milestone 4's vmhandlers.Handler so the
// result satisfies the full oapigen.StrictServerInterface. health.Handler
// is embedded (its two methods, GetClustersHealth/GetVMsHealth, are
// promoted directly); the cluster and VM handlers are named fields with
// explicit forwarding methods below, since all three types are named
// "Handler" and Go disallows embedding same-named fields in one struct.
type apiHandler struct {
	*health.Handler
	cluster *clusterhandlers.Handler
	vm      *vmhandlers.Handler
}

func (h *apiHandler) ListClusters(ctx context.Context, req oapigen.ListClustersRequestObject) (oapigen.ListClustersResponseObject, error) {
	return h.cluster.ListClusters(ctx, req)
}

func (h *apiHandler) CreateCluster(ctx context.Context, req oapigen.CreateClusterRequestObject) (oapigen.CreateClusterResponseObject, error) {
	return h.cluster.CreateCluster(ctx, req)
}

func (h *apiHandler) GetCluster(ctx context.Context, req oapigen.GetClusterRequestObject) (oapigen.GetClusterResponseObject, error) {
	return h.cluster.GetCluster(ctx, req)
}

func (h *apiHandler) DeleteCluster(ctx context.Context, req oapigen.DeleteClusterRequestObject) (oapigen.DeleteClusterResponseObject, error) {
	return h.cluster.DeleteCluster(ctx, req)
}

func (h *apiHandler) ListVMs(ctx context.Context, req oapigen.ListVMsRequestObject) (oapigen.ListVMsResponseObject, error) {
	return h.vm.ListVMs(ctx, req)
}

func (h *apiHandler) CreateVM(ctx context.Context, req oapigen.CreateVMRequestObject) (oapigen.CreateVMResponseObject, error) {
	return h.vm.CreateVM(ctx, req)
}

func (h *apiHandler) GetVM(ctx context.Context, req oapigen.GetVMRequestObject) (oapigen.GetVMResponseObject, error) {
	return h.vm.GetVM(ctx, req)
}

func (h *apiHandler) DeleteVM(ctx context.Context, req oapigen.DeleteVMRequestObject) (oapigen.DeleteVMResponseObject, error) {
	return h.vm.DeleteVM(ctx, req)
}

// version is the application version, set at build time via
// -ldflags "-X main.version=X.Y.Z".
var version = "0.0.1-dev"

func main() {
	// os.Exit runs after mainRun returns, so mainRun's own deferred
	// stop() (below) always executes first — os.Exit here directly would
	// skip it (gocritic: exitAfterDefer).
	os.Exit(mainRun())
}

func mainRun() int {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Coverage exception (documented, not unit-tested): translating
	// SIGTERM/SIGINT into context cancellation is a stdlib concern
	// (signal.NotifyContext), kept here rather than inside run so
	// integration tests can drive run's shutdown path directly via ctx
	// cancellation, without delivering real OS signals to the test binary
	// process (see cmd/osac-service-provider/main_integration_test.go,
	// TC-I-003/004). This means mainRun's own happy path (this line
	// through the final "return 0" below) can only be unit-tested by
	// doing the exact thing that comment says to avoid — sending a real
	// OS signal to the test process — so it is left as an accepted gap;
	// run's happy path (everything mainRun delegates to) is fully proven
	// by main_integration_test.go instead.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("fatal error", "error", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	ln, err := net.Listen("tcp", cfg.Server.Address)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", cfg.Server.Address, err)
	}
	defer func() { _ = ln.Close() }()

	osacBootstrap, err := osac.New(&cfg.OSAC, logger)
	if err != nil {
		return fmt.Errorf("creating OSAC client bootstrap: %w", err)
	}
	defer func() { _ = osacBootstrap.Close() }()
	osacBootstrap.Start(ctx)

	// Coverage exception (documented, not tested): this branch is
	// transitively unreachable for the same reason as its callee,
	// registration.NewRegistrar's own error branch — see the comment
	// there (internal/registration/registration.go).
	registrar, err := registration.NewRegistrar(cfg, logger)
	if err != nil {
		return fmt.Errorf("creating registrar: %w", err)
	}

	publisher, err := statuspublisher.NewPublisher(cfg.DCM.NATSURL, logger)
	if err != nil {
		return fmt.Errorf("creating status publisher: %w", err)
	}
	defer func() { _ = publisher.Close() }()
	publisher.Start(ctx)

	poller := statuspoll.New(
		publicv1.NewClustersClient(osacBootstrap.Conn()),
		publicv1.NewComputeInstancesClient(osacBootstrap.Conn()),
		publisher, cfg.Provider.ClusterName, cfg.Provider.VMName, cfg.Status, logger,
	)
	poller.Start(ctx)

	healthHandler := health.NewHandler(osacBootstrap, time.Now(), version)
	clusterSvc := cluster.New(publicv1.NewClustersClient(osacBootstrap.Conn()), publicv1.NewClusterTemplatesClient(osacBootstrap.Conn()))
	clusterHandler := clusterhandlers.NewHandler(clusterSvc, logger)
	vmSvc := vm.New(
		publicv1.NewComputeInstancesClient(osacBootstrap.Conn()),
		publicv1.NewSubnetsClient(osacBootstrap.Conn()),
		publicv1.NewVirtualNetworksClient(osacBootstrap.Conn()),
	)
	vmHandler := vmhandlers.NewHandler(vmSvc, logger)
	handler := &apiHandler{Handler: healthHandler, cluster: clusterHandler, vm: vmHandler}
	strictAdapter := oapigen.NewStrictHandlerWithOptions(handler, nil, oapigen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  apiserver.NewRequestErrorHandler(logger),
		ResponseErrorHandlerFunc: apiserver.NewResponseErrorHandler(logger),
	})

	srv := apiserver.New(cfg, logger, strictAdapter).WithOnReady(func(ctx context.Context) {
		registrar.Start(ctx)
	})

	return srv.Run(ctx, ln)
}

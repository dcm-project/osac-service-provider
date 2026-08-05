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
	"github.com/dcm-project/osac-service-provider/internal/config"
	"github.com/dcm-project/osac-service-provider/internal/health"
	"github.com/dcm-project/osac-service-provider/internal/osac"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/registration"
	"github.com/dcm-project/osac-service-provider/internal/statuspoll"
	"github.com/dcm-project/osac-service-provider/internal/statuspublisher"
)

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
		publisher, cfg.Status, logger,
	)
	poller.Start(ctx)

	healthHandler := health.NewHandler(osacBootstrap, time.Now(), version)
	strictAdapter := oapigen.NewStrictHandlerWithOptions(healthHandler, nil, oapigen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  apiserver.NewRequestErrorHandler(logger),
		ResponseErrorHandlerFunc: apiserver.NewResponseErrorHandler(logger),
	})

	srv := apiserver.New(cfg, logger, strictAdapter).WithOnReady(func(ctx context.Context) {
		registrar.Start(ctx)
	})

	return srv.Run(ctx, ln)
}

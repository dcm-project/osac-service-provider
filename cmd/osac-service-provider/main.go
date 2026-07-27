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
	"github.com/dcm-project/osac-service-provider/internal/registration"
)

// version is the application version, set at build time via
// -ldflags "-X main.version=X.Y.Z".
var version = "0.0.1-dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	ln, err := net.Listen("tcp", cfg.Server.Address)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", cfg.Server.Address, err)
	}
	defer func() { _ = ln.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	osacBootstrap, err := osac.New(&cfg.OSAC, logger)
	if err != nil {
		return fmt.Errorf("creating OSAC client bootstrap: %w", err)
	}
	defer func() { _ = osacBootstrap.Close() }()
	osacBootstrap.Start(ctx)

	registrar, err := registration.NewRegistrar(cfg, logger)
	if err != nil {
		return fmt.Errorf("creating registrar: %w", err)
	}

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

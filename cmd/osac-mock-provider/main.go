// Command osac-mock-provider fakes the OSAC backend side of the gRPC
// contract osac-sp dials (osac.public.v1's Capabilities, Clusters,
// ComputeInstances, Subnets, VirtualNetworks) plus a client-credentials
// OIDC discovery+token stub, for the kind-based e2e infra (Phase 1 of
// osac-service-provider#17 / FLPATH-4759). See
// .ai/specs/osac-sp-e2e-mock-provider.spec.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/dcm-project/osac-service-provider/internal/mockprovider"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// shutdownTimeout bounds how long graceful shutdown of both listeners may
// take once the context is cancelled. Not configurable (unlike the real
// SP's SP_SERVER_SHUTDOWN_TIMEOUT): this binary has no production traffic
// to drain, only short-lived test requests.
const shutdownTimeout = 5 * time.Second

func main() {
	os.Exit(mainRun())
}

func mainRun() int {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Coverage exception (documented, not unit-tested): same rationale as
	// cmd/osac-service-provider/main.go's mainRun — translating
	// SIGTERM/SIGINT into context cancellation is a stdlib concern, kept
	// out of run so TC-I-031 can drive shutdown directly via ctx
	// cancellation without sending real OS signals to the test process.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("fatal error", "error", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := mockprovider.LoadConfig()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	grpcLn, err := net.Listen("tcp", cfg.GRPCAddress)
	if err != nil {
		return fmt.Errorf("listening for gRPC on %s: %w", cfg.GRPCAddress, err)
	}
	defer func() { _ = grpcLn.Close() }()

	oidcLn, err := net.Listen("tcp", cfg.OIDCAddress)
	if err != nil {
		return fmt.Errorf("listening for OIDC HTTP on %s: %w", cfg.OIDCAddress, err)
	}
	defer func() { _ = oidcLn.Close() }()

	grpcSrv := grpc.NewServer()
	publicv1.RegisterCapabilitiesServer(grpcSrv, mockprovider.NewCapabilitiesServer())
	publicv1.RegisterClustersServer(grpcSrv, mockprovider.NewClustersServer())
	publicv1.RegisterComputeInstancesServer(grpcSrv, mockprovider.NewComputeInstancesServer())
	publicv1.RegisterSubnetsServer(grpcSrv, mockprovider.NewSubnetsServer())
	publicv1.RegisterVirtualNetworksServer(grpcSrv, mockprovider.NewVirtualNetworksServer())

	// OIDCHandler derives each response's token_endpoint from that
	// request's own Host header (DD-089), so it needs no address
	// computed from oidcLn here.
	oidcSrv := &http.Server{Handler: mockprovider.NewOIDCHandler(logger)}

	return serveUntilDone(ctx, logger, shutdownTimeout, grpcSrv, grpcLn, oidcSrv, oidcLn)
}

// serveUntilDone runs both servers until ctx is cancelled or either exits
// unexpectedly (whichever comes first), then gracefully shuts both down and
// returns the triggering error (nil on a clean ctx-driven shutdown). Split
// out from run so it can be unit-tested directly against pre-closed
// listeners/slow handlers (TC-U-148..151) — the same
// "construct the real collaborator, force a real failure, assert the
// wrapped error" technique as
// internal/apiserver/server_unit_test.go's TC-U-080/081 — without needing a
// full mockprovider+osac.Bootstrap stack for every failure branch.
func serveUntilDone(ctx context.Context, logger *slog.Logger, shutdownTimeout time.Duration, grpcSrv *grpc.Server, grpcLn net.Listener, oidcSrv *http.Server, oidcLn net.Listener) error {
	errCh := make(chan error, 2)
	go func() {
		logger.Info("gRPC server listening", "address", grpcLn.Addr().String())
		if err := grpcSrv.Serve(grpcLn); err != nil {
			errCh <- fmt.Errorf("serving gRPC on %s: %w", grpcLn.Addr(), err)
			return
		}
		errCh <- nil
	}()
	go func() {
		logger.Info("OIDC HTTP server listening", "address", oidcLn.Addr().String())
		if err := oidcSrv.Serve(oidcLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("serving OIDC HTTP on %s: %w", oidcLn.Addr(), err)
			return
		}
		errCh <- nil
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errCh:
	}

	logger.Info("shutting down mock provider")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	grpcSrv.GracefulStop()
	if err := oidcSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("OIDC HTTP server shutdown error", "error", err)
	}

	return runErr
}

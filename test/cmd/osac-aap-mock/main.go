// Command osac-aap-mock fakes just enough of Ansible Automation Platform's
// REST API (GetTemplate, LaunchJobTemplate/LaunchWorkflowTemplate, GetJob,
// CanCancelJob, CancelJob) for real osac-operator/BMFO reconciliation loops
// to drive a ClusterOrder to a terminal Ready state, for Tier B Phase 2 of
// the kind-based e2e infra (osac-service-provider#44). See
// .ai/specs/osac-sp-e2e-tier-b.spec.md §2 Phase 2.
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

	"github.com/dcm-project/osac-service-provider/test/aapmock"
)

// shutdownTimeout bounds how long graceful shutdown may take once the
// context is cancelled. Not configurable (unlike the real SP's
// SP_SERVER_SHUTDOWN_TIMEOUT): this binary has no production traffic to
// drain, only short-lived test requests.
const shutdownTimeout = 5 * time.Second

func main() {
	os.Exit(mainRun())
}

func mainRun() int {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Coverage exception (documented, not unit-tested): same rationale as
	// test/cmd/osac-mock-provider/main.go's mainRun — translating
	// SIGTERM/SIGINT into context cancellation is a stdlib concern, kept
	// out of run so tests can drive shutdown directly via ctx
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
	cfg, err := aapmock.LoadConfig()
	if err != nil {
		return fmt.Errorf("initializing: %w", err)
	}

	ln, err := net.Listen("tcp", cfg.Address)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", cfg.Address, err)
	}
	defer func() { _ = ln.Close() }()

	srv := &http.Server{Handler: aapmock.NewHandler(cfg.Token)}

	return serveUntilDone(ctx, logger, shutdownTimeout, srv, ln)
}

// serveUntilDone runs srv until ctx is cancelled or Serve exits
// unexpectedly (whichever comes first), then gracefully shuts it down and
// returns the triggering error (nil on a clean ctx-driven shutdown). Split
// out from run so it can be unit-tested directly against a pre-closed
// listener/slow handler — same technique as
// test/cmd/osac-mock-provider/main.go's serveUntilDone.
func serveUntilDone(ctx context.Context, logger *slog.Logger, shutdownTimeout time.Duration, srv *http.Server, ln net.Listener) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("AAP mock HTTP server listening", "address", ln.Addr().String())
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("serving AAP mock HTTP on %s: %w", ln.Addr(), err)
			return
		}
		errCh <- nil
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errCh:
	}

	logger.Info("shutting down AAP mock")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("AAP mock HTTP server shutdown error", "error", err)
	}

	return runErr
}

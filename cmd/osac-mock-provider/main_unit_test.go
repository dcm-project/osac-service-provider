package main

// Unit scope (per .ai/test-plans/osac-sp-e2e-mock-provider.test-plan.md,
// "6. cmd/osac-mock-provider — unit"): run's top-level error-wrapping
// branches (each failing before any server starts serving, so no fakes
// needed) plus serveUntilDone's own failure/shutdown branches, tested
// directly against real-but-deliberately-broken collaborators (a
// pre-closed net.Listener, a slow http.Handler) — same technique as
// internal/apiserver/server_unit_test.go's TC-U-080/081 and
// cmd/osac-service-provider/main_unit_test.go's TC-U-094..097.

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
)

var _ = Describe("run's top-level error wrapping (unit)", func() {
	// TC-U-144: a LoadConfig failure is wrapped and returned, before any
	// listener is bound.
	It("wraps and returns a config-load failure (TC-U-144)", func() {
		// Neither MOCK_GRPC_ADDRESS nor MOCK_OIDC_ADDRESS is set.
		err := run(context.Background(), slog.New(slog.DiscardHandler))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("initializing"))
	})

	// TC-U-145: a gRPC listener-bind failure (address already in use) is
	// wrapped and returned, before the OIDC listener is bound.
	It("wraps and returns a gRPC listener-bind failure (TC-U-145)", func() {
		held, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = held.Close() }()

		t := GinkgoT()
		t.Setenv("MOCK_GRPC_ADDRESS", held.Addr().String()) // already bound by `held`
		t.Setenv("MOCK_OIDC_ADDRESS", "127.0.0.1:0")

		runErr := run(context.Background(), slog.New(slog.DiscardHandler))
		Expect(runErr).To(HaveOccurred())
		Expect(runErr.Error()).To(ContainSubstring("listening for gRPC"))
	})

	// TC-U-146: an OIDC listener-bind failure (address already in use) is
	// wrapped and returned, after the gRPC listener is bound (and
	// released via its defer) but before either server starts serving.
	It("wraps and returns an OIDC listener-bind failure (TC-U-146)", func() {
		held, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = held.Close() }()

		t := GinkgoT()
		t.Setenv("MOCK_GRPC_ADDRESS", "127.0.0.1:0")
		t.Setenv("MOCK_OIDC_ADDRESS", held.Addr().String()) // already bound by `held`

		runErr := run(context.Background(), slog.New(slog.DiscardHandler))
		Expect(runErr).To(HaveOccurred())
		Expect(runErr.Error()).To(ContainSubstring("listening for OIDC HTTP"))
	})
})

var _ = Describe("mainRun (unit)", func() {
	// TC-U-147: mainRun maps a run failure to exit code 1, in-process,
	// without invoking os.Exit — same technique as
	// cmd/osac-service-provider/main_unit_test.go's TC-U-097. mainRun's
	// happy path (exit code 0) is a documented coverage exception (see
	// its own comment above) since exercising it needs a real OS signal
	// to unblock signal.NotifyContext's ctx.
	It("returns exit code 1 when run fails (TC-U-147)", func() {
		// Same trigger as TC-U-144: neither address env var is set.
		Expect(mainRun()).To(Equal(1))
	})
})

var _ = Describe("serveUntilDone (unit)", func() {
	// newLoopbackListener binds a real, unused loopback listener for
	// tests that need serveUntilDone to actually succeed at Serve()
	// before being torn down.
	newLoopbackListener := func() net.Listener {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		return ln
	}

	// TC-U-148: a ctx cancellation (the normal shutdown trigger) is
	// reported as a nil error once both servers have gracefully stopped.
	It("returns nil when ctx is cancelled (TC-U-148)", func() {
		grpcSrv := grpc.NewServer()
		grpcLn := newLoopbackListener()
		oidcSrv := &http.Server{Handler: http.NotFoundHandler()}
		oidcLn := newLoopbackListener()

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			errCh <- serveUntilDone(ctx, slog.New(slog.DiscardHandler), time.Second, grpcSrv, grpcLn, oidcSrv, oidcLn)
		}()

		// Give both Serve goroutines a moment to actually start serving
		// before cancelling, so this exercises the steady-state path
		// (not a race against startup).
		time.Sleep(20 * time.Millisecond)
		cancel()

		Eventually(errCh, "1s").Should(Receive(BeNil()))
	})

	// TC-U-149: a genuine gRPC Serve failure (the listener is already
	// closed before Serve is ever called, so grpc.Server.quit hasn't
	// fired and the error isn't treated as an expected GracefulStop
	// outcome) is wrapped and returned as serveUntilDone's error.
	It("surfaces a genuine gRPC Serve error (TC-U-149)", func() {
		grpcSrv := grpc.NewServer()
		grpcLn := newLoopbackListener()
		Expect(grpcLn.Close()).To(Succeed()) // closed before Serve is ever called

		oidcSrv := &http.Server{Handler: http.NotFoundHandler()}
		oidcLn := newLoopbackListener()

		runErr := serveUntilDone(context.Background(), slog.New(slog.DiscardHandler), time.Second, grpcSrv, grpcLn, oidcSrv, oidcLn)
		Expect(runErr).To(HaveOccurred())
		Expect(runErr.Error()).To(ContainSubstring("serving gRPC"))
	})

	// TC-U-150: a genuine OIDC HTTP Serve failure (same pre-closed
	// -listener technique, so the error isn't http.ErrServerClosed) is
	// wrapped and returned as serveUntilDone's error.
	It("surfaces a genuine OIDC HTTP Serve error (TC-U-150)", func() {
		grpcSrv := grpc.NewServer()
		grpcLn := newLoopbackListener()

		oidcSrv := &http.Server{Handler: http.NotFoundHandler()}
		oidcLn := newLoopbackListener()
		Expect(oidcLn.Close()).To(Succeed()) // closed before Serve is ever called

		runErr := serveUntilDone(context.Background(), slog.New(slog.DiscardHandler), time.Second, grpcSrv, grpcLn, oidcSrv, oidcLn)
		Expect(runErr).To(HaveOccurred())
		Expect(runErr.Error()).To(ContainSubstring("serving OIDC HTTP"))
	})

	// TC-U-151: an OIDC HTTP Shutdown that cannot finish within the given
	// shutdownTimeout (a real in-flight request still being served) is
	// logged, not returned — mirroring
	// internal/apiserver/server_unit_test.go's TC-U-081 technique
	// (a slow handler + a deliberately tiny timeout), but for
	// serveUntilDone's log-and-continue Shutdown-error branch rather than
	// Run's return-the-error branch.
	It("logs, but does not fail, an OIDC HTTP Shutdown timeout (TC-U-151)", func() {
		grpcSrv := grpc.NewServer()
		grpcLn := newLoopbackListener()

		reqStarted := make(chan struct{})
		oidcSrv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(reqStarted)
			time.Sleep(300 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		})}
		oidcLn := newLoopbackListener()
		oidcAddr := oidcLn.Addr().String()

		var logBuf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			errCh <- serveUntilDone(ctx, logger, 1*time.Millisecond, grpcSrv, grpcLn, oidcSrv, oidcLn)
		}()

		Eventually(func() error {
			conn, err := net.DialTimeout("tcp", oidcAddr, 50*time.Millisecond)
			if err == nil {
				_ = conn.Close()
			}
			return err
		}, "1s", "5ms").Should(Succeed())

		go func() {
			resp, err := http.Get("http://" + oidcAddr) //nolint:noctx,gosec // test helper hitting a loopback address
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		Eventually(reqStarted, "1s").Should(BeClosed())
		cancel()

		Eventually(errCh, "1s").Should(Receive(BeNil()))
		Expect(logBuf.String()).To(ContainSubstring("OIDC HTTP server shutdown error"))
	})
})

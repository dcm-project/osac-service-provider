package main

// Unit scope (mirrors test/cmd/osac-mock-provider/main_unit_test.go): run's
// top-level error-wrapping branches (each failing before the server starts
// serving, so no fakes needed) plus serveUntilDone's own failure/shutdown
// branches, tested directly against real-but-deliberately-broken
// collaborators (a pre-closed net.Listener, a slow http.Handler).

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("run's top-level error wrapping (unit)", func() {
	// TC-U-580: a LoadConfig failure is wrapped and returned, before any
	// listener is bound.
	It("wraps and returns a config-load failure (TC-U-580)", func() {
		// MOCK_AAP_ADDRESS is not set.
		err := run(context.Background(), slog.New(slog.DiscardHandler))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("initializing"))
	})

	// TC-U-581: a listener-bind failure (address already in use) is
	// wrapped and returned.
	It("wraps and returns a listener-bind failure (TC-U-581)", func() {
		held, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = held.Close() }()

		t := GinkgoT()
		t.Setenv("MOCK_AAP_ADDRESS", held.Addr().String()) // already bound by `held`

		runErr := run(context.Background(), slog.New(slog.DiscardHandler))
		Expect(runErr).To(HaveOccurred())
		Expect(runErr.Error()).To(ContainSubstring("listening on"))
	})
})

var _ = Describe("mainRun (unit)", func() {
	// TC-U-582: mainRun maps a run failure to exit code 1, in-process,
	// without invoking os.Exit — mainRun's happy path (exit code 0) is a
	// documented coverage exception (needs a real OS signal to unblock
	// signal.NotifyContext's ctx), same as test/cmd/osac-mock-provider's own.
	It("returns exit code 1 when run fails (TC-U-582)", func() {
		// Same trigger as TC-U-580: MOCK_AAP_ADDRESS is not set.
		Expect(mainRun()).To(Equal(1))
	})
})

var _ = Describe("serveUntilDone (unit)", func() {
	newLoopbackListener := func() net.Listener {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		return ln
	}

	// TC-U-583: a ctx cancellation (the normal shutdown trigger) is
	// reported as a nil error once the server has gracefully stopped.
	It("returns nil when ctx is cancelled (TC-U-583)", func() {
		srv := &http.Server{Handler: http.NotFoundHandler()}
		ln := newLoopbackListener()

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			errCh <- serveUntilDone(ctx, slog.New(slog.DiscardHandler), time.Second, srv, ln)
		}()

		time.Sleep(20 * time.Millisecond)
		cancel()

		Eventually(errCh, "1s").Should(Receive(BeNil()))
	})

	// TC-U-584: a genuine Serve failure (the listener is already closed
	// before Serve is ever called, so it isn't http.ErrServerClosed) is
	// wrapped and returned as serveUntilDone's error.
	It("surfaces a genuine Serve error (TC-U-584)", func() {
		srv := &http.Server{Handler: http.NotFoundHandler()}
		ln := newLoopbackListener()
		Expect(ln.Close()).To(Succeed()) // closed before Serve is ever called

		runErr := serveUntilDone(context.Background(), slog.New(slog.DiscardHandler), time.Second, srv, ln)
		Expect(runErr).To(HaveOccurred())
		Expect(runErr.Error()).To(ContainSubstring("serving AAP mock HTTP"))
	})

	// TC-U-585: a Shutdown that cannot finish within the given
	// shutdownTimeout (a real in-flight request still being served) is
	// logged, not returned.
	It("logs, but does not fail, a Shutdown timeout (TC-U-585)", func() {
		reqStarted := make(chan struct{})
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(reqStarted)
			time.Sleep(300 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		})}
		ln := newLoopbackListener()
		addr := ln.Addr().String()

		var logBuf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			errCh <- serveUntilDone(ctx, logger, 1*time.Millisecond, srv, ln)
		}()

		Eventually(func() error {
			conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
			if err == nil {
				_ = conn.Close()
			}
			return err
		}, "1s", "5ms").Should(Succeed())

		go func() {
			resp, err := http.Get("http://" + addr) //nolint:noctx,gosec // test helper hitting a loopback address
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		Eventually(reqStarted, "1s").Should(BeClosed())
		cancel()

		Eventually(errCh, "1s").Should(Receive(BeNil()))
		Expect(logBuf.String()).To(ContainSubstring("AAP mock HTTP server shutdown error"))
	})
})

package apiserver

// White-box unit tests: this file is package apiserver (not apiserver_test)
// specifically so it can call unexported symbols (waitForReady,
// requestInstance, statusRecordingResponseWriter) directly, the same way
// internal/osac/bootstrap_unit_test.go tests its own unexported helpers —
// these properties are internal implementation details with no exported
// surface to observe them through otherwise.

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dcm-project/osac-service-provider/internal/config"
)

var discardLogger = slog.New(slog.DiscardHandler)

func testCfg() *config.Config {
	return &config.Config{Server: config.ServerConfig{ShutdownTimeout: time.Second}}
}

var _ = Describe("waitForReady", func() {
	// TC-U-077: succeeds once the server starts accepting connections.
	// Also exercises WithReadinessTiming/New's opts loop directly (rather
	// than constructing a Server struct literal), so this Option — a
	// real, reachable code path in production, just never exercised by
	// New's default zero-opts call in the other tests — is covered too.
	It("succeeds once the server starts accepting connections (TC-U-077)", func() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = ln.Close() }()

		mux := http.NewServeMux()
		mux.HandleFunc(healthPath, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		go func() { _ = http.Serve(ln, mux) }() //nolint:gosec // test-only loopback server, no real deployment risk

		s := New(testCfg(), discardLogger, nil, WithReadinessTiming(time.Second, 5*time.Millisecond))
		Expect(s.waitForReady(context.Background(), ln.Addr().String())).To(Succeed())
	})

	// TC-U-089: a malformed address causes http.NewRequestWithContext
	// itself to fail (a raw control character is rejected by net/url),
	// which waitForReady must return as an error rather than looping
	// forever or panicking.
	It("returns an error if the probe request itself cannot be constructed (TC-U-089)", func() {
		s := New(testCfg(), discardLogger, nil, WithReadinessTiming(time.Second, 5*time.Millisecond))
		err := s.waitForReady(context.Background(), "\x7f")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("creating readiness probe request"))
	})

	// TC-U-078: returns a timeout error when the server never becomes
	// reachable within the configured window.
	It("returns a timeout error when the server never becomes reachable (TC-U-078)", func() {
		// Reserve a loopback address and immediately release it, so
		// nothing is listening there for the duration of this test.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		addr := ln.Addr().String()
		Expect(ln.Close()).To(Succeed())

		s := New(testCfg(), discardLogger, nil, WithReadinessTiming(30*time.Millisecond, 5*time.Millisecond))
		err = s.waitForReady(context.Background(), addr)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("timed out"))
	})

	// TC-U-079: returns the context's error when cancelled mid-poll.
	It("returns the context's error when cancelled mid-poll (TC-U-079)", func() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		addr := ln.Addr().String()
		Expect(ln.Close()).To(Succeed())

		ctx, cancel := context.WithCancel(context.Background())
		s := New(testCfg(), discardLogger, nil, WithReadinessTiming(time.Second, 5*time.Millisecond))

		errCh := make(chan error, 1)
		go func() { errCh <- s.waitForReady(ctx, addr) }()

		time.Sleep(15 * time.Millisecond) // let at least one poll attempt happen first
		cancel()

		var gotErr error
		Eventually(errCh, "200ms").Should(Receive(&gotErr))
		Expect(errors.Is(gotErr, context.Canceled)).To(BeTrue())
	})
})

var _ = Describe("requestInstance", func() {
	// TC-U-084: returns nil for a nil request, rather than panicking on a
	// nil pointer dereference.
	It("returns nil for a nil request (TC-U-084)", func() {
		Expect(requestInstance(nil)).To(BeNil())
	})
})

var _ = Describe("statusRecordingResponseWriter", func() {
	// TC-U-085: Unwrap returns the original wrapped writer, per
	// net/http's ResponseController convention for writer wrappers.
	It("Unwrap returns the original wrapped writer (TC-U-085)", func() {
		rec := httptest.NewRecorder()
		sw := &statusRecordingResponseWriter{ResponseWriter: rec, statusCode: http.StatusOK}
		Expect(sw.Unwrap()).To(BeIdenticalTo(rec))
	})

	// TC-U-086: a handler that calls Write directly, without ever calling
	// WriteHeader first (net/http's documented implicit-200 behavior),
	// must still be recorded as status 200 — not left at whatever
	// zero-value statusCode the writer was constructed with.
	It("records status 200 when Write is called without a preceding WriteHeader (TC-U-086)", func() {
		rec := httptest.NewRecorder()
		sw := &statusRecordingResponseWriter{ResponseWriter: rec}
		n, err := sw.Write([]byte("ok"))
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(2))
		Expect(sw.statusCode).To(Equal(http.StatusOK))
	})
})

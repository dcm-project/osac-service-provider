package apiserver_test

// Integration scope (per .ai/test-plans/osac-sp-integration.test-plan.md,
// "1. Server lifecycle", TC-I-001..006): unlike server_unit_test.go, which
// stands in a hand-rolled fakeServerInterface for the real strict adapter,
// these tests wire the REAL health.Handler through the REAL
// oapigen.NewStrictHandlerWithOptions strict adapter into a REAL
// apiserver.Server bound to a REAL loopback listener — closing the "HTTP:
// health.Handler -> strict adapter -> chi router -> real listener" wiring
// gap identified in the Milestone 1 GA readiness audit's pyramid-invariant
// review. Only OSACStatus (real OSAC/Keycloak connectivity) is faked here;
// that real wiring is covered separately by
// cmd/osac-service-provider/main_integration_test.go's TC-I-010..017.
//
// TC-I-006 (startup fails fast with missing config) is NOT here: it
// exercises cmd/osac-service-provider's run(), not apiserver.Server, and
// lives in main_integration_test.go.

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
	"github.com/dcm-project/osac-service-provider/internal/apiserver"
	"github.com/dcm-project/osac-service-provider/internal/config"
	"github.com/dcm-project/osac-service-provider/internal/health"
	"github.com/dcm-project/osac-service-provider/internal/osac"
)

// strictUnimplemented stubs out the Milestone 3 Cluster CRUD methods of
// oapigen.StrictServerInterface — out of scope for this file's
// generic-server-lifecycle tests (dedicated Cluster CRUD integration tests
// live in internal/handlers/cluster). There is no generated
// "Unimplemented" helper for the *strict* interface (only the non-strict
// oapigen.Unimplemented), so this is hand-rolled, mirroring that struct's
// intent.
type strictUnimplemented struct{}

func (strictUnimplemented) ListClusters(context.Context, oapigen.ListClustersRequestObject) (oapigen.ListClustersResponseObject, error) {
	return nil, errors.New("not implemented")
}

func (strictUnimplemented) CreateCluster(context.Context, oapigen.CreateClusterRequestObject) (oapigen.CreateClusterResponseObject, error) {
	return nil, errors.New("not implemented")
}

func (strictUnimplemented) GetCluster(context.Context, oapigen.GetClusterRequestObject) (oapigen.GetClusterResponseObject, error) {
	return nil, errors.New("not implemented")
}

func (strictUnimplemented) DeleteCluster(context.Context, oapigen.DeleteClusterRequestObject) (oapigen.DeleteClusterResponseObject, error) {
	return nil, errors.New("not implemented")
}

// realHandler combines the real health.Handler with strictUnimplemented so
// the result satisfies the full oapigen.StrictServerInterface, exactly as
// cmd/osac-service-provider/main.go's composite handler does.
type realHandler struct {
	*health.Handler
	strictUnimplemented
}

// fakeOSACStatus is a hand-rolled fake satisfying health.OSACStatus, per the
// "no mocking framework" convention. probeFunc, when set, overrides the
// default always-connected behavior (used to simulate a slow downstream
// probe for TC-I-003/004).
type fakeOSACStatus struct {
	tokenStatus osac.TokenStatus
	probeFunc   func(ctx context.Context) osac.ProbeResult
}

func (f *fakeOSACStatus) TokenStatus() osac.TokenStatus { return f.tokenStatus }

func (f *fakeOSACStatus) Probe(ctx context.Context) osac.ProbeResult {
	if f.probeFunc != nil {
		return f.probeFunc(ctx)
	}
	return osac.ProbeResult{Connected: true}
}

var healthyOSACStatus = &fakeOSACStatus{tokenStatus: osac.TokenStatus{Valid: true}}

var integrationDiscardLogger = slog.New(slog.DiscardHandler)

// realStrictHandler wires the real health.Handler through the real strict
// adapter, exactly as cmd/osac-service-provider/main.go does.
func realStrictHandler(osacStatus health.OSACStatus, logger *slog.Logger) oapigen.ServerInterface {
	h := &realHandler{Handler: health.NewHandler(osacStatus, time.Now(), "test-version")}
	return oapigen.NewStrictHandlerWithOptions(h, nil, oapigen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  apiserver.NewRequestErrorHandler(logger),
		ResponseErrorHandlerFunc: apiserver.NewResponseErrorHandler(logger),
	})
}

// runningServer is a Server started on a real loopback listener, with its
// own externally controlled context (standing in for the ctx that
// cmd/osac-service-provider/main.go derives from signal.NotifyContext, per
// Run's documented contract: "pass a context that is cancelled on
// SIGTERM/SIGINT").
type runningServer struct {
	addr   string
	cancel context.CancelFunc
	done   <-chan error
}

func startRunningServer(cfg *config.Config, logger *slog.Logger, handler oapigen.ServerInterface) *runningServer {
	srv := apiserver.New(cfg, logger, handler)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx, ln) }()

	addr := ln.Addr().String()
	Eventually(func() error {
		conn, dialErr := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
		}
		return dialErr
	}, "500ms", "5ms").Should(Succeed())

	return &runningServer{addr: addr, cancel: cancel, done: done}
}

var _ = Describe("Server lifecycle (integration)", func() {
	log := integrationDiscardLogger

	// TC-I-001: server starts and listens on the configured (OS-assigned)
	// address.
	It("starts and accepts TCP connections on the OS-assigned address (TC-I-001)", func() {
		cfg := &config.Config{Server: config.ServerConfig{ShutdownTimeout: time.Second}}
		rs := startRunningServer(cfg, log, realStrictHandler(healthyOSACStatus, log))
		defer func() {
			rs.cancel()
			Eventually(rs.done, "2s").Should(Receive())
		}()

		conn, err := net.DialTimeout("tcp", rs.addr, 200*time.Millisecond)
		Expect(err).NotTo(HaveOccurred())
		_ = conn.Close()
	})

	// TC-I-002: both health routes are reachable end-to-end through the
	// real strict adapter and real chi router.
	It("serves both /clusters/health and /vms/health with HTTP 200 end-to-end (TC-I-002)", func() {
		cfg := &config.Config{Server: config.ServerConfig{ShutdownTimeout: time.Second}}
		rs := startRunningServer(cfg, log, realStrictHandler(healthyOSACStatus, log))
		defer func() {
			rs.cancel()
			Eventually(rs.done, "2s").Should(Receive())
		}()

		for _, path := range []string{"/api/v1alpha1/clusters/health", "/api/v1alpha1/vms/health"} {
			resp, err := http.Get("http://" + rs.addr + path) //nolint:noctx // test helper
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK), "path %s", path)
			_ = resp.Body.Close()
		}
	})

	// TC-I-003: graceful shutdown on context cancellation (standing in for
	// SIGTERM, per Run's documented contract — signal-to-context
	// translation itself is signal.NotifyContext, a stdlib primitive
	// exercised at the main() level, not re-provable here) drains an
	// in-flight request rather than aborting it.
	It("drains an in-flight request to completion when the context is cancelled, simulating SIGTERM (TC-I-003)", func() {
		probeStarted := make(chan struct{})
		osacStatus := &fakeOSACStatus{
			tokenStatus: osac.TokenStatus{Valid: true},
			probeFunc: func(ctx context.Context) osac.ProbeResult {
				close(probeStarted)
				select {
				case <-time.After(500 * time.Millisecond):
				case <-ctx.Done():
				}
				return osac.ProbeResult{Connected: true}
			},
		}
		cfg := &config.Config{Server: config.ServerConfig{ShutdownTimeout: 2 * time.Second}}
		rs := startRunningServer(cfg, log, realStrictHandler(osacStatus, log))

		type result struct {
			status int
			err    error
		}
		respCh := make(chan result, 1)
		go func() {
			resp, err := http.Get("http://" + rs.addr + "/api/v1alpha1/clusters/health") //nolint:noctx // test helper
			if err != nil {
				respCh <- result{err: err}
				return
			}
			defer func() { _ = resp.Body.Close() }()
			respCh <- result{status: resp.StatusCode}
		}()

		Eventually(probeStarted, "1s").Should(BeClosed())
		shutdownStart := time.Now()
		rs.cancel() // simulated SIGTERM

		var res result
		Eventually(respCh, "3s").Should(Receive(&res))
		Expect(res.err).NotTo(HaveOccurred())
		Expect(res.status).To(Equal(http.StatusOK))

		Eventually(rs.done, "3s").Should(Receive(BeNil()))
		Expect(time.Since(shutdownStart)).To(BeNumerically("<", 2*time.Second+500*time.Millisecond))
	})

	// TC-I-004: SIGINT is documented to behave identically to SIGTERM
	// (both are translated to the same ctx cancellation by
	// signal.NotifyContext in main.go — Run itself has no notion of which
	// signal fired). This case exists to keep the test-plan's TC-I-004
	// itemization traceable, and to guard against a future regression that
	// makes Run's shutdown behavior depend on additional state; the
	// exercised code path is identical to TC-I-003's.
	It("behaves identically to TC-I-003 on context cancellation, standing in for SIGINT (TC-I-004)", func() {
		cfg := &config.Config{Server: config.ServerConfig{ShutdownTimeout: 2 * time.Second}}
		rs := startRunningServer(cfg, log, realStrictHandler(healthyOSACStatus, log))

		resp, err := http.Get("http://" + rs.addr + "/api/v1alpha1/clusters/health") //nolint:noctx // test helper
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		_ = resp.Body.Close()

		rs.cancel() // simulated SIGINT
		Eventually(rs.done, "3s").Should(Receive(BeNil()))
	})

	// TC-I-005: once shutdown begins, new connections are no longer
	// accepted.
	It("rejects new connections once shutdown has begun (TC-I-005)", func() {
		cfg := &config.Config{Server: config.ServerConfig{ShutdownTimeout: time.Second}}
		rs := startRunningServer(cfg, log, realStrictHandler(healthyOSACStatus, log))

		rs.cancel()
		Eventually(rs.done, "2s").Should(Receive(BeNil()))

		_, err := net.DialTimeout("tcp", rs.addr, 200*time.Millisecond)
		Expect(err).To(HaveOccurred())
	})
})

package main

// Integration scope (per .ai/test-plans/osac-sp-integration.test-plan.md,
// "2. Health endpoints against real (fake) OSAC + Keycloak" and "4.
// Full-stack smoke test"): these tests run the SP's real cmd/main.go run()
// end to end — real config.Load() from env vars, a real osac.Bootstrap
// performing real OIDC discovery/token-fetch HTTP calls against a fake
// Keycloak httptest.Server, a real gRPC ClientConn dialing a real gRPC
// server (a loopback TCP listener, not bufconn — an equally "real wire"
// substitute for the test-plan's suggested bufconn harness, chosen because
// it requires no new dial-option-injection surface on osac.New), a real
// apiserver.Server on a real loopback listener, and a real environment-agent
// client posting to a fake httptest.Server. This is package main
// (not main_test) specifically so these tests can call the unexported run
// function directly.
//
// TC-I-020..021/023..027 (registration-specific backoff/re-registration
// timing) and TC-I-001..006 (server lifecycle) are NOT here: the former
// needs fast, per-test-configurable backoff/re-registration intervals that
// run()'s fixed production defaults (1s/60s) don't expose via env vars, so
// they use internal/registration's own Options directly (see
// internal/registration/registration_integration_test.go); the latter only
// needs apiserver+health, not the full stack (see
// internal/apiserver/server_integration_test.go). TC-I-022 IS here (below,
// in the health Describe block): it specifically asserts the real health
// endpoint stays responsive while a registration call is pending, which
// needs the real apiserver+health+registrar wiring together — the one
// TC-I-02x case that doesn't fit the internal/registration-only harness.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"

	agentv1alpha1 "github.com/dcm-project/environment-agent/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/internal/versionmatrix"
)

func TestMainIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Main Integration Suite")
}

// ---- fake Keycloak (OIDC discovery + token endpoint) ----

// fakeKeycloak serves a real RFC 8414 discovery document
// (.well-known/oauth-authorization-server) whose token_endpoint is at a
// different path than the issuer root (TC-I-017), plus that token
// endpoint. It deliberately does NOT serve openid-configuration with a
// working token endpoint of its own — only a call counter — since RFC 8414
// succeeding means the OpenID Connect Discovery fallback (unit-test scope
// per TC-U-023/024/025) is never exercised here.
type fakeKeycloak struct {
	server *httptest.Server

	mu                sync.Mutex
	tokenCalls        int
	discoveryCalls    int
	openIDConfigCalls int
	tokenStatus       int
	accessToken       string
	tokenTTL          time.Duration
}

func newFakeKeycloak() *fakeKeycloak {
	k := &fakeKeycloak{tokenStatus: http.StatusOK, accessToken: "fake-access-token", tokenTTL: time.Hour}

	mux := http.NewServeMux()
	mux.HandleFunc("/realms/osac/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		k.mu.Lock()
		k.discoveryCalls++
		k.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"token_endpoint": k.server.URL + "/realms/osac/protocol/openid-connect/token",
		})
	})
	mux.HandleFunc("/realms/osac/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		k.mu.Lock()
		k.openIDConfigCalls++
		k.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"token_endpoint": k.server.URL + "/realms/osac/protocol/openid-connect/token-oidc-fallback",
		})
	})
	mux.HandleFunc("/realms/osac/protocol/openid-connect/token", func(w http.ResponseWriter, _ *http.Request) {
		k.mu.Lock()
		k.tokenCalls++
		status, token, ttl := k.tokenStatus, k.accessToken, k.tokenTTL
		k.mu.Unlock()
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		// Content-Type must be explicit: golang.org/x/oauth2's token
		// response parser branches on it (form-urlencoded vs JSON), and an
		// unset Content-Type is sniffed by net/http rather than treated as
		// JSON, which previously caused spurious parse failures here (seen
		// as unexplained extra token-fetch retries in TC-I-017).
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": token,
			"token_type":   "Bearer",
			"expires_in":   int(ttl.Seconds()),
		})
	})
	// Fail loudly (per the unit test plan's convention) if the SP ever
	// regresses to treating the issuer URL as the token endpoint directly.
	mux.HandleFunc("/realms/osac", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	k.server = httptest.NewServer(mux)
	return k
}

func (k *fakeKeycloak) IssuerURL() string { return k.server.URL + "/realms/osac" }

func (k *fakeKeycloak) TokenCalls() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.tokenCalls
}

func (k *fakeKeycloak) DiscoveryCalls() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.discoveryCalls
}

func (k *fakeKeycloak) OpenIDConfigCalls() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.openIDConfigCalls
}

func (k *fakeKeycloak) SetTokenStatus(status int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.tokenStatus = status
}

func (k *fakeKeycloak) Close() { k.server.Close() }

// ---- fake OSAC Capabilities gRPC server (real loopback TCP, not bufconn) ----

type fakeCapabilitiesImpl struct {
	publicv1.UnimplementedCapabilitiesServer

	mu    sync.Mutex
	err   error
	delay time.Duration
}

func (f *fakeCapabilitiesImpl) Get(ctx context.Context, _ *publicv1.CapabilitiesGetRequest) (*publicv1.CapabilitiesGetResponse, error) {
	f.mu.Lock()
	delay, err := f.delay, f.err
	f.mu.Unlock()

	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return &publicv1.CapabilitiesGetResponse{}, nil
}

func (f *fakeCapabilitiesImpl) SetDelay(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delay = d
}

// reserveLoopbackAddr binds an ephemeral loopback port, notes its address,
// then immediately releases it so a real server can be started (or
// deliberately left unstarted, to simulate OSAC being unreachable) on that
// exact address later, once it's known — needed because run() reads
// cfg.OSAC.FulfillmentAddress from an env var that must be set before run()
// starts, but before any fake gRPC server needs to exist (TC-I-012/013's
// "unreachable, then becomes reachable" scenario).
func reserveLoopbackAddr() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	addr := ln.Addr().String()
	Expect(ln.Close()).To(Succeed())
	return addr
}

// grpcServerHandle is a real gRPC server bound to a specific loopback
// address (reserved ahead of time via reserveLoopbackAddr).
type grpcServerHandle struct {
	server *grpc.Server
}

func startCapabilitiesServer(addr string, impl publicv1.CapabilitiesServer) *grpcServerHandle {
	ln, err := net.Listen("tcp", addr)
	Expect(err).NotTo(HaveOccurred())
	s := grpc.NewServer()
	publicv1.RegisterCapabilitiesServer(s, impl)
	go func() { _ = s.Serve(ln) }()
	return &grpcServerHandle{server: s}
}

func (h *grpcServerHandle) Stop() { h.server.Stop() }

// ---- fake environment-agent (real httptest.Server implementing POST
// /providers, per environment-agent's current, documented SP API contract,
// DD-203) ----

type fakeProviderRequest struct {
	provider agentv1alpha1.Provider
	headers  http.Header
}

type fakeProviderServer struct {
	server *httptest.Server

	mu        sync.Mutex
	reqs      []fakeProviderRequest
	responder func(agentv1alpha1.Provider) (statusCode int, body any, contentType string)
}

func alwaysCreated201(p agentv1alpha1.Provider) (int, any, string) {
	return http.StatusCreated, p, "application/json"
}

func newFakeProviderServer() *fakeProviderServer {
	f := &fakeProviderServer{responder: alwaysCreated201}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1alpha1/providers", func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()

		var p agentv1alpha1.Provider
		_ = json.Unmarshal(bodyBytes, &p)

		f.mu.Lock()
		f.reqs = append(f.reqs, fakeProviderRequest{provider: p, headers: r.Header.Clone()})
		responder := f.responder
		f.mu.Unlock()

		status, respBody, contentType := responder(p)
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(respBody)
	})
	f.server = httptest.NewServer(mux)
	return f
}

func (f *fakeProviderServer) URL() string { return f.server.URL + "/api/v1alpha1" }

func (f *fakeProviderServer) Requests() []fakeProviderRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeProviderRequest, len(f.reqs))
	copy(out, f.reqs)
	return out
}

func (f *fakeProviderServer) RequestsFor(serviceType string) []fakeProviderRequest {
	var out []fakeProviderRequest
	for _, r := range f.Requests() {
		if r.provider.ServiceType == serviceType {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeProviderServer) SetResponder(fn func(agentv1alpha1.Provider) (int, any, string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responder = fn
}

func (f *fakeProviderServer) Close() { f.server.Close() }

// ---- full-stack harness ----

// spHarness wires all three fakes and drives a real run() invocation.
type spHarness struct {
	keycloak         *fakeKeycloak
	capImpl          *fakeCapabilitiesImpl
	grpc             *grpcServerHandle
	grpcAddr         string
	environmentAgent *fakeProviderServer

	serverAddr string
	cancel     context.CancelFunc
	runDone    <-chan error
}

// startGRPCServer starts the real Capabilities gRPC server on the address
// already configured (via SP_OSAC_FULFILLMENT_ADDRESS) but left unstarted
// by startSP(false) — simulating OSAC becoming reachable after the SP was
// already running (TC-I-013). The osac.Bootstrap's persistent
// grpc.ClientConn (created at startup against this same address) retries
// connecting in the background on its own, so no SP-side action is needed
// beyond this.
func (h *spHarness) startGRPCServer() {
	Expect(h.grpc).To(BeNil(), "gRPC server already started")
	h.grpc = startCapabilitiesServer(h.grpcAddr, h.capImpl)
}

// startSP sets the required env vars, starts the real gRPC Capabilities
// server (unless startGRPC is false, simulating OSAC being unreachable from
// boot), and runs cmd/main's real run() in the background.
func startSP(startGRPC bool) *spHarness {
	return startSPWithOptions(spStartOptions{startGRPC: startGRPC})
}

// spStartOptions extends startSP with less-common startup conditions that
// must be established deterministically *before* run() launches (as
// opposed to a test mutating harness state afterward and racing run()'s
// own background goroutines — see keycloakDown below).
type spStartOptions struct {
	startGRPC      bool
	keycloakDown   bool
	agentResponder func(agentv1alpha1.Provider) (int, any, string)
}

// startSPWithOptions is startSP's implementation. keycloakDown closes the
// fake Keycloak server before run() starts, rather than leaving the test to
// close it afterward: closing it post-hoc races osac.Bootstrap's
// background token-fetch goroutine, which can win that race and cache a
// valid token before the test's Close() call lands (TC-I-011 relies on
// Keycloak being unreachable from the very first fetch attempt, not on
// winning a race).
func startSPWithOptions(opts spStartOptions) *spHarness {
	h := &spHarness{
		keycloak:         newFakeKeycloak(),
		capImpl:          &fakeCapabilitiesImpl{},
		environmentAgent: newFakeProviderServer(),
	}
	if opts.agentResponder != nil {
		// Set before run() launches (not after startSPWithOptions
		// returns): otherwise the default alwaysCreated201 responder
		// could already have answered the very first registration
		// attempt before the test gets a chance to swap it out, same
		// race rationale as keycloakDown below.
		h.environmentAgent.SetResponder(opts.agentResponder)
	}

	// Capture the issuer URL string before potentially closing the
	// server: it remains a valid (now-unreachable) URL either way.
	issuerURL := h.keycloak.IssuerURL()
	if opts.keycloakDown {
		h.keycloak.Close()
	}

	grpcAddr := reserveLoopbackAddr()
	h.grpcAddr = grpcAddr
	if opts.startGRPC {
		h.grpc = startCapabilitiesServer(grpcAddr, h.capImpl)
	}

	h.serverAddr = reserveLoopbackAddr()

	t := GinkgoT()
	t.Setenv("SP_SERVER_ADDRESS", h.serverAddr)
	t.Setenv("SP_SERVER_SHUTDOWN_TIMEOUT", "2s")
	t.Setenv("SP_OSAC_FULFILLMENT_ADDRESS", grpcAddr)
	t.Setenv("SP_OSAC_OIDC_ISSUER_URL", issuerURL)
	t.Setenv("SP_OSAC_OIDC_CLIENT_ID", "osac-sp")
	t.Setenv("SP_OSAC_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("SP_OSAC_TLS_ENABLED", "false")
	t.Setenv("SP_OSAC_PROBE_TIMEOUT", "1s")
	t.Setenv("DCM_REGISTRATION_URL", h.environmentAgent.URL())
	t.Setenv("DCM_NATS_URL", "nats://127.0.0.1:4222")
	t.Setenv("SP_ENDPOINT", "https://osac-sp.example.com")
	t.Setenv("SP_PROVIDER_CLUSTER_NAME", "osac-sp-cluster")
	t.Setenv("SP_PROVIDER_VM_NAME", "osac-sp-vm")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, slog.New(slog.DiscardHandler)) }()

	h.cancel = cancel
	h.runDone = done

	Eventually(func() error {
		conn, err := net.DialTimeout("tcp", h.serverAddr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
		}
		return err
	}, "2s", "10ms").Should(Succeed())

	return h
}

func (h *spHarness) stop() {
	h.cancel()
	Eventually(h.runDone, "3s").Should(Receive())
	h.environmentAgent.Close()
	h.keycloak.Close()
	if h.grpc != nil {
		h.grpc.Stop()
	}
}

func (h *spHarness) getHealth(path string) (status int, body map[string]any) {
	resp, err := http.Get(fmt.Sprintf("http://%s%s", h.serverAddr, path)) //nolint:noctx // test helper
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()

	var b map[string]any
	Expect(json.NewDecoder(resp.Body).Decode(&b)).To(Succeed())
	return resp.StatusCode, b
}

var _ = Describe("Health end-to-end (integration)", func() {
	// TC-I-010: healthy end-to-end against real (fake) Keycloak + real gRPC
	// Capabilities server.
	It("reports healthy end-to-end when Keycloak and OSAC are both reachable (TC-I-010)", func() {
		h := startSP(true)
		defer h.stop()

		// The first OIDC token fetch and gRPC dial happen in the
		// background, independently of when the HTTP listener becomes
		// dialable, so an immediate check can transiently observe
		// "unhealthy" during startup; wait for the steady state.
		Eventually(func() string {
			_, body := h.getHealth("/api/v1alpha1/clusters/health")
			return body["status"].(string) //nolint:forcetypeassert // test helper
		}, "2s", "20ms").Should(Equal("healthy"))

		status, body := h.getHealth("/api/v1alpha1/clusters/health")
		Expect(status).To(Equal(http.StatusOK))
		Expect(body["status"]).To(Equal("healthy"))
	})

	// TC-I-011: Keycloak unreachable at startup -> unhealthy, HTTP 200.
	It("reports unhealthy (HTTP 200) when Keycloak is unreachable from the start (TC-I-011)", func() {
		// keycloakDown closes the fake Keycloak before run() starts, so
		// the very first token-fetch attempt fails deterministically —
		// see startSPWithOptions' doc comment for why this must not be
		// done by closing it after startSP returns.
		h := startSPWithOptions(spStartOptions{startGRPC: true, keycloakDown: true})
		defer h.stop()

		Eventually(func() string {
			_, body := h.getHealth("/api/v1alpha1/clusters/health")
			return body["status"].(string) //nolint:forcetypeassert // test helper
		}, "2s", "20ms").Should(Equal("unhealthy"))

		status, _ := h.getHealth("/api/v1alpha1/clusters/health")
		Expect(status).To(Equal(http.StatusOK))
	})

	// TC-I-012: OSAC gRPC unreachable -> unhealthy, HTTP 200.
	It("reports unhealthy (HTTP 200) when the OSAC gRPC server is unreachable (TC-I-012)", func() {
		h := startSP(false) // gRPC server deliberately not started
		defer h.stop()

		status, body := h.getHealth("/api/v1alpha1/clusters/health")
		Expect(status).To(Equal(http.StatusOK))
		Expect(body["status"]).To(Equal("unhealthy"))
	})

	// TC-I-013: recovers once OSAC becomes reachable, proving the probe is
	// re-evaluated per request rather than cached.
	It("recovers to healthy once the OSAC gRPC server starts (TC-I-013)", func() {
		h := startSP(false)
		defer h.stop()

		_, body := h.getHealth("/api/v1alpha1/clusters/health")
		Expect(body["status"]).To(Equal("unhealthy"))

		h.startGRPCServer()

		// The osac.Bootstrap's grpc.ClientConn reconnects to the
		// now-reachable address using its own internal backoff, without
		// any application-level intervention; poll until the probe (which
		// is re-evaluated per request, never cached) reflects that.
		Eventually(func() string {
			_, body := h.getHealth("/api/v1alpha1/clusters/health")
			return body["status"].(string) //nolint:forcetypeassert // test helper
		}, "45s", "200ms").Should(Equal("healthy"))
	})

	// TC-I-014: token is cached, not re-fetched on every health poll.
	It("does not re-fetch the token on repeated health polls (TC-I-014)", func() {
		h := startSP(true)
		defer h.stop()

		// Wait for the initial (background) token fetch to land — this
		// alone does not add extra token-endpoint calls beyond the one
		// background fetch, since TokenStatus() is a pure read.
		Eventually(func() string {
			_, body := h.getHealth("/api/v1alpha1/clusters/health")
			return body["status"].(string) //nolint:forcetypeassert // test helper
		}, "2s", "20ms").Should(Equal("healthy"))

		for range 10 {
			status, body := h.getHealth("/api/v1alpha1/clusters/health")
			Expect(status).To(Equal(http.StatusOK))
			Expect(body["status"]).To(Equal("healthy"))
		}

		Expect(h.keycloak.TokenCalls()).To(Equal(1))
	})

	// TC-I-015: the health probe respects the configured OSAC probe
	// timeout rather than blocking for as long as the backend takes. The
	// fake backend's delay (10s) is deliberately far longer than
	// probeTimeout (1s, see startSP) so a passing assertion actually proves
	// the timeout bounds the wait — not a coincidence of the two durations
	// being close together (the original 1s-delay-vs-1s-timeout setup
	// here could never satisfy any "elapsed < probeTimeout" bound, since
	// the probe legitimately blocks for the full timeout).
	It("returns within the configured probe timeout when OSAC is slow (TC-I-015)", func() {
		h := startSP(true)
		defer h.stop()
		h.capImpl.SetDelay(10 * time.Second)

		start := time.Now()
		status, body := h.getHealth("/api/v1alpha1/clusters/health")
		elapsed := time.Since(start)

		Expect(status).To(Equal(http.StatusOK))
		Expect(body["status"]).To(Equal("unhealthy"))
		// Bounded by probeTimeout (1s), with headroom for scheduling
		// jitter — and well under the 10s backend delay, which is the
		// actual behavior under test.
		Expect(elapsed).To(BeNumerically("<", 2*time.Second))
	})

	// TC-I-016: both health paths agree end-to-end.
	It("reports the same status from both health paths (TC-I-016)", func() {
		h := startSP(false) // fixed unhealthy state
		defer h.stop()

		statusA, bodyA := h.getHealth("/api/v1alpha1/clusters/health")
		statusB, bodyB := h.getHealth("/api/v1alpha1/vms/health")

		Expect(statusA).To(Equal(statusB))
		Expect(bodyA["status"]).To(Equal(bodyB["status"]))
	})

	// TC-I-017: the token fetch uses the discovered endpoint, not the bare
	// issuer URL, and RFC 8414 succeeding means OpenID Connect Discovery is
	// never consulted.
	It("fetches the token from the discovered endpoint, not the issuer URL, with no OIDC fallback (TC-I-017)", func() {
		h := startSP(true)
		defer h.stop()

		Eventually(h.keycloak.TokenCalls, "2s", "10ms").Should(Equal(1))
		Expect(h.keycloak.DiscoveryCalls()).To(BeNumerically(">=", 1))
		Expect(h.keycloak.OpenIDConfigCalls()).To(Equal(0))
	})

	// TC-I-022: registration does not block server readiness — the health
	// endpoint, served on a completely independent listener/goroutine from
	// registration.Registrar's runLoop, must respond normally even while a
	// registration request to the fake environment-agent is still pending.
	It("keeps the health endpoint responsive while registration is still pending (TC-I-022)", func() {
		release := make(chan struct{})
		h := startSPWithOptions(spStartOptions{
			startGRPC: true,
			agentResponder: func(p agentv1alpha1.Provider) (int, any, string) {
				<-release // held open until explicitly released below
				return http.StatusCreated, p, "application/json"
			},
		})
		defer func() {
			close(release) // unblock the fake's handler goroutine before Close()
			h.stop()
		}()

		// The registration attempt is recorded (in flight) but its HTTP
		// response is still withheld — proving the health check below is
		// answered by a genuinely independent code path, not one waiting
		// on registration to finish first.
		Eventually(func() []fakeProviderRequest { return h.environmentAgent.Requests() }, "1s", "10ms").ShouldNot(BeEmpty())

		status, _ := h.getHealth("/api/v1alpha1/clusters/health")
		Expect(status).To(Equal(http.StatusOK))
	})
})

var _ = Describe("Full-stack smoke test (integration)", func() {
	// TC-I-030: cold start reaches healthy and fully registered.
	It("reaches healthy and registers both service types from a cold start (TC-I-030)", func() {
		h := startSP(true)
		defer h.stop()

		Eventually(func() string {
			_, body := h.getHealth("/api/v1alpha1/clusters/health")
			return body["status"].(string) //nolint:forcetypeassert // test helper
		}, "2s", "20ms").Should(Equal("healthy"))

		_, vmBody := h.getHealth("/api/v1alpha1/vms/health")
		Expect(vmBody["status"]).To(Equal("healthy"))

		Eventually(func() []fakeProviderRequest { return h.environmentAgent.RequestsFor("cluster") }, "2s", "20ms").ShouldNot(BeEmpty())
		Eventually(func() []fakeProviderRequest { return h.environmentAgent.RequestsFor("vm") }, "2s", "20ms").ShouldNot(BeEmpty())
	})

	// TC-I-521 (REQ-VERSION-090, AC-VERSION-100): with
	// SP_VERSION_MATRIX_PATH left unset, a cold start advertises exactly
	// versionmatrix.DefaultMatrix's SupportedVersions() — not a
	// coincidentally-similar hand-typed list.
	It("advertises DefaultMatrix's SupportedVersions() when SP_VERSION_MATRIX_PATH is unset (TC-I-521)", func() {
		h := startSP(true)
		defer h.stop()

		Eventually(func() []fakeProviderRequest { return h.environmentAgent.RequestsFor("cluster") }, "2s", "20ms").ShouldNot(BeEmpty())

		req := h.environmentAgent.RequestsFor("cluster")[0]
		versions, ok := req.provider.Metadata.Get("kubernetes_supported_versions")
		Expect(ok).To(BeTrue())
		Expect(versions).To(ConsistOf(versionmatrix.DefaultMatrix.SupportedVersions()))
	})
})

// TC-I-520 (REQ-VERSION-090, AC-VERSION-040/090): startup fails fast when
// SP_VERSION_MATRIX_PATH is set but invalid — the process exits non-zero
// before the HTTP listener opens, and before any registration request
// reaches the fake environment-agent. Uses mainRun() directly (same in-process
// technique as TC-U-097) rather than the startSP harness, since run()
// returns its error synchronously here, before any background goroutine
// (osac.Bootstrap.Start, registrar.Start) is ever launched.
var _ = Describe("Full-stack startup fails fast on an invalid version matrix (integration)", func() {
	It("fails fast before opening the listener or registering, when SP_VERSION_MATRIX_PATH is invalid (TC-I-520)", func() {
		keycloak := newFakeKeycloak()
		defer keycloak.Close()
		environmentAgent := newFakeProviderServer()
		defer environmentAgent.Close()

		grpcAddr := reserveLoopbackAddr()
		serverAddr := reserveLoopbackAddr()

		t := GinkgoT()
		t.Setenv("SP_SERVER_ADDRESS", serverAddr)
		t.Setenv("SP_SERVER_SHUTDOWN_TIMEOUT", "2s")
		t.Setenv("SP_OSAC_FULFILLMENT_ADDRESS", grpcAddr)
		t.Setenv("SP_OSAC_OIDC_ISSUER_URL", keycloak.IssuerURL())
		t.Setenv("SP_OSAC_OIDC_CLIENT_ID", "osac-sp")
		t.Setenv("SP_OSAC_OIDC_CLIENT_SECRET", "secret")
		t.Setenv("SP_OSAC_TLS_ENABLED", "false")
		t.Setenv("SP_OSAC_PROBE_TIMEOUT", "1s")
		t.Setenv("DCM_REGISTRATION_URL", environmentAgent.URL())
		t.Setenv("SP_ENDPOINT", "https://osac-sp.example.com")
		t.Setenv("SP_PROVIDER_CLUSTER_NAME", "osac-sp-cluster")
		t.Setenv("SP_PROVIDER_VM_NAME", "osac-sp-vm")
		t.Setenv("SP_VERSION_MATRIX_PATH", "/nonexistent/path/version-matrix.json")

		Expect(mainRun()).To(Equal(1))

		// No listener was bound: serverAddr is still free to claim.
		ln, err := net.Listen("tcp", serverAddr)
		Expect(err).NotTo(HaveOccurred())
		_ = ln.Close()

		Expect(environmentAgent.Requests()).To(BeEmpty())
	})
})

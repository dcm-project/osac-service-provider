package osac

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"

	"github.com/dcm-project/osac-service-provider/internal/config"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// tokenResult is one queued response for fakeTokenSource.
type tokenResult struct {
	token *oauth2.Token
	err   error
}

// fakeTokenSource is an oauth2.TokenSource returning queued canned
// responses and counting calls, per the unit test plan's fake collaborator
// for the OIDC token endpoint.
type fakeTokenSource struct {
	mu    sync.Mutex
	queue []tokenResult
	calls int
}

func (f *fakeTokenSource) Token() (*oauth2.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if len(f.queue) == 0 {
		return nil, errors.New("fakeTokenSource: queue exhausted")
	}
	r := f.queue[0]
	if len(f.queue) > 1 {
		f.queue = f.queue[1:]
	}
	return r.token, r.err
}

func (f *fakeTokenSource) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeCapabilitiesClient is a hand-rolled fake satisfying
// publicv1.CapabilitiesClient directly, per the unit test plan's "no real
// gRPC dialing in unit scope" convention.
type fakeCapabilitiesClient struct {
	err error
}

func (f *fakeCapabilitiesClient) Get(_ context.Context, _ *publicv1.CapabilitiesGetRequest, _ ...grpc.CallOption) (*publicv1.CapabilitiesGetResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &publicv1.CapabilitiesGetResponse{}, nil
}

// fakeOIDCIssuer is a real httptest.Server implementing both well-known
// discovery documents (RFC 8414's oauth-authorization-server and OpenID
// Connect Discovery's openid-configuration) plus its own advertised token
// endpoint, per DD-060 / TC-U-023/024/025. Each discovery document's status
// is independently configurable so tests can exercise the primary path, the
// fallback path, or both failing. Requests landing on the bare issuer path
// itself (rather than the discovered token endpoint) are recorded
// separately, so a regression to treating the issuer URL as the token
// endpoint fails a test loudly instead of silently "working" against a
// too-permissive fake.
const (
	fakeIssuerPath    = "/realms/osac"
	fakeTokenPath     = fakeIssuerPath + "/protocol/openid-connect/token"
	fakeOAuthASPath   = fakeIssuerPath + "/.well-known/oauth-authorization-server"
	fakeDiscoveryPath = fakeIssuerPath + "/.well-known/openid-configuration"
)

type fakeOIDCIssuer struct {
	server *httptest.Server

	mu              sync.Mutex
	tokenCalls      int
	issuerRootCalls int
	oauthASCalls    int
	oauthASStatus   int
	discoveryCalls  int
	discoveryStatus int
	tokenStatus     int
}

func newFakeOIDCIssuer() *fakeOIDCIssuer {
	f := &fakeOIDCIssuer{oauthASStatus: http.StatusOK, discoveryStatus: http.StatusOK, tokenStatus: http.StatusOK}

	mux := http.NewServeMux()
	mux.HandleFunc(fakeOAuthASPath, func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.oauthASCalls++
		status := f.oauthASStatus
		f.mu.Unlock()
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"token_endpoint": f.server.URL + fakeTokenPath})
	})
	mux.HandleFunc(fakeDiscoveryPath, func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.discoveryCalls++
		status := f.discoveryStatus
		f.mu.Unlock()
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"token_endpoint": f.server.URL + fakeTokenPath})
	})
	mux.HandleFunc(fakeTokenPath, func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.tokenCalls++
		status := f.tokenStatus
		f.mu.Unlock()
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-discovered",
			"token_type":   "Bearer",
			"expires_in":   300,
		})
	})
	mux.HandleFunc(fakeIssuerPath, func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.issuerRootCalls++
		f.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	})

	f.server = httptest.NewServer(mux)
	return f
}

func (f *fakeOIDCIssuer) issuerURL() string { return f.server.URL + fakeIssuerPath }

func (f *fakeOIDCIssuer) TokenCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokenCalls
}

func (f *fakeOIDCIssuer) IssuerRootCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.issuerRootCalls
}

func (f *fakeOIDCIssuer) OAuthASCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.oauthASCalls
}

func (f *fakeOIDCIssuer) DiscoveryCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.discoveryCalls
}

func (f *fakeOIDCIssuer) SetOAuthASStatus(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.oauthASStatus = status
}

func (f *fakeOIDCIssuer) SetDiscoveryStatus(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.discoveryStatus = status
}

func testCfg() *config.OSACConfig {
	return &config.OSACConfig{
		FulfillmentAddress: "osac.example.com:443",
		OIDCIssuerURL:      "https://keycloak.example.com/token",
		OIDCClientID:       "osac-sp",
		OIDCClientSecret:   "secret",
		ProbeTimeout:       time.Second,
	}
}

var discardLogger = slog.New(slog.DiscardHandler)

var _ = Describe("Bootstrap", func() {
	Describe("TokenStatus", func() {
		// TC-U-016: no token fetched yet -> invalid
		It("reports invalid before any token has been fetched (TC-U-016)", func() {
			b := newBootstrap(testCfg(), discardLogger, &fakeTokenSource{}, &fakeCapabilitiesClient{})
			Expect(b.TokenStatus().Valid).To(BeFalse())
		})

		// TC-U-017: cached token not yet expired -> valid
		It("reports valid when the cached token has not expired (TC-U-017)", func() {
			now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			b := newBootstrap(testCfg(), discardLogger, &fakeTokenSource{}, &fakeCapabilitiesClient{}, WithNow(func() time.Time { return now }))
			b.setToken("tok", now.Add(1*time.Hour))

			status := b.TokenStatus()
			Expect(status.Valid).To(BeTrue())
			Expect(status.ExpiresAt).To(Equal(now.Add(1 * time.Hour)))
		})

		// TC-U-018: cached token expired -> invalid
		It("reports invalid once the cached token has expired (TC-U-018)", func() {
			now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			b := newBootstrap(testCfg(), discardLogger, &fakeTokenSource{}, &fakeCapabilitiesClient{}, WithNow(func() time.Time { return now }))
			b.setToken("tok", now.Add(-1*time.Second))

			Expect(b.TokenStatus().Valid).To(BeFalse())
		})
	})

	Describe("Start (token fetch/refresh loop)", func() {
		// TC-U-010: successful fetch on start
		It("obtains and caches a token shortly after Start (TC-U-010)", func() {
			ts := &fakeTokenSource{queue: []tokenResult{
				{token: &oauth2.Token{AccessToken: "tok-1", Expiry: time.Now().Add(1 * time.Hour)}},
			}}
			b := newBootstrap(testCfg(), discardLogger, ts, &fakeCapabilitiesClient{})

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b.Start(ctx)

			Eventually(func() bool { return b.TokenStatus().Valid }, "1s", "5ms").Should(BeTrue())
		})

		// TC-U-013/014: fetch failure is retried with backoff and does not
		// block/crash; eventually succeeds once the source recovers.
		It("retries with backoff after a fetch failure and recovers (TC-U-013/014)", func() {
			ts := &fakeTokenSource{queue: []tokenResult{
				{err: errors.New("keycloak unavailable")},
				{err: errors.New("keycloak unavailable")},
				{token: &oauth2.Token{AccessToken: "tok-1", Expiry: time.Now().Add(1 * time.Hour)}},
			}}
			b := newBootstrap(testCfg(), discardLogger, ts, &fakeCapabilitiesClient{}, WithInitialBackoff(5*time.Millisecond), WithMaxBackoff(20*time.Millisecond))

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b.Start(ctx)

			Eventually(func() bool { return b.TokenStatus().Valid }, "1s", "5ms").Should(BeTrue())
			Expect(ts.Calls()).To(BeNumerically(">=", 3))
		})

		// TC-U-015: Start returns immediately (non-blocking) even when the
		// token source always fails.
		It("returns immediately from Start even when the token source always fails (TC-U-015)", func() {
			ts := &fakeTokenSource{queue: []tokenResult{{err: errors.New("always fails")}}}
			b := newBootstrap(testCfg(), discardLogger, ts, &fakeCapabilitiesClient{}, WithInitialBackoff(time.Hour))

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := make(chan struct{})
			go func() {
				b.Start(ctx)
				close(done)
			}()
			Eventually(done, "100ms").Should(BeClosed())
		})
	})

	Describe("Probe", func() {
		// TC-U-019: connected
		It("reports connected when the Capabilities client succeeds (TC-U-019)", func() {
			b := newBootstrap(testCfg(), discardLogger, &fakeTokenSource{}, &fakeCapabilitiesClient{})
			result := b.Probe(context.Background())
			Expect(result.Connected).To(BeTrue())
			Expect(result.Err).NotTo(HaveOccurred())
		})

		// TC-U-020: not connected
		It("reports not connected when the Capabilities client errors (TC-U-020)", func() {
			b := newBootstrap(testCfg(), discardLogger, &fakeTokenSource{}, &fakeCapabilitiesClient{err: errors.New("unavailable")})
			result := b.Probe(context.Background())
			Expect(result.Connected).To(BeFalse())
			Expect(result.Err).To(HaveOccurred())
		})

		// TC-U-022: Probe never triggers a token fetch.
		It("never triggers a token fetch (TC-U-022)", func() {
			ts := &fakeTokenSource{queue: []tokenResult{{token: &oauth2.Token{AccessToken: "tok", Expiry: time.Now().Add(time.Hour)}}}}
			b := newBootstrap(testCfg(), discardLogger, ts, &fakeCapabilitiesClient{})

			for range 5 {
				b.Probe(context.Background())
			}

			Expect(ts.Calls()).To(Equal(0))
		})
	})

	Describe("New (real OIDC discovery + client-credentials, DD-060)", func() {
		var issuer *fakeOIDCIssuer

		BeforeEach(func() {
			issuer = newFakeOIDCIssuer()
		})

		AfterEach(func() {
			issuer.server.Close()
		})

		// TC-U-023: token requests go to the endpoint discovered from the
		// issuer's .well-known/oauth-authorization-server document (RFC
		// 8414, tried first), never to the bare issuer URL itself, and the
		// OIDC fallback is never consulted when the primary path succeeds.
		It("discovers the token endpoint via RFC 8414 before fetching a token (TC-U-023)", func() {
			cfg := testCfg()
			cfg.OIDCIssuerURL = issuer.issuerURL()

			b, err := New(cfg, discardLogger, WithInitialBackoff(5*time.Millisecond), WithMaxBackoff(20*time.Millisecond))
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = b.Close() }()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b.Start(ctx)

			Eventually(func() bool { return b.TokenStatus().Valid }, "1s", "5ms").Should(BeTrue())
			Expect(issuer.TokenCalls()).To(BeNumerically(">=", 1))
			Expect(issuer.IssuerRootCalls()).To(Equal(0))
			Expect(issuer.OAuthASCalls()).To(BeNumerically(">=", 1))
			Expect(issuer.DiscoveryCalls()).To(Equal(0))
		})

		// TC-U-024: a discovery failure (both well-known documents failing)
		// does not panic or block Start, and is retried with the same
		// exponential backoff as token-fetch failures, recovering once
		// discovery succeeds.
		It("retries discovery with backoff after both well-known documents fail, without blocking, and recovers (TC-U-024)", func() {
			issuer.SetOAuthASStatus(http.StatusInternalServerError)
			issuer.SetDiscoveryStatus(http.StatusInternalServerError)

			cfg := testCfg()
			cfg.OIDCIssuerURL = issuer.issuerURL()

			b, err := New(cfg, discardLogger, WithInitialBackoff(5*time.Millisecond), WithMaxBackoff(20*time.Millisecond))
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = b.Close() }()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b.Start(ctx)

			Consistently(func() bool { return b.TokenStatus().Valid }, "40ms", "5ms").Should(BeFalse())
			Expect(issuer.TokenCalls()).To(Equal(0))

			issuer.SetOAuthASStatus(http.StatusOK)
			issuer.SetDiscoveryStatus(http.StatusOK)
			Eventually(func() bool { return b.TokenStatus().Valid }, "1s", "5ms").Should(BeTrue())
		})

		// TC-U-025: when the RFC 8414 document is unavailable (e.g. a
		// Keycloak realm that doesn't expose oauth-authorization-server, a
		// 404), the SP MUST fall back to OpenID Connect Discovery
		// (openid-configuration) and use its token_endpoint.
		It("falls back to OpenID Connect discovery when RFC 8414 discovery fails (TC-U-025)", func() {
			issuer.SetOAuthASStatus(http.StatusNotFound)

			cfg := testCfg()
			cfg.OIDCIssuerURL = issuer.issuerURL()

			b, err := New(cfg, discardLogger, WithInitialBackoff(5*time.Millisecond), WithMaxBackoff(20*time.Millisecond))
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = b.Close() }()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b.Start(ctx)

			Eventually(func() bool { return b.TokenStatus().Valid }, "1s", "5ms").Should(BeTrue())
			Expect(issuer.OAuthASCalls()).To(BeNumerically(">=", 1))
			Expect(issuer.DiscoveryCalls()).To(BeNumerically(">=", 1))
			Expect(issuer.TokenCalls()).To(BeNumerically(">=", 1))
		})
	})
})

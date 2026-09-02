// Package osac establishes and maintains the service provider's connection
// to OSAC: an OIDC client-credentials token source against OSAC's Keycloak,
// and a gRPC ClientConn to the fulfillment service with a per-RPC
// bearer-token interceptor.
//
// Implements Topic 4.2 (OSAC Client Bootstrap) of the Milestone 1 spec. Per
// DD-020, only the osac.public.v1.Capabilities client is generated/used in
// this milestone — the full CRUD service clients land in Milestone 2.
package osac

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/dcm-project/osac-service-provider/internal/config"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

// oidcDiscoveryTimeout bounds a single OIDC discovery document fetch
// (REQ-OSAC-011).
const oidcDiscoveryTimeout = 10 * time.Second

// oidcServerMetadata is the subset of RFC 8414 / OpenID Connect Discovery
// 1.0 metadata this SP needs.
type oidcServerMetadata struct {
	TokenEndpoint string `json:"token_endpoint"`
}

// oidcWellKnownEndpoints lists the well-known discovery documents to try, in
// order (REQ-OSAC-011/DD-060). This mirrors
// osac-project/fulfillment-service's own
// internal/oauth.DiscoveryTool.Discover(), which backs that project's own
// client-credentials TokenSource — the same grant type this SP performs,
// against the same class of Keycloak issuer. It tries RFC 8414
// ("oauth-authorization-server") first and falls back to OpenID Connect
// Discovery 1.0 ("openid-configuration") only if that fails. (osac-ux's
// proxy/auth/oidc.go queries only "openid-configuration" — but that backs a
// human browser login flow, not client-credentials, so it isn't the right
// thing to mirror here; an earlier version of this code did exactly that,
// which DD-060 documents as a corrected hallucination.)
var oidcWellKnownEndpoints = []string{
	"oauth-authorization-server",
	"openid-configuration",
}

// discoverTokenEndpoint resolves the OAuth2 token endpoint from an OIDC
// issuer URL by trying each of oidcWellKnownEndpoints in order, returning
// the first success (REQ-OSAC-011). An OIDC issuer URL is never itself a
// valid token endpoint — treating it as one was DD-060's original corrected
// hallucination.
func discoverTokenEndpoint(ctx context.Context, httpClient *http.Client, issuerURL string) (string, error) {
	issuerURL = strings.TrimSuffix(issuerURL, "/")

	var errs []error
	for _, wellKnown := range oidcWellKnownEndpoints {
		tokenEndpoint, err := fetchWellKnownTokenEndpoint(ctx, httpClient, issuerURL, wellKnown)
		if err == nil {
			return tokenEndpoint, nil
		}
		errs = append(errs, err)
	}
	return "", fmt.Errorf("discovering OIDC/OAuth token endpoint for issuer %s (tried %v): %w",
		issuerURL, oidcWellKnownEndpoints, errors.Join(errs...))
}

// fetchWellKnownTokenEndpoint fetches a single well-known discovery document
// (e.g. "oauth-authorization-server" or "openid-configuration") and extracts
// its token_endpoint.
func fetchWellKnownTokenEndpoint(ctx context.Context, httpClient *http.Client, issuerURL, wellKnown string) (string, error) {
	discoveryURL := issuerURL + "/.well-known/" + wellKnown

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return "", fmt.Errorf("building discovery request for %s: %w", discoveryURL, err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching discovery document from %s: %w", discoveryURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discovery document at %s returned status %d", discoveryURL, resp.StatusCode)
	}

	var meta oidcServerMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", fmt.Errorf("decoding discovery document from %s: %w", discoveryURL, err)
	}
	if meta.TokenEndpoint == "" {
		return "", fmt.Errorf("discovery document at %s has no token_endpoint", discoveryURL)
	}
	return meta.TokenEndpoint, nil
}

// discoveringTokenSource lazily discovers the OIDC issuer's token endpoint
// (REQ-OSAC-011) on first use, then delegates to a clientcredentials.Config
// -backed token source for that and all subsequent grants. Discovery
// failures are surfaced as Token() errors so the existing exponential
// -backoff retry loop (REQ-OSAC-060) covers discovery failures the same way
// it covers token-fetch failures, without blocking Bootstrap construction.
type discoveringTokenSource struct {
	issuerURL    string
	clientID     string
	clientSecret string
	httpClient   *http.Client

	mu    sync.Mutex
	inner oauth2.TokenSource
}

func (d *discoveringTokenSource) Token() (*oauth2.Token, error) {
	d.mu.Lock()
	inner := d.inner
	d.mu.Unlock()

	if inner == nil {
		discoveryCtx, cancel := context.WithTimeout(context.Background(), oidcDiscoveryTimeout)
		tokenURL, err := discoverTokenEndpoint(discoveryCtx, d.httpClient, d.issuerURL)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("discovering OIDC token endpoint for issuer %s: %w", d.issuerURL, err)
		}

		ccCfg := &clientcredentials.Config{
			ClientID:     d.clientID,
			ClientSecret: d.clientSecret,
			TokenURL:     tokenURL,
		}
		inner = ccCfg.TokenSource(context.Background())

		d.mu.Lock()
		d.inner = inner
		d.mu.Unlock()
	}

	return inner.Token()
}

// refreshMargin is how far before expiry a cached token is considered due
// for refresh (REQ-OSAC-020).
const refreshMargin = 30 * time.Second

// TokenStatus reports whether a currently cached OIDC token is valid.
//
// Implements REQ-OSAC-070.
type TokenStatus struct {
	Valid     bool
	ExpiresAt time.Time
}

// ProbeResult reports the outcome of a connectivity probe against OSAC.
//
// Implements REQ-OSAC-080.
type ProbeResult struct {
	Connected bool
	Err       error
}

// Option configures a Bootstrap.
type Option func(*Bootstrap)

// WithInitialBackoff sets the initial token-fetch retry backoff interval.
func WithInitialBackoff(d time.Duration) Option {
	return func(b *Bootstrap) { b.initialBackoff = d }
}

// WithMaxBackoff sets the maximum token-fetch retry backoff interval.
func WithMaxBackoff(d time.Duration) Option {
	return func(b *Bootstrap) { b.maxBackoff = d }
}

// WithNow overrides the clock used to evaluate token expiry. Intended for
// tests.
func WithNow(fn func() time.Time) Option {
	return func(b *Bootstrap) { b.now = fn }
}

// Bootstrap holds the SP's live connection state to OSAC.
type Bootstrap struct {
	cfg    *config.OSACConfig
	logger *slog.Logger
	now    func() time.Time

	initialBackoff time.Duration
	maxBackoff     time.Duration

	tokenSource oauth2.TokenSource

	mu        sync.RWMutex
	token     string
	expiresAt time.Time
	haveToken bool

	conn      *grpc.ClientConn
	capClient publicv1.CapabilitiesClient
}

// New creates a Bootstrap with a real OIDC token source (issuer discovery
// per REQ-OSAC-011, then an OAuth2 client-credentials grant per
// REQ-OSAC-010) and a real gRPC ClientConn to cfg.FulfillmentAddress
// (REQ-OSAC-030/040/050).
func New(cfg *config.OSACConfig, logger *slog.Logger, opts ...Option) (*Bootstrap, error) {
	ts := &discoveringTokenSource{
		issuerURL:    cfg.OIDCIssuerURL,
		clientID:     cfg.OIDCClientID,
		clientSecret: cfg.OIDCClientSecret,
		httpClient:   &http.Client{Timeout: oidcDiscoveryTimeout},
	}

	b := newBootstrap(cfg, logger, ts, nil, opts...)

	dialOpts, err := dialOptions(cfg, &bearerCreds{b: b})
	if err != nil {
		return nil, fmt.Errorf("building gRPC dial options: %w", err)
	}

	conn, err := grpc.NewClient(cfg.FulfillmentAddress, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("creating gRPC client for %s: %w", cfg.FulfillmentAddress, err)
	}
	b.conn = conn
	b.capClient = publicv1.NewCapabilitiesClient(conn)

	return b, nil
}

// newBootstrap builds a Bootstrap from already-constructed collaborators.
// Used by New (real token source + gRPC conn) and directly by unit tests
// (fake token source and/or hand-rolled fake CapabilitiesClient, per the
// unit test plan's "no real gRPC dialing in unit scope" convention).
func newBootstrap(cfg *config.OSACConfig, logger *slog.Logger, ts oauth2.TokenSource, capClient publicv1.CapabilitiesClient, opts ...Option) *Bootstrap {
	b := &Bootstrap{
		cfg:            cfg,
		logger:         logger,
		now:            time.Now,
		initialBackoff: 1 * time.Second,
		maxBackoff:     60 * time.Second,
		tokenSource:    ts,
		capClient:      capClient,
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// dialOptions builds gRPC dial options for the fulfillment service
// connection: TLS transport credentials (REQ-OSAC-040, unconditional per
// DD-229), plus a per-RPC bearer credential supplier.
func dialOptions(cfg *config.OSACConfig, perRPC credentials.PerRPCCredentials) ([]grpc.DialOption, error) {
	transportCreds, err := transportCredentials(cfg)
	if err != nil {
		return nil, err
	}

	return []grpc.DialOption{
		grpc.WithTransportCredentials(transportCreds),
		grpc.WithPerRPCCredentials(perRPC),
	}, nil
}

// transportCredentials builds TLS gRPC transport credentials
// (REQ-OSAC-040) — unconditionally; there is no insecure fallback (DD-229).
// Split out from dialOptions so unit tests can inspect the resulting
// credentials.TransportCredentials directly (e.g. Info().SecurityProtocol)
// without needing to introspect an opaque []grpc.DialOption (TC-U-012,
// TC-U-013).
func transportCredentials(cfg *config.OSACConfig) (credentials.TransportCredentials, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.TLSCertFile != "" {
		pool, err := loadCACertPool(cfg.TLSCertFile)
		if err != nil {
			return nil, err
		}
		tlsCfg.RootCAs = pool
	}
	return credentials.NewTLS(tlsCfg), nil
}

// loadCACertPool reads a PEM-encoded CA bundle from certFile into a fresh
// x509.CertPool. certFile comes from SP_OSAC_TLS_CERT_FILE, an
// operator-controlled deployment setting (internal/config), never
// request/user input, so this is not the untrusted-path traversal gosec's
// G304 guards against.
func loadCACertPool(certFile string) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	pem, err := os.ReadFile(certFile) //nolint:gosec // operator-controlled config path, not user input (see doc comment above)
	if err != nil {
		return nil, fmt.Errorf("reading TLS CA file %s: %w", certFile, err)
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no certificates found in %s", certFile)
	}
	return pool, nil
}

// bearerCreds supplies the cached OIDC bearer token as gRPC per-RPC
// metadata, without itself triggering a token fetch (REQ-OSAC-090).
type bearerCreds struct {
	b *Bootstrap
}

func (c *bearerCreds) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	c.b.mu.RLock()
	tok, have := c.b.token, c.b.haveToken
	c.b.mu.RUnlock()
	if !have {
		return nil, nil
	}
	return map[string]string{"authorization": "Bearer " + tok}, nil
}

// RequireTransportSecurity returns true: the transport is unconditionally
// TLS (DD-229), so this bearer token must never be attached to anything
// else. Defense in depth — even a future regression that reintroduced an
// insecure transport would have this credential type itself refuse to
// attach the token to it.
func (c *bearerCreds) RequireTransportSecurity() bool { return true }

// Start begins the background OIDC token fetch/refresh loop. It returns
// immediately; token fetch failures are retried with exponential backoff and
// never block startup or crash the process (REQ-OSAC-060).
func (b *Bootstrap) Start(ctx context.Context) {
	go b.refreshLoop(ctx)
}

func (b *Bootstrap) refreshLoop(ctx context.Context) {
	backoff := b.initialBackoff
	for {
		tok, err := b.tokenSource.Token()
		if err != nil {
			b.logger.Warn("OIDC token fetch failed, will retry", "error", err, "backoff", backoff)
			if !sleepOrDone(ctx, backoff) {
				return
			}
			backoff *= 2
			if backoff > b.maxBackoff {
				backoff = b.maxBackoff
			}
			continue
		}

		b.setToken(tok.AccessToken, tok.Expiry)
		backoff = b.initialBackoff

		wait := time.Until(tok.Expiry) - refreshMargin
		if wait < 0 {
			wait = 0
		}
		if !sleepOrDone(ctx, wait) {
			return
		}
	}
}

func sleepOrDone(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (b *Bootstrap) setToken(token string, expiresAt time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.token = token
	b.expiresAt = expiresAt
	b.haveToken = true
}

// TokenStatus reports whether a cached, non-expired OIDC token is available,
// without triggering a new fetch (REQ-OSAC-070, REQ-HLT-050).
func (b *Bootstrap) TokenStatus() TokenStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.haveToken {
		return TokenStatus{Valid: false}
	}
	return TokenStatus{Valid: b.now().Before(b.expiresAt), ExpiresAt: b.expiresAt}
}

// Probe performs a lightweight, unauthenticated connectivity check against
// OSAC's Capabilities service, bounded by cfg.ProbeTimeout. It does not
// trigger a token refresh (REQ-OSAC-080, REQ-OSAC-090).
func (b *Bootstrap) Probe(ctx context.Context) ProbeResult {
	ctx, cancel := context.WithTimeout(ctx, b.cfg.ProbeTimeout)
	defer cancel()

	if _, err := b.capClient.Get(ctx, &publicv1.CapabilitiesGetRequest{}); err != nil {
		return ProbeResult{Connected: false, Err: err}
	}
	return ProbeResult{Connected: true}
}

// Conn returns the shared, authenticated gRPC connection to OSAC's
// fulfillment service. Callers construct typed clients directly from it
// (e.g. publicv1.NewClustersClient(bootstrap.Conn())) — see DD-020. This is
// the exact same connection the internal Capabilities client already uses;
// Conn() does not dial a second connection or apply different credentials
// (REQ-GRPC-010).
func (b *Bootstrap) Conn() *grpc.ClientConn {
	return b.conn
}

// Close releases the underlying gRPC connection.
func (b *Bootstrap) Close() error {
	if b.conn != nil {
		return b.conn.Close()
	}
	return nil
}

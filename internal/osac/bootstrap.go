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
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dcm-project/osac-service-provider/internal/config"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

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

// New creates a Bootstrap with a real OIDC token source (OAuth2
// client-credentials grant, REQ-OSAC-010) and a real gRPC ClientConn to
// cfg.FulfillmentAddress (REQ-OSAC-030/040/050).
func New(cfg *config.OSACConfig, logger *slog.Logger, opts ...Option) (*Bootstrap, error) {
	ccCfg := &clientcredentials.Config{
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		TokenURL:     cfg.OIDCIssuerURL,
	}

	b := newBootstrap(cfg, logger, ccCfg.TokenSource(context.Background()), nil, opts...)

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
// connection: TLS (REQ-OSAC-040) or insecure (REQ-OSAC-050) transport
// credentials, plus a per-RPC bearer credential supplier.
func dialOptions(cfg *config.OSACConfig, perRPC credentials.PerRPCCredentials) ([]grpc.DialOption, error) {
	var transportCreds credentials.TransportCredentials
	if cfg.TLSEnabled {
		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
		if cfg.TLSCertFile != "" {
			pool := x509.NewCertPool()
			pem, err := os.ReadFile(cfg.TLSCertFile)
			if err != nil {
				return nil, fmt.Errorf("reading TLS CA file %s: %w", cfg.TLSCertFile, err)
			}
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("no certificates found in %s", cfg.TLSCertFile)
			}
			tlsCfg.RootCAs = pool
		}
		transportCreds = credentials.NewTLS(tlsCfg)
	} else {
		transportCreds = insecure.NewCredentials()
	}

	return []grpc.DialOption{
		grpc.WithTransportCredentials(transportCreds),
		grpc.WithPerRPCCredentials(perRPC),
	}, nil
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

// RequireTransportSecurity returns false so bearer credentials can be used
// over an insecure connection in non-TLS deployments (REQ-OSAC-050). OSAC v1
// uses a single shared service account over a trusted network path when TLS
// is disabled.
func (c *bearerCreds) RequireTransportSecurity() bool { return false }

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

// Close releases the underlying gRPC connection.
func (b *Bootstrap) Close() error {
	if b.conn != nil {
		return b.conn.Close()
	}
	return nil
}

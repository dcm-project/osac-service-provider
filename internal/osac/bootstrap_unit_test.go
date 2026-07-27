package osac

import (
	"context"
	"errors"
	"log/slog"
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
})

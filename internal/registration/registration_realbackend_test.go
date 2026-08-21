//go:build realbackend

package registration_test

// Tier B (DD-203): registration.Registrar (real code, no fakes) against a
// REAL, locally-built environment-agent binary — not the httptest.Server
// fake in registration_integration_test.go. Proves the fake's modeled
// contract (idempotent create-or-update on name, per-service-type 409
// exclusivity) actually matches environment-agent's real, running
// implementation, not just its documented OpenAPI spec.
//
// Gated behind the "realbackend" build tag so it's excluded from `make
// test`/`make check` by default (no real environment-agent process is
// available there) — only
// .github/workflows/environment-agent-registration.yaml (and
// `make test-realbackend-environment-agent` for local runs) builds/starts
// one and passes REAL_ENVIRONMENT_AGENT_URL. If that env var is unset, both
// specs Skip rather than fail, so `go test -tags realbackend ./...` stays
// safe to run without the real backend present.
//
// TC-I-029's retry-count assertion needs to observe how many registration
// requests actually reached environment-agent for a name that always gets
// rejected (409) — environment-agent's own state has no such counter. A
// small counting reverse proxy sits between the Registrar under test and
// the real backend for that one spec, forwarding every request unmodified
// while recording it by provider name; TC-I-028 talks to the real backend
// directly with no proxy involved.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	agentv1alpha1 "github.com/dcm-project/environment-agent/api/v1alpha1"
	agentclient "github.com/dcm-project/environment-agent/pkg/client"

	"github.com/dcm-project/osac-service-provider/internal/config"
	"github.com/dcm-project/osac-service-provider/internal/registration"
	"github.com/dcm-project/osac-service-provider/internal/versionmatrix"
)

const realBackendURLEnvVar = "REAL_ENVIRONMENT_AGENT_URL"

// newRealBackendRegistrar wires a real Registrar against registrationURL
// (either the real backend directly, or a proxy in front of it), with fast
// intervals so specs complete quickly.
func newRealBackendRegistrar(registrationURL, clusterName, vmName string) *registration.Registrar {
	cfg := &config.Config{
		DCM: config.DCMConfig{RegistrationURL: registrationURL},
		Provider: config.ProviderConfig{
			Endpoint:    "https://osac-sp.realbackend-spike.example.com",
			ClusterName: clusterName,
			VMName:      vmName,
		},
	}
	r, err := registration.NewRegistrar(cfg, discardLogger, versionmatrix.DefaultMatrix,
		registration.WithHTTPClient(&http.Client{Timeout: 5 * time.Second}),
		registration.WithInitialBackoff(200*time.Millisecond),
		registration.WithMaxBackoff(1*time.Second),
		registration.WithReRegistrationInterval(2*time.Second),
	)
	Expect(err).NotTo(HaveOccurred())
	return r
}

// countingProxy forwards every request unmodified to the real backend
// while recording how many registration requests it saw per provider name
// — the observation point TC-I-029 needs that environment-agent's own
// state (which never stores a rejected registration) can't provide.
type countingProxy struct {
	server *httptest.Server

	mu     sync.Mutex
	counts map[string]int
}

func newCountingProxy(targetBaseURL string) *countingProxy {
	target, err := url.Parse(targetBaseURL)
	Expect(err).NotTo(HaveOccurred())

	p := &countingProxy{counts: map[string]int{}}
	reverseProxy := httputil.NewSingleHostReverseProxy(target)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, readErr := io.ReadAll(r.Body)
			if readErr == nil {
				_ = r.Body.Close()
				r.Body = io.NopCloser(bytes.NewReader(body))

				var provider agentv1alpha1.Provider
				if json.Unmarshal(body, &provider) == nil {
					p.mu.Lock()
					p.counts[provider.Name]++
					p.mu.Unlock()
				}
			}
		}
		reverseProxy.ServeHTTP(w, r)
	})
	p.server = httptest.NewServer(mux)
	return p
}

func (p *countingProxy) URL() string { return p.server.URL }

func (p *countingProxy) countFor(name string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[name]
}

func (p *countingProxy) Close() { p.server.Close() }

var _ = Describe("Registrar against a REAL local environment-agent build (Tier B, DD-203)", func() {
	var realBackendURL string

	BeforeEach(func() {
		realBackendURL = os.Getenv(realBackendURLEnvVar)
		if realBackendURL == "" {
			Skip("set " + realBackendURLEnvVar + " (e.g. http://127.0.0.1:8090/api/v1alpha1) to run this spec against a real environment-agent build")
		}
	})

	// TC-I-028 (REQ-REG-010, REQ-REG-040, REQ-REG-100): both registrations
	// succeed against the real backend, and periodic re-registration
	// actually advances the stored provider's update_time — not just "no
	// error", which a purely idempotent no-op create could also produce.
	It("registers cluster and vm and idempotently re-registers, observed via a real GET /providers (TC-I-028)", func() {
		verifyClient, err := agentclient.NewClientWithResponses(realBackendURL)
		Expect(err).NotTo(HaveOccurred())

		clusterName := "test-realbackend-cluster"
		vmName := "test-realbackend-vm"

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		r := newRealBackendRegistrar(realBackendURL, clusterName, vmName)
		r.Start(ctx)

		getProvider := func(name string) *agentv1alpha1.Provider {
			resp, listErr := verifyClient.ListProvidersWithResponse(ctx, nil)
			Expect(listErr).NotTo(HaveOccurred())
			Expect(resp.StatusCode()).To(Equal(http.StatusOK))
			Expect(resp.JSON200).NotTo(BeNil())
			if resp.JSON200.Results == nil {
				return nil
			}
			for _, provider := range *resp.JSON200.Results {
				if provider.Name == name {
					return &provider
				}
			}
			return nil
		}

		Eventually(func() *agentv1alpha1.Provider { return getProvider(clusterName) }, "3s", "50ms").ShouldNot(BeNil())
		cluster := getProvider(clusterName)
		Expect(cluster.ServiceType).To(Equal("cluster"))
		Expect(cluster.Endpoint).To(Equal("https://osac-sp.realbackend-spike.example.com/api/v1alpha1/clusters"))
		Expect(cluster.SchemaVersion).To(Equal("v1alpha1"))
		Expect(cluster.Metadata).NotTo(BeNil())
		platforms, ok := cluster.Metadata.Get("supported_platforms")
		Expect(ok).To(BeTrue())
		Expect(platforms).To(ContainElement("baremetal"))

		vm := getProvider(vmName)
		Expect(vm).NotTo(BeNil())
		Expect(vm.ServiceType).To(Equal("vm"))

		Expect(cluster.UpdateTime).NotTo(BeNil())
		firstUpdateTime := *cluster.UpdateTime

		// Past the re-registration cadence (2s): the same name must have
		// been re-registered (update_time advanced), not merely left
		// alone — proving periodic re-registration really reaches the
		// real backend, not just the initial call.
		Eventually(func() time.Time {
			p := getProvider(clusterName)
			Expect(p).NotTo(BeNil())
			Expect(p.UpdateTime).NotTo(BeNil())
			return *p.UpdateTime
		}, "4s", "100ms").Should(BeTemporally(">", firstUpdateTime))
	})

	// TC-I-029 (REQ-REG-080): a competing provider for an
	// already-held service type is retried on the re-registration
	// cadence against the real backend — not abandoned after one 409, and
	// the process/goroutine keeps running rather than treating it as
	// fatal. Uses a counting proxy (see file doc comment) purely to
	// observe retry count; the 409 itself comes from the real backend.
	//
	// The incumbent deliberately reuses TC-I-028's exact names
	// ("test-realbackend-cluster"/"test-realbackend-vm") rather than a
	// distinct identity: environment-agent's per-service-type slot
	// persists across specs within the same running instance, so a
	// *different* incumbent name here would itself 409 against whatever
	// already holds the slot when TC-I-028 has run first (order-
	// dependent flakiness). Reusing the same name makes this an
	// idempotent renewal regardless of run order — it always succeeds.
	It("retries a real 409 conflict on the re-registration cadence instead of giving up (TC-I-029)", func() {
		incumbentName := "test-realbackend-vm"
		competitorName := "test-realbackend-vm-competitor"

		incumbentCtx, cancelIncumbent := context.WithCancel(context.Background())
		defer cancelIncumbent()
		incumbent := newRealBackendRegistrar(realBackendURL, "test-realbackend-cluster", incumbentName)
		incumbent.Start(incumbentCtx)

		verifyClient, err := agentclient.NewClientWithResponses(realBackendURL)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() bool {
			resp, listErr := verifyClient.ListProvidersWithResponse(context.Background(), nil)
			Expect(listErr).NotTo(HaveOccurred())
			if resp.JSON200 == nil || resp.JSON200.Results == nil {
				return false
			}
			for _, provider := range *resp.JSON200.Results {
				if provider.Name == incumbentName {
					return true
				}
			}
			return false
		}, "3s", "50ms").Should(BeTrue(), "incumbent must hold the vm slot before the competitor is started")

		proxy := newCountingProxy(realBackendURL)
		defer proxy.Close()

		competitorCtx, cancelCompetitor := context.WithCancel(context.Background())
		competitor := newRealBackendRegistrar(proxy.URL(), "test-realbackend-cluster-competitor", competitorName)
		competitor.Start(competitorCtx)

		// Real 409s keep arriving and the competitor keeps retrying on
		// the re-registration cadence (2s) rather than stopping after the
		// first one.
		Eventually(func() int { return proxy.countFor(competitorName) }, "7s", "100ms").Should(BeNumerically(">=", 3))

		// Not fatal: the loop is still running (Done() not yet closed) —
		// the pre-DD-203 behavior would have returned after the first
		// 409, closing Done() almost immediately.
		Consistently(competitor.Done(), "200ms").ShouldNot(BeClosed())
		cancelCompetitor()
		Eventually(competitor.Done(), "1s", "10ms").Should(BeClosed())

		// The incumbent was never displaced.
		resp, listErr := verifyClient.ListProvidersWithResponse(context.Background(), nil)
		Expect(listErr).NotTo(HaveOccurred())
		Expect(resp.JSON200).NotTo(BeNil())
		var sawIncumbent, sawCompetitor bool
		for _, provider := range *resp.JSON200.Results {
			switch provider.Name {
			case incumbentName:
				sawIncumbent = true
			case competitorName:
				sawCompetitor = true
			}
		}
		Expect(sawIncumbent).To(BeTrue())
		Expect(sawCompetitor).To(BeFalse())
	})
})

package registration_test

// Integration scope (per .ai/test-plans/osac-sp-integration.test-plan.md,
// "3. SP registration against a fake control-plane", TC-I-020..027): unlike
// registration_unit_test.go, which fakes http.RoundTripper directly (no
// socket, no real TCP/HTTP framing), these tests run the real generated
// control-plane client (cpclient.ClientWithResponses) over a real
// httptest.Server — a real listener, a real TCP connection, and real HTTP
// request/response framing and JSON (de)serialization on both sides. This
// closes the "registration wiring" pyramid-invariant gap identified in the
// GA readiness audit (the HTTP-routing and gRPC wiring gaps are closed by
// internal/apiserver/server_integration_test.go and
// cmd/osac-service-provider/main_integration_test.go respectively).
//
// TC-I-022 ("registration does not block server readiness") is NOT here:
// it inherently needs the real apiserver + health wiring alongside the
// registrar to assert the health endpoint stays responsive while
// registration is pending, so it lives in
// cmd/osac-service-provider/main_integration_test.go instead, alongside the
// rest of the full-stack health suite.
//
// registration.NewRegistrar's production defaults (1s initial backoff, 60s
// re-registration) are overridden here via its own Options — as
// main_integration_test.go's run()-based harness cannot, since run() always
// constructs a Registrar with NewRegistrar(cfg, logger) and no Options — so
// these tests complete in well under a second rather than tens of seconds.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	cpv1alpha1 "github.com/dcm-project/control-plane/api/sp/v1alpha1/provider"

	"github.com/dcm-project/osac-service-provider/internal/config"
	"github.com/dcm-project/osac-service-provider/internal/registration"
	"github.com/dcm-project/osac-service-provider/internal/versionmatrix"
)

// fakeControlPlaneServer is a real httptest.Server implementing
// control-plane's current, implemented POST /api/v1alpha1/providers
// contract (api/sp/v1alpha1/provider/openapi.yaml) — the integration-test
// analog of registration_unit_test.go's fakeProviderTransport and
// cmd/osac-service-provider/main_integration_test.go's fakeProviderServer,
// duplicated here (not shared across packages) per this project's
// hand-written-fakes-per-package testing convention.
type fakeControlPlaneServer struct {
	server *httptest.Server

	mu           sync.Mutex
	reqs         []capturedRequest
	responder    responderFunc
	hijackCounts map[string]int // serviceType -> remaining "drop connection" responses
}

func newFakeControlPlaneServer() *fakeControlPlaneServer {
	f := &fakeControlPlaneServer{responder: alwaysCreated, hijackCounts: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1alpha1/providers", func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()

		var p cpv1alpha1.Provider
		_ = json.Unmarshal(bodyBytes, &p)

		f.mu.Lock()
		f.reqs = append(f.reqs, capturedRequest{provider: p, headers: r.Header.Clone()})
		responder := f.responder
		hijack := f.hijackCounts[p.ServiceType] > 0
		if hijack {
			f.hijackCounts[p.ServiceType]--
		}
		f.mu.Unlock()

		if hijack {
			// Simulate a real connection-level failure (e.g. "connection
			// reset") rather than a well-formed HTTP error response, by
			// hijacking the connection and closing it without writing
			// anything — the client sees a real transport error, not a
			// decodable status code.
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, hjErr := hj.Hijack(); hjErr == nil {
					_ = conn.Close()
					return
				}
			}
		}

		status, body, contentType := responder(p)
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	})
	f.server = httptest.NewServer(mux)
	return f
}

func (f *fakeControlPlaneServer) URL() string { return f.server.URL + "/api/v1alpha1" }

func (f *fakeControlPlaneServer) Requests() []capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]capturedRequest, len(f.reqs))
	copy(out, f.reqs)
	return out
}

func (f *fakeControlPlaneServer) requestsFor(serviceType string) []capturedRequest {
	var out []capturedRequest
	for _, r := range f.Requests() {
		if r.provider.ServiceType == serviceType {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeControlPlaneServer) SetResponder(fn responderFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responder = fn
}

// SetHijackCount makes the next n requests for serviceType fail at the
// transport level (connection dropped, no HTTP response at all) before
// falling back to the normal responder.
func (f *fakeControlPlaneServer) SetHijackCount(serviceType string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hijackCounts[serviceType] = n
}

func (f *fakeControlPlaneServer) Close() { f.server.Close() }

// newIntegrationRegistrar wires a real Registrar (real generated
// control-plane client, real *http.Client, no injected RoundTripper)
// against srv, with fast backoff/re-registration intervals so tests
// complete quickly.
func newIntegrationRegistrar(srv *fakeControlPlaneServer) *registration.Registrar {
	return newIntegrationRegistrarWithMatrix(srv, versionmatrix.DefaultMatrix)
}

// newIntegrationRegistrarWithMatrix is newIntegrationRegistrar with an
// explicit matrix, for TC-I-510.
func newIntegrationRegistrarWithMatrix(srv *fakeControlPlaneServer, matrix versionmatrix.Matrix) *registration.Registrar {
	cfg := &config.Config{
		DCM: config.DCMConfig{RegistrationURL: srv.URL()},
		Provider: config.ProviderConfig{
			Endpoint:    "https://osac-sp.example.com",
			ClusterName: "osac-sp-cluster",
			VMName:      "osac-sp-vm",
		},
	}
	r, err := registration.NewRegistrar(cfg, discardLogger, matrix,
		registration.WithHTTPClient(&http.Client{Timeout: 5 * time.Second}),
		registration.WithInitialBackoff(20*time.Millisecond),
		registration.WithMaxBackoff(100*time.Millisecond),
		registration.WithReRegistrationInterval(300*time.Millisecond),
	)
	Expect(err).NotTo(HaveOccurred())
	return r
}

var _ = Describe("Registrar against a real fake control-plane server (integration)", func() {
	var srv *fakeControlPlaneServer

	BeforeEach(func() {
		srv = newFakeControlPlaneServer()
	})

	AfterEach(func() {
		srv.Close()
	})

	// TC-I-020: both registrations are sent on startup.
	It("sends both cluster and vm registrations on startup (TC-I-020)", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		r := newIntegrationRegistrar(srv)
		r.Start(ctx)

		Eventually(func() int { return len(srv.Requests()) }, "2s", "10ms").Should(BeNumerically(">=", 2))

		names := map[string]bool{}
		for _, req := range srv.Requests() {
			names[req.provider.Name] = true
		}
		Expect(names).To(HaveKey("osac-sp-cluster"))
		Expect(names).To(HaveKey("osac-sp-vm"))
	})

	// TC-I-021: cluster registration payload matches AC-REG-020 exactly,
	// as actually serialized to JSON and deserialized by a real HTTP
	// server (not merely constructed in memory, per TC-U-050's unit-level
	// equivalent check).
	It("sends a cluster registration payload matching the contract exactly (TC-I-021)", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		r := newIntegrationRegistrar(srv)
		r.Start(ctx)

		Eventually(func() []capturedRequest { return srv.requestsFor("cluster") }, "2s", "10ms").ShouldNot(BeEmpty())

		req := srv.requestsFor("cluster")[0]
		Expect(req.provider.Name).To(Equal("osac-sp-cluster"))
		Expect(req.provider.ServiceType).To(Equal("cluster"))
		Expect(req.provider.Endpoint).To(Equal("https://osac-sp.example.com/api/v1alpha1/clusters"))
		Expect(req.provider.SchemaVersion).To(Equal("v1alpha1"))
		Expect(req.provider.Metadata).NotTo(BeNil())

		platforms, ok := req.provider.Metadata.Get("supported_platforms")
		Expect(ok).To(BeTrue())
		Expect(platforms).To(ContainElement("baremetal"))

		provisioningTypes, ok := req.provider.Metadata.Get("supported_provisioning_types")
		Expect(ok).To(BeTrue())
		Expect(provisioningTypes).To(ContainElement("hypershift"))

		versions, ok := req.provider.Metadata.Get("kubernetes_supported_versions")
		Expect(ok).To(BeTrue())
		Expect(versions).To(ContainElement("1.31"))
		Expect(versions).NotTo(ContainElement("4.18"))
	})

	// TC-I-510 (REQ-VERSION-050, AC-VERSION-020, AC-VERSION-050): the
	// cluster registration payload's advertised versions, as actually
	// sent over a real HTTP round trip, equal the injected matrix's
	// SupportedVersions() exactly.
	It("advertises the injected matrix's SupportedVersions() over a real HTTP round trip (TC-I-510)", func() {
		testMatrix := versionmatrix.Matrix{
			"9.01": "quay.io/example/release:9.01",
			"9.02": "quay.io/example/release:9.02",
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		r := newIntegrationRegistrarWithMatrix(srv, testMatrix)
		r.Start(ctx)

		Eventually(func() []capturedRequest { return srv.requestsFor("cluster") }, "2s", "10ms").ShouldNot(BeEmpty())

		req := srv.requestsFor("cluster")[0]
		versions, ok := req.provider.Metadata.Get("kubernetes_supported_versions")
		Expect(ok).To(BeTrue())
		Expect(versions).To(ConsistOf(testMatrix.SupportedVersions()))
	})

	// TC-I-023: a vm 409 Conflict is non-retryable; cluster registration
	// still succeeds independently, over a real HTTP round trip.
	It("treats a vm 409 as non-retryable while cluster registration still succeeds (TC-I-023)", func() {
		srv.SetResponder(func(p cpv1alpha1.Provider) (int, any, string) {
			if p.ServiceType == "vm" {
				return http.StatusConflict, cpv1alpha1.Error{Title: "already registered", Type: "ALREADY_EXISTS"}, "application/problem+json"
			}
			return http.StatusCreated, p, "application/json"
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		r := newIntegrationRegistrar(srv)
		r.Start(ctx)

		// vm gives up after exactly one 409 — no retry.
		Eventually(func() int { return len(srv.requestsFor("vm")) }, "500ms", "10ms").Should(Equal(1))
		Consistently(func() int { return len(srv.requestsFor("vm")) }, "150ms", "10ms").Should(Equal(1))

		// cluster keeps succeeding/renewing, unaffected.
		Eventually(func() int { return len(srv.requestsFor("cluster")) }, "1s", "10ms").Should(BeNumerically(">=", 2))
	})

	// TC-I-024: a non-retryable 4xx on cluster does not affect vm
	// registration, and (by virtue of the fake server continuing to
	// respond throughout) does not crash the process.
	It("does not let a non-retryable cluster failure block vm registration (TC-I-024)", func() {
		srv.SetResponder(func(p cpv1alpha1.Provider) (int, any, string) {
			if p.ServiceType == "cluster" {
				return http.StatusBadRequest, cpv1alpha1.Error{Title: "invalid", Type: "INVALID_ARGUMENT"}, "application/problem+json"
			}
			return http.StatusCreated, p, "application/json"
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		r := newIntegrationRegistrar(srv)
		r.Start(ctx)

		Eventually(func() int { return len(srv.requestsFor("cluster")) }, "500ms", "10ms").Should(Equal(1))
		Consistently(func() int { return len(srv.requestsFor("cluster")) }, "150ms", "10ms").Should(Equal(1))

		Eventually(func() int { return len(srv.requestsFor("vm")) }, "500ms", "10ms").Should(BeNumerically(">=", 1))
	})

	// TC-I-025: real connection-level failures (not simulated error
	// values, per TC-U-055's unit-level equivalent) use exponential
	// backoff and eventually succeed once the server "recovers" — the
	// first 2 cluster requests have their connection dropped before any
	// HTTP response, the 3rd goes through normally.
	It("retries with exponential backoff after real connection failures and eventually succeeds (TC-I-025)", func() {
		srv.SetHijackCount("cluster", 2)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		r := newIntegrationRegistrar(srv)
		r.Start(ctx)

		Eventually(func() int { return len(srv.requestsFor("cluster")) }, "2s", "10ms").Should(BeNumerically(">=", 3))
		// The 3rd (first non-hijacked) cluster request got a real 201.
		req := srv.requestsFor("cluster")[2]
		Expect(req.provider.Name).To(Equal("osac-sp-cluster"))
	})

	// TC-I-026: registration is idempotent on name across a simulated
	// restart (a second, independent Registrar against the same fake
	// server and config, as if the process had restarted).
	It("sends identical name/service_type pairs across a simulated restart (TC-I-026)", func() {
		ctx1, cancel1 := context.WithCancel(context.Background())
		r1 := newIntegrationRegistrar(srv)
		r1.Start(ctx1)
		Eventually(func() []capturedRequest { return srv.requestsFor("cluster") }, "1s", "10ms").ShouldNot(BeEmpty())
		firstCluster := srv.requestsFor("cluster")[0]
		firstVM := srv.requestsFor("vm")[0]
		cancel1()
		Eventually(r1.Done(), "1s", "10ms").Should(BeClosed())

		ctx2, cancel2 := context.WithCancel(context.Background())
		defer cancel2()
		r2 := newIntegrationRegistrar(srv)
		r2.Start(ctx2)
		Eventually(func() int { return len(srv.requestsFor("cluster")) }, "1s", "10ms").Should(BeNumerically(">=", 2))

		secondCluster := srv.requestsFor("cluster")[1]
		Expect(secondCluster.provider.Name).To(Equal(firstCluster.provider.Name))
		Expect(secondCluster.provider.ServiceType).To(Equal(firstCluster.provider.ServiceType))
		Expect(firstVM.provider.Name).To(Equal("osac-sp-vm"))
	})

	// TC-I-027: no Authorization header is sent on the wire to a real HTTP
	// server (not headers recorded by an in-process fake RoundTripper, per
	// TC-U-059's unit-level equivalent).
	It("sends no Authorization header on the wire (TC-I-027)", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		r := newIntegrationRegistrar(srv)
		r.Start(ctx)

		Eventually(func() int { return len(srv.Requests()) }, "2s", "10ms").Should(BeNumerically(">=", 2))

		for _, req := range srv.Requests() {
			Expect(req.headers.Get("Authorization")).To(BeEmpty())
		}
	})
})

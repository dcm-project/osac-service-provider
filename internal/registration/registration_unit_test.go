package registration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	agentv1alpha1 "github.com/dcm-project/environment-agent/api/v1alpha1"

	"github.com/dcm-project/osac-service-provider/internal/config"
	"github.com/dcm-project/osac-service-provider/internal/registration"
	"github.com/dcm-project/osac-service-provider/internal/versionmatrix"
)

var discardLogger = slog.New(slog.DiscardHandler)

// Fixture-local mirrors of the fake environment-agent's response shapes
// and registration.go's own (unexported) service-type strings — this is an
// external (_test) package, so it cannot import those constants directly.
const (
	clusterServiceType     = "cluster"
	contentTypeJSON        = "application/json"
	contentTypeProblemJSON = "application/problem+json"
)

// capturedRequest is one decoded POST /providers request seen by
// fakeProviderTransport.
type capturedRequest struct {
	provider agentv1alpha1.Provider
	headers  http.Header
}

// responderFunc decides the fake environment-agent's response for a given
// decoded registration request.
type responderFunc func(p agentv1alpha1.Provider) (statusCode int, body any, contentType string)

// fakeProviderTransport is a fake http.RoundTripper implementing
// environment-agent's POST /providers contract, per the unit test plan's
// "fake environment-agent's pkg/client HTTP round-tripper" collaborator
// (DD-203).
type fakeProviderTransport struct {
	mu        sync.Mutex
	requests  []capturedRequest
	responder responderFunc
}

func (f *fakeProviderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()

	var p agentv1alpha1.Provider
	_ = json.Unmarshal(bodyBytes, &p)

	f.mu.Lock()
	f.requests = append(f.requests, capturedRequest{provider: p, headers: req.Header.Clone()})
	f.mu.Unlock()

	statusCode, body, contentType := f.responder(p)
	bb, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(bytes.NewReader(bb)),
		Request:    req,
	}, nil
}

func (f *fakeProviderTransport) Requests() []capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]capturedRequest, len(f.requests))
	copy(out, f.requests)
	return out
}

func (f *fakeProviderTransport) requestsFor(serviceType string) []capturedRequest {
	var out []capturedRequest
	for _, r := range f.Requests() {
		if r.provider.ServiceType == serviceType {
			out = append(out, r)
		}
	}
	return out
}

func alwaysCreated(p agentv1alpha1.Provider) (int, any, string) {
	return http.StatusCreated, p, contentTypeJSON
}

func testConfig() *config.Config {
	return &config.Config{
		DCM: config.DCMConfig{RegistrationURL: "http://environment-agent.example.com/api/v1alpha1"},
		Provider: config.ProviderConfig{
			Endpoint:    "https://osac-sp.example.com",
			ClusterName: "osac-sp-cluster",
			VMName:      "osac-sp-vm",
		},
	}
}

func newTestRegistrar(transport http.RoundTripper, opts ...registration.Option) *registration.Registrar {
	return newTestRegistrarWithMatrix(transport, versionmatrix.DefaultMatrix, opts...)
}

// newTestRegistrarWithMatrix is newTestRegistrar with an explicit matrix,
// for TC-U-510 (proving kubernetes_supported_versions is derived from
// whatever matrix is injected, not DefaultMatrix specifically).
func newTestRegistrarWithMatrix(transport http.RoundTripper, matrix versionmatrix.Matrix, opts ...registration.Option) *registration.Registrar {
	allOpts := append([]registration.Option{
		registration.WithHTTPClient(&http.Client{Transport: transport}),
		registration.WithInitialBackoff(2 * time.Millisecond),
		registration.WithMaxBackoff(10 * time.Millisecond),
		registration.WithReRegistrationInterval(20 * time.Millisecond),
	}, opts...)
	r, err := registration.NewRegistrar(testConfig(), discardLogger, matrix, allOpts...)
	Expect(err).NotTo(HaveOccurred())
	return r
}

var _ = Describe("Registrar", func() {
	// TC-U-050: cluster registration nests OSAC-specific fields under metadata
	It("nests supported platforms/provisioning types/k8s versions under metadata for cluster registration (TC-U-050)", func() {
		transport := &fakeProviderTransport{responder: alwaysCreated}
		r := newTestRegistrar(transport)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		Eventually(func() []capturedRequest { return transport.requestsFor(clusterServiceType) }, "200ms", "5ms").ShouldNot(BeEmpty())

		req := transport.requestsFor(clusterServiceType)[0]
		Expect(req.provider.Name).To(Equal("osac-sp-cluster"))
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
		// Real Kubernetes version numbers (e.g. "1.31"), not OpenShift
		// release versions (e.g. "4.18") — see the comment on
		// kubernetesSupportedVersions in registration.go.
		Expect(versions).To(ContainElement("1.31"))
		Expect(versions).NotTo(ContainElement("4.18"))
	})

	// TC-U-510 (REQ-VERSION-050, AC-VERSION-050): kubernetes_supported_versions
	// is derived from whatever Matrix is injected, not a hardcoded list —
	// a 3-entry test matrix, distinct from DefaultMatrix, proves this.
	It("derives kubernetes_supported_versions from the injected matrix, not DefaultMatrix (TC-U-510)", func() {
		testMatrix := versionmatrix.Matrix{
			"9.01": "quay.io/example/release:9.01",
			"9.02": "quay.io/example/release:9.02",
			"9.03": "quay.io/example/release:9.03",
		}
		transport := &fakeProviderTransport{responder: alwaysCreated}
		r := newTestRegistrarWithMatrix(transport, testMatrix)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		Eventually(func() []capturedRequest { return transport.requestsFor("cluster") }, "200ms", "5ms").ShouldNot(BeEmpty())

		req := transport.requestsFor("cluster")[0]
		versions, ok := req.provider.Metadata.Get("kubernetes_supported_versions")
		Expect(ok).To(BeTrue())
		Expect(versions).To(ConsistOf(testMatrix.SupportedVersions()))
		Expect(versions).NotTo(ContainElement("1.29"))
	})

	// TC-U-051: vm registration payload
	It("registers the vm service type against the /vms endpoint suffix with no OSAC-specific metadata (TC-U-051)", func() {
		transport := &fakeProviderTransport{responder: alwaysCreated}
		r := newTestRegistrar(transport)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		Eventually(func() []capturedRequest { return transport.requestsFor("vm") }, "200ms", "5ms").ShouldNot(BeEmpty())

		req := transport.requestsFor("vm")[0]
		Expect(req.provider.Name).To(Equal("osac-sp-vm"))
		Expect(req.provider.Endpoint).To(Equal("https://osac-sp.example.com/api/v1alpha1/vms"))
		Expect(req.provider.Metadata).To(BeNil())
	})

	// TC-U-059: no Authorization header on registration requests
	It("sends no Authorization header to environment-agent (TC-U-059)", func() {
		transport := &fakeProviderTransport{responder: alwaysCreated}
		r := newTestRegistrar(transport)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		Eventually(func() []capturedRequest { return transport.Requests() }, "200ms", "5ms").ShouldNot(BeEmpty())

		for _, req := range transport.Requests() {
			Expect(req.headers.Get("Authorization")).To(BeEmpty())
		}
	})

	// TC-U-052: a single Start() issues both registrations independently:
	// exactly 2 initial requests total, with distinct name values.
	It("issues exactly 2 independent initial registration requests, for cluster and vm (TC-U-052)", func() {
		transport := &fakeProviderTransport{responder: alwaysCreated}
		// Long re-registration interval so no 3rd (renewal) request can
		// land during this test's brief assertion window.
		r := newTestRegistrar(transport, registration.WithReRegistrationInterval(5*time.Second))

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		Eventually(func() int { return len(transport.Requests()) }, "200ms", "5ms").Should(Equal(2))
		Consistently(func() int { return len(transport.Requests()) }, "50ms", "5ms").Should(Equal(2))

		names := []string{transport.Requests()[0].provider.Name, transport.Requests()[1].provider.Name}
		Expect(names).To(ConsistOf("osac-sp-cluster", "osac-sp-vm"))
	})

	// TC-U-058: successful registration is periodically renewed (idempotent
	// re-registration on name), not sent once and forgotten — this keeps
	// capability metadata fresh and also serves as the retry cadence for a
	// 409 Conflict (REQ-REG-080, DD-203).
	It("periodically re-registers to refresh capability metadata after a successful registration (TC-U-058)", func() {
		transport := &fakeProviderTransport{responder: alwaysCreated}
		r := newTestRegistrar(transport, registration.WithReRegistrationInterval(10*time.Millisecond))

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		Eventually(func() int { return len(transport.requestsFor(clusterServiceType)) }, "500ms", "5ms").Should(BeNumerically(">=", 3))
	})

	// TC-U-055: retryable failures (5xx) use exponential backoff and
	// eventually succeed once environment-agent recovers.
	It("retries a retryable failure and eventually succeeds (TC-U-055)", func() {
		var mu sync.Mutex
		failuresLeft := 3
		transport := &fakeProviderTransport{responder: func(p agentv1alpha1.Provider) (int, any, string) {
			mu.Lock()
			defer mu.Unlock()
			if failuresLeft > 0 {
				failuresLeft--
				return http.StatusServiceUnavailable, agentv1alpha1.Error{Title: "unavailable", Type: "UNAVAILABLE"}, contentTypeProblemJSON
			}
			return http.StatusCreated, p, contentTypeJSON
		}}
		r := newTestRegistrar(transport)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		Eventually(func() int { return len(transport.requestsFor(clusterServiceType)) }, "500ms", "5ms").Should(BeNumerically(">=", 4))
	})

	// TC-U-054: a non-retryable 4xx stops retrying for that registration.
	It("stops retrying after a non-retryable 4xx response (TC-U-054)", func() {
		transport := &fakeProviderTransport{responder: func(_ agentv1alpha1.Provider) (int, any, string) {
			return http.StatusUnprocessableEntity, agentv1alpha1.Error{Title: "invalid", Type: "INVALID_ARGUMENT"}, contentTypeProblemJSON
		}}
		r := newTestRegistrar(transport)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		// Both loops (cluster, vm) give up immediately on a non-retryable
		// status, so Done() closes without needing context cancellation.
		Eventually(r.Done(), "200ms", "5ms").Should(BeClosed())

		clusterCount := len(transport.requestsFor(clusterServiceType))
		vmCount := len(transport.requestsFor("vm"))

		// No further attempts after Done() closes.
		Consistently(func() int { return len(transport.requestsFor(clusterServiceType)) }, "50ms", "5ms").Should(Equal(clusterCount))
		Consistently(func() int { return len(transport.requestsFor("vm")) }, "50ms", "5ms").Should(Equal(vmCount))
	})

	// TC-U-056: registration runs in a goroutine, not synchronously in
	// Start() — Start() must return before a blocked round-tripper is
	// released.
	It("does not block on construction: Start() returns before registration completes (TC-U-056)", func() {
		release := make(chan struct{})
		transport := &fakeProviderTransport{responder: func(p agentv1alpha1.Provider) (int, any, string) {
			<-release // blocks until explicitly released below — proves Start() didn't wait for this
			return http.StatusCreated, p, contentTypeJSON
		}}
		r := newTestRegistrar(transport)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		startReturned := make(chan struct{})
		go func() {
			r.Start(ctx)
			close(startReturned)
		}()

		// Start() must return promptly even though the round-tripper it
		// launched is still blocked on <-release: if Start() ran
		// registration synchronously instead of in a goroutine, this
		// would time out.
		Eventually(startReturned, "200ms", "5ms").Should(BeClosed())

		close(release)
		Eventually(func() []capturedRequest { return transport.Requests() }, "200ms", "5ms").ShouldNot(BeEmpty())
	})

	// TC-U-057: cluster's ongoing failure/retries do not affect vm reaching
	// registered — the reverse direction of TC-U-053's independence check
	// (there, vm fails and cluster is unaffected; here, cluster fails and
	// vm is unaffected).
	It("registers vm successfully regardless of cluster's ongoing retries (TC-U-057)", func() {
		transport := &fakeProviderTransport{responder: func(p agentv1alpha1.Provider) (int, any, string) {
			if p.ServiceType == clusterServiceType {
				return http.StatusServiceUnavailable, agentv1alpha1.Error{Title: "unavailable", Type: "UNAVAILABLE"}, contentTypeProblemJSON
			}
			return http.StatusCreated, p, contentTypeJSON
		}}
		r := newTestRegistrar(transport)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		Eventually(func() int { return len(transport.requestsFor("vm")) }, "200ms", "5ms").Should(BeNumerically(">=", 1))
		Expect(transport.requestsFor("vm")[0].provider.ServiceType).To(Equal("vm"))

		// cluster keeps retrying in the background, unaffected by vm's success.
		Eventually(func() int { return len(transport.requestsFor(clusterServiceType)) }, "300ms", "5ms").Should(BeNumerically(">=", 2))
	})

	// TC-U-060: backoff growth is capped at maxBackoff, not left to grow
	// unbounded. A capped sequence over 8 retries (5+10+20+20+20+20+20+20
	// = 135ms) comfortably finishes within this test's bound; an uncapped
	// doubling sequence over the same 8 retries (5+10+20+40+80+160+320+
	// 640 = 1275ms) could not — a generous-but-decisive gap that
	// distinguishes "cap works" from "cap is computed and discarded"
	// without asserting exact scheduler-jitter-prone timings.
	It("caps backoff growth at maxBackoff instead of growing unbounded (TC-U-060)", func() {
		var mu sync.Mutex
		failuresLeft := 8
		transport := &fakeProviderTransport{responder: func(p agentv1alpha1.Provider) (int, any, string) {
			if p.ServiceType != clusterServiceType {
				// vm registers immediately; only cluster's own retry
				// sequence is under test here, kept independent of the
				// concurrently-running vm loop per this suite's
				// independence convention (TC-U-057).
				return http.StatusCreated, p, contentTypeJSON
			}
			mu.Lock()
			defer mu.Unlock()
			if failuresLeft > 0 {
				failuresLeft--
				return http.StatusServiceUnavailable, agentv1alpha1.Error{Title: "unavailable", Type: "UNAVAILABLE"}, contentTypeProblemJSON
			}
			return http.StatusCreated, p, contentTypeJSON
		}}
		r := newTestRegistrar(transport,
			registration.WithInitialBackoff(5*time.Millisecond),
			registration.WithMaxBackoff(20*time.Millisecond),
			registration.WithReRegistrationInterval(5*time.Second),
		)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		Eventually(func() int { return len(transport.requestsFor(clusterServiceType)) }, "700ms", "5ms").Should(BeNumerically(">=", 9))
	})

	// TC-U-053: a 409 Conflict is retried on the re-registration cadence,
	// not treated as fatal — environment-agent enforces per-service-type
	// exclusivity, so a vm 409 means another provider currently holds that
	// slot, worth retrying into rather than giving up on. Restores the
	// pre-Phase-1 design DD-050 had replaced — see DD-203.
	It("retries a vm 409 conflict on the re-registration cadence instead of giving up (TC-U-053)", func() {
		transport := &fakeProviderTransport{responder: func(p agentv1alpha1.Provider) (int, any, string) {
			if p.ServiceType == "vm" {
				return http.StatusConflict, agentv1alpha1.Error{Title: "already registered", Type: "CONFLICT"}, contentTypeProblemJSON
			}
			return http.StatusCreated, p, contentTypeJSON
		}}
		r := newTestRegistrar(transport, registration.WithReRegistrationInterval(10*time.Millisecond))

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		// vm keeps retrying past the first 409, on the re-registration
		// cadence — it must NOT give up after exactly one attempt.
		Eventually(func() int { return len(transport.requestsFor("vm")) }, "300ms", "5ms").Should(BeNumerically(">=", 3))

		// cluster is unaffected: it keeps succeeding/renewing independently.
		Eventually(func() int { return len(transport.requestsFor(clusterServiceType)) }, "300ms", "5ms").Should(BeNumerically(">=", 2))
	})
})

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
)

var discardLogger = slog.New(slog.DiscardHandler)

// capturedRequest is one decoded POST /providers request seen by
// fakeAgentTransport.
type capturedRequest struct {
	provider agentv1alpha1.Provider
	headers  http.Header
}

// responderFunc decides the fake environment agent's response for a given
// decoded registration request.
type responderFunc func(p agentv1alpha1.Provider) (statusCode int, body any, contentType string)

// fakeAgentTransport is a fake http.RoundTripper implementing the
// environment agent's POST /providers contract, per the unit test plan's
// "fake the environment-agent pkg/client HTTP round-tripper" collaborator.
type fakeAgentTransport struct {
	mu        sync.Mutex
	requests  []capturedRequest
	responder responderFunc
}

func (f *fakeAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
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

func (f *fakeAgentTransport) Requests() []capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]capturedRequest, len(f.requests))
	copy(out, f.requests)
	return out
}

func (f *fakeAgentTransport) requestsFor(serviceType string) []capturedRequest {
	var out []capturedRequest
	for _, r := range f.Requests() {
		if r.provider.ServiceType == serviceType {
			out = append(out, r)
		}
	}
	return out
}

func alwaysCreated(p agentv1alpha1.Provider) (int, any, string) {
	return http.StatusCreated, p, "application/json"
}

func testConfig() *config.Config {
	return &config.Config{
		Agent: config.AgentConfig{RegistrationURL: "http://agent.example.com/api/v1alpha1"},
		Provider: config.ProviderConfig{
			Endpoint:    "https://osac-sp.example.com",
			ClusterName: "osac-sp-cluster",
			VMName:      "osac-sp-vm",
		},
	}
}

func newTestRegistrar(transport http.RoundTripper, opts ...registration.Option) *registration.Registrar {
	allOpts := append([]registration.Option{
		registration.WithHTTPClient(&http.Client{Transport: transport}),
		registration.WithInitialBackoff(2 * time.Millisecond),
		registration.WithMaxBackoff(10 * time.Millisecond),
		registration.WithLeaseRenewalInterval(20 * time.Millisecond),
	}, opts...)
	r, err := registration.NewRegistrar(testConfig(), discardLogger, allOpts...)
	Expect(err).NotTo(HaveOccurred())
	return r
}

var _ = Describe("Registrar", func() {
	// TC-U-050: cluster registration nests OSAC-specific fields under metadata
	It("nests supported platforms/provisioning types/k8s versions under metadata for cluster registration (TC-U-050)", func() {
		transport := &fakeAgentTransport{responder: alwaysCreated}
		r := newTestRegistrar(transport)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		Eventually(func() []capturedRequest { return transport.requestsFor("cluster") }, "200ms", "5ms").ShouldNot(BeEmpty())

		req := transport.requestsFor("cluster")[0]
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

	// TC-U-051: vm registration payload
	It("registers the vm service type against the /vms endpoint suffix with no OSAC-specific metadata (TC-U-051)", func() {
		transport := &fakeAgentTransport{responder: alwaysCreated}
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
	It("sends no Authorization header (TC-U-059)", func() {
		transport := &fakeAgentTransport{responder: alwaysCreated}
		r := newTestRegistrar(transport)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		Eventually(func() []capturedRequest { return transport.Requests() }, "200ms", "5ms").ShouldNot(BeEmpty())

		for _, req := range transport.Requests() {
			Expect(req.headers.Get("Authorization")).To(BeEmpty())
		}
	})

	// TC-U-058: successful registration is periodically renewed (idempotent
	// re-registration), not sent once and forgotten.
	It("periodically re-registers to renew the lease after a successful registration (TC-U-058)", func() {
		transport := &fakeAgentTransport{responder: alwaysCreated}
		r := newTestRegistrar(transport, registration.WithLeaseRenewalInterval(10*time.Millisecond))

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		Eventually(func() int { return len(transport.requestsFor("cluster")) }, "500ms", "5ms").Should(BeNumerically(">=", 3))
	})

	// TC-U-054/TC-U-053: retryable failures (5xx) use exponential backoff and
	// eventually succeed once the agent recovers.
	It("retries a retryable failure and eventually succeeds (TC-U-053/054)", func() {
		var mu sync.Mutex
		failuresLeft := 3
		transport := &fakeAgentTransport{responder: func(p agentv1alpha1.Provider) (int, any, string) {
			mu.Lock()
			defer mu.Unlock()
			if failuresLeft > 0 {
				failuresLeft--
				return http.StatusServiceUnavailable, agentv1alpha1.Error{Title: "unavailable"}, "application/problem+json"
			}
			return http.StatusCreated, p, "application/json"
		}}
		r := newTestRegistrar(transport)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		Eventually(func() int { return len(transport.requestsFor("cluster")) }, "500ms", "5ms").Should(BeNumerically(">=", 4))
	})

	// TC-U-056/057: a non-retryable 4xx stops retrying for that registration.
	It("stops retrying after a non-retryable 4xx response (TC-U-056/057)", func() {
		transport := &fakeAgentTransport{responder: func(_ agentv1alpha1.Provider) (int, any, string) {
			return http.StatusUnprocessableEntity, agentv1alpha1.Error{Title: "invalid"}, "application/problem+json"
		}}
		r := newTestRegistrar(transport)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		// Both loops (cluster, vm) give up immediately on a non-retryable
		// status, so Done() closes without needing context cancellation.
		Eventually(r.Done(), "200ms", "5ms").Should(BeClosed())

		clusterCount := len(transport.requestsFor("cluster"))
		vmCount := len(transport.requestsFor("vm"))

		// No further attempts after Done() closes.
		Consistently(func() int { return len(transport.requestsFor("cluster")) }, "50ms", "5ms").Should(Equal(clusterCount))
		Consistently(func() int { return len(transport.requestsFor("vm")) }, "50ms", "5ms").Should(Equal(vmCount))
	})

	// TC-U-055: a 409 on vm registration is treated as non-fatal and keeps
	// being retried (unlike other 4xx statuses), rather than stopping the
	// loop.
	It("keeps retrying a vm 409 conflict instead of giving up (TC-U-055)", func() {
		transport := &fakeAgentTransport{responder: func(p agentv1alpha1.Provider) (int, any, string) {
			if p.ServiceType == "vm" {
				return http.StatusConflict, agentv1alpha1.Error{Title: "already registered"}, "application/problem+json"
			}
			return http.StatusCreated, p, "application/json"
		}}
		r := newTestRegistrar(transport, registration.WithLeaseRenewalInterval(10*time.Millisecond))

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		Eventually(func() int { return len(transport.requestsFor("vm")) }, "300ms", "5ms").Should(BeNumerically(">=", 3))

		// The registrar as a whole must not have given up: Done() should not
		// have closed (cluster keeps succeeding, vm keeps retrying).
		Consistently(r.Done(), "20ms").ShouldNot(BeClosed())
	})
})

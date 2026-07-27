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

	cpv1alpha1 "github.com/dcm-project/control-plane/api/sp/v1alpha1/provider"

	"github.com/dcm-project/osac-service-provider/internal/config"
	"github.com/dcm-project/osac-service-provider/internal/registration"
)

var discardLogger = slog.New(slog.DiscardHandler)

// capturedRequest is one decoded POST /providers request seen by
// fakeProviderTransport.
type capturedRequest struct {
	provider cpv1alpha1.Provider
	headers  http.Header
}

// responderFunc decides the fake control-plane's response for a given
// decoded registration request.
type responderFunc func(p cpv1alpha1.Provider) (statusCode int, body any, contentType string)

// fakeProviderTransport is a fake http.RoundTripper implementing
// control-plane's POST /providers contract, per the unit test plan's "fake
// control-plane's pkg/sp/client/provider HTTP round-tripper" collaborator.
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

	var p cpv1alpha1.Provider
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

func alwaysCreated(p cpv1alpha1.Provider) (int, any, string) {
	return http.StatusCreated, p, "application/json"
}

func testConfig() *config.Config {
	return &config.Config{
		DCM: config.DCMConfig{RegistrationURL: "http://control-plane.example.com/api/v1alpha1"},
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
		registration.WithReRegistrationInterval(20 * time.Millisecond),
	}, opts...)
	r, err := registration.NewRegistrar(testConfig(), discardLogger, allOpts...)
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
	It("sends no Authorization header to control-plane (TC-U-059)", func() {
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
	// capability metadata fresh. control-plane's Provider row has no
	// lease/TTL to expire (DD-050), so this is a freshness concern only,
	// not slot retention.
	It("periodically re-registers to refresh capability metadata after a successful registration (TC-U-058)", func() {
		transport := &fakeProviderTransport{responder: alwaysCreated}
		r := newTestRegistrar(transport, registration.WithReRegistrationInterval(10*time.Millisecond))

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		Eventually(func() int { return len(transport.requestsFor("cluster")) }, "500ms", "5ms").Should(BeNumerically(">=", 3))
	})

	// TC-U-054/TC-U-053... (053 covers the 4xx/409-non-retryable case
	// below): retryable failures (5xx) use exponential backoff and
	// eventually succeed once control-plane recovers.
	It("retries a retryable failure and eventually succeeds (TC-U-054)", func() {
		var mu sync.Mutex
		failuresLeft := 3
		transport := &fakeProviderTransport{responder: func(p cpv1alpha1.Provider) (int, any, string) {
			mu.Lock()
			defer mu.Unlock()
			if failuresLeft > 0 {
				failuresLeft--
				return http.StatusServiceUnavailable, cpv1alpha1.Error{Title: "unavailable", Type: "UNAVAILABLE"}, "application/problem+json"
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
		transport := &fakeProviderTransport{responder: func(_ cpv1alpha1.Provider) (int, any, string) {
			return http.StatusUnprocessableEntity, cpv1alpha1.Error{Title: "invalid", Type: "INVALID_ARGUMENT"}, "application/problem+json"
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

	// TC-U-053: a 409 Conflict is non-retryable, exactly like any other 4xx
	// — control-plane has no per-service-type exclusivity to contend over
	// (unlike the superseded environment-agent design, where a vm 409 meant
	// transient slot contention worth retrying into). Supersedes the
	// pre-pivot "409 is retryable" test design — see DD-050.
	It("treats a vm 409 conflict as non-retryable, same as other 4xx (TC-U-053)", func() {
		transport := &fakeProviderTransport{responder: func(p cpv1alpha1.Provider) (int, any, string) {
			if p.ServiceType == "vm" {
				return http.StatusConflict, cpv1alpha1.Error{Title: "already registered", Type: "ALREADY_EXISTS"}, "application/problem+json"
			}
			return http.StatusCreated, p, "application/json"
		}}
		r := newTestRegistrar(transport, registration.WithReRegistrationInterval(10*time.Millisecond))

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Start(ctx)

		// vm gives up after exactly one 409 — no retry.
		Eventually(func() int { return len(transport.requestsFor("vm")) }, "200ms", "5ms").Should(Equal(1))
		Consistently(func() int { return len(transport.requestsFor("vm")) }, "50ms", "5ms").Should(Equal(1))

		// cluster is unaffected: it keeps succeeding/renewing independently.
		Eventually(func() int { return len(transport.requestsFor("cluster")) }, "300ms", "5ms").Should(BeNumerically(">=", 2))
	})
})

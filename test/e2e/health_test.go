package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	cpclient "github.com/dcm-project/control-plane/pkg/sp/client/provider"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// health mirrors api/v1alpha1/openapi.yaml's Health schema (this suite
// decodes the real wire response into its own minimal struct rather than
// importing the main module's generated types, keeping test/e2e's module
// (REQ-E2E-080) independent of the parent module — see the test plan's
// "What's real here" note).
type health struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Health-check propagation (§3 of the test plan): asserts osac-sp's real
// internal/osac.Bootstrap — real gRPC dial + real OIDC client-credentials
// fetch against a real backend, not the bufconn fakes its own unit/
// integration tests use — reports healthy, and that control-plane's own
// real healthcheck.Monitor independently agrees.
//
// Label("tier-b-only") (#28, DD-212): these happy-path assertions ran
// identically, and with no additional grounding, against the mock here —
// "a backend that always says yes makes osac-sp report healthy" is
// already covered by internal/osac's bufconn integration tests. e2e.yaml
// now excludes this label; this Describe block runs only against the real
// backend (e2e-tierb.yaml), where it adds real, non-duplicated grounding.
var _ = Describe("osac-sp health, against the real backend", Label("tier-b-only"), func() {
	// TC-E2E-050 / AC-E2E-030
	It("reports the cluster health endpoint as healthy with no failure detail", func() {
		h := eventuallyHealthy("/api/v1alpha1/clusters/health")
		Expect(h.Detail).To(BeEmpty(), "a non-empty detail means either the OIDC token or the OSAC gRPC probe failed")
	})

	// TC-E2E-060 / AC-E2E-030
	It("reports the vm health endpoint identically to the cluster one", func() {
		clusterHealth := eventuallyHealthy("/api/v1alpha1/clusters/health")
		vmHealth := eventuallyHealthy("/api/v1alpha1/vms/health")
		Expect(vmHealth.Status).To(Equal(clusterHealth.Status))
		Expect(vmHealth.Detail).To(Equal(clusterHealth.Detail))
	})

	// TC-E2E-070 / AC-E2E-030
	It("is independently confirmed healthy by real control-plane's own health monitor", func() {
		client, err := cpclient.NewClientWithResponses(controlPlaneURL + "/api/v1alpha1")
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() []string {
			var statuses []string
			for _, serviceType := range []string{"cluster", "vm"} {
				for _, p := range listProviders(client, serviceType) {
					if p.HealthStatus != nil {
						statuses = append(statuses, *p.HealthStatus)
					} else {
						statuses = append(statuses, "<unset>")
					}
				}
			}
			return statuses
		}, 60*time.Second, 5*time.Second).Should(
			ConsistOf(healthStatusReady, healthStatusReady),
			"control-plane's healthcheck.Monitor must have polled osac-sp's own /health endpoint(s) at least once and recorded a healthy status for both registrations")
	})
})

// healthStatusReady is control-plane's own vocabulary for a provider whose
// backing /health check succeeded (internal/sp/store/model.HealthStatusReady
// in control-plane's own source — not re-exported via its generated REST
// client types, so duplicated here as a literal). It deliberately does not
// echo osac-sp's own "healthy" string: the two are different layers'
// independent vocabularies (osac-sp describing its own OSAC connectivity vs.
// control-plane describing its poll of osac-sp's /health endpoint), and this
// suite is exactly what confirmed that distinction against the real wire
// contract (see DD-140).
const healthStatusReady = "ready"

// eventuallyHealthy polls path until it reports "healthy", returning the
// final response for further assertions.
//
// This is deliberately not a single getHealth call: osac-sp's real OIDC
// token fetch + gRPC probe against osac-mock-provider (internal/osac.
// Bootstrap) run asynchronously in the background and are not gated by
// either the Kubernetes Deployment's Available condition (which the
// workflow's "Wait for readiness" step waits on, but which only requires a
// 2xx HTTP response — osac-sp deliberately reports its real status in the
// body, not the HTTP code, per DD-010) or by this suite's own BeforeSuite
// reachability check (which similarly only requires *a* response, not a
// healthy one). A single-shot check here would only pass reliably by
// accident of Ginkgo's default randomized top-level container ordering
// happening to run TC-E2E-040's 90-second registration-cycle wait (in the
// other Describe block) first, incidentally giving Bootstrap time to
// converge before these specs ever ran — exactly the kind of hidden,
// non-deterministic startup-timing assumption this e2e tier exists to
// catch (see DD-141's near-identical root cause in Server.Run's own
// internal readiness gate; DD-142 for this one).
func eventuallyHealthy(path string) health {
	var h health
	Eventually(func() string {
		h = getHealth(path)
		return h.Status
	}, 30*time.Second, 500*time.Millisecond).Should(Equal("healthy"))
	return h
}

func getHealth(path string) health {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, osacSPURLJoin(path), nil)
	Expect(err).NotTo(HaveOccurred())

	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())

	// osac-sp always returns 200 here regardless of health status (DD-010:
	// status lives in the body, not the HTTP code) — assert that explicitly
	// so a future change to that contract fails here with a clear message,
	// not as a confusing json.Unmarshal error on an unexpected body shape.
	Expect(resp.StatusCode).To(Equal(http.StatusOK), "response body: %s", string(body))

	var h health
	Expect(json.Unmarshal(body, &h)).To(Succeed(), "response body: %s", string(body))
	return h
}

func osacSPURLJoin(path string) string {
	return fmt.Sprintf("%s%s", osacSPURL, path)
}

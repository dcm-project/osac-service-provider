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
// fetch against osac-mock-provider, not the bufconn fakes its own unit/
// integration tests use — reports healthy, and that control-plane's own
// real healthcheck.Monitor independently agrees.
var _ = Describe("osac-sp health, against the real mock backend", func() {
	// TC-E2E-050 / AC-E2E-030
	It("reports the cluster health endpoint as healthy with no failure detail", func() {
		h := getHealth("/api/v1alpha1/clusters/health")
		Expect(h.Status).To(Equal("healthy"))
		Expect(h.Detail).To(BeEmpty(), "a non-empty detail means either the OIDC token or the OSAC gRPC probe failed")
	})

	// TC-E2E-060 / AC-E2E-030
	It("reports the vm health endpoint identically to the cluster one", func() {
		clusterHealth := getHealth("/api/v1alpha1/clusters/health")
		vmHealth := getHealth("/api/v1alpha1/vms/health")
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
// contract (see DD-090).
const healthStatusReady = "ready"

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

	var h health
	Expect(json.Unmarshal(body, &h)).To(Succeed(), "response body: %s", string(body))
	return h
}

func osacSPURLJoin(path string) string {
	return fmt.Sprintf("%s%s", osacSPURL, path)
}

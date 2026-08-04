package e2e_test

import (
	"context"
	"fmt"
	"time"

	cptypes "github.com/dcm-project/control-plane/api/sp/v1alpha1/provider"
	cpclient "github.com/dcm-project/control-plane/pkg/sp/client/provider"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Registration contract (§2 of the test plan): asserts osac-sp's real,
// independently-built registration loops (internal/registration.Registrar)
// actually land in a real, independently-built control-plane — the first
// cross-repo proof of this contract anywhere in the DCM project (see
// control-plane#40 / osac-service-provider#17).
var _ = Describe("osac-sp registration with real control-plane", func() {
	var client *cpclient.ClientWithResponses

	BeforeEach(func() {
		var err error
		client, err = cpclient.NewClientWithResponses(controlPlaneURL + "/api/v1alpha1")
		Expect(err).NotTo(HaveOccurred())
	})

	// TC-E2E-020 / AC-E2E-020
	It("registers exactly one cluster-type provider pointing at osac-sp's real endpoint", func() {
		provider := eventuallyFindProvider(client, "cluster")
		Expect(provider.Endpoint).To(Equal(osacSPEndpoint("clusters")))
	})

	// TC-E2E-030 / AC-E2E-020
	It("registers exactly one vm-type provider pointing at osac-sp's real endpoint", func() {
		provider := eventuallyFindProvider(client, "vm")
		Expect(provider.Endpoint).To(Equal(osacSPEndpoint("vms")))
	})

	// TC-E2E-040 / AC-E2E-020
	It("does not duplicate registrations across a re-registration cycle", func() {
		Consistently(func() int {
			return countProvidersOfType(client, "cluster")
		}, 90*time.Second, 10*time.Second).Should(Equal(1),
			"internal/registration.Registrar's periodic re-registration must stay idempotent on name, not create duplicates")
	})
})

// osacSPEndpoint returns the endpoint value osac-sp registers itself with
// for the given resource suffix — SP_ENDPOINT (per
// test/e2e/manifests/osac-service-provider.yaml) plus
// internal/registration.clusterEndpointSuffix/vmEndpointSuffix, exactly as
// Registrar.registerCluster/registerVM build it. Hardcoded here (not read
// back from the cluster) because it's this suite's own fixed,
// known-in-advance test input — the whole point of the assertion is that
// control-plane's records match it.
func osacSPEndpoint(resource string) string {
	return fmt.Sprintf("http://osac-service-provider:8080/api/v1alpha1/%s", resource)
}

func eventuallyFindProvider(client *cpclient.ClientWithResponses, serviceType string) cptypes.Provider {
	var found *cptypes.Provider
	Eventually(func() int {
		found = nil
		n := 0
		for _, p := range listProviders(client, serviceType) {
			n++
			p := p
			found = &p
		}
		return n
	}, 60*time.Second, 2*time.Second).Should(Equal(1),
		"expected exactly one %q-type provider registered with control-plane", serviceType)
	Expect(found).NotTo(BeNil())
	Expect(found.ServiceType).To(Equal(serviceType))
	return *found
}

func countProvidersOfType(client *cpclient.ClientWithResponses, serviceType string) int {
	return len(listProviders(client, serviceType))
}

func listProviders(client *cpclient.ClientWithResponses, serviceType string) []cptypes.Provider {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.ListProvidersWithResponse(ctx, &cptypes.ListProvidersParams{Type: &serviceType})
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode()).To(Equal(200), "unexpected ListProviders response: %s", string(resp.Body))
	Expect(resp.JSON200).NotTo(BeNil(), fmt.Sprintf("response body: %s", string(resp.Body)))

	if resp.JSON200.Providers == nil {
		return nil
	}
	return *resp.JSON200.Providers
}

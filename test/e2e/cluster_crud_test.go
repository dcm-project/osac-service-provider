package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// cluster mirrors api/v1alpha1/openapi.yaml's Cluster schema — only the
// fields this suite asserts on (see health_test.go's health struct for why
// test/e2e defines its own minimal wire types instead of importing the
// main module's generated ones).
type cluster struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Kubeconfig string `json:"kubeconfig"`
}

type clusterList struct {
	Results []cluster `json:"results"`
}

// Cluster CRUD (§5 of the test plan): asserts a full Create/Get/List/Delete
// lifecycle dispatches from real osac-sp into the real osac-mock-provider
// gRPC backend (not the bufconn fakes Milestone 3's own unit/integration
// tests use), reaching the mock's terminal ready status synchronously —
// osac-mock-provider resolves Create straight to CLUSTER_STATE_READY with
// no PROGRESSING transition (REQ-MOCK-030), unlike the bufconn IT
// fixture's own default behavior, so no polling is needed here for status
// convergence, only for the health checks (DD-142) which race against
// Bootstrap's async startup instead.
var _ = Describe("Cluster CRUD, against the real mock backend", func() {
	// TC-E2E-090 / AC-E2E-050
	It("creates, gets, lists, and deletes a cluster end-to-end over real HTTP", func() {
		id := uniqueID("e2e-cluster")

		created := createCluster(id)
		Expect(created.ID).To(Equal(id))
		Expect(created.Status).To(Equal("ACTIVE"), "the real mock resolves Create synchronously to CLUSTER_STATE_READY (REQ-MOCK-030)")
		// Create's own response never populates kubeconfig (REQ-CREATE-050
		// only guarantees id/status) — only Get does, conditionally on ACTIVE
		// (REQ-GET-020) — asserted below.

		got := getCluster(id)
		Expect(got.ID).To(Equal(id))
		Expect(got.Status).To(Equal("ACTIVE"))
		Expect(got.Kubeconfig).NotTo(BeEmpty(), "status is ACTIVE, so REQ-GET-020/REQ-MOCK-120 both require a populated kubeconfig")
		assertValidBase64(got.Kubeconfig)

		Expect(listClusterIDs()).To(ContainElement(id))

		deleteCluster(id, http.StatusNoContent)

		status, _ := osacRequest(http.MethodGet, fmt.Sprintf("/api/v1alpha1/clusters/%s", id), "")
		Expect(status).To(Equal(http.StatusNotFound), "a deleted cluster's Get must be 404, distinct from Delete's own tolerate-404 behavior (REQ-DELETE-020)")
	})

	// TC-E2E-091 / AC-E2E-050
	It("tolerates deleting an already-deleted cluster", func() {
		id := uniqueID("e2e-cluster-del")
		createCluster(id)

		deleteCluster(id, http.StatusNoContent)
		deleteCluster(id, http.StatusNoContent)
	})

	// TC-E2E-092 / AC-E2E-051 (REQ-E2E-092, REQ-CREATE-040/DD-100): a
	// second Create for the same id must be idempotent against the real
	// mock's ALREADY_EXISTS (REQ-MOCK-020) — this is the one contract
	// osac-mock-provider was purpose-built to make testable against a
	// real backend, not just a bufconn fake's simulation of it.
	It("is idempotent on a repeat Create for the same id (real ALREADY_EXISTS)", func() {
		id := uniqueID("e2e-cluster-dup")

		first := createCluster(id)
		Expect(first.ID).To(Equal(id))

		second := createCluster(id)
		Expect(second.ID).To(Equal(first.ID))
		Expect(second.Status).To(Equal(first.Status), "a repeat Create must return the existing cluster's current state (REQ-CREATE-040), not an error")

		ids := listClusterIDs()
		count := 0
		for _, listedID := range ids {
			if listedID == id {
				count++
			}
		}
		Expect(count).To(Equal(1), "the duplicate Create must not have produced a second stored object")
	})
})

// validClusterCreateBody matches internal/handlers/cluster's own
// create_integration_test.go validCreateJSON exactly, so this suite
// exercises the identical, already-spec'd-valid request shape over real
// HTTP instead of a bespoke one.
const validClusterCreateBody = `{"spec":{"version":"1.29","nodes":{"worker":{"count":3}},"metadata":{"name":"e2e"},"provider_hints":{"osac":{"template_id":"default-hcp"}}}}`

func createCluster(id string) cluster {
	status, body := osacRequest(http.MethodPost, fmt.Sprintf("/api/v1alpha1/clusters?id=%s", id), validClusterCreateBody)
	Expect(status).To(Equal(http.StatusCreated), "response body: %s", string(body))
	var c cluster
	Expect(json.Unmarshal(body, &c)).To(Succeed(), "response body: %s", string(body))
	return c
}

func getCluster(id string) cluster {
	status, body := osacRequest(http.MethodGet, fmt.Sprintf("/api/v1alpha1/clusters/%s", id), "")
	Expect(status).To(Equal(http.StatusOK), "response body: %s", string(body))
	var c cluster
	Expect(json.Unmarshal(body, &c)).To(Succeed(), "response body: %s", string(body))
	return c
}

func listClusterIDs() []string {
	status, body := osacRequest(http.MethodGet, "/api/v1alpha1/clusters", "")
	Expect(status).To(Equal(http.StatusOK), "response body: %s", string(body))
	var list clusterList
	Expect(json.Unmarshal(body, &list)).To(Succeed(), "response body: %s", string(body))

	ids := make([]string, 0, len(list.Results))
	for _, c := range list.Results {
		ids = append(ids, c.ID)
	}
	return ids
}

func deleteCluster(id string, wantStatus int) {
	status, body := osacRequest(http.MethodDelete, fmt.Sprintf("/api/v1alpha1/clusters/%s", id), "")
	Expect(status).To(Equal(wantStatus), "response body: %s", string(body))
}

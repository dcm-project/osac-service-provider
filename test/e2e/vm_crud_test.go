package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// vm mirrors api/v1alpha1/openapi.yaml's VirtualMachine schema — only the
// fields this suite asserts on (see cluster_crud_test.go's cluster struct
// for the same rationale).
type vm struct {
	ID                string `json:"id"`
	Status            string `json:"status"`
	InternalIPAddress string `json:"internal_ip_address"`
	ExternalIPAddress string `json:"external_ip_address"`
}

type vmList struct {
	Results []vm `json:"results"`
}

// VM CRUD (§5 of the test plan): same shape as cluster_crud_test.go's
// Cluster CRUD, against Milestone 4's /api/v1alpha1/vms surface.
// osac-mock-provider resolves Create straight to
// COMPUTE_INSTANCE_STATE_RUNNING synchronously (REQ-MOCK-030), so — unlike
// Cluster's Get — no extra RPC/field is conditionally populated here:
// internal_ip_address/external_ip_address are echoed directly from the
// same object on every call (REQ-VMLIST-030), with no mock-side value set
// for them (empty string is a valid response, REQ-VMGET-030).
var _ = Describe("VM CRUD, against the real mock backend", func() {
	// TC-E2E-100 / AC-E2E-060
	It("creates, gets, lists, and deletes a vm end-to-end over real HTTP", func() {
		id := uniqueID("e2e-vm")

		created := createVM(id)
		Expect(created.ID).To(Equal(id))
		Expect(created.Status).To(Equal("RUNNING"), "the real mock resolves Create synchronously to COMPUTE_INSTANCE_STATE_RUNNING (REQ-MOCK-030)")

		got := getVM(id)
		Expect(got.ID).To(Equal(id))
		Expect(got.Status).To(Equal("RUNNING"))

		Expect(listVMIDs()).To(ContainElement(id))

		deleteVM(id, http.StatusNoContent)

		status, _ := osacRequest(http.MethodGet, fmt.Sprintf("/api/v1alpha1/vms/%s", id), "")
		Expect(status).To(Equal(http.StatusNotFound), "a deleted vm's Get must be 404, distinct from Delete's own tolerate-404 behavior (REQ-VMDELETE-020)")
	})

	// TC-E2E-101 / AC-E2E-060
	It("tolerates deleting an already-deleted vm", func() {
		id := uniqueID("e2e-vm-del")
		createVM(id)

		deleteVM(id, http.StatusNoContent)
		deleteVM(id, http.StatusNoContent)
	})
})

// validVMCreateBody matches internal/handlers/vm's own
// create_integration_test.go validCreateJSON exactly, so this suite
// exercises the identical, already-spec'd-valid request shape over real
// HTTP instead of a bespoke one.
const validVMCreateBody = `{"spec":{"storage":{"disks":[{"name":"boot","capacity":"100GB"}]},"guest_os":{"type":"rhel-9"},"metadata":{"name":"e2e"},"provider_hints":{"osac":{"template_id":"default-vm","instance_type":"standard-4-16"}}}}`

func createVM(id string) vm {
	status, body := osacRequest(http.MethodPost, fmt.Sprintf("/api/v1alpha1/vms?id=%s", id), validVMCreateBody)
	Expect(status).To(Equal(http.StatusCreated), "response body: %s", string(body))
	var v vm
	Expect(json.Unmarshal(body, &v)).To(Succeed(), "response body: %s", string(body))
	return v
}

func getVM(id string) vm {
	status, body := osacRequest(http.MethodGet, fmt.Sprintf("/api/v1alpha1/vms/%s", id), "")
	Expect(status).To(Equal(http.StatusOK), "response body: %s", string(body))
	var v vm
	Expect(json.Unmarshal(body, &v)).To(Succeed(), "response body: %s", string(body))
	return v
}

func listVMIDs() []string {
	status, body := osacRequest(http.MethodGet, "/api/v1alpha1/vms", "")
	Expect(status).To(Equal(http.StatusOK), "response body: %s", string(body))
	var list vmList
	Expect(json.Unmarshal(body, &list)).To(Succeed(), "response body: %s", string(body))

	ids := make([]string, 0, len(list.Results))
	for _, v := range list.Results {
		ids = append(ids, v.ID)
	}
	return ids
}

func deleteVM(id string, wantStatus int) {
	status, body := osacRequest(http.MethodDelete, fmt.Sprintf("/api/v1alpha1/vms/%s", id), "")
	Expect(status).To(Equal(wantStatus), "response body: %s", string(body))
}

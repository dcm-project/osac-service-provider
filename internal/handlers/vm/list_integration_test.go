package vm_test

import (
	"encoding/json"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
)

func listVMs(f *integrationFixture, query string) *http.Response {
	resp, err := http.Get(f.URL("/api/v1alpha1/vms" + query)) //nolint:noctx // test helper
	Expect(err).NotTo(HaveOccurred())
	return resp
}

var _ = Describe("VM List (integration, real HTTP + router + bufconn OSAC fake)", func() {
	var f *integrationFixture

	BeforeEach(func() {
		f = newIntegrationFixture()
		DeferCleanup(f.Close)
	})

	// TC-I-320 (REQ-VMLIST-010/030, AC-VMLIST-010): List returns exact
	// entries with the ownership filter applied, over real HTTP, including
	// the internal_ip_address echo.
	It("returns exact entries with the ownership filter applied, over real HTTP (TC-I-320)", func() {
		var recordedFilter string
		f.fake.listFunc = func(req *publicv1.ComputeInstancesListRequest) (*publicv1.ComputeInstancesListResponse, error) {
			recordedFilter = req.GetFilter()
			return &publicv1.ComputeInstancesListResponse{
				Size:  2,
				Total: 2,
				Items: []*publicv1.ComputeInstance{
					{Id: "vm1", Status: &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING, InternalIpAddress: "10.200.1.1"}},
					{Id: "vm2", Status: &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_STARTING, InternalIpAddress: "10.200.1.2"}},
				},
			}, nil
		}

		resp := listVMs(f, "")
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var list v1alpha1.VirtualMachineList
		Expect(json.NewDecoder(resp.Body).Decode(&list)).To(Succeed())

		Expect(recordedFilter).To(Equal(`this.metadata.labels["dcm.io/managed-by"] == "dcm"`))
		Expect(list.Results).To(HaveLen(2))
		Expect(*list.Results[0].Id).To(Equal("vm1"))
		Expect(list.Results[0].Status).To(Equal(v1alpha1.RUNNING))
		Expect(*list.Results[0].InternalIpAddress).To(Equal("10.200.1.1"))
		Expect(*list.Results[1].Id).To(Equal("vm2"))
		Expect(list.Results[1].Status).To(Equal(v1alpha1.PROVISIONING))
		Expect(*list.Results[1].InternalIpAddress).To(Equal("10.200.1.2"))
	})

	// TC-I-321 (REQ-VMLIST-020/040, AC-VMLIST-020): pagination round-trips
	// across two real, sequential HTTP requests.
	It("round-trips page_token through OSAC's offset across two real HTTP requests (TC-I-321)", func() {
		var recordedOffsets []int32
		f.fake.listFunc = func(req *publicv1.ComputeInstancesListRequest) (*publicv1.ComputeInstancesListResponse, error) {
			recordedOffsets = append(recordedOffsets, req.GetOffset())
			return &publicv1.ComputeInstancesListResponse{Size: 50, Total: 100}, nil
		}

		first := listVMs(f, "")
		Expect(first.StatusCode).To(Equal(http.StatusOK))
		var firstList v1alpha1.VirtualMachineList
		Expect(json.NewDecoder(first.Body).Decode(&firstList)).To(Succeed())
		_ = first.Body.Close()
		Expect(firstList.NextPageToken).NotTo(BeNil())

		second := listVMs(f, "?page_token="+*firstList.NextPageToken)
		defer func() { _ = second.Body.Close() }()
		Expect(second.StatusCode).To(Equal(http.StatusOK))

		Expect(recordedOffsets).To(Equal([]int32{0, 50}))
	})
})

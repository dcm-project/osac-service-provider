package vm_test

import (
	"encoding/json"
	"fmt"
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
		Expect(list.Results[0].Status).To(Equal(v1alpha1.VMStatusRUNNING))
		Expect(*list.Results[0].InternalIpAddress).To(Equal("10.200.1.1"))
		Expect(*list.Results[1].Id).To(Equal("vm2"))
		Expect(list.Results[1].Status).To(Equal(v1alpha1.VMStatusPROVISIONING))
		Expect(*list.Results[1].InternalIpAddress).To(Equal("10.200.1.2"))
	})

	// TC-I-321 (REQ-VMLIST-020/040, AC-VMLIST-020): pagination round-trips
	// across two real, sequential HTTP requests.
	It("round-trips page_token through OSAC's offset across two real HTTP requests (TC-I-321)", func() {
		var recordedOffsets []int32
		// Items are keyed off the requested offset so the two pages are
		// distinguishable by content, not just by the offsets the SP
		// happened to request — otherwise a bug that returned page 1's
		// items again for page 2 would go undetected.
		f.fake.listFunc = func(req *publicv1.ComputeInstancesListRequest) (*publicv1.ComputeInstancesListResponse, error) {
			recordedOffsets = append(recordedOffsets, req.GetOffset())
			items := make([]*publicv1.ComputeInstance, 50)
			for i := range items {
				items[i] = &publicv1.ComputeInstance{
					Id:     fmt.Sprintf("v%d", req.GetOffset()+int32(i)),
					Status: &publicv1.ComputeInstanceStatus{State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING},
				}
			}
			return &publicv1.ComputeInstancesListResponse{Size: 50, Total: 100, Items: items}, nil
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
		var secondList v1alpha1.VirtualMachineList
		Expect(json.NewDecoder(second.Body).Decode(&secondList)).To(Succeed())

		Expect(recordedOffsets).To(Equal([]int32{0, 50}))
		Expect(*firstList.Results[0].Id).To(Equal("v0"))
		Expect(*secondList.Results[0].Id).To(Equal("v50"))
	})

	// TC-I-322 (REQ-VMLIST-020, REQ-VMERR-010, AC-VMLIST-030): a malformed
	// token is rejected at the real HTTP boundary without an OSAC RPC.
	It("rejects a malformed page_token at the real HTTP boundary, without calling List (TC-I-322)", func() {
		resp := listVMs(f, "?page_token=not-valid-base64!!!")
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		Expect(resp.Header.Get("Content-Type")).To(Equal("application/problem+json"))
		var body v1alpha1.Error
		Expect(json.NewDecoder(resp.Body).Decode(&body)).To(Succeed())
		Expect(body.Type).To(Equal(v1alpha1.ErrorTypeINVALIDARGUMENT))
		Expect(f.fake.ListCalls()).To(BeEmpty())
	})

	// TC-I-323 (REQ-VMLIST-050, AC-VMLIST-050): the real HTTP boundary applies
	// AEP-158 defaulting and clamping, and rejects negative values.
	DescribeTable("normalizes max_page_size at the real HTTP boundary (TC-I-323)",
		func(value int32, wantInvalid bool, wantLimit int32) {
			resp := listVMs(f, fmt.Sprintf("?max_page_size=%d", value))
			defer func() { _ = resp.Body.Close() }()

			if wantInvalid {
				Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
				Expect(resp.Header.Get("Content-Type")).To(Equal("application/problem+json"))
				var body v1alpha1.Error
				Expect(json.NewDecoder(resp.Body).Decode(&body)).To(Succeed())
				Expect(body.Type).To(Equal(v1alpha1.ErrorTypeINVALIDARGUMENT))
				Expect(*body.Status).To(Equal(int32(http.StatusBadRequest)))
				Expect(*body.Detail).To(ContainSubstring("max_page_size must not be negative"))
				Expect(f.fake.ListCalls()).To(BeEmpty())
				return
			}

			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			calls := f.fake.ListCalls()
			Expect(calls).To(HaveLen(1))
			Expect(calls[0].GetLimit()).To(Equal(wantLimit))
		},
		Entry("rejects a negative value", int32(-1), true, int32(0)),
		Entry("defaults zero", int32(0), false, int32(50)),
		Entry("clamps a value above the maximum", int32(101), false, int32(100)),
		Entry("accepts the minimum", int32(1), false, int32(1)),
		Entry("accepts the maximum", int32(100), false, int32(100)),
	)

	// TC-I-324 (REQ-VMLIST-040, AC-VMLIST-040): an inconsistent Size/Total
	// response must not produce a token that reissues the same page.
	It("does not reissue a page_token for an empty Size/Total-mismatch page, over real HTTP (TC-I-324)", func() {
		f.fake.listFunc = func(*publicv1.ComputeInstancesListRequest) (*publicv1.ComputeInstancesListResponse, error) {
			return &publicv1.ComputeInstancesListResponse{Items: nil, Size: 0, Total: 5}, nil
		}

		resp := listVMs(f, "")
		defer func() { _ = resp.Body.Close() }()

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var list v1alpha1.VirtualMachineList
		Expect(json.NewDecoder(resp.Body).Decode(&list)).To(Succeed())
		Expect(list.NextPageToken).To(BeNil())
		Expect(f.fake.ListCalls()).To(HaveLen(1))
	})
})

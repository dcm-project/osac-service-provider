package aapmock_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dcm-project/osac-service-provider/test/aapmock"
)

type templateLookupResponse struct {
	Count   int `json:"count"`
	Results []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"results"`
}

var _ = Describe("Templates", func() {
	var srv *httptest.Server

	BeforeEach(func() {
		srv = httptest.NewServer(aapmock.NewHandler(testToken))
	})
	AfterEach(func() {
		srv.Close()
	})

	// authedGet is a small test helper for this package's read-only lookup
	// endpoints, which take no body: attaches the shared testToken's
	// Authorization header (DD-225) before issuing a real GET.
	authedGet := func(url string) (*http.Response, error) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Authorization", "Bearer "+testToken)
		return srv.Client().Do(req)
	}

	// TC-U-560: GetTemplate (job template lookup by name) returns a real
	// {count, results} body for any requested name.
	It("returns a real {count, results} body for any requested job template name (TC-U-560)", func() {
		resp, err := authedGet(srv.URL + "/v2/job_templates/?name=osac-create-hosted-cluster")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var lookup templateLookupResponse
		Expect(json.NewDecoder(resp.Body).Decode(&lookup)).To(Succeed())
		Expect(lookup.Count).To(Equal(1))
		Expect(lookup.Results).To(HaveLen(1))
		Expect(lookup.Results[0].Name).To(Equal("osac-create-hosted-cluster"))
		Expect(lookup.Results[0].ID).To(BeNumerically(">", 0))
	})

	// TC-U-560b: the same template name always resolves to the same ID
	// across repeated lookups.
	It("resolves the same template name to a stable ID across lookups", func() {
		resp1, err := authedGet(srv.URL + "/v2/job_templates/?name=osac-create-hosted-cluster")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp1.Body.Close() }()
		var lookup1 templateLookupResponse
		Expect(json.NewDecoder(resp1.Body).Decode(&lookup1)).To(Succeed())

		resp2, err := authedGet(srv.URL + "/v2/job_templates/?name=osac-create-hosted-cluster")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp2.Body.Close() }()
		var lookup2 templateLookupResponse
		Expect(json.NewDecoder(resp2.Body).Decode(&lookup2)).To(Succeed())

		Expect(lookup2.Results[0].ID).To(Equal(lookup1.Results[0].ID))
	})

	// TC-U-561: the workflow_job_templates lookup endpoint independently
	// returns the same real {count, results} shape for any name.
	It("returns a real {count, results} body for any requested workflow template name (TC-U-561)", func() {
		resp, err := authedGet(srv.URL + "/v2/workflow_job_templates/?name=osac-create-tenant")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var lookup templateLookupResponse
		Expect(json.NewDecoder(resp.Body).Decode(&lookup)).To(Succeed())
		Expect(lookup.Count).To(Equal(1))
		Expect(lookup.Results).To(HaveLen(1))
		Expect(lookup.Results[0].Name).To(Equal("osac-create-tenant"))
	})
})

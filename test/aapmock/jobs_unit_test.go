package aapmock_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dcm-project/osac-service-provider/test/aapmock"
)

// launchJob is a small test helper that launches a job template and returns
// its assigned job ID, failing the spec on any non-2xx response.
func launchJob(srv *httptest.Server, extraVars map[string]any) int {
	body, err := json.Marshal(map[string]any{"extra_vars": extraVars})
	Expect(err).NotTo(HaveOccurred())

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v2/job_templates/osac-create-hosted-cluster/launch/", strings.NewReader(string(body)))
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Authorization", "Bearer test-token")

	resp, err := srv.Client().Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	Expect(resp.StatusCode).To(Equal(http.StatusOK))

	var launchResp struct {
		ID int `json:"id"`
	}
	Expect(json.NewDecoder(resp.Body).Decode(&launchResp)).To(Succeed())
	return launchResp.ID
}

var _ = Describe("Jobs", func() {
	var srv *httptest.Server

	BeforeEach(func() {
		srv = httptest.NewServer(aapmock.NewHandler())
	})
	AfterEach(func() {
		srv.Close()
	})

	// TC-U-562: LaunchJobTemplate/LaunchWorkflowTemplate return unique,
	// incrementing job IDs.
	It("returns a unique, incrementing job ID for each launch (TC-U-562)", func() {
		id1 := launchJob(srv, map[string]any{"resource": "cluster-a"})
		id2 := launchJob(srv, map[string]any{"resource": "cluster-b"})
		Expect(id2).To(BeNumerically(">", id1))
	})

	// TC-U-563: LaunchJobTemplate accepts an arbitrary extra_vars payload
	// without validating its shape.
	It("accepts an arbitrary extra_vars payload without validating its shape (TC-U-563)", func() {
		id := launchJob(srv, map[string]any{
			"osac_job_vars": map[string]any{
				"resource": map[string]any{
					"kind": "ClusterOrder",
					"name": "test-cluster",
				},
			},
		})
		Expect(id).To(BeNumerically(">", 0))
	})

	// TC-U-564: GetJob reports a launched job as "successful" immediately,
	// with started/finished both populated (DD-213).
	It("reports a launched job as successful immediately, with started/finished populated (TC-U-564)", func() {
		id := launchJob(srv, map[string]any{"resource": "cluster-a"})

		req, err := http.NewRequest(http.MethodGet, srv.URL+"/v2/jobs/"+itoa(id)+"/", nil)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Authorization", "Bearer test-token")
		resp, err := srv.Client().Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var job struct {
			ID       int    `json:"id"`
			Status   string `json:"status"`
			Started  string `json:"started"`
			Finished string `json:"finished"`
		}
		Expect(json.NewDecoder(resp.Body).Decode(&job)).To(Succeed())
		Expect(job.ID).To(Equal(id))
		Expect(job.Status).To(Equal("successful"))
		Expect(job.Started).NotTo(BeEmpty())
		Expect(job.Finished).NotTo(BeEmpty())
	})

	// TC-U-565: GetJob on an unknown job ID returns a real 404.
	It("returns 404 for an unknown job ID (TC-U-565)", func() {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/v2/jobs/999999/", nil)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Authorization", "Bearer test-token")
		resp, err := srv.Client().Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
	})

	// TC-U-566: CanCancelJob reports can_cancel: true for a just-launched
	// (non-terminal) job.
	It("reports can_cancel: true for a just-launched job (TC-U-566)", func() {
		id := launchJob(srv, map[string]any{"resource": "cluster-a"})

		req, err := http.NewRequest(http.MethodGet, srv.URL+"/v2/jobs/"+itoa(id)+"/cancel/", nil)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("Authorization", "Bearer test-token")
		resp, err := srv.Client().Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var canCancel struct {
			CanCancel bool `json:"can_cancel"`
		}
		Expect(json.NewDecoder(resp.Body).Decode(&canCancel)).To(Succeed())
		Expect(canCancel.CanCancel).To(BeTrue())
	})

	// TC-U-567: CancelJob on a non-terminal job returns 202, and the job's
	// subsequent GetJob reports status: "canceled".
	It("cancels a non-terminal job with 202, then reports canceled (TC-U-567)", func() {
		id := launchJob(srv, map[string]any{"resource": "cluster-a"})

		cancelReq, err := http.NewRequest(http.MethodPost, srv.URL+"/v2/jobs/"+itoa(id)+"/cancel/", nil)
		Expect(err).NotTo(HaveOccurred())
		cancelReq.Header.Set("Authorization", "Bearer test-token")
		cancelResp, err := srv.Client().Do(cancelReq)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = cancelResp.Body.Close() }()
		Expect(cancelResp.StatusCode).To(Equal(http.StatusAccepted))

		getReq, err := http.NewRequest(http.MethodGet, srv.URL+"/v2/jobs/"+itoa(id)+"/", nil)
		Expect(err).NotTo(HaveOccurred())
		getReq.Header.Set("Authorization", "Bearer test-token")
		getResp, err := srv.Client().Do(getReq)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = getResp.Body.Close() }()

		var job struct {
			Status string `json:"status"`
		}
		Expect(json.NewDecoder(getResp.Body).Decode(&job)).To(Succeed())
		Expect(job.Status).To(Equal("canceled"))
	})

	// TC-U-568: CancelJob on an already-terminal job returns 405, not a
	// silent success.
	It("returns 405 when canceling an already-canceled job (TC-U-568)", func() {
		id := launchJob(srv, map[string]any{"resource": "cluster-a"})

		firstCancel, err := http.NewRequest(http.MethodPost, srv.URL+"/v2/jobs/"+itoa(id)+"/cancel/", nil)
		Expect(err).NotTo(HaveOccurred())
		firstCancel.Header.Set("Authorization", "Bearer test-token")
		firstResp, err := srv.Client().Do(firstCancel)
		Expect(err).NotTo(HaveOccurred())
		_ = firstResp.Body.Close()
		Expect(firstResp.StatusCode).To(Equal(http.StatusAccepted))

		secondCancel, err := http.NewRequest(http.MethodPost, srv.URL+"/v2/jobs/"+itoa(id)+"/cancel/", nil)
		Expect(err).NotTo(HaveOccurred())
		secondCancel.Header.Set("Authorization", "Bearer test-token")
		secondResp, err := srv.Client().Do(secondCancel)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = secondResp.Body.Close() }()
		Expect(secondResp.StatusCode).To(Equal(http.StatusMethodNotAllowed))
	})

	// TC-U-572: GetJob/CanCancelJob/CancelJob all reject a non-numeric job
	// ID with 400, rather than panicking or matching an unintended route.
	DescribeTable("rejects a non-numeric job id with 400 (TC-U-572)",
		func(method, path string) {
			req, err := http.NewRequest(method, srv.URL+path, nil)
			Expect(err).NotTo(HaveOccurred())
			req.Header.Set("Authorization", "Bearer test-token")
			resp, err := srv.Client().Do(req)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
		},
		Entry("GetJob", http.MethodGet, "/v2/jobs/not-a-number/"),
		Entry("CanCancelJob", http.MethodGet, "/v2/jobs/not-a-number/cancel/"),
		Entry("CancelJob", http.MethodPost, "/v2/jobs/not-a-number/cancel/"),
	)

	// TC-U-573: CanCancelJob/CancelJob both return 404 for an unknown job
	// ID, matching GetJob's own not-found behavior (TC-U-565).
	DescribeTable("returns 404 for an unknown job id (TC-U-573)",
		func(method, path string) {
			req, err := http.NewRequest(method, srv.URL+path, nil)
			Expect(err).NotTo(HaveOccurred())
			req.Header.Set("Authorization", "Bearer test-token")
			resp, err := srv.Client().Do(req)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = resp.Body.Close() }()
			Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		},
		Entry("CanCancelJob", http.MethodGet, "/v2/jobs/999999/cancel/"),
		Entry("CancelJob", http.MethodPost, "/v2/jobs/999999/cancel/"),
	)

	// TC-U-569: every endpoint requires an Authorization header to be
	// present, but never validates its content (NFR-TB-030 — the
	// permissiveness boundary is deliberate, mirroring DD-132's OIDC stub).
	It("accepts requests with no Authorization header at all (TC-U-569)", func() {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/v2/job_templates/?name=osac-create-hosted-cluster", nil)
		Expect(err).NotTo(HaveOccurred())
		resp, err := srv.Client().Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = resp.Body.Close() }()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
	})
})

func itoa(i int) string {
	return strconv.Itoa(i)
}

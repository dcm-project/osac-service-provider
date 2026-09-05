package main

// TC-I-090: drives this binary's real run() end to end — real env-var
// config loading and a real net.Listen-backed http.Server — then makes
// real HTTP calls through the full launch → poll → cancel lifecycle, same
// package-main-for-unexported-run-access convention as
// test/cmd/osac-mock-provider/main_integration_test.go's TC-I-031.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMainIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "AAP Mock Main Integration Suite")
}

// testToken is the Bearer value this suite configures the real binary
// with (MOCK_AAP_TOKEN) and presents on every request (DD-225).
const testToken = "test-token"

// authedDo builds a request with testToken's Authorization header attached
// and performs it, failing the spec on any construction/transport error.
// body may be nil for a bodyless request (e.g. GET).
func authedDo(method, url string, body *strings.Reader) *http.Response {
	var reqBody *strings.Reader
	if body != nil {
		reqBody = body
	} else {
		reqBody = strings.NewReader("")
	}
	req, err := http.NewRequest(method, url, reqBody) //nolint:noctx // test helper
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Authorization", "Bearer "+testToken)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	return resp
}

// reserveLoopbackAddr binds an ephemeral loopback port, notes its address,
// then immediately releases it so run() can bind that exact address once
// its env var is set — same pattern as
// test/cmd/osac-mock-provider/main_integration_test.go's own helper of the same
// name.
func reserveLoopbackAddr() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	addr := ln.Addr().String()
	Expect(ln.Close()).To(Succeed())
	return addr
}

var _ = Describe("AAP mock binary (integration)", func() {
	// TC-I-090: a real client drives launch → GetJob → cancel against the
	// real binary over a real listener.
	It("serves the full launch/poll/cancel lifecycle over a real listener (TC-I-090)", func() {
		addr := reserveLoopbackAddr()

		t := GinkgoT()
		t.Setenv("MOCK_AAP_ADDRESS", addr)
		t.Setenv("MOCK_AAP_TOKEN", testToken)

		ctx, cancel := context.WithCancel(context.Background())
		runDone := make(chan error, 1)
		go func() { runDone <- run(ctx, slog.New(slog.DiscardHandler)) }()
		defer func() {
			cancel()
			Eventually(runDone, "3s").Should(Receive())
		}()

		Eventually(func() error {
			conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
			if err == nil {
				_ = conn.Close()
			}
			return err
		}, "2s", "10ms").Should(Succeed())

		baseURL := "http://" + addr

		lookupResp := authedDo(http.MethodGet, baseURL+"/v2/job_templates/?name=osac-create-hosted-cluster", nil)
		defer func() { _ = lookupResp.Body.Close() }()
		Expect(lookupResp.StatusCode).To(Equal(http.StatusOK))

		launchBody := strings.NewReader(`{"extra_vars":{"resource":{"kind":"ClusterOrder","name":"it-cluster"}}}`)
		launchResp := authedDo(http.MethodPost, baseURL+"/v2/job_templates/osac-create-hosted-cluster/launch/", launchBody)
		defer func() { _ = launchResp.Body.Close() }()
		Expect(launchResp.StatusCode).To(Equal(http.StatusOK))

		var launched struct {
			ID int `json:"id"`
		}
		Expect(json.NewDecoder(launchResp.Body).Decode(&launched)).To(Succeed())
		Expect(launched.ID).To(BeNumerically(">", 0))

		jobResp := authedDo(http.MethodGet, baseURL+"/v2/jobs/"+strconv.Itoa(launched.ID)+"/", nil)
		defer func() { _ = jobResp.Body.Close() }()

		var jobStatus struct {
			Status string `json:"status"`
		}
		Expect(json.NewDecoder(jobResp.Body).Decode(&jobStatus)).To(Succeed())
		Expect(jobStatus.Status).To(Equal("successful"))
	})
})

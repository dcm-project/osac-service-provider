package main

// TC-I-090: drives this binary's real run() end to end — real env-var
// config loading and a real net.Listen-backed http.Server — then makes
// real HTTP calls through the full launch → poll → cancel lifecycle, same
// package-main-for-unexported-run-access convention as
// cmd/osac-mock-provider/main_integration_test.go's TC-I-031.

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

// reserveLoopbackAddr binds an ephemeral loopback port, notes its address,
// then immediately releases it so run() can bind that exact address once
// its env var is set — same pattern as
// cmd/osac-mock-provider/main_integration_test.go's own helper of the same
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

		lookupResp, err := http.Get(baseURL + "/v2/job_templates/?name=osac-create-hosted-cluster") //nolint:noctx // test helper
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = lookupResp.Body.Close() }()
		Expect(lookupResp.StatusCode).To(Equal(http.StatusOK))

		launchBody := strings.NewReader(`{"extra_vars":{"resource":{"kind":"ClusterOrder","name":"it-cluster"}}}`)
		launchResp, err := http.Post(baseURL+"/v2/job_templates/osac-create-hosted-cluster/launch/", "application/json", launchBody) //nolint:noctx // test helper
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = launchResp.Body.Close() }()
		Expect(launchResp.StatusCode).To(Equal(http.StatusOK))

		var launched struct {
			ID int `json:"id"`
		}
		Expect(json.NewDecoder(launchResp.Body).Decode(&launched)).To(Succeed())
		Expect(launched.ID).To(BeNumerically(">", 0))

		jobResp, err := http.Get(baseURL + "/v2/jobs/" + strconv.Itoa(launched.ID) + "/") //nolint:noctx // test helper
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = jobResp.Body.Close() }()

		var jobStatus struct {
			Status string `json:"status"`
		}
		Expect(json.NewDecoder(jobResp.Body).Decode(&jobStatus)).To(Succeed())
		Expect(jobStatus.Status).To(Equal("successful"))
	})
})

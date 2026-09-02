package main

// TC-I-031 (per .ai/test-plans/osac-sp-e2e-mock-provider.test-plan.md,
// "5. cmd/osac-mock-provider — integration"): drives this binary's real
// run() end to end — real env-var config loading, a real net.Listen-backed
// grpc.Server hosting all 5 fake osac.public.v1 services, and a real
// net.Listen-backed OIDC discovery+token HTTP server — then points a real
// osac.Bootstrap (production osac.New(), the real SP's own client-side
// code) at those two addresses to prove the mock is a genuine end-to-end
// substitute for OSAC. Same package-main-for-unexported-run-access
// convention as cmd/osac-service-provider/main_integration_test.go.

import (
	"context"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dcm-project/osac-service-provider/internal/config"
	"github.com/dcm-project/osac-service-provider/internal/osac"
	"github.com/dcm-project/osac-service-provider/test/mockprovider"
)

// writeTestCACertFile writes mockprovider's static self-signed test
// certificate (its own trust anchor, being self-signed) to a temp file, so
// a real osac.Bootstrap can be pointed at it via TLSCertFile. The mock's
// gRPC server (run(), via mockprovider.ServerTLSConfig) always presents
// this same certificate (REQ-MOCK-140/DD-229).
func writeTestCACertFile(t GinkgoTInterface) string {
	f, err := os.CreateTemp("", "osac-mock-provider-test-ca-*.pem")
	Expect(err).NotTo(HaveOccurred())
	_, err = f.Write(mockprovider.CertPEM)
	Expect(err).NotTo(HaveOccurred())
	Expect(f.Close()).To(Succeed())
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	return f.Name()
}

func TestMainIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Mock Provider Main Integration Suite")
}

// reserveLoopbackAddr binds an ephemeral loopback port, notes its address,
// then immediately releases it so run() can bind that exact address once
// its env vars are set — same rationale/pattern as
// cmd/osac-service-provider/main_integration_test.go's own helper of the
// same name.
func reserveLoopbackAddr() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	addr := ln.Addr().String()
	Expect(ln.Close()).To(Succeed())
	return addr
}

var _ = Describe("Mock provider binary (integration)", func() {
	// TC-I-031: a real osac.Bootstrap authenticates and probes against
	// the real mock binary over real listeners.
	It("lets a real osac.Bootstrap authenticate and probe successfully (TC-I-031)", func() {
		grpcAddr := reserveLoopbackAddr()
		oidcAddr := reserveLoopbackAddr()

		t := GinkgoT()
		t.Setenv("MOCK_GRPC_ADDRESS", grpcAddr)
		t.Setenv("MOCK_OIDC_ADDRESS", oidcAddr)

		ctx, cancel := context.WithCancel(context.Background())
		runDone := make(chan error, 1)
		go func() { runDone <- run(ctx, slog.New(slog.DiscardHandler)) }()
		defer func() {
			cancel()
			Eventually(runDone, "3s").Should(Receive())
		}()

		// Wait for the mock's own listeners to come up before pointing a
		// Bootstrap at them.
		Eventually(func() error {
			conn, err := net.DialTimeout("tcp", grpcAddr, 50*time.Millisecond)
			if err == nil {
				_ = conn.Close()
			}
			return err
		}, "2s", "10ms").Should(Succeed())
		Eventually(func() error {
			conn, err := net.DialTimeout("tcp", oidcAddr, 50*time.Millisecond)
			if err == nil {
				_ = conn.Close()
			}
			return err
		}, "2s", "10ms").Should(Succeed())

		osacCfg := &config.OSACConfig{ //nolint:gosec // OIDCClientSecret below is not a credential, a literal test fixture value never sent anywhere
			FulfillmentAddress: grpcAddr,
			OIDCIssuerURL:      "http://" + oidcAddr,
			OIDCClientID:       "osac-sp",
			OIDCClientSecret:   "unused-by-the-mock",
			TLSCertFile:        writeTestCACertFile(t),
			ProbeTimeout:       2 * time.Second,
		}
		bootstrap, err := osac.New(osacCfg, slog.New(slog.DiscardHandler))
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = bootstrap.Close() }()

		bootstrap.Start(ctx)

		Eventually(func() bool {
			return bootstrap.TokenStatus().Valid
		}, "2s", "20ms").Should(BeTrue())

		probe := bootstrap.Probe(ctx)
		Expect(probe.Err).NotTo(HaveOccurred())
		Expect(probe.Connected).To(BeTrue())
	})
})

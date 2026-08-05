package main

// Unit scope (per .ai/test-plans/osac-sp-unit.test-plan.md, section 8
// "cmd/osac-service-provider"): these cases call run/mainRun directly and
// in-process, each failing before reaching any real OSAC/Keycloak/
// control-plane collaborator — no fakes needed, unlike
// main_integration_test.go's full-stack happy-path suite.

import (
	"context"
	"log/slog"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// setValidEnv sets every required/commonly-used env var to a
// syntactically-valid placeholder value, using the given (already
// reserved-but-unbound) server address. Individual tests below override or
// omit specific vars to force one particular failure branch.
func setValidEnv(serverAddr string) {
	t := GinkgoT()
	t.Setenv("SP_SERVER_ADDRESS", serverAddr)
	t.Setenv("SP_SERVER_SHUTDOWN_TIMEOUT", "1s")
	t.Setenv("SP_OSAC_FULFILLMENT_ADDRESS", "127.0.0.1:1")
	t.Setenv("SP_OSAC_OIDC_ISSUER_URL", "https://keycloak.example.com/realms/osac")
	t.Setenv("SP_OSAC_OIDC_CLIENT_ID", "osac-sp")
	t.Setenv("SP_OSAC_OIDC_CLIENT_SECRET", "secret")
	t.Setenv("SP_OSAC_TLS_ENABLED", "false")
	t.Setenv("SP_OSAC_PROBE_TIMEOUT", "1s")
	t.Setenv("DCM_REGISTRATION_URL", "https://control-plane.example.com/api/v1alpha1")
	t.Setenv("DCM_NATS_URL", "nats://127.0.0.1:4222")
	t.Setenv("SP_ENDPOINT", "https://osac-sp.example.com")
	t.Setenv("SP_PROVIDER_CLUSTER_NAME", "osac-sp-cluster")
	t.Setenv("SP_PROVIDER_VM_NAME", "osac-sp-vm")
}

var _ = Describe("run's top-level error wrapping (unit)", func() {
	// TC-U-094: a config.Load failure is wrapped and returned, before any
	// listener is bound.
	It("wraps and returns a config-load failure (TC-U-094)", func() {
		t := GinkgoT()
		// Every required var except SP_ENDPOINT — deliberately left
		// unset so config.Load fails fast (REQ-XC-CFG-020).
		t.Setenv("SP_OSAC_FULFILLMENT_ADDRESS", "127.0.0.1:1")
		t.Setenv("SP_OSAC_OIDC_ISSUER_URL", "https://keycloak.example.com/realms/osac")
		t.Setenv("SP_OSAC_OIDC_CLIENT_ID", "osac-sp")
		t.Setenv("SP_OSAC_OIDC_CLIENT_SECRET", "secret")
		t.Setenv("DCM_REGISTRATION_URL", "https://control-plane.example.com/api/v1alpha1")

		err := run(context.Background(), slog.New(slog.DiscardHandler))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("initializing"))
	})

	// TC-U-095: a listener-bind failure (address already in use) is
	// wrapped and returned, before any OSAC/registration collaborator is
	// constructed.
	It("wraps and returns a listener-bind failure (TC-U-095)", func() {
		held, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = held.Close() }()

		setValidEnv(held.Addr().String()) // already bound by `held` above

		runErr := run(context.Background(), slog.New(slog.DiscardHandler))
		Expect(runErr).To(HaveOccurred())
		Expect(runErr.Error()).To(ContainSubstring("listening"))
	})

	// TC-U-096: an OSAC bootstrap construction failure (invalid TLS cert
	// file) is wrapped and returned, before registration starts.
	It("wraps and returns an OSAC bootstrap construction failure (TC-U-096)", func() {
		setValidEnv(reserveLoopbackAddr())
		t := GinkgoT()
		t.Setenv("SP_OSAC_TLS_ENABLED", "true")
		t.Setenv("SP_OSAC_TLS_CERT_FILE", "/nonexistent/path/ca.pem")

		runErr := run(context.Background(), slog.New(slog.DiscardHandler))
		Expect(runErr).To(HaveOccurred())
		Expect(runErr.Error()).To(ContainSubstring("creating OSAC client bootstrap"))
	})
})

var _ = Describe("mainRun (unit)", func() {
	// TC-U-097: mainRun maps a run failure to exit code 1, in-process,
	// without invoking os.Exit — proving the exit-code contract without
	// needing a subprocess harness (main()'s actual os.Exit call is a
	// documented coverage exception; see the test plan).
	It("returns exit code 1 when run fails (TC-U-097)", func() {
		// Same trigger as TC-U-094: leave SP_ENDPOINT unset.
		t := GinkgoT()
		t.Setenv("SP_OSAC_FULFILLMENT_ADDRESS", "127.0.0.1:1")
		t.Setenv("SP_OSAC_OIDC_ISSUER_URL", "https://keycloak.example.com/realms/osac")
		t.Setenv("SP_OSAC_OIDC_CLIENT_ID", "osac-sp")
		t.Setenv("SP_OSAC_OIDC_CLIENT_SECRET", "secret")
		t.Setenv("DCM_REGISTRATION_URL", "https://control-plane.example.com/api/v1alpha1")

		Expect(mainRun()).To(Equal(1))
	})
})

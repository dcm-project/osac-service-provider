package config_test

import (
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dcm-project/osac-service-provider/internal/config"
)

var allEnvVars = []string{
	"SP_SERVER_ADDRESS",
	"SP_SERVER_SHUTDOWN_TIMEOUT",
	"SP_SERVER_REQUEST_TIMEOUT",
	"SP_OSAC_FULFILLMENT_ADDRESS",
	"SP_OSAC_OIDC_ISSUER_URL",
	"SP_OSAC_OIDC_CLIENT_ID",
	"SP_OSAC_OIDC_CLIENT_SECRET",
	"SP_OSAC_TLS_ENABLED",
	"SP_OSAC_TLS_CERT_FILE",
	"SP_OSAC_PROBE_TIMEOUT",
	"DCM_REGISTRATION_URL",
	"SP_ENDPOINT",
	"SP_PROVIDER_CLUSTER_NAME",
	"SP_PROVIDER_VM_NAME",
	"SP_VERSION_MATRIX_PATH",
}

func clearEnv() {
	for _, v := range allEnvVars {
		_ = os.Unsetenv(v)
	}
}

// setRequiredEnv sets every required env var so Load() succeeds.
func setRequiredEnv() {
	_ = os.Setenv("SP_OSAC_FULFILLMENT_ADDRESS", "osac.example.com:443")
	_ = os.Setenv("SP_OSAC_OIDC_ISSUER_URL", "https://keycloak.example.com/token")
	_ = os.Setenv("SP_OSAC_OIDC_CLIENT_ID", "osac-sp")
	_ = os.Setenv("SP_OSAC_OIDC_CLIENT_SECRET", "s3cr3t")
	_ = os.Setenv("DCM_REGISTRATION_URL", "https://control-plane.example.com/api/v1alpha1")
	_ = os.Setenv("SP_ENDPOINT", "https://osac-sp.example.com")
}

var _ = Describe("Configuration", func() {
	BeforeEach(clearEnv)
	AfterEach(clearEnv)

	// TC-U-001: Loads all values from environment variables
	It("loads every value from environment variables (TC-U-001)", func() {
		setRequiredEnv()
		_ = os.Setenv("SP_SERVER_ADDRESS", ":9090")
		_ = os.Setenv("SP_SERVER_SHUTDOWN_TIMEOUT", "30s")
		_ = os.Setenv("SP_SERVER_REQUEST_TIMEOUT", "45s")
		_ = os.Setenv("SP_OSAC_TLS_ENABLED", "true")
		_ = os.Setenv("SP_OSAC_TLS_CERT_FILE", "/etc/osac/ca.pem")
		_ = os.Setenv("SP_OSAC_PROBE_TIMEOUT", "9s")
		_ = os.Setenv("SP_PROVIDER_CLUSTER_NAME", "osac-sp-cluster-custom")
		_ = os.Setenv("SP_PROVIDER_VM_NAME", "osac-sp-vm-custom")

		cfg, err := config.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).NotTo(BeNil())

		Expect(cfg.Server.Address).To(Equal(":9090"))
		Expect(cfg.Server.ShutdownTimeout).To(Equal(30 * time.Second))
		Expect(cfg.Server.RequestTimeout).To(Equal(45 * time.Second))

		Expect(cfg.OSAC.FulfillmentAddress).To(Equal("osac.example.com:443"))
		Expect(cfg.OSAC.OIDCIssuerURL).To(Equal("https://keycloak.example.com/token"))
		Expect(cfg.OSAC.OIDCClientID).To(Equal("osac-sp"))
		Expect(cfg.OSAC.OIDCClientSecret).To(Equal("s3cr3t"))
		Expect(cfg.OSAC.TLSEnabled).To(BeTrue())
		Expect(cfg.OSAC.TLSCertFile).To(Equal("/etc/osac/ca.pem"))
		Expect(cfg.OSAC.ProbeTimeout).To(Equal(9 * time.Second))

		Expect(cfg.DCM.RegistrationURL).To(Equal("https://control-plane.example.com/api/v1alpha1"))

		Expect(cfg.Provider.Endpoint).To(Equal("https://osac-sp.example.com"))
		Expect(cfg.Provider.ClusterName).To(Equal("osac-sp-cluster-custom"))
		Expect(cfg.Provider.VMName).To(Equal("osac-sp-vm-custom"))
	})

	// TC-U-540 (REQ-VERSION-090): SP_VERSION_MATRIX_PATH loads exactly
	// when set.
	It("loads SP_VERSION_MATRIX_PATH exactly when set (TC-U-540)", func() {
		setRequiredEnv()
		_ = os.Setenv("SP_VERSION_MATRIX_PATH", "/etc/osac-sp/version-matrix.json")

		cfg, err := config.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.VersionMatrix.Path).To(Equal("/etc/osac-sp/version-matrix.json"))
	})

	// TC-U-002: Applies documented defaults when optional vars are unset
	It("applies documented defaults when optional vars are unset (TC-U-002)", func() {
		setRequiredEnv()

		cfg, err := config.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).NotTo(BeNil())

		Expect(cfg.Server.Address).To(Equal(":8080"))
		Expect(cfg.Server.ShutdownTimeout).To(Equal(15 * time.Second))
		Expect(cfg.Server.RequestTimeout).To(Equal(30 * time.Second))
		Expect(cfg.OSAC.TLSEnabled).To(BeFalse())
		Expect(cfg.OSAC.ProbeTimeout).To(Equal(5 * time.Second))
		Expect(cfg.Provider.ClusterName).To(Equal("osac-sp-cluster"))
		Expect(cfg.Provider.VMName).To(Equal("osac-sp-vm"))
		Expect(cfg.VersionMatrix.Path).To(Equal(""))
	})

	// TC-U-541 (REQ-VERSION-090): SP_VERSION_MATRIX_PATH defaults to the
	// empty string when unset.
	It("defaults SP_VERSION_MATRIX_PATH to the empty string when unset (TC-U-541)", func() {
		setRequiredEnv()

		cfg, err := config.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.VersionMatrix.Path).To(Equal(""))
	})

	// TC-U-003: Fails fast when a required field is missing (table-driven)
	DescribeTable("fails fast when a required field is missing (TC-U-003)",
		func(missingVar string) {
			setRequiredEnv()
			_ = os.Unsetenv(missingVar)

			cfg, err := config.Load()
			Expect(err).To(HaveOccurred())
			Expect(cfg).To(BeNil())
			Expect(err.Error()).To(ContainSubstring(missingVar))
		},
		Entry("SP_OSAC_FULFILLMENT_ADDRESS", "SP_OSAC_FULFILLMENT_ADDRESS"),
		Entry("SP_OSAC_OIDC_ISSUER_URL", "SP_OSAC_OIDC_ISSUER_URL"),
		Entry("SP_OSAC_OIDC_CLIENT_ID", "SP_OSAC_OIDC_CLIENT_ID"),
		Entry("SP_OSAC_OIDC_CLIENT_SECRET", "SP_OSAC_OIDC_CLIENT_SECRET"),
		Entry("DCM_REGISTRATION_URL", "DCM_REGISTRATION_URL"),
		Entry("SP_ENDPOINT", "SP_ENDPOINT"),
	)

	// TC-U-004: Fails fast when a required field is empty string
	It("fails fast when a required field is an empty string (TC-U-004)", func() {
		setRequiredEnv()
		_ = os.Setenv("SP_ENDPOINT", "")

		cfg, err := config.Load()
		Expect(err).To(HaveOccurred())
		Expect(cfg).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("SP_ENDPOINT"))
	})
})

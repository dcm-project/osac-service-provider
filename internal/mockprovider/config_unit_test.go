package mockprovider_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dcm-project/osac-service-provider/internal/mockprovider"
)

var allConfigEnvVars = []string{"MOCK_GRPC_ADDRESS", "MOCK_OIDC_ADDRESS"}

func clearConfigEnv() {
	for _, v := range allConfigEnvVars {
		_ = os.Unsetenv(v)
	}
}

var _ = Describe("Config", func() {
	BeforeEach(clearConfigEnv)
	AfterEach(clearConfigEnv)

	// TC-U-142: loads both addresses from environment variables.
	It("loads both listen addresses from environment variables (TC-U-142)", func() {
		_ = os.Setenv("MOCK_GRPC_ADDRESS", "127.0.0.1:9090")
		_ = os.Setenv("MOCK_OIDC_ADDRESS", "127.0.0.1:9091")

		cfg, err := mockprovider.LoadConfig()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).NotTo(BeNil())
		Expect(cfg.GRPCAddress).To(Equal("127.0.0.1:9090"))
		Expect(cfg.OIDCAddress).To(Equal("127.0.0.1:9091"))
	})

	// TC-U-143: fails fast when a required field is missing (table-driven).
	DescribeTable("fails fast when a required field is missing (TC-U-143)",
		func(missingVar string) {
			_ = os.Setenv("MOCK_GRPC_ADDRESS", "127.0.0.1:9090")
			_ = os.Setenv("MOCK_OIDC_ADDRESS", "127.0.0.1:9091")
			_ = os.Unsetenv(missingVar)

			cfg, err := mockprovider.LoadConfig()
			Expect(err).To(HaveOccurred())
			Expect(cfg).To(BeNil())
			Expect(err.Error()).To(ContainSubstring(missingVar))
		},
		Entry("MOCK_GRPC_ADDRESS", "MOCK_GRPC_ADDRESS"),
		Entry("MOCK_OIDC_ADDRESS", "MOCK_OIDC_ADDRESS"),
	)
})

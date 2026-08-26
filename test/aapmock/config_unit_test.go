package aapmock_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dcm-project/osac-service-provider/test/aapmock"
)

var allConfigEnvVars = []string{"MOCK_AAP_ADDRESS"}

func clearConfigEnv() {
	for _, v := range allConfigEnvVars {
		_ = os.Unsetenv(v)
	}
}

var _ = Describe("Config", func() {
	BeforeEach(clearConfigEnv)
	AfterEach(clearConfigEnv)

	// TC-U-570: loads the listen address from the environment.
	It("loads the listen address from the environment (TC-U-570)", func() {
		_ = os.Setenv("MOCK_AAP_ADDRESS", "127.0.0.1:9092")

		cfg, err := aapmock.LoadConfig()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).NotTo(BeNil())
		Expect(cfg.Address).To(Equal("127.0.0.1:9092"))
	})

	// TC-U-571: fails fast when the required address is missing.
	It("fails fast when MOCK_AAP_ADDRESS is missing (TC-U-571)", func() {
		cfg, err := aapmock.LoadConfig()
		Expect(err).To(HaveOccurred())
		Expect(cfg).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("MOCK_AAP_ADDRESS"))
	})
})

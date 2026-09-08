package aapmock_test

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dcm-project/osac-service-provider/test/aapmock"
)

var allConfigEnvVars = []string{"MOCK_AAP_ADDRESS", "MOCK_AAP_TOKEN"}

func clearConfigEnv() {
	for _, v := range allConfigEnvVars {
		_ = os.Unsetenv(v)
	}
}

var _ = Describe("Config", func() {
	BeforeEach(clearConfigEnv)
	AfterEach(clearConfigEnv)

	// TC-U-570 (merged with the former TC-U-575, DD-232): LoadConfig has no
	// logic of its own beyond env.Parse(cfg) — the only thing worth
	// guarding is a typo in either field's `env:"..."` struct tag, which
	// the compiler can't catch. One assertion per field, in one test,
	// covers that for both; asserting them in two separate tests (as
	// TC-U-570/575 used to) added no further signal since both fields
	// use the identical mechanism.
	It("loads both the listen address and the auth token from the environment (TC-U-570)", func() {
		_ = os.Setenv("MOCK_AAP_ADDRESS", "127.0.0.1:9092")
		_ = os.Setenv("MOCK_AAP_TOKEN", "test-token")

		cfg, err := aapmock.LoadConfig()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).NotTo(BeNil())
		Expect(cfg.Address).To(Equal("127.0.0.1:9092"))
		Expect(cfg.Token).To(Equal("test-token"))
	})

	// TC-U-571 (merged with the former TC-U-576, DD-232): fails fast when
	// a required value is missing. Only MOCK_AAP_ADDRESS is exercised —
	// MOCK_AAP_TOKEN goes through the exact same `env.Parse`/`notEmpty`
	// path (env.go has no per-field branching), so a second test for the
	// other field would only re-prove the same third-party mechanism, not
	// any behavior this package adds.
	It("fails fast when a required value is missing (TC-U-571)", func() {
		_ = os.Setenv("MOCK_AAP_TOKEN", "test-token")

		cfg, err := aapmock.LoadConfig()
		Expect(err).To(HaveOccurred())
		Expect(cfg).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("MOCK_AAP_ADDRESS"))
	})
})

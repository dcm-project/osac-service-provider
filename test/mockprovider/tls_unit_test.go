package mockprovider_test

import (
	"crypto/tls"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dcm-project/osac-service-provider/test/mockprovider"
)

var _ = Describe("ServerTLSConfig", func() {
	// TC-U-157: parses the static test cert/key into a usable *tls.Config.
	It("parses the static test cert/key into a usable tls.Config (TC-U-157)", func() {
		tlsCfg, err := mockprovider.ServerTLSConfig()
		Expect(err).NotTo(HaveOccurred())
		Expect(tlsCfg.Certificates).To(HaveLen(1))

		expected, err := tls.X509KeyPair(mockprovider.CertPEM, mockprovider.KeyPEM)
		Expect(err).NotTo(HaveOccurred())
		Expect(tlsCfg.Certificates[0].Certificate).To(Equal(expected.Certificate))
	})
})

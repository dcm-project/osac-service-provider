package mockprovider_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	publicv1 "github.com/dcm-project/osac-service-provider/internal/osacpb/osac/public/v1"
	"github.com/dcm-project/osac-service-provider/test/mockprovider"
)

var _ = Describe("CapabilitiesServer", func() {
	// TC-U-134: Capabilities/Get always succeeds
	It("always succeeds on Get (TC-U-134)", func() {
		srv := mockprovider.NewCapabilitiesServer()
		_, err := srv.Get(context.Background(), &publicv1.CapabilitiesGetRequest{})
		Expect(err).NotTo(HaveOccurred())
	})
})

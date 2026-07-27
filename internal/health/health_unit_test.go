package health_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/dcm-project/osac-service-provider/api/v1alpha1"
	oapigen "github.com/dcm-project/osac-service-provider/internal/api/server"
	"github.com/dcm-project/osac-service-provider/internal/health"
	"github.com/dcm-project/osac-service-provider/internal/osac"
)

// fakeOSACStatus is a hand-rolled fake satisfying health.OSACStatus.
type fakeOSACStatus struct {
	tokenStatus osac.TokenStatus
	probeResult osac.ProbeResult
}

func (f *fakeOSACStatus) TokenStatus() osac.TokenStatus            { return f.tokenStatus }
func (f *fakeOSACStatus) Probe(_ context.Context) osac.ProbeResult { return f.probeResult }

var _ = Describe("Health handler", func() {
	var startTime time.Time

	BeforeEach(func() {
		startTime = time.Now().Add(-90 * time.Second)
	})

	getHealth := func(fake *fakeOSACStatus) v1alpha1.Health {
		h := health.NewHandler(fake, startTime, "1.2.3")
		resp, err := h.GetHealth(context.Background(), oapigen.GetHealthRequestObject{})
		Expect(err).NotTo(HaveOccurred())
		jsonResp, ok := resp.(oapigen.GetHealth200JSONResponse)
		Expect(ok).To(BeTrue(), "expected a 200 JSON response")
		return v1alpha1.Health(jsonResp)
	}

	// TC-U-030: healthy when token valid and OSAC connected
	It("reports healthy when the token is valid and OSAC is reachable (TC-U-030)", func() {
		resp := getHealth(&fakeOSACStatus{
			tokenStatus: osac.TokenStatus{Valid: true},
			probeResult: osac.ProbeResult{Connected: true},
		})
		Expect(resp.Status).To(Equal(v1alpha1.Healthy))
		Expect(resp.Detail).To(BeNil())
	})

	// TC-U-031: unhealthy when token invalid, connected
	It("reports unhealthy with a token-only detail when the token is invalid (TC-U-031)", func() {
		resp := getHealth(&fakeOSACStatus{
			tokenStatus: osac.TokenStatus{Valid: false},
			probeResult: osac.ProbeResult{Connected: true},
		})
		Expect(resp.Status).To(Equal(v1alpha1.Unhealthy))
		Expect(*resp.Detail).To(Equal("OIDC token invalid"))
	})

	// TC-U-032: unhealthy when disconnected, token valid
	It("reports unhealthy with a connectivity-only detail when OSAC is unreachable (TC-U-032)", func() {
		resp := getHealth(&fakeOSACStatus{
			tokenStatus: osac.TokenStatus{Valid: true},
			probeResult: osac.ProbeResult{Connected: false},
		})
		Expect(resp.Status).To(Equal(v1alpha1.Unhealthy))
		Expect(*resp.Detail).To(Equal("OSAC fulfillment service unreachable"))
	})

	// TC-U-033: unhealthy when both token invalid and disconnected
	It("reports both causes when both the token is invalid and OSAC is unreachable (TC-U-033)", func() {
		resp := getHealth(&fakeOSACStatus{
			tokenStatus: osac.TokenStatus{Valid: false},
			probeResult: osac.ProbeResult{Connected: false},
		})
		Expect(resp.Status).To(Equal(v1alpha1.Unhealthy))
		Expect(*resp.Detail).To(Equal("OIDC token invalid; OSAC fulfillment service unreachable"))
	})

	// TC-U-034/035/036: fixed response fields
	It("always sets type, path, version, and uptime (TC-U-034/035/036)", func() {
		resp := getHealth(&fakeOSACStatus{
			tokenStatus: osac.TokenStatus{Valid: true},
			probeResult: osac.ProbeResult{Connected: true},
		})
		Expect(*resp.Type).To(Equal("osac-service-provider.dcm.io/health"))
		Expect(*resp.Path).To(Equal("health"))
		Expect(*resp.Version).To(Equal("1.2.3"))
		Expect(*resp.Uptime).To(BeNumerically(">=", 90))
	})
})

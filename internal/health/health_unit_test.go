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

// fakeOSACStatus is a hand-rolled fake satisfying health.OSACStatus. It
// counts calls to TokenStatus and Probe so tests can assert the handler
// reads cached state exactly once per check (TC-U-035/036) rather than
// triggering extra work (e.g. a forced token refresh).
type fakeOSACStatus struct {
	tokenStatus osac.TokenStatus
	probeResult osac.ProbeResult

	tokenStatusCalls int
	probeCalls       int
}

func (f *fakeOSACStatus) TokenStatus() osac.TokenStatus {
	f.tokenStatusCalls++
	return f.tokenStatus
}

func (f *fakeOSACStatus) Probe(_ context.Context) osac.ProbeResult {
	f.probeCalls++
	return f.probeResult
}

var _ = Describe("Health handler", func() {
	var startTime time.Time

	BeforeEach(func() {
		startTime = time.Now().Add(-90 * time.Second)
	})

	getHealth := func(fake *fakeOSACStatus) v1alpha1.Health {
		h := health.NewHandler(fake, startTime, "1.2.3")
		resp, err := h.GetClustersHealth(context.Background(), oapigen.GetClustersHealthRequestObject{})
		Expect(err).NotTo(HaveOccurred())
		jsonResp, ok := resp.(oapigen.GetClustersHealth200JSONResponse)
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

	// TC-U-037: the unhealthy detail specifically names the token cause
	// (case-insensitively containing "token") and does not misattribute the
	// failure to connectivity, per REQ-HLT-070. Same scenario as TC-U-031
	// (which asserts the overall exact detail string); this case asserts
	// the AC-HLT-060-specific "names the right cause, not the wrong one"
	// property independently of the exact wording.
	It("names the token cause, not connectivity, in the unhealthy detail (TC-U-037)", func() {
		resp := getHealth(&fakeOSACStatus{
			tokenStatus: osac.TokenStatus{Valid: false},
			probeResult: osac.ProbeResult{Connected: true},
		})
		Expect(*resp.Detail).To(MatchRegexp("(?i)token"))
		Expect(*resp.Detail).NotTo(MatchRegexp("(?i)osac|connect"))
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

	// TC-U-038: the unhealthy detail specifically names the connectivity
	// cause (case-insensitively containing "osac"/"connect") and does not
	// misattribute the failure to the token, per REQ-HLT-070. Same scenario
	// as TC-U-032 (which asserts the overall exact detail string); this
	// case asserts the AC-HLT-070-specific "names the right cause, not the
	// wrong one" property independently of the exact wording.
	It("names the connectivity cause, not the token, in the unhealthy detail (TC-U-038)", func() {
		resp := getHealth(&fakeOSACStatus{
			tokenStatus: osac.TokenStatus{Valid: true},
			probeResult: osac.ProbeResult{Connected: false},
		})
		Expect(*resp.Detail).To(MatchRegexp("(?i)osac|connect"))
		Expect(*resp.Detail).NotTo(MatchRegexp("(?i)token"))
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

	// TC-U-034: fixed response fields
	It("always sets type, path, version, and uptime (TC-U-034)", func() {
		resp := getHealth(&fakeOSACStatus{
			tokenStatus: osac.TokenStatus{Valid: true},
			probeResult: osac.ProbeResult{Connected: true},
		})
		Expect(*resp.Type).To(Equal("osac-service-provider.dcm.io/health"))
		Expect(*resp.Path).To(Equal("health"))
		Expect(*resp.Version).To(Equal("1.2.3"))
		Expect(*resp.Uptime).To(BeNumerically(">=", 90))
	})

	// TC-U-035: the health check reads the OIDC token status from
	// osac.Bootstrap's cache (TokenStatus(), a synchronous getter) exactly
	// once per call, per REQ-HLT-050 — it never forces a fresh token fetch
	// as a side effect of a health check (that would let an unbounded
	// stream of health polls hammer the OIDC provider).
	It("reads cached token status without forcing an extra fetch (TC-U-035)", func() {
		fake := &fakeOSACStatus{
			tokenStatus: osac.TokenStatus{Valid: true},
			probeResult: osac.ProbeResult{Connected: true},
		}
		getHealth(fake)
		Expect(fake.tokenStatusCalls).To(Equal(1))
	})

	// TC-U-036: Probe is invoked exactly once per health call — not zero
	// (health must actually check connectivity, not assume it) and not
	// more than once (no duplicate/backup probes per REQ-HLT-060).
	It("invokes Probe exactly once per health call (TC-U-036)", func() {
		fake := &fakeOSACStatus{
			tokenStatus: osac.TokenStatus{Valid: true},
			probeResult: osac.ProbeResult{Connected: true},
		}
		getHealth(fake)
		Expect(fake.probeCalls).To(Equal(1))
	})

	// TC-U-039: both StrictServerInterface entry points (clusters/vms)
	// report identical status, per REQ-HLT-015/DD-010.
	It("reports identical status from GetClustersHealth and GetVMsHealth (TC-U-039)", func() {
		fake := &fakeOSACStatus{
			tokenStatus: osac.TokenStatus{Valid: false},
			probeResult: osac.ProbeResult{Connected: true},
		}
		h := health.NewHandler(fake, startTime, "1.2.3")

		clustersResp, err := h.GetClustersHealth(context.Background(), oapigen.GetClustersHealthRequestObject{})
		Expect(err).NotTo(HaveOccurred())
		clustersJSON, ok := clustersResp.(oapigen.GetClustersHealth200JSONResponse)
		Expect(ok).To(BeTrue())

		vmsResp, err := h.GetVMsHealth(context.Background(), oapigen.GetVMsHealthRequestObject{})
		Expect(err).NotTo(HaveOccurred())
		vmsJSON, ok := vmsResp.(oapigen.GetVMsHealth200JSONResponse)
		Expect(ok).To(BeTrue())

		Expect(clustersJSON.Status).To(Equal(vmsJSON.Status))
		Expect(clustersJSON.Detail).To(Equal(vmsJSON.Detail))
	})
})

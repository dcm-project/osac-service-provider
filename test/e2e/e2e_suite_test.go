// Package e2e_test implements the common kind-based e2e suite for
// osac-service-provider#17 / FLPATH-4759. The Phase A workflow runs real
// control-plane + osac-sp + osac-mock-provider; the Tier B workflow runs the
// same suite against real fulfillment-service infrastructure and
// environment-agent.
//
// This is a separate Go module (REQ-E2E-080) so the environment-agent REST
// client never enters the main module's go.mod/go.sum. See
// .ai/specs/osac-sp-e2e-suite.spec.md and
// .ai/test-plans/osac-sp-e2e-suite.test-plan.md for the requirements and
// TC-E2E-* cases this package implements.
package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Env vars set by the workflow's "Run e2e suite" step, pointing at the
// kubectl port-forwards it started.
const (
	envOSACSPURL = "OSAC_SP_URL"
)

var (
	osacSPURL string
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "osac-sp e2e suite (kind)")
}

var _ = BeforeSuite(func() {
	osacSPURL = os.Getenv(envOSACSPURL)
	Expect(osacSPURL).NotTo(BeEmpty(), "%s must be set (see the e2e workflow)", envOSACSPURL)

	// TC-E2E-010: the workflow's own osac-sp readiness step
	// (kubectl wait --for=condition=Available) already
	// gates this suite from running before pods are Ready. This is a
	// defensive second check — with a clear, per-target failure message —
	// for the case where this suite is invoked standalone (e.g. locally
	// against an already-running cluster) without that step.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	waitUntilReachable(ctx, "osac-sp", fmt.Sprintf("%s/api/v1alpha1/clusters/health", osacSPURL))
})

func waitUntilReachable(ctx context.Context, name, url string) {
	client := &http.Client{Timeout: 5 * time.Second}
	start := time.Now()
	var lastErr error
	attempts := 0
	for {
		attempts++
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err == nil {
			resp, doErr := client.Do(req)
			if doErr == nil {
				_ = resp.Body.Close()
				return
			}
			lastErr = doErr
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			Fail(fmt.Sprintf("%s (%s) never became reachable within the bounded wait: %v (%d attempts over %s)",
				name, url, lastErr, attempts, time.Since(start).Round(time.Millisecond)))
		case <-time.After(500 * time.Millisecond):
		}
	}
}

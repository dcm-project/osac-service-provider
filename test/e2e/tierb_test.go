// Tier B specs (osac-sp-e2e-tier-b.spec.md, Phase 1): run only when
// .github/workflows/e2e-tierb.yaml's env vars are present. Phase A's
// e2e.yaml never sets them, so these Describe blocks Skip() there instead
// of failing — this file compiles into the same test/e2e binary Phase A
// uses (test plan's "Tier B is a variant of that same suite" framework
// note), it just self-selects at runtime.
//
// TC-TB-030 (osac-sp health against the real backend) deliberately has no
// dedicated spec here: health_test.go's existing
// "osac-sp health, against the real backend" Describe block already
// asserts exactly that shape (healthy status, empty Detail) against
// whatever OSAC_SP_URL points at — Tier B's workflow points it at the
// real ffs-keycloak/ffs-fulfillment-service stack, closing DD-132's
// auth-fidelity gap for free, with no new assertion code needed. As of
// DD-212 (#28), that Describe block is Label("tier-b-only") and runs only
// here, not in Phase A's e2e.yaml.
package e2e_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// insecureHTTPClient skips TLS verification for the direct Keycloak
// token-endpoint call in fetchTokenClaims. Acceptable here specifically
// because TC-TB-020 already deliberately skips JWT *signature*
// verification too (test plan: "no signature verification needed for this
// assertion") — this only asserts claim shape, never anything relying on
// the connection's authenticity. osac-sp itself (the thing whose real
// trust behavior matters) never uses this client; see
// tierb-config/README.md and DD-151 for how it validates the real
// osac-ca-issued cert instead.
var insecureHTTPClient = &http.Client{
	Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // test-only, see comment above
}

// Env vars set only by .github/workflows/e2e-tierb.yaml.
const (
	envKeycloakURL      = "KEYCLOAK_URL"       // e.g. http://localhost:18082/realms/osac
	envTierBAdminSecret = "TIERB_ADMIN_SECRET" // osac-admin's client secret (tierb-config/realm.json)
	envBadAuthOSACSPURL = "BAD_AUTH_OSAC_SP_URL"
	// envPhase2Enabled gates Phase 2 specs (osac-operator/BMFO/osac-aap-mock,
	// REQ-TB-070..100) — set only once .github/workflows/e2e-tierb.yaml
	// deploys that stack, distinct from Phase 1's envKeycloakURL gate.
	envPhase2Enabled = "TIERB_PHASE2_ENABLED"
)

var _ = Describe("Tier B: real Keycloak issues correctly-claimed tokens", func() {
	// TC-TB-020 / REQ-TB-020
	It("issues a client_credentials token for osac-admin carrying username and osac-api audience claims", func() {
		keycloakURL := os.Getenv(envKeycloakURL)
		if keycloakURL == "" {
			Skip("not a Tier B run: " + envKeycloakURL + " is unset")
		}
		adminSecret := os.Getenv(envTierBAdminSecret)
		Expect(adminSecret).NotTo(BeEmpty(), "%s must be set alongside %s", envTierBAdminSecret, envKeycloakURL)

		claims := fetchTokenClaims(keycloakURL, "osac-admin", adminSecret)

		// Per DD-150: real OSAC checks `username`/`groups`, not
		// `organization`/`realm_access.roles` as an earlier draft of this
		// spec assumed. `groups` is deliberately NOT asserted here: a
		// live-cluster spike (DD-150 addendum) confirmed Keycloak's
		// oidc-group-membership-mapper omits the claim entirely for a
		// service account with no group memberships — which is also true
		// of INSTALL.md's own reference realm (`service-account-osac-admin`
		// is never assigned to any group there either), so this is
		// expected/faithful behavior, not a vendoring bug. `username` is
		// the one claim INSTALL.md's own verification steps check.
		Expect(claims["username"]).To(Equal("service-account-osac-admin"))
		Expect(claims["aud"]).To(
			Or(Equal("osac-api"), ContainElement("osac-api")),
			"osac-api must be present as an audience claim (oidc-audience-mapper)",
		)
	})
})

var _ = Describe("Tier B: a real auth failure is genuinely detectable", func() {
	// TC-TB-050 / REQ-TB-060 / AC-TB-020 — opt-in workflow_dispatch variant
	// only (e2e-tierb.yaml); BAD_AUTH_OSAC_SP_URL is unset on every regular
	// PR run.
	It("reports unhealthy with a token/connectivity detail when the client secret is wrong", func() {
		badAuthURL := os.Getenv(envBadAuthOSACSPURL)
		if badAuthURL == "" {
			Skip("opt-in variant only: " + envBadAuthOSACSPURL + " is unset")
		}

		var h health
		Eventually(func() string {
			h = getHealthAt(badAuthURL, "/api/v1alpha1/clusters/health")
			return h.Status
		}, 30*time.Second, 500*time.Millisecond).Should(Equal("unhealthy"))

		Expect(h.Detail).NotTo(BeEmpty())
		Expect(h.Detail).To(ContainSubstring("OIDC token invalid"),
			"a wrong client secret must surface as a token-fetch failure, not an opaque connectivity error")
	})
})

// clusterOrderName/clusterOrderFixture match
// manifests-tierb/clusterorder-fixture.yaml's own metadata.name/apply path.
const (
	clusterOrderName    = "tierb-cluster-order"
	clusterOrderFixture = "manifests-tierb/clusterorder-fixture.yaml"
)

// clusterOrderStatus mirrors just the fields of osac-operator's real
// ClusterOrderStatus (api/v1alpha1/clusterorder_types.go) this suite
// asserts on or reports in failure messages.
type clusterOrderStatus struct {
	Phase      string `json:"phase"`
	Conditions []struct {
		Type    string `json:"type"`
		Status  string `json:"status"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
	} `json:"conditions"`
	ProvisioningJobs []map[string]any `json:"provisioningJobs"`
}

// phase2CRDs are all 8 vendored CRDs (manifests-tierb/crds/) TC-TB-060
// asserts exist before any Phase 2 reconciliation is exercised — the
// original 4 plus BareMetalPool/ComputeInstance/NodePool/BareMetalHost,
// added once osac-operator/BMFO startup broke without them (DD-220/222).
var phase2CRDs = []string{
	"clusterorders.osac.openshift.io",
	"hostedclusters.hypershift.openshift.io",
	"tenants.osac.openshift.io",
	"baremetalinstances.osac.openshift.io",
	"baremetalpools.osac.openshift.io",
	"computeinstances.osac.openshift.io",
	"nodepools.hypershift.openshift.io",
	"baremetalhosts.metal3.io",
}

var _ = Describe("Tier B Phase 2: infra is up before any reconciliation is exercised", func() {
	BeforeEach(func() {
		if os.Getenv(envPhase2Enabled) == "" {
			Skip("not a Tier B Phase 2 run: " + envPhase2Enabled + " is unset")
		}
	})

	// TC-TB-060 / REQ-TB-070 / AC-TB-030 (given clause). The workflow's own
	// `kubectl rollout status`/`helm install` steps already fail the job
	// before this suite even starts if any of this isn't true — this
	// re-asserts it here too so the fact is visible as a named, traceable
	// spec result rather than only as an opaque earlier CI step.
	It("has osac-operator, BMFO, and osac-aap-mock all Ready, and all 8 vendored CRDs registered", func() {
		for _, dep := range []string{"osac-operator", "bmf-operator-controller-manager", "osac-aap-mock"} {
			Expect(deploymentReady(dep)).To(BeTrue(), "deployment/%s is not Ready", dep)
		}
		for _, crd := range phase2CRDs {
			out, err := exec.Command("kubectl", "get", "crd", crd).CombinedOutput() //nolint:gosec // fixed allowlist, not user input
			Expect(err).NotTo(HaveOccurred(), "CRD %q not registered: %s", crd, out)
		}
	})

	// TC-TB-100 / REQ-TB-070. Deliberately thin (DD-216, #46): proves the
	// chart/RBAC/CRD install is sound on its own, without claiming any
	// BareMetalInstance reconciliation fidelity — nothing in this suite
	// ever creates one.
	It("keeps BMFO healthy with zero BareMetalInstance CRs present (deploy-only regression check)", func() {
		Expect(deploymentReady("bmf-operator-controller-manager")).To(BeTrue(), "deployment/bmf-operator-controller-manager is not Ready")

		out, err := exec.Command("kubectl", "get", "baremetalinstances.osac.openshift.io", "-A", "-o", "name").CombinedOutput() //nolint:gosec // fixed args, not user input
		Expect(err).NotTo(HaveOccurred(), "kubectl get baremetalinstances failed: %s", out)
		Expect(strings.TrimSpace(string(out))).To(BeEmpty(), "expected zero BareMetalInstance CRs, found: %s", out)
	})
})

// deploymentReady reports whether the named Deployment (in the current
// kubectl context's default namespace) has all replicas Available.
func deploymentReady(name string) bool {
	out, err := exec.Command("kubectl", "get", "deployment", name, //nolint:gosec // fixed allowlist, not user input
		"-o", "jsonpath={.status.conditions[?(@.type=='Available')].status}").CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "kubectl get deployment %s failed: %s", name, out)
	return strings.TrimSpace(string(out)) == "True"
}

var _ = Describe("Tier B Phase 2: a real ClusterOrder reaches a real terminal state", func() {
	// TC-TB-080/090 / REQ-TB-070, REQ-TB-080, REQ-TB-100 / AC-TB-030
	// (ClusterOrder-only, direct-CR-create scope this landing — DD-216,
	// DD-218).
	It("drives a directly-created ClusterOrder to Ready via real osac-operator + osac-aap-mock", func() {
		if os.Getenv(envPhase2Enabled) == "" {
			Skip("not a Tier B Phase 2 run: " + envPhase2Enabled + " is unset")
		}

		// TC-TB-080: create the fixture directly against the cluster's
		// own API server.
		applyOut, err := exec.Command("kubectl", "apply", "-f", clusterOrderFixture).CombinedOutput() //nolint:gosec // fixed, repo-local path, not user input
		Expect(err).NotTo(HaveOccurred(), "kubectl apply -f %s failed: %s", clusterOrderFixture, applyOut)

		// Confirm osac-operator's ClusterOrderReconciler has picked the
		// object up (a non-empty .status.phase appears) before starting
		// the longer terminal-state wait below — isolates "never
		// reconciled at all" failures from "reconciled but never reached
		// Ready" ones for easier CI triage.
		var status clusterOrderStatus
		Eventually(func() string {
			status = getClusterOrderStatus()
			return status.Phase
		}, 60*time.Second, 2*time.Second).ShouldNot(BeEmpty(),
			"osac-operator never set .status.phase — reconciler may not be running or the CRD wasn't accepted")

		// TC-TB-090: the actual Phase 2 deliverable — real osac-operator
		// reconciliation + osac-aap-mock drive the CR to Ready. osac-aap-mock
		// reports jobs "successful" on the very first poll (DD-214), and
		// osac-operator's own status-poll interval defaults to 30s
		// (pkg/provisioning.DefaultStatusPollInterval) — 3 minutes is
		// several poll cycles of headroom, not a realistic expected runtime,
		// chosen to stay well inside NFR-TB-010's whole-job 25-minute budget
		// even on a full timeout.
		Eventually(func() string {
			status = getClusterOrderStatus()
			return status.Phase
		}, 3*time.Minute, 5*time.Second).Should(Equal("Ready"),
			"ClusterOrder %q never reached Ready; last observed status: %+v", clusterOrderName, status)
	})
})

// getClusterOrderStatus shells out to kubectl to fetch the ClusterOrder
// fixture's current .status — this suite has no Kubernetes client-go
// dependency (REQ-E2E-080 keeps this module's own go.mod minimal), and the
// CI runner already has kubectl configured against the kind cluster for
// every other step in .github/workflows/e2e-tierb.yaml.
func getClusterOrderStatus() clusterOrderStatus {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("kubectl", "get", "clusterorder", clusterOrderName, "-o", "jsonpath={.status}") //nolint:gosec // fixed args, not user input
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		Fail(fmt.Sprintf("kubectl get clusterorder %s failed: %v: %s", clusterOrderName, err, stderr.String()))
	}

	raw := strings.TrimSpace(stdout.String())
	if raw == "" {
		return clusterOrderStatus{}
	}

	var status clusterOrderStatus
	Expect(json.Unmarshal([]byte(raw), &status)).To(Succeed(), "unparseable ClusterOrder status: %s", raw)
	return status
}

// fetchTokenClaims performs a real client_credentials grant directly
// against Keycloak's token endpoint (independent of osac-sp, per the test
// plan's "verified directly against Keycloak... before involving osac-sp"
// note) and returns the decoded JWT access-token payload. Signature
// verification is deliberately skipped — this only asserts claim shape,
// not cryptographic validity, matching TC-TB-020's stated scope.
func fetchTokenClaims(issuerURL, clientID, clientSecret string) map[string]any {
	tokenURL := strings.TrimSuffix(issuerURL, "/") + "/protocol/openid-connect/token"

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := insecureHTTPClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	Expect(resp.StatusCode).To(Equal(http.StatusOK), "token endpoint: %s", tokenURL)

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	Expect(json.NewDecoder(resp.Body).Decode(&tokenResp)).To(Succeed())
	Expect(tokenResp.AccessToken).NotTo(BeEmpty())

	return decodeJWTPayload(tokenResp.AccessToken)
}

func decodeJWTPayload(token string) map[string]any {
	parts := strings.Split(token, ".")
	Expect(parts).To(HaveLen(3), "not a JWT: %s", token)

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	Expect(err).NotTo(HaveOccurred())

	var claims map[string]any
	Expect(json.Unmarshal(payload, &claims)).To(Succeed())
	return claims
}

// getHealthAt is health_test.go's getHealth, generalized to an arbitrary
// base URL (osacSPURL for the regular instance vs. the opt-in bad-auth
// instance's own Service).
func getHealthAt(baseURL, path string) health {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s%s", baseURL, path), nil)
	Expect(err).NotTo(HaveOccurred())

	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()

	var h health
	Expect(json.NewDecoder(resp.Body).Decode(&h)).To(Succeed())
	return h
}

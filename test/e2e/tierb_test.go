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
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
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

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

// bareMetalHost{Unset,Always}Name/Fixture and bareMetalInstance{Unset,Always}Name/Fixture
// match manifests-tierb/baremetalhost-fixture-{unset,always}.yaml and
// manifests-tierb/baremetalinstance-fixture-{unset,always}.yaml's own
// metadata.name/apply path.
const (
	bareMetalHostUnsetName        = "tierb-bmh-unset"
	bareMetalHostUnsetFixture     = "manifests-tierb/baremetalhost-fixture-unset.yaml"
	bareMetalInstanceUnsetName    = "tierb-bmi-unset"
	bareMetalInstanceUnsetFixture = "manifests-tierb/baremetalinstance-fixture-unset.yaml"

	bareMetalHostAlwaysName        = "tierb-bmh-always"
	bareMetalHostAlwaysFixture     = "manifests-tierb/baremetalhost-fixture-always.yaml"
	bareMetalInstanceAlwaysName    = "tierb-bmi-always"
	bareMetalInstanceAlwaysFixture = "manifests-tierb/baremetalinstance-fixture-always.yaml"
)

// Fixture name/path constants for AC-TB-050/TC-TB-130..160's fail-safe and
// release paths (DD-229) — same match manifests-tierb/*.yaml apply-path
// convention as the pairs above.
const (
	bareMetalInstanceNoHostName    = "tierb-bmi-nohost"
	bareMetalInstanceNoHostFixture = "manifests-tierb/baremetalinstance-fixture-nohost.yaml"

	bareMetalHostIneligibleName        = "tierb-bmh-ineligible"
	bareMetalHostIneligibleFixture     = "manifests-tierb/baremetalhost-fixture-ineligible.yaml"
	bareMetalInstanceIneligibleName    = "tierb-bmi-ineligible"
	bareMetalInstanceIneligibleFixture = "manifests-tierb/baremetalinstance-fixture-ineligible.yaml"

	bareMetalHostContendedName         = "tierb-bmh-contended"
	bareMetalHostContendedFixture      = "manifests-tierb/baremetalhost-fixture-contended.yaml"
	bareMetalInstanceContendedAName    = "tierb-bmi-contended-a"
	bareMetalInstanceContendedAFixture = "manifests-tierb/baremetalinstance-fixture-contended-a.yaml"
	bareMetalInstanceContendedBName    = "tierb-bmi-contended-b"
	bareMetalInstanceContendedBFixture = "manifests-tierb/baremetalinstance-fixture-contended-b.yaml"

	bareMetalHostCleanupName        = "tierb-bmh-cleanup"
	bareMetalHostCleanupFixture     = "manifests-tierb/baremetalhost-fixture-cleanup.yaml"
	bareMetalInstanceCleanupName    = "tierb-bmi-cleanup"
	bareMetalInstanceCleanupFixture = "manifests-tierb/baremetalinstance-fixture-cleanup.yaml"
)

// bareMetalInstanceStatus mirrors just the fields of BMFO's real
// BareMetalInstanceStatus (api/v1alpha1/baremetalinstance_types.go) this
// suite asserts on or reports in failure messages.
type bareMetalInstanceStatus struct {
	Phase      string `json:"phase"`
	Conditions []struct {
		Type    string `json:"type"`
		Status  string `json:"status"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
	} `json:"conditions"`
}

var _ = Describe("Tier B Phase 2: a real BareMetalInstance reaches a real terminal state", func() {
	BeforeEach(func() {
		if os.Getenv(envPhase2Enabled) == "" {
			Skip("not a Tier B Phase 2 run: " + envPhase2Enabled + " is unset")
		}
	})

	// TC-TB-110 / REQ-TB-110 / AC-TB-040 (runStrategy unset variant). No
	// real Metal3/Ironic/virtual-BMC infrastructure involved (DD-226/227) —
	// a static BareMetalHost fixture, patched once to simulate completed
	// Metal3 inspection, is sufficient for BMFO's real, unmodified
	// reconciler to allocate it and reach Ready.
	It("drives a BareMetalInstance with runStrategy unset to Ready via real BMFO, with no power-management steps", func() {
		applyOut, err := exec.Command("kubectl", "apply", "-f", bareMetalHostUnsetFixture).CombinedOutput() //nolint:gosec // fixed, repo-local path, not user input
		Expect(err).NotTo(HaveOccurred(), "kubectl apply -f %s failed: %s", bareMetalHostUnsetFixture, applyOut)
		patchBareMetalHostInitialStatus(bareMetalHostUnsetName, "aa:bb:cc:dd:ee:01")

		applyOut, err = exec.Command("kubectl", "apply", "-f", bareMetalInstanceUnsetFixture).CombinedOutput() //nolint:gosec // fixed, repo-local path, not user input
		Expect(err).NotTo(HaveOccurred(), "kubectl apply -f %s failed: %s", bareMetalInstanceUnsetFixture, applyOut)

		var status bareMetalInstanceStatus
		Eventually(func() string {
			status = getBareMetalInstanceStatus(bareMetalInstanceUnsetName)
			return status.Phase
		}, 2*time.Minute, 5*time.Second).Should(Equal("Ready"),
			"BareMetalInstance %q never reached Ready; last observed status: %+v", bareMetalInstanceUnsetName, status)
	})

	// TC-TB-120 / REQ-TB-110 / AC-TB-040 (runStrategy: Always variant).
	// Exercises the power-synced condition path (reconcilePower/
	// SetPowerState) that the unset variant above never touches: BMFO
	// itself only ever patches spec.online, never status.poweredOn
	// (DD-226), so the instance genuinely cannot reach Ready without a
	// fake-BMO step simulating a real baremetal-operator's completed
	// power-on.
	It("drives a BareMetalInstance with runStrategy Always to Ready only after a fake-BMO power-on patch", func() {
		applyOut, err := exec.Command("kubectl", "apply", "-f", bareMetalHostAlwaysFixture).CombinedOutput() //nolint:gosec // fixed, repo-local path, not user input
		Expect(err).NotTo(HaveOccurred(), "kubectl apply -f %s failed: %s", bareMetalHostAlwaysFixture, applyOut)
		patchBareMetalHostInitialStatus(bareMetalHostAlwaysName, "aa:bb:cc:dd:ee:02")

		applyOut, err = exec.Command("kubectl", "apply", "-f", bareMetalInstanceAlwaysFixture).CombinedOutput() //nolint:gosec // fixed, repo-local path, not user input
		Expect(err).NotTo(HaveOccurred(), "kubectl apply -f %s failed: %s", bareMetalInstanceAlwaysFixture, applyOut)

		// Assert it's genuinely blocked on power sync *before* the
		// fake-BMO patch below — proves PowerSynced actually gates Ready,
		// not a vestigial/never-blocking condition that would let a
		// broken reconciler report Ready anyway (mirrors AC-TB-020's "real
		// failure/blocking paths must be genuinely detectable" spirit).
		var status bareMetalInstanceStatus
		Eventually(func() string {
			status = getBareMetalInstanceStatus(bareMetalInstanceAlwaysName)
			return status.Phase
		}, 90*time.Second, 3*time.Second).Should(Equal("Progressing"),
			"BareMetalInstance %q should be Progressing (blocked on power sync) before the fake-BMO patch; last observed status: %+v", bareMetalInstanceAlwaysName, status)

		patchBareMetalHostPoweredOn(bareMetalHostAlwaysName)

		Eventually(func() string {
			status = getBareMetalInstanceStatus(bareMetalInstanceAlwaysName)
			return status.Phase
		}, 90*time.Second, 3*time.Second).Should(Equal("Ready"),
			"BareMetalInstance %q never reached Ready after the fake-BMO patch; last observed status: %+v", bareMetalInstanceAlwaysName, status)
	})
})

var _ = Describe("Tier B Phase 2: BareMetalInstance allocation fails safe, and releases its host on deletion", func() {
	BeforeEach(func() {
		if os.Getenv(envPhase2Enabled) == "" {
			Skip("not a Tier B Phase 2 run: " + envPhase2Enabled + " is unset")
		}
	})

	// TC-TB-130 / REQ-TB-120 / AC-TB-050: a hostType with zero matching
	// BareMetalHost fixtures must converge to a real terminal Failed phase
	// with the exact Allocated=False/"Failed"/"No matching hosts
	// available" condition BMFO's real reconcileInventory sets on its
	// zero-candidates branch (DD-229) — not an indefinite
	// Progressing/Allocating, and not a silent Ready.
	It("converges to Failed with 'no matching hosts' when no BareMetalHost matches its hostType", func() {
		applyOut, err := exec.Command("kubectl", "apply", "-f", bareMetalInstanceNoHostFixture).CombinedOutput() //nolint:gosec // fixed, repo-local path, not user input
		Expect(err).NotTo(HaveOccurred(), "kubectl apply -f %s failed: %s", bareMetalInstanceNoHostFixture, applyOut)

		var status bareMetalInstanceStatus
		Eventually(func() string {
			status = getBareMetalInstanceStatus(bareMetalInstanceNoHostName)
			return status.Phase
		}, 60*time.Second, 3*time.Second).Should(Equal("Failed"),
			"BareMetalInstance %q never reached Failed; last observed status: %+v", bareMetalInstanceNoHostName, status)

		condStatus, reason, message, found := bareMetalInstanceCondition(status, "Allocated")
		Expect(found).To(BeTrue(), "Allocated condition never appeared; last observed status: %+v", status)
		Expect(condStatus).To(Equal("False"))
		Expect(reason).To(Equal("Failed"))
		Expect(message).To(Equal("No matching hosts available"))
	})

	// TC-TB-140 / REQ-TB-120 / AC-TB-050: a distinct regression class from
	// TC-TB-130 above — a host of the right hostType genuinely exists, but
	// BMFO's real FindFreeHost candidate filter (OperationalStatus == OK,
	// DD-229) must still exclude it. A BMFO regression that dropped this
	// filter would pass TC-TB-130 (no host at all) but fail this one.
	It("converges to Failed the same way when its only matching BareMetalHost is ineligible (non-OK operationalStatus)", func() {
		applyOut, err := exec.Command("kubectl", "apply", "-f", bareMetalHostIneligibleFixture).CombinedOutput() //nolint:gosec // fixed, repo-local path, not user input
		Expect(err).NotTo(HaveOccurred(), "kubectl apply -f %s failed: %s", bareMetalHostIneligibleFixture, applyOut)
		patchBareMetalHostIneligibleStatus(bareMetalHostIneligibleName)

		applyOut, err = exec.Command("kubectl", "apply", "-f", bareMetalInstanceIneligibleFixture).CombinedOutput() //nolint:gosec // fixed, repo-local path, not user input
		Expect(err).NotTo(HaveOccurred(), "kubectl apply -f %s failed: %s", bareMetalInstanceIneligibleFixture, applyOut)

		var status bareMetalInstanceStatus
		Eventually(func() string {
			status = getBareMetalInstanceStatus(bareMetalInstanceIneligibleName)
			return status.Phase
		}, 60*time.Second, 3*time.Second).Should(Equal("Failed"),
			"BareMetalInstance %q never reached Failed; last observed status: %+v", bareMetalInstanceIneligibleName, status)

		condStatus, reason, message, found := bareMetalInstanceCondition(status, "Allocated")
		Expect(found).To(BeTrue(), "Allocated condition never appeared; last observed status: %+v", status)
		Expect(condStatus).To(Equal("False"))
		Expect(reason).To(Equal("Failed"))
		Expect(message).To(Equal("No matching hosts available"))
	})

	// TC-TB-150 / REQ-TB-120 / AC-TB-050: two BareMetalInstances racing for
	// the one available BareMetalHost. Grounded directly in AssignHost's
	// real double-claim guard (DD-229) — the loser clears its own
	// ExternalHostID and retries FindFreeHost, which now excludes the
	// claimed host, converging to the same zero-candidates Failed path as
	// TC-TB-130. Which of A/B wins is intentionally not asserted — only
	// that exactly one of them does, never both.
	It("lets exactly one of two competing BareMetalInstances claim the single available host", func() {
		applyOut, err := exec.Command("kubectl", "apply", "-f", bareMetalHostContendedFixture).CombinedOutput() //nolint:gosec // fixed, repo-local path, not user input
		Expect(err).NotTo(HaveOccurred(), "kubectl apply -f %s failed: %s", bareMetalHostContendedFixture, applyOut)
		patchBareMetalHostInitialStatus(bareMetalHostContendedName, "aa:bb:cc:dd:ee:03")

		applyOut, err = exec.Command("kubectl", "apply", "-f", bareMetalInstanceContendedAFixture).CombinedOutput() //nolint:gosec // fixed, repo-local path, not user input
		Expect(err).NotTo(HaveOccurred(), "kubectl apply -f %s failed: %s", bareMetalInstanceContendedAFixture, applyOut)
		applyOut, err = exec.Command("kubectl", "apply", "-f", bareMetalInstanceContendedBFixture).CombinedOutput() //nolint:gosec // fixed, repo-local path, not user input
		Expect(err).NotTo(HaveOccurred(), "kubectl apply -f %s failed: %s", bareMetalInstanceContendedBFixture, applyOut)

		var statusA, statusB bareMetalInstanceStatus
		Eventually(func() []string {
			statusA = getBareMetalInstanceStatus(bareMetalInstanceContendedAName)
			statusB = getBareMetalInstanceStatus(bareMetalInstanceContendedBName)
			return []string{statusA.Phase, statusB.Phase}
		}, 90*time.Second, 3*time.Second).Should(
			Or(Equal([]string{"Ready", "Failed"}), Equal([]string{"Failed", "Ready"})),
			"exactly one of %q/%q must reach Ready and the other Failed; last observed: A=%+v B=%+v",
			bareMetalInstanceContendedAName, bareMetalInstanceContendedBName, statusA, statusB,
		)
	})

	// TC-TB-160 / REQ-TB-120 / AC-TB-050. Uses its own dedicated fixture
	// pair, independent of every other spec in this file, so Ginkgo's
	// randomized spec order can't race this delete against another spec's
	// still-in-use instance. kubectl delete (no --wait=false) blocks until
	// BMFO's handleDeletion finalizer cleanup (UnassignHost) actually
	// completes (DD-229), so the release assertion below needs no
	// Eventually.
	It("releases its BareMetalHost (clears consumerRef) when a Ready BareMetalInstance is deleted", func() {
		applyOut, err := exec.Command("kubectl", "apply", "-f", bareMetalHostCleanupFixture).CombinedOutput() //nolint:gosec // fixed, repo-local path, not user input
		Expect(err).NotTo(HaveOccurred(), "kubectl apply -f %s failed: %s", bareMetalHostCleanupFixture, applyOut)
		patchBareMetalHostInitialStatus(bareMetalHostCleanupName, "aa:bb:cc:dd:ee:04")

		applyOut, err = exec.Command("kubectl", "apply", "-f", bareMetalInstanceCleanupFixture).CombinedOutput() //nolint:gosec // fixed, repo-local path, not user input
		Expect(err).NotTo(HaveOccurred(), "kubectl apply -f %s failed: %s", bareMetalInstanceCleanupFixture, applyOut)

		Eventually(func() string {
			return getBareMetalInstanceStatus(bareMetalInstanceCleanupName).Phase
		}, 60*time.Second, 3*time.Second).Should(Equal("Ready"),
			"BareMetalInstance %q never reached Ready before the delete-release assertion could run", bareMetalInstanceCleanupName)

		deleteOut, err := exec.Command("kubectl", "delete", "baremetalinstance", bareMetalInstanceCleanupName, "--timeout=60s").CombinedOutput() //nolint:gosec // fixed args, not user input
		Expect(err).NotTo(HaveOccurred(), "kubectl delete baremetalinstance %s failed: %s", bareMetalInstanceCleanupName, deleteOut)

		Expect(getBareMetalHostConsumerRef(bareMetalHostCleanupName)).To(BeEmpty(),
			"BareMetalHost %q must have its consumerRef cleared once the BareMetalInstance that claimed it is deleted", bareMetalHostCleanupName)
	})
})

// getBareMetalInstanceStatus mirrors getClusterOrderStatus's own
// kubectl-shell-out approach (see that function's doc comment for why: no
// client-go dependency in this module, kubectl is already configured for
// every other step in .github/workflows/e2e-tierb.yaml).
func getBareMetalInstanceStatus(name string) bareMetalInstanceStatus {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("kubectl", "get", "baremetalinstance", name, "-o", "jsonpath={.status}") //nolint:gosec // fixed args, not user input
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		Fail(fmt.Sprintf("kubectl get baremetalinstance %s failed: %v: %s", name, err, stderr.String()))
	}

	raw := strings.TrimSpace(stdout.String())
	if raw == "" {
		return bareMetalInstanceStatus{}
	}

	var status bareMetalInstanceStatus
	Expect(json.Unmarshal([]byte(raw), &status)).To(Succeed(), "unparseable BareMetalInstance status: %s", raw)
	return status
}

// patchBareMetalHostInitialStatus simulates a BareMetalHost that has
// already completed real Metal3 registration/inspection —
// operationalStatus OK, provisioning state available, one NIC reported —
// sufficient for BMFO's real FindFreeHost to select it (DD-226/227). mac
// must be a unique, valid MAC per fixture; nothing in this suite relies on
// its actual value. Set via --subresource=status since the vendored CRD
// declares subresources: {status: {}} (DD-227) — a plain `kubectl apply`
// body would silently drop it.
func patchBareMetalHostInitialStatus(name, mac string) {
	patch := fmt.Sprintf(`{"status":{"operationalStatus":"OK","poweredOn":false,"provisioning":{"state":"available"},"hardware":{"nics":[{"mac":%q,"name":"eth0"}]}}}`, mac)
	out, err := exec.Command("kubectl", "patch", "baremetalhost", name, //nolint:gosec // fixed args, mac is a compile-time constant, not user input
		"--subresource=status", "--type=merge", "-p", patch).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "kubectl patch baremetalhost %s status failed: %s", name, out)
}

// patchBareMetalHostPoweredOn simulates a real baremetal-operator finishing
// a power-on action. BMFO itself only ever patches spec.online (DD-226),
// never status.poweredOn, so nothing in this suite's own reconciliation
// path will ever set this without a fake-BMO step like this one.
func patchBareMetalHostPoweredOn(name string) {
	out, err := exec.Command("kubectl", "patch", "baremetalhost", name, //nolint:gosec // fixed args, not user input
		"--subresource=status", "--type=merge", "-p", `{"status":{"poweredOn":true}}`).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "kubectl patch baremetalhost %s poweredOn failed: %s", name, out)
}

// patchBareMetalHostIneligibleStatus simulates a BareMetalHost that exists
// but has not (yet, or ever) completed real Metal3 inspection — a non-OK
// operationalStatus. BMFO's real Metal3Client.FindFreeHost filters
// candidates on OperationalStatus == OK before anything else (DD-229), so
// a host patched this way must never be allocated. Same
// --subresource=status requirement as patchBareMetalHostInitialStatus
// (DD-227).
func patchBareMetalHostIneligibleStatus(name string) {
	out, err := exec.Command("kubectl", "patch", "baremetalhost", name, //nolint:gosec // fixed args, not user input
		"--subresource=status", "--type=merge", "-p", `{"status":{"operationalStatus":"Error","poweredOn":false}}`).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "kubectl patch baremetalhost %s status failed: %s", name, out)
}

// bareMetalInstanceCondition returns the named condition's Status/Reason/
// Message (and whether it was found at all) from a bareMetalInstanceStatus
// — used by TC-TB-130/140 to assert the exact condition BMFO's real
// reconcileInventory sets (DD-229), not just "never became Ready", which
// could also pass for a hung reconciler.
func bareMetalInstanceCondition(status bareMetalInstanceStatus, condType string) (condStatus, reason, message string, found bool) {
	for _, c := range status.Conditions {
		if c.Type == condType {
			return c.Status, c.Reason, c.Message, true
		}
	}
	return "", "", "", false
}

// getBareMetalHostConsumerRef returns the named BareMetalHost's
// spec.consumerRef.name (empty if unset) — TC-TB-160's release assertion.
// kubectl delete (no --wait=false) already blocks until BMFO's
// handleDeletion finalizer cleanup (UnassignHost) completes (DD-229), so
// no Eventually is needed at the call site.
func getBareMetalHostConsumerRef(name string) string {
	out, err := exec.Command("kubectl", "get", "baremetalhost", name, "-o", "jsonpath={.spec.consumerRef.name}").CombinedOutput() //nolint:gosec // fixed args, not user input
	Expect(err).NotTo(HaveOccurred(), "kubectl get baremetalhost %s failed: %s", name, out)
	return strings.TrimSpace(string(out))
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

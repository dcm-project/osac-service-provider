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

		// Exact match, not ContainSubstring: internal/health/health.go's
		// unhealthyDetail returns precisely "OIDC token invalid" when only
		// the token is invalid, and
		// "OIDC token invalid; OSAC fulfillment service unreachable" if
		// connectivity is *also* broken (own source, same string asserted
		// by internal/health/health_unit_test.go). A substring match would
		// let that second, unexpected failure mode (connectivity also
		// down) silently pass as if this test's one intended failure mode
		// were the only thing wrong.
		Expect(h.Detail).To(Equal("OIDC token invalid"),
			"a wrong client secret must surface as exactly a token-fetch failure, not an opaque connectivity error or a compounded one")
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

// clusterOrderCondition returns the named condition's Status/Reason/Message
// (and whether it was found at all) from a clusterOrderStatus — mirrors
// bareMetalInstanceCondition below, used by TC-TB-090's deeper assertions to
// check the exact condition osac-operator's real
// ClusterOrderReconciler.provisioningCallbacks sets when a provisioning job
// succeeds (api/v1alpha1/conditions.go: ConditionProgressing/
// ReasonAsExpected), not just ".status.phase == Ready" alone.
func clusterOrderCondition(status clusterOrderStatus, condType string) (condStatus, reason, message string, found bool) {
	for _, c := range status.Conditions {
		if c.Type == condType {
			return c.Status, c.Reason, c.Message, true
		}
	}
	return "", "", "", false
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

	// TC-TB-060 / REQ-TB-070 / AC-TB-030 (given clause). Deployment
	// readiness is deliberately NOT re-checked here: the workflow's own
	// `kubectl rollout status`/`kubectl wait --for=condition=Available`
	// steps for osac-operator/BMFO/osac-aap-mock already gate the job
	// before this suite ever starts (.github/workflows/e2e-tierb.yaml) —
	// re-asserting the identical condition here can never catch anything
	// those steps didn't already catch first, so it was dropped as a
	// no-value duplicate. The CRD check stays: nothing else in the
	// workflow verifies these are registered, and it has a real bug-catch
	// on record (DD-220/222 — osac-operator/BMFO once started, and
	// reported Available, while still missing a CRD they needed).
	It("has all 8 vendored CRDs registered before any Phase 2 reconciliation is exercised", func() {
		for _, crd := range phase2CRDs {
			out, err := exec.Command("kubectl", "get", "crd", crd).CombinedOutput() //nolint:gosec // fixed allowlist, not user input
			Expect(err).NotTo(HaveOccurred(), "CRD %q not registered: %s", crd, out)
		}
	})
})

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

		// Deeper assertions on *how* Ready was reached, not just the phase
		// value itself — a reconciler bug that flips .status.phase to
		// "Ready" directly (skipping the real AAP dispatch/poll path)
		// would still pass the assertion above but fail these. Every field
		// asserted below is fully deterministic — set only by
		// osac-operator's real provisioningCallbacks.OnSuccess handler
		// (api/v1alpha1/conditions.go) plus TriggerJob/PollJob
		// (pkg/provisioning/provision_lifecycle.go), fed by this suite's
		// own osac-aap-mock, which always reports a launched job as
		// "successful" with no pending/running window (DD-214,
		// test/aapmock/jobstore.go) — so nothing here is asserting on a
		// value this suite doesn't control. Only the job's AAP-assigned
		// jobID is left unchecked: an incrementing counter internal to
		// osac-aap-mock whose exact value isn't part of the behavior under
		// test.
		condStatus, reason, message, found := clusterOrderCondition(status, "Progressing")
		Expect(found).To(BeTrue(), "Progressing condition never appeared; last observed status: %+v", status)
		Expect(condStatus).To(Equal("False"))
		Expect(reason).To(Equal("AsExpected"))
		Expect(message).To(Equal(""))

		// Exactly one job: TriggerJob appends a single JobStatus entry and
		// PollJob updates it in place as it transitions toward Succeeded —
		// osac-operator never appends a second entry for the same
		// provisioning cycle (pkg/provisioning/provision_lifecycle.go). A
		// reconciler bug that re-triggered instead of polling would show up
		// here as len > 1, which NotTo(BeEmpty()) would have missed.
		Expect(status.ProvisioningJobs).To(HaveLen(1),
			"expected exactly one provisioning job; last observed status: %+v", status)
		job := status.ProvisioningJobs[0]
		Expect(job["type"]).To(Equal("provision"), "provisioning job's type; last observed status: %+v", status)
		Expect(job["state"]).To(Equal("Succeeded"), "provisioning job's state; last observed status: %+v", status)
		Expect(job["message"]).To(Equal("successful"),
			"provisioning job's message (osac-aap-mock's raw AAP status, passed through verbatim); last observed status: %+v", status)
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
	RunStrategy string `json:"runStrategy"`
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

		// Consistency with TC-TB-130/140's negative-path Allocated checks
		// below: those assert Allocated=False/"Failed" on the failure
		// branch of BMFO's real reconcileInventory; this asserts the
		// success branch's exact counterpart — Allocated=True/reason
		// "Allocated" (internal/controller/baremetalinstance_controller.go).
		// The message is asserted on its two fixture-controlled components
		// (the claimed host's name, and osac-inventory-config's
		// hostClass, DD-228) via substring rather than full equality,
		// since the format also embeds the claiming namespace — an
		// incidental deployment detail, not part of the behavior under
		// test — ahead of those two.
		condStatus, reason, message, found := bareMetalInstanceCondition(status, "Allocated")
		Expect(found).To(BeTrue(), "Allocated condition never appeared; last observed status: %+v", status)
		Expect(condStatus).To(Equal("True"))
		Expect(reason).To(Equal("Allocated"))
		Expect(message).To(And(
			ContainSubstring(bareMetalHostUnsetName),
			ContainSubstring("tierb-fixture-hostclass"),
		), "allocated-host message; last observed status: %+v", status)
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

		// Deeper assertions on *how* Ready was reached, not just the phase
		// value — grounded in BMFO's real syncBareMetalInstanceStatus: a
		// converged, powered-on host gets PowerSynced=True/reason
		// "PowerOn"/message "" (the literal call is
		// SetStatusCondition(HostConditionPowerSynced, ConditionTrue,
		// HostConditionReasonPowerOn, "") — reason and message are, in
		// that order, the 3rd/4th args on BareMetalInstance's
		// SetStatusCondition), and .status.runStrategy mirrors the
		// observed (not requested) power state as "Always"
		// (internal/controller/baremetalinstance_controller.go). Every
		// field below is fully deterministic for this code path, so all
		// are asserted exactly — a reconciler that reached Ready via some
		// other path (e.g. never actually reading the host's power state)
		// would still pass the Phase assertion above but fail these.
		condStatus, reason, message, found := bareMetalInstanceCondition(status, "PowerSynced")
		Expect(found).To(BeTrue(), "PowerSynced condition never appeared; last observed status: %+v", status)
		Expect(condStatus).To(Equal("True"))
		Expect(reason).To(Equal("PowerOn"))
		Expect(message).To(Equal(""))
		Expect(status.RunStrategy).To(Equal("Always"), "observed runStrategy; last observed status: %+v", status)

		// Same Allocated-condition consistency check as TC-TB-110 above —
		// see that test's comment for why the message is checked by
		// substring, not full equality.
		allocStatus, allocReason, allocMessage, allocFound := bareMetalInstanceCondition(status, "Allocated")
		Expect(allocFound).To(BeTrue(), "Allocated condition never appeared; last observed status: %+v", status)
		Expect(allocStatus).To(Equal("True"))
		Expect(allocReason).To(Equal("Allocated"))
		Expect(allocMessage).To(And(
			ContainSubstring(bareMetalHostAlwaysName),
			ContainSubstring("tierb-fixture-hostclass"),
		), "allocated-host message; last observed status: %+v", status)
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

		// Which of A/B wins the race is genuinely nondeterministic (the
		// one field this test has no control over) — but once a winner is
		// known, the contended host's consumerRef is fully determined by
		// it: AssignHost sets spec.consumerRef to the claiming instance's
		// name (DD-229), so it must equal exactly the winner's name, not
		// merely "non-empty". This closes the gap the bare Phase check
		// above leaves open: two instances independently reporting Ready/
		// Failed due to unrelated bugs would still pass that check, but
		// only a real, consistent single allocation passes this one too.
		winner := bareMetalInstanceContendedAName
		if statusB.Phase == "Ready" {
			winner = bareMetalInstanceContendedBName
		}
		Expect(getBareMetalHostConsumerRef(bareMetalHostContendedName)).To(Equal(winner),
			"BareMetalHost %q consumerRef must belong to whichever instance reached Ready", bareMetalHostContendedName)
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
// consumerRef instance name (empty if unset) — TC-TB-150/160's assertions.
// kubectl delete (no --wait=false) already blocks until BMFO's
// handleDeletion finalizer cleanup (UnassignHost) completes (DD-229), so
// no Eventually is needed at the call site.
// Note: BMFO stores consumerRef as a reference object; the .name field may
// contain a UID instead of instance name, so we always resolve by UID.
func getBareMetalHostConsumerRef(name string) string {
	out, err := exec.Command("kubectl", "get", "baremetalhost", name, "-o", "json").CombinedOutput() //nolint:gosec // fixed args, not user input
	Expect(err).NotTo(HaveOccurred(), "kubectl get baremetalhost %s failed: %s", name, out)

	var bmh map[string]interface{}
	err = json.Unmarshal(out, &bmh)
	Expect(err).NotTo(HaveOccurred(), "failed to parse BareMetalHost JSON: %s", out)

	spec, ok := bmh["spec"].(map[string]interface{})
	if !ok {
		return ""
	}

	consumerRef, ok := spec["consumerRef"].(map[string]interface{})
	if !ok {
		return ""
	}

	// BMFO stores consumerRef as a reference object with .uid and .name
	// The .name field appears to store the UID, not the instance name,
	// so we always resolve from UID by looking up the instance
	refUID, ok := consumerRef["uid"].(string)
	if !ok || refUID == "" {
		// Fallback: try .name field in case it contains the actual UID value
		if name, ok := consumerRef["name"].(string); ok && name != "" {
			// This might be a UID masquerading as a name; try to resolve it
			resolvedName := resolveInstanceNameByUID(name)
			if resolvedName != "" {
				return resolvedName
			}
			// If resolution fails, return the name field as-is for debugging
			return name
		}
		return ""
	}

	return resolveInstanceNameByUID(refUID)
}

// resolveInstanceNameByUID looks up a BareMetalInstance by UID and returns its name
func resolveInstanceNameByUID(uid string) string {
	listOut, err := exec.Command("kubectl", "get", "baremetalinstance", "-o", "json").CombinedOutput() //nolint:gosec // fixed args, not user input
	if err != nil {
		return ""
	}

	var instances map[string]interface{}
	err = json.Unmarshal(listOut, &instances)
	if err != nil {
		return ""
	}

	items, ok := instances["items"].([]interface{})
	if !ok {
		return ""
	}

	for _, item := range items {
		instance, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		metadata, ok := instance["metadata"].(map[string]interface{})
		if !ok {
			continue
		}
		instanceUID, ok := metadata["uid"].(string)
		if !ok {
			continue
		}
		if instanceUID == uid {
			if instanceName, ok := metadata["name"].(string); ok {
				return instanceName
			}
		}
	}

	return ""
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

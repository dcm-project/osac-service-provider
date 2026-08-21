// Package registration handles self-registration with DCM's environment
// agent.
//
// Implements Topic 4.4 (SP Registration) of the Milestone 1 spec. Per
// DD-203 (which supersedes DD-050's Phase 1 target — see
// https://github.com/dcm-project/osac-service-provider/issues/33), this
// registers against github.com/dcm-project/environment-agent's REST API
// and generated client — not control-plane's now-deleted SP API
// (control-plane#51) or the archived service-provider-manager.
package registration

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	agentv1alpha1 "github.com/dcm-project/environment-agent/api/v1alpha1"
	agentclient "github.com/dcm-project/environment-agent/pkg/client"

	"github.com/dcm-project/osac-service-provider/internal/config"
	"github.com/dcm-project/osac-service-provider/internal/versionmatrix"
)

const (
	schemaVersion = "v1alpha1"
	httpTimeout   = 30 * time.Second

	clusterServiceType = "cluster"
	vmServiceType      = "vm"

	clusterEndpointSuffix = "/api/v1alpha1/clusters"
	vmEndpointSuffix      = "/api/v1alpha1/vms"
)

// Option configures a Registrar.
type Option func(*Registrar)

// WithInitialBackoff sets the initial retry backoff interval for retryable
// failures.
func WithInitialBackoff(d time.Duration) Option {
	return func(r *Registrar) { r.initialBackoff = d }
}

// WithMaxBackoff sets the maximum retry backoff interval for retryable
// failures.
func WithMaxBackoff(d time.Duration) Option {
	return func(r *Registrar) { r.maxBackoff = d }
}

// WithReRegistrationInterval sets the cadence for periodic re-registration
// (REQ-REG-100), which keeps advertised capability metadata fresh, and also
// doubles as the retry cadence for a 409 Conflict (REQ-REG-080) — unlike a
// transient failure, a 409 means another provider currently holds the
// service-type slot, so retrying faster via exponential backoff wouldn't
// help; retrying on the same cadence as a normal renewal would (DD-203).
func WithReRegistrationInterval(d time.Duration) Option {
	return func(r *Registrar) { r.reRegistrationInterval = d }
}

// WithHTTPClient overrides the HTTP client used by the generated
// environment-agent client. Intended for tests (inject a fake
// http.RoundTripper).
func WithHTTPClient(c *http.Client) Option {
	return func(r *Registrar) { r.httpClient = c }
}

// Registrar performs the SP's two independent registrations (cluster, vm)
// with environment-agent's SP API.
type Registrar struct {
	cfg    *config.Config
	logger *slog.Logger
	client *agentclient.ClientWithResponses
	matrix versionmatrix.Matrix

	initialBackoff         time.Duration
	maxBackoff             time.Duration
	reRegistrationInterval time.Duration
	httpClient             *http.Client

	startOnce sync.Once
	done      chan struct{}
}

// NewRegistrar creates a Registrar targeting cfg.DCM.RegistrationURL.
// matrix's SupportedVersions() becomes the cluster registration payload's
// kubernetes_supported_versions (REQ-VERSION-050) — the single source of
// truth also consulted by internal/cluster for release_image translation,
// eliminating the drift risk of two independently hand-maintained lists.
func NewRegistrar(cfg *config.Config, logger *slog.Logger, matrix versionmatrix.Matrix, opts ...Option) (*Registrar, error) {
	r := &Registrar{
		cfg:                    cfg,
		logger:                 logger,
		matrix:                 matrix,
		initialBackoff:         1 * time.Second,
		maxBackoff:             60 * time.Second,
		reRegistrationInterval: 60 * time.Second,
		httpClient:             &http.Client{Timeout: httpTimeout},
		done:                   make(chan struct{}),
	}
	for _, opt := range opts {
		opt(r)
	}

	// Coverage exception (documented, not tested): as of this client's
	// current generated implementation, NewClient stores cfg.DCM.RegistrationURL
	// as-is (no parsing) and WithHTTPClient never returns an error, so no
	// input reachable from this codebase can make this branch fire today.
	// Kept as defensive error handling against a future client-generation
	// change (e.g. base-URL validation) rather than fabricating a failing
	// fake purely to hit it, per this suite's "test real production
	// types" convention (see .ai/test-plans/osac-sp-unit.test-plan.md,
	// section 4's coverage note).
	client, err := agentclient.NewClientWithResponses(cfg.DCM.RegistrationURL, agentclient.WithHTTPClient(r.httpClient))
	if err != nil {
		return nil, fmt.Errorf("creating environment-agent client: %w", err)
	}
	r.client = client

	return r, nil
}

// Start begins both registration loops in the background. It returns
// immediately (REQ-REG-050); multiple calls are safe, only the first
// launches goroutines.
func (r *Registrar) Start(ctx context.Context) {
	r.startOnce.Do(func() {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			r.runLoop(ctx, r.cfg.Provider.ClusterName, r.registerCluster)
		}()
		go func() {
			defer wg.Done()
			r.runLoop(ctx, r.cfg.Provider.VMName, r.registerVM)
		}()
		go func() {
			wg.Wait()
			close(r.done)
		}()
	})
}

// Done returns a channel closed once both registration loops have returned
// (context cancellation, or a non-retryable failure on both).
func (r *Registrar) Done() <-chan struct{} {
	return r.done
}

// registerCluster builds and sends the cluster registration payload.
//
// Implements REQ-REG-020, REQ-REG-030, REQ-REG-040.
func (r *Registrar) registerCluster(ctx context.Context) (int, error) {
	endpoint := r.cfg.Provider.Endpoint + clusterEndpointSuffix
	metadata := map[string]interface{}{
		"supported_platforms":           []string{"baremetal"},
		"supported_provisioning_types":  []string{"hypershift"},
		"kubernetes_supported_versions": r.matrix.SupportedVersions(),
	}
	return r.registerOnce(ctx, r.cfg.Provider.ClusterName, clusterServiceType, endpoint, metadata)
}

// registerVM builds and sends the vm registration payload.
//
// Implements REQ-REG-020, REQ-REG-030.
func (r *Registrar) registerVM(ctx context.Context) (int, error) {
	endpoint := r.cfg.Provider.Endpoint + vmEndpointSuffix
	return r.registerOnce(ctx, r.cfg.Provider.VMName, vmServiceType, endpoint, nil)
}

// registerOnce sends a single registration request and returns the HTTP
// status code (err is non-nil only for transport-level failures, not
// non-2xx responses).
//
// Implements REQ-REG-115: no Authorization header is set here.
// environment-agent's provider API documents its 401 response as
// "reserved; authentication deferred to future version" — sending none is
// the currently correct, documented behavior (DD-203), not an unenforced
// no-op the way it was under control-plane (DD-050's Authentication Gap).
func (r *Registrar) registerOnce(ctx context.Context, name, serviceType, endpoint string, metadata map[string]interface{}) (int, error) {
	provider := agentv1alpha1.Provider{
		Name:          name,
		ServiceType:   serviceType,
		Endpoint:      endpoint,
		SchemaVersion: schemaVersion,
	}
	if len(metadata) > 0 {
		provider.Metadata = &agentv1alpha1.ProviderMetadata{AdditionalProperties: metadata}
	}

	// environment-agent's registration endpoint is idempotent on name alone
	// (create-or-update), so no `id` query param is needed for the
	// create-or-update semantic.
	resp, err := r.client.CreateProviderWithResponse(ctx, nil, provider)
	if err != nil {
		return 0, fmt.Errorf("sending registration request for %q: %w", name, err)
	}
	return resp.StatusCode(), nil
}

// runLoop drives one service type's registration lifecycle: register, then
// periodically re-register to refresh capability metadata (REQ-REG-100).
// Retryable failures use exponential backoff (REQ-REG-070). A 409 Conflict
// is treated as non-fatal (REQ-REG-080): environment-agent enforces
// per-service-type exclusivity (only one SP, embedded or external, may
// serve a given service_type), so a 409 means another provider currently
// holds this slot — retried on the re-registration cadence, not escalating
// backoff, so this SP can acquire the slot later if the incumbent is
// displaced (DD-203). This applies uniformly to both service types (unlike
// the pre-Phase-1 design this restores, which special-cased vm only — see
// DD-203's "Deliberate divergence" note). Any other non-retryable 4xx stops
// the loop immediately (REQ-REG-090).
func (r *Registrar) runLoop(ctx context.Context, name string, register func(context.Context) (int, error)) {
	backoff := r.initialBackoff

	for {
		statusCode, err := register(ctx)

		switch {
		case err == nil && (statusCode == http.StatusOK || statusCode == http.StatusCreated):
			r.logger.Info("registration successful", "name", name, "status", statusCode)
			backoff = r.initialBackoff
			if !sleepOrDone(ctx, r.reRegistrationInterval) {
				return
			}
			continue

		case err == nil && statusCode == http.StatusConflict:
			r.logger.Warn("registration conflict: service type already served by another provider, will retry on re-registration cadence", "name", name)
			if !sleepOrDone(ctx, r.reRegistrationInterval) {
				return
			}
			continue

		case err == nil && statusCode >= 400 && statusCode < 500:
			r.logger.Error("registration failed with non-retryable status, giving up", "name", name, "status", statusCode)
			return

		case err == nil:
			r.logger.Warn("registration returned unexpected status, will retry", "name", name, "status", statusCode)

		default:
			r.logger.Warn("registration request failed, will retry", "name", name, "error", err)
		}

		if !sleepOrDone(ctx, backoff) {
			return
		}
		backoff *= 2
		if backoff > r.maxBackoff {
			backoff = r.maxBackoff
		}
	}
}

func sleepOrDone(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Package registration handles self-registration with the DCM environment
// agent.
//
// Implements Topic 4.4 (Environment Agent Registration) of the Milestone 1
// spec. Per DD-050, this registers against
// github.com/dcm-project/environment-agent's REST API and generated client
// — not control-plane's SP API or the archived service-provider-manager.
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
)

const (
	schemaVersion = "v1alpha1"
	httpTimeout   = 30 * time.Second

	clusterServiceType = "cluster"
	vmServiceType      = "vm"

	clusterEndpointSuffix = "/api/v1alpha1/clusters"
	vmEndpointSuffix      = "/api/v1alpha1/vms"
)

// kubernetesSupportedVersions is a hardcoded placeholder list for Milestone
// 1, per SC-001 — the full DCM-to-OSAC version compatibility matrix is
// Milestone 6 scope.
var kubernetesSupportedVersions = []string{"4.16", "4.17", "4.18"}

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

// WithLeaseRenewalInterval sets the cadence for periodic re-registration
// (REQ-REG-100) and for retrying a vm registration after a 409 (REQ-REG-080).
func WithLeaseRenewalInterval(d time.Duration) Option {
	return func(r *Registrar) { r.leaseRenewalInterval = d }
}

// WithHTTPClient overrides the HTTP client used by the generated environment
// agent client. Intended for tests (inject a fake http.RoundTripper).
func WithHTTPClient(c *http.Client) Option {
	return func(r *Registrar) { r.httpClient = c }
}

// Registrar performs the SP's two independent registrations (cluster, vm)
// with the environment agent.
type Registrar struct {
	cfg    *config.Config
	logger *slog.Logger
	client *agentclient.ClientWithResponses

	initialBackoff       time.Duration
	maxBackoff           time.Duration
	leaseRenewalInterval time.Duration
	httpClient           *http.Client

	startOnce sync.Once
	done      chan struct{}
}

// NewRegistrar creates a Registrar targeting cfg.Agent.RegistrationURL.
func NewRegistrar(cfg *config.Config, logger *slog.Logger, opts ...Option) (*Registrar, error) {
	r := &Registrar{
		cfg:                  cfg,
		logger:               logger,
		initialBackoff:       1 * time.Second,
		maxBackoff:           60 * time.Second,
		leaseRenewalInterval: 60 * time.Second,
		httpClient:           &http.Client{Timeout: httpTimeout},
		done:                 make(chan struct{}),
	}
	for _, opt := range opts {
		opt(r)
	}

	client, err := agentclient.NewClientWithResponses(cfg.Agent.RegistrationURL, agentclient.WithHTTPClient(r.httpClient))
	if err != nil {
		return nil, fmt.Errorf("creating environment agent client: %w", err)
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
			r.runLoop(ctx, r.cfg.Provider.ClusterName, r.registerCluster, false)
		}()
		go func() {
			defer wg.Done()
			r.runLoop(ctx, r.cfg.Provider.VMName, r.registerVM, true)
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
		"kubernetes_supported_versions": kubernetesSupportedVersions,
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
// Implements REQ-REG-115: the environment agent's registration endpoint
// requires no authentication in its current contract, so no Authorization
// header is set here.
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

	// The agent's registration endpoint is idempotent on name alone, so no
	// query params (e.g. `id`) are needed for the create-or-update semantic.
	resp, err := r.client.CreateProviderWithResponse(ctx, nil, provider)
	if err != nil {
		return 0, fmt.Errorf("sending registration request for %q: %w", name, err)
	}
	return resp.StatusCode(), nil
}

// runLoop drives one service type's registration lifecycle: register, then
// periodically re-register to renew the lease (REQ-REG-100). Retryable
// failures use exponential backoff (REQ-REG-070); non-retryable 4xx
// responses stop the loop (REQ-REG-090); when treat409AsLeaseCadence is set
// (vm only, REQ-REG-080), a 409 is treated like a successful cycle — logged
// at WARN and retried on the lease-renewal cadence rather than growing
// backoff.
func (r *Registrar) runLoop(ctx context.Context, name string, register func(context.Context) (int, error), treat409AsLeaseCadence bool) {
	backoff := r.initialBackoff

	for {
		statusCode, err := register(ctx)

		switch {
		case err == nil && (statusCode == http.StatusOK || statusCode == http.StatusCreated):
			r.logger.Info("registration successful", "name", name, "status", statusCode)
			backoff = r.initialBackoff
			if !sleepOrDone(ctx, r.leaseRenewalInterval) {
				return
			}
			continue

		case err == nil && statusCode == http.StatusConflict && treat409AsLeaseCadence:
			r.logger.Warn("registration conflict: service type already served by another provider, will retry on lease-renewal cadence", "name", name)
			if !sleepOrDone(ctx, r.leaseRenewalInterval) {
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

// Package aapmock fakes just enough of Ansible Automation Platform's REST
// API (GetTemplate, LaunchJobTemplate/LaunchWorkflowTemplate, GetJob,
// CanCancelJob, CancelJob) for real osac-operator/BMFO reconciliation loops
// to drive a ClusterOrder to a terminal Ready state, per
// osac-operator/pkg/aap.Client's real request/response shapes (REQ-TB-080).
// See .ai/specs/osac-sp-e2e-tier-b.spec.md §2 Phase 2 and DD-212/213.
package aapmock

import (
	"fmt"

	env "github.com/caarlos0/env/v11"
)

// Config holds test/cmd/osac-aap-mock's own listen address (REQ-TB-080).
// Deliberately not a reuse of internal/config.Config or
// test/mockprovider.Config: this binary has a single HTTP listener and none
// of either's concerns.
type Config struct {
	// Address is where the fake AAP REST endpoints listen.
	Address string `env:"MOCK_AAP_ADDRESS,notEmpty"`
}

// LoadConfig reads Config from environment variables, failing fast
// (matching internal/config.Load()'s convention) when the required value is
// missing/empty.
func LoadConfig() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("loading configuration: %w", err)
	}
	return cfg, nil
}

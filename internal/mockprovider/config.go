package mockprovider

import (
	"fmt"

	env "github.com/caarlos0/env/v11"
)

// Config holds cmd/osac-mock-provider's own listen addresses (REQ-MOCK-110).
// Deliberately not a reuse of internal/config.Config: this binary has none
// of the real SP's concerns (no HTTP router/middleware chain, no OSAC
// client to configure), just two independent net.Listen addresses.
type Config struct {
	// GRPCAddress is where the fake osac.public.v1 gRPC services listen.
	GRPCAddress string `env:"MOCK_GRPC_ADDRESS,notEmpty"`
	// OIDCAddress is where the fake OIDC discovery+token HTTP endpoints
	// listen.
	OIDCAddress string `env:"MOCK_OIDC_ADDRESS,notEmpty"`
}

// LoadConfig reads Config from environment variables, failing fast
// (matching internal/config.Load()'s convention) when a required value is
// missing/empty.
func LoadConfig() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("loading configuration: %w", err)
	}
	return cfg, nil
}

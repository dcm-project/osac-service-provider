// Package config handles configuration loading from environment variables.
package config

import (
	"fmt"
	"time"

	env "github.com/caarlos0/env/v11"
)

// ServerConfig holds HTTP server settings.
//
// Implements REQ-HTTP-050 (load config from environment variables).
type ServerConfig struct {
	Address         string        `env:"ADDRESS"          envDefault:":8080"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"15s"`
	RequestTimeout  time.Duration `env:"REQUEST_TIMEOUT"  envDefault:"30s"`
}

// OSACConfig holds settings for connecting to the OSAC fulfillment service
// and its Keycloak OIDC issuer.
//
// Implements REQ-OSAC-010, REQ-OSAC-030, REQ-OSAC-040, REQ-OSAC-050,
// REQ-OSAC-080.
type OSACConfig struct {
	FulfillmentAddress string        `env:"FULFILLMENT_ADDRESS,notEmpty"`
	OIDCIssuerURL      string        `env:"OIDC_ISSUER_URL,notEmpty"`
	OIDCClientID       string        `env:"OIDC_CLIENT_ID,notEmpty"`
	OIDCClientSecret   string        `env:"OIDC_CLIENT_SECRET,notEmpty"`
	TLSEnabled         bool          `env:"TLS_ENABLED" envDefault:"false"`
	TLSCertFile        string        `env:"TLS_CERT_FILE"`
	ProbeTimeout       time.Duration `env:"PROBE_TIMEOUT" envDefault:"5s"`
}

// AgentConfig holds settings for reaching the environment agent's
// registration endpoint.
//
// Implements REQ-REG-110.
type AgentConfig struct {
	RegistrationURL string `env:"REGISTRATION_URL,notEmpty"`
}

// ProviderConfig holds this service provider's own identity, as advertised
// during registration.
//
// Implements REQ-REG-010.
type ProviderConfig struct {
	Endpoint    string `env:"ENDPOINT,notEmpty"`
	ClusterName string `env:"PROVIDER_CLUSTER_NAME" envDefault:"osac-sp-cluster"`
	VMName      string `env:"PROVIDER_VM_NAME"      envDefault:"osac-sp-vm"`
}

// Config is the root configuration for the service provider.
type Config struct {
	Server   ServerConfig   `envPrefix:"SP_SERVER_"`
	OSAC     OSACConfig     `envPrefix:"SP_OSAC_"`
	Agent    AgentConfig    `envPrefix:"SP_AGENT_"`
	Provider ProviderConfig `envPrefix:"SP_"`
}

// Load reads configuration from environment variables. It fails fast
// (REQ-XC-CFG-020) when a required value is absent or empty, returning an
// error naming the missing field before any subsystem starts.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("loading configuration: %w", err)
	}
	return cfg, nil
}

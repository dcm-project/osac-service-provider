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

// DCMConfig holds settings for reaching control-plane's SP registration
// endpoint and the shared NATS broker used for async status reporting.
//
// Implements REQ-REG-110, REQ-PUBLISH-010. envPrefix is unprefixed by "SP_"
// (unlike the other nested configs) to match the DCM_REGISTRATION_URL env
// var name already used by sibling SPs (k8s-container-service-provider,
// acm-cluster-service-provider) for the same backend — see DD-050. NATSURL
// follows the same placement principle (DD-071): the NATS broker is a
// shared, DCM-wide backend, not provider-specific.
type DCMConfig struct {
	RegistrationURL string `env:"REGISTRATION_URL,notEmpty"`
	NATSURL         string `env:"NATS_URL,notEmpty"`
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

// StatusConfig holds settings for the async status-reporting poll loop.
//
// Implements REQ-POLL-010.
type StatusConfig struct {
	PollInterval time.Duration `env:"POLL_INTERVAL" envDefault:"30s"`
	ResyncEvery  int           `env:"RESYNC_EVERY"  envDefault:"10"`
}

// Config is the root configuration for the service provider.
type Config struct {
	Server   ServerConfig   `envPrefix:"SP_SERVER_"`
	OSAC     OSACConfig     `envPrefix:"SP_OSAC_"`
	DCM      DCMConfig      `envPrefix:"DCM_"`
	Provider ProviderConfig `envPrefix:"SP_"`
	Status   StatusConfig   `envPrefix:"SP_STATUS_"`
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

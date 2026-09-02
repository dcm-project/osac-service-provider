package mockprovider

import (
	"crypto/tls"
	_ "embed"
	"fmt"
)

// CertPEM and KeyPEM are a static, self-signed test-only TLS
// certificate/key pair (see testdata/README.md) — SANs localhost,
// 127.0.0.1, osac-mock-provider. Exported so both this package's own
// server (ServerTLSConfig) and other in-repo test fakes that need to serve
// or trust the same certificate (e.g. cmd/osac-service-provider's own
// lighter-weight test fixture) can reuse exactly one certificate rather
// than each minting their own.
//
//go:embed testdata/tls-cert.pem
var CertPEM []byte

//go:embed testdata/tls-key.pem
var KeyPEM []byte

// ServerTLSConfig builds a *tls.Config presenting CertPEM/KeyPEM, suitable
// for a real gRPC server (grpc.Creds(credentials.NewTLS(...))) to
// terminate real TLS instead of plaintext — required since osac-sp's
// fulfillment-service dial is unconditionally TLS (DD-229), with no
// insecure fallback for this mock (or any other server this repo's tests
// dial) to fall back to.
func ServerTLSConfig() (*tls.Config, error) {
	cert, err := tls.X509KeyPair(CertPEM, KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parsing mock provider test TLS cert/key: %w", err)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}, nil
}

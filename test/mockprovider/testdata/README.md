# Test-only TLS fixture

`tls-cert.pem` / `tls-key.pem` are a static, self-signed EC (P-256)
certificate/key pair generated once for this repo's own tests and e2e Phase
1 infra (`test/mockprovider`, `test/cmd/osac-mock-provider`,
`test/e2e/manifests/`). They exist so those fakes can terminate real TLS
instead of plaintext, now that `osac-sp`'s fulfillment-service dial is
unconditionally TLS (DD-229) with no insecure fallback.

- **Not a secret.** Both files are checked into git on purpose, the same
  way this repo already checks in other test-only credentials (e.g.
  `test/e2e/manifests-tierb/fulfillment-secrets.yaml`'s realm client
  secrets). Nothing behind this certificate is real; it is never used
  outside this repo's own tests/CI.
- **SANs:** `localhost`, `127.0.0.1` (Go integration tests dialing over
  loopback), `osac-mock-provider` (the Service DNS name `osac-sp` resolves
  in the e2e Phase 1 `kind` cluster).
- **Validity:** 10 years from generation (2026-09-01). No rotation
  mechanism is needed for a static test fixture; regenerate by hand (e.g.
  `openssl req -new -x509 -key tls-key.pem -days 3650 ...` with the same
  SANs) if it ever expires or a SAN needs to change.
- **Never used as a real CA.** `osac-sp`'s own `TLSCertFile` config also
  accepts this same `tls-cert.pem` as the CA to trust, since a self-signed
  leaf certificate is its own trust anchor.

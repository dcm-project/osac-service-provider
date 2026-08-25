# Tier B config (`osac-sp-e2e-tier-b.spec.md` Phase 1)

`realm.json` is a minimal Keycloak realm-export, hand-assembled from
[`fulfillment-service/docs/INSTALL.md`](https://github.com/osac-project/osac/blob/main/fulfillment-service/docs/INSTALL.md)'s
authoritative `KeycloakRealmImport` example (see DD-150) — not a verbatim
copy of any single upstream file.

**The `osac-admin`/`osac-controller` client secrets in this file are
test-only, static, and checked into git on purpose** (NFR-TB-020): this realm
only ever exists inside a throwaway `kind` cluster created and destroyed by
CI. Never reuse these values, or this file's shape, for any real deployment.

# Tier B config (`osac-sp-e2e-tier-b.spec.md`)

`realm.json` (Phase 1) is a minimal Keycloak realm-export, hand-assembled
from
[`fulfillment-service/docs/INSTALL.md`](https://github.com/osac-project/osac/blob/main/fulfillment-service/docs/INSTALL.md)'s
authoritative `KeycloakRealmImport` example (see DD-150) — not a verbatim
copy of any single upstream file.

**The `osac-admin`/`osac-controller` client secrets in this file are
test-only, static, and checked into git on purpose** (NFR-TB-020): this realm
only ever exists inside a throwaway `kind` cluster created and destroyed by
CI. Never reuse these values, or this file's shape, for any real deployment.

`osac-operator-values.yaml` (Phase 2) is a Helm `--values` override for the
real, published `osac-operator` chart — see DD-214 for why each key is set
the way it is (controller enablement, `osac-aap-mock` wiring, `ClusterIssuer`
retargeting). BMFO needs no equivalent file this phase; its only required
override (`osac-inventory-config`/`osac-management-config` stub Secrets,
DD-216) lives in `../manifests-tierb/bmfo-secrets.yaml` instead, since those
are cluster objects, not chart values.

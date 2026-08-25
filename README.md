# osac-service-provider

DCM Service Provider that integrates the
[Open Sovereign AI Cloud (OSAC)](https://github.com/osac-project/) platform
with DCM. It provisions OpenShift clusters and VMs by translating
agent-routed requests into OSAC fulfillment service gRPC API calls, and
reports status changes back via the messaging system.

Registration and dispatch are against `control-plane`'s Service Provider API
for this first release (Phase 1); the environment agent model originally
targeted by the enhancement doc is deferred to a future phase once that
component reaches a defined maturity bar — see DD-050 in
`.ai/specs/osac-sp.spec.md` and
[dcm-project/enhancements#95](https://github.com/dcm-project/enhancements/issues/95).

**Status:** Milestone 1 (scaffold + registration + health) merged. Milestone
2 (gRPC client generation) spec merged, implementation in review. See
[#1](https://github.com/dcm-project/osac-service-provider/issues/1) for the
full implementation plan and milestone breakdown, and `CLAUDE.md` for the
current architecture.

## Design

The authoritative design document is the
[OSAC Service Provider enhancement](https://github.com/dcm-project/enhancements/blob/main/enhancements/osac-sp/osac-sp.md),
merged via
[dcm-project/enhancements#78](https://github.com/dcm-project/enhancements/pull/78).
This repo implements it — if an implementation decision conflicts with the
enhancement doc, open a PR against the enhancement first.

## Tracking

- Epic: [FLPATH-4459](https://redhat.atlassian.net/browse/FLPATH-4459) — DCM:
  OSAC service provider
- Implementation: [FLPATH-4463](https://redhat.atlassian.net/browse/FLPATH-4463)
- Implementation plan: [#1](https://github.com/dcm-project/osac-service-provider/issues/1)

## Conventions

This repo follows the same structure and conventions as sibling DCM service
providers — see
[`k8s-container-service-provider`](https://github.com/dcm-project/k8s-container-service-provider),
[`acm-cluster-service-provider`](https://github.com/dcm-project/acm-cluster-service-provider),
and [`kubevirt-service-provider`](https://github.com/dcm-project/kubevirt-service-provider)
for reference. Details are in [#1](https://github.com/dcm-project/osac-service-provider/issues/1).

## E2E CI pattern (for other SP teams)

This repo built the first kind-based e2e CI tier of any DCM service
provider — real `control-plane` + a real, independently-built SP, with only
the actual external backend mocked (see
[#17](https://github.com/dcm-project/osac-service-provider/issues/17)).
[`docs/e2e-ci-pattern-for-service-providers.md`](./docs/e2e-ci-pattern-for-service-providers.md)
documents the pattern for other SP teams to copy directly, including the
two real mistakes made while hardening it so other repos don't repeat them.

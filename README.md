# osac-service-provider

DCM Service Provider that integrates the
[Open Sovereign AI Cloud (OSAC)](https://github.com/osac-project/) platform
with DCM through the environment agent model. It provisions OpenShift
clusters and VMs by translating agent-routed requests into OSAC fulfillment
service gRPC API calls, and reports status changes back via the messaging
system.

**Status:** Milestone 1 (scaffold + registration + health) implemented,
pending review. See
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

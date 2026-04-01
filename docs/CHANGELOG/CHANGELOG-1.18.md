<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
**Table of Contents**  *generated with [DocToc](https://github.com/thlorenz/doctoc)*

- [v1.18.0-alpha.1](#v1180-alpha1)
  - [Downloads for v1.18.0-alpha.1](#downloads-for-v1180-alpha1)
  - [Changelog since v1.18.0-alpha.0](#changelog-since-v1180-alpha0)
  - [Urgent Update Notes](#urgent-update-notes)
  - [Changes by Kind](#changes-by-kind)
    - [API Changes](#api-changes)
    - [Features & Enhancements](#features--enhancements)
    - [Deprecation](#deprecation)
    - [Bug Fixes](#bug-fixes)
    - [Security](#security)
  - [Other](#other)
    - [Dependencies](#dependencies)
    - [Helm Charts](#helm-charts)
    - [Instrumentation](#instrumentation)
    - [Performance](#performance)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# v1.18.0-alpha.1
## Downloads for v1.18.0-alpha.1

Download v1.18.0-alpha.1 in the [v1.18.0-alpha.1 release page](https://github.com/karmada-io/karmada/releases/tag/v1.18.0-alpha.1).

## Changelog since v1.18.0-alpha.0

## Urgent Update Notes

## Changes by Kind

### API Changes
None.

### Features & Enhancements
- `karmada-scheduler`: Extended the MultiplePodTemplatesScheduling feature to support workloads with only one component. ([#7287](https://github.com/karmada-io/karmada/pull/7287), @seanlaii)
- `karmada-search`: Added `--cluster-api-qps` and `--cluster-api-burst` flags, and used them when creating member-cluster dynamic clients in search control. ([#7325](https://github.com/karmada-io/karmada/pull/7325), @manmathbh)
- `karmada-webhook`: Now rejects `PropagationPolicy` and `ClusterPropagationPolicy` resources that use the `Lt` or `Gt` operator in `spec.placement.clusterTolerations`. ([#7272](https://github.com/karmada-io/karmada/pull/7272), @XiShanYongYe-Chang)

### Deprecation
- `Instrumentation`: The deprecated `cluster` and `cluster_name` Prometheus metric labels have been removed. The newly introduced `member_cluster` metric label name will now be used for that purpose moving forward. ([#7258](https://github.com/karmada-io/karmada/pull/7258), @dahuo98)
- `karmada-controller-manager`: The flags `--cluster-lease-duration` and `--cluster-lease-renew-interval-fraction` have been removed. ([#7271](https://github.com/karmada-io/karmada/pull/7271), @dahuo98)
- `karmadactl`: The deprecated `Etcd.Local.InitImage` in `Karmada Init Configuration` has been removed. ([#7259](https://github.com/karmada-io/karmada/pull/7259), @dahuo98)

### Bug Fixes
- `karmada-agent`: Fixed the issue that certificate rotation CSRs were never auto-approved due to a SignerName mismatch between `cert_rotation_controller` and `agent_csr_approving`. ([#7275](https://github.com/karmada-io/karmada/pull/7275), @hl8086)
- `karmada-controller-manager`: Avoided blocking dependency propagation on informer cache synchronization for newly watched dependent resources. ([#7276](https://github.com/karmada-io/karmada/pull/7276), @whitewindmills)
- `karmada-controller-manager`: Fixed a race condition where graceful eviction tasks could be silently dropped when multiple controllers concurrently modify the same ResourceBinding or ClusterResourceBinding, preventing workloads from being evacuated from tainted or failing clusters. ([#7302](https://github.com/karmada-io/karmada/pull/7302), @Ady0333)
- `karmada-controller-manager`: Fixed an issue where the FullyApplied condition of ResourceBinding could be incorrectly reported during RetryOnConflict retries when the cluster set changed. ([#7226](https://github.com/karmada-io/karmada/pull/7226), @Ady0333)
- `karmada-scheduler`: Relaxed zone affinity matching for multi-zone clusters, so a cluster is eligible when any configured zone overlaps the selector. ([#6431](https://github.com/karmada-io/karmada/pull/6431), @whitewindmills)
- `karmada-search`: Fixed the issue that watch connect cannot reflect resources from recovered clusters immediately. ([#7074](https://github.com/karmada-io/karmada/pull/7074), @XiShanYongYe-Chang)
- `openapi schema`: Fixed the unknown model error by using fully qualified model names as OpenAPI model names instead of Go type names. ([#7301](https://github.com/karmada-io/karmada/pull/7301), @zhzhuang-zju)

### Security
None.

## Other

### Dependencies
- `karmada-controller-manager/karmada-agent`: Upgraded `gopher-lua` dependency to v1.1.1. ([#7270](https://github.com/karmada-io/karmada/pull/7270), @XiShanYongYe-Chang)
- Updated Kubernetes dependencies to v1.35.3. ([#7326](https://github.com/karmada-io/karmada/pull/7326), @dahuo98)

### Helm Charts
- `Helm chart`: Added helm index for `v1.17.0`. ([#7282](https://github.com/karmada-io/karmada/pull/7282), @github-actions)
- `karmada-chart`: Fixed unrendered `{{ ca_crt }}` during upgrades. ([#7185](https://github.com/karmada-io/karmada/pull/7185), @faucct)

### Instrumentation
None.

### Performance
None.

# Changelog

All notable changes are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.1] - 2026-10-29

### Fixed

- `crds.keep` also keeps the CRDs when an Argo CD Application of the chart is deleted
  (`argocd.argoproj.io/sync-options: Delete=false`). Argo CD ignores Helm's resource policy, so
  deleting the Application deleted the CRDs and with them every DNSRecord, whose records the
  operator then removed from the zone.

## [0.3.0] - 2026-10-29

### Added

- Chart value `extraObjects`: more manifests to install with the chart, rendered through `tpl`,
  so an install can carry the Secret's ExternalSecret and the ClusterAccount.

## [0.2.1] - 2026-10-29

### Changed

- The image and the chart are signed with cosign, and the image has a build provenance
  attestation. Earlier releases are unsigned.
- Updated Kubernetes libraries (0.37.1) and controller-runtime (0.25.2). The distroless base image
  is pinned by digest.

### Removed

- The kustomize install manifests under `config/`. The Helm chart is the way to install; the CRDs
  are also attached to each GitHub release.

## [0.2.0] - 2026-10-29

### Added

- `ClusterAccount.spec.namespaceSelector`: let namespaces use the account by their labels, besides
  those named in `allowedNamespaces`. Records notice a namespace's labels changing at once. The
  operator now reads and watches Namespaces, for which the chart's ClusterRole grants
  `get`, `list` and `watch`. A ClusterAccount whose selector is invalid reports `InvalidSpec`, and
  until it is fixed its records with `deletionPolicy: Delete` cannot be deleted.

### Upgrade notes

- Apply the 0.2.0 CRDs before using `namespaceSelector`. An older CRD or operator does not know the
  field and reads an account restricted by the selector alone as open to every namespace, so do
  not roll back below 0.2.0 while one exists. Listing at least one namespace in
  `allowedNamespaces` as well makes older versions fail closed.

## [0.1.0] - 2026-10-28

The first release.

### Added

- `DNSRecord`: one record in a Simply.com DNS zone, of any type Simply.com accepts. Created,
  corrected (also after edits in the control panel, at each resync) and removed with the object.
  `deletionPolicy: Orphan` leaves the record behind; `adopt: true` takes over an existing record.
- `ClusterAccount` and `Account`: the Simply.com API key, for the whole cluster or one namespace,
  with an optional list of the zones it may change, `allowedNamespaces` for a ClusterAccount, and
  `allowAdopt` (off by default) to permit taking over records made by hand.
- Ownership marker in the record comment, so the operator only ever changes or deletes records it
  created or adopted, and finds them again after a backup restore. The owner id is required.
- Helm chart at `oci://ghcr.io/petersr/charts/simply-com-operator` and image at
  `ghcr.io/petersr/simply-com-operator`, for linux/amd64 and linux/arm64.

[Unreleased]: https://github.com/PeterSR/simply-com-operator/compare/v0.3.1...HEAD
[0.3.1]: https://github.com/PeterSR/simply-com-operator/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/PeterSR/simply-com-operator/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/PeterSR/simply-com-operator/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/PeterSR/simply-com-operator/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/PeterSR/simply-com-operator/releases/tag/v0.1.0

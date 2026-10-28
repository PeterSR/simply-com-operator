# Changelog

All notable changes are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-10-29

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

[Unreleased]: https://github.com/PeterSR/simply-com-operator/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/PeterSR/simply-com-operator/releases/tag/v0.1.0

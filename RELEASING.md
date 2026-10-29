# Releasing

A release is a `v*` tag on `main`. Pushing it runs [release.yml](.github/workflows/release.yml),
which tests the tagged commit again, then publishes:

- the image `ghcr.io/petersr/simply-com-operator`, tagged `vX.Y.Z` and `X.Y.Z`, and for a final
  release also `X.Y` and `latest`;
- the chart `oci://ghcr.io/petersr/charts/simply-com-operator`, version `X.Y.Z`, whose default image
  tag is `vX.Y.Z`;
- a GitHub release with the notes from `CHANGELOG.md` and the CRDs as `crds.yaml`.

The image and the chart are signed with cosign (keyless), and the image carries a build provenance
attestation. Releases before 0.2.1 were made while the repository was private and are unsigned.

## Steps

1. Move the `[Unreleased]` entries in `CHANGELOG.md` under a new `## [X.Y.Z] - YYYY-MM-DD`
   heading, and update the links at the bottom.
2. Set `version`, `appVersion` and the image tag in `artifacthub.io/images` in
   `charts/simply-com-operator/Chart.yaml` to `X.Y.Z`. The release workflow sets them from the tag
   anyway; this keeps the chart in the repository honest.
3. Merge that to `main`, then tag the merge commit and push the tag:

   ```bash
   git tag -s vX.Y.Z -m vX.Y.Z
   git push origin vX.Y.Z
   ```

A tag with a suffix (`v0.2.0-rc.1`) is a prerelease: the image gets only its own tags, and the chart
is marked as a prerelease.

## Verifying a release

```bash
cosign verify ghcr.io/petersr/simply-com-operator:vX.Y.Z \
  --certificate-identity-regexp '^https://github.com/PeterSR/simply-com-operator/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
helm pull oci://ghcr.io/petersr/charts/simply-com-operator --version X.Y.Z
```

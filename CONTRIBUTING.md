# Contributing

Thanks for considering a contribution. Bug reports, fixes and support for more of the Simply.com API
are all welcome.

## Repository layout

| Path | What |
|---|---|
| `api/v1alpha1/` | the custom resource types; CRDs are generated from them |
| `internal/simply/` | a small client for the Simply.com API |
| `internal/simply/simplytest/` | an in-memory Simply.com stand-in for tests |
| `internal/controller/` | the controllers; `dnsrecord_plan.go` decides what to do to a zone and is pure |
| `cmd/` | the manager binary |
| `config/` | generated CRDs and RBAC, and kustomize manifests |
| `charts/simply-com-operator/` | the Helm chart; `templates/crds/` and `templates/clusterrole.yaml` are copied from `config/` by `hack/sync-chart.sh` |
| `examples/` | example resources, and an application chart that brings its own records |
| `hack/` | the end-to-end scripts and release helpers |

## Development

You need Go 1.26 or newer and Docker; kind and Helm for the end-to-end test.

```bash
make test             # unit tests, and controller tests against envtest
make lint
make manifests        # after changing api/ or RBAC markers; also updates the chart
make verify-generated # what CI runs to check the above was done
hack/e2e-kind.sh      # image and chart on a kind cluster
```

The controller tests talk to `simplytest`, never to Simply.com. When you rely on a behaviour of the
real API, reproduce it in `simplytest` and say in a comment where it was seen. To check against a
real account, `hack/e2e-live.sh <zone> <label>` creates and removes a few records under a label no
real record uses.

## Adding another Simply.com resource

Each Simply.com area (mail forwards, DNS forwards, ...) becomes its own kind in the
`simplycom.petersr.github.io` group, with an `accountRef` like `DNSRecord`'s and its own controller.
Resolve the account through `SimplyAccess`, extend `internal/simply` with the endpoints, and follow
the same rule as DNS records: never change or delete anything at Simply.com that the operator did
not create or explicitly adopt.

## Pull requests

- Keep a pull request to one change, with tests.
- Add a line under `[Unreleased]` in `CHANGELOG.md` if users would notice the change.
- By contributing you agree that your contribution is licensed under the Apache License 2.0.

Please follow the [code of conduct](CODE_OF_CONDUCT.md).

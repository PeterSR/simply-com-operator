# simply-com-operator

A Kubernetes operator for [Simply.com](https://www.simply.com). Declare DNS records as Kubernetes
objects, next to the workloads that need them, and the operator keeps the zone at Simply.com in line.

```yaml
apiVersion: simplycom.petersr.github.io/v1alpha1
kind: DNSRecord
metadata:
  name: shop
  namespace: shop
spec:
  accountRef:
    name: default
  zone: example.com
  name: shop
  type: CNAME
  data: ingress.example.net
  ttl: 600
```

The operator is built to cover more of the Simply.com API over time (mail forwards, DNS forwards and
so on, each as its own kind sharing the same accounts). Today it manages **DNS records**.

> Status: alpha. The API is `v1alpha1` and may change before 1.0.
>
> This project is not affiliated with or endorsed by Simply.com.

## Contents

- [How it works](#how-it-works)
- [Install](#install)
- [Accounts](#accounts)
- [DNS records](#dns-records)
- [Safety: what the operator will and will not touch](#safety-what-the-operator-will-and-will-not-touch)
- [Declaring records from your own Helm charts](#declaring-records-from-your-own-helm-charts)
- [Configuration](#configuration)
- [Limitations](#limitations)
- [Development](#development)

## How it works

- An **account** (`ClusterAccount`, or a namespaced `Account`) points at a Secret holding a
  Simply.com API key.
- Each **`DNSRecord`** is one record in one zone. The operator creates it, corrects it when the spec
  changes or someone edits it in the control panel, and removes it when the object is deleted.
- Simply.com records have no labels, so the operator marks the records it manages in their
  **comment**: `[simply-com-operator owner=<owner-id> ref=<namespace>/<name>]`, after any comment of
  your own. That marker is how it recognises its records, also after a backup restore that loses the
  object's status. It never changes or deletes a record without its own marker.
- After every change it asks Simply.com to reload the zone; new records are typically served by
  Simply.com's name servers within half a minute.

## Install

The chart and the image are published to GitHub Container Registry.

```bash
helm install simply-com-operator oci://ghcr.io/petersr/charts/simply-com-operator \
  --namespace simply-com-operator --create-namespace
```

The chart installs the CustomResourceDefinitions too (`crds.install`), and keeps them on uninstall
(`crds.keep`) so that uninstalling the chart does not delete every `DNSRecord`. To install the
definitions yourself, each release also carries them as `crds.yaml`:

```bash
kubectl apply -f https://github.com/PeterSR/simply-com-operator/releases/latest/download/crds.yaml
```

Then give it an API key. Create one in the Simply.com control panel under *Account*, *API keys*.

```bash
kubectl -n simply-com-operator create secret generic simply-com-api-key --from-literal=apiKey=<key>
```

```yaml
apiVersion: simplycom.petersr.github.io/v1alpha1
kind: ClusterAccount
metadata:
  name: default
spec:
  apiKeySecretRef:
    name: simply-com-api-key
    namespace: simply-com-operator
  zones:            # optional: the only zones this account may change
    - example.com
```

```console
$ kubectl get clusteraccounts
NAME      READY   REASON          AGE
default   True    Authenticated   5s
```

The chart can create the ClusterAccount for you (`clusterAccount.create=true`); the Secret it leaves
to you or your secret manager.

## Accounts

| Kind | Scope | Secret | Usable by |
|---|---|---|---|
| `ClusterAccount` | cluster | any namespace; defaults to the operator's | `DNSRecord`s in every namespace |
| `Account` | namespace | its own namespace | `DNSRecord`s in its own namespace |

Both have:

| Field | |
|---|---|
| `spec.apiKeySecretRef.name` | the Secret |
| `spec.apiKeySecretRef.key` | the key in it; default `apiKey` |
| `spec.apiKeySecretRef.namespace` | `ClusterAccount` only; default the operator's namespace |
| `spec.zones` | if set, the only zones (Simply.com products) records using this account may change |

The status reports whether Simply.com accepted the key (`Ready`) and which products it can reach
(`status.products`). The key is read when it is used, so a rotated Secret takes effect at once.

## DNS records

| Field | |
|---|---|
| `accountRef.kind` | `ClusterAccount` (default) or `Account`. Immutable. |
| `accountRef.name` | the account. Immutable. |
| `zone` | the Simply.com product holding the zone, which is the domain, such as `example.com`. Immutable. |
| `name` | relative to the zone: `www`, `_dmarc`, `@` for the apex, `*` for a wildcard. A full name inside the zone (`www.example.com`) works too. |
| `type` | `A`, `AAAA`, `ALIAS`, `CAA`, `CNAME`, `HTTPS`, `LOC`, `MX`, `NS`, `SRV`, `SSHFP`, `TLSA`, `TXT` |
| `data` | the value: an address, a host name (trailing dot optional), TXT text without quotes, `weight port target` for SRV |
| `ttl` | seconds, at least 60. Left out, the zone's default applies and a TTL set at Simply.com is kept. |
| `priority` | required for `MX` and `SRV` |
| `comment` | shown in the control panel, before the operator's marker |
| `deletionPolicy` | `Delete` (default) removes the record with the object; `Orphan` leaves it in the zone |
| `adopt` | take over an existing, unmarked record with exactly this name, type and data, instead of reporting a conflict |

```console
$ kubectl get dnsrecords -A
NAMESPACE   NAME   FQDN               TYPE    READY   REASON   AGE
shop        shop   shop.example.com   CNAME   True    Synced   1m
```

`Ready` is `False` with one of these reasons when the record is not in place:

| Reason | Meaning |
|---|---|
| `AccountNotFound`, `SecretNotFound` | the account or its Secret is missing; retried, and picked up when it appears |
| `ZoneNotAllowed` | the account's `zones` does not include the zone |
| `InvalidSpec` | for example an `A` record whose data is not an IPv4 address, or a CNAME at the apex |
| `Conflict` | another record stands in the way; the message says which (see below) |
| `APIError` | Simply.com refused or failed the request; the message has its answer |

Several values for one name (two `A` records, several `TXT` records) are several `DNSRecord`s.

More examples are in [examples/](examples/).

## Safety: what the operator will and will not touch

The operator was written to run against zones that also hold records made by hand.

- It only ever **updates or deletes records carrying its own marker** for that exact object and
  owner id. Deleting a `DNSRecord` that never managed a record deletes nothing.
- A record with the same name, type and data that is **not** marked is a `Conflict`, and is left as
  it is. Set `adopt: true` to take it over; the operator then rewrites its comment to the spec's
  comment plus the marker, and from then on manages (and on deletion removes) it.
- A record marked by another installation or another object is always a `Conflict`, also with `adopt`.
- A `CNAME` cannot share its name with another record. Wanting a CNAME where an unmanaged record
  exists, or another record where an unmanaged CNAME exists, is a `Conflict`.
- Simply.com accepts exact duplicates, so the operator always lists the zone afresh before it
  creates or changes anything.
- With `spec.zones` on the account, records outside those zones are neither written nor read, also
  when the `DNSRecord` is deleted.

**Owner id.** If more than one installation (say, one per cluster) manages records in the same zone,
give each a different `ownerID` in the chart values. Records keep the owner id they were created
with, so changing it later turns the existing records into conflicts.

## Declaring records from your own Helm charts

Because a record is an ordinary Kubernetes object, an application's chart can carry the DNS it needs.
For example, a chart that is installed once per customer, each with its own host name:

```yaml
# values.yaml
dns:
  enabled: true
  zone: example.com
  target: ingress.example.net
  hosts: [app, api]
```

```yaml
# templates/dnsrecords.yaml
{{- if .Values.dns.enabled }}
{{- range .Values.dns.hosts }}
---
apiVersion: simplycom.petersr.github.io/v1alpha1
kind: DNSRecord
metadata:
  name: {{ $.Release.Name }}-{{ . }}
spec:
  accountRef:
    name: default
  zone: {{ $.Values.dns.zone }}
  name: {{ . }}.{{ $.Release.Name }}
  type: CNAME
  data: {{ $.Values.dns.target }}
  ttl: 600
  comment: {{ $.Release.Name }}
{{- end }}
{{- end }}
```

Installing the chart as `acme` creates `app.acme.example.com` and `api.acme.example.com`;
uninstalling it removes them. The full example is in [examples/helm-chart/](examples/helm-chart/).

## Configuration

| Chart value | Flag | Default | |
|---|---|---|---|
| `ownerID` | `--owner-id` | `default` | written into every marker; see above |
| `simplyAPI.url` | `--simply-api-url` | `https://api.simply.com/2` | |
| `simplyAPI.rateLimit`, `simplyAPI.burst` | `--api-rate-limit`, `--api-burst` | `5`, `10` | requests per second to Simply.com, for the whole operator |
| `resyncInterval` | `--resync-interval` | `10m` | how often a record in sync is checked again, undoing changes made in the control panel |
| `retryInterval` | `--retry-interval` | `1m` | wait after a problem only you or Simply.com can fix |
| `accountCheckInterval` | `--account-check-interval` | `1h` | how often an account's key is checked again |
| | `--zone-cache-ttl` | `10s` | how long a zone listing is reused when nothing needs changing |
| `clusterAccount.*` | | off | create a `ClusterAccount` with the chart |
| `rbac.aggregateToDefaultRoles` | | `true` | let users with the built-in `edit`/`view` roles manage `DNSRecord`s and `Account`s |
| `metrics.enabled` | `--metrics-bind-address` | off | Prometheus metrics on `:8080`, optionally with a `ServiceMonitor` |

See [values.yaml](charts/simply-com-operator/values.yaml) for the rest.

Secrets are read directly from the API server when they are used, rather than cached, so the operator
does not hold every Secret in the cluster in memory. It needs `get` on Secrets cluster-wide, for
namespaced `Account`s.

## Limitations

- Simply.com's API can only list a whole zone; the operator lists it once per sync of a record that
  needs changing, and shares a recent listing between records otherwise. Very large zones with many
  managed records will make many requests at startup; the rate limit keeps them in bounds.
- A change made in the control panel to a managed record is undone at the next resync, not at once.
- If the account or its Secret is gone when a `DNSRecord` with `deletionPolicy: Delete` is deleted,
  deletion waits until it is back. Set `deletionPolicy: Orphan` on the object to let it go. Keep
  `Account`s and their Secrets out of the namespaces that are deleted together with their records,
  or use a `ClusterAccount`.
- Only DNS records so far. The accounts are shared by design, so further kinds can follow.

## Development

You need Go 1.26 and Docker; [kind](https://kind.sigs.k8s.io) and Helm for the end-to-end test.

```bash
make test        # unit tests, and controller tests against a real API server (envtest)
make lint
make manifests   # regenerate the CRDs and RBAC, and copy them into the chart
hack/e2e-kind.sh # build the image, install the chart into a kind cluster, run a smoke test
```

The controller tests run against an in-memory Simply.com stand-in
([internal/simply/simplytest](internal/simply/simplytest)) that reproduces the API behaviour the
operator depends on. To try the operator against a real zone, [hack/e2e-live.sh](hack/e2e-live.sh)
creates a handful of records under a test name of your choosing, checks them, and removes them again.

See [CONTRIBUTING.md](CONTRIBUTING.md) and [RELEASING.md](RELEASING.md).

## License

[Apache License 2.0](LICENSE).

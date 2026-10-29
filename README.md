# simply-com-operator

[![test](https://github.com/PeterSR/simply-com-operator/actions/workflows/test.yml/badge.svg)](https://github.com/PeterSR/simply-com-operator/actions/workflows/test.yml)
[![release](https://img.shields.io/github/v/release/PeterSR/simply-com-operator)](https://github.com/PeterSR/simply-com-operator/releases)
[![license](https://img.shields.io/github/license/PeterSR/simply-com-operator)](LICENSE)

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

> Status: alpha. The API is `v1alpha1` and may change before 1.0.
>
> This project is not affiliated with or endorsed by Simply.com.

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
  --namespace simply-com-operator --create-namespace \
  --set ownerID=my-cluster
```

`ownerID` is required: it names this installation in the marker on every record it manages (see
[Safety](#safety-what-the-operator-will-and-will-not-touch)).

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
  zones:              # optional: the only zones this account may change
    - example.com
  allowedNamespaces:  # optional: namespaces whose records may use it
    - shop
  namespaceSelector:  # optional: and namespaces with these labels
    matchLabels:
      simply-dns: allowed
```

```console
$ kubectl get clusteraccounts
NAME      READY   REASON          AGE
default   True    Authenticated   5s
```

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
| `spec.allowAdopt` | let records using this account take over records made by hand (`adopt`); default off |
| `spec.allowedNamespaces` | `ClusterAccount` only: namespaces whose records may use it |
| `spec.namespaceSelector` | `ClusterAccount` only: a label selector for more namespaces whose records may use it |

A ClusterAccount with neither `allowedNamespaces` nor `namespaceSelector` may be used from every
namespace. With either, a namespace may use it when the list names it or the selector matches its
labels. An empty selector (`{}`) matches every namespace. Records notice a label change at once.
Labelling a namespace needs `update` or `patch` on the Namespace, which the built-in `admin` and
`edit` roles do not grant; if others can label namespaces, protect the label with an admission
policy.

A ClusterAccount whose `namespaceSelector` is not a valid selector is `InvalidSpec`; until it is
fixed, its records report `InvalidSpec` and those with `deletionPolicy: Delete` cannot be deleted.

Upgrading from 0.1.x or rolling back to it: see the [changelog](CHANGELOG.md#020---2026-10-29).

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
| `deletionPolicy` | `Delete` (default) removes the record with the object; `Orphan` leaves it in the zone, without the marker |
| `adopt` | take over an existing, unmarked record with exactly this name, type and data, instead of reporting a conflict. The account must have `allowAdopt`. |

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
| `NotPermitted` | neither the ClusterAccount's `allowedNamespaces` nor its `namespaceSelector` lets this namespace in |
| `InvalidSpec` | for example an `A` record whose data is not an IPv4 address, a CNAME at the apex, or a ClusterAccount whose `namespaceSelector` is not a valid selector |
| `Conflict` | another record stands in the way; the message says which (see below) |
| `APIError` | Simply.com refused or failed the request; the message has its answer |

Several values for one name (two `A` records, several `TXT` records) are several `DNSRecord`s.

More examples are in [examples/](examples/).

## Safety: what the operator will and will not touch

The operator was written to run against zones that also hold records made by hand.

- It only ever **updates or deletes records carrying its own marker** for that exact object and
  owner id. Deleting a `DNSRecord` that never managed a record deletes nothing.
- A record with the same name, type and data that is **not** marked is a `Conflict`, and is left as
  it is. Set `adopt: true` to take it over (the account needs `allowAdopt`); the operator then
  rewrites its comment to the spec's comment plus the marker, and from then on manages (and on
  deletion removes) it. A managed record is never changed into a copy of a record it does not manage.
- A record marked by another installation or another object is always a `Conflict`, also with `adopt`.
- A `CNAME` cannot share its name with another record, so a CNAME at a name that has an unmanaged
  record (or the reverse) is a `Conflict`.
- Simply.com accepts exact duplicates, so the operator always lists the zone afresh before it
  creates or changes anything.
- With `spec.zones` on the account, records outside those zones are neither written nor read, also
  when the `DNSRecord` is deleted.
- `deletionPolicy: Orphan` removes the marker as it lets go, so a later object with the same name
  does not quietly take the record over. If the account is gone or Simply.com cannot be reached,
  the object is let go anyway and the marker stays (a warning event says so).
- If an account stops allowing a namespace or a zone, its `DNSRecord`s can still be deleted; their
  records are left in the zone, with a warning event.
- A later object with the same name does not inherit such a left-behind record if its name or type
  differs; that is a `Conflict` until the object sets `adopt`.

**Who may create records.** Anyone who can create a `DNSRecord` can add records to, and with
`allowAdopt` take over, every zone of every account their namespace may use. Extra records at an
existing name are allowed by design (several `A` or `TXT` values), so this includes, say, a second
`A` record for the apex or an `_acme-challenge` TXT. Grant `DNSRecord` creation like you would grant
access to the DNS itself: give ClusterAccounts `allowedNamespaces` or `namespaceSelector`, and
`zones`, and leave the chart's `rbac.aggregateToDefaultRoles` off unless every namespace editor
should have that power. The `zones` and `allowAdopt` of a namespaced `Account` are guard rails for
its own namespace, not a boundary: whoever may edit the Account, or read its Secret, can change
them or use the key directly.

**Owner id.** Every installation that may manage records in the same zone (one per cluster, and a
cluster restored from another's backup) needs its own `ownerID`. Two installations with the same id
would each treat the other's records as their own. Records keep the id they were created with, so
changing it later turns the existing records into conflicts.

## Declaring records from your own Helm charts

Because a record is an ordinary Kubernetes object, an application's chart can carry the DNS it needs.
For example, a chart that is installed once per customer, each with its own host name:

```yaml
# values.yaml
dns:
  enabled: true
  account: default
  zone: example.com
  target: ingress.example.net
  hosts: [app, api]
  ttl: 600
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
    name: {{ $.Values.dns.account }}
  zone: {{ $.Values.dns.zone }}
  name: {{ . }}.{{ $.Release.Name }}
  type: CNAME
  data: {{ $.Values.dns.target }}
  ttl: {{ $.Values.dns.ttl }}
  comment: {{ $.Release.Name }}
{{- end }}
{{- end }}
```

Installing the chart as `acme` creates `app.acme.example.com` and `api.acme.example.com`;
uninstalling it removes them. The full example is in [examples/helm-chart/](examples/helm-chart/).

## Configuration

| Chart value | Flag | Default | |
|---|---|---|---|
| `ownerID` | `--owner-id` | required | written into every marker; see above |
| `simplyAPI.url` | `--simply-api-url` | `https://api.simply.com/2` | |
| `simplyAPI.rateLimit`, `simplyAPI.burst` | `--api-rate-limit`, `--api-burst` | `5`, `10` | requests per second to Simply.com, for the whole operator |
| `resyncInterval` | `--resync-interval` | `10m` | how often a record in sync is checked again, undoing changes made in the control panel |
| `retryInterval` | `--retry-interval` | `1m` | wait after a problem only you or Simply.com can fix |
| `accountCheckInterval` | `--account-check-interval` | `1h` | how often an account's key is checked again |
| | `--zone-cache-ttl` | `10s` | how long a zone listing is reused when nothing needs changing |
| `rbac.aggregateToDefaultRoles` | | `false` | let users with the built-in `edit`/`view` roles manage `DNSRecord`s and `Account`s |
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
- A zone whose reload failed is reloaded at the next sync of any record in it, and every zone is
  reloaded once after the operator starts, in case a change was stored but never published.
- Record data is compared after normalising host names, addresses, SRV targets, TXT and CAA quotes
  and TLSA/SSHFP hex case. For other types (HTTPS, LOC, ...) write the data the way Simply.com
  returns it: otherwise each resync rewrites the record, and a hand-made record with the same value
  is not recognised as one, so the operator adds a second record instead of reporting a conflict.
- Uninstalling the chart keeps the CRDs and every `DNSRecord`, but nothing then removes their
  finalizers. Delete the `DNSRecord`s (or set them to `Orphan`) before uninstalling.
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

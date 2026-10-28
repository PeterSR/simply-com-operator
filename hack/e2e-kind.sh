#!/usr/bin/env bash
# Smoke test of the chart and the image on a kind cluster, without talking to Simply.com (the API
# URL points at a closed port): the chart installs, the operator starts, the CRDs validate, and
# records report the problems they should.
#
#   hack/e2e-kind.sh            create a cluster, test, delete the cluster
#   KEEP_CLUSTER=1 hack/e2e-kind.sh
set -euo pipefail

cluster=${KIND_CLUSTER:-simply-com-operator-e2e}
image=simply-com-operator:e2e
ns=simply-com-operator
root=$(cd "$(dirname "$0")/.." && pwd)

created=
if ! kind get clusters | grep -qx "$cluster"; then
  kind create cluster --name "$cluster" --wait 120s
  created=1
fi
cleanup() {
  if [ -n "$created" ] && [ -z "${KEEP_CLUSTER:-}" ]; then
    kind delete cluster --name "$cluster"
  fi
}
trap cleanup EXIT
kubectl config use-context "kind-$cluster" >/dev/null

docker build -t "$image" --build-arg VERSION=e2e "$root"
kind load docker-image "$image" --name "$cluster"

kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$ns" create secret generic simply-com-api-key --from-literal=apiKey=not-a-real-key \
  --dry-run=client -o yaml | kubectl apply -f -
helm upgrade --install simply-com-operator "$root/charts/simply-com-operator" -n "$ns" \
  --set image.repository=simply-com-operator --set image.tag=e2e --set image.pullPolicy=Never \
  --set ownerID=e2e --set simplyAPI.url=http://127.0.0.1:9 \
  --set retryInterval=5s --wait --timeout 3m
kubectl apply -f - <<YAML
apiVersion: simplycom.petersr.github.io/v1alpha1
kind: ClusterAccount
metadata: {name: default}
spec:
  apiKeySecretRef: {name: simply-com-api-key, namespace: $ns}
  zones: [example.com]
  allowedNamespaces: [default]
YAML

wait_reason() { # object, reason, [kubectl flags]
  for _ in $(seq 1 60); do
    got=$(kubectl get "$1" "${@:3}" -o jsonpath='{.status.conditions[?(@.type=="Ready")].reason}' 2>/dev/null || true)
    if [ "$got" = "$2" ]; then
      echo "ok: $1 is $2"
      return 0
    fi
    sleep 2
  done
  echo "FAIL: $1 reason is '$got', want $2" >&2
  kubectl -n "$ns" logs deploy/simply-com-operator --tail=50 >&2
  return 1
}

kubectl apply -f - <<'YAML'
apiVersion: simplycom.petersr.github.io/v1alpha1
kind: DNSRecord
metadata: {name: no-account, namespace: default}
spec:
  deletionPolicy: Orphan
  accountRef: {name: missing}
  zone: example.com
  name: www
  type: A
  data: 192.0.2.1
---
apiVersion: simplycom.petersr.github.io/v1alpha1
kind: DNSRecord
metadata: {name: bad-address, namespace: default}
spec:
  deletionPolicy: Orphan
  accountRef: {name: default}
  zone: example.com
  name: www
  type: A
  data: not-an-address
---
apiVersion: simplycom.petersr.github.io/v1alpha1
kind: DNSRecord
metadata: {name: other-zone, namespace: default}
spec:
  accountRef: {name: default}
  zone: example.org
  name: www
  type: A
  data: 192.0.2.1
YAML
wait_reason dnsrecord/no-account AccountNotFound
wait_reason dnsrecord/bad-address InvalidSpec
wait_reason dnsrecord/other-zone ZoneNotAllowed

kubectl create namespace outsider --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f - <<'YAML'
apiVersion: simplycom.petersr.github.io/v1alpha1
kind: DNSRecord
metadata: {name: not-permitted, namespace: outsider}
spec:
  accountRef: {name: default}
  zone: example.com
  name: www
  type: A
  data: 192.0.2.1
  deletionPolicy: Orphan
YAML
wait_reason dnsrecord/not-permitted NotPermitted -n outsider

if kubectl apply -f - <<'YAML' 2>/dev/null; then
apiVersion: simplycom.petersr.github.io/v1alpha1
kind: DNSRecord
metadata: {name: mx-without-priority, namespace: default}
spec:
  accountRef: {name: default}
  zone: example.com
  name: "@"
  type: MX
  data: mail.example.com
YAML
  echo "FAIL: an MX record without priority was admitted" >&2
  exit 1
fi
echo "ok: MX without priority refused"

# Orphan, or a zone the account does not allow: deleting needs nothing from Simply.com.
kubectl delete dnsrecord no-account bad-address other-zone --timeout=60s
kubectl -n outsider delete dnsrecord not-permitted --timeout=60s
helm uninstall simply-com-operator -n "$ns" --wait
kubectl get crd dnsrecords.simplycom.petersr.github.io >/dev/null
echo "ok: CRDs kept after uninstall"
echo "e2e passed"

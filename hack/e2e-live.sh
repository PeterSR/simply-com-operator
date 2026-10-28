#!/usr/bin/env bash
# Exercise an installed operator against a real Simply.com zone. It creates a few records under
# <label>.<zone>, checks that Simply.com's name servers serve them, then deletes them again.
# Choose a label no real record lives under.
#
#   hack/e2e-live.sh <zone> [label] [account]
#
# Needs kubectl access to a cluster running the operator with a ready ClusterAccount, and dig.
set -euo pipefail

zone=${1:?usage: $0 <zone> [label] [account]}
label=${2:-simply-com-operator-test}
account=${3:-default}
ns=simply-com-operator-live-test

kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
trap 'kubectl delete namespace "$ns" --wait=true --timeout=120s >/dev/null' EXIT

record() { # name type data [extra yaml]
  cat <<YAML
---
apiVersion: simplycom.petersr.github.io/v1alpha1
kind: DNSRecord
metadata: {name: $1, namespace: $ns}
spec:
  accountRef: {name: $account}
  zone: $zone
  name: $1.$label
  type: $2
  data: "$3"
  ttl: 600
  comment: simply-com-operator live test
${4:-}
YAML
}
{
  record a A 192.0.2.1
  record aaaa AAAA 2001:db8::1
  record txt TXT "simply-com-operator live test"
  record cname CNAME example.com
  record mx MX mail.example.com "  priority: 10"
} | kubectl apply -f -
kubectl -n "$ns" wait dnsrecord --all --for=condition=Ready --timeout=120s

check() { # type name want
  for _ in $(seq 1 24); do
    if dig +short "$1" "$2.$label.$zone" @ns1.simply.com | grep -qF "$3"; then
      echo "ok: $1 $2.$label.$zone"
      return 0
    fi
    sleep 5
  done
  echo "FAIL: $1 $2.$label.$zone not served" >&2
  return 1
}
check A a 192.0.2.1
check AAAA aaaa 2001:db8::1
check TXT txt "simply-com-operator live test"
check CNAME cname example.com
check MX mx mail.example.com

kubectl -n "$ns" delete dnsrecord --all --timeout=120s
echo "live test passed; records removed"

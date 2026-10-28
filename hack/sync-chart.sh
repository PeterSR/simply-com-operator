#!/usr/bin/env bash
# Copies the generated CustomResourceDefinitions and the manager's ClusterRole rules into the Helm
# chart. Run by `make manifests`; CI fails when the chart differs from what this produces.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
chart=$root/charts/simply-com-operator

rm -f "$chart"/templates/crds/*.yaml
for crd in "$root"/config/crd/bases/*.yaml; do
  out=$chart/templates/crds/$(basename "$crd" | sed 's/^simplycom\.petersr\.github\.io_//')
  {
    echo '{{- if .Values.crds.install }}'
    sed -e '/^---$/d' \
        -e 's/^  annotations:$/  annotations:\n    {{- if .Values.crds.keep }}\n    helm.sh\/resource-policy: keep\n    {{- end }}/' \
        "$crd"
    echo '{{- end }}'
  } > "$out"
done

{
  cat <<'YAML'
# Generated from config/rbac/role.yaml by hack/sync-chart.sh; do not edit.
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: {{ include "simply-com-operator.fullname" . }}
  labels:
    {{- include "simply-com-operator.labels" . | nindent 4 }}
YAML
  sed -n '/^rules:$/,$p' "$root/config/rbac/role.yaml"
} > "$chart/templates/clusterrole.yaml"

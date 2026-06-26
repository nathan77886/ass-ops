#!/usr/bin/env bash
set -euo pipefail

namespace="${ASSOPS_HELM_PREFLIGHT_NAMESPACE:-assops-test}"
release="${ASSOPS_HELM_PREFLIGHT_RELEASE:-assops}"
chart_name="${ASSOPS_HELM_PREFLIGHT_CHART_NAME:-assops}"
values_file="${ASSOPS_HELM_PREFLIGHT_VALUES:-deploy/helm/assops/values.test.example.yaml}"
app_secret="${ASSOPS_HELM_PREFLIGHT_APP_SECRET:-assops-test-secret}"
kubeconfig_secret="${ASSOPS_HELM_PREFLIGHT_KUBECONFIG_SECRET:-assops-kubeconfigs}"
kubeconfig_key="${ASSOPS_HELM_PREFLIGHT_KUBECONFIG_KEY:-test-assops-reader.yaml}"

if [[ -n "${ASSOPS_HELM_PREFLIGHT_FULLNAME:-}" ]]; then
  fullname="$ASSOPS_HELM_PREFLIGHT_FULLNAME"
elif [[ "$release" == *"$chart_name"* ]]; then
  fullname="$release"
else
  fullname="${release}-${chart_name}"
fi

need() {
  command -v "$1" >/dev/null || {
    echo "$1 is required for helm-test-preflight" >&2
    exit 1
  }
}

require_secret_key() {
  local secret_name="$1"
  local key="$2"
  local present

  present="$(kubectl -n "$namespace" get secret "$secret_name" -o json 2>/dev/null | python3 -c '
import json
import sys

key = sys.argv[1]
try:
    data = json.load(sys.stdin).get("data", {})
except json.JSONDecodeError:
    print("missing")
    raise SystemExit(0)
print("present" if data.get(key) else "missing")
' "$key" || true)"
  if [[ "$present" != "present" ]]; then
    echo "secret ${secret_name} in namespace ${namespace} is missing required key ${key}" >&2
    return 1
  fi
}

need helm
need kubectl
need python3

if [[ ! -f "$values_file" ]]; then
  echo "values file not found: $values_file" >&2
  exit 1
fi

helm lint deploy/helm/assops -f "$values_file"
helm template "$release" deploy/helm/assops -n "$namespace" -f "$values_file" >/tmp/assops-test-preflight-rendered.yaml

kubectl get namespace "$namespace" >/dev/null

required_app_keys=(
  DATABASE_URL
  ASSOPS_JWT_SECRET
  ASSOPS_WEBHOOK_SECRET_KEY
  ASSOPS_ADMIN_EMAIL
  ASSOPS_ADMIN_PASSWORD
  ASSOPS_APPROVAL_WEBHOOK_TOKEN
  ASSOPS_GITHUB_ACTIONS_READ_TOKEN
  ASSOPS_ARGO_READ_TOKEN
)

for key in "${required_app_keys[@]}"; do
  require_secret_key "$app_secret" "$key"
done

require_secret_key "$kubeconfig_secret" "$kubeconfig_key"

echo "helm-test-preflight passed for ${fullname} in namespace ${namespace}"

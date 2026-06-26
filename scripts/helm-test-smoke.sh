#!/usr/bin/env bash
set -euo pipefail

namespace="${ASSOPS_HELM_SMOKE_NAMESPACE:-assops-test}"
release="${ASSOPS_HELM_SMOKE_RELEASE:-assops}"
chart_name="${ASSOPS_HELM_SMOKE_CHART_NAME:-assops}"
if [[ -n "${ASSOPS_HELM_SMOKE_FULLNAME:-}" ]]; then
  fullname="$ASSOPS_HELM_SMOKE_FULLNAME"
elif [[ "$release" == *"$chart_name"* ]]; then
  fullname="$release"
else
  fullname="${release}-${chart_name}"
fi
web_local_port="${ASSOPS_HELM_SMOKE_WEB_PORT:-18080}"
worker_local_port="${ASSOPS_HELM_SMOKE_WORKER_PORT:-18081}"
node_worker_local_port="${ASSOPS_HELM_SMOKE_NODE_WORKER_PORT:-18082}"

need() {
  command -v "$1" >/dev/null || {
    echo "$1 is required for helm-test-smoke" >&2
    exit 1
  }
}

json_field() {
  python3 -c '
import json
import sys

key = sys.argv[1]
data = json.load(sys.stdin)
value = data
for part in key.split("."):
    if not isinstance(value, dict) or part not in value:
        raise SystemExit(f"missing JSON field: {key}")
    value = value[part]
if isinstance(value, bool):
    print("true" if value else "false")
else:
    print(value)
' "$1"
}

port_forward_pids=()
cleanup() {
  for pid in "${port_forward_pids[@]}"; do
    kill "$pid" 2>/dev/null || true
  done
}
trap cleanup EXIT

start_port_forward() {
  local service="$1"
  local local_port="$2"
  local service_port="$3"
  local log="/tmp/${release}-${service}-port-forward.log"

  kubectl -n "$namespace" port-forward "svc/${fullname}-${service}" "${local_port}:${service_port}" >"$log" 2>&1 &
  local pid="$!"
  port_forward_pids+=("$pid")

  for _ in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1:${local_port}/healthz" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done

  cat "$log" >&2
  echo "port-forward for ${fullname}-${service} did not become healthy" >&2
  return 1
}

check_component() {
  local url="$1"
  local component="$2"
  local response

  response="$(curl -fsS "$url")"
  if [[ "$(printf '%s' "$response" | json_field ok)" != "true" ]]; then
    echo "unexpected ${component} health ok flag: $response" >&2
    return 1
  fi
  if [[ "$(printf '%s' "$response" | json_field component)" != "$component" ]]; then
    echo "unexpected ${component} health payload: $response" >&2
    return 1
  fi
}

need kubectl
need curl
need python3

kubectl -n "$namespace" rollout status "deployment/${fullname}-gateway" --timeout=180s
kubectl -n "$namespace" rollout status "deployment/${fullname}-worker" --timeout=180s
kubectl -n "$namespace" rollout status "deployment/${fullname}-node-worker" --timeout=180s
kubectl -n "$namespace" rollout status "deployment/${fullname}-web" --timeout=180s
kubectl -n "$namespace" get endpoints "${fullname}-worker-health" -o jsonpath='{.subsets[*].addresses[*].ip}' | grep -q .
kubectl -n "$namespace" get endpoints "${fullname}-node-worker-health" -o jsonpath='{.subsets[*].addresses[*].ip}' | grep -q .

start_port_forward web "$web_local_port" 80
start_port_forward worker-health "$worker_local_port" 8081
start_port_forward node-worker-health "$node_worker_local_port" 8082

ASSOPS_GATEWAY_URL="http://127.0.0.1:${web_local_port}" bash scripts/api-smoke.sh
check_component "http://127.0.0.1:${worker_local_port}/healthz" worker
check_component "http://127.0.0.1:${node_worker_local_port}/healthz" node-worker

echo "helm-test-smoke passed for ${fullname} in namespace ${namespace}"

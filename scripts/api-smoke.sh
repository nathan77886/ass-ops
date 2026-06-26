#!/usr/bin/env bash
set -euo pipefail

base_url="${ASSOPS_GATEWAY_URL:-http://localhost:8080}"
email="${ASSOPS_ADMIN_EMAIL:-admin@assops.local}"
password="${ASSOPS_ADMIN_PASSWORD:-admin1234}"

need() {
  command -v "$1" >/dev/null || {
    echo "$1 is required for api-smoke" >&2
    exit 1
  }
}

json_field() {
  python3 -c '
import json
import sys

key = sys.argv[1]
try:
    data = json.load(sys.stdin)
except json.JSONDecodeError as exc:
    raise SystemExit(f"invalid JSON response: {exc}")
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

need curl
need python3

health="$(curl -fsS "$base_url/healthz")"
component="$(printf '%s' "$health" | json_field component)"
if [[ "$component" != "gateway" ]]; then
  echo "unexpected health component: $component" >&2
  exit 1
fi

login_payload="$(
  EMAIL="$email" PASSWORD="$password" python3 - <<'PY'
import json
import os

print(json.dumps({"email": os.environ["EMAIL"], "password": os.environ["PASSWORD"]}))
PY
)"
login="$(curl -fsS "$base_url/api/auth/login" \
  -H 'content-type: application/json' \
  -d "$login_payload")"
token="$(printf '%s' "$login" | json_field token)"
if [[ -z "$token" || "$token" == "null" ]]; then
  echo "login did not return a token" >&2
  exit 1
fi

projects="$(curl -fsS "$base_url/api/projects" \
  -H "authorization: Bearer $token")"
printf '%s' "$projects" | json_field items >/dev/null

queue_summary="$(curl -fsS "$base_url/api/worker-queue/summary" \
  -H "authorization: Bearer $token")"
printf '%s' "$queue_summary" | json_field queued >/dev/null
printf '%s' "$queue_summary" | json_field running >/dev/null

echo "api-smoke passed for $base_url"

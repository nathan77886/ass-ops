#!/usr/bin/env bash
set -euo pipefail

base_url="${ASSOPS_DOCKER_RUNTIME_BASE_URL:-http://127.0.0.1:28082}"
cloudflare_url="${ASSOPS_CLOUDFLARE_URL:-https://ass-ops-api.4nathan.com}"
worker_url="${ASSOPS_WORKER_FRONTEND_URL:-https://ass-ops.4nathan.com}"
output="${ASSOPS_CLOUDFLARE_DOCKER_RUNTIME_EVIDENCE_OUTPUT:-.assops/release-notes/cloudflare-docker-runtime-evidence.json}"
pg_container="${ASSOPS_PG18_CONTAINER:-pg1}"
pg_user="${ASSOPS_PG18_ADMIN_USER:-nas}"
pg_database="${ASSOPS_PG18_DATABASE:-assops}"
require_project="${ASSOPS_CLOUDFLARE_DOCKER_RUNTIME_REQUIRE_PROJECT:-false}"

need() {
  command -v "$1" >/dev/null || {
    echo "$1 is required for cloudflare-docker-runtime-evidence" >&2
    exit 1
  }
}

need curl
need docker
need python3

containers=(
  assops-live-pg18-gateway
  assops-live-pg18-worker
  assops-live-pg18-node-worker
  assops-live-pg18-web
)

for container in "${containers[@]}"; do
  state="$(docker inspect -f '{{.State.Status}}' "$container" 2>/dev/null || true)"
  if [[ "$state" != "running" ]]; then
    echo "runtime container is not running: $container" >&2
    exit 1
  fi
done

schema_table_count="$(docker exec "$pg_container" psql -U "$pg_user" -d "$pg_database" -v ON_ERROR_STOP=1 -tAc \
  "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('users','projects','connection_credentials','provider_accounts','git_remotes','ai_runtimes','argo_connections','ssh_machines','operation_runs','worker_jobs','assets','provider_review_attempts');")"
if [[ "$schema_table_count" != "12" ]]; then
  echo "PG18 GORM schema incomplete: got $schema_table_count/12 required tables" >&2
  exit 1
fi

ASSOPS_GATEWAY_URL="$base_url" \
ASSOPS_ADMIN_EMAIL="${ASSOPS_ADMIN_EMAIL:-admin@assops.local}" \
ASSOPS_ADMIN_PASSWORD="${ASSOPS_ADMIN_PASSWORD:-admin1234}" \
ASSOPS_API_SMOKE_REQUIRE_PROJECT="$require_project" \
  bash scripts/api-smoke.sh >/tmp/assops-cloudflare-docker-runtime-local-smoke.log

ASSOPS_GATEWAY_URL="$cloudflare_url" \
ASSOPS_ADMIN_EMAIL="${ASSOPS_ADMIN_EMAIL:-admin@assops.local}" \
ASSOPS_ADMIN_PASSWORD="${ASSOPS_ADMIN_PASSWORD:-admin1234}" \
ASSOPS_API_SMOKE_REQUIRE_PROJECT="$require_project" \
  bash scripts/api-smoke.sh >/tmp/assops-cloudflare-docker-runtime-public-smoke.log

cf_meta="$(curl -sS -o /tmp/assops-cloudflare-docker-runtime-auth.body -w '%{http_code} %{content_type}' --max-time 20 \
  "$cloudflare_url/api/auth/me")"
cf_code="${cf_meta%% *}"
cf_type="${cf_meta#* }"
if [[ "$cf_code" != "401" || "$cf_type" != application/json* ]]; then
  echo "cloudflare api route is not wired to gateway: $cf_code $cf_type" >&2
  exit 1
fi

worker_api_meta="$(curl -sS -o /tmp/assops-cloudflare-worker-api.body -w '%{http_code} %{content_type}' --max-time 20 \
  "$worker_url/api/auth/me")"
worker_api_code="${worker_api_meta%% *}"
worker_api_type="${worker_api_meta#* }"
if [[ "$worker_api_code" != "401" || "$worker_api_type" != application/json* ]]; then
  echo "worker frontend /api proxy is not wired to gateway: $worker_api_code $worker_api_type" >&2
  exit 1
fi

worker_index_meta="$(curl -sS -o /tmp/assops-cloudflare-worker-index.body -w '%{http_code} %{content_type}' --max-time 20 \
  "$worker_url/")"
worker_index_code="${worker_index_meta%% *}"
worker_index_type="${worker_index_meta#* }"
if [[ "$worker_index_code" != "200" || "$worker_index_type" != text/html* ]]; then
  echo "worker frontend index is not serving HTML: $worker_index_code $worker_index_type" >&2
  exit 1
fi

mkdir -p "$(dirname "$output")"
python3 - "$output" "$base_url" "$cloudflare_url" "$worker_url" "$schema_table_count" "$require_project" "${containers[@]}" <<'PY'
import json
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path

output = Path(sys.argv[1])
base_url = sys.argv[2]
cloudflare_url = sys.argv[3]
worker_url = sys.argv[4]
schema_table_count = int(sys.argv[5])
requires_seeded_project = sys.argv[6].lower() == "true"
containers = sys.argv[7:]

container_status = {}
for name in containers:
    status = subprocess.check_output(
        ["docker", "inspect", "-f", "{{.State.Status}}", name],
        text=True,
    ).strip()
    container_status[name] = status

data = {
    "schema": "assops.cloudflare_docker_runtime_evidence.v1",
    "verified_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
    "public_url": cloudflare_url,
    "worker_frontend_url": worker_url,
    "local_url": base_url,
    "entrypoint_check": {
        "path": "/api/auth/me",
        "expected_status": 401,
        "expected_content_type_prefix": "application/json",
        "result": "passed",
    },
    "worker_frontend_check": {
        "index_path": "/",
        "index_expected_status": 200,
        "api_proxy_path": "/api/auth/me",
        "api_proxy_expected_status": 401,
        "api_proxy_expected_content_type_prefix": "application/json",
        "result": "passed",
    },
    "smoke": {
        "local_api_smoke": "passed",
        "public_api_smoke": "passed",
        "requires_seeded_project": requires_seeded_project,
    },
    "postgres": {
        "container": "pg1",
        "database": "assops",
        "schema_manager": "gorm_auto_migrate",
        "required_schema_tables": 12,
        "schema_table_count": schema_table_count,
    },
    "docker_runtime": {
        "orchestrator": "docker",
        "k3s_used": False,
        "containers": container_status,
    },
    "secret_policy": {
        "contains_token_values": False,
        "contains_database_password": False,
        "contains_kubeconfig": False,
    },
}
output.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")
print(f"cloudflare docker runtime evidence written to {output}")
PY

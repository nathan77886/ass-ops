#!/usr/bin/env bash
set -euo pipefail

base_url="${ASSOPS_DOCKER_RUNTIME_BASE_URL:-http://127.0.0.1:28082}"
cloudflare_url="${ASSOPS_CLOUDFLARE_URL:-https://ass-ops-api.4nathan.com}"
pg_container="${ASSOPS_PG18_CONTAINER:-pg1}"
pg_user="${ASSOPS_PG18_ADMIN_USER:-nas}"
pg_database="${ASSOPS_PG18_DATABASE:-assops}"
require_cloudflare_api_json="${ASSOPS_REQUIRE_CLOUDFLARE_API_JSON:-false}"

need() {
  command -v "$1" >/dev/null || {
    echo "$1 is required for docker-pg18-runtime-check" >&2
    exit 1
  }
}

need curl
need docker
need python3

for container in \
  assops-live-pg18-gateway \
  assops-live-pg18-worker \
  assops-live-pg18-node-worker \
  assops-live-pg18-web; do
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
ASSOPS_API_SMOKE_PROJECT_SLUG="assops-demo" \
ASSOPS_API_SMOKE_REQUIRE_PROJECT=true \
  bash scripts/api-smoke.sh

cf_meta="$(curl -sS -o /tmp/assops-cloudflare-api-check.body -w '%{http_code} %{content_type}' --max-time 10 \
  "$cloudflare_url/api/auth/me" || true)"
cf_code="${cf_meta%% *}"
cf_type="${cf_meta#* }"
if [[ "$cf_type" == application/json* ]]; then
  echo "cloudflare api route returns JSON: $cf_code $cf_type"
elif [[ "$require_cloudflare_api_json" == "true" ]]; then
  echo "cloudflare api route is not wired to gateway: $cf_code $cf_type" >&2
  exit 1
else
  echo "cloudflare api route pending: $cf_code $cf_type"
fi

echo "docker PG18 runtime check passed for $base_url with GORM schema tables: $schema_table_count/12"

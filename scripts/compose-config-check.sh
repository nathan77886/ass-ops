#!/usr/bin/env bash
set -euo pipefail

need() {
  command -v "$1" >/dev/null || {
    echo "$1 is required for compose-config-check" >&2
    exit 1
  }
}

need docker

docker compose -f deploy/docker-compose.yml config --quiet

ASSOPS_POSTGRES_PASSWORD=compose-check-postgres \
ASSOPS_GATEWAY_URL=http://localhost:8080 \
ASSOPS_JWT_SECRET=compose-check-jwt \
ASSOPS_WEBHOOK_SECRET_KEY=compose-check-webhook \
ASSOPS_ADMIN_EMAIL=admin@assops.local \
ASSOPS_ADMIN_PASSWORD=compose-check-admin \
  docker compose -f deploy/compose.prod.yml config --quiet

for migration in backend/migrations/*.sql; do
  name="$(basename "$migration")"
  perm="$(stat -c '%a' "$migration")"
  if (( (8#$perm & 4) == 0 )); then
    echo "${migration} must be world-readable so postgres fresh-volume init can read the bind mount" >&2
    exit 1
  fi
  if ! rg -q "backend/migrations/${name}:/docker-entrypoint-initdb.d/${name}:ro" deploy/docker-compose.yml; then
    echo "deploy/docker-compose.yml is missing migration ${name}" >&2
    exit 1
  fi
  if ! rg -q "backend/migrations/${name}:/docker-entrypoint-initdb.d/${name}:ro" deploy/compose.prod.yml; then
    echo "deploy/compose.prod.yml is missing migration ${name}" >&2
    exit 1
  fi
done

echo "compose-config-check passed"

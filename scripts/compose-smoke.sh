#!/usr/bin/env bash
set -euo pipefail

project="${ASSOPS_COMPOSE_SMOKE_PROJECT:-assops-compose-smoke}"
admin_email="${ASSOPS_ADMIN_EMAIL:-admin@assops.local}"
admin_password="${ASSOPS_ADMIN_PASSWORD:-admin1234}"
web_port="${ASSOPS_COMPOSE_SMOKE_WEB_PORT:-}"
build_images="${ASSOPS_COMPOSE_SMOKE_BUILD:-true}"

need() {
  command -v "$1" >/dev/null || {
    echo "$1 is required for compose-smoke" >&2
    exit 1
  }
}

free_port() {
  python3 - <<'PY'
import socket

with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
}

compose() {
  docker compose -p "$project" -f deploy/docker-compose.yml "$@"
}

container_health() {
  local service="$1"
  local container_id
  container_id="$(compose ps -q "$service")"
  if [[ -z "$container_id" ]]; then
    echo "missing"
    return
  fi
  docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container_id"
}

wait_for_health() {
  local service="$1"
  local expected="${2:-healthy}"
  local status=""

  for _ in $(seq 1 90); do
    status="$(container_health "$service")"
    if [[ "$status" == "$expected" ]]; then
      return 0
    fi
    if [[ "$status" == "exited" || "$status" == "dead" || "$status" == "missing" ]]; then
      compose logs --no-color "$service" >&2 || true
      echo "${service} did not start cleanly; status=${status}" >&2
      return 1
    fi
    sleep 2
  done

  compose ps >&2 || true
  compose logs --no-color "$service" >&2 || true
  echo "${service} did not become ${expected}; last status=${status}" >&2
  return 1
}

need docker
need curl
need python3

if [[ -z "$web_port" ]]; then
  web_port="$(free_port)"
fi

cleanup() {
  compose down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

cleanup

compose_build_args=(--build)
case "$build_images" in
  true)
    compose_build_args=(--build)
    ;;
  false)
    compose_build_args=(--no-build)
    ;;
  *)
    echo "ASSOPS_COMPOSE_SMOKE_BUILD must be true or false" >&2
    exit 1
    ;;
esac

ASSOPS_WEB_PORT="$web_port" \
ASSOPS_ADMIN_EMAIL="$admin_email" \
ASSOPS_ADMIN_PASSWORD="$admin_password" \
  compose up -d "${compose_build_args[@]}" postgres gateway worker node-worker web || {
    if [[ "$build_images" == "true" ]]; then
      cat >&2 <<'EOF'
compose-smoke could not build or start the stack.
If the error is a Docker registry metadata or EOF failure, retry after the configured registry mirror is reachable, or prebuild the project images and run with ASSOPS_COMPOSE_SMOKE_BUILD=false.
EOF
    else
      cat >&2 <<'EOF'
compose-smoke could not start from existing images.
ASSOPS_COMPOSE_SMOKE_BUILD=false requires current local Compose images for the selected ASSOPS_COMPOSE_SMOKE_PROJECT; rebuild once before using no-build mode.
EOF
    fi
    exit 1
  }

wait_for_health postgres
wait_for_health gateway
wait_for_health worker
wait_for_health node-worker

compose exec -T gateway assops-tool db seed-demo >/tmp/assops-compose-smoke-seed-demo.log

ASSOPS_GATEWAY_URL="http://127.0.0.1:${web_port}" \
ASSOPS_ADMIN_EMAIL="$admin_email" \
ASSOPS_ADMIN_PASSWORD="$admin_password" \
ASSOPS_API_SMOKE_REQUIRE_PROJECT=true \
  bash scripts/api-smoke.sh || {
    if [[ "$build_images" == "false" ]]; then
      cat >&2 <<'EOF'
compose-smoke API checks failed while using ASSOPS_COMPOSE_SMOKE_BUILD=false.
Existing local Compose images may be stale; rebuild with ASSOPS_COMPOSE_SMOKE_BUILD=true before treating this as an application regression.
EOF
    fi
    exit 1
  }

echo "compose-smoke passed for project ${project} on http://127.0.0.1:${web_port}"

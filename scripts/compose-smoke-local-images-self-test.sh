#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

cat >"$tmpdir/go" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
out=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o)
      out="$2"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
if [[ -z "$out" ]]; then
  echo "fake go expected -o" >&2
  exit 1
fi
mkdir -p "$(dirname "$out")"
printf '#!/bin/sh\nexit 0\n' >"$out"
chmod +x "$out"
printf 'go build %s\n' "$out" >>"$ASSOPS_COMPOSE_SMOKE_LOCAL_IMAGES_STUB_LOG"
SH
chmod +x "$tmpdir/go"

cat >"$tmpdir/docker" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
{
  printf 'docker'
  for arg in "$@"; do
    printf ' %s' "$arg"
  done
  printf '\n'
} >>"$ASSOPS_COMPOSE_SMOKE_LOCAL_IMAGES_STUB_LOG"
if [[ "$1" == "image" && "$2" == "inspect" ]]; then
  if [[ "${ASSOPS_COMPOSE_SMOKE_LOCAL_IMAGES_STUB_MODE:-ok}" == "missing_base" && "$3" == "missing-base:latest" ]]; then
    exit 1
  fi
  exit 0
fi
if [[ "$1" == "build" ]]; then
  cat >>"$ASSOPS_COMPOSE_SMOKE_LOCAL_IMAGES_DOCKERFILE_LOG"
  printf '\n---\n' >>"$ASSOPS_COMPOSE_SMOKE_LOCAL_IMAGES_DOCKERFILE_LOG"
  exit 0
fi
exit 0
SH
chmod +x "$tmpdir/docker"

mkdir -p "$tmpdir/dist/assets"
printf '<!doctype html>\n' >"$tmpdir/dist/index.html"
printf 'asset\n' >"$tmpdir/dist/assets/index.js"

log="$tmpdir/commands.log"
dockerfile_log="$tmpdir/dockerfiles.log"

PATH="$tmpdir:$PATH" \
ASSOPS_COMPOSE_SMOKE_LOCAL_IMAGES_STUB_LOG="$log" \
ASSOPS_COMPOSE_SMOKE_LOCAL_IMAGES_DOCKERFILE_LOG="$dockerfile_log" \
ASSOPS_COMPOSE_SMOKE_PROJECT=assops-selftest \
ASSOPS_COMPOSE_SMOKE_LOCAL_BASE_IMAGE=local-base:latest \
ASSOPS_COMPOSE_SMOKE_WEB_DIST_DIR="$tmpdir/dist" \
bash "$repo_root/scripts/compose-smoke-local-images.sh" >/tmp/assops-compose-smoke-local-images-ok.log

for binary in gateway worker node-worker assops-tool; do
  grep -q "go build .*${binary}" "$log"
done
for image in gateway worker node-worker web; do
  grep -q -- "docker build -q -t assops-selftest-${image}" "$log"
  grep -q -- "docker image inspect assops-selftest-${image}" "$log"
done
grep -q "FROM local-base:latest" "$dockerfile_log"
grep -q 'ENTRYPOINT \["gateway"\]' "$dockerfile_log"
grep -q 'ENTRYPOINT \["worker"\]' "$dockerfile_log"
grep -q 'ENTRYPOINT \["node-worker"\]' "$dockerfile_log"
grep -q 'COPY web/dist /usr/share/nginx/html' "$dockerfile_log"
grep -q "compose-smoke local images built for project assops-selftest" /tmp/assops-compose-smoke-local-images-ok.log

if PATH="$tmpdir:$PATH" \
  ASSOPS_COMPOSE_SMOKE_LOCAL_IMAGES_STUB_LOG="$tmpdir/missing-base.log" \
  ASSOPS_COMPOSE_SMOKE_LOCAL_IMAGES_DOCKERFILE_LOG="$tmpdir/missing-base-dockerfiles.log" \
  ASSOPS_COMPOSE_SMOKE_PROJECT=assops-selftest \
  ASSOPS_COMPOSE_SMOKE_LOCAL_BASE_IMAGE=missing-base:latest \
  ASSOPS_COMPOSE_SMOKE_WEB_DIST_DIR="$tmpdir/dist" \
  ASSOPS_COMPOSE_SMOKE_LOCAL_IMAGES_STUB_MODE=missing_base \
  bash "$repo_root/scripts/compose-smoke-local-images.sh" >/tmp/assops-compose-smoke-local-images-missing-base.log 2>&1; then
  echo "expected compose-smoke-local-images to reject missing base image" >&2
  exit 1
fi
grep -q "base image not found locally: missing-base:latest" /tmp/assops-compose-smoke-local-images-missing-base.log

if PATH="$tmpdir:$PATH" \
  ASSOPS_COMPOSE_SMOKE_LOCAL_IMAGES_STUB_LOG="$tmpdir/missing-dist.log" \
  ASSOPS_COMPOSE_SMOKE_LOCAL_IMAGES_DOCKERFILE_LOG="$tmpdir/missing-dist-dockerfiles.log" \
  ASSOPS_COMPOSE_SMOKE_WEB_DIST_DIR="$tmpdir/missing-dist" \
  bash "$repo_root/scripts/compose-smoke-local-images.sh" >/tmp/assops-compose-smoke-local-images-missing-dist.log 2>&1; then
  echo "expected compose-smoke-local-images to reject missing web dist" >&2
  exit 1
fi
grep -q "missing-dist is required" /tmp/assops-compose-smoke-local-images-missing-dist.log

echo "compose-smoke-local-images self-test passed"

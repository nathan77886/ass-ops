#!/usr/bin/env bash
set -euo pipefail

project="${ASSOPS_COMPOSE_SMOKE_PROJECT:-assops-compose-smoke}"
base_image="${ASSOPS_COMPOSE_SMOKE_LOCAL_BASE_IMAGE:-nginx:1.27-alpine}"
web_dist="${ASSOPS_COMPOSE_SMOKE_WEB_DIST_DIR:-web/dist}"

need() {
  command -v "$1" >/dev/null || {
    echo "$1 is required for compose-smoke-local-images" >&2
    exit 1
  }
}

need docker
need go

if [[ ! -d "$web_dist" ]]; then
  echo "$web_dist is required; run pnpm -C web build or make first-deployable-check first" >&2
  exit 1
fi

if ! docker image inspect "$base_image" >/dev/null 2>&1; then
  echo "base image not found locally: $base_image" >&2
  echo "Pull or import $base_image, or set ASSOPS_COMPOSE_SMOKE_LOCAL_BASE_IMAGE to an existing local image." >&2
  exit 1
fi

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

mkdir -p "$tmpdir/bin" "$tmpdir/web" "$tmpdir/deploy"

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$tmpdir/bin/gateway" ./backend/cmd/gateway
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$tmpdir/bin/worker" ./backend/cmd/worker
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$tmpdir/bin/node-worker" ./backend/cmd/node-worker
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$tmpdir/bin/assops-tool" ./backend/cmd/assops-tool

cp -R "$web_dist" "$tmpdir/web/dist"
cp deploy/nginx.conf "$tmpdir/deploy/nginx.conf"

docker_build() {
  local image="$1"
  local dockerfile="$2"
  docker build -q -t "$image" -f - "$tmpdir" <<<"$dockerfile" >/dev/null
  docker image inspect "$image" >/dev/null
}

docker_build "${project}-gateway" "FROM ${base_image}
WORKDIR /app
COPY bin/assops-tool /usr/local/bin/assops-tool
COPY bin/gateway /usr/local/bin/gateway
ENTRYPOINT [\"gateway\"]"

docker_build "${project}-worker" "FROM ${base_image}
WORKDIR /app
COPY bin/assops-tool /usr/local/bin/assops-tool
COPY bin/worker /usr/local/bin/worker
ENTRYPOINT [\"worker\"]"

docker_build "${project}-node-worker" "FROM ${base_image}
WORKDIR /app
COPY bin/node-worker /usr/local/bin/node-worker
ENTRYPOINT [\"node-worker\"]"

docker_build "${project}-web" "FROM ${base_image}
COPY deploy/nginx.conf /etc/nginx/conf.d/default.conf
COPY web/dist /usr/share/nginx/html
EXPOSE 80"

echo "compose-smoke local images built for project ${project} from ${base_image}"
echo "Run with ASSOPS_COMPOSE_SMOKE_BUILD=false make compose-smoke"

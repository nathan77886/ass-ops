#!/usr/bin/env bash
set -euo pipefail

project="${ASSOPS_DOCKER_RUNTIME_PROJECT:-assops-live-pg18}"
network="${ASSOPS_DOCKER_RUNTIME_NETWORK:-dev_network}"
runtime_base="${ASSOPS_DOCKER_RUNTIME_BASE_IMAGE:-assops-smoke-artifacts-gateway:latest}"
web_base="${ASSOPS_DOCKER_RUNTIME_WEB_BASE_IMAGE:-nginx:1.27-alpine}"
web_port="${ASSOPS_DOCKER_RUNTIME_WEB_PORT:-28082}"
pg_host="${ASSOPS_PG18_HOST:-pg1}"
pg_container="${ASSOPS_PG18_CONTAINER:-pg1}"
pg_admin_user="${ASSOPS_PG18_ADMIN_USER:-nas}"
pg_database="${ASSOPS_PG18_DATABASE:-assops}"
db_user="${ASSOPS_DB_USER:-assops}"
db_password="${ASSOPS_DB_PASSWORD:-assops}"
admin_email="${ASSOPS_ADMIN_EMAIL:-admin@assops.local}"
admin_password="${ASSOPS_ADMIN_PASSWORD:-admin1234}"
public_url="${ASSOPS_PUBLIC_URL:-https://ass-ops-api.4nathan.com}"
web_dist="${ASSOPS_DOCKER_RUNTIME_WEB_DIST_DIR:-web/dist}"

need() {
  command -v "$1" >/dev/null || {
    echo "$1 is required for docker-pg18-runtime-up" >&2
    exit 1
  }
}

need docker
need go
need python3

if [[ ! -d "$web_dist" ]]; then
  echo "$web_dist is required; run pnpm -C web build or make first-deployable-check first" >&2
  exit 1
fi

for image in "$runtime_base" "$web_base"; do
  if ! docker image inspect "$image" >/dev/null 2>&1; then
    echo "base image not found locally: $image" >&2
    exit 1
  fi
done

if ! docker network inspect "$network" >/dev/null 2>&1; then
  docker network create "$network" >/dev/null
fi

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "$tmpdir"
}
trap cleanup EXIT HUP INT TERM

mkdir -p "$tmpdir/bin" "$tmpdir/backend" "$tmpdir/web" "$tmpdir/deploy"

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$tmpdir/bin/gateway" ./backend/cmd/gateway
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$tmpdir/bin/worker" ./backend/cmd/worker
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$tmpdir/bin/node-worker" ./backend/cmd/node-worker
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$tmpdir/bin/assops-tool" ./backend/cmd/assops-tool

cp -R backend/migrations "$tmpdir/backend/migrations"
cp -R "$web_dist" "$tmpdir/web/dist"
cp deploy/nginx.conf "$tmpdir/deploy/nginx.conf"

docker build --pull=false -q -t "${project}-runtime:local" -f- "$tmpdir" <<DOCKERFILE >/dev/null
FROM ${runtime_base}
WORKDIR /app
COPY backend/migrations ./backend/migrations
COPY bin/assops-tool /usr/local/bin/assops-tool
COPY bin/gateway /usr/local/bin/gateway
COPY bin/worker /usr/local/bin/worker
COPY bin/node-worker /usr/local/bin/node-worker
DOCKERFILE

docker build --pull=false -q -t "${project}-web:local" -f- "$tmpdir" <<DOCKERFILE >/dev/null
FROM ${web_base}
COPY deploy/nginx.conf /etc/nginx/conf.d/default.conf
COPY web/dist /usr/share/nginx/html
EXPOSE 80
DOCKERFILE

if ! docker exec "$pg_container" psql -U "$pg_admin_user" -d "$pg_database" -v ON_ERROR_STOP=1 -tAc "SELECT 1" >/dev/null 2>&1; then
  docker exec "$pg_container" createdb -U "$pg_admin_user" "$pg_database"
fi

docker exec -i "$pg_container" psql -U "$pg_admin_user" -d "$pg_database" -v ON_ERROR_STOP=1 >/dev/null <<SQL
DO \$\$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '${db_user}') THEN
    CREATE ROLE ${db_user} LOGIN PASSWORD '${db_password}';
  END IF;
END
\$\$;
ALTER DATABASE ${pg_database} OWNER TO ${db_user};
ALTER SCHEMA public OWNER TO ${db_user};
GRANT USAGE, CREATE ON SCHEMA public TO ${db_user};
SQL

database_url="postgres://${db_user}@${pg_host}:5432/${pg_database}?password=${db_password}&sslmode=disable"

docker run --rm --network "$network" \
  -e "DATABASE_URL=${database_url}" \
  --entrypoint assops-tool "${project}-runtime:local" db migrate >/tmp/assops-docker-pg18-runtime-migrate.log

docker exec -i "$pg_container" psql -U "$pg_admin_user" -d "$pg_database" -v ON_ERROR_STOP=1 >/dev/null <<SQL
DO \$\$
DECLARE r record;
BEGIN
  FOR r IN SELECT schemaname, tablename FROM pg_tables WHERE schemaname = 'public' LOOP
    EXECUTE format('ALTER TABLE %I.%I OWNER TO ${db_user}', r.schemaname, r.tablename);
  END LOOP;
  FOR r IN SELECT sequence_schema, sequence_name FROM information_schema.sequences WHERE sequence_schema = 'public' LOOP
    EXECUTE format('ALTER SEQUENCE %I.%I OWNER TO ${db_user}', r.sequence_schema, r.sequence_name);
  END LOOP;
  FOR r IN SELECT n.nspname AS schema_name, p.proname AS function_name, pg_get_function_identity_arguments(p.oid) AS args FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = 'public' LOOP
    EXECUTE format('ALTER FUNCTION %I.%I(%s) OWNER TO ${db_user}', r.schema_name, r.function_name, r.args);
  END LOOP;
END
\$\$;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO ${db_user};
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO ${db_user};
GRANT ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public TO ${db_user};
SQL

for container in \
  "${project}-gateway" \
  "${project}-worker" \
  "${project}-node-worker" \
  "${project}-web"; do
  docker rm -f "$container" >/dev/null 2>&1 || true
done

docker volume create "${project}_context" >/dev/null
docker volume create "${project}_bare_repos" >/dev/null
docker volume create "${project}_ssh" >/dev/null
docker volume create "${project}_kubeconfigs" >/dev/null

common_env=(
  -e "DATABASE_URL=${database_url}"
  -e "ASSOPS_ADDR=:8080"
  -e "ASSOPS_GATEWAY_URL=${public_url}"
  -e "ASSOPS_CONTEXT_DIR=/var/lib/assops/context"
  -e "ASSOPS_LOCAL_BARE_BASE_DIRS=/var/lib/assops/bare-repos"
  -e "ASSOPS_JWT_SECRET=dev-${project}-jwt-change-me"
  -e "ASSOPS_WEBHOOK_SECRET_KEY=dev-${project}-webhook-change-me"
  -e "ASSOPS_ADMIN_EMAIL=${admin_email}"
  -e "ASSOPS_ADMIN_PASSWORD=${admin_password}"
  -e "ASSOPS_WORKER_INTERVAL_SECONDS=3"
)

common_volumes=(
  -v "${project}_context:/var/lib/assops/context"
  -v "${project}_bare_repos:/var/lib/assops/bare-repos"
  -v "${project}_ssh:/etc/assops/ssh:ro"
  -v "${project}_kubeconfigs:/etc/assops/kubeconfigs:ro"
)

docker run -d --name "${project}-gateway" --network "$network" --network-alias gateway \
  "${common_env[@]}" "${common_volumes[@]}" \
  --entrypoint gateway "${project}-runtime:local" >/dev/null

docker run -d --name "${project}-worker" --network "$network" \
  "${common_env[@]}" -e "ASSOPS_WORKER_HEALTH_ADDR=:8081" "${common_volumes[@]}" \
  --entrypoint worker "${project}-runtime:local" >/dev/null

docker run -d --name "${project}-node-worker" --network "$network" \
  -e "DATABASE_URL=${database_url}" \
  -e "ASSOPS_GATEWAY_URL=http://gateway:8080" \
  -e "ASSOPS_NODE_WORKER_HEALTH_ADDR=:8082" \
  -v "${project}_ssh:/etc/assops/ssh:ro" \
  --entrypoint node-worker "${project}-runtime:local" \
  -name live-pg18-node -kind local -capabilities echo,git,ssh,ai >/dev/null

docker run -d --name "${project}-web" --network "$network" --network-alias web \
  -p "${web_port}:80" "${project}-web:local" >/dev/null

sleep 3

docker exec "${project}-gateway" assops-tool db seed-demo >/tmp/assops-docker-pg18-runtime-seed-demo.log

ASSOPS_DOCKER_RUNTIME_BASE_URL="http://127.0.0.1:${web_port}" \
ASSOPS_ADMIN_EMAIL="$admin_email" \
ASSOPS_ADMIN_PASSWORD="$admin_password" \
  bash scripts/docker-pg18-runtime-check.sh

echo "docker PG18 runtime up at http://127.0.0.1:${web_port}"

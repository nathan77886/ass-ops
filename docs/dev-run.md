# Local Development Runbook

## Start services

```bash
docker compose -f deploy/docker-compose.yml up -d postgres
go run ./backend/cmd/gateway
go run ./backend/cmd/worker
go run ./backend/cmd/node-worker
cd web && pnpm install && pnpm dev
```

Gateway listens on `:8080`. Vite listens on `:5173` and proxies `/api` to the gateway.

If local port `5432` is already in use:

```bash
ASSOPS_POSTGRES_PORT=55432 docker compose -f deploy/docker-compose.yml up -d postgres
DATABASE_URL='postgres://assops:assops@localhost:55432/assops?sslmode=disable' go run ./backend/cmd/gateway
```

## Login

Default seed user:

- `admin@assops.local`
- `admin1234`

## API smoke test

```bash
TOKEN=$(curl -s http://localhost:8080/api/auth/login \
  -H 'content-type: application/json' \
  -d '{"email":"admin@assops.local","password":"admin1234"}' | jq -r .token)

curl -s http://localhost:8080/api/projects \
  -H "authorization: Bearer $TOKEN" \
  -H 'content-type: application/json' \
  -d '{"name":"Demo","slug":"demo"}'
```

## Environment

```bash
DATABASE_URL='postgres://assops:assops@localhost:5432/assops?sslmode=disable'
ASSOPS_ADDR=':8080'
ASSOPS_GATEWAY_URL='http://localhost:8080'
ASSOPS_CONTEXT_DIR='.assops/context'
ASSOPS_JWT_SECRET='dev-assops-change-me'
ASSOPS_ADMIN_EMAIL='admin@assops.local'
ASSOPS_ADMIN_PASSWORD='admin1234'
```

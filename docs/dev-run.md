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
Docker Compose initializes every numbered file in `backend/migrations` for a fresh PostgreSQL volume, currently `001_init.sql` through `023_kubernetes_pod_restart_access.sql`.

If local port `5432` is already in use:

```bash
ASSOPS_POSTGRES_PORT=55432 docker compose -f deploy/docker-compose.yml up -d postgres
DATABASE_URL='postgres://assops:assops@localhost:55432/assops?sslmode=disable' go run ./backend/cmd/gateway
```

## Login

Default seed user:

- `admin@assops.local`
- `admin1234`

Optional demo data:

```bash
go run ./backend/cmd/assops-tool db seed-demo
go run ./backend/cmd/assops-tool db sync-assets
```

The demo seed command applies migrations, ensures the admin user exists, and idempotently creates a demo project, repository pair, Gitea/GitHub remotes, a disabled RepoSyncAsset, webhook connection/events, sample sync and GitHub Actions history, Argo deployment/rollback read models, SSH/AI examples, a pending multi-approver request, a read-only agent task, and a manual asset relation. It also refreshes the canonical asset ledger so the Asset Center can show the seeded graph immediately; `db sync-assets` remains available for later imports and repairs.
After logging in, the same first-version readiness evidence shown on the Dashboard can be printed from the gateway:

```bash
go run ./backend/cmd/assops-tool --token "$TOKEN" project readiness
```

Local backup smoke:

Set `DATABASE_URL` first if your local PostgreSQL is not running on the default `localhost:5432` credentials.

```bash
go run ./backend/cmd/assops-tool db backup-retain .assops/backups 3
go run ./backend/cmd/assops-tool db inspect-backup .assops/backups/assops-YYYYMMDD-HHMMSS.dump
```

## API smoke test

```bash
make api-smoke
```

Set `ASSOPS_GATEWAY_URL`, `ASSOPS_ADMIN_EMAIL`, and `ASSOPS_ADMIN_PASSWORD` to point the same smoke check at a deployed test gateway. The smoke check verifies `/healthz`, login, project listing, and worker queue summary without creating or modifying rows.

## Environment

```bash
DATABASE_URL='postgres://assops:assops@localhost:5432/assops?sslmode=disable'
ASSOPS_ADDR=':8080'
ASSOPS_GATEWAY_URL='http://localhost:8080'
ASSOPS_CONTEXT_DIR='.assops/context'
ASSOPS_JWT_SECRET='dev-assops-change-me'
ASSOPS_WEBHOOK_SECRET_KEY='dev-webhook-secret-change-me'
ASSOPS_APPROVAL_WEBHOOK_URL=''
ASSOPS_APPROVAL_WEBHOOK_TOKEN=''
ASSOPS_ADMIN_EMAIL='admin@assops.local'
ASSOPS_ADMIN_PASSWORD='admin1234'
ASSOPS_GITHUB_ACTIONS_READ_TOKEN=''
ASSOPS_ARGO_READ_TOKEN=''
ASSOPS_KUBERNETES_LOGS_ENABLED='false'
ASSOPS_KUBERNETES_LOG_PREVIEW_ENABLED='false'
ASSOPS_KUBERNETES_RESTARTS_ENABLED='false'
ASSOPS_KUBECONFIG_SECRET_DIR='/etc/assops/kubeconfigs'
ASSOPS_KUBECTL_PATH='kubectl'
ASSOPS_SSH_KEY_DIR='/etc/assops/ssh/keys'
ASSOPS_SSH_KNOWN_HOSTS_DIR='/etc/assops/ssh/known_hosts'
```

## Reset local database

Use this only for local development data:

```bash
docker compose -f deploy/docker-compose.yml down -v
docker compose -f deploy/docker-compose.yml up -d postgres
```

If a local WIP database was created before migration files were split, gateway startup may stop with a `checksum mismatch for 002_git_first_version.sql` error. That means the database recorded an older local copy of an applied migration. For local development, prefer exporting anything you need, then recreate the Compose volume with the reset commands above so the current migration chain is applied cleanly. Do not patch production `schema_migrations` checksums to bypass this error; create a follow-up migration instead.

## Integration setup notes

- GitHub Actions sync reads `ASSOPS_GITHUB_ACTIONS_READ_TOKEN`; broad `repo` or `delete_repo` scopes are rejected.
- Webhook shared secrets are encrypted with `ASSOPS_WEBHOOK_SECRET_KEY`; set a strong non-default value before sharing the service, and keep it stable because changing it prevents decrypting existing rotated secrets unless they are rotated again.
- Webhook connection rows expose a copyable public URL built from `ASSOPS_GATEWAY_URL`; use provider `gitea` for push-to-sync callbacks and `github` for GitHub `workflow_run` callbacks.
- Argo app sync reads a per-connection `config.token`. `ASSOPS_ARGO_READ_TOKEN` is only used when an admin/owner creates a connection with `use_env_token=true`.
- Argo URLs must be public HTTP(S) endpoints. Localhost, private IPs, link-local IPs, and unresolvable hosts are rejected.
- Kubernetes pod-log metadata uses `ASSOPS_KUBERNETES_LOGS_ENABLED`; Kubernetes Deployment rollout restart uses the separate `ASSOPS_KUBERNETES_RESTARTS_ENABLED` write-operation gate and requires `rbac_restart_pods_status=reviewed`.
- SSH machines should reference key and known_hosts paths under `ASSOPS_SSH_KEY_DIR` and `ASSOPS_SSH_KNOWN_HOSTS_DIR`.

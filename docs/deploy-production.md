# Production Compose Runbook

This runbook describes the first production-shaped ASSOPS deployment. It uses Docker Compose, PostgreSQL, the Go gateway, the control worker, a node worker, and the web UI served by nginx.

It is suitable for a first private deployment or demo environment. A first Helm chart is available in `deploy/helm/assops`; TLS ingress, storage-class choices, and real cluster rollout are still environment-specific.

## Build And Validate

```bash
docker build --target gateway -t assops/gateway:local .
docker build --target worker -t assops/worker:local .
docker build --target node-worker -t assops/node-worker:local .
docker build --target web -t assops/web:local .
```

For a tagged release candidate, push a `v*` tag or manually run the `Release Candidate` workflow in GitHub Actions. The workflow uploads Linux amd64 binaries, the web bundle, a packaged Helm chart, `SHA256SUMS`, and Docker image smoke-build results as an artifact, then creates GitHub artifact attestations for the release files. Tagged `v*` runs also publish gateway, worker, node-worker, and web images to GHCR with version and commit-SHA tags and registry-backed image attestations. Keep the release artifact together with the restore rehearsal JSON report before promoting an update.

Before treating `main` as a release branch, apply the GitHub repository ruleset from `.github/rulesets/main-required-checks.json`; see `docs/github-branch-protection.md`. The ruleset requires PR review, fresh CI checks, and blocks branch deletion or force pushes.
The CI workflow includes `actionlint`, so changes to CI/release/promotion workflows are checked for common GitHub Actions expression and workflow mistakes before merge. It also runs Gitleaks secret scanning to catch accidental hardcoded credentials before protected-branch merge.

Validate Compose expansion:

```bash
ASSOPS_POSTGRES_PASSWORD='change-me-postgres' \
ASSOPS_GATEWAY_URL='https://assops.example.com' \
ASSOPS_JWT_SECRET='change-me-jwt' \
ASSOPS_WEBHOOK_SECRET_KEY='change-me-webhook-secret' \
ASSOPS_ADMIN_EMAIL='admin@example.com' \
ASSOPS_ADMIN_PASSWORD='change-me-admin' \
docker compose -f deploy/compose.prod.yml config
```

Or copy the example environment file and fill in production values:

```bash
cp deploy/.env.prod.example deploy/.env.prod
docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml config
```

## Required Environment

```bash
ASSOPS_POSTGRES_PASSWORD='long random PostgreSQL password'
ASSOPS_GATEWAY_URL='https://assops.example.com'
ASSOPS_JWT_SECRET='long random JWT secret'
ASSOPS_WEBHOOK_SECRET_KEY='long random webhook encryption key'
ASSOPS_ADMIN_EMAIL='admin@example.com'
ASSOPS_ADMIN_PASSWORD='long random bootstrap password'
```

Optional integration environment:

```bash
ASSOPS_APPROVAL_WEBHOOK_URL=''
ASSOPS_APPROVAL_WEBHOOK_TOKEN=''
ASSOPS_GITHUB_ACTIONS_READ_TOKEN=''
ASSOPS_ARGO_READ_TOKEN=''
ASSOPS_WORKER_INTERVAL_SECONDS='3'
ASSOPS_WORKER_HEALTH_ADDR=':8081'
ASSOPS_NODE_WORKER_HEALTH_ADDR=':8082'
ASSOPS_WEB_PORT='8080'
ASSOPS_LOCAL_BARE_BASE_DIRS='/var/lib/assops/bare-repos'
```

## Start

```bash
docker compose -f deploy/compose.prod.yml up -d --build
```

The `web` service is the public entry point. It serves the React app and proxies `/api` and `/healthz` to the internal gateway service.

## Persistent Data

The production Compose file defines:

- `assops_pg`: PostgreSQL data.
- `assops_context`: generated context files.
- `assops_ssh`: SSH keys and known_hosts mounted read-only into services that need them.

Put SSH key files under the `assops_ssh` volume paths expected by the app:

- `/etc/assops/ssh/keys`
- `/etc/assops/ssh/known_hosts`

## Database Migrations

The gateway applies migrations from `backend/migrations` on startup. Migrations are recorded in `schema_migrations`, guarded by a PostgreSQL advisory lock, and skipped when the same filename/checksum has already been applied. Treat applied migration files as immutable: add a new numbered migration for follow-up schema changes instead of editing a migration that may already be recorded in `schema_migrations`.

For a production rollout, run the standalone migration tool before starting or updating long-running services:

```bash
docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml run --rm db-tool assops-tool db migrate
docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml run --rm db-tool assops-tool db migrations
docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml run --rm db-tool assops-tool db sync-assets
```

Gateway startup also runs the same migration path and skips already-applied files when the recorded checksum matches. A checksum mismatch means the file changed after it was applied; stop and create a follow-up migration rather than overwriting the recorded checksum. The standalone command is the preferred preflight step because it separates migration failure from service rollout. `db sync-assets` is idempotent and backfills canonical asset ledger rows/relations after migrations or imports.

The PostgreSQL container also loads migrations for a fresh volume through `/docker-entrypoint-initdb.d`. On existing volumes, use the `db-tool` command above. Check the target volume before first launch:

```bash
docker volume inspect deploy_assops_pg >/dev/null 2>&1 && echo "existing PostgreSQL volume found"
```

## Backup And Restore

Create a compressed PostgreSQL backup in the `assops_backups` volume:

```bash
docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml run --rm db-tool \
  assops-tool db backup /backups/assops-$(date +%Y%m%d-%H%M%S).dump
```

Create a timestamped backup and retain only the newest 14 managed ASSOPS backups:

```bash
docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml run --rm db-tool \
  assops-tool db backup-retain /backups 14
```

Only files named `assops-*.dump` are managed by retention pruning. Manual files with other names are left untouched.
Do not run multiple retained backup jobs against the same directory at once; the command creates a `.assops-backup.lock` file and exits if another retained backup is already active.

Inspect a backup without restoring it:

```bash
docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml run --rm db-tool \
  assops-tool db inspect-backup /backups/assops-YYYYMMDD-HHMMSS.dump
```

To restore a backup, first stop services that write to the database, then run restore against the intended target database:

```bash
docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml stop gateway worker node-worker
docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml run --rm db-tool \
  env ASSOPS_CONFIRM_DB_RESTORE=assops assops-tool db restore /backups/assops-YYYYMMDD-HHMMSS.dump
docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml up -d gateway worker node-worker web
```

Restore uses `pg_restore --clean --if-exists --no-owner`; treat it as destructive for the target database. The command refuses to run unless `ASSOPS_CONFIRM_DB_RESTORE` exactly matches the target database name.

`assops-tool` passes database passwords to `pg_dump`/`pg_restore` through `PGPASSWORD` and strips them from command-line connection URLs and tool output. On shared hosts, process environments may be visible to privileged users, so use temporary or tightly scoped database credentials for backup and rehearsal jobs.

Run a restore rehearsal against a disposable target database:

```bash
ASSOPS_REHEARSAL_DATABASE_URL='postgres://assops:change-me@postgres:5432/assops_restore_test?sslmode=disable'
docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml run --rm db-tool \
  assops-tool db rehearse-restore \
    /backups/assops-YYYYMMDD-HHMMSS.dump \
    "$ASSOPS_REHEARSAL_DATABASE_URL" \
    /backups/release-notes/restore-rehearsal-YYYYMMDD-HHMMSS.json
```

The rehearsal command refuses to target the active `DATABASE_URL`, inspects the dump, restores it into the explicit target database, runs migrations, prints a JSON summary, and optionally writes the same JSON to a private `0600` report file for release notes. By default the target database name must look disposable, such as `assops_restore_test`, `assops_rehearsal`, or `assops_tmp`; set `ASSOPS_ALLOW_RESTORE_REHEARSAL_TARGET=1` only for controlled test infrastructure.

Minimum first-version rehearsal record:

1. Run `db backup-retain /backups 14` before an update.
2. Run `db rehearse-restore` against a disposable PostgreSQL database.
3. Keep the JSON rehearsal report with the release notes.
4. Record the backup filename, restore target, migration list, and rehearsal date.

GitHub Actions also includes `.github/workflows/restore-rehearsal.yml`, a weekly and manual scheduled rehearsal that runs the same backup and disposable restore flow against temporary runner databases and uploads the JSON report as a short-retention artifact. Treat it as a drift detector for the backup tooling; it does not replace an environment-specific rehearsal against retained production backups.

Before promoting a release candidate, validate the downloaded release artifact directory and the restore rehearsal report together:

```bash
docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml run --rm db-tool \
  assops-tool release validate-bundle \
    /backups/release-artifacts/assops-release-candidate \
    /backups/release-notes/restore-rehearsal-YYYYMMDD-HHMMSS.json
```

The command is offline. It verifies `SHA256SUMS`, rejects unsafe checksum paths, requires binary, web, and Helm chart artifacts, and checks that the rehearsal report has a redacted target database, backup object counts, migrations, and an RFC3339 rehearsal timestamp.

After downloading the release candidate, verify the GitHub artifact attestations from the same repository:

```bash
gh attestation verify /backups/release-artifacts/assops-release-candidate/SHA256SUMS --repo <owner>/<repo>
gh attestation verify /backups/release-artifacts/assops-release-candidate/assops-v0.1.0-linux-amd64.tar.gz --repo <owner>/<repo>
gh attestation verify /backups/release-artifacts/assops-release-candidate/assops-web-v0.1.0.tar.gz --repo <owner>/<repo>
gh attestation verify /backups/release-artifacts/assops-release-candidate/assops-0.1.0.tgz --repo <owner>/<repo>
```

For image-based deployments, use the tagged GHCR images emitted by the same `v*` workflow run, for example:

```text
ghcr.io/<owner>/assops-gateway:v0.1.0
ghcr.io/<owner>/assops-worker:v0.1.0
ghcr.io/<owner>/assops-node-worker:v0.1.0
ghcr.io/<owner>/assops-web:v0.1.0
```

Generate a Helm values overlay that pins all ASSOPS workloads to the reviewed GHCR release tag:

```bash
docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml run --rm db-tool \
  assops-tool release helm-values <owner> v0.1.0 /backups/release-notes/helm-values-v0.1.0.yaml
```

Review that file before rollout and apply it together with the environment-specific values:

```bash
helm upgrade --install assops deploy/helm/assops \
  -f deploy/helm/assops/values.yaml \
  -f /backups/release-notes/helm-values-v0.1.0.yaml
```

Generate a promotion plan for the release notes. This validates the release bundle again and writes a Markdown checklist with the exact attestation verification and Helm rollout commands:

```bash
docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml run --rm db-tool \
  assops-tool release promotion-plan \
    <owner>/<repo> \
    <owner> \
    v0.1.0 \
    /backups/release-artifacts/assops-release-candidate \
    /backups/release-notes/restore-rehearsal-YYYYMMDD-HHMMSS.json \
    /backups/release-notes/helm-values-v0.1.0.yaml \
    /backups/release-notes/promotion-plan-v0.1.0.md
```

For image provenance, verify the registry-backed attestation before deployment:

```bash
gh attestation verify oci://ghcr.io/<owner>/assops-gateway:v0.1.0 --repo <owner>/<repo>
gh attestation verify oci://ghcr.io/<owner>/assops-worker:v0.1.0 --repo <owner>/<repo>
gh attestation verify oci://ghcr.io/<owner>/assops-node-worker:v0.1.0 --repo <owner>/<repo>
gh attestation verify oci://ghcr.io/<owner>/assops-web:v0.1.0 --repo <owner>/<repo>
```

## GitHub Production Promotion

`.github/workflows/promote-production.yml` provides the first automated promotion path. It is manual (`workflow_dispatch`) and defaults to preflight only:

1. Generate the Helm values overlay for the selected GHCR owner and release tag.
2. Verify GHCR image attestations for gateway, worker, node-worker, and web.
3. Run `helm lint`.
4. Render the chart and upload the rendered manifest plus generated values as a workflow artifact.

To deploy, run the workflow with `deploy=true`. The deploy job is attached to the GitHub `production` environment, so configure required reviewers in GitHub before using it. The job requires a `KUBE_CONFIG_B64` environment secret containing a base64-encoded kubeconfig for the target cluster.

The workflow accepts an `environment_values` input. Use a reviewed production overlay, starting from `deploy/helm/assops/values.production.example.yaml`, so production renders with an external Secret, external PostgreSQL, and TLS ingress. When `deploy=true`, the workflow runs `helm upgrade --install`, then waits for gateway, worker, node-worker, and web deployments to finish their rollout and prints a deployment/pod summary. Keep `deploy=false` until the production environment, namespace, storage class, TLS ingress, and external secrets are reviewed.

The production values example enables stricter pod/container security settings for Go workloads and migration jobs. Review these settings with the actual images and cluster policy before rollout, especially if you override images.

It also enables chart-managed NetworkPolicies. Confirm that your CNI enforces NetworkPolicy and that the configured web ingress CIDRs match the ingress controller or load balancer path used by the cluster.

The production values example sets conservative global resource requests and limits. Review them against expected repository sync volume, worker concurrency, web traffic, and database migration size before enabling `deploy=true`.

The production values example also enables PodDisruptionBudgets for the core Deployments. With `replicaCount: 1`, voluntary evictions are intentionally blocked; increase replicas or temporarily adjust the PDB for maintenance windows.

ASSOPS application Pods do not require Kubernetes API access. The production values example disables ServiceAccount token automount for application Pods; keep cluster mutation permission isolated to the protected promotion workflow kubeconfig.

Use a namespace-scoped kubeconfig for the workflow instead of cluster-admin credentials. `deploy/k8s/promotion-rbac.yaml` is a first-version RBAC example for the `assops` namespace and `assops-promoter` ServiceAccount. Review it against your cluster policy, apply it out of band, then store a kubeconfig for that ServiceAccount as the protected environment secret `KUBE_CONFIG_B64`.

## Health Checks

```bash
curl -fsS http://localhost:${ASSOPS_WEB_PORT:-8080}/healthz
docker compose -f deploy/compose.prod.yml ps
docker compose -f deploy/compose.prod.yml logs -f gateway worker node-worker
```

The gateway, control worker, and node worker all expose `/healthz` inside their containers. The production Compose healthchecks use those internal endpoints and do not publish worker health ports to the host.

## Security Notes

- Terminate TLS at a reverse proxy or load balancer in front of the `web` service.
- Set `ASSOPS_GATEWAY_URL` to the public HTTP(S) origin before wiring Gitea/GitHub webhook callbacks, for example `https://assops.example.com` with no path, query string, or fragment.
- Keep `ASSOPS_WEBHOOK_SECRET_KEY` stable. Rotated webhook secrets are encrypted with it.
- Keep `ASSOPS_LOCAL_BARE_BASE_DIRS` pointed at a dedicated ASSOPS-owned directory; project template `local_bare` remotes outside that path are rejected.
- Do not mount writable SSH directories into the web service.
- Restrict access to the Compose host and Docker socket.

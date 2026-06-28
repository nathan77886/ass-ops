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

To build release-named images locally without pushing:

```bash
ASSOPS_RELEASE_IMAGE_OWNER=nathan77886 \
ASSOPS_RELEASE_IMAGE_VERSION=v0.1.0 \
make release-images
```

To intentionally publish those images from a trusted workstation, authenticate first and set the explicit push flag:

```bash
docker login ghcr.io
ASSOPS_RELEASE_IMAGE_OWNER=nathan77886 \
ASSOPS_RELEASE_IMAGE_VERSION=v0.1.0 \
ASSOPS_RELEASE_IMAGE_PUSH=true \
make release-images
```

If the target cluster pulls from a private registry, create a namespace-local `kubernetes.io/dockerconfigjson` Secret through the environment's secret-management process and set the private Helm overlay to reference it:

```yaml
image:
  pullSecrets:
    - name: assops-registry-pull
```

For a tagged release candidate, push a `v*` tag or manually run the `Release Candidate` workflow in GitHub Actions. The workflow uploads Linux amd64 binaries, the web bundle, a packaged Helm chart, `SHA256SUMS`, and Docker image smoke-build results as an artifact, then creates GitHub artifact attestations for the release files. Tagged `v*` runs also publish gateway, worker, node-worker, and web images to GHCR with version and commit-SHA tags and registry-backed image attestations. Keep the release artifact together with the restore rehearsal JSON report before promoting an update.

Before treating the repository default branch as a release branch, generate the local branch-protection plan and then have a repository administrator apply the GitHub repository ruleset from `.github/rulesets/main-required-checks.json`; see `docs/github-branch-protection.md`. The ruleset requires PR review, fresh CI checks, and blocks branch deletion or force pushes.
The CI workflow includes `actionlint`, so changes to CI/release/promotion workflows are checked for common GitHub Actions expression and workflow mistakes before merge. It also runs Gitleaks secret scanning to catch accidental hardcoded credentials before protected-branch merge.

Run the offline workflow safety check when editing release, backup, restore, or promotion workflows:

```bash
make workflow-safety-self-test
```

This parses the workflow YAML and checks the first-deployable safety contracts that do not require network access: workflow lint and secret-scan jobs exist, release image publication is tag-gated, protected backup/restore schedules remain default-off, promotion deployment requires the explicit `deploy` input and production environment, and the CI-only restore rehearsal warns against production credentials. It does not replace `actionlint` in CI.

Validate the production Helm example hardening contract locally before a protected environment rollout review:

```bash
make helm-production-hardening-self-test
```

This renders `deploy/helm/assops/values.production.example.yaml` and checks external Secret use, external PostgreSQL, TLS ingress, service-account token automount, NetworkPolicies, PodDisruptionBudgets, non-root/read-only Go containers, retained StorageClass PVCs, and GHCR release image references. It is a no-cluster render check only; real storage class, TLS Secret, registry credentials, and namespace policy still need environment review.

Generate a first-deployable external handoff pack before asking environment owners to apply branch protection, run protected backup rehearsal, or review a Helm rollout:

```bash
make first-deployable-handoff-plan
```

The pack is written to `.assops/release-notes/first-deployable/` by default. It contains a branch-protection plan, production backup rehearsal plan, generated release Helm values, Helm rollout rehearsal plan, Markdown and machine-readable completion audits, JSON Schemas for machine-readable audit/checklist/status files, checksum manifest, and a short index of the external actions still required. This generator writes local files only; it does not call GitHub, contact registries, run Helm against a cluster, invoke Argo, connect to PostgreSQL, read kubeconfigs or token values, or push images. Validate the generated pack with `make first-deployable-handoff-validate`; this includes manifest checksum validation plus cross-file completion audit and evidence checklist/status consistency.

External owners should copy `.assops/release-notes/first-deployable/external-evidence-status.example.json` to a private evidence status file, keep any sensitive run logs or screenshots outside the repository, fill only references and short summaries, then validate only the local status structure with `make first-deployable-external-evidence-validate EVIDENCE_FILE=/path/to/external-evidence-status.json` before using it in release notes. This validator checks schema, ids, source-checklist checksum shape, owner/required-evidence traceability, status fields, UTC `verified_at` timestamps, evidence reference shape, rejected-entry summaries, and secret-shaped text; it does not prove GitHub, registry, Kubernetes, database, provider, or Dashboard truth.

Before declaring the first deployable complete, run `make first-deployable-external-evidence-complete-validate EVIDENCE_FILE=/path/to/external-evidence-status.json`. This uses the same local schema checks but fails until every external evidence entry is `verified` with a reference, verifier, and timestamp; it still does not contact or prove the remote systems.

Run the local completion audit when deciding whether the deployable goal can be closed:

```bash
make first-deployable-completion-audit
```

The audit checks the repository evidence and prints the external proof still required before completion. It is intentionally local only: it does not call GitHub, registries, Kubernetes, Argo, PostgreSQL, MQ, provider APIs, SSH, or Codex CLI.

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
ASSOPS_GITHUB_TEMPLATE_TOKEN=''
ASSOPS_GITEA_TEMPLATE_TOKEN=''
ASSOPS_KUBERNETES_LOGS_ENABLED='false'
ASSOPS_KUBERNETES_LOG_PREVIEW_ENABLED='false'
ASSOPS_KUBERNETES_RESTARTS_ENABLED='false'
ASSOPS_KUBECTL_PATH='kubectl'
ASSOPS_WORKER_INTERVAL_SECONDS='3'
ASSOPS_WORKER_HEALTH_ADDR=':8081'
ASSOPS_NODE_WORKER_HEALTH_ADDR=':8082'
ASSOPS_WEB_PORT='8080'
ASSOPS_LOCAL_BARE_BASE_DIRS='/var/lib/assops/bare-repos'
ASSOPS_CONFIG_GIT_LOCAL_BARE_WRITES_ENABLED='false'
ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION='false'
ASSOPS_ARM_PROVIDER_REVIEW_MUTATION='false'
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
- `assops_bare_repos`: local bare repositories used by Git sync/template flows.
- `assops_ssh`: SSH keys and known_hosts mounted read-only into services that need them.
- `assops_kubeconfigs`: namespace-scoped kubeconfig files mounted read-only into gateway/worker for live pod-log metadata audits.
- `assops_backups`: PostgreSQL backup and restore rehearsal artifacts for the `db-tool` profile.

Put SSH key files under the `assops_ssh` volume paths expected by the app:

- `/etc/assops/ssh/keys`
- `/etc/assops/ssh/known_hosts`

Put reviewed kubeconfig files under the `assops_kubeconfigs` volume path expected by the app:

- `/etc/assops/kubeconfigs`

The value stored in the ASSOPS Kubernetes environment form is a relative path below that directory, such as `test/assops-reader.yaml`. Do not paste kubeconfig contents into the UI.

## Database Schema

The gateway applies the database schema from GORM models on startup with `AutoMigrate`. The old numbered SQL migration chain is not used by the Docker runtime.

For a production rollout, run the standalone migration tool before starting or updating long-running services:

```bash
docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml run --rm db-tool assops-tool db automigrate
docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml run --rm db-tool assops-tool db sync-assets
```

Gateway startup runs the same schema sync path. The standalone command is the preferred preflight step because it separates schema failure from service rollout. `db sync-assets` is idempotent and backfills canonical asset ledger rows/relations after schema sync or imports. Check the target volume before first launch:

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

For artifact-based retained production backups, `.github/workflows/production-retained-backup.yml` provides a protected publication path. It can be run manually and also has a weekly schedule that is disabled by default. Configure the protected GitHub environment with:

- `ASSOPS_ACTIVE_DATABASE_URL`: active production database URL used by `assops-tool db backup-retain`; if the URL includes a password, `assops-tool` strips it from the `pg_dump` command-line argument and passes it through `PGPASSWORD`.
- `ASSOPS_ACTIVE_DATABASE_PASSWORD`: optional password passed through `PGPASSWORD` when the URL omits a password.

Run the backup publication workflow manually before enabling the schedule:

```bash
gh workflow run production-retained-backup.yml \
  -f github_environment=production \
  -f runner=ubuntu-latest \
  -f artifact_name=retained-assops-backup \
  -f retention_days=14 \
  -f keep_count=3
```

The workflow validates its inputs, uses a concurrency group per GitHub environment, disables shell tracing before calling `assops-tool db backup-retain`, stages exactly one `assops-*.dump`, rejects `.env`, kubeconfig, log, key, and PEM-like files, and uploads a private repository artifact. It does not restore data, delete remote artifacts, publish to external storage, encrypt the dump before upload, or bypass GitHub artifact size limits. Use an external environment-owned path or encrypted storage publication for large databases or stricter data-handling requirements.

Set these repository variables only after the protected environment, runner reachability, and manual backup publication run have been reviewed:

- `ASSOPS_PRODUCTION_RETAINED_BACKUP_ENABLED=true`
- `ASSOPS_PRODUCTION_RETAINED_BACKUP_ENVIRONMENT=production`
- `ASSOPS_PRODUCTION_RETAINED_BACKUP_RUNNER=ubuntu-latest`
- `ASSOPS_PRODUCTION_RETAINED_BACKUP_ARTIFACT=retained-assops-backup`
- `ASSOPS_PRODUCTION_RETAINED_BACKUP_RETENTION_DAYS=14`
- `ASSOPS_PRODUCTION_RETAINED_BACKUP_KEEP_COUNT=3`

For retained environment backups, `.github/workflows/production-restore-rehearsal.yml` provides a protected rehearsal path. It can be run manually and also has a weekly schedule that is disabled by default. Configure a GitHub environment such as `production` with:

- `ASSOPS_REHEARSAL_DATABASE_URL`: URL of a pre-created disposable restore database whose name includes `rehearsal`, `restore`, `test`, `tmp`, `scratch`, or `disposable`.
- `ASSOPS_REHEARSAL_DATABASE_PASSWORD`: optional database password passed to PostgreSQL tools through `PGPASSWORD` when the URL does not include a password.
- `ASSOPS_ACTIVE_DATABASE_URL`: optional active database URL used only as a guard so `assops-tool` can reject a rehearsal target that accidentally equals production.

Run the workflow with exactly one backup source:

- `backup_artifact_name`: the name of a retained, unexpired repository workflow artifact that contains one `assops-*.dump` file. The workflow downloads the latest unexpired artifact with this name.
- `backup_path`: a runner-local backup path for self-hosted runners that mount the retained backup store. Set the workflow `runner` input to that self-hosted runner label; the default `ubuntu-latest` runner can only use downloaded artifacts or files created inside the job workspace.

The workflow does not create backups, does not create the disposable database, and does not connect to the active database. It validates inputs, restores only into `ASSOPS_REHEARSAL_DATABASE_URL`, reruns migrations, validates the JSON report shape, and uploads the report as a private short-retention artifact for release notes.

Before enabling the scheduled environment job, generate a local schedule-readiness plan. The command is offline: it validates the intended repository, GitHub environment, runner, cron expression, backup source shape, and artifact retention, but it does not read or print GitHub environment secret values.

You can also generate the protected-environment backup/restore rehearsal checklist without invoking `assops-tool` or GitHub Actions:

```bash
ASSOPS_PRODUCTION_BACKUP_REHEARSAL_REPO=nathan77886/ass-ops \
ASSOPS_PRODUCTION_BACKUP_REHEARSAL_ENV=production \
ASSOPS_PRODUCTION_BACKUP_REHEARSAL_RUNNER=ubuntu-latest \
ASSOPS_PRODUCTION_BACKUP_REHEARSAL_BACKUP_SOURCE=artifact:retained-assops-backup \
ASSOPS_PRODUCTION_BACKUP_REHEARSAL_PLAN_OUTPUT=/backups/release-notes/production-backup-rehearsal-plan.md \
make production-backup-rehearsal-plan
```

This verifier checks only non-secret local inputs: repository, protected environment, runner, cron expression, backup source shape, retained artifact/report names, retention days, and dangerous backup source markers. It does not read `ASSOPS_ACTIVE_DATABASE_URL`, read `ASSOPS_REHEARSAL_DATABASE_URL`, trigger either production workflow, connect to PostgreSQL, download artifacts, or inspect backup contents. Use it before the manual retained-backup and restore-rehearsal dispatches; it does not replace those dispatches.

Use a retained backup artifact source with a GitHub-hosted runner:

```bash
assops-tool release backup-schedule-plan \
  nathan77886/ass-ops \
  production \
  ubuntu-latest \
  '17 3 * * 1' \
  artifact:retained-assops-backup \
  14 \
  /backups/release-notes/backup-schedule-plan.md
```

Use a runner-local mounted backup path only with a self-hosted runner that mounts the retained backup store read-only:

```bash
assops-tool release backup-schedule-plan \
  nathan77886/ass-ops \
  production \
  self-hosted-prod \
  '23 2 * * 0' \
  path:/mnt/assops-backups/assops-YYYYMMDD-HHMMSS.dump \
  30 \
  /backups/release-notes/backup-schedule-plan.md
```

The generated plan includes the required environment secrets, a retained-backup publication contract, a one-time `gh workflow run production-restore-rehearsal.yml` manual dispatch check, and the scheduled configuration values. The publication contract is intentionally offline: it says the rehearsal workflow consumes exactly one retained `assops-*.dump` from either the default-off production retained backup artifact workflow, another environment-owned backup job, or a read-only mounted store, records non-secret timestamp/source/retention/checksum metadata when the producer supports it, and must not create, rotate, delete, overwrite, or publish retained backups itself. For artifact sources, the restore rehearsal workflow also rejects artifacts that do not contain exactly one dump or that include `.env`, kubeconfig, log, key, or PEM-like files. Enable the scheduled trigger only after the manual dispatch succeeds against the chosen retained backup source.

Set these repository variables to enable the scheduled path:

- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_ENABLED=true`
- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_ENVIRONMENT=production`
- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_RUNNER=ubuntu-latest` for artifact sources, or a self-hosted runner label for mounted paths.
- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_BACKUP_ARTIFACT=retained-assops-backup` or `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_BACKUP_PATH=/mnt/assops-backups/assops-YYYYMMDD-HHMMSS.dump`; set exactly one.
- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_REPORT_NAME=production-restore-rehearsal-scheduled`
- `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_RETENTION_DAYS=14`

Before promoting a release candidate, validate the downloaded release artifact directory and the restore rehearsal report together:

```bash
docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml run --rm db-tool \
  assops-tool release validate-bundle \
    /backups/release-artifacts/assops-release-candidate \
    /backups/release-notes/restore-rehearsal-YYYYMMDD-HHMMSS.json
```

The command is offline. It verifies `SHA256SUMS`, rejects unsafe checksum paths, requires binary, web, and Helm chart artifacts, and checks that the rehearsal report has a redacted target database, backup object counts, migrations, and an RFC3339 rehearsal timestamp.

The local self-test for that promotion gate creates a temporary bundle and confirms the CLI/Make path accepts matching checksums and rejects tampered artifacts:

```bash
make release-validate-bundle-self-test
```

The local promotion-plan self-test extends that check through generated Helm values and the operator checklist, including mismatched-value and malformed-repository rejection:

```bash
make release-promotion-plan-self-test
```

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
ASSOPS_RELEASE_COMMIT="$(git rev-parse --short=12 HEAD)" \
ASSOPS_RELEASE_BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml run --rm \
    -e ASSOPS_RELEASE_COMMIT \
    -e ASSOPS_RELEASE_BUILD_TIME \
    db-tool assops-tool release helm-values <owner> v0.1.0 /backups/release-notes/helm-values-v0.1.0.yaml
```

Review that file before rollout and apply it together with the environment-specific values:

```bash
helm upgrade --install assops deploy/helm/assops \
  -f deploy/helm/assops/values.yaml \
  -f /backups/release-notes/values.production.reviewed.yaml \
  -f /backups/release-notes/helm-values-v0.1.0.yaml
```

Generate a promotion plan for the release notes. This validates the release bundle again and writes a Markdown checklist with the exact attestation verification and Helm rollout command shape. Replace `<reviewed-environment-values.yaml>` in the generated plan with the same environment overlay used by the protected promotion workflow.

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

The workflow accepts an `environment_values` input. Use a reviewed production overlay, starting from `deploy/helm/assops/values.production.example.yaml`, so production renders with an external Secret, external PostgreSQL, and TLS ingress. When `deploy=true`, the workflow runs `helm upgrade --install --wait --wait-for-jobs --timeout 10m`, then waits for gateway, worker, node-worker, and web deployments to finish their rollout and prints a deployment/pod summary. If the deployed ASSOPS URL is reachable from GitHub Actions, also set `smoke_url` and provide protected environment secrets `ASSOPS_ADMIN_EMAIL` and `ASSOPS_ADMIN_PASSWORD`; the workflow then runs `scripts/api-smoke.sh` against `/healthz`, login, project listing, and worker queue summary. For private clusters without a public route, leave `smoke_url` empty and set `smoke_via_port_forward=true` to run the same API smoke through `kubectl port-forward svc/<release>-web 18080:80`. Keep `deploy=false` until the production environment, namespace, storage class, TLS ingress, and external secrets are reviewed.

Before setting `deploy=true`, generate a local Helm environment-readiness plan from the same overlay:

```bash
assops-tool release helm-readiness-plan \
  deploy/helm/assops/values.production.example.yaml \
  .assops/release-notes/helm-readiness-plan.md
```

The plan validates the local overlay for external Secret usage, external PostgreSQL, HTTPS/TLS ingress, ServiceAccount token isolation, NetworkPolicy, PodDisruptionBudget, explicit storage classes for retained PVCs, resource requests/limits, and non-root/drop-capability runtime posture. It does not call Kubernetes, Helm, Argo, GitHub, or cloud APIs, and the listed `kubectl` checks should only be run after the target cluster, namespace, and kubeconfig are confirmed out of band.

Before setting `deploy=true`, also generate a protected rollout rehearsal checklist from the same reviewed environment overlay and the generated release-image overlay:

```bash
ASSOPS_HELM_ROLLOUT_REHEARSAL_REPO=<owner>/<repo> \
ASSOPS_HELM_ROLLOUT_REHEARSAL_GHCR_OWNER=<owner> \
ASSOPS_HELM_ROLLOUT_REHEARSAL_VERSION=v0.1.0 \
ASSOPS_HELM_ROLLOUT_REHEARSAL_NAMESPACE=assops \
ASSOPS_HELM_ROLLOUT_REHEARSAL_RELEASE=assops \
ASSOPS_HELM_ROLLOUT_REHEARSAL_ENVIRONMENT=production \
ASSOPS_HELM_ROLLOUT_REHEARSAL_ENV_VALUES=/backups/release-notes/values.production.reviewed.yaml \
ASSOPS_HELM_ROLLOUT_REHEARSAL_RELEASE_VALUES=/backups/release-notes/helm-values-v0.1.0.yaml \
ASSOPS_HELM_ROLLOUT_REHEARSAL_PREVIOUS_VALUES=/backups/release-notes/helm-values-previous.yaml \
ASSOPS_HELM_ROLLOUT_REHEARSAL_RESTORE_REPORT=/backups/release-notes/restore-rehearsal-YYYYMMDD-HHMMSS.json \
ASSOPS_HELM_ROLLOUT_REHEARSAL_PLAN_OUTPUT=/backups/release-notes/helm-rollout-rehearsal-plan-v0.1.0.md \
make helm-rollout-rehearsal-plan
```

This check validates only non-secret local inputs and file shapes: repository, GHCR owner, release version, namespace, release name, reviewed values files, optional previous rollback values, and optional restore rehearsal report. It rejects common secret-shaped paths or names and writes a Markdown checklist without reading kubeconfigs, reading Secret data, calling GitHub, contacting registries, invoking Helm, calling Argo, or mutating Kubernetes.

Before importing a real demo project with provider remotes, generate the live demo import plan:

```bash
assops-tool release demo-import-plan \
  assops-demo \
  https://assops.example.com \
  .assops/release-notes/demo-import-plan.md
```

The plan lists the provider-owned Gitea/GitHub repository setup, ASSOPS project/repository/remote/RepoSyncAsset evidence, callback rehearsal evidence, and readiness snapshot order without calling providers, running Git, creating rows, recording snapshots, or storing remote URLs, tokens, provider responses, Git output, SHAs, branch names, or operator notes.

Before rehearsing a real GitHub remote tag and its Actions evidence, generate the no-call tag rehearsal plan:

```bash
assops-tool release tag-rehearsal-plan \
  assops-demo \
  github-main \
  .assops/release-notes/tag-rehearsal-plan.md
```

The plan accepts only a safe project slug and remote key. It lists the approval-gated tag operation, read-only live lookup, GitHub Actions refresh, tag-result snapshot, Actions refresh snapshot, and graph-evidence checks to collect without accepting or storing tag names, commit SHAs, branches, remote URLs, workflow URLs, provider run IDs, token names, tag messages, Git output, provider request/response bodies, workflow logs, or operator notes. It does not call GitHub, run Git, create or push tags, refresh Actions, enqueue workers, write operation logs, sync assets, or record snapshots.

Before rehearsing a real config repository commit, provider review, refs refresh, and ProjectVersion pin, generate the no-call config repository rehearsal plan:

```bash
assops-tool release config-rehearsal-plan \
  assops-demo \
  github-config \
  .assops/release-notes/config-rehearsal-plan.md
```

The plan accepts only a safe project slug and remote key. It lists the `repo_role=config` repository proof, scaffold-preview review, secret-scan gate, approval-gated `config.git_commit` audit workflow, read-only `git.refs.refresh`, config ref-refresh snapshot, config promotion snapshot, and dry-run `pin-config-commit` checks to collect without accepting or storing branch names, commit SHAs, refs, remote URLs, file contents, provider URLs, token names, Git output, provider responses, workflow logs, raw errors, or operator notes. It does not run Git, create files, commit, push refs, call providers, update ProjectVersion rows, enqueue workers, write operation logs, sync assets, pin config commits, or record snapshots.

Before rehearsing agent allowlisted tool invocation, generate the no-call agent tool rehearsal plan:

```bash
assops-tool release agent-tool-rehearsal-plan \
  assops-demo \
  codex-cli \
  .assops/release-notes/agent-tool-rehearsal-plan.md
```

The plan accepts only a safe project slug and runtime key. It lists agent task/runtime evidence, graph-backed context readiness, approval-gated `agent.execute`, worker claim evidence, allowlisted tool review, terminal `agent_tool_calls` audit evidence, sanitized result callback observation, tool execution arming, allowlisted tool-invocation review, tool-call audit snapshot, and tool-arming snapshot checks to collect without accepting or storing prompts, runtime config, environment variables, tool input/output, raw tool input/output, workspace paths, repository URLs, patch/diff/file content, provider URLs, command output, tokens, credentials, worker secrets, or operator notes. It does not invoke tools, materialize runtime config, materialize tool input, record tool output, start Codex CLI, apply patches, mutate repositories, call providers, update agent tasks, write operation logs, sync assets, or record snapshots.

Before rehearsing agent-driven code modification, generate the no-call agent code rehearsal plan:

```bash
assops-tool release agent-code-rehearsal-plan \
  assops-demo \
  codex-cli \
  .assops/release-notes/agent-code-rehearsal-plan.md
```

The plan accepts only a safe project slug and runtime key. It lists agent task/runtime evidence, `context.generate`, approval-gated `agent.execute`, worker dispatch audit, `codex.execution.plan`, `patch.prepare`, source checkout/branch-policy review, execution arming, tool-call audit snapshot, tool-arming snapshot, and code-audit snapshot checks to collect without accepting or storing repository URLs, workspace paths, branch names, prompts, tool input/output, patch/diff/file content, test commands/output, provider URLs, Git output, command output, tokens, credentials, or operator notes. It does not start Codex CLI, materialize runtime config, checkout source, bind workspaces, create branches, prepare or apply patches, run tests, invoke commit_push_spark, commit, push refs, create provider reviews, update agent tasks, write operation logs, sync assets, or record snapshots.

Before rehearsing live Argo pod log retrieval for the demo environment, generate the no-call pod-log rehearsal plan:

```bash
assops-tool release pod-log-rehearsal-plan \
  assops-demo \
  https://assops.example.com \
  prod \
  assops \
  .assops/release-notes/pod-log-rehearsal-plan.md
```

The plan validates the project slug, public staging HTTPS origin, environment identifier, and Kubernetes namespace shape, then lists the namespace-scoped kubeconfig review, token subject/RBAC review, approval request, sanitized result metadata, and pod-log audit snapshot evidence to collect. Localhost, private IP, `.local`, path, query, fragment, and userinfo origins are rejected. It does not read kubeconfig, call Kubernetes or Argo, open log streams, create approvals, enqueue workers, record snapshots, store log bodies, or expose cluster tokens, authorization headers, client keys, pod env, secret mounts, raw provider responses, raw log bodies, or redacted log bodies.

The first live pod-log backend is opt-in and read-only. Set `ASSOPS_KUBERNETES_LOGS_ENABLED=true` only after the target namespace has a reviewed `kubernetes_environment` row and the worker can read a namespace-scoped kubeconfig from `ASSOPS_KUBECONFIG_SECRET_DIR` (default `/etc/assops/kubeconfigs`). The `kubeconfig_secret_ref` stored in ASSOPS is a relative file reference under that directory, never kubeconfig content. The worker rejects absolute paths, `..`, secret-shaped refs, directories, group/world-writable files, files over 1 MiB, and files that do not look like kubeconfig documents. Keep `ASSOPS_KUBERNETES_LOG_PREVIEW_ENABLED=false` outside private test environments; when enabled, the worker caps `kubectl logs --tail` at 200 lines and stores a 64 KiB best-effort redacted log preview in `operation_runs.result` only, not in operation logs or asset status snapshots.

Operational contract for kubeconfig files:

- Provide kubeconfig files out of band through a read-only Secret or volume mount; ASSOPS never creates or rotates them.
- Keep files namespace-scoped and least-privileged for pod log reads only.
- Use permissions such as `0400` or `0600`; avoid group/world writable modes.
- Rotate by writing a new file and atomically renaming it within the same directory, rather than overwriting in place.
- The Argo/Kubernetes UI can use the same reviewed binding to run `kubectl get pods -o json` and show only sanitized pod metadata for selection: pod name, phase, container names, ready container count, restart count, and creation time.
- `kubectl logs` is invoked without a shell and with a 30 second timeout. Failures are recorded as failed operations without automatic retry.
- Operation results store sanitized metadata such as backend state, pod identity, line count, truncation flag, and timestamps. With `ASSOPS_KUBERNETES_LOG_PREVIEW_ENABLED=true`, operation results can also store a capped best-effort redacted preview so operators can inspect the latest approved audit from the UI. The latest preview is associated with `preview_operation_run_id`; status snapshots retain only preview metadata, not the preview text. They do not store stdout, stderr, raw Kubernetes responses, kubeconfig content, tokens, authorization headers, or raw log bodies; preview redaction is not a substitute for namespace/RBAC scoping.
- Deployment rollout restart is a separate opt-in write path. Set `ASSOPS_KUBERNETES_RESTARTS_ENABLED=true` only after the namespace kubeconfig is reviewed for Deployment patch/restart access and the Kubernetes environment row has `rbac_restart_pods_status=reviewed`; the worker performs `kubectl auth can-i patch deployment/<name>` and `kubectl rollout restart --dry-run=server` before the real rollout restart, and stores only sanitized metadata.

## First Deployable Test Checklist

For a first private test environment using Compose:

1. Copy `deploy/.env.prod.example` to `deploy/.env.prod` and replace every `change-me-*` value.
2. Start the stack with `docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml up -d --build`.
3. Confirm `web`, `gateway`, `worker`, and `node-worker` are healthy with `docker compose --env-file deploy/.env.prod -f deploy/compose.prod.yml ps`.
4. Log in through the web UI and create a project.
5. Add an Argo connection, sync apps, and confirm deployment targets appear.
6. For pod-log metadata audit, provide a namespace-scoped kubeconfig file through either `persistence.kubeconfigs.existingSecretName` or the `assops_kubeconfigs` volume, set `ASSOPS_KUBERNETES_LOGS_ENABLED=true`, optionally set `ASSOPS_KUBERNETES_LOG_PREVIEW_ENABLED=true` only for private test preview, restart gateway/worker, then create a Kubernetes environment row whose environment, cluster, namespace, and relative kubeconfig ref match the deployment target.
7. In the Argo Pod log query card, refresh pods for the deployment target, select a pod/container from the sanitized metadata list, then run the query preview. Only request an audit after the live backend tag is ready; the result records sanitized metadata and, when explicitly enabled, a capped best-effort redacted preview.
8. Add Git remotes for the project repository, sync GitHub Actions, and verify action runs/artifact summaries appear from the local read model.
9. Create or sync a tag run, refresh GitHub Actions for the tag target, and record the local sanitized snapshots when the UI marks them ready.

For Helm-based test environments, start from `deploy/helm/assops/values.test.example.yaml`, then provide the referenced external application Secret and database URL out of band. The chart mounts `/etc/assops/kubeconfigs` into gateway and worker from either `persistence.kubeconfigs.existingSecretName` or the `persistence.kubeconfigs` PVC, and exposes `env.kubernetesLogsEnabled`, `env.kubeconfigSecretDir`, and `env.kubectlPath`.

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
The health response includes `component`, `version`, `commit`, and `build_time`; set the matching environment variables or Helm `env.*` values during release so the deployed build can be verified from the web `/healthz` proxy and each internal service.

## Security Notes

- Terminate TLS at a reverse proxy or load balancer in front of the `web` service.
- Set `ASSOPS_GATEWAY_URL` to the public HTTP(S) origin before wiring Gitea/GitHub webhook callbacks, for example `https://assops.example.com` with no path, query string, or fragment.
- Generate the no-call provider callback rehearsal plan before wiring staging providers:

  ```bash
  assops-tool release callback-rehearsal-plan \
    https://assops.example.com \
    .assops/release-notes/callback-rehearsal-plan.md
  ```

  The plan validates the public callback origin shape and lists the Gitea/GitHub test-delivery, replay-proof, threshold-audit, threshold-configuration, provider-metrics comparison, and sanitized snapshot evidence to collect without calling providers or storing payloads, headers, tokens, provider responses, or operator notes.
- Keep `ASSOPS_WEBHOOK_SECRET_KEY` stable. Rotated webhook secrets are encrypted with it.
- Keep `ASSOPS_LOCAL_BARE_BASE_DIRS` pointed at a dedicated ASSOPS-owned directory; project template `local_bare` remotes outside that path are rejected.
- Repo sync, ref refresh, tag creation, and tag lookup operations that use `local_bare` remotes are limited to existing bare repositories under `ASSOPS_LOCAL_BARE_BASE_DIRS`.
- Keep `ASSOPS_CONFIG_GIT_LOCAL_BARE_WRITES_ENABLED=false` outside private test environments. When enabled, config scaffold Git writes require exactly one `local_bare` config remote under `ASSOPS_LOCAL_BARE_BASE_DIRS` and persist only sanitized result metadata plus local synced-state.
- Do not mount writable SSH directories into the web service.
- Before rehearsing SSH verify/exec against a real authorized machine, generate the no-call SSH rehearsal plan:

  ```bash
  assops-tool release ssh-rehearsal-plan \
    assops-demo \
    prod \
    .assops/release-notes/ssh-rehearsal-plan.md
  ```

  The plan accepts only a safe project slug and environment label. It lists the approval-gated `ssh.verify`/`ssh.exec` evidence, operation-to-command-to-machine graph proof, target-environment proof, and rehearsal snapshot sequence without accepting or storing hostnames, IP addresses, usernames, ports, SSH key paths, known_hosts bodies, command text, stdout/stderr, runbook URLs, fixture IDs, operator identities, approval notes, or incident details. It does not read SSH keys, open sockets, start SSH, enqueue workers, create approvals, write operation logs, sync assets, or record snapshots.
- Restrict access to the Compose host and Docker socket.

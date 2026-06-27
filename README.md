# ASSOPS

ASSOPS is an operations control-plane MVP for projects, Git remotes, worker jobs, node workers, AI runtime context, approval-gated agent task audits, Argo app sync, and controlled SSH command execution.

## Stack

- Backend: Go HTTP API
- Worker: Go control-worker
- Node Worker: Go polling worker
- Tool: Go CLI
- Frontend: Vite + React + TypeScript + Ant Design
- Database: PostgreSQL
- Local deploy: Docker Compose

## Quick Start

Run the full local/test stack with Docker Compose:

```bash
docker compose -f deploy/docker-compose.yml up --build
```

Open `http://localhost:8080`.

For hot-reload development, run only PostgreSQL in Compose and start the
processes locally:

```bash
docker compose -f deploy/docker-compose.yml up -d postgres
go run ./backend/cmd/gateway
go run ./backend/cmd/worker
go run ./backend/cmd/node-worker
cd web && pnpm install && pnpm dev
```

Open `http://localhost:5173` for the Vite dev server.

Default local development login:

- Email: `admin@assops.local`
- Password: `admin1234`

The Helm chart uses the bootstrap admin values from `deploy/helm/assops/values.yaml` or your external Secret.

Override with:

```bash
ASSOPS_ADMIN_EMAIL=you@example.com ASSOPS_ADMIN_PASSWORD='change-me' go run ./backend/cmd/gateway
```

## Useful Commands

```bash
make postgres
make compose-up
make compose-down
make gateway
make worker
make node-worker
make web
make test
make build
```

## CI And Deployment

- CI workflow: `.github/workflows/ci.yml`
- Scheduled restore rehearsal workflow: `.github/workflows/restore-rehearsal.yml`
- Production retained backup artifact workflow: `.github/workflows/production-retained-backup.yml`
- Production backup restore rehearsal workflow: `.github/workflows/production-restore-rehearsal.yml`
- Release candidate workflow: `.github/workflows/release.yml`
- Production promotion workflow: `.github/workflows/promote-production.yml`
- Dependabot maintenance: `.github/dependabot.yml`
- Production-shaped Compose: `deploy/compose.prod.yml`
- Production promotion RBAC example: `deploy/k8s/promotion-rbac.yaml`
- Multi-target Docker build: `Dockerfile`
- Deployment runbooks: `docs/deploy-production.md`, `docs/deploy-helm.md`
- GitHub branch protection template: `.github/rulesets/main-required-checks.json`
- Code owners: `.github/CODEOWNERS`

CI validates workflow syntax/semantics with `actionlint`, secret scanning with Gitleaks, Go tests, `go vet`, frontend build, Compose config, the gateway, Helm test smoke, and release image script self-tests, database backup/restore rehearsal, Helm lint/template for default, test, private-test overlay, and production example values, the test Helm readiness plan, a disposable kind-based Helm install smoke test with gateway `/healthz` build-metadata verification plus worker and node-worker health probes, Docker image builds, and `govulncheck`. The tag-based release workflow also verifies the published GHCR image manifests through the Helm image preflight after all four component images are pushed.
The scheduled restore rehearsal workflow runs weekly and on demand against disposable GitHub Actions PostgreSQL databases, then uploads the JSON rehearsal report as a short-retention artifact.
The production retained backup artifact workflow is protected-environment scoped, supports manual dispatch, and includes a weekly schedule that is disabled unless `ASSOPS_PRODUCTION_RETAINED_BACKUP_ENABLED=true` is configured. It creates one retained `assops-*.dump` artifact from `ASSOPS_ACTIVE_DATABASE_URL` for the restore rehearsal consumer. GitHub artifact size limits, additional backup encryption, external storage publication, mounted-path retention, and real environment secret/runner setup remain environment-owned.
The production backup restore rehearsal workflow is protected-environment scoped, supports manual dispatch, and includes a weekly schedule that is disabled unless `ASSOPS_PRODUCTION_RESTORE_REHEARSAL_ENABLED=true` is configured. It restores an existing retained `assops-*.dump` backup from either the latest unexpired named repository artifact or a self-hosted runner path into an explicitly configured disposable database secret, then uploads the private rehearsal report for release notes. Artifact sources must contain exactly one dump and no `.env`, kubeconfig, log, key, or PEM-like files. `assops-tool release backup-schedule-plan` can generate an offline readiness plan for choosing the retained backup source, retained-backup publication contract, runner, secrets, and artifact retention before enabling the scheduled path.
Dependabot is configured for weekly Go, web npm, GitHub Actions, and Docker image update PRs.
The release candidate workflow builds Linux amd64 binaries, the web bundle, a packaged Helm chart, checksums, and Docker image smoke builds for `v*` tags or manual runs. It also creates GitHub artifact attestations for release files. Tagged `v*` runs publish gateway, worker, node-worker, and web images to GHCR with version and commit-SHA tags, then attach registry-backed image attestations.
Apply the repository ruleset in `docs/github-branch-protection.md` before treating the repository default branch as the protected release branch.

## Context Files

Create a project in the UI, then generate context from Project Detail or call:

```bash
curl -X POST http://localhost:8080/api/projects/$PROJECT_ID/context/generate \
  -H "Authorization: Bearer $ASSOPS_TOKEN"
```

Generated files are written under `.assops/context/<project-id>/`:

- `ASSOPS_CONTEXT.md`
- `assops-context.json`
- `tool-manifest.json`

When the canonical ledger has been backfilled with `assops-tool db sync-assets`, the generated context also includes an `asset_graph` snapshot with canonical assets, relations, recent status snapshots, type counts, and health counts for read-only agent planning. Deployment targets include dry-run execution readiness, and rollback points include a `rollback_guardrail` summary that keeps preview-only execution mode visible in generated context and agent plans. Approved agent execution currently records a simulation-only tool-call audit (`context.generate`, `plan.review`, `runtime.check`, and `patch.prepare`) tied to the operation run. The runtime check records the selected project/global AI runtime readiness and Codex CLI metadata readiness gates without exposing runtime config, and patch preparation records structured readiness gates showing that Codex CLI process spawning, repository mutation, and pull request creation remain disabled.

The CLI can read those files:

```bash
go run ./backend/cmd/assops-tool project brief
go run ./backend/cmd/assops-tool --token "$ASSOPS_TOKEN" project readiness
go run ./backend/cmd/assops-tool repo remotes
go run ./backend/cmd/assops-tool remote actions
go run ./backend/cmd/assops-tool plan validate
```

Database operations:

```bash
go run ./backend/cmd/assops-tool db migrate
go run ./backend/cmd/assops-tool db sync-assets
go run ./backend/cmd/assops-tool db backup-retain .assops/backups 3
go run ./backend/cmd/assops-tool db rehearse-restore .assops/backups/assops-YYYYMMDD-HHMMSS.dump 'postgres://localhost:5432/assops_restore_test?user=USER&sslmode=disable' .assops/release-notes/restore-rehearsal.json
go run ./backend/cmd/assops-tool release validate-bundle .assops/release-artifacts .assops/release-notes/restore-rehearsal.json
gh workflow run production-retained-backup.yml -f github_environment=production -f runner=ubuntu-latest -f artifact_name=retained-assops-backup -f retention_days=14 -f keep_count=3
make production-backup-rehearsal-plan REPO=nathan77886/ass-ops BACKUP_SOURCE=artifact:retained-assops-backup OUTPUT=.assops/release-notes/production-backup-rehearsal-plan.md
go run ./backend/cmd/assops-tool release backup-schedule-plan nathan77886/ass-ops production ubuntu-latest '17 3 * * 1' artifact:retained-assops-backup 14 .assops/release-notes/backup-schedule-plan.md
make first-deployable-check
make first-deployable-handoff-plan
make release-validate-bundle ARTIFACT_DIR=.assops/release-artifacts REHEARSAL_REPORT=.assops/release-notes/restore-rehearsal.json
make release-helm-values GHCR_OWNER=nathan77886 VERSION=v0.1.0 OUTPUT=.assops/release-notes/helm-values-v0.1.0.yaml
make release-helm-test-readiness-plan OUTPUT=.assops/release-notes/helm-test-readiness-plan.md
make release-promotion-plan REPO=nathan77886/ass-ops GHCR_OWNER=nathan77886 VERSION=v0.1.0 ARTIFACT_DIR=.assops/release-artifacts REHEARSAL_REPORT=.assops/release-notes/restore-rehearsal.json HELM_VALUES=.assops/release-notes/helm-values-v0.1.0.yaml OUTPUT=.assops/release-notes/promotion-plan-v0.1.0.md
make release-backup-schedule-plan REPO=nathan77886/ass-ops ENV=production RUNNER=ubuntu-latest CRON='17 3 * * 1' BACKUP_SOURCE=artifact:retained-assops-backup RETENTION_DAYS=14 OUTPUT=.assops/release-notes/backup-schedule-plan.md
make production-backup-rehearsal-plan REPO=nathan77886/ass-ops BACKUP_SOURCE=artifact:retained-assops-backup OUTPUT=.assops/release-notes/production-backup-rehearsal-plan.md
ASSOPS_HELM_ROLLOUT_REHEARSAL_REPO=nathan77886/ass-ops ASSOPS_HELM_ROLLOUT_REHEARSAL_GHCR_OWNER=nathan77886 ASSOPS_HELM_ROLLOUT_REHEARSAL_VERSION=v0.1.0 ASSOPS_HELM_ROLLOUT_REHEARSAL_ENV_VALUES=.assops/release-notes/values.production.reviewed.yaml ASSOPS_HELM_ROLLOUT_REHEARSAL_RELEASE_VALUES=.assops/release-notes/helm-values-v0.1.0.yaml ASSOPS_HELM_ROLLOUT_REHEARSAL_PLAN_OUTPUT=.assops/release-notes/helm-rollout-rehearsal-plan.md make helm-rollout-rehearsal-plan
make release-rehearsal-plans-self-test
make rehearsal-make-targets-self-test
make first-deployable-handoff-plan-self-test
make release-validate-bundle-self-test
make release-promotion-plan-self-test
make first-deployable-prereqs
make compose-config-check
make compose-smoke-local-images
make compose-smoke-local-images-self-test
ASSOPS_COMPOSE_SMOKE_BUILD=false make compose-smoke
make docker-pg18-runtime-up
make docker-pg18-runtime-check
make cloudflare-docker-route-check
make cloudflare-docker-runtime-evidence
make github-ruleset-evidence
make first-deployable-external-audit
make first-deployable-local-runtime-check
make compose-smoke
make release-images
make helm-test-preflight
make helm-test-image-preflight
make helm-test-smoke
make helm-production-hardening-self-test
make workflow-safety-self-test
```

`make first-deployable-check` is the local pre-test-deploy gate. It requires `go`, `pnpm`, `helm`, Docker Compose, `curl`, and `python3`, then runs backend tests, Compose config/migration mount checks, the web i18n/build gate, every checked-in first-deployable self-test target, the checked-in Helm test readiness plan, Helm lint, and Helm rendering for default, test, private-test, and production values. The coverage audit self-test fails if a Make self-test target is missing from the gate, deployment docs, or completion audit. The API smoke self-test covers the seeded project-to-repository-to-GitHub remote chain plus Kubernetes environment/deployment target readiness, pod metadata listing, and Argo pod-log preview guardrails without pulling logs, creating approvals, or mutating Kubernetes.
`make first-deployable-prereqs` checks only those local tool prerequisites and gives a targeted install hint when a required binary such as Helm v3 or Docker is missing.
`make compose-config-check` validates dev and production Compose manifests offline with placeholder non-secret values, and verifies every migration file is mounted and world-readable for fresh PostgreSQL initialization.
`make compose-smoke` is the disposable local runtime rehearsal: it builds gateway, worker, node-worker, and web images, starts an isolated fresh PostgreSQL volume, waits for service health, seeds demo data inside the temporary database, runs the gateway API smoke through the web entrypoint, verifies worker/node-worker health, and removes its temporary containers and volumes. The API smoke covers health/login/projects/queue plus first-version read surfaces for assets, operations, approvals, repo sync/tag runs, AI runtimes, project-level Argo/Kubernetes/deployment/SSH/webhook/agent lists, repository-to-remote GitHub Actions/labels lists, and field-level GitHub artifact/tag/label evidence; compose smoke requires the seeded project so those project-level and repository-level probes cannot be skipped.
`make compose-smoke-local-images` builds current gateway, worker, node-worker, tool, and web smoke images from local binaries and `web/dist` using an existing local base image (`nginx:1.27-alpine` by default). Use it with `ASSOPS_COMPOSE_SMOKE_BUILD=false make compose-smoke` when the Docker registry mirror cannot resolve the normal `golang`, `node`, or `alpine` build images. `make compose-smoke-local-images-self-test` uses fake `go` and `docker` binaries to lock that image-name/base-image/Dockerfile contract without building images.
`make docker-pg18-runtime-up` rebuilds the long-lived local Docker runtime from current binaries and `web/dist`, applies migrations to the host PG18 `assops` database, seeds demo data, recreates the `assops-live-pg18-*` containers, and runs the Docker PG18 runtime check. It uses Docker only and does not create or mutate k3s resources.
`make docker-pg18-runtime-check` verifies the long-lived local Docker runtime backed by the host PG18 container: the `assops-live-pg18-*` containers are running, the PG18 `assops` database has the latest checked-in migration, and the gateway API smoke passes through the Docker web entrypoint. It reports the Cloudflare `/api` route status separately so a static Pages response is not mistaken for a local Docker failure.
`make cloudflare-docker-route-check` runs the same Docker/PG18 runtime checks but fails until `https://ass-ops-api.4nathan.com/api/auth/me` returns gateway JSON instead of static frontend HTML. Use it after updating the Cloudflare Tunnel routing to point the API hostname at the Docker web entrypoint. The frontend hostname `https://ass-ops.4nathan.com` remains bound to the `ass-ops` Worker.
`make cloudflare-docker-runtime-evidence` verifies the same Docker/PG18 runtime plus full local and public API smoke through `https://ass-ops-api.4nathan.com`, confirms the `https://ass-ops.4nathan.com` Worker frontend serves HTML, confirms the Worker `/api` compatibility proxy reaches gateway JSON, then writes a sanitized JSON evidence file to `.assops/release-notes/cloudflare-docker-runtime-evidence.json`. It performs live Docker, PostgreSQL, local HTTP, and Cloudflare HTTP checks, so keep it outside the offline `first-deployable-check` gate.
When deploying the Cloudflare Worker frontend on `https://ass-ops.4nathan.com`, build with `VITE_API_BASE=https://ass-ops-api.4nathan.com` or use the checked-in production-domain fallback in `web/src/main.tsx`; local Docker and Vite keep using relative `/api`. The deployed Worker also keeps a compatibility proxy for `/api/*` and `/healthz` so older cached frontend bundles still reach `ass-ops-api`.
`make github-ruleset-evidence` verifies the real GitHub repository ruleset against `.github/rulesets/main-required-checks.json`, checks that the default branch has the expected applied rules, writes `.assops/release-notes/github-ruleset-evidence.json`, and updates `.assops/release-notes/external-evidence-status.local.json` with the ruleset evidence marked verified. It calls the GitHub API through the current `gh` login but does not print token values, push commits, or modify workflows.
`make first-deployable-external-audit` reads the local external evidence status file, checks GitHub workflow runs, tags, releases, environments, secret names, artifacts, and GHCR package visibility through the current `gh` login, then writes `.assops/release-notes/first-deployable-external-audit.json` with the verified item count, remaining blockers, and non-secret `next_actions` command templates/evidence expectations for each blocker. The target immediately validates the generated audit shape. It is read-only and does not trigger workflows, push tags, publish packages, deploy Helm, or read secret values.
`make first-deployable-external-audit-validate AUDIT_FILE=.assops/release-notes/first-deployable-external-audit.json` validates that audit shape offline: every blocker must have `next_actions`, secret policy flags must stay false, and command templates must avoid secret-shaped material or destructive markers.
`make first-deployable-external-audit-runbook AUDIT_FILE=.assops/release-notes/first-deployable-external-audit.json` renders those blockers and `next_actions` into `.assops/release-notes/first-deployable-external-audit-runbook.md` for an external operator. The runbook generator validates the audit first, writes command templates only, and validates that the Markdown still covers every blocker/action without secret-shaped material. `make first-deployable-external-audit-runbook-validate RUNBOOK_FILE=.assops/release-notes/first-deployable-external-audit-runbook.md` reruns that Markdown check against the current audit file.
`make first-deployable-local-runtime-check` runs `first-deployable-check`, builds current local Compose smoke images, then runs `compose-smoke` in no-build mode. Use it when you need a full local runtime proof without depending on external Docker registry metadata during the Compose build step.
`make release-images` builds the four runtime images with the same `ghcr.io/<owner>/assops-<component>:<version>` naming used by the release workflow. It only builds by default; set `ASSOPS_RELEASE_IMAGE_PUSH=true` after `docker login` when intentionally publishing to a registry. `make release-images-self-test` uses a fake Docker binary to verify the image naming and push gating contract without building or pushing images.
`make release-validate-bundle-self-test` creates a temporary release artifact bundle, SHA256SUMS, and restore rehearsal report, then verifies both the CLI and Make `release-validate-bundle` path accept the good bundle and reject a checksum mismatch. It writes only temporary local files and does not read real release artifacts or backup data.
`make release-promotion-plan-self-test` creates a temporary release bundle and generated Helm values, then verifies the Make `release-promotion-plan` path emits the promotion checklist with attestation, image, Helm, smoke, and rollback guardrails. It also verifies mismatched Helm values and malformed repository names are rejected without calling GitHub, registries, Helm, Kubernetes, or databases.
`make production-backup-rehearsal-plan` validates the intended protected-environment backup/restore rehearsal inputs offline: repository, environment, runner, cron, retained backup source shape, artifact/report names, retention days, and dangerous backup source markers. It writes a checklist without reading database URLs, printing secrets, triggering GitHub Actions, or connecting to PostgreSQL.
`make helm-rollout-rehearsal-plan` validates the intended protected-environment Helm rollout inputs offline: repository, GHCR owner, release version, namespace, release name, environment values, release-image values, optional previous values, and optional restore rehearsal report. It writes a checklist without reading kubeconfigs, reading Secret data, calling GitHub, contacting registries, running Helm, invoking Argo, or mutating Kubernetes.
`make first-deployable-handoff-plan` writes `.assops/release-notes/first-deployable/` with a local branch-protection plan, production backup rehearsal plan, generated release Helm values, Helm rollout rehearsal plan, Markdown and machine-readable completion audits, JSON Schemas for machine-readable audit/checklist/status files, checksum manifest, and a handoff index for the external actions that still need protected-environment owners. It does not call GitHub, contact registries, run Helm against a cluster, invoke Argo, connect to PostgreSQL, read secrets, or push images. `make first-deployable-handoff-plan-manifest-validate` checks the generated manifest safety flags, file paths, and checksums; `make first-deployable-handoff-validate` also checks required files, schema identities, completion audit blockers, status-to-checklist checksum binding, checklist/status ids, owner/required-evidence traceability, completion-blocker flags, and local-plan references across the pack. `make first-deployable-handoff-plan-self-test` generates the same pack in a temporary directory and verifies the safety boundary plus required files. After external owners fill a copy of `external-evidence-status.example.json`, run `make first-deployable-external-evidence-validate EVIDENCE_FILE=/path/to/external-evidence-status.json` to check the evidence status structure, source-checklist checksum shape, owner/required-evidence fields, UTC `verified_at` timestamps, reference shape, rejected-entry summaries, and secret-shaped text without validating the remote systems themselves. Before closing the deployable goal, run `make first-deployable-external-evidence-complete-validate EVIDENCE_FILE=/path/to/external-evidence-status.json`; it fails until every external evidence entry is marked `verified` with required proof fields.
`make release-rehearsal-plans-self-test` exercises the no-call release checklist generators for callback, live demo import, pod logs, SSH, GitHub tag evidence, config repository evidence, agent tool audit, agent code audit, and branch protection. It writes only temporary local Markdown files and verifies the safety boundary text without calling providers, GitHub, Git, Kubernetes, Argo, SSH, Codex CLI, or ASSOPS APIs.
`make rehearsal-make-targets-self-test` verifies the documented short Make variable forms for production backup rehearsal and Helm rollout rehearsal targets, so `REPO=... OUTPUT=... make ...` paths stay aligned with the underlying `ASSOPS_*` environment variable scripts.
`make helm-test-preflight` is the read-only pre-install gate for a test cluster. It lints and renders the chart, checks the namespace, verifies the external application Secret keys, verifies the kubeconfig Secret key, and runs non-mutating `kubectl auth can-i` checks for pod metadata, pod logs, and deployment restart RBAC without installing or mutating Kubernetes resources.
`deploy/helm/assops/values.test.private.example.yaml` is a copyable private test overlay template for chart-managed PostgreSQL, release image tags, and registry pull Secrets.
`make helm-test-image-preflight` renders the chart and checks that every referenced image has registry metadata available to the current Docker credentials before a Helm install is attempted. Set `ASSOPS_HELM_IMAGE_PREFLIGHT_VALUES` and `ASSOPS_HELM_IMAGE_PREFLIGHT_EXTRA_VALUES` for private test overlays, and use `image.pullSecrets` in that overlay when the cluster needs registry credentials.
`make provider-review-live-test-plan` renders the same private Helm overlay locally and verifies provider-review live execution switches, application Secret wiring, and secret-shaped render safety before a real GitHub `execute-live` / `cleanup-live` rehearsal. Set `ASSOPS_PROVIDER_REVIEW_LIVE_TEST_VALUES` and `ASSOPS_PROVIDER_REVIEW_LIVE_TEST_EXTRA_VALUES` to the same files used for Helm install; the check does not read token values, call GitHub, push images, or contact Kubernetes.
`make helm-test-smoke` is the read-only post-test-deploy gate for a Helm release. It waits for gateway, worker, node-worker, and web rollouts, checks worker health endpoints, port-forwards the web/worker health Services, and runs the gateway API smoke without writing ASSOPS rows.
`make helm-production-hardening-self-test` renders the production example values locally and asserts the expected hardening signals: external Secret use, external PostgreSQL, TLS ingress, disabled service-account token automount, NetworkPolicies, PodDisruptionBudgets, non-root/read-only Go containers, retained StorageClass PVCs, and GHCR release image references. It does not contact a cluster, registry, Secret store, or cloud provider.
`make workflow-safety-self-test` parses the GitHub Actions workflow YAML locally and asserts the first-deployable safety contracts: actionlint/Gitleaks jobs exist, release image publication is tag-gated, production backup/restore schedules stay default-off behind protected environments, promotion deploy requires the explicit `deploy` input and production environment, and the CI restore rehearsal warns against production credentials. It is an offline structural check and does not replace CI actionlint.

## Runtime Integrations

Real local adapters are available for:

- Git repository sync and tag push through the control worker. Successful repository tags automatically enqueue a sanitized remote tag lookup, and GitHub target remotes also enqueue a GitHub Actions/artifact refresh operation tied back to the tag run.
- GitHub Actions run, artifact, and repository-label metadata sync through the GitHub REST API, with artifact names/sizes/expiry and label names/colors/descriptions shown from the local read model and no download URLs, provider URLs, or provider tokens exposed.
- GitHub `workflow_run` webhooks that update the Actions read model.
- Argo CD application sync through the Argo CD REST API.
- SSH command execution on registered SSH machines.

Useful optional environment variables:

```bash
ASSOPS_GITHUB_ACTIONS_READ_TOKEN='github fine-grained token with actions read access'
ASSOPS_WEBHOOK_SECRET_KEY='long random key for webhook secret encryption'
ASSOPS_APPROVAL_WEBHOOK_URL='optional HTTP endpoint for approval pending/expired notifications'
ASSOPS_APPROVAL_WEBHOOK_TOKEN='optional bearer token for approval notifications'
ASSOPS_ARGO_READ_TOKEN='optional Argo token; only used by connections created with use_env_token=true'
ASSOPS_KUBERNETES_LOGS_ENABLED='false'
ASSOPS_KUBERNETES_LOG_PREVIEW_ENABLED='false'
ASSOPS_KUBERNETES_RESTARTS_ENABLED='false'
ASSOPS_KUBECONFIG_SECRET_DIR='/etc/assops/kubeconfigs'
ASSOPS_KUBECTL_PATH='kubectl'
ASSOPS_SSH_KEY_DIR='/etc/assops/ssh/keys'
ASSOPS_SSH_KNOWN_HOSTS_DIR='/etc/assops/ssh/known_hosts'
ASSOPS_LOCAL_BARE_BASE_DIRS='/var/lib/assops/bare-repos'
ASSOPS_CONFIG_GIT_LOCAL_BARE_WRITES_ENABLED='false'
ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION='false'
ASSOPS_ARM_PROVIDER_REVIEW_MUTATION='false'
ASSOPS_GITHUB_TEMPLATE_TOKEN='optional GitHub token for project template repository creation'
ASSOPS_GITEA_TEMPLATE_TOKEN='optional Gitea token for project template repository creation'
ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MYORG='optional provider-account scoped GitHub template token'
ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_MYORG='optional provider-account scoped Gitea template token'
ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API='false'
```

Security defaults:

- Webhook connection secrets are shown once, then stored encrypted with `ASSOPS_WEBHOOK_SECRET_KEY`; set a strong non-default value in shared environments. Legacy plaintext rows remain readable for migration compatibility.
- Webhook URLs shown in the UI are built from `ASSOPS_GATEWAY_URL`; set it to the public gateway origin before wiring provider callbacks.
- Approval requests can require one or more approver decisions and notify an external HTTP endpoint with `ASSOPS_APPROVAL_WEBHOOK_URL` for pending, approved, rejected, and expired events; notification payloads include approval metadata but not the original request payload.
- SSH key paths and known_hosts paths must live inside the configured ASSOPS SSH directories.
- Argo server URLs must resolve to public HTTP(S) addresses; private, loopback, link-local, localhost, and DNS failures are rejected.
- Argo `insecure_skip_verify` and `use_env_token` can only be set by `admin` or `owner` users.
- Kubernetes pod-log metadata audits are disabled by default. Enable `ASSOPS_KUBERNETES_LOGS_ENABLED=true` only after gateway/worker can read a namespace-scoped kubeconfig from `ASSOPS_KUBECONFIG_SECRET_DIR` and the Kubernetes environment row has reviewed token-subject/RBAC metadata. The Argo/Kubernetes UI can then refresh deployment-target pod metadata with `kubectl get pods` and use those pod/container names for approval-gated log audits. `ASSOPS_KUBERNETES_LOG_PREVIEW_ENABLED=true` is a separate private-test overlay switch that caps `kubectl logs --tail` at 200 lines and stores a 64 KiB best-effort redacted preview in `operation_runs.result`; it never stores raw stdout/stderr, kubeconfig content, raw Kubernetes responses, or preview text in operation logs/status snapshots.
- Kubernetes rollout restarts are disabled separately by default. Enable `ASSOPS_KUBERNETES_RESTARTS_ENABLED=true` only for a namespace-scoped kubeconfig reviewed for Deployment restart access; the Kubernetes environment row must have `rbac_restart_pods_status=reviewed`, and the worker still runs `kubectl auth can-i patch deployment/<name>` plus a server dry-run before `kubectl rollout restart deployment/<name>`. Results store sanitized metadata only.
- SSH command output is sanitized before it is stored.
- Project template `local_bare` repository provisioning is limited to paths under `ASSOPS_LOCAL_BARE_BASE_DIRS`.
- Repo sync, ref refresh, tag creation, and tag lookup operations that use `local_bare` remotes require existing bare repositories under `ASSOPS_LOCAL_BARE_BASE_DIRS`.
- Config repository scaffold commits are disabled by default. Enable `ASSOPS_CONFIG_GIT_LOCAL_BARE_WRITES_ENABLED=true` only in a test environment with exactly one `local_bare` config remote under `ASSOPS_LOCAL_BARE_BASE_DIRS`; the worker writes fixed scaffold files, updates local synced-state metadata, and records only sanitized booleans/counts.
- Provider review live execution is disabled by default. For a private test environment, set both `ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION=true` and `ASSOPS_ARM_PROVIDER_REVIEW_MUTATION=true`, configure a GitHub template token env, approve the provider-review execution request, claim the current attempt, record live-readiness and mutation-arming evidence, then use the Approval audit `Execute live review` action. This first deployable path is GitHub-only and creates a review branch plus pull request; Gitea and generic stepwise provider adapters remain disabled.
- Before running that private GitHub path, run `make provider-review-live-test-plan` against the private Helm overlay to produce a local checklist and catch disabled execution/mutation switches or rendered secret-shaped literals. This is a no-network, no-token, no-k8s verifier; it proves only local render readiness, not real GitHub execution.
- Project template GitHub/Gitea repository provisioning reads provider account token environment names, never stored token values. Allowed names are `ASSOPS_GITHUB_TEMPLATE_TOKEN`, `ASSOPS_GITEA_TEMPLATE_TOKEN`, or provider-scoped `ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_*` / `ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_*`; local/private provider API hosts are blocked unless `ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API=true` is explicitly set for development. Provider Accounts include manual token-env rotation, safe automated token-env rotation execution from ready candidate metadata, a manual health check that verifies the configured token environment variable against the provider `/user` API, and sanitized status metadata. Template run results surface sanitized provider provisioning diagnostics such as provider type, HTTP status, owner, repository name, provider message, structured repository reconciliation summaries, and approval-gated provider review execution requests. Approval payloads include a content-redacted starter-file staging summary with file count, id, path, kind, and status only, plus a redacted provider API request plan with endpoint keys and payload-shape names and a provider review preflight reconciliation summary for credential/gate/adapter/operation status, including a redacted adapter contract for required capabilities and scopes and a redacted adapter execution blueprint for the request builder/response handler/idempotency ledger boundary, but no URL, token, token-env name, repository name, or file content. Approved provider review attempts persist redacted request and response-diagnostic summaries with the planned payload builder, response handler, execution status, idempotency requirements, success status class, and retryable status classes for each operation; attempt response status is limited to `pending`, `success`, `retryable`, `failed`, or `blocked`, with unknown values redacted as `blocked`. The attempt orchestration summary also exposes a redacted execution candidate with next operation, endpoint key, data-integrity metadata gates, execution-blocker gates, a redacted branch policy plan that blocks default/protected branch direct writes before review-branch PR/MR execution, a redacted request-validation preflight summary, and a nested live adapter contract plan without materializing URLs, headers, bodies, refs, file content, or credentials. Green data-integrity gates do not mean provider mutation is allowed. When `ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION=true`, `ASSOPS_ARM_PROVIDER_REVIEW_MUTATION=true`, and a GitHub template token env is configured, `POST /api/provider-review-attempts/{id}/execute-live` can manually run the atomic GitHub review executor for an approved, claimed, live-readiness-reviewed attempt. The executor creates an `assops/template/...` review branch, commits the staged starter files, opens the PR, and records only sanitized status class, provider review URL, execution phase, retryable flag, manual cleanup hint, idempotency hash, execution time, and cleanup flags; it does not store token values, request/response bodies, headers, repository refs, branch names, or file content. If automatic review-branch cleanup fails, `POST /api/provider-review-attempts/{id}/cleanup-live` is available only for the failed attempt that still has `review_branch_delete_required`; it deletes the review branch and records sanitized cleanup status without changing the original failed execution into success. Template remotes marked `protected` create the external repository but skip default-branch starter-file pushes by default unless metadata explicitly sets `allow_protected_branch_push`.
- The live adapter contract now records allowlisted request builder, provider client, execute method, response handler, and required capability names for the future adapter boundary while keeping provider API mutation disabled and sensitive request material suppressed.
- Provider review commit-file request builders now use the same allowlisted `build_redacted_file_batch_request` name across request summaries, execution blueprints, disabled request-builder plans, and live-adapter contracts.
- Provider review execution blueprints now derive operation builder and response-handler names from the same shared allowlist used by request materialization and live-adapter contracts, while keeping a generic redacted fallback for unknown operations.
- Provider review adapter tests now lock the GitHub/Gitea disabled adapter surface across runtime selection, request builder, provider client, execute method, response handler, live-adapter contract metadata, provider-send metadata, and retry/backoff metadata without materializing provider URLs, credentials, refs, branch names, request/response bodies, headers, or file content.
- Provider review request materialization, provider-send, and retry/backoff plans reject mismatched operation/endpoint pairs before any future provider request can be staged; attempt operations map to endpoint operations as `create_branch_ref -> create_branch_ref`, `commit_starter_files -> commit_files`, and `open_review_request -> open_review`.
- Provider review request materialization and response plans also reject mismatched payload-builder or response-handler names, so generic sanitized defaults cannot be staged for concrete provider operations.
- Provider review disabled adapter surfaces now reject same-provider operation/endpoint mismatches across runtime selection, request builders, provider clients, execute methods, response handlers, live adapters, and live-adapter contracts before a future adapter can bind them.
- Provider review dispatch metadata now requires both the execution claim plan and adapter contract to match the same operation and endpoint before downstream request validation can be treated as metadata-ready.
- Provider review downstream subplans now require matching mode, operation, and endpoint identity before branch policy, provider-send, retry/backoff, response result recording, transaction, provider-call boundary, or execution-lock metadata can be treated as ready; invocation, activation, execution-lock, transaction, provider-call boundary, and request-validation preflight paths also require the execution claim plan itself to match the same operation and endpoint before accepting claim or idempotency metadata readiness.
- Provider review transaction, provider-call boundary, and result-recording plans also require the response plan to carry the expected completed/planned/failed attempt-status transitions and dependency-unlock metadata for the same operation before treating response metadata as ready.
- Provider review invocation and activation summaries now also require each nested request, credential, runtime, transport, provider-send, response, transaction, execution-lock, branch-policy, or activation subplan to match the same operation and endpoint before mirroring its ready flag.
- Provider review attempt activation snapshots can now persist sanitized adapter activation/provider-call-boundary metadata for the current execution candidate into canonical asset status history, while still recording no provider request, no provider-call boundary write, disabled mutation, and no URLs/tokens/refs/branch names/file content/request or response bodies/headers/idempotency material/provider request ids.
- Provider review attempt send snapshots can now persist sanitized provider-send/transport/retry-backoff metadata for the current execution candidate into canonical asset status history, while still recording no provider request, no send attempt, disabled mutation, and no request URLs/paths/bodies/headers/authorization headers/idempotency keys/provider request ids/refs/branch names/file content/response bodies or headers.
- Provider review attempt response snapshots can now persist sanitized response/result/transaction/provider-call-boundary metadata for the current execution candidate into canonical asset status history, while still recording no provider response, no response record, no transaction record, disabled mutation, and no provider request ids/response bodies/headers/provider URLs/tokens/idempotency keys/refs/branch names/file content.
- A standalone GitHub review-branch executor now has isolated tests and guarded manual ledger entrypoints: it reads the base ref, creates an `assops/template/...` review branch, commits staged files to that branch, and opens a pull request while keeping token and file content out of returned metadata. On starter-file commit or PR creation failure it attempts to delete the review branch and records only the failed phase, retryable flag, and `review_branch_delete_required` cleanup hint when automatic cleanup cannot complete. The Approval audit UI can call this execution path only after the local attempt is claimed, live-readiness evidence is recorded, mutation arming is reviewed, and both provider-review execution gates are enabled; when cleanup is required it can also call the manual cleanup path and display sanitized cleanup status. Gitea, generic stepwise provider sends, retry workers, and automated cleanup workers remain later work.
- Provider review live launch plans still expose the redacted stepwise adapter contract (`create_branch_ref`, `commit_starter_files`, `open_review_request`) without materializing provider URLs, token env names, branch names, repository refs, request/response bodies, headers, or file contents. The first deployable execution slice intentionally bypasses that generic adapter layer and uses the narrower atomic GitHub executor plus sanitized ledger recording instead.

## MVP Boundaries

Implemented as real local code:

- Auth login and current user
- PostgreSQL migration and admin seed
- Dashboard first-version readiness checklist derived from canonical assets and recent operations
- Project, repository, GitRemote CRUD
- Operation runs, worker jobs, operation logs
- Control-worker queue consumption, PostgreSQL-only queue backend posture, and worker queue health summaries
- Node-worker register, heartbeat, claim, log upload, complete/fail
- Gateway, control-worker, and node-worker `/healthz` endpoints for Compose healthchecks
- AI runtime CRUD and verify marker
- Agent task, generated plan, approve plan, approval-gated execute-plan operation enqueue, and simulation-only tool-call audit with patch workflow guardrails and execution readiness gates
- Argo connection CRUD, Argo app sync/list, deployment posture summary, redacted dry-run deployment/rollback execution plans, and rollback point visibility
- SSH machine CRUD and controlled SSH command runs
- Asset Center inventory search, relation-degree ranked cross-project graph search, saved graph views, selected-asset relation graph, status history, manual graph relation edits, operation-run, worker-job, and project-template-run asset visibility with target/output links, and upstream/downstream dependency path queries
- Canonical asset ledger backfill/sync from current domain tables, deduplicated asset status snapshots, graph repair reporting, plus best-effort refresh after key asset-producing writes and worker operation completion
- Provider account management for GitHub/Gitea template repository creation, with masked token-env display, dry-run token rotation candidate planning, and template account selectors
- Gitea/GitHub webhook connection, HMAC verification, event audit asset graph visibility, connection health summaries, callback rehearsal readiness preview, RepoSyncAsset enqueue, and GitHub Actions/artifact/label read-model updates
- RepoSyncAsset archive/restore, filtered sync run history, per-asset sync health analytics, list-level risk summaries, 14-day trend, and provider/capacity signals with visible warning/danger thresholds
- Background approval expiry sweep with expired-event notifications
- Approval summary metrics, admin/owner approval rule editing with audit history, reminder-candidate SLA watch, manual and scheduled reminder delivery, escalation routing with destination adapter readiness previews, approval delegation/revocation, multi-approver progress, per-user decision audit, saved audit views, audit filters for status/action/requester/keyword/time windows, approval detail drilldown, and approval request/rule asset graph visibility
- SSE live operation log stream for selected operation runs
- Agent task list, read-only context plan generation from project operational context, agent-task/tool-call, SSH-command-run, and approval-request/rule asset graph entries, canonical asset graph snapshots, asset health snapshots, and tool-call audit visibility
- ContextBuilder writing ASSOPS context files
- assops-tool local context commands and operations API query
- assops-tool database migration history, locked migration apply, PostgreSQL backup/restore, retained backup, backup inspection, and guarded restore rehearsal commands
- Explicit `assops-tool db seed-demo` scenario fixtures for local/demo environments, including sample RepoSync/Webhook/Actions/Argo/approval/asset graph data
- First-pass Helm chart for Kubernetes-shaped gateway, worker, node-worker, web, migration job, and optional PostgreSQL resources
- Project template runs can create GitHub/Gitea provider repositories through provider accounts or backward-compatible explicit metadata, appear as canonical assets linked to their template and starter-file outputs, show sanitized external provider diagnostics, reconciliation guidance, provider review readiness gates, approval-gated PR/MR execution request previews with execution guardrail gates, redacted starter-file staging summaries, redacted provider API request plans visible as endpoint-key/payload-shape tags, provider review preflight reconciliation summaries with credential configured/present booleans and adapter contract capability tags, and sanitized Approval audit summaries for audit-only provider review approvals, retry repository provisioning for runs that already created project metadata, or initialize and push starter files to an explicitly configured local bare Git repository provider. Provider accounts expose token rotation summaries, safe automated rotation candidate plans, and an operator-triggered execution path that updates ready token-env metadata without reading token values or calling provider APIs.

Not yet first-class:

- Canonical Asset / AssetRelation write model across every asset-producing path.
- Asset dependency paths, manual graph edges, saved graph views, and cross-project graph search exist, and direct project/repository/remote/RepoSyncAsset lifecycle/run queue plus operation-run enqueue/status/cancel writes, worker-status/repo-tag writes and stale worker recovery, provider-account management, webhook-connection/delivery-health writes, webhook-event delivery read-model writes, approval request/rule lifecycle writes, AI-runtime and agent-task management, worker-node registration/heartbeat, Argo sync status/read-model writes, SSH/manual-relation create/delete, template-worker project creation/completion/retry writes, and GitHub Actions pipeline read-model writes now sync the canonical ledger in transaction. Canonical sync also prunes stale derived relations while preserving manual operator-curated edges and reports remaining graph repair counts, but transactionally maintained canonical writes across every asset-producing path are still deferred.
- External Gitea/GitHub project-template repository provisioning is first-pass only; provider accounts cover basic account selection, manual token-env rotation with due/soon/fresh visibility, account-level rotation planning, automated ready-candidate token-env execution, manual token/API health checks, protected-branch push avoidance, protected-branch strategy diagnostics, provider review readiness gates, approval-gated provider review execution requests with explicit execution guardrails, redacted starter-file staging summaries, redacted provider API request plans, provider review credential preflight reconciliation, redacted adapter contracts, redacted branch policy plans, redacted live adapter contract plans, redacted adapter rehearsal, explicit mutation arming config, redacted adapter execution blueprints, sanitized failure diagnostics, structured repository reconciliation guidance, operator-triggered provisioning retry, and the guarded GitHub-only manual atomic PR path. Gitea PR/MR execution and the generic stepwise provider adapter remain disabled.
- Production Kubernetes rollout/TLS/storage-class hardening.
- WebSocket/Redis-backed log fanout.
- Codex CLI process execution for AI tasks.
- Fully automated disaster-recovery rehearsal operations for production backups; the protected workflow now has an opt-in weekly schedule and offline schedule-readiness plan, but retained backup storage publication and environment-specific secret/runner wiring are still environment-owned.

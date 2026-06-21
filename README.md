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

```bash
docker compose -f deploy/docker-compose.yml up -d postgres
go run ./backend/cmd/gateway
go run ./backend/cmd/worker
go run ./backend/cmd/node-worker
cd web && pnpm install && pnpm dev
```

Open `http://localhost:5173`.

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
- Release candidate workflow: `.github/workflows/release.yml`
- Production promotion workflow: `.github/workflows/promote-production.yml`
- Dependabot maintenance: `.github/dependabot.yml`
- Production-shaped Compose: `deploy/compose.prod.yml`
- Production promotion RBAC example: `deploy/k8s/promotion-rbac.yaml`
- Multi-target Docker build: `Dockerfile`
- Deployment runbooks: `docs/deploy-production.md`, `docs/deploy-helm.md`
- GitHub branch protection template: `.github/rulesets/main-required-checks.json`
- Code owners: `.github/CODEOWNERS`

CI validates workflow syntax/semantics with `actionlint`, secret scanning with Gitleaks, Go tests, `go vet`, frontend build, Compose config, database backup/restore rehearsal, Helm lint/template for both default and production example values, a disposable kind-based Helm install smoke test, Docker image builds, and `govulncheck`.
The scheduled restore rehearsal workflow runs weekly and on demand against disposable GitHub Actions PostgreSQL databases, then uploads the JSON rehearsal report as a short-retention artifact.
Dependabot is configured for weekly Go, web npm, GitHub Actions, and Docker image update PRs.
The release candidate workflow builds Linux amd64 binaries, the web bundle, a packaged Helm chart, checksums, and Docker image smoke builds for `v*` tags or manual runs. It also creates GitHub artifact attestations for release files. Tagged `v*` runs publish gateway, worker, node-worker, and web images to GHCR with version and commit-SHA tags, then attach registry-backed image attestations.
Apply the repository ruleset in `docs/github-branch-protection.md` before treating `main` as the protected release branch.

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

When the canonical ledger has been backfilled with `assops-tool db sync-assets`, the generated context also includes an `asset_graph` snapshot with canonical assets, relations, recent status snapshots, type counts, and health counts for read-only agent planning. Approved agent execution currently records a simulation-only tool-call audit (`context.generate`, `plan.review`, `runtime.check`, and `patch.prepare`) tied to the operation run. The runtime check records the selected project/global AI runtime readiness without exposing runtime config; it does not mutate repositories or open PRs yet.

The CLI can read those files:

```bash
go run ./backend/cmd/assops-tool project brief
go run ./backend/cmd/assops-tool repo remotes
go run ./backend/cmd/assops-tool remote actions
go run ./backend/cmd/assops-tool plan validate
```

Database operations:

```bash
go run ./backend/cmd/assops-tool db migrate
go run ./backend/cmd/assops-tool db sync-assets
go run ./backend/cmd/assops-tool db backup-retain .assops/backups 3
go run ./backend/cmd/assops-tool db rehearse-restore .assops/backups/assops-YYYYMMDD-HHMMSS.dump 'postgres://assops:assops@localhost:5432/assops_restore_test?sslmode=disable' .assops/release-notes/restore-rehearsal.json
go run ./backend/cmd/assops-tool release validate-bundle .assops/release-artifacts .assops/release-notes/restore-rehearsal.json
make release-validate-bundle ARTIFACT_DIR=.assops/release-artifacts REHEARSAL_REPORT=.assops/release-notes/restore-rehearsal.json
make release-helm-values GHCR_OWNER=nathan77886 VERSION=v0.1.0 OUTPUT=.assops/release-notes/helm-values-v0.1.0.yaml
make release-promotion-plan REPO=nathan77886/ass-ops GHCR_OWNER=nathan77886 VERSION=v0.1.0 ARTIFACT_DIR=.assops/release-artifacts REHEARSAL_REPORT=.assops/release-notes/restore-rehearsal.json HELM_VALUES=.assops/release-notes/helm-values-v0.1.0.yaml OUTPUT=.assops/release-notes/promotion-plan-v0.1.0.md
```

## Runtime Integrations

Real local adapters are available for:

- Git repository sync and tag push through the control worker.
- GitHub Actions run sync through the GitHub REST API.
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
ASSOPS_SSH_KEY_DIR='/etc/assops/ssh/keys'
ASSOPS_SSH_KNOWN_HOSTS_DIR='/etc/assops/ssh/known_hosts'
ASSOPS_LOCAL_BARE_BASE_DIRS='/var/lib/assops/bare-repos'
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
- SSH command output is sanitized before it is stored.
- Project template `local_bare` repository provisioning is limited to paths under `ASSOPS_LOCAL_BARE_BASE_DIRS`.
- Project template GitHub/Gitea repository provisioning reads provider account token environment names, never stored token values. Allowed names are `ASSOPS_GITHUB_TEMPLATE_TOKEN`, `ASSOPS_GITEA_TEMPLATE_TOKEN`, or provider-scoped `ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_*` / `ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_*`; local/private provider API hosts are blocked unless `ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API=true` is explicitly set for development. Provider Accounts include manual token-env rotation, a manual health check that verifies the configured token environment variable against the provider `/user` API, and sanitized status metadata. Template run results surface sanitized provider provisioning diagnostics such as provider type, HTTP status, owner, repository name, and provider message. Template remotes marked `protected` create the external repository but skip starter-file pushes by default unless metadata explicitly sets `allow_protected_branch_push`.

## MVP Boundaries

Implemented as real local code:

- Auth login and current user
- PostgreSQL migration and admin seed
- Project, repository, GitRemote CRUD
- Operation runs, worker jobs, operation logs
- Control-worker queue consumption and worker queue health summaries
- Node-worker register, heartbeat, claim, log upload, complete/fail
- Gateway, control-worker, and node-worker `/healthz` endpoints for Compose healthchecks
- AI runtime CRUD and verify marker
- Agent task, generated plan, approve plan, approval-gated execute-plan operation enqueue, and simulation-only tool-call audit
- Argo connection CRUD, Argo app sync/list, deployment posture summary, and rollback point visibility
- SSH machine CRUD and controlled SSH command runs
- Asset Center inventory search, cross-project graph search, saved graph views, selected-asset relation graph, status history, manual graph relation edits, and upstream/downstream dependency path queries
- Canonical asset ledger backfill/sync from current domain tables, deduplicated asset status snapshots, plus best-effort refresh after key asset-producing writes and worker operation completion
- Provider account management for GitHub/Gitea template repository creation, with masked token-env display and template account selectors
- Gitea/GitHub webhook connection, HMAC verification, event audit, connection health summaries, RepoSyncAsset enqueue, and GitHub Actions read-model updates
- RepoSyncAsset archive/restore, filtered sync run history, per-asset sync health analytics, list-level risk summaries, 14-day trend, and provider/capacity signals
- Background approval expiry sweep with expired-event notifications
- Approval summary metrics, admin/owner approval rule editing with audit history, reminder-candidate SLA watch, manual and scheduled reminder delivery, escalation routing, approval delegation/revocation, multi-approver progress, per-user decision audit, saved audit views, audit filters for status/action/requester/keyword/time windows, and approval detail drilldown
- SSE live operation log stream for selected operation runs
- Agent task list, read-only context plan generation from project operational context, canonical asset graph snapshots, asset health snapshots, and tool-call audit visibility
- ContextBuilder writing ASSOPS context files
- assops-tool local context commands and operations API query
- assops-tool database migration history, locked migration apply, PostgreSQL backup/restore, retained backup, backup inspection, and guarded restore rehearsal commands
- Explicit `assops-tool db seed-demo` scenario fixtures for local/demo environments, including sample RepoSync/Webhook/Actions/Argo/approval/asset graph data
- First-pass Helm chart for Kubernetes-shaped gateway, worker, node-worker, web, migration job, and optional PostgreSQL resources
- Project template runs can create GitHub/Gitea provider repositories through provider accounts or backward-compatible explicit metadata, show sanitized external provider diagnostics on failure, retry repository provisioning for runs that already created project metadata, or initialize and push starter files to an explicitly configured local bare Git repository provider.

Not yet first-class:

- Canonical Asset / AssetRelation write model across every asset-producing path.
- Asset dependency paths, manual graph edges, saved graph views, and cross-project graph search exist, but transactionally maintained canonical writes across every asset-producing path are still deferred.
- External Gitea/GitHub project-template repository provisioning is first-pass only; provider accounts cover basic account selection, manual token-env rotation, manual token/API health checks, protected-branch push avoidance, sanitized failure diagnostics, and operator-triggered provisioning retry, while automated token rotation and provider-specific protected-branch reconciliation remain limited.
- Production Kubernetes rollout/TLS/storage-class hardening.
- WebSocket/Redis-backed log fanout.
- Codex CLI process execution for AI tasks.
- Scheduled disaster-recovery rehearsal automation for production backups.

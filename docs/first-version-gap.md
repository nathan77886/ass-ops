# ASSOPS First-Version Gap Notes

Last checked: 2026-06-22

Sources:

- Notion ASSOPS root: https://app.notion.com/p/383e7c17fa8180dc894aebee4a18b423
- Notion "想法": https://app.notion.com/p/383e7c17fa818123923bc8494b115be4
- Notion "产品设计": https://app.notion.com/p/383e7c17fa8181cbaff5c43beb3f61ca
- Notion "功能落地": https://app.notion.com/p/383e7c17fa818110a8b4d25889ef3978

## Current Shape

The codebase now has a working control-plane skeleton:

- Go gateway, control worker, node worker, and local CLI.
- PostgreSQL migrations with users, projects, project templates, repositories, git remotes, operation runs, worker jobs, AI runtime/task skeletons, SSH machines, Argo connections, deployment targets, deployment records, rollback points, adapter run tables, and the first Asset/AssetRelation tables.
- React/Ant Design console for dashboard, assets, projects, git remotes, operations, workers, AI runtime/tasks, Argo, and SSH.
- Real adapters for Git sync/tag, GitHub Actions sync, Argo app sync, and SSH command execution.
- Durable approval requests for high-risk operations and operator-only SSH command output.
- Context file generation for `.assops/context`.
- Derived asset inventory APIs that expose current projects, project templates, repositories, remotes, pipeline runs, hosts, Argo apps, deployment targets, runtimes, and node agents through a common asset shape.

This is stronger than a mock-only skeleton, but it is not yet the asset-centered product described in Notion.

## Notion Target

The product direction is an engineering asset management platform, not a CI/CD-only tool. The core abstractions are:

- Unified asset ledger.
- Asset relation graph.
- Operation engine with approval and audit.
- RepoSync as a first-class asset.
- SSH worker based Gitea to GitHub sync.
- AI Agent that acts only through controlled assets and operations.

The staged plan in Notion starts with an asset ledger, then provider sync, then Repo Sync SSH, then project template instantiation, deployment, and AI Agent.

## Implemented Enough For MVP Demo

### Backend

- Auth/login and seeded admin user.
- Project/repository/remote CRUD.
- Remote sync/tag operations and run tables.
- GitHub Actions run sync.
- Argo connection CRUD, app sync/list, and deployment target read model.
- SSH machine CRUD and command runs.
- Worker job lifecycle, logs, stale job recovery, node worker APIs, and worker queue health summaries.
- AI runtime and agent task/plan skeleton.
- Context generation and CLI readers.

### Frontend

- Dashboard and operation list.
- Project/repository detail and read-only project template catalog.
- Repo Sync page with source/target remotes, branches/tags, GitHub Actions list, and run tables.
- Worker node page with queue health, stale-node, stale-running-job, queued-by-tool, and recent-failure visibility.
- AI runtime/task pages.
- Argo connection/app page with sync polling and deployment target table.
- SSH machine page with command execution modal and command run list.

### Security/Guardrails

- No raw shell invocation for Git adapter commands.
- Git refs and SHAs are validated.
- Git/GitHub/SSH output is sanitized before storage.
- GitHub token scopes reject broad or destructive scopes.
- SSH key and known_hosts paths are constrained to configured directories.
- Argo URL SSRF defenses exist at validation and dial layers.
- Argo sensitive config is limited to admin/owner.

## Largest Product Gaps

### 1. Unified Asset Write Model And Graph

Notion's first principle is Asset / AssetRelation / AssetOperationRun. The current code now has `assets`, `asset_relations`, `asset_status_snapshots` tables, derived read APIs, an idempotent `assops-tool db sync-assets` command that backfills canonical asset rows/relations from current domain tables and writes deduplicated asset status snapshots, and an Asset Center with inventory search, type/project/search filters, cross-project graph search, saved graph views, relation tables, selected-asset status history, selected-asset relation graph, upstream/downstream dependency path queries, and manual relation create/delete for operator-curated graph edges. Direct project CRUD, repository, Git remote, RepoSyncAsset lifecycle, Argo connection create, SSH machine create, manual relation create, template-driven project creation, Argo app sync read-model writes, and GitHub Actions sync read-model writes now sync the canonical ledger in the same database transaction as the domain write, while other key CRUD and worker completion paths still trigger a best-effort canonical refresh after successful domain writes. The primary write model still writes most domain tables directly rather than treating canonical assets as the transaction source of truth.

Impact:

- The app can show and search an asset inventory, render filtered cross-project graph subviews, save and reapply graph filters/selections, render relation tables, inspect selected-asset status history, render selected-asset graphs, traverse upstream/downstream dependency paths, preserve manual operator-curated graph edges, backfill canonical assets/relations/status snapshots on demand, transactionally sync the canonical ledger for direct project/repository/remote/RepoSyncAsset lifecycle writes plus Argo connection, SSH machine, manual relation, template-worker project creation, Argo app/deployment/rollback read-model writes, and GitHub Actions pipeline read-model writes, and refresh the canonical ledger after remaining worker operation completion paths, but cannot yet transactionally canonicalize every asset-producing write path.
- AI context now includes canonical asset graph and recent asset health/status snapshots after the ledger has been refreshed by write hooks or `db sync-assets`.

Recommended next slice:

- Extend transactionally maintained canonical assets/relations beyond direct project/repository/remote/RepoSyncAsset lifecycle writes, Argo/SSH create paths, manual relation creation, template-worker project creation, Argo app sync read models, and GitHub Actions pipeline read models to the remaining asset-producing CRUD and manual relation maintenance paths.
- Keep `db sync-assets` available for imports, migrations, and repair jobs until every asset-producing path is fully canonical.
- Add richer graph ranking/grouping after the write model is stable and real asset volume exposes the useful query patterns.

### 2. RepoSyncAsset Lifecycle Is Still Narrow

Notion treats RepoSyncAsset as the key model for Gitea -> GitHub SSH sync. The implementation now has `repo_sync_assets`, list/create/run/detail/update/archive/restore APIs, worker status tracking, a web UI for saving/running/editing/enabling/disabling/archiving/restoring source -> target sync policies, failed sync rerun from the last ref, RepoSync run history filters, per-asset run analytics (total, success/failure/running counts, success rate, recent failures, average duration, list-level risk summaries, 14-day trend, and provider/capacity signals), and a Gitea push webhook path that can enqueue enabled webhook-triggered assets. Archive currently means hidden, disabled soft-archive; restore clears the archive marker and re-enables the asset.

Impact:

- RepoSyncAsset lifecycle basics are now covered, and webhook-triggered sync policies now have replay and one-time secret rotation controls.
- Operators can inspect per-asset sync health, list-level risk summaries, recent trend, active queue pressure, provider status, webhook failures, and GitHub Actions volume without opening every individual run.
- Webhook shared secrets are now encrypted at rest for new/rotated connections, with legacy plaintext fallback for existing rows.
- The UI now shows a copyable fully qualified webhook URL generated from `ASSOPS_GATEWAY_URL`.
- GitHub `workflow_run` webhooks can now update the `github_action_runs` read model for the connected GitHub remote.

Recommended next slice:

- Add richer production capacity planning after real sync volume and provider limits are known.

### 3. Webhook Intake Needs Hardening

Notion's critical flow is Gitea push webhook -> Gateway -> Worker SSH sync -> GitHub Actions. Current implementation supports project-scoped `webhook_connections`, encrypted-at-rest one-time shared secrets for new/rotated connections, HMAC-SHA256 verification, `POST /api/webhooks/gitea/{connection_id}`, `POST /api/webhooks/github/{connection_id}`, Gitea push ref parsing, matching enabled RepoSyncAssets, enqueueing `repo.sync`, GitHub `workflow_run` read-model updates, webhook event audit rows, connection-level delivery health summaries, replay controls, secret rotation, basic rate limiting, delivery-id deduplication, deployment-manifest `ASSOPS_GATEWAY_URL` wiring, and public webhook URL normalization to an HTTP(S) origin.

Recommended next slice:

- Run provider-level callback rehearsals against real Gitea/GitHub webhook settings once a public staging hostname is available.

### 4. Project Template Instantiation Partially Complete

The Notion v0.4 goal is `project.create_from_template`. The code now has a read-only `project_templates` table, seeded built-in template metadata, list/detail/preview APIs, UI catalog/detail/preview modals, asset inventory exposure, provider account management for GitHub/Gitea template provisioning, manual provider token-env rotation, manual provider token/API health checks, sanitized external provider provisioning diagnostics in template run results, and a durable `project.create_from_template` operation with persisted `project_template_runs` step status. The operation creates the project asset, repository metadata, template-defined Git remotes, a RepoSyncAsset when source/target remotes can be resolved by ID or template remote key, and starter file rows exposed as `template_file` assets. When a template remote is explicitly configured with `provider_type: local_bare` and an absolute local `remote_url`, the worker initializes a bare Git repository, commits the starter files, pushes the default branch, and marks the repository/files as provisioned. When a template remote is configured with `provider_type: github` or `provider_type: gitea`, a provider account reference, and an allowed token environment variable, the worker can create the upstream repository before pushing starter files. Template remotes marked `protected` create the external repository but skip starter-file pushes by default unless metadata explicitly sets `allow_protected_branch_push`. Backward-compatible inline provider metadata still works for existing templates. Failed or unprovisioned runs that already created project metadata can now enqueue an operator-triggered repository provisioning retry through the control worker, preserving the original run audit trail while reusing the existing project, repository, remotes, and starter files. This is still a first-pass provider create path, not a full automated provider reconciliation system.

The older `git_remotes.source_provider_id` column remains an unused provider-layer placeholder; current provider account wiring uses `source_account_id`.

Recommended next slice:

- Add automated token rotation and provider-specific protected-branch reconciliation after manual token-env rotation, protected push avoidance, health checks, provider response diagnostics, and operator-triggered retry have covered the basic token/API failure modes.
- Harden starter-file push to external repositories with credential strategy, protected branch handling, and provider-specific idempotency checks.

### 5. Deployment Is Still Read-Model Only

Argo app sync is real, and Argo sync now derives `deployment_targets` by project/environment/cluster/namespace, records latest `deployment_records`, captures `rollback_points` when Argo reports a revision or images, links these objects in the asset relation graph, and shows a read-only deployment posture summary with target count, unhealthy target count, environment count, and available rollback points. This makes "where is this app deployed?", "is anything unhealthy?", and "what could I roll back to?" visible, but deploy execution, k8s cluster credentials, Helm releases, secrets, and actual rollback execution are still not implemented.

Recommended next slice:

- Keep first implementation read-only or mock deploy until approval and environment rules are stronger.
- Add actual deployment execution and rollback actions only after approval and environment rules are stronger.

### 6. Approval Flow Needs Policy Depth

Policy now converts high-risk developer actions into durable `operation_approvals` rows, exposes an Operations UI for approve/reject, and only creates worker-claimable jobs after approval. Covered actions include repository tags, direct remote tag operations, SSH commands, agent execute, and operation cancel. Approval rules are now action-scoped through `operation_approval_rules`, with required approver role snapshots, required approval counts, expiry timestamps, escalation thresholds, and escalation channels copied onto each request; the Operations UI now exposes those rules and lets admin/owner users create or update rule keys, approver roles, approval counts, expiry, notification channels, escalation routing, priority, enabled state, and metadata. Rule changes are recorded in `operation_approval_rule_audits` with actor, action, before snapshot, and after snapshot. Each approval/rejection is recorded in `operation_approval_decisions`; approve actions remain pending until the configured count is reached, while rejection still closes the request immediately. Approvers or already delegated users can delegate a pending approval to an existing user by email; active delegations grant approve/reject/remind permission, can be revoked by the delegator/current approver/operator, and are visible in the approval audit detail. Pending, approved, rejected, expired, reminder, and escalation approval events can notify an external HTTP hook through `ASSOPS_APPROVAL_WEBHOOK_URL`; notification payloads intentionally omit the original request payload. Approval audit summary metrics now show pending, expiring-soon, notification-failed, total, and top action counts with the same project visibility rules as approval lists. Approval reminder candidates now surface pending approvals that need operator attention because notification delivery failed, expiry is near, escalation is due, or the request has waited beyond the first SLA window without enough approvers; approvers can manually send a reminder from the SLA watch table, and the control worker sends throttled scheduled reminders and escalations with `last_reminded_at` / `reminder_count` and `last_escalated_at` / `escalation_count` audit fields. Approval audit details join approval metadata, per-user decisions, delegation history, notification status, resulting operation, worker jobs, operation logs, and domain run records, with list filters for status, action, resource type, requester, keyword, and created time. Users can save, apply, update, and delete their own approval audit filter views. The control worker also sweeps expired pending approvals on its polling loop, so expired notifications no longer depend on list/detail reads.

Recommended next slice:

- Add richer escalation destinations after editable approval rules, escalation routing, delegation/revocation, reminder delivery, SLA watch candidates, and real operator usage clarify required workflows.

### 7. AI Agent Is A Skeleton

Agent task and plan records exist, and plan generation now refreshes a read-only project context snapshot that includes repositories, remotes, recent operations, approvals, deployment targets, rollback points, SSH machines, GitHub Actions runs, canonical asset graph counts/relations, and recent asset status/health snapshots when the ledger has been synced. Approved execution now creates durable `agent_tool_calls` audit rows tied to the operation run, moves them through queued/running/completed or failed states in the control worker, and exposes status summaries in the Agent Task detail UI. This first-version execution path is intentionally simulation-only: it records context review, plan review, AI runtime readiness, and patch preparation intent, but does not invoke Codex CLI, mutate repositories, open PRs, or deploy. Runtime readiness uses the selected project/global AI runtime metadata and intentionally omits runtime config from the audit payload.

Recommended next slice:

- Add real Codex CLI execution behind the existing tool-call audit ledger and approval-gated patch workflow.
- Defer code mutation and deployment execution until approval policy depth exists.

## Operational Gaps

- No Redis-backed queue/lock/pub-sub yet; worker queue is PostgreSQL only, though the UI now exposes queue/node health summaries for operator triage.
- Operation logs now have a lightweight SSE stream for selected runs using one-second database polling. WebSocket fanout, Redis pub/sub, and resumable cross-process event delivery are still deferred.
- Initial CI now covers workflow linting, secret scanning, Go tests/vet, frontend build, Compose config, database backup/restore rehearsal, Helm lint/template, a disposable kind-based Helm install smoke test, Docker image build, and `govulncheck`. A separate scheduled restore rehearsal workflow runs weekly and on demand against disposable GitHub Actions PostgreSQL databases, then uploads the generated JSON report as a short-retention artifact. Dependabot now proposes weekly Go, web npm, GitHub Actions, and Docker image update PRs. A release-candidate workflow now builds Linux amd64 binaries, the web bundle, a packaged Helm chart, checksums, GitHub artifact attestations, and Docker image smoke builds on `v*` tags or manual dispatch; tagged `v*` runs publish gateway, worker, node-worker, and web images to GHCR with version and commit-SHA tags, then attach registry-backed image attestations; `assops-tool release validate-bundle` can verify a downloaded artifact directory plus restore rehearsal report before promotion. A GitHub repository ruleset template plus CODEOWNERS now defines first-version `main` protection with PR review, code owner review, fresh required checks, and deletion/force-push blocks; it still needs to be applied to the remote repository by an administrator. A manual production promotion workflow now performs image attestation verification and Helm render preflight by default, with an opt-in protected-environment Helm rollout path that still needs real environment secrets and cluster review before use.
- Production-shaped Dockerfile and Compose manifest now cover gateway, control worker, node worker, web, PostgreSQL, worker healthchecks, and a one-shot database tool service. The database tool supports timestamped retained backups, non-destructive backup inspection, destructive restore, and a guarded `db rehearse-restore` command that restores into an explicit disposable database, reruns migrations, and can write a private JSON rehearsal report for release notes. A local production-like rehearsal was run against disposable Compose databases on 2026-06-21 UTC, producing `.assops/release-notes/restore-rehearsal-20260621-205245.json` with migrations `001_init.sql`, `002_git_first_version.sql`, and `003_provider_accounts.sql`. A first Helm chart now renders Kubernetes-shaped gateway, worker, node-worker, web, migration job, optional PostgreSQL, Services, PVCs, optional Ingress resources, has a values schema checked by CI, and is smoke-installed into a disposable kind namespace in CI; TLS ingress, storage-class hardening, environment-specific retained production backup rehearsal, and real environment rollout are still deferred.
- Database migrations now have a `schema_migrations` table, checksum validation, a PostgreSQL advisory lock, and `assops-tool db migrate|migrations`; applied migration files are treated as immutable, with follow-up schema changes going into new numbered migrations. Compose init scripts remain only a fresh-volume convenience.
- Explicit demo seed data now covers a project, repository pair, remotes, a disabled RepoSyncAsset, webhook connection/events, sample sync and GitHub Actions history, Argo deployment/rollback read models, SSH/AI examples, a pending multi-approver request, a read-only agent task, canonical asset refresh, and a manual asset relation for local graph demos.

## Suggested Next Implementation Order

1. Wire the scheduled restore rehearsal pattern to retained environment backups once production storage, credentials, and artifact retention are chosen.
2. Add automated provider token rotation and protected-branch-aware credential strategy for external repository provisioning.
3. Add real environment Helm rollout rehearsals after storage, ingress, and secret handling are chosen.

## Definition Of First-Version Ready

For a first version aligned with the Notion scope, the project should be able to demonstrate:

1. Create or import a project as an asset.
2. Attach source and mirror repositories as assets.
3. Define a RepoSyncAsset between them.
4. Trigger sync manually and from a Gitea webhook.
5. See GitHub Actions state tied back to the repository/project.
6. Register SSH machines and run controlled commands with audit output.
7. Sync Argo apps and see their deployment target relationship to the project.
8. View operation history and logs.
9. Enforce policy/approval for high-risk operations.
10. Generate AI-readable project context from the asset graph.

package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

type DemoSeedResult struct {
	ProjectID         string `json:"project_id"`
	RepositoryID      string `json:"repository_id"`
	SourceRemote      string `json:"source_remote_id"`
	TargetRemote      string `json:"target_remote_id"`
	RepoSyncAsset     string `json:"repo_sync_asset_id"`
	WebhookConnection string `json:"webhook_connection_id"`
	SSHMachineID      string `json:"ssh_machine_id"`
	ArgoID            string `json:"argo_connection_id"`
	AIRuntimeID       string `json:"ai_runtime_id"`
	AgentTaskID       string `json:"agent_task_id"`
}

type demoSeedDefaults struct {
	ProjectSlug     string
	RepositoryKey   string
	SourceRemoteKey string
	TargetRemoteKey string
	SSHHost         string
	RepoSyncEnabled bool
}

const demoSeedAdvisoryLockID int64 = 451127631724520

func defaultDemoSeedDefaults() demoSeedDefaults {
	return demoSeedDefaults{
		ProjectSlug:     "assops-demo",
		RepositoryKey:   "demo-service",
		SourceRemoteKey: "gitea",
		TargetRemoteKey: "github",
		SSHHost:         "192.0.2.10",
		RepoSyncEnabled: false,
	}
}

func (s *Store) SeedDemoData(ctx context.Context, cfg Config) (*DemoSeedResult, error) {
	if err := s.SeedAdmin(ctx, cfg); err != nil {
		return nil, err
	}
	admin, err := s.UserByEmail(ctx, cfg.AdminEmail)
	if err != nil {
		return nil, fmt.Errorf("loading seed admin: %w", err)
	}

	defaults := defaultDemoSeedDefaults()
	tx, err := s.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting demo seed transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", demoSeedAdvisoryLockID); err != nil {
		return nil, fmt.Errorf("locking demo seed transaction: %w", err)
	}

	projectID, err := upsertDemoProject(ctx, tx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO project_members(project_id, user_id, role)
		VALUES ($1, $2, 'owner')
		ON CONFLICT(project_id, user_id) DO UPDATE SET role='owner'`,
		projectID, admin.ID); err != nil {
		return nil, fmt.Errorf("upserting demo project membership: %w", err)
	}
	repositoryID, err := upsertDemoRepository(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	sourceRemoteID, err := upsertDemoRemote(ctx, tx, repositoryID, demoRemoteSeed{
		Name:         "Demo Gitea Origin",
		Kind:         "gitea",
		RemoteKey:    defaults.SourceRemoteKey,
		ProviderType: "gitea",
		RemoteURL:    "ssh://git@gitea.example.com/demo/demo-service.git",
		WebURL:       "https://gitea.example.com/demo/demo-service",
		RemoteRole:   "source",
		IsPrimary:    true,
	})
	if err != nil {
		return nil, err
	}
	targetRemoteID, err := upsertDemoRemote(ctx, tx, repositoryID, demoRemoteSeed{
		Name:         "Demo GitHub Mirror",
		Kind:         "github",
		RemoteKey:    defaults.TargetRemoteKey,
		ProviderType: "github",
		RemoteURL:    "git@github.com:example/demo-service.git",
		WebURL:       "https://github.com/example/demo-service",
		RemoteRole:   "mirror",
		IsPrimary:    false,
	})
	if err != nil {
		return nil, err
	}
	repoSyncAssetID, err := upsertDemoRepoSyncAsset(ctx, tx, defaults, projectID, repositoryID, sourceRemoteID, targetRemoteID)
	if err != nil {
		return nil, err
	}
	webhookConnectionID, err := upsertDemoWebhookConnection(ctx, tx, projectID, sourceRemoteID)
	if err != nil {
		return nil, err
	}
	sshMachineID, err := upsertDemoSSHMachine(ctx, tx, defaults, projectID)
	if err != nil {
		return nil, err
	}
	argoID, err := upsertDemoArgoConnection(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	aiRuntimeID, err := upsertDemoAIRuntime(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	agentTaskID, err := upsertDemoAgentTask(ctx, tx, projectID, admin.ID)
	if err != nil {
		return nil, err
	}
	if err := upsertDemoOperationalHistory(ctx, tx, projectID, repositoryID, sourceRemoteID, targetRemoteID, repoSyncAssetID, webhookConnectionID, argoID, admin.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing demo seed transaction: %w", err)
	}
	if _, err := s.SyncCanonicalAssets(ctx); err != nil {
		return nil, fmt.Errorf("syncing canonical assets after demo seed: %w", err)
	}
	if err := s.seedDemoManualAssetRelation(ctx, projectID, repositoryID, repoSyncAssetID); err != nil {
		return nil, err
	}
	return &DemoSeedResult{
		ProjectID:         projectID,
		RepositoryID:      repositoryID,
		SourceRemote:      sourceRemoteID,
		TargetRemote:      targetRemoteID,
		RepoSyncAsset:     repoSyncAssetID,
		WebhookConnection: webhookConnectionID,
		SSHMachineID:      sshMachineID,
		ArgoID:            argoID,
		AIRuntimeID:       aiRuntimeID,
		AgentTaskID:       agentTaskID,
	}, nil
}

func upsertDemoProject(ctx context.Context, tx *sqlx.Tx) (string, error) {
	var id string
	if err := tx.GetContext(ctx, &id, `
		INSERT INTO projects(name, slug, description)
		VALUES ('ASSOPS Demo', 'assops-demo', 'Demo project seeded by assops-tool db seed-demo.')
		ON CONFLICT(slug) DO UPDATE SET
			name=EXCLUDED.name,
			description=EXCLUDED.description,
			updated_at=now()
		RETURNING id`); err != nil {
		return "", fmt.Errorf("upserting demo project: %w", err)
	}
	return id, nil
}

func upsertDemoRepository(ctx context.Context, tx *sqlx.Tx, projectID string) (string, error) {
	var id string
	if err := tx.GetContext(ctx, &id, `
		INSERT INTO project_git_repositories(
			project_id, name, repo_key, display_name, repo_role, status, description, default_branch
		)
		VALUES (
			$1, 'Demo Service', 'demo-service', 'Demo Service', 'service', 'active',
			'Demo repository pair for source-to-mirror workflows.', 'main'
		)
		ON CONFLICT(project_id, repo_key) DO UPDATE SET
			name=EXCLUDED.name,
			display_name=EXCLUDED.display_name,
			repo_role=EXCLUDED.repo_role,
			status=EXCLUDED.status,
			description=EXCLUDED.description,
			default_branch=EXCLUDED.default_branch,
			updated_at=now()
		RETURNING id`, projectID); err != nil {
		return "", fmt.Errorf("upserting demo repository: %w", err)
	}
	return id, nil
}

type demoRemoteSeed struct {
	Name         string
	Kind         string
	RemoteKey    string
	ProviderType string
	RemoteURL    string
	WebURL       string
	RemoteRole   string
	IsPrimary    bool
}

func upsertDemoRemote(ctx context.Context, tx *sqlx.Tx, repositoryID string, seed demoRemoteSeed) (string, error) {
	var id string
	if err := tx.GetContext(ctx, &id, `
		INSERT INTO git_remotes(
			project_git_repository_id, name, kind, remote_key, provider_type, remote_url, web_url,
			remote_role, is_primary, sync_enabled, protected, urls, default_branch, metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true, false, $10, 'main', $11)
		ON CONFLICT(project_git_repository_id, name) DO UPDATE SET
			kind=EXCLUDED.kind,
			remote_key=EXCLUDED.remote_key,
			provider_type=EXCLUDED.provider_type,
			remote_url=EXCLUDED.remote_url,
			web_url=EXCLUDED.web_url,
			remote_role=EXCLUDED.remote_role,
			is_primary=EXCLUDED.is_primary,
			sync_enabled=EXCLUDED.sync_enabled,
			protected=EXCLUDED.protected,
			urls=EXCLUDED.urls,
			default_branch=EXCLUDED.default_branch,
			metadata=EXCLUDED.metadata,
			updated_at=now()
		RETURNING id`,
		repositoryID,
		seed.Name,
		seed.Kind,
		seed.RemoteKey,
		seed.ProviderType,
		seed.RemoteURL,
		seed.WebURL,
		seed.RemoteRole,
		seed.IsPrimary,
		JSONValue{Data: []string{seed.RemoteURL}},
		JSONValue{Data: map[string]any{"source": "demo_seed"}},
	); err != nil {
		return "", fmt.Errorf("upserting demo remote %q: %w", seed.Name, err)
	}
	return id, nil
}

func upsertDemoRepoSyncAsset(ctx context.Context, tx *sqlx.Tx, defaults demoSeedDefaults, projectID, repositoryID, sourceRemoteID, targetRemoteID string) (string, error) {
	var id string
	if err := tx.GetContext(ctx, &id, `
		INSERT INTO repo_sync_assets(
			project_id, project_git_repository_id, name, source_remote_id, target_remote_id,
			trigger_mode, sync_mode, transport, driver, refs, enabled, metadata
		)
		VALUES (
			$1, $2, 'Demo Gitea to GitHub mirror', $3, $4,
			'manual_or_webhook', 'selected_refs', 'ssh', 'projectops_worker_git_ssh', $5, $6, $7
		)
		ON CONFLICT(project_git_repository_id, name) DO UPDATE SET
			source_remote_id=EXCLUDED.source_remote_id,
			target_remote_id=EXCLUDED.target_remote_id,
			trigger_mode=EXCLUDED.trigger_mode,
			sync_mode=EXCLUDED.sync_mode,
			transport=EXCLUDED.transport,
			driver=EXCLUDED.driver,
			refs=EXCLUDED.refs,
			enabled=EXCLUDED.enabled,
			metadata=EXCLUDED.metadata,
			updated_at=now()
		RETURNING id`,
		projectID,
		repositoryID,
		sourceRemoteID,
		targetRemoteID,
		JSONValue{Data: map[string]any{"branches": []string{"main"}, "tags": []string{}}},
		defaults.RepoSyncEnabled,
		JSONValue{Data: map[string]any{"source": "demo_seed", "note": "disabled by default to avoid accidental sync"}},
	); err != nil {
		return "", fmt.Errorf("upserting demo repo sync asset: %w", err)
	}
	return id, nil
}

func upsertDemoWebhookConnection(ctx context.Context, tx *sqlx.Tx, projectID, sourceRemoteID string) (string, error) {
	return upsertByProjectName(ctx, tx, "webhook_connections", projectID, "Demo Gitea push webhook", func() (string, []any) {
		return `
			INSERT INTO webhook_connections(project_id, provider, name, source_remote_id, secret_token, secret_ciphertext, enabled, event_types, last_delivery_status, metadata)
			VALUES ($1, 'gitea', 'Demo Gitea push webhook', $2, 'demo-webhook-secret', '', true, $3, 'verified', $4)
			RETURNING id`,
			[]any{projectID, sourceRemoteID, JSONValue{Data: []string{"push"}}, JSONValue{Data: map[string]any{"source": "demo_seed", "note": "sample secret for local demo only"}}}
	}, func() (string, []any) {
		return `
			UPDATE webhook_connections
			SET provider='gitea',
				source_remote_id=$3,
				enabled=true,
				event_types=$4,
				last_delivery_status='verified',
				last_delivery_error='',
				metadata=$5,
				updated_at=now()
			WHERE project_id=$1 AND name=$2
			RETURNING id`,
			[]any{projectID, "Demo Gitea push webhook", sourceRemoteID, JSONValue{Data: []string{"push"}}, JSONValue{Data: map[string]any{"source": "demo_seed", "note": "sample secret for local demo only"}}}
	})
}

func upsertDemoSSHMachine(ctx context.Context, tx *sqlx.Tx, defaults demoSeedDefaults, projectID string) (string, error) {
	return upsertByProjectName(ctx, tx, "ssh_machines", projectID, "Demo Deploy Host", func() (string, []any) {
		return `
			INSERT INTO ssh_machines(project_id, name, host, port, username, auth_type, metadata)
			VALUES ($1, 'Demo Deploy Host', $2, 22, 'deploy', 'key', $3)
			RETURNING id`,
			[]any{projectID, defaults.SSHHost, JSONValue{Data: map[string]any{"source": "demo_seed", "environment": "demo"}}}
	}, func() (string, []any) {
		return `
			UPDATE ssh_machines
			SET host=$3, port=22, username='deploy', auth_type='key', metadata=$4, updated_at=now()
			WHERE project_id=$1 AND name=$2
			RETURNING id`,
			[]any{projectID, "Demo Deploy Host", defaults.SSHHost, JSONValue{Data: map[string]any{"source": "demo_seed", "environment": "demo"}}}
	})
}

func upsertDemoArgoConnection(ctx context.Context, tx *sqlx.Tx, projectID string) (string, error) {
	return upsertByProjectName(ctx, tx, "argo_connections", projectID, "Demo Argo CD", func() (string, []any) {
		return `
			INSERT INTO argo_connections(project_id, name, server_url, auth_type, config)
			VALUES ($1, 'Demo Argo CD', 'https://argocd.example.com', 'token', $2)
			RETURNING id`,
			[]any{projectID, JSONValue{Data: map[string]any{"source": "demo_seed", "project": "demo"}}}
	}, func() (string, []any) {
		return `
			UPDATE argo_connections
			SET server_url='https://argocd.example.com', auth_type='token', config=$3, updated_at=now()
			WHERE project_id=$1 AND name=$2
			RETURNING id`,
			[]any{projectID, "Demo Argo CD", JSONValue{Data: map[string]any{"source": "demo_seed", "project": "demo"}}}
	})
}

func upsertDemoAIRuntime(ctx context.Context, tx *sqlx.Tx, projectID string) (string, error) {
	return upsertByProjectName(ctx, tx, "ai_runtimes", projectID, "Demo Codex Runtime", func() (string, []any) {
		return `
			INSERT INTO ai_runtimes(project_id, name, runtime_type, codex_binary, model, config, status)
			VALUES ($1, 'Demo Codex Runtime', 'codex-cli', 'codex', '', $2, 'unknown')
			RETURNING id`,
			[]any{projectID, JSONValue{Data: map[string]any{"source": "demo_seed", "mode": "read_only_first"}}}
	}, func() (string, []any) {
		return `
			UPDATE ai_runtimes
			SET runtime_type='codex-cli', codex_binary='codex', model='', config=$3, status='unknown', updated_at=now()
			WHERE project_id=$1 AND name=$2
			RETURNING id`,
			[]any{projectID, "Demo Codex Runtime", JSONValue{Data: map[string]any{"source": "demo_seed", "mode": "read_only_first"}}}
	})
}

func upsertDemoAgentTask(ctx context.Context, tx *sqlx.Tx, projectID, adminID string) (string, error) {
	title := "Review demo operations"
	var id string
	err := tx.GetContext(ctx, &id, `
		SELECT id
		FROM agent_tasks
		WHERE project_id=$1 AND title=$2
		ORDER BY created_at
		LIMIT 1`, projectID, title)
	if err == nil {
		if err := tx.GetContext(ctx, &id, `
			UPDATE agent_tasks
			SET prompt=$3, created_by=$4, status='draft', updated_at=now()
			WHERE id=$1 AND project_id=$2
			RETURNING id`, id, projectID, demoAgentPrompt(), adminID); err != nil {
			return "", fmt.Errorf("updating demo agent task: %w", err)
		}
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("checking demo agent task: %w", err)
	}
	if err := tx.GetContext(ctx, &id, `
		INSERT INTO agent_tasks(project_id, title, prompt, created_by, status)
		VALUES ($1, $2, $3, $4, 'draft')
		RETURNING id`, projectID, title, demoAgentPrompt(), adminID); err != nil {
		return "", fmt.Errorf("inserting demo agent task: %w", err)
	}
	return id, nil
}

func upsertDemoOperationalHistory(ctx context.Context, tx *sqlx.Tx, projectID, repositoryID, sourceRemoteID, targetRemoteID, repoSyncAssetID, webhookConnectionID, argoID, adminID string) error {
	completedOpID, err := upsertDemoOperationRun(ctx, tx, projectID, targetRemoteID, "repo.sync", "Demo completed mirror sync", "completed", "", map[string]any{"ref": "refs/heads/main"}, map[string]any{"after_sha": "0123456789abcdef0123456789abcdef01234567"}, -72)
	if err != nil {
		return err
	}
	if err := upsertDemoRepoSyncRun(ctx, tx, completedOpID, projectID, repositoryID, sourceRemoteID, targetRemoteID, repoSyncAssetID, adminID, "refs/heads/main", "completed", "", -72); err != nil {
		return err
	}
	failedOpID, err := upsertDemoOperationRun(ctx, tx, projectID, targetRemoteID, "repo.sync", "Demo failed mirror sync", "failed", "target remote rejected non-fast-forward update", map[string]any{"ref": "refs/heads/release"}, map[string]any{"retryable": true}, -30)
	if err != nil {
		return err
	}
	if err := upsertDemoRepoSyncRun(ctx, tx, failedOpID, projectID, repositoryID, sourceRemoteID, targetRemoteID, repoSyncAssetID, adminID, "refs/heads/release", "failed", "target remote rejected non-fast-forward update", -30); err != nil {
		return err
	}
	if err := upsertDemoGitHubActionRun(ctx, tx, completedOpID, targetRemoteID); err != nil {
		return err
	}
	if err := upsertDemoWebhookEvents(ctx, tx, webhookConnectionID, projectID, repoSyncAssetID, completedOpID); err != nil {
		return err
	}
	if _, err := upsertDemoOperationRun(ctx, tx, projectID, "", "argo.apps.sync", "Demo Argo app sync", "completed", "", map[string]any{"argo_connection_id": argoID}, map[string]any{"synced_apps": 1, "deployment_targets": 1}, -69); err != nil {
		return err
	}
	if err := upsertDemoDeploymentReadModel(ctx, tx, projectID, argoID); err != nil {
		return err
	}
	if err := upsertDemoApproval(ctx, tx, projectID, repoSyncAssetID, adminID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE repo_sync_assets
		SET last_sync_status='failed',
			last_sync_run_id=$2,
			last_synced_at=now() - interval '30 hours',
			updated_at=now()
		WHERE id=$1`, repoSyncAssetID, failedOpID); err != nil {
		return fmt.Errorf("updating demo repo sync asset status: %w", err)
	}
	return nil
}

func upsertDemoOperationRun(ctx context.Context, tx *sqlx.Tx, projectID, remoteID, operationType, title, status, errorMessage string, input, result map[string]any, hoursAgo int) (string, error) {
	var id string
	err := tx.GetContext(ctx, &id, `
		SELECT id
		FROM operation_runs
		WHERE project_id=$1 AND operation_type=$2 AND title=$3
		ORDER BY created_at
		LIMIT 1`, projectID, operationType, title)
	if err == nil {
		if err := tx.GetContext(ctx, &id, `
			UPDATE operation_runs
			SET git_remote_id=NULLIF($4,'')::uuid,
				status=$5,
				input=$6,
				result=$7,
				error=$8,
				started_at=now() + ($9::int * interval '1 hour'),
				finished_at=now() + (($9::int * interval '1 hour') + interval '4 minutes'),
				updated_at=now()
			WHERE id=$1 AND project_id=$2 AND operation_type=$3
			RETURNING id`,
			id,
			projectID,
			operationType,
			remoteID,
			status,
			JSONValue{Data: input},
			JSONValue{Data: result},
			errorMessage,
			hoursAgo,
		); err != nil {
			return "", fmt.Errorf("updating demo operation %q: %w", title, err)
		}
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("checking demo operation %q: %w", title, err)
	}
	if err := tx.GetContext(ctx, &id, `
		INSERT INTO operation_runs(project_id, git_remote_id, operation_type, status, title, input, result, error, started_at, finished_at, created_at, updated_at)
		VALUES ($1, NULLIF($2,'')::uuid, $3, $4, $5, $6, $7, $8, now() + ($9::int * interval '1 hour'), now() + (($9::int * interval '1 hour') + interval '4 minutes'), now() + ($9::int * interval '1 hour'), now())
		RETURNING id`,
		projectID,
		remoteID,
		operationType,
		status,
		title,
		JSONValue{Data: input},
		JSONValue{Data: result},
		errorMessage,
		hoursAgo,
	); err != nil {
		return "", fmt.Errorf("inserting demo operation %q: %w", title, err)
	}
	return id, nil
}

func upsertDemoRepoSyncRun(ctx context.Context, tx *sqlx.Tx, operationRunID, projectID, repositoryID, sourceRemoteID, targetRemoteID, repoSyncAssetID, adminID, ref, status, errorMessage string, hoursAgo int) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM repo_sync_runs WHERE operation_run_id=$1", operationRunID); err != nil {
		return fmt.Errorf("deleting existing demo repo sync run: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO repo_sync_runs(
			operation_run_id, git_remote_id, project_id, project_git_repository_id, repo_sync_asset_id,
			source_remote_id, target_remote_id, ref, before_sha, after_sha, actor_user_id,
			status, stdout, stderr, error_message, started_at, finished_at, created_at
		)
		VALUES (
			$1, $5, $2, $3, $6,
			$4, $5, $8, '0000000000000000000000000000000000000000', '0123456789abcdef0123456789abcdef01234567', $7,
			$9, $10, '', $11, now() + ($12::int * interval '1 hour'), now() + (($12::int * interval '1 hour') + interval '4 minutes'), now() + ($12::int * interval '1 hour')
		)`,
		operationRunID,
		projectID,
		repositoryID,
		sourceRemoteID,
		targetRemoteID,
		repoSyncAssetID,
		adminID,
		ref,
		status,
		demoRepoSyncStdout(status),
		errorMessage,
		hoursAgo,
	); err != nil {
		return fmt.Errorf("inserting demo repo sync run: %w", err)
	}
	return nil
}

func upsertDemoGitHubActionRun(ctx context.Context, tx *sqlx.Tx, operationRunID, targetRemoteID string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO github_action_runs(
			operation_run_id, git_remote_id, external_run_id, run_id, workflow_name, branch, commit_sha,
			status, conclusion, html_url, metadata, started_at, updated_at, synced_at, created_at
		)
		VALUES ($1, $2, '100200300', '100200300', 'CI', 'main', '0123456789abcdef0123456789abcdef01234567', 'completed', 'success', 'https://github.com/example/demo-service/actions/runs/100200300', $3, now() - interval '70 hours', now() - interval '69 hours', now() - interval '69 hours', now() - interval '70 hours')
		ON CONFLICT (git_remote_id, external_run_id) WHERE external_run_id <> '' DO UPDATE SET
			operation_run_id=EXCLUDED.operation_run_id,
			run_id=EXCLUDED.run_id,
			workflow_name=EXCLUDED.workflow_name,
			branch=EXCLUDED.branch,
			commit_sha=EXCLUDED.commit_sha,
			status=EXCLUDED.status,
			conclusion=EXCLUDED.conclusion,
			html_url=EXCLUDED.html_url,
			metadata=EXCLUDED.metadata,
			started_at=EXCLUDED.started_at,
			updated_at=EXCLUDED.updated_at,
			synced_at=EXCLUDED.synced_at`,
		operationRunID,
		targetRemoteID,
		JSONValue{Data: map[string]any{"source": "demo_seed"}},
	)
	if err != nil {
		return fmt.Errorf("upserting demo GitHub Action run: %w", err)
	}
	return nil
}

func upsertDemoWebhookEvents(ctx context.Context, tx *sqlx.Tx, webhookConnectionID, projectID, repoSyncAssetID, operationRunID string) error {
	for _, event := range []struct {
		deliveryID string
		status     string
		error      string
		hoursAgo   int
	}{
		{"demo-delivery-main", "processed", "", -71},
		{"demo-delivery-ignored", "ignored", "push ref refs/heads/docs did not match enabled RepoSyncAsset refs", -18},
	} {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO webhook_events(webhook_connection_id, project_id, provider, event_type, delivery_id, signature_valid, matched_repo_sync_asset_id, operation_run_id, status, error_message, payload, result, received_at, processed_at)
			VALUES ($1, $2, 'gitea', 'push', $3, true, $4, $5, $6, $7, $8, $9, now() + ($10::int * interval '1 hour'), now() + (($10::int * interval '1 hour') + interval '1 minute'))
			ON CONFLICT (webhook_connection_id, delivery_id) WHERE delivery_id <> '' AND signature_valid DO UPDATE SET
				matched_repo_sync_asset_id=EXCLUDED.matched_repo_sync_asset_id,
				operation_run_id=EXCLUDED.operation_run_id,
				status=EXCLUDED.status,
				error_message=EXCLUDED.error_message,
				payload=EXCLUDED.payload,
				result=EXCLUDED.result,
				received_at=EXCLUDED.received_at,
				processed_at=EXCLUDED.processed_at`,
			webhookConnectionID,
			projectID,
			event.deliveryID,
			repoSyncAssetID,
			operationRunID,
			event.status,
			event.error,
			JSONValue{Data: map[string]any{"ref": "refs/heads/main", "repository": map[string]any{"full_name": "demo/demo-service"}}},
			JSONValue{Data: map[string]any{"source": "demo_seed", "status": event.status}},
			event.hoursAgo,
		)
		if err != nil {
			return fmt.Errorf("upserting demo webhook event %q: %w", event.deliveryID, err)
		}
	}
	return nil
}

func upsertDemoDeploymentReadModel(ctx context.Context, tx *sqlx.Tx, projectID, argoID string) error {
	var targetID string
	if err := tx.GetContext(ctx, &targetID, `
		INSERT INTO deployment_targets(project_id, name, environment, cluster_name, namespace, source, argo_connection_id, status, metadata)
		VALUES ($1, 'demo-cluster/demo', 'staging', 'demo-cluster', 'demo', 'argocd', $2, 'Synced', $3)
		ON CONFLICT(project_id, environment, cluster_name, namespace) DO UPDATE SET
			name=EXCLUDED.name,
			source=EXCLUDED.source,
			argo_connection_id=EXCLUDED.argo_connection_id,
			status=EXCLUDED.status,
			metadata=EXCLUDED.metadata,
			updated_at=now()
		RETURNING id`, projectID, argoID, JSONValue{Data: map[string]any{"source": "demo_seed"}}); err != nil {
		return fmt.Errorf("upserting demo deployment target: %w", err)
	}
	var appID string
	if err := tx.GetContext(ctx, &appID, `
		INSERT INTO argo_apps(project_id, argo_connection_id, deployment_target_id, name, namespace, status, metadata)
		VALUES ($1, $2, $3, 'demo-service', 'demo', 'Synced', $4)
		ON CONFLICT(argo_connection_id, name) DO UPDATE SET
			deployment_target_id=EXCLUDED.deployment_target_id,
			namespace=EXCLUDED.namespace,
			status=EXCLUDED.status,
			metadata=EXCLUDED.metadata,
			updated_at=now()
		RETURNING id`, projectID, argoID, targetID, JSONValue{Data: map[string]any{"source": "demo_seed"}}); err != nil {
		return fmt.Errorf("upserting demo Argo app: %w", err)
	}
	var recordID string
	if err := tx.GetContext(ctx, &recordID, `
		INSERT INTO deployment_records(project_id, deployment_target_id, argo_connection_id, argo_app_id, name, environment, namespace, cluster_name, source, status, revision, image_refs, metadata, observed_at)
		VALUES ($1, $2, $3, $4, 'demo-service', 'staging', 'demo', 'demo-cluster', 'argocd', 'Synced', '0123456789abcdef0123456789abcdef01234567', $5, $6, now() - interval '69 hours')
		ON CONFLICT(project_id, source, name, environment, namespace, cluster_name) DO UPDATE SET
			deployment_target_id=EXCLUDED.deployment_target_id,
			argo_connection_id=EXCLUDED.argo_connection_id,
			argo_app_id=EXCLUDED.argo_app_id,
			status=EXCLUDED.status,
			revision=EXCLUDED.revision,
			image_refs=EXCLUDED.image_refs,
			metadata=EXCLUDED.metadata,
			observed_at=EXCLUDED.observed_at,
			updated_at=now()
		RETURNING id`, projectID, targetID, argoID, appID, JSONValue{Data: []string{"ghcr.io/example/demo-service:demo"}}, JSONValue{Data: map[string]any{"source": "demo_seed"}}); err != nil {
		return fmt.Errorf("upserting demo deployment record: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO rollback_points(project_id, deployment_record_id, deployment_target_id, name, environment, revision, image_refs, source, status, metadata, captured_at)
		VALUES ($1, $2, $3, 'demo-service', 'staging', '0123456789abcdef0123456789abcdef01234567', $4, 'argocd', 'available', $5, now() - interval '69 hours')
		ON CONFLICT(project_id, source, name, environment, revision) DO UPDATE SET
			deployment_record_id=EXCLUDED.deployment_record_id,
			deployment_target_id=EXCLUDED.deployment_target_id,
			image_refs=EXCLUDED.image_refs,
			status=EXCLUDED.status,
			metadata=EXCLUDED.metadata,
			captured_at=EXCLUDED.captured_at`, projectID, recordID, targetID, JSONValue{Data: []string{"ghcr.io/example/demo-service:demo"}}, JSONValue{Data: map[string]any{"source": "demo_seed"}}); err != nil {
		return fmt.Errorf("upserting demo rollback point: %w", err)
	}
	return nil
}

func upsertDemoApproval(ctx context.Context, tx *sqlx.Tx, projectID, repoSyncAssetID, adminID string) error {
	var approvalID string
	err := tx.GetContext(ctx, &approvalID, `
		SELECT id
		FROM operation_approvals
		WHERE resource_type='repo_sync_asset' AND resource_id=$1 AND action='repo.sync'
		ORDER BY created_at
		LIMIT 1`, repoSyncAssetID)
	if err == nil {
		_, err = tx.ExecContext(ctx, `
			UPDATE operation_approvals
			SET project_id=$2,
				title='Demo pending mirror sync approval',
				request_payload=$3,
				status='pending',
				required_approver_roles=ARRAY['admin','owner']::TEXT[],
				required_approval_count=2,
				notification_channels=ARRAY['ui']::TEXT[],
				notification_status='pending',
				requested_by=$4,
				decided_by=$4,
				decision_reason='seeded first approval; waiting for another approver',
				decided_at=NULL,
				expires_at=now() + interval '24 hours',
				expired_at=NULL,
				updated_at=now()
			WHERE id=$1`, approvalID, projectID, JSONValue{Data: map[string]any{"source": "demo_seed", "repo_sync_asset_id": repoSyncAssetID}}, adminID)
		if err != nil {
			return fmt.Errorf("updating demo approval: %w", err)
		}
	} else {
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("checking demo approval: %w", err)
		}
		if err := tx.GetContext(ctx, &approvalID, `
			INSERT INTO operation_approvals(project_id, resource_type, resource_id, action, title, request_payload, status, required_approver_roles, required_approval_count, notification_channels, notification_status, requested_by, decided_by, decision_reason, expires_at)
			VALUES ($1, 'repo_sync_asset', $2, 'repo.sync', 'Demo pending mirror sync approval', $3, 'pending', ARRAY['admin','owner']::TEXT[], 2, ARRAY['ui']::TEXT[], 'pending', $4, $4, 'seeded first approval; waiting for another approver', now() + interval '24 hours')
			RETURNING id`, projectID, repoSyncAssetID, JSONValue{Data: map[string]any{"source": "demo_seed", "repo_sync_asset_id": repoSyncAssetID}}, adminID); err != nil {
			return fmt.Errorf("inserting demo approval: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO operation_approval_decisions(operation_approval_id, user_id, decision, reason)
		VALUES ($1, $2, 'approved', 'Seeded first approval for demo multi-approver progress')
		ON CONFLICT(operation_approval_id, user_id) DO UPDATE SET
			decision=EXCLUDED.decision,
			reason=EXCLUDED.reason,
			decided_at=now(),
			updated_at=now()`, approvalID, adminID); err != nil {
		return fmt.Errorf("upserting demo approval decision: %w", err)
	}
	return nil
}

func (s *Store) seedDemoManualAssetRelation(ctx context.Context, projectID, repositoryID, repoSyncAssetID string) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO asset_relations(project_id, from_asset_id, to_asset_id, relation_type, metadata)
		SELECT $1, from_asset.id, to_asset.id, 'observes', $4
		FROM assets from_asset
		JOIN assets to_asset ON to_asset.asset_type='repo_sync' AND to_asset.source_table='repo_sync_assets' AND to_asset.source_id=$3::uuid
		WHERE from_asset.asset_type='repository' AND from_asset.source_table='project_git_repositories' AND from_asset.source_id=$2::uuid
		ON CONFLICT(from_asset_id, to_asset_id, relation_type) DO UPDATE SET
			metadata=asset_relations.metadata || EXCLUDED.metadata`,
		projectID,
		repositoryID,
		repoSyncAssetID,
		JSONValue{Data: map[string]any{"source": "manual", "demo_seed": true, "note": "operator-curated demo relation"}},
	)
	if err != nil {
		return fmt.Errorf("upserting demo manual asset relation: %w", err)
	}
	return nil
}

func demoRepoSyncStdout(status string) string {
	if status == "completed" {
		return "Fetched refs/heads/main from Demo Gitea Origin and pushed to Demo GitHub Mirror."
	}
	return "Fetched refs/heads/release from Demo Gitea Origin; push was rejected by target policy."
}

func upsertByProjectName(
	ctx context.Context,
	tx *sqlx.Tx,
	table string,
	projectID string,
	name string,
	insertSQL func() (string, []any),
	updateSQL func() (string, []any),
) (string, error) {
	var existing string
	err := tx.GetContext(ctx, &existing, fmt.Sprintf("SELECT id FROM %s WHERE project_id=$1 AND name=$2 ORDER BY created_at LIMIT 1", table), projectID, name)
	if err == nil {
		query, args := updateSQL()
		var id string
		if err := tx.GetContext(ctx, &id, query, args...); err != nil {
			return "", fmt.Errorf("updating demo %s %q: %w", table, name, err)
		}
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("checking demo %s %q: %w", table, name, err)
	}
	query, args := insertSQL()
	var id string
	if err := tx.GetContext(ctx, &id, query, args...); err != nil {
		return "", fmt.Errorf("inserting demo %s %q: %w", table, name, err)
	}
	return id, nil
}

func demoAgentPrompt() string {
	return "Summarize project assets, repository sync state, deployment posture, SSH access, and approvals. Do not mutate anything."
}

package app

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
)

type DemoReadinessDataOptions struct {
	ProjectName    string
	ProjectSlug    string
	RepositoryName string
	RepositoryKey  string
	ActorUserID    string
	DryRun         bool
	RecordSnapshot bool
}

type demoReadinessDataSyncFunc func(context.Context, sqlx.QueryerContext) (AssetSyncResult, error)

type demoReadinessRemoteSpec struct {
	Name         string
	Kind         string
	RemoteKey    string
	ProviderType string
	RemoteRole   string
	IsPrimary    bool
}

const demoReadinessDataAdvisoryLockID int64 = 451127631724521

func EnsureDemoReadinessData(ctx context.Context, store *Store, opts DemoReadinessDataOptions) (map[string]any, error) {
	return ensureDemoReadinessDataWithSync(ctx, store, opts, SyncCanonicalAssetsWith)
}

func ensureDemoReadinessDataWithSync(ctx context.Context, store *Store, opts DemoReadinessDataOptions, syncFn demoReadinessDataSyncFunc) (map[string]any, error) {
	opts = normalizeDemoReadinessDataOptions(opts)
	if store == nil || store.DB == nil {
		return nil, fmt.Errorf("store database is required")
	}
	result := newDemoReadinessDataResult(opts)
	existing, err := observeDemoReadinessData(ctx, store.DB, opts)
	if err != nil {
		return nil, err
	}
	for key, value := range existing {
		result[key] = value
	}
	if opts.DryRun {
		result["recording_enabled"] = false
		result["recording_state"] = "dry_run"
		return result, nil
	}

	tx, err := store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting demo readiness data transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", demoReadinessDataAdvisoryLockID); err != nil {
		return nil, fmt.Errorf("locking demo readiness data transaction: %w", err)
	}

	projectID, projectCreated, err := ensureDemoReadinessProject(ctx, tx, opts)
	if err != nil {
		return nil, err
	}
	if opts.ActorUserID != "" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO project_members(project_id, user_id, role)
			VALUES ($1, $2, 'owner')
			ON CONFLICT(project_id, user_id) DO NOTHING`,
			projectID, opts.ActorUserID); err != nil {
			return nil, fmt.Errorf("upserting demo readiness project membership: %w", err)
		}
	}
	repositoryID, repositoryCreated, err := ensureDemoReadinessRepository(ctx, tx, projectID, opts)
	if err != nil {
		return nil, err
	}
	remoteIDs := make([]string, 0, 2)
	remoteCreatedCount := 0
	for _, spec := range demoReadinessRemoteSpecs() {
		remoteID, created, err := ensureDemoReadinessRemote(ctx, tx, repositoryID, spec)
		if err != nil {
			return nil, err
		}
		remoteIDs = append(remoteIDs, remoteID)
		if created {
			remoteCreatedCount++
		}
	}
	syncResult, err := syncFn(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("syncing canonical assets for demo readiness data: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing demo readiness data: %w", err)
	}

	result["project_id"] = projectID
	result["repository_id"] = repositoryID
	result["remote_ids"] = remoteIDs
	result["project_created"] = projectCreated
	result["repository_created"] = repositoryCreated
	result["git_remote_created"] = remoteCreatedCount > 0
	result["git_remote_created_count"] = remoteCreatedCount
	result["git_remote_count"] = len(remoteIDs)
	result["asset_graph_written"] = true
	result["canonical_assets_synced"] = true
	result["synced_assets"] = syncResult.SyncedAssets
	result["inserted_relations"] = syncResult.InsertedRelations
	result["pruned_relations"] = syncResult.PrunedRelations
	result["inserted_status_snapshots"] = syncResult.InsertedStatusSnapshots
	result["recording_state"] = "recorded"
	result["recording_enabled"] = true

	if opts.RecordSnapshot {
		snapshot, err := RecordDemoReadinessSnapshot(ctx, store, DemoReadinessSnapshotOptions{ProjectID: projectID})
		if err != nil {
			return nil, fmt.Errorf("recording demo readiness snapshot after data execution: %w", err)
		}
		result["readiness_snapshot"] = snapshot
		result["readiness_snapshot_written"] = snapshot["readiness_snapshot_written"] == true
		result["asset_graph_snapshot_written"] = snapshot["asset_graph_snapshot_written"] == true
		result["snapshot_recording_state"] = snapshot["recording_state"]
	}
	return result, nil
}

func normalizeDemoReadinessDataOptions(opts DemoReadinessDataOptions) DemoReadinessDataOptions {
	if opts.ProjectName == "" {
		opts.ProjectName = "ASSOPS Demo"
	}
	if opts.ProjectSlug == "" {
		opts.ProjectSlug = "assops-demo"
	}
	if opts.RepositoryName == "" {
		opts.RepositoryName = "ASSOPS Demo Service"
	}
	if opts.RepositoryKey == "" {
		opts.RepositoryKey = "demo-service"
	}
	opts.ProjectSlug = slugify(opts.ProjectSlug)
	opts.RepositoryKey = slugify(opts.RepositoryKey)
	return opts
}

func newDemoReadinessDataResult(opts DemoReadinessDataOptions) map[string]any {
	return map[string]any{
		"mode":                          "first_version_demo_readiness_data_execution",
		"dry_run":                       opts.DryRun,
		"project_slug":                  opts.ProjectSlug,
		"repository_key":                opts.RepositoryKey,
		"record_snapshot":               opts.RecordSnapshot,
		"external_call_made":            false,
		"provider_api_called":           false,
		"git_command_executed":          false,
		"git_remote_url_written":        false,
		"remote_url_included":           false,
		"credential_included":           false,
		"git_output_included":           false,
		"operation_log_written":         false,
		"raw_provider_response_written": false,
		"asset_graph_written":           false,
		"canonical_assets_synced":       false,
		"readiness_snapshot_written":    false,
		"asset_graph_snapshot_written":  false,
		"suppressed_fields": []string{
			"remote_url",
			"web_url",
			"git_credentials",
			"provider_token",
			"authorization_header",
			"git_output",
			"provider_response_body",
			"provider_response_headers",
		},
	}
}

func observeDemoReadinessData(ctx context.Context, db sqlx.QueryerContext, opts DemoReadinessDataOptions) (map[string]any, error) {
	result := map[string]any{
		"project_observed":    false,
		"repository_observed": false,
		"git_remote_count":    0,
	}
	var projectID string
	if err := sqlx.GetContext(ctx, db, &projectID, `SELECT id FROM projects WHERE slug=$1`, opts.ProjectSlug); err != nil {
		if err == sql.ErrNoRows {
			return result, nil
		}
		return nil, fmt.Errorf("observing demo readiness project: %w", err)
	}
	result["project_observed"] = true
	result["project_id"] = projectID

	var repositoryID string
	if err := sqlx.GetContext(ctx, db, &repositoryID, `
		SELECT id FROM project_git_repositories
		WHERE project_id=$1 AND repo_key=$2`, projectID, opts.RepositoryKey); err != nil {
		if err == sql.ErrNoRows {
			return result, nil
		}
		return nil, fmt.Errorf("observing demo readiness repository: %w", err)
	}
	result["repository_observed"] = true
	result["repository_id"] = repositoryID

	var count int
	if err := sqlx.GetContext(ctx, db, &count, `
		SELECT COUNT(*) FROM git_remotes
		WHERE project_git_repository_id=$1 AND remote_key IN ('gitea', 'github')`, repositoryID); err != nil {
		return nil, fmt.Errorf("observing demo readiness remotes: %w", err)
	}
	result["git_remote_count"] = count
	return result, nil
}

func ensureDemoReadinessProject(ctx context.Context, tx *sqlx.Tx, opts DemoReadinessDataOptions) (string, bool, error) {
	var id string
	if err := tx.GetContext(ctx, &id, `SELECT id FROM projects WHERE slug=$1`, opts.ProjectSlug); err != nil {
		if err != sql.ErrNoRows {
			return "", false, fmt.Errorf("loading demo readiness project: %w", err)
		}
		if err := tx.GetContext(ctx, &id, `
			INSERT INTO projects(name, slug, description)
			VALUES ($1, $2, $3)
			RETURNING id`,
			opts.ProjectName,
			opts.ProjectSlug,
			"First-version demo project created by the readiness data execution path."); err != nil {
			return "", false, fmt.Errorf("creating demo readiness project: %w", err)
		}
		return id, true, nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE projects
		SET name=$2,
			description=CASE WHEN COALESCE(description, '') = '' THEN $3 ELSE description END,
			updated_at=now()
		WHERE id=$1`,
		id,
		opts.ProjectName,
		"First-version demo project created by the readiness data execution path."); err != nil {
		return "", false, fmt.Errorf("updating demo readiness project: %w", err)
	}
	return id, false, nil
}

func ensureDemoReadinessRepository(ctx context.Context, tx *sqlx.Tx, projectID string, opts DemoReadinessDataOptions) (string, bool, error) {
	var id string
	if err := tx.GetContext(ctx, &id, `
		SELECT id FROM project_git_repositories
		WHERE project_id=$1 AND repo_key=$2`, projectID, opts.RepositoryKey); err != nil {
		if err != sql.ErrNoRows {
			return "", false, fmt.Errorf("loading demo readiness repository: %w", err)
		}
		if err := tx.GetContext(ctx, &id, `
			INSERT INTO project_git_repositories(
				project_id, name, repo_key, display_name, repo_role, status, description, default_branch
			)
			VALUES ($1, $2, $3, $2, 'service', 'active', $4, 'main')
			RETURNING id`,
			projectID,
			opts.RepositoryName,
			opts.RepositoryKey,
			"Local first-version demo repository shell for readiness evidence."); err != nil {
			return "", false, fmt.Errorf("creating demo readiness repository: %w", err)
		}
		return id, true, nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE project_git_repositories
		SET name=$2,
			display_name=$2,
			repo_role='service',
			status='active',
			default_branch='main',
			updated_at=now()
		WHERE id=$1`,
		id,
		opts.RepositoryName); err != nil {
		return "", false, fmt.Errorf("updating demo readiness repository: %w", err)
	}
	return id, false, nil
}

func demoReadinessRemoteSpecs() []demoReadinessRemoteSpec {
	return []demoReadinessRemoteSpec{
		{
			Name:         "Demo Gitea Source",
			Kind:         "gitea",
			RemoteKey:    "gitea",
			ProviderType: "gitea",
			RemoteRole:   "source",
			IsPrimary:    true,
		},
		{
			Name:         "Demo GitHub Mirror",
			Kind:         "github",
			RemoteKey:    "github",
			ProviderType: "github",
			RemoteRole:   "mirror",
			IsPrimary:    false,
		},
	}
}

func ensureDemoReadinessRemote(ctx context.Context, tx *sqlx.Tx, repositoryID string, spec demoReadinessRemoteSpec) (string, bool, error) {
	var id string
	if err := tx.GetContext(ctx, &id, `
		SELECT id FROM git_remotes
		WHERE project_git_repository_id=$1 AND remote_key=$2
		ORDER BY created_at ASC
		LIMIT 1`, repositoryID, spec.RemoteKey); err != nil {
		if err != sql.ErrNoRows {
			return "", false, fmt.Errorf("loading demo readiness remote %q: %w", spec.RemoteKey, err)
		}
		if err := tx.GetContext(ctx, &id, `
			INSERT INTO git_remotes(
				project_git_repository_id, name, kind, remote_key, provider_type, remote_url, web_url,
				remote_role, is_primary, sync_enabled, protected, latest_sha, last_sync_status,
				urls, default_branch, metadata
			)
			VALUES ($1, $2, $3, $4, $5, '', '', $6, $7, true, false, '', 'never', $8, 'main', $9)
			RETURNING id`,
			repositoryID,
			spec.Name,
			spec.Kind,
			spec.RemoteKey,
			spec.ProviderType,
			spec.RemoteRole,
			spec.IsPrimary,
			JSONValue{Data: []string{}},
			JSONValue{Data: map[string]any{"source": "demo_readiness_data", "url_intentionally_omitted": true}},
		); err != nil {
			return "", false, fmt.Errorf("creating demo readiness remote %q: %w", spec.RemoteKey, err)
		}
		return id, true, nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE git_remotes
		SET name=$2,
			kind=$3,
			provider_type=$4,
			remote_url='',
			web_url='',
			remote_role=$5,
			is_primary=$6,
			sync_enabled=true,
			protected=false,
			latest_sha='',
			last_sync_status='never',
			urls=$7,
			default_branch='main',
			metadata=$8,
			updated_at=now()
		WHERE id=$1`,
		id,
		spec.Name,
		spec.Kind,
		spec.ProviderType,
		spec.RemoteRole,
		spec.IsPrimary,
		JSONValue{Data: []string{}},
		JSONValue{Data: map[string]any{"source": "demo_readiness_data", "url_intentionally_omitted": true}},
	); err != nil {
		return "", false, fmt.Errorf("updating demo readiness remote %q: %w", spec.RemoteKey, err)
	}
	return id, false, nil
}

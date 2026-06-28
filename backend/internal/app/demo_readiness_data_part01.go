package app

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
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

type demoReadinessDataSyncFunc func(context.Context, *gorm.DB) (AssetSyncResult, error)

type demoReadinessRemoteSpec struct {
	Name         string
	Kind         string
	RemoteKey    string
	ProviderType string
	RemoteRole   string
	IsPrimary    bool
}

func EnsureDemoReadinessData(ctx context.Context, store *Store, opts DemoReadinessDataOptions) (map[string]any, error) {
	return ensureDemoReadinessDataWithSync(ctx, store, opts, syncCanonicalAssetsGorm)
}

func ensureDemoReadinessDataWithSync(ctx context.Context, store *Store, opts DemoReadinessDataOptions, syncFn demoReadinessDataSyncFunc) (map[string]any, error) {
	opts = normalizeDemoReadinessDataOptions(opts)
	result := newDemoReadinessDataResult(opts)
	if opts.DryRun {
		result["recording_enabled"] = false
		result["recording_state"] = "dry_run"
		return result, nil
	}
	if store == nil || store.Gorm == nil {
		return nil, fmt.Errorf("gorm store is required")
	}
	existing, err := observeDemoReadinessData(ctx, store.Gorm, opts)
	if err != nil {
		return nil, err
	}
	for key, value := range existing {
		result[key] = value
	}
	var projectID string
	var repositoryID string
	var remoteIDs []string
	var projectCreated bool
	var repositoryCreated bool
	remoteCreatedCount := 0
	var syncResult AssetSyncResult
	if err := store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		projectID, projectCreated, err = ensureDemoReadinessProject(ctx, tx, opts)
		if err != nil {
			return err
		}
		if opts.ActorUserID != "" {
			if err := ensureDemoReadinessProjectMember(ctx, tx, projectID, opts.ActorUserID); err != nil {
				return err
			}
		}
		repositoryID, repositoryCreated, err = ensureDemoReadinessRepository(ctx, tx, projectID, opts)
		if err != nil {
			return err
		}
		remoteIDs = make([]string, 0, 2)
		for _, spec := range demoReadinessRemoteSpecs() {
			remoteID, created, err := ensureDemoReadinessRemote(ctx, tx, repositoryID, spec)
			if err != nil {
				return err
			}
			remoteIDs = append(remoteIDs, remoteID)
			if created {
				remoteCreatedCount++
			}
		}
		syncResult, err = syncFn(ctx, tx)
		if err != nil {
			return fmt.Errorf("syncing canonical assets for demo readiness data: %w", err)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("recording demo readiness data: %w", err)
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

func observeDemoReadinessData(ctx context.Context, db *gorm.DB, opts DemoReadinessDataOptions) (map[string]any, error) {
	result := map[string]any{
		"project_observed":    false,
		"repository_observed": false,
		"git_remote_count":    0,
	}
	var project GormProject
	if err := db.WithContext(ctx).Where(&GormProject{Slug: opts.ProjectSlug}).First(&project).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return result, nil
		}
		return nil, fmt.Errorf("observing demo readiness project: %w", err)
	}
	result["project_observed"] = true
	result["project_id"] = project.ID

	var repository GormProjectGitRepository
	if err := db.WithContext(ctx).Where(&GormProjectGitRepository{ProjectID: project.ID, RepoKey: opts.RepositoryKey}).First(&repository).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return result, nil
		}
		return nil, fmt.Errorf("observing demo readiness repository: %w", err)
	}
	result["repository_observed"] = true
	result["repository_id"] = repository.ID

	var remotes []GormGitRemote
	if err := db.WithContext(ctx).Where(&GormGitRemote{ProjectGitRepositoryID: repository.ID}).Find(&remotes).Error; err != nil {
		return nil, fmt.Errorf("observing demo readiness remotes: %w", err)
	}
	count := 0
	for _, remote := range remotes {
		if remote.RemoteKey == "gitea" || remote.RemoteKey == "github" {
			count++
		}
	}
	result["git_remote_count"] = count
	return result, nil
}

func ensureDemoReadinessProject(ctx context.Context, tx *gorm.DB, opts DemoReadinessDataOptions) (string, bool, error) {
	var project GormProject
	if err := tx.WithContext(ctx).Where(&GormProject{Slug: opts.ProjectSlug}).First(&project).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, fmt.Errorf("loading demo readiness project: %w", err)
		}
		project = GormProject{Name: opts.ProjectName, Slug: opts.ProjectSlug, Description: "First-version demo project created by the readiness data execution path."}
		if err := tx.WithContext(ctx).Create(&project).Error; err != nil {
			return "", false, fmt.Errorf("creating demo readiness project: %w", err)
		}
		return project.ID, true, nil
	}
	project.Name = opts.ProjectName
	if project.Description == "" {
		project.Description = "First-version demo project created by the readiness data execution path."
	}
	if err := tx.WithContext(ctx).Save(&project).Error; err != nil {
		return "", false, fmt.Errorf("updating demo readiness project: %w", err)
	}
	return project.ID, false, nil
}

func ensureDemoReadinessProjectMember(ctx context.Context, tx *gorm.DB, projectID, actorUserID string) error {
	member := GormProjectMember{ProjectID: projectID, UserID: actorUserID}
	updates := GormProjectMember{Role: "owner"}
	if err := tx.WithContext(ctx).Where(&member).Assign(updates).FirstOrCreate(&member).Error; err != nil {
		return fmt.Errorf("upserting demo readiness project membership: %w", err)
	}
	return nil
}

func ensureDemoReadinessRepository(ctx context.Context, tx *gorm.DB, projectID string, opts DemoReadinessDataOptions) (string, bool, error) {
	var repository GormProjectGitRepository
	if err := tx.WithContext(ctx).Where(&GormProjectGitRepository{ProjectID: projectID, RepoKey: opts.RepositoryKey}).First(&repository).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, fmt.Errorf("loading demo readiness repository: %w", err)
		}
		repository = GormProjectGitRepository{ProjectID: projectID, Name: opts.RepositoryName, RepoKey: opts.RepositoryKey, DisplayName: opts.RepositoryName, RepoRole: "service", Status: "active", Description: "Local first-version demo repository shell for readiness evidence.", DefaultBranch: "main"}
		if err := tx.WithContext(ctx).Create(&repository).Error; err != nil {
			return "", false, fmt.Errorf("creating demo readiness repository: %w", err)
		}
		return repository.ID, true, nil
	}
	repository.Name = opts.RepositoryName
	repository.DisplayName = opts.RepositoryName
	repository.RepoRole = "service"
	repository.Status = "active"
	repository.DefaultBranch = "main"
	if err := tx.WithContext(ctx).Save(&repository).Error; err != nil {
		return "", false, fmt.Errorf("updating demo readiness repository: %w", err)
	}
	return repository.ID, false, nil
}

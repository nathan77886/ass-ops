package app

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"sort"
	"strings"
)

func RecordConfigRepositoryRefRefreshSnapshot(ctx context.Context, store *Store, opts ConfigRepositoryRefRefreshSnapshotOptions) (map[string]any, error) {
	repoID := strings.TrimSpace(opts.RepositoryID)
	if repoID == "" {
		return nil, fmt.Errorf("repository id is required")
	}
	repo := opts.Repository
	if len(repo) == 0 {
		var err error
		repo, err = configRepositorySnapshotRepo(ctx, store.Gorm, repoID)
		if err != nil {
			return nil, err
		}
	}
	projectID := strings.TrimSpace(fmt.Sprint(repo["project_id"]))
	if projectID == "" || projectID == "<nil>" {
		return nil, fmt.Errorf("config repository has no project")
	}
	preview := opts.Preview
	if len(preview) == 0 {
		remotes, err := queryConfigRepositorySnapshotRemotes(ctx, store.Gorm, repoID)
		if err != nil {
			return nil, fmt.Errorf("loading config remotes: %w", err)
		}
		versions, err := queryConfigRepositorySnapshotVersions(ctx, store.Gorm, projectID)
		if err != nil {
			return nil, fmt.Errorf("loading project versions: %w", err)
		}
		workflowOperations, err := queryConfigRepositoryGitWorkflowOperationsGorm(ctx, store.Gorm, projectID, repoID)
		if err != nil {
			return nil, fmt.Errorf("loading config git workflow operations: %w", err)
		}
		refRefreshOperations, err := queryConfigRepositoryRefRefreshOperationsGorm(ctx, store.Gorm, projectID, repoID)
		if err != nil {
			return nil, fmt.Errorf("loading config ref refresh operations: %w", err)
		}
		preview = configRepositoryScaffoldPreview(repo, remotes, versions, workflowOperations, refRefreshOperations)
	}
	assetID, assetErr := gitRepositoryAssetID(ctx, store.Gorm, repoID)
	if assetErr != nil && !errors.Is(assetErr, ErrNotFound) {
		return nil, assetErr
	}
	snapshot := configRepositoryRefRefreshSnapshotPayload(repo, preview, assetErr == nil)
	ready, state, missing := configRepositoryRefRefreshSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                  "config_ref_refresh_snapshot_recording",
		"recording_state":                       state,
		"recording_ready":                       ready,
		"recording_enabled":                     ready && !opts.DryRun,
		"dry_run":                               opts.DryRun,
		"project_id":                            projectID,
		"project_git_repository_id":             repoID,
		"git_repository_asset_observed":         assetErr == nil,
		"snapshot":                              snapshot,
		"snapshots_written":                     0,
		"snapshots_skipped_as_duplicate":        0,
		"ref_refresh_snapshot_written":          false,
		"asset_status_snapshot_written":         false,
		"operation_log_written":                 false,
		"external_call_made":                    false,
		"git_write_performed":                   false,
		"git_commit_created":                    false,
		"git_push_performed":                    false,
		"provider_review_created":               false,
		"project_version_pin_written":           false,
		"live_remote_validation_performed":      false,
		"raw_git_output_recorded":               false,
		"raw_provider_response_recorded":        false,
		"file_content_included":                 false,
		"secret_included":                       false,
		"canonical_asset_status_snapshot_try":   false,
		"snapshot_commit_attempted":             false,
		"config_ref_refresh_observed":           boolOnlyFromAny(snapshot["config_ref_refresh_observed"]),
		"config_ref_refresh_completed":          boolOnlyFromAny(snapshot["config_ref_refresh_completed"]),
		"future_live_workflow_remains_disabled": true,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"git_repository_asset_missing"}
		result["message"] = "Config ref refresh snapshot is derived, but the canonical git_repository asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Config ref refresh snapshot is waiting for terminal ref-refresh evidence; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized config ref refresh snapshot was not written."
		return result, nil
	}
	status, health := configRepositoryRefRefreshSnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "config ref refresh evidence snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording config ref refresh snapshot: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	if written >= 0 {
		result["snapshots_skipped_as_duplicate"] = 1 - written
		result["ref_refresh_snapshot_written"] = written > 0
		result["asset_status_snapshot_written"] = written > 0
	}
	result["message"] = "Sanitized config ref refresh snapshot recorded from local operation evidence."
	return result, nil
}

func configRepositorySnapshotRepo(ctx context.Context, db *gorm.DB, repoID string) (map[string]any, error) {
	var repo GormProjectGitRepository
	if err := db.WithContext(ctx).First(&repo, "id = ?", repoID).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return gitRepositoryMap(repo), nil
}

func queryConfigRepositorySnapshotRemotes(ctx context.Context, db *gorm.DB, repoID string) ([]map[string]any, error) {
	var remotes []GormGitRemote
	if err := db.WithContext(ctx).Where(&GormGitRemote{ProjectGitRepositoryID: repoID}).Find(&remotes).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(remotes))
	for _, remote := range remotes {
		items = append(items, map[string]any{"id": remote.ID, "name": remote.Name, "remote_key": remote.RemoteKey, "provider_type": remote.ProviderType, "remote_role": remote.RemoteRole, "default_branch": remote.DefaultBranch, "latest_sha": remote.LatestSHA, "last_sync_status": remote.LastSyncStatus, "created_at": remote.CreatedAt})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return projectVersionTimeFromAny(items[i]["created_at"]).After(projectVersionTimeFromAny(items[j]["created_at"]))
	})
	return items, nil
}

func queryConfigRepositorySnapshotVersions(ctx context.Context, db *gorm.DB, projectID string) ([]map[string]any, error) {
	var versions []GormProjectVersion
	if err := db.WithContext(ctx).Where(&GormProjectVersion{ProjectID: projectID}).Find(&versions).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(versions))
	for _, version := range versions {
		items = append(items, map[string]any{"id": version.ID, "version": version.Version, "metadata": mapFromAny(version.Metadata.Data), "created_at": version.CreatedAt})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return projectVersionTimeFromAny(items[i]["created_at"]).After(projectVersionTimeFromAny(items[j]["created_at"]))
	})
	return limitMaps(items, 100), nil
}

func queryConfigRepositoryGitWorkflowOperationsGorm(ctx context.Context, db *gorm.DB, projectID, repoID string) ([]map[string]any, error) {
	return queryConfigRepositoryOperationsGorm(ctx, db, projectID, repoID, "config.git_commit", func(input map[string]any) bool {
		return strings.TrimSpace(fmt.Sprint(input["project_git_repository_id"])) == repoID
	}, 20, true)
}

func queryConfigRepositoryRefRefreshOperationsGorm(ctx context.Context, db *gorm.DB, projectID, repoID string) ([]map[string]any, error) {
	return queryConfigRepositoryOperationsGorm(ctx, db, projectID, repoID, "git.refs.refresh", func(input map[string]any) bool {
		return strings.TrimSpace(fmt.Sprint(input["config_repository_id"])) == repoID && strings.TrimSpace(fmt.Sprint(input["refresh_kind"])) == "config_ref_validation_refresh"
	}, 20, false)
}

func queryConfigRepositoryOperationsGorm(ctx context.Context, db *gorm.DB, projectID, repoID, operationType string, matchInput func(map[string]any) bool, limit int, includeLogCount bool) ([]map[string]any, error) {
	var runs []GormOperationRun
	if err := db.WithContext(ctx).Where(&GormOperationRun{OperationType: operationType}).Find(&runs).Error; err != nil {
		return nil, err
	}
	logCounts := map[string]int{}
	if includeLogCount {
		var logs []GormOperationLog
		if err := db.WithContext(ctx).Find(&logs).Error; err != nil {
			return nil, err
		}
		for _, log := range logs {
			if log.OperationRunID.Valid {
				logCounts[log.OperationRunID.String]++
			}
		}
	}
	items := []map[string]any{}
	for _, run := range runs {
		if run.ProjectID.Valid && run.ProjectID.String != projectID {
			continue
		}
		input := mapFromAny(run.Input.Data)
		if !matchInput(input) {
			continue
		}
		item := map[string]any{"id": run.ID, "git_remote_id": nullableStringValue(run.GitRemoteID), "status": run.Status, "error": run.Error, "created_at": run.CreatedAt, "updated_at": run.UpdatedAt, "started_at": nullableTimeAny(run.StartedAt), "finished_at": nullableTimeAny(run.FinishedAt)}
		if includeLogCount {
			item["operation_log_count"] = logCounts[run.ID]
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return projectVersionTimeFromAny(items[i]["created_at"]).After(projectVersionTimeFromAny(items[j]["created_at"]))
	})
	return limitMaps(items, limit), nil
}

func gitRepositoryAssetID(ctx context.Context, db *gorm.DB, repoID string) (string, error) {
	var assets []GormAsset
	if err := db.WithContext(ctx).Where(&GormAsset{AssetType: "git_repository", SourceTable: "project_git_repositories", SourceID: validNullString(repoID)}).Find(&assets).Error; err != nil {
		return "", fmt.Errorf("loading git repository asset: %w", err)
	}
	if len(assets) == 0 {
		return "", ErrNotFound
	}
	sort.SliceStable(assets, func(i, j int) bool {
		if assets[i].UpdatedAt.Equal(assets[j].UpdatedAt) {
			return assets[i].ID > assets[j].ID
		}
		return assets[i].UpdatedAt.After(assets[j].UpdatedAt)
	})
	return assets[0].ID, nil
}

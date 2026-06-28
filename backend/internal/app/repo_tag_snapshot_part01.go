package app

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"strings"
)

type RepoTagRunResultSnapshotOptions struct {
	RepoTagRunID string
	DryRun       bool
}

type RepoTagRunActionsRefreshSnapshotOptions struct {
	RepoTagRunID string
	DryRun       bool
}

func RecordRepoTagRunResultSnapshot(ctx context.Context, store *Store, opts RepoTagRunResultSnapshotOptions) (map[string]any, error) {
	runID := strings.TrimSpace(opts.RepoTagRunID)
	if runID == "" {
		return nil, fmt.Errorf("repo tag run id is required")
	}
	if _, err := uuid.Parse(runID); err != nil {
		return nil, fmt.Errorf("repo tag run id must be a uuid")
	}
	run, err := repoTagRunForSnapshot(ctx, store.Gorm, runID)
	if err != nil {
		return nil, err
	}
	assetID, assetErr := repoTagRunAssetID(ctx, store.Gorm, runID)
	if assetErr != nil && !opts.DryRun {
		return nil, assetErr
	}
	plan := repoTagRemoteRehearsalPlan(run)
	snapshot := repoTagRunResultSnapshotPayload(run, plan, assetErr == nil)
	result := map[string]any{
		"mode":                             "repo_tag_run_result_snapshot_recording",
		"recording_state":                  "ready_to_record",
		"recording_ready":                  true,
		"recording_enabled":                !opts.DryRun,
		"dry_run":                          opts.DryRun,
		"repo_tag_run_id":                  runID,
		"repo_tag_run_asset_observed":      assetErr == nil,
		"snapshot":                         snapshot,
		"snapshots_written":                0,
		"snapshots_skipped_as_duplicate":   0,
		"tag_result_snapshot_written":      false,
		"asset_status_snapshot_written":    false,
		"operation_log_written":            false,
		"external_call_made":               false,
		"remote_tag_lookup_performed":      false,
		"github_actions_refresh_performed": false,
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["missing_evidence"] = []string{"repo_tag_run_asset_missing"}
		result["message"] = "Repo tag result snapshot is derived, but the canonical repo_tag_run asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized repo tag result snapshot was not written."
		return result, nil
	}
	status := cleanPreviewString(snapshot["result_recording_state"])
	if status == "" {
		status = "unknown"
	}
	health := "warning"
	switch status {
	case "recorded":
		health = "low"
	case "failed":
		health = "high"
	}
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "repo tag result snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording repo tag result snapshot: %w", err)
	}
	result["recording_state"] = "recorded"
	result["snapshots_written"] = written
	if written >= 0 {
		result["snapshots_skipped_as_duplicate"] = 1 - written
		result["tag_result_snapshot_written"] = written > 0
		result["asset_status_snapshot_written"] = written > 0
	}
	result["message"] = "Sanitized repo tag result snapshot recorded from local repo_tag_run state."
	return result, nil
}

func RecordRepoTagRunActionsRefreshSnapshot(ctx context.Context, store *Store, opts RepoTagRunActionsRefreshSnapshotOptions) (map[string]any, error) {
	runID := strings.TrimSpace(opts.RepoTagRunID)
	if runID == "" {
		return nil, fmt.Errorf("repo tag run id is required")
	}
	if _, err := uuid.Parse(runID); err != nil {
		return nil, fmt.Errorf("repo tag run id must be a uuid")
	}
	run, err := repoTagRunForSnapshot(ctx, store.Gorm, runID)
	if err != nil {
		return nil, err
	}
	assetID, assetErr := repoTagRunAssetID(ctx, store.Gorm, runID)
	if assetErr != nil && !strings.Contains(assetErr.Error(), "not found") {
		return nil, assetErr
	}
	evidence, err := repoTagRunActionsRefreshEvidence(ctx, store.Gorm, run)
	if err != nil {
		return nil, err
	}
	snapshot := repoTagRunActionsRefreshSnapshotPayload(run, evidence, assetErr == nil)
	ready := boolOnlyFromAny(snapshot["actions_refresh_recording_ready"])
	state := cleanPreviewString(snapshot["actions_refresh_recording_state"])
	if state == "" {
		state = "blocked"
	}
	result := map[string]any{
		"mode":                                  "repo_tag_run_actions_refresh_snapshot_recording",
		"recording_state":                       state,
		"recording_ready":                       ready,
		"recording_enabled":                     ready && !opts.DryRun,
		"dry_run":                               opts.DryRun,
		"repo_tag_run_id":                       runID,
		"repo_tag_run_asset_observed":           assetErr == nil,
		"snapshot":                              snapshot,
		"snapshots_written":                     0,
		"snapshots_skipped_as_duplicate":        0,
		"actions_refresh_snapshot_written":      false,
		"asset_status_snapshot_written":         false,
		"operation_log_written":                 false,
		"external_call_made":                    false,
		"provider_api_called":                   false,
		"github_actions_refresh_performed":      false,
		"github_actions_refresh_evidence_found": boolOnlyFromAny(snapshot["github_actions_refresh_evidence_found"]),
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"repo_tag_run_asset_missing"}
		result["message"] = "Repo tag Actions refresh snapshot is derived, but the canonical repo_tag_run asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["missing_evidence"] = snapshot["missing_evidence"]
		result["message"] = "Repo tag Actions refresh snapshot is waiting for a completed tag run and locally synced GitHub Actions evidence; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["recording_state"] = "ready_to_record"
		result["message"] = "Dry run only; sanitized repo tag Actions refresh snapshot was not written."
		return result, nil
	}
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, "github_actions_refresh_recorded", "low", "repo tag GitHub Actions refresh snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording repo tag Actions refresh snapshot: %w", err)
	}
	result["recording_state"] = "recorded"
	result["snapshots_written"] = written
	if written >= 0 {
		result["snapshots_skipped_as_duplicate"] = 1 - written
		result["actions_refresh_snapshot_written"] = written > 0
		result["asset_status_snapshot_written"] = written > 0
	}
	result["message"] = "Sanitized repo tag GitHub Actions refresh snapshot recorded from local github_action_runs state."
	return result, nil
}

func repoTagRunForSnapshot(ctx context.Context, db *gorm.DB, runID string) (map[string]any, error) {
	if db == nil {
		return nil, fmt.Errorf("gorm database is not configured")
	}
	var run GormRepoTagRun
	if err := db.WithContext(ctx).First(&run, "id = ?", runID).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	item := repoTagRunMap(run)
	if run.ProjectGitRepositoryID.Valid && strings.TrimSpace(run.ProjectGitRepositoryID.String) != "" {
		var repo GormProjectGitRepository
		if err := db.WithContext(ctx).First(&repo, "id = ?", run.ProjectGitRepositoryID.String).Error; err == nil {
			item["project_id"] = firstNonEmptyString(fmt.Sprint(item["project_id"]), repo.ProjectID)
			item["repo_key"] = repo.RepoKey
			item["repo_role"] = repo.RepoRole
		} else if !errorsIsRecordNotFound(err) {
			return nil, err
		}
	}
	remoteID := cleanOptionalID(firstNonEmptyString(run.TargetRemoteID.String, run.GitRemoteID))
	if remoteID != "" {
		var remote GormGitRemote
		if err := db.WithContext(ctx).First(&remote, "id = ?", remoteID).Error; err == nil {
			item["provider_type"] = remote.ProviderType
			item["remote_role"] = remote.RemoteRole
		} else if !errorsIsRecordNotFound(err) {
			return nil, err
		}
	}
	return item, nil
}

func repoTagRunProjectID(ctx context.Context, db *gorm.DB, runID string) (string, error) {
	run, err := repoTagRunForSnapshot(ctx, db, runID)
	if err != nil {
		return "", err
	}
	projectID := cleanOptionalID(fmt.Sprint(run["project_id"]))
	if projectID == "" {
		return "", fmt.Errorf("repo tag run %s has no project binding", runID)
	}
	return projectID, nil
}

func repoTagRunAssetID(ctx context.Context, db *gorm.DB, runID string) (string, error) {
	var asset GormAsset
	err := db.WithContext(ctx).Where(&GormAsset{AssetType: "repo_tag_run", SourceTable: "repo_tag_runs", SourceID: validNullString(runID)}).First(&asset).Error
	if err != nil {
		if errorsIsRecordNotFound(err) {
			return "", fmt.Errorf("repo_tag_run asset for %s not found; run db sync-assets first", runID)
		}
		return "", err
	}
	assetID := strings.TrimSpace(asset.ID)
	if assetID == "" {
		return "", fmt.Errorf("repo_tag_run asset for %s has empty id", runID)
	}
	return assetID, nil
}

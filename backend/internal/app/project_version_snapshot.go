package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

type ProjectVersionValidationSnapshotOptions struct {
	ProjectVersionID       string
	DryRun                 bool
	RequireRecordedRefresh bool
	RecordingTrigger       string
}

func RecordProjectVersionValidationSnapshot(ctx context.Context, store *Store, opts ProjectVersionValidationSnapshotOptions) (map[string]any, error) {
	versionID := strings.TrimSpace(opts.ProjectVersionID)
	if versionID == "" {
		return nil, fmt.Errorf("project version id is required")
	}
	preview, err := projectVersionValidationPreviewFromDB(ctx, store.Gorm, versionID)
	if err != nil {
		return nil, err
	}
	recordingTrigger := strings.TrimSpace(opts.RecordingTrigger)
	if recordingTrigger == "" {
		recordingTrigger = "operator_request"
	}
	assetID, assetErr := projectVersionAssetID(ctx, store.Gorm, versionID)
	snapshot := projectVersionValidationSnapshotPayload(preview, assetErr == nil)
	result := map[string]any{
		"mode":                           "project_version_validation_snapshot_recording",
		"recording_state":                "ready_to_record",
		"recording_ready":                true,
		"recording_enabled":              !opts.DryRun,
		"recording_trigger":              recordingTrigger,
		"auto_record_terminal_required":  opts.RequireRecordedRefresh,
		"auto_record_terminal_satisfied": !opts.RequireRecordedRefresh,
		"dry_run":                        opts.DryRun,
		"project_version_id":             versionID,
		"project_version_asset_observed": assetErr == nil,
		"snapshot":                       snapshot,
		"snapshots_written":              0,
		"snapshots_skipped_as_duplicate": 0,
		"validation_snapshot_written":    false,
		"asset_status_snapshot_written":  false,
		"operation_log_written":          false,
		"external_call_made":             false,
	}
	if opts.RequireRecordedRefresh {
		ready, state, missing := projectVersionValidationSnapshotAutoRecordReadiness(preview, snapshot)
		result["recording_state"] = state
		result["recording_ready"] = ready
		result["recording_enabled"] = ready && !opts.DryRun
		result["auto_record_terminal_satisfied"] = ready
		if len(missing) > 0 {
			result["missing_evidence"] = missing
		}
		if !ready {
			result["message"] = "ProjectVersion validation snapshot auto-recording is waiting for a terminal recorded refresh result; no snapshot was written."
			return result, nil
		}
		result["recording_state"] = "ready_to_record"
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["missing_evidence"] = []string{"project_version_asset_missing"}
		result["message"] = "ProjectVersion validation snapshot is derived, but the canonical project_version asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized ProjectVersion validation snapshot was not written."
		return result, nil
	}
	status := strings.TrimSpace(fmt.Sprint(snapshot["validation_state"]))
	if status == "" || status == "<nil>" {
		status = "unknown"
	}
	health := "warning"
	switch status {
	case "ready":
		health = "low"
	case "blocked":
		health = "high"
	}
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "project version validation snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording project version validation snapshot: %w", err)
	}
	result["recording_state"] = "recorded"
	result["snapshots_written"] = written
	if written >= 0 {
		result["snapshots_skipped_as_duplicate"] = 1 - written
		result["validation_snapshot_written"] = written > 0
		result["asset_status_snapshot_written"] = written > 0
	}
	result["message"] = "Sanitized ProjectVersion validation snapshot recorded from local synced database state."
	return result, nil
}

func projectVersionValidationSnapshotAutoRecordReadiness(preview map[string]any, snapshot map[string]any) (bool, string, []string) {
	summary := mapFromAny(preview["provider_refresh_result_summary"])
	rerunEvidence := mapFromAny(preview["validation_rerun_evidence"])
	status := strings.TrimSpace(fmt.Sprint(summary["validation_rerun_status"]))
	if status == "" || status == "<nil>" {
		status = "not_requested"
	}
	activeCount := intFromAny(summary["active_count"], 0)
	operationCount := intFromAny(summary["operation_count"], 0)
	state := "blocked"
	if status == "waiting_for_workers" || activeCount > 0 {
		state = "waiting_for_workers"
	}
	missing := []string{}
	if operationCount == 0 {
		missing = append(missing, "provider_refresh_execution_not_performed")
	}
	if activeCount > 0 {
		missing = append(missing, "refresh_workers_still_running")
	}
	if status != "recorded" {
		missing = append(missing, "validation_rerun_not_recorded")
	}
	if !boolOnlyFromAny(rerunEvidence["server_side_validation_recheck_ready"]) {
		missing = append(missing, "server_side_validation_recheck_not_terminal")
	}
	if !boolOnlyFromAny(snapshot["snapshot_ready_for_review"]) {
		missing = append(missing, "validation_snapshot_not_ready_for_review")
	}
	ready := operationCount > 0 &&
		activeCount == 0 &&
		status == "recorded" &&
		boolOnlyFromAny(rerunEvidence["server_side_validation_recheck_ready"]) &&
		boolOnlyFromAny(snapshot["snapshot_ready_for_review"])
	if ready {
		return true, "ready_to_record", nil
	}
	return false, state, missing
}

func projectVersionValidationPreviewFromDB(ctx context.Context, db *gorm.DB, versionID string) (map[string]any, error) {
	version, err := projectVersionByIDGorm(ctx, db, versionID)
	if err != nil {
		return nil, err
	}
	projectID := version.ProjectID
	versionMap := projectVersionSnapshotMap(version)
	remotes, repoIDs, remoteIDs, err := projectVersionRemoteMaps(ctx, db, projectID)
	if err != nil {
		return nil, fmt.Errorf("loading version remotes: %w", err)
	}
	tagRuns, err := projectVersionTagRunMaps(ctx, db, projectID)
	if err != nil {
		return nil, fmt.Errorf("loading version tag runs: %w", err)
	}
	actionRuns, err := projectVersionActionRunMaps(ctx, db, remoteIDs)
	if err != nil {
		return nil, fmt.Errorf("loading version action runs: %w", err)
	}
	argoApps, err := projectVersionArgoAppMaps(ctx, db, projectID)
	if err != nil {
		return nil, fmt.Errorf("loading version Argo apps: %w", err)
	}
	argoConnections, err := projectVersionArgoConnectionMaps(ctx, db, projectID)
	if err != nil {
		return nil, fmt.Errorf("loading version Argo connections: %w", err)
	}
	refreshOperations, err := queryProjectVersionRefreshOperationsGorm(ctx, db, versionID, projectID)
	if err != nil {
		return nil, fmt.Errorf("loading project version refresh operations: %w", err)
	}
	backgroundOperations, err := queryProjectVersionValidationRerunOperationsGorm(ctx, db, versionID, projectID)
	if err != nil {
		return nil, fmt.Errorf("loading project version validation rerun operations: %w", err)
	}
	_ = repoIDs
	return projectVersionValidationPreview(versionMap, remotes, tagRuns, actionRuns, argoApps, argoConnections, refreshOperations, backgroundOperations), nil
}

func projectVersionAssetID(ctx context.Context, db *gorm.DB, versionID string) (string, error) {
	var asset GormAsset
	if err := db.WithContext(ctx).Where(&GormAsset{AssetType: "project_version", SourceTable: "project_versions", SourceID: validNullString(versionID)}).First(&asset).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return "", fmt.Errorf("project_version asset for %s not found; run db sync-assets first", versionID)
		}
		return "", err
	}
	assetID := strings.TrimSpace(asset.ID)
	if assetID == "" {
		return "", fmt.Errorf("project_version asset for %s has empty id", versionID)
	}
	return assetID, nil
}

func projectVersionByIDGorm(ctx context.Context, db *gorm.DB, versionID string) (GormProjectVersion, error) {
	if db == nil {
		return GormProjectVersion{}, fmt.Errorf("gorm database is not configured")
	}
	var version GormProjectVersion
	if err := db.WithContext(ctx).First(&version, &GormProjectVersion{ID: versionID}).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return GormProjectVersion{}, ErrNotFound
		}
		return GormProjectVersion{}, err
	}
	return version, nil
}

func projectVersionSnapshotMap(version GormProjectVersion) map[string]any {
	return map[string]any{"id": version.ID, "project_id": version.ProjectID, "version": version.Version, "source": version.Source, "metadata": mapFromAny(version.Metadata.Data), "created_at": version.CreatedAt}
}

func projectVersionRemoteMaps(ctx context.Context, db *gorm.DB, projectID string) ([]map[string]any, map[string]bool, map[string]bool, error) {
	var repos []GormProjectGitRepository
	if err := db.WithContext(ctx).Where(&GormProjectGitRepository{ProjectID: projectID}).Find(&repos).Error; err != nil {
		return nil, nil, nil, err
	}
	repoByID := map[string]GormProjectGitRepository{}
	repoIDs := map[string]bool{}
	for _, repo := range repos {
		repoByID[repo.ID] = repo
		repoIDs[repo.ID] = true
	}
	var remotes []GormGitRemote
	if err := db.WithContext(ctx).Find(&remotes).Error; err != nil {
		return nil, nil, nil, err
	}
	items := []map[string]any{}
	remoteIDs := map[string]bool{}
	for _, remote := range remotes {
		repo, ok := repoByID[remote.ProjectGitRepositoryID]
		if !ok {
			continue
		}
		remoteIDs[remote.ID] = true
		items = append(items, map[string]any{"id": remote.ID, "remote_key": remote.RemoteKey, "provider_type": remote.ProviderType, "latest_sha": remote.LatestSHA, "default_branch": remote.DefaultBranch, "repo_key": repo.RepoKey, "repo_role": repo.RepoRole, "repository_name": repo.Name})
	}
	return items, repoIDs, remoteIDs, nil
}

func projectVersionTagRunMaps(ctx context.Context, db *gorm.DB, projectID string) ([]map[string]any, error) {
	var runs []GormRepoTagRun
	if err := db.WithContext(ctx).Find(&runs).Error; err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, run := range runs {
		if !run.ProjectID.Valid || run.ProjectID.String != projectID {
			continue
		}
		items = append(items, map[string]any{"id": run.ID, "project_git_repository_id": nullableStringValue(run.ProjectGitRepositoryID), "target_remote_id": nullableStringValue(run.TargetRemoteID), "git_remote_id": run.GitRemoteID, "tag_name": run.TagName, "target_sha": run.TargetSHA, "status": run.Status, "created_at": run.CreatedAt, "finished_at": nullableTimeAny(run.FinishedAt)})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return projectVersionTimeFromAny(items[i]["created_at"]).After(projectVersionTimeFromAny(items[j]["created_at"]))
	})
	return limitMaps(items, 500), nil
}

func projectVersionActionRunMaps(ctx context.Context, db *gorm.DB, remoteIDs map[string]bool) ([]map[string]any, error) {
	var runs []GormGitHubActionRun
	if err := db.WithContext(ctx).Find(&runs).Error; err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, run := range runs {
		if !remoteIDs[run.GitRemoteID] {
			continue
		}
		items = append(items, map[string]any{"id": run.ID, "git_remote_id": run.GitRemoteID, "run_id": run.RunID, "workflow_name": run.WorkflowName, "branch": run.Branch, "commit_sha": run.CommitSHA, "status": run.Status, "conclusion": run.Conclusion, "started_at": nullableTimeAny(run.StartedAt), "updated_at": nullableTimeAny(run.UpdatedAt)})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return projectVersionTimeFromAny(items[i]["updated_at"]).After(projectVersionTimeFromAny(items[j]["updated_at"]))
	})
	return limitMaps(items, 500), nil
}

func projectVersionArgoAppMaps(ctx context.Context, db *gorm.DB, projectID string) ([]map[string]any, error) {
	var apps []GormArgoApp
	if err := db.WithContext(ctx).Where(&GormArgoApp{ProjectID: projectID}).Find(&apps).Error; err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, app := range apps {
		items = append(items, map[string]any{"id": app.ID, "name": app.Name, "namespace": app.Namespace, "status": app.Status, "metadata": mapFromAny(app.Metadata.Data), "synced_at": nullableTimeAny(app.SyncedAt), "updated_at": app.UpdatedAt})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return projectVersionTimeFromAny(items[i]["updated_at"]).After(projectVersionTimeFromAny(items[j]["updated_at"]))
	})
	return limitMaps(items, 500), nil
}

func projectVersionArgoConnectionMaps(ctx context.Context, db *gorm.DB, projectID string) ([]map[string]any, error) {
	var connections []GormArgoConnection
	if err := db.WithContext(ctx).Where(&GormArgoConnection{ProjectID: projectID}).Find(&connections).Error; err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, connection := range connections {
		items = append(items, map[string]any{"id": connection.ID, "name": connection.Name, "last_sync_status": connection.LastSyncStatus, "updated_at": connection.UpdatedAt})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return projectVersionTimeFromAny(items[i]["updated_at"]).After(projectVersionTimeFromAny(items[j]["updated_at"]))
	})
	return limitMaps(items, 100), nil
}

func queryProjectVersionRefreshOperationsGorm(ctx context.Context, db *gorm.DB, versionID, projectID string) ([]map[string]any, error) {
	return queryProjectVersionOperationsGorm(ctx, db, versionID, projectID, map[string]bool{"git.refs.refresh": true, "github.actions.sync": true, "argo.apps.sync": true}, 50)
}

func queryProjectVersionValidationRerunOperationsGorm(ctx context.Context, db *gorm.DB, versionID, projectID string) ([]map[string]any, error) {
	return queryProjectVersionOperationsGorm(ctx, db, versionID, projectID, map[string]bool{"project_version.validation_rerun": true}, 20)
}

func queryProjectVersionOperationsGorm(ctx context.Context, db *gorm.DB, versionID, projectID string, types map[string]bool, limit int) ([]map[string]any, error) {
	var runs []GormOperationRun
	if err := db.WithContext(ctx).Find(&runs).Error; err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, run := range runs {
		if !types[run.OperationType] {
			continue
		}
		if run.ProjectID.Valid && run.ProjectID.String != projectID {
			continue
		}
		input := mapFromAny(run.Input.Data)
		if strings.TrimSpace(fmt.Sprint(input["project_version_id"])) != versionID {
			continue
		}
		items = append(items, map[string]any{"id": run.ID, "operation_type": run.OperationType, "status": run.Status, "error": run.Error, "input": input, "result": mapFromAny(run.Result.Data), "started_at": nullableTimeAny(run.StartedAt), "finished_at": nullableTimeAny(run.FinishedAt), "created_at": run.CreatedAt, "updated_at": run.UpdatedAt})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return projectVersionTimeFromAny(items[i]["created_at"]).After(projectVersionTimeFromAny(items[j]["created_at"]))
	})
	return limitMaps(items, limit), nil
}

func limitMaps(items []map[string]any, limit int) []map[string]any {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

func projectVersionTimeFromAny(value any) time.Time {
	switch typed := value.(type) {
	case time.Time:
		return typed
	case *time.Time:
		if typed != nil {
			return *typed
		}
	}
	return time.Time{}
}

func projectVersionValidationSnapshotPayload(preview map[string]any, assetObserved bool) map[string]any {
	summary := mapFromAny(preview["provider_refresh_result_summary"])
	rerunEvidence := mapFromAny(preview["validation_rerun_evidence"])
	backgroundPlan := mapFromAny(preview["background_validation_rerun_plan"])
	snapshotPlan := mapFromAny(backgroundPlan["validation_snapshot_write_plan"])
	return map[string]any{
		"mode":                                 "project_version_validation_snapshot",
		"project_version_id":                   preview["version_id"],
		"validation_state":                     preview["validation_state"],
		"repository_count":                     intFromAny(preview["repository_count"], 0),
		"ready_count":                          intFromAny(preview["ready_count"], 0),
		"partial_count":                        intFromAny(preview["partial_count"], 0),
		"blocked_count":                        intFromAny(preview["blocked_count"], 0),
		"provider_refresh_status":              summary["validation_rerun_status"],
		"operation_count":                      intFromAny(summary["operation_count"], 0),
		"active_count":                         intFromAny(summary["active_count"], 0),
		"terminal_count":                       intFromAny(summary["terminal_count"], 0),
		"server_side_validation_recheck":       boolOnlyFromAny(rerunEvidence["server_side_validation_recheck"]),
		"server_side_validation_recheck_ready": boolOnlyFromAny(rerunEvidence["server_side_validation_recheck_ready"]),
		"validation_rerun_recorded":            boolOnlyFromAny(summary["validation_rerun_recorded"]),
		"snapshot_state":                       snapshotPlan["snapshot_state"],
		"snapshot_ready_for_review":            boolOnlyFromAny(snapshotPlan["snapshot_ready_for_review"]),
		"project_version_asset_observed":       assetObserved,
		"validation_source":                    "local_synced_database_state",
		"external_call_made":                   false,
		"provider_api_called":                  false,
		"git_fetch_performed":                  false,
		"argocd_api_called":                    false,
		"raw_response_included":                false,
		"secret_included":                      false,
		"operation_log_written":                false,
		"background_worker_enqueued":           false,
		"missing_required_evidence":            projectVersionValidationSnapshotMissingEvidence(preview, summary, rerunEvidence, assetObserved),
	}
}

func projectVersionValidationSnapshotMissingEvidence(preview, summary, rerunEvidence map[string]any, assetObserved bool) []string {
	missing := []string{}
	if !assetObserved {
		missing = append(missing, "project_version_asset_missing")
	}
	if intFromAny(preview["repository_count"], 0) == 0 {
		missing = append(missing, "project_version_repository_manifest_missing")
	}
	if !boolOnlyFromAny(rerunEvidence["server_side_validation_recheck_ready"]) {
		missing = append(missing, "server_side_validation_recheck_not_terminal")
	}
	if !boolOnlyFromAny(summary["validation_rerun_recorded"]) {
		missing = append(missing, "validation_rerun_not_recorded")
	}
	return missing
}

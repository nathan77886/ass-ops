package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
)

type ConfigRepositoryGitWorkflowPromotionSnapshotOptions struct {
	RepositoryID string
	DryRun       bool
	Repository   map[string]any
	Preview      map[string]any
}

type ConfigRepositoryRefRefreshSnapshotOptions struct {
	RepositoryID string
	DryRun       bool
	Repository   map[string]any
	Preview      map[string]any
}

func (s *Server) recordConfigRepositoryGitWorkflowPromotionSnapshot(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	projectID, err := s.projectIDForRepositoryGorm(r.Context(), repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	resource := PolicyResource{Type: "git_repository", ID: repoID, ProjectID: projectID}
	if !s.requireProjectMembershipForPolicy(w, r, resource) {
		return
	}
	decision := NewPolicyChecker().Check(currentUser(r), resource, "config.git_commit")
	if decision.Effect != PolicyAllow {
		writeJSON(w, http.StatusForbidden, decision)
		return
	}
	repo, _, _, preview, err := s.configRepositoryScaffoldPreviewForRequest(r.Context(), repoID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load config scaffold preview")
		return
	}
	result, err := RecordConfigRepositoryGitWorkflowPromotionSnapshot(r.Context(), s.store, ConfigRepositoryGitWorkflowPromotionSnapshotOptions{
		RepositoryID: repoID,
		DryRun:       req.DryRun,
		Repository:   repo,
		Preview:      preview,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("config git workflow promotion snapshot failed", "repository_id", repoID, "project_id", projectID, "error", err)
		}
		writeError(w, http.StatusBadRequest, "record config git workflow promotion snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordConfigRepositoryRefRefreshSnapshot(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	projectID, err := s.projectIDForRepositoryGorm(r.Context(), repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	resource := PolicyResource{Type: "git_repository", ID: repoID, ProjectID: projectID}
	if !s.requireProjectPolicy(w, r, resource, "git.refs.refresh") {
		return
	}
	repo, _, _, preview, err := s.configRepositoryScaffoldPreviewForRequest(r.Context(), repoID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load config scaffold preview")
		return
	}
	result, err := RecordConfigRepositoryRefRefreshSnapshot(r.Context(), s.store, ConfigRepositoryRefRefreshSnapshotOptions{
		RepositoryID: repoID,
		DryRun:       req.DryRun,
		Repository:   repo,
		Preview:      preview,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("config ref refresh snapshot failed", "repository_id", repoID, "project_id", projectID, "error", err)
		}
		writeError(w, http.StatusBadRequest, "record config ref refresh snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func RecordConfigRepositoryGitWorkflowPromotionSnapshot(ctx context.Context, store *Store, opts ConfigRepositoryGitWorkflowPromotionSnapshotOptions) (map[string]any, error) {
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
		preview = configRepositoryScaffoldPreview(repo, remotes, versions, workflowOperations)
	}
	assetID, assetErr := gitRepositoryAssetID(ctx, store.Gorm, repoID)
	if assetErr != nil && !errors.Is(assetErr, ErrNotFound) {
		return nil, assetErr
	}
	snapshot := configRepositoryGitWorkflowPromotionSnapshotPayload(repo, preview, assetErr == nil)
	ready, state, missing := configRepositoryGitWorkflowPromotionSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                  "config_git_workflow_promotion_snapshot_recording",
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
		"promotion_snapshot_written":            false,
		"asset_status_snapshot_written":         false,
		"operation_log_written":                 false,
		"external_call_made":                    false,
		"git_workspace_mutation_enabled":        false,
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
		"audit_operation_observed":              boolOnlyFromAny(snapshot["audit_operation_observed"]),
		"sanitized_audit_result_recorded":       boolOnlyFromAny(snapshot["sanitized_audit_result_recorded"]),
		"promotion_ready_for_operator_review":   boolOnlyFromAny(snapshot["promotion_ready_for_operator_review"]),
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
		result["message"] = "Config Git workflow promotion snapshot is derived, but the canonical git_repository asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Config Git workflow promotion snapshot is waiting for sanitized audit evidence ready for operator review; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized config Git workflow promotion snapshot was not written."
		return result, nil
	}
	status, health := configRepositoryGitWorkflowPromotionSnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "config Git workflow promotion review snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording config Git workflow promotion snapshot: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	if written >= 0 {
		result["snapshots_skipped_as_duplicate"] = 1 - written
		result["promotion_snapshot_written"] = written > 0
		result["asset_status_snapshot_written"] = written > 0
	}
	result["message"] = "Sanitized config Git workflow promotion snapshot recorded from local audit evidence."
	return result, nil
}

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

func configRepositoryGitWorkflowPromotionSnapshotPayload(repo map[string]any, preview map[string]any, assetObserved bool) map[string]any {
	commitPlan := mapFromAny(preview["git_commit_plan"])
	promotionPlan := mapFromAny(commitPlan["promotion_readiness_plan"])
	workflowEvidence := mapFromAny(preview["git_workflow_audit_evidence"])
	resultPlan := mapFromAny(commitPlan["result_recording_plan"])
	statusSnapshotWriteEligible := assetObserved
	return map[string]any{
		"mode":                                  "config_git_workflow_promotion_snapshot",
		"project_git_repository_id":             cleanOptionalID(fmt.Sprint(repo["id"])),
		"repo_key":                              cleanPreviewString(repo["repo_key"]),
		"repo_role":                             cleanPreviewString(repo["repo_role"]),
		"scaffold_state":                        cleanPreviewString(preview["scaffold_state"]),
		"file_count":                            intFromAny(preview["file_count"], 0),
		"remote_count":                          intFromAny(preview["remote_count"], 0),
		"git_repository_asset_observed":         assetObserved,
		"status_snapshot_write_eligible":        statusSnapshotWriteEligible,
		"status_snapshot_written":               statusSnapshotWriteEligible,
		"audit_operation_observed":              boolOnlyFromAny(promotionPlan["audit_operation_observed"]),
		"sanitized_audit_result_recorded":       boolOnlyFromAny(promotionPlan["sanitized_audit_result_recorded"]),
		"promotion_state":                       cleanPreviewString(promotionPlan["promotion_state"]),
		"promotion_ready_for_operator_review":   boolOnlyFromAny(promotionPlan["promotion_ready"]),
		"promotion_ready_reason":                cleanPreviewString(promotionPlan["promotion_ready_reason"]),
		"result_recording_state":                cleanPreviewString(resultPlan["result_recording_state"]),
		"result_recording_ready":                boolOnlyFromAny(resultPlan["result_recording_ready"]),
		"workflow_evidence_state":               cleanPreviewString(workflowEvidence["evidence_state"]),
		"workflow_operation_count":              intFromAny(workflowEvidence["operation_count"], 0),
		"workflow_operation_log_count":          intFromAny(workflowEvidence["operation_log_count"], 0),
		"workflow_active_count":                 intFromAny(workflowEvidence["active_count"], 0),
		"workflow_failed_count":                 intFromAny(workflowEvidence["failed_count"], 0),
		"workflow_canceled_count":               intFromAny(workflowEvidence["canceled_count"], 0),
		"project_version_pin_observed":          boolOnlyFromAny(promotionPlan["project_version_pin_observed"]),
		"live_commit_validation_observed":       boolOnlyFromAny(promotionPlan["live_commit_validation_observed"]),
		"live_git_workflow_enabled":             false,
		"live_git_commit_enabled":               false,
		"git_workspace_mutation_enabled":        false,
		"git_commit_created":                    false,
		"git_push_performed":                    false,
		"provider_review_created":               false,
		"project_version_pin_written":           false,
		"live_remote_validation_performed":      false,
		"external_call_made":                    false,
		"file_content_included":                 false,
		"secret_included":                       false,
		"contains_file_content":                 false,
		"contains_remote_url":                   false,
		"contains_credentials":                  false,
		"contains_commit_sha":                   false,
		"contains_branch_name":                  false,
		"contains_git_output":                   false,
		"contains_provider_response":            false,
		"raw_git_output_recorded":               false,
		"raw_provider_response_recorded":        false,
		"operation_log_written":                 false,
		"future_live_workflow_remains_disabled": true,
		"required_controls":                     promotionPlan["required_controls"],
		"disabled_backends":                     promotionPlan["disabled_backends"],
		"promotion_blockers":                    promotionPlan["promotion_blockers"],
		"suppressed_fields":                     promotionPlan["suppressed_fields"],
	}
}

func configRepositoryGitWorkflowPromotionSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := cleanPreviewString(snapshot["promotion_state"])
	if state == "" {
		state = "blocked"
	}
	if !boolOnlyFromAny(snapshot["git_repository_asset_observed"]) {
		missing = append(missing, "git_repository_asset_missing")
	}
	if !boolOnlyFromAny(snapshot["audit_operation_observed"]) {
		missing = append(missing, "config_git_workflow_audit_operation_missing")
	}
	if !boolOnlyFromAny(snapshot["sanitized_audit_result_recorded"]) {
		missing = append(missing, "sanitized_config_git_workflow_audit_result_not_recorded")
	}
	if !boolOnlyFromAny(snapshot["promotion_ready_for_operator_review"]) {
		missing = append(missing, "config_git_workflow_promotion_not_ready")
	}
	if len(missing) > 0 {
		return false, state, missing
	}
	return true, "promotion_review_ready", nil
}

func configRepositoryGitWorkflowPromotionSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "promotion_review_ready":
		return "config_git_workflow_promotion_review_ready", "low"
	case "failed", "mixed_failed", "canceled", "unknown":
		return "config_git_workflow_promotion_" + state, "high"
	default:
		return "config_git_workflow_promotion_" + state, "warning"
	}
}

func configRepositoryRefRefreshSnapshotPayload(repo map[string]any, preview map[string]any, assetObserved bool) map[string]any {
	commitPlan := mapFromAny(preview["git_commit_plan"])
	refEvidence := mapFromAny(preview["config_ref_refresh_evidence"])
	statusSnapshotWriteEligible := assetObserved
	return map[string]any{
		"mode":                                  "config_ref_refresh_snapshot",
		"project_git_repository_id":             cleanOptionalID(fmt.Sprint(repo["id"])),
		"repo_key":                              cleanPreviewString(repo["repo_key"]),
		"repo_role":                             cleanPreviewString(repo["repo_role"]),
		"scaffold_state":                        cleanPreviewString(preview["scaffold_state"]),
		"file_count":                            intFromAny(preview["file_count"], 0),
		"remote_count":                          intFromAny(preview["remote_count"], 0),
		"git_repository_asset_observed":         assetObserved,
		"status_snapshot_write_eligible":        statusSnapshotWriteEligible,
		"status_snapshot_written":               statusSnapshotWriteEligible,
		"config_ref_refresh_observed":           boolOnlyFromAny(refEvidence["has_ref_refresh_operations"]),
		"config_ref_refresh_completed":          boolOnlyFromAny(refEvidence["git_fetch_performed"]),
		"refresh_state":                         cleanPreviewString(refEvidence["refresh_state"]),
		"ref_refresh_operation_count":           intFromAny(refEvidence["operation_count"], 0),
		"ref_refresh_active_count":              intFromAny(refEvidence["active_count"], 0),
		"ref_refresh_completed_count":           intFromAny(refEvidence["completed_count"], 0),
		"ref_refresh_failed_count":              intFromAny(refEvidence["failed_count"], 0),
		"ref_refresh_canceled_count":            intFromAny(refEvidence["canceled_count"], 0),
		"ref_refresh_unknown_count":             intFromAny(refEvidence["unknown_count"], 0),
		"commit_plan_state":                     cleanPreviewString(commitPlan["plan_state"]),
		"config_ref_refresh_plan_observed":      boolOnlyFromAny(commitPlan["config_ref_refresh_observed"]),
		"config_ref_refresh_plan_completed":     boolOnlyFromAny(commitPlan["config_ref_refresh_completed"]),
		"live_commit_validation_input_source":   cleanPreviewString(refEvidence["live_commit_validation_input_source"]),
		"git_fetch_performed":                   boolOnlyFromAny(refEvidence["git_fetch_performed"]),
		"git_write_performed":                   false,
		"git_commit_created":                    false,
		"git_push_performed":                    false,
		"provider_review_created":               false,
		"project_version_pin_written":           false,
		"live_remote_validation_performed":      false,
		"external_call_made":                    false,
		"file_content_included":                 false,
		"secret_included":                       false,
		"contains_file_content":                 false,
		"contains_remote_url":                   false,
		"contains_credentials":                  false,
		"contains_commit_sha":                   false,
		"contains_branch_name":                  false,
		"contains_git_output":                   false,
		"contains_provider_response":            false,
		"raw_git_output_recorded":               false,
		"raw_provider_response_recorded":        false,
		"operation_log_written":                 false,
		"future_live_workflow_remains_disabled": true,
		"required_controls":                     []string{"config_remote_review", "git_ref_refresh_worker", "synced_state_review", "redacted_snapshot_recording"},
		"disabled_backends":                     []string{"git_write", "git_commit", "git_push", "pull_request_create", "project_version_update", "live_commit_validation"},
		"suppressed_fields":                     []string{"remote_url", "git_credentials", "provider_token", "authorization_header", "git_output", "commit_sha", "branch_name", "error_message"},
	}
}

func configRepositoryRefRefreshSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := cleanPreviewString(snapshot["refresh_state"])
	if state == "" {
		state = "not_requested"
	}
	if !boolOnlyFromAny(snapshot["git_repository_asset_observed"]) {
		missing = append(missing, "git_repository_asset_missing")
	}
	if !boolOnlyFromAny(snapshot["config_ref_refresh_observed"]) {
		missing = append(missing, "config_ref_refresh_operation_missing")
	}
	if state == "waiting_for_worker" {
		missing = append(missing, "config_ref_refresh_waiting_for_worker")
	}
	if state == "failed" {
		missing = append(missing, "config_ref_refresh_failed")
	}
	if state == "canceled" {
		missing = append(missing, "config_ref_refresh_canceled")
	}
	if state == "unknown" {
		missing = append(missing, "config_ref_refresh_unknown")
	}
	if !boolOnlyFromAny(snapshot["config_ref_refresh_completed"]) {
		missing = append(missing, "config_ref_refresh_not_completed")
	}
	if len(missing) > 0 {
		return false, state, missing
	}
	return true, "ref_refresh_recorded", nil
}

func configRepositoryRefRefreshSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "ref_refresh_recorded":
		return "config_ref_refresh_recorded", "low"
	case "failed", "canceled", "unknown":
		return "config_ref_refresh_" + state, "high"
	default:
		return "config_ref_refresh_" + state, "warning"
	}
}

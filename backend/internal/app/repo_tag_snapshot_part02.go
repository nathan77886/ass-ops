package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"strings"
	"time"
)

func repoTagRunActionsRefreshEvidence(ctx context.Context, db *gorm.DB, run map[string]any) (map[string]any, error) {
	remoteID := cleanOptionalID(firstNonEmptyString(fmt.Sprint(run["target_remote_id"]), fmt.Sprint(run["git_remote_id"])))
	targetSHA := cleanPreviewString(run["target_sha"])
	evidence := map[string]any{
		"mode":                                  "repo_tag_github_actions_refresh_evidence",
		"target_remote_bound":                   remoteID != "",
		"target_sha_configured":                 targetSHA != "",
		"evidence_scope":                        "commit",
		"github_actions_refresh_evidence_found": false,
		"github_actions_total":                  0,
		"github_actions_success":                0,
		"github_actions_failure":                0,
		"github_actions_active":                 0,
		"github_actions_synced":                 0,
		"github_action_run_link_count":          0,
		"latest_synced_at":                      nil,
		"latest_updated_at":                     nil,
		"provider_api_called":                   false,
		"external_call_made":                    false,
		"contains_provider_response":            false,
		"contains_remote_url":                   false,
		"contains_ref_name":                     false,
		"contains_commit_sha":                   false,
		"contains_workflow_name":                false,
		"contains_html_url":                     false,
	}
	if remoteID == "" {
		return evidence, nil
	}
	if targetSHA == "" {
		evidence["evidence_scope"] = "remote_all_commits"
	}
	var runs []GormGitHubActionRun
	query := db.WithContext(ctx).Where(&GormGitHubActionRun{GitRemoteID: remoteID})
	if targetSHA != "" {
		query = query.Where(&GormGitHubActionRun{CommitSHA: targetSHA})
	}
	if err := query.Find(&runs).Error; err != nil {
		return nil, err
	}
	total := len(runs)
	successCount := 0
	failureCount := 0
	activeCount := 0
	syncedCount := 0
	var latestSyncedAt any
	var latestUpdatedAt any
	for _, actionRun := range runs {
		if strings.EqualFold(actionRun.Conclusion, "success") {
			successCount++
		}
		state := strings.ToLower(firstNonEmptyString(actionRun.Conclusion, actionRun.Status))
		switch state {
		case "failure", "failed", "error", "timed_out", "cancelled", "canceled":
			failureCount++
		}
		switch strings.ToLower(actionRun.Status) {
		case "queued", "running", "pending", "in_progress":
			activeCount++
		}
		if actionRun.SyncedAt.Valid {
			syncedCount++
			latestSyncedAt = maxNullTimeAny(latestSyncedAt, actionRun.SyncedAt.Time)
		}
		if actionRun.UpdatedAt.Valid {
			latestUpdatedAt = maxNullTimeAny(latestUpdatedAt, actionRun.UpdatedAt.Time)
		}
	}
	status := strings.ToLower(cleanPreviewString(run["status"]))
	tagObserved := status == "completed" || status == "succeeded" || status == "success"
	linkCount := 0
	if tagObserved && targetSHA != "" {
		linkCount = total
	}
	evidence["github_actions_total"] = total
	evidence["github_actions_success"] = successCount
	evidence["github_actions_failure"] = failureCount
	evidence["github_actions_active"] = activeCount
	evidence["github_actions_synced"] = syncedCount
	evidence["github_action_run_link_count"] = linkCount
	evidence["latest_synced_at"] = latestSyncedAt
	evidence["latest_updated_at"] = latestUpdatedAt
	evidence["github_actions_refresh_evidence_found"] = total > 0 && syncedCount > 0
	return evidence, nil
}

func recordAssetStatusSnapshotIfChanged(ctx context.Context, db *gorm.DB, assetID, status, health, summary string, raw map[string]any) (int, error) {
	if db == nil {
		return 0, fmt.Errorf("gorm database is not configured")
	}
	var snapshots []GormAssetStatusSnapshot
	if err := db.WithContext(ctx).Where(&GormAssetStatusSnapshot{AssetID: assetID}).Find(&snapshots).Error; err != nil {
		return 0, err
	}
	var latest *GormAssetStatusSnapshot
	for i := range snapshots {
		if latest == nil || snapshots[i].CollectedAt.After(latest.CollectedAt) {
			latest = &snapshots[i]
		}
	}
	if latest != nil && latest.Status == status && latest.Health == health && jsonValuesEqual(latest.Raw.Data, raw) {
		return 0, nil
	}
	snapshot := GormAssetStatusSnapshot{AssetID: assetID, Status: status, Health: health, Summary: summary, Raw: JSONValue{Data: raw}, CollectedAt: time.Now().UTC()}
	if err := db.WithContext(ctx).Create(&snapshot).Error; err != nil {
		return 0, err
	}
	return 1, nil
}

func maxNullTimeAny(current any, candidate time.Time) any {
	if existing, ok := current.(time.Time); ok && existing.After(candidate) {
		return existing
	}
	return candidate
}

func errorsIsRecordNotFound(err error) bool {
	return err == gorm.ErrRecordNotFound
}

func repoTagRunResultSnapshotPayload(run, plan map[string]any, assetObserved bool) map[string]any {
	resultPlan := mapFromAny(plan["result_recording_plan"])
	resultEvidence := mapFromAny(resultPlan["tag_result_evidence"])
	lookupPreflight := mapFromAny(plan["live_remote_lookup_preflight"])
	liveResultPlan := mapFromAny(plan["live_result_plan"])
	actionsPlan := mapFromAny(plan["actions_refresh_plan"])
	return map[string]any{
		"mode":                             "repo_tag_run_result_snapshot",
		"repo_tag_run_id":                  cleanOptionalID(fmt.Sprint(run["id"])),
		"project_id":                       cleanOptionalID(fmt.Sprint(run["project_id"])),
		"project_git_repository_id":        cleanOptionalID(fmt.Sprint(run["project_git_repository_id"])),
		"target_remote_bound":              boolOnlyFromAny(plan["target_remote_bound"]),
		"tag_name_configured":              boolOnlyFromAny(plan["tag_name_configured"]),
		"target_sha_configured":            boolOnlyFromAny(plan["target_sha_configured"]),
		"tag_run_status":                   cleanPreviewString(plan["tag_run_status"]),
		"rehearsal_state":                  cleanPreviewString(plan["rehearsal_state"]),
		"result_recording_state":           cleanPreviewString(resultPlan["result_recording_state"]),
		"result_recording_ready":           boolOnlyFromAny(resultPlan["result_recording_ready"]),
		"result_recording_ready_reason":    cleanPreviewString(resultPlan["result_recording_ready_reason"]),
		"tag_result_evidence_state":        cleanPreviewString(resultEvidence["evidence_state"]),
		"live_remote_tag_success_observed": boolOnlyFromAny(plan["live_remote_tag_success_observed"]),
		"live_remote_tag_failed_observed":  boolOnlyFromAny(plan["live_remote_tag_failed_observed"]),
		"operation_run_bound":              boolOnlyFromAny(resultEvidence["operation_run_bound"]),
		"repo_tag_run_asset_observed":      assetObserved,
		"live_remote_lookup_state":         cleanPreviewString(lookupPreflight["lookup_state"]),
		"live_result_state":                cleanPreviewString(liveResultPlan["live_result_state"]),
		"github_actions_refresh_state":     cleanPreviewString(actionsPlan["refresh_state"]),
		"remote_tag_lookup_performed":      false,
		"git_ls_remote_performed":          false,
		"provider_api_called":              false,
		"github_actions_refresh_performed": false,
		"repo_tag_run_updated":             false,
		"operation_log_written":            false,
		"external_call_made":               false,
		"raw_git_output_recorded":          false,
		"raw_provider_response_recorded":   false,
		"tag_name_included":                false,
		"target_sha_included":              false,
		"remote_url_included":              false,
		"secret_included":                  false,
		"contains_token":                   false,
		"contains_remote_url":              false,
		"contains_ref_name":                false,
		"contains_tag_message":             false,
		"suppressed_fields":                []string{"tag_name", "target_sha", "tag_message", "remote_url", "git_credentials", "provider_token", "authorization_header", "git_output", "github_actions_response", "provider_response_body", "provider_response_headers"},
	}
}

func repoTagRunActionsRefreshSnapshotPayload(run, evidence map[string]any, assetObserved bool) map[string]any {
	status := strings.ToLower(cleanPreviewString(run["status"]))
	tagObserved := status == "completed" || status == "succeeded" || status == "success"
	tagFailed := status == "failed" || status == "error" || status == "canceled" || status == "cancelled"
	tagWaiting := status == "" || status == "queued" || status == "running" || status == "pending" || status == "in_progress"
	evidenceFound := boolOnlyFromAny(evidence["github_actions_refresh_evidence_found"])
	ready := tagObserved && evidenceFound && assetObserved
	state := "blocked"
	if ready {
		state = "recorded"
	} else if tagFailed {
		state = "failed"
	} else if tagWaiting {
		state = "waiting_for_tag_completion"
	} else if tagObserved {
		state = "waiting_for_actions_refresh"
	}
	missing := []string{}
	if !assetObserved {
		missing = append(missing, "repo_tag_run_asset_missing")
	}
	if !tagObserved {
		missing = append(missing, "live_remote_tag_success_not_observed")
	}
	if !boolOnlyFromAny(evidence["target_remote_bound"]) {
		missing = append(missing, "target_remote_missing")
	}
	if !evidenceFound {
		missing = append(missing, "github_actions_refresh_evidence_missing")
	}
	return map[string]any{
		"mode":                                  "repo_tag_run_actions_refresh_snapshot",
		"repo_tag_run_id":                       cleanOptionalID(fmt.Sprint(run["id"])),
		"project_id":                            cleanOptionalID(fmt.Sprint(run["project_id"])),
		"project_git_repository_id":             cleanOptionalID(fmt.Sprint(run["project_git_repository_id"])),
		"repo_tag_run_asset_observed":           assetObserved,
		"tag_run_status":                        status,
		"live_remote_tag_success_observed":      tagObserved,
		"live_remote_tag_failed_observed":       tagFailed,
		"actions_refresh_recording_state":       state,
		"actions_refresh_recording_ready":       ready,
		"target_remote_bound":                   boolOnlyFromAny(evidence["target_remote_bound"]),
		"target_sha_configured":                 boolOnlyFromAny(evidence["target_sha_configured"]),
		"evidence_scope":                        cleanPreviewString(evidence["evidence_scope"]),
		"github_actions_refresh_evidence_found": evidenceFound,
		"github_actions_total":                  intFromAny(evidence["github_actions_total"], 0),
		"github_actions_success":                intFromAny(evidence["github_actions_success"], 0),
		"github_actions_failure":                intFromAny(evidence["github_actions_failure"], 0),
		"github_actions_active":                 intFromAny(evidence["github_actions_active"], 0),
		"github_actions_synced":                 intFromAny(evidence["github_actions_synced"], 0),
		"github_action_run_link_count":          intFromAny(evidence["github_action_run_link_count"], 0),
		"latest_synced_at":                      evidence["latest_synced_at"],
		"latest_updated_at":                     evidence["latest_updated_at"],
		"missing_evidence":                      missing,
		"github_actions_refresh_performed":      false,
		"provider_api_called":                   false,
		"external_call_made":                    false,
		"operation_log_written":                 false,
		"repo_tag_run_link_written":             tagObserved && evidenceFound && intFromAny(evidence["github_action_run_link_count"], 0) > 0,
		"repo_tag_run_link_source":              "canonical_asset_relation",
		"raw_provider_response_recorded":        false,
		"contains_token":                        false,
		"contains_remote_url":                   false,
		"contains_ref_name":                     false,
		"contains_commit_sha":                   false,
		"contains_workflow_name":                false,
		"contains_html_url":                     false,
		"tag_name_included":                     false,
		"target_sha_included":                   false,
		"sanitized_metadata_only":               true,
		"suppressed_fields":                     []string{"tag_name", "target_sha", "branch", "commit_sha", "workflow_name", "html_url", "remote_url", "provider_token", "authorization_header", "github_actions_response", "provider_response_body", "provider_response_headers"},
	}
}

package app

import (
	"fmt"
)

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

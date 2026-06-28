package app

import (
	"fmt"
	"strings"
)

func projectVersionValidationRerunOperationSummary(operations []map[string]any) map[string]any {
	queued, running, completed, failed, canceled := 0, 0, 0, 0, 0
	items := make([]map[string]any, 0, len(operations))
	latestState := "not_requested"
	snapshotWritten := false
	for _, operation := range operations {
		status := cleanPreviewString(operation["status"])
		if status == "" {
			status = "unknown"
		}
		switch status {
		case "queued":
			queued++
		case "running":
			running++
		case "completed":
			completed++
		case "failed":
			failed++
		case "canceled":
			canceled++
		}
		result := mapFromAny(operation["result"])
		operationResult := mapFromAny(result["operation_result"])
		validationSnapshotWritten := boolOnlyFromAny(result["validation_snapshot_written"]) ||
			boolOnlyFromAny(operationResult["validation_snapshot_written"])
		if validationSnapshotWritten {
			snapshotWritten = true
		}
		item := map[string]any{
			"operation_run_id":               operation["id"],
			"status":                         status,
			"created_at":                     operation["created_at"],
			"updated_at":                     operation["updated_at"],
			"started_at":                     operation["started_at"],
			"finished_at":                    operation["finished_at"],
			"validation_snapshot_written":    validationSnapshotWritten,
			"recording_state":                firstNonEmptyString(cleanPreviewString(result["recording_state"]), cleanPreviewString(operationResult["recording_state"])),
			"raw_response_included":          false,
			"secret_included":                false,
			"external_call_made":             false,
			"provider_api_called":            false,
			"raw_provider_response_recorded": false,
		}
		if status == "failed" {
			item["error_recorded"] = cleanOptionalText(fmt.Sprint(operation["error"])) != ""
		}
		items = append(items, item)
	}
	activeCount := queued + running
	operationCount := len(operations)
	switch {
	case operationCount == 0:
		latestState = "not_requested"
	case activeCount > 0:
		latestState = "waiting_for_worker"
	case failed > 0:
		latestState = "failed"
	case canceled > 0:
		latestState = "canceled"
	default:
		latestState = "recorded"
	}
	return map[string]any{
		"mode":                        "project_version_background_validation_rerun_summary",
		"operation_count":             operationCount,
		"queued_count":                queued,
		"running_count":               running,
		"completed_count":             completed,
		"failed_count":                failed,
		"canceled_count":              canceled,
		"active_count":                activeCount,
		"terminal_count":              completed + failed + canceled,
		"background_rerun_state":      latestState,
		"background_worker_enqueued":  operationCount > 0,
		"automatic_background_rerun":  operationCount > 0,
		"validation_snapshot_written": snapshotWritten,
		"raw_response_included":       false,
		"secret_included":             false,
		"external_call_made":          false,
		"provider_api_called":         false,
		"suppressed_fields":           []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body"},
		"items":                       items,
	}
}

func projectVersionBackgroundValidationRerunPlan(summary map[string]any, rerunEvidence map[string]any, backgroundSummaries ...map[string]any) map[string]any {
	rerunStatus := strings.TrimSpace(fmt.Sprint(summary["validation_rerun_status"]))
	if rerunStatus == "" || rerunStatus == "<nil>" {
		rerunStatus = "not_requested"
	}
	backgroundSummary := map[string]any{}
	if len(backgroundSummaries) > 0 {
		backgroundSummary = backgroundSummaries[0]
	}
	operationCount := intFromAny(summary["operation_count"], 0)
	activeCount := intFromAny(summary["active_count"], 0)
	terminal := operationCount > 0 && activeCount == 0
	backgroundOperationCount := intFromAny(backgroundSummary["operation_count"], 0)
	backgroundActiveCount := intFromAny(backgroundSummary["active_count"], 0)
	backgroundState := cleanPreviewString(backgroundSummary["background_rerun_state"])
	backgroundSnapshotWritten := boolOnlyFromAny(backgroundSummary["validation_snapshot_written"])
	snapshotWritePlan := projectVersionValidationSnapshotWritePlan(summary, rerunEvidence, terminal)
	planState := "blocked"
	blockedReasons := []string{"provider_refresh_execution_not_performed", "background_validation_rerun_disabled"}
	switch rerunStatus {
	case "waiting_for_workers":
		planState = "waiting_for_workers"
		blockedReasons = []string{"refresh_workers_still_running", "background_validation_rerun_disabled"}
	case "recorded":
		planState = "ready_for_operator_review"
		blockedReasons = []string{"background_validation_rerun_disabled", "validation_snapshot_write_disabled"}
	case "refresh_failed":
		planState = "blocked"
		blockedReasons = []string{"refresh_worker_failed", "background_validation_rerun_disabled"}
	case "refresh_canceled":
		planState = "blocked"
		blockedReasons = []string{"refresh_worker_canceled", "background_validation_rerun_disabled"}
	}
	if backgroundOperationCount > 0 {
		switch backgroundState {
		case "waiting_for_worker":
			planState = "waiting_for_worker"
			blockedReasons = []string{"background_validation_worker_running"}
		case "recorded":
			planState = "recorded"
			blockedReasons = []string{}
		case "failed":
			planState = "failed"
			blockedReasons = []string{"background_validation_worker_failed"}
		case "canceled":
			planState = "canceled"
			blockedReasons = []string{"background_validation_worker_canceled"}
		}
	}
	return map[string]any{
		"mode":                                    "project_version_background_validation_rerun_plan",
		"plan_state":                              planState,
		"rerun_status":                            rerunStatus,
		"background_rerun_ready_for_review":       rerunStatus == "recorded" && terminal,
		"automatic_background_rerun":              backgroundOperationCount > 0,
		"background_worker_enqueued":              backgroundOperationCount > 0,
		"standalone_background_worker_enabled":    true,
		"background_worker_active":                backgroundActiveCount > 0,
		"background_rerun_state":                  backgroundState,
		"background_validation_rerun_summary":     backgroundSummary,
		"control_worker_auto_snapshot_supported":  true,
		"control_worker_auto_snapshot_ready":      rerunStatus == "recorded" && terminal,
		"control_worker_auto_snapshot_trigger":    "refresh_worker_completion",
		"validation_snapshot_written":             backgroundSnapshotWritten,
		"validation_snapshot_write_plan":          snapshotWritePlan,
		"validation_rerun_recorded":               boolOnlyFromAny(summary["validation_rerun_recorded"]),
		"server_side_validation_recheck_observed": boolOnlyFromAny(rerunEvidence["server_side_validation_recheck"]),
		"server_side_validation_recheck_ready":    boolOnlyFromAny(rerunEvidence["server_side_validation_recheck_ready"]),
		"provider_refresh_operation_observed":     operationCount > 0,
		"provider_refresh_terminal":               terminal,
		"operation_count":                         operationCount,
		"active_count":                            activeCount,
		"external_call_made":                      false,
		"provider_api_called":                     false,
		"git_fetch_performed":                     false,
		"argocd_api_called":                       false,
		"raw_response_included":                   false,
		"secret_included":                         false,
		"required_controls": []string{
			"terminal_refresh_workers",
			"server_side_validation_recheck",
			"validation_snapshot_write_audit",
			"control_worker_auto_snapshot_review",
			"standalone_background_worker_policy_review",
		},
		"rerun_sequence": []string{
			"observe_terminal_refresh_workers",
			"rerun_validation_against_synced_state",
			"record_validation_snapshot",
			"control_worker_auto_record_validation_snapshot",
			"publish_background_rerun_result",
		},
		"disabled_backends": []string{"raw_provider_response_recording"},
		"suppressed_fields": []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body"},
		"blocked_reasons":   blockedReasons,
		"message":           "Standalone background validation rerun can now enqueue a control-worker job that rereads local synced state and records a sanitized ProjectVersion validation snapshot without provider calls.",
	}
}

func projectVersionValidationSnapshotWritePlan(summary map[string]any, rerunEvidence map[string]any, terminal bool) map[string]any {
	rerunStatus := strings.TrimSpace(fmt.Sprint(summary["validation_rerun_status"]))
	if rerunStatus == "" || rerunStatus == "<nil>" {
		rerunStatus = "not_requested"
	}
	snapshotState := "blocked"
	if rerunStatus == "waiting_for_workers" {
		snapshotState = "waiting_for_workers"
	} else if rerunStatus == "recorded" && terminal {
		snapshotState = "metadata_review_ready"
	}
	reviewReady := snapshotState == "metadata_review_ready" &&
		boolOnlyFromAny(summary["validation_rerun_recorded"]) &&
		boolOnlyFromAny(rerunEvidence["server_side_validation_recheck_ready"])
	blockedReasons := []string{"validation_snapshot_write_disabled"}
	if rerunStatus == "not_requested" {
		blockedReasons = append(blockedReasons, "provider_refresh_execution_not_performed")
	}
	if rerunStatus == "waiting_for_workers" {
		blockedReasons = append(blockedReasons, "refresh_workers_still_running")
	}
	if rerunStatus == "refresh_failed" {
		blockedReasons = append(blockedReasons, "refresh_worker_failed")
	}
	if rerunStatus == "refresh_canceled" {
		blockedReasons = append(blockedReasons, "refresh_worker_canceled")
	}
	if !boolOnlyFromAny(rerunEvidence["server_side_validation_recheck_ready"]) {
		blockedReasons = append(blockedReasons, "server_side_validation_recheck_not_terminal")
	}
	if !boolOnlyFromAny(summary["validation_rerun_recorded"]) {
		blockedReasons = append(blockedReasons, "validation_rerun_not_recorded")
	}
	return map[string]any{
		"mode":                                    "project_version_validation_snapshot_write_plan",
		"snapshot_state":                          snapshotState,
		"snapshot_ready_for_review":               reviewReady,
		"snapshot_write_enabled":                  false,
		"validation_snapshot_written":             false,
		"asset_status_snapshot_written":           false,
		"operation_log_written":                   false,
		"background_worker_enqueued":              false,
		"automatic_background_rerun":              false,
		"standalone_background_worker_enabled":    true,
		"control_worker_auto_snapshot_supported":  true,
		"control_worker_auto_snapshot_ready":      reviewReady,
		"server_side_validation_recheck_observed": boolOnlyFromAny(rerunEvidence["server_side_validation_recheck"]),
		"server_side_validation_recheck_ready":    boolOnlyFromAny(rerunEvidence["server_side_validation_recheck_ready"]),
		"validation_rerun_recorded":               boolOnlyFromAny(summary["validation_rerun_recorded"]),
		"provider_refresh_terminal":               terminal,
		"provider_refresh_status":                 rerunStatus,
		"operation_count":                         intFromAny(summary["operation_count"], 0),
		"active_count":                            intFromAny(summary["active_count"], 0),
		"repository_count":                        intFromAny(rerunEvidence["repository_count"], 0),
		"ready_count":                             intFromAny(rerunEvidence["ready_count"], 0),
		"partial_count":                           intFromAny(rerunEvidence["partial_count"], 0),
		"blocked_count":                           intFromAny(rerunEvidence["blocked_count"], 0),
		"validation_state":                        rerunEvidence["validation_state"],
		"validation_source":                       "local_synced_database_state",
		"external_call_made":                      false,
		"provider_api_called":                     false,
		"git_fetch_performed":                     false,
		"argocd_api_called":                       false,
		"raw_response_included":                   false,
		"secret_included":                         false,
		"required_snapshot_fields":                []string{"project_version_id", "validation_state", "repository_count", "ready_count", "partial_count", "blocked_count", "provider_refresh_status", "operation_count", "server_side_validation_recheck_status"},
		"required_controls":                       []string{"terminal_refresh_workers", "server_side_validation_recheck", "snapshot_schema_review", "snapshot_operator_review", "asset_status_snapshot_audit", "operation_log_redaction_review"},
		"disabled_backends":                       []string{"operation_log_write", "raw_provider_response_recording"},
		"suppressed_fields":                       []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body", "repository_ref"},
		"blocked_reasons":                         blockedReasons,
		"message":                                 "Metadata-only validation snapshot write preflight; standalone background workers may record asset status snapshots, but no operation log raw output, raw provider response, Git output, Argo response, URL, credential, or workflow log is written.",
	}
}

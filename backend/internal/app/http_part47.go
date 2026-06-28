package app

import (
	"fmt"
	"strings"
)

func attachProjectVersionRefreshResultSummary(refreshPlan map[string]any, summary map[string]any, rerunEvidence map[string]any) {
	if refreshPlan == nil || summary == nil {
		return
	}
	refreshPlan["result_summary"] = summary
	refreshPlan["validation_rerun_evidence"] = rerunEvidence
	executionPlan := mapFromAny(refreshPlan["execution_plan"])
	if len(executionPlan) == 0 {
		return
	}
	plannedKinds := stringSliceFromAny(executionPlan["planned_refresh_kinds"])
	workerBindingEvidence := projectVersionRefreshWorkerResultBindingEvidence(summary, plannedKinds)
	refreshPlan["worker_result_binding_evidence"] = workerBindingEvidence
	operationCount := intFromAny(summary["operation_count"], 0)
	validationRecorded := summary["validation_rerun_recorded"] == true
	executionPlan["operation_enqueued"] = operationCount > 0
	executionPlan["worker_job_created"] = operationCount > 0
	executionPlan["validation_reopened"] = validationRecorded
	executionPlan["refresh_result_summary"] = summary
	executionPlan["worker_result_binding_evidence"] = workerBindingEvidence
	executionPlan["worker_result_binding_state"] = workerBindingEvidence["binding_state"]
	executionPlan["server_side_validation_recheck_observed"] = boolOnlyFromAny(rerunEvidence["server_side_validation_recheck"])
	executionPlan["automatic_background_rerun"] = boolOnlyFromAny(rerunEvidence["automatic_background_rerun"])
	executionPlan["validation_rerun_evidence"] = rerunEvidence
	if resultPlan := mapFromAny(executionPlan["result_recording_plan"]); len(resultPlan) > 0 {
		terminalRefresh := operationCount > 0 && intFromAny(summary["active_count"], 0) == 0
		bindingState := cleanPreviewString(workerBindingEvidence["binding_state"])
		resultState := projectVersionRefreshResultRecordingState(summary)
		resultReason := projectVersionRefreshResultRecordingReason(summary)
		resultReady := terminalRefresh
		if resultState == "recorded" && len(plannedKinds) > 0 && bindingState != "recorded" {
			resultState = "partial_recorded"
			resultReason = "planned_refresh_result_missing"
			resultReady = false
		}
		resultPlan["result_recording_state"] = resultState
		resultPlan["result_recording_ready"] = resultReady
		resultPlan["result_recording_ready_reason"] = resultReason
		resultPlan["recording_enabled"] = resultReady
		resultPlan["result_written"] = resultReady
		resultPlan["operation_log_written"] = resultReady
		resultPlan["canonical_asset_sync_queued"] = resultReady
		resultPlan["status_snapshot_write_eligible"] = resultReady
		resultPlan["status_snapshot_written"] = resultPlan["status_snapshot_write_eligible"]
		resultPlan["validation_rerun_recorded"] = validationRecorded
		resultPlan["git_ref_fetch_result_recorded"] = projectVersionRefreshKindTerminalObserved(summary, "git_ref_fetch")
		resultPlan["github_actions_result_recorded"] = projectVersionRefreshKindTerminalObserved(summary, "github_actions_api_refresh")
		resultPlan["argo_revision_result_recorded"] = projectVersionRefreshKindTerminalObserved(summary, "argocd_app_refresh")
		resultPlan["refresh_result_summary"] = summary
		resultPlan["worker_result_binding_evidence"] = workerBindingEvidence
		resultPlan["worker_result_binding_state"] = workerBindingEvidence["binding_state"]
		resultPlan["server_side_validation_recheck_observed"] = boolOnlyFromAny(rerunEvidence["server_side_validation_recheck"])
		resultPlan["automatic_background_rerun"] = boolOnlyFromAny(rerunEvidence["automatic_background_rerun"])
		resultPlan["validation_rerun_evidence"] = rerunEvidence
		resultPlan["blocked_reasons"] = projectVersionRefreshResultBlockedReasons(summary, workerBindingEvidence)
	}
}

func attachProjectVersionBackgroundValidationRerunPlan(refreshPlan map[string]any, backgroundPlan map[string]any) {
	if refreshPlan == nil || backgroundPlan == nil {
		return
	}
	refreshPlan["background_validation_rerun_plan"] = backgroundPlan
	executionPlan := mapFromAny(refreshPlan["execution_plan"])
	if len(executionPlan) == 0 {
		return
	}
	executionPlan["background_validation_rerun_plan"] = backgroundPlan
	executionPlan["background_validation_rerun_state"] = backgroundPlan["plan_state"]
	if resultPlan := mapFromAny(executionPlan["result_recording_plan"]); len(resultPlan) > 0 {
		resultPlan["background_validation_rerun_plan"] = backgroundPlan
		resultPlan["background_validation_rerun_state"] = backgroundPlan["plan_state"]
		resultPlan["background_validation_rerun_ready_for_review"] = backgroundPlan["background_rerun_ready_for_review"]
	}
}

func projectVersionRefreshWorkerResultBindingEvidence(summary map[string]any, plannedKinds []string) map[string]any {
	operationCount := intFromAny(summary["operation_count"], 0)
	activeCount := intFromAny(summary["active_count"], 0)
	failedCount := intFromAny(summary["failed_count"], 0)
	canceledCount := intFromAny(summary["canceled_count"], 0)
	observedKinds := stringSliceFromAny(summary["refresh_kinds"])
	missingKinds := []string{}
	for _, kind := range plannedKinds {
		if strings.TrimSpace(kind) == "" {
			continue
		}
		if !stringInSlice(observedKinds, kind) {
			missingKinds = append(missingKinds, kind)
		}
	}
	allPlannedObserved := len(plannedKinds) > 0 && len(missingKinds) == 0
	bindingState := "not_recorded"
	switch {
	case operationCount == 0:
		bindingState = "not_recorded"
	case activeCount > 0:
		bindingState = "waiting_for_workers"
	case failedCount > 0:
		bindingState = "failed"
	case canceledCount > 0:
		bindingState = "canceled"
	case allPlannedObserved:
		bindingState = "recorded"
	default:
		bindingState = "partial_recorded"
	}
	return map[string]any{
		"mode":                            "project_version_refresh_worker_result_binding_evidence",
		"binding_state":                   bindingState,
		"project_version_scope_bound":     operationCount > 0,
		"operation_result_bound":          operationCount > 0,
		"worker_result_observed":          operationCount > 0,
		"terminal_worker_result_observed": operationCount > 0 && activeCount == 0,
		"all_planned_results_observed":    allPlannedObserved,
		"planned_refresh_kinds":           plannedKinds,
		"observed_refresh_kinds":          observedKinds,
		"missing_planned_result_kinds":    missingKinds,
		"operation_count":                 operationCount,
		"active_count":                    activeCount,
		"terminal_count":                  intFromAny(summary["terminal_count"], 0),
		"failed_count":                    failedCount,
		"canceled_count":                  canceledCount,
		"validation_rerun_status":         summary["validation_rerun_status"],
		"validation_rerun_recorded":       boolOnlyFromAny(summary["validation_rerun_recorded"]),
		"external_call_made":              false,
		"provider_api_called":             false,
		"git_fetch_performed":             false,
		"argocd_api_called":               false,
		"raw_response_included":           false,
		"raw_git_output_included":         false,
		"raw_argo_response_included":      false,
		"secret_included":                 false,
		"contains_remote_url":             false,
		"contains_provider_token":         false,
		"contains_provider_response":      false,
		"suppressed_fields":               []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body", "operation_error_detail"},
		"message":                         "ProjectVersion refresh worker result binding is reconciled from operation kind/status metadata only; provider responses, Git output, Argo responses, URLs, credentials, and workflow logs remain suppressed.",
	}
}

func projectVersionRefreshResultRecordingState(summary map[string]any) string {
	switch strings.TrimSpace(fmt.Sprint(summary["validation_rerun_status"])) {
	case "recorded":
		return "recorded"
	case "waiting_for_workers":
		return "waiting"
	case "refresh_failed":
		return "failed"
	case "refresh_canceled":
		return "canceled"
	default:
		return "blocked"
	}
}

func projectVersionRefreshResultRecordingReason(summary map[string]any) string {
	switch strings.TrimSpace(fmt.Sprint(summary["validation_rerun_status"])) {
	case "recorded":
		return "validation_rerun_recorded"
	case "waiting_for_workers":
		return "refresh_workers_still_running"
	case "refresh_failed":
		return "refresh_worker_failed"
	case "refresh_canceled":
		return "refresh_worker_canceled"
	default:
		return "provider_refresh_execution_not_performed"
	}
}

func projectVersionRefreshKindTerminalObserved(summary map[string]any, kind string) bool {
	counts := mapFromAny(mapFromAny(summary["status_counts_by_kind"])[kind])
	total := 0
	for _, status := range []string{"queued", "running", "completed", "failed", "canceled"} {
		total += intFromAny(counts[status], 0)
	}
	if total == 0 {
		return false
	}
	return intFromAny(counts["queued"], 0) == 0 && intFromAny(counts["running"], 0) == 0
}

func projectVersionRefreshResultBlockedReasons(summary map[string]any, workerBindingEvidence map[string]any) []string {
	switch strings.TrimSpace(fmt.Sprint(summary["validation_rerun_status"])) {
	case "recorded":
		if len(workerBindingEvidence) > 0 {
			missingKinds := stringSliceFromAny(workerBindingEvidence["missing_planned_result_kinds"])
			if len(stringSliceFromAny(workerBindingEvidence["planned_refresh_kinds"])) == 0 || len(missingKinds) == 0 {
				return []string{}
			}
			bindingState := cleanPreviewString(workerBindingEvidence["binding_state"])
			if bindingState != "" && bindingState != "recorded" {
				reasons := []string{"planned_refresh_result_missing"}
				for _, kind := range missingKinds {
					if strings.TrimSpace(kind) != "" {
						reasons = append(reasons, "missing_"+kind)
					}
				}
				return reasons
			}
		}
		return []string{}
	case "waiting_for_workers":
		return []string{"refresh_workers_still_running"}
	case "refresh_failed":
		return []string{"refresh_worker_failed"}
	case "refresh_canceled":
		return []string{"refresh_worker_canceled"}
	default:
		return []string{"provider_refresh_execution_not_performed", "synced_state_write_not_performed", "validation_auto_reload_not_observed"}
	}
}

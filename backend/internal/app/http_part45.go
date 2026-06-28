package app

import (
	"fmt"
	"strings"
)

func projectVersionValidationPreview(version map[string]any, remotes, tagRuns, actionRuns, argoApps []map[string]any, argoConnections ...[]map[string]any) map[string]any {
	metadata := mapFromAny(version["metadata"])
	repositories := mapSliceFromAny(metadata["repositories"])
	items := make([]map[string]any, 0, len(repositories))
	ready, partial, blocked := 0, 0, 0
	for index, repoItem := range repositories {
		item := projectVersionValidationItem(index, repoItem, remotes, tagRuns, actionRuns, argoApps)
		switch item["status"] {
		case "ready":
			ready++
		case "partial":
			partial++
		default:
			blocked++
		}
		items = append(items, item)
	}
	var argoConnectionRows []map[string]any
	if len(argoConnections) > 0 {
		argoConnectionRows = argoConnections[0]
	}
	var refreshOperationRows []map[string]any
	if len(argoConnections) > 1 {
		refreshOperationRows = argoConnections[1]
	}
	var backgroundOperationRows []map[string]any
	if len(argoConnections) > 2 {
		backgroundOperationRows = argoConnections[2]
	}
	refreshSummary := projectVersionRefreshResultSummary(refreshOperationRows)
	backgroundSummary := projectVersionValidationRerunOperationSummary(backgroundOperationRows)
	refreshPlan := projectVersionProviderRefreshPlan(repositories, remotes, argoConnectionRows)
	overall := "blocked"
	switch {
	case len(items) > 0 && blocked == 0 && partial == 0:
		overall = "ready"
	case ready > 0 || partial > 0:
		overall = "partial"
	}
	rerunEvidence := projectVersionValidationRerunEvidence(refreshSummary, overall, len(items), ready, partial, blocked)
	backgroundRerunPlan := projectVersionBackgroundValidationRerunPlan(refreshSummary, rerunEvidence, backgroundSummary)
	attachProjectVersionRefreshResultSummary(refreshPlan, refreshSummary, rerunEvidence)
	attachProjectVersionBackgroundValidationRerunPlan(refreshPlan, backgroundRerunPlan)
	return map[string]any{
		"version_id":                          version["id"],
		"version":                             version["version"],
		"mode":                                "synced_state_validation_preview",
		"validation_state":                    overall,
		"external_call_made":                  false,
		"provider_api_called":                 false,
		"git_fetch_performed":                 false,
		"argocd_api_called":                   false,
		"validation_source":                   "local_synced_database_state",
		"repository_count":                    len(items),
		"ready_count":                         ready,
		"partial_count":                       partial,
		"blocked_count":                       blocked,
		"items":                               items,
		"provider_refresh_plan":               refreshPlan,
		"provider_refresh_result_summary":     refreshSummary,
		"background_validation_rerun_summary": backgroundSummary,
		"validation_rerun_evidence":           rerunEvidence,
		"background_validation_rerun_plan":    backgroundRerunPlan,
		"required_live_rehearsal":             stringSliceFromAny(refreshPlan["required_live_rehearsal"]),
	}
}

func projectVersionRefreshResultSummary(operations []map[string]any) map[string]any {
	statusCounts := map[string]any{}
	kindCounts := map[string]any{}
	kinds := []string{}
	items := make([]map[string]any, 0, len(operations))
	queued, running, completed, failed, canceled := 0, 0, 0, 0, 0
	for _, operation := range operations {
		status := strings.TrimSpace(fmt.Sprint(operation["status"]))
		if status == "" || status == "<nil>" {
			status = "unknown"
		}
		input := mapFromAny(operation["input"])
		refreshKind := strings.TrimSpace(fmt.Sprint(input["refresh_kind"]))
		if refreshKind == "" || refreshKind == "<nil>" {
			refreshKind = strings.TrimSpace(fmt.Sprint(operation["operation_type"]))
		}
		if refreshKind == "" || refreshKind == "<nil>" {
			refreshKind = "unknown"
		}
		if !stringInSlice(kinds, refreshKind) {
			kinds = append(kinds, refreshKind)
		}
		perKindCounts := mapFromAny(kindCounts[refreshKind])
		if len(perKindCounts) == 0 {
			perKindCounts = map[string]any{}
			kindCounts[refreshKind] = perKindCounts
		}
		statusCounts[status] = intFromAny(statusCounts[status], 0) + 1
		perKindCounts[status] = intFromAny(perKindCounts[status], 0) + 1
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
		item := map[string]any{
			"operation_run_id":      operation["id"],
			"operation_type":        operation["operation_type"],
			"refresh_kind":          refreshKind,
			"status":                status,
			"started_at":            operation["started_at"],
			"finished_at":           operation["finished_at"],
			"created_at":            operation["created_at"],
			"updated_at":            operation["updated_at"],
			"raw_response_included": false,
			"secret_included":       false,
		}
		if status == "failed" {
			item["error_recorded"] = cleanOptionalText(fmt.Sprint(operation["error"])) != ""
		}
		items = append(items, item)
	}
	operationCount := len(operations)
	terminalCount := completed + failed + canceled
	activeCount := queued + running
	rerunStatus := "not_requested"
	if operationCount > 0 {
		rerunStatus = "waiting_for_workers"
		if activeCount == 0 {
			if failed > 0 {
				rerunStatus = "refresh_failed"
			} else if canceled > 0 {
				rerunStatus = "refresh_canceled"
			} else {
				rerunStatus = "recorded"
			}
		}
	}
	return map[string]any{
		"mode":                      "project_version_refresh_result_summary",
		"operation_count":           operationCount,
		"queued_count":              queued,
		"running_count":             running,
		"completed_count":           completed,
		"failed_count":              failed,
		"canceled_count":            canceled,
		"active_count":              activeCount,
		"terminal_count":            terminalCount,
		"validation_rerun_status":   rerunStatus,
		"validation_rerun_recorded": rerunStatus == "recorded",
		"has_refresh_failures":      failed > 0,
		"has_refresh_cancellations": canceled > 0,
		"refresh_kinds":             kinds,
		"status_counts":             statusCounts,
		"status_counts_by_kind":     kindCounts,
		"items":                     items,
		"raw_response_included":     false,
		"secret_included":           false,
		"suppressed_fields":         []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body"},
	}
}

func projectVersionValidationRerunEvidence(summary map[string]any, validationState string, repositoryCount, readyCount, partialCount, blockedCount int) map[string]any {
	rerunStatus := strings.TrimSpace(fmt.Sprint(summary["validation_rerun_status"]))
	operationCount := intFromAny(summary["operation_count"], 0)
	activeCount := intFromAny(summary["active_count"], 0)
	serverSideRecheck := operationCount > 0
	if rerunStatus == "" || rerunStatus == "<nil>" {
		rerunStatus = "not_requested"
	}
	return map[string]any{
		"mode":                                   "project_version_validation_rerun_evidence",
		"rerun_state":                            rerunStatus,
		"rerun_source":                           "validation_preview_request",
		"server_side_validation_recheck":         serverSideRecheck,
		"server_side_validation_recheck_ready":   operationCount > 0 && activeCount == 0,
		"automatic_background_rerun":             false,
		"control_worker_auto_snapshot_supported": true,
		"validation_rerun_recorded":              rerunStatus == "recorded",
		"provider_refresh_operation_observed":    operationCount > 0,
		"provider_refresh_active":                activeCount > 0,
		"provider_refresh_terminal":              operationCount > 0 && activeCount == 0,
		"provider_refresh_status":                rerunStatus,
		"operation_count":                        operationCount,
		"active_count":                           activeCount,
		"validation_state":                       validationState,
		"repository_count":                       repositoryCount,
		"ready_count":                            readyCount,
		"partial_count":                          partialCount,
		"blocked_count":                          blockedCount,
		"validation_source":                      "local_synced_database_state",
		"external_call_made":                     false,
		"provider_api_called":                    false,
		"git_fetch_performed":                    false,
		"argocd_api_called":                      false,
		"raw_response_included":                  false,
		"secret_included":                        false,
		"suppressed_fields":                      []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body"},
		"message":                                "This validation response is a server-side recheck of local synced database state; the control worker can auto-record a sanitized snapshot after terminal refresh workers, and standalone background rerun can enqueue the same local-only snapshot recorder without raw provider response binding.",
	}
}

package app

import (
	"fmt"
	"strings"
)

func repoTagLiveResultPlan(rehearsalState string, tagObserved, tagFailed bool, lookupPreflight map[string]any) map[string]any {
	resultState := "blocked"
	if rehearsalState == "observed" {
		resultState = "planned"
	}
	if tagFailed {
		resultState = "failed"
	}
	return map[string]any{
		"mode":                              "repo_tag_live_result_plan",
		"live_result_state":                 resultState,
		"live_remote_tag_success_observed":  tagObserved,
		"live_remote_tag_failed_observed":   tagFailed,
		"remote_tag_lookup_performed":       false,
		"repo_tag_run_result_write_planned": tagObserved,
		"repo_tag_run_result_written":       false,
		"operation_log_written":             false,
		"external_call_made":                false,
		"contains_token":                    false,
		"contains_remote_url":               false,
		"contains_ref_name":                 false,
		"contains_tag_message":              false,
		"required_controls": []string{
			"remote_tag_lookup",
			"tag_result_classification",
			"repo_tag_run_update",
			"operation_log_summary",
			"sanitized_result_recording",
		},
		"disabled_backends": []string{
			"remote_tag_lookup",
			"repo_tag_run_update",
			"operation_log_write",
		},
		"suppressed_fields": []string{
			"remote_url",
			"git_credentials",
			"provider_token",
			"authorization_header",
			"tag_message",
			"git_output",
		},
		"blocked_reasons":              repoTagLiveResultBlockedReasons(tagObserved, tagFailed),
		"execution_blockers":           []string{"live_remote_tag_result_write_not_performed"},
		"live_remote_lookup_preflight": lookupPreflight,
		"message":                      "Live remote tag result persistence is planned only; no remote lookup, repo_tag_run update, or operation log write is performed.",
	}
}

func repoTagLiveResultBlockedReasons(tagObserved, tagFailed bool) []string {
	if tagObserved {
		return []string{"repo_tag_run_result_update_not_wired"}
	}
	if tagFailed {
		return []string{"live_remote_tag_failed_observed", "repo_tag_run_result_update_not_wired"}
	}
	return []string{"live_remote_tag_success_not_observed", "repo_tag_run_result_update_not_wired"}
}

func repoTagActionsRefreshPlan(rehearsalState string, tagObserved, tagFailed bool, lookupPreflight map[string]any, refreshStatus string, refreshResult map[string]any) map[string]any {
	refreshPerformed := refreshStatus == "completed"
	refreshRunning := refreshStatus == "queued" || refreshStatus == "running"
	refreshFailed := refreshStatus == "failed" || refreshStatus == "timeout" || refreshStatus == "canceled" || refreshStatus == "cancelled"
	syncedCount := intFromAny(refreshResult["count"], 0)
	actionsEvidenceFound := refreshPerformed && syncedCount > 0
	linkWritten := actionsEvidenceFound
	refreshState := "blocked"
	if rehearsalState == "observed" {
		refreshState = "planned"
	}
	if refreshRunning {
		refreshState = "running"
	}
	if refreshPerformed {
		refreshState = "waiting_for_actions_refresh"
	}
	if actionsEvidenceFound {
		refreshState = "recorded"
	}
	if tagFailed {
		refreshState = "failed"
	}
	if refreshFailed {
		refreshState = "failed"
	}
	disabledBackends := []string{"github_action_run_link_write", "provider_response_recording"}
	if !refreshPerformed {
		disabledBackends = append([]string{"github_actions_api_sync"}, disabledBackends...)
	}
	blockedReasons := repoTagActionsRefreshBlockedReasons(tagObserved, tagFailed)
	if refreshRunning {
		blockedReasons = []string{"github_actions_refresh_running"}
	} else if linkWritten {
		blockedReasons = []string{"provider_response_recording_not_performed"}
	} else if refreshPerformed {
		blockedReasons = []string{"github_actions_refresh_evidence_missing", "github_action_run_link_write_not_performed"}
	}
	if refreshFailed {
		blockedReasons = []string{"github_actions_refresh_failed"}
	}
	return map[string]any{
		"mode":                               "repo_tag_github_actions_refresh_plan",
		"refresh_state":                      refreshState,
		"refresh_operation_status":           refreshStatus,
		"refresh_after_tag_success_required": true,
		"live_remote_tag_success_observed":   tagObserved,
		"live_remote_tag_failed_observed":    tagFailed,
		"github_actions_sync_enabled":        tagObserved && !tagFailed && !refreshRunning && !refreshPerformed,
		"github_actions_refresh_performed":   refreshPerformed,
		"github_action_runs_synced":          actionsEvidenceFound,
		"github_action_runs_synced_count":    syncedCount,
		"repo_tag_run_link_written":          linkWritten,
		"repo_tag_run_link_source":           "canonical_asset_relation",
		"repo_tag_run_link_write_mode":       "derived_canonical_relation",
		"external_call_made":                 refreshPerformed,
		"contains_token":                     false,
		"contains_remote_url":                false,
		"contains_provider_response":         false,
		"required_controls": []string{
			"github_actions_remote_review",
			"github_actions_api_sync",
			"action_run_linking",
			"sanitized_refresh_result_recording",
		},
		"disabled_backends": disabledBackends,
		"suppressed_fields": []string{
			"provider_token",
			"authorization_header",
			"remote_url",
			"github_actions_response",
			"provider_response_body",
			"provider_response_headers",
		},
		"blocked_reasons":              blockedReasons,
		"execution_blockers":           repoTagActionsRefreshExecutionBlockers(refreshPerformed, actionsEvidenceFound),
		"live_remote_lookup_preflight": lookupPreflight,
		"message":                      "GitHub Actions refresh after live tag success can enqueue the provider-backed sync worker; raw provider responses, remote URLs, tokens, and workflow logs stay suppressed while matched action-run links are derived through the canonical asset graph.",
	}
}

func repoTagActionsRefreshExecutionBlockers(refreshPerformed, actionsEvidenceFound bool) []string {
	if actionsEvidenceFound {
		return []string{"provider_response_recording_not_performed"}
	}
	if refreshPerformed && !actionsEvidenceFound {
		return []string{"github_actions_refresh_evidence_missing", "github_action_run_link_write_not_performed"}
	}
	return []string{"github_actions_refresh_not_performed"}
}

func repoTagActionsRefreshBlockedReasons(tagObserved, tagFailed bool) []string {
	if tagObserved {
		return []string{"github_actions_refresh_not_performed"}
	}
	if tagFailed {
		return []string{"live_remote_tag_failed_observed", "github_actions_refresh_not_performed"}
	}
	return []string{"live_remote_tag_success_not_observed", "github_actions_refresh_not_performed"}
}

func repoTagRemoteRehearsalResultRecordingPlan(evidence map[string]any, lookupPreflight map[string]any) map[string]any {
	evidenceState := strings.TrimSpace(fmt.Sprint(evidence["evidence_state"]))
	resultRecorded := boolOnlyFromAny(evidence["sanitized_result_recorded"])
	recordingState := "blocked"
	recordingReason := "remote_tag_rehearsal_execution_not_performed"
	switch evidenceState {
	case "recorded":
		recordingState = "recorded"
		recordingReason = "sanitized_remote_tag_success_observed"
	case "failed":
		recordingState = "failed"
		recordingReason = "sanitized_remote_tag_failure_observed"
	case "waiting_for_worker":
		recordingState = "waiting_for_worker"
		recordingReason = "remote_tag_run_waiting_for_worker"
	}
	return map[string]any{
		"mode":                            "repo_tag_remote_rehearsal_result_recording_plan",
		"result_recording_state":          recordingState,
		"result_recording_ready":          resultRecorded,
		"result_recording_ready_reason":   recordingReason,
		"recording_enabled":               resultRecorded,
		"result_written":                  resultRecorded,
		"repo_tag_run_updated":            false,
		"github_action_runs_synced":       false,
		"remote_tag_success_recorded":     evidenceState == "recorded",
		"live_result_subplan_recorded":    resultRecorded,
		"actions_refresh_result_recorded": false,
		"raw_git_output_recorded":         false,
		"raw_provider_response_recorded":  false,
		"contains_token":                  false,
		"contains_remote_url":             false,
		"contains_ref_name":               false,
		"contains_tag_message":            false,
		"tag_result_evidence":             evidence,
		"live_remote_lookup_preflight":    lookupPreflight,
		"result_recording_sequence": []string{
			"classify_remote_tag_result",
			"record_sanitized_tag_summary",
			"record_github_actions_refresh_summary",
			"persist_repo_tag_run_result",
		},
		"result_diagnostic_fields": []string{
			"tag_run_status",
			"tag_name_configured",
			"target_sha_configured",
			"target_remote_bound",
			"live_remote_tag_success_observed",
			"live_remote_tag_failed_observed",
			"live_result_state",
			"github_actions_refresh_status",
			"github_actions_refresh_state",
		},
		"suppressed_fields": []string{
			"remote_url",
			"git_credentials",
			"provider_token",
			"authorization_header",
			"tag_message",
			"git_output",
			"github_actions_response",
			"provider_response_body",
			"provider_response_headers",
		},
		"blocked_reasons": []string{
			"remote_tag_rehearsal_execution_not_performed",
			"repo_tag_run_result_update_not_wired",
			"github_actions_refresh_not_performed",
		},
		"message": "Remote tag rehearsal result recording reconciles sanitized repo_tag_run state only; no repo_tag_run update, GitHub Actions sync result, Git output, or provider response is persisted.",
	}
}

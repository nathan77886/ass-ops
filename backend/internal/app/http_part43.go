package app

import (
	"strings"
)

func repoTagRemoteRehearsalPlan(run map[string]any) map[string]any {
	status := strings.TrimSpace(stringFromMap(run, "status"))
	if status == "" {
		status = "unknown"
	}
	tagNameConfigured := strings.TrimSpace(stringFromMap(run, "tag_name")) != ""
	targetSHAConfigured := strings.TrimSpace(stringFromMap(run, "target_sha")) != ""
	targetRemoteBound := strings.TrimSpace(firstNonEmptyString(stringFromMap(run, "target_remote_id"), stringFromMap(run, "git_remote_id"))) != ""
	tagObserved := status == "completed" || status == "succeeded" || status == "success"
	tagFailed := status == "failed" || status == "error" || status == "canceled" || status == "cancelled"
	lookupStatus := strings.ToLower(strings.TrimSpace(stringFromMap(run, "lookup_operation_status")))
	lookupResult := mapFromAny(run["lookup_operation_result"])
	if len(lookupResult) == 0 {
		lookupResult = map[string]any{
			"git_remote_lookup_performed":  boolOnlyFromAny(run["lookup_git_remote_lookup_performed"]),
			"remote_tag_found":             boolOnlyFromAny(run["lookup_remote_tag_found"]),
			"matched_sha_present":          boolOnlyFromAny(run["lookup_matched_sha_present"]),
			"matched_count":                intFromAny(run["lookup_matched_count"], 0),
			"credential_userinfo_stripped": boolOnlyFromAny(run["lookup_credential_userinfo_stripped"]),
		}
	}
	actionsRefreshStatus := strings.ToLower(strings.TrimSpace(stringFromMap(run, "actions_refresh_operation_status")))
	actionsRefreshResult := map[string]any{"count": intFromAny(run["actions_refresh_synced_count"], 0)}
	lookupFound := boolOnlyFromAny(lookupResult["remote_tag_found"])
	lookupPerformed := boolOnlyFromAny(lookupResult["git_remote_lookup_performed"]) || lookupStatus == "completed"
	lookupRunning := lookupStatus == "queued" || lookupStatus == "running"
	lookupFailed := lookupStatus == "failed" || lookupStatus == "timeout" || strings.TrimSpace(stringFromMap(run, "lookup_operation_error")) != "" || (lookupPerformed && !lookupFound)
	lookupObserved := lookupPerformed && !lookupFailed
	rehearsalState := "planned"
	if !tagNameConfigured || !targetRemoteBound {
		rehearsalState = "blocked"
	}
	if tagFailed {
		rehearsalState = "failed"
	}
	if tagObserved {
		rehearsalState = "observed"
	}
	if lookupFailed {
		rehearsalState = "failed"
	}
	if lookupRunning {
		rehearsalState = "running"
	}
	if lookupObserved {
		rehearsalState = "observed"
	}
	blockedReasons := []string{}
	if !tagNameConfigured {
		blockedReasons = append(blockedReasons, "tag_name_missing")
	}
	if !targetRemoteBound {
		blockedReasons = append(blockedReasons, "target_remote_missing")
	}
	if !targetSHAConfigured {
		blockedReasons = append(blockedReasons, "target_sha_missing")
	}
	if !tagObserved && !lookupObserved {
		blockedReasons = append(blockedReasons, "live_remote_tag_success_not_observed")
	}
	if tagFailed {
		blockedReasons = append(blockedReasons, "live_remote_tag_failed_observed")
	}
	resultEvidence := repoTagRemoteResultEvidence(run, rehearsalState, tagObserved, tagFailed, tagNameConfigured, targetSHAConfigured, targetRemoteBound)
	lookupPreflight := repoTagLiveRemoteLookupPreflight(rehearsalState, status, tagObserved, tagFailed, tagNameConfigured, targetSHAConfigured, targetRemoteBound, lookupStatus, lookupResult, lookupFailed)
	actionsRefreshPlan := repoTagActionsRefreshPlan(rehearsalState, tagObserved, tagFailed, lookupPreflight, actionsRefreshStatus, actionsRefreshResult)
	actionsRefreshPerformed := boolOnlyFromAny(actionsRefreshPlan["github_actions_refresh_performed"])
	disabledBackends := []string{"git_tag", "git_push", "github_actions_api_sync"}
	if actionsRefreshPerformed {
		disabledBackends = []string{"git_tag", "git_push"}
	}
	if !lookupPerformed {
		disabledBackends = append(disabledBackends, "remote_tag_lookup", "repo_tag_run_update")
	}
	return map[string]any{
		"mode":                             "repo_tag_remote_rehearsal_plan",
		"rehearsal_state":                  rehearsalState,
		"tag_run_status":                   status,
		"tag_name_configured":              tagNameConfigured,
		"target_sha_configured":            targetSHAConfigured,
		"target_remote_bound":              targetRemoteBound,
		"live_remote_tag_success_observed": tagObserved,
		"live_remote_tag_failed_observed":  tagFailed,
		"tag_result_evidence":              resultEvidence,
		"execution_enabled":                false,
		"external_call_made":               lookupPerformed || actionsRefreshPerformed,
		"git_tag_created":                  false,
		"git_push_performed":               false,
		"github_actions_refresh_performed": actionsRefreshPerformed,
		"remote_tag_lookup_performed":      lookupPerformed,
		"result_written":                   boolOnlyFromAny(resultEvidence["sanitized_result_recorded"]),
		"contains_token":                   false,
		"contains_remote_url":              false,
		"contains_ref_name":                false,
		"contains_tag_message":             false,
		"required_controls": []string{
			"operation_approval",
			"target_remote_review",
			"git_credential_review",
			"tag_protection_review",
			"github_actions_refresh",
			"remote_tag_success_recording",
		},
		"live_rehearsal_sequence": []string{
			"approve_remote_tag_operation",
			"create_or_verify_remote_tag",
			"lookup_remote_tag_result",
			"classify_live_remote_tag_result",
			"persist_sanitized_tag_run_result",
			"refresh_github_actions_after_tag",
			"record_redacted_actions_refresh_result",
		},
		"disabled_backends": disabledBackends,
		"suppressed_fields": []string{
			"tag_name",
			"target_sha",
			"remote_url",
			"git_credentials",
			"provider_token",
			"authorization_header",
			"tag_message",
			"git_output",
			"github_actions_response",
		},
		"blocked_reasons":              blockedReasons,
		"live_remote_lookup_preflight": lookupPreflight,
		"live_result_plan":             repoTagLiveResultPlan(rehearsalState, tagObserved, tagFailed, lookupPreflight),
		"actions_refresh_plan":         actionsRefreshPlan,
		"result_recording_plan":        repoTagRemoteRehearsalResultRecordingPlan(resultEvidence, lookupPreflight),
		"message":                      "Remote tag success rehearsal reconciles sanitized tag-run evidence; live remote lookup is available as a controlled read-only worker while tag creation, push, and provider-backed Actions refresh remain disabled.",
	}
}

func repoTagLiveRemoteLookupPreflight(rehearsalState, status string, tagObserved, tagFailed, tagNameConfigured, targetSHAConfigured, targetRemoteBound bool, lookupStatus string, lookupResult map[string]any, lookupFailed bool) map[string]any {
	lookupState := "blocked"
	if tagNameConfigured && targetRemoteBound {
		lookupState = "planned"
	}
	if lookupStatus == "queued" || lookupStatus == "running" {
		lookupState = "running"
	}
	if tagObserved {
		lookupState = "observed"
	}
	if tagFailed {
		lookupState = "failed"
	}
	lookupPerformed := boolOnlyFromAny(lookupResult["git_remote_lookup_performed"]) || lookupStatus == "completed"
	if lookupPerformed && !lookupFailed {
		lookupState = "observed"
	}
	if lookupFailed {
		lookupState = "failed"
	}
	blockedReasons := []string{}
	if !lookupPerformed && lookupState != "running" {
		blockedReasons = append(blockedReasons, "remote_tag_lookup_backend_disabled", "remote_tag_lookup_not_run")
	}
	if !tagNameConfigured {
		blockedReasons = append(blockedReasons, "tag_name_missing")
	}
	if !targetRemoteBound {
		blockedReasons = append(blockedReasons, "target_remote_missing")
	}
	if !tagObserved && !lookupPerformed {
		blockedReasons = append(blockedReasons, "live_remote_tag_success_not_observed")
	}
	if tagFailed {
		blockedReasons = append(blockedReasons, "live_remote_tag_failed_observed")
	}
	return map[string]any{
		"mode":                             "repo_tag_live_remote_lookup_preflight",
		"lookup_state":                     lookupState,
		"lookup_operation_status":          lookupStatus,
		"tag_run_status":                   status,
		"rehearsal_state":                  rehearsalState,
		"lookup_ready_for_review":          tagNameConfigured && targetRemoteBound && !tagFailed && !lookupPerformed,
		"tag_name_configured":              tagNameConfigured,
		"target_sha_configured":            targetSHAConfigured,
		"target_remote_bound":              targetRemoteBound,
		"live_remote_tag_success_observed": tagObserved,
		"live_remote_tag_failed_observed":  tagFailed,
		"remote_tag_lookup_performed":      lookupPerformed,
		"remote_tag_found":                 boolOnlyFromAny(lookupResult["remote_tag_found"]),
		"matched_sha_present":              boolOnlyFromAny(lookupResult["matched_sha_present"]),
		"matched_count":                    intFromAny(lookupResult["matched_count"], 0),
		"credential_userinfo_stripped":     boolOnlyFromAny(lookupResult["credential_userinfo_stripped"]),
		"git_ls_remote_performed":          lookupPerformed,
		"provider_api_called":              false,
		"github_actions_refresh_performed": false,
		"repo_tag_run_update_performed":    lookupPerformed,
		"operation_log_written":            false,
		"external_call_made":               lookupPerformed,
		"contains_token":                   false,
		"contains_remote_url":              false,
		"contains_ref_name":                false,
		"contains_target_sha":              false,
		"contains_tag_message":             false,
		"required_lookup_fields":           []string{"target_remote_id", "tag_name", "target_sha", "tag_run_status", "repository_binding", "provider_type"},
		"required_review_controls":         []string{"target_remote_review", "git_credential_review", "tag_ref_policy_review", "actions_refresh_scope_review", "sanitized_result_recording_review"},
		"disabled_backends":                repoTagLookupDisabledBackends(lookupPerformed),
		"suppressed_fields":                []string{"tag_name", "target_sha", "tag_message", "remote_url", "git_credentials", "provider_token", "authorization_header", "git_output", "github_actions_response", "provider_response_body", "provider_response_headers"},
		"blocked_reasons":                  blockedReasons,
		"message":                          "Live remote tag lookup uses a controlled read-only git ls-remote worker; raw Git output, remote URLs, and credentials remain suppressed.",
	}
}

func repoTagLookupDisabledBackends(lookupPerformed bool) []string {
	backends := []string{"provider_tag_lookup", "github_actions_api_sync", "operation_log_write"}
	if !lookupPerformed {
		backends = append([]string{"remote_tag_lookup", "git_ls_remote", "repo_tag_run_update"}, backends...)
	}
	return backends
}

func repoTagRemoteResultEvidence(run map[string]any, rehearsalState string, tagObserved, tagFailed, tagNameConfigured, targetSHAConfigured, targetRemoteBound bool) map[string]any {
	status := strings.ToLower(strings.TrimSpace(stringFromMap(run, "status")))
	if status == "" {
		status = "unknown"
	}
	waiting := status == "queued" || status == "running" || status == "pending" || status == "unknown"
	state := "blocked"
	switch {
	case tagObserved:
		state = "recorded"
	case tagFailed:
		state = "failed"
	case waiting && tagNameConfigured && targetSHAConfigured && targetRemoteBound:
		state = "waiting_for_worker"
	}
	return map[string]any{
		"mode":                             "repo_tag_remote_result_evidence",
		"evidence_state":                   state,
		"tag_run_status":                   status,
		"tag_name_configured":              tagNameConfigured,
		"target_sha_configured":            targetSHAConfigured,
		"target_remote_bound":              targetRemoteBound,
		"operation_run_bound":              strings.TrimSpace(stringFromMap(run, "operation_run_id")) != "",
		"finished_at":                      run["finished_at"],
		"created_at":                       run["created_at"],
		"live_remote_tag_success_observed": tagObserved,
		"live_remote_tag_failed_observed":  tagFailed,
		"sanitized_result_recorded":        tagObserved || tagFailed,
		"waiting_for_worker":               state == "waiting_for_worker",
		"rehearsal_state":                  rehearsalState,
		"external_call_made":               false,
		"git_tag_created":                  false,
		"git_push_performed":               false,
		"remote_tag_lookup_performed":      false,
		"github_actions_refresh_performed": false,
		"raw_git_output_recorded":          false,
		"raw_provider_response_recorded":   false,
		"contains_token":                   false,
		"contains_remote_url":              false,
		"contains_ref_name":                false,
		"contains_tag_message":             false,
		"suppressed_fields":                []string{"tag_name", "target_sha", "tag_message", "remote_url", "git_credentials", "provider_token", "authorization_header", "git_output", "github_actions_response", "provider_response_body", "provider_response_headers"},
	}
}

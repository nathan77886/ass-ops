package app

import (
	"fmt"
	"strings"
)

func providerRefreshKindExecutionPlan(kind string, plannedKinds, blockedKinds []string) map[string]any {
	state := "not_required"
	if stringInSlice(plannedKinds, kind) {
		state = "planned"
	} else if stringInSlice(blockedKinds, kind) {
		state = "blocked"
	}
	switch kind {
	case "git_ref_fetch":
		return map[string]any{
			"mode":                       "provider_refresh_git_ref_fetch_plan",
			"kind":                       kind,
			"refresh_state":              state,
			"operation_enqueued":         false,
			"worker_job_created":         false,
			"fetch_only_backend_enabled": state == "planned",
			"git_fetch_performed":        false,
			"git_remote_sync_performed":  false,
			"remote_ref_verified":        false,
			"synced_state_write_planned": state == "planned",
			"synced_state_written":       false,
			"external_call_made":         false,
			"contains_remote_url":        false,
			"contains_git_credentials":   false,
			"contains_commit_body":       false,
			"required_controls":          []string{"git_remote_credential_review", "ref_validation_policy", "synced_state_write_audit"},
			"disabled_backends":          []string{"git_push", "remote_mutation", "raw_git_output_recording", "server_side_automatic_validation_rerun"},
			"suppressed_fields":          []string{"remote_url", "git_credentials", "authorization_header", "commit_body", "raw_git_output"},
			"blocked_reasons":            providerRefreshKindBlockedReasons(state, "git_ref_fetch_not_enqueued"),
			"execution_blockers":         []string{"git_ref_fetch_not_enqueued", "server_side_validation_rerun_not_performed"},
			"message":                    "Git ref refresh can enqueue a fetch-only worker job; this preview does not fetch, expose remote URL, push refs, or rerun validation server-side.",
		}
	case "github_actions_api_refresh":
		return map[string]any{
			"mode":                          "provider_refresh_github_actions_plan",
			"kind":                          kind,
			"refresh_state":                 state,
			"operation_enqueued":            false,
			"worker_job_created":            false,
			"github_actions_sync_enabled":   state == "planned",
			"github_actions_api_called":     false,
			"github_actions_runs_synced":    false,
			"github_actions_scope_verified": false,
			"synced_state_write_planned":    state == "planned",
			"synced_state_written":          false,
			"external_call_made":            false,
			"contains_provider_token":       false,
			"contains_remote_url":           false,
			"contains_provider_response":    false,
			"required_controls":             []string{"github_actions_scope_review", "provider_account_binding", "synced_state_write_audit"},
			"disabled_backends":             []string{"provider_mutation", "raw_provider_response_recording", "server_side_automatic_validation_rerun"},
			"suppressed_fields":             []string{"provider_token", "authorization_header", "remote_url", "github_actions_response", "workflow_logs", "provider_response_body", "provider_response_headers"},
			"blocked_reasons":               providerRefreshKindBlockedReasons(state, "github_actions_api_refresh_not_enqueued"),
			"execution_blockers":            []string{"github_actions_api_refresh_not_enqueued", "server_side_validation_rerun_not_performed"},
			"message":                       "GitHub Actions refresh can enqueue the existing sync worker job; this preview does not call the provider, record raw responses, or rerun validation server-side.",
		}
	case "argocd_app_refresh":
		return map[string]any{
			"mode":                         "provider_refresh_argo_revision_plan",
			"kind":                         kind,
			"refresh_state":                state,
			"operation_enqueued":           false,
			"worker_job_created":           false,
			"argocd_app_sync_enabled":      state == "planned",
			"argocd_api_called":            false,
			"argocd_app_refresh_performed": false,
			"argo_revision_bound":          false,
			"synced_state_write_planned":   state == "planned",
			"synced_state_written":         false,
			"external_call_made":           false,
			"contains_provider_token":      false,
			"contains_argo_response":       false,
			"required_controls":            []string{"argo_connection_review", "argo_revision_binding", "synced_state_write_audit"},
			"disabled_backends":            []string{"provider_mutation", "raw_argo_response_recording", "server_side_automatic_validation_rerun"},
			"suppressed_fields":            []string{"provider_token", "authorization_header", "argo_response", "raw_argo_response", "provider_response_body", "provider_response_headers"},
			"blocked_reasons":              providerRefreshKindBlockedReasons(state, "argocd_app_refresh_not_enqueued"),
			"execution_blockers":           []string{"argocd_app_refresh_not_enqueued", "server_side_validation_rerun_not_performed"},
			"message":                      "Argo revision refresh can enqueue the existing Argo app sync worker job; this preview does not call Argo, record raw responses, or rerun validation server-side.",
		}
	default:
		return map[string]any{
			"mode":               "provider_refresh_unknown_kind_plan",
			"kind":               kind,
			"refresh_state":      state,
			"external_call_made": false,
			"blocked_reasons":    providerRefreshKindBlockedReasons(state, "provider_refresh_kind_not_performed"),
		}
	}
}

func providerRefreshKindBlockedReasons(state, executionReason string) []string {
	if state == "not_required" {
		return []string{"refresh_kind_not_required"}
	}
	if state == "blocked" {
		return []string{"refresh_kind_blocked", executionReason}
	}
	return []string{executionReason}
}

func projectVersionProviderRefreshResultRecordingPlan(plannedKinds []string) map[string]any {
	return map[string]any{
		"mode":                           "provider_refresh_result_recording_plan",
		"result_recording_state":         "blocked",
		"result_recording_ready":         false,
		"result_recording_ready_reason":  "provider_refresh_execution_not_performed",
		"recording_enabled":              false,
		"result_written":                 false,
		"operation_log_written":          false,
		"canonical_asset_sync_queued":    false,
		"status_snapshot_write_eligible": false,
		"status_snapshot_written":        false,
		"validation_rerun_recorded":      false,
		"git_ref_fetch_result_recorded":  false,
		"github_actions_result_recorded": false,
		"argo_revision_result_recorded":  false,
		"planned_refresh_kinds":          plannedKinds,
		"required_result_fields":         []string{"operation_run_id", "refresh_kind", "status", "started_at", "finished_at", "synced_entity_count", "git_ref_fetch_status", "github_actions_refresh_status", "argo_revision_refresh_status", "validation_rerun_status"},
		"suppressed_fields":              []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body"},
		"blocked_reasons":                []string{"provider_refresh_execution_not_performed", "synced_state_write_not_performed", "validation_auto_reload_not_observed"},
		"message":                        "Refresh results are not recorded by this preview; future execution must write sanitized status, counts, and validation rerun state only.",
		"raw_response_included":          false,
		"raw_git_output_included":        false,
		"raw_argo_response_included":     false,
		"provider_request_id_included":   false,
	}
}

func stringInSlice(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func projectVersionValidationItem(index int, manifest map[string]any, remotes, tagRuns, actionRuns, argoApps []map[string]any) map[string]any {
	remoteID := strings.TrimSpace(stringFromMap(manifest, "remote_id"))
	commitSHA := strings.TrimSpace(firstNonEmptyString(stringFromMap(manifest, "commit_sha"), stringFromMap(manifest, "config_commit_sha")))
	tagName := strings.TrimSpace(stringFromMap(manifest, "tag"))
	actionRunID := strings.TrimSpace(stringFromMap(manifest, "github_action_run_id"))
	argoRevision := strings.TrimSpace(stringFromMap(manifest, "argo_revision"))
	checks := make([]map[string]any, 0, 5)
	remote := findRowByID(remotes, remoteID)
	checks = append(checks, validationCheck("remote_present", remote != nil, false, "manifest remote is available in synced database state"))
	refChecksConfigured := commitSHA != "" || tagName != "" || actionRunID != "" || argoRevision != ""
	if commitSHA != "" && remote != nil {
		latestSHA := strings.TrimSpace(fmt.Sprint(remote["latest_sha"]))
		checks = append(checks, validationCheck("commit_matches_remote_latest", latestSHA != "" && strings.EqualFold(latestSHA, commitSHA), latestSHA != "", "manifest commit matches synced remote latest_sha"))
	}
	if tagName != "" {
		tagRun := findProjectVersionTagRun(tagRuns, remoteID, tagName, commitSHA)
		checks = append(checks, validationCheck("tag_run_observed", tagRun != nil, len(tagRuns) > 0, "tag has a local repo_tag_run observation"))
	}
	if actionRunID != "" {
		actionRun := findProjectVersionActionRun(actionRuns, remoteID, actionRunID, commitSHA)
		checks = append(checks, validationCheck("github_action_run_observed", actionRun != nil, len(actionRuns) > 0, "GitHub Actions run has a local synced observation"))
	}
	if argoRevision != "" {
		argoApp := findProjectVersionArgoRevision(argoApps, argoRevision)
		checks = append(checks, validationCheck("argo_revision_observed", argoApp != nil, len(argoApps) > 0, "Argo revision has a local synced app observation"))
	}
	if remote != nil && !refChecksConfigured {
		checks = append(checks, validationCheck("version_refs_configured", false, true, "manifest item has a remote but no commit, tag, action, or Argo revision to validate"))
	}
	status := validationStatus(checks)
	return map[string]any{
		"index":               index,
		"repo_key":            manifest["repo_key"],
		"repo_role":           manifest["repo_role"],
		"remote_id":           remoteID,
		"remote_key":          manifest["remote_key"],
		"status":              status,
		"checks":              checks,
		"external_call_made":  false,
		"secret_included":     false,
		"credential_included": false,
	}
}

func validationCheck(name string, ready, observed bool, message string) map[string]any {
	status := "blocked"
	if ready {
		status = "ready"
	} else if observed {
		status = "partial"
	}
	return map[string]any{"name": name, "status": status, "message": message}
}

func validationStatus(checks []map[string]any) string {
	if len(checks) == 0 {
		return "blocked"
	}
	hasReady := false
	hasPartial := false
	for _, check := range checks {
		switch check["status"] {
		case "ready":
			hasReady = true
		case "partial":
			hasPartial = true
		default:
			return "blocked"
		}
	}
	if hasPartial {
		return "partial"
	}
	if hasReady {
		return "ready"
	}
	return "blocked"
}

func findRowByID(rows []map[string]any, id string) map[string]any {
	for _, row := range rows {
		if strings.TrimSpace(fmt.Sprint(row["id"])) == id {
			return row
		}
	}
	return nil
}

func findProjectVersionTagRun(rows []map[string]any, remoteID, tagName, commitSHA string) map[string]any {
	for _, row := range rows {
		if remoteID != "" && strings.TrimSpace(fmt.Sprint(row["target_remote_id"])) != remoteID && strings.TrimSpace(fmt.Sprint(row["git_remote_id"])) != remoteID {
			continue
		}
		if tagName != "" && strings.TrimSpace(fmt.Sprint(row["tag_name"])) != tagName {
			continue
		}
		if commitSHA != "" && !strings.EqualFold(strings.TrimSpace(fmt.Sprint(row["target_sha"])), commitSHA) {
			continue
		}
		return row
	}
	return nil
}

func findProjectVersionActionRun(rows []map[string]any, remoteID, actionRunID, commitSHA string) map[string]any {
	for _, row := range rows {
		idMatches := strings.TrimSpace(fmt.Sprint(row["id"])) == actionRunID || strings.TrimSpace(fmt.Sprint(row["run_id"])) == actionRunID
		if !idMatches {
			continue
		}
		if remoteID != "" && strings.TrimSpace(fmt.Sprint(row["git_remote_id"])) != remoteID {
			continue
		}
		if commitSHA != "" && !strings.EqualFold(strings.TrimSpace(fmt.Sprint(row["commit_sha"])), commitSHA) {
			continue
		}
		return row
	}
	return nil
}

func findProjectVersionArgoRevision(rows []map[string]any, argoRevision string) map[string]any {
	needle := strings.TrimSpace(argoRevision)
	for _, row := range rows {
		metadata := mapFromAny(row["metadata"])
		revision := firstNonEmptyString(stringFromMap(metadata, "revision"), stringFromMap(metadata, "target_revision"))
		if strings.EqualFold(strings.TrimSpace(revision), needle) {
			return row
		}
	}
	return nil
}

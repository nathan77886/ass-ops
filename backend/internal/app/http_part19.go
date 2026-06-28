package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"strings"
)

func configRepositoryGitWorkflowInput(repo map[string]any, remotes []map[string]any, preview map[string]any) map[string]any {
	defaultBranch := cleanOptionalText(stringFromMap(repo, "default_branch"))
	remoteID := ""
	remoteProvider := ""
	localBareCandidates := 0
	selectedRemote := map[string]any(nil)
	for _, remote := range remotes {
		if strings.EqualFold(cleanOptionalText(stringFromMap(remote, "provider_type")), "local_bare") {
			localBareCandidates++
			selectedRemote = remote
		}
	}
	if localBareCandidates != 1 && len(remotes) > 0 {
		selectedRemote = remotes[0]
	}
	if selectedRemote != nil {
		remoteID = cleanOptionalID(fmt.Sprint(selectedRemote["id"]))
		remoteProvider = cleanOptionalText(stringFromMap(selectedRemote, "provider_type"))
		if defaultBranch == "" {
			defaultBranch = cleanOptionalText(stringFromMap(selectedRemote, "default_branch"))
		}
	}
	return map[string]any{
		"project_git_repository_id":        cleanOptionalID(fmt.Sprint(repo["id"])),
		"config_remote_id":                 remoteID,
		"provider_type":                    remoteProvider,
		"local_bare_write_candidate_count": localBareCandidates,
		"local_bare_write_eligible":        localBareCandidates == 1 && strings.EqualFold(remoteProvider, "local_bare"),
		"default_branch_configured":        defaultBranch != "",
		"scaffold_file_count":              preview["file_count"],
		"remote_count":                     preview["remote_count"],
		"mode":                             "approval_gated_audit_only",
		"file_content_included":            false,
		"secret_included":                  false,
		"external_call_made":               false,
		"git_write_performed":              false,
	}
}

func enqueueConfigRepositoryGitWorkflowGorm(ctx context.Context, tx *gorm.DB, projectID string, repo map[string]any, remotes []map[string]any, preview map[string]any, actorID string) (map[string]any, error) {
	input := configRepositoryGitWorkflowInput(repo, remotes, preview)
	input["actor_user_id"] = cleanOptionalID(actorID)
	title := "config git workflow " + cleanOptionalText(stringFromMap(repo, "name"))
	if strings.TrimSpace(title) == "config git workflow" {
		title = "config git workflow"
	}
	return enqueueOperationGorm(ctx, tx, projectID, cleanOptionalID(stringFromMap(input, "config_remote_id")), "config.git_commit", title, input, []string{"git", "config"}, "control-worker")
}

func configRepositoryGitWorkflowRequestResult(op map[string]any) map[string]any {
	return map[string]any{
		"mode":                         "config_repository_git_workflow_request_result",
		"operation_run_id":             op["id"],
		"operation_type":               "config.git_commit",
		"operation_created":            true,
		"worker_job_created":           true,
		"approval_gated":               true,
		"git_write_performed":          false,
		"external_call_made":           false,
		"file_content_included":        false,
		"secret_included":              false,
		"project_version_pin_written":  false,
		"live_commit_validation":       "disabled",
		"sanitized_result_expected":    true,
		"required_worker_capabilities": []string{"git", "config"},
		"suppressed_fields":            []string{"file_content", "secret_values", "git_credentials", "provider_token", "remote_url", "branch_name", "commit_message", "commit_sha"},
	}
}

func configRepositoryGitCommitWorkspacePlan(fileCount, remoteCount int, defaultBranchConfigured bool) map[string]any {
	metadataReady := fileCount > 0 && remoteCount > 0 && defaultBranchConfigured
	blockedReasons := []string{"git_workspace_backend_disabled", "secret_scan_not_performed", "git_commit_not_created", "provider_review_not_created"}
	if fileCount == 0 {
		blockedReasons = append(blockedReasons, "scaffold_files_missing")
	}
	if remoteCount == 0 {
		blockedReasons = append(blockedReasons, "config_remote_missing")
	}
	if !defaultBranchConfigured {
		blockedReasons = append(blockedReasons, "default_branch_missing")
	}
	return map[string]any{
		"mode":                      "config_repository_git_workspace_plan",
		"workspace_state":           "blocked",
		"workspace_ready":           false,
		"workspace_ready_reason":    "config_git_workspace_backend_disabled",
		"metadata_ready":            metadataReady,
		"workspace_bound":           false,
		"git_clone_performed":       false,
		"file_content_materialized": false,
		"secret_scan_performed":     false,
		"git_commit_created":        false,
		"git_push_performed":        false,
		"provider_review_created":   false,
		"external_call_made":        false,
		"contains_file_content":     false,
		"contains_secret_values":    false,
		"required_workspace_fields": []string{"operation_run_id", "repository_id", "remote_id", "workspace_id", "scaffold_file_count", "secret_scan_status", "commit_author"},
		"suppressed_fields":         []string{"file_content", "secret_values", "git_credentials", "provider_token", "remote_url", "branch_name", "commit_message", "author_email"},
		"blocked_reasons":           blockedReasons,
		"execution_sequence":        []string{"bind_clean_workspace", "materialize_scaffold_files", "run_secret_scan", "create_review_branch", "commit_scaffold", "push_review_branch", "open_provider_review"},
		"message":                   "Config Git workspace execution is preview-only; no workspace, file content, Git ref, commit, push, or provider review is created.",
	}
}

func configRepositoryRemoteReviewPlan(planState string, remoteCount int, defaultBranchConfigured bool) map[string]any {
	metadataReady := planState == "planned" && remoteCount > 0 && defaultBranchConfigured
	reviewState := "blocked"
	if metadataReady {
		reviewState = "planned"
	}
	blockedReasons := []string{"git_push_not_performed", "provider_review_workflow_not_wired", "provider_review_not_created"}
	if remoteCount == 0 {
		blockedReasons = append(blockedReasons, "config_remote_missing")
	}
	if !defaultBranchConfigured {
		blockedReasons = append(blockedReasons, "default_branch_missing")
	}
	return map[string]any{
		"mode":                             "config_repository_remote_review_plan",
		"review_state":                     reviewState,
		"metadata_ready":                   metadataReady,
		"review_branch_ready":              metadataReady,
		"protected_default_branch_avoided": true,
		"git_push_performed":               false,
		"review_branch_pushed":             false,
		"provider_review_created":          false,
		"provider_review_link_recorded":    false,
		"external_call_made":               false,
		"contains_token":                   false,
		"contains_remote_url":              false,
		"contains_branch_name":             false,
		"contains_commit_message":          false,
		"contains_provider_response":       false,
		"required_review_fields":           []string{"operation_run_id", "repository_id", "remote_id", "review_branch_key", "base_branch_key", "commit_sha_status", "provider_review_status"},
		"required_controls":                []string{"branch_policy_review", "protected_branch_avoidance", "provider_review_workflow", "provider_response_redaction", "operator_review_before_merge"},
		"execution_sequence":               []string{"derive_review_branch", "push_review_branch", "open_provider_review_request", "record_review_request_summary", "wait_for_operator_merge"},
		"disabled_backends":                []string{"git_push", "pull_request_create", "merge_request_create", "provider_review_link_write", "provider_response_recording"},
		"suppressed_fields":                []string{"remote_url", "branch_name", "commit_message", "commit_sha", "git_credentials", "provider_token", "authorization_header", "provider_response_body", "provider_response_headers"},
		"blocked_reasons":                  blockedReasons,
		"execution_blockers":               []string{"git_push_not_performed", "provider_review_workflow_not_wired"},
		"message":                          "Config remote push and provider review creation are planned only; no review branch, provider request, URL, response, or branch name is persisted.",
	}
}

func configRepositoryProjectVersionPinEvidence(repo map[string]any, remotes, versions []map[string]any) map[string]any {
	// This function receives raw ProjectVersion metadata. Return only redacted
	// evidence; never include the original metadata map or raw config_commit_sha.
	repoKey := strings.TrimSpace(stringFromMap(repo, "repo_key"))
	repoID := strings.TrimSpace(fmt.Sprint(repo["id"]))
	remoteByID := map[string]map[string]any{}
	for _, remote := range remotes {
		remoteID := strings.TrimSpace(fmt.Sprint(remote["id"]))
		if remoteID != "" && remoteID != "<nil>" {
			remoteByID[remoteID] = remote
		}
	}
	pinned, validated, mismatched := 0, 0, 0
	items := []map[string]any{}
	for _, version := range versions {
		metadata := mapFromAny(version["metadata"])
		for _, manifest := range mapSliceFromAny(metadata["repositories"]) {
			configSHA := strings.TrimSpace(stringFromMap(manifest, "config_commit_sha"))
			if configSHA == "" {
				continue
			}
			manifestRepoKey := strings.TrimSpace(stringFromMap(manifest, "repo_key"))
			manifestRepoID := strings.TrimSpace(stringFromMap(manifest, "repository_id"))
			manifestRole := strings.TrimSpace(stringFromMap(manifest, "repo_role"))
			_ = manifestRole
			repositoryMatches := (manifestRepoKey != "" && manifestRepoKey == repoKey) || (manifestRepoID != "" && manifestRepoID == repoID)
			if !repositoryMatches {
				continue
			}
			remoteID := strings.TrimSpace(stringFromMap(manifest, "remote_id"))
			remote := remoteByID[remoteID]
			latestSHA := ""
			if remote != nil {
				latestSHA = strings.TrimSpace(stringFromMap(remote, "latest_sha"))
			}
			validationStatus := "not_observed"
			if latestSHA != "" && strings.EqualFold(latestSHA, configSHA) {
				validationStatus = "validated"
				validated++
			} else if latestSHA != "" {
				validationStatus = "mismatched"
				mismatched++
			}
			pinned++
			items = append(items, map[string]any{
				"project_version_id":        version["id"],
				"version":                   version["version"],
				"repo_key":                  manifestRepoKey,
				"repo_role":                 manifestRole,
				"remote_id":                 remoteID,
				"config_commit_sha_present": true,
				"remote_latest_sha_present": latestSHA != "",
				"validation_status":         validationStatus,
				"commit_sha_included":       false,
				"remote_url_included":       false,
				"secret_included":           false,
			})
		}
	}
	pinState := "not_recorded"
	if pinned > 0 {
		pinState = "recorded"
	}
	liveState := "not_recorded"
	if validated > 0 {
		liveState = "recorded"
	} else if mismatched > 0 {
		liveState = "mismatched"
	} else if pinned > 0 {
		liveState = "waiting_for_synced_remote"
	}
	return map[string]any{
		"mode":                       "config_repository_project_version_pin_evidence",
		"project_version_count":      len(versions),
		"pinned_version_count":       pinned,
		"validated_version_count":    validated,
		"mismatched_version_count":   mismatched,
		"config_commit_sha_recorded": pinned > 0,
		"live_validation_recorded":   validated > 0,
		"pin_state":                  pinState,
		"live_validation_state":      liveState,
		"items":                      items,
		"external_call_made":         false,
		"git_fetch_performed":        false,
		"commit_sha_included":        false,
		"remote_url_included":        false,
		"secret_included":            false,
		"suppressed_fields":          []string{"config_commit_sha", "remote_url", "git_credentials", "provider_token", "authorization_header", "provider_response_body"},
	}
}

package app

import (
	"fmt"
	"strings"
)

func configRepositoryGitWorkflowAuditEvidence(operations []map[string]any) map[string]any {
	items := make([]map[string]any, 0, len(operations))
	statusCounts := map[string]int{}
	logCount := 0
	for _, operation := range operations {
		status := strings.ToLower(strings.TrimSpace(fmt.Sprint(operation["status"])))
		if status == "" || status == "<nil>" {
			status = "unknown"
		}
		statusCounts[status]++
		rowLogCount := intFromAny(operation["operation_log_count"], 0)
		logCount += rowLogCount
		items = append(items, map[string]any{
			"operation_run_id":                  cleanOptionalID(fmt.Sprint(operation["id"])),
			"status":                            status,
			"created_at":                        operation["created_at"],
			"updated_at":                        operation["updated_at"],
			"started_at":                        operation["started_at"],
			"finished_at":                       operation["finished_at"],
			"operation_log_count":               rowLogCount,
			"result_scope":                      "sanitized_config_git_workflow_intent",
			"git_write_performed":               false,
			"git_commit_created":                false,
			"git_push_performed":                false,
			"external_call_made":                false,
			"file_content_included":             false,
			"secret_included":                   false,
			"raw_git_output_recorded":           false,
			"raw_provider_response_recorded":    false,
			"project_version_pin_written":       false,
			"live_commit_validation_performed":  false,
			"project_version_pin_write_allowed": false,
		})
	}
	queued := statusCounts["queued"] + statusCounts["pending"]
	running := statusCounts["running"]
	completed := statusCounts["completed"] + statusCounts["succeeded"] + statusCounts["success"]
	failed := statusCounts["failed"] + statusCounts["error"]
	canceled := statusCounts["canceled"] + statusCounts["cancelled"]
	active := queued + running
	known := queued + running + completed + failed + canceled
	unknown := len(operations) - known
	if unknown < 0 {
		unknown = 0
	}
	state := "not_requested"
	switch {
	case len(operations) == 0:
		state = "not_requested"
	case active > 0:
		state = "waiting_for_worker"
	case failed > 0 && canceled > 0:
		state = "mixed_failed"
	case failed > 0:
		state = "failed"
	case canceled > 0:
		state = "canceled"
	case unknown > 0:
		state = "unknown"
	default:
		state = "recorded"
	}
	sanitizedRecorded := len(operations) > 0 && active == 0 && unknown == 0 && logCount > 0
	return map[string]any{
		"mode":                                      "config_repository_git_workflow_audit_evidence",
		"evidence_state":                            state,
		"has_audit_operations":                      len(operations) > 0,
		"operation_count":                           len(operations),
		"active_count":                              active,
		"queued_count":                              queued,
		"running_count":                             running,
		"completed_count":                           completed,
		"failed_count":                              failed,
		"canceled_count":                            canceled,
		"unknown_count":                             unknown,
		"operation_log_count":                       logCount,
		"sanitized_result_recorded":                 sanitizedRecorded,
		"has_failures":                              failed > 0,
		"has_cancellations":                         canceled > 0,
		"has_unknown_status":                        unknown > 0,
		"items":                                     items,
		"git_write_performed":                       false,
		"git_commit_created":                        false,
		"git_push_performed":                        false,
		"external_call_made":                        false,
		"file_content_included":                     false,
		"secret_included":                           false,
		"raw_git_output_recorded":                   false,
		"raw_provider_response_recorded":            false,
		"project_version_pin_written":               false,
		"live_commit_validation_performed":          false,
		"live_remote_commit_validation_performed":   false,
		"operation_result_contains_raw_git_output":  false,
		"operation_result_contains_provider_body":   false,
		"operation_result_contains_file_content":    false,
		"operation_result_contains_secret_material": false,
		"suppressed_fields": []string{
			"file_content",
			"secret_values",
			"git_credentials",
			"provider_token",
			"remote_url",
			"branch_name",
			"commit_message",
			"commit_sha",
			"git_output",
			"provider_response_body",
			"provider_response_headers",
		},
	}
}

func configRepositoryGitCommitPlan(repo map[string]any, files, remotes []map[string]any, scaffoldBlockedReasons []string, pinEvidence, workflowEvidence, refRefreshEvidence map[string]any) map[string]any {
	planState := "planned"
	blockedReasons := append([]string{}, scaffoldBlockedReasons...)
	defaultBranch := strings.TrimSpace(stringFromMap(repo, "default_branch"))
	if defaultBranch == "" {
		blockedReasons = append(blockedReasons, "default_branch_missing")
	}
	if len(files) == 0 {
		blockedReasons = append(blockedReasons, "scaffold_files_missing")
	}
	if len(blockedReasons) > 0 {
		planState = "blocked"
	}
	approvalPlan := configRepositoryGitCommitApprovalPlan(planState, blockedReasons)
	workspacePlan := configRepositoryGitCommitWorkspacePlan(len(files), len(remotes), defaultBranch != "")
	remoteReviewPlan := configRepositoryRemoteReviewPlan(planState, len(remotes), defaultBranch != "")
	pinValidationPlan := configRepositoryProjectVersionPinValidationPlan(defaultBranch != "", len(remotes) > 0, pinEvidence)
	promotionReadinessPlan := configRepositoryGitCommitPromotionReadinessPlan(pinEvidence, workflowEvidence)
	pinObserved := boolOnlyFromAny(pinEvidence["config_commit_sha_recorded"])
	liveValidationObserved := boolOnlyFromAny(pinEvidence["live_validation_recorded"])
	refRefreshObserved := boolOnlyFromAny(refRefreshEvidence["has_ref_refresh_operations"])
	refRefreshCompleted := boolOnlyFromAny(refRefreshEvidence["git_fetch_performed"])
	steps := []map[string]any{
		{
			"kind":   "scaffold_review",
			"status": statusWhen(len(files) > 0 && !stringListContains(blockedReasons, "repository_role_is_not_config")),
			"checks": []string{"repository_role", "scaffold_paths", "human_file_review"},
			"reason": reasonWhen(len(files) > 0 && !stringListContains(blockedReasons, "repository_role_is_not_config"), "config scaffold paths are ready for human review", "config repository scaffold is not ready"),
		},
		{
			"kind":   "remote_binding",
			"status": statusWhen(len(remotes) > 0),
			"checks": []string{"git_remote", "provider_type", "branch_policy"},
			"reason": reasonWhen(len(remotes) > 0, "at least one config remote is available for future Git workflow", "config remote is required before commit rehearsal"),
		},
		{
			"kind":   "workspace_checkout",
			"status": "blocked",
			"checks": []string{"clone_or_fetch", "clean_worktree", "credential_binding"},
			"reason": "Git checkout/fetch is not performed by this preview",
		},
		{
			"kind":   "review_branch",
			"status": statusWhen(defaultBranch != ""),
			"checks": []string{"default_branch", "review_branch_policy", "protected_branch_avoidance"},
			"reason": reasonWhen(defaultBranch != "", "review branch can be derived after branch policy review", "default branch metadata is required"),
		},
		{
			"kind":   "scaffold_commit",
			"status": "blocked",
			"checks": []string{"file_materialization", "secret_scan", "commit_author_policy"},
			"reason": "File content materialization and git commit are disabled in this preview",
		},
		{
			"kind":   "remote_push",
			"status": "blocked",
			"checks": []string{"git_push", "provider_protection", "review_request"},
			"reason": "Git push and PR/MR creation require a future approval-gated provider workflow",
		},
		{
			"kind":   "project_version_pin",
			"status": statusWhen(pinObserved),
			"checks": []string{"config_commit_sha", "ProjectVersion.metadata.repositories[].config_commit_sha"},
			"reason": reasonWhen(pinObserved, "ProjectVersion config_commit_sha metadata is already recorded for this config repository", "ProjectVersion config commit pin is not written by this preview"),
		},
		{
			"kind":   "live_commit_validation",
			"status": statusWhen(liveValidationObserved),
			"checks": []string{"git_fetch", "remote_commit_lookup", "synced_state_validation"},
			"reason": reasonWhen(liveValidationObserved, "config_commit_sha matches synced remote latest_sha; config ref refresh evidence is available when requested", "Live commit validation is limited to synced state until config ref refresh completes"),
		},
		{
			"kind":   "config_ref_refresh",
			"status": statusWhen(refRefreshCompleted),
			"checks": []string{"git_refs_refresh", "config_remote", "synced_state_update"},
			"reason": reasonWhen(refRefreshCompleted, "config remote refs refresh completed and can feed synced-state validation", "Config remote refs refresh has not completed"),
		},
	}
	return map[string]any{
		"mode":                              "config_repository_git_commit_plan_preview",
		"plan_state":                        planState,
		"execution_enabled":                 planState == "planned",
		"execution_mode":                    "approval_gated_audit_only",
		"operation_request_enabled":         planState == "planned",
		"external_call_made":                false,
		"git_clone_performed":               false,
		"git_fetch_performed":               false,
		"git_commit_created":                false,
		"git_push_performed":                false,
		"pull_request_created":              false,
		"project_version_pin_written":       false,
		"live_commit_validation_performed":  false,
		"project_version_pin_observed":      pinObserved,
		"live_commit_validation_observed":   liveValidationObserved,
		"config_ref_refresh_observed":       refRefreshObserved,
		"config_ref_refresh_completed":      refRefreshCompleted,
		"audit_operation_observed":          boolOnlyFromAny(workflowEvidence["has_audit_operations"]),
		"sanitized_result_observed":         boolOnlyFromAny(workflowEvidence["sanitized_result_recorded"]),
		"file_content_materialized":         false,
		"secret_scan_performed":             false,
		"credential_bound":                  false,
		"scaffold_file_count":               len(files),
		"remote_count":                      len(remotes),
		"default_branch_configured":         defaultBranch != "",
		"required_controls":                 []string{"config_remote_review", "branch_policy_review", "human_file_review", "secret_scan", "commit_author_policy", "provider_review_workflow", "project_version_config_commit_pin", "live_remote_commit_validation"},
		"disabled_backends":                 []string{"git_clone", "git_fetch", "file_write", "git_commit", "git_push", "pull_request_create", "project_version_update", "live_commit_validation"},
		"enabled_backends":                  []string{"operation_run_enqueue", "worker_job_enqueue", "sanitized_audit_result_recording"},
		"blocked_reasons":                   blockedReasons,
		"suppressed_fields":                 []string{"file_content", "secret_values", "git_credentials", "provider_token", "author_email", "remote_url", "branch_name", "commit_message"},
		"steps":                             steps,
		"execution_sequence":                []string{"review_scaffold", "bind_config_remote", "checkout_clean_workspace", "create_review_branch", "materialize_files", "run_secret_scan", "commit_scaffold", "push_review_branch", "open_review_request", "pin_config_commit_sha", "validate_remote_commit"},
		"required_project_version_metadata": []string{"repositories[].repo_key", "repositories[].remote_id", "repositories[].config_commit_sha"},
		"approval_request_plan":             approvalPlan,
		"workspace_execution_plan":          workspacePlan,
		"remote_review_plan":                remoteReviewPlan,
		"project_version_pin_plan":          pinValidationPlan,
		"git_workflow_audit_evidence":       workflowEvidence,
		"config_ref_refresh_evidence":       refRefreshEvidence,
		"promotion_readiness_plan":          promotionReadinessPlan,
		"result_recording_plan":             configRepositoryGitCommitResultRecordingPlan(pinEvidence, workflowEvidence),
		"message":                           "Config repository Git workflow can now enqueue an approval-gated audit job; file materialization, Git commit/push, provider requests, ProjectVersion pin writes, and live validation remain disabled.",
	}
}

func configRepositoryGitCommitApprovalPlan(planState string, blockedReasons []string) map[string]any {
	metadataReady := planState == "planned"
	metadataBlockedReasons := append([]string{}, blockedReasons...)
	requestReadyReason := "config_git_commit_metadata_ready"
	if !metadataReady {
		requestReadyReason = "config_git_commit_metadata_blocked"
	}
	return map[string]any{
		"mode":                     "config_repository_git_commit_approval_plan",
		"request_state":            planState,
		"request_ready":            metadataReady,
		"request_ready_reason":     requestReadyReason,
		"metadata_ready":           metadataReady,
		"operation_created":        false,
		"approval_request_created": false,
		"worker_job_created":       false,
		"external_call_made":       false,
		"required_approval_fields": []string{"operation_run_id", "repository_id", "remote_id", "default_branch", "scaffold_file_count", "requested_by", "reason"},
		"suppressed_fields":        []string{"file_content", "secret_values", "git_credentials", "provider_token", "remote_url", "branch_name", "commit_message", "author_email"},
		"blocked_reasons":          metadataBlockedReasons,
		"execution_blockers":       []string{"git_workspace_backend_disabled", "git_commit_not_created", "provider_review_workflow_not_wired", "project_version_pin_write_disabled"},
		"required_operator_action": "Request approval for a config Git workflow audit job before any future checkout, file materialization, commit, push, ProjectVersion pin, or live validation backend is armed.",
	}
}

package app

func agentCodeModificationPlan(auditEvidenceRows ...map[string]any) map[string]any {
	auditEvidence := map[string]any{}
	if len(auditEvidenceRows) > 0 {
		auditEvidence = auditEvidenceRows[0]
	}
	codeEvidence := agentCodeModificationEvidence(auditEvidence)
	executionArmingPlan := agentCodeModificationExecutionArmingPlan(codeEvidence)
	sourceCheckoutBranchReviewPlan := agentCodeModificationSourceCheckoutBranchReviewPlan(codeEvidence, executionArmingPlan)
	return map[string]any{
		"mode":                                "redacted_agent_code_modification_plan",
		"plan_state":                          "blocked",
		"plan_ready":                          false,
		"plan_ready_reason":                   "agent_code_modification_backend_disabled",
		"execution_enabled":                   false,
		"mutation_enabled":                    false,
		"external_call_made":                  false,
		"repository_mutation_allowed":         false,
		"source_checkout_performed":           false,
		"workspace_bound":                     false,
		"branch_created":                      false,
		"patch_content_materialized":          false,
		"diff_materialized":                   false,
		"file_patch_applied":                  false,
		"tests_executed":                      false,
		"git_commit_created":                  false,
		"git_push_performed":                  false,
		"pull_request_created":                false,
		"commit_push_agent_invoked":           false,
		"requires_source_remote_review":       true,
		"requires_branch_policy_review":       true,
		"requires_patch_review":               true,
		"requires_test_plan_review":           true,
		"requires_commit_push_agent":          true,
		"contains_token":                      false,
		"contains_remote_url":                 false,
		"contains_branch_name":                false,
		"contains_workspace_path":             false,
		"contains_patch_content":              false,
		"contains_diff_content":               false,
		"contains_file_content":               false,
		"execution_boundary_redacted":         true,
		"code_modification_evidence":          codeEvidence,
		"execution_arming_plan":               executionArmingPlan,
		"source_checkout_branch_review_plan":  sourceCheckoutBranchReviewPlan,
		"source_checkout_branch_review_ready": sourceCheckoutBranchReviewPlan["review_ready"],
		"execution_arming_ready":              executionArmingPlan["arming_ready"],
		"audit_result_recorded":               boolOnlyFromAny(codeEvidence["sanitized_result_recorded"]),
		"blocked_reasons": []string{
			"agent_code_modification_backend_disabled",
			"source_checkout_not_armed",
			"branch_creation_not_armed",
			"patch_apply_not_armed",
			"commit_push_agent_not_invoked",
			"provider_pr_workflow_not_wired",
		},
		"required_controls": []string{
			"agent_execute_approval",
			"runtime_verification",
			"source_remote_review",
			"workspace_binding",
			"branch_policy_review",
			"structured_patch_review",
			"human_patch_approval",
			"test_plan_review",
			"commit_push_agent",
			"provider_review_reconciliation",
		},
		"disabled_backends": []string{
			"source_checkout",
			"branch_create",
			"file_patch_apply",
			"test_command_execute",
			"git_commit",
			"git_push",
			"pull_request_create",
			"commit_push_agent",
		},
		"suppressed_fields": []string{
			"runtime_config",
			"environment_variables",
			"authorization_header",
			"source_remote_url",
			"workspace_path",
			"branch_name",
			"prompt_body",
			"patch_content",
			"diff_content",
			"file_content",
			"test_output",
			"token",
		},
		"execution_sequence": []string{
			"request_agent_execute_approval",
			"verify_runtime_metadata",
			"review_source_remote",
			"bind_workspace",
			"create_review_branch",
			"capture_structured_patch",
			"review_diff",
			"run_test_plan",
			"request_patch_apply_approval",
			"apply_patch",
			"delegate_commit_push_agent",
			"open_provider_review",
		},
		"result_recording_plan": agentCodeModificationResultRecordingPlan(codeEvidence),
		"message":               "Agent code modification is represented as a redacted rehearsal plan only; source checkout, branch creation, patch application, tests, commit, push, and review creation remain disabled.",
	}
}

func agentCodeModificationSourceCheckoutBranchReviewPlan(codeEvidence, executionArmingPlan map[string]any) map[string]any {
	auditState := cleanPreviewString(codeEvidence["evidence_state"])
	hasAudit := boolOnlyFromAny(codeEvidence["has_code_modification_audit"])
	completeAudit := boolOnlyFromAny(executionArmingPlan["arming_ready"])
	workerDispatchRecorded := boolOnlyFromAny(codeEvidence["worker_dispatch_audit_recorded"])
	codexPlanRecorded := boolOnlyFromAny(codeEvidence["codex_execution_plan_recorded"])
	patchPrepareRecorded := boolOnlyFromAny(codeEvidence["patch_prepare_audit_recorded"])
	terminalAuditRecorded := boolOnlyFromAny(codeEvidence["sanitized_result_recorded"])

	reviewState := "blocked"
	reviewReason := "agent_code_source_checkout_branch_review_audit_not_recorded"
	switch {
	case completeAudit:
		reviewState = "ready_for_operator_review"
		reviewReason = "agent_code_source_checkout_branch_review_ready_for_operator_review"
	case auditState == "waiting_for_worker":
		reviewState = "waiting_for_worker"
		reviewReason = "agent_code_source_checkout_branch_review_waiting_for_worker"
	case hasAudit:
		reviewState = "partial_audit"
		reviewReason = "agent_code_source_checkout_branch_review_incomplete_audit"
	}

	missing := append([]string{}, stringSliceFromAny(codeEvidence["missing_audit_evidence"])...)
	if !completeAudit && !stringListContains(missing, "operator_source_checkout_branch_review") {
		missing = append(missing, "operator_source_checkout_branch_review")
	}

	return map[string]any{
		"mode":                                "redacted_agent_source_checkout_branch_review_plan",
		"review_state":                        reviewState,
		"review_ready":                        completeAudit,
		"review_ready_reason":                 reviewReason,
		"audit_evidence_state":                auditState,
		"worker_dispatch_audit_recorded":      workerDispatchRecorded,
		"codex_execution_plan_recorded":       codexPlanRecorded,
		"patch_prepare_audit_recorded":        patchPrepareRecorded,
		"terminal_audit_recorded":             terminalAuditRecorded,
		"review_evidence_scope":               "shared_code_modification_audit",
		"source_remote_review_ready":          completeAudit,
		"workspace_binding_review_ready":      completeAudit,
		"branch_policy_review_ready":          completeAudit,
		"source_remote_review_scope":          "shared_code_modification_audit",
		"workspace_binding_review_scope":      "shared_code_modification_audit",
		"branch_policy_review_scope":          "shared_code_modification_audit",
		"review_branch_required":              true,
		"default_branch_direct_write_blocked": true,
		"source_checkout_performed":           false,
		"workspace_bound":                     false,
		"branch_created":                      false,
		"default_branch_checked_out":          false,
		"repository_mutation_allowed":         false,
		"external_call_made":                  false,
		"git_fetch_performed":                 false,
		"git_checkout_performed":              false,
		"git_branch_created":                  false,
		"contains_source_remote_url":          false,
		"contains_workspace_path":             false,
		"contains_branch_name":                false,
		"contains_default_branch_name":        false,
		"contains_token":                      false,
		"required_review_fields":              []string{"operation_run_id", "agent_task_id", "source_remote_review", "workspace_binding_review", "branch_policy_review", "review_branch_policy", "operator_review_status"},
		"required_operator_controls":          []string{"source_remote_review", "workspace_binding_review", "branch_policy_review", "default_branch_protection_review", "operator_source_checkout_branch_review"},
		"missing_evidence":                    missing,
		"disabled_backends":                   []string{"source_checkout", "workspace_bind", "git_fetch", "git_checkout", "branch_create", "default_branch_write", "repository_mutation"},
		"suppressed_fields":                   []string{"source_remote_url", "repository_url", "workspace_path", "branch_name", "default_branch", "review_branch_name", "authorization_header", "runtime_config", "environment_variables", "token", "api_key"},
		"message":                             "Source checkout and branch creation are represented as a redacted operator-review preflight derived from the shared code-modification audit in this phase; no repository is cloned, no workspace is bound, no branch is created, and default-branch writes remain blocked.",
	}
}

func agentCodeModificationExecutionArmingPlan(codeEvidence map[string]any) map[string]any {
	evidenceState := cleanPreviewString(codeEvidence["evidence_state"])
	completeAudit := evidenceState == "recorded" &&
		boolOnlyFromAny(codeEvidence["worker_dispatch_audit_recorded"]) &&
		boolOnlyFromAny(codeEvidence["codex_execution_plan_recorded"]) &&
		boolOnlyFromAny(codeEvidence["patch_prepare_audit_recorded"]) &&
		boolOnlyFromAny(codeEvidence["sanitized_result_recorded"])
	armingState := "blocked"
	armingReason := "agent_code_modification_audit_not_recorded"
	switch {
	case completeAudit:
		armingState = "ready_for_operator_review"
		armingReason = "sanitized_agent_code_modification_audit_ready_for_future_execution_review"
	case evidenceState == "waiting_for_worker":
		armingState = "waiting_for_worker"
		armingReason = "agent_code_modification_audit_waiting_for_worker"
	case boolOnlyFromAny(codeEvidence["has_code_modification_audit"]):
		armingState = "partial_audit"
		armingReason = "agent_code_modification_audit_incomplete"
	}
	missing := append([]string{}, stringSliceFromAny(codeEvidence["missing_audit_evidence"])...)
	if !completeAudit && !stringListContains(missing, "future_operator_execution_review") {
		missing = append(missing, "future_operator_execution_review")
	}
	return map[string]any{
		"mode":                           "redacted_agent_code_modification_execution_arming_plan",
		"arming_state":                   armingState,
		"arming_ready":                   armingState == "ready_for_operator_review",
		"arming_ready_reason":            armingReason,
		"audit_evidence_state":           evidenceState,
		"worker_dispatch_audit_recorded": boolOnlyFromAny(codeEvidence["worker_dispatch_audit_recorded"]),
		"codex_execution_plan_recorded":  boolOnlyFromAny(codeEvidence["codex_execution_plan_recorded"]),
		"patch_prepare_audit_recorded":   boolOnlyFromAny(codeEvidence["patch_prepare_audit_recorded"]),
		"terminal_audit_recorded":        boolOnlyFromAny(codeEvidence["sanitized_result_recorded"]),
		"source_checkout_performed":      false,
		"workspace_bound":                false,
		"branch_created":                 false,
		"patch_content_materialized":     false,
		"diff_materialized":              false,
		"file_patch_applied":             false,
		"tests_executed":                 false,
		"git_commit_created":             false,
		"git_push_performed":             false,
		"provider_review_created":        false,
		"commit_push_agent_invoked":      false,
		"external_call_made":             false,
		"repository_mutation_allowed":    false,
		"contains_source_remote_url":     false,
		"contains_workspace_path":        false,
		"contains_branch_name":           false,
		"contains_patch_content":         false,
		"contains_diff_content":          false,
		"contains_file_content":          false,
		"contains_test_output":           false,
		"contains_token":                 false,
		"required_controls":              []string{"agent_execute_approval", "runtime_verification", "source_remote_review", "workspace_binding_review", "branch_policy_review", "structured_patch_review", "test_plan_review", "commit_push_agent_review", "provider_review_reconciliation"},
		"required_evidence":              []string{"worker_dispatch_plan_audit", "codex_execution_plan_audit", "patch_prepare_audit", "terminal_tool_call_audit", "future_operator_execution_review"},
		"missing_evidence":               missing,
		"disabled_backends":              []string{"source_checkout", "workspace_bind", "branch_create", "file_patch_apply", "test_command_execute", "git_commit", "git_push", "pull_request_create", "commit_push_agent"},
		"suppressed_fields":              []string{"runtime_config", "environment_variables", "authorization_header", "source_remote_url", "repository_url", "workspace_path", "branch_name", "prompt_body", "tool_input", "tool_output", "patch_content", "diff_content", "file_content", "test_output", "command_output", "token", "api_key"},
		"message":                        "Agent code modification execution is only ready for future operator review; source checkout, branch creation, patch application, tests, commit, push, and provider review remain disabled.",
	}
}

package app

func agentWorkerToolInvocationReviewPlan(allowedToolNames []string, auditEvidence, armingPlan map[string]any) map[string]any {
	metadataReady := boolOnlyFromAny(armingPlan["metadata_ready"])
	allowlistReady := boolOnlyFromAny(armingPlan["allowlist_ready"])
	terminalAuditObserved := boolOnlyFromAny(armingPlan["terminal_audit_observed"])
	armingSuccessfulAuditRecorded := boolOnlyFromAny(armingPlan["successful_audit_recorded"])
	callbackWired := boolOnlyFromAny(armingPlan["result_callback_wired"])
	callbackObserved := boolOnlyFromAny(armingPlan["result_callback_observed"])
	armingReady := boolOnlyFromAny(armingPlan["arming_ready"])
	hasAudit := boolOnlyFromAny(auditEvidence["has_tool_call_audit"])
	auditState := cleanPreviewString(armingPlan["audit_state"])
	successfulSanitizedResultRecorded := terminalAuditObserved && armingSuccessfulAuditRecorded
	readyForOperatorReview := metadataReady && allowlistReady && hasAudit && callbackObserved && successfulSanitizedResultRecorded && armingReady

	reviewState := "blocked"
	reviewReason := "agent_tool_invocation_review_metadata_not_ready"
	switch {
	case readyForOperatorReview:
		reviewState = "ready_for_operator_review"
		reviewReason = "allowlisted_tool_invocation_preflight_ready_for_operator_review"
	case metadataReady && allowlistReady && hasAudit && callbackWired && terminalAuditObserved && !armingSuccessfulAuditRecorded:
		reviewState = auditState
		reviewReason = "agent_tool_call_audit_not_successful"
	case metadataReady && allowlistReady && hasAudit && callbackWired && !terminalAuditObserved:
		reviewState = "waiting_for_terminal_audit"
		reviewReason = "agent_tool_call_audit_not_terminal"
	case metadataReady && allowlistReady && callbackWired:
		reviewState = "audit_ready"
		reviewReason = "allowlisted_tool_invocation_audit_boundary_ready"
	case metadataReady && allowlistReady:
		reviewState = "callback_blocked"
		reviewReason = "agent_result_callback_not_wired"
	case metadataReady:
		reviewState = "allowlist_blocked"
		reviewReason = "agent_tool_allowlist_missing"
	}

	missing := []string{}
	if !metadataReady {
		missing = append(missing, "runtime_metadata")
	}
	if !allowlistReady {
		missing = append(missing, "tool_allowlist")
	}
	if !callbackWired {
		missing = append(missing, "result_callback")
	}
	if !hasAudit {
		missing = append(missing, "tool_call_audit_evidence")
	}
	if hasAudit && !terminalAuditObserved {
		missing = append(missing, "terminal_tool_call_audit")
	}
	if hasAudit && terminalAuditObserved && !armingSuccessfulAuditRecorded {
		missing = append(missing, "successful_tool_call_audit")
	}
	if hasAudit && !callbackObserved {
		missing = append(missing, "result_callback_observation")
	}
	if hasAudit && !successfulSanitizedResultRecorded {
		missing = append(missing, "sanitized_result_recording")
	}

	return map[string]any{
		"mode":                                 "redacted_agent_tool_invocation_review_plan",
		"review_state":                         reviewState,
		"review_ready":                         readyForOperatorReview,
		"review_ready_reason":                  reviewReason,
		"metadata_ready":                       metadataReady,
		"allowlist_ready":                      allowlistReady,
		"allowed_tool_count":                   len(allowedToolNames),
		"audit_evidence_observed":              hasAudit,
		"terminal_audit_observed":              terminalAuditObserved,
		"audit_state":                          auditState,
		"successful_audit_recorded":            armingSuccessfulAuditRecorded,
		"sanitized_result_recorded":            terminalAuditObserved,
		"successful_sanitized_result_recorded": successfulSanitizedResultRecorded,
		"result_callback_wired":                callbackWired,
		"result_callback_observed":             callbackObserved,
		"arming_ready_for_operator_review":     armingReady,
		"live_tool_invocation_allowed":         false,
		"tool_invocation_enabled":              false,
		"allowlisted_tool_invoked":             false,
		"tool_input_materialized":              false,
		"tool_output_recorded":                 false,
		"raw_tool_input_materialized":          false,
		"raw_tool_output_recorded":             false,
		"runtime_config_materialized":          false,
		"codex_cli_process_started":            false,
		"repository_mutation_allowed":          false,
		"external_call_made":                   false,
		"operator_review_recorded":             false,
		"allowed_tool_names":                   allowedToolNames,
		"required_review_fields":               []string{"operation_run_id", "agent_task_id", "tool_name", "tool_call_id", "allowlist_entry", "input_schema_key", "output_schema_key", "sanitization_status", "operator_review_status"},
		"required_operator_controls":           []string{"agent_execute_approval", "verified_runtime_metadata", "allowlisted_tool_review", "terminal_audit_review", "result_callback_review", "raw_io_redaction_review"},
		"missing_evidence":                     missing,
		"disabled_backends":                    []string{"worker_tool_invoke", "tool_input_materialization", "tool_output_recording", "codex_cli_process", "patch_apply", "repository_mutation", "provider_call"},
		"suppressed_fields":                    []string{"runtime_config", "environment_variables", "authorization_header", "workspace_path", "repository_url", "prompt_body", "tool_input", "tool_output", "raw_tool_input", "raw_tool_output", "patch_content", "diff_content", "file_content", "token", "api_key", "bearer_token"},
		"message":                              "Allowlisted tool invocation review is a redacted preflight only; live tool calls, raw tool I/O, Codex CLI, patch application, repository mutation, and provider calls remain disabled.",
	}
}

func agentWorkerToolExecutionArmingPlan(prerequisiteState string, allowedToolNames []string, auditEvidence, resultCallbackPlan map[string]any) map[string]any {
	metadataReady := prerequisiteState == "metadata_available"
	allowlistReady := len(allowedToolNames) > 0
	auditObserved := boolOnlyFromAny(auditEvidence["has_tool_call_audit"])
	auditTerminal := boolOnlyFromAny(auditEvidence["sanitized_result_recorded"])
	auditState := cleanPreviewString(auditEvidence["evidence_state"])
	successfulAuditRecorded := auditTerminal && auditState == "recorded"
	callbackWired := boolOnlyFromAny(resultCallbackPlan["callback_enabled"])
	callbackObserved := boolOnlyFromAny(resultCallbackPlan["result_written"])
	armingState := "blocked"
	armingReason := "agent_tool_execution_metadata_not_ready"
	switch {
	case metadataReady && allowlistReady && auditObserved && successfulAuditRecorded && callbackWired:
		armingState = "ready_for_operator_review"
		armingReason = "sanitized_agent_tool_audit_ready_for_future_invocation_review"
	case metadataReady && allowlistReady && auditObserved && auditTerminal && callbackWired && !successfulAuditRecorded:
		armingState = auditState
		armingReason = "agent_tool_call_audit_not_successful"
	case metadataReady && allowlistReady && auditObserved && callbackWired:
		armingState = "waiting_for_terminal_audit"
		armingReason = "agent_tool_call_audit_not_terminal"
	case metadataReady && allowlistReady && callbackWired:
		armingState = "audit_ready"
		armingReason = "agent_tool_audit_boundary_ready"
	case metadataReady && allowlistReady:
		armingState = "callback_blocked"
		armingReason = "agent_result_callback_not_wired"
	case metadataReady:
		armingState = "allowlist_blocked"
		armingReason = "agent_tool_allowlist_missing"
	}
	missing := []string{}
	if !metadataReady {
		missing = append(missing, "runtime_metadata")
	}
	if !allowlistReady {
		missing = append(missing, "tool_allowlist")
	}
	if !callbackWired {
		missing = append(missing, "result_callback")
	}
	if !auditObserved {
		missing = append(missing, "tool_call_audit_evidence")
	}
	if auditObserved && !auditTerminal {
		missing = append(missing, "terminal_tool_call_audit")
	}
	if auditObserved && auditTerminal && !successfulAuditRecorded {
		missing = append(missing, "successful_tool_call_audit")
	}
	return map[string]any{
		"mode":                        "redacted_agent_tool_execution_arming_plan",
		"arming_state":                armingState,
		"arming_ready":                armingState == "ready_for_operator_review",
		"arming_ready_reason":         armingReason,
		"metadata_ready":              metadataReady,
		"allowlist_ready":             allowlistReady,
		"allowed_tool_count":          len(allowedToolNames),
		"audit_evidence_observed":     auditObserved,
		"audit_state":                 auditState,
		"terminal_audit_observed":     auditTerminal,
		"successful_audit_recorded":   successfulAuditRecorded,
		"result_callback_wired":       callbackWired,
		"result_callback_observed":    callbackObserved,
		"tool_invocation_enabled":     false,
		"tool_invoked":                false,
		"allowlisted_tool_invoked":    false,
		"codex_cli_process_started":   false,
		"patch_applied":               false,
		"repository_mutation_allowed": false,
		"external_call_made":          false,
		"raw_tool_input_materialized": false,
		"raw_tool_output_recorded":    false,
		"runtime_config_materialized": false,
		"contains_runtime_config":     false,
		"contains_prompt_body":        false,
		"contains_tool_input":         false,
		"contains_tool_output":        false,
		"contains_patch_content":      false,
		"contains_diff_content":       false,
		"contains_token":              false,
		"required_controls":           []string{"agent_execute_approval", "verified_runtime_metadata", "tool_allowlist_review", "result_callback_audit", "operator_execution_review", "raw_io_redaction_review"},
		"required_evidence":           []string{"runtime_metadata", "tool_allowlist", "tool_call_audit_evidence", "terminal_tool_call_audit", "successful_tool_call_audit", "result_callback"},
		"missing_evidence":            missing,
		"allowed_tool_names":          allowedToolNames,
		"disabled_backends":           []string{"worker_tool_invoke", "codex_cli_process", "tool_input_materialization", "tool_output_recording", "patch_apply", "repository_mutation"},
		"suppressed_fields":           []string{"runtime_config", "environment_variables", "authorization_header", "workspace_path", "repository_url", "prompt_body", "tool_input", "tool_output", "patch_content", "diff_content", "file_content", "token", "api_key", "bearer_token"},
		"message":                     "Allowlisted tool execution is only ready for future operator review; live tool invocation, Codex CLI, patch application, repository mutation, and raw tool I/O remain disabled.",
	}
}

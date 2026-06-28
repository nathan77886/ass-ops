package app

import ()

func agentToolCallAuditEvidence(toolCalls []map[string]any) map[string]any {
	statusCounts := map[string]any{}
	toolCounts := map[string]any{}
	toolNames := []string{}
	queued, running, completed, failed, canceled, unknown, absent := 0, 0, 0, 0, 0, 0, 0
	items := make([]map[string]any, 0, len(toolCalls))
	for _, call := range toolCalls {
		status := cleanPreviewString(call["status"])
		if status == "" {
			status = "absent"
		}
		toolName := cleanPreviewString(call["tool_name"])
		if toolName == "" {
			toolName = "unknown"
		}
		if !stringInSlice(toolNames, toolName) {
			toolNames = append(toolNames, toolName)
		}
		statusCounts[status] = intFromAny(statusCounts[status], 0) + 1
		toolCounts[toolName] = intFromAny(toolCounts[toolName], 0) + 1
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
		case "absent":
			absent++
		default:
			unknown++
		}
		items = append(items, map[string]any{
			"tool_call_id":                call["id"],
			"operation_run_id":            call["operation_run_id"],
			"tool_name":                   toolName,
			"status":                      status,
			"started_at":                  call["started_at"],
			"finished_at":                 call["finished_at"],
			"created_at":                  call["created_at"],
			"updated_at":                  call["updated_at"],
			"input_included":              false,
			"output_included":             false,
			"raw_tool_output_recorded":    false,
			"raw_runtime_output_recorded": false,
			"secret_included":             false,
		})
	}
	operationCount := len(toolCalls)
	activeCount := queued + running
	terminalCount := completed + failed + canceled + unknown + absent
	evidenceState := "not_recorded"
	if operationCount > 0 {
		evidenceState = "waiting_for_worker"
		if activeCount == 0 {
			if failed > 0 && canceled > 0 {
				evidenceState = "mixed_failed"
			} else if failed > 0 {
				evidenceState = "failed"
			} else if canceled > 0 {
				evidenceState = "canceled"
			} else if absent > 0 {
				evidenceState = "absent"
			} else if unknown > 0 {
				evidenceState = "unknown"
			} else {
				evidenceState = "recorded"
			}
		}
	}
	return map[string]any{
		"mode":                        "agent_tool_call_audit_evidence",
		"evidence_state":              evidenceState,
		"tool_call_count":             operationCount,
		"queued_count":                queued,
		"running_count":               running,
		"completed_count":             completed,
		"failed_count":                failed,
		"canceled_count":              canceled,
		"unknown_count":               unknown,
		"absent_count":                absent,
		"active_count":                activeCount,
		"terminal_count":              terminalCount,
		"has_tool_call_audit":         operationCount > 0,
		"sanitized_result_recorded":   operationCount > 0 && activeCount == 0,
		"has_failures":                failed > 0,
		"has_cancellations":           canceled > 0,
		"has_unknown_status":          unknown > 0,
		"has_absent_status":           absent > 0,
		"tool_names":                  toolNames,
		"status_counts":               statusCounts,
		"tool_counts":                 toolCounts,
		"items":                       items,
		"external_call_made":          false,
		"tool_invocation_enabled":     false,
		"tool_invoked":                false,
		"repository_mutation_allowed": false,
		"raw_tool_output_recorded":    false,
		"raw_runtime_output_recorded": false,
		"raw_patch_recorded":          false,
		"raw_diff_recorded":           false,
		"input_included":              false,
		"output_included":             false,
		"secret_included":             false,
		"suppressed_fields":           []string{"runtime_config", "environment_variables", "authorization_header", "workspace_path", "repository_url", "prompt_body", "tool_input", "tool_output", "patch_content", "diff_content", "token", "api_key", "bearer_token"},
		"message":                     "Agent tool-call audit evidence records sanitized status metadata only; raw tool input, output, runtime config, patch, diff, and credentials remain suppressed.",
	}
}

func agentWorkerAllowedToolNames() []string {
	return []string{
		"context.generate",
		"runtime.check",
		"codex.execution.plan",
		"patch.prepare",
	}
}

func agentWorkerClaimPlan(prerequisiteState string) map[string]any {
	metadataReady := prerequisiteState == "metadata_available"
	blockedReasons := []string{"worker_node_not_claimed_yet", "idempotency_claim_pending"}
	if !metadataReady {
		blockedReasons = append(blockedReasons, "runtime_metadata_not_ready")
	}
	return map[string]any{
		"mode":                       "redacted_agent_worker_claim_plan",
		"claim_state":                "queued",
		"claim_ready":                true,
		"claim_ready_reason":         "worker_job_enqueued_for_audit_execution",
		"metadata_ready":             metadataReady,
		"worker_claim_enabled":       true,
		"worker_job_created":         true,
		"worker_node_claimed":        false,
		"operation_locked":           false,
		"idempotency_claimed":        false,
		"external_call_made":         false,
		"required_claim_fields":      []string{"operation_run_id", "agent_task_id", "agent_plan_id", "required_capability", "claim_attempt", "claimed_by", "claimed_at"},
		"suppressed_fields":          []string{"runtime_config", "environment_variables", "worker_secret", "authorization_header", "workspace_path", "prompt_body"},
		"blocked_reasons":            blockedReasons,
		"required_worker_capability": "ai",
		"message":                    "Agent execution creates a worker job for audit processing; runtime secrets and prompt bodies are still not materialized in the claim plan.",
	}
}

func agentWorkerToolInvocationPlan(allowedToolNames []string) map[string]any {
	return map[string]any{
		"mode":                        "redacted_agent_tool_invocation_plan",
		"invocation_state":            "blocked",
		"invocation_ready":            false,
		"invocation_ready_reason":     "agent_tool_invocation_backend_disabled",
		"tool_invocation_enabled":     false,
		"tool_invoked":                false,
		"external_call_made":          false,
		"repository_mutation_allowed": false,
		"contains_tool_input":         false,
		"contains_tool_output":        false,
		"allowed_tool_names":          allowedToolNames,
		"required_invocation_fields":  []string{"operation_run_id", "agent_task_id", "tool_name", "tool_call_id", "input_schema_key", "output_schema_key", "started_at", "finished_at"},
		"suppressed_fields":           []string{"runtime_config", "environment_variables", "authorization_header", "workspace_path", "repository_url", "prompt_body", "tool_input", "tool_output", "patch_content", "diff_content", "token"},
		"blocked_reasons":             []string{"tool_invocation_not_armed", "tool_input_materialization_disabled", "tool_output_recording_disabled"},
		"message":                     "Agent tool invocation is audit-only; allowlisted tool names are recorded without materializing tool input or output.",
	}
}

func agentWorkerResultCallbackPlan(auditEvidence map[string]any) map[string]any {
	resultObserved := boolOnlyFromAny(auditEvidence["has_tool_call_audit"])
	resultRecorded := boolOnlyFromAny(auditEvidence["sanitized_result_recorded"])
	callbackState := "planned"
	readyReason := "sanitized_agent_audit_result_callback_wired"
	blockedReasons := []string{"sanitized_tool_result_not_recorded_yet", "canonical_asset_sync_pending"}
	if resultObserved {
		callbackState = cleanPreviewString(auditEvidence["evidence_state"])
		readyReason = "sanitized_agent_audit_result_observed"
		blockedReasons = []string{"canonical_asset_sync_pending"}
		if !resultRecorded {
			blockedReasons = append(blockedReasons, "agent_tool_call_audit_not_terminal")
		}
	}
	return map[string]any{
		"mode":                           "redacted_agent_result_callback_plan",
		"callback_state":                 callbackState,
		"callback_ready":                 true,
		"callback_ready_reason":          readyReason,
		"callback_enabled":               true,
		"callback_scope":                 "sanitized_audit_status_only",
		"result_written":                 resultRecorded,
		"operation_log_written":          resultRecorded,
		"agent_task_status_written":      false,
		"tool_call_status_written":       resultRecorded,
		"canonical_asset_sync_queued":    false,
		"status_snapshot_write_eligible": false,
		"status_snapshot_written":        false,
		"raw_tool_output_recorded":       false,
		"raw_runtime_output_recorded":    false,
		"raw_patch_recorded":             false,
		"raw_diff_recorded":              false,
		"contains_tool_output":           false,
		"contains_runtime_config":        false,
		"requires_human_result_review":   true,
		"tool_call_audit_evidence":       auditEvidence,
		"required_result_fields":         []string{"operation_run_id", "agent_task_id", "tool_call_id", "tool_name", "status", "sanitization_status", "started_at", "finished_at"},
		"suppressed_fields":              []string{"runtime_config", "environment_variables", "authorization_header", "workspace_path", "repository_url", "prompt_body", "tool_input", "tool_output", "patch_content", "diff_content", "token", "api_key", "bearer_token"},
		"blocked_reasons":                blockedReasons,
		"message":                        "Agent worker completion records sanitized audit status metadata; raw tool output, runtime output, patch, diff, and config material remain suppressed.",
	}
}

package app

import (
	"fmt"
	"strings"
)

func agentCodeModificationEvidence(auditEvidence map[string]any) map[string]any {
	toolCounts := mapFromAny(auditEvidence["tool_counts"])
	hasAudit := boolOnlyFromAny(auditEvidence["has_tool_call_audit"])
	activeCount := intFromAny(auditEvidence["active_count"], 0)
	hasCodexPlan := intFromAny(toolCounts["codex.execution.plan"], 0) > 0
	hasPatchPrepare := intFromAny(toolCounts["patch.prepare"], 0) > 0
	hasWorkerDispatch := intFromAny(toolCounts["worker.dispatch.plan"], 0) > 0
	auditState := cleanPreviewString(auditEvidence["evidence_state"])
	terminalAuditRecorded := boolOnlyFromAny(auditEvidence["sanitized_result_recorded"])
	sanitizedCodeResultRecorded := auditState == "recorded" && terminalAuditRecorded && hasWorkerDispatch && hasCodexPlan && hasPatchPrepare
	evidenceState := "not_recorded"
	switch {
	case !hasAudit:
		evidenceState = "not_recorded"
	case activeCount > 0:
		evidenceState = "waiting_for_worker"
	case auditState == "failed" || auditState == "canceled" || auditState == "mixed_failed" || auditState == "unknown" || auditState == "absent":
		evidenceState = auditState
	case sanitizedCodeResultRecorded:
		evidenceState = "recorded"
	case terminalAuditRecorded:
		evidenceState = "partial_recorded"
	default:
		evidenceState = "blocked"
	}
	missing := []string{}
	if !hasWorkerDispatch {
		missing = append(missing, "worker_dispatch_plan_audit")
	}
	if !hasCodexPlan {
		missing = append(missing, "codex_execution_plan_audit")
	}
	if !hasPatchPrepare {
		missing = append(missing, "patch_prepare_audit")
	}
	if !terminalAuditRecorded {
		missing = append(missing, "terminal_tool_call_audit")
	}
	if !sanitizedCodeResultRecorded {
		missing = append(missing, "sanitized_code_modification_result")
	}
	return map[string]any{
		"mode":                              "redacted_agent_code_modification_evidence",
		"evidence_state":                    evidenceState,
		"tool_call_audit_state":             auditState,
		"has_code_modification_audit":       hasAudit,
		"sanitized_result_recorded":         sanitizedCodeResultRecorded,
		"worker_dispatch_audit_recorded":    hasWorkerDispatch,
		"codex_execution_plan_recorded":     hasCodexPlan,
		"patch_prepare_audit_recorded":      hasPatchPrepare,
		"completed_tool_call_count":         intFromAny(auditEvidence["completed_count"], 0),
		"failed_tool_call_count":            intFromAny(auditEvidence["failed_count"], 0),
		"active_tool_call_count":            activeCount,
		"terminal_tool_call_count":          intFromAny(auditEvidence["terminal_count"], 0),
		"required_audit_evidence":           []string{"worker_dispatch_plan_audit", "codex_execution_plan_audit", "patch_prepare_audit", "terminal_tool_call_audit", "sanitized_code_modification_result"},
		"missing_audit_evidence":            missing,
		"execution_enabled":                 false,
		"mutation_enabled":                  false,
		"external_call_made":                false,
		"repository_mutation_allowed":       false,
		"source_checkout_performed":         false,
		"workspace_bound":                   false,
		"branch_created":                    false,
		"patch_content_materialized":        false,
		"diff_materialized":                 false,
		"file_patch_applied":                false,
		"tests_executed":                    false,
		"git_commit_created":                false,
		"git_push_performed":                false,
		"pull_request_created":              false,
		"commit_push_agent_invoked":         false,
		"raw_patch_recorded":                false,
		"raw_diff_recorded":                 false,
		"raw_file_content_recorded":         false,
		"raw_command_output_recorded":       false,
		"raw_test_output_recorded":          false,
		"contains_token":                    false,
		"contains_remote_url":               false,
		"contains_branch_name":              false,
		"contains_workspace_path":           false,
		"contains_patch_content":            false,
		"contains_diff_content":             false,
		"contains_file_content":             false,
		"suppressed_fields":                 []string{"runtime_config", "environment_variables", "authorization_header", "source_remote_url", "repository_url", "workspace_path", "branch_name", "prompt_body", "tool_input", "tool_output", "patch_content", "diff_content", "file_content", "test_output", "command_output", "token", "api_key"},
		"tool_call_audit_evidence_attached": hasAudit,
		"message":                           "Agent code modification evidence is reconciled from sanitized audit rows only; source checkout, patch content, diff, tests, commit, push, and provider review remain disabled.",
	}
}

func agentCodeModificationResultRecordingPlan(evidenceRows ...map[string]any) map[string]any {
	evidence := map[string]any{}
	if len(evidenceRows) > 0 {
		evidence = evidenceRows[0]
	}
	recordingState := "blocked"
	recordingEnabled := false
	resultWritten := false
	readyReason := "agent_code_modification_result_not_recorded"
	if boolOnlyFromAny(evidence["has_code_modification_audit"]) {
		recordingState = cleanPreviewString(evidence["evidence_state"])
		recordingEnabled = boolOnlyFromAny(evidence["sanitized_result_recorded"])
		resultWritten = boolOnlyFromAny(evidence["sanitized_result_recorded"])
		if resultWritten {
			readyReason = "sanitized_agent_code_modification_audit_observed"
		} else {
			readyReason = "agent_code_modification_audit_incomplete"
		}
	}
	return map[string]any{
		"mode":                         "redacted_agent_code_modification_result_recording_plan",
		"recording_state":              recordingState,
		"recording_ready_reason":       readyReason,
		"recording_enabled":            recordingEnabled,
		"result_written":               resultWritten,
		"operation_log_written":        false,
		"patch_artifact_written":       false,
		"diff_artifact_written":        false,
		"test_result_written":          false,
		"commit_record_written":        false,
		"push_record_written":          false,
		"pr_record_written":            false,
		"raw_patch_recorded":           false,
		"raw_diff_recorded":            false,
		"raw_file_content_recorded":    false,
		"raw_command_output_recorded":  false,
		"raw_test_output_recorded":     false,
		"contains_token":               false,
		"contains_remote_url":          false,
		"contains_branch_name":         false,
		"contains_patch_content":       false,
		"contains_diff_content":        false,
		"contains_file_content":        false,
		"requires_sanitization":        true,
		"requires_human_result_review": true,
		"code_modification_evidence":   evidence,
		"suppressed_fields": []string{
			"source_remote_url",
			"workspace_path",
			"branch_name",
			"patch_content",
			"diff_content",
			"file_content",
			"test_output",
			"command_output",
			"token",
		},
		"message": "No code modification result is persisted until sanitized patch, diff, test, commit, push, and provider review records are wired.",
	}
}

func agentWorkerDispatchPlan(runtime map[string]any, auditEvidenceRows ...map[string]any) map[string]any {
	cliReadiness := agentCodexCLIReadiness(runtime)
	runtimeReady := strings.TrimSpace(fmt.Sprint(cliReadiness["readiness"])) == "metadata_ready"
	dispatchPrerequisite := "metadata_blocked"
	if runtimeReady {
		dispatchPrerequisite = "metadata_available"
	}
	claimPlan := agentWorkerClaimPlan(dispatchPrerequisite)
	allowedToolNames := agentWorkerAllowedToolNames()
	toolInvocationPlan := agentWorkerToolInvocationPlan(allowedToolNames)
	auditEvidence := map[string]any{}
	if len(auditEvidenceRows) > 0 {
		auditEvidence = auditEvidenceRows[0]
	}
	resultCallbackPlan := agentWorkerResultCallbackPlan(auditEvidence)
	toolExecutionArmingPlan := agentWorkerToolExecutionArmingPlan(dispatchPrerequisite, allowedToolNames, auditEvidence, resultCallbackPlan)
	toolInvocationReviewPlan := agentWorkerToolInvocationReviewPlan(allowedToolNames, auditEvidence, toolExecutionArmingPlan)
	resultRecorded := boolOnlyFromAny(auditEvidence["sanitized_result_recorded"])
	return map[string]any{
		"mode":                           "redacted_agent_worker_dispatch_plan",
		"dispatch_state":                 "audit_queued",
		"dispatch_ready":                 true,
		"dispatch_ready_reason":          "agent_worker_audit_job_enqueued",
		"prerequisite_state":             dispatchPrerequisite,
		"execution_enabled":              false,
		"audit_worker_execution_enabled": true,
		"worker_claim_enabled":           true,
		"worker_job_created":             true,
		"worker_node_claimed":            false,
		"tool_invocation_enabled":        false,
		"tool_invoked":                   false,
		"external_call_made":             false,
		"repository_mutation_allowed":    false,
		"result_callback_enabled":        true,
		"result_written":                 resultRecorded,
		"context_snapshot_materialized":  true,
		"tool_call_audit_evidence":       auditEvidence,
		"worker_claim_plan":              claimPlan,
		"tool_invocation_plan":           toolInvocationPlan,
		"tool_execution_arming_plan":     toolExecutionArmingPlan,
		"tool_invocation_review_plan":    toolInvocationReviewPlan,
		"result_callback_plan":           resultCallbackPlan,
		"requires_operation_run":         true,
		"requires_approved_plan":         true,
		"requires_worker_capability":     true,
		"requires_runtime_verification":  true,
		"requires_tool_allowlist":        true,
		"requires_result_callback":       true,
		"contains_token":                 false,
		"contains_runtime_config":        false,
		"contains_prompt_body":           false,
		"contains_tool_input":            false,
		"contains_tool_output":           false,
		"contains_workspace_path":        false,
		"dispatch_boundary_redacted":     true,
		"blocked_reasons": []string{
			"tool_invocation_not_armed",
			"codex_cli_execution_backend_disabled",
			"repository_mutation_not_armed",
		},
		"required_controls": []string{
			"agent_execute_approval",
			"worker_capability_ai",
			"runtime_verification",
			"tool_allowlist",
			"context_snapshot",
			"result_callback_audit",
			"human_result_review",
		},
		"required_worker_capabilities": []string{
			"ai",
			"context.read",
			"agent.audit",
		},
		"allowed_tool_names": allowedToolNames,
		"disabled_backends": []string{
			"worker_tool_invoke",
			"codex_cli_process",
			"file_patch_apply",
			"git_commit",
			"git_push",
			"pull_request_create",
		},
		"suppressed_fields": []string{
			"runtime_config",
			"environment_variables",
			"authorization_header",
			"workspace_path",
			"repository_url",
			"prompt_body",
			"tool_input",
			"tool_output",
			"patch_content",
			"diff_content",
			"token",
			"api_key",
			"bearer_token",
		},
		"dispatch_sequence": []string{
			"verify_operation_run",
			"verify_approved_plan",
			"select_ai_worker",
			"bind_runtime_metadata",
			"materialize_context_snapshot",
			"invoke_allowlisted_tools",
			"record_tool_results",
			"mark_operation_complete",
		},
		"runtime_readiness": cliReadiness,
		"message":           "Agent worker dispatch now enqueues a real audit worker job and sanitized result callback; allowlisted tool invocation, Codex CLI, patch, git, and pull request mutations remain disabled.",
	}
}

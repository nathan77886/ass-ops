package app

import (
	"strings"
	"testing"
)

func assertAgentWorkerDispatchSubplansSafe(t *testing.T, got map[string]any) {
	t.Helper()
	claimPlan := mapFromAny(got["worker_claim_plan"])
	if claimPlan["mode"] != "redacted_agent_worker_claim_plan" ||
		claimPlan["claim_state"] != "queued" ||
		claimPlan["claim_ready"] != true ||
		claimPlan["claim_ready_reason"] != "worker_job_enqueued_for_audit_execution" ||
		claimPlan["worker_claim_enabled"] != true ||
		claimPlan["worker_job_created"] != true ||
		claimPlan["worker_node_claimed"] != false ||
		claimPlan["operation_locked"] != false ||
		claimPlan["idempotency_claimed"] != false ||
		claimPlan["external_call_made"] != false {
		t.Fatalf("worker claim plan should expose queued audit job without secrets: %#v", claimPlan)
	}
	if got["prerequisite_state"] == "metadata_available" && claimPlan["metadata_ready"] != true {
		t.Fatalf("metadata-available dispatch should mark claim metadata ready: %#v", claimPlan)
	}
	if got["prerequisite_state"] == "metadata_blocked" && !containsString(stringSliceFromAny(claimPlan["blocked_reasons"]), "runtime_metadata_not_ready") {
		t.Fatalf("metadata-blocked dispatch should report runtime metadata blocker: %#v", claimPlan)
	}
	for _, field := range []string{"operation_run_id", "agent_task_id", "agent_plan_id", "required_capability", "claim_attempt", "claimed_by", "claimed_at"} {
		if !containsString(stringSliceFromAny(claimPlan["required_claim_fields"]), field) {
			t.Fatalf("worker claim required fields missing %q: %#v", field, claimPlan["required_claim_fields"])
		}
	}
	for _, field := range []string{"runtime_config", "environment_variables", "worker_secret", "authorization_header", "workspace_path", "prompt_body"} {
		if !containsString(stringSliceFromAny(claimPlan["suppressed_fields"]), field) {
			t.Fatalf("worker claim suppressed_fields missing %q: %#v", field, claimPlan["suppressed_fields"])
		}
	}
	for _, reason := range []string{"worker_node_not_claimed_yet", "idempotency_claim_pending"} {
		if !containsString(stringSliceFromAny(claimPlan["blocked_reasons"]), reason) {
			t.Fatalf("worker claim blocked reasons missing %q: %#v", reason, claimPlan["blocked_reasons"])
		}
	}

	toolPlan := mapFromAny(got["tool_invocation_plan"])
	if toolPlan["mode"] != "redacted_agent_tool_invocation_plan" ||
		toolPlan["invocation_state"] != "blocked" ||
		toolPlan["invocation_ready"] != false ||
		toolPlan["invocation_ready_reason"] != "agent_tool_invocation_backend_disabled" ||
		toolPlan["tool_invocation_enabled"] != false ||
		toolPlan["tool_invoked"] != false ||
		toolPlan["external_call_made"] != false ||
		toolPlan["repository_mutation_allowed"] != false ||
		toolPlan["contains_tool_input"] != false ||
		toolPlan["contains_tool_output"] != false {
		t.Fatalf("tool invocation plan should stay disabled and redacted: %#v", toolPlan)
	}
	for _, tool := range []string{"context.generate", "runtime.check", "codex.execution.plan", "patch.prepare"} {
		if !containsString(stringSliceFromAny(toolPlan["allowed_tool_names"]), tool) {
			t.Fatalf("tool invocation allowed tools missing %q: %#v", tool, toolPlan["allowed_tool_names"])
		}
	}
	for _, field := range []string{"operation_run_id", "agent_task_id", "tool_name", "tool_call_id", "input_schema_key", "output_schema_key", "started_at", "finished_at"} {
		if !containsString(stringSliceFromAny(toolPlan["required_invocation_fields"]), field) {
			t.Fatalf("tool invocation required fields missing %q: %#v", field, toolPlan["required_invocation_fields"])
		}
	}
	for _, field := range []string{"runtime_config", "environment_variables", "authorization_header", "workspace_path", "repository_url", "prompt_body", "tool_input", "tool_output", "patch_content", "diff_content", "token"} {
		if !containsString(stringSliceFromAny(toolPlan["suppressed_fields"]), field) {
			t.Fatalf("tool invocation suppressed_fields missing %q: %#v", field, toolPlan["suppressed_fields"])
		}
	}
	for _, reason := range []string{"tool_invocation_not_armed", "tool_input_materialization_disabled", "tool_output_recording_disabled"} {
		if !containsString(stringSliceFromAny(toolPlan["blocked_reasons"]), reason) {
			t.Fatalf("tool invocation blocked reasons missing %q: %#v", reason, toolPlan["blocked_reasons"])
		}
	}
	armingPlan := mapFromAny(got["tool_execution_arming_plan"])
	if armingPlan["mode"] != "redacted_agent_tool_execution_arming_plan" ||
		armingPlan["tool_invocation_enabled"] != false ||
		armingPlan["tool_invoked"] != false ||
		armingPlan["allowlisted_tool_invoked"] != false ||
		armingPlan["codex_cli_process_started"] != false ||
		armingPlan["patch_applied"] != false ||
		armingPlan["repository_mutation_allowed"] != false ||
		armingPlan["external_call_made"] != false ||
		armingPlan["raw_tool_input_materialized"] != false ||
		armingPlan["raw_tool_output_recorded"] != false ||
		armingPlan["runtime_config_materialized"] != false ||
		armingPlan["contains_runtime_config"] != false ||
		armingPlan["contains_prompt_body"] != false ||
		armingPlan["contains_tool_input"] != false ||
		armingPlan["contains_tool_output"] != false ||
		armingPlan["contains_patch_content"] != false ||
		armingPlan["contains_diff_content"] != false ||
		armingPlan["contains_token"] != false {
		t.Fatalf("tool execution arming plan should stay disabled and redacted: %#v", armingPlan)
	}
	if got["prerequisite_state"] == "metadata_available" && armingPlan["metadata_ready"] != true {
		t.Fatalf("metadata-available dispatch should mark tool arming metadata ready: %#v", armingPlan)
	}
	if got["prerequisite_state"] == "metadata_blocked" && (armingPlan["metadata_ready"] != false || armingPlan["arming_state"] != "blocked") {
		t.Fatalf("metadata-blocked dispatch should block tool arming: %#v", armingPlan)
	}
	for _, tool := range []string{"context.generate", "runtime.check", "codex.execution.plan", "patch.prepare"} {
		if !containsString(stringSliceFromAny(armingPlan["allowed_tool_names"]), tool) {
			t.Fatalf("tool arming allowed tool missing %q: %#v", tool, armingPlan["allowed_tool_names"])
		}
	}
	for _, control := range []string{"agent_execute_approval", "verified_runtime_metadata", "tool_allowlist_review", "result_callback_audit", "operator_execution_review", "raw_io_redaction_review"} {
		if !containsString(stringSliceFromAny(armingPlan["required_controls"]), control) {
			t.Fatalf("tool arming required control missing %q: %#v", control, armingPlan["required_controls"])
		}
	}
	for _, field := range []string{"runtime_metadata", "tool_allowlist", "tool_call_audit_evidence", "terminal_tool_call_audit", "successful_tool_call_audit", "result_callback"} {
		if !containsString(stringSliceFromAny(armingPlan["required_evidence"]), field) {
			t.Fatalf("tool arming required evidence missing %q: %#v", field, armingPlan["required_evidence"])
		}
	}
	for _, backend := range []string{"worker_tool_invoke", "codex_cli_process", "tool_input_materialization", "tool_output_recording", "patch_apply", "repository_mutation"} {
		if !containsString(stringSliceFromAny(armingPlan["disabled_backends"]), backend) {
			t.Fatalf("tool arming disabled backend missing %q: %#v", backend, armingPlan["disabled_backends"])
		}
	}
	for _, field := range []string{"runtime_config", "environment_variables", "authorization_header", "workspace_path", "repository_url", "prompt_body", "tool_input", "tool_output", "patch_content", "diff_content", "file_content", "token", "api_key", "bearer_token"} {
		if !containsString(stringSliceFromAny(armingPlan["suppressed_fields"]), field) {
			t.Fatalf("tool arming suppressed field missing %q: %#v", field, armingPlan["suppressed_fields"])
		}
	}

	reviewPlan := mapFromAny(got["tool_invocation_review_plan"])
	if reviewPlan["mode"] != "redacted_agent_tool_invocation_review_plan" ||
		reviewPlan["live_tool_invocation_allowed"] != false ||
		reviewPlan["tool_invocation_enabled"] != false ||
		reviewPlan["allowlisted_tool_invoked"] != false ||
		reviewPlan["tool_input_materialized"] != false ||
		reviewPlan["tool_output_recorded"] != false ||
		reviewPlan["raw_tool_input_materialized"] != false ||
		reviewPlan["raw_tool_output_recorded"] != false ||
		reviewPlan["runtime_config_materialized"] != false ||
		reviewPlan["codex_cli_process_started"] != false ||
		reviewPlan["repository_mutation_allowed"] != false ||
		reviewPlan["external_call_made"] != false ||
		reviewPlan["operator_review_recorded"] != false {
		t.Fatalf("tool invocation review plan should stay disabled and redacted: %#v", reviewPlan)
	}
	if got["prerequisite_state"] == "metadata_available" && reviewPlan["metadata_ready"] != true {
		t.Fatalf("metadata-available dispatch should mark tool invocation review metadata ready: %#v", reviewPlan)
	}
	if got["prerequisite_state"] == "metadata_blocked" && (reviewPlan["metadata_ready"] != false || reviewPlan["review_state"] != "blocked") {
		t.Fatalf("metadata-blocked dispatch should block tool invocation review: %#v", reviewPlan)
	}
	for _, tool := range []string{"context.generate", "runtime.check", "codex.execution.plan", "patch.prepare"} {
		if !containsString(stringSliceFromAny(reviewPlan["allowed_tool_names"]), tool) {
			t.Fatalf("tool invocation review allowed tool missing %q: %#v", tool, reviewPlan["allowed_tool_names"])
		}
	}
	for _, field := range []string{"operation_run_id", "agent_task_id", "tool_name", "tool_call_id", "allowlist_entry", "input_schema_key", "output_schema_key", "sanitization_status", "operator_review_status"} {
		if !containsString(stringSliceFromAny(reviewPlan["required_review_fields"]), field) {
			t.Fatalf("tool invocation review required field missing %q: %#v", field, reviewPlan["required_review_fields"])
		}
	}
	for _, control := range []string{"agent_execute_approval", "verified_runtime_metadata", "allowlisted_tool_review", "terminal_audit_review", "result_callback_review", "raw_io_redaction_review"} {
		if !containsString(stringSliceFromAny(reviewPlan["required_operator_controls"]), control) {
			t.Fatalf("tool invocation review operator control missing %q: %#v", control, reviewPlan["required_operator_controls"])
		}
	}
	for _, backend := range []string{"worker_tool_invoke", "tool_input_materialization", "tool_output_recording", "codex_cli_process", "patch_apply", "repository_mutation", "provider_call"} {
		if !containsString(stringSliceFromAny(reviewPlan["disabled_backends"]), backend) {
			t.Fatalf("tool invocation review disabled backend missing %q: %#v", backend, reviewPlan["disabled_backends"])
		}
	}
	for _, field := range []string{"runtime_config", "environment_variables", "authorization_header", "workspace_path", "repository_url", "prompt_body", "tool_input", "tool_output", "raw_tool_input", "raw_tool_output", "patch_content", "diff_content", "file_content", "token", "api_key", "bearer_token"} {
		if !containsString(stringSliceFromAny(reviewPlan["suppressed_fields"]), field) {
			t.Fatalf("tool invocation review suppressed field missing %q: %#v", field, reviewPlan["suppressed_fields"])
		}
	}

	callbackPlan := mapFromAny(got["result_callback_plan"])
	evidence := mapFromAny(got["tool_call_audit_evidence"])
	hasAuditEvidence := boolOnlyFromAny(evidence["has_tool_call_audit"])
	successfulAuditRecorded := cleanPreviewString(evidence["evidence_state"]) == "recorded" && boolOnlyFromAny(evidence["sanitized_result_recorded"])
	if hasAuditEvidence && successfulAuditRecorded && got["prerequisite_state"] == "metadata_available" {
		if armingPlan["arming_state"] != "ready_for_operator_review" ||
			armingPlan["arming_ready"] != true ||
			armingPlan["audit_evidence_observed"] != true ||
			armingPlan["terminal_audit_observed"] != true ||
			armingPlan["successful_audit_recorded"] != true ||
			armingPlan["result_callback_wired"] != true {
			t.Fatalf("terminal audit evidence should make tool arming ready only for operator review: %#v", armingPlan)
		}
	} else if armingPlan["arming_ready"] == true {
		t.Fatalf("tool arming cannot be ready without metadata and terminal audit evidence: %#v", armingPlan)
	}
	if hasAuditEvidence && successfulAuditRecorded && got["prerequisite_state"] == "metadata_available" {
		if reviewPlan["review_state"] != "ready_for_operator_review" ||
			reviewPlan["review_ready"] != true ||
			reviewPlan["audit_evidence_observed"] != true ||
			reviewPlan["terminal_audit_observed"] != true ||
			reviewPlan["successful_audit_recorded"] != true ||
			reviewPlan["result_callback_wired"] != true ||
			len(stringSliceFromAny(reviewPlan["missing_evidence"])) != 0 {
			t.Fatalf("terminal audit evidence should make tool invocation review ready only for operator review: %#v", reviewPlan)
		}
	} else if reviewPlan["review_ready"] == true {
		t.Fatalf("tool invocation review cannot be ready without metadata, terminal audit, and callback evidence: %#v", reviewPlan)
	}
	if callbackPlan["mode"] != "redacted_agent_result_callback_plan" ||
		callbackPlan["callback_ready"] != true ||
		callbackPlan["callback_enabled"] != true ||
		callbackPlan["callback_scope"] != "sanitized_audit_status_only" ||
		callbackPlan["agent_task_status_written"] != false ||
		callbackPlan["canonical_asset_sync_queued"] != false ||
		callbackPlan["status_snapshot_write_eligible"] != false ||
		callbackPlan["status_snapshot_written"] != false ||
		callbackPlan["status_snapshot_written"] != callbackPlan["status_snapshot_write_eligible"] ||
		callbackPlan["raw_tool_output_recorded"] != false ||
		callbackPlan["raw_runtime_output_recorded"] != false ||
		callbackPlan["raw_patch_recorded"] != false ||
		callbackPlan["raw_diff_recorded"] != false ||
		callbackPlan["contains_tool_output"] != false ||
		callbackPlan["contains_runtime_config"] != false ||
		callbackPlan["requires_human_result_review"] != true {
		t.Fatalf("result callback plan should stay disabled and redacted: %#v", callbackPlan)
	}
	if hasAuditEvidence {
		if callbackPlan["callback_state"] != evidence["evidence_state"] ||
			callbackPlan["callback_ready_reason"] != "sanitized_agent_audit_result_observed" {
			t.Fatalf("result callback plan should reflect sanitized audit evidence only: callback=%#v evidence=%#v", callbackPlan, evidence)
		}
		resultRecorded := boolOnlyFromAny(evidence["sanitized_result_recorded"])
		if callbackPlan["result_written"] != resultRecorded ||
			callbackPlan["operation_log_written"] != resultRecorded ||
			callbackPlan["tool_call_status_written"] != resultRecorded {
			t.Fatalf("result callback plan should only write terminal sanitized audit result: callback=%#v evidence=%#v", callbackPlan, evidence)
		}
	} else if callbackPlan["callback_state"] != "planned" ||
		callbackPlan["callback_ready_reason"] != "sanitized_agent_audit_result_callback_wired" ||
		callbackPlan["result_written"] != false ||
		callbackPlan["operation_log_written"] != false ||
		callbackPlan["tool_call_status_written"] != false {
		t.Fatalf("result callback plan without evidence should stay planned and unwritten: %#v", callbackPlan)
	}
	for _, field := range []string{"operation_run_id", "agent_task_id", "tool_call_id", "tool_name", "status", "sanitization_status", "started_at", "finished_at"} {
		if !containsString(stringSliceFromAny(callbackPlan["required_result_fields"]), field) {
			t.Fatalf("result callback required fields missing %q: %#v", field, callbackPlan["required_result_fields"])
		}
	}
	for _, field := range []string{"runtime_config", "environment_variables", "authorization_header", "workspace_path", "repository_url", "prompt_body", "tool_input", "tool_output", "patch_content", "diff_content", "token"} {
		if !containsString(stringSliceFromAny(callbackPlan["suppressed_fields"]), field) {
			t.Fatalf("result callback suppressed_fields missing %q: %#v", field, callbackPlan["suppressed_fields"])
		}
	}
	expectedReasons := []string{"sanitized_tool_result_not_recorded_yet", "canonical_asset_sync_pending"}
	if hasAuditEvidence {
		expectedReasons = []string{"canonical_asset_sync_pending"}
	}
	for _, reason := range expectedReasons {
		if !containsString(stringSliceFromAny(callbackPlan["blocked_reasons"]), reason) {
			t.Fatalf("result callback blocked reasons missing %q: %#v", reason, callbackPlan["blocked_reasons"])
		}
	}
	message := cleanPreviewString(callbackPlan["message"])
	if !strings.Contains(message, "sanitized audit status") || !strings.Contains(message, "remain suppressed") {
		t.Fatalf("result callback message should describe sanitized callback without raw output: %#v", callbackPlan["message"])
	}
}

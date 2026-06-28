package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAgentWorkerDispatchPlanReconcilesToolCallAuditEvidence(t *testing.T) {
	evidence := agentToolCallAuditEvidence([]map[string]any{
		{"id": "call-runtime", "operation_run_id": "op-1", "tool_name": "runtime.check", "status": "completed", "input": map[string]any{"token": "do-not-serialize"}, "output": map[string]any{"raw": "actual tool output"}},
		{"id": "call-patch", "operation_run_id": "op-1", "tool_name": "patch.prepare", "status": "completed"},
	})
	if evidence["evidence_state"] != "recorded" ||
		evidence["has_tool_call_audit"] != true ||
		evidence["sanitized_result_recorded"] != true ||
		intFromAny(evidence["tool_call_count"], 0) != 2 ||
		evidence["input_included"] != false ||
		evidence["output_included"] != false ||
		evidence["raw_tool_output_recorded"] != false ||
		evidence["secret_included"] != false {
		t.Fatalf("tool call audit evidence = %#v", evidence)
	}
	got := agentWorkerDispatchPlan(map[string]any{
		"name":         "Demo Codex",
		"runtime_type": "codex-cli",
		"codex_binary": "codex",
		"status":       "verified",
	}, evidence)
	if got["result_written"] != true ||
		got["tool_invocation_enabled"] != false ||
		got["tool_invoked"] != false ||
		got["external_call_made"] != false ||
		got["repository_mutation_allowed"] != false {
		t.Fatalf("dispatch plan should reflect audit evidence without enabling tools: %#v", got)
	}
	armingPlan := mapFromAny(got["tool_execution_arming_plan"])
	if armingPlan["arming_state"] != "ready_for_operator_review" ||
		armingPlan["arming_ready"] != true ||
		armingPlan["audit_evidence_observed"] != true ||
		armingPlan["terminal_audit_observed"] != true ||
		armingPlan["result_callback_observed"] != true ||
		armingPlan["tool_invocation_enabled"] != false ||
		armingPlan["tool_invoked"] != false ||
		armingPlan["raw_tool_output_recorded"] != false ||
		armingPlan["contains_tool_input"] != false ||
		armingPlan["contains_tool_output"] != false {
		t.Fatalf("tool execution arming should reconcile audit evidence without enabling invocation: %#v", armingPlan)
	}
	reviewPlan := mapFromAny(got["tool_invocation_review_plan"])
	if reviewPlan["review_state"] != "ready_for_operator_review" ||
		reviewPlan["review_ready"] != true ||
		reviewPlan["metadata_ready"] != true ||
		reviewPlan["allowlist_ready"] != true ||
		reviewPlan["audit_evidence_observed"] != true ||
		reviewPlan["terminal_audit_observed"] != true ||
		reviewPlan["sanitized_result_recorded"] != true ||
		reviewPlan["successful_sanitized_result_recorded"] != true ||
		reviewPlan["result_callback_wired"] != true ||
		reviewPlan["result_callback_observed"] != true ||
		reviewPlan["arming_ready_for_operator_review"] != true ||
		reviewPlan["live_tool_invocation_allowed"] != false ||
		reviewPlan["tool_invocation_enabled"] != false ||
		reviewPlan["allowlisted_tool_invoked"] != false ||
		reviewPlan["raw_tool_input_materialized"] != false ||
		reviewPlan["raw_tool_output_recorded"] != false ||
		reviewPlan["operator_review_recorded"] != false {
		t.Fatalf("tool invocation review should reconcile audit evidence without enabling invocation: %#v", reviewPlan)
	}
	callbackPlan := mapFromAny(got["result_callback_plan"])
	if callbackPlan["callback_state"] != "recorded" ||
		callbackPlan["callback_ready_reason"] != "sanitized_agent_audit_result_observed" ||
		callbackPlan["result_written"] != true ||
		callbackPlan["operation_log_written"] != true ||
		callbackPlan["tool_call_status_written"] != true ||
		callbackPlan["raw_tool_output_recorded"] != false ||
		callbackPlan["contains_tool_output"] != false ||
		callbackPlan["contains_runtime_config"] != false {
		t.Fatalf("callback plan should reflect sanitized audit evidence only: %#v", callbackPlan)
	}
	assertAgentWorkerDispatchSubplansSafe(t, got)
	encoded, _ := json.Marshal(got)
	for _, forbidden := range []string{"do-not-serialize", "actual tool output", "Bearer secret", "PRIVATE KEY"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("worker dispatch evidence leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestAgentWorkerDispatchPlanDoesNotWriteResultForActiveAudit(t *testing.T) {
	evidence := agentToolCallAuditEvidence([]map[string]any{
		{"id": "call-runtime", "operation_run_id": "op-1", "tool_name": "runtime.check", "status": "running", "input": map[string]any{"token": "do-not-serialize"}, "output": map[string]any{"raw": "actual tool output"}},
	})
	if evidence["evidence_state"] != "waiting_for_worker" ||
		evidence["has_tool_call_audit"] != true ||
		evidence["sanitized_result_recorded"] != false ||
		intFromAny(evidence["active_count"], 0) != 1 {
		t.Fatalf("active tool audit evidence = %#v", evidence)
	}
	got := agentWorkerDispatchPlan(map[string]any{
		"name":         "Demo Codex",
		"runtime_type": "codex-cli",
		"codex_binary": "codex",
		"status":       "verified",
	}, evidence)
	if got["result_written"] != false ||
		got["result_callback_enabled"] != true ||
		got["tool_invocation_enabled"] != false ||
		got["tool_invoked"] != false ||
		got["external_call_made"] != false {
		t.Fatalf("dispatch plan should not claim result written for active audit: %#v", got)
	}
	callbackPlan := mapFromAny(got["result_callback_plan"])
	if callbackPlan["callback_state"] != "waiting_for_worker" ||
		callbackPlan["callback_ready_reason"] != "sanitized_agent_audit_result_observed" ||
		callbackPlan["result_written"] != false ||
		callbackPlan["operation_log_written"] != false ||
		callbackPlan["tool_call_status_written"] != false ||
		!containsString(stringSliceFromAny(callbackPlan["blocked_reasons"]), "agent_tool_call_audit_not_terminal") ||
		callbackPlan["raw_tool_output_recorded"] != false ||
		callbackPlan["contains_tool_output"] != false {
		t.Fatalf("callback plan should wait for terminal sanitized audit before writing: %#v", callbackPlan)
	}
	armingPlan := mapFromAny(got["tool_execution_arming_plan"])
	if armingPlan["terminal_audit_observed"] != false ||
		armingPlan["result_callback_observed"] != false ||
		armingPlan["arming_ready"] != false {
		t.Fatalf("active audit should not arm tool execution: %#v", armingPlan)
	}
	reviewPlan := mapFromAny(got["tool_invocation_review_plan"])
	if reviewPlan["terminal_audit_observed"] != false ||
		reviewPlan["result_callback_observed"] != false ||
		reviewPlan["review_ready"] != false {
		t.Fatalf("active audit should not make tool invocation review ready: %#v", reviewPlan)
	}
	encoded, _ := json.Marshal(got)
	for _, forbidden := range []string{"do-not-serialize", "actual tool output"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("worker dispatch active audit leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestAgentWorkerDispatchPlanWritesResultForFailedTerminalAudit(t *testing.T) {
	evidence := agentToolCallAuditEvidence([]map[string]any{
		{"id": "call-runtime", "operation_run_id": "op-1", "tool_name": "runtime.check", "status": "failed", "input": map[string]any{"token": "do-not-serialize"}, "output": map[string]any{"raw": "actual tool output"}},
	})
	if evidence["evidence_state"] != "failed" ||
		evidence["has_tool_call_audit"] != true ||
		evidence["sanitized_result_recorded"] != true ||
		intFromAny(evidence["failed_count"], 0) != 1 {
		t.Fatalf("failed terminal tool audit evidence = %#v", evidence)
	}
	got := agentWorkerDispatchPlan(map[string]any{
		"name":         "Demo Codex",
		"runtime_type": "codex-cli",
		"codex_binary": "codex",
		"status":       "verified",
	}, evidence)
	if got["result_written"] != true ||
		got["tool_invocation_enabled"] != false ||
		got["tool_invoked"] != false ||
		got["external_call_made"] != false {
		t.Fatalf("dispatch plan should record failed terminal result without enabling tools: %#v", got)
	}
	callbackPlan := mapFromAny(got["result_callback_plan"])
	if callbackPlan["callback_state"] != "failed" ||
		callbackPlan["result_written"] != true ||
		callbackPlan["operation_log_written"] != true ||
		callbackPlan["tool_call_status_written"] != true ||
		callbackPlan["raw_tool_output_recorded"] != false ||
		callbackPlan["contains_tool_output"] != false {
		t.Fatalf("callback plan should write sanitized failed terminal result only: %#v", callbackPlan)
	}
	armingPlan := mapFromAny(got["tool_execution_arming_plan"])
	if armingPlan["arming_state"] != "failed" ||
		armingPlan["terminal_audit_observed"] != true ||
		armingPlan["successful_audit_recorded"] != false ||
		armingPlan["result_callback_observed"] != true ||
		armingPlan["arming_ready"] != false ||
		!containsString(stringSliceFromAny(armingPlan["missing_evidence"]), "successful_tool_call_audit") {
		t.Fatalf("failed terminal audit should record result but not arm tool execution: %#v", armingPlan)
	}
	reviewPlan := mapFromAny(got["tool_invocation_review_plan"])
	if reviewPlan["review_state"] != "failed" ||
		reviewPlan["terminal_audit_observed"] != true ||
		reviewPlan["successful_sanitized_result_recorded"] != false ||
		reviewPlan["result_callback_observed"] != true ||
		reviewPlan["review_ready"] != false {
		t.Fatalf("failed terminal audit should not make tool invocation review ready: %#v", reviewPlan)
	}
	encoded, _ := json.Marshal(got)
	for _, forbidden := range []string{"do-not-serialize", "actual tool output"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("worker dispatch failed audit leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestAgentWorkerDispatchPlanDoesNotWriteResultForMixedActiveAudit(t *testing.T) {
	evidence := agentToolCallAuditEvidence([]map[string]any{
		{"id": "call-runtime", "operation_run_id": "op-1", "tool_name": "runtime.check", "status": "completed", "input": map[string]any{"token": "do-not-serialize"}, "output": map[string]any{"raw": "actual tool output"}},
		{"id": "call-context", "operation_run_id": "op-1", "tool_name": "context.generate", "status": "running", "input": map[string]any{"prompt": "do-not-serialize"}, "output": map[string]any{"raw": "actual tool output"}},
	})
	if evidence["evidence_state"] != "waiting_for_worker" ||
		evidence["has_tool_call_audit"] != true ||
		evidence["sanitized_result_recorded"] != false ||
		intFromAny(evidence["completed_count"], 0) != 1 ||
		intFromAny(evidence["active_count"], 0) != 1 {
		t.Fatalf("mixed active tool audit evidence = %#v", evidence)
	}
	got := agentWorkerDispatchPlan(map[string]any{
		"name":         "Demo Codex",
		"runtime_type": "codex-cli",
		"codex_binary": "codex",
		"status":       "verified",
	}, evidence)
	if got["result_written"] != false ||
		got["tool_invocation_enabled"] != false ||
		got["tool_invoked"] != false ||
		got["external_call_made"] != false {
		t.Fatalf("dispatch plan should not claim result written while any audit is active: %#v", got)
	}
	callbackPlan := mapFromAny(got["result_callback_plan"])
	if callbackPlan["callback_state"] != "waiting_for_worker" ||
		callbackPlan["result_written"] != false ||
		callbackPlan["operation_log_written"] != false ||
		callbackPlan["tool_call_status_written"] != false ||
		!containsString(stringSliceFromAny(callbackPlan["blocked_reasons"]), "agent_tool_call_audit_not_terminal") {
		t.Fatalf("callback plan should wait for all audit rows to become terminal: %#v", callbackPlan)
	}
	armingPlan := mapFromAny(got["tool_execution_arming_plan"])
	if armingPlan["terminal_audit_observed"] != false ||
		armingPlan["result_callback_observed"] != false ||
		armingPlan["arming_ready"] != false {
		t.Fatalf("mixed active audit should not arm tool execution: %#v", armingPlan)
	}
	reviewPlan := mapFromAny(got["tool_invocation_review_plan"])
	if reviewPlan["terminal_audit_observed"] != false ||
		reviewPlan["result_callback_observed"] != false ||
		reviewPlan["review_ready"] != false {
		t.Fatalf("mixed active audit should not make tool invocation review ready: %#v", reviewPlan)
	}
	encoded, _ := json.Marshal(got)
	for _, forbidden := range []string{"do-not-serialize", "actual tool output"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("worker dispatch mixed audit leaked %q: %s", forbidden, encoded)
		}
	}
}

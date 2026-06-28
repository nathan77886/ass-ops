package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWriteSSEWithIDIncludesReplayCursor(t *testing.T) {
	var b strings.Builder
	if err := writeSSEWithID(&b, "log", "2026-06-22T10:00:00Z|log-1", map[string]any{"id": "log-1"}); err != nil {
		t.Fatalf("writeSSEWithID: %v", err)
	}
	got := b.String()
	for _, token := range []string{
		"id: 2026-06-22T10:00:00Z|log-1\n",
		"event: log\n",
		`data: {"id":"log-1"}`,
	} {
		if !strings.Contains(got, token) {
			t.Fatalf("SSE payload missing %q in %q", token, got)
		}
	}
}

func TestOperationLogStreamClientErrorMessageIsGeneric(t *testing.T) {
	var b strings.Builder
	rawErr := "loading operation logs: pq: password=secret dbname=assops"
	if err := writeSSE(&b, "stream_error", map[string]any{"message": operationLogStreamClientErrorMessage}); err != nil {
		t.Fatalf("writeSSE stream_error: %v", err)
	}
	got := b.String()
	if !strings.Contains(got, `event: stream_error`) ||
		!strings.Contains(got, `data: {"message":"operation log stream failed"}`) {
		t.Fatalf("stream_error payload = %q", got)
	}
	for _, leaked := range []string{"pq:", "password=", "dbname=", rawErr} {
		if strings.Contains(got, leaked) {
			t.Fatalf("stream_error leaked internal error detail %q in %q", leaked, got)
		}
	}
}

func TestAgentToolAuditSnapshotPayloadSanitizesEvidence(t *testing.T) {
	task := map[string]any{
		"id":         "task-1",
		"project_id": "project-1",
		"title":      "do not serialize title",
		"prompt":     "prompt with secret",
		"status":     "completed",
	}
	evidence := agentToolCallAuditEvidence([]map[string]any{
		{
			"id":               "call-1",
			"operation_run_id": "op-1",
			"tool_name":        "runtime.check",
			"status":           "completed",
			"input":            map[string]any{"token": "do-not-serialize"},
			"output":           map[string]any{"raw": "actual tool output"},
		},
	})
	callbackPlan := agentWorkerResultCallbackPlan(evidence)
	snapshot := agentToolAuditSnapshotPayload(task, evidence, callbackPlan, true)
	ready, state, missing := agentToolAuditSnapshotReadiness(snapshot)
	if !ready || state != "recorded" || len(missing) != 0 {
		t.Fatalf("readiness = %v/%s/%#v; snapshot=%#v", ready, state, missing, snapshot)
	}
	if snapshot["has_tool_call_audit"] != true ||
		snapshot["sanitized_result_recorded"] != true ||
		snapshot["status_snapshot_write_eligible"] != true ||
		snapshot["status_snapshot_written"] != true ||
		snapshot["status_snapshot_written"] != snapshot["status_snapshot_write_eligible"] ||
		snapshot["external_call_made"] != false ||
		snapshot["tool_invoked"] != false ||
		snapshot["raw_tool_output_recorded"] != false ||
		snapshot["input_included"] != false ||
		snapshot["output_included"] != false ||
		snapshot["prompt_body_included"] != false ||
		snapshot["runtime_config_materialized"] != false ||
		snapshot["secret_included"] != false {
		t.Fatalf("unexpected sanitized agent tool audit snapshot: %#v", snapshot)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, forbidden := range []string{"do-not-serialize", "actual tool output", "prompt with secret", "runtime.check"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("agent tool audit snapshot leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestAgentToolAuditSnapshotStatusHealthForTerminalStates(t *testing.T) {
	tests := []struct {
		state      string
		wantStatus string
		wantHealth string
	}{
		{state: "recorded", wantStatus: "agent_tool_audit_recorded", wantHealth: "low"},
		{state: "failed", wantStatus: "agent_tool_audit_failed", wantHealth: "high"},
		{state: "mixed_failed", wantStatus: "agent_tool_audit_mixed_failed", wantHealth: "high"},
		{state: "canceled", wantStatus: "agent_tool_audit_canceled", wantHealth: "high"},
		{state: "unknown", wantStatus: "agent_tool_audit_unknown", wantHealth: "high"},
		{state: "absent", wantStatus: "agent_tool_audit_absent", wantHealth: "high"},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			status, health := agentToolAuditSnapshotStatusHealth(tt.state)
			if status != tt.wantStatus || health != tt.wantHealth {
				t.Fatalf("status/health = %s/%s, want %s/%s", status, health, tt.wantStatus, tt.wantHealth)
			}
		})
	}
}

func TestAgentToolArmingSnapshotPayloadSanitizesEvidence(t *testing.T) {
	task := map[string]any{
		"id":         "task-1",
		"project_id": "project-1",
		"title":      "do not serialize title",
		"prompt":     "prompt with secret",
		"status":     "completed",
	}
	evidence := agentToolCallAuditEvidence([]map[string]any{
		{
			"id":               "call-1",
			"operation_run_id": "op-1",
			"tool_name":        "runtime.check",
			"status":           "completed",
			"input":            map[string]any{"token": "do-not-serialize", "runtime_config": "secret runtime"},
			"output":           map[string]any{"raw": "actual tool output", "authorization_header": "Bearer secret"},
		},
	})
	dispatch := agentWorkerDispatchPlan(map[string]any{
		"id":           "runtime-1",
		"name":         "Secret Runtime",
		"runtime_type": "codex-cli",
		"codex_binary": "codex",
		"model":        "gpt-5-codex",
		"status":       "verified",
		"config":       map[string]any{"token": "do-not-serialize"},
	}, evidence)
	snapshot := agentToolArmingSnapshotPayload(task, dispatch, true)
	ready, state, missing := agentToolArmingSnapshotReadiness(snapshot)
	if !ready || state != "ready_for_operator_review" || len(missing) != 0 {
		t.Fatalf("readiness = %v/%s/%#v; snapshot=%#v", ready, state, missing, snapshot)
	}
	if snapshot["runtime_metadata_ready"] != true ||
		snapshot["tool_allowlist_ready"] != true ||
		snapshot["audit_evidence_observed"] != true ||
		snapshot["terminal_audit_observed"] != true ||
		snapshot["successful_audit_recorded"] != true ||
		snapshot["sanitized_result_recorded"] != true ||
		snapshot["successful_sanitized_result_recorded"] != true ||
		snapshot["result_callback_observed"] != true ||
		snapshot["arming_ready_for_operator_review"] != true ||
		snapshot["tool_review_ready_for_operator"] != true ||
		snapshot["status_snapshot_write_eligible"] != true ||
		snapshot["status_snapshot_written"] != snapshot["status_snapshot_write_eligible"] ||
		snapshot["live_tool_invocation_allowed"] != false ||
		snapshot["tool_invocation_enabled"] != false ||
		snapshot["tool_invoked"] != false ||
		snapshot["allowlisted_tool_invoked"] != false ||
		snapshot["raw_tool_input_materialized"] != false ||
		snapshot["raw_tool_output_recorded"] != false ||
		snapshot["runtime_config_materialized"] != false ||
		snapshot["codex_cli_process_started"] != false ||
		snapshot["repository_mutation_allowed"] != false ||
		snapshot["contains_token"] != false {
		t.Fatalf("unexpected tool arming snapshot: %#v", snapshot)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, forbidden := range []string{"do-not-serialize", "secret runtime", "actual tool output", "Bearer secret", "prompt with secret", "Secret Runtime"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("agent tool arming snapshot leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestAgentToolArmingSnapshotBlocksFailedTerminalAudit(t *testing.T) {
	task := map[string]any{
		"id":         "task-1",
		"project_id": "project-1",
		"status":     "completed",
	}
	evidence := agentToolCallAuditEvidence([]map[string]any{
		{"id": "call-1", "operation_run_id": "op-1", "tool_name": "runtime.check", "status": "failed"},
		{"id": "call-2", "operation_run_id": "op-1", "tool_name": "patch.prepare", "status": "completed"},
	})
	if evidence["evidence_state"] != "failed" || evidence["sanitized_result_recorded"] != true {
		t.Fatalf("failed terminal audit should still be recorded as terminal evidence: %#v", evidence)
	}
	dispatch := agentWorkerDispatchPlan(map[string]any{
		"name":         "Demo Runtime",
		"runtime_type": "codex-cli",
		"codex_binary": "codex",
		"status":       "verified",
	}, evidence)
	snapshot := agentToolArmingSnapshotPayload(task, dispatch, true)
	ready, state, missing := agentToolArmingSnapshotReadiness(snapshot)
	if ready ||
		state != "failed" ||
		snapshot["terminal_audit_observed"] != true ||
		snapshot["successful_audit_recorded"] != false ||
		snapshot["sanitized_result_recorded"] != true ||
		snapshot["successful_sanitized_result_recorded"] != false ||
		snapshot["arming_ready_for_operator_review"] != false ||
		snapshot["tool_review_ready_for_operator"] != false {
		t.Fatalf("failed terminal audit should not arm future tool invocation: ready=%v state=%s missing=%#v snapshot=%#v", ready, state, missing, snapshot)
	}
	for _, want := range []string{"successful_tool_call_audit", "sanitized_result_recording", "tool_execution_arming_not_ready", "tool_invocation_review_not_ready"} {
		if !containsString(missing, want) {
			t.Fatalf("failed terminal audit missing %s in %#v", want, missing)
		}
	}
}

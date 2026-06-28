package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestAgentToolInvocationReviewRequiresCallbackBeforeTerminalWait(t *testing.T) {
	evidence := map[string]any{
		"has_tool_call_audit":       true,
		"sanitized_result_recorded": false,
	}
	armingPlan := map[string]any{
		"metadata_ready":           true,
		"allowlist_ready":          true,
		"audit_evidence_observed":  true,
		"terminal_audit_observed":  false,
		"result_callback_wired":    false,
		"result_callback_observed": false,
		"arming_ready":             false,
	}

	got := agentWorkerToolInvocationReviewPlan(agentWorkerAllowedToolNames(), evidence, armingPlan)

	if got["review_state"] != "callback_blocked" ||
		got["review_ready"] != false ||
		got["review_ready_reason"] != "agent_result_callback_not_wired" ||
		!containsString(stringSliceFromAny(got["missing_evidence"]), "result_callback") ||
		got["live_tool_invocation_allowed"] != false ||
		got["raw_tool_output_recorded"] != false ||
		got["external_call_made"] != false {
		t.Fatalf("tool invocation review should require callback before waiting for terminal audit: %#v", got)
	}
}

func TestAgentToolCallAuditEvidenceStatusCombinations(t *testing.T) {
	tests := []struct {
		name       string
		statuses   []string
		wantState  string
		wantActive int
	}{
		{name: "empty", wantState: "not_recorded"},
		{name: "queued waits", statuses: []string{"queued", "completed"}, wantState: "waiting_for_worker", wantActive: 1},
		{name: "failed terminal", statuses: []string{"failed", "completed"}, wantState: "failed"},
		{name: "mixed failed and canceled terminal", statuses: []string{"failed", "canceled", "completed"}, wantState: "mixed_failed"},
		{name: "canceled terminal", statuses: []string{"canceled"}, wantState: "canceled"},
		{name: "unknown terminal", statuses: []string{"mystery"}, wantState: "unknown"},
		{name: "absent terminal", statuses: []string{""}, wantState: "absent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := make([]map[string]any, 0, len(tt.statuses))
			for index, status := range tt.statuses {
				rows = append(rows, map[string]any{
					"id":               fmt.Sprintf("call-%d", index),
					"operation_run_id": "op-1",
					"tool_name":        "runtime.check",
					"status":           status,
					"input":            map[string]any{"token": "do-not-serialize"},
					"output":           map[string]any{"raw": "actual tool output"},
				})
			}
			evidence := agentToolCallAuditEvidence(rows)
			if evidence["evidence_state"] != tt.wantState ||
				intFromAny(evidence["active_count"], 0) != tt.wantActive ||
				evidence["input_included"] != false ||
				evidence["output_included"] != false ||
				evidence["raw_tool_output_recorded"] != false ||
				evidence["secret_included"] != false {
				t.Fatalf("tool call audit evidence = %#v", evidence)
			}
			for _, field := range []string{"token", "api_key", "bearer_token"} {
				if !containsString(stringSliceFromAny(evidence["suppressed_fields"]), field) {
					t.Fatalf("tool call evidence suppressed_fields missing %q: %#v", field, evidence["suppressed_fields"])
				}
			}
			encoded, _ := json.Marshal(evidence)
			for _, forbidden := range []string{"do-not-serialize", "actual tool output", "Bearer secret", "PRIVATE KEY"} {
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("tool call evidence leaked %q: %s", forbidden, encoded)
				}
			}
		})
	}
}

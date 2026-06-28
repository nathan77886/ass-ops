package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSSHMachineRehearsalEvidenceStatesAreSanitized(t *testing.T) {
	machine := map[string]any{
		"id":         "machine-1",
		"project_id": "project-1",
		"name":       "prod-api",
		"host":       "10.0.0.12",
		"port":       22,
		"username":   "deploy",
		"auth_type":  "key",
		"metadata":   map[string]any{},
	}
	tests := []struct {
		name      string
		runs      []map[string]any
		wantState string
	}{
		{
			name: "active run waits for workers",
			runs: []map[string]any{
				{"id": "run-2", "status": "running", "operation_type": "ssh.exec", "stdout": "hidden"},
				{"id": "run-1", "status": "completed", "exit_code": 0, "operation_type": "ssh.verify"},
			},
			wantState: "waiting_for_workers",
		},
		{
			name: "recorded verify and exec marks sanitized result recorded",
			runs: []map[string]any{
				{"id": "run-2", "status": "completed", "exit_code": 0, "operation_type": "ssh.exec", "stdout": "hidden"},
				{"id": "run-1", "status": "completed", "exit_code": 0, "operation_type": "ssh.verify"},
			},
			wantState: "recorded",
		},
		{
			name: "completed verify and exec without exit code stay partial",
			runs: []map[string]any{
				{"id": "run-2", "status": "completed", "operation_type": "ssh.exec", "stdout": "hidden"},
				{"id": "run-1", "status": "completed", "operation_type": "ssh.verify"},
			},
			wantState: "partial_recorded",
		},
		{
			name: "failed terminal run is recorded as failed",
			runs: []map[string]any{
				{"id": "run-2", "status": "failed", "operation_type": "ssh.exec", "stderr": "hidden"},
				{"id": "run-1", "status": "completed", "exit_code": 0, "operation_type": "ssh.verify"},
			},
			wantState: "failed",
		},
		{
			name: "failed run takes priority over active run",
			runs: []map[string]any{
				{"id": "run-3", "status": "running", "operation_type": "ssh.exec", "stdout": "hidden"},
				{"id": "run-2", "status": "failed", "operation_type": "ssh.exec", "stderr": "hidden"},
				{"id": "run-1", "status": "completed", "exit_code": 0, "operation_type": "ssh.verify"},
			},
			wantState: "failed",
		},
		{
			name: "canceled terminal run is recorded as canceled",
			runs: []map[string]any{
				{"id": "run-1", "status": "canceled", "operation_type": "ssh.verify", "raw_error": "hidden"},
			},
			wantState: "canceled",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preview := buildSSHMachineRehearsalPreview(machine, tt.runs)
			evidence := mapFromAny(preview["recent_evidence"])
			if evidence["evidence_state"] != tt.wantState || evidence["has_live_evidence"] != true {
				t.Fatalf("unexpected evidence: %#v", evidence)
			}
			resultPlan := mapFromAny(preview["result_recording_plan"])
			wantRecorded := tt.wantState == "recorded"
			if resultPlan["recording_state"] != tt.wantState ||
				resultPlan["recording_ready"] != wantRecorded ||
				resultPlan["recording_enabled"] != wantRecorded ||
				resultPlan["result_written"] != wantRecorded ||
				resultPlan["auth_binding_recorded"] != wantRecorded {
				t.Fatalf("unexpected result plan: %#v", resultPlan)
			}
			if preview["live_evidence_recorded"] != true {
				t.Fatalf("preview should mark live evidence present: %#v", preview)
			}
			if got, want := preview["sanitized_result_recorded"], wantRecorded; got != want {
				t.Fatalf("sanitized_result_recorded = %#v, want %v; preview=%#v", got, want, preview)
			}
			if resultPlan["stdout_included"] != false || resultPlan["stderr_included"] != false || resultPlan["raw_error_included"] != false || resultPlan["private_key_included"] != false {
				t.Fatalf("result plan leaked sensitive flags: %#v", resultPlan)
			}
			encoded, _ := json.Marshal(preview)
			for _, forbidden := range []string{"hidden", "stdout", "stderr", "raw_error"} {
				if strings.Contains(string(encoded), forbidden) && forbidden == "hidden" {
					t.Fatalf("preview leaked sensitive value %q: %s", forbidden, encoded)
				}
			}
			assertSSHRehearsalPlansSafe(t, preview)
		})
	}
}

package app

import (
	"testing"
)

func TestSSHMachineRehearsalPreviewStates(t *testing.T) {
	tests := []struct {
		name               string
		machine            map[string]any
		runs               []map[string]any
		wantState          string
		wantVerifyStatus   string
		wantExecStatus     string
		wantUnknownRuns    int
		wantRequiredChecks int
		wantEvidenceState  string
		wantControlState   string
	}{
		{
			name: "blocked metadata",
			machine: map[string]any{
				"id":         "machine-1",
				"project_id": "project-1",
				"name":       "missing-host",
				"port":       22,
				"metadata":   map[string]any{},
			},
			wantState:          "blocked",
			wantVerifyStatus:   "blocked",
			wantExecStatus:     "blocked",
			wantRequiredChecks: 2,
			wantEvidenceState:  "not_recorded",
			wantControlState:   "blocked",
		},
		{
			name: "planned with no runs",
			machine: map[string]any{
				"id":         "machine-1",
				"project_id": "project-1",
				"name":       "prod-api",
				"host":       "10.0.0.12",
				"port":       22,
				"username":   "deploy",
				"auth_type":  "key",
				"metadata":   map[string]any{},
			},
			wantState:          "planned",
			wantVerifyStatus:   "planned",
			wantExecStatus:     "blocked",
			wantRequiredChecks: 2,
			wantEvidenceState:  "not_recorded",
			wantControlState:   "planned",
		},
		{
			name: "partial live controls without completed rehearsal",
			machine: map[string]any{
				"id":         "machine-1",
				"project_id": "project-1",
				"name":       "prod-api",
				"host":       "10.0.0.12",
				"port":       22,
				"username":   "deploy",
				"auth_type":  "key",
				"metadata": map[string]any{
					"live_rehearsal_runbook": "https://runbooks.example.com/ssh/prod-api",
				},
			},
			wantState:          "planned",
			wantVerifyStatus:   "planned",
			wantExecStatus:     "blocked",
			wantRequiredChecks: 2,
			wantEvidenceState:  "not_recorded",
			wantControlState:   "partial",
		},
		{
			name: "partial with unfinished verify",
			machine: map[string]any{
				"id":         "machine-1",
				"project_id": "project-1",
				"name":       "prod-api",
				"host":       "10.0.0.12",
				"port":       22,
				"username":   "deploy",
				"auth_type":  "key",
				"metadata":   map[string]any{},
			},
			runs: []map[string]any{
				{"id": "run-1", "status": "queued", "operation_type": "ssh.verify"},
			},
			wantState:          "partial",
			wantVerifyStatus:   "planned",
			wantExecStatus:     "blocked",
			wantRequiredChecks: 2,
			wantEvidenceState:  "waiting_for_workers",
			wantControlState:   "planned",
		},
		{
			name: "ready with completed verify and exec",
			machine: map[string]any{
				"id":         "machine-1",
				"project_id": "project-1",
				"name":       "prod-api",
				"host":       "10.0.0.12",
				"port":       22,
				"username":   "deploy",
				"auth_type":  "key",
				"metadata":   map[string]any{},
			},
			runs: []map[string]any{
				{"id": "run-2", "status": "completed", "exit_code": 0, "operation_type": "ssh.exec"},
				{"id": "run-1", "status": "completed", "exit_code": 0, "operation_type": "ssh.verify"},
			},
			wantState:          "ready",
			wantVerifyStatus:   "completed",
			wantExecStatus:     "completed",
			wantRequiredChecks: 0,
			wantEvidenceState:  "recorded",
			wantControlState:   "partial",
		},
		{
			name: "unknown operation does not count as exec",
			machine: map[string]any{
				"id":         "machine-1",
				"project_id": "project-1",
				"name":       "prod-api",
				"host":       "10.0.0.12",
				"port":       22,
				"username":   "deploy",
				"auth_type":  "key",
				"metadata":   map[string]any{},
			},
			runs: []map[string]any{
				{"id": "run-unknown", "status": "completed"},
			},
			wantState:          "partial",
			wantVerifyStatus:   "planned",
			wantExecStatus:     "blocked",
			wantUnknownRuns:    1,
			wantRequiredChecks: 2,
			wantEvidenceState:  "partial_recorded",
			wantControlState:   "planned",
		},
		{
			name: "completed runs without exit code do not complete rehearsal",
			machine: map[string]any{
				"id":         "machine-1",
				"project_id": "project-1",
				"name":       "prod-api",
				"host":       "10.0.0.12",
				"port":       22,
				"username":   "deploy",
				"auth_type":  "key",
				"metadata":   map[string]any{},
			},
			runs: []map[string]any{
				{"id": "run-2", "status": "completed", "operation_type": "ssh.exec"},
				{"id": "run-1", "status": "completed", "operation_type": "ssh.verify"},
			},
			wantState:          "partial",
			wantVerifyStatus:   "planned",
			wantExecStatus:     "blocked",
			wantRequiredChecks: 2,
			wantEvidenceState:  "partial_recorded",
			wantControlState:   "planned",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preview := buildSSHMachineRehearsalPreview(tt.machine, tt.runs)
			if preview["rehearsal_state"] != tt.wantState {
				t.Fatalf("rehearsal_state = %#v, want %s; preview=%#v", preview["rehearsal_state"], tt.wantState, preview)
			}
			steps := sliceOfMapsFromAny(preview["steps"])
			if statusByKind(steps, "verify_rehearsal") != tt.wantVerifyStatus {
				t.Fatalf("verify status = %s, want %s; steps=%#v", statusByKind(steps, "verify_rehearsal"), tt.wantVerifyStatus, steps)
			}
			if statusByKind(steps, "exec_rehearsal") != tt.wantExecStatus {
				t.Fatalf("exec status = %s, want %s; steps=%#v", statusByKind(steps, "exec_rehearsal"), tt.wantExecStatus, steps)
			}
			evidence := mapFromAny(preview["recent_evidence"])
			if intFromAny(evidence["unknown_runs"], 0) != tt.wantUnknownRuns {
				t.Fatalf("unknown_runs = %#v, want %d; evidence=%#v", evidence["unknown_runs"], tt.wantUnknownRuns, evidence)
			}
			if evidence["evidence_state"] != tt.wantEvidenceState {
				t.Fatalf("evidence_state = %#v, want %s; evidence=%#v", evidence["evidence_state"], tt.wantEvidenceState, evidence)
			}
			controlEvidence := mapFromAny(preview["live_rehearsal_control_evidence"])
			if controlEvidence["control_state"] != tt.wantControlState {
				t.Fatalf("control_state = %#v, want %s; control=%#v", controlEvidence["control_state"], tt.wantControlState, controlEvidence)
			}
			if tt.name == "partial live controls without completed rehearsal" {
				missing := stringSliceFromAny(controlEvidence["missing_evidence"])
				for _, field := range []string{"authorized_machine_fixture", "operator_approval_proof", "completed_ssh_verify", "completed_ssh_exec"} {
					if !containsString(missing, field) {
						t.Fatalf("partial live controls missing evidence %q: %#v", field, controlEvidence)
					}
				}
			}
			required := stringSliceFromAny(preview["required_live_rehearsal"])
			if len(required) != tt.wantRequiredChecks {
				t.Fatalf("required_live_rehearsal = %#v, want len %d", required, tt.wantRequiredChecks)
			}
			assertSSHRehearsalPlansSafe(t, preview)
			approvalPlan := mapFromAny(preview["approval_request_plan"])
			if tt.wantState == "blocked" && !containsString(stringSliceFromAny(approvalPlan["blocked_reasons"]), "machine_metadata_incomplete") {
				t.Fatalf("blocked rehearsal should report metadata blocker: %#v", approvalPlan)
			}
			if tt.wantState != "blocked" && len(stringSliceFromAny(approvalPlan["blocked_reasons"])) != 0 {
				t.Fatalf("metadata-ready rehearsal should not report metadata blockers: %#v", approvalPlan)
			}
			if tt.name == "unknown operation does not count as exec" && evidence["completed_exec"] != false {
				t.Fatalf("unknown operation should not complete exec: %#v", evidence)
			}
			if tt.name == "completed runs without exit code do not complete rehearsal" {
				if evidence["completed_verify"] != false ||
					evidence["completed_exec"] != false ||
					intFromAny(evidence["completed_without_exit_code_runs"], 0) != 2 {
					t.Fatalf("completed runs without exit code should not complete rehearsal: %#v", evidence)
				}
				resultPlan := mapFromAny(preview["result_recording_plan"])
				if !containsString(stringSliceFromAny(resultPlan["blocked_reasons"]), "ssh_completed_result_exit_code_missing") {
					t.Fatalf("missing exit-code blocker in result plan: %#v", resultPlan)
				}
			}
		})
	}
}

package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAgentCodeModificationSourceCheckoutBranchReviewIntermediateStates(t *testing.T) {
	tests := []struct {
		name        string
		rows        []map[string]any
		wantState   string
		wantReady   bool
		wantMissing string
	}{
		{
			name:        "active audit waits for worker",
			rows:        []map[string]any{{"id": "call-worker", "tool_name": "worker.dispatch.plan", "status": "running"}},
			wantState:   "waiting_for_worker",
			wantReady:   false,
			wantMissing: "terminal_tool_call_audit",
		},
		{
			name: "terminal partial audit stays partial",
			rows: []map[string]any{
				{"id": "call-codex", "tool_name": "codex.execution.plan", "status": "completed"},
				{"id": "call-patch", "tool_name": "patch.prepare", "status": "completed"},
			},
			wantState:   "partial_audit",
			wantReady:   false,
			wantMissing: "worker_dispatch_plan_audit",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentCodeModificationPlan(agentToolCallAuditEvidence(tt.rows))
			sourceReview := mapFromAny(got["source_checkout_branch_review_plan"])
			if sourceReview["review_state"] != tt.wantState ||
				sourceReview["review_ready"] != tt.wantReady ||
				sourceReview["review_evidence_scope"] != "shared_code_modification_audit" ||
				sourceReview["source_remote_review_ready"] != false ||
				sourceReview["workspace_binding_review_ready"] != false ||
				sourceReview["branch_policy_review_ready"] != false ||
				sourceReview["source_checkout_performed"] != false ||
				sourceReview["branch_created"] != false ||
				sourceReview["repository_mutation_allowed"] != false ||
				!containsString(stringSliceFromAny(sourceReview["missing_evidence"]), tt.wantMissing) ||
				!containsString(stringSliceFromAny(sourceReview["missing_evidence"]), "operator_source_checkout_branch_review") {
				t.Fatalf("unexpected intermediate source checkout branch review: %#v", sourceReview)
			}
			message := cleanPreviewString(sourceReview["message"])
			if !strings.Contains(message, "shared code-modification audit") || !strings.Contains(message, "no repository is cloned") {
				t.Fatalf("source review message should disclose shared audit scope and no checkout: %#v", message)
			}
			assertAgentCodeModificationPlanSafe(t, got)
		})
	}
}

func TestAgentCodeModificationEvidenceRequiresWorkerDispatchAudit(t *testing.T) {
	evidence := agentToolCallAuditEvidence([]map[string]any{
		{"id": "call-codex", "operation_run_id": "op-1", "tool_name": "codex.execution.plan", "status": "completed"},
		{"id": "call-patch", "operation_run_id": "op-1", "tool_name": "patch.prepare", "status": "completed"},
	})
	got := agentCodeModificationPlan(evidence)
	codeEvidence := mapFromAny(got["code_modification_evidence"])
	if codeEvidence["evidence_state"] != "partial_recorded" ||
		codeEvidence["sanitized_result_recorded"] != false ||
		codeEvidence["worker_dispatch_audit_recorded"] != false ||
		codeEvidence["codex_execution_plan_recorded"] != true ||
		codeEvidence["patch_prepare_audit_recorded"] != true ||
		!containsString(stringSliceFromAny(codeEvidence["missing_audit_evidence"]), "worker_dispatch_plan_audit") ||
		!containsString(stringSliceFromAny(codeEvidence["missing_audit_evidence"]), "sanitized_code_modification_result") {
		t.Fatalf("missing worker dispatch audit should keep code modification evidence partial: %#v", codeEvidence)
	}
	recording := mapFromAny(got["result_recording_plan"])
	if recording["recording_state"] != "partial_recorded" ||
		recording["result_written"] != false ||
		recording["recording_enabled"] != false ||
		recording["recording_ready_reason"] != "agent_code_modification_audit_incomplete" {
		t.Fatalf("recording should require complete sanitized code audit evidence: %#v", recording)
	}
	assertAgentCodeModificationPlanSafe(t, got)
}

func TestAgentCodeModificationEvidenceRejectsFailedAuditComponents(t *testing.T) {
	tests := []struct {
		name       string
		rows       []map[string]any
		wantState  string
		wantReason string
	}{
		{
			name: "failed worker dispatch blocks code result",
			rows: []map[string]any{
				{"id": "call-worker", "operation_run_id": "op-1", "tool_name": "worker.dispatch.plan", "status": "failed"},
				{"id": "call-codex", "operation_run_id": "op-1", "tool_name": "codex.execution.plan", "status": "completed"},
				{"id": "call-patch", "operation_run_id": "op-1", "tool_name": "patch.prepare", "status": "completed"},
			},
			wantState:  "failed",
			wantReason: "agent_code_modification_audit_incomplete",
		},
		{
			name: "failed patch prepare blocks code result",
			rows: []map[string]any{
				{"id": "call-worker", "operation_run_id": "op-1", "tool_name": "worker.dispatch.plan", "status": "completed"},
				{"id": "call-codex", "operation_run_id": "op-1", "tool_name": "codex.execution.plan", "status": "completed"},
				{"id": "call-patch", "operation_run_id": "op-1", "tool_name": "patch.prepare", "status": "failed"},
			},
			wantState:  "failed",
			wantReason: "agent_code_modification_audit_incomplete",
		},
		{
			name: "mixed failed and canceled blocks code result",
			rows: []map[string]any{
				{"id": "call-worker", "operation_run_id": "op-1", "tool_name": "worker.dispatch.plan", "status": "failed"},
				{"id": "call-codex", "operation_run_id": "op-1", "tool_name": "codex.execution.plan", "status": "canceled"},
				{"id": "call-patch", "operation_run_id": "op-1", "tool_name": "patch.prepare", "status": "completed"},
			},
			wantState:  "mixed_failed",
			wantReason: "agent_code_modification_audit_incomplete",
		},
		{
			name:       "missing terminal audit blocks code result",
			rows:       nil,
			wantState:  "not_recorded",
			wantReason: "agent_code_modification_result_not_recorded",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentCodeModificationPlan(agentToolCallAuditEvidence(tt.rows))
			codeEvidence := mapFromAny(got["code_modification_evidence"])
			if codeEvidence["evidence_state"] != tt.wantState ||
				codeEvidence["sanitized_result_recorded"] != false ||
				!containsString(stringSliceFromAny(codeEvidence["missing_audit_evidence"]), "sanitized_code_modification_result") {
				t.Fatalf("failed or missing audit component should block sanitized code result: %#v", codeEvidence)
			}
			recording := mapFromAny(got["result_recording_plan"])
			if recording["recording_enabled"] != false ||
				recording["result_written"] != false ||
				recording["recording_ready_reason"] != tt.wantReason {
				t.Fatalf("failed or missing audit should not enable recording: %#v", recording)
			}
			assertAgentCodeModificationPlanSafe(t, got)
		})
	}
}

func TestAgentWorkerDispatchPlan(t *testing.T) {
	tests := []struct {
		name             string
		runtime          map[string]any
		wantPrerequisite string
	}{
		{
			name:             "missing runtime keeps dispatch metadata blocked",
			runtime:          map[string]any{},
			wantPrerequisite: "metadata_blocked",
		},
		{
			name: "verified runtime only makes dispatch metadata available",
			runtime: map[string]any{
				"name":         "Demo Codex",
				"runtime_type": "codex-cli",
				"codex_binary": "codex",
				"status":       "verified",
				"config":       map[string]any{"token": "do-not-serialize"},
			},
			wantPrerequisite: "metadata_available",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentWorkerDispatchPlan(tt.runtime)
			if got["mode"] != "redacted_agent_worker_dispatch_plan" ||
				got["dispatch_state"] != "audit_queued" ||
				got["dispatch_ready"] != true ||
				got["dispatch_ready_reason"] != "agent_worker_audit_job_enqueued" ||
				got["prerequisite_state"] != tt.wantPrerequisite ||
				got["execution_enabled"] != false ||
				got["audit_worker_execution_enabled"] != true ||
				got["worker_claim_enabled"] != true ||
				got["worker_job_created"] != true ||
				got["worker_node_claimed"] != false ||
				got["tool_invocation_enabled"] != false ||
				got["tool_invoked"] != false ||
				got["result_written"] != false ||
				got["result_callback_enabled"] != true ||
				got["repository_mutation_allowed"] != false ||
				got["external_call_made"] != false {
				t.Fatalf("unexpected worker dispatch plan: %#v", got)
			}
			for _, required := range []string{"agent_execute_approval", "worker_capability_ai", "runtime_verification", "tool_allowlist", "result_callback_audit"} {
				if !containsString(stringSliceFromAny(got["required_controls"]), required) {
					t.Fatalf("required_controls missing %q: %#v", required, got["required_controls"])
				}
			}
			for _, capability := range []string{"ai", "context.read", "agent.audit"} {
				if !containsString(stringSliceFromAny(got["required_worker_capabilities"]), capability) {
					t.Fatalf("required_worker_capabilities missing %q: %#v", capability, got["required_worker_capabilities"])
				}
			}
			for _, tool := range []string{"context.generate", "runtime.check", "codex.execution.plan", "patch.prepare"} {
				if !containsString(stringSliceFromAny(got["allowed_tool_names"]), tool) {
					t.Fatalf("allowed_tool_names missing %q: %#v", tool, got["allowed_tool_names"])
				}
			}
			for _, backend := range []string{"worker_tool_invoke", "codex_cli_process", "git_push"} {
				if !containsString(stringSliceFromAny(got["disabled_backends"]), backend) {
					t.Fatalf("disabled_backends missing %q: %#v", backend, got["disabled_backends"])
				}
			}
			for _, field := range []string{"runtime_config", "environment_variables", "tool_input", "tool_output", "token"} {
				if !containsString(stringSliceFromAny(got["suppressed_fields"]), field) {
					t.Fatalf("suppressed_fields missing %q: %#v", field, got["suppressed_fields"])
				}
			}
			armingPlan := mapFromAny(got["tool_execution_arming_plan"])
			if tt.wantPrerequisite == "metadata_available" {
				if armingPlan["arming_state"] != "audit_ready" ||
					armingPlan["arming_ready"] != false ||
					armingPlan["metadata_ready"] != true ||
					armingPlan["allowlist_ready"] != true ||
					armingPlan["audit_evidence_observed"] != false ||
					!containsString(stringSliceFromAny(armingPlan["missing_evidence"]), "tool_call_audit_evidence") {
					t.Fatalf("metadata-ready dispatch should keep tool execution arming audit-ready but not armed: %#v", armingPlan)
				}
			}
			assertAgentWorkerDispatchSubplansSafe(t, got)
			encoded, _ := json.Marshal(got)
			for _, forbidden := range []string{"do-not-serialize", "ASSOPS_", "OPENAI_", "GITHUB_TOKEN", "PRIVATE KEY", "Bearer", "password"} {
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("worker dispatch plan should not expose sensitive config hints: %s", encoded)
				}
			}
		})
	}
}

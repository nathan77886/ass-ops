package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestAgentCodexExecutionPlan(t *testing.T) {
	tests := []struct {
		name             string
		runtime          map[string]any
		wantPrerequisite string
	}{
		{
			name:             "missing runtime keeps metadata blocked",
			runtime:          map[string]any{},
			wantPrerequisite: "metadata_blocked",
		},
		{
			name: "verified runtime only makes metadata available",
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
			got := agentCodexExecutionPlan(tt.runtime)
			if got["mode"] != "redacted_codex_execution_plan" ||
				got["plan_state"] != "blocked" ||
				got["prerequisite_state"] != tt.wantPrerequisite ||
				got["plan_ready"] != false ||
				got["execution_enabled"] != false ||
				got["process_spawn_enabled"] != false ||
				got["repository_mutation_allowed"] != false ||
				got["pull_request_creation"] != false ||
				got["codex_cli_process_started"] != false ||
				got["file_patch_applied"] != false ||
				got["git_write_performed"] != false {
				t.Fatalf("unexpected Codex execution plan: %#v", got)
			}
			for _, required := range []string{"agent_execute_approval", "runtime_verification", "structured_patch_review", "commit_push_agent"} {
				if !containsString(stringSliceFromAny(got["required_controls"]), required) {
					t.Fatalf("required_controls missing %q: %#v", required, got["required_controls"])
				}
			}
			for _, backend := range []string{"codex_cli_process", "file_patch_apply", "git_commit", "git_push", "pull_request_create"} {
				if !containsString(stringSliceFromAny(got["disabled_backends"]), backend) {
					t.Fatalf("disabled_backends missing %q: %#v", backend, got["disabled_backends"])
				}
			}
			if !slices.Equal(stringSliceFromAny(got["disabled_backends"]), agentDisabledMutationBackends()) {
				t.Fatalf("disabled_backends drifted from shared mutation backend contract: %#v", got["disabled_backends"])
			}
			for _, field := range []string{"runtime_config", "environment_variables", "patch_content", "diff_content", "token"} {
				if !containsString(stringSliceFromAny(got["suppressed_fields"]), field) {
					t.Fatalf("suppressed_fields missing %q: %#v", field, got["suppressed_fields"])
				}
			}
			encoded, _ := json.Marshal(got)
			for _, forbidden := range []string{"do-not-serialize", "ASSOPS_", "OPENAI_", "GITHUB_TOKEN", "PRIVATE KEY"} {
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("Codex execution plan should not expose sensitive config hints: %s", encoded)
				}
			}
		})
	}
}

func TestAgentCodeModificationPlan(t *testing.T) {
	got := agentCodeModificationPlan()
	assertAgentCodeModificationPlanSafe(t, got)
	if got["plan_ready_reason"] != "agent_code_modification_backend_disabled" {
		t.Fatalf("unexpected plan_ready_reason: %#v", got)
	}
	for _, required := range []string{"source_remote_review", "branch_policy_review", "structured_patch_review", "test_plan_review", "commit_push_agent"} {
		if !containsString(stringSliceFromAny(got["required_controls"]), required) {
			t.Fatalf("required_controls missing %q: %#v", required, got["required_controls"])
		}
	}
	for _, backend := range []string{"source_checkout", "branch_create", "file_patch_apply", "test_command_execute", "git_commit", "git_push", "pull_request_create", "commit_push_agent"} {
		if !containsString(stringSliceFromAny(got["disabled_backends"]), backend) {
			t.Fatalf("disabled_backends missing %q: %#v", backend, got["disabled_backends"])
		}
	}
	for _, field := range []string{"source_remote_url", "workspace_path", "branch_name", "patch_content", "diff_content", "file_content", "test_output", "token"} {
		if !containsString(stringSliceFromAny(got["suppressed_fields"]), field) {
			t.Fatalf("suppressed_fields missing %q: %#v", field, got["suppressed_fields"])
		}
	}
	recording := mapFromAny(got["result_recording_plan"])
	if recording["mode"] != "redacted_agent_code_modification_result_recording_plan" ||
		recording["recording_enabled"] != false ||
		recording["result_written"] != false ||
		recording["operation_log_written"] != false ||
		recording["patch_artifact_written"] != false ||
		recording["diff_artifact_written"] != false ||
		recording["test_result_written"] != false ||
		recording["commit_record_written"] != false ||
		recording["push_record_written"] != false ||
		recording["pr_record_written"] != false ||
		recording["raw_patch_recorded"] != false ||
		recording["raw_diff_recorded"] != false ||
		recording["raw_file_content_recorded"] != false ||
		recording["raw_command_output_recorded"] != false ||
		recording["raw_test_output_recorded"] != false {
		t.Fatalf("result recording plan should stay disabled and redacted: %#v", recording)
	}
	for _, field := range []string{"source_remote_url", "branch_name", "patch_content", "diff_content", "file_content", "test_output", "token"} {
		if !containsString(stringSliceFromAny(recording["suppressed_fields"]), field) {
			t.Fatalf("recording suppressed_fields missing %q: %#v", field, recording["suppressed_fields"])
		}
	}
	arming := mapFromAny(got["execution_arming_plan"])
	if arming["arming_state"] != "blocked" ||
		arming["arming_ready"] != false ||
		arming["source_checkout_performed"] != false ||
		arming["branch_created"] != false ||
		arming["file_patch_applied"] != false ||
		arming["tests_executed"] != false ||
		arming["git_commit_created"] != false ||
		arming["git_push_performed"] != false ||
		arming["provider_review_created"] != false ||
		arming["commit_push_agent_invoked"] != false ||
		arming["repository_mutation_allowed"] != false ||
		arming["contains_patch_content"] != false ||
		arming["contains_diff_content"] != false ||
		arming["contains_file_content"] != false {
		t.Fatalf("code modification arming should stay blocked and redacted: %#v", arming)
	}
	sourceReview := mapFromAny(got["source_checkout_branch_review_plan"])
	if sourceReview["mode"] != "redacted_agent_source_checkout_branch_review_plan" ||
		sourceReview["review_state"] != "blocked" ||
		sourceReview["review_ready"] != false ||
		sourceReview["source_checkout_performed"] != false ||
		sourceReview["workspace_bound"] != false ||
		sourceReview["branch_created"] != false ||
		sourceReview["default_branch_direct_write_blocked"] != true ||
		sourceReview["repository_mutation_allowed"] != false ||
		sourceReview["contains_source_remote_url"] != false ||
		sourceReview["contains_workspace_path"] != false ||
		sourceReview["contains_branch_name"] != false {
		t.Fatalf("source checkout branch review should stay blocked and redacted: %#v", sourceReview)
	}
	encoded, _ := json.Marshal(got)
	encodedText := string(encoded)
	lowerEncodedText := strings.ToLower(encodedText)
	for _, forbidden := range []string{"do-not-serialize", "assops_", "openai_", "github_token", "private key", "bearer", "password"} {
		if strings.Contains(lowerEncodedText, forbidden) {
			t.Fatalf("code modification plan should not expose sensitive config hints: %s", encoded)
		}
	}
}

func TestAgentCodeModificationPlanReconcilesAuditEvidence(t *testing.T) {
	evidence := agentToolCallAuditEvidence([]map[string]any{
		{"id": "call-worker", "operation_run_id": "op-1", "tool_name": "worker.dispatch.plan", "status": "completed", "input": map[string]any{"token": "do-not-serialize"}, "output": map[string]any{"raw": "actual tool output"}},
		{"id": "call-codex", "operation_run_id": "op-1", "tool_name": "codex.execution.plan", "status": "completed", "input": map[string]any{"workspace_path": "/tmp/secret-workspace"}, "output": map[string]any{"diff_content": "secret diff"}},
		{"id": "call-patch", "operation_run_id": "op-1", "tool_name": "patch.prepare", "status": "completed", "input": map[string]any{"patch_content": "secret patch"}, "output": map[string]any{"test_output": "secret test"}},
	})
	got := agentCodeModificationPlan(evidence)
	assertAgentCodeModificationPlanSafe(t, got)
	codeEvidence := mapFromAny(got["code_modification_evidence"])
	if codeEvidence["evidence_state"] != "recorded" ||
		codeEvidence["has_code_modification_audit"] != true ||
		codeEvidence["sanitized_result_recorded"] != true ||
		codeEvidence["worker_dispatch_audit_recorded"] != true ||
		codeEvidence["codex_execution_plan_recorded"] != true ||
		codeEvidence["patch_prepare_audit_recorded"] != true ||
		codeEvidence["repository_mutation_allowed"] != false ||
		codeEvidence["source_checkout_performed"] != false ||
		codeEvidence["file_patch_applied"] != false ||
		codeEvidence["tests_executed"] != false ||
		codeEvidence["git_commit_created"] != false ||
		codeEvidence["git_push_performed"] != false ||
		codeEvidence["raw_patch_recorded"] != false ||
		codeEvidence["raw_diff_recorded"] != false ||
		codeEvidence["contains_patch_content"] != false ||
		len(stringSliceFromAny(codeEvidence["missing_audit_evidence"])) != 0 {
		t.Fatalf("code modification evidence should reconcile complete audit without enabling mutation: %#v", codeEvidence)
	}
	recording := mapFromAny(got["result_recording_plan"])
	if recording["recording_state"] != "recorded" ||
		recording["recording_enabled"] != true ||
		recording["result_written"] != true ||
		recording["recording_ready_reason"] != "sanitized_agent_code_modification_audit_observed" ||
		recording["patch_artifact_written"] != false ||
		recording["diff_artifact_written"] != false ||
		recording["test_result_written"] != false ||
		recording["commit_record_written"] != false ||
		recording["push_record_written"] != false ||
		recording["raw_patch_recorded"] != false ||
		recording["raw_diff_recorded"] != false ||
		recording["raw_test_output_recorded"] != false {
		t.Fatalf("code modification recording should reflect sanitized audit only: %#v", recording)
	}
	arming := mapFromAny(got["execution_arming_plan"])
	if arming["arming_state"] != "ready_for_operator_review" ||
		arming["arming_ready"] != true ||
		arming["worker_dispatch_audit_recorded"] != true ||
		arming["codex_execution_plan_recorded"] != true ||
		arming["patch_prepare_audit_recorded"] != true ||
		arming["terminal_audit_recorded"] != true ||
		arming["source_checkout_performed"] != false ||
		arming["branch_created"] != false ||
		arming["file_patch_applied"] != false ||
		arming["tests_executed"] != false ||
		arming["git_commit_created"] != false ||
		arming["git_push_performed"] != false ||
		arming["provider_review_created"] != false ||
		arming["repository_mutation_allowed"] != false {
		t.Fatalf("code modification arming should reconcile audit only for operator review: %#v", arming)
	}
	sourceReview := mapFromAny(got["source_checkout_branch_review_plan"])
	if sourceReview["review_state"] != "ready_for_operator_review" ||
		sourceReview["review_ready"] != true ||
		sourceReview["review_evidence_scope"] != "shared_code_modification_audit" ||
		sourceReview["worker_dispatch_audit_recorded"] != true ||
		sourceReview["codex_execution_plan_recorded"] != true ||
		sourceReview["patch_prepare_audit_recorded"] != true ||
		sourceReview["terminal_audit_recorded"] != true ||
		sourceReview["source_remote_review_ready"] != true ||
		sourceReview["workspace_binding_review_ready"] != true ||
		sourceReview["branch_policy_review_ready"] != true ||
		sourceReview["source_remote_review_scope"] != "shared_code_modification_audit" ||
		sourceReview["workspace_binding_review_scope"] != "shared_code_modification_audit" ||
		sourceReview["branch_policy_review_scope"] != "shared_code_modification_audit" ||
		sourceReview["review_branch_required"] != true ||
		sourceReview["default_branch_direct_write_blocked"] != true ||
		sourceReview["source_checkout_performed"] != false ||
		sourceReview["workspace_bound"] != false ||
		sourceReview["branch_created"] != false ||
		sourceReview["git_fetch_performed"] != false ||
		sourceReview["git_checkout_performed"] != false ||
		sourceReview["git_branch_created"] != false ||
		sourceReview["contains_source_remote_url"] != false ||
		sourceReview["contains_branch_name"] != false ||
		len(stringSliceFromAny(sourceReview["missing_evidence"])) != 0 {
		t.Fatalf("source checkout branch review should reconcile audit only for operator review: %#v", sourceReview)
	}
	encoded, _ := json.Marshal(got)
	for _, forbidden := range []string{"do-not-serialize", "actual tool output", "/tmp/secret-workspace", "secret diff", "secret patch", "secret test"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("code modification evidence leaked %q: %s", forbidden, encoded)
		}
	}
}

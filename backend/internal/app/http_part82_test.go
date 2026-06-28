package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func assertAgentCodeModificationPlanSafe(t *testing.T, got map[string]any) {
	t.Helper()
	if got["mode"] != "redacted_agent_code_modification_plan" ||
		got["plan_state"] != "blocked" ||
		got["plan_ready"] != false ||
		got["execution_enabled"] != false ||
		got["mutation_enabled"] != false ||
		got["external_call_made"] != false ||
		got["repository_mutation_allowed"] != false ||
		got["source_checkout_performed"] != false ||
		got["workspace_bound"] != false ||
		got["branch_created"] != false ||
		got["patch_content_materialized"] != false ||
		got["diff_materialized"] != false ||
		got["file_patch_applied"] != false ||
		got["tests_executed"] != false ||
		got["git_commit_created"] != false ||
		got["git_push_performed"] != false ||
		got["pull_request_created"] != false ||
		got["commit_push_agent_invoked"] != false ||
		got["contains_token"] != false ||
		got["contains_remote_url"] != false ||
		got["contains_branch_name"] != false ||
		got["contains_workspace_path"] != false ||
		got["contains_patch_content"] != false ||
		got["contains_diff_content"] != false ||
		got["contains_file_content"] != false {
		t.Fatalf("agent code modification plan should stay disabled and redacted: %#v", got)
	}
	if !containsString(stringSliceFromAny(got["blocked_reasons"]), "commit_push_agent_not_invoked") {
		t.Fatalf("blocked_reasons should include commit_push_agent_not_invoked: %#v", got["blocked_reasons"])
	}
	codeEvidence := mapFromAny(got["code_modification_evidence"])
	if codeEvidence["mode"] != "redacted_agent_code_modification_evidence" ||
		codeEvidence["execution_enabled"] != false ||
		codeEvidence["mutation_enabled"] != false ||
		codeEvidence["external_call_made"] != false ||
		codeEvidence["repository_mutation_allowed"] != false ||
		codeEvidence["source_checkout_performed"] != false ||
		codeEvidence["workspace_bound"] != false ||
		codeEvidence["branch_created"] != false ||
		codeEvidence["patch_content_materialized"] != false ||
		codeEvidence["diff_materialized"] != false ||
		codeEvidence["file_patch_applied"] != false ||
		codeEvidence["tests_executed"] != false ||
		codeEvidence["git_commit_created"] != false ||
		codeEvidence["git_push_performed"] != false ||
		codeEvidence["pull_request_created"] != false ||
		codeEvidence["commit_push_agent_invoked"] != false ||
		codeEvidence["raw_patch_recorded"] != false ||
		codeEvidence["raw_diff_recorded"] != false ||
		codeEvidence["raw_file_content_recorded"] != false ||
		codeEvidence["raw_command_output_recorded"] != false ||
		codeEvidence["raw_test_output_recorded"] != false ||
		codeEvidence["contains_token"] != false ||
		codeEvidence["contains_remote_url"] != false ||
		codeEvidence["contains_branch_name"] != false ||
		codeEvidence["contains_workspace_path"] != false ||
		codeEvidence["contains_patch_content"] != false ||
		codeEvidence["contains_diff_content"] != false ||
		codeEvidence["contains_file_content"] != false {
		t.Fatalf("code modification evidence should stay disabled and redacted: %#v", codeEvidence)
	}
	for _, field := range []string{"worker_dispatch_plan_audit", "codex_execution_plan_audit", "patch_prepare_audit", "terminal_tool_call_audit"} {
		if !containsString(stringSliceFromAny(codeEvidence["required_audit_evidence"]), field) {
			t.Fatalf("code modification required audit evidence missing %q: %#v", field, codeEvidence["required_audit_evidence"])
		}
	}
	for _, field := range []string{"runtime_config", "environment_variables", "authorization_header", "source_remote_url", "repository_url", "workspace_path", "branch_name", "prompt_body", "tool_input", "tool_output", "patch_content", "diff_content", "file_content", "test_output", "command_output", "token", "api_key"} {
		if !containsString(stringSliceFromAny(codeEvidence["suppressed_fields"]), field) {
			t.Fatalf("code modification evidence suppressed_fields missing %q: %#v", field, codeEvidence["suppressed_fields"])
		}
	}
	recording := mapFromAny(got["result_recording_plan"])
	if recording["recording_enabled"] == true && codeEvidence["sanitized_result_recorded"] != true {
		t.Fatalf("recording cannot be enabled without sanitized code evidence: recording=%#v evidence=%#v", recording, codeEvidence)
	}
	arming := mapFromAny(got["execution_arming_plan"])
	if arming["mode"] != "redacted_agent_code_modification_execution_arming_plan" ||
		arming["source_checkout_performed"] != false ||
		arming["workspace_bound"] != false ||
		arming["branch_created"] != false ||
		arming["patch_content_materialized"] != false ||
		arming["diff_materialized"] != false ||
		arming["file_patch_applied"] != false ||
		arming["tests_executed"] != false ||
		arming["git_commit_created"] != false ||
		arming["git_push_performed"] != false ||
		arming["provider_review_created"] != false ||
		arming["commit_push_agent_invoked"] != false ||
		arming["external_call_made"] != false ||
		arming["repository_mutation_allowed"] != false ||
		arming["contains_source_remote_url"] != false ||
		arming["contains_workspace_path"] != false ||
		arming["contains_branch_name"] != false ||
		arming["contains_patch_content"] != false ||
		arming["contains_diff_content"] != false ||
		arming["contains_file_content"] != false ||
		arming["contains_test_output"] != false ||
		arming["contains_token"] != false {
		t.Fatalf("code modification execution arming should stay disabled and redacted: %#v", arming)
	}
	if got["execution_arming_ready"] != arming["arming_ready"] {
		t.Fatalf("top-level arming flag should mirror arming plan: got=%#v arming=%#v", got["execution_arming_ready"], arming)
	}
	sourceReview := mapFromAny(got["source_checkout_branch_review_plan"])
	if sourceReview["mode"] != "redacted_agent_source_checkout_branch_review_plan" ||
		sourceReview["review_evidence_scope"] != "shared_code_modification_audit" ||
		sourceReview["source_checkout_performed"] != false ||
		sourceReview["workspace_bound"] != false ||
		sourceReview["branch_created"] != false ||
		sourceReview["default_branch_checked_out"] != false ||
		sourceReview["repository_mutation_allowed"] != false ||
		sourceReview["external_call_made"] != false ||
		sourceReview["git_fetch_performed"] != false ||
		sourceReview["git_checkout_performed"] != false ||
		sourceReview["git_branch_created"] != false ||
		sourceReview["contains_source_remote_url"] != false ||
		sourceReview["contains_workspace_path"] != false ||
		sourceReview["contains_branch_name"] != false ||
		sourceReview["contains_default_branch_name"] != false ||
		sourceReview["contains_token"] != false {
		t.Fatalf("source checkout branch review should stay disabled and redacted: %#v", sourceReview)
	}
	if got["source_checkout_branch_review_ready"] != sourceReview["review_ready"] {
		t.Fatalf("top-level source checkout branch review flag should mirror plan: got=%#v plan=%#v", got["source_checkout_branch_review_ready"], sourceReview)
	}
	for _, field := range []string{"source_remote_review_scope", "workspace_binding_review_scope", "branch_policy_review_scope"} {
		if sourceReview[field] != "shared_code_modification_audit" {
			t.Fatalf("source checkout branch review %s should disclose shared audit scope: %#v", field, sourceReview)
		}
	}
	for _, field := range []string{"operation_run_id", "agent_task_id", "source_remote_review", "workspace_binding_review", "branch_policy_review", "review_branch_policy", "operator_review_status"} {
		if !containsString(stringSliceFromAny(sourceReview["required_review_fields"]), field) {
			t.Fatalf("source checkout branch review required field missing %q: %#v", field, sourceReview["required_review_fields"])
		}
	}
	for _, control := range []string{"source_remote_review", "workspace_binding_review", "branch_policy_review", "default_branch_protection_review", "operator_source_checkout_branch_review"} {
		if !containsString(stringSliceFromAny(sourceReview["required_operator_controls"]), control) {
			t.Fatalf("source checkout branch review operator control missing %q: %#v", control, sourceReview["required_operator_controls"])
		}
	}
	for _, backend := range []string{"source_checkout", "workspace_bind", "git_fetch", "git_checkout", "branch_create", "default_branch_write", "repository_mutation"} {
		if !containsString(stringSliceFromAny(sourceReview["disabled_backends"]), backend) {
			t.Fatalf("source checkout branch review disabled backend missing %q: %#v", backend, sourceReview["disabled_backends"])
		}
	}
	for _, field := range []string{"source_remote_url", "repository_url", "workspace_path", "branch_name", "default_branch", "review_branch_name", "authorization_header", "runtime_config", "environment_variables", "token", "api_key"} {
		if !containsString(stringSliceFromAny(sourceReview["suppressed_fields"]), field) {
			t.Fatalf("source checkout branch review suppressed field missing %q: %#v", field, sourceReview["suppressed_fields"])
		}
	}
	for _, control := range []string{"agent_execute_approval", "runtime_verification", "source_remote_review", "workspace_binding_review", "branch_policy_review", "structured_patch_review", "test_plan_review", "commit_push_agent_review", "provider_review_reconciliation"} {
		if !containsString(stringSliceFromAny(arming["required_controls"]), control) {
			t.Fatalf("code modification arming required control missing %q: %#v", control, arming["required_controls"])
		}
	}
	for _, evidence := range []string{"worker_dispatch_plan_audit", "codex_execution_plan_audit", "patch_prepare_audit", "terminal_tool_call_audit", "future_operator_execution_review"} {
		if !containsString(stringSliceFromAny(arming["required_evidence"]), evidence) {
			t.Fatalf("code modification arming required evidence missing %q: %#v", evidence, arming["required_evidence"])
		}
	}
	for _, backend := range []string{"source_checkout", "workspace_bind", "branch_create", "file_patch_apply", "test_command_execute", "git_commit", "git_push", "pull_request_create", "commit_push_agent"} {
		if !containsString(stringSliceFromAny(arming["disabled_backends"]), backend) {
			t.Fatalf("code modification arming disabled backend missing %q: %#v", backend, arming["disabled_backends"])
		}
	}
	for _, field := range []string{"runtime_config", "environment_variables", "authorization_header", "source_remote_url", "repository_url", "workspace_path", "branch_name", "prompt_body", "tool_input", "tool_output", "patch_content", "diff_content", "file_content", "test_output", "command_output", "token", "api_key"} {
		if !containsString(stringSliceFromAny(arming["suppressed_fields"]), field) {
			t.Fatalf("code modification arming suppressed field missing %q: %#v", field, arming["suppressed_fields"])
		}
	}
	if boolOnlyFromAny(arming["arming_ready"]) {
		if arming["arming_state"] != "ready_for_operator_review" ||
			arming["worker_dispatch_audit_recorded"] != true ||
			arming["codex_execution_plan_recorded"] != true ||
			arming["patch_prepare_audit_recorded"] != true ||
			arming["terminal_audit_recorded"] != true {
			t.Fatalf("ready code modification arming should only reflect complete audit evidence: %#v", arming)
		}
	} else if arming["arming_state"] == "ready_for_operator_review" {
		t.Fatalf("code modification arming cannot be ready state without arming_ready: %#v", arming)
	}
	if boolOnlyFromAny(sourceReview["review_ready"]) {
		if sourceReview["review_state"] != "ready_for_operator_review" ||
			sourceReview["review_evidence_scope"] != "shared_code_modification_audit" ||
			sourceReview["worker_dispatch_audit_recorded"] != true ||
			sourceReview["codex_execution_plan_recorded"] != true ||
			sourceReview["patch_prepare_audit_recorded"] != true ||
			sourceReview["terminal_audit_recorded"] != true ||
			sourceReview["default_branch_direct_write_blocked"] != true {
			t.Fatalf("ready source checkout branch review should only reflect complete audit evidence: %#v", sourceReview)
		}
	} else if sourceReview["review_state"] == "ready_for_operator_review" {
		t.Fatalf("source checkout branch review cannot be ready state without review_ready: %#v", sourceReview)
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func containsStringContaining(items []string, target string) bool {
	for _, item := range items {
		if strings.Contains(item, target) {
			return true
		}
	}
	return false
}

func newDeploymentTargetExecutionGateRequest() *http.Request {
	return newDeploymentTargetExecutionGateRequestForUser(&User{ID: "admin-1", Role: "admin"})
}

func newDeploymentTargetExecutionGateRequestForUser(user *User) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/deployment-targets/target-1/execution-gate", strings.NewReader(`{}`))
	req = withRouteParam(req, "id", "target-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, user))
}

func newRollbackPointExecutionGateRequest() *http.Request {
	return newRollbackPointExecutionGateRequestForUser(&User{ID: "admin-1", Role: "admin"})
}

func newRollbackPointExecutionGateRequestForUser(user *User) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/rollback-points/rollback-1/execution-gate", strings.NewReader(`{}`))
	req = withRouteParam(req, "id", "rollback-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, user))
}

func sliceOfMapsFromAny(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, mapFromAny(item))
		}
		return out
	default:
		return nil
	}
}

func statusByGate(gates []map[string]any, gate string) string {
	for _, item := range gates {
		if fmt.Sprint(item["gate"]) == gate {
			return fmt.Sprint(item["status"])
		}
	}
	return ""
}

func statusByName(items []map[string]any, name string) string {
	for _, item := range items {
		if fmt.Sprint(item["name"]) == name {
			return fmt.Sprint(item["status"])
		}
	}
	return ""
}

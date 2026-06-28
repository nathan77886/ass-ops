package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func providerReviewArmingAllAttemptLiveReadinessForTest(attemptCount int) map[string]bool {
	out := map[string]bool{}
	for i := 1; i <= attemptCount; i++ {
		out[fmt.Sprintf("attempt-%d", i)] = true
	}
	return out
}

func providerReviewActivationSnapshotAttempt(status, dependency string) map[string]any {
	return map[string]any{
		"id":                      "attempt-1",
		"operation_approval_id":   "approval-1",
		"project_template_run_id": "run-1",
		"provider_type":           "github",
		"review_kind":             "pull_request",
		"operation_name":          "create_branch_ref",
		"endpoint_key":            "github.create_branch_ref",
		"status":                  status,
		"replay_check":            "detect_existing_branch_ref",
		"conflict_policy":         "treat_existing_matching_ref_as_success",
		"retry_policy":            "retry_only_after_response_diagnostics",
		"operation_order":         10,
		"depends_on_operation":    "",
		"dependency_status":       dependency,
		"request_summary": map[string]any{
			"mode":                        "redacted_attempt_request_summary",
			"operation_name":              "create_branch_ref",
			"endpoint_key":                "github.create_branch_ref",
			"payload_builder":             "build_redacted_branch_ref_request",
			"response_handler":            "handle_branch_ref_response",
			"execution_status":            "ready_for_adapter_implementation",
			"request_body_included":       false,
			"headers_included":            false,
			"idempotency_key_kind":        "operation_scope_hash",
			"idempotency_key_included":    false,
			"requires_provider_client":    true,
			"requires_request_builder":    true,
			"requires_response_handler":   true,
			"requires_idempotency_ledger": true,
			"provider_api_call_made":      false,
			"provider_api_mutation":       "disabled",
			"external_call_made":          false,
			"payload_redacted":            true,
			"contains_token":              false,
			"contains_provider_url":       false,
			"contains_repository_ref":     false,
			"contains_branch_name":        false,
			"contains_file_content":       false,
		},
		"response_diagnostics": map[string]any{
			"mode":                     "redacted_attempt_response_diagnostics",
			"endpoint_key":             "github.create_branch_ref",
			"status":                   "pending",
			"success_status_class":     "2xx",
			"retryable_status_classes": []string{"5xx"},
			"response_body_included":   false,
			"headers_included":         false,
			"contains_token":           false,
			"contains_provider_url":    false,
			"provider_api_call_made":   false,
			"provider_api_mutation":    "disabled",
			"external_call_made":       false,
		},
		"provider_api_call_made": false,
		"provider_api_mutation":  "disabled",
		"external_call_made":     false,
		"claimed_at":             nil,
		"approval_id":            "approval-1",
		"approval_project_id":    "project-1",
		"approval_action":        templateProviderReviewExecuteApprovalAction,
		"approval_status":        "approved",
	}
}

func providerReviewActivationSnapshotLedger(attempt map[string]any) map[string]any {
	return providerReviewAttemptLedgerSummary([]map[string]any{attempt})
}

func newAgentCodeAuditSnapshotRequestAs(body string, user *User) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/agent/tasks/task-1/code-audit-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "task-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, user))
}

func TestAgentExecutionAuditSteps(t *testing.T) {
	steps := agentExecutionAuditSteps(
		map[string]any{"id": "task-1"},
		map[string]any{"id": "plan-1", "content": "approved plan"},
		map[string]any{"id": "op-1"},
		map[string]any{
			"id":           "runtime-1",
			"name":         "Demo Codex",
			"runtime_type": "codex-cli",
			"codex_binary": "codex",
			"model":        "gpt-5-codex",
			"status":       "verified",
		},
	)
	if len(steps) != 6 {
		t.Fatalf("len(steps) = %d, want 6", len(steps))
	}
	wantTools := []string{"context.generate", "plan.review", "runtime.check", "worker.dispatch.plan", "codex.execution.plan", "patch.prepare"}
	for i, tool := range wantTools {
		if steps[i]["tool_name"] != tool {
			t.Fatalf("step %d tool = %v, want %s", i, steps[i]["tool_name"], tool)
		}
	}
	runtimeInput := mapFromAny(steps[2]["input"])
	if runtimeInput["runtime_id"] != "runtime-1" || runtimeInput["status"] != "verified" {
		t.Fatalf("runtime.check input missing runtime readiness: %#v", runtimeInput)
	}
	runtimeOutput := mapFromAny(steps[2]["output"])
	if runtimeOutput["mutation_enabled"] != false {
		t.Fatalf("runtime.check should keep mutation disabled: %#v", runtimeOutput)
	}
	cliReadiness := mapFromAny(runtimeOutput["codex_cli_readiness"])
	if cliReadiness["readiness"] != "metadata_ready" ||
		cliReadiness["execution_enabled"] != false ||
		cliReadiness["process_spawn_enabled"] != false ||
		cliReadiness["repository_mutation_allowed"] != false {
		t.Fatalf("runtime.check should expose disabled Codex CLI readiness: %#v", cliReadiness)
	}
	if statusByGate(sliceOfMapsFromAny(cliReadiness["gates"]), "runtime_verified") != "ready" ||
		statusByGate(sliceOfMapsFromAny(cliReadiness["gates"]), "codex_cli_process") != "blocked" {
		t.Fatalf("runtime.check Codex CLI gates should keep process execution blocked: %#v", cliReadiness["gates"])
	}
	if _, ok := runtimeInput["config"]; ok {
		t.Fatalf("runtime.check input should not expose runtime config: %#v", runtimeInput)
	}
	workerDispatchInput := mapFromAny(steps[3]["input"])
	if workerDispatchInput["mode"] != "redacted_worker_dispatch_plan" {
		t.Fatalf("worker.dispatch.plan mode = %v, want redacted_worker_dispatch_plan", workerDispatchInput["mode"])
	}
	workerDispatchOutput := mapFromAny(steps[3]["output"])
	workerDispatchPlan := mapFromAny(workerDispatchOutput["worker_dispatch_plan"])
	if workerDispatchPlan["mode"] != "redacted_agent_worker_dispatch_plan" ||
		workerDispatchPlan["dispatch_state"] != "audit_queued" ||
		workerDispatchPlan["prerequisite_state"] != "metadata_available" ||
		workerDispatchPlan["audit_worker_execution_enabled"] != true ||
		workerDispatchPlan["worker_claim_enabled"] != true ||
		workerDispatchPlan["worker_job_created"] != true ||
		workerDispatchPlan["tool_invocation_enabled"] != false ||
		workerDispatchPlan["tool_invoked"] != false ||
		workerDispatchPlan["result_callback_enabled"] != true ||
		workerDispatchPlan["result_written"] != false {
		t.Fatalf("worker.dispatch.plan should expose queued audit worker boundary without mutation: %#v", workerDispatchPlan)
	}
	if !containsString(stringSliceFromAny(workerDispatchPlan["required_controls"]), "worker_capability_ai") ||
		!containsString(stringSliceFromAny(workerDispatchPlan["allowed_tool_names"]), "context.generate") ||
		!containsString(stringSliceFromAny(workerDispatchPlan["disabled_backends"]), "worker_tool_invoke") ||
		!containsString(stringSliceFromAny(workerDispatchPlan["suppressed_fields"]), "tool_output") ||
		!containsString(stringSliceFromAny(workerDispatchPlan["blocked_reasons"]), "repository_mutation_not_armed") {
		t.Fatalf("worker.dispatch.plan missing controls/backends/suppression: %#v", workerDispatchPlan)
	}
	assertAgentWorkerDispatchSubplansSafe(t, workerDispatchPlan)
	codexPlanInput := mapFromAny(steps[4]["input"])
	if codexPlanInput["mode"] != "redacted_execution_plan" {
		t.Fatalf("codex.execution.plan mode = %v, want redacted_execution_plan", codexPlanInput["mode"])
	}
	codexPlanOutput := mapFromAny(steps[4]["output"])
	codexPlan := mapFromAny(codexPlanOutput["codex_execution_plan"])
	if codexPlan["mode"] != "redacted_codex_execution_plan" ||
		codexPlan["plan_state"] != "blocked" ||
		codexPlan["prerequisite_state"] != "metadata_available" {
		t.Fatalf("codex.execution.plan should expose blocked metadata-available plan: %#v", codexPlan)
	}
	if codexPlan["execution_enabled"] != false ||
		codexPlan["process_spawn_enabled"] != false ||
		codexPlan["repository_mutation_allowed"] != false ||
		codexPlan["pull_request_creation"] != false ||
		codexPlan["external_call_made"] != false ||
		codexPlan["command_invoked"] != false ||
		codexPlan["file_patch_applied"] != false ||
		codexPlan["git_write_performed"] != false {
		t.Fatalf("codex.execution.plan should keep every mutation backend disabled: %#v", codexPlan)
	}
	if !containsString(stringSliceFromAny(codexPlan["disabled_backends"]), "codex_cli_process") ||
		!containsString(stringSliceFromAny(codexPlan["disabled_backends"]), "git_push") ||
		!containsString(stringSliceFromAny(codexPlan["required_controls"]), "commit_push_agent") ||
		!containsString(stringSliceFromAny(codexPlan["suppressed_fields"]), "runtime_config") ||
		!containsString(stringSliceFromAny(codexPlan["blocked_reasons"]), "repository_mutation_not_armed") {
		t.Fatalf("codex.execution.plan missing redacted controls/backends/suppression: %#v", codexPlan)
	}
	patchInput := mapFromAny(steps[5]["input"])
	if patchInput["mode"] != "simulation_only" {
		t.Fatalf("patch.prepare mode = %v, want simulation_only", patchInput["mode"])
	}
	patchOutput := mapFromAny(steps[5]["output"])
	if !strings.Contains(fmt.Sprint(patchOutput["message"]), "code mutation remains disabled") {
		t.Fatalf("patch.prepare output should document disabled mutation: %#v", patchOutput)
	}
	patchGuardrail := mapFromAny(patchOutput["patch_workflow_guardrail"])
	if patchGuardrail["mutation_enabled"] != false || patchGuardrail["repository_mutation_allowed"] != false {
		t.Fatalf("patch guardrail should keep mutation disabled: %#v", patchGuardrail)
	}
	if patchGuardrail["codex_cli_invocation"] != "disabled" || patchGuardrail["pull_request_creation"] != "disabled" {
		t.Fatalf("patch guardrail should disable Codex CLI and PR creation: %#v", patchGuardrail)
	}
	codeModificationPlan := mapFromAny(patchGuardrail["code_modification_plan"])
	assertAgentCodeModificationPlanSafe(t, codeModificationPlan)
	blockedReasons := stringSliceFromAny(patchGuardrail["blocked_reasons"])
	if len(blockedReasons) < 3 {
		t.Fatalf("patch guardrail should expose blocked reasons: %#v", patchGuardrail)
	}
	readiness := sliceOfMapsFromAny(patchGuardrail["execution_readiness"])
	if len(readiness) < 5 {
		t.Fatalf("patch guardrail should expose execution readiness gates: %#v", patchGuardrail)
	}
	if statusByGate(readiness, "codex_cli_process") != "blocked" ||
		statusByGate(readiness, "repository_mutation") != "blocked" ||
		statusByGate(readiness, "pull_request_workflow") != "blocked" {
		t.Fatalf("mutation-related readiness gates should remain blocked: %#v", readiness)
	}
	if statusByGate(readiness, "agent_execute_approval") != "audit_ready" ||
		statusByGate(readiness, "runtime_metadata") != "audit_checked" {
		t.Fatalf("audit readiness gates missing approved/check states: %#v", readiness)
	}
	planInput := mapFromAny(steps[1]["input"])
	if planInput["plan_bytes"] != len("approved plan") {
		t.Fatalf("plan_bytes = %v, want %d", planInput["plan_bytes"], len("approved plan"))
	}
}

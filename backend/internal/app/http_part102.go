package app

import (
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strings"
)

func agentCodexExecutionPlan(runtime map[string]any) map[string]any {
	cliReadiness := agentCodexCLIReadiness(runtime)
	prerequisiteState := "metadata_blocked"
	if strings.TrimSpace(fmt.Sprint(cliReadiness["readiness"])) == "metadata_ready" {
		prerequisiteState = "metadata_available"
	}
	return map[string]any{
		"mode":                          "redacted_codex_execution_plan",
		"plan_state":                    "blocked",
		"prerequisite_state":            prerequisiteState,
		"plan_ready":                    false,
		"plan_ready_reason":             "codex_cli_execution_backend_disabled",
		"execution_enabled":             false,
		"process_spawn_enabled":         false,
		"repository_mutation_allowed":   false,
		"pull_request_creation":         false,
		"external_call_made":            false,
		"codex_cli_process_started":     false,
		"command_invoked":               false,
		"workspace_bound":               false,
		"context_snapshot_materialized": true,
		"patch_content_materialized":    false,
		"diff_materialized":             false,
		"file_patch_applied":            false,
		"git_write_performed":           false,
		"approval_action":               "agent.execute",
		"requires_approval":             true,
		"requires_runtime_verification": true,
		"requires_workspace_binding":    true,
		"requires_patch_review":         true,
		"requires_human_approval":       true,
		"contains_token":                false,
		"contains_runtime_config":       false,
		"contains_prompt_body":          false,
		"contains_tool_input":           false,
		"contains_tool_output":          false,
		"contains_patch_content":        false,
		"contains_diff_content":         false,
		"execution_boundary_redacted":   true,
		"blocked_reasons": []string{
			"codex_cli_execution_backend_disabled",
			"process_spawn_disabled",
			"repository_mutation_not_armed",
			"pull_request_workflow_not_wired",
		},
		"required_controls": []string{
			"agent_execute_approval",
			"runtime_verification",
			"workspace_binding",
			"context_snapshot",
			"structured_patch_review",
			"human_patch_approval",
			"commit_push_agent",
			"provider_review_reconciliation",
		},
		"disabled_backends": agentDisabledMutationBackends(),
		"suppressed_fields": []string{
			"runtime_config",
			"environment_variables",
			"authorization_header",
			"workspace_path",
			"repository_url",
			"prompt_body",
			"tool_input",
			"tool_output",
			"patch_content",
			"diff_content",
			"token",
		},
		"execution_sequence": []string{
			"record_context_snapshot",
			"verify_runtime_metadata",
			"bind_workspace",
			"request_process_launch_approval",
			"start_codex_cli",
			"capture_structured_patch",
			"review_patch",
			"request_patch_apply_approval",
			"apply_patch",
			"delegate_commit_push",
		},
		"message": "Codex CLI execution is still a redacted audit plan; no process, patch, git, or pull request mutation is enabled.",
	}
}

func agentDisabledMutationBackends() []string {
	return []string{
		"codex_cli_process",
		"file_patch_apply",
		"git_commit",
		"git_push",
		"pull_request_create",
	}
}

func agentExecutionReadinessGates() []map[string]any {
	return []map[string]any{
		{
			"gate":    "agent_execute_approval",
			"status":  "audit_ready",
			"message": "agent.execute approval only permits audit rows; real Codex CLI execution remains blocked",
		},
		{
			"gate":    "runtime_metadata",
			"status":  "audit_checked",
			"message": "AI runtime metadata is reviewed for audit without exposing runtime secrets",
		},
		{
			"gate":    "codex_cli_process",
			"status":  "blocked",
			"message": "Codex CLI process execution is not enabled",
		},
		{
			"gate":    "repository_mutation",
			"status":  "blocked",
			"message": "repository mutation requires a future approval-gated patch apply operation",
		},
		{
			"gate":    "pull_request_workflow",
			"status":  "blocked",
			"message": "pull request creation is not wired to a provider account workflow",
		},
	}
}

func agentRuntimeCheckStep(taskID, opID string, runtime map[string]any) map[string]any {
	input := map[string]any{
		"agent_task_id":    taskID,
		"operation_run_id": opID,
		"mode":             "read_only_runtime_check",
	}
	output := map[string]any{
		"mutation_enabled": false,
	}
	if len(runtime) == 0 {
		output["readiness"] = "missing"
		output["message"] = "no AI runtime is configured for this project or globally; execution remains audit-only"
	} else {
		status := strings.TrimSpace(fmt.Sprint(runtime["status"]))
		if status == "" || status == "<nil>" {
			status = "unknown"
		}
		input["runtime_id"] = strings.TrimSpace(fmt.Sprint(runtime["id"]))
		input["runtime_name"] = strings.TrimSpace(fmt.Sprint(runtime["name"]))
		input["runtime_type"] = strings.TrimSpace(fmt.Sprint(runtime["runtime_type"]))
		input["codex_binary"] = strings.TrimSpace(fmt.Sprint(runtime["codex_binary"]))
		input["model"] = strings.TrimSpace(fmt.Sprint(runtime["model"]))
		input["status"] = status
		output["readiness"] = status
		output["message"] = "AI runtime metadata checked for audit; repository mutation remains disabled"
	}
	output["codex_cli_readiness"] = agentCodexCLIReadiness(runtime)
	return map[string]any{
		"tool_name": "runtime.check",
		"input":     input,
		"output":    output,
	}
}

func agentCodexCLIReadiness(runtime map[string]any) map[string]any {
	status := strings.TrimSpace(fmt.Sprint(runtime["status"]))
	if status == "" || status == "<nil>" {
		status = "missing"
	}
	codexBinary := strings.TrimSpace(fmt.Sprint(runtime["codex_binary"]))
	if codexBinary == "<nil>" {
		codexBinary = ""
	}
	runtimeName := strings.TrimSpace(fmt.Sprint(runtime["name"]))
	if runtimeName == "<nil>" {
		runtimeName = ""
	}
	runtimeType := strings.TrimSpace(fmt.Sprint(runtime["runtime_type"]))
	if runtimeType == "<nil>" {
		runtimeType = ""
	}
	runtimeConfigured := runtimeName != "" && runtimeType != ""
	runtimeVerified := status == "verified"
	binaryConfigured := codexBinary != ""

	gates := []map[string]any{
		{
			"gate":    "runtime_configured",
			"status":  readinessStatus(runtimeConfigured),
			"message": "project or global AI runtime metadata must be selected before Codex CLI execution can be enabled",
		},
		{
			"gate":    "runtime_verified",
			"status":  readinessStatus(runtimeVerified),
			"message": "AI runtime must be verified before any future Codex CLI process launch",
		},
		{
			"gate":    "codex_binary_configured",
			"status":  readinessStatus(binaryConfigured),
			"message": "Codex CLI binary path/name must be configured in runtime metadata",
		},
		{
			"gate":    "codex_cli_process",
			"status":  "blocked",
			"message": "process spawning is disabled; this audit row is a dry-run readiness preview only",
		},
		{
			"gate":    "repository_mutation",
			"status":  "blocked",
			"message": "repository writes require a future approval-gated patch apply operation",
		},
		{
			"gate":    "pull_request_workflow",
			"status":  "blocked",
			"message": "pull request creation requires a future provider account workflow",
		},
	}

	readiness := "blocked"
	if runtimeConfigured && runtimeVerified && binaryConfigured {
		readiness = "metadata_ready"
	}
	return map[string]any{
		"readiness":                   readiness,
		"execution_enabled":           false,
		"process_spawn_enabled":       false,
		"external_call_made":          false,
		"repository_mutation_allowed": false,
		"pull_request_creation":       false,
		"runtime_status":              status,
		"gates":                       gates,
		"next_step":                   "Enable Codex CLI only after process launch, patch application, and PR creation each have approval gates and provider reconciliation.",
	}
}

func readinessStatus(ready bool) string {
	if ready {
		return "ready"
	}
	return "blocked"
}

func (s *Server) createConnectionCredential(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "connection_credential", ProjectID: projectID}, "create") {
		return
	}
	s.createConnectionCredentialForProject(w, r, projectID)
}

func (s *Server) createGlobalConnectionCredential(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "connection_credential"}, "create") {
		return
	}
	s.createConnectionCredentialForProject(w, r, "")
}

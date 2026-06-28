package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func assertProviderRefreshExecutionPlanSafe(t *testing.T, executionPlan map[string]any) {
	t.Helper()
	if executionPlan["operation_enqueued"] != false ||
		executionPlan["worker_job_created"] != false ||
		executionPlan["validation_auto_reload_supported"] != true ||
		executionPlan["server_side_validation_rerun"] != false ||
		executionPlan["git_fetch_performed"] != false ||
		executionPlan["provider_api_called"] != false ||
		executionPlan["argocd_api_called"] != false ||
		executionPlan["synced_state_written"] != false ||
		executionPlan["validation_reopened"] != false ||
		executionPlan["secret_included"] != false {
		t.Fatalf("refresh execution preview should keep external execution flags false: %#v", executionPlan)
	}
	for _, field := range []string{"remote_url", "provider_token", "authorization_header", "git_credentials"} {
		if !containsString(stringSliceFromAny(executionPlan["suppressed_fields"]), field) {
			t.Fatalf("execution plan suppressed_fields missing %q: %#v", field, executionPlan["suppressed_fields"])
		}
	}
	for _, planKey := range []string{"git_ref_fetch_plan", "github_actions_refresh_plan", "argo_revision_refresh_plan"} {
		kindPlan := mapFromAny(executionPlan[planKey])
		if kindPlan["refresh_state"] == "" ||
			kindPlan["external_call_made"] != false {
			t.Fatalf("refresh kind plan should have state and keep external calls disabled: %s %#v", planKey, kindPlan)
		}
		for _, reason := range []string{"server_side_validation_rerun_not_performed"} {
			if kindPlan["refresh_state"] != "not_required" && !containsString(stringSliceFromAny(kindPlan["execution_blockers"]), reason) {
				t.Fatalf("refresh kind plan execution blockers missing %q: %s %#v", reason, planKey, kindPlan)
			}
		}
	}
	gitFetchPlan := mapFromAny(executionPlan["git_ref_fetch_plan"])
	if gitFetchPlan["mode"] != "provider_refresh_git_ref_fetch_plan" ||
		gitFetchPlan["git_fetch_performed"] != false ||
		gitFetchPlan["git_remote_sync_performed"] != false ||
		gitFetchPlan["remote_ref_verified"] != false ||
		gitFetchPlan["synced_state_written"] != false ||
		gitFetchPlan["contains_remote_url"] != false ||
		gitFetchPlan["contains_git_credentials"] != false ||
		gitFetchPlan["contains_commit_body"] != false {
		t.Fatalf("git fetch subplan preview should stay redacted and not executed: %#v", gitFetchPlan)
	}
	if gitFetchPlan["refresh_state"] == "planned" && gitFetchPlan["fetch_only_backend_enabled"] != true {
		t.Fatalf("planned git fetch subplan should expose fetch-only backend readiness: %#v", gitFetchPlan)
	}
	for _, backend := range []string{"git_push", "remote_mutation", "raw_git_output_recording", "server_side_automatic_validation_rerun"} {
		if !containsString(stringSliceFromAny(gitFetchPlan["disabled_backends"]), backend) {
			t.Fatalf("git fetch subplan disabled backend missing %q: %#v", backend, gitFetchPlan["disabled_backends"])
		}
	}
	for _, field := range []string{"remote_url", "git_credentials", "authorization_header", "commit_body", "raw_git_output"} {
		if !containsString(stringSliceFromAny(gitFetchPlan["suppressed_fields"]), field) {
			t.Fatalf("git fetch subplan suppressed field missing %q: %#v", field, gitFetchPlan["suppressed_fields"])
		}
	}
	actionsPlan := mapFromAny(executionPlan["github_actions_refresh_plan"])
	if actionsPlan["mode"] != "provider_refresh_github_actions_plan" ||
		actionsPlan["github_actions_api_called"] != false ||
		actionsPlan["github_actions_runs_synced"] != false ||
		actionsPlan["github_actions_scope_verified"] != false ||
		actionsPlan["synced_state_written"] != false ||
		actionsPlan["contains_provider_token"] != false ||
		actionsPlan["contains_remote_url"] != false ||
		actionsPlan["contains_provider_response"] != false {
		t.Fatalf("GitHub Actions subplan preview should stay redacted and not executed: %#v", actionsPlan)
	}
	if actionsPlan["refresh_state"] == "planned" && actionsPlan["github_actions_sync_enabled"] != true {
		t.Fatalf("planned GitHub Actions subplan should expose sync backend readiness: %#v", actionsPlan)
	}
	for _, backend := range []string{"provider_mutation", "raw_provider_response_recording", "server_side_automatic_validation_rerun"} {
		if !containsString(stringSliceFromAny(actionsPlan["disabled_backends"]), backend) {
			t.Fatalf("GitHub Actions subplan disabled backend missing %q: %#v", backend, actionsPlan["disabled_backends"])
		}
	}
	argoPlan := mapFromAny(executionPlan["argo_revision_refresh_plan"])
	if argoPlan["mode"] != "provider_refresh_argo_revision_plan" ||
		argoPlan["argocd_api_called"] != false ||
		argoPlan["argocd_app_refresh_performed"] != false ||
		argoPlan["argo_revision_bound"] != false ||
		argoPlan["synced_state_written"] != false ||
		argoPlan["contains_provider_token"] != false ||
		argoPlan["contains_argo_response"] != false {
		t.Fatalf("Argo refresh subplan preview should stay redacted and not executed: %#v", argoPlan)
	}
	if argoPlan["refresh_state"] == "planned" && argoPlan["argocd_app_sync_enabled"] != true {
		t.Fatalf("planned Argo subplan should expose sync backend readiness: %#v", argoPlan)
	}
	for _, backend := range []string{"provider_mutation", "raw_argo_response_recording", "server_side_automatic_validation_rerun"} {
		if !containsString(stringSliceFromAny(argoPlan["disabled_backends"]), backend) {
			t.Fatalf("Argo subplan disabled backend missing %q: %#v", backend, argoPlan["disabled_backends"])
		}
	}
	for _, field := range []string{"provider_token", "authorization_header", "argo_response", "raw_argo_response", "provider_response_body", "provider_response_headers"} {
		if !containsString(stringSliceFromAny(argoPlan["suppressed_fields"]), field) {
			t.Fatalf("Argo subplan suppressed field missing %q: %#v", field, argoPlan["suppressed_fields"])
		}
	}
	resultPlan := mapFromAny(executionPlan["result_recording_plan"])
	if resultPlan["mode"] != "provider_refresh_result_recording_plan" ||
		resultPlan["result_recording_state"] != "blocked" ||
		resultPlan["result_recording_ready"] != false ||
		resultPlan["result_recording_ready_reason"] != "provider_refresh_execution_not_performed" ||
		resultPlan["recording_enabled"] != false ||
		resultPlan["result_written"] != false ||
		resultPlan["operation_log_written"] != false ||
		resultPlan["canonical_asset_sync_queued"] != false ||
		resultPlan["status_snapshot_written"] != false ||
		resultPlan["validation_rerun_recorded"] != false ||
		resultPlan["git_ref_fetch_result_recorded"] != false ||
		resultPlan["github_actions_result_recorded"] != false ||
		resultPlan["argo_revision_result_recorded"] != false ||
		resultPlan["raw_response_included"] != false ||
		resultPlan["raw_git_output_included"] != false ||
		resultPlan["raw_argo_response_included"] != false ||
		resultPlan["provider_request_id_included"] != false {
		t.Fatalf("refresh result recording plan should keep all result flags false: %#v", resultPlan)
	}
	workerBinding := mapFromAny(executionPlan["worker_result_binding_evidence"])
	if workerBinding["mode"] != "project_version_refresh_worker_result_binding_evidence" ||
		workerBinding["external_call_made"] != false ||
		workerBinding["provider_api_called"] != false ||
		workerBinding["git_fetch_performed"] != false ||
		workerBinding["argocd_api_called"] != false ||
		workerBinding["raw_response_included"] != false ||
		workerBinding["raw_git_output_included"] != false ||
		workerBinding["raw_argo_response_included"] != false ||
		workerBinding["secret_included"] != false ||
		workerBinding["contains_remote_url"] != false ||
		workerBinding["contains_provider_token"] != false ||
		workerBinding["contains_provider_response"] != false {
		t.Fatalf("refresh worker binding evidence should stay redacted: %#v", workerBinding)
	}
	for _, field := range []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body", "operation_error_detail"} {
		if !containsString(stringSliceFromAny(workerBinding["suppressed_fields"]), field) {
			t.Fatalf("worker binding suppressed_fields missing %q: %#v", field, workerBinding["suppressed_fields"])
		}
	}
	for _, field := range []string{"operation_run_id", "refresh_kind", "status", "started_at", "finished_at", "synced_entity_count", "git_ref_fetch_status", "github_actions_refresh_status", "argo_revision_refresh_status", "validation_rerun_status"} {
		if !containsString(stringSliceFromAny(resultPlan["required_result_fields"]), field) {
			t.Fatalf("result plan required_result_fields missing %q: %#v", field, resultPlan["required_result_fields"])
		}
	}
	for _, field := range []string{"remote_url", "provider_token", "authorization_header", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body"} {
		if !containsString(stringSliceFromAny(resultPlan["suppressed_fields"]), field) {
			t.Fatalf("result plan suppressed_fields missing %q: %#v", field, resultPlan["suppressed_fields"])
		}
	}
	for _, reason := range []string{"provider_refresh_execution_not_performed", "synced_state_write_not_performed", "validation_auto_reload_not_observed"} {
		if !containsString(stringSliceFromAny(resultPlan["blocked_reasons"]), reason) {
			t.Fatalf("result plan blocked_reasons missing %q: %#v", reason, resultPlan["blocked_reasons"])
		}
	}
	encodedExecutionPlan, _ := json.Marshal(executionPlan)
	for _, forbidden := range []string{"https://token@", "Bearer secret", "password=secret", "git@github.com:"} {
		if strings.Contains(string(encodedExecutionPlan), forbidden) {
			t.Fatalf("refresh execution plan leaked %q: %s", forbidden, encodedExecutionPlan)
		}
	}
}

func assertProjectVersionBackgroundRerunPlanSafe(t *testing.T, plan map[string]any) {
	t.Helper()
	if plan["standalone_background_worker_enabled"] != true ||
		plan["control_worker_auto_snapshot_supported"] != true ||
		plan["external_call_made"] != false ||
		plan["provider_api_called"] != false ||
		plan["git_fetch_performed"] != false ||
		plan["argocd_api_called"] != false ||
		plan["raw_response_included"] != false ||
		plan["secret_included"] != false {
		t.Fatalf("background rerun plan should stay redacted and local-only: %#v", plan)
	}
	for _, control := range []string{"terminal_refresh_workers", "server_side_validation_recheck", "validation_snapshot_write_audit", "control_worker_auto_snapshot_review", "standalone_background_worker_policy_review"} {
		if !containsString(stringSliceFromAny(plan["required_controls"]), control) {
			t.Fatalf("background rerun required control missing %q: %#v", control, plan["required_controls"])
		}
	}
	for _, backend := range []string{"raw_provider_response_recording"} {
		if !containsString(stringSliceFromAny(plan["disabled_backends"]), backend) {
			t.Fatalf("background rerun disabled backend missing %q: %#v", backend, plan["disabled_backends"])
		}
	}
	if containsString(stringSliceFromAny(plan["disabled_backends"]), "standalone_background_validation_worker") {
		t.Fatalf("background rerun should not report standalone worker as disabled: %#v", plan["disabled_backends"])
	}
	for _, field := range []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body"} {
		if !containsString(stringSliceFromAny(plan["suppressed_fields"]), field) {
			t.Fatalf("background rerun suppressed field missing %q: %#v", field, plan["suppressed_fields"])
		}
	}
	encoded, _ := json.Marshal(plan)
	for _, forbidden := range []string{"https://token@", "Bearer secret", "raw_provider_response\":true", "raw_git_output\":\""} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("background rerun plan leaked %q: %s", forbidden, encoded)
		}
	}
}

func assertProjectVersionValidationSnapshotWritePlanSafe(t *testing.T, plan map[string]any) {
	t.Helper()
	if plan["mode"] != "project_version_validation_snapshot_write_plan" ||
		plan["snapshot_write_enabled"] != false ||
		plan["validation_snapshot_written"] != false ||
		plan["asset_status_snapshot_written"] != false ||
		plan["operation_log_written"] != false ||
		plan["background_worker_enqueued"] != false ||
		plan["automatic_background_rerun"] != false ||
		plan["control_worker_auto_snapshot_supported"] != true ||
		plan["external_call_made"] != false ||
		plan["provider_api_called"] != false ||
		plan["git_fetch_performed"] != false ||
		plan["argocd_api_called"] != false ||
		plan["raw_response_included"] != false ||
		plan["secret_included"] != false {
		t.Fatalf("validation snapshot write plan should stay disabled and redacted: %#v", plan)
	}
	for _, field := range []string{"project_version_id", "validation_state", "repository_count", "ready_count", "partial_count", "blocked_count", "provider_refresh_status", "operation_count", "server_side_validation_recheck_status"} {
		if !containsString(stringSliceFromAny(plan["required_snapshot_fields"]), field) {
			t.Fatalf("snapshot write required_snapshot_fields missing %q: %#v", field, plan["required_snapshot_fields"])
		}
	}
	for _, control := range []string{"terminal_refresh_workers", "server_side_validation_recheck", "snapshot_schema_review", "snapshot_operator_review", "asset_status_snapshot_audit", "operation_log_redaction_review"} {
		if !containsString(stringSliceFromAny(plan["required_controls"]), control) {
			t.Fatalf("snapshot write required control missing %q: %#v", control, plan["required_controls"])
		}
	}
	for _, backend := range []string{"operation_log_write", "raw_provider_response_recording"} {
		if !containsString(stringSliceFromAny(plan["disabled_backends"]), backend) {
			t.Fatalf("snapshot write disabled backend missing %q: %#v", backend, plan["disabled_backends"])
		}
	}
	if containsString(stringSliceFromAny(plan["disabled_backends"]), "standalone_background_validation_worker") {
		t.Fatalf("snapshot write should not report standalone worker as disabled: %#v", plan["disabled_backends"])
	}
	for _, field := range []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body", "repository_ref"} {
		if !containsString(stringSliceFromAny(plan["suppressed_fields"]), field) {
			t.Fatalf("snapshot write suppressed field missing %q: %#v", field, plan["suppressed_fields"])
		}
	}
	for _, reason := range []string{"validation_snapshot_write_disabled"} {
		if !containsString(stringSliceFromAny(plan["blocked_reasons"]), reason) {
			t.Fatalf("snapshot write blocked reason missing %q: %#v", reason, plan["blocked_reasons"])
		}
	}
	encoded, _ := json.Marshal(plan)
	for _, forbidden := range []string{"https://token@", "Bearer secret", "raw_provider_response\":true", "raw_git_output\":\"", "workflow_logs\":\""} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("snapshot write plan leaked %q: %s", forbidden, encoded)
		}
	}
}

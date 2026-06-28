package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func assertConfigRepositoryGitCommitSubplansSafe(t *testing.T, commitPlan map[string]any) {
	t.Helper()
	approvalPlan := mapFromAny(commitPlan["approval_request_plan"])
	for _, field := range []string{"repositories[].repo_key", "repositories[].remote_id", "repositories[].config_commit_sha"} {
		if !containsString(stringSliceFromAny(commitPlan["required_project_version_metadata"]), field) {
			t.Fatalf("config commit required ProjectVersion metadata missing %q: %#v", field, commitPlan["required_project_version_metadata"])
		}
	}
	if approvalPlan["mode"] != "config_repository_git_commit_approval_plan" ||
		approvalPlan["operation_created"] != false ||
		approvalPlan["approval_request_created"] != false ||
		approvalPlan["worker_job_created"] != false ||
		approvalPlan["external_call_made"] != false {
		t.Fatalf("config git approval plan should stay redacted: %#v", approvalPlan)
	}
	if commitPlan["plan_state"] == "planned" {
		if approvalPlan["request_ready"] != true || approvalPlan["request_ready_reason"] != "config_git_commit_metadata_ready" {
			t.Fatalf("planned config commit should allow approval request metadata: %#v", approvalPlan)
		}
	} else if approvalPlan["request_ready"] != false || approvalPlan["request_ready_reason"] != "config_git_commit_metadata_blocked" {
		t.Fatalf("blocked config commit should block approval request metadata: %#v", approvalPlan)
	}
	if commitPlan["plan_state"] == "planned" && approvalPlan["metadata_ready"] != true {
		t.Fatalf("planned config commit should mark approval metadata ready: %#v", approvalPlan)
	}
	if commitPlan["plan_state"] == "planned" && len(stringSliceFromAny(approvalPlan["blocked_reasons"])) != 0 {
		t.Fatalf("planned config commit should not report metadata blockers: %#v", approvalPlan["blocked_reasons"])
	}
	for _, field := range []string{"operation_run_id", "repository_id", "remote_id", "default_branch", "scaffold_file_count", "requested_by", "reason"} {
		if !containsString(stringSliceFromAny(approvalPlan["required_approval_fields"]), field) {
			t.Fatalf("config approval required fields missing %q: %#v", field, approvalPlan["required_approval_fields"])
		}
	}
	for _, field := range []string{"file_content", "secret_values", "git_credentials", "provider_token", "remote_url", "branch_name", "commit_message", "author_email"} {
		if !containsString(stringSliceFromAny(approvalPlan["suppressed_fields"]), field) {
			t.Fatalf("config approval suppressed_fields missing %q: %#v", field, approvalPlan["suppressed_fields"])
		}
	}
	for _, reason := range []string{"git_workspace_backend_disabled", "git_commit_not_created", "provider_review_workflow_not_wired", "project_version_pin_write_disabled"} {
		if !containsString(stringSliceFromAny(approvalPlan["execution_blockers"]), reason) {
			t.Fatalf("config approval execution blockers missing %q: %#v", reason, approvalPlan["execution_blockers"])
		}
	}

	workspacePlan := mapFromAny(commitPlan["workspace_execution_plan"])
	if workspacePlan["mode"] != "config_repository_git_workspace_plan" ||
		workspacePlan["workspace_state"] != "blocked" ||
		workspacePlan["workspace_ready"] != false ||
		workspacePlan["workspace_ready_reason"] != "config_git_workspace_backend_disabled" ||
		workspacePlan["workspace_bound"] != false ||
		workspacePlan["git_clone_performed"] != false ||
		workspacePlan["file_content_materialized"] != false ||
		workspacePlan["secret_scan_performed"] != false ||
		workspacePlan["git_commit_created"] != false ||
		workspacePlan["git_push_performed"] != false ||
		workspacePlan["provider_review_created"] != false ||
		workspacePlan["external_call_made"] != false ||
		workspacePlan["contains_file_content"] != false ||
		workspacePlan["contains_secret_values"] != false {
		t.Fatalf("config workspace plan should stay disabled and redacted: %#v", workspacePlan)
	}
	if commitPlan["plan_state"] == "planned" && workspacePlan["metadata_ready"] != true {
		t.Fatalf("planned config commit should mark workspace metadata ready: %#v", workspacePlan)
	}
	for _, field := range []string{"operation_run_id", "repository_id", "remote_id", "workspace_id", "scaffold_file_count", "secret_scan_status", "commit_author"} {
		if !containsString(stringSliceFromAny(workspacePlan["required_workspace_fields"]), field) {
			t.Fatalf("config workspace required fields missing %q: %#v", field, workspacePlan["required_workspace_fields"])
		}
	}
	for _, field := range []string{"file_content", "secret_values", "git_credentials", "provider_token", "remote_url", "branch_name", "commit_message", "author_email"} {
		if !containsString(stringSliceFromAny(workspacePlan["suppressed_fields"]), field) {
			t.Fatalf("config workspace suppressed_fields missing %q: %#v", field, workspacePlan["suppressed_fields"])
		}
	}
	for _, reason := range []string{"git_workspace_backend_disabled", "secret_scan_not_performed", "git_commit_not_created", "provider_review_not_created"} {
		if !containsString(stringSliceFromAny(workspacePlan["blocked_reasons"]), reason) {
			t.Fatalf("config workspace blocked reasons missing %q: %#v", reason, workspacePlan["blocked_reasons"])
		}
	}

	remoteReviewPlan := mapFromAny(commitPlan["remote_review_plan"])
	if remoteReviewPlan["mode"] != "config_repository_remote_review_plan" ||
		remoteReviewPlan["review_state"] == "" ||
		remoteReviewPlan["git_push_performed"] != false ||
		remoteReviewPlan["review_branch_pushed"] != false ||
		remoteReviewPlan["provider_review_created"] != false ||
		remoteReviewPlan["provider_review_link_recorded"] != false ||
		remoteReviewPlan["external_call_made"] != false ||
		remoteReviewPlan["contains_token"] != false ||
		remoteReviewPlan["contains_remote_url"] != false ||
		remoteReviewPlan["contains_branch_name"] != false ||
		remoteReviewPlan["contains_commit_message"] != false ||
		remoteReviewPlan["contains_provider_response"] != false {
		t.Fatalf("config remote review plan should stay disabled and redacted: %#v", remoteReviewPlan)
	}
	if commitPlan["plan_state"] == "planned" && (remoteReviewPlan["metadata_ready"] != true || remoteReviewPlan["review_state"] != "planned") {
		t.Fatalf("planned config commit should mark remote review metadata ready: %#v", remoteReviewPlan)
	}
	if remoteReviewPlan["protected_default_branch_avoided"] != true {
		t.Fatalf("config remote review should avoid protected default branch: %#v", remoteReviewPlan)
	}
	for _, field := range []string{"operation_run_id", "repository_id", "remote_id", "review_branch_key", "base_branch_key", "commit_sha_status", "provider_review_status"} {
		if !containsString(stringSliceFromAny(remoteReviewPlan["required_review_fields"]), field) {
			t.Fatalf("config remote review required fields missing %q: %#v", field, remoteReviewPlan["required_review_fields"])
		}
	}
	for _, control := range []string{"branch_policy_review", "protected_branch_avoidance", "provider_review_workflow", "provider_response_redaction", "operator_review_before_merge"} {
		if !containsString(stringSliceFromAny(remoteReviewPlan["required_controls"]), control) {
			t.Fatalf("config remote review controls missing %q: %#v", control, remoteReviewPlan["required_controls"])
		}
	}
	for _, step := range []string{"derive_review_branch", "push_review_branch", "open_provider_review_request", "record_review_request_summary", "wait_for_operator_merge"} {
		if !containsString(stringSliceFromAny(remoteReviewPlan["execution_sequence"]), step) {
			t.Fatalf("config remote review sequence missing %q: %#v", step, remoteReviewPlan["execution_sequence"])
		}
	}
	for _, backend := range []string{"git_push", "pull_request_create", "merge_request_create", "provider_review_link_write", "provider_response_recording"} {
		if !containsString(stringSliceFromAny(remoteReviewPlan["disabled_backends"]), backend) {
			t.Fatalf("config remote review disabled backend missing %q: %#v", backend, remoteReviewPlan["disabled_backends"])
		}
	}
	for _, field := range []string{"remote_url", "branch_name", "commit_message", "commit_sha", "git_credentials", "provider_token", "authorization_header", "provider_response_body", "provider_response_headers"} {
		if !containsString(stringSliceFromAny(remoteReviewPlan["suppressed_fields"]), field) {
			t.Fatalf("config remote review suppressed_fields missing %q: %#v", field, remoteReviewPlan["suppressed_fields"])
		}
	}
	for _, reason := range []string{"git_push_not_performed", "provider_review_workflow_not_wired", "provider_review_not_created"} {
		if !containsString(stringSliceFromAny(remoteReviewPlan["blocked_reasons"]), reason) {
			t.Fatalf("config remote review blocked reasons missing %q: %#v", reason, remoteReviewPlan["blocked_reasons"])
		}
	}
	for _, blocker := range []string{"git_push_not_performed", "provider_review_workflow_not_wired"} {
		if !containsString(stringSliceFromAny(remoteReviewPlan["execution_blockers"]), blocker) {
			t.Fatalf("config remote review execution blockers missing %q: %#v", blocker, remoteReviewPlan["execution_blockers"])
		}
	}

	pinPlan := mapFromAny(commitPlan["project_version_pin_plan"])
	if pinPlan["mode"] != "config_repository_project_version_pin_validation_plan" ||
		pinPlan["pin_state"] != "blocked" ||
		pinPlan["pin_ready"] != false ||
		pinPlan["pin_ready_reason"] != "config_commit_sha_pin_write_disabled" ||
		pinPlan["project_version_pin_written"] != false ||
		pinPlan["config_commit_sha_recorded"] != false ||
		pinPlan["live_commit_validation_started"] != false ||
		pinPlan["live_commit_validation_recorded"] != false ||
		pinPlan["git_fetch_performed"] != false ||
		pinPlan["external_call_made"] != false ||
		pinPlan["contains_commit_sha"] != false ||
		pinPlan["contains_remote_url"] != false {
		t.Fatalf("config ProjectVersion pin plan should stay disabled and redacted: %#v", pinPlan)
	}
	if commitPlan["plan_state"] == "planned" && pinPlan["metadata_ready"] != true {
		t.Fatalf("planned config commit should mark pin metadata ready: %#v", pinPlan)
	}
	for _, field := range []string{"project_version_id", "repository_id", "remote_id", "repo_key", "config_commit_sha", "validation_status"} {
		if !containsString(stringSliceFromAny(pinPlan["required_pin_fields"]), field) {
			t.Fatalf("config pin required fields missing %q: %#v", field, pinPlan["required_pin_fields"])
		}
	}
	for _, field := range []string{"remote_url", "branch_name", "commit_message", "commit_sha", "git_credentials", "provider_token", "provider_response_body"} {
		if !containsString(stringSliceFromAny(pinPlan["suppressed_fields"]), field) {
			t.Fatalf("config pin suppressed_fields missing %q: %#v", field, pinPlan["suppressed_fields"])
		}
	}
	for _, reason := range []string{"project_version_pin_write_disabled", "live_remote_commit_validation_not_performed"} {
		if !containsString(stringSliceFromAny(pinPlan["blocked_reasons"]), reason) {
			t.Fatalf("config pin blocked reasons missing %q: %#v", reason, pinPlan["blocked_reasons"])
		}
	}
	pinWritePreflight := mapFromAny(pinPlan["pin_write_preflight_plan"])
	assertConfigRepositoryPinWritePreflightPlanSafe(t, pinWritePreflight)
	if commitPlan["plan_state"] == "planned" && pinWritePreflight["preflight_state"] != "metadata_review_ready" {
		t.Fatalf("planned config commit should make pin write preflight metadata-review-ready: %#v", pinWritePreflight)
	}
	encoded, _ := json.Marshal(commitPlan)
	for _, forbidden := range []string{"secret_values_here", "git@github.com", "https://token@", "Bearer", "password", "author@example.com"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("config git commit subplans leaked %q: %s", forbidden, encoded)
		}
	}
}

func assertConfigRepositoryPinWritePreflightPlanSafe(t *testing.T, plan map[string]any) {
	t.Helper()
	if plan["mode"] != "config_repository_project_version_pin_write_preflight_plan" ||
		plan["project_version_pin_written"] != false ||
		plan["project_version_update_enabled"] != false ||
		plan["project_version_metadata_written"] != false ||
		plan["live_commit_validation_started"] != false ||
		plan["live_remote_validation_performed"] != false ||
		plan["git_fetch_performed"] != false ||
		plan["external_call_made"] != false ||
		plan["contains_commit_sha"] != false ||
		plan["contains_remote_url"] != false ||
		plan["contains_git_credentials"] != false ||
		plan["contains_provider_token"] != false {
		t.Fatalf("config pin write preflight should stay disabled and redacted: %#v", plan)
	}
	for _, field := range []string{"project_version_id", "repository_id", "remote_id", "repo_key", "config_commit_sha", "pin_source_operation_run_id", "validation_status", "reviewed_by"} {
		if !containsString(stringSliceFromAny(plan["required_write_fields"]), field) {
			t.Fatalf("config pin write required_write_fields missing %q: %#v", field, plan["required_write_fields"])
		}
	}
	for _, control := range []string{"operator_review", "config_commit_sha_source_review", "project_version_metadata_schema_review", "live_remote_validation_review", "redacted_pin_result_recording"} {
		if !containsString(stringSliceFromAny(plan["required_controls"]), control) {
			t.Fatalf("config pin write required_controls missing %q: %#v", control, plan["required_controls"])
		}
	}
	for _, backend := range []string{"project_version_update", "live_commit_validation", "git_fetch", "remote_commit_lookup", "operation_log_write"} {
		if !containsString(stringSliceFromAny(plan["disabled_backends"]), backend) {
			t.Fatalf("config pin write disabled_backends missing %q: %#v", backend, plan["disabled_backends"])
		}
	}
	for _, field := range []string{"config_commit_sha", "remote_url", "branch_name", "commit_message", "git_credentials", "provider_token", "authorization_header", "provider_response_body", "provider_response_headers", "operator_identity"} {
		if !containsString(stringSliceFromAny(plan["suppressed_fields"]), field) {
			t.Fatalf("config pin write suppressed_fields missing %q: %#v", field, plan["suppressed_fields"])
		}
	}
	for _, reason := range []string{"project_version_pin_write_disabled"} {
		if !containsString(stringSliceFromAny(plan["blocked_reasons"]), reason) {
			t.Fatalf("config pin write blocked_reasons missing %q: %#v", reason, plan["blocked_reasons"])
		}
	}
	if plan["preflight_state"] == "observed" && plan["pin_write_ready_for_review"] != false {
		t.Fatalf("observed config pin write preflight should not be ready for a new pin write: %#v", plan)
	}
	encoded, _ := json.Marshal(plan)
	for _, forbidden := range []string{"secret_values_here", "git@github.com", "https://token@", "Bearer", "password", "author@example.com", "abc123"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("config pin write preflight leaked %q: %s", forbidden, encoded)
		}
	}
}

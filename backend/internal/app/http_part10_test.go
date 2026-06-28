package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestConfigRepositoryScaffoldPreview(t *testing.T) {
	preview := configRepositoryScaffoldPreview(map[string]any{
		"id":             "repo-1",
		"name":           "Config Repository",
		"repo_key":       "config",
		"repo_role":      "config",
		"default_branch": "main",
	}, []map[string]any{{
		"id":               "remote-1",
		"name":             "origin",
		"remote_key":       "github",
		"provider_type":    "github",
		"remote_role":      "target",
		"default_branch":   "main",
		"latest_sha":       "abc123",
		"last_sync_status": "completed",
	}})

	if preview["mode"] != "config_repository_scaffold_preview" ||
		preview["scaffold_state"] != "ready" ||
		preview["git_write_performed"] != false ||
		preview["external_call_made"] != false ||
		preview["file_content_included"] != false ||
		preview["secret_included"] != false ||
		preview["file_count"] != 10 ||
		preview["remote_count"] != 1 {
		t.Fatalf("config scaffold preview = %#v", preview)
	}
	files := sliceOfMapsFromAny(preview["files"])
	if len(files) != 10 {
		t.Fatalf("files = %#v", files)
	}
	paths := map[string]bool{}
	for _, file := range files {
		paths[fmt.Sprint(file["path"])] = true
		if fmt.Sprint(file["path"]) == "envs/prod/secrets.example.yaml" && file["required"] != true {
			t.Fatalf("prod secrets example should be required: %#v", file)
		}
	}
	for _, path := range []string{"envs/dev/values.yaml", "envs/test/README.md", "envs/prod/secrets.example.yaml", "README.md"} {
		if !paths[path] {
			t.Fatalf("missing scaffold path %s in %#v", path, paths)
		}
	}
	suppressed := stringSliceFromAny(preview["suppressed_fields"])
	if !containsString(suppressed, "secret_values") || !containsString(suppressed, "git_credentials") {
		t.Fatalf("suppressed fields = %#v", suppressed)
	}
	commitPlan := mapFromAny(preview["git_commit_plan"])
	if commitPlan["mode"] != "config_repository_git_commit_plan_preview" ||
		commitPlan["plan_state"] != "planned" ||
		commitPlan["execution_enabled"] != true ||
		commitPlan["execution_mode"] != "approval_gated_audit_only" ||
		commitPlan["operation_request_enabled"] != true ||
		commitPlan["git_clone_performed"] != false ||
		commitPlan["git_commit_created"] != false ||
		commitPlan["git_push_performed"] != false ||
		commitPlan["project_version_pin_written"] != false ||
		commitPlan["live_commit_validation_performed"] != false ||
		commitPlan["file_content_materialized"] != false ||
		commitPlan["scaffold_file_count"] != 10 ||
		commitPlan["remote_count"] != 1 {
		t.Fatalf("git commit plan = %#v", commitPlan)
	}
	if !containsString(stringSliceFromAny(commitPlan["required_controls"]), "project_version_config_commit_pin") ||
		!containsString(stringSliceFromAny(commitPlan["disabled_backends"]), "git_commit") ||
		!containsString(stringSliceFromAny(commitPlan["disabled_backends"]), "live_commit_validation") ||
		!containsString(stringSliceFromAny(commitPlan["enabled_backends"]), "operation_run_enqueue") ||
		!containsString(stringSliceFromAny(commitPlan["enabled_backends"]), "sanitized_audit_result_recording") ||
		!containsString(stringSliceFromAny(commitPlan["suppressed_fields"]), "remote_url") ||
		statusByKind(sliceOfMapsFromAny(commitPlan["steps"]), "workspace_checkout") != "blocked" ||
		statusByKind(sliceOfMapsFromAny(commitPlan["steps"]), "remote_binding") != "planned" {
		t.Fatalf("git commit plan controls/backends/steps = %#v", commitPlan)
	}
	assertConfigRepositoryGitCommitSubplansSafe(t, commitPlan)
	promotionPlan := mapFromAny(commitPlan["promotion_readiness_plan"])
	if promotionPlan["mode"] != "config_repository_audit_to_live_promotion_readiness_plan" ||
		promotionPlan["promotion_state"] != "blocked" ||
		promotionPlan["promotion_ready"] != false ||
		promotionPlan["promotion_ready_reason"] != "config_git_commit_audit_result_not_recorded" ||
		promotionPlan["live_git_workflow_enabled"] != false ||
		promotionPlan["git_workspace_mutation_enabled"] != false ||
		promotionPlan["git_commit_created"] != false ||
		promotionPlan["git_push_performed"] != false ||
		promotionPlan["provider_review_created"] != false ||
		promotionPlan["project_version_pin_written"] != false ||
		promotionPlan["live_remote_validation_performed"] != false ||
		promotionPlan["external_call_made"] != false ||
		promotionPlan["contains_file_content"] != false ||
		promotionPlan["contains_remote_url"] != false ||
		promotionPlan["contains_credentials"] != false ||
		promotionPlan["contains_git_output"] != false ||
		promotionPlan["contains_provider_response"] != false {
		t.Fatalf("promotion readiness plan should stay blocked and redacted: %#v", promotionPlan)
	}
	for _, field := range []string{"file_content", "secret_values", "git_credentials", "provider_token", "remote_url", "branch_name", "commit_message", "commit_sha", "git_output", "provider_response_body", "provider_response_headers"} {
		if !containsString(stringSliceFromAny(promotionPlan["suppressed_fields"]), field) {
			t.Fatalf("promotion suppressed fields missing %q: %#v", field, promotionPlan["suppressed_fields"])
		}
	}
	resultPlan := mapFromAny(commitPlan["result_recording_plan"])
	if resultPlan["mode"] != "config_repository_git_commit_result_recording_plan" ||
		resultPlan["result_recording_state"] != "blocked" ||
		resultPlan["result_recording_ready"] != false ||
		resultPlan["result_recording_ready_reason"] != "config_git_commit_execution_not_performed" ||
		resultPlan["recording_enabled"] != false ||
		resultPlan["result_written"] != false ||
		resultPlan["operation_log_written"] != false ||
		resultPlan["scaffold_artifact_recorded"] != false ||
		resultPlan["commit_record_written"] != false ||
		resultPlan["push_record_written"] != false ||
		resultPlan["review_request_recorded"] != false ||
		resultPlan["remote_review_subplan_recorded"] != false ||
		resultPlan["project_version_pin_written"] != false ||
		resultPlan["config_commit_sha_recorded"] != false ||
		resultPlan["live_validation_recorded"] != false ||
		resultPlan["raw_file_content_recorded"] != false ||
		resultPlan["raw_secret_value_recorded"] != false ||
		resultPlan["raw_git_output_recorded"] != false ||
		resultPlan["raw_provider_response_recorded"] != false ||
		resultPlan["contains_token"] != false ||
		resultPlan["contains_remote_url"] != false ||
		resultPlan["contains_branch_name"] != false ||
		resultPlan["contains_commit_message"] != false {
		t.Fatalf("git commit result recording plan should stay disabled and redacted: %#v", resultPlan)
	}
	resultPromotionPlan := mapFromAny(resultPlan["promotion_readiness_plan"])
	if resultPromotionPlan["promotion_state"] != "blocked" ||
		resultPromotionPlan["promotion_ready"] != false ||
		resultPromotionPlan["git_commit_created"] != false ||
		resultPromotionPlan["project_version_pin_written"] != false ||
		resultPromotionPlan["live_remote_validation_performed"] != false {
		t.Fatalf("result promotion readiness should remain blocked: %#v", resultPromotionPlan)
	}
	for _, required := range []string{"scaffold_file_count", "secret_scan_status", "commit_created", "push_performed", "review_request_created", "remote_review_state", "config_commit_sha_present", "live_validation_status"} {
		if !containsString(stringSliceFromAny(resultPlan["result_diagnostic_fields"]), required) {
			t.Fatalf("result diagnostic fields missing %q: %#v", required, resultPlan["result_diagnostic_fields"])
		}
	}
	for _, field := range []string{"file_content", "secret_values", "git_credentials", "provider_token", "remote_url", "branch_name", "commit_message", "commit_sha", "provider_response_body", "provider_response_headers"} {
		if !containsString(stringSliceFromAny(resultPlan["suppressed_fields"]), field) {
			t.Fatalf("result suppressed fields missing %q: %#v", field, resultPlan["suppressed_fields"])
		}
	}
	encodedCommitPlan, _ := json.Marshal(commitPlan)
	for _, forbidden := range []string{"secret_values_here", "git@github.com", "https://token@", "Bearer", "password"} {
		if strings.Contains(string(encodedCommitPlan), forbidden) {
			t.Fatalf("git commit plan leaked %q: %s", forbidden, encodedCommitPlan)
		}
	}

	blocked := configRepositoryScaffoldPreview(map[string]any{
		"id":        "repo-2",
		"name":      "Service",
		"repo_key":  "service",
		"repo_role": "service",
	}, nil)
	if blocked["scaffold_state"] != "blocked" {
		t.Fatalf("blocked scaffold state = %#v", blocked)
	}
	reasons := stringSliceFromAny(blocked["blocked_reasons"])
	if !containsString(reasons, "repository_role_is_not_config") || !containsString(reasons, "config_remote_missing") {
		t.Fatalf("blocked reasons = %#v", reasons)
	}
	blockedCommitPlan := mapFromAny(blocked["git_commit_plan"])
	if blockedCommitPlan["plan_state"] != "blocked" ||
		statusByKind(sliceOfMapsFromAny(blockedCommitPlan["steps"]), "scaffold_review") != "blocked" ||
		statusByKind(sliceOfMapsFromAny(blockedCommitPlan["steps"]), "remote_binding") != "blocked" {
		t.Fatalf("blocked git commit plan = %#v", blockedCommitPlan)
	}
	blockedResultPlan := mapFromAny(blockedCommitPlan["result_recording_plan"])
	if blockedResultPlan["result_recording_state"] != "blocked" ||
		blockedResultPlan["recording_enabled"] != false ||
		blockedResultPlan["result_written"] != false ||
		blockedResultPlan["project_version_pin_written"] != false {
		t.Fatalf("blocked result recording plan should remain disabled: %#v", blockedResultPlan)
	}
	blockedApprovalPlan := mapFromAny(blockedCommitPlan["approval_request_plan"])
	if blockedApprovalPlan["metadata_ready"] != false ||
		!containsString(stringSliceFromAny(blockedApprovalPlan["blocked_reasons"]), "repository_role_is_not_config") ||
		!containsString(stringSliceFromAny(blockedApprovalPlan["blocked_reasons"]), "config_remote_missing") {
		t.Fatalf("blocked approval plan should explain missing config metadata: %#v", blockedApprovalPlan)
	}
	blockedRemoteReviewPlan := mapFromAny(blockedCommitPlan["remote_review_plan"])
	if blockedRemoteReviewPlan["review_state"] != "blocked" ||
		blockedRemoteReviewPlan["metadata_ready"] != false ||
		!containsString(stringSliceFromAny(blockedRemoteReviewPlan["blocked_reasons"]), "config_remote_missing") ||
		!containsString(stringSliceFromAny(blockedRemoteReviewPlan["blocked_reasons"]), "default_branch_missing") {
		t.Fatalf("blocked remote review plan should explain missing remote/default branch metadata: %#v", blockedRemoteReviewPlan)
	}
	assertConfigRepositoryGitCommitSubplansSafe(t, blockedCommitPlan)

	nilRole := configRepositoryScaffoldPreview(map[string]any{
		"id":        "repo-3",
		"name":      "Existing",
		"repo_key":  "existing",
		"repo_role": nil,
	}, nil)
	if nilRole["repo_role"] != "" {
		t.Fatalf("nil repo role should not leak as string: %#v", nilRole["repo_role"])
	}
	nilRoleCommitPlan := mapFromAny(nilRole["git_commit_plan"])
	if nilRoleCommitPlan["plan_state"] != "blocked" ||
		statusByKind(sliceOfMapsFromAny(nilRoleCommitPlan["steps"]), "scaffold_review") != "blocked" {
		t.Fatalf("nil-role git commit plan = %#v", nilRoleCommitPlan)
	}
}

func TestPinConfigCommitMetadataUpdatesExistingManifest(t *testing.T) {
	next, changed, err := pinConfigCommitMetadata(map[string]any{
		"release": "v1",
		"repositories": []map[string]any{{
			"repository_id": "repo-1",
			"repo_key":      "config",
			"tag":           "keep-me",
		}},
	}, map[string]any{
		"id":        "repo-1",
		"repo_key":  "config",
		"repo_role": "config",
	}, map[string]any{
		"id":            "remote-1",
		"remote_key":    "github",
		"remote_role":   "target",
		"provider_type": "github",
	}, "abc123")
	if err != nil {
		t.Fatalf("pinConfigCommitMetadata returned error: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	if next["release"] != "v1" {
		t.Fatalf("release field not preserved: %#v", next)
	}
	repositories := sliceOfMapsFromAny(next["repositories"])
	if len(repositories) != 1 {
		t.Fatalf("repositories = %#v", repositories)
	}
	item := repositories[0]
	expected := map[string]any{
		"repository_id":     "repo-1",
		"repo_key":          "config",
		"repo_role":         "config",
		"remote_id":         "remote-1",
		"remote_key":        "github",
		"remote_role":       "target",
		"provider_type":     "github",
		"config_commit_sha": "abc123",
		"validation_status": "local_synced_remote_latest_sha",
		"tag":               "keep-me",
	}
	for key, value := range expected {
		if item[key] != value {
			t.Fatalf("item[%s] = %#v, want %#v in %#v", key, item[key], value, item)
		}
	}
}

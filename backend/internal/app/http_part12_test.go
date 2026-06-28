package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestConfigRepositoryScaffoldPreviewReconcilesGitWorkflowAuditEvidence(t *testing.T) {
	preview := configRepositoryScaffoldPreview(
		map[string]any{
			"id":             "repo-1",
			"name":           "Config Repository",
			"repo_key":       "config",
			"repo_role":      "config",
			"default_branch": "main",
		},
		[]map[string]any{{
			"id":               "remote-1",
			"name":             "origin",
			"remote_key":       "github",
			"provider_type":    "github",
			"remote_role":      "target",
			"default_branch":   "main",
			"latest_sha":       "abc123",
			"last_sync_status": "completed",
		}},
		nil,
		[]map[string]any{{
			"id":                  "op-config-1",
			"status":              "completed",
			"created_at":          time.Now(),
			"updated_at":          time.Now(),
			"started_at":          time.Now(),
			"finished_at":         time.Now(),
			"operation_log_count": int64(2),
			"remote_url":          "https://token@example.com/repo.git",
			"provider_token":      "Bearer secret",
			"commit_sha":          "abc123",
			"branch_name":         "main",
			"git_output":          "secret output",
		}},
	)

	evidence := mapFromAny(preview["git_workflow_audit_evidence"])
	if evidence["mode"] != "config_repository_git_workflow_audit_evidence" ||
		evidence["evidence_state"] != "recorded" ||
		evidence["has_audit_operations"] != true ||
		evidence["sanitized_result_recorded"] != true ||
		intFromAny(evidence["operation_count"], 0) != 1 ||
		intFromAny(evidence["completed_count"], 0) != 1 ||
		intFromAny(evidence["operation_log_count"], 0) != 2 ||
		evidence["git_write_performed"] != false ||
		evidence["external_call_made"] != false ||
		evidence["file_content_included"] != false ||
		evidence["secret_included"] != false ||
		evidence["raw_git_output_recorded"] != false ||
		evidence["raw_provider_response_recorded"] != false ||
		evidence["project_version_pin_written"] != false ||
		evidence["live_commit_validation_performed"] != false {
		t.Fatalf("unexpected workflow audit evidence: %#v", evidence)
	}
	items := sliceOfMapsFromAny(evidence["items"])
	if len(items) != 1 ||
		items[0]["operation_run_id"] != "op-config-1" ||
		items[0]["status"] != "completed" ||
		intFromAny(items[0]["operation_log_count"], 0) != 2 ||
		items[0]["git_commit_created"] != false ||
		items[0]["git_push_performed"] != false ||
		items[0]["project_version_pin_written"] != false {
		t.Fatalf("unexpected workflow audit evidence items: %#v", items)
	}
	commitPlan := mapFromAny(preview["git_commit_plan"])
	if commitPlan["audit_operation_observed"] != true ||
		commitPlan["sanitized_result_observed"] != true ||
		commitPlan["git_commit_created"] != false ||
		commitPlan["git_push_performed"] != false ||
		commitPlan["project_version_pin_written"] != false {
		t.Fatalf("unexpected commit plan audit evidence: %#v", commitPlan)
	}
	resultPlan := mapFromAny(commitPlan["result_recording_plan"])
	if resultPlan["result_recording_state"] != "audit_recorded" ||
		resultPlan["result_recording_ready"] != true ||
		resultPlan["result_written"] != true ||
		resultPlan["operation_log_written"] != true ||
		resultPlan["sanitized_audit_result_recorded"] != true ||
		resultPlan["project_version_pin_written"] != false ||
		resultPlan["config_commit_sha_recorded"] != false ||
		resultPlan["live_validation_recorded"] != false ||
		resultPlan["raw_git_output_recorded"] != false ||
		resultPlan["raw_provider_response_recorded"] != false {
		t.Fatalf("unexpected result plan audit evidence: %#v", resultPlan)
	}
	promotionPlan := mapFromAny(commitPlan["promotion_readiness_plan"])
	if promotionPlan["promotion_state"] != "audit_result_ready_for_review" ||
		promotionPlan["promotion_ready"] != true ||
		promotionPlan["sanitized_audit_result_recorded"] != true ||
		promotionPlan["git_commit_created"] != false ||
		promotionPlan["git_push_performed"] != false ||
		promotionPlan["project_version_pin_written"] != false ||
		promotionPlan["live_remote_validation_performed"] != false ||
		promotionPlan["external_call_made"] != false {
		t.Fatalf("unexpected promotion readiness for audit evidence: %#v", promotionPlan)
	}
	resultPromotionPlan := mapFromAny(resultPlan["promotion_readiness_plan"])
	if resultPromotionPlan["promotion_state"] != "audit_result_ready_for_review" ||
		resultPromotionPlan["promotion_ready"] != true ||
		resultPromotionPlan["provider_review_created"] != false {
		t.Fatalf("unexpected result promotion readiness for audit evidence: %#v", resultPromotionPlan)
	}
	encoded, _ := json.Marshal(preview)
	for _, forbidden := range []string{"https://token@", "Bearer secret", "secret output"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("workflow audit evidence leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestConfigRepositoryGitWorkflowAuditRequiresOperationLogEvidence(t *testing.T) {
	preview := configRepositoryScaffoldPreview(
		map[string]any{
			"id":             "repo-1",
			"name":           "Config Repository",
			"repo_key":       "config",
			"repo_role":      "config",
			"default_branch": "main",
		},
		[]map[string]any{{
			"id":               "remote-1",
			"name":             "origin",
			"remote_key":       "github",
			"provider_type":    "github",
			"remote_role":      "target",
			"default_branch":   "main",
			"latest_sha":       "abc123",
			"last_sync_status": "completed",
		}},
		nil,
		[]map[string]any{{
			"id":                  "op-config-1",
			"status":              "completed",
			"operation_log_count": int64(0),
			"git_output":          "secret output",
		}},
	)

	evidence := mapFromAny(preview["git_workflow_audit_evidence"])
	if evidence["evidence_state"] != "recorded" ||
		evidence["has_audit_operations"] != true ||
		evidence["sanitized_result_recorded"] != false ||
		intFromAny(evidence["operation_log_count"], 0) != 0 {
		t.Fatalf("terminal config workflow audit without logs should not record sanitized result: %#v", evidence)
	}
	commitPlan := mapFromAny(preview["git_commit_plan"])
	if commitPlan["audit_operation_observed"] != true ||
		commitPlan["sanitized_result_observed"] != false {
		t.Fatalf("commit plan should observe audit operation without sanitized result: %#v", commitPlan)
	}
	resultPlan := mapFromAny(commitPlan["result_recording_plan"])
	if resultPlan["result_recording_state"] != "blocked" ||
		resultPlan["result_recording_ready"] != false ||
		resultPlan["recording_enabled"] != false ||
		resultPlan["result_written"] != false ||
		resultPlan["operation_log_written"] != false ||
		resultPlan["result_recording_ready_reason"] != "config_git_commit_audit_operation_log_missing" ||
		resultPlan["sanitized_audit_result_recorded"] != false ||
		!containsString(stringSliceFromAny(resultPlan["blocked_reasons"]), "config_git_commit_audit_operation_log_missing") {
		t.Fatalf("terminal config workflow audit without logs should keep result recording blocked: %#v", resultPlan)
	}
	promotionPlan := mapFromAny(commitPlan["promotion_readiness_plan"])
	if promotionPlan["promotion_state"] != "blocked" ||
		promotionPlan["promotion_ready"] != false ||
		promotionPlan["promotion_ready_reason"] != "config_git_commit_audit_operation_log_missing" {
		t.Fatalf("terminal config workflow audit without logs should block promotion readiness: %#v", promotionPlan)
	}
	encoded, _ := json.Marshal(preview)
	if strings.Contains(string(encoded), "secret output") {
		t.Fatalf("config workflow audit without logs leaked git output: %s", encoded)
	}
}

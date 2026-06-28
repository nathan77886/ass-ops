package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigRepositoryGitWorkflowResultRecordingRequiresAuditEvidenceBeyondExistingPin(t *testing.T) {
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
		[]map[string]any{{
			"id":      "version-1",
			"version": "v0.1.0",
			"metadata": map[string]any{"repositories": []any{
				map[string]any{
					"repo_key":          "config",
					"repo_role":         "config",
					"remote_id":         "remote-1",
					"config_commit_sha": "abc123",
				},
			}},
		}},
		nil,
	)

	commitPlan := mapFromAny(preview["git_commit_plan"])
	if commitPlan["project_version_pin_observed"] != true ||
		commitPlan["live_commit_validation_observed"] != true ||
		commitPlan["audit_operation_observed"] != false {
		t.Fatalf("expected existing pin/live evidence without audit operation: %#v", commitPlan)
	}
	resultPlan := mapFromAny(commitPlan["result_recording_plan"])
	if resultPlan["result_recording_state"] != "partial" ||
		resultPlan["result_recording_ready"] != false ||
		resultPlan["recording_enabled"] != false ||
		resultPlan["result_written"] != false ||
		resultPlan["result_recording_ready_reason"] != "project_version_config_commit_pin_observed" ||
		resultPlan["project_version_pin_observed"] != true ||
		resultPlan["live_validation_recorded"] != true ||
		resultPlan["sanitized_audit_result_recorded"] != false {
		t.Fatalf("pin/live evidence alone should not claim result recording readiness: %#v", resultPlan)
	}
	promotionPlan := mapFromAny(commitPlan["promotion_readiness_plan"])
	if promotionPlan["promotion_state"] != "partial_evidence" ||
		promotionPlan["promotion_ready"] != false ||
		promotionPlan["promotion_ready_reason"] != "project_version_pin_or_live_validation_observed_without_audit_result" {
		t.Fatalf("pin/live evidence alone should not make promotion ready: %#v", promotionPlan)
	}
}

func TestConfigRepositoryGitWorkflowUnknownAuditDoesNotRecordSanitizedResult(t *testing.T) {
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
			"status":              "needs_review",
			"operation_log_count": int64(1),
		}},
	)

	evidence := mapFromAny(preview["git_workflow_audit_evidence"])
	if evidence["evidence_state"] != "unknown" ||
		evidence["sanitized_result_recorded"] != false ||
		intFromAny(evidence["unknown_count"], 0) != 1 {
		t.Fatalf("unknown audit should not claim sanitized result recording: %#v", evidence)
	}
	commitPlan := mapFromAny(preview["git_commit_plan"])
	if commitPlan["sanitized_result_observed"] != false {
		t.Fatalf("unknown audit should not be observed as sanitized result: %#v", commitPlan)
	}
	resultPlan := mapFromAny(commitPlan["result_recording_plan"])
	if resultPlan["result_recording_state"] != "blocked" ||
		resultPlan["result_recording_ready"] != false ||
		resultPlan["result_written"] != false ||
		resultPlan["operation_log_written"] != true {
		t.Fatalf("unknown audit should keep result recording blocked while preserving log evidence: %#v", resultPlan)
	}
	promotionPlan := mapFromAny(commitPlan["promotion_readiness_plan"])
	if promotionPlan["promotion_state"] != "unknown" ||
		promotionPlan["promotion_ready"] != false ||
		promotionPlan["promotion_ready_reason"] != "config_git_commit_audit_operation_unknown" {
		t.Fatalf("unknown audit should block promotion readiness: %#v", promotionPlan)
	}
}

func TestConfigRepositoryGitWorkflowUnknownAuditBlocksResultRecordingWithExistingPin(t *testing.T) {
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
		[]map[string]any{{
			"id":      "version-1",
			"version": "v0.1.0",
			"metadata": map[string]any{"repositories": []any{
				map[string]any{
					"repo_key":          "config",
					"repo_role":         "config",
					"remote_id":         "remote-1",
					"config_commit_sha": "abc123",
				},
			}},
		}},
		[]map[string]any{{
			"id":                  "op-config-1",
			"status":              "needs_review",
			"operation_log_count": int64(1),
		}},
	)

	commitPlan := mapFromAny(preview["git_commit_plan"])
	if commitPlan["project_version_pin_observed"] != true ||
		commitPlan["live_commit_validation_observed"] != true ||
		commitPlan["sanitized_result_observed"] != false {
		t.Fatalf("expected existing pin/live evidence with unknown audit: %#v", commitPlan)
	}
	resultPlan := mapFromAny(commitPlan["result_recording_plan"])
	if resultPlan["result_recording_state"] != "blocked" ||
		resultPlan["result_recording_ready"] != false ||
		resultPlan["result_written"] != false ||
		resultPlan["result_recording_ready_reason"] != "config_git_commit_audit_operation_unknown" ||
		resultPlan["project_version_pin_observed"] != true ||
		resultPlan["live_validation_recorded"] != true ||
		resultPlan["sanitized_audit_result_recorded"] != false {
		t.Fatalf("unknown audit should take priority over existing pin/live evidence: %#v", resultPlan)
	}
	promotionPlan := mapFromAny(commitPlan["promotion_readiness_plan"])
	if promotionPlan["promotion_state"] != "unknown" ||
		promotionPlan["promotion_ready"] != false ||
		promotionPlan["promotion_ready_reason"] != "config_git_commit_audit_operation_unknown" {
		t.Fatalf("unknown audit should block promotion readiness with existing pin/live evidence: %#v", promotionPlan)
	}
}

func TestConfigRepositoryGitWorkflowPromotionSnapshotPayloadSanitizesEvidence(t *testing.T) {
	repo := map[string]any{
		"id":             "repo-1",
		"project_id":     "project-1",
		"name":           "Config Repository",
		"repo_key":       "config",
		"repo_role":      "config",
		"default_branch": "main",
	}
	preview := configRepositoryScaffoldPreview(
		repo,
		[]map[string]any{{
			"id":               "remote-1",
			"name":             "origin",
			"remote_key":       "github",
			"provider_type":    "github",
			"remote_role":      "target",
			"default_branch":   "main",
			"latest_sha":       "abc123",
			"last_sync_status": "completed",
			"remote_url":       "https://token@example.com/repo.git",
		}},
		nil,
		[]map[string]any{{
			"id":                  "op-config-1",
			"status":              "completed",
			"operation_log_count": int64(2),
			"remote_url":          "https://token@example.com/repo.git",
			"provider_token":      "Bearer secret",
			"commit_sha":          "abc123",
			"branch_name":         "secret-branch",
			"git_output":          "secret git output",
			"provider_response":   "secret provider response",
		}},
	)
	snapshot := configRepositoryGitWorkflowPromotionSnapshotPayload(repo, preview, true)
	ready, state, missing := configRepositoryGitWorkflowPromotionSnapshotReadiness(snapshot)
	if !ready || state != "promotion_review_ready" || len(missing) != 0 {
		t.Fatalf("readiness = %v/%s/%#v; snapshot=%#v", ready, state, missing, snapshot)
	}
	if snapshot["promotion_ready_for_operator_review"] != true ||
		snapshot["sanitized_audit_result_recorded"] != true ||
		snapshot["status_snapshot_write_eligible"] != true ||
		snapshot["status_snapshot_written"] != true ||
		snapshot["status_snapshot_written"] != snapshot["status_snapshot_write_eligible"] ||
		snapshot["git_commit_created"] != false ||
		snapshot["git_push_performed"] != false ||
		snapshot["provider_review_created"] != false ||
		snapshot["project_version_pin_written"] != false ||
		snapshot["live_remote_validation_performed"] != false ||
		snapshot["external_call_made"] != false ||
		snapshot["contains_commit_sha"] != false ||
		snapshot["contains_remote_url"] != false ||
		snapshot["raw_git_output_recorded"] != false ||
		snapshot["raw_provider_response_recorded"] != false {
		t.Fatalf("unexpected promotion snapshot payload: %#v", snapshot)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, forbidden := range []string{"https://token@", "Bearer secret", "abc123", "secret-branch", "secret git output", "secret provider response"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("promotion snapshot leaked %q: %s", forbidden, encoded)
		}
	}
}

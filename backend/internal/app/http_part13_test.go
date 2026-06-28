package app

import (
	"testing"
)

func TestConfigRepositoryGitWorkflowAuditOperationLogEvidenceMatrix(t *testing.T) {
	tests := []struct {
		name          string
		operations    []map[string]any
		wantState     string
		wantSanitized bool
		wantResult    string
		wantReady     bool
		wantReason    string
		wantPromotion string
	}{
		{
			name: "completed with log records sanitized audit",
			operations: []map[string]any{{
				"id":                  "op-config-completed",
				"status":              "completed",
				"operation_log_count": int64(1),
			}},
			wantState:     "recorded",
			wantSanitized: true,
			wantResult:    "audit_recorded",
			wantReady:     true,
			wantReason:    "sanitized_config_git_workflow_audit_result_observed",
			wantPromotion: "audit_result_ready_for_review",
		},
		{
			name: "failed without log stays visible but not recorded",
			operations: []map[string]any{{
				"id":                  "op-config-failed",
				"status":              "failed",
				"operation_log_count": int64(0),
			}},
			wantState:     "failed",
			wantSanitized: false,
			wantResult:    "failed",
			wantReady:     false,
			wantReason:    "config_git_commit_audit_operation_failed",
			wantPromotion: "failed",
		},
		{
			name: "failed with log records terminal audit without promotion",
			operations: []map[string]any{{
				"id":                  "op-config-failed",
				"status":              "failed",
				"operation_log_count": int64(1),
			}},
			wantState:     "failed",
			wantSanitized: true,
			wantResult:    "failed",
			wantReady:     true,
			wantReason:    "config_git_commit_audit_operation_failed",
			wantPromotion: "failed",
		},
		{
			name: "canceled without log stays visible but not recorded",
			operations: []map[string]any{{
				"id":                  "op-config-canceled",
				"status":              "canceled",
				"operation_log_count": int64(0),
			}},
			wantState:     "canceled",
			wantSanitized: false,
			wantResult:    "canceled",
			wantReady:     false,
			wantReason:    "config_git_commit_audit_operation_canceled",
			wantPromotion: "canceled",
		},
		{
			name: "canceled with log records terminal audit without promotion",
			operations: []map[string]any{{
				"id":                  "op-config-canceled",
				"status":              "canceled",
				"operation_log_count": int64(1),
			}},
			wantState:     "canceled",
			wantSanitized: true,
			wantResult:    "canceled",
			wantReady:     true,
			wantReason:    "config_git_commit_audit_operation_canceled",
			wantPromotion: "canceled",
		},
		{
			name: "mixed failed with log records terminal audit",
			operations: []map[string]any{
				{"id": "op-config-failed", "status": "failed", "operation_log_count": int64(1)},
				{"id": "op-config-canceled", "status": "canceled", "operation_log_count": int64(1)},
			},
			wantState:     "mixed_failed",
			wantSanitized: true,
			wantResult:    "failed",
			wantReady:     true,
			wantReason:    "config_git_commit_audit_operation_failed",
			wantPromotion: "failed",
		},
		{
			name: "mixed failed without log stays visible but not recorded",
			operations: []map[string]any{
				{"id": "op-config-failed", "status": "failed", "operation_log_count": int64(0)},
				{"id": "op-config-canceled", "status": "canceled", "operation_log_count": int64(0)},
			},
			wantState:     "mixed_failed",
			wantSanitized: false,
			wantResult:    "failed",
			wantReady:     false,
			wantReason:    "config_git_commit_audit_operation_failed",
			wantPromotion: "failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
				tt.operations,
			)
			evidence := mapFromAny(preview["git_workflow_audit_evidence"])
			if evidence["evidence_state"] != tt.wantState ||
				evidence["sanitized_result_recorded"] != tt.wantSanitized {
				t.Fatalf("unexpected config workflow audit evidence: %#v", evidence)
			}
			commitPlan := mapFromAny(preview["git_commit_plan"])
			resultPlan := mapFromAny(commitPlan["result_recording_plan"])
			if resultPlan["result_recording_state"] != tt.wantResult ||
				resultPlan["result_recording_ready"] != tt.wantReady ||
				resultPlan["recording_enabled"] != tt.wantReady ||
				resultPlan["result_written"] != tt.wantReady ||
				resultPlan["result_recording_ready_reason"] != tt.wantReason ||
				resultPlan["sanitized_audit_result_recorded"] != tt.wantSanitized {
				t.Fatalf("unexpected config workflow result plan: %#v", resultPlan)
			}
			if !tt.wantReady && !containsString(stringSliceFromAny(resultPlan["blocked_reasons"]), tt.wantReason) {
				t.Fatalf("blocked config workflow result plan missing reason %q: %#v", tt.wantReason, resultPlan)
			}
			promotionPlan := mapFromAny(commitPlan["promotion_readiness_plan"])
			if promotionPlan["promotion_state"] != tt.wantPromotion ||
				promotionPlan["promotion_ready"] != (tt.wantPromotion == "audit_result_ready_for_review") {
				t.Fatalf("unexpected config workflow promotion plan: %#v", promotionPlan)
			}
		})
	}
}

func TestConfigRepositoryGitWorkflowAuditEvidenceTakesPriorityOverExistingPin(t *testing.T) {
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
			"status":              "failed",
			"operation_log_count": int64(1),
		}},
	)

	commitPlan := mapFromAny(preview["git_commit_plan"])
	if commitPlan["project_version_pin_observed"] != true ||
		commitPlan["live_commit_validation_observed"] != true {
		t.Fatalf("expected existing pin/live evidence: %#v", commitPlan)
	}
	resultPlan := mapFromAny(commitPlan["result_recording_plan"])
	if resultPlan["result_recording_state"] != "failed" ||
		resultPlan["result_recording_ready_reason"] != "config_git_commit_audit_operation_failed" ||
		resultPlan["result_written"] != true ||
		resultPlan["operation_log_written"] != true ||
		resultPlan["project_version_pin_written"] != false {
		t.Fatalf("failed workflow audit should take priority over existing pin/live evidence: %#v", resultPlan)
	}
	promotionPlan := mapFromAny(commitPlan["promotion_readiness_plan"])
	if promotionPlan["promotion_state"] != "failed" ||
		promotionPlan["promotion_ready"] != false ||
		promotionPlan["promotion_ready_reason"] != "config_git_commit_audit_operation_failed" ||
		promotionPlan["project_version_pin_observed"] != true ||
		promotionPlan["live_commit_validation_observed"] != true ||
		promotionPlan["git_push_performed"] != false ||
		promotionPlan["project_version_pin_written"] != false ||
		promotionPlan["live_remote_validation_performed"] != false {
		t.Fatalf("failed workflow audit should block promotion readiness: %#v", promotionPlan)
	}
}

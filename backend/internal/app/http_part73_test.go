package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentToolArmingSnapshotBlocksNonSuccessfulTerminalAudits(t *testing.T) {
	tests := []struct {
		name      string
		rows      []map[string]any
		wantState string
	}{
		{
			name: "canceled",
			rows: []map[string]any{
				{"id": "call-1", "operation_run_id": "op-1", "tool_name": "runtime.check", "status": "canceled"},
			},
			wantState: "canceled",
		},
		{
			name: "mixed failed",
			rows: []map[string]any{
				{"id": "call-1", "operation_run_id": "op-1", "tool_name": "runtime.check", "status": "failed"},
				{"id": "call-2", "operation_run_id": "op-1", "tool_name": "patch.prepare", "status": "canceled"},
			},
			wantState: "mixed_failed",
		},
		{
			name: "unknown",
			rows: []map[string]any{
				{"id": "call-1", "operation_run_id": "op-1", "tool_name": "runtime.check", "status": "mystery"},
			},
			wantState: "unknown",
		},
		{
			name: "absent",
			rows: []map[string]any{
				{"id": "call-1", "operation_run_id": "op-1", "tool_name": "runtime.check", "status": ""},
			},
			wantState: "absent",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evidence := agentToolCallAuditEvidence(tt.rows)
			dispatch := agentWorkerDispatchPlan(map[string]any{
				"name":         "Demo Runtime",
				"runtime_type": "codex-cli",
				"codex_binary": "codex",
				"status":       "verified",
			}, evidence)
			snapshot := agentToolArmingSnapshotPayload(map[string]any{
				"id":         "task-1",
				"project_id": "project-1",
			}, dispatch, true)
			ready, state, missing := agentToolArmingSnapshotReadiness(snapshot)
			if ready ||
				state != tt.wantState ||
				snapshot["terminal_audit_observed"] != true ||
				snapshot["sanitized_result_recorded"] != true ||
				snapshot["successful_audit_recorded"] != false ||
				snapshot["successful_sanitized_result_recorded"] != false ||
				snapshot["arming_ready_for_operator_review"] != false ||
				snapshot["tool_review_ready_for_operator"] != false {
				t.Fatalf("non-successful terminal audit should not arm: ready=%v state=%s missing=%#v snapshot=%#v", ready, state, missing, snapshot)
			}
			if !containsString(missing, "successful_tool_call_audit") ||
				!containsString(missing, "sanitized_result_recording") {
				t.Fatalf("non-successful terminal audit missing blockers: %#v", missing)
			}
		})
	}
}

func TestAgentCodeAuditSnapshotPayloadSanitizesEvidence(t *testing.T) {
	task := map[string]any{
		"id":         "task-1",
		"project_id": "project-1",
		"title":      "do not serialize title",
		"prompt":     "prompt with secret",
		"status":     "completed",
	}
	toolEvidence := agentToolCallAuditEvidence([]map[string]any{
		{
			"id":               "call-1",
			"operation_run_id": "op-1",
			"tool_name":        "worker.dispatch.plan",
			"status":           "completed",
			"input":            map[string]any{"token": "do-not-serialize"},
			"output":           map[string]any{"raw": "actual worker output", "workspace": "/tmp/secret-workspace"},
		},
		{
			"id":               "call-2",
			"operation_run_id": "op-1",
			"tool_name":        "codex.execution.plan",
			"status":           "completed",
			"output":           map[string]any{"branch": "feature/secret-branch"},
		},
		{
			"id":               "call-3",
			"operation_run_id": "op-1",
			"tool_name":        "patch.prepare",
			"status":           "completed",
			"output":           map[string]any{"patch": "secret patch", "diff": "secret diff", "test": "secret test output"},
		},
	})
	codeEvidence := agentCodeModificationEvidence(toolEvidence)
	arming := agentCodeModificationExecutionArmingPlan(codeEvidence)
	sourceReview := agentCodeModificationSourceCheckoutBranchReviewPlan(codeEvidence, arming)
	recording := agentCodeModificationResultRecordingPlan(codeEvidence)
	snapshot := agentCodeAuditSnapshotPayload(task, codeEvidence, recording, arming, sourceReview, true)
	ready, state, missing := agentCodeAuditSnapshotReadiness(snapshot)
	if !ready || state != "recorded" || len(missing) != 0 {
		t.Fatalf("readiness = %v/%s/%#v; snapshot=%#v", ready, state, missing, snapshot)
	}
	if snapshot["has_code_modification_audit"] != true ||
		snapshot["sanitized_result_recorded"] != true ||
		snapshot["worker_dispatch_audit_recorded"] != true ||
		snapshot["codex_execution_plan_recorded"] != true ||
		snapshot["patch_prepare_audit_recorded"] != true ||
		snapshot["execution_arming_ready"] != true ||
		snapshot["source_checkout_branch_review_ready"] != true ||
		snapshot["status_snapshot_write_eligible"] != true ||
		snapshot["status_snapshot_written"] != true ||
		snapshot["status_snapshot_written"] != snapshot["status_snapshot_write_eligible"] ||
		snapshot["source_checkout_performed"] != false ||
		snapshot["branch_created"] != false ||
		snapshot["diff_materialized"] != false ||
		snapshot["file_patch_applied"] != false ||
		snapshot["tests_executed"] != false ||
		snapshot["git_commit_created"] != false ||
		snapshot["git_push_performed"] != false ||
		snapshot["provider_review_created"] != false ||
		snapshot["raw_patch_recorded"] != false ||
		snapshot["raw_diff_recorded"] != false ||
		snapshot["raw_test_output_recorded"] != false ||
		snapshot["contains_token"] != false {
		t.Fatalf("unexpected sanitized agent code audit snapshot: %#v", snapshot)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, forbidden := range []string{
		"do-not-serialize",
		"actual worker output",
		"/tmp/secret-workspace",
		"feature/secret-branch",
		"secret patch",
		"secret diff",
		"secret test output",
		"prompt with secret",
		"worker.dispatch.plan",
		"codex.execution.plan",
		"patch.prepare",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("agent code audit snapshot leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestAgentCodeAuditSnapshotStatusHealthForTerminalStates(t *testing.T) {
	tests := []struct {
		state      string
		wantStatus string
		wantHealth string
	}{
		{state: "recorded", wantStatus: "agent_code_audit_recorded", wantHealth: "low"},
		{state: "failed", wantStatus: "agent_code_audit_failed", wantHealth: "high"},
		{state: "mixed_failed", wantStatus: "agent_code_audit_mixed_failed", wantHealth: "high"},
		{state: "canceled", wantStatus: "agent_code_audit_canceled", wantHealth: "high"},
		{state: "unknown", wantStatus: "agent_code_audit_unknown", wantHealth: "high"},
		{state: "absent", wantStatus: "agent_code_audit_absent", wantHealth: "high"},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			status, health := agentCodeAuditSnapshotStatusHealth(tt.state)
			if status != tt.wantStatus || health != tt.wantHealth {
				t.Fatalf("status/health = %s/%s, want %s/%s", status, health, tt.wantStatus, tt.wantHealth)
			}
		})
	}
}

type agentToolAuditSnapshotCall struct {
	id       string
	status   string
	toolName string
}

func newAgentToolAuditSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/agent/tasks/task-1/tool-audit-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "task-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newAgentToolAuditSnapshotRequestAs(body string, user *User) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/agent/tasks/task-1/tool-audit-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "task-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, user))
}

func newAgentToolArmingSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/agent/tasks/task-1/tool-arming-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "task-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newAgentToolArmingSnapshotRequestAs(body string, user *User) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/agent/tasks/task-1/tool-arming-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "task-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, user))
}

func newProviderReviewAttemptClaimRequest() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/claim", strings.NewReader(`{}`))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptLocalResultRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/local-result", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptIdempotencySnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/idempotency-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptLiveExecutionReadinessSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/live-execution-readiness-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptLiveExecutionPreflightRequest() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/live-execution-preflight", strings.NewReader(`{}`))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptLiveExecutionLaunchPlanRequest() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/live-execution-launch-plan", strings.NewReader(`{}`))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewMutationArmingSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/operation-approvals/approval-1/provider-review-arming-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "approval-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewCurrentAttemptLiveReadinessSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/operation-approvals/approval-1/provider-review-current-live-readiness-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "approval-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewCurrentAttemptLiveExecutionLaunchPlanRequest() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/operation-approvals/approval-1/provider-review-current-live-launch-plan", strings.NewReader(`{}`))
	req = withRouteParam(req, "id", "approval-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewCurrentLiveExecutionGateRequest() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/operation-approvals/approval-1/provider-review-current-live-execution-gate", strings.NewReader(`{}`))
	req = withRouteParam(req, "id", "approval-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptActivationSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/activation-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

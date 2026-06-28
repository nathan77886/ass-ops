package app

import (
	"fmt"
	"testing"
)

func TestProjectVersionRefreshResultSummaryReportsFailures(t *testing.T) {
	summary := projectVersionRefreshResultSummary([]map[string]any{
		{"id": "op-git", "operation_type": "git.refs.refresh", "status": "failed", "error": "sanitized failure", "input": map[string]any{"refresh_kind": "git_ref_fetch"}},
	})
	if summary["validation_rerun_status"] != "refresh_failed" ||
		summary["validation_rerun_recorded"] != false ||
		summary["failed_count"] != 1 ||
		summary["has_refresh_failures"] != true {
		t.Fatalf("failed refresh summary = %#v", summary)
	}
	if !containsString(projectVersionRefreshResultBlockedReasons(summary, nil), "refresh_worker_failed") {
		t.Fatalf("failed refresh summary should report refresh_worker_failed: %#v", summary)
	}
	items := sliceOfMapsFromAny(summary["items"])
	if len(items) != 1 || items[0]["error_recorded"] != true || items[0]["raw_response_included"] != false {
		t.Fatalf("failed refresh item = %#v", items)
	}
}

func TestProjectVersionRefreshResultSummaryStatusCombinations(t *testing.T) {
	tests := []struct {
		name         string
		statuses     []string
		wantStatus   string
		wantReason   string
		wantState    string
		wantReady    bool
		wantActive   int
		wantFailed   bool
		wantCanceled int
	}{
		{
			name:       "queued only waits",
			statuses:   []string{"queued"},
			wantStatus: "waiting_for_workers",
			wantReason: "refresh_workers_still_running",
			wantState:  "waiting",
			wantReady:  false,
			wantActive: 1,
		},
		{
			name:         "canceled only is not failed",
			statuses:     []string{"canceled"},
			wantStatus:   "refresh_canceled",
			wantReason:   "refresh_worker_canceled",
			wantState:    "canceled",
			wantReady:    true,
			wantCanceled: 1,
		},
		{
			name:       "running and failed still waits with failure evidence",
			statuses:   []string{"running", "failed"},
			wantStatus: "waiting_for_workers",
			wantReason: "refresh_workers_still_running",
			wantState:  "waiting",
			wantReady:  false,
			wantActive: 1,
			wantFailed: true,
		},
		{
			name:         "running and canceled still waits",
			statuses:     []string{"running", "canceled"},
			wantStatus:   "waiting_for_workers",
			wantReason:   "refresh_workers_still_running",
			wantState:    "waiting",
			wantReady:    false,
			wantActive:   1,
			wantCanceled: 1,
		},
		{
			name:         "running failed and canceled still waits with failure evidence",
			statuses:     []string{"running", "failed", "canceled"},
			wantStatus:   "waiting_for_workers",
			wantReason:   "refresh_workers_still_running",
			wantState:    "waiting",
			wantReady:    false,
			wantActive:   1,
			wantFailed:   true,
			wantCanceled: 1,
		},
		{
			name:         "failed and canceled reports failure",
			statuses:     []string{"failed", "canceled"},
			wantStatus:   "refresh_failed",
			wantReason:   "refresh_worker_failed",
			wantState:    "failed",
			wantReady:    true,
			wantFailed:   true,
			wantCanceled: 1,
		},
		{
			name:       "completed only records result",
			statuses:   []string{"completed"},
			wantStatus: "recorded",
			wantReason: "validation_rerun_recorded",
			wantState:  "recorded",
			wantReady:  true,
		},
		{
			name:       "same kind queued and completed still waits",
			statuses:   []string{"queued", "completed"},
			wantStatus: "waiting_for_workers",
			wantReason: "refresh_workers_still_running",
			wantState:  "waiting",
			wantReady:  false,
			wantActive: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			operations := make([]map[string]any, 0, len(tt.statuses))
			for index, status := range tt.statuses {
				operations = append(operations, map[string]any{
					"id":             fmt.Sprintf("op-%d", index),
					"operation_type": "git.refs.refresh",
					"status":         status,
					"input":          map[string]any{"refresh_kind": "git_ref_fetch"},
				})
			}
			summary := projectVersionRefreshResultSummary(operations)
			if summary["validation_rerun_status"] != tt.wantStatus ||
				intFromAny(summary["active_count"], 0) != tt.wantActive ||
				intFromAny(summary["canceled_count"], 0) != tt.wantCanceled ||
				summary["has_refresh_failures"] != tt.wantFailed {
				t.Fatalf("summary = %#v", summary)
			}
			if got := projectVersionRefreshResultRecordingState(summary); got != tt.wantState {
				t.Fatalf("recording state = %q, want %q", got, tt.wantState)
			}
			if got := projectVersionRefreshResultRecordingReason(summary); got != tt.wantReason {
				t.Fatalf("recording reason = %q, want %q", got, tt.wantReason)
			}
			if got := projectVersionRefreshKindTerminalObserved(summary, "git_ref_fetch"); got != tt.wantReady {
				t.Fatalf("terminal kind observed = %v, want %v; summary=%#v", got, tt.wantReady, summary)
			}
			if tt.wantReason != "validation_rerun_recorded" && !containsString(projectVersionRefreshResultBlockedReasons(summary, nil), tt.wantReason) {
				t.Fatalf("blocked reasons missing %q: %#v", tt.wantReason, projectVersionRefreshResultBlockedReasons(summary, nil))
			}
		})
	}
}

func TestProjectVersionProviderRefreshExecutionPlanBlocked(t *testing.T) {
	refreshPlan := projectVersionProviderRefreshPlan(
		[]map[string]any{
			{"repo_key": "service", "remote_id": "missing-remote", "commit_sha": "abc123"},
			{"repo_key": "deploy", "remote_id": "remote-1", "argo_revision": "rev-1"},
		},
		[]map[string]any{{"id": "remote-1", "remote_key": "github", "provider_type": "github"}},
		nil,
	)
	if refreshPlan["plan_state"] != "blocked" {
		t.Fatalf("refresh plan should be blocked: %#v", refreshPlan)
	}
	executionPlan := mapFromAny(refreshPlan["execution_plan"])
	if executionPlan["execution_state"] != "blocked" ||
		executionPlan["planned_step_count"] != 0 ||
		executionPlan["blocked_step_count"] != 2 ||
		executionPlan["unique_planned_kind_count"] != 0 ||
		executionPlan["unique_blocked_kind_count"] != 1 ||
		!containsString(stringSliceFromAny(executionPlan["blocked_refresh_kinds"]), "argocd_app_refresh") {
		t.Fatalf("blocked refresh execution plan = %#v", executionPlan)
	}
	assertProviderRefreshExecutionPlanSafe(t, executionPlan)
}

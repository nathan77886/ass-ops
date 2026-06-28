package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectVersionValidationPreviewIncludesRefreshResultSummary(t *testing.T) {
	preview := projectVersionValidationPreview(
		map[string]any{
			"id":      "version-1",
			"version": "v0.1.0",
			"metadata": map[string]any{"repositories": []any{
				map[string]any{"repo_key": "service", "remote_id": "remote-1", "commit_sha": "abc123", "github_action_run_id": "run-1"},
			}},
		},
		[]map[string]any{{"id": "remote-1", "provider_type": "github", "latest_sha": "abc123"}},
		nil,
		nil,
		nil,
		nil,
		[]map[string]any{
			{"id": "op-git", "operation_type": "git.refs.refresh", "status": "completed", "input": map[string]any{"refresh_kind": "git_ref_fetch"}},
			{"id": "op-actions", "operation_type": "github.actions.sync", "status": "running", "input": map[string]any{"refresh_kind": "github_actions_api_refresh"}},
		},
	)
	summary := mapFromAny(preview["provider_refresh_result_summary"])
	if summary["mode"] != "project_version_refresh_result_summary" ||
		summary["operation_count"] != 2 ||
		summary["completed_count"] != 1 ||
		summary["running_count"] != 1 ||
		summary["active_count"] != 1 ||
		summary["validation_rerun_status"] != "waiting_for_workers" ||
		summary["validation_rerun_recorded"] != false ||
		summary["raw_response_included"] != false ||
		summary["secret_included"] != false {
		t.Fatalf("refresh result summary = %#v", summary)
	}
	refreshPlan := mapFromAny(preview["provider_refresh_plan"])
	executionPlan := mapFromAny(refreshPlan["execution_plan"])
	if executionPlan["operation_enqueued"] != true ||
		executionPlan["worker_job_created"] != true ||
		executionPlan["validation_auto_reload_supported"] != true ||
		executionPlan["server_side_validation_rerun"] != false ||
		executionPlan["validation_reopened"] != false {
		t.Fatalf("refresh execution plan did not reflect observed operations: %#v", executionPlan)
	}
	resultPlan := mapFromAny(executionPlan["result_recording_plan"])
	if resultPlan["result_recording_state"] != "waiting" ||
		resultPlan["result_recording_ready"] != false ||
		resultPlan["result_written"] != false ||
		resultPlan["operation_log_written"] != false ||
		resultPlan["canonical_asset_sync_queued"] != false ||
		resultPlan["status_snapshot_write_eligible"] != false ||
		resultPlan["status_snapshot_written"] != false ||
		resultPlan["status_snapshot_written"] != resultPlan["status_snapshot_write_eligible"] ||
		resultPlan["git_ref_fetch_result_recorded"] != true ||
		resultPlan["github_actions_result_recorded"] != false ||
		resultPlan["validation_rerun_recorded"] != false ||
		!containsString(stringSliceFromAny(resultPlan["blocked_reasons"]), "refresh_workers_still_running") {
		t.Fatalf("refresh result recording plan = %#v", resultPlan)
	}
	workerBinding := mapFromAny(executionPlan["worker_result_binding_evidence"])
	if workerBinding["mode"] != "project_version_refresh_worker_result_binding_evidence" ||
		workerBinding["binding_state"] != "waiting_for_workers" ||
		workerBinding["project_version_scope_bound"] != true ||
		workerBinding["operation_result_bound"] != true ||
		workerBinding["worker_result_observed"] != true ||
		workerBinding["terminal_worker_result_observed"] != false ||
		workerBinding["all_planned_results_observed"] != true ||
		workerBinding["external_call_made"] != false ||
		workerBinding["provider_api_called"] != false ||
		workerBinding["raw_response_included"] != false ||
		workerBinding["secret_included"] != false {
		t.Fatalf("refresh worker result binding evidence = %#v", workerBinding)
	}
	if len(stringSliceFromAny(workerBinding["missing_planned_result_kinds"])) != 0 {
		t.Fatalf("worker binding should have all planned kinds observed: %#v", workerBinding)
	}
	resultBinding := mapFromAny(resultPlan["worker_result_binding_evidence"])
	if resultBinding["binding_state"] != "waiting_for_workers" {
		t.Fatalf("result plan should carry worker binding evidence: %#v", resultPlan)
	}
	rerunEvidence := mapFromAny(preview["validation_rerun_evidence"])
	if rerunEvidence["rerun_state"] != "waiting_for_workers" ||
		rerunEvidence["server_side_validation_recheck"] != true ||
		rerunEvidence["server_side_validation_recheck_ready"] != false ||
		rerunEvidence["automatic_background_rerun"] != false ||
		rerunEvidence["provider_refresh_active"] != true ||
		rerunEvidence["raw_response_included"] != false ||
		rerunEvidence["secret_included"] != false {
		t.Fatalf("validation rerun evidence = %#v", rerunEvidence)
	}
	backgroundPlan := mapFromAny(preview["background_validation_rerun_plan"])
	if backgroundPlan["mode"] != "project_version_background_validation_rerun_plan" ||
		backgroundPlan["plan_state"] != "waiting_for_workers" ||
		backgroundPlan["background_rerun_ready_for_review"] != false ||
		backgroundPlan["automatic_background_rerun"] != false ||
		backgroundPlan["background_worker_enqueued"] != false ||
		backgroundPlan["validation_snapshot_written"] != false ||
		backgroundPlan["provider_refresh_operation_observed"] != true ||
		backgroundPlan["provider_refresh_terminal"] != false {
		t.Fatalf("waiting background rerun plan = %#v", backgroundPlan)
	}
	if !containsString(stringSliceFromAny(backgroundPlan["blocked_reasons"]), "refresh_workers_still_running") {
		t.Fatalf("waiting background rerun blockers missing worker reason: %#v", backgroundPlan["blocked_reasons"])
	}
	assertProjectVersionBackgroundRerunPlanSafe(t, backgroundPlan)
	waitingSnapshotPlan := mapFromAny(backgroundPlan["validation_snapshot_write_plan"])
	if waitingSnapshotPlan["snapshot_state"] != "waiting_for_workers" ||
		waitingSnapshotPlan["snapshot_ready_for_review"] != false ||
		waitingSnapshotPlan["provider_refresh_status"] != "waiting_for_workers" ||
		!containsString(stringSliceFromAny(waitingSnapshotPlan["blocked_reasons"]), "refresh_workers_still_running") {
		t.Fatalf("waiting snapshot write plan = %#v", waitingSnapshotPlan)
	}
	assertProjectVersionValidationSnapshotWritePlanSafe(t, waitingSnapshotPlan)
	if mapFromAny(executionPlan["background_validation_rerun_plan"])["plan_state"] != "waiting_for_workers" {
		t.Fatalf("execution plan should carry background rerun plan: %#v", executionPlan)
	}
	encoded, _ := json.Marshal(preview)
	for _, forbidden := range []string{"secret-token", "Bearer secret", "https://token@", "raw_provider_response\":true", "raw_git_output\":\""} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("refresh summary leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestProjectVersionRefreshWorkerResultBindingEvidenceRequiresPlannedKinds(t *testing.T) {
	summary := projectVersionRefreshResultSummary([]map[string]any{
		{"id": "op-git", "operation_type": "git.refs.refresh", "status": "completed", "input": map[string]any{"refresh_kind": "git_ref_fetch", "remote_url": "https://token@example.com/repo.git"}},
	})
	evidence := projectVersionRefreshWorkerResultBindingEvidence(summary, []string{"git_ref_fetch", "github_actions_api_refresh"})
	if evidence["binding_state"] != "partial_recorded" ||
		evidence["terminal_worker_result_observed"] != true ||
		evidence["all_planned_results_observed"] != false ||
		evidence["raw_response_included"] != false ||
		evidence["raw_git_output_included"] != false ||
		evidence["raw_argo_response_included"] != false ||
		evidence["secret_included"] != false ||
		evidence["contains_remote_url"] != false ||
		evidence["contains_provider_token"] != false ||
		evidence["contains_provider_response"] != false ||
		!containsString(stringSliceFromAny(evidence["missing_planned_result_kinds"]), "github_actions_api_refresh") {
		t.Fatalf("worker result binding should stay partial until every planned kind is observed: %#v", evidence)
	}
	for _, field := range []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body", "operation_error_detail"} {
		if !containsString(stringSliceFromAny(evidence["suppressed_fields"]), field) {
			t.Fatalf("worker binding suppressed_fields missing %q: %#v", field, evidence["suppressed_fields"])
		}
	}
	encoded, _ := json.Marshal(evidence)
	for _, forbidden := range []string{"https://token@example.com", "Bearer secret", "raw_provider_response\":true", "raw_git_output\":\""} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("worker binding evidence leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestProjectVersionRefreshResultPlanBlocksWhenPlannedKindMissing(t *testing.T) {
	summary := projectVersionRefreshResultSummary([]map[string]any{
		{"id": "op-git", "operation_type": "git.refs.refresh", "status": "completed", "input": map[string]any{"refresh_kind": "git_ref_fetch", "remote_url": "https://token@example.com/repo.git"}},
	})
	plannedKinds := []string{"git_ref_fetch", "github_actions_api_refresh"}
	refreshPlan := map[string]any{
		"execution_plan": map[string]any{
			"planned_refresh_kinds": plannedKinds,
			"result_recording_plan": projectVersionProviderRefreshResultRecordingPlan(plannedKinds),
		},
	}
	rerunEvidence := projectVersionValidationRerunEvidence(summary, "partial", 1, 0, 1, 0)
	attachProjectVersionRefreshResultSummary(refreshPlan, summary, rerunEvidence)
	executionPlan := mapFromAny(refreshPlan["execution_plan"])
	workerBinding := mapFromAny(executionPlan["worker_result_binding_evidence"])
	resultPlan := mapFromAny(executionPlan["result_recording_plan"])
	if workerBinding["binding_state"] != "partial_recorded" ||
		!containsString(stringSliceFromAny(workerBinding["missing_planned_result_kinds"]), "github_actions_api_refresh") {
		t.Fatalf("worker binding should stay partial until every planned kind is observed: %#v", workerBinding)
	}
	if resultPlan["result_recording_state"] != "partial_recorded" ||
		resultPlan["result_recording_ready"] != false ||
		resultPlan["result_written"] != false ||
		resultPlan["operation_log_written"] != false ||
		resultPlan["canonical_asset_sync_queued"] != false ||
		resultPlan["status_snapshot_write_eligible"] != false ||
		resultPlan["status_snapshot_written"] != false ||
		resultPlan["status_snapshot_written"] != resultPlan["status_snapshot_write_eligible"] ||
		resultPlan["validation_rerun_recorded"] != true ||
		resultPlan["git_ref_fetch_result_recorded"] != true ||
		resultPlan["github_actions_result_recorded"] != false ||
		resultPlan["result_recording_ready_reason"] != "planned_refresh_result_missing" ||
		!containsString(stringSliceFromAny(resultPlan["blocked_reasons"]), "planned_refresh_result_missing") ||
		!containsString(stringSliceFromAny(resultPlan["blocked_reasons"]), "missing_github_actions_api_refresh") {
		t.Fatalf("partial worker binding should block refresh result recording: %#v", resultPlan)
	}
	encoded, _ := json.Marshal(resultPlan)
	for _, forbidden := range []string{"https://token@example.com", "Bearer secret", "raw_provider_response\":true", "raw_git_output\":\""} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("partial refresh result plan leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestProjectVersionRefreshResultPlanAllowsEmptyPlannedKindsFallback(t *testing.T) {
	summary := projectVersionRefreshResultSummary([]map[string]any{
		{"id": "op-git", "operation_type": "git.refs.refresh", "status": "completed", "input": map[string]any{"refresh_kind": "git_ref_fetch"}},
	})
	refreshPlan := map[string]any{
		"execution_plan": map[string]any{
			"result_recording_plan": projectVersionProviderRefreshResultRecordingPlan(nil),
		},
	}
	rerunEvidence := projectVersionValidationRerunEvidence(summary, "ready", 1, 1, 0, 0)
	attachProjectVersionRefreshResultSummary(refreshPlan, summary, rerunEvidence)
	resultPlan := mapFromAny(mapFromAny(refreshPlan["execution_plan"])["result_recording_plan"])
	if resultPlan["result_recording_state"] != "recorded" ||
		resultPlan["result_recording_ready"] != true ||
		resultPlan["result_written"] != true ||
		resultPlan["status_snapshot_write_eligible"] != true ||
		resultPlan["status_snapshot_written"] != resultPlan["status_snapshot_write_eligible"] ||
		resultPlan["result_recording_ready_reason"] != "validation_rerun_recorded" ||
		len(stringSliceFromAny(resultPlan["blocked_reasons"])) != 0 {
		t.Fatalf("empty planned kinds fallback should not falsely downgrade recorded refresh evidence: %#v", resultPlan)
	}
}

func TestProjectVersionRefreshResultSummaryRecordsValidationRerunWhenTerminal(t *testing.T) {
	summary := projectVersionRefreshResultSummary([]map[string]any{
		{"id": "op-git", "operation_type": "git.refs.refresh", "status": "completed", "input": map[string]any{"refresh_kind": "git_ref_fetch"}},
		{"id": "op-actions", "operation_type": "github.actions.sync", "status": "completed", "input": map[string]any{"refresh_kind": "github_actions_api_refresh"}},
	})
	if summary["validation_rerun_status"] != "recorded" ||
		summary["validation_rerun_recorded"] != true ||
		summary["terminal_count"] != 2 ||
		summary["active_count"] != 0 ||
		summary["has_refresh_failures"] != false {
		t.Fatalf("terminal refresh summary = %#v", summary)
	}
	plannedKinds := []string{"git_ref_fetch", "github_actions_api_refresh"}
	if reasons := projectVersionRefreshResultBlockedReasons(summary, projectVersionRefreshWorkerResultBindingEvidence(summary, plannedKinds)); len(reasons) != 0 {
		t.Fatalf("terminal refresh summary should not have blocked reasons: %#v", reasons)
	}
	if !projectVersionRefreshKindTerminalObserved(summary, "git_ref_fetch") ||
		!projectVersionRefreshKindTerminalObserved(summary, "github_actions_api_refresh") ||
		projectVersionRefreshKindTerminalObserved(summary, "argocd_app_refresh") {
		t.Fatalf("terminal refresh kind observation mismatch: %#v", summary)
	}
	refreshPlan := map[string]any{
		"execution_plan": map[string]any{
			"planned_refresh_kinds": plannedKinds,
			"result_recording_plan": projectVersionProviderRefreshResultRecordingPlan(plannedKinds),
		},
	}
	rerunEvidence := projectVersionValidationRerunEvidence(summary, "ready", 2, 2, 0, 0)
	attachProjectVersionRefreshResultSummary(refreshPlan, summary, rerunEvidence)
	resultPlan := mapFromAny(mapFromAny(refreshPlan["execution_plan"])["result_recording_plan"])
	if resultPlan["result_written"] != true ||
		resultPlan["operation_log_written"] != true ||
		resultPlan["canonical_asset_sync_queued"] != true ||
		resultPlan["status_snapshot_write_eligible"] != true ||
		resultPlan["status_snapshot_written"] != true ||
		resultPlan["status_snapshot_written"] != resultPlan["status_snapshot_write_eligible"] ||
		resultPlan["validation_rerun_recorded"] != true {
		t.Fatalf("terminal refresh result plan should mark sanitized writes recorded: %#v", resultPlan)
	}
}

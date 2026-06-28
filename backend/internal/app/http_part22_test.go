package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectVersionValidationRerunEvidenceRecordsServerSideRecheck(t *testing.T) {
	tests := []struct {
		name         string
		status       string
		wantState    string
		wantRecorded bool
	}{
		{name: "recorded terminal", status: "completed", wantState: "recorded", wantRecorded: true},
		{name: "failed terminal", status: "failed", wantState: "refresh_failed"},
		{name: "canceled terminal", status: "canceled", wantState: "refresh_canceled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preview := projectVersionValidationPreview(
				map[string]any{
					"id":      "version-1",
					"version": "v0.1.0",
					"metadata": map[string]any{"repositories": []any{
						map[string]any{"repo_key": "service", "remote_id": "remote-1", "commit_sha": "abc123"},
					}},
				},
				[]map[string]any{{"id": "remote-1", "provider_type": "github", "latest_sha": "abc123"}},
				nil,
				nil,
				nil,
				nil,
				[]map[string]any{
					{"id": "op-git", "operation_type": "git.refs.refresh", "status": tt.status, "input": map[string]any{"refresh_kind": "git_ref_fetch", "remote_url": "https://token@example.com/repo.git"}},
				},
			)
			evidence := mapFromAny(preview["validation_rerun_evidence"])
			if evidence["mode"] != "project_version_validation_rerun_evidence" ||
				evidence["rerun_state"] != tt.wantState ||
				evidence["rerun_source"] != "validation_preview_request" ||
				evidence["server_side_validation_recheck"] != true ||
				evidence["server_side_validation_recheck_ready"] != true ||
				evidence["automatic_background_rerun"] != false ||
				evidence["validation_rerun_recorded"] != tt.wantRecorded ||
				evidence["provider_refresh_terminal"] != true ||
				evidence["external_call_made"] != false ||
				evidence["provider_api_called"] != false ||
				evidence["raw_response_included"] != false ||
				evidence["secret_included"] != false {
				t.Fatalf("validation rerun evidence = %#v", evidence)
			}
			executionPlan := mapFromAny(mapFromAny(preview["provider_refresh_plan"])["execution_plan"])
			if executionPlan["server_side_validation_recheck_observed"] != true ||
				executionPlan["automatic_background_rerun"] != false {
				t.Fatalf("execution plan should surface server-side recheck evidence only: %#v", executionPlan)
			}
			resultPlan := mapFromAny(executionPlan["result_recording_plan"])
			if resultPlan["validation_rerun_recorded"] != tt.wantRecorded ||
				resultPlan["result_written"] != true ||
				resultPlan["operation_log_written"] != true ||
				resultPlan["server_side_validation_recheck_observed"] != true ||
				resultPlan["automatic_background_rerun"] != false {
				t.Fatalf("result plan should reflect validation recheck evidence: %#v", resultPlan)
			}
			backgroundPlan := mapFromAny(preview["background_validation_rerun_plan"])
			wantBackgroundReady := tt.wantState == "recorded"
			if backgroundPlan["mode"] != "project_version_background_validation_rerun_plan" ||
				backgroundPlan["background_rerun_ready_for_review"] != wantBackgroundReady ||
				backgroundPlan["automatic_background_rerun"] != false ||
				backgroundPlan["background_worker_enqueued"] != false ||
				backgroundPlan["validation_snapshot_written"] != false ||
				backgroundPlan["provider_refresh_terminal"] != true {
				t.Fatalf("terminal background rerun plan = %#v", backgroundPlan)
			}
			if wantBackgroundReady && backgroundPlan["plan_state"] != "ready_for_operator_review" {
				t.Fatalf("recorded terminal background rerun should be ready for operator review: %#v", backgroundPlan)
			}
			if !wantBackgroundReady && backgroundPlan["plan_state"] != "blocked" {
				t.Fatalf("failed/canceled terminal background rerun should stay blocked: %#v", backgroundPlan)
			}
			assertProjectVersionBackgroundRerunPlanSafe(t, backgroundPlan)
			snapshotPlan := mapFromAny(backgroundPlan["validation_snapshot_write_plan"])
			if snapshotPlan["mode"] != "project_version_validation_snapshot_write_plan" ||
				snapshotPlan["snapshot_ready_for_review"] != wantBackgroundReady ||
				snapshotPlan["snapshot_write_enabled"] != false ||
				snapshotPlan["validation_snapshot_written"] != false ||
				snapshotPlan["provider_refresh_terminal"] != true {
				t.Fatalf("terminal snapshot write plan = %#v", snapshotPlan)
			}
			if wantBackgroundReady && snapshotPlan["snapshot_state"] != "metadata_review_ready" {
				t.Fatalf("recorded terminal snapshot write should be ready for review: %#v", snapshotPlan)
			}
			if !wantBackgroundReady && snapshotPlan["snapshot_state"] != "blocked" {
				t.Fatalf("failed/canceled snapshot write should stay blocked: %#v", snapshotPlan)
			}
			if !wantBackgroundReady && snapshotPlan["snapshot_ready_for_review"] != false {
				t.Fatalf("failed/canceled snapshot write should not be ready for review: %#v", snapshotPlan)
			}
			assertProjectVersionValidationSnapshotWritePlanSafe(t, snapshotPlan)
			encoded, _ := json.Marshal(preview)
			for _, forbidden := range []string{"https://token@example.com", "Bearer secret", "raw_provider_response\":true", "raw_git_output\":\""} {
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("validation rerun evidence leaked %q: %s", forbidden, encoded)
				}
			}
		})
	}
}

func TestProjectVersionValidationSnapshotAutoRecordReadinessRequiresRecordedTerminalRefresh(t *testing.T) {
	baseVersion := map[string]any{
		"id":      "version-1",
		"version": "v0.1.0",
		"metadata": map[string]any{"repositories": []any{
			map[string]any{"repo_key": "service", "remote_id": "remote-1", "commit_sha": "abc123"},
		}},
	}
	waitingPreview := projectVersionValidationPreview(
		baseVersion,
		[]map[string]any{{"id": "remote-1", "provider_type": "github", "latest_sha": "abc123"}},
		nil,
		nil,
		nil,
		nil,
		[]map[string]any{
			{"id": "op-git", "operation_type": "git.refs.refresh", "status": "running", "input": map[string]any{"refresh_kind": "git_ref_fetch"}},
		},
	)
	waitingSnapshot := projectVersionValidationSnapshotPayload(waitingPreview, true)
	ready, state, missing := projectVersionValidationSnapshotAutoRecordReadiness(waitingPreview, waitingSnapshot)
	if ready || state != "waiting_for_workers" ||
		!containsString(missing, "refresh_workers_still_running") ||
		!containsString(missing, "validation_rerun_not_recorded") {
		t.Fatalf("waiting refresh should not be auto-recordable: ready=%v state=%s missing=%#v", ready, state, missing)
	}

	recordedPreview := projectVersionValidationPreview(
		baseVersion,
		[]map[string]any{{"id": "remote-1", "provider_type": "github", "latest_sha": "abc123"}},
		nil,
		nil,
		nil,
		nil,
		[]map[string]any{
			{"id": "op-git", "operation_type": "git.refs.refresh", "status": "completed", "input": map[string]any{"refresh_kind": "git_ref_fetch"}},
		},
	)
	recordedSnapshot := projectVersionValidationSnapshotPayload(recordedPreview, true)
	ready, state, missing = projectVersionValidationSnapshotAutoRecordReadiness(recordedPreview, recordedSnapshot)
	if !ready || state != "ready_to_record" || len(missing) != 0 {
		t.Fatalf("recorded terminal refresh should be auto-recordable: ready=%v state=%s missing=%#v", ready, state, missing)
	}
}

func TestProjectVersionValidationRerunEvidenceWithoutOperations(t *testing.T) {
	summary := projectVersionRefreshResultSummary(nil)
	evidence := projectVersionValidationRerunEvidence(summary, "blocked", 0, 0, 0, 0)
	if evidence["rerun_state"] != "not_requested" ||
		evidence["server_side_validation_recheck"] != false ||
		evidence["server_side_validation_recheck_ready"] != false ||
		evidence["automatic_background_rerun"] != false ||
		evidence["provider_refresh_operation_observed"] != false ||
		evidence["raw_response_included"] != false ||
		evidence["secret_included"] != false {
		t.Fatalf("empty validation rerun evidence = %#v", evidence)
	}
	backgroundPlan := projectVersionBackgroundValidationRerunPlan(summary, evidence)
	if backgroundPlan["plan_state"] != "blocked" ||
		backgroundPlan["provider_refresh_operation_observed"] != false ||
		backgroundPlan["background_rerun_ready_for_review"] != false ||
		backgroundPlan["automatic_background_rerun"] != false ||
		backgroundPlan["validation_snapshot_written"] != false {
		t.Fatalf("empty background rerun plan = %#v", backgroundPlan)
	}
	assertProjectVersionBackgroundRerunPlanSafe(t, backgroundPlan)
	snapshotPlan := mapFromAny(backgroundPlan["validation_snapshot_write_plan"])
	if snapshotPlan["snapshot_state"] != "blocked" ||
		snapshotPlan["snapshot_ready_for_review"] != false ||
		snapshotPlan["provider_refresh_status"] != "not_requested" ||
		!containsString(stringSliceFromAny(snapshotPlan["blocked_reasons"]), "provider_refresh_execution_not_performed") {
		t.Fatalf("empty snapshot write plan = %#v", snapshotPlan)
	}
	assertProjectVersionValidationSnapshotWritePlanSafe(t, snapshotPlan)
	encoded, _ := json.Marshal(evidence)
	for _, forbidden := range []string{"Bearer secret", "raw_provider_response\":true", "raw_git_output\":\""} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("empty validation rerun evidence leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestProjectVersionValidationSnapshotPayloadIsAllowlisted(t *testing.T) {
	preview := projectVersionValidationPreview(
		map[string]any{
			"id":      "version-1",
			"version": "v0.1.0",
			"metadata": map[string]any{"repositories": []any{
				map[string]any{
					"repo_key":             "service",
					"remote_id":            "remote-1",
					"commit_sha":           "abc123",
					"github_action_run_id": "run-1",
				},
			}},
		},
		[]map[string]any{{"id": "remote-1", "provider_type": "github", "latest_sha": "abc123", "remote_url": "https://token@example.com/repo.git"}},
		nil,
		[]map[string]any{{"id": "run-1", "git_remote_id": "remote-1", "commit_sha": "abc123", "workflow_logs": "secret logs"}},
		nil,
		nil,
		[]map[string]any{
			{"id": "op-git", "operation_type": "git.refs.refresh", "status": "completed", "input": map[string]any{"refresh_kind": "git_ref_fetch", "remote_url": "https://token@example.com/repo.git"}, "error": "Bearer secret"},
		},
	)
	snapshot := projectVersionValidationSnapshotPayload(preview, true)
	if snapshot["mode"] != "project_version_validation_snapshot" ||
		snapshot["project_version_id"] != "version-1" ||
		snapshot["validation_state"] != "ready" ||
		snapshot["repository_count"] != 1 ||
		snapshot["ready_count"] != 1 ||
		snapshot["operation_count"] != 1 ||
		snapshot["server_side_validation_recheck"] != true ||
		snapshot["server_side_validation_recheck_ready"] != true ||
		snapshot["project_version_asset_observed"] != true ||
		snapshot["external_call_made"] != false ||
		snapshot["provider_api_called"] != false ||
		snapshot["git_fetch_performed"] != false ||
		snapshot["argocd_api_called"] != false ||
		snapshot["raw_response_included"] != false ||
		snapshot["secret_included"] != false ||
		snapshot["operation_log_written"] != false ||
		snapshot["background_worker_enqueued"] != false {
		t.Fatalf("ProjectVersion validation snapshot payload = %#v", snapshot)
	}
	if missing := stringSliceFromAny(snapshot["missing_required_evidence"]); len(missing) != 0 {
		t.Fatalf("snapshot should not report missing evidence after terminal local recheck: %#v", missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, forbidden := range []string{
		"https://token@example.com",
		"Bearer secret",
		"secret logs",
		"abc123",
		"remote_url",
		"workflow_logs",
		"raw_provider_response",
		"raw_git_output",
		"provider_token",
		"authorization_header",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("ProjectVersion validation snapshot leaked %q: %s", forbidden, encoded)
		}
	}
}

package app

import (
	"testing"
)

func TestProjectVersionValidationPreviewUsesSyncedStateOnly(t *testing.T) {
	preview := projectVersionValidationPreview(
		map[string]any{
			"id":      "version-1",
			"version": "v0.1.0",
			"metadata": map[string]any{"repositories": []any{
				map[string]any{
					"repo_key":             "service",
					"repo_role":            "service",
					"remote_id":            "remote-1",
					"remote_key":           "github",
					"commit_sha":           "ABC123",
					"tag":                  "v0.1.0",
					"github_action_run_id": "run-1",
					"argo_revision":        "ABC123",
				},
			}},
		},
		[]map[string]any{{"id": "remote-1", "provider_type": "github", "latest_sha": "abc123"}},
		[]map[string]any{{"target_remote_id": "remote-1", "tag_name": "v0.1.0", "target_sha": "abc123"}},
		[]map[string]any{{"id": "run-1", "git_remote_id": "remote-1", "commit_sha": "abc123"}},
		[]map[string]any{{"metadata": map[string]any{"revision": "abc123"}}},
		[]map[string]any{{"id": "argo-1", "name": "staging"}},
	)
	if preview["validation_state"] != "ready" ||
		preview["external_call_made"] != false ||
		preview["provider_api_called"] != false ||
		preview["git_fetch_performed"] != false ||
		preview["argocd_api_called"] != false ||
		preview["validation_source"] != "local_synced_database_state" {
		t.Fatalf("project version validation preview = %#v", preview)
	}
	items := sliceOfMapsFromAny(preview["items"])
	if len(items) != 1 || items[0]["status"] != "ready" || items[0]["external_call_made"] != false || items[0]["secret_included"] != false {
		t.Fatalf("validation items = %#v", items)
	}
	checks := sliceOfMapsFromAny(items[0]["checks"])
	for _, name := range []string{"remote_present", "commit_matches_remote_latest", "tag_run_observed", "github_action_run_observed", "argo_revision_observed"} {
		if statusByName(checks, name) != "ready" {
			t.Fatalf("check %s not ready in %#v", name, checks)
		}
	}
	rehearsal := stringSliceFromAny(preview["required_live_rehearsal"])
	for _, required := range []string{"git_ref_fetch", "github_actions_api_refresh", "argocd_app_refresh"} {
		if !containsString(rehearsal, required) {
			t.Fatalf("required_live_rehearsal missing %q: %#v", required, rehearsal)
		}
	}
	refreshPlan := mapFromAny(preview["provider_refresh_plan"])
	if refreshPlan["plan_state"] != "planned" || refreshPlan["external_call_made"] != false || refreshPlan["planned_count"] != 3 || refreshPlan["blocked_count"] != 0 {
		t.Fatalf("refresh plan = %#v", refreshPlan)
	}
	executionPlan := mapFromAny(refreshPlan["execution_plan"])
	if executionPlan["mode"] != "provider_refresh_execution_plan_preview" ||
		executionPlan["execution_state"] != "ready_for_approval" ||
		executionPlan["execution_enabled"] != true ||
		executionPlan["planned_step_count"] != 3 ||
		executionPlan["blocked_step_count"] != 0 ||
		executionPlan["unique_planned_kind_count"] != 3 ||
		executionPlan["unique_blocked_kind_count"] != 0 {
		t.Fatalf("refresh execution plan = %#v", executionPlan)
	}
	backgroundPlan := mapFromAny(preview["background_validation_rerun_plan"])
	if backgroundPlan["mode"] != "project_version_background_validation_rerun_plan" ||
		backgroundPlan["plan_state"] != "blocked" ||
		backgroundPlan["background_rerun_ready_for_review"] != false ||
		backgroundPlan["automatic_background_rerun"] != false ||
		backgroundPlan["background_worker_enqueued"] != false ||
		backgroundPlan["validation_snapshot_written"] != false ||
		backgroundPlan["provider_refresh_operation_observed"] != false {
		t.Fatalf("background rerun plan without operations should stay blocked: %#v", backgroundPlan)
	}
	assertProjectVersionBackgroundRerunPlanSafe(t, backgroundPlan)
	snapshotPlan := mapFromAny(backgroundPlan["validation_snapshot_write_plan"])
	if snapshotPlan["mode"] != "project_version_validation_snapshot_write_plan" ||
		snapshotPlan["snapshot_state"] != "blocked" ||
		snapshotPlan["snapshot_ready_for_review"] != false ||
		snapshotPlan["snapshot_write_enabled"] != false ||
		snapshotPlan["provider_refresh_status"] != "not_requested" {
		t.Fatalf("snapshot write plan without operations should stay blocked: %#v", snapshotPlan)
	}
	assertProjectVersionValidationSnapshotWritePlanSafe(t, snapshotPlan)
	assertProviderRefreshExecutionPlanSafe(t, executionPlan)
	for _, required := range []string{"operation_approval", "provider_account_binding", "result_recording_audit", "ui_auto_validation_reload"} {
		if !containsString(stringSliceFromAny(executionPlan["required_controls"]), required) {
			t.Fatalf("execution plan required_controls missing %q: %#v", required, executionPlan)
		}
	}
	for _, backend := range []string{"provider_mutation", "raw_provider_response_recording", "server_side_automatic_validation_rerun"} {
		if !containsString(stringSliceFromAny(executionPlan["disabled_backends"]), backend) {
			t.Fatalf("execution plan disabled_backends missing %q: %#v", backend, executionPlan)
		}
	}
	steps := sliceOfMapsFromAny(refreshPlan["steps"])
	for _, kind := range []string{"git_ref_fetch", "github_actions_api_refresh", "argocd_app_refresh"} {
		if statusByKind(steps, kind) != "planned" {
			t.Fatalf("refresh step %s not planned in %#v", kind, steps)
		}
	}
}

func TestProjectVersionValidationPreviewReportsStandaloneBackgroundRerun(t *testing.T) {
	preview := projectVersionValidationPreview(
		map[string]any{
			"id":      "version-1",
			"version": "v0.1.0",
			"metadata": map[string]any{"repositories": []any{
				map[string]any{
					"repo_key":             "service",
					"repo_role":            "service",
					"remote_id":            "remote-1",
					"remote_key":           "github",
					"commit_sha":           "abc123",
					"tag":                  "v0.1.0",
					"github_action_run_id": "run-1",
				},
			}},
		},
		[]map[string]any{{"id": "remote-1", "provider_type": "github", "latest_sha": "abc123"}},
		[]map[string]any{{"target_remote_id": "remote-1", "tag_name": "v0.1.0", "target_sha": "abc123"}},
		[]map[string]any{{"id": "run-1", "git_remote_id": "remote-1", "commit_sha": "abc123"}},
		nil,
		nil,
		[]map[string]any{{"id": "op-refresh", "operation_type": "github.actions.sync", "status": "completed", "input": map[string]any{"refresh_kind": "github_actions_api_refresh"}}},
		[]map[string]any{{
			"id":             "op-validation",
			"operation_type": "project_version.validation_rerun",
			"status":         "completed",
			"result": map[string]any{
				"recording_state":               "recorded",
				"validation_snapshot_written":   true,
				"asset_status_snapshot_written": true,
			},
		}},
	)
	summary := mapFromAny(preview["background_validation_rerun_summary"])
	if summary["background_rerun_state"] != "recorded" || summary["operation_count"] != 1 || summary["validation_snapshot_written"] != true {
		t.Fatalf("background rerun summary = %#v", summary)
	}
	plan := mapFromAny(preview["background_validation_rerun_plan"])
	if plan["plan_state"] != "recorded" ||
		plan["automatic_background_rerun"] != true ||
		plan["background_worker_enqueued"] != true ||
		plan["standalone_background_worker_enabled"] != true ||
		plan["validation_snapshot_written"] != true ||
		plan["external_call_made"] != false ||
		plan["provider_api_called"] != false ||
		plan["raw_response_included"] != false {
		t.Fatalf("background rerun plan = %#v", plan)
	}
	assertProjectVersionBackgroundRerunPlanSafe(t, plan)
}

func TestProjectVersionValidationPreviewReportsPartialAndBlockedChecks(t *testing.T) {
	preview := projectVersionValidationPreview(
		map[string]any{
			"id":      "version-1",
			"version": "v0.1.0",
			"metadata": map[string]any{"repositories": []any{
				map[string]any{
					"repo_key":             "service",
					"remote_id":            "remote-1",
					"commit_sha":           "want-sha",
					"github_action_run_id": "run-1",
				},
				map[string]any{"repo_key": "missing", "remote_id": "remote-missing"},
			}},
		},
		[]map[string]any{{"id": "remote-1", "latest_sha": "other-sha"}},
		nil,
		[]map[string]any{{"id": "run-1", "git_remote_id": "remote-1", "commit_sha": "other-sha"}},
		nil,
	)
	if preview["validation_state"] != "partial" || preview["ready_count"] != 0 || preview["partial_count"] != 1 || preview["blocked_count"] != 1 {
		t.Fatalf("validation summary = %#v", preview)
	}
	items := sliceOfMapsFromAny(preview["items"])
	if len(items) != 2 || items[0]["status"] != "partial" || items[1]["status"] != "blocked" {
		t.Fatalf("validation items = %#v", items)
	}
	checks := sliceOfMapsFromAny(items[0]["checks"])
	if statusByName(checks, "remote_present") != "ready" ||
		statusByName(checks, "commit_matches_remote_latest") != "partial" ||
		statusByName(checks, "github_action_run_observed") != "partial" {
		t.Fatalf("partial item checks = %#v", checks)
	}
	refreshPlan := mapFromAny(preview["provider_refresh_plan"])
	if refreshPlan["plan_state"] != "partial" || refreshPlan["planned_count"] != 1 || refreshPlan["blocked_count"] != 2 {
		t.Fatalf("refresh plan should show planned refresh plus blocked steps: %#v", refreshPlan)
	}
	executionPlan := mapFromAny(refreshPlan["execution_plan"])
	if executionPlan["execution_state"] != "partial" ||
		executionPlan["planned_step_count"] != 1 ||
		executionPlan["blocked_step_count"] != 2 ||
		executionPlan["unique_planned_kind_count"] != 1 ||
		executionPlan["unique_blocked_kind_count"] != 1 ||
		!containsString(stringSliceFromAny(executionPlan["planned_refresh_kinds"]), "git_ref_fetch") ||
		!containsString(stringSliceFromAny(executionPlan["blocked_refresh_kinds"]), "github_actions_api_refresh") {
		t.Fatalf("partial refresh execution plan = %#v", executionPlan)
	}
	assertProviderRefreshExecutionPlanSafe(t, executionPlan)
}

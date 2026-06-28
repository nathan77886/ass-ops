package app

import (
	"testing"
)

func assertRepoTagRemoteRehearsalPlanSafe(t *testing.T, plan map[string]any) {
	t.Helper()
	if plan["execution_enabled"] != false ||
		plan["git_tag_created"] != false ||
		plan["git_push_performed"] != false ||
		plan["github_actions_refresh_performed"] != false ||
		plan["contains_token"] != false ||
		plan["contains_remote_url"] != false ||
		plan["contains_ref_name"] != false ||
		plan["contains_tag_message"] != false {
		t.Fatalf("tag rehearsal plan should keep live execution disabled and redacted: %#v", plan)
	}
	lookupPerformed := plan["remote_tag_lookup_performed"] == true
	if plan["external_call_made"] != lookupPerformed {
		t.Fatalf("external_call_made should track controlled lookup only: %#v", plan)
	}
	for _, backend := range []string{"git_tag", "git_push", "github_actions_api_sync"} {
		if !containsString(stringSliceFromAny(plan["disabled_backends"]), backend) {
			t.Fatalf("disabled backends missing %q: %#v", backend, plan["disabled_backends"])
		}
	}
	for _, backend := range []string{"remote_tag_lookup", "repo_tag_run_update"} {
		if lookupPerformed == containsString(stringSliceFromAny(plan["disabled_backends"]), backend) {
			t.Fatalf("lookup backend disabled state mismatch for %q: %#v", backend, plan["disabled_backends"])
		}
	}
	for _, step := range []string{"lookup_remote_tag_result", "persist_sanitized_tag_run_result", "refresh_github_actions_after_tag"} {
		if !containsString(stringSliceFromAny(plan["live_rehearsal_sequence"]), step) {
			t.Fatalf("live rehearsal sequence missing %q: %#v", step, plan["live_rehearsal_sequence"])
		}
	}
	for _, field := range []string{"remote_url", "git_credentials", "provider_token", "authorization_header", "tag_message", "git_output", "github_actions_response"} {
		if !containsString(stringSliceFromAny(plan["suppressed_fields"]), field) {
			t.Fatalf("suppressed fields missing %q: %#v", field, plan["suppressed_fields"])
		}
	}
	tagObserved := plan["live_remote_tag_success_observed"] == true
	tagFailed := plan["live_remote_tag_failed_observed"] == true
	wantSubplanState := "blocked"
	if tagObserved {
		wantSubplanState = "planned"
	}
	if tagFailed {
		wantSubplanState = "failed"
	}
	wantResultState := "blocked"
	wantResultReady := false
	if tagObserved {
		wantResultState = "recorded"
		wantResultReady = true
	}
	if tagFailed {
		wantResultState = "failed"
		wantResultReady = true
	}
	if !tagObserved && !tagFailed && plan["tag_name_configured"] == true && plan["target_sha_configured"] == true && plan["target_remote_bound"] == true {
		wantResultState = "waiting_for_worker"
	}
	if plan["result_written"] != wantResultReady {
		t.Fatalf("tag rehearsal result_written = %v, want %v: %#v", plan["result_written"], wantResultReady, plan)
	}
	wantLookupState := "blocked"
	if plan["tag_name_configured"] == true && plan["target_remote_bound"] == true {
		wantLookupState = "planned"
	}
	if plan["rehearsal_state"] == "running" {
		wantLookupState = "running"
	}
	if tagObserved {
		wantLookupState = "observed"
	}
	if tagFailed {
		wantLookupState = "failed"
	}
	if lookupPerformed && !tagFailed {
		wantLookupState = "observed"
	}
	lookupPreflight := mapFromAny(plan["live_remote_lookup_preflight"])
	if lookupPreflight["mode"] != "repo_tag_live_remote_lookup_preflight" ||
		lookupPreflight["lookup_state"] != wantLookupState ||
		lookupPreflight["remote_tag_lookup_performed"] != lookupPerformed ||
		lookupPreflight["git_ls_remote_performed"] != lookupPerformed ||
		lookupPreflight["provider_api_called"] != false ||
		lookupPreflight["github_actions_refresh_performed"] != false ||
		lookupPreflight["repo_tag_run_update_performed"] != lookupPerformed ||
		lookupPreflight["operation_log_written"] != false ||
		lookupPreflight["external_call_made"] != lookupPerformed ||
		lookupPreflight["contains_token"] != false ||
		lookupPreflight["contains_remote_url"] != false ||
		lookupPreflight["contains_ref_name"] != false ||
		lookupPreflight["contains_target_sha"] != false ||
		lookupPreflight["contains_tag_message"] != false {
		t.Fatalf("tag lookup preflight should stay disabled and redacted: %#v", lookupPreflight)
	}
	wantLookupReady := plan["tag_name_configured"] == true && plan["target_remote_bound"] == true && !tagFailed && !lookupPerformed
	if lookupPreflight["lookup_ready_for_review"] != wantLookupReady {
		t.Fatalf("lookup review readiness = %v, want %v: %#v", lookupPreflight["lookup_ready_for_review"], wantLookupReady, lookupPreflight)
	}
	for _, field := range []string{"target_remote_id", "tag_name", "tag_run_status", "repository_binding", "provider_type"} {
		if !containsString(stringSliceFromAny(lookupPreflight["required_lookup_fields"]), field) {
			t.Fatalf("lookup preflight required field missing %q: %#v", field, lookupPreflight["required_lookup_fields"])
		}
	}
	for _, backend := range []string{"provider_tag_lookup", "github_actions_api_sync", "operation_log_write"} {
		if !containsString(stringSliceFromAny(lookupPreflight["disabled_backends"]), backend) {
			t.Fatalf("lookup preflight disabled backend missing %q: %#v", backend, lookupPreflight["disabled_backends"])
		}
	}
	for _, backend := range []string{"remote_tag_lookup", "git_ls_remote", "repo_tag_run_update"} {
		if lookupPerformed == containsString(stringSliceFromAny(lookupPreflight["disabled_backends"]), backend) {
			t.Fatalf("lookup preflight backend disabled state mismatch for %q: %#v", backend, lookupPreflight["disabled_backends"])
		}
	}
	for _, field := range []string{"tag_name", "target_sha", "tag_message", "remote_url", "git_credentials", "provider_token", "authorization_header", "git_output", "github_actions_response", "provider_response_body", "provider_response_headers"} {
		if !containsString(stringSliceFromAny(lookupPreflight["suppressed_fields"]), field) {
			t.Fatalf("lookup preflight suppressed field missing %q: %#v", field, lookupPreflight["suppressed_fields"])
		}
	}
	if !lookupPerformed && !containsString(stringSliceFromAny(lookupPreflight["blocked_reasons"]), "remote_tag_lookup_not_run") && plan["rehearsal_state"] != "running" {
		t.Fatalf("lookup preflight should keep not-run blocker until lookup runs: %#v", lookupPreflight["blocked_reasons"])
	}
	wantLiveResultReason := "live_remote_tag_success_not_observed"
	if tagObserved {
		wantLiveResultReason = "repo_tag_run_result_update_not_wired"
	}
	if tagFailed {
		wantLiveResultReason = "live_remote_tag_failed_observed"
	}
	wantActionsRefreshReason := "live_remote_tag_success_not_observed"
	if tagObserved {
		wantActionsRefreshReason = "github_actions_refresh_not_performed"
	}
	if tagFailed {
		wantActionsRefreshReason = "live_remote_tag_failed_observed"
	}
	liveResultPlan := mapFromAny(plan["live_result_plan"])
	if liveResultPlan["mode"] != "repo_tag_live_result_plan" ||
		liveResultPlan["live_result_state"] != wantSubplanState ||
		liveResultPlan["remote_tag_lookup_performed"] != false ||
		liveResultPlan["repo_tag_run_result_written"] != false ||
		liveResultPlan["operation_log_written"] != false ||
		liveResultPlan["external_call_made"] != false ||
		liveResultPlan["contains_token"] != false ||
		liveResultPlan["contains_remote_url"] != false ||
		liveResultPlan["contains_ref_name"] != false ||
		liveResultPlan["contains_tag_message"] != false {
		t.Fatalf("tag live result plan should stay disabled and redacted: %#v", liveResultPlan)
	}
	if plan["live_remote_tag_success_observed"] == true && liveResultPlan["repo_tag_run_result_write_planned"] != true {
		t.Fatalf("observed tag should plan repo_tag_run result write: %#v", liveResultPlan)
	}
	if !containsString(stringSliceFromAny(liveResultPlan["blocked_reasons"]), wantLiveResultReason) ||
		!containsString(stringSliceFromAny(liveResultPlan["execution_blockers"]), "live_remote_tag_result_write_not_performed") {
		t.Fatalf("live result reasons/blockers = %#v", liveResultPlan)
	}
	if mapFromAny(liveResultPlan["live_remote_lookup_preflight"])["lookup_state"] != wantLookupState {
		t.Fatalf("live result should carry lookup preflight: %#v", liveResultPlan["live_remote_lookup_preflight"])
	}
	for _, backend := range []string{"remote_tag_lookup", "repo_tag_run_update", "operation_log_write"} {
		if !containsString(stringSliceFromAny(liveResultPlan["disabled_backends"]), backend) {
			t.Fatalf("live result disabled backends missing %q: %#v", backend, liveResultPlan["disabled_backends"])
		}
	}
	actionsRefreshPlan := mapFromAny(plan["actions_refresh_plan"])
	if actionsRefreshPlan["mode"] != "repo_tag_github_actions_refresh_plan" ||
		actionsRefreshPlan["refresh_state"] != wantSubplanState ||
		actionsRefreshPlan["refresh_after_tag_success_required"] != true ||
		actionsRefreshPlan["github_actions_refresh_performed"] != false ||
		actionsRefreshPlan["github_action_runs_synced"] != false ||
		actionsRefreshPlan["repo_tag_run_link_written"] != false ||
		actionsRefreshPlan["external_call_made"] != false ||
		actionsRefreshPlan["contains_token"] != false ||
		actionsRefreshPlan["contains_remote_url"] != false ||
		actionsRefreshPlan["contains_provider_response"] != false {
		t.Fatalf("tag actions refresh plan should stay disabled and redacted: %#v", actionsRefreshPlan)
	}
	if !containsString(stringSliceFromAny(actionsRefreshPlan["blocked_reasons"]), wantActionsRefreshReason) ||
		!containsString(stringSliceFromAny(actionsRefreshPlan["execution_blockers"]), "github_actions_refresh_not_performed") {
		t.Fatalf("actions refresh reasons/blockers = %#v", actionsRefreshPlan)
	}
	if mapFromAny(actionsRefreshPlan["live_remote_lookup_preflight"])["lookup_state"] != wantLookupState {
		t.Fatalf("actions refresh should carry lookup preflight: %#v", actionsRefreshPlan["live_remote_lookup_preflight"])
	}
	wantDisabledBackends := []string{"github_actions_api_sync", "github_action_run_link_write", "provider_response_recording"}
	if actionsRefreshPlan["github_actions_refresh_performed"] == true && intFromAny(actionsRefreshPlan["github_action_runs_synced_count"], 0) > 0 {
		wantDisabledBackends = []string{"provider_response_recording"}
	}
	for _, backend := range wantDisabledBackends {
		if !containsString(stringSliceFromAny(actionsRefreshPlan["disabled_backends"]), backend) {
			t.Fatalf("actions refresh disabled backends missing %q: %#v", backend, actionsRefreshPlan["disabled_backends"])
		}
	}
	resultPlan := mapFromAny(plan["result_recording_plan"])
	if resultPlan["mode"] != "repo_tag_remote_rehearsal_result_recording_plan" ||
		resultPlan["result_recording_state"] != wantResultState ||
		resultPlan["result_recording_ready"] != wantResultReady ||
		resultPlan["recording_enabled"] != wantResultReady ||
		resultPlan["result_written"] != wantResultReady ||
		resultPlan["repo_tag_run_updated"] != false ||
		resultPlan["github_action_runs_synced"] != false ||
		resultPlan["remote_tag_success_recorded"] != tagObserved ||
		resultPlan["live_result_subplan_recorded"] != wantResultReady ||
		resultPlan["actions_refresh_result_recorded"] != false ||
		resultPlan["raw_git_output_recorded"] != false ||
		resultPlan["raw_provider_response_recorded"] != false ||
		resultPlan["contains_token"] != false ||
		resultPlan["contains_remote_url"] != false ||
		resultPlan["contains_ref_name"] != false ||
		resultPlan["contains_tag_message"] != false {
		t.Fatalf("tag rehearsal result plan should stay disabled and redacted: %#v", resultPlan)
	}
	resultEvidence := mapFromAny(resultPlan["tag_result_evidence"])
	if resultEvidence["mode"] != "repo_tag_remote_result_evidence" ||
		resultEvidence["evidence_state"] != wantResultState && !(wantResultState == "blocked" && resultEvidence["evidence_state"] == "blocked") ||
		resultEvidence["external_call_made"] != false ||
		resultEvidence["raw_git_output_recorded"] != false ||
		resultEvidence["raw_provider_response_recorded"] != false ||
		resultEvidence["contains_token"] != false ||
		resultEvidence["contains_remote_url"] != false ||
		resultEvidence["contains_ref_name"] != false ||
		resultEvidence["contains_tag_message"] != false {
		t.Fatalf("tag result evidence should stay redacted: %#v", resultEvidence)
	}
	if mapFromAny(resultPlan["live_remote_lookup_preflight"])["lookup_state"] != wantLookupState {
		t.Fatalf("result recording should carry lookup preflight: %#v", resultPlan["live_remote_lookup_preflight"])
	}
	for _, field := range []string{"tag_run_status", "tag_name_configured", "target_sha_configured", "target_remote_bound", "live_remote_tag_success_observed", "live_remote_tag_failed_observed", "live_result_state", "github_actions_refresh_status", "github_actions_refresh_state"} {
		if !containsString(stringSliceFromAny(resultPlan["result_diagnostic_fields"]), field) {
			t.Fatalf("result diagnostic fields missing %q: %#v", field, resultPlan["result_diagnostic_fields"])
		}
	}
	for _, field := range []string{"remote_url", "git_credentials", "provider_token", "authorization_header", "tag_message", "git_output", "github_actions_response", "provider_response_body", "provider_response_headers"} {
		if !containsString(stringSliceFromAny(resultPlan["suppressed_fields"]), field) {
			t.Fatalf("result suppressed fields missing %q: %#v", field, resultPlan["suppressed_fields"])
		}
	}
}

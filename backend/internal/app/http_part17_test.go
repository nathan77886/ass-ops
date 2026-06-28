package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigRepositoryGitWorkflowRequestAndApprovalAuditAreRedacted(t *testing.T) {
	repo := map[string]any{
		"id":             "repo-1",
		"name":           "Config Repository",
		"repo_key":       "config",
		"repo_role":      "config",
		"default_branch": "main",
	}
	remotes := []map[string]any{{
		"id":             "remote-1",
		"provider_type":  "github",
		"default_branch": "main",
		"remote_url":     "https://token@example.com/repo.git",
	}}
	preview := configRepositoryScaffoldPreview(repo, remotes)
	input := configRepositoryGitWorkflowInput(repo, remotes, preview)
	if input["project_git_repository_id"] != "repo-1" ||
		input["config_remote_id"] != "remote-1" ||
		input["default_branch_configured"] != true ||
		input["git_write_performed"] != false ||
		input["external_call_made"] != false ||
		input["file_content_included"] != false ||
		input["secret_included"] != false {
		t.Fatalf("config workflow input = %#v", input)
	}
	op := map[string]any{"id": "op-1"}
	result := configRepositoryGitWorkflowRequestResult(op)
	if result["operation_created"] != true ||
		result["worker_job_created"] != true ||
		result["git_write_performed"] != false ||
		result["external_call_made"] != false ||
		result["project_version_pin_written"] != false ||
		result["sanitized_result_expected"] != true {
		t.Fatalf("config workflow request result = %#v", result)
	}
	audit := operationApprovalPayloadAudit(map[string]any{"request_payload": map[string]any{
		"kind":                  "config_git_commit",
		"project_id":            "project-1",
		"repo_id":               "repo-1",
		"input":                 input,
		"scaffold_file_count":   10,
		"project_version_count": 1,
		"approval_result": map[string]any{
			"operation_request_result": result,
		},
		"file_content":    "secret_values_here",
		"remote_url":      "https://token@example.com/repo.git",
		"provider_token":  "Bearer secret",
		"git_credentials": "PRIVATE KEY",
	}})
	if audit["kind"] != "config_git_commit" ||
		audit["payload_redacted"] != true ||
		audit["git_write_performed"] != false ||
		audit["external_call_made"] != false ||
		audit["file_content_included"] != false ||
		audit["secret_included"] != false {
		t.Fatalf("config workflow approval audit = %#v", audit)
	}
	encoded, _ := json.Marshal(audit)
	for _, forbidden := range []string{"secret_values_here", "https://token@", "Bearer secret", "PRIVATE KEY", "main"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("config workflow approval audit leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestRepoTagRemoteRehearsalPlan(t *testing.T) {
	items := repoTagRunsWithRemoteRehearsal([]map[string]any{
		{
			"id":               "run-1",
			"status":           "completed",
			"tag_name":         "v1.0.0",
			"target_sha":       "abc123",
			"target_remote_id": "remote-1",
			"operation_run_id": "op-1",
			"tag_message":      "do-not-serialize",
			"remote_url":       "https://token@example.com/repo.git",
			"provider_token":   "Bearer secret",
			"git_output":       "secret git output",
		},
		{
			"id":               "run-2",
			"status":           "queued",
			"tag_name":         "v1.0.1",
			"target_sha":       "",
			"target_remote_id": "remote-1",
		},
		{
			"id":         "run-3",
			"status":     "queued",
			"tag_name":   "",
			"target_sha": "def456",
		},
		{
			"id":            "run-4",
			"status":        "success",
			"tag_name":      "v1.0.2",
			"target_sha":    "abc456",
			"git_remote_id": "remote-2",
		},
		{
			"id":               "run-5",
			"status":           "failed",
			"tag_name":         "v1.0.3",
			"target_sha":       "abc789",
			"target_remote_id": "remote-3",
		},
		{
			"id":               "run-6",
			"tag_name":         "v1.0.4",
			"target_sha":       "abc999",
			"target_remote_id": "remote-4",
		},
		{
			"id":                      "run-7",
			"status":                  "running",
			"tag_name":                "v1.0.5",
			"target_remote_id":        "remote-5",
			"lookup_operation_status": "running",
		},
		{
			"id":                      "run-8",
			"status":                  "failed",
			"tag_name":                "v1.0.6",
			"target_remote_id":        "remote-6",
			"lookup_operation_status": "failed",
			"lookup_operation_error":  "sanitized lookup failure",
		},
		{
			"id":                      "run-9",
			"status":                  "completed",
			"tag_name":                "v1.0.7",
			"target_sha":              "abc777",
			"target_remote_id":        "remote-7",
			"lookup_operation_status": "completed",
			"lookup_operation_result": map[string]any{
				"git_remote_lookup_performed": true,
				"remote_tag_found":            true,
				"matched_sha_present":         true,
				"matched_count":               1,
				"raw_git_output_recorded":     false,
				"remote_url_recorded":         false,
				"credentials_recorded":        false,
				"contains_token":              false,
			},
		},
	})
	observedPlan := mapFromAny(items[0]["remote_rehearsal_plan"])
	if observedPlan["mode"] != "repo_tag_remote_rehearsal_plan" ||
		observedPlan["rehearsal_state"] != "observed" ||
		observedPlan["tag_run_status"] != "completed" ||
		observedPlan["tag_name_configured"] != true ||
		observedPlan["target_sha_configured"] != true ||
		observedPlan["target_remote_bound"] != true ||
		observedPlan["live_remote_tag_success_observed"] != true {
		t.Fatalf("observed tag rehearsal plan = %#v", observedPlan)
	}
	observedEvidence := mapFromAny(observedPlan["tag_result_evidence"])
	if observedEvidence["mode"] != "repo_tag_remote_result_evidence" ||
		observedEvidence["evidence_state"] != "recorded" ||
		observedEvidence["sanitized_result_recorded"] != true ||
		observedEvidence["operation_run_bound"] != true ||
		observedEvidence["git_tag_created"] != false ||
		observedEvidence["git_push_performed"] != false ||
		observedEvidence["remote_tag_lookup_performed"] != false ||
		observedEvidence["github_actions_refresh_performed"] != false {
		t.Fatalf("observed tag result evidence = %#v", observedEvidence)
	}
	assertRepoTagRemoteRehearsalPlanSafe(t, observedPlan)
	plannedPlan := mapFromAny(items[1]["remote_rehearsal_plan"])
	if plannedPlan["rehearsal_state"] != "planned" ||
		plannedPlan["target_sha_configured"] != false ||
		!containsString(stringSliceFromAny(plannedPlan["blocked_reasons"]), "target_sha_missing") ||
		!containsString(stringSliceFromAny(plannedPlan["blocked_reasons"]), "live_remote_tag_success_not_observed") {
		t.Fatalf("planned tag rehearsal plan = %#v", plannedPlan)
	}
	if lookup := mapFromAny(plannedPlan["live_remote_lookup_preflight"]); lookup["lookup_state"] != "planned" || lookup["lookup_ready_for_review"] != true {
		t.Fatalf("planned tag with missing SHA should still allow lookup review: %#v", lookup)
	}
	assertRepoTagRemoteRehearsalPlanSafe(t, plannedPlan)
	blockedPlan := mapFromAny(items[2]["remote_rehearsal_plan"])
	if blockedPlan["rehearsal_state"] != "blocked" ||
		blockedPlan["tag_name_configured"] != false ||
		blockedPlan["target_remote_bound"] != false ||
		!containsString(stringSliceFromAny(blockedPlan["blocked_reasons"]), "tag_name_missing") ||
		!containsString(stringSliceFromAny(blockedPlan["blocked_reasons"]), "target_remote_missing") {
		t.Fatalf("blocked tag rehearsal plan = %#v", blockedPlan)
	}
	assertRepoTagRemoteRehearsalPlanSafe(t, blockedPlan)
	fallbackPlan := mapFromAny(items[3]["remote_rehearsal_plan"])
	if fallbackPlan["rehearsal_state"] != "observed" ||
		fallbackPlan["tag_run_status"] != "success" ||
		fallbackPlan["target_remote_bound"] != true ||
		fallbackPlan["live_remote_tag_success_observed"] != true {
		t.Fatalf("git_remote_id fallback tag rehearsal plan = %#v", fallbackPlan)
	}
	assertRepoTagRemoteRehearsalPlanSafe(t, fallbackPlan)
	failedPlan := mapFromAny(items[4]["remote_rehearsal_plan"])
	if failedPlan["rehearsal_state"] != "failed" ||
		failedPlan["live_remote_tag_success_observed"] != false ||
		failedPlan["live_remote_tag_failed_observed"] != true ||
		!containsString(stringSliceFromAny(failedPlan["blocked_reasons"]), "live_remote_tag_failed_observed") {
		t.Fatalf("failed tag rehearsal plan = %#v", failedPlan)
	}
	failedEvidence := mapFromAny(failedPlan["tag_result_evidence"])
	if failedEvidence["evidence_state"] != "failed" ||
		failedEvidence["sanitized_result_recorded"] != true ||
		failedEvidence["live_remote_tag_failed_observed"] != true {
		t.Fatalf("failed tag result evidence = %#v", failedEvidence)
	}
	if lookup := mapFromAny(failedPlan["live_remote_lookup_preflight"]); lookup["lookup_state"] != "failed" || lookup["lookup_ready_for_review"] != false {
		t.Fatalf("failed tag should keep lookup failed and not review-ready: %#v", lookup)
	}
	assertRepoTagRemoteRehearsalPlanSafe(t, failedPlan)
	unknownPlan := mapFromAny(items[5]["remote_rehearsal_plan"])
	if unknownPlan["rehearsal_state"] != "planned" ||
		unknownPlan["tag_run_status"] != "unknown" ||
		unknownPlan["live_remote_tag_success_observed"] != false {
		t.Fatalf("unknown status tag rehearsal plan = %#v", unknownPlan)
	}
	if lookup := mapFromAny(unknownPlan["live_remote_lookup_preflight"]); lookup["lookup_state"] != "planned" || lookup["lookup_ready_for_review"] != true {
		t.Fatalf("unknown tag with complete metadata should plan lookup review: %#v", lookup)
	}
	assertRepoTagRemoteRehearsalPlanSafe(t, unknownPlan)
	runningLookupPlan := mapFromAny(items[6]["remote_rehearsal_plan"])
	if runningLookupPlan["rehearsal_state"] != "running" {
		t.Fatalf("running lookup should mark rehearsal running: %#v", runningLookupPlan)
	}
	if lookup := mapFromAny(runningLookupPlan["live_remote_lookup_preflight"]); lookup["lookup_state"] != "running" || lookup["lookup_ready_for_review"] != true {
		t.Fatalf("running lookup preflight = %#v", lookup)
	}
	assertRepoTagRemoteRehearsalPlanSafe(t, runningLookupPlan)
	failedLookupPlan := mapFromAny(items[7]["remote_rehearsal_plan"])
	if failedLookupPlan["rehearsal_state"] != "failed" {
		t.Fatalf("failed lookup should mark rehearsal failed: %#v", failedLookupPlan)
	}
	if lookup := mapFromAny(failedLookupPlan["live_remote_lookup_preflight"]); lookup["lookup_state"] != "failed" {
		t.Fatalf("failed lookup preflight = %#v", lookup)
	}
	assertRepoTagRemoteRehearsalPlanSafe(t, failedLookupPlan)
	observedLookupPlan := mapFromAny(items[8]["remote_rehearsal_plan"])
	if observedLookupPlan["rehearsal_state"] != "observed" ||
		observedLookupPlan["remote_tag_lookup_performed"] != true ||
		observedLookupPlan["external_call_made"] != true {
		t.Fatalf("observed lookup plan = %#v", observedLookupPlan)
	}
	if lookup := mapFromAny(observedLookupPlan["live_remote_lookup_preflight"]); lookup["lookup_state"] != "observed" || lookup["remote_tag_lookup_performed"] != true || lookup["repo_tag_run_update_performed"] != true {
		t.Fatalf("observed lookup preflight = %#v", lookup)
	}
	assertRepoTagRemoteRehearsalPlanSafe(t, observedLookupPlan)
	for _, plan := range []map[string]any{observedPlan, failedPlan} {
		encodedPlan, _ := json.Marshal(plan)
		for _, forbidden := range []string{"do-not-serialize", "git@github.com", "https://token@", "Bearer", "password", "abc123", "v1.0.0", "secret git output"} {
			if strings.Contains(string(encodedPlan), forbidden) {
				t.Fatalf("tag rehearsal plan leaked %q: %s", forbidden, encodedPlan)
			}
		}
	}
}

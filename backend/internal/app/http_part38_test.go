package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderReviewAttemptDependencySanitizers(t *testing.T) {
	for _, item := range []struct {
		input string
		want  string
	}{
		{"independent", "independent"},
		{"waiting_for_dependency", "waiting_for_dependency"},
		{"dependency_satisfied", "dependency_satisfied"},
		{"dependency_failed", "dependency_failed"},
		{"", "independent"},
		{"running", "independent"},
		{"<script>alert(1)</script>", "independent"},
		{strings.Repeat("x", 200), "independent"},
	} {
		if got := safeProviderReviewAttemptDependencyStatus(item.input); got != item.want {
			t.Fatalf("safeProviderReviewAttemptDependencyStatus(%q) = %q, want %q", item.input, got, item.want)
		}
	}
	for _, item := range []struct {
		input string
		want  string
		ready bool
	}{
		{"independent", "independent", true},
		{"dependency_satisfied", "dependency_satisfied", true},
		{"waiting_for_dependency", "waiting_for_dependency", false},
		{"dependency_failed", "dependency_failed", false},
		{"raw_dependency", "blocked", false},
		{"", "blocked", false},
	} {
		if got := safeProviderReviewAttemptClaimDependencyStatus(item.input); got != item.want {
			t.Fatalf("safeProviderReviewAttemptClaimDependencyStatus(%q) = %q, want %q", item.input, got, item.want)
		}
		if got := providerReviewAttemptClaimDependencyReady(item.input); got != item.ready {
			t.Fatalf("providerReviewAttemptClaimDependencyReady(%q) = %v, want %v", item.input, got, item.ready)
		}
	}
	for _, item := range []struct {
		input string
		want  string
	}{
		{"not_recorded", "not_recorded"},
		{"ready", "ready"},
		{"waiting_for_dependency", "waiting_for_dependency"},
		{"blocked", "blocked"},
		{"completed", "completed"},
		{"", "not_recorded"},
		{"<script>alert(1)</script>", "not_recorded"},
	} {
		if got := safeProviderReviewAttemptDependencyChainStatus(item.input); got != item.want {
			t.Fatalf("safeProviderReviewAttemptDependencyChainStatus(%q) = %q, want %q", item.input, got, item.want)
		}
	}
	for _, item := range []struct {
		input string
		want  string
	}{
		{"not_recorded", "not_recorded"},
		{"planned", "planned"},
		{"running", "running"},
		{"completed", "completed"},
		{"blocked", "blocked"},
		{"ready", "not_recorded"},
		{"", "not_recorded"},
		{"<script>alert(1)</script>", "not_recorded"},
	} {
		if got := safeProviderReviewAttemptOrchestrationStatus(item.input); got != item.want {
			t.Fatalf("safeProviderReviewAttemptOrchestrationStatus(%q) = %q, want %q", item.input, got, item.want)
		}
	}
	for _, item := range []struct {
		input string
		want  string
	}{
		{"", ""},
		{"create_branch_ref", "create_branch_ref"},
		{"commit_starter_files", "commit_starter_files"},
		{"open_review_request", ""},
		{"secret-repo", ""},
		{"<script>alert(1)</script>", ""},
		{strings.Repeat("x", 200), ""},
	} {
		if got := safeProviderReviewAttemptDependencyName(item.input); got != item.want {
			t.Fatalf("safeProviderReviewAttemptDependencyName(%q) = %q, want %q", item.input, got, item.want)
		}
	}
}

func TestProviderReviewAttemptLiveExecutionDiagnosticsExposeRetryAndCleanupOnly(t *testing.T) {
	attempt := map[string]any{
		"response_diagnostics": map[string]any{
			"mode":                   "raw_attempt_response_diagnostics",
			"endpoint_key":           "github.create_branch_ref",
			"status":                 "ready",
			"provider_api_call_made": true,
			"contains_token":         true,
			"token":                  "secret-token",
			"url":                    "https://api.github.example.test/repos/acme/secret-repo",
		},
	}
	result := reviewBranchExecutionResult{
		ExecutionPhase:      "commit_starter_files",
		ProviderStatusClass: "5xx",
		Retryable:           false,
		ManualCleanupHint:   "review_branch_delete_required",
		ExternalCallMade:    true,
		ProviderAPIMutation: true,
		CleanupAttempted:    true,
		CleanupRequired:     true,
	}
	diagnostics := providerReviewAttemptLiveExecutionDiagnostics(attempt, "failed", "5xx", result)
	if diagnostics["status"] != "failed" ||
		diagnostics["live_execution_phase"] != "commit_starter_files" ||
		diagnostics["live_execution_retryable"] != false ||
		diagnostics["manual_cleanup_hint"] != "review_branch_delete_required" ||
		diagnostics["cleanup_required"] != true ||
		diagnostics["contains_token"] != false ||
		diagnostics["contains_provider_url"] != false ||
		diagnostics["contains_repository_ref"] != false ||
		diagnostics["contains_branch_name"] != false ||
		diagnostics["contains_file_content"] != false {
		t.Fatalf("unexpected live execution diagnostics: %#v", diagnostics)
	}
	encoded, _ := json.Marshal(diagnostics)
	for _, leak := range []string{"secret-token", "api.github.example.test", "secret-repo", "raw_attempt_response_diagnostics"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("live execution diagnostics leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptLiveCleanupResponseStaysSanitized(t *testing.T) {
	attempt := map[string]any{
		"id":                                 "attempt-1",
		"operation_approval_id":              "approval-1",
		"operation_name":                     "create_branch_ref",
		"endpoint_key":                       "github.create_branch_ref",
		"status":                             "failed",
		"provider_type":                      "github",
		"provider_api_mutation":              "enabled",
		"live_execution_manual_cleanup_hint": "review_branch_delete_required",
		"cleanup_attempted":                  true,
		"cleanup_succeeded":                  true,
		"cleanup_required":                   false,
		"response_diagnostics": map[string]any{
			"token":          "secret-token",
			"provider_url":   "https://api.github.example.test/repos/acme/secret-repo",
			"repository_ref": "refs/heads/assops/template/secret",
		},
	}
	result := reviewBranchExecutionResult{
		ExecutionPhase:      "cleanup_review_branch",
		ProviderStatusClass: "2xx",
		Retryable:           false,
		ExternalCallMade:    true,
		ProviderAPIMutation: true,
		CleanupAttempted:    true,
		CleanupSucceeded:    true,
		CleanupRequired:     false,
	}
	response := providerReviewAttemptLiveCleanupResponse(attempt, providerReviewAttemptLedgerSummary([]map[string]any{attempt}), true, "success", "2xx", result)
	if response["live_cleanup_state"] != "cleanup_completed" ||
		response["live_cleanup_success"] != true ||
		response["cleanup_required"] != false ||
		response["contains_token"] != false ||
		response["contains_repository_ref"] != false ||
		response["contains_branch_name"] != false {
		t.Fatalf("unexpected cleanup response: %#v", response)
	}
	encoded, _ := json.Marshal(response)
	for _, leak := range []string{"secret-token", "api.github.example.test", "secret-repo", "refs/heads/assops/template/secret"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("live cleanup response leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptOrchestrationSummaryBlocksUnknownStatus(t *testing.T) {
	summary := providerReviewAttemptOrchestrationSummary([]map[string]any{{
		"name":              "create_branch_ref",
		"status":            "retrying",
		"dependency_status": "raw_dependency",
	}})
	if summary["ready_count"] != 0 ||
		summary["blocked_count"] != 1 ||
		summary["next_operation"] != "" ||
		summary["dependency_chain_status"] != "blocked" {
		t.Fatalf("unknown status orchestration summary = %#v", summary)
	}
	chainPlan := mapFromAny(summary["dependency_chain_plan"])
	if chainPlan["status"] != "blocked" ||
		chainPlan["chain_ready_for_next_attempt"] != false ||
		chainPlan["blocked_count"] != 1 ||
		chainPlan["provider_api_call_made"] != false ||
		chainPlan["provider_api_mutation"] != "disabled" {
		t.Fatalf("unknown status dependency chain plan = %#v", chainPlan)
	}
	ordered := sliceOfMapsFromAny(chainPlan["ordered_operations"])
	if len(ordered) != 1 || ordered[0]["dependency_status"] != "blocked" {
		t.Fatalf("unknown dependency status should stay blocked in chain plan: %#v", ordered)
	}
	candidate := mapFromAny(summary["execution_candidate"])
	if candidate["next_operation"] != "" || candidate["status"] != "blocked" {
		t.Fatalf("unknown status execution candidate = %#v", candidate)
	}
}

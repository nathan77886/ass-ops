package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReviewBranchAttemptLedgerAdapterPlanRedactsExecutorInputs(t *testing.T) {
	plan := reviewBranchAttemptLedgerAdapterPlan(reviewBranchAttemptLedgerAdapterInput{
		ProviderType:           "github",
		OperationName:          "create_branch_ref",
		EndpointKey:            "github.create_branch_ref",
		AttemptStatus:          "running",
		DependencyStatus:       "independent",
		FileCount:              2,
		ClaimRecorded:          true,
		PreflightMetadataReady: true,
	})
	if plan["adapter_plan_ready"] != true ||
		plan["live_execution_ready"] != false ||
		plan["schema_allows_provider_call"] != false ||
		plan["provider_api_mutation"] != "disabled" ||
		plan["provider_api_call_made"] != false ||
		plan["external_call_made"] != false {
		t.Fatalf("unexpected review branch ledger plan: %#v", plan)
	}
	pipeline := mapSliceFromAny(plan["pipeline"])
	if len(pipeline) != 3 {
		t.Fatalf("pipeline length = %d, want 3", len(pipeline))
	}
	want := []struct {
		name     string
		endpoint string
		files    int
	}{
		{name: "create_branch_ref", endpoint: "github.create_branch_ref"},
		{name: "commit_starter_files", endpoint: "github.commit_files", files: 2},
		{name: "open_review_request", endpoint: "github.open_review"},
	}
	for i, item := range want {
		if pipeline[i]["name"] != item.name ||
			pipeline[i]["endpoint_key"] != item.endpoint ||
			pipeline[i]["file_count"] != item.files ||
			pipeline[i]["provider_api_mutation"] != "disabled" ||
			pipeline[i]["contains_token"] != false ||
			pipeline[i]["contains_file_content"] != false {
			t.Fatalf("unexpected pipeline[%d]: %#v", i, pipeline[i])
		}
	}
	encoded, _ := json.Marshal(plan)
	for _, forbidden := range []string{
		"test-token",
		"ASSOPS_GITHUB_TEMPLATE_TOKEN",
		"assops/review/attempt-1",
		"release/main",
		"# hello",
		"package service",
		"https://api.github.com",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("plan leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestReviewBranchAttemptLedgerAdapterPlanBlocksUnsupportedOperation(t *testing.T) {
	plan := reviewBranchAttemptLedgerAdapterPlan(reviewBranchAttemptLedgerAdapterInput{
		ProviderType:           "github",
		OperationName:          "commit_starter_files",
		EndpointKey:            "github.open_review",
		AttemptStatus:          "running",
		DependencyStatus:       "dependency_satisfied",
		ClaimRecorded:          true,
		PreflightMetadataReady: true,
	})
	if plan["adapter_plan_ready"] != false ||
		plan["operation_supported"] != false ||
		!containsString(stringSliceFromAny(plan["blocked_reasons"]), "provider_review_attempt_operation_endpoint_invalid") {
		t.Fatalf("unsupported operation should be blocked: %#v", plan)
	}
}

func TestReviewBranchAttemptLedgerAdapterPlanFromEmptyAttemptBlocks(t *testing.T) {
	plan := reviewBranchAttemptLedgerAdapterPlanFromAttempt(map[string]any{}, map[string]any{})
	if plan["adapter_plan_ready"] != false ||
		plan["operation_supported"] != false ||
		plan["ledger_metadata_ready"] != false ||
		plan["live_execution_ready"] != false ||
		plan["provider_api_mutation"] != "disabled" ||
		!containsString(stringSliceFromAny(plan["blocked_reasons"]), "provider_review_attempt_operation_endpoint_invalid") {
		t.Fatalf("empty attempt should produce blocked dry-run plan: %#v", plan)
	}
}

func TestReviewBranchAttemptLedgerAdapterPlanFromLaunchPayload(t *testing.T) {
	attempt := map[string]any{
		"id":                      "attempt-1",
		"operation_approval_id":   "approval-1",
		"project_template_run_id": "run-1",
		"provider_type":           "github",
		"operation_name":          "commit_starter_files",
		"endpoint_key":            "github.commit_files",
		"status":                  "running",
		"dependency_status":       "dependency_satisfied",
		"claimed_at":              "2026-06-26T00:00:00Z",
		"request_summary":         map[string]any{"file_count": 3},
	}
	preflight := map[string]any{
		"preflight": map[string]any{
			"live_execution_preflight_metadata_ready": true,
		},
	}
	launch := providerReviewAttemptLiveExecutionLaunchPlanPayload(attempt, preflight)
	plan := mapFromAny(launch["review_branch_ledger_adapter_plan"])
	if !reviewBranchAttemptLedgerAdapterPlanMatchesAttempt(plan, "commit_starter_files", "github.commit_files") ||
		launch["review_branch_ledger_adapter_observed"] != true ||
		plan["file_count"] != 3 ||
		plan["ledger_metadata_ready"] != true ||
		plan["live_execution_ready"] != false {
		t.Fatalf("unexpected launch ledger adapter plan: launch=%#v plan=%#v", launch, plan)
	}
}

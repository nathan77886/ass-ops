package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRecordProviderReviewAttemptLocalResultHandlerRejectsInvalidResultBeforeDBLookup(t *testing.T) {
	server := &Server{}
	rr := httptest.NewRecorder()

	server.recordProviderReviewAttemptLocalResult(rr, newProviderReviewAttemptLocalResultRequest(`{"result":"provider_success_with_body"}`))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rr.Code, rr.Body.String())
	}
}

func TestRecordProviderReviewAttemptLocalResultHandlerRejectsInvalidJSONBeforeDBLookup(t *testing.T) {
	server := &Server{}
	rr := httptest.NewRecorder()

	server.recordProviderReviewAttemptLocalResult(rr, newProviderReviewAttemptLocalResultRequest(`{"result":`))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rr.Code, rr.Body.String())
	}
}

func TestProviderReviewAttemptSnapshotPayloadSanitizesAttempt(t *testing.T) {
	snapshot := providerReviewAttemptSnapshotPayload(map[string]any{
		"id":                      "attempt-1",
		"operation_approval_id":   "approval-1",
		"project_template_run_id": "run-1",
		"provider_type":           "github",
		"review_kind":             "pull_request",
		"operation_name":          "create_branch_ref",
		"endpoint_key":            "github.create_branch_ref",
		"status":                  "running",
		"dependency_status":       "independent",
		"operation_order":         10,
		"replay_check":            "detect_existing_branch_ref",
		"conflict_policy":         "treat_existing_matching_ref_as_success",
		"retry_policy":            "retry_only_after_response_diagnostics",
		"request_summary":         map[string]any{"raw": "secret request body"},
		"response_diagnostics":    map[string]any{"raw": "secret response body"},
		"idempotency_key_hash":    "secret hash",
		"claimed_by_user_id":      "user-secret",
		"claimed_at":              time.Now(),
	}, true)
	ready, state, missing := providerReviewAttemptSnapshotReadiness(snapshot)
	if !ready || state != "running" || len(missing) != 0 {
		t.Fatalf("readiness = %v/%s/%#v; snapshot=%#v", ready, state, missing, snapshot)
	}
	if snapshot["provider_review_attempt_asset_observed"] != true ||
		snapshot["status_snapshot_write_eligible"] != true ||
		snapshot["status_snapshot_written"] != true ||
		snapshot["status_snapshot_written"] != snapshot["status_snapshot_write_eligible"] ||
		snapshot["provider_api_call_made"] != false ||
		snapshot["external_call_made"] != false ||
		snapshot["provider_api_mutation"] != "disabled" ||
		snapshot["request_body_included"] != false ||
		snapshot["response_body_included"] != false ||
		snapshot["idempotency_key_included"] != false ||
		snapshot["contains_token"] != false ||
		snapshot["contains_provider_url"] != false ||
		snapshot["contains_repository_ref"] != false ||
		snapshot["contains_branch_name"] != false ||
		snapshot["contains_file_content"] != false {
		t.Fatalf("unexpected provider review attempt snapshot: %#v", snapshot)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, forbidden := range []string{"secret request body", "secret response body", "secret hash", "user-secret", "claimed_by_user_id"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("provider review attempt snapshot leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestProviderReviewAttemptSnapshotPayloadBlocksIncompleteIdentity(t *testing.T) {
	snapshot := providerReviewAttemptSnapshotPayload(map[string]any{
		"id":                      "attempt-1",
		"operation_approval_id":   "approval-1",
		"project_template_run_id": "run-1",
		"provider_type":           "github",
		"review_kind":             "pull_request",
		"operation_name":          "create_branch_ref",
		"endpoint_key":            "",
		"status":                  "planned",
		"dependency_status":       "independent",
	}, true)
	ready, state, missing := providerReviewAttemptSnapshotReadiness(snapshot)
	if ready ||
		state != "planned" ||
		snapshot["status_snapshot_write_eligible"] != false ||
		snapshot["status_snapshot_written"] != snapshot["status_snapshot_write_eligible"] ||
		!containsString(missing, "provider_review_attempt_endpoint_missing") {
		t.Fatalf("incomplete attempt identity should block snapshot write eligibility: ready=%v state=%s missing=%#v snapshot=%#v", ready, state, missing, snapshot)
	}
}

func TestProviderReviewAdapterRehearsalSanitizerRecomputesStatus(t *testing.T) {
	summary := sanitizedProviderReviewAdapterRehearsal(map[string]any{
		"status":                    "ready",
		"operation_count":           99,
		"ready_operation_count":     98,
		"blocked_operation_count":   97,
		"mutation_arming_candidate": true,
		"operations": []map[string]any{
			{
				"name":               "commit_starter_files",
				"endpoint_key":       "github.commit_files",
				"status":             "blocked",
				"blocked_reasons":    []any{"starter_file_payload_staged"},
				"external_call_made": true,
			},
		},
	})
	if summary["status"] != "blocked" ||
		summary["operation_count"] != 1 ||
		summary["ready_operation_count"] != 0 ||
		summary["blocked_operation_count"] != 1 ||
		summary["mutation_arming_candidate"] != false ||
		summary["external_call_made"] != false ||
		summary["provider_api_mutation"] != "disabled" {
		t.Fatalf("sanitized rehearsal should recompute status and counts: %#v", summary)
	}
	empty := sanitizedProviderReviewAdapterRehearsal(map[string]any{"status": "ready"})
	if empty["status"] != "not_recorded" || empty["mutation_arming_candidate"] != false {
		t.Fatalf("empty rehearsal should be not recorded: %#v", empty)
	}
}

func TestProviderReviewMutationArmingPlanSanitizerKeepsMutationOff(t *testing.T) {
	armed := sanitizedProviderReviewMutationArmingPlan(map[string]any{
		"status":                   "armed",
		"mode":                     "raw_mutation_arming_plan",
		"required_config":          "SECRET_CONFIG",
		"execution_enabled_config": true,
		"adapter_rehearsal_ready":  true,
		"mutation_armed":           true,
		"external_call_made":       true,
		"provider_api_call_made":   true,
		"provider_api_mutation":    "enabled",
		"contains_token":           true,
		"contains_provider_url":    true,
		"contains_repository_ref":  true,
		"contains_file_content":    true,
		"blocked_reasons":          []any{"provider_review_mutation_armed", "<script>alert(1)</script>"},
	})
	if armed["status"] != "ready_to_arm" ||
		armed["mode"] != "redacted_mutation_arming_plan" ||
		armed["required_config"] != "ASSOPS_ARM_PROVIDER_REVIEW_MUTATION" ||
		armed["mutation_armed"] != false ||
		armed["external_call_made"] != false ||
		armed["provider_api_call_made"] != false ||
		armed["provider_api_mutation"] != "disabled" ||
		armed["contains_token"] != false ||
		armed["contains_provider_url"] != false ||
		armed["contains_repository_ref"] != false ||
		armed["contains_file_content"] != false ||
		armed["adapter_mutation_currently_off"] != true {
		t.Fatalf("armed mutation plan should be downgraded and redacted: %#v", armed)
	}
	reasons := stringSliceFromAny(armed["blocked_reasons"])
	if !containsString(reasons, "provider_review_mutation_armed") ||
		containsString(reasons, "<script>alert(1)</script>") {
		t.Fatalf("mutation arming reasons should be allowlisted: %#v", reasons)
	}

	blocked := sanitizedProviderReviewMutationArmingPlan(map[string]any{
		"status":                   "ready_to_arm",
		"execution_enabled_config": true,
		"adapter_rehearsal_ready":  false,
	})
	if blocked["status"] != "blocked" || blocked["mutation_armed"] != false || blocked["provider_api_mutation"] != "disabled" {
		t.Fatalf("blocked mutation plan should remain mutation-off: %#v", blocked)
	}
}

func TestProviderReviewCurrentAttemptLiveExecutionLaunchPlanRejectsNilStore(t *testing.T) {
	_, err := ProviderReviewCurrentAttemptLiveExecutionLaunchPlan(context.Background(), nil, ProviderReviewCurrentAttemptLiveExecutionLaunchPlanOptions{
		OperationApprovalID: "approval-1",
	})
	if err == nil || !strings.Contains(err.Error(), "store is required") {
		t.Fatalf("ProviderReviewCurrentAttemptLiveExecutionLaunchPlan nil store error = %v, want store is required", err)
	}
}

func TestProviderReviewCurrentLiveExecutionGateRejectsNilStore(t *testing.T) {
	_, err := ProviderReviewCurrentLiveExecutionGate(context.Background(), nil, ProviderReviewCurrentLiveExecutionGateOptions{
		OperationApprovalID: "approval-1",
	})
	if err == nil || !strings.Contains(err.Error(), "store is required") {
		t.Fatalf("ProviderReviewCurrentLiveExecutionGate nil store error = %v, want store is required", err)
	}
}

func TestProviderReviewCurrentAttemptCandidateFromLedger(t *testing.T) {
	ledger := providerReviewAttemptLedgerSummary([]map[string]any{
		{
			"id":                   "attempt-1",
			"operation_name":       "create_branch_ref",
			"endpoint_key":         "github.create_branch_ref",
			"status":               "planned",
			"dependency_status":    "independent",
			"operation_order":      10,
			"request_summary":      map[string]any{},
			"response_diagnostics": map[string]any{},
		},
	})
	if got := providerReviewCurrentAttemptCandidateFromLedger(ledger); cleanOptionalID(fmt.Sprint(got["id"])) != "attempt-1" {
		t.Fatalf("candidate id = %#v, want attempt-1", got)
	}
	tests := []struct {
		name   string
		ledger map[string]any
	}{
		{
			name: "missing next operation",
			ledger: map[string]any{
				"operations": ledger["operations"],
				"orchestration": map[string]any{"execution_candidate": map[string]any{
					"endpoint_key": "github.create_branch_ref",
				}},
			},
		},
		{
			name: "missing endpoint",
			ledger: map[string]any{
				"operations": ledger["operations"],
				"orchestration": map[string]any{"execution_candidate": map[string]any{
					"next_operation": "create_branch_ref",
				}},
			},
		},
		{
			name: "mismatched endpoint",
			ledger: map[string]any{
				"operations": ledger["operations"],
				"orchestration": map[string]any{"execution_candidate": map[string]any{
					"next_operation": "create_branch_ref",
					"endpoint_key":   "github.open_review",
				}},
			},
		},
		{
			name: "empty operations",
			ledger: map[string]any{
				"operations": []map[string]any{},
				"orchestration": map[string]any{"execution_candidate": map[string]any{
					"next_operation": "create_branch_ref",
					"endpoint_key":   "github.create_branch_ref",
				}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := providerReviewCurrentAttemptCandidateFromLedger(tt.ledger); len(got) != 0 {
				t.Fatalf("candidate = %#v, want empty", got)
			}
		})
	}
}

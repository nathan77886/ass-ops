package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderReviewAttemptAdapterTransactionPlan(t *testing.T) {
	operation := map[string]any{
		"name":            "create_branch_ref",
		"endpoint_key":    "github.create_branch_ref",
		"operation_order": 10,
	}
	claimPlan := map[string]any{
		"mode":                 "redacted_attempt_execution_claim_plan",
		"operation_name":       "create_branch_ref",
		"endpoint_key":         "github.create_branch_ref",
		"claim_metadata_ready": true,
	}
	responsePlan := map[string]any{
		"mode":                         "redacted_attempt_adapter_response_plan",
		"operation_name":               "create_branch_ref",
		"endpoint_key":                 "github.create_branch_ref",
		"success_attempt_status":       "completed",
		"retry_attempt_status":         "planned",
		"failure_attempt_status":       "failed",
		"dependency_unlocks_operation": "commit_starter_files",
		"dependency_update_status":     "dependency_satisfied",
		"requires_dependency_update":   true,
	}
	plan := providerReviewAttemptAdapterTransactionPlan(operation, claimPlan, responsePlan)
	if plan["mode"] != "redacted_attempt_adapter_transaction_plan" ||
		plan["transaction_state"] != "blocked" ||
		plan["transaction_ready"] != false ||
		plan["transaction_ready_reason"] != "provider_review_transaction_not_armed" ||
		plan["transaction_metadata_ready"] != true ||
		plan["operation_name"] != "create_branch_ref" ||
		plan["endpoint_key"] != "github.create_branch_ref" ||
		plan["operation_order"] != 10 ||
		plan["claim_status_from"] != "planned" ||
		plan["claim_status_to"] != "running" ||
		plan["success_attempt_status"] != "completed" ||
		plan["retry_attempt_status"] != "planned" ||
		plan["failure_attempt_status"] != "failed" ||
		plan["dependency_unlocks_operation"] != "commit_starter_files" ||
		plan["dependency_update_status"] != "dependency_satisfied" ||
		plan["requires_database_transaction"] != true ||
		plan["requires_attempt_status_planned"] != true ||
		plan["requires_attempt_status_running"] != true ||
		plan["requires_optimistic_lock"] != true ||
		plan["requires_idempotency_ledger"] != true ||
		plan["requires_provider_call_boundary"] != true ||
		plan["requires_response_diagnostics"] != true ||
		plan["requires_dependency_update"] != true ||
		plan["requires_mutation_arming"] != true ||
		len(mapFromAny(plan["provider_call_boundary_plan"])) == 0 ||
		plan["transaction_opened"] != false ||
		plan["attempt_claim_verified"] != false ||
		plan["idempotency_claim_verified"] != false ||
		plan["provider_call_boundary_recorded"] != false ||
		plan["provider_response_classified"] != false ||
		plan["attempt_status_updated"] != false ||
		plan["response_recorded"] != false ||
		plan["dependency_update_recorded"] != false ||
		plan["provider_request_id_recorded"] != false ||
		plan["provider_response_body_recorded"] != false ||
		plan["provider_response_headers_recorded"] != false ||
		plan["adapter_implemented"] != false ||
		plan["mutation_armed"] != false ||
		plan["external_call_made"] != false ||
		plan["provider_api_call_made"] != false ||
		plan["provider_api_mutation"] != "disabled" ||
		plan["request_body_included"] != false ||
		plan["response_body_included"] != false ||
		plan["headers_included"] != false ||
		plan["authorization_header_included"] != false ||
		plan["provider_url_included"] != false ||
		plan["idempotency_key_included"] != false ||
		plan["contains_token"] != false ||
		plan["contains_provider_url"] != false ||
		plan["contains_repository_ref"] != false ||
		plan["contains_branch_name"] != false ||
		plan["contains_file_content"] != false ||
		plan["transaction_boundary_redacted"] != true {
		t.Fatalf("providerReviewAttemptAdapterTransactionPlan() = %#v", plan)
	}
	sequence := stringSliceFromAny(plan["transaction_sequence"])
	if len(sequence) != 6 ||
		sequence[0] != "verify_attempt_claim" ||
		sequence[1] != "verify_idempotency_claim" ||
		sequence[2] != "record_provider_call_boundary" ||
		sequence[3] != "classify_provider_response" ||
		sequence[4] != "update_attempt_status" ||
		sequence[5] != "update_dependency_status" {
		t.Fatalf("transaction sequence = %#v", sequence)
	}
	boundaryPlan := mapFromAny(plan["provider_call_boundary_plan"])
	if boundaryPlan["mode"] != "redacted_attempt_adapter_provider_call_boundary_plan" ||
		boundaryPlan["provider_call_boundary_state"] != "blocked" ||
		boundaryPlan["provider_call_boundary_ready"] != false ||
		boundaryPlan["provider_call_boundary_ready_reason"] != "provider_review_provider_call_boundary_not_armed" ||
		boundaryPlan["provider_call_boundary_metadata_ready"] != true ||
		boundaryPlan["operation_name"] != "create_branch_ref" ||
		boundaryPlan["endpoint_key"] != "github.create_branch_ref" ||
		boundaryPlan["operation_order"] != 10 ||
		boundaryPlan["idempotency_key_kind"] != "operation_scope_hash" ||
		boundaryPlan["requires_database_transaction"] != true ||
		boundaryPlan["requires_attempt_claim"] != true ||
		boundaryPlan["requires_idempotency_claim"] != true ||
		boundaryPlan["requires_response_diagnostics"] != true ||
		boundaryPlan["requires_mutation_arming"] != true ||
		boundaryPlan["transaction_opened"] != false ||
		boundaryPlan["attempt_claim_verified"] != false ||
		boundaryPlan["idempotency_claim_verified"] != false ||
		boundaryPlan["provider_call_boundary_opened"] != false ||
		boundaryPlan["provider_call_boundary_recorded"] != false ||
		boundaryPlan["provider_call_started_recorded"] != false ||
		boundaryPlan["provider_call_finished_recorded"] != false ||
		boundaryPlan["provider_request_sent"] != false ||
		boundaryPlan["provider_response_received"] != false ||
		boundaryPlan["provider_request_id_recorded"] != false ||
		boundaryPlan["provider_response_status_recorded"] != false ||
		boundaryPlan["provider_response_body_recorded"] != false ||
		boundaryPlan["provider_response_headers_recorded"] != false ||
		boundaryPlan["provider_call_boundary_redacted"] != true ||
		boundaryPlan["external_call_made"] != false ||
		boundaryPlan["provider_api_call_made"] != false ||
		boundaryPlan["provider_api_mutation"] != "disabled" ||
		boundaryPlan["request_body_included"] != false ||
		boundaryPlan["response_body_included"] != false ||
		boundaryPlan["headers_included"] != false ||
		boundaryPlan["authorization_header_included"] != false ||
		boundaryPlan["provider_url_included"] != false ||
		boundaryPlan["idempotency_key_included"] != false ||
		boundaryPlan["provider_request_id_included"] != false ||
		boundaryPlan["contains_token"] != false ||
		boundaryPlan["contains_provider_url"] != false ||
		boundaryPlan["contains_repository_ref"] != false ||
		boundaryPlan["contains_branch_name"] != false ||
		boundaryPlan["contains_file_content"] != false {
		t.Fatalf("provider call boundary plan = %#v", boundaryPlan)
	}
	boundarySequence := stringSliceFromAny(boundaryPlan["boundary_sequence"])
	if len(boundarySequence) != 7 ||
		boundarySequence[0] != "verify_attempt_claim" ||
		boundarySequence[4] != "stage_provider_request_send" ||
		boundarySequence[6] != "commit_database_transaction" {
		t.Fatalf("provider call boundary sequence = %#v", boundarySequence)
	}
	boundaryBlockedReasons := stringSliceFromAny(boundaryPlan["blocked_reasons"])
	if len(boundaryBlockedReasons) != 4 ||
		boundaryBlockedReasons[0] != "provider_review_provider_call_boundary_not_armed" ||
		boundaryBlockedReasons[1] != "provider_api_call_not_made" ||
		boundaryBlockedReasons[2] != "provider_review_adapter_not_implemented" ||
		boundaryBlockedReasons[3] != "provider_review_mutation_not_armed" {
		t.Fatalf("provider call boundary blocked reasons = %#v", boundaryBlockedReasons)
	}
	blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
	if len(blockedReasons) != 5 ||
		blockedReasons[0] != "provider_review_attempt_claim_not_recorded" ||
		blockedReasons[1] != "provider_review_transaction_not_armed" ||
		blockedReasons[2] != "provider_api_call_not_made" ||
		blockedReasons[3] != "provider_review_adapter_not_implemented" ||
		blockedReasons[4] != "provider_review_mutation_not_armed" {
		t.Fatalf("transaction blocked reasons = %#v", blockedReasons)
	}
	encoded, _ := json.Marshal(plan)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("transaction plan leaked %q: %s", leak, encoded)
		}
	}
	if got := providerReviewAttemptAdapterTransactionPlan(nil, nil, nil); len(got) != 0 {
		t.Fatalf("empty operation transaction plan = %#v", got)
	}
	got := providerReviewAttemptAdapterTransactionPlan(
		map[string]any{"name": "raw_operation", "endpoint_key": "github.create_branch_ref"},
		claimPlan,
		responsePlan,
	)
	if len(got) != 0 {
		t.Fatalf("invalid operation transaction plan should be empty: %#v", got)
	}
	got = providerReviewAttemptAdapterTransactionPlan(
		map[string]any{"name": "create_branch_ref", "endpoint_key": "github.secret"},
		claimPlan,
		responsePlan,
	)
	if len(got) != 0 {
		t.Fatalf("invalid endpoint transaction plan should be empty: %#v", got)
	}
	notReadyPlan := providerReviewAttemptAdapterTransactionPlan(operation, map[string]any{"claim_metadata_ready": false}, responsePlan)
	if notReadyPlan["transaction_metadata_ready"] != false {
		t.Fatalf("not ready transaction plan = %#v", notReadyPlan)
	}
	nilClaimPlan := providerReviewAttemptAdapterTransactionPlan(operation, nil, responsePlan)
	if nilClaimPlan["transaction_metadata_ready"] != false {
		t.Fatalf("nil claim transaction plan = %#v", nilClaimPlan)
	}
	mismatchedClaimPlan := providerReviewAttemptAdapterTransactionPlan(operation, map[string]any{
		"mode":                 "redacted_attempt_execution_claim_plan",
		"operation_name":       "commit_starter_files",
		"endpoint_key":         "github.commit_files",
		"claim_metadata_ready": true,
	}, responsePlan)
	if mismatchedClaimPlan["transaction_metadata_ready"] != false {
		t.Fatalf("mismatched claim identity transaction plan should not be metadata-ready: %#v", mismatchedClaimPlan)
	}
	mismatchedClaimBoundaryPlan := mapFromAny(mismatchedClaimPlan["provider_call_boundary_plan"])
	if mismatchedClaimBoundaryPlan["provider_call_boundary_metadata_ready"] != false {
		t.Fatalf("mismatched claim identity boundary plan should not be metadata-ready: %#v", mismatchedClaimBoundaryPlan)
	}
	mismatchedResponseModePlan := providerReviewAttemptAdapterTransactionPlan(operation, claimPlan, map[string]any{"mode": "raw_response_plan"})
	if mismatchedResponseModePlan["transaction_metadata_ready"] != false {
		t.Fatalf("mismatched response mode transaction plan = %#v", mismatchedResponseModePlan)
	}
	mismatchedResponseModeBoundaryPlan := mapFromAny(mismatchedResponseModePlan["provider_call_boundary_plan"])
	if mismatchedResponseModeBoundaryPlan["provider_call_boundary_metadata_ready"] != false {
		t.Fatalf("mismatched response mode boundary plan = %#v", mismatchedResponseModeBoundaryPlan)
	}
	invalidResponseContractBoundaryPlan := providerReviewAttemptAdapterProviderCallBoundaryPlan(operation, claimPlan, map[string]any{
		"mode":                         providerReviewAttemptAdapterResponsePlanMode,
		"operation_name":               "create_branch_ref",
		"endpoint_key":                 "github.create_branch_ref",
		"success_attempt_status":       "completed",
		"retry_attempt_status":         "planned",
		"failure_attempt_status":       "failed",
		"dependency_unlocks_operation": "commit_starter_files",
		"dependency_update_status":     "independent",
		"requires_dependency_update":   true,
	})
	if invalidResponseContractBoundaryPlan["provider_call_boundary_metadata_ready"] != false {
		t.Fatalf("invalid response contract boundary plan should not be metadata-ready: %#v", invalidResponseContractBoundaryPlan)
	}
	mismatchedResponseIdentityPlan := providerReviewAttemptAdapterTransactionPlan(operation, claimPlan, map[string]any{
		"mode":                         "redacted_attempt_adapter_response_plan",
		"operation_name":               "commit_starter_files",
		"endpoint_key":                 "github.commit_files",
		"success_attempt_status":       "completed",
		"retry_attempt_status":         "planned",
		"failure_attempt_status":       "failed",
		"dependency_unlocks_operation": "open_review_request",
		"dependency_update_status":     "dependency_satisfied",
		"requires_dependency_update":   true,
	})
	if mismatchedResponseIdentityPlan["transaction_metadata_ready"] != false {
		t.Fatalf("mismatched response identity transaction plan should not be metadata-ready: %#v", mismatchedResponseIdentityPlan)
	}
	mismatchedResponseIdentityBoundaryPlan := mapFromAny(mismatchedResponseIdentityPlan["provider_call_boundary_plan"])
	if mismatchedResponseIdentityBoundaryPlan["provider_call_boundary_metadata_ready"] != false {
		t.Fatalf("mismatched response identity boundary plan = %#v", mismatchedResponseIdentityBoundaryPlan)
	}
	redactedPlan := providerReviewAttemptAdapterTransactionPlan(operation, claimPlan, map[string]any{
		"mode":                         "redacted_attempt_adapter_response_plan",
		"operation_name":               "create_branch_ref",
		"endpoint_key":                 "github.create_branch_ref",
		"success_attempt_status":       "raw-success-secret",
		"retry_attempt_status":         "raw-retry-secret",
		"failure_attempt_status":       "raw-failure-secret",
		"dependency_unlocks_operation": "raw-operation-secret",
		"dependency_update_status":     "raw-dependency-secret",
	})
	if redactedPlan["transaction_metadata_ready"] != false ||
		redactedPlan["success_attempt_status"] != "blocked" ||
		redactedPlan["retry_attempt_status"] != "blocked" ||
		redactedPlan["failure_attempt_status"] != "blocked" ||
		redactedPlan["dependency_unlocks_operation"] != "" ||
		redactedPlan["dependency_update_status"] != "blocked" {
		t.Fatalf("transaction plan should redact raw response values: %#v", redactedPlan)
	}
	redactedBoundaryPlan := mapFromAny(redactedPlan["provider_call_boundary_plan"])
	if redactedBoundaryPlan["provider_call_boundary_metadata_ready"] != false {
		t.Fatalf("transaction boundary should reject raw response contract: %#v", redactedBoundaryPlan)
	}
	encoded, _ = json.Marshal(redactedPlan)
	for _, leak := range []string{"raw-success-secret", "raw-retry-secret", "raw-failure-secret", "raw-operation-secret", "raw-dependency-secret"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("transaction plan leaked raw response value %q: %s", leak, encoded)
		}
	}
}

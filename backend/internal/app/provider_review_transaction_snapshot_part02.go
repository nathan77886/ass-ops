package app

import (
	"fmt"
)

func safeProviderReviewSnapshotMutationState(values ...string) string {
	for _, value := range values {
		cleaned := cleanOptionalText(value)
		if cleaned != "" && cleaned != "disabled" {
			return "enabled"
		}
	}
	return "disabled"
}

func providerReviewAttemptTransactionSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "transaction_blocked"
	if boolOnlyFromAny(snapshot["transaction_metadata_ready"]) && boolOnlyFromAny(snapshot["provider_call_boundary_metadata_ready"]) {
		state = "transaction_metadata_ready"
	}
	if !boolOnlyFromAny(snapshot["provider_review_attempt_asset_observed"]) {
		missing = append(missing, "provider_review_attempt_asset_missing")
	}
	if cleanOptionalID(fmt.Sprint(snapshot["provider_review_attempt_id"])) == "" {
		missing = append(missing, "provider_review_attempt_id_missing")
	}
	if !boolOnlyFromAny(snapshot["candidate_observed"]) {
		missing = append(missing, "provider_review_execution_candidate_missing")
	}
	if !boolOnlyFromAny(snapshot["candidate_matches_attempt"]) {
		missing = append(missing, "provider_review_attempt_not_current_candidate")
	}
	if !boolOnlyFromAny(snapshot["transaction_plan_observed"]) {
		missing = append(missing, "provider_review_transaction_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["provider_call_boundary_plan_observed"]) {
		missing = append(missing, "provider_review_provider_call_boundary_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["transaction_metadata_ready"]) {
		missing = append(missing, "provider_review_transaction_metadata_not_ready")
	}
	if !boolOnlyFromAny(snapshot["provider_call_boundary_metadata_ready"]) {
		missing = append(missing, "provider_review_provider_call_boundary_metadata_not_ready")
	}
	if len(stringSliceFromAny(snapshot["transaction_sequence"])) == 0 {
		missing = append(missing, "provider_review_transaction_sequence_missing")
	}
	if len(stringSliceFromAny(snapshot["provider_call_boundary_sequence"])) == 0 {
		missing = append(missing, "provider_review_provider_call_boundary_sequence_missing")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["transaction_opened"]) ||
		boolOnlyFromAny(snapshot["transaction_recorded"]) ||
		boolOnlyFromAny(snapshot["attempt_claim_verified"]) ||
		boolOnlyFromAny(snapshot["idempotency_claim_verified"]) ||
		boolOnlyFromAny(snapshot["provider_call_boundary_opened"]) ||
		boolOnlyFromAny(snapshot["provider_call_boundary_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_call_started_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_call_finished_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["provider_response_received"]) ||
		boolOnlyFromAny(snapshot["provider_request_id_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_response_status_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_response_body_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_response_headers_recorded"]) ||
		boolOnlyFromAny(snapshot["response_recorded"]) ||
		boolOnlyFromAny(snapshot["dependency_update_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_request_id_included"]) ||
		boolOnlyFromAny(snapshot["response_body_included"]) ||
		boolOnlyFromAny(snapshot["headers_included"]) ||
		boolOnlyFromAny(snapshot["contains_token"]) ||
		boolOnlyFromAny(snapshot["contains_provider_url"]) ||
		boolOnlyFromAny(snapshot["contains_repository_ref"]) ||
		boolOnlyFromAny(snapshot["contains_branch_name"]) ||
		boolOnlyFromAny(snapshot["contains_file_content"]) {
		missing = append(missing, "provider_review_transaction_not_no_call")
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptTransactionSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "transaction_metadata_ready":
		return "provider_review_attempt_transaction_metadata_ready", "low"
	case "transaction_blocked":
		return "provider_review_attempt_transaction_blocked", "warning"
	default:
		return "provider_review_attempt_transaction_unknown", "warning"
	}
}

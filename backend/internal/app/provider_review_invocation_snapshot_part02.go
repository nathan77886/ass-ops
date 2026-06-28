package app

import (
	"fmt"
)

func safeProviderReviewInvocationReadyReason(value string) string {
	switch cleanOptionalText(value) {
	case "provider_api_invocation_not_armed",
		"provider_review_claim_metadata_not_ready",
		"provider_review_execution_lock_not_acquired",
		"provider_review_adapter_activation_not_armed",
		"provider_credential_runtime_binding_not_armed",
		"provider_review_adapter_runtime_not_bound",
		"provider_branch_policy_not_armed",
		"provider_request_not_materialized",
		"provider_api_call_not_made",
		"provider_review_transaction_not_recorded",
		"provider_review_adapter_not_implemented",
		"provider_review_mutation_not_armed":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func providerReviewAttemptInvocationSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "invocation_blocked"
	if boolOnlyFromAny(snapshot["invocation_contract_ready"]) {
		state = "invocation_contract_ready"
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
	if !boolOnlyFromAny(snapshot["invocation_plan_observed"]) {
		missing = append(missing, "provider_review_invocation_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["invocation_contract_ready"]) {
		missing = append(missing, "provider_review_invocation_contract_not_ready")
	}
	if len(stringSliceFromAny(snapshot["required_subplans"])) == 0 {
		missing = append(missing, "provider_review_invocation_required_subplans_missing")
	}
	if len(stringSliceFromAny(snapshot["invocation_sequence"])) == 0 {
		missing = append(missing, "provider_review_invocation_sequence_missing")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["attempt_claim_recorded"]) ||
		boolOnlyFromAny(snapshot["idempotency_claim_recorded"]) ||
		boolOnlyFromAny(snapshot["execution_lock_acquired"]) ||
		boolOnlyFromAny(snapshot["adapter_activation_approved"]) ||
		boolOnlyFromAny(snapshot["duplicate_send_detected"]) ||
		boolOnlyFromAny(snapshot["credential_bound"]) ||
		boolOnlyFromAny(snapshot["adapter_runtime_bound"]) ||
		boolOnlyFromAny(snapshot["branch_policy_verified"]) ||
		boolOnlyFromAny(snapshot["request_materialized"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["response_recorded"]) ||
		boolOnlyFromAny(snapshot["transaction_recorded"]) ||
		boolOnlyFromAny(snapshot["dependency_update_recorded"]) ||
		boolOnlyFromAny(snapshot["adapter_implemented"]) ||
		boolOnlyFromAny(snapshot["request_body_included"]) ||
		boolOnlyFromAny(snapshot["response_body_included"]) ||
		boolOnlyFromAny(snapshot["headers_included"]) ||
		boolOnlyFromAny(snapshot["authorization_header_included"]) ||
		boolOnlyFromAny(snapshot["provider_url_included"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_included"]) ||
		boolOnlyFromAny(snapshot["provider_request_id_included"]) ||
		boolOnlyFromAny(snapshot["contains_token"]) ||
		boolOnlyFromAny(snapshot["contains_provider_url"]) ||
		boolOnlyFromAny(snapshot["contains_repository_ref"]) ||
		boolOnlyFromAny(snapshot["contains_branch_name"]) ||
		boolOnlyFromAny(snapshot["contains_file_content"]) {
		missing = append(missing, "provider_review_invocation_not_no_call")
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptInvocationSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "invocation_contract_ready":
		return "provider_review_attempt_invocation_contract_ready", "low"
	case "invocation_blocked":
		return "provider_review_attempt_invocation_blocked", "warning"
	default:
		return "provider_review_attempt_invocation_unknown", "warning"
	}
}

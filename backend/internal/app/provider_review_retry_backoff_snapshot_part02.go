package app

import (
	"fmt"
)

func providerReviewAttemptRetryBackoffSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "retry_backoff_blocked"
	if boolOnlyFromAny(snapshot["retry_backoff_metadata_ready"]) {
		state = "retry_backoff_metadata_ready"
	}
	for _, item := range []struct {
		field  string
		reason string
	}{
		{"provider_review_attempt_asset_observed", "provider_review_attempt_asset_missing"},
		{"candidate_observed", "provider_review_execution_candidate_missing"},
		{"candidate_matches_attempt", "provider_review_attempt_not_current_candidate"},
		{"provider_send_plan_observed", "provider_review_provider_send_plan_missing"},
		{"retry_backoff_plan_observed", "provider_review_retry_backoff_plan_missing"},
		{"retry_backoff_metadata_ready", "provider_review_retry_backoff_metadata_not_ready"},
	} {
		if !boolOnlyFromAny(snapshot[item.field]) {
			missing = append(missing, item.reason)
		}
	}
	if cleanOptionalID(fmt.Sprint(snapshot["provider_review_attempt_id"])) == "" {
		missing = append(missing, "provider_review_attempt_id_missing")
	}
	for _, item := range []struct {
		field  string
		reason string
	}{
		{"retryable_status_classes", "provider_review_retryable_status_classes_missing"},
		{"transport_retryable_status_classes", "provider_review_transport_retryable_status_classes_missing"},
		{"retry_backoff_sequence", "provider_review_retry_backoff_sequence_missing"},
		{"retry_backoff_suppressed_fields", "provider_review_retry_backoff_suppressed_fields_missing"},
	} {
		if len(stringSliceFromAny(snapshot[item.field])) == 0 {
			missing = append(missing, item.reason)
		}
	}
	if safeProviderReviewRetryPolicy(stringFromMap(snapshot, "retry_policy")) == "" {
		missing = append(missing, "provider_review_retry_policy_missing")
	}
	if intFromAny(snapshot["max_attempts"], 0) <= 0 {
		missing = append(missing, "provider_review_retry_budget_missing")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["provider_response_received"]) ||
		boolOnlyFromAny(snapshot["retry_scheduled"]) ||
		boolOnlyFromAny(snapshot["retry_attempt_recorded"]) ||
		boolOnlyFromAny(snapshot["retry_after_value_recorded"]) ||
		boolOnlyFromAny(snapshot["retry_after_header_included"]) ||
		boolOnlyFromAny(snapshot["provider_rate_limit_value_included"]) ||
		boolOnlyFromAny(snapshot["provider_error_code_included"]) ||
		boolOnlyFromAny(snapshot["request_body_included"]) ||
		boolOnlyFromAny(snapshot["response_body_included"]) ||
		boolOnlyFromAny(snapshot["headers_included"]) ||
		boolOnlyFromAny(snapshot["authorization_header_included"]) ||
		boolOnlyFromAny(snapshot["provider_url_included"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_included"]) ||
		boolOnlyFromAny(snapshot["contains_token"]) ||
		boolOnlyFromAny(snapshot["contains_provider_url"]) ||
		boolOnlyFromAny(snapshot["contains_repository_ref"]) ||
		boolOnlyFromAny(snapshot["contains_branch_name"]) ||
		boolOnlyFromAny(snapshot["contains_file_content"]) {
		missing = append(missing, "provider_review_retry_backoff_not_no_call")
	}
	if len(missing) > 0 {
		state = "retry_backoff_blocked"
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptRetryBackoffSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "retry_backoff_metadata_ready":
		return "provider_review_attempt_retry_backoff_metadata_ready", "low"
	case "retry_backoff_blocked":
		return "provider_review_attempt_retry_backoff_blocked", "warning"
	default:
		return "provider_review_attempt_retry_backoff_unknown", "warning"
	}
}

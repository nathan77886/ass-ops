package app

import (
	"fmt"
)

func providerReviewAttemptIdempotencySnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "idempotency_blocked"
	if boolOnlyFromAny(snapshot["idempotency_metadata_ready"]) {
		state = "idempotency_metadata_ready"
	}
	for _, item := range []struct {
		field  string
		reason string
	}{
		{"provider_review_attempt_asset_observed", "provider_review_attempt_asset_missing"},
		{"candidate_observed", "provider_review_execution_candidate_missing"},
		{"candidate_matches_attempt", "provider_review_attempt_not_current_candidate"},
		{"claim_plan_observed", "provider_review_claim_plan_missing"},
		{"claim_plan_matches_attempt", "provider_review_claim_plan_not_current_attempt"},
		{"request_summary_observed", "provider_review_request_summary_missing"},
		{"request_summary_matches_attempt", "provider_review_request_summary_not_current_attempt"},
		{"idempotency_metadata_ready", "provider_review_idempotency_metadata_not_ready"},
		{"requires_idempotency_ledger", "provider_review_idempotency_ledger_requirement_missing"},
	} {
		if !boolOnlyFromAny(snapshot[item.field]) {
			missing = append(missing, item.reason)
		}
	}
	if cleanOptionalID(fmt.Sprint(snapshot["provider_review_attempt_id"])) == "" {
		missing = append(missing, "provider_review_attempt_id_missing")
	}
	if cleanOptionalText(stringFromMap(snapshot, "idempotency_key_kind")) != "operation_scope_hash" {
		missing = append(missing, "provider_review_idempotency_key_kind_missing")
	}
	if safeProviderReviewReplayCheck(stringFromMap(snapshot, "replay_check")) == "" {
		missing = append(missing, "provider_review_replay_check_missing")
	}
	if safeProviderReviewConflictPolicy(stringFromMap(snapshot, "conflict_policy")) == "" {
		missing = append(missing, "provider_review_conflict_policy_missing")
	}
	if safeProviderReviewRetryPolicy(stringFromMap(snapshot, "retry_policy")) == "" {
		missing = append(missing, "provider_review_retry_policy_missing")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["provider_response_received"]) ||
		boolOnlyFromAny(snapshot["idempotency_claim_recorded"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_included"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_materialized"]) ||
		boolOnlyFromAny(snapshot["request_body_included"]) ||
		boolOnlyFromAny(snapshot["response_body_included"]) ||
		boolOnlyFromAny(snapshot["headers_included"]) ||
		boolOnlyFromAny(snapshot["provider_url_included"]) ||
		boolOnlyFromAny(snapshot["provider_request_id_included"]) ||
		boolOnlyFromAny(snapshot["contains_token"]) ||
		boolOnlyFromAny(snapshot["contains_provider_url"]) ||
		boolOnlyFromAny(snapshot["contains_repository_ref"]) ||
		boolOnlyFromAny(snapshot["contains_branch_name"]) ||
		boolOnlyFromAny(snapshot["contains_file_content"]) {
		missing = append(missing, "provider_review_idempotency_not_no_call")
	}
	if len(missing) > 0 {
		state = "idempotency_blocked"
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptIdempotencySnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "idempotency_metadata_ready":
		return "provider_review_attempt_idempotency_metadata_ready", "low"
	case "idempotency_blocked":
		return "provider_review_attempt_idempotency_blocked", "warning"
	default:
		return "provider_review_attempt_idempotency_unknown", "warning"
	}
}

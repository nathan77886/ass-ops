package app

import (
	"fmt"
)

func providerReviewAttemptResultRecordingSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "result_recording_blocked"
	if boolOnlyFromAny(snapshot["response_metadata_ready"]) && boolOnlyFromAny(snapshot["result_recording_metadata_ready"]) {
		state = "result_recording_metadata_ready"
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
	if !boolOnlyFromAny(snapshot["response_plan_observed"]) {
		missing = append(missing, "provider_review_response_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["result_recording_plan_observed"]) {
		missing = append(missing, "provider_review_result_recording_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["response_metadata_ready"]) {
		missing = append(missing, "provider_review_response_metadata_not_ready")
	}
	if !boolOnlyFromAny(snapshot["result_recording_metadata_ready"]) {
		missing = append(missing, "provider_review_result_recording_metadata_not_ready")
	}
	if len(stringSliceFromAny(snapshot["result_recording_sequence"])) == 0 {
		missing = append(missing, "provider_review_result_recording_sequence_missing")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["provider_response_received"]) ||
		boolOnlyFromAny(snapshot["response_recorded"]) ||
		boolOnlyFromAny(snapshot["result_recorded"]) ||
		boolOnlyFromAny(snapshot["response_classified"]) ||
		boolOnlyFromAny(snapshot["attempt_status_mapped"]) ||
		boolOnlyFromAny(snapshot["attempt_result_persisted"]) ||
		boolOnlyFromAny(snapshot["dependency_update_staged"]) ||
		boolOnlyFromAny(snapshot["dependency_update_recorded"]) ||
		boolOnlyFromAny(snapshot["transaction_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_request_id_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_response_status_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_response_body_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_response_headers_recorded"]) ||
		boolOnlyFromAny(snapshot["response_body_included"]) ||
		boolOnlyFromAny(snapshot["headers_included"]) ||
		boolOnlyFromAny(snapshot["provider_url_included"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_included"]) ||
		boolOnlyFromAny(snapshot["provider_request_id_included"]) ||
		boolOnlyFromAny(snapshot["provider_response_status_included"]) ||
		boolOnlyFromAny(snapshot["contains_token"]) ||
		boolOnlyFromAny(snapshot["contains_provider_url"]) ||
		boolOnlyFromAny(snapshot["contains_repository_ref"]) ||
		boolOnlyFromAny(snapshot["contains_branch_name"]) ||
		boolOnlyFromAny(snapshot["contains_file_content"]) {
		missing = append(missing, "provider_review_result_recording_not_no_call")
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptResultRecordingSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "result_recording_metadata_ready":
		return "provider_review_attempt_result_recording_metadata_ready", "low"
	case "result_recording_blocked":
		return "provider_review_attempt_result_recording_blocked", "warning"
	default:
		return "provider_review_attempt_result_recording_unknown", "warning"
	}
}

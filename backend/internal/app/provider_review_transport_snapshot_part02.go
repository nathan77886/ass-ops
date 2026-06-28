package app

import (
	"fmt"
)

func providerReviewAttemptTransportSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "transport_blocked"
	if boolOnlyFromAny(snapshot["transport_metadata_ready"]) {
		state = "transport_metadata_ready"
	}
	for _, item := range []struct {
		field  string
		reason string
	}{
		{"provider_review_attempt_asset_observed", "provider_review_attempt_asset_missing"},
		{"candidate_observed", "provider_review_execution_candidate_missing"},
		{"candidate_matches_attempt", "provider_review_attempt_not_current_candidate"},
		{"transport_plan_observed", "provider_review_transport_plan_missing"},
		{"transport_metadata_ready", "provider_review_transport_metadata_not_ready"},
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
		{"method", "provider_review_transport_method_missing"},
		{"payload_shape", "provider_review_transport_payload_shape_missing"},
		{"auth_scheme", "provider_review_transport_auth_scheme_missing"},
		{"accept_header", "provider_review_transport_accept_header_missing"},
		{"content_type", "provider_review_transport_content_type_missing"},
	} {
		if cleanOptionalText(stringFromMap(snapshot, item.field)) == "" {
			missing = append(missing, item.reason)
		}
	}
	if intFromAny(snapshot["timeout_seconds"], 0) <= 0 {
		missing = append(missing, "provider_review_transport_timeout_missing")
	}
	for _, item := range []struct {
		field  string
		reason string
	}{
		{"expected_success_classes", "provider_review_transport_success_classes_missing"},
		{"retryable_status_classes", "provider_review_transport_retryable_classes_missing"},
		{"diagnostic_fields", "provider_review_transport_diagnostic_fields_missing"},
	} {
		if len(stringSliceFromAny(snapshot[item.field])) == 0 {
			missing = append(missing, item.reason)
		}
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["provider_client_bound"]) ||
		boolOnlyFromAny(snapshot["credential_bound"]) ||
		boolOnlyFromAny(snapshot["runtime_bound"]) ||
		boolOnlyFromAny(snapshot["request_path_materialized"]) ||
		boolOnlyFromAny(snapshot["request_url_materialized"]) ||
		boolOnlyFromAny(snapshot["request_body_materialized"]) ||
		boolOnlyFromAny(snapshot["headers_materialized"]) ||
		boolOnlyFromAny(snapshot["authorization_header_materialized"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["provider_response_received"]) ||
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
		missing = append(missing, "provider_review_transport_not_no_call")
	}
	if len(missing) > 0 {
		state = "transport_blocked"
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptTransportSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "transport_metadata_ready":
		return "provider_review_attempt_transport_metadata_ready", "low"
	case "transport_blocked":
		return "provider_review_attempt_transport_blocked", "warning"
	default:
		return "provider_review_attempt_transport_unknown", "warning"
	}
}

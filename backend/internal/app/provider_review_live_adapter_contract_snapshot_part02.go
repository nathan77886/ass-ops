package app

import (
	"fmt"
)

func providerReviewAttemptLiveAdapterContractSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "live_adapter_contract_blocked"
	if boolOnlyFromAny(snapshot["live_adapter_contract_metadata_ready"]) {
		state = "live_adapter_contract_metadata_ready"
	}
	for _, item := range []struct {
		field  string
		reason string
	}{
		{"provider_review_attempt_asset_observed", "provider_review_attempt_asset_missing"},
		{"candidate_observed", "provider_review_execution_candidate_missing"},
		{"candidate_matches_attempt", "provider_review_attempt_not_current_candidate"},
		{"live_adapter_plan_observed", "provider_review_live_adapter_plan_missing"},
		{"live_adapter_contract_plan_observed", "provider_review_live_adapter_contract_plan_missing"},
		{"live_adapter_contract_metadata_ready", "provider_review_live_adapter_contract_metadata_not_ready"},
		{"contract_registered", "provider_review_live_adapter_contract_not_registered"},
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
		{"contract_input_fields", "provider_review_live_adapter_contract_inputs_missing"},
		{"contract_output_fields", "provider_review_live_adapter_contract_outputs_missing"},
		{"contract_error_classes", "provider_review_live_adapter_contract_errors_missing"},
		{"contract_persisted_fields", "provider_review_live_adapter_contract_persisted_fields_missing"},
		{"contract_suppressed_fields", "provider_review_live_adapter_contract_suppressed_fields_missing"},
		{"contract_sequence", "provider_review_live_adapter_contract_sequence_missing"},
		{"required_capabilities", "provider_review_live_adapter_contract_capabilities_missing"},
	} {
		if len(stringSliceFromAny(snapshot[item.field])) == 0 {
			missing = append(missing, item.reason)
		}
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["live_adapter_implemented"]) ||
		boolOnlyFromAny(snapshot["contract_implemented"]) ||
		boolOnlyFromAny(snapshot["request_contract_materialized"]) ||
		boolOnlyFromAny(snapshot["response_contract_materialized"]) ||
		boolOnlyFromAny(snapshot["error_contract_materialized"]) ||
		boolOnlyFromAny(snapshot["result_contract_materialized"]) ||
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
		missing = append(missing, "provider_review_live_adapter_contract_not_no_call")
	}
	if len(missing) > 0 {
		state = "live_adapter_contract_blocked"
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptLiveAdapterContractSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "live_adapter_contract_metadata_ready":
		return "provider_review_attempt_live_adapter_contract_metadata_ready", "low"
	case "live_adapter_contract_blocked":
		return "provider_review_attempt_live_adapter_contract_blocked", "warning"
	default:
		return "provider_review_attempt_live_adapter_contract_unknown", "warning"
	}
}

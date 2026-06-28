package app

import (
	"fmt"
)

func providerReviewAttemptAdapterRehearsalBlockedReason(evidence string) string {
	switch cleanOptionalText(evidence) {
	case "attempt_claim_metadata":
		return "provider_review_claim_metadata"
	case "adapter_contract":
		return "provider_review_adapter_contract"
	case "request_materialization":
		return "provider_review_request_materialization"
	case "branch_policy":
		return "provider_review_branch_policy"
	case "credential_binding":
		return "provider_review_credential_binding"
	case "adapter_runtime":
		return "provider_review_adapter_runtime"
	case "transport_metadata":
		return "provider_review_transport_metadata"
	case "response_recording":
		return "provider_review_response_recording"
	case "transaction_boundary":
		return "provider_review_transaction_boundary"
	default:
		return "provider_review_adapter_rehearsal"
	}
}

func providerReviewAttemptAdapterRehearsalMutationArmingPlanForSnapshot(rehearsal map[string]any) map[string]any {
	rehearsalReady := cleanOptionalText(stringFromMap(rehearsal, "status")) == "ready" && boolOnlyFromAny(rehearsal["mutation_arming_candidate"])
	status := "blocked"
	if rehearsalReady {
		status = "ready_to_arm"
	}
	return map[string]any{
		"mode":                           "redacted_mutation_arming_plan",
		"status":                         status,
		"provider_type":                  safeProviderReviewProviderType(stringFromMap(rehearsal, "provider_type")),
		"review_kind":                    cleanOptionalText(stringFromMap(rehearsal, "review_kind")),
		"execution_enabled_config":       false,
		"adapter_rehearsal_ready":        rehearsalReady,
		"mutation_armed_config":          false,
		"mutation_armed":                 false,
		"blocked_reasons":                []string{"provider_review_execution_enabled", "provider_review_mutation_armed"},
		"external_call_made":             false,
		"provider_api_call_made":         false,
		"provider_api_mutation":          "disabled",
		"contains_token":                 false,
		"contains_provider_url":          false,
		"contains_repository_ref":        false,
		"contains_file_content":          false,
		"requires_operator_review":       true,
		"requires_adapter_rehearsal":     true,
		"adapter_mutation_currently_off": true,
	}
}

func providerReviewAttemptAdapterRehearsalSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "adapter_rehearsal_blocked"
	if boolOnlyFromAny(snapshot["adapter_rehearsal_contract_ready"]) {
		state = "adapter_rehearsal_contract_ready"
	}
	if boolOnlyFromAny(snapshot["adapter_rehearsal_ready"]) && boolOnlyFromAny(snapshot["mutation_arming_candidate"]) {
		state = "adapter_rehearsal_ready"
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
	if !boolOnlyFromAny(snapshot["adapter_rehearsal_observed"]) {
		missing = append(missing, "provider_review_adapter_rehearsal_missing")
	}
	if !boolOnlyFromAny(snapshot["adapter_rehearsal_contract_ready"]) {
		missing = append(missing, "provider_review_adapter_rehearsal_contract_not_ready")
	}
	if !boolOnlyFromAny(snapshot["mutation_arming_contract_ready"]) {
		missing = append(missing, "provider_review_mutation_arming_contract_not_ready")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["live_adapter_implemented"]) ||
		boolOnlyFromAny(snapshot["provider_client_constructed"]) ||
		boolOnlyFromAny(snapshot["request_body_materialized"]) ||
		boolOnlyFromAny(snapshot["response_body_materialized"]) ||
		boolOnlyFromAny(snapshot["headers_materialized"]) ||
		boolOnlyFromAny(snapshot["provider_url_included"]) ||
		boolOnlyFromAny(snapshot["authorization_header_included"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_included"]) {
		missing = append(missing, "provider_review_adapter_rehearsal_not_no_call")
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptAdapterRehearsalSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "adapter_rehearsal_ready":
		return "provider_review_attempt_adapter_rehearsal_ready", "low"
	case "adapter_rehearsal_contract_ready":
		return "provider_review_attempt_adapter_rehearsal_contract_ready", "low"
	case "adapter_rehearsal_blocked":
		return "provider_review_attempt_adapter_rehearsal_blocked", "warning"
	default:
		return "provider_review_attempt_adapter_rehearsal_unknown", "warning"
	}
}

func safeProviderReviewAttemptRehearsalOperations(values []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(values))
	for _, value := range values {
		name := safeProviderReviewAttemptOperationName(stringFromMap(value, "name"))
		endpointKey := safeProviderReviewEndpointKey(stringFromMap(value, "endpoint_key"))
		if name == "" || endpointKey == "" {
			continue
		}
		out = append(out, map[string]any{
			"name":                    name,
			"endpoint_key":            endpointKey,
			"status":                  cleanOptionalText(stringFromMap(value, "status")),
			"blocked_reasons":         safeProviderReviewBlockedReasons(stringSliceFromAny(value["blocked_reasons"])),
			"external_call_made":      false,
			"provider_api_call_made":  false,
			"provider_api_mutation":   "disabled",
			"contains_token":          false,
			"contains_provider_url":   false,
			"contains_repository_ref": false,
			"contains_branch_name":    false,
			"contains_file_content":   false,
		})
	}
	return out
}

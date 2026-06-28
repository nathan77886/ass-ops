package app

import (
	"fmt"
)

func providerReviewAttemptAdapterBlueprintSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "adapter_blueprint_blocked"
	if boolOnlyFromAny(snapshot["invocation_contract_ready"]) {
		state = "adapter_blueprint_contract_ready"
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
	for _, item := range []struct {
		field  string
		reason string
	}{
		{"dispatch_plan_observed", "provider_review_dispatch_plan_missing"},
		{"invocation_plan_observed", "provider_review_invocation_plan_missing"},
		{"adapter_activation_plan_observed", "provider_review_activation_plan_missing"},
		{"live_adapter_plan_observed", "provider_review_live_adapter_plan_missing"},
		{"provider_send_plan_observed", "provider_review_provider_send_plan_missing"},
		{"transaction_plan_observed", "provider_review_transaction_plan_missing"},
		{"provider_call_boundary_plan_observed", "provider_review_provider_call_boundary_plan_missing"},
		{"dispatch_contract_ready", "provider_review_dispatch_contract_not_ready"},
		{"invocation_contract_ready", "provider_review_invocation_contract_not_ready"},
		{"adapter_activation_contract_ready", "provider_review_activation_contract_not_ready"},
		{"live_adapter_contract_ready", "provider_review_live_adapter_contract_not_ready"},
		{"provider_send_contract_ready", "provider_review_provider_send_contract_not_ready"},
		{"retry_backoff_contract_ready", "provider_review_retry_backoff_contract_not_ready"},
		{"transaction_contract_ready", "provider_review_transaction_contract_not_ready"},
		{"provider_call_boundary_contract_ready", "provider_review_provider_call_boundary_contract_not_ready"},
	} {
		if !boolOnlyFromAny(snapshot[item.field]) {
			missing = append(missing, item.reason)
		}
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["adapter_implemented"]) ||
		boolOnlyFromAny(snapshot["live_adapter_implemented"]) ||
		boolOnlyFromAny(snapshot["provider_client_bound"]) ||
		boolOnlyFromAny(snapshot["request_materialized"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["provider_response_received"]) ||
		boolOnlyFromAny(snapshot["transaction_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_call_boundary_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_url_included"]) ||
		boolOnlyFromAny(snapshot["authorization_header_included"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_included"]) ||
		boolOnlyFromAny(snapshot["provider_request_id_included"]) {
		missing = append(missing, "provider_review_adapter_blueprint_not_no_call")
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptAdapterBlueprintSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "adapter_blueprint_contract_ready":
		return "provider_review_attempt_adapter_blueprint_contract_ready", "low"
	case "adapter_blueprint_blocked":
		return "provider_review_attempt_adapter_blueprint_blocked", "warning"
	default:
		return "provider_review_attempt_adapter_blueprint_unknown", "warning"
	}
}

func safeProviderReviewBlueprintNames(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = cleanOptionalText(value)
		if value == "" || len(value) > 96 || seen[value] {
			continue
		}
		ok := true
		for _, r := range value {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptLiveExecutionPreflightOptions struct {
	AttemptID         string
	Attempt           map[string]any
	LiveGuardObserved bool
}

func ProviderReviewAttemptLiveExecutionPreflight(ctx context.Context, store *Store, opts ProviderReviewAttemptLiveExecutionPreflightOptions) (map[string]any, error) {
	if store == nil || store.DB == nil {
		return nil, fmt.Errorf("store is required")
	}
	attemptID := cleanOptionalID(opts.AttemptID)
	if attemptID == "" {
		return nil, fmt.Errorf("provider review attempt id is required")
	}
	attempt := opts.Attempt
	var err error
	if len(attempt) == 0 {
		attempt, err = providerReviewAttemptForActivationSnapshot(ctx, store, attemptID)
		if err != nil {
			return nil, err
		}
	}
	assetID, assetErr := providerReviewAttemptAssetID(ctx, store.DB, attemptID)
	assetObserved := assetErr == nil && assetID != ""
	liveGuardObserved := opts.LiveGuardObserved
	if assetObserved && !liveGuardObserved {
		liveGuardObserved, err = providerReviewAttemptStatusObserved(ctx, store, assetID, "provider_review_attempt_live_execution_guard_ready")
		if err != nil {
			return nil, err
		}
	}
	preflight := providerReviewAttemptLiveExecutionPreflightPayload(attempt, assetObserved, liveGuardObserved)
	ready, state, missing := providerReviewAttemptLiveExecutionPreflightReadiness(preflight)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_live_execution_preflight",
		"preflight_state":                        state,
		"preflight_ready":                        ready,
		"provider_review_attempt_id":             attemptID,
		"operation_approval_id":                  cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"project_template_run_id":                cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed": assetObserved,
		"preflight":                              preflight,
		"external_call_made":                     false,
		"provider_api_call_made":                 false,
		"provider_api_mutation":                  "disabled",
		"provider_request_sent":                  false,
		"provider_response_received":             false,
		"mutation_armed":                         false,
		"live_adapter_implemented":               false,
		"future_live_execution_still_blocked":    true,
		"operation_log_written":                  false,
		"asset_status_snapshot_written":          false,
		"contains_token":                         false,
		"contains_provider_url":                  false,
		"contains_repository_ref":                false,
		"contains_branch_name":                   false,
		"contains_file_content":                  false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if !ready {
		result["message"] = "Provider review live execution preflight is blocked; live adapter implementation, mutation arming, and provider send remain disabled."
		return result, nil
	}
	result["message"] = "Provider review live execution preflight is ready."
	return result, nil
}

func providerReviewAttemptLiveExecutionPreflightPayload(attempt map[string]any, assetObserved, liveGuardObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	attemptStatus := safeProviderReviewAttemptStatus(stringFromMap(attempt, "status"))
	claimRecorded := providerReviewAttemptClaimRecorded(attempt)
	return map[string]any{
		"mode":                                       "redacted_provider_review_live_execution_preflight",
		"provider_review_attempt_id":                 cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":                      cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"project_template_run_id":                    cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed":     assetObserved,
		"provider_type":                              safeProviderReviewProviderType(stringFromMap(attempt, "provider_type")),
		"review_kind":                                cleanOptionalText(stringFromMap(attempt, "review_kind")),
		"operation_name":                             operationName,
		"endpoint_key":                               endpointKey,
		"attempt_status":                             attemptStatus,
		"dependency_status":                          safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(attempt, "dependency_status")),
		"claim_recorded":                             claimRecorded,
		"live_execution_guard_observed":              liveGuardObserved,
		"live_execution_preflight_metadata_ready":    assetObserved && attemptStatus == "running" && claimRecorded && liveGuardObserved,
		"requires_attempt_running":                   true,
		"requires_attempt_claim":                     true,
		"requires_live_execution_guard":              true,
		"requires_live_adapter_implementation":       true,
		"requires_mutation_arming":                   true,
		"requires_provider_send":                     true,
		"live_adapter_implemented":                   false,
		"mutation_armed":                             false,
		"provider_send_armed":                        false,
		"provider_request_sent":                      false,
		"provider_response_received":                 false,
		"provider_api_call_made":                     false,
		"external_call_made":                         false,
		"provider_api_mutation":                      "disabled",
		"request_body_included":                      false,
		"response_body_included":                     false,
		"headers_included":                           false,
		"authorization_header_included":              false,
		"provider_url_included":                      false,
		"provider_request_id_included":               false,
		"idempotency_key_included":                   false,
		"contains_token":                             false,
		"contains_provider_url":                      false,
		"contains_repository_ref":                    false,
		"contains_branch_name":                       false,
		"contains_file_content":                      false,
		"future_live_execution_still_blocked":        true,
		"live_execution_preflight_boundary_redacted": true,
		"blocked_reasons": []string{
			"provider_review_live_adapter_not_implemented",
			"provider_review_mutation_not_armed",
			"provider_request_send_not_armed",
		},
	}
}

func providerReviewAttemptLiveExecutionPreflightReadiness(preflight map[string]any) (bool, string, []string) {
	missing := []string{}
	for _, item := range []struct {
		field  string
		reason string
	}{
		{"provider_review_attempt_asset_observed", "provider_review_attempt_asset_missing"},
		{"claim_recorded", "provider_review_attempt_claim_not_recorded"},
		{"live_execution_guard_observed", "provider_review_live_execution_guard_missing"},
		{"requires_attempt_running", "provider_review_attempt_running_requirement_missing"},
		{"requires_attempt_claim", "provider_review_attempt_claim_requirement_missing"},
		{"requires_live_execution_guard", "provider_review_live_execution_guard_requirement_missing"},
		{"requires_live_adapter_implementation", "provider_review_live_adapter_requirement_missing"},
		{"requires_mutation_arming", "provider_review_mutation_arming_requirement_missing"},
		{"requires_provider_send", "provider_review_provider_send_requirement_missing"},
		{"future_live_execution_still_blocked", "provider_review_live_execution_still_blocked_expected"},
	} {
		if !boolOnlyFromAny(preflight[item.field]) {
			missing = append(missing, item.reason)
		}
	}
	if stringFromMap(preflight, "attempt_status") != "running" {
		missing = append(missing, "provider_review_attempt_not_running")
	}
	if !boolOnlyFromAny(preflight["live_adapter_implemented"]) {
		missing = append(missing, "provider_review_live_adapter_not_implemented")
	}
	if !boolOnlyFromAny(preflight["mutation_armed"]) {
		missing = append(missing, "provider_review_mutation_not_armed")
	}
	if !boolOnlyFromAny(preflight["provider_send_armed"]) {
		missing = append(missing, "provider_request_send_not_armed")
	}
	if boolOnlyFromAny(preflight["provider_api_call_made"]) ||
		boolOnlyFromAny(preflight["external_call_made"]) ||
		stringFromMap(preflight, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(preflight["provider_request_sent"]) ||
		boolOnlyFromAny(preflight["provider_response_received"]) ||
		boolOnlyFromAny(preflight["request_body_included"]) ||
		boolOnlyFromAny(preflight["response_body_included"]) ||
		boolOnlyFromAny(preflight["headers_included"]) ||
		boolOnlyFromAny(preflight["authorization_header_included"]) ||
		boolOnlyFromAny(preflight["provider_url_included"]) ||
		boolOnlyFromAny(preflight["provider_request_id_included"]) ||
		boolOnlyFromAny(preflight["idempotency_key_included"]) ||
		boolOnlyFromAny(preflight["contains_token"]) ||
		boolOnlyFromAny(preflight["contains_provider_url"]) ||
		boolOnlyFromAny(preflight["contains_repository_ref"]) ||
		boolOnlyFromAny(preflight["contains_branch_name"]) ||
		boolOnlyFromAny(preflight["contains_file_content"]) {
		missing = append(missing, "provider_review_live_execution_preflight_not_no_call")
	}
	if len(missing) == 0 {
		return true, "live_execution_preflight_ready", nil
	}
	return false, "live_execution_preflight_blocked", missing
}

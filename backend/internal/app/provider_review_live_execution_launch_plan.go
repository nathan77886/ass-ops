package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptLiveExecutionLaunchPlanOptions struct {
	AttemptID         string
	Attempt           map[string]any
	LiveGuardObserved bool
}

func ProviderReviewAttemptLiveExecutionLaunchPlan(ctx context.Context, store *Store, opts ProviderReviewAttemptLiveExecutionLaunchPlanOptions) (map[string]any, error) {
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
	preflight, err := ProviderReviewAttemptLiveExecutionPreflight(ctx, store, ProviderReviewAttemptLiveExecutionPreflightOptions{
		AttemptID:         attemptID,
		Attempt:           attempt,
		LiveGuardObserved: opts.LiveGuardObserved,
	})
	if err != nil {
		return nil, err
	}
	launchPlan := providerReviewAttemptLiveExecutionLaunchPlanPayload(attempt, preflight)
	ready, state, missing := providerReviewAttemptLiveExecutionLaunchPlanReadiness(launchPlan)
	result := map[string]any{
		"mode":                                "provider_review_attempt_live_execution_launch_plan",
		"launch_plan_state":                   state,
		"launch_plan_ready":                   ready,
		"provider_review_attempt_id":          attemptID,
		"operation_approval_id":               cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"project_template_run_id":             cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"live_execution_preflight_ready":      boolOnlyFromAny(preflight["preflight_ready"]),
		"live_execution_preflight_state":      cleanOptionalText(stringFromMap(preflight, "preflight_state")),
		"launch_plan":                         launchPlan,
		"external_call_made":                  false,
		"provider_api_call_made":              false,
		"provider_api_mutation":               "disabled",
		"provider_request_materialized":       false,
		"provider_request_sent":               false,
		"provider_response_received":          false,
		"provider_client_constructed":         false,
		"live_adapter_invoked":                false,
		"execute_method_invoked":              false,
		"response_handler_invoked":            false,
		"transaction_recorded":                false,
		"operation_log_written":               false,
		"asset_status_snapshot_written":       false,
		"contains_token":                      false,
		"contains_provider_url":               false,
		"contains_repository_ref":             false,
		"contains_branch_name":                false,
		"contains_file_content":               false,
		"future_live_execution_still_blocked": true,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if !ready {
		result["message"] = "Provider review live execution launch plan is blocked; live adapter invocation, request materialization, provider send, response handling, and transaction recording remain disabled."
		return result, nil
	}
	result["message"] = "Provider review live execution launch plan is ready."
	return result, nil
}

func providerReviewAttemptLiveExecutionLaunchPlanPayload(attempt, preflight map[string]any) map[string]any {
	providerType := safeProviderReviewProviderType(stringFromMap(attempt, "provider_type"))
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	liveAdapterPlan := providerReviewAttemptLiveAdapterPlan(providerType, operationName, endpointKey)
	contractPlan := mapFromAny(liveAdapterPlan["contract_plan"])
	preflightPayload := mapFromAny(preflight["preflight"])
	ledgerAdapterPlan := reviewBranchAttemptLedgerAdapterPlanFromAttempt(attempt, preflight)
	preflightMetadataReady := boolOnlyFromAny(preflightPayload["live_execution_preflight_metadata_ready"])
	adapterPlanObserved := providerReviewAttemptPlanMatchesOperation(liveAdapterPlan, "redacted_attempt_live_adapter_plan", operationName, endpointKey)
	contractPlanObserved := providerReviewAttemptPlanMatchesOperation(contractPlan, "redacted_attempt_live_adapter_contract_plan", operationName, endpointKey)
	ledgerAdapterPlanObserved := reviewBranchAttemptLedgerAdapterPlanMatchesAttempt(ledgerAdapterPlan, operationName, endpointKey)
	return map[string]any{
		"mode":                                    "redacted_provider_review_live_execution_launch_plan",
		"provider_review_attempt_id":              cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":                   cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"project_template_run_id":                 cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_type":                           providerType,
		"operation_name":                          operationName,
		"endpoint_key":                            endpointKey,
		"preflight_state":                         cleanOptionalText(stringFromMap(preflight, "preflight_state")),
		"preflight_ready":                         boolOnlyFromAny(preflight["preflight_ready"]),
		"preflight_metadata_ready":                preflightMetadataReady,
		"live_adapter_plan_observed":              adapterPlanObserved,
		"live_adapter_contract_plan_observed":     contractPlanObserved,
		"review_branch_ledger_adapter_plan":       ledgerAdapterPlan,
		"review_branch_ledger_adapter_observed":   ledgerAdapterPlanObserved,
		"adapter_name":                            cleanOptionalText(stringFromMap(liveAdapterPlan, "adapter_name")),
		"adapter_interface_registered":            boolOnlyFromAny(liveAdapterPlan["adapter_interface_registered"]),
		"live_adapter_registered":                 boolOnlyFromAny(liveAdapterPlan["live_adapter_registered"]),
		"live_adapter_implemented":                false,
		"live_adapter_invoked":                    false,
		"live_adapter_contract_implemented":       false,
		"provider_client_constructed":             false,
		"request_builder_invoked":                 false,
		"provider_request_materialized":           false,
		"provider_request_sent":                   false,
		"provider_response_received":              false,
		"execute_method_invoked":                  false,
		"response_handler_invoked":                false,
		"transaction_recorded":                    false,
		"dependency_update_recorded":              false,
		"operation_log_written":                   false,
		"asset_status_snapshot_written":           false,
		"requires_preflight_ready":                true,
		"requires_live_adapter_plan":              true,
		"requires_live_adapter_contract_plan":     true,
		"requires_live_adapter_implementation":    true,
		"requires_provider_client":                true,
		"requires_request_builder":                true,
		"requires_execute_method":                 true,
		"requires_response_handler":               true,
		"requires_transaction_handler":            true,
		"requires_mutation_arming":                true,
		"launch_plan_metadata_ready":              preflightMetadataReady && adapterPlanObserved && contractPlanObserved,
		"provider_api_call_made":                  false,
		"external_call_made":                      false,
		"provider_api_mutation":                   "disabled",
		"request_body_included":                   false,
		"response_body_included":                  false,
		"headers_included":                        false,
		"authorization_header_included":           false,
		"provider_url_included":                   false,
		"idempotency_key_included":                false,
		"provider_request_id_included":            false,
		"contains_token":                          false,
		"contains_provider_url":                   false,
		"contains_repository_ref":                 false,
		"contains_branch_name":                    false,
		"contains_file_content":                   false,
		"future_live_execution_still_blocked":     true,
		"live_execution_launch_boundary_redacted": true,
		"launch_sequence": []string{
			"verify_live_execution_preflight",
			"verify_live_adapter_contract",
			"construct_provider_client",
			"build_provider_request",
			"invoke_execute_method",
			"send_provider_request",
			"classify_provider_response",
			"record_attempt_transaction",
		},
		"blocked_reasons": []string{
			"provider_review_live_execution_preflight_not_ready",
			"provider_review_live_adapter_not_implemented",
			"provider_review_mutation_not_armed",
			"provider_request_send_not_armed",
			"provider_review_transaction_recording_not_armed",
		},
	}
}

func providerReviewAttemptLiveExecutionLaunchPlanReadiness(launchPlan map[string]any) (bool, string, []string) {
	missing := []string{}
	for _, item := range []struct {
		field  string
		reason string
	}{
		{"requires_preflight_ready", "provider_review_live_execution_preflight_requirement_missing"},
		{"requires_live_adapter_plan", "provider_review_live_adapter_plan_requirement_missing"},
		{"requires_live_adapter_contract_plan", "provider_review_live_adapter_contract_requirement_missing"},
		{"requires_live_adapter_implementation", "provider_review_live_adapter_requirement_missing"},
		{"requires_provider_client", "provider_review_provider_client_requirement_missing"},
		{"requires_request_builder", "provider_review_request_builder_requirement_missing"},
		{"requires_execute_method", "provider_review_execute_method_requirement_missing"},
		{"requires_response_handler", "provider_review_response_handler_requirement_missing"},
		{"requires_transaction_handler", "provider_review_transaction_handler_requirement_missing"},
		{"requires_mutation_arming", "provider_review_mutation_arming_requirement_missing"},
		{"future_live_execution_still_blocked", "provider_review_live_execution_still_blocked_expected"},
	} {
		if !boolOnlyFromAny(launchPlan[item.field]) {
			missing = append(missing, item.reason)
		}
	}
	if !boolOnlyFromAny(launchPlan["preflight_ready"]) {
		missing = append(missing, "provider_review_live_execution_preflight_not_ready")
	}
	if !boolOnlyFromAny(launchPlan["preflight_metadata_ready"]) {
		missing = append(missing, "provider_review_live_execution_preflight_metadata_not_ready")
	}
	if !boolOnlyFromAny(launchPlan["live_adapter_plan_observed"]) {
		missing = append(missing, "provider_review_live_adapter_plan_missing")
	}
	if !boolOnlyFromAny(launchPlan["live_adapter_contract_plan_observed"]) {
		missing = append(missing, "provider_review_live_adapter_contract_plan_missing")
	}
	if !boolOnlyFromAny(launchPlan["live_adapter_implemented"]) {
		missing = append(missing, "provider_review_live_adapter_not_implemented")
	}
	if !boolOnlyFromAny(launchPlan["provider_client_constructed"]) {
		missing = append(missing, "provider_review_provider_client_not_constructed")
	}
	if !boolOnlyFromAny(launchPlan["request_builder_invoked"]) {
		missing = append(missing, "provider_review_request_builder_not_invoked")
	}
	if !boolOnlyFromAny(launchPlan["provider_request_materialized"]) {
		missing = append(missing, "provider_review_request_not_materialized")
	}
	if !boolOnlyFromAny(launchPlan["provider_request_sent"]) {
		missing = append(missing, "provider_request_send_not_armed")
	}
	if !boolOnlyFromAny(launchPlan["response_handler_invoked"]) {
		missing = append(missing, "provider_review_response_handler_not_invoked")
	}
	if !boolOnlyFromAny(launchPlan["transaction_recorded"]) {
		missing = append(missing, "provider_review_transaction_recording_not_armed")
	}
	if boolOnlyFromAny(launchPlan["provider_api_call_made"]) ||
		boolOnlyFromAny(launchPlan["external_call_made"]) ||
		stringFromMap(launchPlan, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(launchPlan["provider_request_sent"]) ||
		boolOnlyFromAny(launchPlan["provider_response_received"]) ||
		boolOnlyFromAny(launchPlan["request_body_included"]) ||
		boolOnlyFromAny(launchPlan["response_body_included"]) ||
		boolOnlyFromAny(launchPlan["headers_included"]) ||
		boolOnlyFromAny(launchPlan["authorization_header_included"]) ||
		boolOnlyFromAny(launchPlan["provider_url_included"]) ||
		boolOnlyFromAny(launchPlan["provider_request_id_included"]) ||
		boolOnlyFromAny(launchPlan["idempotency_key_included"]) ||
		boolOnlyFromAny(launchPlan["contains_token"]) ||
		boolOnlyFromAny(launchPlan["contains_provider_url"]) ||
		boolOnlyFromAny(launchPlan["contains_repository_ref"]) ||
		boolOnlyFromAny(launchPlan["contains_branch_name"]) ||
		boolOnlyFromAny(launchPlan["contains_file_content"]) {
		missing = append(missing, "provider_review_live_execution_launch_not_no_call")
	}
	if len(missing) == 0 {
		return true, "live_execution_launch_ready", nil
	}
	return false, "live_execution_launch_blocked", missing
}

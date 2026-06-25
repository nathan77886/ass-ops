package app

import (
	"context"
	"fmt"
)

type ProviderReviewCurrentLiveExecutionGateOptions struct {
	OperationApprovalID string
	Approval            map[string]any
	AttemptLedger       map[string]any
}

func ProviderReviewCurrentLiveExecutionGate(ctx context.Context, store *Store, opts ProviderReviewCurrentLiveExecutionGateOptions) (map[string]any, error) {
	if store == nil || store.DB == nil {
		return nil, fmt.Errorf("store is required")
	}
	approvalID := cleanOptionalID(opts.OperationApprovalID)
	if approvalID == "" {
		return nil, fmt.Errorf("operation approval id is required")
	}
	launchResult, err := ProviderReviewCurrentAttemptLiveExecutionLaunchPlan(ctx, store, ProviderReviewCurrentAttemptLiveExecutionLaunchPlanOptions{
		OperationApprovalID: approvalID,
		Approval:            opts.Approval,
		AttemptLedger:       opts.AttemptLedger,
	})
	if err != nil {
		return nil, err
	}
	gate := providerReviewCurrentLiveExecutionGatePayload(approvalID, launchResult)
	result := map[string]any{
		"mode":                                "provider_review_current_live_execution_gate",
		"execution_gate_state":                "provider_review_live_execution_gate_blocked",
		"execution_gate_ready":                false,
		"operation_approval_id":               approvalID,
		"operation_approval_action":           cleanOptionalText(stringFromMap(launchResult, "operation_approval_action")),
		"operation_approval_status":           cleanOptionalText(stringFromMap(launchResult, "operation_approval_status")),
		"candidate_observed":                  boolOnlyFromAny(launchResult["candidate_observed"]),
		"provider_review_attempt_id":          cleanOptionalID(fmt.Sprint(launchResult["provider_review_attempt_id"])),
		"next_attempt_operation":              cleanOptionalText(stringFromMap(launchResult, "next_attempt_operation")),
		"endpoint_key":                        cleanOptionalText(stringFromMap(launchResult, "endpoint_key")),
		"current_launch_plan_ready":           boolOnlyFromAny(launchResult["launch_plan_ready"]),
		"current_launch_plan_state":           cleanOptionalText(stringFromMap(launchResult, "launch_plan_state")),
		"live_execution_preflight_ready":      boolOnlyFromAny(launchResult["live_execution_preflight_ready"]),
		"live_execution_preflight_state":      cleanOptionalText(stringFromMap(launchResult, "live_execution_preflight_state")),
		"launch_result":                       launchResult,
		"execution_gate":                      gate,
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
		"missing_evidence":                    providerReviewCurrentLiveExecutionGateMissingEvidence(launchResult),
		"message":                             "Provider review live execution gate is blocked; real provider execution requires a reviewed live adapter, mutation arming, provider send, response handling, and transaction recording.",
	}
	return result, nil
}

func providerReviewCurrentLiveExecutionGatePayload(approvalID string, launchResult map[string]any) map[string]any {
	return map[string]any{
		"mode":                                      "redacted_provider_review_live_execution_gate",
		"operation_approval_id":                     approvalID,
		"provider_review_attempt_id":                cleanOptionalID(fmt.Sprint(launchResult["provider_review_attempt_id"])),
		"operation_name":                            cleanOptionalText(stringFromMap(launchResult, "next_attempt_operation")),
		"endpoint_key":                              cleanOptionalText(stringFromMap(launchResult, "endpoint_key")),
		"current_launch_plan_ready":                 boolOnlyFromAny(launchResult["launch_plan_ready"]),
		"current_launch_plan_state":                 cleanOptionalText(stringFromMap(launchResult, "launch_plan_state")),
		"live_execution_preflight_ready":            boolOnlyFromAny(launchResult["live_execution_preflight_ready"]),
		"live_execution_preflight_state":            cleanOptionalText(stringFromMap(launchResult, "live_execution_preflight_state")),
		"requires_current_execution_candidate":      true,
		"requires_approved_operation_approval":      true,
		"requires_current_launch_plan":              true,
		"requires_live_adapter_implementation":      true,
		"requires_provider_client":                  true,
		"requires_request_builder":                  true,
		"requires_mutation_arming":                  true,
		"requires_provider_send":                    true,
		"requires_response_handler":                 true,
		"requires_transaction_recording":            true,
		"requires_operation_log_recording":          true,
		"requires_asset_status_snapshot_recording":  true,
		"operator_final_approval_required":          true,
		"live_adapter_implemented":                  false,
		"provider_client_constructed":               false,
		"request_builder_invoked":                   false,
		"provider_request_materialized":             false,
		"provider_request_sent":                     false,
		"provider_response_received":                false,
		"response_handler_invoked":                  false,
		"transaction_recorded":                      false,
		"operation_log_written":                     false,
		"asset_status_snapshot_written":             false,
		"provider_api_call_made":                    false,
		"external_call_made":                        false,
		"provider_api_mutation":                     "disabled",
		"request_body_included":                     false,
		"response_body_included":                    false,
		"headers_included":                          false,
		"authorization_header_included":             false,
		"provider_url_included":                     false,
		"idempotency_key_included":                  false,
		"provider_request_id_included":              false,
		"contains_token":                            false,
		"contains_provider_url":                     false,
		"contains_repository_ref":                   false,
		"contains_branch_name":                      false,
		"contains_file_content":                     false,
		"future_live_execution_still_blocked":       true,
		"live_execution_gate_boundary_redacted":     true,
		"live_execution_gate_metadata_observed":     boolOnlyFromAny(launchResult["candidate_observed"]),
		"live_execution_gate_blocks_provider_send":  true,
		"live_execution_gate_blocks_provider_write": true,
		"gate_sequence": []string{
			"verify_current_launch_plan",
			"verify_live_adapter_implementation",
			"verify_mutation_arming",
			"verify_provider_send_arming",
			"verify_response_handler_binding",
			"verify_transaction_recording",
			"verify_operator_final_approval",
		},
	}
}

func providerReviewCurrentLiveExecutionGateMissingEvidence(launchResult map[string]any) []string {
	missing := []string{}
	if !boolOnlyFromAny(launchResult["candidate_observed"]) {
		missing = append(missing, "provider_review_current_attempt_missing")
	}
	if stringFromMap(launchResult, "operation_approval_status") != "approved" {
		missing = append(missing, "operation_approval_not_approved")
	}
	if !boolOnlyFromAny(launchResult["launch_plan_ready"]) {
		missing = append(missing, "provider_review_current_launch_plan_not_ready")
	}
	if !boolOnlyFromAny(launchResult["live_execution_preflight_ready"]) {
		missing = append(missing, "provider_review_live_execution_preflight_not_ready")
	}
	missing = append(missing,
		"provider_review_live_adapter_not_implemented",
		"provider_review_mutation_not_armed",
		"provider_request_send_not_armed",
		"provider_review_response_handler_not_bound",
		"provider_review_transaction_recording_not_armed",
		"provider_review_operator_final_approval_required",
	)
	return missing
}

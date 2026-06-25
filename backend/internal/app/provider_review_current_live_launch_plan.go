package app

import (
	"context"
	"fmt"
)

type ProviderReviewCurrentAttemptLiveExecutionLaunchPlanOptions struct {
	OperationApprovalID string
	Approval            map[string]any
	AttemptLedger       map[string]any
}

func ProviderReviewCurrentAttemptLiveExecutionLaunchPlan(ctx context.Context, store *Store, opts ProviderReviewCurrentAttemptLiveExecutionLaunchPlanOptions) (map[string]any, error) {
	if store == nil || store.DB == nil {
		return nil, fmt.Errorf("store is required")
	}
	approvalID := cleanOptionalID(opts.OperationApprovalID)
	if approvalID == "" {
		return nil, fmt.Errorf("operation approval id is required")
	}
	approval := opts.Approval
	var err error
	if len(approval) == 0 {
		approval, err = providerReviewApprovalForArmingSnapshot(ctx, store, approvalID)
		if err != nil {
			return nil, err
		}
	}
	ledger := opts.AttemptLedger
	if len(ledger) == 0 {
		ledger, err = providerReviewAttemptLedgerForApprovalSnapshot(ctx, store, approvalID)
		if err != nil {
			return nil, err
		}
	}
	candidate := providerReviewCurrentAttemptCandidateFromLedger(ledger)
	result := map[string]any{
		"mode":                                "provider_review_current_attempt_live_execution_launch_plan",
		"launch_plan_state":                   "current_attempt_not_ready",
		"launch_plan_ready":                   false,
		"operation_approval_id":               approvalID,
		"operation_approval_action":           cleanOptionalText(stringFromMap(approval, "action")),
		"operation_approval_status":           cleanOptionalText(stringFromMap(approval, "status")),
		"candidate_observed":                  len(candidate) > 0,
		"provider_review_attempt_id":          cleanOptionalID(fmt.Sprint(candidate["id"])),
		"next_attempt_operation":              cleanOptionalText(stringFromMap(candidate, "name")),
		"endpoint_key":                        cleanOptionalText(stringFromMap(candidate, "endpoint_key")),
		"attempt_result":                      nil,
		"launch_plan":                         nil,
		"live_execution_preflight_ready":      false,
		"live_execution_preflight_state":      "current_attempt_not_ready",
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
	if stringFromMap(approval, "action") != templateProviderReviewExecuteApprovalAction {
		result["launch_plan_state"] = "provider_review_execution_approval_action"
		result["missing_evidence"] = []string{"provider_review_execution_approval_action"}
		result["message"] = "Provider review current live launch plan requires a provider review execution approval."
		return result, nil
	}
	if stringFromMap(approval, "status") != "approved" {
		result["launch_plan_state"] = "operation_approval_not_approved"
		result["missing_evidence"] = []string{"operation_approval_not_approved"}
		result["message"] = "Provider review current live launch plan is waiting for an approved provider review execution approval."
		return result, nil
	}
	attemptID := cleanOptionalID(fmt.Sprint(candidate["id"]))
	if attemptID == "" {
		result["missing_evidence"] = []string{"provider_review_current_attempt_missing"}
		result["message"] = "Provider review current live launch plan is waiting for a ready execution candidate in the local attempt ledger."
		return result, nil
	}
	attemptResult, err := ProviderReviewAttemptLiveExecutionLaunchPlan(ctx, store, ProviderReviewAttemptLiveExecutionLaunchPlanOptions{
		AttemptID: attemptID,
	})
	if err != nil {
		return nil, err
	}
	result["attempt_result"] = attemptResult
	for _, key := range []string{
		"launch_plan_state",
		"launch_plan_ready",
		"live_execution_preflight_ready",
		"live_execution_preflight_state",
		"launch_plan",
		"provider_request_materialized",
		"provider_request_sent",
		"provider_response_received",
		"provider_client_constructed",
		"live_adapter_invoked",
		"execute_method_invoked",
		"response_handler_invoked",
		"transaction_recorded",
		"operation_log_written",
		"asset_status_snapshot_written",
		"future_live_execution_still_blocked",
		"missing_evidence",
		"message",
	} {
		if value, ok := attemptResult[key]; ok {
			result[key] = value
		}
	}
	result["mode"] = "provider_review_current_attempt_live_execution_launch_plan"
	result["provider_review_attempt_id"] = attemptID
	result["next_attempt_operation"] = cleanOptionalText(stringFromMap(candidate, "name"))
	result["endpoint_key"] = cleanOptionalText(stringFromMap(candidate, "endpoint_key"))
	return result, nil
}

package app

import (
	"context"
	"fmt"
)

type ProviderReviewCurrentAttemptLiveReadinessSnapshotOptions struct {
	OperationApprovalID string
	DryRun              bool
	Approval            map[string]any
	AttemptLedger       map[string]any
}

func RecordProviderReviewCurrentAttemptLiveReadinessSnapshot(ctx context.Context, store *Store, opts ProviderReviewCurrentAttemptLiveReadinessSnapshotOptions) (map[string]any, error) {
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
		"mode":                                   "provider_review_current_attempt_live_execution_readiness_snapshot_recording",
		"recording_state":                        "current_attempt_not_ready",
		"recording_ready":                        false,
		"recording_enabled":                      false,
		"dry_run":                                opts.DryRun,
		"operation_approval_id":                  approvalID,
		"operation_approval_action":              cleanOptionalText(stringFromMap(approval, "action")),
		"operation_approval_status":              cleanOptionalText(stringFromMap(approval, "status")),
		"candidate_observed":                     len(candidate) > 0,
		"provider_review_attempt_id":             cleanOptionalID(fmt.Sprint(candidate["id"])),
		"next_attempt_operation":                 cleanOptionalText(stringFromMap(candidate, "name")),
		"endpoint_key":                           cleanOptionalText(stringFromMap(candidate, "endpoint_key")),
		"provider_review_attempt_asset_observed": false,
		"snapshots_written":                      0,
		"snapshots_skipped_as_duplicate":         0,
		"provider_review_attempt_live_execution_readiness_snapshot_written": false,
		"asset_status_snapshot_written":                                     false,
		"operation_log_written":                                             false,
		"external_call_made":                                                false,
		"provider_api_call_made":                                            false,
		"provider_api_mutation":                                             "disabled",
		"provider_request_sent":                                             false,
		"provider_response_received":                                        false,
		"mutation_armed":                                                    false,
		"live_adapter_implemented":                                          false,
		"future_live_execution_still_blocked":                               true,
		"contains_token":                                                    false,
		"contains_provider_url":                                             false,
		"contains_repository_ref":                                           false,
		"contains_branch_name":                                              false,
		"contains_file_content":                                             false,
	}
	if stringFromMap(approval, "action") != templateProviderReviewExecuteApprovalAction {
		result["recording_state"] = "provider_review_execution_approval_action"
		result["missing_evidence"] = []string{"provider_review_execution_approval_action"}
		result["message"] = "Provider review current live-readiness snapshot requires a provider review execution approval."
		return result, nil
	}
	if stringFromMap(approval, "status") != "approved" {
		result["recording_state"] = "operation_approval_not_approved"
		result["missing_evidence"] = []string{"operation_approval_not_approved"}
		result["message"] = "Provider review current live-readiness snapshot is waiting for an approved provider review execution approval."
		return result, nil
	}
	attemptID := cleanOptionalID(fmt.Sprint(candidate["id"]))
	if attemptID == "" {
		result["missing_evidence"] = []string{"provider_review_current_attempt_missing"}
		result["message"] = "Provider review current live-readiness snapshot is waiting for a ready execution candidate in the local attempt ledger."
		return result, nil
	}
	attemptResult, err := RecordProviderReviewAttemptLiveExecutionReadinessSnapshot(ctx, store, ProviderReviewAttemptLiveExecutionReadinessSnapshotOptions{
		AttemptID: attemptID,
		DryRun:    opts.DryRun,
		Ledger:    ledger,
	})
	if err != nil {
		return nil, err
	}
	result["attempt_result"] = attemptResult
	for _, key := range []string{
		"recording_state",
		"recording_ready",
		"recording_enabled",
		"provider_review_attempt_asset_observed",
		"snapshots_written",
		"snapshots_skipped_as_duplicate",
		"provider_review_attempt_live_execution_readiness_snapshot_written",
		"asset_status_snapshot_written",
		"provider_request_sent",
		"provider_response_received",
		"mutation_armed",
		"live_adapter_implemented",
		"future_live_execution_still_blocked",
		"status_snapshot_write_eligible",
		"missing_evidence",
		"message",
		"snapshot",
	} {
		if value, ok := attemptResult[key]; ok {
			result[key] = value
		}
	}
	result["mode"] = "provider_review_current_attempt_live_execution_readiness_snapshot_recording"
	result["provider_review_attempt_id"] = attemptID
	result["next_attempt_operation"] = cleanOptionalText(stringFromMap(candidate, "name"))
	result["endpoint_key"] = cleanOptionalText(stringFromMap(candidate, "endpoint_key"))
	return result, nil
}

func providerReviewCurrentAttemptCandidateFromLedger(ledger map[string]any) map[string]any {
	orchestration := mapFromAny(ledger["orchestration"])
	executionCandidate := mapFromAny(orchestration["execution_candidate"])
	nextOperation := safeProviderReviewAttemptOperationName(stringFromMap(executionCandidate, "next_operation"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(executionCandidate, "endpoint_key"))
	if nextOperation == "" || endpointKey == "" {
		return map[string]any{}
	}
	for _, operation := range providerReviewAttemptLedgerOperationsFromAny(ledger["operations"]) {
		if safeProviderReviewAttemptOperationName(stringFromMap(operation, "name")) == nextOperation &&
			safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key")) == endpointKey {
			return operation
		}
	}
	return map[string]any{}
}

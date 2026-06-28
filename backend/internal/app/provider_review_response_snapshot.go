package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptResponseSnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
	Ledger    map[string]any
}

func RecordProviderReviewAttemptResponseSnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptResponseSnapshotOptions) (map[string]any, error) {
	if store == nil || store.Gorm == nil {
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
	approvalID := cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"]))
	ledger := opts.Ledger
	if len(ledger) == 0 {
		ledger, err = providerReviewAttemptLedgerForApprovalSnapshot(ctx, store, approvalID)
		if err != nil {
			return nil, err
		}
	}
	assetID, assetErr := providerReviewAttemptAssetID(ctx, store.Gorm, attemptID)
	snapshot := providerReviewAttemptResponseSnapshotPayload(attempt, ledger, assetErr == nil)
	ready, state, missing := providerReviewAttemptResponseSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_response_snapshot_recording",
		"recording_state":                        state,
		"recording_ready":                        ready,
		"recording_enabled":                      ready && !opts.DryRun,
		"dry_run":                                opts.DryRun,
		"provider_review_attempt_id":             attemptID,
		"operation_approval_id":                  approvalID,
		"project_template_run_id":                cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed": assetErr == nil,
		"snapshot":                               snapshot,
		"snapshots_written":                      0,
		"snapshots_skipped_as_duplicate":         0,
		"provider_review_attempt_response_snapshot_written": false,
		"asset_status_snapshot_written":                     false,
		"operation_log_written":                             false,
		"external_call_made":                                false,
		"provider_api_call_made":                            false,
		"provider_api_mutation":                             "disabled",
		"mutation_armed":                                    false,
		"provider_request_sent":                             false,
		"provider_response_received":                        false,
		"response_recorded":                                 false,
		"transaction_recorded":                              false,
		"provider_call_boundary_recorded":                   false,
		"contains_token":                                    false,
		"contains_provider_url":                             false,
		"contains_repository_ref":                           false,
		"contains_branch_name":                              false,
		"contains_file_content":                             false,
		"canonical_asset_status_snapshot_try":               false,
		"snapshot_commit_attempted":                         false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt response snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt response snapshot is waiting for the current execution candidate, response plan, result recording plan, and transaction metadata; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt response snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptResponseSnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "provider review attempt response snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording provider review attempt response snapshot recorded: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["provider_review_attempt_response_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized provider review attempt response snapshot recorded from local response, result recording, transaction, and provider-call boundary metadata."
	return result, nil
}

func providerReviewAttemptResponseSnapshotPayload(attempt, ledger map[string]any, assetObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	responsePlan := mapFromAny(dispatchPlan["response_plan"])
	resultPlan := mapFromAny(responsePlan["result_recording_plan"])
	transactionPlan := mapFromAny(dispatchPlan["transaction_plan"])
	providerCallBoundaryPlan := mapFromAny(transactionPlan["provider_call_boundary_plan"])
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	responseMetadataReady := providerReviewAttemptResponsePlanReadyForOperation(responsePlan, operationName, endpointKey)
	resultRecordingMetadataReady := boolOnlyFromAny(resultPlan["result_recording_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(resultPlan, "redacted_attempt_adapter_result_recording_plan", operationName, endpointKey)
	transactionMetadataReady := providerReviewAttemptTransactionPlanReadyForOperation(transactionPlan, operationName, endpointKey)
	providerCallBoundaryMetadataReady := boolOnlyFromAny(providerCallBoundaryPlan["provider_call_boundary_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(providerCallBoundaryPlan, "redacted_attempt_adapter_provider_call_boundary_plan", operationName, endpointKey)
	statusSnapshotWriteEligible := assetObserved && candidateMatches && len(responsePlan) > 0 && len(resultPlan) > 0 && len(transactionPlan) > 0 && len(providerCallBoundaryPlan) > 0
	return map[string]any{
		"mode":                                        "provider_review_attempt_response_snapshot",
		"provider_review_attempt_id":                  cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":                       cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"project_template_run_id":                     cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed":      assetObserved,
		"operation_name":                              operationName,
		"endpoint_key":                                endpointKey,
		"attempt_status":                              safeProviderReviewAttemptStatus(stringFromMap(attempt, "status")),
		"dependency_status":                           safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(attempt, "dependency_status")),
		"operation_order":                             intFromAny(attempt["operation_order"], 0),
		"candidate_observed":                          len(candidate) > 0,
		"candidate_matches_attempt":                   candidateMatches,
		"candidate_status":                            cleanOptionalText(stringFromMap(candidate, "status")),
		"dispatch_plan_observed":                      len(dispatchPlan) > 0,
		"response_plan_observed":                      len(responsePlan) > 0,
		"result_recording_plan_observed":              len(resultPlan) > 0,
		"transaction_plan_observed":                   len(transactionPlan) > 0,
		"provider_call_boundary_plan_observed":        len(providerCallBoundaryPlan) > 0,
		"response_metadata_ready":                     responseMetadataReady,
		"result_recording_metadata_ready":             resultRecordingMetadataReady,
		"transaction_metadata_ready":                  transactionMetadataReady,
		"provider_call_boundary_metadata_ready":       providerCallBoundaryMetadataReady,
		"response_recording_ready":                    boolOnlyFromAny(responsePlan["response_recording_ready"]),
		"result_recording_ready":                      boolOnlyFromAny(resultPlan["result_recording_ready"]),
		"transaction_ready":                           boolOnlyFromAny(transactionPlan["transaction_ready"]),
		"provider_call_boundary_ready":                boolOnlyFromAny(providerCallBoundaryPlan["provider_call_boundary_ready"]),
		"response_handler":                            safeProviderReviewResponseHandlerName(stringFromMap(responsePlan, "response_handler")),
		"response_status":                             safeProviderReviewAttemptResponseStatus(stringFromMap(responsePlan, "response_status")),
		"expected_success_classes":                    safeProviderReviewStatusClasses(stringSliceFromAny(responsePlan["expected_success_classes"])),
		"retryable_status_classes":                    safeProviderReviewStatusClasses(stringSliceFromAny(responsePlan["retryable_status_classes"])),
		"terminal_failure_status_classes":             safeProviderReviewStatusClasses(stringSliceFromAny(responsePlan["terminal_failure_status_classes"])),
		"success_attempt_status":                      safeProviderReviewAttemptStatus(stringFromMap(responsePlan, "success_attempt_status")),
		"retry_attempt_status":                        safeProviderReviewAttemptStatus(stringFromMap(responsePlan, "retry_attempt_status")),
		"failure_attempt_status":                      safeProviderReviewAttemptStatus(stringFromMap(responsePlan, "failure_attempt_status")),
		"dependency_unlocks_operation":                safeProviderReviewAttemptOperationName(stringFromMap(responsePlan, "dependency_unlocks_operation")),
		"dependency_update_status":                    safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(responsePlan, "dependency_update_status")),
		"requires_response_handler":                   true,
		"requires_response_diagnostics":               true,
		"requires_transaction_boundary":               true,
		"requires_database_transaction":               true,
		"requires_provider_call_boundary":             true,
		"requires_dependency_update":                  boolOnlyFromAny(responsePlan["requires_dependency_update"]),
		"requires_mutation_arming":                    true,
		"adapter_implemented":                         false,
		"mutation_armed":                              false,
		"provider_request_sent":                       false,
		"provider_response_received":                  false,
		"response_recorded":                           false,
		"response_classified":                         false,
		"attempt_status_mapped":                       false,
		"attempt_result_persisted":                    false,
		"dependency_update_staged":                    false,
		"dependency_update_recorded":                  false,
		"transaction_opened":                          false,
		"transaction_recorded":                        false,
		"attempt_claim_verified":                      false,
		"idempotency_claim_verified":                  false,
		"provider_call_boundary_opened":               false,
		"provider_call_boundary_recorded":             false,
		"provider_call_started_recorded":              false,
		"provider_call_finished_recorded":             false,
		"provider_request_id_recorded":                false,
		"provider_response_status_recorded":           false,
		"provider_response_body_recorded":             false,
		"provider_response_headers_recorded":          false,
		"external_call_made":                          false,
		"provider_api_call_made":                      false,
		"provider_api_mutation":                       "disabled",
		"request_body_included":                       false,
		"response_body_included":                      false,
		"headers_included":                            false,
		"authorization_header_included":               false,
		"provider_url_included":                       false,
		"idempotency_key_included":                    false,
		"provider_request_id_included":                false,
		"contains_token":                              false,
		"contains_provider_url":                       false,
		"contains_repository_ref":                     false,
		"contains_branch_name":                        false,
		"contains_file_content":                       false,
		"status_snapshot_write_eligible":              statusSnapshotWriteEligible,
		"status_snapshot_written":                     statusSnapshotWriteEligible,
		"response_boundary_redacted":                  true,
		"result_recording_boundary_redacted":          true,
		"transaction_boundary_redacted":               true,
		"provider_call_boundary_redacted":             true,
		"future_live_provider_response_still_blocked": true,
	}
}

func providerReviewAttemptResponseSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "response_blocked"
	if boolOnlyFromAny(snapshot["response_metadata_ready"]) &&
		boolOnlyFromAny(snapshot["result_recording_metadata_ready"]) &&
		boolOnlyFromAny(snapshot["transaction_metadata_ready"]) &&
		boolOnlyFromAny(snapshot["provider_call_boundary_metadata_ready"]) {
		state = "response_metadata_ready"
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
	if !boolOnlyFromAny(snapshot["transaction_plan_observed"]) {
		missing = append(missing, "provider_review_transaction_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["provider_call_boundary_plan_observed"]) {
		missing = append(missing, "provider_review_provider_call_boundary_plan_missing")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["provider_response_received"]) ||
		boolOnlyFromAny(snapshot["response_recorded"]) ||
		boolOnlyFromAny(snapshot["transaction_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_call_boundary_recorded"]) {
		missing = append(missing, "provider_review_response_not_no_call")
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptResponseSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "response_metadata_ready":
		return "provider_review_attempt_response_metadata_ready", "low"
	case "response_blocked":
		return "provider_review_attempt_response_blocked", "warning"
	default:
		return "provider_review_attempt_response_unknown", "warning"
	}
}

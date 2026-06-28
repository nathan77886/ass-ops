package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptAdapterBlueprintSnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
	Ledger    map[string]any
}

func RecordProviderReviewAttemptAdapterBlueprintSnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptAdapterBlueprintSnapshotOptions) (map[string]any, error) {
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
	snapshot := providerReviewAttemptAdapterBlueprintSnapshotPayload(attempt, ledger, assetErr == nil)
	ready, state, missing := providerReviewAttemptAdapterBlueprintSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_adapter_blueprint_snapshot_recording",
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
		"provider_review_attempt_adapter_blueprint_snapshot_written": false,
		"asset_status_snapshot_written":                              false,
		"operation_log_written":                                      false,
		"external_call_made":                                         false,
		"provider_api_call_made":                                     false,
		"provider_api_mutation":                                      "disabled",
		"mutation_armed":                                             false,
		"adapter_implemented":                                        false,
		"live_adapter_implemented":                                   false,
		"provider_request_sent":                                      false,
		"contains_token":                                             false,
		"contains_provider_url":                                      false,
		"contains_repository_ref":                                    false,
		"contains_branch_name":                                       false,
		"contains_file_content":                                      false,
		"canonical_asset_status_snapshot_try":                        false,
		"snapshot_commit_attempted":                                  false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt adapter blueprint snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt adapter blueprint snapshot is waiting for the current execution candidate and redacted adapter blueprint contract; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt adapter blueprint snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptAdapterBlueprintSnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "provider review attempt adapter blueprint snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording provider review attempt adapter blueprint snapshot: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["provider_review_attempt_adapter_blueprint_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized provider review attempt adapter blueprint snapshot recorded from local redacted blueprint metadata."
	return result, nil
}

func providerReviewAttemptAdapterBlueprintSnapshotPayload(attempt, ledger map[string]any, assetObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	invocationPlan := mapFromAny(dispatchPlan["invocation_plan"])
	activationPlan := mapFromAny(invocationPlan["adapter_activation_plan"])
	liveAdapterPlan := mapFromAny(activationPlan["live_adapter_plan"])
	liveAdapterContract := mapFromAny(liveAdapterPlan["contract_plan"])
	providerSendPlan := mapFromAny(invocationPlan["provider_send_plan"])
	retryBackoffPlan := mapFromAny(providerSendPlan["retry_backoff_plan"])
	transactionPlan := mapFromAny(dispatchPlan["transaction_plan"])
	providerCallBoundaryPlan := mapFromAny(transactionPlan["provider_call_boundary_plan"])
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	dispatchContractReady := providerReviewAttemptPlanMatchesOperation(dispatchPlan, "redacted_attempt_adapter_dispatch_plan", operationName, endpointKey)
	invocationContractReady := providerReviewAttemptPlanMatchesOperation(invocationPlan, "redacted_attempt_adapter_invocation_plan", operationName, endpointKey)
	activationContractReady := providerReviewAttemptPlanMatchesOperation(activationPlan, "redacted_attempt_adapter_activation_plan", operationName, endpointKey)
	liveAdapterContractReady := providerReviewAttemptPlanMatchesOperation(liveAdapterPlan, "redacted_attempt_live_adapter_plan", operationName, endpointKey) &&
		providerReviewAttemptPlanMatchesOperation(liveAdapterContract, "redacted_attempt_live_adapter_contract_plan", operationName, endpointKey)
	providerSendContractReady := providerReviewAttemptPlanMatchesOperation(providerSendPlan, "redacted_attempt_adapter_provider_send_plan", operationName, endpointKey)
	retryBackoffContractReady := providerReviewAttemptPlanMatchesOperation(retryBackoffPlan, "redacted_attempt_adapter_retry_backoff_plan", operationName, endpointKey)
	transactionContractReady := providerReviewAttemptPlanMatchesOperation(transactionPlan, "redacted_attempt_adapter_transaction_plan", operationName, endpointKey)
	providerCallBoundaryContractReady := providerReviewAttemptPlanMatchesOperation(providerCallBoundaryPlan, "redacted_attempt_adapter_provider_call_boundary_plan", operationName, endpointKey)
	statusSnapshotWriteEligible := assetObserved && candidateMatches && len(invocationPlan) > 0
	return map[string]any{
		"mode":                                        "provider_review_attempt_adapter_blueprint_snapshot",
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
		"invocation_plan_observed":                    len(invocationPlan) > 0,
		"adapter_activation_plan_observed":            len(activationPlan) > 0,
		"live_adapter_plan_observed":                  len(liveAdapterPlan) > 0,
		"provider_send_plan_observed":                 len(providerSendPlan) > 0,
		"transaction_plan_observed":                   len(transactionPlan) > 0,
		"provider_call_boundary_plan_observed":        len(providerCallBoundaryPlan) > 0,
		"dispatch_contract_ready":                     dispatchContractReady,
		"invocation_contract_ready":                   invocationContractReady,
		"adapter_activation_contract_ready":           activationContractReady,
		"live_adapter_contract_ready":                 liveAdapterContractReady,
		"provider_send_contract_ready":                providerSendContractReady,
		"retry_backoff_contract_ready":                retryBackoffContractReady,
		"transaction_contract_ready":                  transactionContractReady,
		"provider_call_boundary_contract_ready":       providerCallBoundaryContractReady,
		"provider_type":                               safeProviderReviewProviderType(stringFromMap(dispatchPlan, "provider_type")),
		"adapter_kind":                                cleanOptionalText(stringFromMap(dispatchPlan, "adapter_kind")),
		"method":                                      cleanOptionalText(stringFromMap(dispatchPlan, "method")),
		"payload_shape":                               cleanOptionalText(stringFromMap(dispatchPlan, "payload_shape")),
		"payload_builder":                             safeProviderReviewPayloadBuilderName(stringFromMap(dispatchPlan, "payload_builder")),
		"response_handler":                            safeProviderReviewResponseHandlerName(stringFromMap(dispatchPlan, "response_handler")),
		"invocation_state":                            cleanOptionalText(stringFromMap(invocationPlan, "invocation_state")),
		"invocation_ready":                            boolOnlyFromAny(invocationPlan["invocation_ready"]),
		"adapter_activation_metadata_ready":           boolOnlyFromAny(activationPlan["adapter_activation_metadata_ready"]),
		"provider_send_metadata_ready":                boolOnlyFromAny(providerSendPlan["provider_send_metadata_ready"]),
		"transaction_metadata_ready":                  boolOnlyFromAny(transactionPlan["transaction_metadata_ready"]),
		"required_subplans":                           safeProviderReviewBlueprintNames(stringSliceFromAny(invocationPlan["required_subplans"])),
		"invocation_sequence":                         safeProviderReviewBlueprintNames(stringSliceFromAny(invocationPlan["invocation_sequence"])),
		"adapter_activation_required_interfaces":      safeProviderReviewBlueprintNames(stringSliceFromAny(activationPlan["adapter_activation_required_interfaces"])),
		"live_adapter_required_methods":               safeProviderReviewBlueprintNames(stringSliceFromAny(liveAdapterPlan["live_adapter_required_methods"])),
		"contract_input_fields":                       safeProviderReviewBlueprintNames(stringSliceFromAny(liveAdapterContract["contract_input_fields"])),
		"contract_output_fields":                      safeProviderReviewBlueprintNames(stringSliceFromAny(liveAdapterContract["contract_output_fields"])),
		"contract_error_classes":                      safeProviderReviewBlueprintNames(stringSliceFromAny(liveAdapterContract["contract_error_classes"])),
		"contract_persisted_fields":                   safeProviderReviewBlueprintNames(stringSliceFromAny(liveAdapterContract["contract_persisted_fields"])),
		"adapter_implemented":                         false,
		"live_adapter_implemented":                    false,
		"adapter_activation_approved":                 false,
		"provider_client_bound":                       false,
		"request_materialized":                        false,
		"provider_request_sent":                       false,
		"provider_response_received":                  false,
		"transaction_recorded":                        false,
		"provider_call_boundary_recorded":             false,
		"mutation_armed":                              false,
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
		"adapter_blueprint_boundary_redacted":         true,
		"future_live_adapter_execution_still_blocked": true,
	}
}

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

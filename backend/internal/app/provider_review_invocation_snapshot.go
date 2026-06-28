package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptInvocationSnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
	Ledger    map[string]any
}

func RecordProviderReviewAttemptInvocationSnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptInvocationSnapshotOptions) (map[string]any, error) {
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
	snapshot := providerReviewAttemptInvocationSnapshotPayload(attempt, ledger, assetErr == nil)
	ready, state, missing := providerReviewAttemptInvocationSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_invocation_snapshot_recording",
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
		"provider_review_attempt_invocation_snapshot_written": false,
		"asset_status_snapshot_written":                       false,
		"operation_log_written":                               false,
		"external_call_made":                                  false,
		"provider_api_call_made":                              false,
		"provider_api_mutation":                               "disabled",
		"mutation_armed":                                      false,
		"attempt_claim_recorded":                              false,
		"idempotency_claim_recorded":                          false,
		"execution_lock_acquired":                             false,
		"adapter_activation_approved":                         false,
		"credential_bound":                                    false,
		"adapter_runtime_bound":                               false,
		"branch_policy_verified":                              false,
		"request_materialized":                                false,
		"provider_request_sent":                               false,
		"response_recorded":                                   false,
		"transaction_recorded":                                false,
		"dependency_update_recorded":                          false,
		"contains_token":                                      false,
		"contains_provider_url":                               false,
		"contains_repository_ref":                             false,
		"contains_branch_name":                                false,
		"contains_file_content":                               false,
		"canonical_asset_status_snapshot_try":                 false,
		"snapshot_commit_attempted":                           false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt invocation snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt invocation snapshot is waiting for the current execution candidate and redacted invocation contract; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt invocation snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptInvocationSnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "provider review attempt invocation snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording provider review attempt invocation snapshot recorded: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["provider_review_attempt_invocation_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized provider review attempt invocation snapshot recorded from local redacted invocation metadata."
	return result, nil
}

func providerReviewAttemptInvocationSnapshotPayload(attempt, ledger map[string]any, assetObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	invocationPlan := mapFromAny(dispatchPlan["invocation_plan"])
	executionLockPlan := mapFromAny(invocationPlan["execution_lock_plan"])
	activationPlan := mapFromAny(invocationPlan["adapter_activation_plan"])
	providerSendPlan := mapFromAny(invocationPlan["provider_send_plan"])
	transactionPlan := mapFromAny(dispatchPlan["transaction_plan"])
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	invocationContractReady := providerReviewAttemptPlanMatchesOperation(invocationPlan, "redacted_attempt_adapter_invocation_plan", operationName, endpointKey)
	noCall := !boolOnlyFromAny(invocationPlan["attempt_claim_recorded"]) &&
		!boolOnlyFromAny(invocationPlan["idempotency_claim_recorded"]) &&
		!boolOnlyFromAny(invocationPlan["execution_lock_acquired"]) &&
		!boolOnlyFromAny(invocationPlan["adapter_activation_approved"]) &&
		!boolOnlyFromAny(invocationPlan["duplicate_send_detected"]) &&
		!boolOnlyFromAny(invocationPlan["credential_bound"]) &&
		!boolOnlyFromAny(invocationPlan["adapter_runtime_bound"]) &&
		!boolOnlyFromAny(invocationPlan["branch_policy_verified"]) &&
		!boolOnlyFromAny(invocationPlan["request_materialized"]) &&
		!boolOnlyFromAny(invocationPlan["provider_request_sent"]) &&
		!boolOnlyFromAny(invocationPlan["response_recorded"]) &&
		!boolOnlyFromAny(invocationPlan["transaction_recorded"]) &&
		!boolOnlyFromAny(invocationPlan["dependency_update_recorded"]) &&
		!boolOnlyFromAny(invocationPlan["adapter_implemented"]) &&
		!boolOnlyFromAny(invocationPlan["mutation_armed"]) &&
		!boolOnlyFromAny(invocationPlan["external_call_made"]) &&
		!boolOnlyFromAny(invocationPlan["provider_api_call_made"]) &&
		stringFromMap(invocationPlan, "provider_api_mutation") == "disabled" &&
		!boolOnlyFromAny(invocationPlan["request_body_included"]) &&
		!boolOnlyFromAny(invocationPlan["response_body_included"]) &&
		!boolOnlyFromAny(invocationPlan["headers_included"]) &&
		!boolOnlyFromAny(invocationPlan["authorization_header_included"]) &&
		!boolOnlyFromAny(invocationPlan["provider_url_included"]) &&
		!boolOnlyFromAny(invocationPlan["idempotency_key_included"]) &&
		!boolOnlyFromAny(invocationPlan["provider_request_id_included"]) &&
		!boolOnlyFromAny(invocationPlan["contains_token"]) &&
		!boolOnlyFromAny(invocationPlan["contains_provider_url"]) &&
		!boolOnlyFromAny(invocationPlan["contains_repository_ref"]) &&
		!boolOnlyFromAny(invocationPlan["contains_branch_name"]) &&
		!boolOnlyFromAny(invocationPlan["contains_file_content"])
	requiredSubplans := safeProviderReviewBlueprintNames(stringSliceFromAny(invocationPlan["required_subplans"]))
	invocationSequence := safeProviderReviewBlueprintNames(stringSliceFromAny(invocationPlan["invocation_sequence"]))
	statusSnapshotWriteEligible := assetObserved && candidateMatches && invocationContractReady && noCall && len(requiredSubplans) > 0 && len(invocationSequence) > 0
	return map[string]any{
		"mode":                                       "provider_review_attempt_invocation_snapshot",
		"provider_review_attempt_id":                 cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":                      cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"project_template_run_id":                    cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed":     assetObserved,
		"operation_name":                             operationName,
		"endpoint_key":                               endpointKey,
		"attempt_status":                             safeProviderReviewAttemptStatus(stringFromMap(attempt, "status")),
		"dependency_status":                          safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(attempt, "dependency_status")),
		"operation_order":                            intFromAny(attempt["operation_order"], 0),
		"candidate_observed":                         len(candidate) > 0,
		"candidate_matches_attempt":                  candidateMatches,
		"candidate_status":                           cleanOptionalText(stringFromMap(candidate, "status")),
		"dispatch_plan_observed":                     len(dispatchPlan) > 0,
		"invocation_plan_observed":                   len(invocationPlan) > 0,
		"invocation_contract_ready":                  invocationContractReady,
		"invocation_state":                           cleanOptionalText(stringFromMap(invocationPlan, "invocation_state")),
		"invocation_ready":                           boolOnlyFromAny(invocationPlan["invocation_ready"]),
		"invocation_ready_reason":                    safeProviderReviewInvocationReadyReason(stringFromMap(invocationPlan, "invocation_ready_reason")),
		"required_subplans":                          requiredSubplans,
		"invocation_sequence":                        invocationSequence,
		"execution_lock_plan_observed":               len(executionLockPlan) > 0,
		"adapter_activation_plan_observed":           len(activationPlan) > 0,
		"provider_send_plan_observed":                len(providerSendPlan) > 0,
		"transaction_plan_observed":                  len(transactionPlan) > 0,
		"claim_metadata_ready":                       boolOnlyFromAny(invocationPlan["claim_metadata_ready"]),
		"execution_lock_metadata_ready":              boolOnlyFromAny(invocationPlan["execution_lock_metadata_ready"]),
		"adapter_activation_metadata_ready":          boolOnlyFromAny(invocationPlan["adapter_activation_metadata_ready"]),
		"credential_binding_ready":                   boolOnlyFromAny(invocationPlan["credential_binding_ready"]),
		"adapter_runtime_ready":                      boolOnlyFromAny(invocationPlan["adapter_runtime_ready"]),
		"branch_policy_metadata_ready":               boolOnlyFromAny(invocationPlan["branch_policy_metadata_ready"]),
		"request_materialization_ready":              boolOnlyFromAny(invocationPlan["request_materialization_ready"]),
		"transport_metadata_ready":                   boolOnlyFromAny(invocationPlan["transport_metadata_ready"]),
		"provider_send_metadata_ready":               boolOnlyFromAny(invocationPlan["provider_send_metadata_ready"]),
		"response_recording_ready":                   boolOnlyFromAny(invocationPlan["response_recording_ready"]),
		"transaction_metadata_ready":                 boolOnlyFromAny(invocationPlan["transaction_metadata_ready"]),
		"claim_metadata_ready_reason":                cleanOptionalText(stringFromMap(invocationPlan, "claim_metadata_ready_reason")),
		"execution_lock_ready_reason":                cleanOptionalText(stringFromMap(invocationPlan, "execution_lock_ready_reason")),
		"adapter_activation_ready_reason":            cleanOptionalText(stringFromMap(invocationPlan, "adapter_activation_ready_reason")),
		"adapter_runtime_ready_reason":               cleanOptionalText(stringFromMap(invocationPlan, "adapter_runtime_ready_reason")),
		"branch_policy_ready_reason":                 cleanOptionalText(stringFromMap(invocationPlan, "branch_policy_ready_reason")),
		"transport_metadata_ready_reason":            cleanOptionalText(stringFromMap(invocationPlan, "transport_metadata_ready_reason")),
		"provider_send_ready_reason":                 cleanOptionalText(stringFromMap(invocationPlan, "provider_send_ready_reason")),
		"transaction_metadata_ready_reason":          cleanOptionalText(stringFromMap(invocationPlan, "transaction_metadata_ready_reason")),
		"requires_attempt_claim":                     boolOnlyFromAny(invocationPlan["requires_attempt_claim"]),
		"requires_idempotency_claim":                 boolOnlyFromAny(invocationPlan["requires_idempotency_claim"]),
		"requires_execution_lock":                    boolOnlyFromAny(invocationPlan["requires_execution_lock"]),
		"requires_adapter_activation":                boolOnlyFromAny(invocationPlan["requires_adapter_activation"]),
		"requires_credential_binding":                boolOnlyFromAny(invocationPlan["requires_credential_binding"]),
		"requires_adapter_runtime":                   boolOnlyFromAny(invocationPlan["requires_adapter_runtime"]),
		"requires_branch_policy":                     boolOnlyFromAny(invocationPlan["requires_branch_policy"]),
		"requires_request_materialization":           boolOnlyFromAny(invocationPlan["requires_request_materialization"]),
		"requires_transport":                         boolOnlyFromAny(invocationPlan["requires_transport"]),
		"requires_response_recording":                boolOnlyFromAny(invocationPlan["requires_response_recording"]),
		"requires_transaction_boundary":              boolOnlyFromAny(invocationPlan["requires_transaction_boundary"]),
		"requires_mutation_arming":                   boolOnlyFromAny(invocationPlan["requires_mutation_arming"]),
		"attempt_claim_recorded":                     boolOnlyFromAny(invocationPlan["attempt_claim_recorded"]),
		"idempotency_claim_recorded":                 boolOnlyFromAny(invocationPlan["idempotency_claim_recorded"]),
		"execution_lock_acquired":                    boolOnlyFromAny(invocationPlan["execution_lock_acquired"]),
		"adapter_activation_approved":                boolOnlyFromAny(invocationPlan["adapter_activation_approved"]),
		"duplicate_send_detected":                    boolOnlyFromAny(invocationPlan["duplicate_send_detected"]),
		"credential_bound":                           boolOnlyFromAny(invocationPlan["credential_bound"]),
		"adapter_runtime_bound":                      boolOnlyFromAny(invocationPlan["adapter_runtime_bound"]),
		"branch_policy_verified":                     boolOnlyFromAny(invocationPlan["branch_policy_verified"]),
		"request_materialized":                       boolOnlyFromAny(invocationPlan["request_materialized"]),
		"provider_request_sent":                      boolOnlyFromAny(invocationPlan["provider_request_sent"]),
		"response_recorded":                          boolOnlyFromAny(invocationPlan["response_recorded"]),
		"transaction_recorded":                       boolOnlyFromAny(invocationPlan["transaction_recorded"]),
		"dependency_update_recorded":                 boolOnlyFromAny(invocationPlan["dependency_update_recorded"]),
		"adapter_implemented":                        boolOnlyFromAny(invocationPlan["adapter_implemented"]),
		"mutation_armed":                             boolOnlyFromAny(invocationPlan["mutation_armed"]),
		"external_call_made":                         boolOnlyFromAny(invocationPlan["external_call_made"]),
		"provider_api_call_made":                     boolOnlyFromAny(invocationPlan["provider_api_call_made"]),
		"provider_api_mutation":                      cleanOptionalText(stringFromMap(invocationPlan, "provider_api_mutation")),
		"request_body_included":                      boolOnlyFromAny(invocationPlan["request_body_included"]),
		"response_body_included":                     boolOnlyFromAny(invocationPlan["response_body_included"]),
		"headers_included":                           boolOnlyFromAny(invocationPlan["headers_included"]),
		"authorization_header_included":              boolOnlyFromAny(invocationPlan["authorization_header_included"]),
		"provider_url_included":                      boolOnlyFromAny(invocationPlan["provider_url_included"]),
		"idempotency_key_included":                   boolOnlyFromAny(invocationPlan["idempotency_key_included"]),
		"provider_request_id_included":               boolOnlyFromAny(invocationPlan["provider_request_id_included"]),
		"contains_token":                             boolOnlyFromAny(invocationPlan["contains_token"]),
		"contains_provider_url":                      boolOnlyFromAny(invocationPlan["contains_provider_url"]),
		"contains_repository_ref":                    boolOnlyFromAny(invocationPlan["contains_repository_ref"]),
		"contains_branch_name":                       boolOnlyFromAny(invocationPlan["contains_branch_name"]),
		"contains_file_content":                      boolOnlyFromAny(invocationPlan["contains_file_content"]),
		"blocked_reasons":                            safeProviderReviewBlueprintNames(stringSliceFromAny(invocationPlan["blocked_reasons"])),
		"status_snapshot_write_eligible":             statusSnapshotWriteEligible,
		"status_snapshot_written":                    false,
		"invocation_boundary_redacted":               boolOnlyFromAny(invocationPlan["invocation_boundary_redacted"]),
		"future_live_provider_invocation_blocked":    true,
		"future_live_provider_request_still_blocked": true,
	}
}

func safeProviderReviewInvocationReadyReason(value string) string {
	switch cleanOptionalText(value) {
	case "provider_api_invocation_not_armed",
		"provider_review_claim_metadata_not_ready",
		"provider_review_execution_lock_not_acquired",
		"provider_review_adapter_activation_not_armed",
		"provider_credential_runtime_binding_not_armed",
		"provider_review_adapter_runtime_not_bound",
		"provider_branch_policy_not_armed",
		"provider_request_not_materialized",
		"provider_api_call_not_made",
		"provider_review_transaction_not_recorded",
		"provider_review_adapter_not_implemented",
		"provider_review_mutation_not_armed":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func providerReviewAttemptInvocationSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "invocation_blocked"
	if boolOnlyFromAny(snapshot["invocation_contract_ready"]) {
		state = "invocation_contract_ready"
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
	if !boolOnlyFromAny(snapshot["invocation_plan_observed"]) {
		missing = append(missing, "provider_review_invocation_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["invocation_contract_ready"]) {
		missing = append(missing, "provider_review_invocation_contract_not_ready")
	}
	if len(stringSliceFromAny(snapshot["required_subplans"])) == 0 {
		missing = append(missing, "provider_review_invocation_required_subplans_missing")
	}
	if len(stringSliceFromAny(snapshot["invocation_sequence"])) == 0 {
		missing = append(missing, "provider_review_invocation_sequence_missing")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["attempt_claim_recorded"]) ||
		boolOnlyFromAny(snapshot["idempotency_claim_recorded"]) ||
		boolOnlyFromAny(snapshot["execution_lock_acquired"]) ||
		boolOnlyFromAny(snapshot["adapter_activation_approved"]) ||
		boolOnlyFromAny(snapshot["duplicate_send_detected"]) ||
		boolOnlyFromAny(snapshot["credential_bound"]) ||
		boolOnlyFromAny(snapshot["adapter_runtime_bound"]) ||
		boolOnlyFromAny(snapshot["branch_policy_verified"]) ||
		boolOnlyFromAny(snapshot["request_materialized"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["response_recorded"]) ||
		boolOnlyFromAny(snapshot["transaction_recorded"]) ||
		boolOnlyFromAny(snapshot["dependency_update_recorded"]) ||
		boolOnlyFromAny(snapshot["adapter_implemented"]) ||
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
		missing = append(missing, "provider_review_invocation_not_no_call")
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptInvocationSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "invocation_contract_ready":
		return "provider_review_attempt_invocation_contract_ready", "low"
	case "invocation_blocked":
		return "provider_review_attempt_invocation_blocked", "warning"
	default:
		return "provider_review_attempt_invocation_unknown", "warning"
	}
}

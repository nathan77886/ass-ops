package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptActivationSnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
	Ledger    map[string]any
}

func RecordProviderReviewAttemptActivationSnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptActivationSnapshotOptions) (map[string]any, error) {
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
	snapshot := providerReviewAttemptActivationSnapshotPayload(attempt, ledger, assetErr == nil)
	ready, state, missing := providerReviewAttemptActivationSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_activation_snapshot_recording",
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
		"provider_review_attempt_activation_snapshot_written": false,
		"asset_status_snapshot_written":                       false,
		"operation_log_written":                               false,
		"external_call_made":                                  false,
		"provider_api_call_made":                              false,
		"provider_api_mutation":                               "disabled",
		"mutation_armed":                                      false,
		"provider_request_sent":                               false,
		"provider_call_boundary_recorded":                     false,
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
		result["message"] = "Provider review attempt activation snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt activation snapshot is waiting for the current execution candidate, activation plan, and provider-call boundary metadata; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt activation snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptActivationSnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "provider review attempt activation snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording provider review attempt activation snapshot recorded: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["provider_review_attempt_activation_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized provider review attempt activation snapshot recorded from local dispatch and provider-call boundary evidence."
	return result, nil
}

func providerReviewAttemptForActivationSnapshot(ctx context.Context, store *Store, attemptID string) (map[string]any, error) {
	return providerReviewAttemptForSnapshot(ctx, store.Gorm, attemptID)
}

func providerReviewAttemptActivationSnapshotPayload(attempt, ledger map[string]any, assetObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	invocationPlan := mapFromAny(dispatchPlan["invocation_plan"])
	activationPlan := mapFromAny(invocationPlan["adapter_activation_plan"])
	transactionPlan := mapFromAny(dispatchPlan["transaction_plan"])
	providerCallBoundaryPlan := mapFromAny(transactionPlan["provider_call_boundary_plan"])
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	activationMetadataReady := providerReviewAttemptActivationPlanReadyForOperation(activationPlan, operationName, endpointKey)
	providerCallBoundaryMetadataReady := boolOnlyFromAny(providerCallBoundaryPlan["provider_call_boundary_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(providerCallBoundaryPlan, "redacted_attempt_adapter_provider_call_boundary_plan", operationName, endpointKey)
	statusSnapshotWriteEligible := assetObserved && candidateMatches && len(activationPlan) > 0 && len(providerCallBoundaryPlan) > 0
	return map[string]any{
		"mode":                                          "provider_review_attempt_activation_snapshot",
		"provider_review_attempt_id":                    cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":                         cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"project_template_run_id":                       cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed":        assetObserved,
		"operation_name":                                operationName,
		"endpoint_key":                                  endpointKey,
		"attempt_status":                                safeProviderReviewAttemptStatus(stringFromMap(attempt, "status")),
		"dependency_status":                             safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(attempt, "dependency_status")),
		"operation_order":                               intFromAny(attempt["operation_order"], 0),
		"candidate_observed":                            len(candidate) > 0,
		"candidate_matches_attempt":                     candidateMatches,
		"candidate_status":                              cleanOptionalText(stringFromMap(candidate, "status")),
		"dispatch_plan_observed":                        len(dispatchPlan) > 0,
		"dispatch_metadata_ready":                       boolOnlyFromAny(dispatchPlan["dispatch_metadata_ready"]),
		"invocation_plan_observed":                      len(invocationPlan) > 0,
		"activation_plan_observed":                      len(activationPlan) > 0,
		"adapter_activation_metadata_ready":             activationMetadataReady,
		"adapter_activation_ready_reason":               cleanOptionalText(stringFromMap(activationPlan, "adapter_activation_metadata_ready_reason")),
		"live_adapter_registered":                       boolOnlyFromAny(activationPlan["live_adapter_registered"]),
		"live_adapter_implemented":                      false,
		"provider_call_boundary_plan_observed":          len(providerCallBoundaryPlan) > 0,
		"provider_call_boundary_metadata_ready":         providerCallBoundaryMetadataReady,
		"transaction_metadata_ready":                    boolOnlyFromAny(transactionPlan["transaction_metadata_ready"]),
		"claim_metadata_ready":                          boolOnlyFromAny(activationPlan["claim_metadata_ready"]),
		"execution_lock_metadata_ready":                 boolOnlyFromAny(activationPlan["execution_lock_metadata_ready"]),
		"credential_binding_ready":                      boolOnlyFromAny(activationPlan["credential_binding_ready"]),
		"adapter_runtime_ready":                         boolOnlyFromAny(activationPlan["adapter_runtime_ready"]),
		"request_materialization_ready":                 boolOnlyFromAny(activationPlan["request_materialization_ready"]),
		"transport_metadata_ready":                      boolOnlyFromAny(activationPlan["transport_metadata_ready"]),
		"provider_send_metadata_ready":                  boolOnlyFromAny(activationPlan["provider_send_metadata_ready"]),
		"response_recording_ready":                      boolOnlyFromAny(activationPlan["response_recording_ready"]),
		"transaction_boundary_ready":                    boolOnlyFromAny(transactionPlan["transaction_metadata_ready"]),
		"adapter_activation_approved":                   false,
		"mutation_gate_armed":                           false,
		"mutation_armed":                                false,
		"provider_request_sent":                         false,
		"provider_call_boundary_recorded":               false,
		"provider_call_started_recorded":                false,
		"provider_call_finished_recorded":               false,
		"external_call_made":                            false,
		"provider_api_call_made":                        false,
		"provider_api_mutation":                         "disabled",
		"operation_log_written":                         false,
		"request_body_included":                         false,
		"response_body_included":                        false,
		"headers_included":                              false,
		"authorization_header_included":                 false,
		"provider_url_included":                         false,
		"idempotency_key_included":                      false,
		"provider_request_id_included":                  false,
		"contains_token":                                false,
		"contains_provider_url":                         false,
		"contains_repository_ref":                       false,
		"contains_branch_name":                          false,
		"contains_file_content":                         false,
		"status_snapshot_write_eligible":                statusSnapshotWriteEligible,
		"status_snapshot_written":                       statusSnapshotWriteEligible,
		"adapter_activation_boundary_redacted":          true,
		"provider_call_boundary_redacted":               true,
		"future_live_provider_activation_still_blocked": true,
	}
}

func providerReviewAttemptActivationSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "activation_blocked"
	if boolOnlyFromAny(snapshot["adapter_activation_metadata_ready"]) && boolOnlyFromAny(snapshot["provider_call_boundary_metadata_ready"]) {
		state = "activation_metadata_ready"
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
	if !boolOnlyFromAny(snapshot["activation_plan_observed"]) {
		missing = append(missing, "provider_review_adapter_activation_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["provider_call_boundary_plan_observed"]) {
		missing = append(missing, "provider_review_provider_call_boundary_plan_missing")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) {
		missing = append(missing, "provider_review_activation_not_no_call")
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptActivationSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "activation_metadata_ready":
		return "provider_review_attempt_activation_metadata_ready", "low"
	case "activation_blocked":
		return "provider_review_attempt_activation_blocked", "warning"
	default:
		return "provider_review_attempt_activation_unknown", "warning"
	}
}

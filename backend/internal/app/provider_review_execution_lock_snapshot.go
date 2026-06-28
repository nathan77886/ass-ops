package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptExecutionLockSnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
	Ledger    map[string]any
}

func RecordProviderReviewAttemptExecutionLockSnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptExecutionLockSnapshotOptions) (map[string]any, error) {
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
	snapshot := providerReviewAttemptExecutionLockSnapshotPayload(attempt, ledger, assetErr == nil)
	ready, state, missing := providerReviewAttemptExecutionLockSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_execution_lock_snapshot_recording",
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
		"provider_review_attempt_execution_lock_snapshot_written": false,
		"asset_status_snapshot_written":                           false,
		"operation_log_written":                                   false,
		"external_call_made":                                      false,
		"provider_api_call_made":                                  false,
		"provider_api_mutation":                                   "disabled",
		"mutation_armed":                                          false,
		"attempt_claim_recorded":                                  false,
		"idempotency_claim_recorded":                              false,
		"execution_lock_acquired":                                 false,
		"duplicate_send_guard_recorded":                           false,
		"provider_request_sent":                                   false,
		"contains_token":                                          false,
		"contains_provider_url":                                   false,
		"contains_repository_ref":                                 false,
		"contains_branch_name":                                    false,
		"contains_file_content":                                   false,
		"canonical_asset_status_snapshot_try":                     false,
		"snapshot_commit_attempted":                               false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt execution lock snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt execution lock snapshot is waiting for the current execution candidate and execution lock metadata; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt execution lock snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptExecutionLockSnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "provider review attempt execution lock snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording provider review attempt execution lock snapshot recorded: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["provider_review_attempt_execution_lock_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized provider review attempt execution lock snapshot recorded from local execution lock metadata."
	return result, nil
}

func providerReviewAttemptExecutionLockSnapshotPayload(attempt, ledger map[string]any, assetObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	invocationPlan := mapFromAny(dispatchPlan["invocation_plan"])
	executionLockPlan := mapFromAny(invocationPlan["execution_lock_plan"])
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	executionLockContractReady := providerReviewAttemptPlanMatchesOperation(executionLockPlan, "redacted_attempt_adapter_execution_lock_plan", operationName, endpointKey)
	executionLockMetadataReady := providerReviewAttemptExecutionLockPlanReadyForOperation(executionLockPlan, operationName, endpointKey)
	// Structural write eligibility is intentionally weaker than recording readiness; readiness also requires the contract and metadata checks below.
	statusSnapshotWriteEligible := assetObserved && candidateMatches && len(executionLockPlan) > 0
	return map[string]any{
		"mode":                                       "provider_review_attempt_execution_lock_snapshot",
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
		"execution_lock_plan_observed":               len(executionLockPlan) > 0,
		"execution_lock_contract_ready":              executionLockContractReady,
		"execution_lock_metadata_ready":              executionLockMetadataReady,
		"execution_lock_state":                       cleanOptionalText(stringFromMap(executionLockPlan, "execution_lock_state")),
		"execution_lock_ready":                       boolOnlyFromAny(executionLockPlan["execution_lock_ready"]),
		"execution_lock_ready_reason":                cleanOptionalText(stringFromMap(executionLockPlan, "execution_lock_ready_reason")),
		"execution_lock_metadata_ready_reason":       cleanOptionalText(stringFromMap(executionLockPlan, "execution_lock_metadata_ready_reason")),
		"lock_scope":                                 cleanOptionalText(stringFromMap(executionLockPlan, "lock_scope")),
		"lock_key_kind":                              cleanOptionalText(stringFromMap(executionLockPlan, "lock_key_kind")),
		"duplicate_send_policy":                      cleanOptionalText(stringFromMap(executionLockPlan, "duplicate_send_policy")),
		"stale_running_policy":                       cleanOptionalText(stringFromMap(executionLockPlan, "stale_running_policy")),
		"requires_attempt_claim":                     boolOnlyFromAny(executionLockPlan["requires_attempt_claim"]),
		"requires_attempt_status_planned":            boolOnlyFromAny(executionLockPlan["requires_attempt_status_planned"]),
		"requires_dependency_ready":                  boolOnlyFromAny(executionLockPlan["requires_dependency_ready"]),
		"requires_optimistic_lock":                   boolOnlyFromAny(executionLockPlan["requires_optimistic_lock"]),
		"requires_idempotency_claim":                 boolOnlyFromAny(executionLockPlan["requires_idempotency_claim"]),
		"requires_database_transaction":              boolOnlyFromAny(executionLockPlan["requires_database_transaction"]),
		"requires_mutation_arming":                   boolOnlyFromAny(executionLockPlan["requires_mutation_arming"]),
		"claim_metadata_ready":                       boolOnlyFromAny(executionLockPlan["claim_metadata_ready"]),
		"transaction_metadata_ready":                 boolOnlyFromAny(executionLockPlan["transaction_metadata_ready"]),
		"attempt_claim_recorded":                     false,
		"idempotency_claim_recorded":                 false,
		"execution_lock_acquired":                    false,
		"optimistic_lock_verified":                   false,
		"duplicate_send_detected":                    false,
		"duplicate_send_guard_recorded":              false,
		"stale_running_recovered":                    false,
		"provider_request_sent":                      false,
		"external_call_made":                         false,
		"provider_api_call_made":                     false,
		"provider_api_mutation":                      "disabled",
		"request_body_included":                      false,
		"response_body_included":                     false,
		"headers_included":                           false,
		"authorization_header_included":              false,
		"provider_url_included":                      false,
		"lock_key_included":                          false,
		"idempotency_key_included":                   false,
		"provider_request_id_included":               false,
		"contains_token":                             false,
		"contains_provider_url":                      false,
		"contains_repository_ref":                    false,
		"contains_branch_name":                       false,
		"contains_file_content":                      false,
		"status_snapshot_write_eligible":             statusSnapshotWriteEligible,
		"status_snapshot_written":                    statusSnapshotWriteEligible,
		"execution_lock_boundary_redacted":           true,
		"future_live_execution_lock_still_blocked":   true,
		"future_live_provider_request_still_blocked": true,
	}
}

func providerReviewAttemptExecutionLockSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "execution_lock_blocked"
	if boolOnlyFromAny(snapshot["execution_lock_metadata_ready"]) {
		state = "execution_lock_metadata_ready"
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
	if !boolOnlyFromAny(snapshot["execution_lock_plan_observed"]) {
		missing = append(missing, "provider_review_execution_lock_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["execution_lock_contract_ready"]) {
		missing = append(missing, "provider_review_execution_lock_contract_not_ready")
	}
	if !boolOnlyFromAny(snapshot["execution_lock_metadata_ready"]) {
		missing = append(missing, "provider_review_execution_lock_metadata_not_ready")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["execution_lock_acquired"]) ||
		boolOnlyFromAny(snapshot["idempotency_claim_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["lock_key_included"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_included"]) ||
		boolOnlyFromAny(snapshot["provider_request_id_included"]) {
		missing = append(missing, "provider_review_execution_lock_not_no_call")
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptExecutionLockSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "execution_lock_metadata_ready":
		return "provider_review_attempt_execution_lock_metadata_ready", "low"
	case "execution_lock_blocked":
		return "provider_review_attempt_execution_lock_blocked", "warning"
	default:
		return "provider_review_attempt_execution_lock_unknown", "warning"
	}
}

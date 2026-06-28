package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptProviderCallBoundarySnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
	Ledger    map[string]any
}

func RecordProviderReviewAttemptProviderCallBoundarySnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptProviderCallBoundarySnapshotOptions) (map[string]any, error) {
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
	snapshot := providerReviewAttemptProviderCallBoundarySnapshotPayload(attempt, ledger, assetErr == nil)
	ready, state, missing := providerReviewAttemptProviderCallBoundarySnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_provider_call_boundary_snapshot_recording",
		"recording_state":                        state,
		"recording_ready":                        ready,
		"recording_enabled":                      ready && !opts.DryRun,
		"dry_run":                                opts.DryRun,
		"provider_review_attempt_id":             attemptID,
		"operation_approval_id":                  approvalID,
		"project_template_run_id":                cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed": assetErr == nil,
		"snapshot":                               snapshot,
		"status_snapshot_write_eligible":         boolOnlyFromAny(snapshot["status_snapshot_write_eligible"]),
		"snapshots_written":                      0,
		"snapshots_skipped_as_duplicate":         0,
		"provider_review_attempt_provider_call_boundary_snapshot_written": false,
		"asset_status_snapshot_written":                                   false,
		"operation_log_written":                                           false,
		"external_call_made":                                              false,
		"provider_api_call_made":                                          false,
		"provider_api_mutation":                                           "disabled",
		"mutation_armed":                                                  false,
		"provider_request_sent":                                           false,
		"provider_response_received":                                      false,
		"provider_call_boundary_opened":                                   false,
		"provider_call_boundary_recorded":                                 false,
		"provider_call_started_recorded":                                  false,
		"provider_call_finished_recorded":                                 false,
		"provider_request_id_recorded":                                    false,
		"provider_response_status_recorded":                               false,
		"provider_response_body_recorded":                                 false,
		"provider_response_headers_recorded":                              false,
		"contains_token":                                                  false,
		"contains_provider_url":                                           false,
		"contains_repository_ref":                                         false,
		"contains_branch_name":                                            false,
		"contains_file_content":                                           false,
		"canonical_asset_status_snapshot_try":                             false,
		"snapshot_commit_attempted":                                       false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt provider-call-boundary snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt provider-call-boundary snapshot is waiting for the current execution candidate and redacted provider-call-boundary metadata; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt provider-call-boundary snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptProviderCallBoundarySnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "provider review attempt provider-call-boundary snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording provider review attempt provider-call-boundary snapshot recorded: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["provider_review_attempt_provider_call_boundary_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized provider review attempt provider-call-boundary snapshot recorded from local boundary metadata."
	return result, nil
}

func providerReviewAttemptProviderCallBoundarySnapshotPayload(attempt, ledger map[string]any, assetObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	transactionPlan := mapFromAny(dispatchPlan["transaction_plan"])
	providerCallBoundaryPlan := mapFromAny(transactionPlan["provider_call_boundary_plan"])
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	providerCallBoundaryMetadataReady := boolOnlyFromAny(providerCallBoundaryPlan["provider_call_boundary_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(providerCallBoundaryPlan, "redacted_attempt_adapter_provider_call_boundary_plan", operationName, endpointKey)
	noCall := !boolOnlyFromAny(providerCallBoundaryPlan["provider_call_boundary_opened"]) &&
		!boolOnlyFromAny(providerCallBoundaryPlan["provider_call_boundary_recorded"]) &&
		!boolOnlyFromAny(providerCallBoundaryPlan["provider_call_started_recorded"]) &&
		!boolOnlyFromAny(providerCallBoundaryPlan["provider_call_finished_recorded"]) &&
		!boolOnlyFromAny(providerCallBoundaryPlan["provider_request_sent"]) &&
		!boolOnlyFromAny(providerCallBoundaryPlan["provider_response_received"]) &&
		!boolOnlyFromAny(providerCallBoundaryPlan["provider_request_id_recorded"]) &&
		!boolOnlyFromAny(providerCallBoundaryPlan["provider_response_status_recorded"]) &&
		!boolOnlyFromAny(providerCallBoundaryPlan["provider_response_body_recorded"]) &&
		!boolOnlyFromAny(providerCallBoundaryPlan["provider_response_headers_recorded"]) &&
		!boolOnlyFromAny(providerCallBoundaryPlan["external_call_made"]) &&
		!boolOnlyFromAny(providerCallBoundaryPlan["provider_api_call_made"]) &&
		stringFromMap(providerCallBoundaryPlan, "provider_api_mutation") == "disabled" &&
		!boolOnlyFromAny(providerCallBoundaryPlan["contains_token"]) &&
		!boolOnlyFromAny(providerCallBoundaryPlan["contains_provider_url"]) &&
		!boolOnlyFromAny(providerCallBoundaryPlan["contains_repository_ref"]) &&
		!boolOnlyFromAny(providerCallBoundaryPlan["contains_branch_name"]) &&
		!boolOnlyFromAny(providerCallBoundaryPlan["contains_file_content"])
	boundarySequence := safeProviderReviewBlueprintNames(stringSliceFromAny(providerCallBoundaryPlan["boundary_sequence"]))
	statusSnapshotWriteEligible := assetObserved && candidateMatches && providerCallBoundaryMetadataReady && noCall && len(boundarySequence) > 0
	return map[string]any{
		"mode":                                    "provider_review_attempt_provider_call_boundary_snapshot",
		"provider_review_attempt_id":              cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":                   cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"project_template_run_id":                 cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed":  assetObserved,
		"operation_name":                          operationName,
		"endpoint_key":                            endpointKey,
		"attempt_status":                          safeProviderReviewAttemptStatus(stringFromMap(attempt, "status")),
		"dependency_status":                       safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(attempt, "dependency_status")),
		"operation_order":                         intFromAny(attempt["operation_order"], 0),
		"candidate_observed":                      len(candidate) > 0,
		"candidate_matches_attempt":               candidateMatches,
		"candidate_status":                        cleanOptionalText(stringFromMap(candidate, "status")),
		"dispatch_plan_observed":                  len(dispatchPlan) > 0,
		"transaction_plan_observed":               len(transactionPlan) > 0,
		"provider_call_boundary_plan_observed":    len(providerCallBoundaryPlan) > 0,
		"provider_call_boundary_metadata_ready":   providerCallBoundaryMetadataReady,
		"provider_call_boundary_ready":            boolOnlyFromAny(providerCallBoundaryPlan["provider_call_boundary_ready"]),
		"provider_call_boundary_ready_reason":     safeProviderReviewProviderCallBoundaryReadyReason(stringFromMap(providerCallBoundaryPlan, "provider_call_boundary_ready_reason")),
		"provider_call_boundary_sequence":         boundarySequence,
		"requires_provider_call_boundary":         boolOnlyFromAny(providerCallBoundaryPlan["requires_provider_call_boundary"]),
		"requires_idempotency_ledger":             boolOnlyFromAny(providerCallBoundaryPlan["requires_idempotency_ledger"]),
		"requires_response_diagnostics":           boolOnlyFromAny(providerCallBoundaryPlan["requires_response_diagnostics"]),
		"requires_database_transaction":           true,
		"mutation_armed":                          false,
		"provider_call_boundary_opened":           boolOnlyFromAny(providerCallBoundaryPlan["provider_call_boundary_opened"]),
		"provider_call_boundary_recorded":         boolOnlyFromAny(providerCallBoundaryPlan["provider_call_boundary_recorded"]),
		"provider_call_started_recorded":          boolOnlyFromAny(providerCallBoundaryPlan["provider_call_started_recorded"]),
		"provider_call_finished_recorded":         boolOnlyFromAny(providerCallBoundaryPlan["provider_call_finished_recorded"]),
		"provider_request_sent":                   boolOnlyFromAny(providerCallBoundaryPlan["provider_request_sent"]),
		"provider_response_received":              boolOnlyFromAny(providerCallBoundaryPlan["provider_response_received"]),
		"provider_request_id_recorded":            boolOnlyFromAny(providerCallBoundaryPlan["provider_request_id_recorded"]),
		"provider_response_status_recorded":       boolOnlyFromAny(providerCallBoundaryPlan["provider_response_status_recorded"]),
		"provider_response_body_recorded":         boolOnlyFromAny(providerCallBoundaryPlan["provider_response_body_recorded"]),
		"provider_response_headers_recorded":      boolOnlyFromAny(providerCallBoundaryPlan["provider_response_headers_recorded"]),
		"external_call_made":                      boolOnlyFromAny(providerCallBoundaryPlan["external_call_made"]),
		"provider_api_call_made":                  boolOnlyFromAny(providerCallBoundaryPlan["provider_api_call_made"]),
		"provider_api_mutation":                   safeProviderReviewSnapshotMutationState(stringFromMap(providerCallBoundaryPlan, "provider_api_mutation")),
		"request_body_included":                   false,
		"response_body_included":                  boolOnlyFromAny(providerCallBoundaryPlan["provider_response_body_recorded"]),
		"headers_included":                        boolOnlyFromAny(providerCallBoundaryPlan["provider_response_headers_recorded"]),
		"authorization_header_included":           false,
		"provider_url_included":                   false,
		"idempotency_key_included":                false,
		"provider_request_id_included":            boolOnlyFromAny(providerCallBoundaryPlan["provider_request_id_recorded"]),
		"contains_token":                          boolOnlyFromAny(providerCallBoundaryPlan["contains_token"]),
		"contains_provider_url":                   boolOnlyFromAny(providerCallBoundaryPlan["contains_provider_url"]),
		"contains_repository_ref":                 boolOnlyFromAny(providerCallBoundaryPlan["contains_repository_ref"]),
		"contains_branch_name":                    boolOnlyFromAny(providerCallBoundaryPlan["contains_branch_name"]),
		"contains_file_content":                   boolOnlyFromAny(providerCallBoundaryPlan["contains_file_content"]),
		"no_call_observed":                        noCall,
		"status_snapshot_write_eligible":          statusSnapshotWriteEligible,
		"status_snapshot_written":                 false,
		"provider_call_boundary_redacted":         true,
		"future_live_provider_call_still_blocked": true,
	}
}

func providerReviewAttemptProviderCallBoundarySnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "provider_call_boundary_blocked"
	if boolOnlyFromAny(snapshot["provider_call_boundary_metadata_ready"]) {
		state = "provider_call_boundary_metadata_ready"
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
	if !boolOnlyFromAny(snapshot["transaction_plan_observed"]) {
		missing = append(missing, "provider_review_transaction_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["provider_call_boundary_plan_observed"]) {
		missing = append(missing, "provider_review_provider_call_boundary_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["provider_call_boundary_metadata_ready"]) {
		missing = append(missing, "provider_review_provider_call_boundary_metadata_not_ready")
	}
	if len(stringSliceFromAny(snapshot["provider_call_boundary_sequence"])) == 0 {
		missing = append(missing, "provider_review_provider_call_boundary_sequence_missing")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["provider_call_boundary_opened"]) ||
		boolOnlyFromAny(snapshot["provider_call_boundary_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_call_started_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_call_finished_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["provider_response_received"]) ||
		boolOnlyFromAny(snapshot["provider_request_id_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_response_status_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_response_body_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_response_headers_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_request_id_included"]) ||
		boolOnlyFromAny(snapshot["response_body_included"]) ||
		boolOnlyFromAny(snapshot["headers_included"]) ||
		boolOnlyFromAny(snapshot["contains_token"]) ||
		boolOnlyFromAny(snapshot["contains_provider_url"]) ||
		boolOnlyFromAny(snapshot["contains_repository_ref"]) ||
		boolOnlyFromAny(snapshot["contains_branch_name"]) ||
		boolOnlyFromAny(snapshot["contains_file_content"]) {
		missing = append(missing, "provider_review_provider_call_boundary_not_no_call")
	}
	if len(missing) > 0 {
		state = "provider_call_boundary_blocked"
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptProviderCallBoundarySnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "provider_call_boundary_metadata_ready":
		return "provider_review_attempt_provider_call_boundary_metadata_ready", "low"
	case "provider_call_boundary_blocked":
		return "provider_review_attempt_provider_call_boundary_blocked", "warning"
	default:
		return "provider_review_attempt_provider_call_boundary_unknown", "warning"
	}
}

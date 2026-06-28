package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptRequestValidationSnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
	Ledger    map[string]any
}

func RecordProviderReviewAttemptRequestValidationSnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptRequestValidationSnapshotOptions) (map[string]any, error) {
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
	snapshot := providerReviewAttemptRequestValidationSnapshotPayload(attempt, ledger, assetErr == nil)
	ready, state, missing := providerReviewAttemptRequestValidationSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_request_validation_snapshot_recording",
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
		"provider_review_attempt_request_validation_snapshot_written": false,
		"asset_status_snapshot_written":                               false,
		"operation_log_written":                                       false,
		"external_call_made":                                          false,
		"provider_api_call_made":                                      false,
		"provider_api_mutation":                                       "disabled",
		"mutation_armed":                                              false,
		"request_validated":                                           false,
		"request_materialized":                                        false,
		"provider_request_sent":                                       false,
		"contains_token":                                              false,
		"contains_provider_url":                                       false,
		"contains_repository_ref":                                     false,
		"contains_branch_name":                                        false,
		"contains_file_content":                                       false,
		"canonical_asset_status_snapshot_try":                         false,
		"snapshot_commit_attempted":                                   false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt request-validation snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt request-validation snapshot is waiting for the current execution candidate and redacted request-validation preflight contract; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt request-validation snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptRequestValidationSnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "provider review attempt request-validation snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording provider review attempt request-validation snapshot recorded: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["provider_review_attempt_request_validation_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized provider review attempt request-validation snapshot recorded from local redacted preflight metadata."
	return result, nil
}

func providerReviewAttemptRequestValidationSnapshotPayload(attempt, ledger map[string]any, assetObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	preflight := mapFromAny(dispatchPlan["request_validation_preflight"])
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	preflightContractReady := providerReviewAttemptPlanMatchesOperation(preflight, "redacted_attempt_adapter_request_validation_preflight", operationName, endpointKey)
	statusSnapshotWriteEligible := assetObserved &&
		candidateMatches &&
		preflightContractReady &&
		boolOnlyFromAny(preflight["dispatch_metadata_ready"]) &&
		boolOnlyFromAny(preflight["attempt_claim_metadata_ready"]) &&
		boolOnlyFromAny(preflight["idempotency_metadata_ready"])
	return map[string]any{
		"mode":                                         "provider_review_attempt_request_validation_snapshot",
		"provider_review_attempt_id":                   cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":                        cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"project_template_run_id":                      cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed":       assetObserved,
		"operation_name":                               operationName,
		"endpoint_key":                                 endpointKey,
		"attempt_status":                               safeProviderReviewAttemptStatus(stringFromMap(attempt, "status")),
		"dependency_status":                            safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(attempt, "dependency_status")),
		"operation_order":                              intFromAny(attempt["operation_order"], 0),
		"candidate_observed":                           len(candidate) > 0,
		"candidate_matches_attempt":                    candidateMatches,
		"candidate_status":                             cleanOptionalText(stringFromMap(candidate, "status")),
		"dispatch_plan_observed":                       len(dispatchPlan) > 0,
		"request_validation_preflight_observed":        len(preflight) > 0,
		"request_validation_contract_ready":            preflightContractReady,
		"preflight_state":                              cleanOptionalText(stringFromMap(preflight, "preflight_state")),
		"preflight_ready":                              boolOnlyFromAny(preflight["preflight_ready"]),
		"preflight_ready_reason":                       cleanOptionalText(stringFromMap(preflight, "preflight_ready_reason")),
		"dispatch_metadata_ready":                      boolOnlyFromAny(preflight["dispatch_metadata_ready"]),
		"attempt_claim_metadata_ready":                 boolOnlyFromAny(preflight["attempt_claim_metadata_ready"]),
		"idempotency_metadata_ready":                   boolOnlyFromAny(preflight["idempotency_metadata_ready"]),
		"request_materialization_ready":                boolOnlyFromAny(preflight["request_materialization_ready"]),
		"branch_policy_metadata_ready":                 boolOnlyFromAny(preflight["branch_policy_metadata_ready"]),
		"credential_binding_ready":                     boolOnlyFromAny(preflight["credential_binding_ready"]),
		"transport_metadata_ready":                     boolOnlyFromAny(preflight["transport_metadata_ready"]),
		"request_envelope_contract_ready":              boolOnlyFromAny(preflight["request_envelope_contract_ready"]),
		"request_envelope_metadata_ready":              boolOnlyFromAny(preflight["request_envelope_metadata_ready"]),
		"response_recording_ready":                     boolOnlyFromAny(preflight["response_recording_ready"]),
		"transaction_metadata_ready":                   boolOnlyFromAny(preflight["transaction_metadata_ready"]),
		"protected_branch_policy_check":                false,
		"token_env_check":                              false,
		"request_validated":                            false,
		"request_materialized":                         false,
		"provider_request_sent":                        false,
		"external_call_made":                           false,
		"provider_api_call_made":                       false,
		"provider_api_mutation":                        "disabled",
		"mutation_armed":                               false,
		"request_body_included":                        false,
		"headers_included":                             false,
		"authorization_header_included":                false,
		"provider_url_included":                        false,
		"repository_ref_included":                      false,
		"branch_name_included":                         false,
		"file_content_included":                        false,
		"idempotency_key_included":                     false,
		"provider_request_id_included":                 false,
		"contains_token":                               false,
		"contains_provider_url":                        false,
		"contains_repository_ref":                      false,
		"contains_branch_name":                         false,
		"contains_file_content":                        false,
		"requires_request_materialization":             boolOnlyFromAny(preflight["requires_request_materialization"]),
		"requires_branch_policy_verification":          boolOnlyFromAny(preflight["requires_branch_policy_verification"]),
		"requires_credential_binding":                  boolOnlyFromAny(preflight["requires_credential_binding"]),
		"requires_transport_metadata":                  boolOnlyFromAny(preflight["requires_transport_metadata"]),
		"requires_response_recording":                  boolOnlyFromAny(preflight["requires_response_recording"]),
		"requires_transaction_boundary":                boolOnlyFromAny(preflight["requires_transaction_boundary"]),
		"requires_mutation_arming":                     boolOnlyFromAny(preflight["requires_mutation_arming"]),
		"blocked_reasons":                              safeProviderReviewBlueprintNames(stringSliceFromAny(preflight["blocked_reasons"])),
		"status_snapshot_write_eligible":               statusSnapshotWriteEligible,
		"status_snapshot_written":                      statusSnapshotWriteEligible,
		"request_validation_boundary_redacted":         true,
		"future_live_request_validation_still_blocked": true,
		"future_live_provider_request_still_blocked":   true,
	}
}

func providerReviewAttemptRequestValidationSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "request_validation_blocked"
	if boolOnlyFromAny(snapshot["request_validation_contract_ready"]) &&
		boolOnlyFromAny(snapshot["dispatch_metadata_ready"]) {
		state = "request_validation_metadata_ready"
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
	if !boolOnlyFromAny(snapshot["request_validation_preflight_observed"]) {
		missing = append(missing, "provider_review_request_validation_preflight_missing")
	}
	if !boolOnlyFromAny(snapshot["request_validation_contract_ready"]) {
		missing = append(missing, "provider_review_request_validation_contract_not_ready")
	}
	if !boolOnlyFromAny(snapshot["dispatch_metadata_ready"]) {
		missing = append(missing, "provider_review_dispatch_metadata_not_ready")
	}
	if !boolOnlyFromAny(snapshot["attempt_claim_metadata_ready"]) {
		missing = append(missing, "provider_review_claim_metadata_not_ready")
	}
	if !boolOnlyFromAny(snapshot["idempotency_metadata_ready"]) {
		missing = append(missing, "provider_review_idempotency_metadata_not_ready")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["request_validated"]) ||
		boolOnlyFromAny(snapshot["request_materialized"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["request_body_included"]) ||
		boolOnlyFromAny(snapshot["headers_included"]) ||
		boolOnlyFromAny(snapshot["authorization_header_included"]) ||
		boolOnlyFromAny(snapshot["provider_url_included"]) ||
		boolOnlyFromAny(snapshot["repository_ref_included"]) ||
		boolOnlyFromAny(snapshot["branch_name_included"]) ||
		boolOnlyFromAny(snapshot["file_content_included"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_included"]) ||
		boolOnlyFromAny(snapshot["provider_request_id_included"]) {
		missing = append(missing, "provider_review_request_validation_not_no_call")
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptRequestValidationSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "request_validation_metadata_ready":
		return "provider_review_attempt_request_validation_metadata_ready", "low"
	case "request_validation_blocked":
		return "provider_review_attempt_request_validation_blocked", "warning"
	default:
		return "provider_review_attempt_request_validation_unknown", "warning"
	}
}

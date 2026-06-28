package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptRequestMaterializationSnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
	Ledger    map[string]any
}

func RecordProviderReviewAttemptRequestMaterializationSnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptRequestMaterializationSnapshotOptions) (map[string]any, error) {
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
	snapshot := providerReviewAttemptRequestMaterializationSnapshotPayload(attempt, ledger, assetErr == nil)
	ready, state, missing := providerReviewAttemptRequestMaterializationSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_request_materialization_snapshot_recording",
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
		"provider_review_attempt_request_materialization_snapshot_written": false,
		"asset_status_snapshot_written":                                    false,
		"operation_log_written":                                            false,
		"external_call_made":                                               false,
		"provider_api_call_made":                                           false,
		"provider_api_mutation":                                            "disabled",
		"mutation_armed":                                                   false,
		"request_materialized":                                             false,
		"request_validated":                                                false,
		"provider_request_sent":                                            false,
		"request_body_included":                                            false,
		"headers_included":                                                 false,
		"authorization_header_included":                                    false,
		"provider_url_included":                                            false,
		"contains_token":                                                   false,
		"contains_provider_url":                                            false,
		"contains_repository_ref":                                          false,
		"contains_branch_name":                                             false,
		"contains_file_content":                                            false,
		"canonical_asset_status_snapshot_try":                              false,
		"snapshot_commit_attempted":                                        false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt request-materialization snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt request-materialization snapshot is waiting for the current execution candidate and redacted request-materialization contract; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt request-materialization snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptRequestMaterializationSnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "provider review attempt request-materialization snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording provider review attempt request-materialization snapshot recorded: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["provider_review_attempt_request_materialization_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized provider review attempt request-materialization snapshot recorded from local redacted adapter request metadata."
	return result, nil
}

func providerReviewAttemptRequestMaterializationSnapshotPayload(attempt, ledger map[string]any, assetObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	requestPlan := mapFromAny(dispatchPlan["request_materialization_plan"])
	preflight := mapFromAny(dispatchPlan["request_validation_preflight"])
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	requestContractReady := providerReviewAttemptPlanMatchesOperation(requestPlan, providerReviewAttemptAdapterRequestMaterializationPlanMode, operationName, endpointKey)
	noCall := !boolOnlyFromAny(requestPlan["request_path_materialized"]) &&
		!boolOnlyFromAny(requestPlan["request_url_materialized"]) &&
		!boolOnlyFromAny(requestPlan["request_body_materialized"]) &&
		!boolOnlyFromAny(requestPlan["payload_materialized"]) &&
		!boolOnlyFromAny(requestPlan["headers_materialized"]) &&
		!boolOnlyFromAny(requestPlan["starter_file_manifest_materialized"]) &&
		!boolOnlyFromAny(requestPlan["authorization_header_materialized"]) &&
		!boolOnlyFromAny(requestPlan["external_call_made"]) &&
		!boolOnlyFromAny(requestPlan["provider_api_call_made"]) &&
		stringFromMap(requestPlan, "provider_api_mutation") == "disabled" &&
		!boolOnlyFromAny(requestPlan["request_body_included"]) &&
		!boolOnlyFromAny(requestPlan["headers_included"]) &&
		!boolOnlyFromAny(requestPlan["provider_url_included"]) &&
		!boolOnlyFromAny(requestPlan["repository_ref_included"]) &&
		!boolOnlyFromAny(requestPlan["branch_name_included"]) &&
		!boolOnlyFromAny(requestPlan["file_content_included"])
	statusSnapshotWriteEligible := assetObserved && candidateMatches && requestContractReady && noCall
	return map[string]any{
		"mode":                                              "provider_review_attempt_request_materialization_snapshot",
		"provider_review_attempt_id":                        cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":                             cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"project_template_run_id":                           cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed":            assetObserved,
		"operation_name":                                    operationName,
		"endpoint_key":                                      endpointKey,
		"attempt_status":                                    safeProviderReviewAttemptStatus(stringFromMap(attempt, "status")),
		"dependency_status":                                 safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(attempt, "dependency_status")),
		"operation_order":                                   intFromAny(attempt["operation_order"], 0),
		"candidate_observed":                                len(candidate) > 0,
		"candidate_matches_attempt":                         candidateMatches,
		"candidate_status":                                  cleanOptionalText(stringFromMap(candidate, "status")),
		"dispatch_plan_observed":                            len(dispatchPlan) > 0,
		"request_materialization_plan_observed":             len(requestPlan) > 0,
		"request_materialization_contract_ready":            requestContractReady,
		"request_materialization_state":                     cleanOptionalText(stringFromMap(requestPlan, "request_materialization_state")),
		"request_materialization_ready":                     boolOnlyFromAny(requestPlan["request_materialization_ready"]),
		"request_materialization_ready_reason":              cleanOptionalText(stringFromMap(requestPlan, "request_materialization_ready_reason")),
		"provider_type":                                     safeProviderReviewProviderType(stringFromMap(requestPlan, "provider_type")),
		"method":                                            cleanOptionalText(stringFromMap(requestPlan, "method")),
		"endpoint_path_template_key":                        cleanOptionalText(stringFromMap(requestPlan, "endpoint_path_template_key")),
		"payload_shape":                                     cleanOptionalText(stringFromMap(requestPlan, "payload_shape")),
		"payload_builder":                                   cleanOptionalText(stringFromMap(requestPlan, "payload_builder")),
		"requires_request_builder":                          boolOnlyFromAny(requestPlan["requires_request_builder"]),
		"requires_provider_repository_context":              boolOnlyFromAny(requestPlan["requires_provider_repository_context"]),
		"requires_redacted_payload_summary":                 boolOnlyFromAny(requestPlan["requires_redacted_payload_summary"]),
		"requires_starter_file_manifest":                    boolOnlyFromAny(requestPlan["requires_starter_file_manifest"]),
		"requires_mutation_arming":                          boolOnlyFromAny(requestPlan["requires_mutation_arming"]),
		"request_builder_implemented":                       boolOnlyFromAny(requestPlan["request_builder_implemented"]),
		"provider_repository_context_resolved":              boolOnlyFromAny(requestPlan["provider_repository_context_resolved"]),
		"request_validation_preflight_observed":             len(preflight) > 0,
		"request_validation_contract_ready":                 providerReviewAttemptPlanMatchesOperation(preflight, "redacted_attempt_adapter_request_validation_preflight", operationName, endpointKey),
		"request_validation_metadata_ready":                 boolOnlyFromAny(preflight["dispatch_metadata_ready"]) && boolOnlyFromAny(preflight["attempt_claim_metadata_ready"]) && boolOnlyFromAny(preflight["idempotency_metadata_ready"]),
		"request_path_materialized":                         boolOnlyFromAny(requestPlan["request_path_materialized"]),
		"request_url_materialized":                          boolOnlyFromAny(requestPlan["request_url_materialized"]),
		"request_body_materialized":                         boolOnlyFromAny(requestPlan["request_body_materialized"]),
		"payload_materialized":                              boolOnlyFromAny(requestPlan["payload_materialized"]),
		"headers_materialized":                              boolOnlyFromAny(requestPlan["headers_materialized"]),
		"starter_file_manifest_materialized":                boolOnlyFromAny(requestPlan["starter_file_manifest_materialized"]),
		"authorization_header_materialized":                 boolOnlyFromAny(requestPlan["authorization_header_materialized"]),
		"request_materialized":                              false,
		"request_validated":                                 false,
		"provider_request_sent":                             false,
		"external_call_made":                                boolOnlyFromAny(requestPlan["external_call_made"]),
		"provider_api_call_made":                            boolOnlyFromAny(requestPlan["provider_api_call_made"]),
		"provider_api_mutation":                             cleanOptionalText(stringFromMap(requestPlan, "provider_api_mutation")),
		"mutation_armed":                                    false,
		"request_body_included":                             boolOnlyFromAny(requestPlan["request_body_included"]),
		"headers_included":                                  boolOnlyFromAny(requestPlan["headers_included"]),
		"authorization_header_included":                     false,
		"provider_url_included":                             boolOnlyFromAny(requestPlan["provider_url_included"]),
		"repository_ref_included":                           boolOnlyFromAny(requestPlan["repository_ref_included"]),
		"branch_name_included":                              boolOnlyFromAny(requestPlan["branch_name_included"]),
		"file_content_included":                             boolOnlyFromAny(requestPlan["file_content_included"]),
		"idempotency_key_included":                          false,
		"provider_request_id_included":                      false,
		"contains_token":                                    boolOnlyFromAny(requestPlan["contains_token"]),
		"contains_provider_url":                             boolOnlyFromAny(requestPlan["contains_provider_url"]),
		"contains_repository_ref":                           boolOnlyFromAny(requestPlan["contains_repository_ref"]),
		"contains_branch_name":                              boolOnlyFromAny(requestPlan["contains_branch_name"]),
		"contains_file_content":                             boolOnlyFromAny(requestPlan["contains_file_content"]),
		"blocked_reasons":                                   safeProviderReviewBlueprintNames(stringSliceFromAny(requestPlan["blocked_reasons"])),
		"status_snapshot_write_eligible":                    statusSnapshotWriteEligible,
		"status_snapshot_written":                           statusSnapshotWriteEligible,
		"request_materialization_boundary_redacted":         boolOnlyFromAny(requestPlan["request_materialization_boundary_redacted"]),
		"future_live_request_materialization_still_blocked": true,
		"future_live_provider_request_still_blocked":        true,
	}
}

func providerReviewAttemptRequestMaterializationSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "request_materialization_blocked"
	if boolOnlyFromAny(snapshot["request_materialization_contract_ready"]) {
		state = "request_materialization_contract_ready"
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
	if !boolOnlyFromAny(snapshot["request_materialization_plan_observed"]) {
		missing = append(missing, "provider_review_request_materialization_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["request_materialization_contract_ready"]) {
		missing = append(missing, "provider_review_request_materialization_contract_not_ready")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["request_materialized"]) ||
		boolOnlyFromAny(snapshot["request_validated"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["request_path_materialized"]) ||
		boolOnlyFromAny(snapshot["request_url_materialized"]) ||
		boolOnlyFromAny(snapshot["request_body_materialized"]) ||
		boolOnlyFromAny(snapshot["payload_materialized"]) ||
		boolOnlyFromAny(snapshot["headers_materialized"]) ||
		boolOnlyFromAny(snapshot["starter_file_manifest_materialized"]) ||
		boolOnlyFromAny(snapshot["authorization_header_materialized"]) ||
		boolOnlyFromAny(snapshot["request_body_included"]) ||
		boolOnlyFromAny(snapshot["headers_included"]) ||
		boolOnlyFromAny(snapshot["authorization_header_included"]) ||
		boolOnlyFromAny(snapshot["provider_url_included"]) ||
		boolOnlyFromAny(snapshot["repository_ref_included"]) ||
		boolOnlyFromAny(snapshot["branch_name_included"]) ||
		boolOnlyFromAny(snapshot["file_content_included"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_included"]) ||
		boolOnlyFromAny(snapshot["provider_request_id_included"]) {
		missing = append(missing, "provider_review_request_materialization_not_no_call")
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptRequestMaterializationSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "request_materialization_contract_ready":
		return "provider_review_attempt_request_materialization_contract_ready", "low"
	case "request_materialization_blocked":
		return "provider_review_attempt_request_materialization_blocked", "warning"
	default:
		return "provider_review_attempt_request_materialization_unknown", "warning"
	}
}

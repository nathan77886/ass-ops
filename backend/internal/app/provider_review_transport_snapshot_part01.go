package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptTransportSnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
	Ledger    map[string]any
}

func RecordProviderReviewAttemptTransportSnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptTransportSnapshotOptions) (map[string]any, error) {
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
	assetObserved := assetErr == nil && assetID != ""
	snapshot := providerReviewAttemptTransportSnapshotPayload(attempt, ledger, assetObserved)
	ready, state, missing := providerReviewAttemptTransportSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_transport_snapshot_recording",
		"recording_state":                        state,
		"recording_ready":                        ready,
		"recording_enabled":                      ready && !opts.DryRun,
		"dry_run":                                opts.DryRun,
		"provider_review_attempt_id":             attemptID,
		"operation_approval_id":                  approvalID,
		"project_template_run_id":                cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed": assetObserved,
		"snapshot":                               snapshot,
		"status_snapshot_write_eligible":         boolOnlyFromAny(snapshot["status_snapshot_write_eligible"]),
		"snapshots_written":                      0,
		"snapshots_skipped_as_duplicate":         0,
		"provider_review_attempt_transport_snapshot_written": false,
		"asset_status_snapshot_written":                      false,
		"operation_log_written":                              false,
		"external_call_made":                                 false,
		"provider_api_call_made":                             false,
		"provider_api_mutation":                              "disabled",
		"mutation_armed":                                     false,
		"provider_request_sent":                              false,
		"provider_response_received":                         false,
		"provider_client_bound":                              false,
		"request_path_materialized":                          false,
		"request_url_materialized":                           false,
		"request_body_materialized":                          false,
		"headers_materialized":                               false,
		"authorization_header_materialized":                  false,
		"contains_token":                                     false,
		"contains_provider_url":                              false,
		"contains_repository_ref":                            false,
		"contains_branch_name":                               false,
		"contains_file_content":                              false,
		"canonical_asset_status_snapshot_try":                false,
		"snapshot_commit_attempted":                          false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if !assetObserved {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt transport snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt transport snapshot is waiting for the current execution candidate and redacted transport metadata; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt transport snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptTransportSnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "provider review attempt transport snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording provider review attempt transport snapshot recorded: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["provider_review_attempt_transport_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized provider review attempt transport snapshot recorded from local transport metadata."
	return result, nil
}

func providerReviewAttemptTransportSnapshotPayload(attempt, ledger map[string]any, assetObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	requestPlan := mapFromAny(dispatchPlan["request_materialization_plan"])
	credentialPlan := mapFromAny(dispatchPlan["credential_binding_plan"])
	runtimePlan := mapFromAny(dispatchPlan["adapter_runtime_plan"])
	transportPlan := mapFromAny(dispatchPlan["transport_plan"])
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	transportMetadataReady := providerReviewAttemptTransportPlanReadyForOperation(transportPlan, operationName, endpointKey)
	noCall := !boolOnlyFromAny(transportPlan["provider_client_bound"]) &&
		!boolOnlyFromAny(transportPlan["credential_bound"]) &&
		!boolOnlyFromAny(transportPlan["runtime_bound"]) &&
		!boolOnlyFromAny(transportPlan["request_path_materialized"]) &&
		!boolOnlyFromAny(transportPlan["request_url_materialized"]) &&
		!boolOnlyFromAny(transportPlan["request_body_materialized"]) &&
		!boolOnlyFromAny(transportPlan["headers_materialized"]) &&
		!boolOnlyFromAny(transportPlan["authorization_header_materialized"]) &&
		!boolOnlyFromAny(transportPlan["provider_request_sent"]) &&
		!boolOnlyFromAny(transportPlan["provider_response_received"]) &&
		!boolOnlyFromAny(transportPlan["external_call_made"]) &&
		!boolOnlyFromAny(transportPlan["provider_api_call_made"]) &&
		stringFromMap(transportPlan, "provider_api_mutation") == "disabled" &&
		!boolOnlyFromAny(transportPlan["request_body_included"]) &&
		!boolOnlyFromAny(transportPlan["response_body_included"]) &&
		!boolOnlyFromAny(transportPlan["headers_included"]) &&
		!boolOnlyFromAny(transportPlan["authorization_header_included"]) &&
		!boolOnlyFromAny(transportPlan["provider_url_included"]) &&
		!boolOnlyFromAny(transportPlan["idempotency_key_included"]) &&
		!boolOnlyFromAny(transportPlan["provider_request_id_included"]) &&
		!boolOnlyFromAny(transportPlan["contains_token"]) &&
		!boolOnlyFromAny(transportPlan["contains_provider_url"]) &&
		!boolOnlyFromAny(transportPlan["contains_repository_ref"]) &&
		!boolOnlyFromAny(transportPlan["contains_branch_name"]) &&
		!boolOnlyFromAny(transportPlan["contains_file_content"])
	expectedClasses := safeProviderReviewStatusClasses(stringSliceFromAny(transportPlan["expected_success_classes"]))
	retryableClasses := safeProviderReviewStatusClasses(stringSliceFromAny(transportPlan["retryable_status_classes"]))
	diagnosticFields := safeProviderReviewBlueprintNames(stringSliceFromAny(transportPlan["diagnostic_fields"]))
	statusSnapshotWriteEligible := assetObserved &&
		candidateMatches &&
		transportMetadataReady &&
		noCall &&
		len(transportPlan) > 0 &&
		cleanOptionalText(stringFromMap(transportPlan, "method")) != "" &&
		cleanOptionalText(stringFromMap(transportPlan, "payload_shape")) != "" &&
		cleanOptionalText(stringFromMap(transportPlan, "auth_scheme")) != "" &&
		cleanOptionalText(stringFromMap(transportPlan, "accept_header")) != "" &&
		cleanOptionalText(stringFromMap(transportPlan, "content_type")) != "" &&
		intFromAny(transportPlan["timeout_seconds"], 0) > 0 &&
		len(expectedClasses) > 0 &&
		len(retryableClasses) > 0 &&
		len(diagnosticFields) > 0
	return map[string]any{
		"mode":                                       "provider_review_attempt_transport_snapshot",
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
		"request_materialization_plan_observed":      len(requestPlan) > 0,
		"credential_binding_plan_observed":           len(credentialPlan) > 0,
		"adapter_runtime_plan_observed":              len(runtimePlan) > 0,
		"transport_plan_observed":                    len(transportPlan) > 0,
		"transport_metadata_ready":                   transportMetadataReady,
		"transport_ready":                            boolOnlyFromAny(transportPlan["transport_ready"]),
		"transport_ready_reason":                     safeProviderReviewTransportReadyReason(stringFromMap(transportPlan, "transport_ready_reason")),
		"provider_type":                              safeProviderReviewProviderType(stringFromMap(transportPlan, "provider_type")),
		"method":                                     cleanOptionalText(stringFromMap(transportPlan, "method")),
		"payload_shape":                              cleanOptionalText(stringFromMap(transportPlan, "payload_shape")),
		"auth_scheme":                                cleanOptionalText(stringFromMap(transportPlan, "auth_scheme")),
		"accept_header":                              cleanOptionalText(stringFromMap(transportPlan, "accept_header")),
		"content_type":                               cleanOptionalText(stringFromMap(transportPlan, "content_type")),
		"timeout_seconds":                            intFromAny(transportPlan["timeout_seconds"], 0),
		"expected_success_classes":                   expectedClasses,
		"retryable_status_classes":                   retryableClasses,
		"diagnostic_fields":                          diagnosticFields,
		"auth_header_redacted":                       boolOnlyFromAny(transportPlan["auth_header_redacted"]),
		"provider_client_bound":                      boolOnlyFromAny(transportPlan["provider_client_bound"]),
		"credential_bound":                           boolOnlyFromAny(transportPlan["credential_bound"]),
		"runtime_bound":                              boolOnlyFromAny(transportPlan["runtime_bound"]),
		"request_path_materialized":                  boolOnlyFromAny(transportPlan["request_path_materialized"]),
		"request_url_materialized":                   boolOnlyFromAny(transportPlan["request_url_materialized"]),
		"request_body_materialized":                  boolOnlyFromAny(transportPlan["request_body_materialized"]),
		"headers_materialized":                       boolOnlyFromAny(transportPlan["headers_materialized"]),
		"authorization_header_materialized":          boolOnlyFromAny(transportPlan["authorization_header_materialized"]),
		"provider_request_sent":                      boolOnlyFromAny(transportPlan["provider_request_sent"]),
		"provider_response_received":                 boolOnlyFromAny(transportPlan["provider_response_received"]),
		"external_call_made":                         boolOnlyFromAny(transportPlan["external_call_made"]),
		"provider_api_call_made":                     boolOnlyFromAny(transportPlan["provider_api_call_made"]),
		"provider_api_mutation":                      safeProviderReviewSnapshotMutationState(stringFromMap(transportPlan, "provider_api_mutation")),
		"mutation_armed":                             false,
		"request_body_included":                      boolOnlyFromAny(transportPlan["request_body_included"]),
		"response_body_included":                     boolOnlyFromAny(transportPlan["response_body_included"]),
		"headers_included":                           boolOnlyFromAny(transportPlan["headers_included"]),
		"authorization_header_included":              boolOnlyFromAny(transportPlan["authorization_header_included"]),
		"provider_url_included":                      boolOnlyFromAny(transportPlan["provider_url_included"]),
		"idempotency_key_included":                   boolOnlyFromAny(transportPlan["idempotency_key_included"]),
		"provider_request_id_included":               boolOnlyFromAny(transportPlan["provider_request_id_included"]),
		"contains_token":                             boolOnlyFromAny(transportPlan["contains_token"]),
		"contains_provider_url":                      boolOnlyFromAny(transportPlan["contains_provider_url"]),
		"contains_repository_ref":                    boolOnlyFromAny(transportPlan["contains_repository_ref"]),
		"contains_branch_name":                       boolOnlyFromAny(transportPlan["contains_branch_name"]),
		"contains_file_content":                      boolOnlyFromAny(transportPlan["contains_file_content"]),
		"no_call_observed":                           noCall,
		"status_snapshot_write_eligible":             statusSnapshotWriteEligible,
		"status_snapshot_written":                    false,
		"transport_boundary_redacted":                true,
		"future_live_provider_request_still_blocked": true,
	}
}

func safeProviderReviewTransportReadyReason(value string) string {
	switch cleanOptionalText(value) {
	case "ready",
		"provider_api_transport_not_armed",
		"provider_adapter_not_implemented",
		"provider_credentials_not_bound",
		"provider_runtime_not_bound":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

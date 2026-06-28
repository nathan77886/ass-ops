package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptSendSnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
	Ledger    map[string]any
}

func RecordProviderReviewAttemptSendSnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptSendSnapshotOptions) (map[string]any, error) {
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
	snapshot := providerReviewAttemptSendSnapshotPayload(attempt, ledger, assetErr == nil)
	ready, state, missing := providerReviewAttemptSendSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_send_snapshot_recording",
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
		"provider_review_attempt_send_snapshot_written": false,
		"asset_status_snapshot_written":                 false,
		"operation_log_written":                         false,
		"external_call_made":                            false,
		"provider_api_call_made":                        false,
		"provider_api_mutation":                         "disabled",
		"mutation_armed":                                false,
		"provider_request_sent":                         false,
		"send_attempted":                                false,
		"provider_response_received":                    false,
		"contains_token":                                false,
		"contains_provider_url":                         false,
		"contains_repository_ref":                       false,
		"contains_branch_name":                          false,
		"contains_file_content":                         false,
		"canonical_asset_status_snapshot_try":           false,
		"snapshot_commit_attempted":                     false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt send snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt send snapshot is waiting for the current execution candidate, provider-send plan, transport plan, and retry/backoff metadata; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt send snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptSendSnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "provider review attempt send snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording provider review attempt send snapshot recorded: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["provider_review_attempt_send_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized provider review attempt send snapshot recorded from local provider-send, transport, and retry/backoff metadata."
	return result, nil
}

func providerReviewAttemptSendSnapshotPayload(attempt, ledger map[string]any, assetObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	invocationPlan := mapFromAny(dispatchPlan["invocation_plan"])
	requestPlan := mapFromAny(dispatchPlan["request_materialization_plan"])
	transportPlan := mapFromAny(dispatchPlan["transport_plan"])
	credentialPlan := mapFromAny(dispatchPlan["credential_binding_plan"])
	runtimePlan := mapFromAny(dispatchPlan["adapter_runtime_plan"])
	providerSendPlan := mapFromAny(invocationPlan["provider_send_plan"])
	retryBackoffPlan := mapFromAny(providerSendPlan["retry_backoff_plan"])
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	providerSendMetadataReady := providerReviewAttemptProviderSendPlanReadyForOperation(providerSendPlan, operationName, endpointKey)
	transportReady := providerReviewAttemptTransportPlanReadyForOperation(transportPlan, operationName, endpointKey)
	retryBackoffReady := boolOnlyFromAny(retryBackoffPlan["retry_backoff_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(retryBackoffPlan, "redacted_attempt_adapter_retry_backoff_plan", operationName, endpointKey)
	statusSnapshotWriteEligible := assetObserved && candidateMatches && len(providerSendPlan) > 0 && len(transportPlan) > 0 && len(retryBackoffPlan) > 0
	return map[string]any{
		"mode":                                       "provider_review_attempt_send_snapshot",
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
		"request_materialization_plan_observed":      len(requestPlan) > 0,
		"transport_plan_observed":                    len(transportPlan) > 0,
		"credential_binding_plan_observed":           len(credentialPlan) > 0,
		"adapter_runtime_plan_observed":              len(runtimePlan) > 0,
		"provider_send_plan_observed":                len(providerSendPlan) > 0,
		"retry_backoff_plan_observed":                len(retryBackoffPlan) > 0,
		"provider_send_metadata_ready":               providerSendMetadataReady,
		"transport_metadata_ready":                   transportReady,
		"retry_backoff_metadata_ready":               retryBackoffReady,
		"request_materialization_ready":              boolOnlyFromAny(providerSendPlan["request_materialization_ready"]),
		"credential_binding_ready":                   boolOnlyFromAny(providerSendPlan["credential_binding_ready"]),
		"adapter_runtime_ready":                      boolOnlyFromAny(providerSendPlan["adapter_runtime_ready"]),
		"method":                                     cleanOptionalText(stringFromMap(providerSendPlan, "method")),
		"payload_shape":                              cleanOptionalText(stringFromMap(providerSendPlan, "payload_shape")),
		"auth_scheme":                                cleanOptionalText(stringFromMap(providerSendPlan, "auth_scheme")),
		"content_type":                               cleanOptionalText(stringFromMap(providerSendPlan, "content_type")),
		"timeout_seconds":                            intFromAny(providerSendPlan["timeout_seconds"], 0),
		"expected_success_classes":                   safeProviderReviewStatusClasses(stringSliceFromAny(transportPlan["expected_success_classes"])),
		"retryable_status_classes":                   safeProviderReviewStatusClasses(stringSliceFromAny(retryBackoffPlan["retryable_status_classes"])),
		"transport_retryable_status_classes":         safeProviderReviewStatusClasses(stringSliceFromAny(retryBackoffPlan["transport_retryable_status_classes"])),
		"retry_policy":                               safeProviderReviewRetryPolicy(stringFromMap(retryBackoffPlan, "retry_policy")),
		"max_attempts":                               intFromAny(retryBackoffPlan["max_attempts"], 0),
		"initial_backoff_seconds":                    intFromAny(retryBackoffPlan["initial_backoff_seconds"], 0),
		"max_backoff_seconds":                        intFromAny(retryBackoffPlan["max_backoff_seconds"], 0),
		"jitter":                                     cleanOptionalText(stringFromMap(retryBackoffPlan, "jitter")),
		"request_path_materialized":                  false,
		"request_url_materialized":                   false,
		"request_body_materialized":                  false,
		"headers_materialized":                       false,
		"authorization_header_materialized":          false,
		"provider_client_bound":                      false,
		"credential_bound":                           false,
		"runtime_bound":                              false,
		"mutation_armed":                             false,
		"send_attempted":                             false,
		"provider_request_sent":                      false,
		"provider_response_received":                 false,
		"retry_scheduled":                            false,
		"retry_attempt_recorded":                     false,
		"retry_after_value_recorded":                 false,
		"retry_after_header_included":                false,
		"provider_rate_limit_value_included":         false,
		"provider_error_code_included":               false,
		"external_call_made":                         false,
		"provider_api_call_made":                     false,
		"provider_api_mutation":                      "disabled",
		"request_body_included":                      false,
		"response_body_included":                     false,
		"headers_included":                           false,
		"authorization_header_included":              false,
		"provider_url_included":                      false,
		"idempotency_key_included":                   false,
		"provider_request_id_included":               false,
		"contains_token":                             false,
		"contains_provider_url":                      false,
		"contains_repository_ref":                    false,
		"contains_branch_name":                       false,
		"contains_file_content":                      false,
		"status_snapshot_write_eligible":             statusSnapshotWriteEligible,
		"status_snapshot_written":                    statusSnapshotWriteEligible,
		"provider_send_boundary_redacted":            true,
		"transport_boundary_redacted":                true,
		"retry_backoff_boundary_redacted":            true,
		"future_live_provider_request_still_blocked": true,
	}
}

func providerReviewAttemptSendSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "send_blocked"
	if boolOnlyFromAny(snapshot["provider_send_metadata_ready"]) &&
		boolOnlyFromAny(snapshot["transport_metadata_ready"]) &&
		boolOnlyFromAny(snapshot["retry_backoff_metadata_ready"]) {
		state = "send_metadata_ready"
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
	if !boolOnlyFromAny(snapshot["provider_send_plan_observed"]) {
		missing = append(missing, "provider_review_provider_send_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["transport_plan_observed"]) {
		missing = append(missing, "provider_review_transport_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["retry_backoff_plan_observed"]) {
		missing = append(missing, "provider_review_retry_backoff_plan_missing")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["send_attempted"]) {
		missing = append(missing, "provider_review_send_not_no_call")
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptSendSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "send_metadata_ready":
		return "provider_review_attempt_send_metadata_ready", "low"
	case "send_blocked":
		return "provider_review_attempt_send_blocked", "warning"
	default:
		return "provider_review_attempt_send_unknown", "warning"
	}
}

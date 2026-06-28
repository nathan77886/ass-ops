package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptRetryBackoffSnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
	Ledger    map[string]any
}

func RecordProviderReviewAttemptRetryBackoffSnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptRetryBackoffSnapshotOptions) (map[string]any, error) {
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
	snapshot := providerReviewAttemptRetryBackoffSnapshotPayload(attempt, ledger, assetErr == nil)
	ready, state, missing := providerReviewAttemptRetryBackoffSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_retry_backoff_snapshot_recording",
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
		"provider_review_attempt_retry_backoff_snapshot_written": false,
		"asset_status_snapshot_written":                          false,
		"operation_log_written":                                  false,
		"external_call_made":                                     false,
		"provider_api_call_made":                                 false,
		"provider_api_mutation":                                  "disabled",
		"mutation_armed":                                         false,
		"provider_request_sent":                                  false,
		"provider_response_received":                             false,
		"retry_scheduled":                                        false,
		"retry_attempt_recorded":                                 false,
		"retry_after_value_recorded":                             false,
		"retry_after_header_included":                            false,
		"provider_rate_limit_value_included":                     false,
		"provider_error_code_included":                           false,
		"contains_token":                                         false,
		"contains_provider_url":                                  false,
		"contains_repository_ref":                                false,
		"contains_branch_name":                                   false,
		"contains_file_content":                                  false,
		"canonical_asset_status_snapshot_try":                    false,
		"snapshot_commit_attempted":                              false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt retry/backoff snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt retry/backoff snapshot is waiting for the current execution candidate and redacted retry/backoff metadata; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt retry/backoff snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptRetryBackoffSnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "provider review attempt retry/backoff snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording provider review attempt retry/backoff snapshot recorded: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["provider_review_attempt_retry_backoff_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized provider review attempt retry/backoff snapshot recorded from local retry/backoff metadata."
	return result, nil
}

func providerReviewAttemptRetryBackoffSnapshotPayload(attempt, ledger map[string]any, assetObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	invocationPlan := mapFromAny(dispatchPlan["invocation_plan"])
	transportPlan := mapFromAny(dispatchPlan["transport_plan"])
	providerSendPlan := mapFromAny(invocationPlan["provider_send_plan"])
	retryBackoffPlan := mapFromAny(providerSendPlan["retry_backoff_plan"])
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	retryBackoffMetadataReady := boolOnlyFromAny(retryBackoffPlan["retry_backoff_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(retryBackoffPlan, "redacted_attempt_adapter_retry_backoff_plan", operationName, endpointKey)
	noCall := !boolOnlyFromAny(retryBackoffPlan["retry_scheduled"]) &&
		!boolOnlyFromAny(retryBackoffPlan["retry_attempt_recorded"]) &&
		!boolOnlyFromAny(retryBackoffPlan["retry_after_value_recorded"]) &&
		!boolOnlyFromAny(retryBackoffPlan["retry_after_header_included"]) &&
		!boolOnlyFromAny(retryBackoffPlan["provider_rate_limit_value_included"]) &&
		!boolOnlyFromAny(retryBackoffPlan["provider_error_code_included"]) &&
		!boolOnlyFromAny(retryBackoffPlan["external_call_made"]) &&
		!boolOnlyFromAny(retryBackoffPlan["provider_api_call_made"]) &&
		stringFromMap(retryBackoffPlan, "provider_api_mutation") == "disabled" &&
		!boolOnlyFromAny(retryBackoffPlan["request_body_included"]) &&
		!boolOnlyFromAny(retryBackoffPlan["response_body_included"]) &&
		!boolOnlyFromAny(retryBackoffPlan["headers_included"]) &&
		!boolOnlyFromAny(retryBackoffPlan["authorization_header_included"]) &&
		!boolOnlyFromAny(retryBackoffPlan["provider_url_included"]) &&
		!boolOnlyFromAny(retryBackoffPlan["idempotency_key_included"]) &&
		!boolOnlyFromAny(retryBackoffPlan["contains_token"]) &&
		!boolOnlyFromAny(retryBackoffPlan["contains_provider_url"]) &&
		!boolOnlyFromAny(retryBackoffPlan["contains_repository_ref"]) &&
		!boolOnlyFromAny(retryBackoffPlan["contains_branch_name"]) &&
		!boolOnlyFromAny(retryBackoffPlan["contains_file_content"])
	retryableClasses := safeProviderReviewStatusClasses(stringSliceFromAny(retryBackoffPlan["retryable_status_classes"]))
	transportRetryableClasses := safeProviderReviewStatusClasses(stringSliceFromAny(retryBackoffPlan["transport_retryable_status_classes"]))
	sequence := safeProviderReviewBlueprintNames(stringSliceFromAny(retryBackoffPlan["retry_backoff_sequence"]))
	suppressed := safeProviderReviewBlueprintNames(stringSliceFromAny(retryBackoffPlan["retry_backoff_suppressed_fields"]))
	blockedReasons := safeProviderReviewBlueprintNames(stringSliceFromAny(retryBackoffPlan["blocked_reasons"]))
	statusSnapshotWriteEligible := assetObserved && candidateMatches && retryBackoffMetadataReady && noCall && len(retryBackoffPlan) > 0 && len(retryableClasses) > 0 && len(transportRetryableClasses) > 0 && len(sequence) > 0 && len(suppressed) > 0 && safeProviderReviewRetryPolicy(stringFromMap(retryBackoffPlan, "retry_policy")) != "" && intFromAny(retryBackoffPlan["max_attempts"], 0) > 0
	return map[string]any{
		"mode":                                       "provider_review_attempt_retry_backoff_snapshot",
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
		"transport_plan_observed":                    len(transportPlan) > 0,
		"provider_send_plan_observed":                len(providerSendPlan) > 0,
		"retry_backoff_plan_observed":                len(retryBackoffPlan) > 0,
		"retry_backoff_metadata_ready":               retryBackoffMetadataReady,
		"retry_backoff_state":                        cleanOptionalText(stringFromMap(retryBackoffPlan, "retry_backoff_state")),
		"retry_backoff_ready":                        boolOnlyFromAny(retryBackoffPlan["retry_backoff_ready"]),
		"retry_backoff_ready_reason":                 safeProviderReviewRetryBackoffReadyReason(stringFromMap(retryBackoffPlan, "retry_backoff_ready_reason")),
		"retry_policy":                               safeProviderReviewRetryPolicy(stringFromMap(retryBackoffPlan, "retry_policy")),
		"max_attempts":                               intFromAny(retryBackoffPlan["max_attempts"], 0),
		"initial_backoff_seconds":                    intFromAny(retryBackoffPlan["initial_backoff_seconds"], 0),
		"max_backoff_seconds":                        intFromAny(retryBackoffPlan["max_backoff_seconds"], 0),
		"jitter":                                     cleanOptionalText(stringFromMap(retryBackoffPlan, "jitter")),
		"retryable_status_classes":                   retryableClasses,
		"transport_retryable_status_classes":         transportRetryableClasses,
		"requires_response_diagnostics":              boolOnlyFromAny(retryBackoffPlan["requires_response_diagnostics"]),
		"requires_idempotency_ledger":                boolOnlyFromAny(retryBackoffPlan["requires_idempotency_ledger"]),
		"requires_attempt_ledger":                    boolOnlyFromAny(retryBackoffPlan["requires_attempt_ledger"]),
		"requires_mutation_arming":                   boolOnlyFromAny(retryBackoffPlan["requires_mutation_arming"]),
		"retry_backoff_sequence":                     sequence,
		"retry_backoff_suppressed_fields":            suppressed,
		"blocked_reasons":                            blockedReasons,
		"retry_scheduled":                            boolOnlyFromAny(retryBackoffPlan["retry_scheduled"]),
		"retry_attempt_recorded":                     boolOnlyFromAny(retryBackoffPlan["retry_attempt_recorded"]),
		"retry_after_value_recorded":                 boolOnlyFromAny(retryBackoffPlan["retry_after_value_recorded"]),
		"retry_after_header_included":                boolOnlyFromAny(retryBackoffPlan["retry_after_header_included"]),
		"provider_rate_limit_value_included":         boolOnlyFromAny(retryBackoffPlan["provider_rate_limit_value_included"]),
		"provider_error_code_included":               boolOnlyFromAny(retryBackoffPlan["provider_error_code_included"]),
		"provider_response_received":                 false,
		"provider_request_sent":                      false,
		"external_call_made":                         boolOnlyFromAny(retryBackoffPlan["external_call_made"]),
		"provider_api_call_made":                     boolOnlyFromAny(retryBackoffPlan["provider_api_call_made"]),
		"provider_api_mutation":                      safeProviderReviewSnapshotMutationState(stringFromMap(retryBackoffPlan, "provider_api_mutation")),
		"mutation_armed":                             false,
		"request_body_included":                      boolOnlyFromAny(retryBackoffPlan["request_body_included"]),
		"response_body_included":                     boolOnlyFromAny(retryBackoffPlan["response_body_included"]),
		"headers_included":                           boolOnlyFromAny(retryBackoffPlan["headers_included"]),
		"authorization_header_included":              boolOnlyFromAny(retryBackoffPlan["authorization_header_included"]),
		"provider_url_included":                      boolOnlyFromAny(retryBackoffPlan["provider_url_included"]),
		"idempotency_key_included":                   boolOnlyFromAny(retryBackoffPlan["idempotency_key_included"]),
		"contains_token":                             boolOnlyFromAny(retryBackoffPlan["contains_token"]),
		"contains_provider_url":                      boolOnlyFromAny(retryBackoffPlan["contains_provider_url"]),
		"contains_repository_ref":                    boolOnlyFromAny(retryBackoffPlan["contains_repository_ref"]),
		"contains_branch_name":                       boolOnlyFromAny(retryBackoffPlan["contains_branch_name"]),
		"contains_file_content":                      boolOnlyFromAny(retryBackoffPlan["contains_file_content"]),
		"no_call_observed":                           noCall,
		"status_snapshot_write_eligible":             statusSnapshotWriteEligible,
		"status_snapshot_written":                    false,
		"retry_backoff_boundary_redacted":            true,
		"future_retry_execution_still_blocked":       true,
		"future_live_provider_request_still_blocked": true,
	}
}

func safeProviderReviewRetryBackoffReadyReason(value string) string {
	switch cleanOptionalText(value) {
	case "provider_retry_backoff_not_armed",
		"provider_response_diagnostics_not_recorded",
		"provider_idempotency_ledger_not_claimed",
		"provider_review_mutation_not_armed":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func providerReviewAttemptRetryBackoffSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "retry_backoff_blocked"
	if boolOnlyFromAny(snapshot["retry_backoff_metadata_ready"]) {
		state = "retry_backoff_metadata_ready"
	}
	for _, item := range []struct {
		field  string
		reason string
	}{
		{"provider_review_attempt_asset_observed", "provider_review_attempt_asset_missing"},
		{"candidate_observed", "provider_review_execution_candidate_missing"},
		{"candidate_matches_attempt", "provider_review_attempt_not_current_candidate"},
		{"provider_send_plan_observed", "provider_review_provider_send_plan_missing"},
		{"retry_backoff_plan_observed", "provider_review_retry_backoff_plan_missing"},
		{"retry_backoff_metadata_ready", "provider_review_retry_backoff_metadata_not_ready"},
	} {
		if !boolOnlyFromAny(snapshot[item.field]) {
			missing = append(missing, item.reason)
		}
	}
	if cleanOptionalID(fmt.Sprint(snapshot["provider_review_attempt_id"])) == "" {
		missing = append(missing, "provider_review_attempt_id_missing")
	}
	for _, item := range []struct {
		field  string
		reason string
	}{
		{"retryable_status_classes", "provider_review_retryable_status_classes_missing"},
		{"transport_retryable_status_classes", "provider_review_transport_retryable_status_classes_missing"},
		{"retry_backoff_sequence", "provider_review_retry_backoff_sequence_missing"},
		{"retry_backoff_suppressed_fields", "provider_review_retry_backoff_suppressed_fields_missing"},
	} {
		if len(stringSliceFromAny(snapshot[item.field])) == 0 {
			missing = append(missing, item.reason)
		}
	}
	if safeProviderReviewRetryPolicy(stringFromMap(snapshot, "retry_policy")) == "" {
		missing = append(missing, "provider_review_retry_policy_missing")
	}
	if intFromAny(snapshot["max_attempts"], 0) <= 0 {
		missing = append(missing, "provider_review_retry_budget_missing")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["provider_response_received"]) ||
		boolOnlyFromAny(snapshot["retry_scheduled"]) ||
		boolOnlyFromAny(snapshot["retry_attempt_recorded"]) ||
		boolOnlyFromAny(snapshot["retry_after_value_recorded"]) ||
		boolOnlyFromAny(snapshot["retry_after_header_included"]) ||
		boolOnlyFromAny(snapshot["provider_rate_limit_value_included"]) ||
		boolOnlyFromAny(snapshot["provider_error_code_included"]) ||
		boolOnlyFromAny(snapshot["request_body_included"]) ||
		boolOnlyFromAny(snapshot["response_body_included"]) ||
		boolOnlyFromAny(snapshot["headers_included"]) ||
		boolOnlyFromAny(snapshot["authorization_header_included"]) ||
		boolOnlyFromAny(snapshot["provider_url_included"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_included"]) ||
		boolOnlyFromAny(snapshot["contains_token"]) ||
		boolOnlyFromAny(snapshot["contains_provider_url"]) ||
		boolOnlyFromAny(snapshot["contains_repository_ref"]) ||
		boolOnlyFromAny(snapshot["contains_branch_name"]) ||
		boolOnlyFromAny(snapshot["contains_file_content"]) {
		missing = append(missing, "provider_review_retry_backoff_not_no_call")
	}
	if len(missing) > 0 {
		state = "retry_backoff_blocked"
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptRetryBackoffSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "retry_backoff_metadata_ready":
		return "provider_review_attempt_retry_backoff_metadata_ready", "low"
	case "retry_backoff_blocked":
		return "provider_review_attempt_retry_backoff_blocked", "warning"
	default:
		return "provider_review_attempt_retry_backoff_unknown", "warning"
	}
}

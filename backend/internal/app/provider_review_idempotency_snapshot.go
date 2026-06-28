package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptIdempotencySnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
	Ledger    map[string]any
}

func RecordProviderReviewAttemptIdempotencySnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptIdempotencySnapshotOptions) (map[string]any, error) {
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
	snapshot := providerReviewAttemptIdempotencySnapshotPayload(attempt, ledger, assetObserved)
	ready, state, missing := providerReviewAttemptIdempotencySnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_idempotency_snapshot_recording",
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
		"provider_review_attempt_idempotency_snapshot_written": false,
		"asset_status_snapshot_written":                        false,
		"operation_log_written":                                false,
		"external_call_made":                                   false,
		"provider_api_call_made":                               false,
		"provider_api_mutation":                                "disabled",
		"mutation_armed":                                       false,
		"idempotency_claim_recorded":                           false,
		"idempotency_key_included":                             false,
		"idempotency_key_materialized":                         false,
		"provider_request_sent":                                false,
		"contains_token":                                       false,
		"contains_provider_url":                                false,
		"contains_repository_ref":                              false,
		"contains_branch_name":                                 false,
		"contains_file_content":                                false,
		"canonical_asset_status_snapshot_try":                  false,
		"snapshot_commit_attempted":                            false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if !assetObserved {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt idempotency snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt idempotency snapshot is waiting for the current execution candidate and redacted idempotency metadata; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt idempotency snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptIdempotencySnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "provider review attempt idempotency snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording provider review attempt idempotency snapshot recorded: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["provider_review_attempt_idempotency_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized provider review attempt idempotency snapshot recorded from local idempotency metadata."
	return result, nil
}

func providerReviewAttemptIdempotencySnapshotPayload(attempt, ledger map[string]any, assetObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	requestSummary := mapFromAny(attempt["request_summary"])
	responseDiagnostics := mapFromAny(attempt["response_diagnostics"])
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	claimPlan := mapFromAny(candidate["claim_plan"])
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	requestSummaryMatches := providerReviewAttemptPlanMatchesOperation(requestSummary, "redacted_attempt_request_summary", operationName, endpointKey)
	claimPlanMatches := providerReviewAttemptPlanMatchesOperation(claimPlan, "redacted_attempt_execution_claim_plan", operationName, endpointKey)
	idempotencyMetadataReady := claimPlanMatches &&
		boolOnlyFromAny(claimPlan["idempotency_metadata_ready"]) &&
		requestSummaryMatches &&
		boolOnlyFromAny(requestSummary["requires_idempotency_ledger"]) &&
		cleanOptionalText(stringFromMap(requestSummary, "idempotency_key_kind")) == "operation_scope_hash"
	noCall := !boolOnlyFromAny(requestSummary["idempotency_key_included"]) &&
		!boolOnlyFromAny(requestSummary["provider_api_call_made"]) &&
		stringFromMap(requestSummary, "provider_api_mutation") == "disabled" &&
		!boolOnlyFromAny(requestSummary["external_call_made"]) &&
		!boolOnlyFromAny(requestSummary["request_body_included"]) &&
		!boolOnlyFromAny(requestSummary["headers_included"]) &&
		!boolOnlyFromAny(requestSummary["contains_token"]) &&
		!boolOnlyFromAny(requestSummary["contains_provider_url"]) &&
		!boolOnlyFromAny(requestSummary["contains_repository_ref"]) &&
		!boolOnlyFromAny(requestSummary["contains_branch_name"]) &&
		!boolOnlyFromAny(requestSummary["contains_file_content"]) &&
		!boolOnlyFromAny(claimPlan["idempotency_claim_recorded"]) &&
		!boolOnlyFromAny(claimPlan["provider_api_call_made"]) &&
		stringFromMap(claimPlan, "provider_api_mutation") == "disabled" &&
		!boolOnlyFromAny(claimPlan["external_call_made"]) &&
		!boolOnlyFromAny(claimPlan["idempotency_key_included"]) &&
		!boolOnlyFromAny(claimPlan["contains_token"]) &&
		!boolOnlyFromAny(claimPlan["contains_provider_url"]) &&
		!boolOnlyFromAny(claimPlan["contains_repository_ref"]) &&
		!boolOnlyFromAny(claimPlan["contains_branch_name"]) &&
		!boolOnlyFromAny(claimPlan["contains_file_content"])
	replayCheck := safeProviderReviewReplayCheck(stringFromMap(attempt, "replay_check"))
	conflictPolicy := safeProviderReviewConflictPolicy(stringFromMap(attempt, "conflict_policy"))
	retryPolicy := safeProviderReviewRetryPolicy(stringFromMap(attempt, "retry_policy"))
	blockedReasons := safeProviderReviewBlueprintNames(stringSliceFromAny(claimPlan["blocked_reasons"]))
	statusSnapshotWriteEligible := assetObserved &&
		candidateMatches &&
		idempotencyMetadataReady &&
		noCall &&
		replayCheck != "" &&
		conflictPolicy != "" &&
		retryPolicy != "" &&
		len(requestSummary) > 0 &&
		len(claimPlan) > 0
	return map[string]any{
		"mode":                                        "provider_review_attempt_idempotency_snapshot",
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
		"claim_plan_observed":                         len(claimPlan) > 0,
		"claim_plan_matches_attempt":                  claimPlanMatches,
		"request_summary_observed":                    len(requestSummary) > 0,
		"request_summary_matches_attempt":             requestSummaryMatches,
		"response_diagnostics_observed":               len(responseDiagnostics) > 0,
		"idempotency_metadata_ready":                  idempotencyMetadataReady,
		"response_diagnostics_ready":                  boolOnlyFromAny(claimPlan["response_diagnostics_ready"]),
		"requires_idempotency_ledger":                 boolOnlyFromAny(requestSummary["requires_idempotency_ledger"]),
		"requires_response_diagnostics":               boolOnlyFromAny(requestSummary["requires_response_diagnostics"]) || boolOnlyFromAny(claimPlan["requires_response_diagnostics"]),
		"requires_persisted_attempt":                  true,
		"requires_attempt_status_planned":             boolOnlyFromAny(claimPlan["requires_attempt_status_planned"]),
		"requires_dependency_ready":                   boolOnlyFromAny(claimPlan["requires_dependency_ready"]),
		"requires_optimistic_lock":                    boolOnlyFromAny(claimPlan["requires_optimistic_lock"]),
		"idempotency_key_kind":                        cleanOptionalText(stringFromMap(requestSummary, "idempotency_key_kind")),
		"idempotency_key_included":                    boolOnlyFromAny(requestSummary["idempotency_key_included"]) || boolOnlyFromAny(claimPlan["idempotency_key_included"]),
		"idempotency_key_materialized":                false,
		"idempotency_claim_recorded":                  boolOnlyFromAny(claimPlan["idempotency_claim_recorded"]),
		"replay_check":                                replayCheck,
		"conflict_policy":                             conflictPolicy,
		"retry_policy":                                retryPolicy,
		"response_status":                             safeProviderReviewAttemptResponseStatus(stringFromMap(responseDiagnostics, "status")),
		"success_status_class":                        safeProviderReviewStatusClass(stringFromMap(responseDiagnostics, "success_status_class")),
		"retryable_status_classes":                    safeProviderReviewStatusClasses(stringSliceFromAny(responseDiagnostics["retryable_status_classes"])),
		"blocked_reasons":                             blockedReasons,
		"provider_request_sent":                       false,
		"provider_response_received":                  false,
		"provider_api_call_made":                      boolOnlyFromAny(requestSummary["provider_api_call_made"]) || boolOnlyFromAny(claimPlan["provider_api_call_made"]),
		"provider_api_mutation":                       safeProviderReviewSnapshotMutationState(stringFromMap(requestSummary, "provider_api_mutation"), stringFromMap(claimPlan, "provider_api_mutation")),
		"external_call_made":                          boolOnlyFromAny(requestSummary["external_call_made"]) || boolOnlyFromAny(claimPlan["external_call_made"]),
		"mutation_armed":                              false,
		"request_body_included":                       boolOnlyFromAny(requestSummary["request_body_included"]),
		"response_body_included":                      boolOnlyFromAny(responseDiagnostics["response_body_included"]),
		"headers_included":                            boolOnlyFromAny(requestSummary["headers_included"]) || boolOnlyFromAny(responseDiagnostics["headers_included"]),
		"provider_url_included":                       false,
		"provider_request_id_included":                false,
		"contains_token":                              boolOnlyFromAny(requestSummary["contains_token"]) || boolOnlyFromAny(claimPlan["contains_token"]) || boolOnlyFromAny(responseDiagnostics["contains_token"]),
		"contains_provider_url":                       boolOnlyFromAny(requestSummary["contains_provider_url"]) || boolOnlyFromAny(claimPlan["contains_provider_url"]) || boolOnlyFromAny(responseDiagnostics["contains_provider_url"]),
		"contains_repository_ref":                     boolOnlyFromAny(requestSummary["contains_repository_ref"]) || boolOnlyFromAny(claimPlan["contains_repository_ref"]),
		"contains_branch_name":                        boolOnlyFromAny(requestSummary["contains_branch_name"]) || boolOnlyFromAny(claimPlan["contains_branch_name"]),
		"contains_file_content":                       boolOnlyFromAny(requestSummary["contains_file_content"]) || boolOnlyFromAny(claimPlan["contains_file_content"]),
		"no_call_observed":                            noCall,
		"status_snapshot_write_eligible":              statusSnapshotWriteEligible,
		"status_snapshot_written":                     false,
		"idempotency_boundary_redacted":               true,
		"future_live_idempotency_claim_still_blocked": true,
		"future_live_provider_request_still_blocked":  true,
	}
}

func providerReviewAttemptIdempotencySnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "idempotency_blocked"
	if boolOnlyFromAny(snapshot["idempotency_metadata_ready"]) {
		state = "idempotency_metadata_ready"
	}
	for _, item := range []struct {
		field  string
		reason string
	}{
		{"provider_review_attempt_asset_observed", "provider_review_attempt_asset_missing"},
		{"candidate_observed", "provider_review_execution_candidate_missing"},
		{"candidate_matches_attempt", "provider_review_attempt_not_current_candidate"},
		{"claim_plan_observed", "provider_review_claim_plan_missing"},
		{"claim_plan_matches_attempt", "provider_review_claim_plan_not_current_attempt"},
		{"request_summary_observed", "provider_review_request_summary_missing"},
		{"request_summary_matches_attempt", "provider_review_request_summary_not_current_attempt"},
		{"idempotency_metadata_ready", "provider_review_idempotency_metadata_not_ready"},
		{"requires_idempotency_ledger", "provider_review_idempotency_ledger_requirement_missing"},
	} {
		if !boolOnlyFromAny(snapshot[item.field]) {
			missing = append(missing, item.reason)
		}
	}
	if cleanOptionalID(fmt.Sprint(snapshot["provider_review_attempt_id"])) == "" {
		missing = append(missing, "provider_review_attempt_id_missing")
	}
	if cleanOptionalText(stringFromMap(snapshot, "idempotency_key_kind")) != "operation_scope_hash" {
		missing = append(missing, "provider_review_idempotency_key_kind_missing")
	}
	if safeProviderReviewReplayCheck(stringFromMap(snapshot, "replay_check")) == "" {
		missing = append(missing, "provider_review_replay_check_missing")
	}
	if safeProviderReviewConflictPolicy(stringFromMap(snapshot, "conflict_policy")) == "" {
		missing = append(missing, "provider_review_conflict_policy_missing")
	}
	if safeProviderReviewRetryPolicy(stringFromMap(snapshot, "retry_policy")) == "" {
		missing = append(missing, "provider_review_retry_policy_missing")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["provider_response_received"]) ||
		boolOnlyFromAny(snapshot["idempotency_claim_recorded"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_included"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_materialized"]) ||
		boolOnlyFromAny(snapshot["request_body_included"]) ||
		boolOnlyFromAny(snapshot["response_body_included"]) ||
		boolOnlyFromAny(snapshot["headers_included"]) ||
		boolOnlyFromAny(snapshot["provider_url_included"]) ||
		boolOnlyFromAny(snapshot["provider_request_id_included"]) ||
		boolOnlyFromAny(snapshot["contains_token"]) ||
		boolOnlyFromAny(snapshot["contains_provider_url"]) ||
		boolOnlyFromAny(snapshot["contains_repository_ref"]) ||
		boolOnlyFromAny(snapshot["contains_branch_name"]) ||
		boolOnlyFromAny(snapshot["contains_file_content"]) {
		missing = append(missing, "provider_review_idempotency_not_no_call")
	}
	if len(missing) > 0 {
		state = "idempotency_blocked"
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptIdempotencySnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "idempotency_metadata_ready":
		return "provider_review_attempt_idempotency_metadata_ready", "low"
	case "idempotency_blocked":
		return "provider_review_attempt_idempotency_blocked", "warning"
	default:
		return "provider_review_attempt_idempotency_unknown", "warning"
	}
}

package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptResultRecordingSnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
	Ledger    map[string]any
}

func RecordProviderReviewAttemptResultRecordingSnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptResultRecordingSnapshotOptions) (map[string]any, error) {
	if store == nil || store.DB == nil {
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
	assetID, assetErr := providerReviewAttemptAssetID(ctx, store.DB, attemptID)
	snapshot := providerReviewAttemptResultRecordingSnapshotPayload(attempt, ledger, assetErr == nil)
	ready, state, missing := providerReviewAttemptResultRecordingSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_result_recording_snapshot_recording",
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
		"provider_review_attempt_result_recording_snapshot_written": false,
		"asset_status_snapshot_written":                             false,
		"operation_log_written":                                     false,
		"external_call_made":                                        false,
		"provider_api_call_made":                                    false,
		"provider_api_mutation":                                     "disabled",
		"mutation_armed":                                            false,
		"provider_request_sent":                                     false,
		"provider_response_received":                                false,
		"response_recorded":                                         false,
		"result_recorded":                                           false,
		"attempt_result_persisted":                                  false,
		"dependency_update_recorded":                                false,
		"transaction_recorded":                                      false,
		"contains_token":                                            false,
		"contains_provider_url":                                     false,
		"contains_repository_ref":                                   false,
		"contains_branch_name":                                      false,
		"contains_file_content":                                     false,
		"canonical_asset_status_snapshot_try":                       false,
		"snapshot_commit_attempted":                                 false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt result-recording snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt result-recording snapshot is waiting for the current execution candidate, response plan, and result-recording metadata; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt result-recording snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptResultRecordingSnapshotStatusHealth(state)
	tx, err := store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting provider review attempt result-recording snapshot transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text), hashtext($2::text))`, assetID, status); err != nil {
		return nil, fmt.Errorf("locking provider review attempt result-recording snapshot asset: %w", err)
	}
	execResult, err := tx.ExecContext(ctx, `
		INSERT INTO asset_status_snapshots(asset_id, status, health, summary, raw)
		SELECT $1, $2, $3, 'provider review attempt result-recording snapshot recorded', $4
		WHERE NOT EXISTS (
			SELECT 1
			FROM asset_status_snapshots latest
			WHERE latest.asset_id=$1
				AND latest.status=$2
				AND latest.health=$3
				AND latest.raw=$4
				AND latest.collected_at=(
					SELECT max(collected_at)
					FROM asset_status_snapshots newest
					WHERE newest.asset_id=$1
				)
		)`,
		assetID, status, health, JSONValue{Data: snapshot})
	if err != nil {
		return nil, fmt.Errorf("inserting provider review attempt result-recording snapshot: %w", err)
	}
	written := 0
	rowsAffectedWarning := ""
	if rows, err := execResult.RowsAffected(); err == nil {
		written = int(rows)
	} else {
		written = -1
		rowsAffectedWarning = "rows affected unavailable"
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing provider review attempt result-recording snapshot: %w", err)
	}
	committed = true
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	if written >= 0 {
		result["snapshots_skipped_as_duplicate"] = 1 - written
		result["provider_review_attempt_result_recording_snapshot_written"] = written > 0
		result["asset_status_snapshot_written"] = written > 0
	}
	if rowsAffectedWarning != "" {
		result["rows_affected_warning"] = rowsAffectedWarning
		result["rows_affected_unknown"] = true
		result["snapshots_skipped_as_duplicate"] = -1
		result["provider_review_attempt_result_recording_snapshot_written"] = false
		result["asset_status_snapshot_written"] = false
	}
	result["message"] = "Sanitized provider review attempt result-recording snapshot recorded from local response/result metadata."
	return result, nil
}

func providerReviewAttemptResultRecordingSnapshotPayload(attempt, ledger map[string]any, assetObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	responsePlan := mapFromAny(dispatchPlan["response_plan"])
	resultPlan := mapFromAny(responsePlan["result_recording_plan"])
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	responseMetadataReady := providerReviewAttemptResponsePlanReadyForOperation(responsePlan, operationName, endpointKey)
	resultRecordingMetadataReady := boolOnlyFromAny(resultPlan["result_recording_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(resultPlan, "redacted_attempt_adapter_result_recording_plan", operationName, endpointKey)
	noCall := !boolOnlyFromAny(resultPlan["result_recorded"]) &&
		!boolOnlyFromAny(resultPlan["response_classified"]) &&
		!boolOnlyFromAny(resultPlan["attempt_status_mapped"]) &&
		!boolOnlyFromAny(resultPlan["attempt_result_persisted"]) &&
		!boolOnlyFromAny(resultPlan["dependency_update_staged"]) &&
		!boolOnlyFromAny(resultPlan["provider_request_id_recorded"]) &&
		!boolOnlyFromAny(resultPlan["provider_response_status_recorded"]) &&
		!boolOnlyFromAny(resultPlan["provider_response_body_recorded"]) &&
		!boolOnlyFromAny(resultPlan["provider_response_headers_recorded"]) &&
		!boolOnlyFromAny(resultPlan["external_call_made"]) &&
		!boolOnlyFromAny(resultPlan["provider_api_call_made"]) &&
		stringFromMap(resultPlan, "provider_api_mutation") == "disabled" &&
		!boolOnlyFromAny(resultPlan["response_body_included"]) &&
		!boolOnlyFromAny(resultPlan["headers_included"]) &&
		!boolOnlyFromAny(resultPlan["provider_request_id_included"]) &&
		!boolOnlyFromAny(resultPlan["provider_response_status_included"]) &&
		!boolOnlyFromAny(resultPlan["provider_url_included"]) &&
		!boolOnlyFromAny(resultPlan["idempotency_key_included"]) &&
		!boolOnlyFromAny(resultPlan["contains_token"]) &&
		!boolOnlyFromAny(resultPlan["contains_provider_url"]) &&
		!boolOnlyFromAny(resultPlan["contains_repository_ref"]) &&
		!boolOnlyFromAny(resultPlan["contains_branch_name"]) &&
		!boolOnlyFromAny(resultPlan["contains_file_content"])
	sequence := safeProviderReviewBlueprintNames(stringSliceFromAny(resultPlan["result_recording_sequence"]))
	statusSnapshotWriteEligible := assetObserved && candidateMatches && responseMetadataReady && resultRecordingMetadataReady && noCall && len(sequence) > 0
	return map[string]any{
		"mode":                                      "provider_review_attempt_result_recording_snapshot",
		"provider_review_attempt_id":                cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":                     cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"project_template_run_id":                   cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed":    assetObserved,
		"operation_name":                            operationName,
		"endpoint_key":                              endpointKey,
		"attempt_status":                            safeProviderReviewAttemptStatus(stringFromMap(attempt, "status")),
		"dependency_status":                         safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(attempt, "dependency_status")),
		"operation_order":                           intFromAny(attempt["operation_order"], 0),
		"candidate_observed":                        len(candidate) > 0,
		"candidate_matches_attempt":                 candidateMatches,
		"candidate_status":                          cleanOptionalText(stringFromMap(candidate, "status")),
		"dispatch_plan_observed":                    len(dispatchPlan) > 0,
		"response_plan_observed":                    len(responsePlan) > 0,
		"result_recording_plan_observed":            len(resultPlan) > 0,
		"response_metadata_ready":                   responseMetadataReady,
		"result_recording_metadata_ready":           resultRecordingMetadataReady,
		"result_recording_ready":                    boolOnlyFromAny(resultPlan["result_recording_ready"]),
		"result_recording_ready_reason":             safeProviderReviewResultRecordingReadyReason(stringFromMap(resultPlan, "result_recording_ready_reason")),
		"result_recording_sequence":                 sequence,
		"response_status":                           safeProviderReviewAttemptResponseStatus(stringFromMap(resultPlan, "response_status")),
		"success_attempt_status":                    safeProviderReviewAttemptStatus(stringFromMap(resultPlan, "success_attempt_status")),
		"retry_attempt_status":                      safeProviderReviewAttemptStatus(stringFromMap(resultPlan, "retry_attempt_status")),
		"failure_attempt_status":                    safeProviderReviewAttemptStatus(stringFromMap(resultPlan, "failure_attempt_status")),
		"dependency_unlocks_operation":              safeProviderReviewAttemptOperationName(stringFromMap(resultPlan, "dependency_unlocks_operation")),
		"dependency_update_status":                  safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(resultPlan, "dependency_update_status")),
		"diagnostic_fields":                         safeProviderReviewBlueprintNames(stringSliceFromAny(resultPlan["result_recording_diagnostic_fields"])),
		"persisted_fields":                          safeProviderReviewBlueprintNames(stringSliceFromAny(resultPlan["result_recording_persisted_fields"])),
		"suppressed_fields":                         safeProviderReviewBlueprintNames(stringSliceFromAny(resultPlan["result_recording_suppressed_fields"])),
		"requires_response_handler":                 boolOnlyFromAny(resultPlan["requires_response_handler"]),
		"requires_response_diagnostics":             boolOnlyFromAny(resultPlan["requires_response_diagnostics"]),
		"requires_transaction_boundary":             boolOnlyFromAny(resultPlan["requires_transaction_boundary"]),
		"requires_dependency_update":                boolOnlyFromAny(resultPlan["requires_dependency_update"]),
		"requires_mutation_arming":                  boolOnlyFromAny(resultPlan["requires_mutation_arming"]),
		"mutation_armed":                            false,
		"provider_request_sent":                     false,
		"provider_response_received":                false,
		"response_recorded":                         false,
		"result_recorded":                           boolOnlyFromAny(resultPlan["result_recorded"]),
		"response_classified":                       boolOnlyFromAny(resultPlan["response_classified"]),
		"attempt_status_mapped":                     boolOnlyFromAny(resultPlan["attempt_status_mapped"]),
		"attempt_result_persisted":                  boolOnlyFromAny(resultPlan["attempt_result_persisted"]),
		"dependency_update_staged":                  boolOnlyFromAny(resultPlan["dependency_update_staged"]),
		"dependency_update_recorded":                false,
		"transaction_recorded":                      false,
		"provider_request_id_recorded":              boolOnlyFromAny(resultPlan["provider_request_id_recorded"]),
		"provider_response_status_recorded":         boolOnlyFromAny(resultPlan["provider_response_status_recorded"]),
		"provider_response_body_recorded":           boolOnlyFromAny(resultPlan["provider_response_body_recorded"]),
		"provider_response_headers_recorded":        boolOnlyFromAny(resultPlan["provider_response_headers_recorded"]),
		"external_call_made":                        boolOnlyFromAny(resultPlan["external_call_made"]),
		"provider_api_call_made":                    boolOnlyFromAny(resultPlan["provider_api_call_made"]),
		"provider_api_mutation":                     safeProviderReviewSnapshotMutationState(stringFromMap(resultPlan, "provider_api_mutation")),
		"request_body_included":                     false,
		"response_body_included":                    boolOnlyFromAny(resultPlan["response_body_included"]),
		"headers_included":                          boolOnlyFromAny(resultPlan["headers_included"]),
		"authorization_header_included":             false,
		"provider_url_included":                     boolOnlyFromAny(resultPlan["provider_url_included"]),
		"idempotency_key_included":                  boolOnlyFromAny(resultPlan["idempotency_key_included"]),
		"provider_request_id_included":              boolOnlyFromAny(resultPlan["provider_request_id_included"]),
		"provider_response_status_included":         boolOnlyFromAny(resultPlan["provider_response_status_included"]),
		"contains_token":                            boolOnlyFromAny(resultPlan["contains_token"]),
		"contains_provider_url":                     boolOnlyFromAny(resultPlan["contains_provider_url"]),
		"contains_repository_ref":                   boolOnlyFromAny(resultPlan["contains_repository_ref"]),
		"contains_branch_name":                      boolOnlyFromAny(resultPlan["contains_branch_name"]),
		"contains_file_content":                     boolOnlyFromAny(resultPlan["contains_file_content"]),
		"no_call_observed":                          noCall,
		"status_snapshot_write_eligible":            statusSnapshotWriteEligible,
		"status_snapshot_written":                   false,
		"result_recording_boundary_redacted":        true,
		"future_live_provider_result_still_blocked": true,
	}
}

func safeProviderReviewResultRecordingReadyReason(value string) string {
	switch cleanOptionalText(value) {
	case "provider_review_result_recording_not_armed",
		"provider_review_response_metadata_not_ready",
		"provider_api_call_not_made":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func providerReviewAttemptResultRecordingSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "result_recording_blocked"
	if boolOnlyFromAny(snapshot["response_metadata_ready"]) && boolOnlyFromAny(snapshot["result_recording_metadata_ready"]) {
		state = "result_recording_metadata_ready"
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
	if !boolOnlyFromAny(snapshot["response_plan_observed"]) {
		missing = append(missing, "provider_review_response_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["result_recording_plan_observed"]) {
		missing = append(missing, "provider_review_result_recording_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["response_metadata_ready"]) {
		missing = append(missing, "provider_review_response_metadata_not_ready")
	}
	if !boolOnlyFromAny(snapshot["result_recording_metadata_ready"]) {
		missing = append(missing, "provider_review_result_recording_metadata_not_ready")
	}
	if len(stringSliceFromAny(snapshot["result_recording_sequence"])) == 0 {
		missing = append(missing, "provider_review_result_recording_sequence_missing")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["provider_response_received"]) ||
		boolOnlyFromAny(snapshot["response_recorded"]) ||
		boolOnlyFromAny(snapshot["result_recorded"]) ||
		boolOnlyFromAny(snapshot["response_classified"]) ||
		boolOnlyFromAny(snapshot["attempt_status_mapped"]) ||
		boolOnlyFromAny(snapshot["attempt_result_persisted"]) ||
		boolOnlyFromAny(snapshot["dependency_update_staged"]) ||
		boolOnlyFromAny(snapshot["dependency_update_recorded"]) ||
		boolOnlyFromAny(snapshot["transaction_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_request_id_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_response_status_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_response_body_recorded"]) ||
		boolOnlyFromAny(snapshot["provider_response_headers_recorded"]) ||
		boolOnlyFromAny(snapshot["response_body_included"]) ||
		boolOnlyFromAny(snapshot["headers_included"]) ||
		boolOnlyFromAny(snapshot["provider_url_included"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_included"]) ||
		boolOnlyFromAny(snapshot["provider_request_id_included"]) ||
		boolOnlyFromAny(snapshot["provider_response_status_included"]) ||
		boolOnlyFromAny(snapshot["contains_token"]) ||
		boolOnlyFromAny(snapshot["contains_provider_url"]) ||
		boolOnlyFromAny(snapshot["contains_repository_ref"]) ||
		boolOnlyFromAny(snapshot["contains_branch_name"]) ||
		boolOnlyFromAny(snapshot["contains_file_content"]) {
		missing = append(missing, "provider_review_result_recording_not_no_call")
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptResultRecordingSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "result_recording_metadata_ready":
		return "provider_review_attempt_result_recording_metadata_ready", "low"
	case "result_recording_blocked":
		return "provider_review_attempt_result_recording_blocked", "warning"
	default:
		return "provider_review_attempt_result_recording_unknown", "warning"
	}
}

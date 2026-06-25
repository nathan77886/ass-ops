package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptLiveAdapterContractSnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
	Ledger    map[string]any
}

func RecordProviderReviewAttemptLiveAdapterContractSnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptLiveAdapterContractSnapshotOptions) (map[string]any, error) {
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
	snapshot := providerReviewAttemptLiveAdapterContractSnapshotPayload(attempt, ledger, assetErr == nil)
	ready, state, missing := providerReviewAttemptLiveAdapterContractSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_live_adapter_contract_snapshot_recording",
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
		"provider_review_attempt_live_adapter_contract_snapshot_written": false,
		"asset_status_snapshot_written":                                  false,
		"operation_log_written":                                          false,
		"external_call_made":                                             false,
		"provider_api_call_made":                                         false,
		"provider_api_mutation":                                          "disabled",
		"mutation_armed":                                                 false,
		"live_adapter_implemented":                                       false,
		"provider_request_sent":                                          false,
		"provider_response_received":                                     false,
		"request_contract_materialized":                                  false,
		"response_contract_materialized":                                 false,
		"error_contract_materialized":                                    false,
		"result_contract_materialized":                                   false,
		"contains_token":                                                 false,
		"contains_provider_url":                                          false,
		"contains_repository_ref":                                        false,
		"contains_branch_name":                                           false,
		"contains_file_content":                                          false,
		"canonical_asset_status_snapshot_try":                            false,
		"snapshot_commit_attempted":                                      false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt live-adapter contract snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt live-adapter contract snapshot is waiting for the current execution candidate and redacted live-adapter contract metadata; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt live-adapter contract snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptLiveAdapterContractSnapshotStatusHealth(state)
	tx, err := store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting provider review attempt live-adapter contract snapshot transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text), hashtext($2::text))`, assetID, status); err != nil {
		return nil, fmt.Errorf("locking provider review attempt live-adapter contract snapshot asset: %w", err)
	}
	execResult, err := tx.ExecContext(ctx, `
		INSERT INTO asset_status_snapshots(asset_id, status, health, summary, raw)
		SELECT $1, $2, $3, 'provider review attempt live-adapter contract snapshot recorded', $4
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
		return nil, fmt.Errorf("inserting provider review attempt live-adapter contract snapshot: %w", err)
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
		return nil, fmt.Errorf("committing provider review attempt live-adapter contract snapshot: %w", err)
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
		result["provider_review_attempt_live_adapter_contract_snapshot_written"] = written > 0
		result["asset_status_snapshot_written"] = written > 0
	}
	if rowsAffectedWarning != "" {
		result["rows_affected_warning"] = rowsAffectedWarning
		result["rows_affected_unknown"] = true
		result["snapshots_skipped_as_duplicate"] = -1
		result["provider_review_attempt_live_adapter_contract_snapshot_written"] = false
		result["asset_status_snapshot_written"] = false
	}
	result["message"] = "Sanitized provider review attempt live-adapter contract snapshot recorded from local contract metadata."
	return result, nil
}

func providerReviewAttemptLiveAdapterContractSnapshotPayload(attempt, ledger map[string]any, assetObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	invocationPlan := mapFromAny(dispatchPlan["invocation_plan"])
	activationPlan := mapFromAny(invocationPlan["adapter_activation_plan"])
	liveAdapterPlan := mapFromAny(activationPlan["live_adapter_plan"])
	contractPlan := mapFromAny(liveAdapterPlan["contract_plan"])
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	contractMetadataReady := providerReviewAttemptPlanMatchesOperation(liveAdapterPlan, "redacted_attempt_live_adapter_plan", operationName, endpointKey) &&
		providerReviewAttemptPlanMatchesOperation(contractPlan, "redacted_attempt_live_adapter_contract_plan", operationName, endpointKey)
	noCall := !boolOnlyFromAny(contractPlan["contract_implemented"]) &&
		!boolOnlyFromAny(contractPlan["request_contract_materialized"]) &&
		!boolOnlyFromAny(contractPlan["response_contract_materialized"]) &&
		!boolOnlyFromAny(contractPlan["error_contract_materialized"]) &&
		!boolOnlyFromAny(contractPlan["result_contract_materialized"]) &&
		!boolOnlyFromAny(contractPlan["provider_request_sent"]) &&
		!boolOnlyFromAny(contractPlan["external_call_made"]) &&
		!boolOnlyFromAny(contractPlan["provider_api_call_made"]) &&
		stringFromMap(contractPlan, "provider_api_mutation") == "disabled" &&
		!boolOnlyFromAny(contractPlan["request_body_included"]) &&
		!boolOnlyFromAny(contractPlan["response_body_included"]) &&
		!boolOnlyFromAny(contractPlan["headers_included"]) &&
		!boolOnlyFromAny(contractPlan["authorization_header_included"]) &&
		!boolOnlyFromAny(contractPlan["provider_url_included"]) &&
		!boolOnlyFromAny(contractPlan["idempotency_key_included"]) &&
		!boolOnlyFromAny(contractPlan["provider_request_id_included"]) &&
		!boolOnlyFromAny(contractPlan["contains_token"]) &&
		!boolOnlyFromAny(contractPlan["contains_provider_url"]) &&
		!boolOnlyFromAny(contractPlan["contains_repository_ref"]) &&
		!boolOnlyFromAny(contractPlan["contains_branch_name"]) &&
		!boolOnlyFromAny(contractPlan["contains_file_content"])
	inputs := safeProviderReviewBlueprintNames(stringSliceFromAny(contractPlan["contract_input_fields"]))
	outputs := safeProviderReviewBlueprintNames(stringSliceFromAny(contractPlan["contract_output_fields"]))
	errors := safeProviderReviewBlueprintNames(stringSliceFromAny(contractPlan["contract_error_classes"]))
	persisted := safeProviderReviewBlueprintNames(stringSliceFromAny(contractPlan["contract_persisted_fields"]))
	suppressed := safeProviderReviewBlueprintNames(stringSliceFromAny(contractPlan["contract_suppressed_fields"]))
	sequence := safeProviderReviewBlueprintNames(stringSliceFromAny(contractPlan["contract_sequence"]))
	capabilities := safeProviderReviewBlueprintNames(stringSliceFromAny(contractPlan["required_capabilities"]))
	contractRegistered := boolOnlyFromAny(contractPlan["contract_registered"])
	statusSnapshotWriteEligible := assetObserved && candidateMatches && contractMetadataReady && noCall && contractRegistered && len(capabilities) > 0 && len(inputs) > 0 && len(outputs) > 0 && len(errors) > 0 && len(persisted) > 0 && len(suppressed) > 0 && len(sequence) > 0
	return map[string]any{
		"mode":                                        "provider_review_attempt_live_adapter_contract_snapshot",
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
		"dispatch_plan_observed":                      len(dispatchPlan) > 0,
		"invocation_plan_observed":                    len(invocationPlan) > 0,
		"adapter_activation_plan_observed":            len(activationPlan) > 0,
		"live_adapter_plan_observed":                  len(liveAdapterPlan) > 0,
		"live_adapter_contract_plan_observed":         len(contractPlan) > 0,
		"live_adapter_contract_metadata_ready":        contractMetadataReady,
		"contract_state":                              cleanOptionalText(stringFromMap(contractPlan, "contract_state")),
		"contract_ready":                              boolOnlyFromAny(contractPlan["contract_ready"]),
		"contract_ready_reason":                       safeProviderReviewLiveAdapterContractReadyReason(stringFromMap(contractPlan, "contract_ready_reason")),
		"provider_type":                               safeProviderReviewProviderType(stringFromMap(contractPlan, "provider_type")),
		"adapter_name":                                safeProviderReviewBlueprintName(stringFromMap(contractPlan, "adapter_name")),
		"builder_name":                                safeProviderReviewPayloadBuilderName(stringFromMap(contractPlan, "builder_name")),
		"client_kind":                                 safeProviderReviewBlueprintName(stringFromMap(contractPlan, "client_kind")),
		"execute_method_name":                         safeProviderReviewBlueprintName(stringFromMap(contractPlan, "execute_method_name")),
		"response_handler_name":                       safeProviderReviewResponseHandlerName(stringFromMap(contractPlan, "response_handler_name")),
		"http_method":                                 cleanOptionalText(stringFromMap(contractPlan, "http_method")),
		"endpoint_path_template_key":                  safeProviderReviewBlueprintName(stringFromMap(contractPlan, "endpoint_path_template_key")),
		"payload_shape":                               cleanOptionalText(stringFromMap(contractPlan, "payload_shape")),
		"auth_scheme":                                 cleanOptionalText(stringFromMap(contractPlan, "auth_scheme")),
		"required_capabilities":                       capabilities,
		"contract_input_fields":                       inputs,
		"contract_output_fields":                      outputs,
		"contract_error_classes":                      errors,
		"contract_persisted_fields":                   persisted,
		"contract_suppressed_fields":                  suppressed,
		"contract_sequence":                           sequence,
		"requires_activation_plan":                    boolOnlyFromAny(contractPlan["requires_activation_plan"]),
		"requires_attempt_claim":                      boolOnlyFromAny(contractPlan["requires_attempt_claim"]),
		"requires_execution_lock":                     boolOnlyFromAny(contractPlan["requires_execution_lock"]),
		"requires_credential_binding":                 boolOnlyFromAny(contractPlan["requires_credential_binding"]),
		"requires_provider_client":                    boolOnlyFromAny(contractPlan["requires_provider_client"]),
		"requires_request_builder":                    boolOnlyFromAny(contractPlan["requires_request_builder"]),
		"requires_transport":                          boolOnlyFromAny(contractPlan["requires_transport"]),
		"requires_response_handler":                   boolOnlyFromAny(contractPlan["requires_response_handler"]),
		"requires_transaction_handler":                boolOnlyFromAny(contractPlan["requires_transaction_handler"]),
		"requires_mutation_arming":                    boolOnlyFromAny(contractPlan["requires_mutation_arming"]),
		"contract_registered":                         contractRegistered,
		"contract_implemented":                        boolOnlyFromAny(contractPlan["contract_implemented"]),
		"request_contract_materialized":               boolOnlyFromAny(contractPlan["request_contract_materialized"]),
		"response_contract_materialized":              boolOnlyFromAny(contractPlan["response_contract_materialized"]),
		"error_contract_materialized":                 boolOnlyFromAny(contractPlan["error_contract_materialized"]),
		"result_contract_materialized":                boolOnlyFromAny(contractPlan["result_contract_materialized"]),
		"provider_request_sent":                       boolOnlyFromAny(contractPlan["provider_request_sent"]),
		"provider_response_received":                  false,
		"external_call_made":                          boolOnlyFromAny(contractPlan["external_call_made"]),
		"provider_api_call_made":                      boolOnlyFromAny(contractPlan["provider_api_call_made"]),
		"provider_api_mutation":                       safeProviderReviewSnapshotMutationState(stringFromMap(contractPlan, "provider_api_mutation")),
		"mutation_armed":                              false,
		"live_adapter_implemented":                    false,
		"request_body_included":                       boolOnlyFromAny(contractPlan["request_body_included"]),
		"response_body_included":                      boolOnlyFromAny(contractPlan["response_body_included"]),
		"headers_included":                            boolOnlyFromAny(contractPlan["headers_included"]),
		"authorization_header_included":               boolOnlyFromAny(contractPlan["authorization_header_included"]),
		"provider_url_included":                       boolOnlyFromAny(contractPlan["provider_url_included"]),
		"idempotency_key_included":                    boolOnlyFromAny(contractPlan["idempotency_key_included"]),
		"provider_request_id_included":                boolOnlyFromAny(contractPlan["provider_request_id_included"]),
		"contains_token":                              boolOnlyFromAny(contractPlan["contains_token"]),
		"contains_provider_url":                       boolOnlyFromAny(contractPlan["contains_provider_url"]),
		"contains_repository_ref":                     boolOnlyFromAny(contractPlan["contains_repository_ref"]),
		"contains_branch_name":                        boolOnlyFromAny(contractPlan["contains_branch_name"]),
		"contains_file_content":                       boolOnlyFromAny(contractPlan["contains_file_content"]),
		"no_call_observed":                            noCall,
		"status_snapshot_write_eligible":              statusSnapshotWriteEligible,
		"status_snapshot_written":                     false,
		"live_adapter_contract_boundary_redacted":     true,
		"future_live_adapter_execution_still_blocked": true,
	}
}

func safeProviderReviewBlueprintName(value string) string {
	names := safeProviderReviewBlueprintNames([]string{value})
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func safeProviderReviewLiveAdapterContractReadyReason(value string) string {
	switch cleanOptionalText(value) {
	case "provider_review_live_adapter_contract_not_armed",
		"provider_review_adapter_not_implemented",
		"provider_review_mutation_not_armed":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func providerReviewAttemptLiveAdapterContractSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "live_adapter_contract_blocked"
	if boolOnlyFromAny(snapshot["live_adapter_contract_metadata_ready"]) {
		state = "live_adapter_contract_metadata_ready"
	}
	for _, item := range []struct {
		field  string
		reason string
	}{
		{"provider_review_attempt_asset_observed", "provider_review_attempt_asset_missing"},
		{"candidate_observed", "provider_review_execution_candidate_missing"},
		{"candidate_matches_attempt", "provider_review_attempt_not_current_candidate"},
		{"live_adapter_plan_observed", "provider_review_live_adapter_plan_missing"},
		{"live_adapter_contract_plan_observed", "provider_review_live_adapter_contract_plan_missing"},
		{"live_adapter_contract_metadata_ready", "provider_review_live_adapter_contract_metadata_not_ready"},
		{"contract_registered", "provider_review_live_adapter_contract_not_registered"},
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
		{"contract_input_fields", "provider_review_live_adapter_contract_inputs_missing"},
		{"contract_output_fields", "provider_review_live_adapter_contract_outputs_missing"},
		{"contract_error_classes", "provider_review_live_adapter_contract_errors_missing"},
		{"contract_persisted_fields", "provider_review_live_adapter_contract_persisted_fields_missing"},
		{"contract_suppressed_fields", "provider_review_live_adapter_contract_suppressed_fields_missing"},
		{"contract_sequence", "provider_review_live_adapter_contract_sequence_missing"},
		{"required_capabilities", "provider_review_live_adapter_contract_capabilities_missing"},
	} {
		if len(stringSliceFromAny(snapshot[item.field])) == 0 {
			missing = append(missing, item.reason)
		}
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["live_adapter_implemented"]) ||
		boolOnlyFromAny(snapshot["contract_implemented"]) ||
		boolOnlyFromAny(snapshot["request_contract_materialized"]) ||
		boolOnlyFromAny(snapshot["response_contract_materialized"]) ||
		boolOnlyFromAny(snapshot["error_contract_materialized"]) ||
		boolOnlyFromAny(snapshot["result_contract_materialized"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["provider_response_received"]) ||
		boolOnlyFromAny(snapshot["request_body_included"]) ||
		boolOnlyFromAny(snapshot["response_body_included"]) ||
		boolOnlyFromAny(snapshot["headers_included"]) ||
		boolOnlyFromAny(snapshot["authorization_header_included"]) ||
		boolOnlyFromAny(snapshot["provider_url_included"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_included"]) ||
		boolOnlyFromAny(snapshot["provider_request_id_included"]) ||
		boolOnlyFromAny(snapshot["contains_token"]) ||
		boolOnlyFromAny(snapshot["contains_provider_url"]) ||
		boolOnlyFromAny(snapshot["contains_repository_ref"]) ||
		boolOnlyFromAny(snapshot["contains_branch_name"]) ||
		boolOnlyFromAny(snapshot["contains_file_content"]) {
		missing = append(missing, "provider_review_live_adapter_contract_not_no_call")
	}
	if len(missing) > 0 {
		state = "live_adapter_contract_blocked"
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptLiveAdapterContractSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "live_adapter_contract_metadata_ready":
		return "provider_review_attempt_live_adapter_contract_metadata_ready", "low"
	case "live_adapter_contract_blocked":
		return "provider_review_attempt_live_adapter_contract_blocked", "warning"
	default:
		return "provider_review_attempt_live_adapter_contract_unknown", "warning"
	}
}

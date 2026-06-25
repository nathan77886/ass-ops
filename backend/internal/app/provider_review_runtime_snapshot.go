package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptRuntimeSnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
	Ledger    map[string]any
}

func RecordProviderReviewAttemptRuntimeSnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptRuntimeSnapshotOptions) (map[string]any, error) {
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
	snapshot := providerReviewAttemptRuntimeSnapshotPayload(attempt, ledger, assetErr == nil)
	ready, state, missing := providerReviewAttemptRuntimeSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_runtime_snapshot_recording",
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
		"provider_review_attempt_runtime_snapshot_written": false,
		"asset_status_snapshot_written":                    false,
		"operation_log_written":                            false,
		"external_call_made":                               false,
		"provider_api_call_made":                           false,
		"provider_api_mutation":                            "disabled",
		"mutation_armed":                                   false,
		"live_adapter_implemented":                         false,
		"provider_client_constructed":                      false,
		"runtime_bound":                                    false,
		"contains_token":                                   false,
		"contains_provider_url":                            false,
		"contains_repository_ref":                          false,
		"contains_branch_name":                             false,
		"contains_file_content":                            false,
		"canonical_asset_status_snapshot_try":              false,
		"snapshot_commit_attempted":                        false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt runtime snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt runtime snapshot is waiting for the current execution candidate and adapter runtime contract; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt runtime snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptRuntimeSnapshotStatusHealth(state)
	tx, err := store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting provider review attempt runtime snapshot transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text), hashtext($2::text))`, assetID, status); err != nil {
		return nil, fmt.Errorf("locking provider review attempt runtime snapshot asset: %w", err)
	}
	execResult, err := tx.ExecContext(ctx, `
		INSERT INTO asset_status_snapshots(asset_id, status, health, summary, raw)
		SELECT $1, $2, $3, 'provider review attempt runtime snapshot recorded', $4
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
		return nil, fmt.Errorf("inserting provider review attempt runtime snapshot: %w", err)
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
		return nil, fmt.Errorf("committing provider review attempt runtime snapshot: %w", err)
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
		result["provider_review_attempt_runtime_snapshot_written"] = written > 0
		result["asset_status_snapshot_written"] = written > 0
	}
	if rowsAffectedWarning != "" {
		result["rows_affected_warning"] = rowsAffectedWarning
		result["rows_affected_unknown"] = true
		result["snapshots_skipped_as_duplicate"] = -1
		result["provider_review_attempt_runtime_snapshot_written"] = false
		result["asset_status_snapshot_written"] = false
	}
	result["message"] = "Sanitized provider review attempt runtime snapshot recorded from local adapter runtime metadata."
	return result, nil
}

func providerReviewAttemptRuntimeSnapshotPayload(attempt, ledger map[string]any, assetObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	runtimePlan := mapFromAny(dispatchPlan["adapter_runtime_plan"])
	providerClientPlan := mapFromAny(runtimePlan["provider_client_plan"])
	executeMethodPlan := mapFromAny(runtimePlan["execute_method_plan"])
	requestBuilderPlan := mapFromAny(runtimePlan["request_builder_plan"])
	responseHandlerPlan := mapFromAny(runtimePlan["response_handler_plan"])
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	runtimeContractReady := providerReviewAttemptPlanMatchesOperation(runtimePlan, "redacted_attempt_adapter_runtime_plan", operationName, endpointKey) &&
		boolOnlyFromAny(runtimePlan["adapter_interface_registered"]) &&
		boolOnlyFromAny(runtimePlan["adapter_dispatch_registered"]) &&
		boolOnlyFromAny(runtimePlan["runtime_adapter_selected"]) &&
		boolOnlyFromAny(runtimePlan["operation_supported"])
	providerClientContractReady := providerReviewAttemptPlanMatchesOperation(providerClientPlan, "redacted_attempt_adapter_provider_client_plan", operationName, endpointKey)
	executeMethodContractReady := providerReviewAttemptPlanMatchesOperation(executeMethodPlan, "redacted_attempt_adapter_execute_method_plan", operationName, endpointKey)
	requestBuilderContractReady := providerReviewAttemptPlanMatchesOperation(requestBuilderPlan, "redacted_attempt_adapter_request_builder_plan", operationName, endpointKey)
	responseHandlerContractReady := providerReviewAttemptPlanMatchesOperation(responseHandlerPlan, "redacted_attempt_adapter_response_handler_plan", operationName, endpointKey)
	// Structural write eligibility is intentionally weaker than recording readiness; readiness also requires the runtime contract checks below.
	statusSnapshotWriteEligible := assetObserved && candidateMatches && len(runtimePlan) > 0
	return map[string]any{
		"mode":                                   "provider_review_attempt_runtime_snapshot",
		"provider_review_attempt_id":             cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":                  cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"project_template_run_id":                cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed": assetObserved,
		"operation_name":                         operationName,
		"endpoint_key":                           endpointKey,
		"attempt_status":                         safeProviderReviewAttemptStatus(stringFromMap(attempt, "status")),
		"dependency_status":                      safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(attempt, "dependency_status")),
		"operation_order":                        intFromAny(attempt["operation_order"], 0),
		"candidate_observed":                     len(candidate) > 0,
		"candidate_matches_attempt":              candidateMatches,
		"candidate_status":                       cleanOptionalText(stringFromMap(candidate, "status")),
		"dispatch_plan_observed":                 len(dispatchPlan) > 0,
		"adapter_runtime_plan_observed":          len(runtimePlan) > 0,
		"provider_client_plan_observed":          len(providerClientPlan) > 0,
		"execute_method_plan_observed":           len(executeMethodPlan) > 0,
		"request_builder_plan_observed":          len(requestBuilderPlan) > 0,
		"response_handler_plan_observed":         len(responseHandlerPlan) > 0,
		"runtime_contract_ready":                 runtimeContractReady,
		"runtime_metadata_ready":                 runtimeContractReady,
		"provider_client_contract_ready":         providerClientContractReady,
		"execute_method_contract_ready":          executeMethodContractReady,
		"request_builder_contract_ready":         requestBuilderContractReady,
		"response_handler_contract_ready":        responseHandlerContractReady,
		"runtime_state":                          cleanOptionalText(stringFromMap(runtimePlan, "runtime_state")),
		"runtime_ready":                          boolOnlyFromAny(runtimePlan["runtime_ready"]),
		"runtime_ready_reason":                   cleanOptionalText(stringFromMap(runtimePlan, "runtime_ready_reason")),
		"provider_type":                          safeProviderReviewProviderType(stringFromMap(runtimePlan, "provider_type")),
		"adapter_kind":                           cleanOptionalText(stringFromMap(runtimePlan, "adapter_kind")),
		"adapter_interface_registered":           boolOnlyFromAny(runtimePlan["adapter_interface_registered"]),
		"adapter_dispatch_registered":            boolOnlyFromAny(runtimePlan["adapter_dispatch_registered"]),
		"runtime_adapter_selected":               boolOnlyFromAny(runtimePlan["runtime_adapter_selected"]),
		"operation_supported":                    boolOnlyFromAny(runtimePlan["operation_supported"]),
		"provider_client_plan_kind":              cleanOptionalText(stringFromMap(providerClientPlan, "client_kind")),
		"execute_method_name":                    cleanOptionalText(stringFromMap(executeMethodPlan, "method_name")),
		"request_builder_name":                   cleanOptionalText(stringFromMap(requestBuilderPlan, "builder_name")),
		"response_handler_name":                  cleanOptionalText(stringFromMap(responseHandlerPlan, "handler_name")),
		"required_runtime_methods":               safeProviderReviewRuntimeMethods(stringSliceFromAny(runtimePlan["required_runtime_methods"])),
		"live_adapter_implemented":               false,
		"provider_client_constructed":            false,
		"execute_method_bound":                   false,
		"request_builder_bound":                  false,
		"response_handler_bound":                 false,
		"transaction_handler_bound":              false,
		"runtime_bound":                          false,
		"mutation_armed":                         false,
		"external_call_made":                     false,
		"provider_api_call_made":                 false,
		"provider_api_mutation":                  "disabled",
		"request_body_included":                  false,
		"response_body_included":                 false,
		"headers_included":                       false,
		"authorization_header_included":          false,
		"provider_url_included":                  false,
		"idempotency_key_included":               false,
		"contains_token":                         false,
		"contains_provider_url":                  false,
		"contains_repository_ref":                false,
		"contains_branch_name":                   false,
		"contains_file_content":                  false,
		"status_snapshot_write_eligible":         statusSnapshotWriteEligible,
		"status_snapshot_written":                statusSnapshotWriteEligible,
		"runtime_boundary_redacted":              true,
		"future_live_runtime_still_blocked":      true,
		"future_live_provider_request_blocked":   true,
	}
}

func providerReviewAttemptRuntimeSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "runtime_blocked"
	if boolOnlyFromAny(snapshot["runtime_contract_ready"]) {
		state = "runtime_contract_ready"
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
	if !boolOnlyFromAny(snapshot["adapter_runtime_plan_observed"]) {
		missing = append(missing, "provider_review_runtime_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["runtime_contract_ready"]) {
		missing = append(missing, "provider_review_runtime_contract_not_ready")
	}
	if !boolOnlyFromAny(snapshot["provider_client_contract_ready"]) {
		missing = append(missing, "provider_review_provider_client_contract_not_ready")
	}
	if !boolOnlyFromAny(snapshot["execute_method_contract_ready"]) {
		missing = append(missing, "provider_review_execute_method_contract_not_ready")
	}
	if !boolOnlyFromAny(snapshot["request_builder_contract_ready"]) {
		missing = append(missing, "provider_review_request_builder_contract_not_ready")
	}
	if !boolOnlyFromAny(snapshot["response_handler_contract_ready"]) {
		missing = append(missing, "provider_review_response_handler_contract_not_ready")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["live_adapter_implemented"]) ||
		boolOnlyFromAny(snapshot["provider_client_constructed"]) ||
		boolOnlyFromAny(snapshot["execute_method_bound"]) ||
		boolOnlyFromAny(snapshot["request_builder_bound"]) ||
		boolOnlyFromAny(snapshot["response_handler_bound"]) ||
		boolOnlyFromAny(snapshot["transaction_handler_bound"]) ||
		boolOnlyFromAny(snapshot["runtime_bound"]) ||
		boolOnlyFromAny(snapshot["provider_url_included"]) ||
		boolOnlyFromAny(snapshot["authorization_header_included"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_included"]) {
		missing = append(missing, "provider_review_runtime_not_no_call")
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptRuntimeSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "runtime_contract_ready":
		return "provider_review_attempt_runtime_contract_ready", "low"
	case "runtime_blocked":
		return "provider_review_attempt_runtime_blocked", "warning"
	default:
		return "provider_review_attempt_runtime_unknown", "warning"
	}
}

func safeProviderReviewRuntimeMethods(values []string) []string {
	allowed := map[string]bool{
		"build_request":              true,
		"send_provider_request":      true,
		"handle_response":            true,
		"record_attempt_transaction": true,
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = cleanOptionalText(value)
		if allowed[value] {
			out = append(out, value)
		}
	}
	return out
}

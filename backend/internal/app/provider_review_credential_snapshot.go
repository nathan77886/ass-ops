package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptCredentialSnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
	Ledger    map[string]any
}

func RecordProviderReviewAttemptCredentialSnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptCredentialSnapshotOptions) (map[string]any, error) {
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
	snapshot := providerReviewAttemptCredentialSnapshotPayload(attempt, ledger, assetErr == nil)
	ready, state, missing := providerReviewAttemptCredentialSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_credential_snapshot_recording",
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
		"provider_review_attempt_credential_snapshot_written": false,
		"asset_status_snapshot_written":                       false,
		"operation_log_written":                               false,
		"external_call_made":                                  false,
		"provider_api_call_made":                              false,
		"provider_api_mutation":                               "disabled",
		"mutation_armed":                                      false,
		"credential_bound":                                    false,
		"authorization_header_materialized":                   false,
		"contains_token":                                      false,
		"contains_provider_url":                               false,
		"contains_repository_ref":                             false,
		"contains_branch_name":                                false,
		"contains_file_content":                               false,
		"canonical_asset_status_snapshot_try":                 false,
		"snapshot_commit_attempted":                           false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt credential snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt credential snapshot is waiting for the current execution candidate and credential binding contract; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt credential snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptCredentialSnapshotStatusHealth(state)
	tx, err := store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting provider review attempt credential snapshot transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text), hashtext($2::text))`, assetID, status); err != nil {
		return nil, fmt.Errorf("locking provider review attempt credential snapshot asset: %w", err)
	}
	execResult, err := tx.ExecContext(ctx, `
		INSERT INTO asset_status_snapshots(asset_id, status, health, summary, raw)
		SELECT $1, $2, $3, 'provider review attempt credential snapshot recorded', $4
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
		return nil, fmt.Errorf("inserting provider review attempt credential snapshot: %w", err)
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
		return nil, fmt.Errorf("committing provider review attempt credential snapshot: %w", err)
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
		result["provider_review_attempt_credential_snapshot_written"] = written > 0
		result["asset_status_snapshot_written"] = written > 0
	}
	if rowsAffectedWarning != "" {
		result["rows_affected_warning"] = rowsAffectedWarning
		result["rows_affected_unknown"] = true
		result["snapshots_skipped_as_duplicate"] = -1
		result["provider_review_attempt_credential_snapshot_written"] = false
		result["asset_status_snapshot_written"] = false
	}
	result["message"] = "Sanitized provider review attempt credential snapshot recorded from local credential binding metadata."
	return result, nil
}

func providerReviewAttemptCredentialSnapshotPayload(attempt, ledger map[string]any, assetObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	credentialPlan := mapFromAny(dispatchPlan["credential_binding_plan"])
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	credentialContractReady := providerReviewAttemptPlanMatchesOperation(credentialPlan, "redacted_attempt_adapter_credential_binding_plan", operationName, endpointKey)
	credentialReady := providerReviewAttemptCredentialPlanReadyForOperation(credentialPlan, operationName, endpointKey)
	statusSnapshotWriteEligible := assetObserved && candidateMatches && len(credentialPlan) > 0
	return map[string]any{
		"mode":                                   "provider_review_attempt_credential_snapshot",
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
		"credential_binding_plan_observed":       len(credentialPlan) > 0,
		"credential_binding_contract_ready":      credentialContractReady,
		"credential_binding_ready":               credentialReady,
		"credential_binding_state":               cleanOptionalText(stringFromMap(credentialPlan, "credential_binding_state")),
		"credential_binding_ready_reason":        cleanOptionalText(stringFromMap(credentialPlan, "credential_binding_ready_reason")),
		"provider_type":                          safeProviderReviewProviderType(stringFromMap(credentialPlan, "provider_type")),
		"auth_scheme":                            cleanOptionalText(stringFromMap(credentialPlan, "auth_scheme")),
		"credential_source_kind":                 cleanOptionalText(stringFromMap(credentialPlan, "credential_source_kind")),
		"requires_provider_account":              boolOnlyFromAny(credentialPlan["requires_provider_account"]),
		"requires_allowed_token_env":             boolOnlyFromAny(credentialPlan["requires_allowed_token_env"]),
		"requires_runtime_token_present":         boolOnlyFromAny(credentialPlan["requires_runtime_token_present"]),
		"requires_mutation_arming":               boolOnlyFromAny(credentialPlan["requires_mutation_arming"]),
		"credential_bound":                       false,
		"authorization_header_materialized":      false,
		"token_env_name_included":                false,
		"token_value_included":                   false,
		"token_stored":                           false,
		"headers_included":                       false,
		"external_call_made":                     false,
		"provider_api_call_made":                 false,
		"provider_api_mutation":                  "disabled",
		"authorization_header_included":          false,
		"provider_url_included":                  false,
		"contains_token":                         false,
		"contains_provider_url":                  false,
		"contains_repository_ref":                false,
		"contains_branch_name":                   false,
		"contains_file_content":                  false,
		"status_snapshot_write_eligible":         statusSnapshotWriteEligible,
		"status_snapshot_written":                statusSnapshotWriteEligible,
		"credential_boundary_redacted":           true,
		"future_live_credential_binding_blocked": true,
	}
}

func providerReviewAttemptCredentialSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "credential_blocked"
	if boolOnlyFromAny(snapshot["credential_binding_contract_ready"]) {
		state = "credential_contract_ready"
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
	if !boolOnlyFromAny(snapshot["credential_binding_plan_observed"]) {
		missing = append(missing, "provider_review_credential_binding_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["credential_binding_contract_ready"]) {
		missing = append(missing, "provider_review_credential_binding_contract_not_ready")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["credential_bound"]) ||
		boolOnlyFromAny(snapshot["authorization_header_materialized"]) ||
		boolOnlyFromAny(snapshot["token_value_included"]) ||
		boolOnlyFromAny(snapshot["token_stored"]) {
		missing = append(missing, "provider_review_credential_not_no_secret")
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptCredentialSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "credential_contract_ready":
		return "provider_review_attempt_credential_contract_ready", "low"
	case "credential_blocked":
		return "provider_review_attempt_credential_blocked", "warning"
	default:
		return "provider_review_attempt_credential_unknown", "warning"
	}
}

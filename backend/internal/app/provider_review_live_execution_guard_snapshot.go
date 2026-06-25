package app

import (
	"context"
	"fmt"

	"github.com/lib/pq"
)

type ProviderReviewAttemptLiveExecutionGuardSnapshotOptions struct {
	AttemptID                string
	DryRun                   bool
	Attempt                  map[string]any
	AttemptReadinessObserved bool
	ArmingObserved           bool
}

func RecordProviderReviewAttemptLiveExecutionGuardSnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptLiveExecutionGuardSnapshotOptions) (map[string]any, error) {
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
	assetID, assetErr := providerReviewAttemptAssetID(ctx, store.DB, attemptID)
	assetObserved := assetErr == nil && assetID != ""
	attemptReadinessObserved := opts.AttemptReadinessObserved
	armingObserved := opts.ArmingObserved
	if assetObserved && !attemptReadinessObserved {
		attemptReadinessObserved, err = providerReviewAttemptStatusObserved(ctx, store, assetID, "provider_review_attempt_live_execution_review_ready")
		if err != nil {
			return nil, err
		}
	}
	if approvalID != "" && !armingObserved {
		armingObserved, err = providerReviewApprovalStatusObserved(ctx, store, approvalID, "provider_review_mutation_arming_review_ready")
		if err != nil {
			return nil, err
		}
	}
	snapshot := providerReviewAttemptLiveExecutionGuardSnapshotPayload(attempt, assetObserved, attemptReadinessObserved, armingObserved)
	ready, state, missing := providerReviewAttemptLiveExecutionGuardSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_live_execution_guard_snapshot_recording",
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
		"provider_review_attempt_live_execution_guard_written": false,
		"asset_status_snapshot_written":                        false,
		"operation_log_written":                                false,
		"external_call_made":                                   false,
		"provider_api_call_made":                               false,
		"provider_api_mutation":                                "disabled",
		"provider_request_sent":                                false,
		"provider_response_received":                           false,
		"mutation_armed":                                       false,
		"live_adapter_implemented":                             false,
		"future_live_execution_still_blocked":                  true,
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
		result["message"] = "Provider review attempt live execution guard snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt live execution guard snapshot is waiting for a claimed running attempt, persisted live-readiness evidence, and mutation-arming review evidence; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt live execution guard snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptLiveExecutionGuardSnapshotStatusHealth(state)
	tx, err := store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting provider review attempt live execution guard snapshot transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text), hashtext($2::text))`, assetID, status); err != nil {
		return nil, fmt.Errorf("locking provider review attempt live execution guard snapshot asset: %w", err)
	}
	execResult, err := tx.ExecContext(ctx, `
		INSERT INTO asset_status_snapshots(asset_id, status, health, summary, raw)
		SELECT $1, $2, $3, 'provider review attempt live execution guard snapshot recorded', $4
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
		return nil, fmt.Errorf("inserting provider review attempt live execution guard snapshot: %w", err)
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
		return nil, fmt.Errorf("committing provider review attempt live execution guard snapshot: %w", err)
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
		result["provider_review_attempt_live_execution_guard_written"] = written > 0
		result["asset_status_snapshot_written"] = written > 0
	}
	if rowsAffectedWarning != "" {
		result["rows_affected_warning"] = rowsAffectedWarning
		result["rows_affected_unknown"] = true
		result["snapshots_skipped_as_duplicate"] = -1
		result["provider_review_attempt_live_execution_guard_written"] = false
		result["asset_status_snapshot_written"] = false
	}
	result["message"] = "Sanitized provider review attempt live execution guard snapshot recorded from local claim, readiness, and arming evidence."
	return result, nil
}

func providerReviewAttemptStatusObserved(ctx context.Context, store *Store, assetID, status string) (bool, error) {
	rows, err := queryMaps(ctx, store.DB, `
		SELECT DISTINCT status
		FROM asset_status_snapshots
		WHERE asset_id=$1
			AND status = ANY($2)`, assetID, pq.Array([]string{status}))
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if cleanOptionalText(stringFromMap(row, "status")) == status {
			return true, nil
		}
	}
	return false, nil
}

func providerReviewApprovalStatusObserved(ctx context.Context, store *Store, approvalID, status string) (bool, error) {
	rows, err := queryMaps(ctx, store.DB, `
		SELECT DISTINCT snapshot.status
		FROM assets a
		JOIN asset_status_snapshots snapshot ON snapshot.asset_id=a.id
		WHERE a.asset_type='operation_approval'
			AND a.source_table='operation_approvals'
			AND a.source_id=$1::uuid
			AND snapshot.status = ANY($2)`, approvalID, pq.Array([]string{status}))
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if cleanOptionalText(stringFromMap(row, "status")) == status {
			return true, nil
		}
	}
	return false, nil
}

func providerReviewAttemptLiveExecutionGuardSnapshotPayload(attempt map[string]any, assetObserved, attemptReadinessObserved, armingObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	claimRecorded := providerReviewAttemptClaimRecorded(attempt)
	attemptStatus := safeProviderReviewAttemptStatus(stringFromMap(attempt, "status"))
	guardReady := assetObserved && attemptStatus == "running" && claimRecorded && attemptReadinessObserved && armingObserved
	return map[string]any{
		"mode":                                      "provider_review_attempt_live_execution_guard_snapshot",
		"provider_review_attempt_id":                cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":                     cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"project_template_run_id":                   cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed":    assetObserved,
		"operation_name":                            operationName,
		"endpoint_key":                              endpointKey,
		"attempt_status":                            attemptStatus,
		"dependency_status":                         safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(attempt, "dependency_status")),
		"claim_recorded":                            claimRecorded,
		"claimed_at_recorded":                       claimRecorded,
		"attempt_live_execution_readiness_observed": attemptReadinessObserved,
		"mutation_arming_review_observed":           armingObserved,
		"live_execution_guard_ready":                guardReady,
		"requires_attempt_running":                  true,
		"requires_attempt_claim":                    true,
		"requires_attempt_live_execution_readiness": true,
		"requires_mutation_arming_review":           true,
		"requires_live_adapter_implementation":      true,
		"requires_provider_operator_execution":      true,
		"future_live_execution_still_blocked":       true,
		"live_adapter_implemented":                  false,
		"mutation_armed":                            false,
		"provider_request_sent":                     false,
		"provider_response_received":                false,
		"provider_api_call_made":                    false,
		"external_call_made":                        false,
		"provider_api_mutation":                     "disabled",
		"request_body_included":                     false,
		"response_body_included":                    false,
		"headers_included":                          false,
		"provider_url_included":                     false,
		"provider_request_id_included":              false,
		"idempotency_key_included":                  false,
		"contains_token":                            false,
		"contains_provider_url":                     false,
		"contains_repository_ref":                   false,
		"contains_branch_name":                      false,
		"contains_file_content":                     false,
		"status_snapshot_write_eligible":            guardReady,
		"status_snapshot_written":                   false,
		"live_execution_guard_boundary_redacted":    true,
	}
}

func providerReviewAttemptLiveExecutionGuardSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	for _, item := range []struct {
		field  string
		reason string
	}{
		{"provider_review_attempt_asset_observed", "provider_review_attempt_asset_missing"},
		{"claim_recorded", "provider_review_attempt_claim_not_recorded"},
		{"attempt_live_execution_readiness_observed", "provider_review_attempt_live_execution_readiness_missing"},
		{"mutation_arming_review_observed", "provider_review_mutation_arming_review_missing"},
		{"requires_attempt_running", "provider_review_attempt_running_requirement_missing"},
		{"requires_attempt_claim", "provider_review_attempt_claim_requirement_missing"},
		{"requires_attempt_live_execution_readiness", "provider_review_attempt_readiness_requirement_missing"},
		{"requires_mutation_arming_review", "provider_review_mutation_arming_requirement_missing"},
		{"future_live_execution_still_blocked", "provider_review_live_execution_guard_not_blocked"},
	} {
		if !boolOnlyFromAny(snapshot[item.field]) {
			missing = append(missing, item.reason)
		}
	}
	if cleanOptionalID(fmt.Sprint(snapshot["provider_review_attempt_id"])) == "" {
		missing = append(missing, "provider_review_attempt_id_missing")
	}
	if stringFromMap(snapshot, "attempt_status") != "running" {
		missing = append(missing, "provider_review_attempt_not_running")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["live_adapter_implemented"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["provider_response_received"]) ||
		boolOnlyFromAny(snapshot["request_body_included"]) ||
		boolOnlyFromAny(snapshot["response_body_included"]) ||
		boolOnlyFromAny(snapshot["headers_included"]) ||
		boolOnlyFromAny(snapshot["provider_url_included"]) ||
		boolOnlyFromAny(snapshot["provider_request_id_included"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_included"]) ||
		boolOnlyFromAny(snapshot["contains_token"]) ||
		boolOnlyFromAny(snapshot["contains_provider_url"]) ||
		boolOnlyFromAny(snapshot["contains_repository_ref"]) ||
		boolOnlyFromAny(snapshot["contains_branch_name"]) ||
		boolOnlyFromAny(snapshot["contains_file_content"]) {
		missing = append(missing, "provider_review_live_execution_guard_not_no_call")
	}
	if len(missing) == 0 {
		return true, "live_execution_guard_ready", nil
	}
	return false, "live_execution_guard_blocked", missing
}

func providerReviewAttemptLiveExecutionGuardSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "live_execution_guard_ready":
		return "provider_review_attempt_live_execution_guard_ready", "low"
	case "live_execution_guard_blocked":
		return "provider_review_attempt_live_execution_guard_blocked", "warning"
	default:
		return "provider_review_attempt_live_execution_guard_unknown", "warning"
	}
}

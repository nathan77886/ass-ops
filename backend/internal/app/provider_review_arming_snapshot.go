package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

type ProviderReviewMutationArmingSnapshotOptions struct {
	OperationApprovalID           string
	DryRun                        bool
	Approval                      map[string]any
	AttemptLedger                 map[string]any
	AttemptLiveExecutionReadiness map[string]bool
}

func RecordProviderReviewMutationArmingSnapshot(ctx context.Context, store *Store, opts ProviderReviewMutationArmingSnapshotOptions) (map[string]any, error) {
	if store == nil || store.DB == nil {
		return nil, fmt.Errorf("store is required")
	}
	approvalID := cleanOptionalID(opts.OperationApprovalID)
	if approvalID == "" {
		return nil, fmt.Errorf("operation approval id is required")
	}
	approval := opts.Approval
	var err error
	if len(approval) == 0 {
		approval, err = providerReviewApprovalForArmingSnapshot(ctx, store, approvalID)
		if err != nil {
			return nil, err
		}
	}
	ledger := opts.AttemptLedger
	if len(ledger) == 0 {
		ledger, err = providerReviewAttemptLedgerForApprovalSnapshot(ctx, store, approvalID)
		if err != nil {
			return nil, err
		}
	}
	liveReadiness := opts.AttemptLiveExecutionReadiness
	if liveReadiness == nil && providerReviewAttemptLedgerAttemptCount(ledger) > 0 {
		liveReadiness, err = providerReviewAttemptLiveExecutionReadinessForArmingSnapshot(ctx, store, ledger)
		if err != nil {
			return nil, err
		}
	}
	assetID, assetErr := operationApprovalAssetID(ctx, store, approvalID)
	snapshot := providerReviewMutationArmingSnapshotPayload(approval, ledger, assetErr == nil, liveReadiness)
	ready, state, missing := providerReviewMutationArmingSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                              "provider_review_mutation_arming_snapshot_recording",
		"recording_state":                   state,
		"recording_ready":                   ready,
		"recording_enabled":                 ready && !opts.DryRun,
		"dry_run":                           opts.DryRun,
		"operation_approval_id":             approvalID,
		"project_id":                        cleanOptionalID(fmt.Sprint(approval["project_id"])),
		"operation_approval_asset_observed": assetErr == nil,
		"snapshot":                          snapshot,
		"snapshots_written":                 0,
		"snapshots_skipped_as_duplicate":    0,
		"provider_review_mutation_arming_snapshot_written": false,
		"asset_status_snapshot_written":                    false,
		"operation_log_written":                            false,
		"external_call_made":                               false,
		"provider_api_call_made":                           false,
		"provider_api_mutation":                            "disabled",
		"mutation_armed":                                   false,
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
		result["missing_evidence"] = []string{"operation_approval_asset_missing"}
		result["message"] = "Provider review mutation arming snapshot is derived, but the canonical operation_approval asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review mutation arming snapshot is waiting for an approved provider-review execution approval, rehearsal-ready arming plan, and persisted attempt ledger; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review mutation arming snapshot was not written."
		return result, nil
	}
	status, health := providerReviewMutationArmingSnapshotStatusHealth(state)
	tx, err := store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting provider review mutation arming snapshot transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text), hashtext($2::text))`, assetID, status); err != nil {
		return nil, fmt.Errorf("locking provider review mutation arming snapshot asset: %w", err)
	}
	execResult, err := tx.ExecContext(ctx, `
		INSERT INTO asset_status_snapshots(asset_id, status, health, summary, raw)
		SELECT $1, $2, $3, 'provider review mutation arming snapshot recorded', $4
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
		return nil, fmt.Errorf("inserting provider review mutation arming snapshot: %w", err)
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
		return nil, fmt.Errorf("committing provider review mutation arming snapshot: %w", err)
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
		result["provider_review_mutation_arming_snapshot_written"] = written > 0
		result["asset_status_snapshot_written"] = written > 0
	}
	if rowsAffectedWarning != "" {
		result["rows_affected_warning"] = rowsAffectedWarning
		result["rows_affected_unknown"] = true
		result["snapshots_skipped_as_duplicate"] = -1
		result["provider_review_mutation_arming_snapshot_written"] = false
		result["asset_status_snapshot_written"] = false
	}
	result["message"] = "Sanitized provider review mutation arming snapshot recorded from local approval and attempt-ledger evidence."
	return result, nil
}

func providerReviewApprovalForArmingSnapshot(ctx context.Context, store *Store, approvalID string) (map[string]any, error) {
	return queryOne(ctx, store.DB, `
		SELECT id, project_id, operation_run_id, resource_type, resource_id, action, title, status, request_payload, created_at, updated_at
		FROM operation_approvals
		WHERE id=$1`, approvalID)
}

func providerReviewAttemptLedgerForApprovalSnapshot(ctx context.Context, store *Store, approvalID string) (map[string]any, error) {
	attempts, err := queryMaps(ctx, store.DB, `
		SELECT
			id,
			operation_name,
			endpoint_key,
			status,
			replay_check,
			conflict_policy,
			retry_policy,
			operation_order,
			depends_on_operation,
			dependency_status,
			request_summary,
			response_diagnostics,
			provider_api_call_made,
			provider_api_mutation,
			external_call_made,
			claimed_at,
			claimed_by_user_id
		FROM provider_review_attempts
		WHERE operation_approval_id=$1
		ORDER BY operation_order ASC, created_at ASC, operation_name ASC`, approvalID)
	if err != nil {
		return nil, err
	}
	return providerReviewAttemptLedgerSummary(attempts), nil
}

func providerReviewAttemptLiveExecutionReadinessForArmingSnapshot(ctx context.Context, store *Store, ledger map[string]any) (map[string]bool, error) {
	attemptIDs := providerReviewAttemptLedgerAttemptIDs(ledger)
	if len(attemptIDs) == 0 {
		return map[string]bool{}, nil
	}
	rows, err := queryMaps(ctx, store.DB, `
		SELECT DISTINCT a.source_id::text AS attempt_id
		FROM assets a
		JOIN asset_status_snapshots snapshot ON snapshot.asset_id=a.id
		WHERE a.asset_type='provider_review_attempt'
			AND a.source_table='provider_review_attempts'
			AND a.source_id::text = ANY($1)
			AND snapshot.status='provider_review_attempt_live_execution_review_ready'`, pq.Array(attemptIDs))
	if err != nil {
		return nil, err
	}
	observed := map[string]bool{}
	for _, row := range rows {
		id := cleanOptionalID(fmt.Sprint(row["attempt_id"]))
		if id != "" {
			observed[id] = true
		}
	}
	return observed, nil
}

func operationApprovalAssetID(ctx context.Context, store *Store, approvalID string) (string, error) {
	row, err := queryOne(ctx, store.DB, `
		SELECT id::text AS id
		FROM assets
		WHERE asset_type='operation_approval'
			AND source_table='operation_approvals'
			AND source_id=$1::uuid
		LIMIT 1`, approvalID)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("operation_approval asset for %s not found; run db sync-assets first", approvalID)
		}
		return "", err
	}
	assetID := strings.TrimSpace(fmt.Sprint(row["id"]))
	if assetID == "" || assetID == "<nil>" {
		return "", fmt.Errorf("operation_approval asset for %s has empty id", approvalID)
	}
	return assetID, nil
}

func providerReviewMutationArmingSnapshotPayload(approval, ledger map[string]any, assetObserved bool, liveReadiness map[string]bool) map[string]any {
	audit := operationApprovalPayloadAudit(approval)
	reconciliation := mapFromAny(audit["provider_review_reconciliation"])
	if len(reconciliation) == 0 {
		reconciliation = mapFromAny(mapFromAny(audit["approval_result"])["provider_review_reconciliation"])
	}
	armingPlan := sanitizedProviderReviewMutationArmingPlan(mapFromAny(reconciliation["mutation_arming_plan"]))
	rehearsal := mapFromAny(reconciliation["adapter_rehearsal"])
	blueprint := mapFromAny(reconciliation["execution_blueprint"])
	executionRequest := mapFromAny(audit["execution_request"])
	attemptCount := providerReviewAttemptLedgerAttemptCount(ledger)
	liveReadinessEvidence, liveReadinessObserved := providerReviewAttemptLiveExecutionReadinessEvidence(ledger, liveReadiness)
	liveReadinessComplete := attemptCount > 0 && liveReadinessObserved == attemptCount
	statusSnapshotWriteEligible := assetObserved && attemptCount > 0 && liveReadinessComplete
	return map[string]any{
		"mode":                                      "provider_review_mutation_arming_snapshot",
		"operation_approval_id":                     cleanOptionalID(fmt.Sprint(approval["id"])),
		"project_id":                                cleanOptionalID(fmt.Sprint(approval["project_id"])),
		"project_template_run_id":                   cleanOptionalID(fmt.Sprint(audit["project_template_run_id"])),
		"operation_approval_asset_observed":         assetObserved,
		"operation_approval_action":                 cleanOptionalText(stringFromMap(approval, "action")),
		"operation_approval_status":                 cleanOptionalText(stringFromMap(approval, "status")),
		"provider_type":                             cleanOptionalText(stringFromMap(armingPlan, "provider_type")),
		"review_kind":                               cleanOptionalText(stringFromMap(armingPlan, "review_kind")),
		"execution_request_status":                  cleanOptionalText(stringFromMap(executionRequest, "status")),
		"arming_status":                             safeProviderReviewMutationArmingStatus(stringFromMap(armingPlan, "status")),
		"required_config":                           "ASSOPS_ARM_PROVIDER_REVIEW_MUTATION",
		"execution_enabled_config":                  boolOnlyFromAny(armingPlan["execution_enabled_config"]),
		"adapter_rehearsal_ready":                   boolOnlyFromAny(armingPlan["adapter_rehearsal_ready"]),
		"mutation_armed_config":                     boolOnlyFromAny(armingPlan["mutation_armed_config"]),
		"mutation_armed":                            false,
		"requires_operator_review":                  true,
		"requires_adapter_rehearsal":                true,
		"adapter_mutation_currently_off":            true,
		"adapter_rehearsal_status":                  cleanOptionalText(stringFromMap(rehearsal, "status")),
		"adapter_rehearsal_operation_count":         intFromAny(rehearsal["operation_count"], 0),
		"adapter_rehearsal_ready_operation_count":   intFromAny(rehearsal["ready_operation_count"], 0),
		"adapter_rehearsal_blocked_operation_count": intFromAny(rehearsal["blocked_operation_count"], 0),
		"execution_blueprint_status":                cleanOptionalText(stringFromMap(blueprint, "status")),
		"live_adapter_implemented":                  false,
		"attempt_ledger_observed":                   attemptCount > 0,
		"attempt_count":                             attemptCount,
		"attempt_live_execution_readiness_required": true,
		"attempt_live_execution_readiness_complete": liveReadinessComplete,
		"attempt_live_execution_readiness_count":    liveReadinessObserved,
		"attempt_live_execution_readiness_evidence": liveReadinessEvidence,
		"next_attempt_operation":                    cleanOptionalText(stringFromMap(mapFromAny(ledger["orchestration"]), "next_operation")),
		"attempt_dependency_chain_status":           cleanOptionalText(stringFromMap(mapFromAny(ledger["orchestration"]), "dependency_chain_status")),
		"blocked_reasons":                           safeProviderReviewBlockedReasons(stringSliceFromAny(armingPlan["blocked_reasons"])),
		"external_call_made":                        false,
		"provider_api_call_made":                    false,
		"provider_api_mutation":                     "disabled",
		"operation_log_written":                     false,
		"request_body_included":                     false,
		"response_body_included":                    false,
		"headers_included":                          false,
		"idempotency_key_included":                  false,
		"contains_token":                            false,
		"contains_provider_url":                     false,
		"contains_repository_ref":                   false,
		"contains_branch_name":                      false,
		"contains_file_content":                     false,
		"status_snapshot_write_eligible":            statusSnapshotWriteEligible,
		"status_snapshot_written":                   statusSnapshotWriteEligible,
	}
}

func providerReviewMutationArmingSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := safeProviderReviewMutationArmingStatus(stringFromMap(snapshot, "arming_status"))
	if state == "" {
		state = "blocked"
	}
	if !boolOnlyFromAny(snapshot["operation_approval_asset_observed"]) {
		missing = append(missing, "operation_approval_asset_missing")
	}
	if cleanOptionalID(fmt.Sprint(snapshot["operation_approval_id"])) == "" {
		missing = append(missing, "operation_approval_id_missing")
	}
	if stringFromMap(snapshot, "operation_approval_action") != templateProviderReviewExecuteApprovalAction {
		missing = append(missing, "provider_review_execution_approval_action")
	}
	if stringFromMap(snapshot, "operation_approval_status") != "approved" {
		missing = append(missing, "operation_approval_not_approved")
	}
	if state != "ready_to_arm" {
		missing = append(missing, "provider_review_mutation_not_ready_to_arm")
	}
	if !boolOnlyFromAny(snapshot["execution_enabled_config"]) {
		missing = append(missing, "provider_review_execution_enabled")
	}
	if !boolOnlyFromAny(snapshot["adapter_rehearsal_ready"]) {
		missing = append(missing, "provider_review_adapter_rehearsal")
	}
	if !boolOnlyFromAny(snapshot["attempt_ledger_observed"]) {
		missing = append(missing, "provider_review_attempt_ledger")
	}
	if !boolOnlyFromAny(snapshot["attempt_live_execution_readiness_complete"]) {
		missing = append(missing, "provider_review_attempt_live_execution_readiness")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) {
		missing = append(missing, "provider_review_mutation_not_no_call")
	}
	if len(missing) > 0 {
		return false, state, missing
	}
	return true, "ready_for_operator_review", nil
}

func providerReviewAttemptLedgerAttemptCount(ledger map[string]any) int {
	if count := intFromAny(ledger["attempt_count"], 0); count > 0 {
		return count
	}
	// If callers omit attempt_count, fall back to operations; mismatched count vs operations blocks readiness.
	return len(providerReviewAttemptLedgerOperationsFromAny(ledger["operations"]))
}

func providerReviewAttemptLedgerAttemptIDs(ledger map[string]any) []string {
	operations := providerReviewAttemptLedgerOperationsFromAny(ledger["operations"])
	ids := make([]string, 0, len(operations))
	seen := map[string]bool{}
	for _, operation := range operations {
		id := cleanOptionalID(fmt.Sprint(operation["id"]))
		if id == "" || seen[id] {
			continue
		}
		ids = append(ids, id)
		seen[id] = true
	}
	return ids
}

func providerReviewAttemptLiveExecutionReadinessEvidence(ledger map[string]any, observed map[string]bool) ([]map[string]any, int) {
	if observed == nil {
		observed = map[string]bool{}
	}
	operations := providerReviewAttemptLedgerOperationsFromAny(ledger["operations"])
	evidence := make([]map[string]any, 0, len(operations))
	observedCount := 0
	for _, operation := range operations {
		id := cleanOptionalID(fmt.Sprint(operation["id"]))
		ready := id != "" && observed[id]
		if ready {
			observedCount++
		}
		evidence = append(evidence, map[string]any{
			"operation_name": cleanOptionalText(stringFromMap(operation, "name")),
			"endpoint_key":   cleanOptionalText(stringFromMap(operation, "endpoint_key")),
			"status":         cleanOptionalText(stringFromMap(operation, "status")),
			"observed":       ready,
		})
	}
	return evidence, observedCount
}

func providerReviewAttemptLedgerOperationsFromAny(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if row := mapFromAny(item); len(row) > 0 {
				out = append(out, row)
			}
		}
		return out
	default:
		return nil
	}
}

func providerReviewMutationArmingSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "ready_for_operator_review":
		return "provider_review_mutation_arming_review_ready", "low"
	case "blocked":
		return "provider_review_mutation_arming_blocked", "warning"
	default:
		return "provider_review_mutation_arming_" + cleanOptionalText(state), "warning"
	}
}

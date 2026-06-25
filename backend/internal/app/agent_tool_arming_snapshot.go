package app

import (
	"context"
	"fmt"
	"strings"
)

type AgentToolArmingSnapshotOptions struct {
	AgentTaskID string
	DryRun      bool
	Task        map[string]any
}

func RecordAgentToolArmingSnapshot(ctx context.Context, store *Store, opts AgentToolArmingSnapshotOptions) (map[string]any, error) {
	taskID := strings.TrimSpace(opts.AgentTaskID)
	if taskID == "" {
		return nil, fmt.Errorf("agent task id is required")
	}
	task := opts.Task
	if len(task) == 0 {
		var err error
		task, err = agentTaskForToolAuditSnapshot(ctx, store.DB, taskID)
		if err != nil {
			return nil, err
		}
	}
	projectID := strings.TrimSpace(fmt.Sprint(task["project_id"]))
	toolCalls, err := agentTaskToolCallsForSnapshot(ctx, store.DB, taskID)
	if err != nil {
		return nil, fmt.Errorf("loading agent tool arming audit evidence: %w", err)
	}
	runtime, err := latestProjectAIRuntime(ctx, store.DB, projectID)
	if err != nil {
		return nil, err
	}
	evidence := agentToolCallAuditEvidence(toolCalls)
	dispatchPlan := agentWorkerDispatchPlan(runtime, evidence)
	assetID, assetErr := agentTaskAssetID(ctx, store.DB, taskID)
	snapshot := agentToolArmingSnapshotPayload(task, dispatchPlan, assetErr == nil)
	ready, state, missing := agentToolArmingSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                 "agent_tool_arming_snapshot_recording",
		"recording_state":                      state,
		"recording_ready":                      ready,
		"recording_enabled":                    ready && !opts.DryRun,
		"dry_run":                              opts.DryRun,
		"project_id":                           projectID,
		"agent_task_id":                        taskID,
		"agent_task_asset_observed":            assetErr == nil,
		"snapshot":                             snapshot,
		"snapshots_written":                    0,
		"snapshots_skipped_as_duplicate":       0,
		"agent_tool_arming_snapshot_written":   false,
		"asset_status_snapshot_written":        false,
		"operation_log_written":                false,
		"external_call_made":                   false,
		"tool_invocation_enabled":              false,
		"tool_invoked":                         false,
		"allowlisted_tool_invoked":             false,
		"raw_tool_input_materialized":          false,
		"raw_tool_output_recorded":             false,
		"runtime_config_materialized":          false,
		"codex_cli_process_started":            false,
		"repository_mutation_allowed":          false,
		"secret_included":                      false,
		"arming_ready_for_operator_review":     boolOnlyFromAny(snapshot["arming_ready_for_operator_review"]),
		"tool_review_ready_for_operator":       boolOnlyFromAny(snapshot["tool_review_ready_for_operator"]),
		"sanitized_result_recorded":            boolOnlyFromAny(snapshot["sanitized_result_recorded"]),
		"successful_sanitized_result_recorded": boolOnlyFromAny(snapshot["successful_sanitized_result_recorded"]),
		"canonical_asset_status_snapshot_try":  false,
		"snapshot_commit_attempted":            false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"agent_task_asset_missing"}
		result["message"] = "Agent tool arming snapshot is derived, but the canonical agent_task asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Agent tool arming snapshot is waiting for runtime, allowlist, terminal audit, and callback evidence ready for operator review; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized agent tool arming snapshot was not written."
		return result, nil
	}
	status, health := agentToolArmingSnapshotStatusHealth(state)
	tx, err := store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting agent tool arming snapshot transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text), hashtext($2::text))`, assetID, status); err != nil {
		return nil, fmt.Errorf("locking agent tool arming snapshot asset: %w", err)
	}
	execResult, err := tx.ExecContext(ctx, `
		INSERT INTO asset_status_snapshots(asset_id, status, health, summary, raw)
		SELECT $1, $2, $3, 'agent tool arming snapshot recorded', $4
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
		return nil, fmt.Errorf("inserting agent tool arming snapshot: %w", err)
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
		return nil, fmt.Errorf("committing agent tool arming snapshot: %w", err)
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
		result["agent_tool_arming_snapshot_written"] = written > 0
		result["asset_status_snapshot_written"] = written > 0
	}
	if rowsAffectedWarning != "" {
		result["rows_affected_warning"] = rowsAffectedWarning
		result["rows_affected_unknown"] = true
		result["snapshots_skipped_as_duplicate"] = -1
		result["agent_tool_arming_snapshot_written"] = false
		result["asset_status_snapshot_written"] = false
	}
	result["message"] = "Sanitized agent tool arming snapshot recorded from local audit evidence."
	return result, nil
}

func agentToolArmingSnapshotPayload(task, dispatchPlan map[string]any, assetObserved bool) map[string]any {
	armingPlan := mapFromAny(dispatchPlan["tool_execution_arming_plan"])
	reviewPlan := mapFromAny(dispatchPlan["tool_invocation_review_plan"])
	callbackPlan := mapFromAny(dispatchPlan["result_callback_plan"])
	evidence := mapFromAny(dispatchPlan["tool_call_audit_evidence"])
	statusSnapshotWriteEligible := assetObserved
	return map[string]any{
		"mode":                                 "agent_tool_invocation_arming_snapshot",
		"agent_task_id":                        cleanPreviewString(task["id"]),
		"project_id":                           cleanPreviewString(task["project_id"]),
		"agent_task_status":                    cleanPreviewString(task["status"]),
		"agent_task_asset_observed":            assetObserved,
		"status_snapshot_write_eligible":       statusSnapshotWriteEligible,
		"status_snapshot_written":              statusSnapshotWriteEligible,
		"dispatch_state":                       cleanPreviewString(dispatchPlan["dispatch_state"]),
		"dispatch_prerequisite_state":          cleanPreviewString(dispatchPlan["prerequisite_state"]),
		"runtime_metadata_ready":               boolOnlyFromAny(armingPlan["metadata_ready"]),
		"tool_allowlist_ready":                 boolOnlyFromAny(armingPlan["allowlist_ready"]),
		"allowed_tool_count":                   intFromAny(armingPlan["allowed_tool_count"], 0),
		"audit_evidence_observed":              boolOnlyFromAny(armingPlan["audit_evidence_observed"]),
		"terminal_audit_observed":              boolOnlyFromAny(armingPlan["terminal_audit_observed"]),
		"successful_audit_recorded":            boolOnlyFromAny(armingPlan["successful_audit_recorded"]),
		"sanitized_result_recorded":            boolOnlyFromAny(reviewPlan["sanitized_result_recorded"]),
		"successful_sanitized_result_recorded": boolOnlyFromAny(reviewPlan["successful_sanitized_result_recorded"]),
		"result_callback_wired":                boolOnlyFromAny(armingPlan["result_callback_wired"]),
		"result_callback_observed":             boolOnlyFromAny(armingPlan["result_callback_observed"]),
		"callback_state":                       cleanPreviewString(callbackPlan["callback_state"]),
		"callback_scope":                       cleanPreviewString(callbackPlan["callback_scope"]),
		"tool_call_count":                      intFromAny(evidence["tool_call_count"], 0),
		"active_count":                         intFromAny(evidence["active_count"], 0),
		"terminal_count":                       intFromAny(evidence["terminal_count"], 0),
		"completed_count":                      intFromAny(evidence["completed_count"], 0),
		"failed_count":                         intFromAny(evidence["failed_count"], 0),
		"arming_state":                         cleanPreviewString(armingPlan["arming_state"]),
		"arming_ready_for_operator_review":     boolOnlyFromAny(armingPlan["arming_ready"]),
		"arming_ready_reason":                  cleanPreviewString(armingPlan["arming_ready_reason"]),
		"tool_review_state":                    cleanPreviewString(reviewPlan["review_state"]),
		"tool_review_ready_for_operator":       boolOnlyFromAny(reviewPlan["review_ready"]),
		"tool_review_ready_reason":             cleanPreviewString(reviewPlan["review_ready_reason"]),
		"live_tool_invocation_allowed":         false,
		"tool_invocation_enabled":              false,
		"tool_invoked":                         false,
		"allowlisted_tool_invoked":             false,
		"tool_input_materialized":              false,
		"tool_output_recorded":                 false,
		"raw_tool_input_materialized":          false,
		"raw_tool_output_recorded":             false,
		"runtime_config_materialized":          false,
		"codex_cli_process_started":            false,
		"patch_applied":                        false,
		"repository_mutation_allowed":          false,
		"external_call_made":                   false,
		"operation_log_written":                false,
		"contains_runtime_config":              false,
		"contains_prompt_body":                 false,
		"contains_tool_input":                  false,
		"contains_tool_output":                 false,
		"contains_patch_content":               false,
		"contains_diff_content":                false,
		"contains_token":                       false,
		"allowed_tool_names":                   armingPlan["allowed_tool_names"],
		"required_controls":                    armingPlan["required_controls"],
		"required_operator_controls":           reviewPlan["required_operator_controls"],
		"disabled_backends":                    reviewPlan["disabled_backends"],
		"suppressed_fields":                    reviewPlan["suppressed_fields"],
		"arming_missing_evidence":              armingPlan["missing_evidence"],
		"review_missing_evidence":              reviewPlan["missing_evidence"],
		"future_live_tool_invocation_blocked":  true,
	}
}

func agentToolArmingSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := cleanPreviewString(snapshot["arming_state"])
	if state == "" {
		state = "blocked"
	}
	if !boolOnlyFromAny(snapshot["agent_task_asset_observed"]) {
		missing = append(missing, "agent_task_asset_missing")
	}
	if !boolOnlyFromAny(snapshot["runtime_metadata_ready"]) {
		missing = append(missing, "runtime_metadata")
	}
	if !boolOnlyFromAny(snapshot["tool_allowlist_ready"]) {
		missing = append(missing, "tool_allowlist")
	}
	if !boolOnlyFromAny(snapshot["result_callback_wired"]) {
		missing = append(missing, "result_callback")
	}
	if !boolOnlyFromAny(snapshot["audit_evidence_observed"]) {
		missing = append(missing, "tool_call_audit_evidence")
	}
	if boolOnlyFromAny(snapshot["audit_evidence_observed"]) && !boolOnlyFromAny(snapshot["terminal_audit_observed"]) {
		missing = append(missing, "terminal_tool_call_audit")
	}
	if boolOnlyFromAny(snapshot["audit_evidence_observed"]) && boolOnlyFromAny(snapshot["terminal_audit_observed"]) && !boolOnlyFromAny(snapshot["successful_audit_recorded"]) {
		missing = append(missing, "successful_tool_call_audit")
	}
	if boolOnlyFromAny(snapshot["audit_evidence_observed"]) && !boolOnlyFromAny(snapshot["successful_sanitized_result_recorded"]) {
		missing = append(missing, "sanitized_result_recording")
	}
	if !boolOnlyFromAny(snapshot["result_callback_observed"]) {
		missing = append(missing, "result_callback_observation")
	}
	if !boolOnlyFromAny(snapshot["arming_ready_for_operator_review"]) {
		missing = append(missing, "tool_execution_arming_not_ready")
	}
	if !boolOnlyFromAny(snapshot["tool_review_ready_for_operator"]) {
		missing = append(missing, "tool_invocation_review_not_ready")
	}
	if len(missing) > 0 {
		return false, state, missing
	}
	return true, "ready_for_operator_review", nil
}

func agentToolArmingSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "ready_for_operator_review":
		return "agent_tool_arming_review_ready", "low"
	case "failed", "mixed_failed", "canceled", "unknown", "absent":
		return "agent_tool_arming_" + state, "high"
	default:
		return "agent_tool_arming_" + state, "warning"
	}
}

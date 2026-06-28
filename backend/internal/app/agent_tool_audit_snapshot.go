package app

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AgentToolAuditSnapshotOptions struct {
	AgentTaskID string
	DryRun      bool
	Task        map[string]any
}

func RecordAgentToolAuditSnapshot(ctx context.Context, store *Store, opts AgentToolAuditSnapshotOptions) (map[string]any, error) {
	taskID := strings.TrimSpace(opts.AgentTaskID)
	if taskID == "" {
		return nil, fmt.Errorf("agent task id is required")
	}
	task := opts.Task
	if len(task) == 0 {
		var err error
		task, err = agentTaskForToolAuditSnapshot(ctx, store.Gorm, taskID)
		if err != nil {
			return nil, err
		}
	}
	toolCalls, err := agentTaskToolCallsForSnapshot(ctx, store.Gorm, taskID)
	if err != nil {
		return nil, fmt.Errorf("loading agent tool-call audit evidence: %w", err)
	}
	evidence := agentToolCallAuditEvidence(toolCalls)
	callbackPlan := agentWorkerResultCallbackPlan(evidence)
	assetID, assetErr := agentTaskAssetID(ctx, store.Gorm, taskID)
	snapshot := agentToolAuditSnapshotPayload(task, evidence, callbackPlan, assetErr == nil)
	ready, state, missing := agentToolAuditSnapshotReadiness(snapshot)
	projectID := strings.TrimSpace(fmt.Sprint(task["project_id"]))
	result := map[string]any{
		"mode":                                "agent_tool_audit_snapshot_recording",
		"recording_state":                     state,
		"recording_ready":                     ready,
		"recording_enabled":                   ready && !opts.DryRun,
		"dry_run":                             opts.DryRun,
		"project_id":                          projectID,
		"agent_task_id":                       taskID,
		"agent_task_asset_observed":           assetErr == nil,
		"snapshot":                            snapshot,
		"snapshots_written":                   0,
		"snapshots_skipped_as_duplicate":      0,
		"agent_tool_audit_snapshot_written":   false,
		"asset_status_snapshot_written":       false,
		"operation_log_written":               false,
		"external_call_made":                  false,
		"tool_invocation_enabled":             false,
		"tool_invoked":                        false,
		"codex_cli_process_started":           false,
		"repository_mutation_allowed":         false,
		"raw_tool_input_materialized":         false,
		"raw_tool_output_recorded":            false,
		"runtime_config_materialized":         false,
		"prompt_body_included":                false,
		"patch_content_included":              false,
		"diff_content_included":               false,
		"secret_included":                     false,
		"sanitized_result_recorded":           boolOnlyFromAny(snapshot["sanitized_result_recorded"]),
		"tool_call_count":                     intFromAny(snapshot["tool_call_count"], 0),
		"active_count":                        intFromAny(snapshot["active_count"], 0),
		"terminal_count":                      intFromAny(snapshot["terminal_count"], 0),
		"canonical_asset_status_snapshot_try": false,
		"snapshot_commit_attempted":           false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"agent_task_asset_missing"}
		result["message"] = "Agent tool-call audit snapshot is derived, but the canonical agent_task asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Agent tool-call audit snapshot is waiting for terminal sanitized audit evidence; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized agent tool-call audit snapshot was not written."
		return result, nil
	}
	status, health := agentToolAuditSnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "agent tool-call audit snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording agent tool-call audit snapshot: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["agent_tool_audit_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized agent tool-call audit snapshot recorded from local audit evidence."
	return result, nil
}

func agentTaskForToolAuditSnapshot(ctx context.Context, db *gorm.DB, taskID string) (map[string]any, error) {
	if db == nil {
		return nil, fmt.Errorf("gorm database is not configured")
	}
	var task GormAgentTask
	if err := db.WithContext(ctx).First(&task, &GormAgentTask{GormBase: GormBase{ID: taskID}}).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return map[string]any{
		"id":         task.ID,
		"project_id": task.ProjectID,
		"status":     task.Status,
		"created_at": task.CreatedAt,
		"updated_at": task.UpdatedAt,
	}, nil
}

func agentToolAuditSnapshotStatusHealth(state string) (string, string) {
	status := "agent_tool_audit_" + state
	health := "warning"
	switch state {
	case "recorded":
		health = "low"
	case "failed", "mixed_failed", "canceled", "unknown", "absent":
		health = "high"
	}
	return status, health
}

func agentTaskToolCallsForSnapshot(ctx context.Context, db *gorm.DB, taskID string) ([]map[string]any, error) {
	if db == nil {
		return nil, fmt.Errorf("gorm database is not configured")
	}
	var calls []GormAgentToolCall
	if err := db.WithContext(ctx).
		Where(&GormAgentToolCall{AgentTaskID: taskID}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}, Desc: true}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}, Desc: true}).
		Limit(100).
		Find(&calls).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		items = append(items, map[string]any{
			"id":               call.ID,
			"agent_task_id":    call.AgentTaskID,
			"operation_run_id": call.OperationRunID,
			"project_id":       call.ProjectID,
			"tool_name":        call.ToolName,
			"status":           call.Status,
			"started_at":       call.StartedAt,
			"finished_at":      call.FinishedAt,
			"created_at":       call.CreatedAt,
			"updated_at":       call.CreatedAt,
		})
	}
	return items, nil
}

func agentTaskAssetID(ctx context.Context, db *gorm.DB, taskID string) (string, error) {
	if db == nil {
		return "", fmt.Errorf("gorm database is not configured")
	}
	var asset GormAsset
	if err := db.WithContext(ctx).
		Where(&GormAsset{AssetType: "agent_task", SourceTable: "agent_tasks", SourceID: validNullString(taskID)}).
		First(&asset).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return "", fmt.Errorf("agent_task asset for %s not found; run db sync-assets first", taskID)
		}
		return "", err
	}
	assetID := strings.TrimSpace(asset.ID)
	if assetID == "" || assetID == "<nil>" {
		return "", fmt.Errorf("agent_task asset for %s has empty id", taskID)
	}
	return assetID, nil
}

func agentToolAuditSnapshotPayload(task, evidence, callbackPlan map[string]any, assetObserved bool) map[string]any {
	// Audit snapshots are structurally complete from local task/evidence data; the handler still gates actual writes on asset presence.
	statusSnapshotWriteEligible := true
	return map[string]any{
		"mode":                                "agent_tool_call_audit_snapshot",
		"agent_task_id":                       cleanPreviewString(task["id"]),
		"project_id":                          cleanPreviewString(task["project_id"]),
		"agent_task_asset_observed":           assetObserved,
		"agent_task_status":                   cleanPreviewString(task["status"]),
		"evidence_state":                      cleanPreviewString(evidence["evidence_state"]),
		"tool_call_count":                     intFromAny(evidence["tool_call_count"], 0),
		"queued_count":                        intFromAny(evidence["queued_count"], 0),
		"running_count":                       intFromAny(evidence["running_count"], 0),
		"completed_count":                     intFromAny(evidence["completed_count"], 0),
		"failed_count":                        intFromAny(evidence["failed_count"], 0),
		"canceled_count":                      intFromAny(evidence["canceled_count"], 0),
		"unknown_count":                       intFromAny(evidence["unknown_count"], 0),
		"absent_count":                        intFromAny(evidence["absent_count"], 0),
		"active_count":                        intFromAny(evidence["active_count"], 0),
		"terminal_count":                      intFromAny(evidence["terminal_count"], 0),
		"has_tool_call_audit":                 boolOnlyFromAny(evidence["has_tool_call_audit"]),
		"sanitized_result_recorded":           boolOnlyFromAny(evidence["sanitized_result_recorded"]),
		"has_failures":                        boolOnlyFromAny(evidence["has_failures"]),
		"has_cancellations":                   boolOnlyFromAny(evidence["has_cancellations"]),
		"has_unknown_status":                  boolOnlyFromAny(evidence["has_unknown_status"]),
		"has_absent_status":                   boolOnlyFromAny(evidence["has_absent_status"]),
		"result_callback_state":               cleanPreviewString(callbackPlan["callback_state"]),
		"result_callback_enabled":             boolOnlyFromAny(callbackPlan["callback_enabled"]),
		"result_written":                      boolOnlyFromAny(callbackPlan["result_written"]),
		"tool_call_status_written":            boolOnlyFromAny(callbackPlan["tool_call_status_written"]),
		"status_snapshot_write_eligible":      statusSnapshotWriteEligible,
		"status_snapshot_written":             statusSnapshotWriteEligible,
		"canonical_asset_status_snapshot_try": assetObserved,
		"external_call_made":                  false,
		"tool_invocation_enabled":             false,
		"tool_invoked":                        false,
		"allowlisted_tool_invoked":            false,
		"codex_cli_process_started":           false,
		"repository_mutation_allowed":         false,
		"raw_tool_input_materialized":         false,
		"raw_tool_output_recorded":            false,
		"raw_runtime_output_recorded":         false,
		"raw_patch_recorded":                  false,
		"raw_diff_recorded":                   false,
		"input_included":                      false,
		"output_included":                     false,
		"runtime_config_materialized":         false,
		"prompt_body_included":                false,
		"patch_content_included":              false,
		"diff_content_included":               false,
		"secret_included":                     false,
		"sanitized_metadata_only":             true,
		"suppressed_fields": []string{
			"title", "prompt", "runtime_config", "environment_variables", "authorization_header",
			"workspace_path", "repository_url", "prompt_body", "tool_input", "tool_output",
			"raw_tool_input", "raw_tool_output", "patch_content", "diff_content", "file_content",
			"token", "api_key", "bearer_token", "secret",
		},
	}
}

func agentToolAuditSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := make([]string, 0)
	state := cleanPreviewString(snapshot["evidence_state"])
	if state == "" {
		state = "not_recorded"
	}
	if !boolOnlyFromAny(snapshot["has_tool_call_audit"]) {
		missing = append(missing, "agent_tool_call_audit_missing")
	}
	if intFromAny(snapshot["active_count"], 0) > 0 {
		missing = append(missing, "agent_tool_call_audit_active")
	}
	if !boolOnlyFromAny(snapshot["sanitized_result_recorded"]) {
		missing = append(missing, "sanitized_agent_tool_result_not_recorded")
	}
	if len(missing) > 0 {
		return false, state, missing
	}
	return true, state, nil
}

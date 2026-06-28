package app

import (
	"context"
	"fmt"
	"strings"
)

type AgentCodeAuditSnapshotOptions struct {
	AgentTaskID string
	DryRun      bool
	Task        map[string]any
}

func RecordAgentCodeAuditSnapshot(ctx context.Context, store *Store, opts AgentCodeAuditSnapshotOptions) (map[string]any, error) {
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
		return nil, fmt.Errorf("loading agent code audit evidence: %w", err)
	}
	toolEvidence := agentToolCallAuditEvidence(toolCalls)
	codeEvidence := agentCodeModificationEvidence(toolEvidence)
	executionArmingPlan := agentCodeModificationExecutionArmingPlan(codeEvidence)
	sourceReviewPlan := agentCodeModificationSourceCheckoutBranchReviewPlan(codeEvidence, executionArmingPlan)
	resultRecordingPlan := agentCodeModificationResultRecordingPlan(codeEvidence)
	assetID, assetErr := agentTaskAssetID(ctx, store.Gorm, taskID)
	snapshot := agentCodeAuditSnapshotPayload(task, codeEvidence, resultRecordingPlan, executionArmingPlan, sourceReviewPlan, assetErr == nil)
	ready, state, missing := agentCodeAuditSnapshotReadiness(snapshot)
	projectID := strings.TrimSpace(fmt.Sprint(task["project_id"]))
	result := map[string]any{
		"mode":                                "agent_code_audit_snapshot_recording",
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
		"agent_code_audit_snapshot_written":   false,
		"asset_status_snapshot_written":       false,
		"operation_log_written":               false,
		"external_call_made":                  false,
		"source_checkout_performed":           false,
		"workspace_bound":                     false,
		"branch_created":                      false,
		"default_branch_checked_out":          false,
		"patch_content_materialized":          false,
		"diff_materialized":                   false,
		"file_patch_applied":                  false,
		"tests_executed":                      false,
		"git_commit_created":                  false,
		"git_push_performed":                  false,
		"provider_review_created":             false,
		"commit_push_agent_invoked":           false,
		"repository_mutation_allowed":         false,
		"raw_patch_recorded":                  false,
		"raw_diff_recorded":                   false,
		"raw_file_content_recorded":           false,
		"raw_test_output_recorded":            false,
		"raw_command_output_recorded":         false,
		"contains_workspace_path":             false,
		"contains_branch_name":                false,
		"contains_default_branch_name":        false,
		"contains_patch_content":              false,
		"contains_diff_content":               false,
		"contains_file_content":               false,
		"contains_token":                      false,
		"has_code_modification_audit":         boolOnlyFromAny(snapshot["has_code_modification_audit"]),
		"sanitized_result_recorded":           boolOnlyFromAny(snapshot["sanitized_result_recorded"]),
		"execution_arming_ready":              boolOnlyFromAny(snapshot["execution_arming_ready"]),
		"source_checkout_branch_review_ready": boolOnlyFromAny(snapshot["source_checkout_branch_review_ready"]),
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
		result["message"] = "Agent code audit snapshot is derived, but the canonical agent_task asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Agent code audit snapshot is waiting for complete terminal sanitized code-modification audit evidence; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized agent code audit snapshot was not written."
		return result, nil
	}
	status, health := agentCodeAuditSnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "agent code audit snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording agent code audit snapshot: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["agent_code_audit_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized agent code audit snapshot recorded from local audit evidence."
	return result, nil
}

func agentCodeAuditSnapshotStatusHealth(state string) (string, string) {
	status := "agent_code_audit_" + state
	health := "warning"
	switch state {
	case "recorded":
		health = "low"
	case "failed", "mixed_failed", "canceled", "unknown", "absent":
		health = "high"
	}
	return status, health
}

func agentCodeAuditSnapshotPayload(task, codeEvidence, resultRecordingPlan, executionArmingPlan, sourceReviewPlan map[string]any, assetObserved bool) map[string]any {
	// Code audit snapshots are structurally complete from local audit plans; the handler still gates actual writes on asset presence.
	statusSnapshotWriteEligible := true
	return map[string]any{
		"mode":                                "agent_code_modification_audit_snapshot",
		"agent_task_id":                       cleanPreviewString(task["id"]),
		"project_id":                          cleanPreviewString(task["project_id"]),
		"agent_task_asset_observed":           assetObserved,
		"agent_task_status":                   cleanPreviewString(task["status"]),
		"evidence_state":                      cleanPreviewString(codeEvidence["evidence_state"]),
		"tool_call_audit_state":               cleanPreviewString(codeEvidence["tool_call_audit_state"]),
		"has_code_modification_audit":         boolOnlyFromAny(codeEvidence["has_code_modification_audit"]),
		"sanitized_result_recorded":           boolOnlyFromAny(codeEvidence["sanitized_result_recorded"]),
		"worker_dispatch_audit_recorded":      boolOnlyFromAny(codeEvidence["worker_dispatch_audit_recorded"]),
		"codex_execution_plan_recorded":       boolOnlyFromAny(codeEvidence["codex_execution_plan_recorded"]),
		"patch_prepare_audit_recorded":        boolOnlyFromAny(codeEvidence["patch_prepare_audit_recorded"]),
		"completed_tool_call_count":           intFromAny(codeEvidence["completed_tool_call_count"], 0),
		"failed_tool_call_count":              intFromAny(codeEvidence["failed_tool_call_count"], 0),
		"active_tool_call_count":              intFromAny(codeEvidence["active_tool_call_count"], 0),
		"terminal_tool_call_count":            intFromAny(codeEvidence["terminal_tool_call_count"], 0),
		"result_recording_state":              cleanPreviewString(resultRecordingPlan["recording_state"]),
		"result_recording_enabled":            boolOnlyFromAny(resultRecordingPlan["recording_enabled"]),
		"result_written":                      boolOnlyFromAny(resultRecordingPlan["result_written"]),
		"execution_arming_state":              cleanPreviewString(executionArmingPlan["arming_state"]),
		"execution_arming_ready":              boolOnlyFromAny(executionArmingPlan["arming_ready"]),
		"source_checkout_branch_review_state": cleanPreviewString(sourceReviewPlan["review_state"]),
		"source_checkout_branch_review_ready": boolOnlyFromAny(sourceReviewPlan["review_ready"]),
		"review_branch_required":              boolOnlyFromAny(sourceReviewPlan["review_branch_required"]),
		"default_branch_direct_write_blocked": boolOnlyFromAny(sourceReviewPlan["default_branch_direct_write_blocked"]),
		"status_snapshot_write_eligible":      statusSnapshotWriteEligible,
		"status_snapshot_written":             statusSnapshotWriteEligible,
		"canonical_asset_status_snapshot_try": assetObserved,
		"operation_log_written":               false,
		"patch_artifact_written":              false,
		"diff_artifact_written":               false,
		"test_result_written":                 false,
		"commit_record_written":               false,
		"push_record_written":                 false,
		"pr_record_written":                   false,
		"external_call_made":                  false,
		"source_checkout_performed":           false,
		"workspace_bound":                     false,
		"branch_created":                      false,
		"default_branch_checked_out":          false,
		"git_fetch_performed":                 false,
		"git_checkout_performed":              false,
		"git_branch_created":                  false,
		"patch_content_materialized":          false,
		"diff_materialized":                   false,
		"file_patch_applied":                  false,
		"tests_executed":                      false,
		"git_commit_created":                  false,
		"git_push_performed":                  false,
		"pull_request_created":                false,
		"provider_review_created":             false,
		"commit_push_agent_invoked":           false,
		"repository_mutation_allowed":         false,
		"raw_patch_recorded":                  false,
		"raw_diff_recorded":                   false,
		"raw_file_content_recorded":           false,
		"raw_command_output_recorded":         false,
		"raw_test_output_recorded":            false,
		"contains_token":                      false,
		"contains_remote_url":                 false,
		"contains_source_remote_url":          false,
		"contains_workspace_path":             false,
		"contains_branch_name":                false,
		"contains_default_branch_name":        false,
		"contains_patch_content":              false,
		"contains_diff_content":               false,
		"contains_file_content":               false,
		"contains_test_output":                false,
		"sanitized_metadata_only":             true,
		"required_audit_evidence":             []string{"worker_dispatch_plan_audit", "codex_execution_plan_audit", "patch_prepare_audit", "terminal_tool_call_audit"},
		"missing_audit_evidence":              stringSliceFromAny(codeEvidence["missing_audit_evidence"]),
		"suppressed_fields": []string{
			"title", "prompt", "runtime_config", "environment_variables", "authorization_header",
			"source_remote_url", "repository_url", "workspace_path", "branch_name", "default_branch",
			"review_branch_name", "prompt_body", "tool_input", "tool_output", "raw_tool_input",
			"raw_tool_output", "patch_content", "diff_content", "file_content", "test_output",
			"command_output", "token", "api_key", "bearer_token", "secret",
		},
	}
}

func agentCodeAuditSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := append([]string{}, stringSliceFromAny(snapshot["missing_audit_evidence"])...)
	state := cleanPreviewString(snapshot["evidence_state"])
	if state == "" {
		state = "not_recorded"
	}
	if !boolOnlyFromAny(snapshot["has_code_modification_audit"]) {
		missing = append(missing, "agent_code_modification_audit_missing")
	}
	if state != "recorded" {
		missing = append(missing, "agent_code_modification_audit_incomplete")
	}
	if intFromAny(snapshot["active_tool_call_count"], 0) > 0 {
		missing = append(missing, "agent_tool_call_audit_active")
	}
	if !boolOnlyFromAny(snapshot["sanitized_result_recorded"]) {
		missing = append(missing, "sanitized_code_modification_result")
	}
	if !boolOnlyFromAny(snapshot["worker_dispatch_audit_recorded"]) {
		missing = append(missing, "worker_dispatch_plan_audit")
	}
	if !boolOnlyFromAny(snapshot["codex_execution_plan_recorded"]) {
		missing = append(missing, "codex_execution_plan_audit")
	}
	if !boolOnlyFromAny(snapshot["patch_prepare_audit_recorded"]) {
		missing = append(missing, "patch_prepare_audit")
	}
	missing = uniqueStrings(missing)
	if len(missing) > 0 {
		return false, state, missing
	}
	return true, state, nil
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		cleaned := strings.TrimSpace(value)
		if cleaned == "" || seen[cleaned] {
			continue
		}
		seen[cleaned] = true
		out = append(out, cleaned)
	}
	return out
}

package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

type ProjectVersionValidationSnapshotOptions struct {
	ProjectVersionID       string
	DryRun                 bool
	RequireRecordedRefresh bool
	RecordingTrigger       string
}

func RecordProjectVersionValidationSnapshot(ctx context.Context, store *Store, opts ProjectVersionValidationSnapshotOptions) (map[string]any, error) {
	versionID := strings.TrimSpace(opts.ProjectVersionID)
	if versionID == "" {
		return nil, fmt.Errorf("project version id is required")
	}
	preview, err := projectVersionValidationPreviewFromDB(ctx, store.DB, versionID)
	if err != nil {
		return nil, err
	}
	recordingTrigger := strings.TrimSpace(opts.RecordingTrigger)
	if recordingTrigger == "" {
		recordingTrigger = "operator_request"
	}
	assetID, assetErr := projectVersionAssetID(ctx, store.DB, versionID)
	snapshot := projectVersionValidationSnapshotPayload(preview, assetErr == nil)
	result := map[string]any{
		"mode":                           "project_version_validation_snapshot_recording",
		"recording_state":                "ready_to_record",
		"recording_ready":                true,
		"recording_enabled":              !opts.DryRun,
		"recording_trigger":              recordingTrigger,
		"auto_record_terminal_required":  opts.RequireRecordedRefresh,
		"auto_record_terminal_satisfied": !opts.RequireRecordedRefresh,
		"dry_run":                        opts.DryRun,
		"project_version_id":             versionID,
		"project_version_asset_observed": assetErr == nil,
		"snapshot":                       snapshot,
		"snapshots_written":              0,
		"snapshots_skipped_as_duplicate": 0,
		"validation_snapshot_written":    false,
		"asset_status_snapshot_written":  false,
		"operation_log_written":          false,
		"external_call_made":             false,
	}
	if opts.RequireRecordedRefresh {
		ready, state, missing := projectVersionValidationSnapshotAutoRecordReadiness(preview, snapshot)
		result["recording_state"] = state
		result["recording_ready"] = ready
		result["recording_enabled"] = ready && !opts.DryRun
		result["auto_record_terminal_satisfied"] = ready
		if len(missing) > 0 {
			result["missing_evidence"] = missing
		}
		if !ready {
			result["message"] = "ProjectVersion validation snapshot auto-recording is waiting for a terminal recorded refresh result; no snapshot was written."
			return result, nil
		}
		result["recording_state"] = "ready_to_record"
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["missing_evidence"] = []string{"project_version_asset_missing"}
		result["message"] = "ProjectVersion validation snapshot is derived, but the canonical project_version asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized ProjectVersion validation snapshot was not written."
		return result, nil
	}
	status := strings.TrimSpace(fmt.Sprint(snapshot["validation_state"]))
	if status == "" || status == "<nil>" {
		status = "unknown"
	}
	health := "warning"
	switch status {
	case "ready":
		health = "low"
	case "blocked":
		health = "high"
	}
	tx, err := store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting project version validation snapshot transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	execResult, err := tx.ExecContext(ctx, `
		INSERT INTO asset_status_snapshots(asset_id, status, health, summary, raw)
		SELECT $1, $2, $3, 'project version validation snapshot recorded', $4
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
		return nil, fmt.Errorf("inserting project version validation snapshot: %w", err)
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
		return nil, fmt.Errorf("committing project version validation snapshot: %w", err)
	}
	committed = true
	result["recording_state"] = "recorded"
	result["snapshots_written"] = written
	if written >= 0 {
		result["snapshots_skipped_as_duplicate"] = 1 - written
		result["validation_snapshot_written"] = written > 0
		result["asset_status_snapshot_written"] = written > 0
	}
	if rowsAffectedWarning != "" {
		result["rows_affected_warning"] = rowsAffectedWarning
		result["rows_affected_unknown"] = true
		result["snapshots_skipped_as_duplicate"] = -1
	}
	result["message"] = "Sanitized ProjectVersion validation snapshot recorded from local synced database state."
	return result, nil
}

func projectVersionValidationSnapshotAutoRecordReadiness(preview map[string]any, snapshot map[string]any) (bool, string, []string) {
	summary := mapFromAny(preview["provider_refresh_result_summary"])
	rerunEvidence := mapFromAny(preview["validation_rerun_evidence"])
	status := strings.TrimSpace(fmt.Sprint(summary["validation_rerun_status"]))
	if status == "" || status == "<nil>" {
		status = "not_requested"
	}
	activeCount := intFromAny(summary["active_count"], 0)
	operationCount := intFromAny(summary["operation_count"], 0)
	state := "blocked"
	if status == "waiting_for_workers" || activeCount > 0 {
		state = "waiting_for_workers"
	}
	missing := []string{}
	if operationCount == 0 {
		missing = append(missing, "provider_refresh_execution_not_performed")
	}
	if activeCount > 0 {
		missing = append(missing, "refresh_workers_still_running")
	}
	if status != "recorded" {
		missing = append(missing, "validation_rerun_not_recorded")
	}
	if !boolOnlyFromAny(rerunEvidence["server_side_validation_recheck_ready"]) {
		missing = append(missing, "server_side_validation_recheck_not_terminal")
	}
	if !boolOnlyFromAny(snapshot["snapshot_ready_for_review"]) {
		missing = append(missing, "validation_snapshot_not_ready_for_review")
	}
	ready := operationCount > 0 &&
		activeCount == 0 &&
		status == "recorded" &&
		boolOnlyFromAny(rerunEvidence["server_side_validation_recheck_ready"]) &&
		boolOnlyFromAny(snapshot["snapshot_ready_for_review"])
	if ready {
		return true, "ready_to_record", nil
	}
	return false, state, missing
}

func projectVersionValidationPreviewFromDB(ctx context.Context, db sqlx.ExtContext, versionID string) (map[string]any, error) {
	projectID, err := projectIDForProjectVersion(ctx, db, versionID)
	if err != nil {
		return nil, err
	}
	version, err := queryOne(ctx, db, `
		SELECT id, project_id, version, source, metadata, created_at
		FROM project_versions
		WHERE id=$1`, versionID)
	if err != nil {
		return nil, err
	}
	remotes, err := queryMaps(ctx, db, `
		SELECT gr.id, gr.remote_key, gr.provider_type, gr.latest_sha, gr.default_branch, r.repo_key, r.repo_role, r.name AS repository_name
		FROM git_remotes gr
		JOIN project_git_repositories r ON r.id=gr.project_git_repository_id
		WHERE r.project_id=$1`, projectID)
	if err != nil {
		return nil, fmt.Errorf("loading version remotes: %w", err)
	}
	tagRuns, err := queryMaps(ctx, db, `
		SELECT id, project_git_repository_id, target_remote_id, git_remote_id, tag_name, target_sha, status, created_at, finished_at
		FROM repo_tag_runs
		WHERE project_id=$1
		ORDER BY created_at DESC
		LIMIT 500`, projectID)
	if err != nil {
		return nil, fmt.Errorf("loading version tag runs: %w", err)
	}
	actionRuns, err := queryMaps(ctx, db, `
		SELECT id, git_remote_id, run_id, workflow_name, branch, commit_sha, status, conclusion, started_at, updated_at
		FROM github_action_runs
		WHERE git_remote_id IN (
			SELECT gr.id
			FROM git_remotes gr
			JOIN project_git_repositories r ON r.id=gr.project_git_repository_id
			WHERE r.project_id=$1
		)
		ORDER BY updated_at DESC
		LIMIT 500`, projectID)
	if err != nil {
		return nil, fmt.Errorf("loading version action runs: %w", err)
	}
	argoApps, err := queryMaps(ctx, db, `
		SELECT id, name, namespace, status, metadata, synced_at, updated_at
		FROM argo_apps
		WHERE project_id=$1
		ORDER BY updated_at DESC
		LIMIT 500`, projectID)
	if err != nil {
		return nil, fmt.Errorf("loading version Argo apps: %w", err)
	}
	argoConnections, err := queryMaps(ctx, db, `
		SELECT id, name, last_sync_status
		FROM argo_connections
		WHERE project_id=$1
		ORDER BY updated_at DESC
		LIMIT 100`, projectID)
	if err != nil {
		return nil, fmt.Errorf("loading version Argo connections: %w", err)
	}
	refreshOperations, err := queryProjectVersionRefreshOperations(ctx, db, versionID)
	if err != nil {
		return nil, fmt.Errorf("loading project version refresh operations: %w", err)
	}
	backgroundOperations, err := queryProjectVersionValidationRerunOperations(ctx, db, versionID)
	if err != nil {
		return nil, fmt.Errorf("loading project version validation rerun operations: %w", err)
	}
	return projectVersionValidationPreview(version, remotes, tagRuns, actionRuns, argoApps, argoConnections, refreshOperations, backgroundOperations), nil
}

func projectVersionAssetID(ctx context.Context, db sqlx.ExtContext, versionID string) (string, error) {
	row, err := queryOne(ctx, db, `
		SELECT id::text AS id
		FROM assets
		WHERE asset_type='project_version'
			AND source_table='project_versions'
			AND source_id=$1::uuid
		LIMIT 1`, versionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("project_version asset for %s not found; run db sync-assets first", versionID)
		}
		return "", err
	}
	assetID := strings.TrimSpace(fmt.Sprint(row["id"]))
	if assetID == "" || assetID == "<nil>" {
		return "", fmt.Errorf("project_version asset for %s has empty id", versionID)
	}
	return assetID, nil
}

func projectVersionValidationSnapshotPayload(preview map[string]any, assetObserved bool) map[string]any {
	summary := mapFromAny(preview["provider_refresh_result_summary"])
	rerunEvidence := mapFromAny(preview["validation_rerun_evidence"])
	backgroundPlan := mapFromAny(preview["background_validation_rerun_plan"])
	snapshotPlan := mapFromAny(backgroundPlan["validation_snapshot_write_plan"])
	return map[string]any{
		"mode":                                 "project_version_validation_snapshot",
		"project_version_id":                   preview["version_id"],
		"validation_state":                     preview["validation_state"],
		"repository_count":                     intFromAny(preview["repository_count"], 0),
		"ready_count":                          intFromAny(preview["ready_count"], 0),
		"partial_count":                        intFromAny(preview["partial_count"], 0),
		"blocked_count":                        intFromAny(preview["blocked_count"], 0),
		"provider_refresh_status":              summary["validation_rerun_status"],
		"operation_count":                      intFromAny(summary["operation_count"], 0),
		"active_count":                         intFromAny(summary["active_count"], 0),
		"terminal_count":                       intFromAny(summary["terminal_count"], 0),
		"server_side_validation_recheck":       boolOnlyFromAny(rerunEvidence["server_side_validation_recheck"]),
		"server_side_validation_recheck_ready": boolOnlyFromAny(rerunEvidence["server_side_validation_recheck_ready"]),
		"validation_rerun_recorded":            boolOnlyFromAny(summary["validation_rerun_recorded"]),
		"snapshot_state":                       snapshotPlan["snapshot_state"],
		"snapshot_ready_for_review":            boolOnlyFromAny(snapshotPlan["snapshot_ready_for_review"]),
		"project_version_asset_observed":       assetObserved,
		"validation_source":                    "local_synced_database_state",
		"external_call_made":                   false,
		"provider_api_called":                  false,
		"git_fetch_performed":                  false,
		"argocd_api_called":                    false,
		"raw_response_included":                false,
		"secret_included":                      false,
		"operation_log_written":                false,
		"background_worker_enqueued":           false,
		"missing_required_evidence":            projectVersionValidationSnapshotMissingEvidence(preview, summary, rerunEvidence, assetObserved),
	}
}

func projectVersionValidationSnapshotMissingEvidence(preview, summary, rerunEvidence map[string]any, assetObserved bool) []string {
	missing := []string{}
	if !assetObserved {
		missing = append(missing, "project_version_asset_missing")
	}
	if intFromAny(preview["repository_count"], 0) == 0 {
		missing = append(missing, "project_version_repository_manifest_missing")
	}
	if !boolOnlyFromAny(rerunEvidence["server_side_validation_recheck_ready"]) {
		missing = append(missing, "server_side_validation_recheck_not_terminal")
	}
	if !boolOnlyFromAny(summary["validation_rerun_recorded"]) {
		missing = append(missing, "validation_rerun_not_recorded")
	}
	return missing
}

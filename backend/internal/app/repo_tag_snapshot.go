package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type RepoTagRunResultSnapshotOptions struct {
	RepoTagRunID string
	DryRun       bool
}

func RecordRepoTagRunResultSnapshot(ctx context.Context, store *Store, opts RepoTagRunResultSnapshotOptions) (map[string]any, error) {
	runID := strings.TrimSpace(opts.RepoTagRunID)
	if runID == "" {
		return nil, fmt.Errorf("repo tag run id is required")
	}
	if _, err := uuid.Parse(runID); err != nil {
		return nil, fmt.Errorf("repo tag run id must be a uuid")
	}
	run, err := repoTagRunForSnapshot(ctx, store.DB, runID)
	if err != nil {
		return nil, err
	}
	assetID, assetErr := repoTagRunAssetID(ctx, store.DB, runID)
	if assetErr != nil && !opts.DryRun {
		return nil, assetErr
	}
	plan := repoTagRemoteRehearsalPlan(run)
	snapshot := repoTagRunResultSnapshotPayload(run, plan, assetErr == nil)
	result := map[string]any{
		"mode":                             "repo_tag_run_result_snapshot_recording",
		"recording_state":                  "ready_to_record",
		"recording_ready":                  true,
		"recording_enabled":                !opts.DryRun,
		"dry_run":                          opts.DryRun,
		"repo_tag_run_id":                  runID,
		"repo_tag_run_asset_observed":      assetErr == nil,
		"snapshot":                         snapshot,
		"snapshots_written":                0,
		"snapshots_skipped_as_duplicate":   0,
		"tag_result_snapshot_written":      false,
		"asset_status_snapshot_written":    false,
		"operation_log_written":            false,
		"external_call_made":               false,
		"remote_tag_lookup_performed":      false,
		"github_actions_refresh_performed": false,
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["missing_evidence"] = []string{"repo_tag_run_asset_missing"}
		result["message"] = "Repo tag result snapshot is derived, but the canonical repo_tag_run asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized repo tag result snapshot was not written."
		return result, nil
	}
	status := cleanPreviewString(snapshot["result_recording_state"])
	if status == "" {
		status = "unknown"
	}
	health := "warning"
	switch status {
	case "recorded":
		health = "low"
	case "failed":
		health = "high"
	}
	tx, err := store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting repo tag result snapshot transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	execResult, err := tx.ExecContext(ctx, `
		INSERT INTO asset_status_snapshots(asset_id, status, health, summary, raw)
		SELECT $1, $2, $3, 'repo tag result snapshot recorded', $4
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
		return nil, fmt.Errorf("inserting repo tag result snapshot: %w", err)
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
		return nil, fmt.Errorf("committing repo tag result snapshot: %w", err)
	}
	committed = true
	result["recording_state"] = "recorded"
	result["snapshots_written"] = written
	if written >= 0 {
		result["snapshots_skipped_as_duplicate"] = 1 - written
		result["tag_result_snapshot_written"] = written > 0
		result["asset_status_snapshot_written"] = written > 0
	}
	if rowsAffectedWarning != "" {
		result["rows_affected_warning"] = rowsAffectedWarning
		result["rows_affected_unknown"] = true
		result["snapshots_skipped_as_duplicate"] = -1
	}
	result["message"] = "Sanitized repo tag result snapshot recorded from local repo_tag_run state."
	return result, nil
}

func repoTagRunForSnapshot(ctx context.Context, db sqlx.ExtContext, runID string) (map[string]any, error) {
	return queryOne(ctx, db, `
		SELECT rtr.id,
			rtr.operation_run_id,
			COALESCE(rtr.project_id, pgr.project_id) AS project_id,
			rtr.project_git_repository_id,
			rtr.target_remote_id,
			rtr.git_remote_id,
			rtr.tag_name,
			rtr.target_sha,
			rtr.status,
			rtr.error_message,
			rtr.started_at,
			rtr.finished_at,
			rtr.created_at,
			gr.provider_type,
			gr.remote_role,
			pgr.repo_key,
			pgr.repo_role
		FROM repo_tag_runs rtr
		LEFT JOIN project_git_repositories pgr ON pgr.id=rtr.project_git_repository_id
		LEFT JOIN git_remotes gr ON gr.id=COALESCE(rtr.target_remote_id, rtr.git_remote_id)
		WHERE rtr.id=$1`, runID)
}

func repoTagRunProjectID(ctx context.Context, db sqlx.ExtContext, runID string) (string, error) {
	run, err := repoTagRunForSnapshot(ctx, db, runID)
	if err != nil {
		return "", err
	}
	projectID := cleanOptionalID(fmt.Sprint(run["project_id"]))
	if projectID == "" {
		return "", fmt.Errorf("repo tag run %s has no project binding", runID)
	}
	return projectID, nil
}

func repoTagRunAssetID(ctx context.Context, db sqlx.ExtContext, runID string) (string, error) {
	row, err := queryOne(ctx, db, `
		SELECT id::text AS id
		FROM assets
		WHERE asset_type='repo_tag_run'
			AND source_table='repo_tag_runs'
			AND source_id=$1::uuid
		LIMIT 1`, runID)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("repo_tag_run asset for %s not found; run db sync-assets first", runID)
		}
		return "", err
	}
	assetID := strings.TrimSpace(fmt.Sprint(row["id"]))
	if assetID == "" || assetID == "<nil>" {
		return "", fmt.Errorf("repo_tag_run asset for %s has empty id", runID)
	}
	return assetID, nil
}

func repoTagRunResultSnapshotPayload(run, plan map[string]any, assetObserved bool) map[string]any {
	resultPlan := mapFromAny(plan["result_recording_plan"])
	resultEvidence := mapFromAny(resultPlan["tag_result_evidence"])
	lookupPreflight := mapFromAny(plan["live_remote_lookup_preflight"])
	liveResultPlan := mapFromAny(plan["live_result_plan"])
	actionsPlan := mapFromAny(plan["actions_refresh_plan"])
	return map[string]any{
		"mode":                             "repo_tag_run_result_snapshot",
		"repo_tag_run_id":                  cleanOptionalID(fmt.Sprint(run["id"])),
		"project_id":                       cleanOptionalID(fmt.Sprint(run["project_id"])),
		"project_git_repository_id":        cleanOptionalID(fmt.Sprint(run["project_git_repository_id"])),
		"target_remote_bound":              boolOnlyFromAny(plan["target_remote_bound"]),
		"tag_name_configured":              boolOnlyFromAny(plan["tag_name_configured"]),
		"target_sha_configured":            boolOnlyFromAny(plan["target_sha_configured"]),
		"tag_run_status":                   cleanPreviewString(plan["tag_run_status"]),
		"rehearsal_state":                  cleanPreviewString(plan["rehearsal_state"]),
		"result_recording_state":           cleanPreviewString(resultPlan["result_recording_state"]),
		"result_recording_ready":           boolOnlyFromAny(resultPlan["result_recording_ready"]),
		"result_recording_ready_reason":    cleanPreviewString(resultPlan["result_recording_ready_reason"]),
		"tag_result_evidence_state":        cleanPreviewString(resultEvidence["evidence_state"]),
		"live_remote_tag_success_observed": boolOnlyFromAny(plan["live_remote_tag_success_observed"]),
		"live_remote_tag_failed_observed":  boolOnlyFromAny(plan["live_remote_tag_failed_observed"]),
		"operation_run_bound":              boolOnlyFromAny(resultEvidence["operation_run_bound"]),
		"repo_tag_run_asset_observed":      assetObserved,
		"live_remote_lookup_state":         cleanPreviewString(lookupPreflight["lookup_state"]),
		"live_result_state":                cleanPreviewString(liveResultPlan["live_result_state"]),
		"github_actions_refresh_state":     cleanPreviewString(actionsPlan["refresh_state"]),
		"remote_tag_lookup_performed":      false,
		"git_ls_remote_performed":          false,
		"provider_api_called":              false,
		"github_actions_refresh_performed": false,
		"repo_tag_run_updated":             false,
		"operation_log_written":            false,
		"external_call_made":               false,
		"raw_git_output_recorded":          false,
		"raw_provider_response_recorded":   false,
		"tag_name_included":                false,
		"target_sha_included":              false,
		"remote_url_included":              false,
		"secret_included":                  false,
		"contains_token":                   false,
		"contains_remote_url":              false,
		"contains_ref_name":                false,
		"contains_tag_message":             false,
		"suppressed_fields":                []string{"tag_name", "target_sha", "tag_message", "remote_url", "git_credentials", "provider_token", "authorization_header", "git_output", "github_actions_response", "provider_response_body", "provider_response_headers"},
	}
}

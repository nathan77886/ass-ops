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

type RepoTagRunActionsRefreshSnapshotOptions struct {
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
		result["tag_result_snapshot_written"] = false
		result["asset_status_snapshot_written"] = false
	}
	result["message"] = "Sanitized repo tag result snapshot recorded from local repo_tag_run state."
	return result, nil
}

func RecordRepoTagRunActionsRefreshSnapshot(ctx context.Context, store *Store, opts RepoTagRunActionsRefreshSnapshotOptions) (map[string]any, error) {
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
	if assetErr != nil && !strings.Contains(assetErr.Error(), "not found") {
		return nil, assetErr
	}
	evidence, err := repoTagRunActionsRefreshEvidence(ctx, store.DB, run)
	if err != nil {
		return nil, err
	}
	snapshot := repoTagRunActionsRefreshSnapshotPayload(run, evidence, assetErr == nil)
	ready := boolOnlyFromAny(snapshot["actions_refresh_recording_ready"])
	state := cleanPreviewString(snapshot["actions_refresh_recording_state"])
	if state == "" {
		state = "blocked"
	}
	result := map[string]any{
		"mode":                                  "repo_tag_run_actions_refresh_snapshot_recording",
		"recording_state":                       state,
		"recording_ready":                       ready,
		"recording_enabled":                     ready && !opts.DryRun,
		"dry_run":                               opts.DryRun,
		"repo_tag_run_id":                       runID,
		"repo_tag_run_asset_observed":           assetErr == nil,
		"snapshot":                              snapshot,
		"snapshots_written":                     0,
		"snapshots_skipped_as_duplicate":        0,
		"actions_refresh_snapshot_written":      false,
		"asset_status_snapshot_written":         false,
		"operation_log_written":                 false,
		"external_call_made":                    false,
		"provider_api_called":                   false,
		"github_actions_refresh_performed":      false,
		"github_actions_refresh_evidence_found": boolOnlyFromAny(snapshot["github_actions_refresh_evidence_found"]),
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"repo_tag_run_asset_missing"}
		result["message"] = "Repo tag Actions refresh snapshot is derived, but the canonical repo_tag_run asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["missing_evidence"] = snapshot["missing_evidence"]
		result["message"] = "Repo tag Actions refresh snapshot is waiting for a completed tag run and locally synced GitHub Actions evidence; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["recording_state"] = "ready_to_record"
		result["message"] = "Dry run only; sanitized repo tag Actions refresh snapshot was not written."
		return result, nil
	}
	tx, err := store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting repo tag Actions refresh snapshot transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	execResult, err := tx.ExecContext(ctx, `
		INSERT INTO asset_status_snapshots(asset_id, status, health, summary, raw)
		SELECT $1, $2, $3, 'repo tag GitHub Actions refresh snapshot recorded', $4
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
		assetID, "github_actions_refresh_recorded", "low", JSONValue{Data: snapshot})
	if err != nil {
		return nil, fmt.Errorf("inserting repo tag Actions refresh snapshot: %w", err)
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
		return nil, fmt.Errorf("committing repo tag Actions refresh snapshot: %w", err)
	}
	committed = true
	result["recording_state"] = "recorded"
	result["snapshots_written"] = written
	if written >= 0 {
		result["snapshots_skipped_as_duplicate"] = 1 - written
		result["actions_refresh_snapshot_written"] = written > 0
		result["asset_status_snapshot_written"] = written > 0
	}
	if rowsAffectedWarning != "" {
		result["rows_affected_warning"] = rowsAffectedWarning
		result["rows_affected_unknown"] = true
		result["snapshots_skipped_as_duplicate"] = -1
		result["actions_refresh_snapshot_written"] = false
		result["asset_status_snapshot_written"] = false
	}
	result["message"] = "Sanitized repo tag GitHub Actions refresh snapshot recorded from local github_action_runs state."
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

func repoTagRunActionsRefreshEvidence(ctx context.Context, db sqlx.ExtContext, run map[string]any) (map[string]any, error) {
	remoteID := cleanOptionalID(firstNonEmptyString(fmt.Sprint(run["target_remote_id"]), fmt.Sprint(run["git_remote_id"])))
	targetSHA := cleanPreviewString(run["target_sha"])
	evidence := map[string]any{
		"mode":                                  "repo_tag_github_actions_refresh_evidence",
		"target_remote_bound":                   remoteID != "",
		"target_sha_configured":                 targetSHA != "",
		"evidence_scope":                        "commit",
		"github_actions_refresh_evidence_found": false,
		"github_actions_total":                  0,
		"github_actions_success":                0,
		"github_actions_failure":                0,
		"github_actions_active":                 0,
		"github_actions_synced":                 0,
		"github_action_run_link_count":          0,
		"latest_synced_at":                      nil,
		"latest_updated_at":                     nil,
		"provider_api_called":                   false,
		"external_call_made":                    false,
		"contains_provider_response":            false,
		"contains_remote_url":                   false,
		"contains_ref_name":                     false,
		"contains_commit_sha":                   false,
		"contains_workflow_name":                false,
		"contains_html_url":                     false,
	}
	if remoteID == "" {
		return evidence, nil
	}
	if targetSHA == "" {
		evidence["evidence_scope"] = "remote_all_commits"
	}
	row, err := queryOne(ctx, db, `
		SELECT COUNT(*)::int AS total,
			COUNT(*) FILTER (WHERE lower(conclusion)='success')::int AS success_count,
			COUNT(*) FILTER (WHERE COALESCE(NULLIF(conclusion, ''), status) IN ('failure', 'failed', 'error', 'timed_out', 'cancelled', 'canceled'))::int AS failure_count,
			COUNT(*) FILTER (WHERE status IN ('queued', 'running', 'pending', 'in_progress'))::int AS active_count,
			COUNT(*) FILTER (WHERE synced_at IS NOT NULL)::int AS synced_count,
			MAX(synced_at) AS latest_synced_at,
			MAX(updated_at) AS latest_updated_at
		FROM github_action_runs
		WHERE git_remote_id=$1
			AND ($2 = '' OR commit_sha=$2)`, remoteID, targetSHA)
	if err != nil {
		return nil, err
	}
	total := intFromAny(row["total"], 0)
	status := strings.ToLower(cleanPreviewString(run["status"]))
	tagObserved := status == "completed" || status == "succeeded" || status == "success"
	linkCount := 0
	if tagObserved && targetSHA != "" {
		linkCount = total
	}
	evidence["github_actions_total"] = total
	evidence["github_actions_success"] = intFromAny(row["success_count"], 0)
	evidence["github_actions_failure"] = intFromAny(row["failure_count"], 0)
	evidence["github_actions_active"] = intFromAny(row["active_count"], 0)
	evidence["github_actions_synced"] = intFromAny(row["synced_count"], 0)
	evidence["github_action_run_link_count"] = linkCount
	evidence["latest_synced_at"] = row["latest_synced_at"]
	evidence["latest_updated_at"] = row["latest_updated_at"]
	evidence["github_actions_refresh_evidence_found"] = total > 0 && intFromAny(row["synced_count"], 0) > 0
	return evidence, nil
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

func repoTagRunActionsRefreshSnapshotPayload(run, evidence map[string]any, assetObserved bool) map[string]any {
	status := strings.ToLower(cleanPreviewString(run["status"]))
	tagObserved := status == "completed" || status == "succeeded" || status == "success"
	tagFailed := status == "failed" || status == "error" || status == "canceled" || status == "cancelled"
	tagWaiting := status == "" || status == "queued" || status == "running" || status == "pending" || status == "in_progress"
	evidenceFound := boolOnlyFromAny(evidence["github_actions_refresh_evidence_found"])
	ready := tagObserved && evidenceFound && assetObserved
	state := "blocked"
	if ready {
		state = "recorded"
	} else if tagFailed {
		state = "failed"
	} else if tagWaiting {
		state = "waiting_for_tag_completion"
	} else if tagObserved {
		state = "waiting_for_actions_refresh"
	}
	missing := []string{}
	if !assetObserved {
		missing = append(missing, "repo_tag_run_asset_missing")
	}
	if !tagObserved {
		missing = append(missing, "live_remote_tag_success_not_observed")
	}
	if !boolOnlyFromAny(evidence["target_remote_bound"]) {
		missing = append(missing, "target_remote_missing")
	}
	if !evidenceFound {
		missing = append(missing, "github_actions_refresh_evidence_missing")
	}
	return map[string]any{
		"mode":                                  "repo_tag_run_actions_refresh_snapshot",
		"repo_tag_run_id":                       cleanOptionalID(fmt.Sprint(run["id"])),
		"project_id":                            cleanOptionalID(fmt.Sprint(run["project_id"])),
		"project_git_repository_id":             cleanOptionalID(fmt.Sprint(run["project_git_repository_id"])),
		"repo_tag_run_asset_observed":           assetObserved,
		"tag_run_status":                        status,
		"live_remote_tag_success_observed":      tagObserved,
		"live_remote_tag_failed_observed":       tagFailed,
		"actions_refresh_recording_state":       state,
		"actions_refresh_recording_ready":       ready,
		"target_remote_bound":                   boolOnlyFromAny(evidence["target_remote_bound"]),
		"target_sha_configured":                 boolOnlyFromAny(evidence["target_sha_configured"]),
		"evidence_scope":                        cleanPreviewString(evidence["evidence_scope"]),
		"github_actions_refresh_evidence_found": evidenceFound,
		"github_actions_total":                  intFromAny(evidence["github_actions_total"], 0),
		"github_actions_success":                intFromAny(evidence["github_actions_success"], 0),
		"github_actions_failure":                intFromAny(evidence["github_actions_failure"], 0),
		"github_actions_active":                 intFromAny(evidence["github_actions_active"], 0),
		"github_actions_synced":                 intFromAny(evidence["github_actions_synced"], 0),
		"github_action_run_link_count":          intFromAny(evidence["github_action_run_link_count"], 0),
		"latest_synced_at":                      evidence["latest_synced_at"],
		"latest_updated_at":                     evidence["latest_updated_at"],
		"missing_evidence":                      missing,
		"github_actions_refresh_performed":      false,
		"provider_api_called":                   false,
		"external_call_made":                    false,
		"operation_log_written":                 false,
		"repo_tag_run_link_written":             tagObserved && evidenceFound && intFromAny(evidence["github_action_run_link_count"], 0) > 0,
		"repo_tag_run_link_source":              "canonical_asset_relation",
		"raw_provider_response_recorded":        false,
		"contains_token":                        false,
		"contains_remote_url":                   false,
		"contains_ref_name":                     false,
		"contains_commit_sha":                   false,
		"contains_workflow_name":                false,
		"contains_html_url":                     false,
		"tag_name_included":                     false,
		"target_sha_included":                   false,
		"sanitized_metadata_only":               true,
		"suppressed_fields":                     []string{"tag_name", "target_sha", "branch", "commit_sha", "workflow_name", "html_url", "remote_url", "provider_token", "authorization_header", "github_actions_response", "provider_response_body", "provider_response_headers"},
	}
}

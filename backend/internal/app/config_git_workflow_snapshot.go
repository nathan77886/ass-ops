package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jmoiron/sqlx"
)

type ConfigRepositoryGitWorkflowPromotionSnapshotOptions struct {
	RepositoryID string
	DryRun       bool
	Repository   map[string]any
	Preview      map[string]any
}

func (s *Server) recordConfigRepositoryGitWorkflowPromotionSnapshot(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	projectID, err := projectIDForRepository(r.Context(), s.store.DB, repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	resource := PolicyResource{Type: "git_repository", ID: repoID, ProjectID: projectID}
	if !s.requireProjectMembershipForPolicy(w, r, resource) {
		return
	}
	decision := NewPolicyChecker().Check(currentUser(r), resource, "config.git_commit")
	if decision.Effect != PolicyAllow {
		writeJSON(w, http.StatusForbidden, decision)
		return
	}
	repo, _, _, preview, err := s.configRepositoryScaffoldPreviewForRequest(r.Context(), repoID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load config scaffold preview")
		return
	}
	result, err := RecordConfigRepositoryGitWorkflowPromotionSnapshot(r.Context(), s.store, ConfigRepositoryGitWorkflowPromotionSnapshotOptions{
		RepositoryID: repoID,
		DryRun:       req.DryRun,
		Repository:   repo,
		Preview:      preview,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("config git workflow promotion snapshot failed", "repository_id", repoID, "project_id", projectID, "error", err)
		}
		writeError(w, http.StatusBadRequest, "record config git workflow promotion snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func RecordConfigRepositoryGitWorkflowPromotionSnapshot(ctx context.Context, store *Store, opts ConfigRepositoryGitWorkflowPromotionSnapshotOptions) (map[string]any, error) {
	repoID := strings.TrimSpace(opts.RepositoryID)
	if repoID == "" {
		return nil, fmt.Errorf("repository id is required")
	}
	repo := opts.Repository
	if len(repo) == 0 {
		var err error
		repo, err = queryOne(ctx, store.DB, "SELECT * FROM project_git_repositories WHERE id=$1", repoID)
		if err != nil {
			return nil, err
		}
	}
	projectID := strings.TrimSpace(fmt.Sprint(repo["project_id"]))
	if projectID == "" || projectID == "<nil>" {
		return nil, fmt.Errorf("config repository has no project")
	}
	preview := opts.Preview
	if len(preview) == 0 {
		remotes, err := queryConfigRepositorySnapshotRemotes(ctx, store.DB, repoID)
		if err != nil {
			return nil, fmt.Errorf("loading config remotes: %w", err)
		}
		versions, err := queryConfigRepositorySnapshotVersions(ctx, store.DB, projectID)
		if err != nil {
			return nil, fmt.Errorf("loading project versions: %w", err)
		}
		workflowOperations, err := queryConfigRepositoryGitWorkflowOperations(ctx, store.DB, projectID, repoID)
		if err != nil {
			return nil, fmt.Errorf("loading config git workflow operations: %w", err)
		}
		preview = configRepositoryScaffoldPreview(repo, remotes, versions, workflowOperations)
	}
	assetID, assetErr := gitRepositoryAssetID(ctx, store.DB, repoID)
	if assetErr != nil && !errors.Is(assetErr, ErrNotFound) {
		return nil, assetErr
	}
	snapshot := configRepositoryGitWorkflowPromotionSnapshotPayload(repo, preview, assetErr == nil)
	ready, state, missing := configRepositoryGitWorkflowPromotionSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                  "config_git_workflow_promotion_snapshot_recording",
		"recording_state":                       state,
		"recording_ready":                       ready,
		"recording_enabled":                     ready && !opts.DryRun,
		"dry_run":                               opts.DryRun,
		"project_id":                            projectID,
		"project_git_repository_id":             repoID,
		"git_repository_asset_observed":         assetErr == nil,
		"snapshot":                              snapshot,
		"snapshots_written":                     0,
		"snapshots_skipped_as_duplicate":        0,
		"promotion_snapshot_written":            false,
		"asset_status_snapshot_written":         false,
		"operation_log_written":                 false,
		"external_call_made":                    false,
		"git_workspace_mutation_enabled":        false,
		"git_commit_created":                    false,
		"git_push_performed":                    false,
		"provider_review_created":               false,
		"project_version_pin_written":           false,
		"live_remote_validation_performed":      false,
		"raw_git_output_recorded":               false,
		"raw_provider_response_recorded":        false,
		"file_content_included":                 false,
		"secret_included":                       false,
		"canonical_asset_status_snapshot_try":   false,
		"snapshot_commit_attempted":             false,
		"audit_operation_observed":              boolOnlyFromAny(snapshot["audit_operation_observed"]),
		"sanitized_audit_result_recorded":       boolOnlyFromAny(snapshot["sanitized_audit_result_recorded"]),
		"promotion_ready_for_operator_review":   boolOnlyFromAny(snapshot["promotion_ready_for_operator_review"]),
		"future_live_workflow_remains_disabled": true,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"git_repository_asset_missing"}
		result["message"] = "Config Git workflow promotion snapshot is derived, but the canonical git_repository asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Config Git workflow promotion snapshot is waiting for sanitized audit evidence ready for operator review; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized config Git workflow promotion snapshot was not written."
		return result, nil
	}
	status, health := configRepositoryGitWorkflowPromotionSnapshotStatusHealth(state)
	tx, err := store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting config Git workflow promotion snapshot transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text), hashtext($2::text))`, assetID, status); err != nil {
		return nil, fmt.Errorf("locking config Git workflow promotion snapshot asset: %w", err)
	}
	execResult, err := tx.ExecContext(ctx, `
		INSERT INTO asset_status_snapshots(asset_id, status, health, summary, raw)
		SELECT $1, $2, $3, 'config Git workflow promotion review snapshot recorded', $4
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
		return nil, fmt.Errorf("inserting config Git workflow promotion snapshot: %w", err)
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
		return nil, fmt.Errorf("committing config Git workflow promotion snapshot: %w", err)
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
		result["promotion_snapshot_written"] = written > 0
		result["asset_status_snapshot_written"] = written > 0
	}
	if rowsAffectedWarning != "" {
		result["rows_affected_warning"] = rowsAffectedWarning
		result["rows_affected_unknown"] = true
		result["snapshots_skipped_as_duplicate"] = 0
		result["promotion_snapshot_written"] = true
		result["asset_status_snapshot_written"] = true
	}
	result["message"] = "Sanitized config Git workflow promotion snapshot recorded from local audit evidence."
	return result, nil
}

func queryConfigRepositorySnapshotRemotes(ctx context.Context, db sqlx.ExtContext, repoID string) ([]map[string]any, error) {
	return queryMaps(ctx, db, `
		SELECT id, name, remote_key, provider_type, remote_role, default_branch, latest_sha, last_sync_status
		FROM git_remotes
		WHERE project_git_repository_id=$1
		ORDER BY created_at DESC`, repoID)
}

func queryConfigRepositorySnapshotVersions(ctx context.Context, db sqlx.ExtContext, projectID string) ([]map[string]any, error) {
	return queryMaps(ctx, db, `
		SELECT id, version, metadata, created_at
		FROM project_versions
		WHERE project_id=$1
		ORDER BY created_at DESC
		LIMIT 100`, projectID)
}

func gitRepositoryAssetID(ctx context.Context, db sqlx.ExtContext, repoID string) (string, error) {
	var assetID string
	if err := sqlx.GetContext(ctx, db, &assetID, `
		SELECT id
		FROM assets
		WHERE asset_type='git_repository'
			AND source_table='project_git_repositories'
			AND source_id=$1
		ORDER BY updated_at DESC, id DESC
		LIMIT 1`, repoID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("loading git repository asset: %w", err)
	}
	return assetID, nil
}

func configRepositoryGitWorkflowPromotionSnapshotPayload(repo map[string]any, preview map[string]any, assetObserved bool) map[string]any {
	commitPlan := mapFromAny(preview["git_commit_plan"])
	promotionPlan := mapFromAny(commitPlan["promotion_readiness_plan"])
	workflowEvidence := mapFromAny(preview["git_workflow_audit_evidence"])
	resultPlan := mapFromAny(commitPlan["result_recording_plan"])
	return map[string]any{
		"mode":                                  "config_git_workflow_promotion_snapshot",
		"project_git_repository_id":             cleanOptionalID(fmt.Sprint(repo["id"])),
		"repo_key":                              cleanPreviewString(repo["repo_key"]),
		"repo_role":                             cleanPreviewString(repo["repo_role"]),
		"scaffold_state":                        cleanPreviewString(preview["scaffold_state"]),
		"file_count":                            intFromAny(preview["file_count"], 0),
		"remote_count":                          intFromAny(preview["remote_count"], 0),
		"git_repository_asset_observed":         assetObserved,
		"status_snapshot_written":               assetObserved,
		"audit_operation_observed":              boolOnlyFromAny(promotionPlan["audit_operation_observed"]),
		"sanitized_audit_result_recorded":       boolOnlyFromAny(promotionPlan["sanitized_audit_result_recorded"]),
		"promotion_state":                       cleanPreviewString(promotionPlan["promotion_state"]),
		"promotion_ready_for_operator_review":   boolOnlyFromAny(promotionPlan["promotion_ready"]),
		"promotion_ready_reason":                cleanPreviewString(promotionPlan["promotion_ready_reason"]),
		"result_recording_state":                cleanPreviewString(resultPlan["result_recording_state"]),
		"result_recording_ready":                boolOnlyFromAny(resultPlan["result_recording_ready"]),
		"workflow_evidence_state":               cleanPreviewString(workflowEvidence["evidence_state"]),
		"workflow_operation_count":              intFromAny(workflowEvidence["operation_count"], 0),
		"workflow_operation_log_count":          intFromAny(workflowEvidence["operation_log_count"], 0),
		"workflow_active_count":                 intFromAny(workflowEvidence["active_count"], 0),
		"workflow_failed_count":                 intFromAny(workflowEvidence["failed_count"], 0),
		"workflow_canceled_count":               intFromAny(workflowEvidence["canceled_count"], 0),
		"project_version_pin_observed":          boolOnlyFromAny(promotionPlan["project_version_pin_observed"]),
		"live_commit_validation_observed":       boolOnlyFromAny(promotionPlan["live_commit_validation_observed"]),
		"live_git_workflow_enabled":             false,
		"live_git_commit_enabled":               false,
		"git_workspace_mutation_enabled":        false,
		"git_commit_created":                    false,
		"git_push_performed":                    false,
		"provider_review_created":               false,
		"project_version_pin_written":           false,
		"live_remote_validation_performed":      false,
		"external_call_made":                    false,
		"file_content_included":                 false,
		"secret_included":                       false,
		"contains_file_content":                 false,
		"contains_remote_url":                   false,
		"contains_credentials":                  false,
		"contains_commit_sha":                   false,
		"contains_branch_name":                  false,
		"contains_git_output":                   false,
		"contains_provider_response":            false,
		"raw_git_output_recorded":               false,
		"raw_provider_response_recorded":        false,
		"operation_log_written":                 false,
		"future_live_workflow_remains_disabled": true,
		"required_controls":                     promotionPlan["required_controls"],
		"disabled_backends":                     promotionPlan["disabled_backends"],
		"promotion_blockers":                    promotionPlan["promotion_blockers"],
		"suppressed_fields":                     promotionPlan["suppressed_fields"],
	}
}

func configRepositoryGitWorkflowPromotionSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := cleanPreviewString(snapshot["promotion_state"])
	if state == "" {
		state = "blocked"
	}
	if !boolOnlyFromAny(snapshot["git_repository_asset_observed"]) {
		missing = append(missing, "git_repository_asset_missing")
	}
	if !boolOnlyFromAny(snapshot["audit_operation_observed"]) {
		missing = append(missing, "config_git_workflow_audit_operation_missing")
	}
	if !boolOnlyFromAny(snapshot["sanitized_audit_result_recorded"]) {
		missing = append(missing, "sanitized_config_git_workflow_audit_result_not_recorded")
	}
	if !boolOnlyFromAny(snapshot["promotion_ready_for_operator_review"]) {
		missing = append(missing, "config_git_workflow_promotion_not_ready")
	}
	if len(missing) > 0 {
		return false, state, missing
	}
	return true, "promotion_review_ready", nil
}

func configRepositoryGitWorkflowPromotionSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "promotion_review_ready":
		return "config_git_workflow_promotion_review_ready", "low"
	case "failed", "mixed_failed", "canceled", "unknown":
		return "config_git_workflow_promotion_" + state, "high"
	default:
		return "config_git_workflow_promotion_" + state, "warning"
	}
}

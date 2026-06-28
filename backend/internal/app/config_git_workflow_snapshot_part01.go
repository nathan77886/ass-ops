package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strings"
)

type ConfigRepositoryGitWorkflowPromotionSnapshotOptions struct {
	RepositoryID string
	DryRun       bool
	Repository   map[string]any
	Preview      map[string]any
}

type ConfigRepositoryRefRefreshSnapshotOptions struct {
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
	projectID, err := s.projectIDForRepositoryGorm(r.Context(), repoID)
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

func (s *Server) recordConfigRepositoryRefRefreshSnapshot(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	projectID, err := s.projectIDForRepositoryGorm(r.Context(), repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	resource := PolicyResource{Type: "git_repository", ID: repoID, ProjectID: projectID}
	if !s.requireProjectPolicy(w, r, resource, "git.refs.refresh") {
		return
	}
	repo, _, _, preview, err := s.configRepositoryScaffoldPreviewForRequest(r.Context(), repoID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load config scaffold preview")
		return
	}
	result, err := RecordConfigRepositoryRefRefreshSnapshot(r.Context(), s.store, ConfigRepositoryRefRefreshSnapshotOptions{
		RepositoryID: repoID,
		DryRun:       req.DryRun,
		Repository:   repo,
		Preview:      preview,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("config ref refresh snapshot failed", "repository_id", repoID, "project_id", projectID, "error", err)
		}
		writeError(w, http.StatusBadRequest, "record config ref refresh snapshot failed")
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
		repo, err = configRepositorySnapshotRepo(ctx, store.Gorm, repoID)
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
		remotes, err := queryConfigRepositorySnapshotRemotes(ctx, store.Gorm, repoID)
		if err != nil {
			return nil, fmt.Errorf("loading config remotes: %w", err)
		}
		versions, err := queryConfigRepositorySnapshotVersions(ctx, store.Gorm, projectID)
		if err != nil {
			return nil, fmt.Errorf("loading project versions: %w", err)
		}
		workflowOperations, err := queryConfigRepositoryGitWorkflowOperationsGorm(ctx, store.Gorm, projectID, repoID)
		if err != nil {
			return nil, fmt.Errorf("loading config git workflow operations: %w", err)
		}
		preview = configRepositoryScaffoldPreview(repo, remotes, versions, workflowOperations)
	}
	assetID, assetErr := gitRepositoryAssetID(ctx, store.Gorm, repoID)
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
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "config Git workflow promotion review snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording config Git workflow promotion snapshot: %w", err)
	}
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
	result["message"] = "Sanitized config Git workflow promotion snapshot recorded from local audit evidence."
	return result, nil
}

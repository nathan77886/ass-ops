package app

import (
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"net/http"
	"strings"
)

func configRepositoryGitCommitPromotionReadinessPlan(evidence map[string]any, workflowEvidence map[string]any) map[string]any {
	pinObserved := boolOnlyFromAny(evidence["config_commit_sha_recorded"])
	liveObserved := boolOnlyFromAny(evidence["live_validation_recorded"])
	workflowObserved := boolOnlyFromAny(workflowEvidence["has_audit_operations"])
	workflowRecorded := boolOnlyFromAny(workflowEvidence["sanitized_result_recorded"])
	workflowState := strings.TrimSpace(fmt.Sprint(workflowEvidence["evidence_state"]))
	promotionState := "blocked"
	promotionReason := "config_git_commit_audit_result_not_recorded"
	switch {
	case workflowState == "waiting_for_worker":
		promotionState = "waiting_for_worker"
		promotionReason = "config_git_commit_audit_operation_waiting_for_worker"
	case workflowState == "failed" || workflowState == "mixed_failed":
		promotionState = "failed"
		promotionReason = "config_git_commit_audit_operation_failed"
	case workflowState == "canceled":
		promotionState = "canceled"
		promotionReason = "config_git_commit_audit_operation_canceled"
	case workflowState == "unknown":
		promotionState = "unknown"
		promotionReason = "config_git_commit_audit_operation_unknown"
	case workflowState == "recorded" && workflowObserved && !workflowRecorded:
		promotionState = "blocked"
		promotionReason = "config_git_commit_audit_operation_log_missing"
	case workflowRecorded && pinObserved && liveObserved:
		promotionState = "ready_for_live_workflow_review"
		promotionReason = "audit_result_pin_and_live_validation_ready_for_operator_review"
	case workflowRecorded:
		promotionState = "audit_result_ready_for_review"
		promotionReason = "sanitized_audit_result_ready_for_operator_review"
	case pinObserved || liveObserved:
		promotionState = "partial_evidence"
		promotionReason = "project_version_pin_or_live_validation_observed_without_audit_result"
	}
	promotionReady := promotionState == "ready_for_live_workflow_review" || promotionState == "audit_result_ready_for_review"
	return map[string]any{
		"mode":                             "config_repository_audit_to_live_promotion_readiness_plan",
		"promotion_state":                  promotionState,
		"promotion_ready":                  promotionReady,
		"promotion_ready_reason":           promotionReason,
		"audit_operation_observed":         workflowObserved,
		"sanitized_audit_result_recorded":  workflowRecorded,
		"project_version_pin_observed":     pinObserved,
		"live_commit_validation_observed":  liveObserved,
		"live_git_workflow_enabled":        false,
		"live_git_commit_enabled":          false,
		"git_workspace_mutation_enabled":   false,
		"git_commit_created":               false,
		"git_push_performed":               false,
		"provider_review_created":          false,
		"project_version_pin_written":      false,
		"live_remote_validation_performed": false,
		"external_call_made":               false,
		"contains_file_content":            false,
		"contains_remote_url":              false,
		"contains_credentials":             false,
		"contains_commit_sha":              false,
		"contains_branch_name":             false,
		"contains_git_output":              false,
		"contains_provider_response":       false,
		"required_controls":                []string{"operator_review", "git_workspace_backend", "secret_scan_backend", "git_commit_backend", "git_push_backend", "provider_review_backend", "project_version_pin_write_backend", "live_remote_validation_backend"},
		"disabled_backends":                []string{"git_workspace_mutation", "secret_scan", "git_commit", "git_push", "provider_review", "project_version_pin_write", "live_remote_validation"},
		"promotion_blockers":               []string{"git_workspace_backend_disabled", "secret_scan_not_performed", "git_commit_not_created", "git_push_not_performed", "provider_review_workflow_not_wired", "project_version_pin_write_disabled", "live_remote_commit_validation_not_performed"},
		"promotion_sequence":               []string{"operator_review_sanitized_audit_result", "materialize_scaffold_in_clean_workspace", "run_secret_scan", "commit_config_scaffold", "push_review_branch", "open_provider_review", "pin_project_version_config_commit_sha", "validate_live_remote_commit", "record_redacted_live_result"},
		"suppressed_fields":                []string{"file_content", "secret_values", "git_credentials", "provider_token", "remote_url", "branch_name", "commit_message", "commit_sha", "git_output", "provider_response_body", "provider_response_headers"},
		"message":                          "Sanitized audit evidence can only be reviewed for future promotion; this preview still performs no Git mutation, provider request, ProjectVersion pin write, or live validation.",
	}
}

func stringListContains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func (s *Server) createRepositorySync(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	projectID, err := s.projectIDForRepositoryGorm(r.Context(), repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_repository", ID: repoID, ProjectID: projectID}, "repo.sync") {
		return
	}
	var req struct {
		SourceRemoteID  string         `json:"source_remote_id"`
		TargetRemoteIDs []string       `json:"target_remote_ids"`
		Refs            map[string]any `json:"refs"`
		AllowForce      bool           `json:"allow_force"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.SourceRemoteID == "" {
		writeError(w, http.StatusBadRequest, "source_remote_id is required")
		return
	}
	var repoModel GormProjectGitRepository
	if err := s.store.Gorm.WithContext(r.Context()).First(&repoModel, &GormProjectGitRepository{GormBase: GormBase{ID: repoID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	repo := gitRepositoryMap(repoModel)
	source, err := remoteForRepositoryGorm(r.Context(), s.store.Gorm, repoID, req.SourceRemoteID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	targetIDs := req.TargetRemoteIDs
	if len(targetIDs) == 0 {
		targetIDs, err = defaultTargetRemoteIDsGorm(r.Context(), s.store.Gorm, repoID, req.SourceRemoteID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not select target remotes")
			return
		}
	}
	if len(targetIDs) == 0 {
		writeError(w, http.StatusBadRequest, "target_remote_ids is required")
		return
	}
	var runs []map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		for _, targetID := range targetIDs {
			if targetID == req.SourceRemoteID {
				return fmt.Errorf("target_remote_ids cannot include source_remote_id")
			}
			target, err := remoteForRepositoryGorm(r.Context(), tx, repoID, targetID)
			if err != nil {
				return fmt.Errorf("target remote not found in repository")
			}
			run, err := s.enqueueRepoSyncRunGorm(r.Context(), tx, repo, source, target, req.Refs, req.AllowForce, currentUser(r).ID, "")
			if err != nil {
				return fmt.Errorf("could not enqueue sync")
			}
			runs = append(runs, run)
		}
		_, err := syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"items": runs})
}

func (s *Server) createRepositoryTag(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	projectID, err := s.projectIDForRepositoryGorm(r.Context(), repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	var req repositoryTagRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.TagName == "" {
		writeError(w, http.StatusBadRequest, "tag_name is required")
		return
	}
	payload := map[string]any{"kind": "repository_tag", "repo_id": repoID, "request": req}
	if !s.requireProjectPolicyOrApproval(w, r, PolicyResource{Type: "git_repository", ID: repoID, ProjectID: projectID}, "repo.tag", "tag "+req.TagName, payload) {
		return
	}
	var runs []map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		runs, err = s.enqueueRepositoryTagRunsGorm(r.Context(), tx, repoID, req, currentUser(r).ID)
		if err != nil {
			return err
		}
		_, err = syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	runs = repoTagRunsWithRemoteRehearsal(runs)
	writeJSON(w, http.StatusCreated, map[string]any{"items": runs})
}

func (s *Server) listRepoSyncAssets(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	includeArchived := boolQuery(r, "include_archived")
	projectID, err := s.projectIDForRepositoryGorm(r.Context(), repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_asset", ProjectID: projectID}, "read") {
		return
	}
	items, err := s.repoSyncAssetListGorm(r.Context(), repoID, includeArchived)
	if err != nil {
		writeQueryResult(w, nil, err)
		return
	}
	annotateRepoSyncAssetRisks(items)
	writeQueryResult(w, items, nil)
}

func annotateRepoSyncAssetRisks(items []map[string]any) {
	for _, item := range items {
		severity, summary := repoSyncAssetRisk(item)
		item["risk_severity"] = severity
		item["risk_summary"] = summary
	}
}

func repoSyncAssetRisk(asset map[string]any) (string, string) {
	if repoSyncAssetArchived(asset) {
		return "warning", "archived; restore before running"
	}
	if enabled, ok := asset["enabled"].(bool); ok && !enabled {
		return "warning", "disabled; manual and webhook runs are paused"
	}
	if signalSeverityFromSync(asset["last_sync_status"]) == "danger" {
		return "danger", "last sync failed"
	}
	runningRuns := intFromAny(asset["running_runs"], 0)
	if runningRuns >= 3 {
		return "danger", fmt.Sprintf("%d active runs", runningRuns)
	}
	if runningRuns > 0 {
		return "warning", fmt.Sprintf("%d active runs", runningRuns)
	}
	failedRuns := intFromAny(asset["failed_runs"], 0)
	if failedRuns >= 5 {
		return "danger", fmt.Sprintf("%d failed runs", failedRuns)
	}
	totalRuns := intFromAny(asset["total_runs"], 0)
	successRate := floatFromAny(asset["success_rate"], 100)
	if totalRuns >= 5 && successRate < 50 {
		return "danger", fmt.Sprintf("%.0f%% success rate", successRate)
	}
	if failedRuns > 0 {
		return "warning", fmt.Sprintf("%d failed runs", failedRuns)
	}
	if totalRuns >= 5 && successRate < 80 {
		return "warning", fmt.Sprintf("%.0f%% success rate", successRate)
	}
	return "ok", "healthy"
}

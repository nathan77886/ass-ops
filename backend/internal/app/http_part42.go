package app

import (
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strings"
)

func (s *Server) createRepoTagRunActionsRefresh(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	_, projectID, err := s.repoTagRunWithProjectGorm(r.Context(), s.store.Gorm, runID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_tag_run", ID: runID, ProjectID: projectID}, "update") {
		return
	}
	tx := s.store.Gorm.WithContext(r.Context()).Begin()
	if tx.Error != nil {
		writeError(w, http.StatusInternalServerError, "could not start Actions refresh transaction")
		return
	}
	defer tx.Rollback()
	lockedRun, projectID, err := s.repoTagRunWithProjectGorm(r.Context(), tx, runID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	status := strings.ToLower(strings.TrimSpace(lockedRun.Status))
	if status != "completed" && status != "succeeded" && status != "success" {
		writeError(w, http.StatusBadRequest, "repo tag run must be completed before refreshing GitHub Actions")
		return
	}
	targetRemoteID := cleanOptionalID(firstNonEmptyString(lockedRun.TargetRemoteID.String, lockedRun.GitRemoteID))
	if targetRemoteID == "" {
		writeError(w, http.StatusBadRequest, "target_remote_id is required")
		return
	}
	remote, remoteProjectID, err := s.gitRemoteWithProjectGormTx(r.Context(), tx, targetRemoteID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if remoteProjectID != projectID {
		writeError(w, http.StatusBadRequest, "target remote does not belong to repo tag project")
		return
	}
	if _, _, err := gitHubRepositoryFromRemote(gitRemoteMap(remote, nil, remoteProjectID)); err != nil {
		writeError(w, http.StatusBadRequest, "target remote must be a GitHub repository")
		return
	}
	inFlight, err := s.repoTagInFlightOperationGorm(r.Context(), tx, runID, "github.actions.sync", "repo_tag_actions_refresh", "github.actions.sync")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not check Actions refresh queue")
		return
	}
	if inFlight != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"mode":                         "repo_tag_actions_refresh_enqueue",
			"idempotent":                   true,
			"repo_tag_run_id":              runID,
			"target_remote_id":             targetRemoteID,
			"operation":                    repoTagLookupOperationSummary(inFlight),
			"worker_job":                   repoTagLookupWorkerJobSummary(inFlight),
			"provider_api_call_enqueued":   true,
			"raw_provider_response_stored": false,
			"credentials_recorded":         false,
			"remote_url_recorded":          false,
		})
		return
	}
	input := map[string]any{
		"repo_tag_run_id":  runID,
		"target_remote_id": targetRemoteID,
		"refresh_kind":     "repo_tag_actions_refresh",
		"commit_sha":       strings.TrimSpace(lockedRun.TargetSHA),
		"tag_name":         strings.TrimSpace(lockedRun.TagName),
		"limit":            50,
	}
	op, err := enqueueOperationGorm(r.Context(), tx, projectID, targetRemoteID, "github.actions.sync", "refresh GitHub Actions after repository tag", input, []string{"github", "git"}, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not enqueue Actions refresh")
		return
	}
	if _, err := syncCanonicalAssetsGorm(r.Context(), tx); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync canonical assets")
		return
	}
	if err := tx.Commit().Error; err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit Actions refresh")
		return
	}
	job, jobErr := s.workerJobForOperationGorm(r.Context(), s.store.Gorm, fmt.Sprint(op["id"]), "github.actions.sync")
	if jobErr != nil && s.log != nil {
		s.log.Warn("repo tag Actions refresh worker job lookup failed", "operation_id", op["id"], "repo_tag_run_id", runID, "error", jobErr)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"mode":                         "repo_tag_actions_refresh_enqueue",
		"idempotent":                   false,
		"repo_tag_run_id":              runID,
		"target_remote_id":             targetRemoteID,
		"operation":                    repoTagLookupOperationSummary(op),
		"worker_job":                   repoTagLookupWorkerJobSummary(job),
		"provider_api_call_enqueued":   true,
		"raw_provider_response_stored": false,
		"credentials_recorded":         false,
		"remote_url_recorded":          false,
	})
}

func repoTagRunsWithRemoteRehearsal(items []map[string]any) []map[string]any {
	for _, item := range items {
		item["remote_rehearsal_plan"] = repoTagRemoteRehearsalPlan(item)
	}
	return items
}

func (s *Server) recordRepoTagRunResultSnapshot(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	projectID, err := repoTagRunProjectID(r.Context(), s.store.Gorm, runID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_tag_run", ID: runID, ProjectID: projectID}, "update") {
		return
	}
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := RecordRepoTagRunResultSnapshot(r.Context(), s.store, RepoTagRunResultSnapshotOptions{RepoTagRunID: runID, DryRun: req.DryRun})
	if err != nil {
		writeError(w, http.StatusBadRequest, "record repo tag result snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordRepoTagRunActionsRefreshSnapshot(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	projectID, err := repoTagRunProjectID(r.Context(), s.store.Gorm, runID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_tag_run", ID: runID, ProjectID: projectID}, "update") {
		return
	}
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := RecordRepoTagRunActionsRefreshSnapshot(r.Context(), s.store, RepoTagRunActionsRefreshSnapshotOptions{RepoTagRunID: runID, DryRun: req.DryRun})
	if err != nil {
		writeError(w, http.StatusBadRequest, "record repo tag actions refresh snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

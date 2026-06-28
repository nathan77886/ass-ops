package app

import (
	"context"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"net/http"
	"strings"
)

func (s *Server) annotateRepoTagRunOperationsGorm(ctx context.Context, items []map[string]any) error {
	if len(items) == 0 {
		return nil
	}
	var ops []GormOperationRun
	if err := s.store.Gorm.WithContext(ctx).Where(gormField("operation_type", []string{"repo.tag.lookup", "github.actions.sync"})).Order(gormOrderDesc("created_at")).Find(&ops).Error; err != nil {
		return err
	}
	itemByID := make(map[string]map[string]any, len(items))
	for _, item := range items {
		itemByID[cleanOptionalID(fmt.Sprint(item["id"]))] = item
	}
	seenLookup := map[string]bool{}
	seenActions := map[string]bool{}
	for _, op := range ops {
		input := mapFromAny(op.Input.Data)
		runID := cleanOptionalID(fmt.Sprint(input["repo_tag_run_id"]))
		item := itemByID[runID]
		if item == nil {
			continue
		}
		switch op.OperationType {
		case "repo.tag.lookup":
			if seenLookup[runID] {
				continue
			}
			seenLookup[runID] = true
			result := mapFromAny(op.Result.Data)
			item["lookup_operation_id"] = op.ID
			item["lookup_operation_status"] = op.Status
			item["lookup_operation_error"] = op.Error
			item["lookup_git_remote_lookup_performed"] = boolOnlyFromAny(result["git_remote_lookup_performed"])
			item["lookup_remote_tag_found"] = boolOnlyFromAny(result["remote_tag_found"])
			item["lookup_matched_sha_present"] = boolOnlyFromAny(result["matched_sha_present"])
			item["lookup_matched_count"] = intFromAny(result["matched_count"], 0)
			item["lookup_credential_userinfo_stripped"] = boolOnlyFromAny(result["credential_userinfo_stripped"])
			item["lookup_operation_started_at"] = nullableTimeAny(op.StartedAt)
			item["lookup_operation_finished_at"] = nullableTimeAny(op.FinishedAt)
			item["lookup_operation_created_at"] = op.CreatedAt
		case "github.actions.sync":
			if seenActions[runID] || strings.TrimSpace(fmt.Sprint(input["refresh_kind"])) != "repo_tag_actions_refresh" {
				continue
			}
			seenActions[runID] = true
			result := mapFromAny(op.Result.Data)
			item["actions_refresh_operation_id"] = op.ID
			item["actions_refresh_operation_status"] = op.Status
			item["actions_refresh_synced_count"] = intFromAny(result["count"], 0)
			item["actions_refresh_operation_started_at"] = nullableTimeAny(op.StartedAt)
			item["actions_refresh_operation_finished_at"] = nullableTimeAny(op.FinishedAt)
			item["actions_refresh_operation_created_at"] = op.CreatedAt
		}
	}
	return nil
}

func (s *Server) createRepoTagRunLiveLookup(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	run, projectID, err := s.repoTagRunWithProjectGorm(r.Context(), s.store.Gorm, runID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_tag_run", ID: runID, ProjectID: projectID}, "update") {
		return
	}
	targetRemoteID := cleanOptionalID(firstNonEmptyString(run.TargetRemoteID.String, run.GitRemoteID))
	tagName := strings.TrimSpace(run.TagName)
	if targetRemoteID == "" {
		writeError(w, http.StatusBadRequest, "target_remote_id is required")
		return
	}
	if tagName == "" {
		writeError(w, http.StatusBadRequest, "tag_name is required")
		return
	}
	if !isSafeGitRefPart(tagName) {
		writeError(w, http.StatusBadRequest, "tag_name is invalid")
		return
	}
	tx := s.store.Gorm.WithContext(r.Context()).Begin()
	if tx.Error != nil {
		writeError(w, http.StatusInternalServerError, "could not start lookup transaction")
		return
	}
	defer tx.Rollback()
	lockedRun, projectID, err := s.repoTagRunWithProjectGorm(r.Context(), tx, runID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	targetRemoteID = cleanOptionalID(firstNonEmptyString(lockedRun.TargetRemoteID.String, lockedRun.GitRemoteID))
	tagName = strings.TrimSpace(lockedRun.TagName)
	if targetRemoteID == "" || tagName == "" {
		writeError(w, http.StatusBadRequest, "repo tag run requires target remote and tag name")
		return
	}
	inFlight, err := s.repoTagInFlightOperationGorm(r.Context(), tx, runID, "repo.tag.lookup", "", "repo.tag.lookup")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not check lookup queue")
		return
	}
	if inFlight != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"mode":                    "repo_tag_live_lookup_enqueue",
			"idempotent":              true,
			"repo_tag_run_id":         runID,
			"target_remote_id":        targetRemoteID,
			"operation":               repoTagLookupOperationSummary(inFlight),
			"worker_job":              repoTagLookupWorkerJobSummary(inFlight),
			"credentials_recorded":    false,
			"remote_url_recorded":     false,
			"raw_git_output_recorded": false,
		})
		return
	}
	input := map[string]any{
		"repo_tag_run_id":  runID,
		"target_remote_id": targetRemoteID,
		"tag_name":         tagName,
	}
	op, err := enqueueOperationGorm(r.Context(), tx, projectID, targetRemoteID, "repo.tag.lookup", "lookup repository tag", input, []string{"git"}, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not enqueue live lookup")
		return
	}
	if _, err := syncCanonicalAssetsGorm(r.Context(), tx); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync canonical assets")
		return
	}
	if err := tx.Commit().Error; err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit live lookup")
		return
	}
	job, _ := s.workerJobForOperationGorm(r.Context(), s.store.Gorm, fmt.Sprint(op["id"]), "repo.tag.lookup")
	writeJSON(w, http.StatusAccepted, map[string]any{
		"mode":                    "repo_tag_live_lookup_enqueue",
		"idempotent":              false,
		"repo_tag_run_id":         runID,
		"target_remote_id":        targetRemoteID,
		"operation":               repoTagLookupOperationSummary(op),
		"worker_job":              repoTagLookupWorkerJobSummary(job),
		"credentials_recorded":    false,
		"remote_url_recorded":     false,
		"raw_git_output_recorded": false,
	})
}

func repoTagLookupOperationSummary(operation map[string]any) map[string]any {
	if operation == nil {
		return nil
	}
	return map[string]any{
		"id":             operation["id"],
		"operation_type": operation["operation_type"],
		"status":         operation["status"],
		"error":          operation["error"],
		"started_at":     operation["started_at"],
		"finished_at":    operation["finished_at"],
		"created_at":     operation["created_at"],
		"updated_at":     operation["updated_at"],
	}
}

func repoTagLookupWorkerJobSummary(job map[string]any) map[string]any {
	if job == nil {
		return nil
	}
	return map[string]any{
		"id":        job["worker_job_id"],
		"tool_name": job["worker_job_tool_name"],
		"status":    job["worker_job_status"],
	}
}

func (s *Server) repoTagRunWithProjectGorm(ctx context.Context, db *gorm.DB, runID string) (GormRepoTagRun, string, error) {
	var run GormRepoTagRun
	if err := db.WithContext(ctx).Where(&GormRepoTagRun{ID: cleanOptionalID(runID)}).First(&run).Error; err != nil {
		return run, "", gormNotFoundAsErrNotFound(err)
	}
	projectID := cleanOptionalID(run.ProjectID.String)
	if projectID == "" && cleanOptionalID(run.ProjectGitRepositoryID.String) != "" {
		var repo GormProjectGitRepository
		if err := db.WithContext(ctx).Where(&GormProjectGitRepository{GormBase: GormBase{ID: cleanOptionalID(run.ProjectGitRepositoryID.String)}}).First(&repo).Error; err != nil {
			return run, "", gormNotFoundAsErrNotFound(err)
		}
		projectID = repo.ProjectID
	}
	return run, projectID, nil
}

func (s *Server) repoTagInFlightOperationGorm(ctx context.Context, db *gorm.DB, runID, operationType, refreshKind, toolName string) (map[string]any, error) {
	var ops []GormOperationRun
	if err := db.WithContext(ctx).
		Where(&GormOperationRun{OperationType: operationType}).
		Where("status IN ?", []string{"queued", "running"}).
		Order("created_at DESC").
		Find(&ops).Error; err != nil {
		return nil, err
	}
	for _, op := range ops {
		input := mapFromAny(op.Input.Data)
		if cleanOptionalID(fmt.Sprint(input["repo_tag_run_id"])) != cleanOptionalID(runID) {
			continue
		}
		if refreshKind != "" && strings.TrimSpace(fmt.Sprint(input["refresh_kind"])) != refreshKind {
			continue
		}
		item := operationRunGormMap(op)
		job, err := s.workerJobForOperationGorm(ctx, db, op.ID, toolName)
		if err != nil {
			return nil, err
		}
		for key, value := range job {
			item[key] = value
		}
		return item, nil
	}
	return nil, nil
}

func (s *Server) workerJobForOperationGorm(ctx context.Context, db *gorm.DB, opID, toolName string) (map[string]any, error) {
	var job GormWorkerJob
	err := db.WithContext(ctx).
		Where(&GormWorkerJob{OperationRunID: validNullString(cleanOptionalID(opID)), ToolName: toolName}).
		Order("created_at DESC").
		First(&job).Error
	if errorsIsRecordNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"worker_job_id": job.ID, "worker_job_status": job.Status, "worker_job_tool_name": job.ToolName}, nil
}

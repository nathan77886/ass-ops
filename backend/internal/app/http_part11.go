package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"net/http"
	"strings"
)

func (s *Server) getProjectVersionValidation(w http.ResponseWriter, r *http.Request) {
	versionID := chi.URLParam(r, "id")
	versionModel, err := projectVersionByIDGorm(r.Context(), s.store.Gorm, versionID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := versionModel.ProjectID
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project_version", ID: versionID, ProjectID: projectID}, "read") {
		return
	}
	version := projectVersionMap(versionModel)
	remotes, err := projectVersionRemoteMapsGorm(r.Context(), s.store.Gorm, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load version remotes")
		return
	}
	tagRuns, err := projectVersionTagRunMapsGorm(r.Context(), s.store.Gorm, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load tag runs")
		return
	}
	actionRuns, err := projectVersionActionRunMapsGorm(r.Context(), s.store.Gorm, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load action runs")
		return
	}
	argoApps, err := projectVersionArgoAppMapsGorm(r.Context(), s.store.Gorm, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load Argo apps")
		return
	}
	argoConnections, err := projectVersionArgoConnectionMapsGorm(r.Context(), s.store.Gorm, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load Argo connections")
		return
	}
	refreshOperations, err := queryProjectVersionRefreshOperationsGorm(r.Context(), s.store.Gorm, fmt.Sprint(version["id"]), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load project version refresh operations")
		return
	}
	backgroundOperations, err := queryProjectVersionValidationRerunOperationsGorm(r.Context(), s.store.Gorm, fmt.Sprint(version["id"]), projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load project version validation rerun operations")
		return
	}
	writeJSON(w, http.StatusOK, projectVersionValidationPreview(version, remotes, tagRuns, actionRuns, argoApps, argoConnections, refreshOperations, backgroundOperations))
}

func (s *Server) refreshProjectVersionProviders(w http.ResponseWriter, r *http.Request) {
	versionID := chi.URLParam(r, "id")
	versionModel, err := projectVersionByIDGorm(r.Context(), s.store.Gorm, versionID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := versionModel.ProjectID
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project_version", ID: versionID, ProjectID: projectID}, "project_version.refresh") {
		return
	}
	version, remotes, argoConnections, err := s.projectVersionRefreshInputsGorm(r.Context(), versionID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load project version refresh inputs")
		return
	}
	metadata := mapFromAny(version["metadata"])
	repositories := mapSliceFromAny(metadata["repositories"])
	refreshPlan := projectVersionProviderRefreshPlan(repositories, remotes, argoConnections)
	steps := mapSliceFromAny(refreshPlan["steps"])
	var result map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = s.enqueueProjectVersionRefreshOperationsGorm(r.Context(), tx, version, steps, argoConnections, currentUser(r).ID)
		if err != nil {
			return err
		}
		_, err = syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		if errors.Is(err, errProjectVersionRefreshAlreadyQueued) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) requestProjectVersionValidationRerun(w http.ResponseWriter, r *http.Request) {
	versionID := chi.URLParam(r, "id")
	versionModel, err := projectVersionByIDGorm(r.Context(), s.store.Gorm, versionID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := versionModel.ProjectID
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project_version", ID: versionID, ProjectID: projectID}, "project_version.refresh") {
		return
	}
	version := projectVersionMap(versionModel)
	var result map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = s.enqueueProjectVersionValidationRerunGorm(r.Context(), tx, version, currentUser(r).ID)
		if err != nil {
			return err
		}
		_, err = syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		if errors.Is(err, errProjectVersionRefreshAlreadyQueued) {
			writeError(w, http.StatusConflict, "project version validation rerun is already queued or running")
			return
		}
		writeError(w, http.StatusBadRequest, "could not enqueue validation rerun")
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) pinProjectVersionConfigCommit(w http.ResponseWriter, r *http.Request) {
	versionID := chi.URLParam(r, "id")
	version, err := projectVersionByIDGorm(r.Context(), s.store.Gorm, versionID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := version.ProjectID
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project_version", ID: versionID, ProjectID: projectID}, "update") {
		return
	}
	var req struct {
		RepositoryID string `json:"repository_id"`
		RemoteID     string `json:"remote_id"`
		DryRun       bool   `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.RepositoryID) == "" {
		writeError(w, http.StatusBadRequest, "repository_id is required")
		return
	}
	result, err := PinConfigCommit(r.Context(), s.store, ConfigCommitPinOptions{
		ProjectVersionID: versionID,
		RepositoryID:     req.RepositoryID,
		RemoteID:         req.RemoteID,
		DryRun:           req.DryRun,
	})
	if err != nil {
		if errors.Is(err, errRepositoryRoleNotConfig) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":                       "repository is not a config repository",
				"blocked_reasons":             []string{"repository_role_is_not_config"},
				"project_version_id":          versionID,
				"repository_id":               strings.TrimSpace(req.RepositoryID),
				"config_commit_sha_written":   false,
				"project_version_pin_written": false,
				"external_call_made":          false,
				"git_fetch_performed":         false,
				"provider_api_called":         false,
				"operation_log_written":       false,
				"commit_sha_included":         false,
				"remote_url_included":         false,
				"secret_included":             false,
			})
			return
		}
		if s.log != nil {
			s.log.Warn("config commit pin failed", "project_version_id", versionID, "repository_id", strings.TrimSpace(req.RepositoryID), "error", err)
		}
		writeError(w, http.StatusBadRequest, "pin config commit failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordProjectVersionValidationSnapshot(w http.ResponseWriter, r *http.Request) {
	s.recordProjectVersionValidationSnapshotWithOptions(w, r, false)
}

func (s *Server) recordProjectVersionValidationRerunSnapshot(w http.ResponseWriter, r *http.Request) {
	s.recordProjectVersionValidationSnapshotWithOptions(w, r, true)
}

func (s *Server) recordProjectVersionValidationSnapshotWithOptions(w http.ResponseWriter, r *http.Request, requireRecordedRefresh bool) {
	versionID := chi.URLParam(r, "id")
	version, err := projectVersionByIDGorm(r.Context(), s.store.Gorm, versionID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := version.ProjectID
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project_version", ID: versionID, ProjectID: projectID}, "update") {
		return
	}
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	recordingTrigger := "operator_request"
	if requireRecordedRefresh {
		recordingTrigger = "validation_auto_reload"
	}
	result, err := RecordProjectVersionValidationSnapshot(r.Context(), s.store, ProjectVersionValidationSnapshotOptions{
		ProjectVersionID:       versionID,
		DryRun:                 req.DryRun,
		RequireRecordedRefresh: requireRecordedRefresh,
		RecordingTrigger:       recordingTrigger,
	})
	if err != nil {
		if s.log != nil {
			s.log.Warn("project version validation snapshot failed", "project_version_id", versionID, "error", err)
		}
		writeError(w, http.StatusBadRequest, "record validation snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) projectVersionRefreshInputsGorm(ctx context.Context, versionID, projectID string) (map[string]any, []map[string]any, []map[string]any, error) {
	version, err := projectVersionByIDGorm(ctx, s.store.Gorm, versionID)
	if err != nil {
		return nil, nil, nil, err
	}
	remotes, err := projectVersionRemoteMapsGorm(ctx, s.store.Gorm, projectID)
	if err != nil {
		return nil, nil, nil, err
	}
	argoConnections, err := projectVersionArgoConnectionMapsGorm(ctx, s.store.Gorm, projectID)
	if err != nil {
		return nil, nil, nil, err
	}
	return projectVersionMap(version), remotes, argoConnections, nil
}

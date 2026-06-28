package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func severityForCount(count, warningAt, dangerAt int) string {
	switch {
	case dangerAt > 0 && count >= dangerAt:
		return "danger"
	case warningAt > 0 && count >= warningAt:
		return "warning"
	default:
		return "ok"
	}
}

func floatFromAny(value any, fallback float64) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case int32:
		return float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err == nil {
			return parsed
		}
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func (s *Server) updateRepoSyncAsset(w http.ResponseWriter, r *http.Request) {
	assetID := chi.URLParam(r, "id")
	var assetModel GormRepoSyncAsset
	if err := s.store.Gorm.WithContext(r.Context()).First(&assetModel, &GormRepoSyncAsset{GormBase: GormBase{ID: assetID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	projectID := assetModel.ProjectID
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_asset", ID: assetID, ProjectID: projectID}, "update") {
		return
	}
	if assetModel.ArchivedAt.Valid {
		writeError(w, http.StatusConflict, "repo sync asset is archived")
		return
	}
	var req struct {
		Name        *string         `json:"name"`
		TriggerMode *string         `json:"trigger_mode"`
		SyncMode    *string         `json:"sync_mode"`
		Transport   *string         `json:"transport"`
		Driver      *string         `json:"driver"`
		Refs        *map[string]any `json:"refs"`
		Enabled     *bool           `json:"enabled"`
		Metadata    *map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	var item map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var asset GormRepoSyncAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&asset, &GormRepoSyncAsset{GormBase: GormBase{ID: assetID}}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		if asset.ArchivedAt.Valid {
			return errRepoSyncAssetArchived
		}
		if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
			asset.Name = strings.TrimSpace(*req.Name)
		}
		if req.TriggerMode != nil && strings.TrimSpace(*req.TriggerMode) != "" {
			asset.TriggerMode = strings.TrimSpace(*req.TriggerMode)
		}
		if req.SyncMode != nil && strings.TrimSpace(*req.SyncMode) != "" {
			asset.SyncMode = strings.TrimSpace(*req.SyncMode)
		}
		if req.Transport != nil && strings.TrimSpace(*req.Transport) != "" {
			asset.Transport = strings.TrimSpace(*req.Transport)
		}
		if req.Driver != nil && strings.TrimSpace(*req.Driver) != "" {
			asset.Driver = strings.TrimSpace(*req.Driver)
		}
		if req.Refs != nil {
			asset.Refs = JSONValue{Data: *req.Refs}
		}
		if req.Enabled != nil {
			asset.Enabled = *req.Enabled
		}
		if req.Metadata != nil {
			asset.Metadata = JSONValue{Data: *req.Metadata}
		}
		if err := tx.Save(&asset).Error; err != nil {
			return err
		}
		item = repoSyncAssetMap(asset)
		_, err := syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		if errors.Is(err, errRepoSyncAssetArchived) {
			writeError(w, http.StatusConflict, "repo sync asset is archived")
			return
		}
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) archiveRepoSyncAsset(w http.ResponseWriter, r *http.Request) {
	assetID := chi.URLParam(r, "id")
	var assetModel GormRepoSyncAsset
	if err := s.store.Gorm.WithContext(r.Context()).First(&assetModel, &GormRepoSyncAsset{GormBase: GormBase{ID: assetID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	projectID := assetModel.ProjectID
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_asset", ID: assetID, ProjectID: projectID}, "update") {
		return
	}
	var item map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var asset GormRepoSyncAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&asset, &GormRepoSyncAsset{GormBase: GormBase{ID: assetID}}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		asset.Enabled = false
		if !asset.ArchivedAt.Valid {
			asset.ArchivedAt = validNullTime(time.Now())
		}
		if err := tx.Save(&asset).Error; err != nil {
			return err
		}
		item = repoSyncAssetMap(asset)
		_, err := syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) restoreRepoSyncAsset(w http.ResponseWriter, r *http.Request) {
	assetID := chi.URLParam(r, "id")
	var assetModel GormRepoSyncAsset
	if err := s.store.Gorm.WithContext(r.Context()).First(&assetModel, &GormRepoSyncAsset{GormBase: GormBase{ID: assetID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	projectID := assetModel.ProjectID
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_asset", ID: assetID, ProjectID: projectID}, "update") {
		return
	}
	var item map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var asset GormRepoSyncAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&asset, &GormRepoSyncAsset{GormBase: GormBase{ID: assetID}}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		asset.ArchivedAt = sql.NullTime{}
		asset.Enabled = true
		if err := tx.Save(&asset).Error; err != nil {
			return err
		}
		item = repoSyncAssetMap(asset)
		_, err := syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) runRepoSyncAsset(w http.ResponseWriter, r *http.Request) {
	assetID := chi.URLParam(r, "id")
	var assetModel GormRepoSyncAsset
	if err := s.store.Gorm.WithContext(r.Context()).First(&assetModel, &GormRepoSyncAsset{GormBase: GormBase{ID: assetID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	projectID := assetModel.ProjectID
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_asset", ID: assetID, ProjectID: projectID}, "repo.sync") {
		return
	}
	var run map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var lockedAsset GormRepoSyncAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedAsset, &GormRepoSyncAsset{GormBase: GormBase{ID: assetID}}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		if !lockedAsset.Enabled {
			return errRepoSyncAssetDisabled
		}
		if lockedAsset.ArchivedAt.Valid {
			return errRepoSyncAssetArchived
		}
		repoID := lockedAsset.ProjectGitRepositoryID
		var repoModel GormProjectGitRepository
		if err := tx.First(&repoModel, &GormProjectGitRepository{GormBase: GormBase{ID: repoID}}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		repo := gitRepositoryMap(repoModel)
		source, err := remoteForRepositoryGorm(r.Context(), tx, repoID, lockedAsset.SourceRemoteID)
		if err != nil {
			return fmt.Errorf("source remote not found in repository")
		}
		target, err := remoteForRepositoryGorm(r.Context(), tx, repoID, lockedAsset.TargetRemoteID)
		if err != nil {
			return fmt.Errorf("target remote not found in repository")
		}
		run, err = s.enqueueRepoSyncRunGorm(r.Context(), tx, repo, source, target, mapFromAny(lockedAsset.Refs.Data), false, currentUser(r).ID, assetID)
		if err != nil {
			return fmt.Errorf("could not enqueue repo sync asset")
		}
		if err := tx.Model(&GormRepoSyncAsset{}).
			Where(&GormRepoSyncAsset{GormBase: GormBase{ID: assetID}}).
			Updates(map[string]any{"last_sync_status": "queued", "last_sync_run_id": validNullString(cleanOptionalID(fmt.Sprint(run["id"])))}).Error; err != nil {
			return err
		}
		_, err = syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		if errors.Is(err, errRepoSyncAssetDisabled) {
			writeError(w, http.StatusConflict, "repo sync asset is disabled")
			return
		}
		if errors.Is(err, errRepoSyncAssetArchived) {
			writeError(w, http.StatusConflict, "repo sync asset is archived")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"run": run})
}

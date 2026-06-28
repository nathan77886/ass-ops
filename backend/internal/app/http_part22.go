package app

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"strings"
)

func (s *Server) repoSyncAssetListGorm(ctx context.Context, repoID string, includeArchived bool) ([]map[string]any, error) {
	var assets []GormRepoSyncAsset
	query := s.store.Gorm.WithContext(ctx).Where(&GormRepoSyncAsset{ProjectGitRepositoryID: repoID})
	if !includeArchived {
		query = query.Where(&GormRepoSyncAsset{ArchivedAt: sql.NullTime{Valid: false}})
	}
	if err := query.Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "created_at"}, Desc: true}}}).Find(&assets).Error; err != nil {
		return nil, err
	}
	remoteNames, err := repoSyncRemoteNamesGorm(ctx, s.store.Gorm, assets)
	if err != nil {
		return nil, err
	}
	runsByAsset, err := repoSyncRunsByAssetGorm(ctx, s.store.Gorm, assets)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(assets))
	for _, asset := range assets {
		item := repoSyncAssetMap(asset)
		item["source_remote_name"] = remoteNames[asset.SourceRemoteID]
		item["target_remote_name"] = remoteNames[asset.TargetRemoteID]
		for key, value := range repoSyncAssetAnalyticsFromRuns(runsByAsset[asset.ID]) {
			item[key] = value
		}
		items = append(items, item)
	}
	return items, nil
}

func repoSyncRemoteNamesGorm(ctx context.Context, db *gorm.DB, assets []GormRepoSyncAsset) (map[string]string, error) {
	ids := []string{}
	seen := map[string]bool{}
	for _, asset := range assets {
		for _, id := range []string{asset.SourceRemoteID, asset.TargetRemoteID} {
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	var remotes []GormGitRemote
	if err := db.WithContext(ctx).Find(&remotes, ids).Error; err != nil {
		return nil, err
	}
	names := make(map[string]string, len(remotes))
	for _, remote := range remotes {
		names[remote.ID] = remote.Name
	}
	return names, nil
}

func repoSyncRunsByAssetGorm(ctx context.Context, db *gorm.DB, assets []GormRepoSyncAsset) (map[string][]GormRepoSyncRun, error) {
	ids := make([]string, 0, len(assets))
	for _, asset := range assets {
		ids = append(ids, asset.ID)
	}
	items := map[string][]GormRepoSyncRun{}
	if len(ids) == 0 {
		return items, nil
	}
	var runs []GormRepoSyncRun
	if err := db.WithContext(ctx).Where(map[string]any{"repo_sync_asset_id": ids}).Find(&runs).Error; err != nil {
		return nil, err
	}
	for _, run := range runs {
		if run.RepoSyncAssetID.Valid {
			items[run.RepoSyncAssetID.String] = append(items[run.RepoSyncAssetID.String], run)
		}
	}
	return items, nil
}

func repoSyncAssetAnalyticsFromRuns(runs []GormRepoSyncRun) map[string]any {
	total := len(runs)
	completed := 0
	failed := 0
	running := 0
	var lastRunAt any
	var lastSuccessAt any
	var lastFailureAt any
	lastFailureMessage := ""
	durationTotal := 0.0
	durationCount := 0
	for _, run := range runs {
		if lastRunAt == nil || run.CreatedAt.After(projectVersionTimeFromAny(lastRunAt)) {
			lastRunAt = run.CreatedAt
		}
		switch run.Status {
		case "completed":
			completed++
			if run.FinishedAt.Valid && (lastSuccessAt == nil || run.FinishedAt.Time.After(projectVersionTimeFromAny(lastSuccessAt))) {
				lastSuccessAt = run.FinishedAt.Time
			}
		case "failed":
			failed++
			if run.FinishedAt.Valid && (lastFailureAt == nil || run.FinishedAt.Time.After(projectVersionTimeFromAny(lastFailureAt))) {
				lastFailureAt = run.FinishedAt.Time
			}
			if strings.TrimSpace(run.ErrorMessage) != "" && (lastFailureAt == run.FinishedAt.Time || lastFailureMessage == "") {
				lastFailureMessage = run.ErrorMessage
			}
		case "queued", "running", "provisioning":
			running++
		}
		if run.StartedAt.Valid && run.FinishedAt.Valid {
			durationTotal += run.FinishedAt.Time.Sub(run.StartedAt.Time).Seconds()
			durationCount++
		}
	}
	successRate := 0.0
	if total > 0 {
		successRate = float64(completed) / float64(total) * 100
	}
	avgDuration := 0.0
	if durationCount > 0 {
		avgDuration = durationTotal / float64(durationCount)
	}
	return map[string]any{"total_runs": total, "completed_runs": completed, "failed_runs": failed, "running_runs": running, "success_rate": successRate, "last_run_at": lastRunAt, "last_success_at": lastSuccessAt, "last_failure_at": lastFailureAt, "last_failure_message": lastFailureMessage, "avg_duration_seconds": avgDuration}
}

func (s *Server) repoSyncAssetDetailMapGorm(ctx context.Context, assetID string) (map[string]any, error) {
	var asset GormRepoSyncAsset
	if err := s.store.Gorm.WithContext(ctx).First(&asset, &GormRepoSyncAsset{GormBase: GormBase{ID: assetID}}).Error; err != nil {
		return nil, gormNotFoundAsErrNotFound(err)
	}
	item := repoSyncAssetMap(asset)
	var repo GormProjectGitRepository
	if err := s.store.Gorm.WithContext(ctx).First(&repo, &GormProjectGitRepository{GormBase: GormBase{ID: asset.ProjectGitRepositoryID}}).Error; err == nil {
		item["repository_name"] = repo.Name
	}
	remoteNames, err := repoSyncRemoteNamesGorm(ctx, s.store.Gorm, []GormRepoSyncAsset{asset})
	if err != nil {
		return nil, err
	}
	item["source_remote_name"] = remoteNames[asset.SourceRemoteID]
	item["target_remote_name"] = remoteNames[asset.TargetRemoteID]
	runsByAsset, err := repoSyncRunsByAssetGorm(ctx, s.store.Gorm, []GormRepoSyncAsset{asset})
	if err != nil {
		return nil, err
	}
	for key, value := range repoSyncAssetAnalyticsFromRuns(runsByAsset[asset.ID]) {
		item[key] = value
	}
	return item, nil
}

func (s *Server) createRepoSyncAsset(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	var repoModel GormProjectGitRepository
	if err := s.store.Gorm.WithContext(r.Context()).First(&repoModel, &GormProjectGitRepository{GormBase: GormBase{ID: repoID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	projectID := repoModel.ProjectID
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_asset", ProjectID: projectID}, "create") {
		return
	}
	var req struct {
		Name           string         `json:"name"`
		SourceRemoteID string         `json:"source_remote_id"`
		TargetRemoteID string         `json:"target_remote_id"`
		TriggerMode    string         `json:"trigger_mode"`
		SyncMode       string         `json:"sync_mode"`
		Transport      string         `json:"transport"`
		Driver         string         `json:"driver"`
		Refs           map[string]any `json:"refs"`
		Enabled        *bool          `json:"enabled"`
		Metadata       map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.SourceRemoteID == "" || req.TargetRemoteID == "" {
		writeError(w, http.StatusBadRequest, "source_remote_id and target_remote_id are required")
		return
	}
	if req.SourceRemoteID == req.TargetRemoteID {
		writeError(w, http.StatusBadRequest, "source and target remotes must differ")
		return
	}
	source, err := remoteForRepositoryGorm(r.Context(), s.store.Gorm, repoID, req.SourceRemoteID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "source remote not found in repository")
		return
	}
	target, err := remoteForRepositoryGorm(r.Context(), s.store.Gorm, repoID, req.TargetRemoteID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "target remote not found in repository")
		return
	}
	if req.Name == "" {
		req.Name = fmt.Sprint(source["name"]) + " to " + fmt.Sprint(target["name"])
	}
	if req.TriggerMode == "" {
		req.TriggerMode = "manual"
	}
	if req.SyncMode == "" {
		req.SyncMode = "selected_refs"
	}
	if req.Transport == "" {
		req.Transport = "ssh"
	}
	if req.Driver == "" {
		req.Driver = "projectops_worker_git_ssh"
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	var item map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		asset := GormRepoSyncAsset{
			ProjectID:              projectID,
			ProjectGitRepositoryID: repoID,
			Name:                   req.Name,
			SourceRemoteID:         cleanOptionalID(fmt.Sprint(source["id"])),
			TargetRemoteID:         cleanOptionalID(fmt.Sprint(target["id"])),
			TriggerMode:            req.TriggerMode,
			SyncMode:               req.SyncMode,
			Transport:              req.Transport,
			Driver:                 req.Driver,
			Refs:                   JSONValue{Data: req.Refs},
			Enabled:                enabled,
			LastSyncStatus:         "never",
			Metadata:               JSONValue{Data: req.Metadata},
		}
		if err := tx.Create(&asset).Error; err != nil {
			return err
		}
		item = repoSyncAssetMap(asset)
		_, err := syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		writeError(w, http.StatusBadRequest, "could not create resource")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

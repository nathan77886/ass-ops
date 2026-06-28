package app

import (
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"strings"
)

func assetMatchesQuery(asset GormAsset, queryText string) bool {
	if queryText == "" {
		return true
	}
	for _, value := range []string{asset.Name, asset.DisplayName, asset.ExternalID, asset.SourceTable} {
		if strings.Contains(strings.ToLower(value), queryText) {
			return true
		}
	}
	return false
}

func assetRiskRank(riskLevel string) int {
	switch riskLevel {
	case "high":
		return 300
	case "warning":
		return 200
	case "normal":
		return 100
	default:
		return 0
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func assetGraphViewMap(view GormAssetGraphView) map[string]any {
	return map[string]any{"id": view.ID, "user_id": view.UserID, "name": view.Name, "filters": mapFromAny(view.Filters.Data), "created_at": view.CreatedAt, "updated_at": view.UpdatedAt}
}

func assetGraphViewMaps(views []GormAssetGraphView) []map[string]any {
	items := make([]map[string]any, 0, len(views))
	for _, view := range views {
		items = append(items, assetGraphViewMap(view))
	}
	return items
}

func assetStatusSnapshotMap(snapshot GormAssetStatusSnapshot) map[string]any {
	return map[string]any{"id": snapshot.ID, "asset_id": snapshot.AssetID, "status": snapshot.Status, "health": snapshot.Health, "summary": snapshot.Summary, "raw": mapFromAny(snapshot.Raw.Data), "collected_at": snapshot.CollectedAt}
}

func assetStatusSnapshotMaps(snapshots []GormAssetStatusSnapshot) []map[string]any {
	items := make([]map[string]any, 0, len(snapshots))
	for _, snapshot := range snapshots {
		items = append(items, assetStatusSnapshotMap(snapshot))
	}
	return items
}

func assetRelationMap(relation GormAssetRelation) map[string]any {
	return map[string]any{"id": relation.ID, "project_id": nullableStringValue(relation.ProjectID), "from_asset_id": relation.FromAssetID, "to_asset_id": relation.ToAssetID, "relation_type": relation.RelationType, "metadata": mapFromAny(relation.Metadata.Data), "created_at": relation.CreatedAt}
}

func assetRelationMaps(relations []GormAssetRelation) []map[string]any {
	items := make([]map[string]any, 0, len(relations))
	for _, relation := range relations {
		items = append(items, assetRelationMap(relation))
	}
	return items
}

func (s *Server) deleteAssetRelation(w http.ResponseWriter, r *http.Request) {
	relationID := chi.URLParam(r, "id")
	var relation GormAssetRelation
	if err := s.store.Gorm.WithContext(r.Context()).First(&relation, &GormAssetRelation{ID: relationID}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	projectID := cleanOptionalID(relation.ProjectID.String)
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "asset relation has no project")
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "asset_relation", ID: relationID, ProjectID: projectID}, "update") {
		return
	}
	if stringFromMap(mapFromAny(relation.Metadata.Data), "source") != "manual" {
		writeError(w, http.StatusConflict, "only manual asset relations can be deleted")
		return
	}
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var locked GormAssetRelation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, &GormAssetRelation{ID: relationID}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		if stringFromMap(mapFromAny(locked.Metadata.Data), "source") != "manual" {
			return errAssetRelationNotManual
		}
		if err := tx.Delete(&locked).Error; err != nil {
			return err
		}
		_, err := syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		if errors.Is(err, errAssetRelationNotManual) {
			writeError(w, http.StatusConflict, "only manual asset relations can be deleted")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not delete asset relation")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func cleanAssetRelationType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "_")
	if len(value) > 80 {
		value = value[:80]
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "_.-")
}

func relationProjectID(fromAsset, toAsset map[string]any) string {
	fromProjectID := cleanOptionalID(fmt.Sprint(fromAsset["project_id"]))
	toProjectID := cleanOptionalID(fmt.Sprint(toAsset["project_id"]))
	if fromProjectID != "" && toProjectID != "" && fromProjectID != toProjectID {
		return ""
	}
	if fromProjectID != "" {
		return fromProjectID
	}
	return toProjectID
}

func (s *Server) listAssetDependencies(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "read") {
		return
	}
	user := currentUser(r)
	direction := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("direction")))
	if direction == "" {
		direction = "downstream"
	}
	if direction != "downstream" && direction != "upstream" {
		writeError(w, http.StatusBadRequest, "direction must be downstream or upstream")
		return
	}
	depth := intFromAny(r.URL.Query().Get("depth"), 4)
	if depth < 1 || depth > 8 {
		writeError(w, http.StatusBadRequest, "depth must be between 1 and 8")
		return
	}
	items, err := s.assetDependencyPathsGorm(r.Context(), user, chi.URLParam(r, "id"), r.URL.Query().Get("project_id"), direction, depth, 501)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	truncated := len(items) > 500
	if truncated {
		items = items[:500]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "truncated": truncated})
}

func (s *Server) createGitRepository(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_repository", ProjectID: projectID}, "create") {
		return
	}
	var req struct {
		Name          string `json:"name"`
		RepoKey       string `json:"repo_key"`
		DisplayName   string `json:"display_name"`
		RepoRole      string `json:"repo_role"`
		Status        string `json:"status"`
		Description   string `json:"description"`
		DefaultBranch string `json:"default_branch"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.RepoKey == "" {
		req.RepoKey = slugify(req.Name)
	}
	if req.DefaultBranch == "" {
		req.DefaultBranch = "main"
	}
	if req.RepoRole == "" {
		req.RepoRole = "code"
	}
	if req.Status == "" {
		req.Status = "active"
	}
	repo := GormProjectGitRepository{
		ProjectID:     projectID,
		Name:          req.Name,
		RepoKey:       req.RepoKey,
		DisplayName:   req.DisplayName,
		RepoRole:      req.RepoRole,
		Status:        req.Status,
		Description:   req.Description,
		DefaultBranch: req.DefaultBranch,
	}
	if err := s.store.Gorm.WithContext(r.Context()).Create(&repo).Error; err != nil {
		writeError(w, http.StatusBadRequest, "could not create resource")
		return
	}
	if _, err := s.store.SyncCanonicalAssets(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync git repository asset")
		return
	}
	writeJSON(w, http.StatusCreated, gitRepositoryMap(repo))
}

func (s *Server) listGitRepositories(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_repository", ProjectID: projectID}, "read") {
		return
	}
	var repos []GormProjectGitRepository
	err := s.store.Gorm.WithContext(r.Context()).
		Where(map[string]any{"project_id": projectID}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}, Desc: true}).
		Find(&repos).Error
	writeQueryResult(w, gitRepositoryMaps(repos), err)
}

func (s *Server) getGitRepository(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	projectID, err := s.projectIDForRepositoryGorm(r.Context(), repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_repository", ID: repoID, ProjectID: projectID}, "read") {
		return
	}
	repo, err := s.gitRepositoryByIDGorm(r.Context(), repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, gitRepositoryMap(repo))
}

func (s *Server) getConfigRepositoryScaffold(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	projectID, err := s.projectIDForRepositoryGorm(r.Context(), repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_repository", ID: repoID, ProjectID: projectID}, "read") {
		return
	}
	_, _, _, preview, err := s.configRepositoryScaffoldPreviewForRequest(r.Context(), repoID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load config scaffold preview")
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

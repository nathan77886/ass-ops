package app

import (
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"strconv"
	"strings"
)

func assetGraphLimit(raw string) int {
	limit := 80
	if parsed, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
		limit = parsed
	}
	if limit < 1 {
		return 1
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func (s *Server) listAssetGraphViews(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "read") {
		return
	}
	var views []GormAssetGraphView
	if err := s.store.Gorm.WithContext(r.Context()).Where(&GormAssetGraphView{UserID: currentUser(r).ID}).Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "updated_at"}, Desc: true}, {Column: clause.Column{Name: "name"}}}}).Limit(200).Find(&views).Error; err != nil {
		writeQueryResult(w, nil, err)
		return
	}
	writeQueryResult(w, assetGraphViewMaps(views), nil)
}

func (s *Server) createAssetGraphView(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "read") {
		return
	}
	var req struct {
		Name    string         `json:"name"`
		Filters map[string]any `json:"filters"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(name) > 80 {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	filters, err := sanitizeAssetGraphViewFilters(req.Filters)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	view := GormAssetGraphView{UserID: currentUser(r).ID, Name: name, Filters: JSONValue{Data: filters}}
	if err := s.store.Gorm.WithContext(r.Context()).Create(&view).Error; err != nil {
		if isUniqueViolation(err, "asset_graph_views_user_id_name_key") {
			writeError(w, http.StatusBadRequest, "an asset graph view with this name already exists")
			return
		}
		writeError(w, http.StatusBadRequest, "could not create asset graph view")
		return
	}
	writeJSON(w, http.StatusCreated, assetGraphViewMap(view))
}

func (s *Server) updateAssetGraphView(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "read") {
		return
	}
	var req struct {
		Name    string          `json:"name"`
		Filters json.RawMessage `json:"filters"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if req.Name != "" && name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(name) > 80 {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	var filters map[string]any
	filtersProvided := false
	if len(req.Filters) > 0 && string(req.Filters) != "null" {
		var raw map[string]any
		if err := json.Unmarshal(req.Filters, &raw); err != nil {
			writeError(w, http.StatusBadRequest, "invalid filters")
			return
		}
		sanitized, err := sanitizeAssetGraphViewFilters(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		filtersProvided = true
		filters = sanitized
	}
	var view GormAssetGraphView
	if err := s.store.Gorm.WithContext(r.Context()).Where(&GormAssetGraphView{GormBase: GormBase{ID: chi.URLParam(r, "id")}, UserID: currentUser(r).ID}).First(&view).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusBadRequest, "could not update asset graph view")
		return
	}
	if name != "" {
		view.Name = name
	}
	if filtersProvided {
		view.Filters = JSONValue{Data: filters}
	}
	if err := s.store.Gorm.WithContext(r.Context()).Save(&view).Error; err != nil {
		if isUniqueViolation(err, "asset_graph_views_user_id_name_key") {
			writeError(w, http.StatusBadRequest, "an asset graph view with this name already exists")
			return
		}
		writeError(w, http.StatusBadRequest, "could not update asset graph view")
		return
	}
	writeJSON(w, http.StatusOK, assetGraphViewMap(view))
}

func (s *Server) deleteAssetGraphView(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "read") {
		return
	}
	result := s.store.Gorm.WithContext(r.Context()).Where(&GormAssetGraphView{GormBase: GormBase{ID: chi.URLParam(r, "id")}, UserID: currentUser(r).ID}).Delete(&GormAssetGraphView{})
	if result.Error != nil {
		writeError(w, http.StatusInternalServerError, "could not delete asset graph view")
		return
	}
	if result.RowsAffected == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAssetRelations(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "read") {
		return
	}
	user := currentUser(r)
	relations, err := s.visibleAssetRelationsGorm(r.Context(), user, chi.URLParam(r, "id"), r.URL.Query().Get("project_id"), 500)
	if err != nil {
		writeQueryResult(w, nil, err)
		return
	}
	writeQueryResult(w, assetRelationMaps(relations), nil)
}

func (s *Server) listAssetStatusSnapshots(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "read") {
		return
	}
	assetID := chi.URLParam(r, "id")
	var asset GormAsset
	if err := s.store.Gorm.WithContext(r.Context()).First(&asset, &GormAsset{GormBase: GormBase{ID: assetID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	projectID := cleanOptionalID(asset.ProjectID.String)
	if projectID != "" && projectID != "<nil>" && !s.requireProjectPolicy(w, r, PolicyResource{Type: "asset", ID: assetID, ProjectID: projectID}, "read") {
		return
	}
	var snapshots []GormAssetStatusSnapshot
	if err := s.store.Gorm.WithContext(r.Context()).Where(&GormAssetStatusSnapshot{AssetID: assetID}).Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "collected_at"}, Desc: true}, {Column: clause.Column{Name: "id"}, Desc: true}}}).Limit(50).Find(&snapshots).Error; err != nil {
		writeQueryResult(w, nil, err)
		return
	}
	writeQueryResult(w, assetStatusSnapshotMaps(snapshots), nil)
}

func (s *Server) createAssetRelation(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "asset"}, "update") {
		return
	}
	var req struct {
		FromAssetID  string         `json:"from_asset_id"`
		ToAssetID    string         `json:"to_asset_id"`
		RelationType string         `json:"relation_type"`
		Metadata     map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.FromAssetID = strings.TrimSpace(req.FromAssetID)
	req.ToAssetID = strings.TrimSpace(req.ToAssetID)
	req.RelationType = cleanAssetRelationType(req.RelationType)
	if req.FromAssetID == "" || req.ToAssetID == "" || req.RelationType == "" {
		writeError(w, http.StatusBadRequest, "from_asset_id, to_asset_id, and relation_type are required")
		return
	}
	if req.FromAssetID == req.ToAssetID {
		writeError(w, http.StatusBadRequest, "from_asset_id and to_asset_id must differ")
		return
	}
	if _, err := s.store.SyncCanonicalAssets(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync canonical assets")
		return
	}
	fromAsset, err := canonicalAssetForRelationGorm(r.Context(), s.store.Gorm, req.FromAssetID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	toAsset, err := canonicalAssetForRelationGorm(r.Context(), s.store.Gorm, req.ToAssetID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := relationProjectID(fromAsset, toAsset)
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "at least one asset must belong to a project")
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "asset_relation", ProjectID: projectID}, "update") {
		return
	}
	metadata := req.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["source"] = "manual"
	metadata["created_by"] = userIDOrNil(currentUser(r))
	var item map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var relation GormAssetRelation
		err := tx.Where(&GormAssetRelation{FromAssetID: cleanOptionalID(fmt.Sprint(fromAsset["id"])), ToAssetID: cleanOptionalID(fmt.Sprint(toAsset["id"])), RelationType: req.RelationType}).First(&relation).Error
		if err != nil && !errorsIsRecordNotFound(err) {
			return err
		}
		merged := metadata
		if !errorsIsRecordNotFound(err) {
			merged = mapFromAny(relation.Metadata.Data)
			for key, value := range metadata {
				merged[key] = value
			}
		}
		relation.ProjectID = validNullString(projectID)
		relation.FromAssetID = cleanOptionalID(fmt.Sprint(fromAsset["id"]))
		relation.ToAssetID = cleanOptionalID(fmt.Sprint(toAsset["id"]))
		relation.RelationType = req.RelationType
		relation.Metadata = JSONValue{Data: merged}
		if err := tx.Save(&relation).Error; err != nil {
			return err
		}
		item = assetRelationMap(relation)
		_, err = syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		writeError(w, http.StatusBadRequest, "could not create asset relation")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

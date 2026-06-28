package app

import (
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strings"
)

func normalizeRuleStringList(values []string, fallback []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		clean := strings.ToLower(strings.TrimSpace(value))
		if clean == "" || seen[clean] {
			continue
		}
		seen[clean] = true
		result = append(result, clean)
	}
	if len(result) == 0 {
		return fallback
	}
	return result
}

func (s *Server) listOperationApprovalViews(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "read") {
		return
	}
	user := currentUser(r)
	var views []GormOperationApprovalView
	if err := s.store.Gorm.WithContext(r.Context()).Where(&GormOperationApprovalView{UserID: user.ID}).Order(gormOrderDesc("updated_at")).Order(gormOrderAsc("name")).Limit(200).Find(&views).Error; err != nil {
		writeQueryResult(w, nil, err)
		return
	}
	items := make([]map[string]any, 0, len(views))
	for _, view := range views {
		items = append(items, operationApprovalViewMap(view))
	}
	writeQueryResult(w, items, nil)
}

func (s *Server) createOperationApprovalView(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "read") {
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
	filters, err := sanitizeOperationApprovalViewFilters(req.Filters)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	view := GormOperationApprovalView{UserID: currentUser(r).ID, Name: name, Filters: JSONValue{Data: filters}}
	if err := s.store.Gorm.WithContext(r.Context()).Create(&view).Error; err != nil {
		if isUniqueViolation(err, "operation_approval_views_user_id_name_key") {
			writeError(w, http.StatusBadRequest, "an approval view with this name already exists")
			return
		}
		writeError(w, http.StatusBadRequest, "could not create approval view")
		return
	}
	writeJSON(w, http.StatusCreated, operationApprovalViewMap(view))
}

func (s *Server) updateOperationApprovalView(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "read") {
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
	var filtersPatch map[string]any
	if len(req.Filters) > 0 && string(req.Filters) != "null" {
		var raw map[string]any
		if err := json.Unmarshal(req.Filters, &raw); err != nil {
			writeError(w, http.StatusBadRequest, "invalid filters")
			return
		}
		filters, err := sanitizeOperationApprovalViewFilters(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		filtersPatch = filters
	}
	var view GormOperationApprovalView
	if err := s.store.Gorm.WithContext(r.Context()).Where(&GormOperationApprovalView{GormBase: GormBase{ID: chi.URLParam(r, "id")}, UserID: currentUser(r).ID}).First(&view).Error; errorsIsRecordNotFound(err) {
		writeError(w, http.StatusNotFound, "not found")
		return
	} else if err != nil {
		writeError(w, http.StatusBadRequest, "could not update approval view")
		return
	}
	if name != "" {
		view.Name = name
	}
	if filtersPatch != nil {
		view.Filters = JSONValue{Data: filtersPatch}
	}
	if err := s.store.Gorm.WithContext(r.Context()).Save(&view).Error; err != nil {
		if isUniqueViolation(err, "operation_approval_views_user_id_name_key") {
			writeError(w, http.StatusBadRequest, "an approval view with this name already exists")
			return
		}
		writeError(w, http.StatusBadRequest, "could not update approval view")
		return
	}
	writeJSON(w, http.StatusOK, operationApprovalViewMap(view))
}

func (s *Server) deleteOperationApprovalView(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "read") {
		return
	}
	result := s.store.Gorm.WithContext(r.Context()).Where(&GormOperationApprovalView{GormBase: GormBase{ID: chi.URLParam(r, "id")}, UserID: currentUser(r).ID}).Delete(&GormOperationApprovalView{})
	if result.Error != nil {
		writeError(w, http.StatusInternalServerError, "could not delete approval view")
		return
	}
	if result.RowsAffected == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func operationApprovalViewMap(view GormOperationApprovalView) map[string]any {
	return map[string]any{"id": view.ID, "user_id": view.UserID, "name": view.Name, "filters": mapFromAny(view.Filters.Data), "created_at": view.CreatedAt, "updated_at": view.UpdatedAt}
}

type operationApprovalFilters struct {
	Status       string
	Action       string
	ResourceType string
	Query        string
	RequestedBy  string
	Since        string
	Until        string
}

func operationApprovalFiltersFromRequest(r *http.Request) (operationApprovalFilters, error) {
	q := r.URL.Query()
	filters := operationApprovalFilters{
		Status:       strings.TrimSpace(q.Get("status")),
		Action:       strings.TrimSpace(q.Get("action")),
		ResourceType: strings.TrimSpace(q.Get("resource_type")),
		Query:        strings.TrimSpace(q.Get("q")),
		RequestedBy:  strings.TrimSpace(q.Get("requested_by")),
		Since:        strings.TrimSpace(q.Get("since")),
		Until:        strings.TrimSpace(q.Get("until")),
	}
	if err := validateOptionalRFC3339("since", filters.Since); err != nil {
		return operationApprovalFilters{}, err
	}
	if err := validateOptionalRFC3339("until", filters.Until); err != nil {
		return operationApprovalFilters{}, err
	}
	return filters, nil
}

func sanitizeOperationApprovalViewFilters(input map[string]any) (map[string]any, error) {
	out := map[string]any{}
	status := approvalViewFilterString(input, "status", 40)
	if status != "" {
		switch status {
		case "pending", "approved", "rejected", "expired":
			out["status"] = status
		default:
			return nil, fmt.Errorf("status is invalid")
		}
	}
	for _, item := range []struct {
		key   string
		limit int
	}{
		{key: "action", limit: 120},
		{key: "resource_type", limit: 80},
		{key: "q", limit: 160},
		{key: "requested_by", limit: 160},
	} {
		if value := approvalViewFilterString(input, item.key, item.limit); value != "" {
			out[item.key] = value
		}
	}
	for _, key := range []string{"since", "until"} {
		value := approvalViewFilterString(input, key, 80)
		if value == "" {
			continue
		}
		if err := validateOptionalRFC3339(key, value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, nil
}

func sanitizeAssetGraphViewFilters(input map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for _, item := range []struct {
		key   string
		limit int
	}{
		{key: "project_id", limit: 80},
		{key: "asset_type", limit: 80},
		{key: "q", limit: 160},
		{key: "selected_asset_id", limit: 180},
	} {
		if value := approvalViewFilterString(input, item.key, item.limit); value != "" {
			out[item.key] = value
		}
	}
	return out, nil
}

func approvalViewFilterString(input map[string]any, key string, limit int) string {
	if input == nil {
		return ""
	}
	value, ok := input[key]
	if !ok || value == nil {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	text = strings.TrimSpace(text)
	if len(text) > limit {
		text = text[:limit]
	}
	return text
}

func likeContains(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	b.WriteByte('%')
	for _, r := range value {
		switch r {
		case '\\', '%', '_':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('%')
	return b.String()
}

package app

import (
	"context"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"net/http"
	"strings"
)

func (s *Server) getProjectTemplate(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project_template"}, "read") {
		return
	}
	var template GormProjectTemplate
	if err := s.store.Gorm.WithContext(r.Context()).First(&template, &GormProjectTemplate{GormBase: GormBase{ID: chi.URLParam(r, "id")}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	writeQueryOne(w, projectTemplateMap(template), nil)
}

func (s *Server) previewProjectTemplate(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project_template"}, "read") {
		return
	}
	var template GormProjectTemplate
	if err := s.store.Gorm.WithContext(r.Context()).First(&template, &GormProjectTemplate{GormBase: GormBase{ID: chi.URLParam(r, "id")}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	var req struct {
		Name        string         `json:"name"`
		Slug        string         `json:"slug"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Slug == "" {
		req.Slug = slugify(req.Name)
	}
	req.Slug = slugify(req.Slug)
	writeJSON(w, http.StatusOK, projectTemplatePreview(projectTemplateMap(template), req.Name, req.Slug, req.Description, req.Parameters))
}

func (s *Server) createProjectFromTemplate(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "project_template"}, "create") {
		return
	}
	templateID := chi.URLParam(r, "id")
	var templateModel GormProjectTemplate
	if err := s.store.Gorm.WithContext(r.Context()).Where(&GormProjectTemplate{GormBase: GormBase{ID: templateID}, Status: "active"}).First(&templateModel).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	template := projectTemplateMap(templateModel)
	var req struct {
		Name        string         `json:"name"`
		Slug        string         `json:"slug"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Slug == "" {
		req.Slug = slugify(req.Name)
	}
	req.Slug = slugify(req.Slug)
	if req.Parameters == nil {
		req.Parameters = map[string]any{}
	}
	input := map[string]any{
		"project_template_id": templateID,
		"template_slug":       template["slug"],
		"project_name":        req.Name,
		"project_slug":        req.Slug,
		"description":         req.Description,
		"parameters":          req.Parameters,
	}
	var op map[string]any
	var run map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		op, err = enqueueOperationGorm(r.Context(), tx, "", "", "project.create_from_template", "create project "+req.Name+" from "+fmt.Sprint(template["name"]), input, []string{"template"}, "control-worker")
		if err != nil {
			return err
		}
		run, err = createProjectTemplateRunGorm(r.Context(), tx, op, template, req.Name, req.Slug, req.Description, req.Parameters, currentUser(r).ID)
		if err != nil {
			return err
		}
		_, err = syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		writeError(w, http.StatusBadRequest, "could not create template operation")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"operation": op, "run": run})
}

func createProjectTemplateRunGorm(ctx context.Context, tx *gorm.DB, op, template map[string]any, projectName, projectSlug, description string, parameters map[string]any, actorID string) (map[string]any, error) {
	run := GormProjectTemplateRun{
		OperationRunID:    validNullString(cleanOptionalID(fmt.Sprint(op["id"]))),
		ProjectTemplateID: validNullString(cleanOptionalID(fmt.Sprint(template["id"]))),
		RequestedBy:       validNullString(actorID),
		Status:            "queued",
		ProjectName:       projectName,
		ProjectSlug:       projectSlug,
		Input:             JSONValue{Data: map[string]any{"description": description, "parameters": parameters}},
		Steps:             JSONValue{Data: templateRunSteps(template["steps"])},
		Result:            JSONValue{Data: map[string]any{}},
	}
	if err := tx.WithContext(ctx).Create(&run).Error; err != nil {
		return nil, err
	}
	return projectTemplateRunMap(run), nil
}

func projectTemplateNamesByID(ctx context.Context, db *gorm.DB, runs []GormProjectTemplateRun) (map[string]string, error) {
	ids := make([]string, 0, len(runs))
	seen := map[string]bool{}
	for _, run := range runs {
		id := cleanOptionalID(run.ProjectTemplateID.String)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var templates []GormProjectTemplate
	if err := db.WithContext(ctx).Find(&templates, ids).Error; err != nil {
		return nil, err
	}
	for _, template := range templates {
		out[template.ID] = template.Name
	}
	return out, nil
}

func operationStatusesByID(ctx context.Context, db *gorm.DB, runs []GormProjectTemplateRun) (map[string]string, error) {
	ids := make([]string, 0, len(runs))
	seen := map[string]bool{}
	for _, run := range runs {
		id := cleanOptionalID(run.OperationRunID.String)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var ops []GormOperationRun
	if err := db.WithContext(ctx).Find(&ops, ids).Error; err != nil {
		return nil, err
	}
	for _, op := range ops {
		out[op.ID] = op.Status
	}
	return out, nil
}

func userCanAccessProjectTemplateRun(ctx context.Context, db *gorm.DB, user *User, run GormProjectTemplateRun) (bool, error) {
	if userCanReadAllProjects(user) {
		return true, nil
	}
	if user == nil {
		return false, nil
	}
	if cleanOptionalID(run.RequestedBy.String) == user.ID {
		return true, nil
	}
	projectID := cleanOptionalID(run.ProjectID.String)
	if projectID == "" {
		return false, nil
	}
	var count int64
	if err := db.WithContext(ctx).Model(&GormProjectMember{}).Where(&GormProjectMember{ProjectID: projectID, UserID: user.ID}).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func templateRunSteps(value any) []map[string]any {
	stepsAny, _ := value.([]any)
	if len(stepsAny) == 0 {
		return []map[string]any{
			{"key": "project", "title": "Create project asset", "status": "queued"},
			{"key": "repository", "title": "Create repository metadata", "status": "queued"},
			{"key": "remotes", "title": "Bind repository remotes", "status": "queued"},
			{"key": "repo_sync", "title": "Configure repo sync asset", "status": "queued"},
			{"key": "files", "title": "Plan starter repository files", "status": "queued"},
		}
	}
	steps := make([]map[string]any, 0, len(stepsAny))
	for _, item := range stepsAny {
		step := mapFromAny(item)
		if stringFromMap(step, "status") == "" {
			step["status"] = "queued"
		}
		steps = append(steps, step)
	}
	return steps
}

func projectTemplateMap(template GormProjectTemplate) map[string]any {
	return map[string]any{
		"id":          template.ID,
		"slug":        template.Slug,
		"name":        template.Name,
		"description": template.Description,
		"version":     template.Version,
		"status":      template.Status,
		"defaults":    template.Defaults.Data,
		"steps":       template.Steps.Data,
		"metadata":    template.Metadata.Data,
		"created_at":  template.CreatedAt,
		"updated_at":  template.UpdatedAt,
	}
}

func gormNotFoundAsErrNotFound(err error) error {
	if errorsIsRecordNotFound(err) {
		return ErrNotFound
	}
	return err
}

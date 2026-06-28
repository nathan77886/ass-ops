package app

import (
	"context"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"strings"
	"time"
)

func parseProviderAccountTime(value any) time.Time {
	switch typed := value.(type) {
	case time.Time:
		return typed
	case string:
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	case []byte:
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(string(typed)))
		if err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func maskProviderTokenEnv(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 12 {
		return strings.Repeat("*", len(value))
	}
	return value[:8] + strings.Repeat("*", len(value)-12) + value[len(value)-4:]
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func rawStringFromMap(input map[string]any, key string) string {
	value, ok := input[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprint(value)
	}
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project", ID: projectID, ProjectID: projectID}, "read") {
		return
	}
	project, err := s.projectByIDGorm(r.Context(), projectID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, projectMap(project))
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project", ID: projectID, ProjectID: projectID}, "update") {
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Slug        *string `json:"slug"`
		Description *string `json:"description"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	project, err := s.projectByIDGorm(r.Context(), projectID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		project.Name = strings.TrimSpace(*req.Name)
	}
	if req.Slug != nil && strings.TrimSpace(*req.Slug) != "" {
		project.Slug = strings.TrimSpace(*req.Slug)
	}
	if req.Description != nil {
		project.Description = *req.Description
	}
	if err := s.store.Gorm.WithContext(r.Context()).Save(&project).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	if _, err := s.store.SyncCanonicalAssets(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync project asset")
		return
	}
	writeJSON(w, http.StatusOK, projectMap(project))
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project", ID: projectID, ProjectID: projectID}, "delete") {
		return
	}
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var project GormProject
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&project, &GormProject{GormBase: GormBase{ID: projectID}}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		if err := deleteProjectScopedRowsGorm(r.Context(), tx, projectID); err != nil {
			return err
		}
		if err := tx.Delete(&project).Error; err != nil {
			return err
		}
		_, err := syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": projectID})
}

func deleteProjectScopedRowsGorm(ctx context.Context, tx *gorm.DB, projectID string) error {
	queries := []struct {
		tables []string
		query  string
		args   []any
	}{
		{[]string{"asset_relations", "assets"}, "DELETE FROM asset_relations WHERE project_id = ? OR from_asset_id IN (SELECT id FROM assets WHERE project_id = ? OR (asset_type = ? AND source_table = ? AND source_id = ?)) OR to_asset_id IN (SELECT id FROM assets WHERE project_id = ? OR (asset_type = ? AND source_table = ? AND source_id = ?))", []any{projectID, projectID, "project", "projects", projectID, projectID, "project", "projects", projectID}},
		{[]string{"asset_status_snapshots", "assets"}, "DELETE FROM asset_status_snapshots WHERE asset_id IN (SELECT id FROM assets WHERE project_id = ? OR (asset_type = ? AND source_table = ? AND source_id = ?))", []any{projectID, "project", "projects", projectID}},
		{[]string{"assets"}, "DELETE FROM assets WHERE project_id = ? OR (asset_type = ? AND source_table = ? AND source_id = ?)", []any{projectID, "project", "projects", projectID}},
		{[]string{"agent_tool_tokens", "agent_tasks"}, "DELETE FROM agent_tool_tokens WHERE agent_task_id IN (SELECT id FROM agent_tasks WHERE project_id = ?)", []any{projectID}},
		{[]string{"agent_plans", "agent_tasks"}, "DELETE FROM agent_plans WHERE agent_task_id IN (SELECT id FROM agent_tasks WHERE project_id = ?)", []any{projectID}},
		{[]string{"agent_tool_calls", "agent_tasks", "operation_runs"}, "DELETE FROM agent_tool_calls WHERE project_id = ? OR agent_task_id IN (SELECT id FROM agent_tasks WHERE project_id = ?) OR operation_run_id IN (SELECT id FROM operation_runs WHERE project_id = ?)", []any{projectID, projectID, projectID}},
		{[]string{"agent_context_snapshots", "agent_tasks"}, "DELETE FROM agent_context_snapshots WHERE project_id = ? OR agent_task_id IN (SELECT id FROM agent_tasks WHERE project_id = ?)", []any{projectID, projectID}},
		{[]string{"agent_tasks"}, "DELETE FROM agent_tasks WHERE project_id = ?", []any{projectID}},
		{[]string{"webhook_threshold_configurations"}, "DELETE FROM webhook_threshold_configurations WHERE project_id = ?", []any{projectID}},
		{[]string{"webhook_threshold_decision_audits"}, "DELETE FROM webhook_threshold_decision_audits WHERE project_id = ?", []any{projectID}},
		{[]string{"provider_review_attempts", "operation_approvals"}, "DELETE FROM provider_review_attempts WHERE operation_approval_id IN (SELECT id FROM operation_approvals WHERE project_id = ?)", []any{projectID}},
		{[]string{"operation_approval_decisions", "operation_approvals"}, "DELETE FROM operation_approval_decisions WHERE operation_approval_id IN (SELECT id FROM operation_approvals WHERE project_id = ?)", []any{projectID}},
		{[]string{"operation_approval_delegations", "operation_approvals"}, "DELETE FROM operation_approval_delegations WHERE operation_approval_id IN (SELECT id FROM operation_approvals WHERE project_id = ?)", []any{projectID}},
		{[]string{"operation_approvals"}, "DELETE FROM operation_approvals WHERE project_id = ?", []any{projectID}},
		{[]string{"provider_review_attempts", "project_template_runs"}, "DELETE FROM provider_review_attempts WHERE project_template_run_id IN (SELECT id FROM project_template_runs WHERE project_id = ?)", []any{projectID}},
		{[]string{"project_template_files", "project_template_runs", "project_git_repositories"}, "DELETE FROM project_template_files WHERE project_id = ? OR project_template_run_id IN (SELECT id FROM project_template_runs WHERE project_id = ?) OR project_git_repository_id IN (SELECT id FROM project_git_repositories WHERE project_id = ?)", []any{projectID, projectID, projectID}},
		{[]string{"project_template_runs"}, "DELETE FROM project_template_runs WHERE project_id = ?", []any{projectID}},
		{[]string{"operation_logs", "operation_runs"}, "DELETE FROM operation_logs WHERE operation_run_id IN (SELECT id FROM operation_runs WHERE project_id = ?)", []any{projectID}},
		{[]string{"operation_logs", "worker_jobs", "operation_runs"}, "DELETE FROM operation_logs WHERE worker_job_id IN (SELECT id FROM worker_jobs WHERE operation_run_id IN (SELECT id FROM operation_runs WHERE project_id = ?))", []any{projectID}},
		{[]string{"worker_jobs", "operation_runs"}, "DELETE FROM worker_jobs WHERE operation_run_id IN (SELECT id FROM operation_runs WHERE project_id = ?)", []any{projectID}},
		{[]string{"github_action_artifacts", "github_action_runs", "operation_runs"}, "DELETE FROM github_action_artifacts WHERE github_action_run_id IN (SELECT id FROM github_action_runs WHERE operation_run_id IN (SELECT id FROM operation_runs WHERE project_id = ?))", []any{projectID}},
		{[]string{"github_action_artifacts", "github_action_runs", "git_remotes", "project_git_repositories"}, "DELETE FROM github_action_artifacts WHERE github_action_run_id IN (SELECT id FROM github_action_runs WHERE git_remote_id IN (SELECT gr.id FROM git_remotes gr JOIN project_git_repositories pgr ON pgr.id = gr.project_git_repository_id WHERE pgr.project_id = ?))", []any{projectID}},
		{[]string{"github_action_runs", "operation_runs"}, "DELETE FROM github_action_runs WHERE operation_run_id IN (SELECT id FROM operation_runs WHERE project_id = ?)", []any{projectID}},
		{[]string{"github_action_runs", "git_remotes", "project_git_repositories"}, "DELETE FROM github_action_runs WHERE git_remote_id IN (SELECT gr.id FROM git_remotes gr JOIN project_git_repositories pgr ON pgr.id = gr.project_git_repository_id WHERE pgr.project_id = ?)", []any{projectID}},
		{[]string{"github_repository_labels", "operation_runs"}, "DELETE FROM github_repository_labels WHERE operation_run_id IN (SELECT id FROM operation_runs WHERE project_id = ?)", []any{projectID}},
		{[]string{"github_repository_labels", "git_remotes", "project_git_repositories"}, "DELETE FROM github_repository_labels WHERE git_remote_id IN (SELECT gr.id FROM git_remotes gr JOIN project_git_repositories pgr ON pgr.id = gr.project_git_repository_id WHERE pgr.project_id = ?)", []any{projectID}},
		{[]string{"repo_sync_runs", "operation_runs", "project_git_repositories"}, "DELETE FROM repo_sync_runs WHERE project_id = ? OR operation_run_id IN (SELECT id FROM operation_runs WHERE project_id = ?) OR project_git_repository_id IN (SELECT id FROM project_git_repositories WHERE project_id = ?)", []any{projectID, projectID, projectID}},
		{[]string{"repo_tag_runs", "operation_runs", "project_git_repositories"}, "DELETE FROM repo_tag_runs WHERE project_id = ? OR operation_run_id IN (SELECT id FROM operation_runs WHERE project_id = ?) OR project_git_repository_id IN (SELECT id FROM project_git_repositories WHERE project_id = ?)", []any{projectID, projectID, projectID}},
		{[]string{"ssh_command_runs", "operation_runs"}, "DELETE FROM ssh_command_runs WHERE project_id = ? OR operation_run_id IN (SELECT id FROM operation_runs WHERE project_id = ?)", []any{projectID, projectID}},
		{[]string{"webhook_events", "operation_runs"}, "DELETE FROM webhook_events WHERE project_id = ? OR operation_run_id IN (SELECT id FROM operation_runs WHERE project_id = ?)", []any{projectID, projectID}},
		{[]string{"webhook_events", "webhook_connections", "repo_sync_assets"}, "DELETE FROM webhook_events WHERE webhook_connection_id IN (SELECT id FROM webhook_connections WHERE project_id = ?) OR matched_repo_sync_asset_id IN (SELECT id FROM repo_sync_assets WHERE project_id = ?)", []any{projectID, projectID}},
		{[]string{"operation_runs"}, "DELETE FROM operation_runs WHERE project_id = ?", []any{projectID}},
		{[]string{"rollback_points"}, "DELETE FROM rollback_points WHERE project_id = ?", []any{projectID}},
		{[]string{"deployment_records"}, "DELETE FROM deployment_records WHERE project_id = ?", []any{projectID}},
		{[]string{"argo_apps"}, "DELETE FROM argo_apps WHERE project_id = ?", []any{projectID}},
		{[]string{"deployment_targets"}, "DELETE FROM deployment_targets WHERE project_id = ?", []any{projectID}},
		{[]string{"kubernetes_environments"}, "DELETE FROM kubernetes_environments WHERE project_id = ?", []any{projectID}},
		{[]string{"argo_connections"}, "DELETE FROM argo_connections WHERE project_id = ?", []any{projectID}},
		{[]string{"ssh_machines"}, "DELETE FROM ssh_machines WHERE project_id = ?", []any{projectID}},
		{[]string{"ai_runtimes"}, "DELETE FROM ai_runtimes WHERE project_id = ?", []any{projectID}},
		{[]string{"webhook_connections"}, "DELETE FROM webhook_connections WHERE project_id = ?", []any{projectID}},
		{[]string{"repo_sync_assets"}, "DELETE FROM repo_sync_assets WHERE project_id = ?", []any{projectID}},
		{[]string{"repo_sync_policies", "git_remotes", "project_git_repositories"}, "DELETE FROM repo_sync_policies WHERE git_remote_id IN (SELECT gr.id FROM git_remotes gr JOIN project_git_repositories pgr ON pgr.id = gr.project_git_repository_id WHERE pgr.project_id = ?)", []any{projectID}},
		{[]string{"provider_accounts", "connection_credentials"}, "UPDATE provider_accounts SET credential_id = NULL WHERE credential_id IN (SELECT id FROM connection_credentials WHERE project_id = ?)", []any{projectID}},
		{[]string{"git_remotes", "project_git_repositories"}, "DELETE FROM git_remotes WHERE project_git_repository_id IN (SELECT id FROM project_git_repositories WHERE project_id = ?)", []any{projectID}},
		{[]string{"project_git_repositories"}, "DELETE FROM project_git_repositories WHERE project_id = ?", []any{projectID}},
		{[]string{"project_versions"}, "DELETE FROM project_versions WHERE project_id = ?", []any{projectID}},
		{[]string{"connection_credentials"}, "DELETE FROM connection_credentials WHERE project_id = ?", []any{projectID}},
		{[]string{"project_members"}, "DELETE FROM project_members WHERE project_id = ?", []any{projectID}},
	}
	for _, item := range queries {
		for _, table := range item.tables {
			if !tx.Migrator().HasTable(table) {
				goto nextQuery
			}
		}
		if err := tx.WithContext(ctx).Exec(item.query, item.args...).Error; err != nil {
			return err
		}
	nextQuery:
	}
	return nil
}

func (s *Server) listProjectVersions(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project_version", ProjectID: projectID}, "read") {
		return
	}
	var versions []GormProjectVersion
	if err := s.store.Gorm.WithContext(r.Context()).Where(&GormProjectVersion{ProjectID: projectID}).Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "created_at"}, Desc: true}}}).Find(&versions).Error; err != nil {
		writeQueryResult(w, nil, err)
		return
	}
	writeQueryResult(w, projectVersionMaps(versions), nil)
}

func (s *Server) createProjectVersion(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project_version", ProjectID: projectID}, "create") {
		return
	}
	var req struct {
		Version  string         `json:"version"`
		Source   string         `json:"source"`
		Metadata map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Version = strings.TrimSpace(req.Version)
	if req.Version == "" {
		writeError(w, http.StatusBadRequest, "version is required")
		return
	}
	if len(req.Version) > 200 {
		writeError(w, http.StatusBadRequest, "version must be 200 characters or fewer")
		return
	}
	if strings.TrimSpace(req.Source) == "" {
		req.Source = "manual"
	}
	version := GormProjectVersion{ProjectID: projectID, Version: req.Version, Source: req.Source, Metadata: JSONValue{Data: nonNilMap(req.Metadata)}}
	if err := s.store.Gorm.WithContext(r.Context()).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "project_id"}, {Name: "version"}}, DoUpdates: clause.AssignmentColumns([]string{"source", "metadata"})}).Create(&version).Error; err != nil {
		writeError(w, http.StatusBadRequest, "could not create project version")
		return
	}
	writeJSON(w, http.StatusCreated, projectVersionMap(version))
}

func (s *Server) getProjectVersion(w http.ResponseWriter, r *http.Request) {
	versionID := chi.URLParam(r, "id")
	version, err := projectVersionByIDGorm(r.Context(), s.store.Gorm, versionID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "project_version", ID: versionID, ProjectID: version.ProjectID}, "read") {
		return
	}
	writeQueryOne(w, projectVersionMap(version), nil)
}

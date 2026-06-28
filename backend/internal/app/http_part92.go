package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
)

func cleanAIProviderType(value string) string {
	switch strings.TrimSpace(value) {
	case "openai", "anthropic", "openrouter", "gemini", "groq", "azure_openai", "custom", "local":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func sanitizeAIRuntimes(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, sanitizeAIRuntime(item))
	}
	return out
}

func sanitizeAIRuntime(item map[string]any) map[string]any {
	if item == nil {
		return nil
	}
	out := make(map[string]any, len(item)+1)
	for key, value := range item {
		if key == "config" {
			out[key] = sanitizeAIRuntimeConfig(mapFromAny(value))
			continue
		}
		out[key] = value
	}
	out["api_key_configured"] = boolOnlyFromAny(item["credential_configured"])
	return out
}

func sanitizeAIRuntimeConfig(config map[string]any) map[string]any {
	out := cloneMap(config)
	for key := range out {
		clean := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
		if strings.Contains(clean, "api_key") || strings.Contains(clean, "token") || strings.Contains(clean, "secret") || strings.Contains(clean, "password") {
			delete(out, key)
		}
	}
	return out
}

func aiRuntimeMap(runtime GormAIRuntime, credential *GormConnectionCredential) map[string]any {
	item := map[string]any{
		"id":                 runtime.ID,
		"project_id":         nullableStringValue(runtime.ProjectID),
		"name":               runtime.Name,
		"runtime_type":       runtime.RuntimeType,
		"codex_binary":       runtime.CodexBinary,
		"provider_type":      runtime.ProviderType,
		"api_base_url":       runtime.APIBaseURL,
		"credential_id":      nullableStringValue(runtime.CredentialID),
		"model":              runtime.Model,
		"config":             sanitizeAIRuntimeConfig(mapFromAny(runtime.Config.Data)),
		"status":             runtime.Status,
		"created_at":         runtime.CreatedAt,
		"updated_at":         runtime.UpdatedAt,
		"api_key_configured": false,
	}
	if credential != nil {
		item["credential_name"] = credential.Name
		item["credential_kind"] = credential.Kind
		item["credential_configured"] = credential.SecretCiphertext != ""
		item["api_key_configured"] = credential.SecretCiphertext != ""
	} else {
		item["credential_configured"] = false
	}
	return item
}

func nullableStringValue(value sql.NullString) any {
	if !value.Valid || cleanOptionalText(value.String) == "" {
		return nil
	}
	return value.String
}

func (s *Server) connectionCredentialsForRuntimes(ctx context.Context, runtimes []GormAIRuntime) (map[string]*GormConnectionCredential, error) {
	ids := make([]string, 0, len(runtimes))
	seen := map[string]bool{}
	for _, runtime := range runtimes {
		id := cleanOptionalID(runtime.CredentialID.String)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return map[string]*GormConnectionCredential{}, nil
	}
	var credentials []GormConnectionCredential
	if err := s.store.Gorm.WithContext(ctx).Find(&credentials, ids).Error; err != nil {
		return nil, err
	}
	out := make(map[string]*GormConnectionCredential, len(credentials))
	for i := range credentials {
		credential := credentials[i]
		out[credential.ID] = &credential
	}
	return out, nil
}

func (s *Server) projectByIDGorm(ctx context.Context, projectID string) (GormProject, error) {
	projectID = cleanOptionalID(projectID)
	if projectID == "" {
		return GormProject{}, ErrNotFound
	}
	var project GormProject
	err := s.store.Gorm.WithContext(ctx).Where(map[string]any{"id": projectID}).First(&project).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return project, ErrNotFound
	}
	return project, err
}

func projectVersionMap(version GormProjectVersion) map[string]any {
	return map[string]any{"id": version.ID, "project_id": version.ProjectID, "version": version.Version, "source": version.Source, "metadata": mapFromAny(version.Metadata.Data), "created_at": version.CreatedAt}
}

func projectVersionMaps(versions []GormProjectVersion) []map[string]any {
	items := make([]map[string]any, 0, len(versions))
	for _, version := range versions {
		items = append(items, projectVersionMap(version))
	}
	return items
}

func projectVersionRemoteMapsGorm(ctx context.Context, db *gorm.DB, projectID string) ([]map[string]any, error) {
	var repos []GormProjectGitRepository
	if err := db.WithContext(ctx).Where(&GormProjectGitRepository{ProjectID: projectID}).Find(&repos).Error; err != nil {
		return nil, err
	}
	repoByID := make(map[string]GormProjectGitRepository, len(repos))
	repoIDs := make([]string, 0, len(repos))
	for _, repo := range repos {
		repoByID[repo.ID] = repo
		repoIDs = append(repoIDs, repo.ID)
	}
	if len(repoIDs) == 0 {
		return []map[string]any{}, nil
	}
	var remotes []GormGitRemote
	if err := db.WithContext(ctx).Where(map[string]any{"project_git_repository_id": repoIDs}).Find(&remotes).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(remotes))
	for _, remote := range remotes {
		repo := repoByID[remote.ProjectGitRepositoryID]
		items = append(items, map[string]any{"id": remote.ID, "remote_key": remote.RemoteKey, "provider_type": remote.ProviderType, "latest_sha": remote.LatestSHA, "default_branch": remote.DefaultBranch, "repo_key": repo.RepoKey, "repo_role": repo.RepoRole, "repository_name": repo.Name, "project_id": repo.ProjectID, "repository_id": repo.ID, "repository_status": repo.Status})
	}
	return items, nil
}

func projectVersionTagRunMapsGorm(ctx context.Context, db *gorm.DB, projectID string) ([]map[string]any, error) {
	var runs []GormRepoTagRun
	if err := db.WithContext(ctx).Where(&GormRepoTagRun{ProjectID: validNullString(projectID)}).Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "created_at"}, Desc: true}}}).Limit(500).Find(&runs).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		items = append(items, map[string]any{"id": run.ID, "project_git_repository_id": nullableStringValue(run.ProjectGitRepositoryID), "target_remote_id": nullableStringValue(run.TargetRemoteID), "git_remote_id": run.GitRemoteID, "tag_name": run.TagName, "target_sha": run.TargetSHA, "status": run.Status, "created_at": run.CreatedAt, "finished_at": nullableTimeAny(run.FinishedAt)})
	}
	return items, nil
}

func projectVersionActionRunMapsGorm(ctx context.Context, db *gorm.DB, projectID string) ([]map[string]any, error) {
	remotes, err := projectVersionRemoteMapsGorm(ctx, db, projectID)
	if err != nil {
		return nil, err
	}
	remoteIDs := make([]string, 0, len(remotes))
	for _, remote := range remotes {
		if remoteID := cleanOptionalID(fmt.Sprint(remote["id"])); remoteID != "" {
			remoteIDs = append(remoteIDs, remoteID)
		}
	}
	if len(remoteIDs) == 0 {
		return []map[string]any{}, nil
	}
	var runs []GormGitHubActionRun
	if err := db.WithContext(ctx).Where(map[string]any{"git_remote_id": remoteIDs}).Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "updated_at"}, Desc: true}}}).Limit(500).Find(&runs).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		items = append(items, map[string]any{"id": run.ID, "git_remote_id": run.GitRemoteID, "run_id": run.RunID, "workflow_name": run.WorkflowName, "branch": run.Branch, "commit_sha": run.CommitSHA, "status": run.Status, "conclusion": run.Conclusion, "started_at": nullableTimeAny(run.StartedAt), "updated_at": nullableTimeAny(run.UpdatedAt)})
	}
	return items, nil
}

func projectVersionArgoAppMapsGorm(ctx context.Context, db *gorm.DB, projectID string) ([]map[string]any, error) {
	var apps []GormArgoApp
	if err := db.WithContext(ctx).Where(&GormArgoApp{ProjectID: projectID}).Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "updated_at"}, Desc: true}}}).Limit(500).Find(&apps).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(apps))
	for _, app := range apps {
		items = append(items, map[string]any{"id": app.ID, "name": app.Name, "namespace": app.Namespace, "status": app.Status, "metadata": mapFromAny(app.Metadata.Data), "synced_at": nullableTimeAny(app.SyncedAt), "updated_at": app.UpdatedAt})
	}
	return items, nil
}

func projectVersionArgoConnectionMapsGorm(ctx context.Context, db *gorm.DB, projectID string) ([]map[string]any, error) {
	var connections []GormArgoConnection
	if err := db.WithContext(ctx).Where(&GormArgoConnection{ProjectID: projectID}).Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "updated_at"}, Desc: true}}}).Limit(100).Find(&connections).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(connections))
	for _, connection := range connections {
		items = append(items, map[string]any{"id": connection.ID, "name": connection.Name, "last_sync_status": connection.LastSyncStatus})
	}
	return items, nil
}

func projectMaps(projects []GormProject) []map[string]any {
	items := make([]map[string]any, 0, len(projects))
	for _, project := range projects {
		items = append(items, projectMap(project))
	}
	return items
}

func projectMap(project GormProject) map[string]any {
	return map[string]any{
		"id":          project.ID,
		"name":        project.Name,
		"slug":        project.Slug,
		"description": project.Description,
		"created_at":  project.CreatedAt,
		"updated_at":  project.UpdatedAt,
	}
}

func (s *Server) gitRepositoryByIDGorm(ctx context.Context, repoID string) (GormProjectGitRepository, error) {
	repoID = cleanOptionalID(repoID)
	if repoID == "" {
		return GormProjectGitRepository{}, ErrNotFound
	}
	var repo GormProjectGitRepository
	err := s.store.Gorm.WithContext(ctx).Where(map[string]any{"id": repoID}).First(&repo).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return repo, ErrNotFound
	}
	return repo, err
}

func gitRepositoryMaps(repos []GormProjectGitRepository) []map[string]any {
	items := make([]map[string]any, 0, len(repos))
	for _, repo := range repos {
		items = append(items, gitRepositoryMap(repo))
	}
	return items
}

func gitRepositoryMap(repo GormProjectGitRepository) map[string]any {
	return map[string]any{
		"id":             repo.ID,
		"project_id":     repo.ProjectID,
		"name":           repo.Name,
		"repo_key":       repo.RepoKey,
		"display_name":   repo.DisplayName,
		"repo_role":      repo.RepoRole,
		"status":         repo.Status,
		"description":    repo.Description,
		"default_branch": repo.DefaultBranch,
		"created_at":     repo.CreatedAt,
		"updated_at":     repo.UpdatedAt,
	}
}

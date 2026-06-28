package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (s *Server) repoSyncRunListGorm(ctx context.Context, scope repoSyncRunListScope, filters repoSyncRunFilters, user *User) ([]map[string]any, error) {
	db := s.store.Gorm.WithContext(ctx).Model(&GormRepoSyncRun{})
	if scope.RepoID != "" {
		db = db.Where(gormField("project_git_repository_id", scope.RepoID))
	}
	if scope.RemoteID != "" {
		db = whereAnyFieldEquals(db, scope.RemoteID, "source_remote_id", "target_remote_id", "git_remote_id")
	}
	if filters.AssetID != "" {
		db = db.Where(gormField("repo_sync_asset_id", filters.AssetID))
	}
	if filters.Status != "" {
		db = db.Where(gormField("status", filters.Status))
	}
	if filters.Ref != "" {
		db = db.Where(gormField("ref", filters.Ref))
	}
	if filters.Since != "" {
		since, err := time.Parse(time.RFC3339, filters.Since)
		if err != nil {
			return nil, err
		}
		db = whereFieldGTE(db, "created_at", since)
	}
	if filters.Until != "" {
		until, err := time.Parse(time.RFC3339, filters.Until)
		if err != nil {
			return nil, err
		}
		db = whereFieldLTE(db, "created_at", until)
	}
	var runs []GormRepoSyncRun
	if err := db.Order(gormOrderDesc("created_at")).Limit(250).Find(&runs).Error; err != nil {
		return nil, err
	}
	if scope.RepoID == "" && scope.RemoteID == "" && !userCanReadAllProjects(user) {
		allowed, err := s.projectMembershipSetGorm(ctx, user)
		if err != nil {
			return nil, err
		}
		reposByID, err := s.repoProjectIDMapForRunsGorm(ctx, runs)
		if err != nil {
			return nil, err
		}
		filtered := runs[:0]
		for _, run := range runs {
			projectID := cleanOptionalID(run.ProjectID.String)
			if projectID == "" {
				projectID = reposByID[cleanOptionalID(run.ProjectGitRepositoryID.String)]
			}
			if projectID == "" || allowed[projectID] {
				filtered = append(filtered, run)
			}
		}
		runs = filtered
	}
	if len(runs) > 100 {
		runs = runs[:100]
	}
	items := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		items = append(items, repoSyncRunMap(run))
	}
	return items, nil
}

func (s *Server) repoProjectIDMapForRunsGorm(ctx context.Context, runs []GormRepoSyncRun) (map[string]string, error) {
	repoIDs := make([]string, 0, len(runs))
	seen := map[string]bool{}
	for _, run := range runs {
		repoID := cleanOptionalID(run.ProjectGitRepositoryID.String)
		if repoID == "" || seen[repoID] {
			continue
		}
		seen[repoID] = true
		repoIDs = append(repoIDs, repoID)
	}
	if len(repoIDs) == 0 {
		return map[string]string{}, nil
	}
	var repos []GormProjectGitRepository
	if err := s.store.Gorm.WithContext(ctx).Where(gormField("id", repoIDs)).Find(&repos).Error; err != nil {
		return nil, err
	}
	projectsByRepo := make(map[string]string, len(repos))
	for _, repo := range repos {
		projectsByRepo[repo.ID] = repo.ProjectID
	}
	return projectsByRepo, nil
}

type repoSyncRunFilters struct {
	AssetID string
	Status  string
	Ref     string
	Since   string
	Until   string
}

func repoSyncRunFiltersFromRequest(r *http.Request) (repoSyncRunFilters, error) {
	q := r.URL.Query()
	filters := repoSyncRunFilters{
		AssetID: strings.TrimSpace(q.Get("asset_id")),
		Status:  strings.TrimSpace(q.Get("status")),
		Ref:     strings.TrimSpace(q.Get("ref")),
		Since:   strings.TrimSpace(q.Get("since")),
		Until:   strings.TrimSpace(q.Get("until")),
	}
	if err := validateOptionalRFC3339("since", filters.Since); err != nil {
		return repoSyncRunFilters{}, err
	}
	if err := validateOptionalRFC3339("until", filters.Until); err != nil {
		return repoSyncRunFilters{}, err
	}
	return filters, nil
}

func validateOptionalRFC3339(name, value string) error {
	if value == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return fmt.Errorf("%s must be RFC3339", name)
	}
	return nil
}

func boolQuery(r *http.Request, key string) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get(key))) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

func (s *Server) listRepoTagRuns(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "repo_tag_run"}, "read") {
		return
	}
	repoID := r.URL.Query().Get("repo_id")
	remoteID := r.URL.Query().Get("remote_id")
	user := currentUser(r)
	switch {
	case repoID != "":
		projectID, err := s.projectIDForRepositoryGorm(r.Context(), repoID)
		if err != nil {
			writeQueryOne(w, nil, err)
			return
		}
		if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_tag_run", ProjectID: projectID}, "read") {
			return
		}
		items, err := s.repoTagRunListGorm(r.Context(), repoTagRunListScope{RepoID: repoID}, user)
		writeQueryResult(w, items, err)
	case remoteID != "":
		_, projectID, err := s.gitRemoteWithProjectGorm(r.Context(), remoteID)
		if err != nil {
			writeQueryOne(w, nil, err)
			return
		}
		if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_tag_run", ProjectID: projectID}, "read") {
			return
		}
		items, err := s.repoTagRunListGorm(r.Context(), repoTagRunListScope{RemoteID: remoteID}, user)
		writeQueryResult(w, items, err)
	default:
		items, err := s.repoTagRunListGorm(r.Context(), repoTagRunListScope{}, user)
		writeQueryResult(w, items, err)
	}
}

type repoTagRunListScope struct {
	RepoID   string
	RemoteID string
}

func (s *Server) repoTagRunListGorm(ctx context.Context, scope repoTagRunListScope, user *User) ([]map[string]any, error) {
	db := s.store.Gorm.WithContext(ctx).Model(&GormRepoTagRun{})
	if scope.RepoID != "" {
		db = db.Where(&GormRepoTagRun{ProjectGitRepositoryID: validNullString(scope.RepoID)})
	}
	if scope.RemoteID != "" {
		db = whereAnyFieldEquals(db, scope.RemoteID, "target_remote_id", "git_remote_id")
	}
	var runs []GormRepoTagRun
	if err := db.Order(gormOrderDesc("created_at")).Limit(250).Find(&runs).Error; err != nil {
		return nil, err
	}
	if scope.RepoID == "" && scope.RemoteID == "" && !userCanReadAllProjects(user) {
		allowed, err := s.projectMembershipSetGorm(ctx, user)
		if err != nil {
			return nil, err
		}
		reposByID, err := s.repoProjectIDMapForTagRunsGorm(ctx, runs)
		if err != nil {
			return nil, err
		}
		filtered := runs[:0]
		for _, run := range runs {
			projectID := cleanOptionalID(run.ProjectID.String)
			if projectID == "" {
				projectID = reposByID[cleanOptionalID(run.ProjectGitRepositoryID.String)]
			}
			if projectID == "" || allowed[projectID] {
				filtered = append(filtered, run)
			}
		}
		runs = filtered
	}
	if len(runs) > 100 {
		runs = runs[:100]
	}
	items := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		items = append(items, repoTagRunMap(run))
	}
	if err := s.annotateRepoTagRunOperationsGorm(ctx, items); err != nil {
		return nil, err
	}
	return repoTagRunsWithRemoteRehearsal(items), nil
}

func (s *Server) repoProjectIDMapForTagRunsGorm(ctx context.Context, runs []GormRepoTagRun) (map[string]string, error) {
	repoIDs := make([]string, 0, len(runs))
	seen := map[string]bool{}
	for _, run := range runs {
		repoID := cleanOptionalID(run.ProjectGitRepositoryID.String)
		if repoID == "" || seen[repoID] {
			continue
		}
		seen[repoID] = true
		repoIDs = append(repoIDs, repoID)
	}
	if len(repoIDs) == 0 {
		return map[string]string{}, nil
	}
	var repos []GormProjectGitRepository
	if err := s.store.Gorm.WithContext(ctx).Where(gormField("id", repoIDs)).Find(&repos).Error; err != nil {
		return nil, err
	}
	projectsByRepo := make(map[string]string, len(repos))
	for _, repo := range repos {
		projectsByRepo[repo.ID] = repo.ProjectID
	}
	return projectsByRepo, nil
}

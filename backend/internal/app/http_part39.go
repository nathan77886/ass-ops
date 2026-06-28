package app

import (
	"context"
	"database/sql"
	"github.com/go-chi/chi/v5"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (s *Server) getGitRemote(w http.ResponseWriter, r *http.Request) {
	remote, projectID, err := s.gitRemoteWithProjectGorm(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_remote", ID: chi.URLParam(r, "id"), ProjectID: projectID}, "read") {
		return
	}
	credential, err := s.connectionCredentialByID(r.Context(), remote.CredentialID.String)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, gitRemoteMap(remote, credential, projectID))
}

func (s *Server) updateGitRemote(w http.ResponseWriter, r *http.Request) {
	remote, projectID, err := s.gitRemoteWithProjectGorm(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_remote", ID: chi.URLParam(r, "id"), ProjectID: projectID}, "update") {
		return
	}
	var req gitRemotePatchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	applyGitRemotePatch(&remote, req)
	if err := s.store.Gorm.WithContext(r.Context()).Save(&remote).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	if _, err := s.store.SyncCanonicalAssets(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync git remote asset")
		return
	}
	credential, err := s.connectionCredentialByID(r.Context(), remote.CredentialID.String)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, gitRemoteMap(remote, credential, projectID))
}

func (s *Server) listGitHubActions(w http.ResponseWriter, r *http.Request) {
	remoteID := chi.URLParam(r, "id")
	_, projectID, err := s.gitRemoteWithProjectGorm(r.Context(), remoteID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_remote", ID: remoteID, ProjectID: projectID}, "read") {
		return
	}
	items, err := s.gitHubActionRunMapsGorm(r.Context(), remoteID)
	writeQueryResult(w, items, err)
}

func (s *Server) listGitHubLabels(w http.ResponseWriter, r *http.Request) {
	remoteID := chi.URLParam(r, "id")
	_, projectID, err := s.gitRemoteWithProjectGorm(r.Context(), remoteID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_remote", ID: remoteID, ProjectID: projectID}, "read") {
		return
	}
	items, err := s.gitHubRepositoryLabelMapsGorm(r.Context(), remoteID)
	writeQueryResult(w, items, err)
}

func (s *Server) gitHubActionRunMapsGorm(ctx context.Context, remoteID string) ([]map[string]any, error) {
	var runs []GormGitHubActionRun
	if err := s.store.Gorm.WithContext(ctx).Where(&GormGitHubActionRun{GitRemoteID: remoteID}).Order(gormOrderDesc("created_at")).Limit(50).Find(&runs).Error; err != nil {
		return nil, err
	}
	runIDs := make([]string, 0, len(runs))
	for _, run := range runs {
		runIDs = append(runIDs, run.ID)
	}
	artifactsByRun := map[string][]GormGitHubActionArtifact{}
	if len(runIDs) > 0 {
		var artifacts []GormGitHubActionArtifact
		if err := s.store.Gorm.WithContext(ctx).Where(gormField("github_action_run_id", runIDs)).Order(gormOrderDesc("created_at")).Order(gormOrderAsc("name")).Find(&artifacts).Error; err != nil {
			return nil, err
		}
		for _, artifact := range artifacts {
			artifactsByRun[artifact.GitHubActionRunID] = append(artifactsByRun[artifact.GitHubActionRunID], artifact)
		}
	}
	items := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		artifacts := artifactsByRun[run.ID]
		item := gitHubActionRunMap(run)
		artifactItems := make([]map[string]any, 0, len(artifacts))
		activeCount := 0
		expiredCount := 0
		var totalSize int64
		latestSynced := time.Time{}
		for _, artifact := range artifacts {
			if artifact.Expired {
				expiredCount++
			} else {
				activeCount++
			}
			totalSize += artifact.SizeInBytes
			if artifact.SyncedAt.After(latestSynced) {
				latestSynced = artifact.SyncedAt
			}
			artifactItems = append(artifactItems, gitHubActionArtifactMap(artifact))
		}
		item["artifact_count"] = len(artifacts)
		item["active_artifact_count"] = activeCount
		item["expired_artifact_count"] = expiredCount
		item["total_artifact_size_in_bytes"] = totalSize
		item["latest_artifact_synced_at"] = nullableTimeAny(sql.NullTime{Time: latestSynced, Valid: !latestSynced.IsZero()})
		item["artifacts"] = artifactItems
		items = append(items, item)
	}
	return items, nil
}

func gitHubActionRunMap(run GormGitHubActionRun) map[string]any {
	return map[string]any{
		"id":               run.ID,
		"operation_run_id": nullableStringValue(run.OperationRunID),
		"git_remote_id":    run.GitRemoteID,
		"external_run_id":  run.ExternalRunID,
		"workflow_name":    run.WorkflowName,
		"run_id":           run.RunID,
		"branch":           run.Branch,
		"commit_sha":       run.CommitSHA,
		"status":           run.Status,
		"conclusion":       run.Conclusion,
		"html_url":         run.HTMLURL,
		"metadata":         mapFromAny(run.Metadata.Data),
		"started_at":       nullableTimeAny(run.StartedAt),
		"updated_at":       nullableTimeAny(run.UpdatedAt),
		"synced_at":        nullableTimeAny(run.SyncedAt),
		"created_at":       run.CreatedAt,
	}
}

func gitHubActionArtifactMap(artifact GormGitHubActionArtifact) map[string]any {
	return map[string]any{
		"id":                   artifact.ID,
		"external_artifact_id": artifact.ExternalArtifactID,
		"name":                 artifact.Name,
		"size_in_bytes":        artifact.SizeInBytes,
		"expired":              artifact.Expired,
		"created_at":           nullableTimeAny(artifact.CreatedAt),
		"updated_at":           nullableTimeAny(artifact.UpdatedAt),
		"expires_at":           nullableTimeAny(artifact.ExpiresAt),
		"synced_at":            artifact.SyncedAt,
	}
}

func (s *Server) gitHubRepositoryLabelMapsGorm(ctx context.Context, remoteID string) ([]map[string]any, error) {
	var labels []GormGitHubRepositoryLabel
	if err := s.store.Gorm.WithContext(ctx).Where(&GormGitHubRepositoryLabel{GitRemoteID: remoteID}).Find(&labels).Error; err != nil {
		return nil, err
	}
	sort.Slice(labels, func(i, j int) bool { return strings.ToLower(labels[i].Name) < strings.ToLower(labels[j].Name) })
	if len(labels) > 500 {
		labels = labels[:500]
	}
	items := make([]map[string]any, 0, len(labels))
	for _, label := range labels {
		items = append(items, map[string]any{
			"id":                         label.ID,
			"operation_run_id":           nullableStringValue(label.OperationRunID),
			"git_remote_id":              label.GitRemoteID,
			"external_label_id":          label.ExternalLabelID,
			"node_id":                    label.NodeID,
			"name":                       label.Name,
			"color":                      label.Color,
			"description":                label.Description,
			"is_default":                 label.IsDefault,
			"synced_at":                  label.SyncedAt,
			"created_at":                 label.CreatedAt,
			"provider_response_included": false,
			"credential_included":        false,
			"result_scope":               "github_repository_label_read_model",
		})
	}
	return items, nil
}

func (s *Server) listRepoSyncRuns(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "repo_sync_run"}, "read") {
		return
	}
	repoID := r.URL.Query().Get("repo_id")
	remoteID := r.URL.Query().Get("remote_id")
	filters, err := repoSyncRunFiltersFromRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user := currentUser(r)
	switch {
	case repoID != "":
		projectID, err := s.projectIDForRepositoryGorm(r.Context(), repoID)
		if err != nil {
			writeQueryOne(w, nil, err)
			return
		}
		if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_run", ProjectID: projectID}, "read") {
			return
		}
		items, err := s.repoSyncRunListGorm(r.Context(), repoSyncRunListScope{RepoID: repoID}, filters, user)
		writeQueryResult(w, items, err)
	case remoteID != "":
		_, projectID, err := s.gitRemoteWithProjectGorm(r.Context(), remoteID)
		if err != nil {
			writeQueryOne(w, nil, err)
			return
		}
		if !s.requireProjectPolicy(w, r, PolicyResource{Type: "repo_sync_run", ProjectID: projectID}, "read") {
			return
		}
		items, err := s.repoSyncRunListGorm(r.Context(), repoSyncRunListScope{RemoteID: remoteID}, filters, user)
		writeQueryResult(w, items, err)
	default:
		items, err := s.repoSyncRunListGorm(r.Context(), repoSyncRunListScope{}, filters, user)
		writeQueryResult(w, items, err)
	}
}

type repoSyncRunListScope struct {
	RepoID   string
	RemoteID string
}

package app

import (
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strings"
)

func (s *Server) updateGitRepository(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	repo, err := s.gitRepositoryByIDGorm(r.Context(), repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	projectID := repo.ProjectID
	if cleanOptionalID(projectID) == "" {
		writeQueryOne(w, nil, ErrNotFound)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_repository", ID: repoID, ProjectID: projectID}, "update") {
		return
	}
	var req struct {
		Name          *string `json:"name"`
		RepoKey       *string `json:"repo_key"`
		DisplayName   *string `json:"display_name"`
		RepoRole      *string `json:"repo_role"`
		Status        *string `json:"status"`
		Description   *string `json:"description"`
		DefaultBranch *string `json:"default_branch"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	applyGitRepositoryPatch(&repo, req)
	if err := s.store.Gorm.WithContext(r.Context()).Save(&repo).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	if _, err := s.store.SyncCanonicalAssets(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync git repository asset")
		return
	}
	writeJSON(w, http.StatusOK, gitRepositoryMap(repo))
}

func configRepositoryScaffoldPreview(repo map[string]any, remotes []map[string]any, optionalRows ...[]map[string]any) map[string]any {
	repoRole := strings.ToLower(strings.TrimSpace(stringFromMap(repo, "repo_role")))
	environments := []string{"dev", "test", "prod"}
	files := make([]map[string]any, 0, len(environments)*3+1)
	for _, env := range environments {
		files = append(files,
			map[string]any{
				"path":        "envs/" + env + "/values.yaml",
				"environment": env,
				"purpose":     "environment values entrypoint",
				"required":    true,
			},
			map[string]any{
				"path":        "envs/" + env + "/secrets.example.yaml",
				"environment": env,
				"purpose":     "redacted secret shape only; real secrets stay outside Git",
				"required":    true,
			},
			map[string]any{
				"path":        "envs/" + env + "/README.md",
				"environment": env,
				"purpose":     "operator notes, owners, rollout checks, and rollback hints",
				"required":    true,
			},
		)
	}
	files = append(files, map[string]any{
		"path":        "README.md",
		"environment": "all",
		"purpose":     "config repository overview and branch/review policy",
		"required":    true,
	})
	remoteSummaries := make([]map[string]any, 0, len(remotes))
	for _, remote := range remotes {
		remoteSummaries = append(remoteSummaries, map[string]any{
			"id":               remote["id"],
			"name":             remote["name"],
			"remote_key":       remote["remote_key"],
			"provider_type":    remote["provider_type"],
			"remote_role":      remote["remote_role"],
			"default_branch":   remote["default_branch"],
			"latest_sha":       remote["latest_sha"],
			"last_sync_status": remote["last_sync_status"],
		})
	}
	blockedReasons := []string{}
	if repoRole != "config" {
		blockedReasons = append(blockedReasons, "repository_role_is_not_config")
	}
	if len(remotes) == 0 {
		blockedReasons = append(blockedReasons, "config_remote_missing")
	}
	scaffoldState := "ready"
	if len(blockedReasons) > 0 {
		scaffoldState = "blocked"
	}
	var versions []map[string]any
	if len(optionalRows) > 0 {
		versions = optionalRows[0]
	}
	var workflowOperations []map[string]any
	if len(optionalRows) > 1 {
		workflowOperations = optionalRows[1]
	}
	var refRefreshOperations []map[string]any
	if len(optionalRows) > 2 {
		refRefreshOperations = optionalRows[2]
	}
	pinEvidence := configRepositoryProjectVersionPinEvidence(repo, remoteSummaries, versions)
	workflowEvidence := configRepositoryGitWorkflowAuditEvidence(workflowOperations)
	refRefreshEvidence := configRepositoryRefRefreshEvidence(refRefreshOperations)
	commitPlan := configRepositoryGitCommitPlan(repo, files, remoteSummaries, blockedReasons, pinEvidence, workflowEvidence, refRefreshEvidence)
	return map[string]any{
		"mode":                         "config_repository_scaffold_preview",
		"scaffold_state":               scaffoldState,
		"repository_id":                repo["id"],
		"repository_name":              stringFromMap(repo, "name"),
		"repo_key":                     stringFromMap(repo, "repo_key"),
		"repo_role":                    stringFromMap(repo, "repo_role"),
		"default_branch":               stringFromMap(repo, "default_branch"),
		"environments":                 environments,
		"files":                        files,
		"file_count":                   len(files),
		"remote_count":                 len(remotes),
		"remotes":                      remoteSummaries,
		"required_controls":            []string{"config_remote_review", "branch_policy_review", "human_file_review", "project_version_config_commit_pin"},
		"blocked_reasons":              blockedReasons,
		"git_write_performed":          false,
		"external_call_made":           false,
		"file_content_included":        false,
		"secret_included":              false,
		"project_version_pin_evidence": pinEvidence,
		"git_workflow_audit_evidence":  workflowEvidence,
		"config_ref_refresh_evidence":  refRefreshEvidence,
		"live_commit_validation":       "not_performed",
		"live_commit_validation_state": pinEvidence["live_validation_state"],
		"git_commit_plan":              commitPlan,
		"next_step":                    "Create or sync the config remote, commit the scaffold files through a reviewed Git workflow, then pin config_commit_sha in ProjectVersion.",
		"suppressed_fields":            []string{"file_content", "secret_values", "git_credentials", "provider_token", "author_email"},
	}
}

func configRepositoryRefRefreshEvidence(operations []map[string]any) map[string]any {
	items := make([]map[string]any, 0, len(operations))
	statusCounts := map[string]int{}
	for _, operation := range operations {
		status := strings.ToLower(strings.TrimSpace(fmt.Sprint(operation["status"])))
		if status == "" || status == "<nil>" {
			status = "unknown"
		}
		statusCounts[status]++
		items = append(items, map[string]any{
			"operation_run_id":          cleanOptionalID(fmt.Sprint(operation["id"])),
			"config_remote_id":          cleanOptionalID(fmt.Sprint(operation["git_remote_id"])),
			"status":                    status,
			"created_at":                operation["created_at"],
			"updated_at":                operation["updated_at"],
			"started_at":                operation["started_at"],
			"finished_at":               operation["finished_at"],
			"result_scope":              "sanitized_config_ref_refresh",
			"git_write_performed":       false,
			"git_commit_created":        false,
			"git_push_performed":        false,
			"file_content_included":     false,
			"secret_included":           false,
			"remote_url_included":       false,
			"commit_sha_included":       false,
			"raw_git_output_recorded":   false,
			"credentials_recorded":      false,
			"external_call_made":        status == "running" || status == "completed" || status == "succeeded" || status == "success" || status == "failed" || status == "error",
			"sanitized_error_recorded":  strings.TrimSpace(fmt.Sprint(operation["error"])) != "" && strings.TrimSpace(fmt.Sprint(operation["error"])) != "<nil>",
			"error_message_included":    false,
			"project_version_pin_write": false,
		})
	}
	queued := statusCounts["queued"] + statusCounts["pending"]
	running := statusCounts["running"]
	completed := statusCounts["completed"] + statusCounts["succeeded"] + statusCounts["success"]
	failed := statusCounts["failed"] + statusCounts["error"]
	canceled := statusCounts["canceled"] + statusCounts["cancelled"]
	active := queued + running
	known := queued + running + completed + failed + canceled
	unknown := len(operations) - known
	if unknown < 0 {
		unknown = 0
	}
	state := "not_requested"
	switch {
	case len(operations) == 0:
		state = "not_requested"
	case active > 0:
		state = "waiting_for_worker"
	case failed > 0:
		state = "failed"
	case canceled > 0:
		state = "canceled"
	case unknown > 0:
		state = "unknown"
	default:
		state = "recorded"
	}
	return map[string]any{
		"mode":                                "config_repository_ref_refresh_evidence",
		"refresh_state":                       state,
		"has_ref_refresh_operations":          len(operations) > 0,
		"operation_count":                     len(operations),
		"active_count":                        active,
		"queued_count":                        queued,
		"running_count":                       running,
		"completed_count":                     completed,
		"failed_count":                        failed,
		"canceled_count":                      canceled,
		"unknown_count":                       unknown,
		"git_fetch_performed":                 completed > 0,
		"external_call_made":                  completed > 0 || failed > 0 || running > 0,
		"git_write_performed":                 false,
		"git_commit_created":                  false,
		"git_push_performed":                  false,
		"file_content_included":               false,
		"secret_included":                     false,
		"remote_url_included":                 false,
		"commit_sha_included":                 false,
		"raw_git_output_recorded":             false,
		"credentials_recorded":                false,
		"project_version_pin_written":         false,
		"live_commit_validation_input_source": "git_remotes.latest_sha_after_refresh",
		"items":                               items,
		"suppressed_fields":                   []string{"remote_url", "git_credentials", "provider_token", "authorization_header", "git_output", "commit_sha", "error_message"},
	}
}

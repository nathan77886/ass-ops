package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"sort"
	"strings"
)

func (s *Server) refreshConfigRepositoryRefs(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	projectID, err := s.projectIDForRepositoryGorm(r.Context(), repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	resource := PolicyResource{Type: "git_repository", ID: repoID, ProjectID: projectID}
	if !s.requireProjectPolicy(w, r, resource, "git.refs.refresh") {
		return
	}
	repoModel, err := s.gitRepositoryByIDGorm(r.Context(), repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if strings.ToLower(strings.TrimSpace(repoModel.RepoRole)) != "config" {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":               "repository is not a config repository",
			"blocked_reasons":     []string{"repository_role_is_not_config"},
			"git_write_performed": false,
			"external_call_made":  false,
		})
		return
	}
	remote, err := configRepositoryPrimaryRemoteGorm(r.Context(), s.store.Gorm, repoID)
	if errors.Is(err, ErrNotFound) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":               "config remote is required",
			"blocked_reasons":     []string{"config_remote_missing"},
			"git_write_performed": false,
			"external_call_made":  false,
		})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load config remote")
		return
	}
	var op map[string]any
	idempotent := false
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var locked GormProjectGitRepository
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, &GormProjectGitRepository{GormBase: GormBase{ID: repoID}}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		existing, err := queryConfigRepositoryRefRefreshOperationsGorm(r.Context(), tx, projectID, repoID)
		if err != nil {
			return err
		}
		for _, item := range existing {
			status := strings.TrimSpace(fmt.Sprint(item["status"]))
			if status == "queued" || status == "running" {
				op = item
				idempotent = true
				return nil
			}
		}
		remoteID := cleanOptionalID(fmt.Sprint(remote["id"]))
		input := map[string]any{
			"config_repository_id":   cleanOptionalID(repoID),
			"config_remote_id":       remoteID,
			"remote_id":              remoteID,
			"repo_key":               cleanOptionalText(repoModel.RepoKey),
			"default_branch_present": strings.TrimSpace(fmt.Sprint(remote["default_branch"])) != "" || strings.TrimSpace(repoModel.DefaultBranch) != "",
			"refresh_kind":           "config_ref_validation_refresh",
			"validation_scope":       "config_repository",
			"git_write_performed":    false,
			"file_content_included":  false,
			"secret_included":        false,
		}
		title := "refresh config repository refs " + cleanOptionalText(repoModel.Name)
		var opErr error
		op, opErr = enqueueOperationGorm(r.Context(), tx, projectID, remoteID, "git.refs.refresh", title, input, []string{"git"}, "")
		if opErr != nil {
			return opErr
		}
		_, err = syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not enqueue config ref refresh")
		return
	}
	refreshState := "queued"
	if idempotent {
		refreshState = "already_queued"
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"mode":                         "config_repository_ref_refresh_request_result",
		"repository_id":                repoID,
		"config_remote_id":             remote["id"],
		"operation":                    configRepositoryOperationSummary(op),
		"operation_request_state":      refreshState,
		"refresh_state":                refreshState,
		"idempotent":                   idempotent,
		"git_refs_refresh_enqueued":    true,
		"git_write_performed":          false,
		"git_commit_created":           false,
		"git_push_performed":           false,
		"file_content_included":        false,
		"secret_included":              false,
		"remote_url_recorded":          false,
		"credentials_recorded":         false,
		"raw_git_output_recorded":      false,
		"project_version_pin_written":  false,
		"live_commit_validation_scope": "synced_remote_state_after_worker",
		"suppressed_fields":            []string{"remote_url", "git_credentials", "provider_token", "authorization_header", "git_output", "commit_sha"},
	})
}

func (s *Server) requestConfigRepositoryGitWorkflow(w http.ResponseWriter, r *http.Request) {
	repoID := chi.URLParam(r, "id")
	projectID, err := s.projectIDForRepositoryGorm(r.Context(), repoID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	resource := PolicyResource{Type: "git_repository", ID: repoID, ProjectID: projectID}
	if !s.requireProjectMembershipForPolicy(w, r, resource) {
		return
	}
	decision := NewPolicyChecker().Check(currentUser(r), resource, "config.git_commit")
	if decision.Effect == PolicyDeny {
		writeJSON(w, http.StatusForbidden, decision)
		return
	}
	repo, remotes, versions, preview, err := s.configRepositoryScaffoldPreviewForRequest(r.Context(), repoID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load config scaffold preview")
		return
	}
	commitPlan := mapFromAny(preview["git_commit_plan"])
	if commitPlan["plan_state"] != "planned" {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":               "config git workflow is not ready",
			"blocked_reasons":     commitPlan["blocked_reasons"],
			"git_commit_plan":     commitPlan,
			"scaffold_preview":    preview,
			"external_call_made":  false,
			"git_write_performed": false,
		})
		return
	}
	input := configRepositoryGitWorkflowInput(repo, remotes, preview)
	payload := map[string]any{
		"kind":                  "config_git_commit",
		"project_id":            projectID,
		"repo_id":               repoID,
		"input":                 input,
		"scaffold_file_count":   preview["file_count"],
		"project_version_count": len(versions),
		"file_content_included": false,
		"secret_included":       false,
		"external_call_made":    false,
		"git_write_performed":   false,
	}
	if decision.Effect == PolicyRequireConfirm {
		approval, err := s.createOperationApproval(r.Context(), resource, "config.git_commit", "config git workflow "+fmt.Sprint(repo["name"]), payload, currentUser(r).ID)
		if err != nil {
			if isUniqueViolation(err, "idx_operation_approvals_pending_once") {
				writeError(w, http.StatusConflict, "approval request is already pending")
				return
			}
			writeError(w, http.StatusInternalServerError, "could not create approval request")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"approval": approval, "decision": decision})
		return
	}
	var op map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		op, err = enqueueConfigRepositoryGitWorkflowGorm(r.Context(), tx, projectID, repo, remotes, preview, currentUser(r).ID)
		if err != nil {
			return err
		}
		_, err = syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not enqueue config git workflow")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"operation":                op,
		"git_commit_plan":          commitPlan,
		"scaffold_preview":         preview,
		"operation_request_state":  "queued",
		"operation_request_result": configRepositoryGitWorkflowRequestResult(op),
	})
}

func (s *Server) configRepositoryScaffoldPreviewForRequest(ctx context.Context, repoID, projectID string) (map[string]any, []map[string]any, []map[string]any, map[string]any, error) {
	repoModel, err := s.gitRepositoryByIDGorm(ctx, repoID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	repo := gitRepositoryMap(repoModel)
	remotes, err := queryConfigRepositorySnapshotRemotes(ctx, s.store.Gorm, repoID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	versions, err := queryConfigRepositorySnapshotVersions(ctx, s.store.Gorm, projectID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	workflowOperations, err := queryConfigRepositoryGitWorkflowOperationsGorm(ctx, s.store.Gorm, projectID, repoID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	refRefreshOperations, err := queryConfigRepositoryRefRefreshOperationsGorm(ctx, s.store.Gorm, projectID, repoID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return repo, remotes, versions, configRepositoryScaffoldPreview(repo, remotes, versions, workflowOperations, refRefreshOperations), nil
}

func configRepositoryOperationSummary(op map[string]any) map[string]any {
	return map[string]any{
		"operation_run_id": op["id"],
		"operation_type":   op["operation_type"],
		"status":           op["status"],
		"git_remote_id":    op["git_remote_id"],
		"created_at":       op["created_at"],
		"updated_at":       op["updated_at"],
	}
}

func configRepositoryPrimaryRemoteGorm(ctx context.Context, db *gorm.DB, repoID string) (map[string]any, error) {
	var remotes []GormGitRemote
	if err := db.WithContext(ctx).Where(&GormGitRemote{ProjectGitRepositoryID: repoID}).Find(&remotes).Error; err != nil {
		return nil, err
	}
	if len(remotes) == 0 {
		return nil, ErrNotFound
	}
	sort.SliceStable(remotes, func(i, j int) bool {
		leftRank := configRemoteRoleRank(remotes[i].RemoteRole)
		rightRank := configRemoteRoleRank(remotes[j].RemoteRole)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return remotes[i].CreatedAt.After(remotes[j].CreatedAt)
	})
	remote := remotes[0]
	return map[string]any{"id": remote.ID, "name": remote.Name, "remote_key": remote.RemoteKey, "provider_type": remote.ProviderType, "remote_role": remote.RemoteRole, "default_branch": remote.DefaultBranch, "latest_sha": remote.LatestSHA, "last_sync_status": remote.LastSyncStatus}, nil
}

func configRemoteRoleRank(role string) int {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "target", "origin", "config":
		return 0
	default:
		return 1
	}
}

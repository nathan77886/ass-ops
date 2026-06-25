package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

var errRepositoryRoleNotConfig = errors.New("repository is not a config repository")

type ConfigCommitPinOptions struct {
	ProjectVersionID string
	RepositoryID     string
	RemoteID         string
	DryRun           bool
}

func PinConfigCommit(ctx context.Context, store *Store, opts ConfigCommitPinOptions) (map[string]any, error) {
	versionID := strings.TrimSpace(opts.ProjectVersionID)
	repositoryID := strings.TrimSpace(opts.RepositoryID)
	remoteID := strings.TrimSpace(opts.RemoteID)
	if versionID == "" {
		return nil, fmt.Errorf("project version id is required")
	}
	if repositoryID == "" {
		return nil, fmt.Errorf("repository id is required")
	}
	tx, err := store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting config commit pin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	version, err := queryOne(ctx, tx, `
		SELECT id, project_id, version, source, metadata, created_at
		FROM project_versions
		WHERE id=$1
		FOR UPDATE`, versionID)
	if err != nil {
		return nil, err
	}
	projectID := strings.TrimSpace(fmt.Sprint(version["project_id"]))
	repository, err := queryOne(ctx, tx, `
		SELECT id, project_id, name, repo_key, repo_role, default_branch
		FROM project_git_repositories
		WHERE id=$1 AND project_id=$2`, repositoryID, projectID)
	if err != nil {
		return nil, err
	}
	if strings.ToLower(strings.TrimSpace(fmt.Sprint(repository["repo_role"]))) != "config" {
		return nil, errRepositoryRoleNotConfig
	}
	remote, err := configCommitPinRemote(ctx, tx, repositoryID, remoteID)
	if err != nil {
		return nil, err
	}
	latestSHA := strings.TrimSpace(fmt.Sprint(remote["latest_sha"]))
	if latestSHA == "" || latestSHA == "<nil>" {
		return nil, fmt.Errorf("config remote latest_sha is required before pinning")
	}
	metadata := mapFromAny(version["metadata"])
	nextMetadata, changed, err := pinConfigCommitMetadata(metadata, repository, remote, latestSHA)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"mode":                             "project_version_config_commit_pin",
		"project_version_id":               versionID,
		"repository_id":                    repositoryID,
		"remote_id":                        remote["id"],
		"dry_run":                          opts.DryRun,
		"pin_state":                        map[bool]string{true: "updated", false: "already_pinned"}[changed],
		"metadata_changed":                 changed,
		"project_version_metadata_written": false,
		"config_commit_sha_written":        false,
		"config_commit_sha_present":        true,
		"external_call_made":               false,
		"git_fetch_performed":              false,
		"git_push_performed":               false,
		"provider_api_called":              false,
		"operation_log_written":            false,
		"commit_sha_included":              false,
		"remote_url_included":              false,
		"secret_included":                  false,
		"message":                          "Config commit pin prepared from local synced remote latest_sha; no Git/provider call or operation log write was performed.",
	}
	if opts.DryRun {
		result["pin_state"] = map[bool]string{true: "dry_run_update", false: "already_pinned"}[changed]
		return result, nil
	}
	if !changed {
		result["project_version_metadata_written"] = false
		result["config_commit_sha_written"] = false
		return result, nil
	}
	metadataJSON, err := json.Marshal(nextMetadata)
	if err != nil {
		return nil, fmt.Errorf("encoding project version metadata: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE project_versions
		SET metadata=$2::jsonb
		WHERE id=$1`, versionID, string(metadataJSON)); err != nil {
		return nil, fmt.Errorf("updating project version config commit pin: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing project version config commit pin: %w", err)
	}
	committed = true
	result["project_version_metadata_written"] = true
	result["config_commit_sha_written"] = true
	return result, nil
}

func configCommitPinRemote(ctx context.Context, db sqlx.ExtContext, repositoryID, remoteID string) (map[string]any, error) {
	if remoteID != "" {
		return queryOne(ctx, db, `
			SELECT id, project_git_repository_id, name, remote_key, provider_type, remote_role, latest_sha, last_sync_status
			FROM git_remotes
			WHERE id=$1 AND project_git_repository_id=$2`, remoteID, repositoryID)
	}
	remotes, err := queryMaps(ctx, db, `
		SELECT id, project_git_repository_id, name, remote_key, provider_type, remote_role, latest_sha, last_sync_status
		FROM git_remotes
		WHERE project_git_repository_id=$1
			AND COALESCE(latest_sha, '') <> ''
		ORDER BY updated_at DESC, created_at DESC
		LIMIT 2`, repositoryID)
	if err != nil {
		return nil, err
	}
	if len(remotes) == 0 {
		return nil, fmt.Errorf("no config remote with latest_sha found")
	}
	if len(remotes) > 1 {
		return nil, fmt.Errorf("multiple config remotes have latest_sha; pass --remote-id")
	}
	return remotes[0], nil
}

func pinConfigCommitMetadata(metadata map[string]any, repository, remote map[string]any, configSHA string) (map[string]any, bool, error) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	next := cloneMap(metadata)
	repositoryID := strings.TrimSpace(fmt.Sprint(repository["id"]))
	repoKey := strings.TrimSpace(fmt.Sprint(repository["repo_key"]))
	repositories := mapSliceFromAny(next["repositories"])
	changed := false
	matched := false
	for index, item := range repositories {
		itemRepoID := strings.TrimSpace(fmt.Sprint(item["repository_id"]))
		itemRepoKey := strings.TrimSpace(fmt.Sprint(item["repo_key"]))
		if (itemRepoID == "" || itemRepoID != repositoryID) && (itemRepoKey == "" || itemRepoKey != repoKey) {
			continue
		}
		matched = true
		// cloneMap is shallow; replace the slice item instead of mutating shared metadata in place.
		updated := cloneMap(item)
		changed = applyConfigCommitPin(updated, repository, remote, configSHA) || changed
		repositories[index] = updated
		break
	}
	if !matched {
		item := map[string]any{}
		applyConfigCommitPin(item, repository, remote, configSHA)
		repositories = append(repositories, item)
		changed = true
	}
	next["repositories"] = repositories
	return next, changed, nil
}

func applyConfigCommitPin(item map[string]any, repository, remote map[string]any, configSHA string) bool {
	updates := map[string]any{
		"repository_id":     strings.TrimSpace(fmt.Sprint(repository["id"])),
		"repo_key":          strings.TrimSpace(fmt.Sprint(repository["repo_key"])),
		"repo_role":         strings.TrimSpace(fmt.Sprint(repository["repo_role"])),
		"remote_id":         strings.TrimSpace(fmt.Sprint(remote["id"])),
		"remote_key":        strings.TrimSpace(fmt.Sprint(remote["remote_key"])),
		"remote_role":       strings.TrimSpace(fmt.Sprint(remote["remote_role"])),
		"provider_type":     strings.TrimSpace(fmt.Sprint(remote["provider_type"])),
		"config_commit_sha": strings.TrimSpace(configSHA),
		"validation_status": "local_synced_remote_latest_sha",
	}
	changed := false
	for key, value := range updates {
		if value == "" || value == "<nil>" {
			continue
		}
		if strings.TrimSpace(fmt.Sprint(item[key])) != value {
			item[key] = value
			changed = true
		}
	}
	return changed
}

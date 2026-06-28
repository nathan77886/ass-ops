package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	var result map[string]any
	err := store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var version GormProjectVersion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&version, &GormProjectVersion{ID: versionID}).Error; err != nil {
			if errorsIsRecordNotFound(err) {
				return ErrNotFound
			}
			return err
		}
		projectID := strings.TrimSpace(version.ProjectID)
		var repository GormProjectGitRepository
		if err := tx.Where(&GormProjectGitRepository{GormBase: GormBase{ID: repositoryID}, ProjectID: projectID}).First(&repository).Error; err != nil {
			if errorsIsRecordNotFound(err) {
				return ErrNotFound
			}
			return err
		}
		if strings.ToLower(strings.TrimSpace(repository.RepoRole)) != "config" {
			return errRepositoryRoleNotConfig
		}
		remote, err := configCommitPinRemote(ctx, tx, repositoryID, remoteID)
		if err != nil {
			return err
		}
		latestSHA := strings.TrimSpace(fmt.Sprint(remote["latest_sha"]))
		if latestSHA == "" || latestSHA == "<nil>" {
			return fmt.Errorf("config remote latest_sha is required before pinning")
		}
		metadata := mapFromAny(version.Metadata.Data)
		repositoryMap := configRepositoryPinMap(repository)
		nextMetadata, changed, err := pinConfigCommitMetadata(metadata, repositoryMap, remote, latestSHA)
		if err != nil {
			return err
		}
		result = map[string]any{
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
			return nil
		}
		if !changed {
			result["project_version_metadata_written"] = false
			result["config_commit_sha_written"] = false
			return nil
		}
		version.Metadata = JSONValue{Data: nextMetadata}
		if err := tx.Save(&version).Error; err != nil {
			return fmt.Errorf("updating project version config commit pin: %w", err)
		}
		result["project_version_metadata_written"] = true
		result["config_commit_sha_written"] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func configCommitPinRemote(ctx context.Context, db *gorm.DB, repositoryID, remoteID string) (map[string]any, error) {
	if remoteID != "" {
		var remote GormGitRemote
		if err := db.WithContext(ctx).Where(&GormGitRemote{GormBase: GormBase{ID: remoteID}, ProjectGitRepositoryID: repositoryID}).First(&remote).Error; err != nil {
			if errorsIsRecordNotFound(err) {
				return nil, ErrNotFound
			}
			return nil, err
		}
		return configRemotePinMap(remote), nil
	}
	var rows []GormGitRemote
	if err := db.WithContext(ctx).
		Where(&GormGitRemote{ProjectGitRepositoryID: repositoryID}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "updated_at"}, Desc: true}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}, Desc: true}).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	remotes := make([]map[string]any, 0, 2)
	for _, row := range rows {
		if strings.TrimSpace(row.LatestSHA) == "" {
			continue
		}
		remotes = append(remotes, configRemotePinMap(row))
		if len(remotes) >= 2 {
			break
		}
	}
	if len(remotes) == 0 {
		return nil, fmt.Errorf("no config remote with latest_sha found")
	}
	if len(remotes) > 1 {
		return nil, fmt.Errorf("multiple config remotes have latest_sha; pass --remote-id")
	}
	return remotes[0], nil
}

func configRepositoryPinMap(repository GormProjectGitRepository) map[string]any {
	return map[string]any{"id": repository.ID, "project_id": repository.ProjectID, "name": repository.Name, "repo_key": repository.RepoKey, "repo_role": repository.RepoRole, "default_branch": repository.DefaultBranch}
}

func configRemotePinMap(remote GormGitRemote) map[string]any {
	return map[string]any{"id": remote.ID, "project_git_repository_id": remote.ProjectGitRepositoryID, "name": remote.Name, "remote_key": remote.RemoteKey, "provider_type": remote.ProviderType, "remote_role": remote.RemoteRole, "latest_sha": remote.LatestSHA, "last_sync_status": remote.LastSyncStatus}
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

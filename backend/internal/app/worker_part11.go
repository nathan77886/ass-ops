package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"time"
)

func projectTemplateRunMap(run GormProjectTemplateRun) map[string]any {
	return map[string]any{
		"id":                  run.ID,
		"operation_run_id":    nullableStringValue(run.OperationRunID),
		"project_template_id": nullableStringValue(run.ProjectTemplateID),
		"project_id":          nullableStringValue(run.ProjectID),
		"requested_by":        nullableStringValue(run.RequestedBy),
		"status":              run.Status,
		"project_name":        run.ProjectName,
		"project_slug":        run.ProjectSlug,
		"input":               mapFromAny(run.Input.Data),
		"steps":               mapSliceFromAny(run.Steps.Data),
		"result":              mapFromAny(run.Result.Data),
		"error_message":       run.ErrorMessage,
		"started_at":          nullableTimeAny(run.StartedAt),
		"finished_at":         nullableTimeAny(run.FinishedAt),
		"created_at":          run.CreatedAt,
		"updated_at":          run.UpdatedAt,
	}
}

func projectTemplateFileMap(file GormProjectTemplateFile) map[string]any {
	return map[string]any{
		"id":                        file.ID,
		"project_template_run_id":   nullableStringValue(file.ProjectTemplateRunID),
		"project_template_id":       nullableStringValue(file.ProjectTemplateID),
		"project_id":                nullableStringValue(file.ProjectID),
		"project_git_repository_id": nullableStringValue(file.ProjectGitRepositoryID),
		"path":                      file.Path,
		"kind":                      file.Kind,
		"content":                   file.Content,
		"status":                    file.Status,
		"metadata":                  mapFromAny(file.Metadata.Data),
		"created_at":                file.CreatedAt,
		"updated_at":                file.UpdatedAt,
	}
}

func repoSyncAssetMap(asset GormRepoSyncAsset) map[string]any {
	return map[string]any{
		"id":                        asset.ID,
		"project_id":                asset.ProjectID,
		"project_git_repository_id": asset.ProjectGitRepositoryID,
		"name":                      asset.Name,
		"source_remote_id":          asset.SourceRemoteID,
		"target_remote_id":          asset.TargetRemoteID,
		"trigger_mode":              asset.TriggerMode,
		"sync_mode":                 asset.SyncMode,
		"transport":                 asset.Transport,
		"driver":                    asset.Driver,
		"refs":                      mapFromAny(asset.Refs.Data),
		"enabled":                   asset.Enabled,
		"last_sync_status":          asset.LastSyncStatus,
		"last_sync_run_id":          nullableStringValue(asset.LastSyncRunID),
		"last_synced_at":            nullableTimeAny(asset.LastSyncedAt),
		"metadata":                  mapFromAny(asset.Metadata.Data),
		"archived_at":               nullableTimeAny(asset.ArchivedAt),
		"created_at":                asset.CreatedAt,
		"updated_at":                asset.UpdatedAt,
	}
}

func (w *ControlWorker) markProjectTemplateProvisionRetryCompleted(ctx context.Context, runID string, repo map[string]any, remotes []map[string]any, files []map[string]any, steps []map[string]any, result map[string]any, provision *gitExecutionResult) error {
	return w.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if provision != nil {
			if provisioned, _ := provision.Details["provisioned"].(bool); provisioned {
				if err := markTemplateRepositoryProvisionedGorm(ctx, tx, repo, files, provision); err != nil {
					return err
				}
			}
		}
		updated := tx.Model(&GormProjectTemplateRun{}).
			Where(&GormProjectTemplateRun{GormBase: GormBase{ID: runID}}).
			Updates(map[string]any{"status": "completed", "steps": JSONValue{Data: steps}, "result": JSONValue{Data: result}, "error_message": "", "finished_at": time.Now()})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 0 {
			return ErrNotFound
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for completed project template provision retry: %w", err)
		}
		return nil
	})
}

func (w *ControlWorker) markProjectTemplateProvisionRetryFailed(ctx context.Context, runID string, result map[string]any, cause error) error {
	failedSteps := templateStepsWithProvisionFailure(result["steps"])
	result["steps"] = failedSteps
	errorMessage := truncateProviderError(cause.Error(), providerRunErrorLimit)
	result["error"] = errorMessage
	return w.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updated := tx.Model(&GormProjectTemplateRun{}).
			Where(&GormProjectTemplateRun{GormBase: GormBase{ID: runID}}).
			Updates(map[string]any{"status": "failed", "steps": JSONValue{Data: failedSteps}, "result": JSONValue{Data: result}, "error_message": errorMessage, "finished_at": time.Now()})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 0 {
			return ErrNotFound
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for failed project template provision retry: %w", err)
		}
		return nil
	})
}

func templateRunResult(project, repo map[string]any, remotes []map[string]any, syncAsset map[string]any, files []map[string]any, steps []map[string]any) map[string]any {
	result := map[string]any{
		"project_id":   project["id"],
		"project_slug": project["slug"],
		"project_name": project["name"],
		"steps":        steps,
	}
	if repo != nil {
		result["repository_id"] = repo["id"]
		result["repository_key"] = repo["repo_key"]
	}
	if len(remotes) > 0 {
		result["remote_ids"] = mapRemoteIDs(remotes)
		result["remotes"] = remotes
	}
	if syncAsset != nil {
		result["repo_sync_asset_id"] = syncAsset["id"]
	}
	if len(files) > 0 {
		result["template_file_ids"] = mapTemplateFileIDs(files)
		result["template_files"] = templateFileSummaries(files)
	}
	return result
}

func createProjectFromTemplateGorm(ctx context.Context, store *Store, tx *gorm.DB, opID string) (map[string]any, map[string]any, []map[string]any, map[string]any, []map[string]any, []map[string]any, error) {
	var runModel GormProjectTemplateRun
	if err := tx.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(&GormProjectTemplateRun{OperationRunID: validNullString(opID)}).
		First(&runModel).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return nil, nil, nil, nil, nil, nil, ErrNotFound
		}
		return nil, nil, nil, nil, nil, nil, err
	}
	run := projectTemplateRunMap(runModel)
	var template GormProjectTemplate
	if runModel.ProjectTemplateID.Valid {
		if err := tx.WithContext(ctx).First(&template, &GormProjectTemplate{GormBase: GormBase{ID: runModel.ProjectTemplateID.String}}).Error; err != nil && !errorsIsRecordNotFound(err) {
			return nil, nil, nil, nil, nil, nil, err
		}
	}
	run["template_defaults"] = mapFromAny(template.Defaults.Data)
	run["template_slug"] = template.Slug
	projectSlug := strings.TrimSpace(runModel.ProjectSlug)
	projectName := strings.TrimSpace(runModel.ProjectName)
	if projectName == "" || projectSlug == "" {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("template run is missing project name or slug")
	}
	input := mapFromAny(runModel.Input.Data)
	parameters := mapFromAny(input["parameters"])
	defaults := mapFromAny(template.Defaults.Data)
	projectModel := GormProject{Name: projectName, Slug: projectSlug, Description: stringFromMap(input, "description")}
	if err := tx.WithContext(ctx).Create(&projectModel).Error; err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	project := projectMap(projectModel)
	if requestedBy := cleanOptionalID(runModel.RequestedBy.String); requestedBy != "" {
		member := GormProjectMember{ProjectID: projectModel.ID, UserID: requestedBy, Role: "owner"}
		if err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&member).Error; err != nil {
			return nil, nil, nil, nil, nil, nil, err
		}
	}
	repo, err := createTemplateRepositoryGorm(ctx, tx, project, defaults, parameters)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	remotes, err := createTemplateRemotesGorm(ctx, store, tx, repo, defaults, parameters)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	syncAsset, err := createTemplateRepoSyncAssetGorm(ctx, tx, opID, project, repo, remotes, defaults, parameters)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	files, err := createTemplateFilesGorm(ctx, tx, run, project, repo, defaults, parameters)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	steps := completeTemplateSteps(run["steps"], project, repo, remotes, syncAsset, files)
	if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("syncing canonical assets for project template creation: %w", err)
	}
	return project, repo, remotes, syncAsset, files, steps, nil
}

func createTemplateRepositoryGorm(ctx context.Context, tx *gorm.DB, project map[string]any, defaults, parameters map[string]any) (map[string]any, error) {
	repoDefaults := mapFromAny(defaults["repository"])
	repoParams := mapFromAny(parameters["repository"])
	projectSlug := strings.TrimSpace(fmt.Sprint(project["slug"]))
	projectName := strings.TrimSpace(fmt.Sprint(project["name"]))
	repo := GormProjectGitRepository{
		ProjectID:     cleanOptionalID(fmt.Sprint(project["id"])),
		Name:          firstNonEmptyString(stringFromMap(repoParams, "name"), templateNameWithSuffix(projectSlug, stringFromMap(repoDefaults, "name_suffix"), "service")),
		RepoKey:       firstNonEmptyString(stringFromMap(repoParams, "repo_key"), templateNameWithSuffix(projectSlug, stringFromMap(repoDefaults, "repo_key_suffix"), "service")),
		DisplayName:   firstNonEmptyString(stringFromMap(repoParams, "display_name"), templateDisplayName(projectName, stringFromMap(repoDefaults, "display_name_suffix"), "Service")),
		RepoRole:      firstNonEmptyString(stringFromMap(repoParams, "repo_role"), stringFromMap(defaults, "repo_role"), "code"),
		Status:        "planned",
		Description:   "Created from project template; repository provider binding is pending.",
		DefaultBranch: firstNonEmptyString(stringFromMap(repoParams, "default_branch"), stringFromMap(defaults, "default_branch"), "main"),
	}
	if err := tx.WithContext(ctx).Create(&repo).Error; err != nil {
		return nil, err
	}
	return gitRepositoryMap(repo), nil
}

func createTemplateRemotesGorm(ctx context.Context, store *Store, tx *gorm.DB, repo, defaults, parameters map[string]any) ([]map[string]any, error) {
	remoteItems := templateRemoteItems(defaults, parameters)
	remotes := make([]map[string]any, 0, len(remoteItems))
	for _, item := range remoteItems {
		remote, err := createTemplateRemoteGorm(ctx, store, tx, repo, item)
		if err != nil {
			return nil, err
		}
		remotes = append(remotes, remote)
	}
	return remotes, nil
}

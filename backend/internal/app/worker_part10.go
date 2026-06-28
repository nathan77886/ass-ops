package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"strings"
	"time"
)

func (w *ControlWorker) prepareProjectTemplateRun(ctx context.Context, opID string) (map[string]any, error) {
	var run GormProjectTemplateRun
	if err := w.store.Gorm.WithContext(ctx).Where(&GormProjectTemplateRun{OperationRunID: validNullString(opID)}).First(&run).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	templateSlug := ""
	templateName := ""
	if run.ProjectTemplateID.Valid {
		var template GormProjectTemplate
		if err := w.store.Gorm.WithContext(ctx).First(&template, &GormProjectTemplate{GormBase: GormBase{ID: run.ProjectTemplateID.String}}).Error; err == nil {
			templateSlug = template.Slug
			templateName = template.Name
		} else if !errorsIsRecordNotFound(err) {
			return nil, err
		}
	}
	projectSlug := strings.TrimSpace(run.ProjectSlug)
	projectName := strings.TrimSpace(run.ProjectName)
	if projectName == "" || projectSlug == "" {
		return nil, fmt.Errorf("template run is missing project name or slug")
	}
	return map[string]any{
		"project_slug":  projectSlug,
		"project_name":  projectName,
		"template_id":   nullableStringValue(run.ProjectTemplateID),
		"template_slug": templateSlug,
		"template_name": templateName,
		"steps":         run.Steps,
	}, nil
}

func (w *ControlWorker) executeProjectTemplateRun(ctx context.Context, opID string) (map[string]any, error) {
	var project map[string]any
	var repo map[string]any
	var remotes []map[string]any
	var syncAsset map[string]any
	var files []map[string]any
	var steps []map[string]any
	err := w.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		project, repo, remotes, syncAsset, files, steps, err = createProjectFromTemplateGorm(ctx, w.store, tx, opID)
		if err != nil {
			return err
		}
		result := templateRunResult(project, repo, remotes, syncAsset, files, steps)
		result["repository_provisioned"] = false
		updated := tx.Model(&GormProjectTemplateRun{}).
			Where(&GormProjectTemplateRun{OperationRunID: validNullString(opID)}).
			Updates(map[string]any{
				"status":     "provisioning",
				"project_id": validNullString(cleanOptionalID(fmt.Sprint(project["id"]))),
				"steps":      JSONValue{Data: steps},
				"result":     JSONValue{Data: result},
			})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result := templateRunResult(project, repo, remotes, syncAsset, files, steps)
	result["repository_provisioned"] = false

	executor := NewGitExecutor("")
	executor.LocalBareBaseDirs = w.cfg.LocalBareBaseDirs
	provision, err := executor.ProvisionTemplateRepository(ctx, repo, remotes, files)
	mergeGitExecutionResult(result, provision)
	if provision != nil && provision.Details != nil {
		provisioned, _ := provision.Details["provisioned"].(bool)
		result["repository_provisioned"] = provisioned
		if reason, _ := provision.Details["reason"].(string); reason != "" {
			result["repository_provision_reason"] = reason
		}
		if provisioned {
			repo["status"] = "active"
			for _, file := range files {
				file["status"] = "pushed"
			}
			steps = completeTemplateStepsWithRepositoryProvision(steps, provision)
			result = templateRunResult(project, repo, remotes, syncAsset, files, steps)
			result["repository_provisioned"] = true
			mergeGitExecutionResult(result, provision)
		}
	}
	if err != nil {
		_ = w.markProjectTemplateRunFailed(ctx, opID, result, err)
		result["_template_run_failure_recorded"] = true
		return result, err
	}
	if err := w.markProjectTemplateRunCompletedWithRetry(ctx, opID, repo, remotes, files, steps, result, provision); err != nil {
		if provisioned, _ := result["repository_provisioned"].(bool); provisioned {
			result["_template_run_completion_pending"] = true
		}
		return result, err
	}
	result["_template_run_recorded"] = true
	return result, nil
}

func (w *ControlWorker) markProjectTemplateRunCompletedWithRetry(ctx context.Context, opID string, repo map[string]any, remotes []map[string]any, files []map[string]any, steps []map[string]any, result map[string]any, provision *gitExecutionResult) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 200 * time.Millisecond):
			}
		}
		if err := w.markProjectTemplateRunCompleted(ctx, opID, repo, remotes, files, steps, result, provision); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

func (w *ControlWorker) executeProjectTemplateProvisionRetry(ctx context.Context, opID string) (map[string]any, error) {
	runID, err := projectTemplateRunIDForRetryOperation(ctx, w.store.Gorm, opID)
	if err != nil {
		return nil, err
	}
	run, project, repo, remotes, syncAsset, files, steps, err := loadProjectTemplateProvisionResources(ctx, w.store.Gorm, runID)
	if err != nil {
		return nil, err
	}
	result := templateRunResult(project, repo, remotes, syncAsset, files, steps)
	result["repository_provisioned"] = false
	result["provision_retry"] = map[string]any{
		"operation_run_id": opID,
		"run_id":           runID,
	}
	result["retry_of_operation_run_id"] = cleanOptionalID(fmt.Sprint(run["operation_run_id"]))

	executor := NewGitExecutor("")
	executor.LocalBareBaseDirs = w.cfg.LocalBareBaseDirs
	provision, err := executor.ProvisionTemplateRepository(ctx, repo, remotes, files)
	mergeGitExecutionResult(result, provision)
	if provision != nil && provision.Details != nil {
		provisioned, _ := provision.Details["provisioned"].(bool)
		result["repository_provisioned"] = provisioned
		if reason, _ := provision.Details["reason"].(string); reason != "" {
			result["repository_provision_reason"] = reason
		}
		if provisioned {
			repo["status"] = "active"
			for _, file := range files {
				file["status"] = "pushed"
			}
			steps = completeTemplateStepsWithRepositoryProvision(steps, provision)
			result = templateRunResult(project, repo, remotes, syncAsset, files, steps)
			result["repository_provisioned"] = true
			result["provision_retry"] = map[string]any{
				"operation_run_id": opID,
				"run_id":           runID,
				"completed_at":     time.Now().UTC().Format(time.RFC3339),
			}
			result["retry_of_operation_run_id"] = cleanOptionalID(fmt.Sprint(run["operation_run_id"]))
			mergeGitExecutionResult(result, provision)
		}
	}
	if err != nil {
		_ = w.markProjectTemplateProvisionRetryFailed(ctx, runID, result, err)
		result["_template_retry_recorded"] = true
		return result, err
	}
	if err := w.markProjectTemplateProvisionRetryCompleted(ctx, runID, repo, remotes, files, steps, result, provision); err != nil {
		return result, err
	}
	result["_template_retry_recorded"] = true
	return result, nil
}

func projectTemplateRunIDForRetryOperation(ctx context.Context, db *gorm.DB, opID string) (string, error) {
	op, err := operationRunMapByID(ctx, db, opID)
	if err != nil {
		return "", err
	}
	input := mapFromAny(op["input"])
	runID := cleanOptionalID(fmt.Sprint(input["project_template_run_id"]))
	if runID == "" {
		return "", fmt.Errorf("template provision retry is missing project_template_run_id")
	}
	return runID, nil
}

func loadProjectTemplateProvisionResources(ctx context.Context, db *gorm.DB, runID string) (map[string]any, map[string]any, map[string]any, []map[string]any, map[string]any, []map[string]any, []map[string]any, error) {
	var runModel GormProjectTemplateRun
	if err := db.WithContext(ctx).First(&runModel, &GormProjectTemplateRun{GormBase: GormBase{ID: runID}}).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return nil, nil, nil, nil, nil, nil, nil, ErrNotFound
		}
		return nil, nil, nil, nil, nil, nil, nil, err
	}
	run := projectTemplateRunMap(runModel)
	projectID := cleanOptionalID(fmt.Sprint(run["project_id"]))
	if projectID == "" {
		return nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("template run has no project to reconcile")
	}
	var projectModel GormProject
	if err := db.WithContext(ctx).First(&projectModel, &GormProject{GormBase: GormBase{ID: projectID}}).Error; err != nil {
		return nil, nil, nil, nil, nil, nil, nil, err
	}
	project := projectMap(projectModel)
	result := mapFromAny(run["result"])
	repoID := cleanOptionalID(fmt.Sprint(result["repository_id"]))
	var repoModel GormProjectGitRepository
	if repoID != "" {
		err := db.WithContext(ctx).First(&repoModel, &GormProjectGitRepository{GormBase: GormBase{ID: repoID}}).Error
		if err != nil {
			return nil, nil, nil, nil, nil, nil, nil, err
		}
	} else {
		var repos []GormProjectGitRepository
		if err := db.WithContext(ctx).Where(&GormProjectGitRepository{ProjectID: projectID}).Order(gormOrderDesc("created_at")).Limit(1).Find(&repos).Error; err != nil {
			return nil, nil, nil, nil, nil, nil, nil, err
		}
		if len(repos) == 0 {
			return nil, nil, nil, nil, nil, nil, nil, ErrNotFound
		}
		repoModel = repos[0]
	}
	repo := gitRepositoryMap(repoModel)
	var remoteModels []GormGitRemote
	if err := db.WithContext(ctx).Where(&GormGitRemote{ProjectGitRepositoryID: repoModel.ID}).Order(gormOrderAsc("created_at")).Order(gormOrderAsc("name")).Find(&remoteModels).Error; err != nil {
		return nil, nil, nil, nil, nil, nil, nil, err
	}
	remotes := make([]map[string]any, 0, len(remoteModels))
	for _, remote := range remoteModels {
		remotes = append(remotes, gitRemoteMap(remote, nil, ""))
	}
	var fileModels []GormProjectTemplateFile
	if err := db.WithContext(ctx).Where(&GormProjectTemplateFile{ProjectTemplateRunID: validNullString(runID)}).Order(gormOrderAsc("created_at")).Order(gormOrderAsc("path")).Find(&fileModels).Error; err != nil {
		return nil, nil, nil, nil, nil, nil, nil, err
	}
	files := make([]map[string]any, 0, len(fileModels))
	for _, file := range fileModels {
		files = append(files, projectTemplateFileMap(file))
	}
	var syncAsset map[string]any
	syncAssetID := cleanOptionalID(fmt.Sprint(result["repo_sync_asset_id"]))
	if syncAssetID != "" {
		var asset GormRepoSyncAsset
		if err := db.WithContext(ctx).First(&asset, &GormRepoSyncAsset{GormBase: GormBase{ID: syncAssetID}}).Error; err != nil {
			if errorsIsRecordNotFound(err) {
				syncAsset = nil
			} else {
				return nil, nil, nil, nil, nil, nil, nil, err
			}
		} else {
			syncAsset = repoSyncAssetMap(asset)
		}
	}
	steps := templateStepsWithProvisionRetry(run["steps"])
	return run, project, repo, remotes, syncAsset, files, steps, nil
}

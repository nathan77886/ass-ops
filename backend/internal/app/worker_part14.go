package app

import (
	"context"
	"database/sql"
	"fmt"
	"gorm.io/gorm"
	"strings"
	"time"
)

func nullableIntFromMap(result map[string]any, key string) any {
	switch value := result[key].(type) {
	case int:
		return value
	case int64:
		return value
	case float64:
		return int(value)
	default:
		return nil
	}
}

func validNullTimePtr(value *time.Time) sql.NullTime {
	if value == nil || value.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *value, Valid: true}
}

func mergeGitHubActionsResult(result map[string]any, syncResult *GitHubActionsSyncResult) {
	if syncResult == nil {
		return
	}
	result["_github_actions_result"] = syncResult
	result["remote_id"] = syncResult.RemoteID
	result["repository"] = syncResult.Owner + "/" + syncResult.Repo
	result["count"] = len(syncResult.Runs)
}

func mergeGitHubRepositoryLabelsResult(result map[string]any, syncResult *GitHubRepositoryLabelsSyncResult) {
	if syncResult == nil {
		return
	}
	result["_github_repository_labels_result"] = syncResult
	result["remote_id"] = syncResult.RemoteID
	result["repository"] = syncResult.Owner + "/" + syncResult.Repo
	result["count"] = len(syncResult.Labels)
	result["provider_response_included"] = false
	result["credential_included"] = false
}

func mergeSSHExecutionResult(result map[string]any, execution *SSHExecutionResult) {
	if execution == nil {
		return
	}
	result["stdout"] = execution.Stdout
	result["stderr"] = execution.Stderr
	result["exit_code"] = execution.ExitCode
	result["details"] = execution.Details
}

func mergeArgoSyncResult(result map[string]any, syncResult *ArgoSyncResult) {
	if syncResult == nil {
		return
	}
	result["_argo_sync_result"] = syncResult
	result["project_id"] = syncResult.ProjectID
	result["connection_id"] = syncResult.ConnectionID
	result["server_url"] = syncResult.ServerURL
	result["count"] = len(syncResult.Apps)
}

func (w *ControlWorker) recordGitHubActionsAdapterRun(ctx context.Context, tx *gorm.DB, opID string, result map[string]any) error {
	syncResult, ok := result["_github_actions_result"].(*GitHubActionsSyncResult)
	delete(result, "_github_actions_result")
	if !ok || syncResult == nil || syncResult.RemoteID == "" {
		return nil
	}
	result["remote_id"] = syncResult.RemoteID
	result["repository"] = syncResult.Owner + "/" + syncResult.Repo
	result["count"] = len(syncResult.Runs)
	artifactCount := 0
	if err := tx.WithContext(ctx).Where(&GormGitHubActionRun{GitRemoteID: syncResult.RemoteID}).Delete(&GormGitHubActionRun{}).Error; err != nil {
		return err
	}
	for _, run := range syncResult.Runs {
		actionRun := GormGitHubActionRun{
			OperationRunID: validNullString(opID),
			GitRemoteID:    syncResult.RemoteID,
			ExternalRunID:  run.ExternalRunID,
			WorkflowName:   run.WorkflowName,
			RunID:          run.RunID,
			Branch:         run.Branch,
			CommitSHA:      run.CommitSHA,
			Status:         run.Status,
			Conclusion:     run.Conclusion,
			HTMLURL:        run.HTMLURL,
			Metadata:       JSONValue{Data: run.Metadata},
			StartedAt:      validNullTimePtr(run.StartedAt),
			UpdatedAt:      validNullTimePtr(run.UpdatedAt),
			SyncedAt:       validNullTime(time.Now()),
		}
		if err := tx.WithContext(ctx).Create(&actionRun).Error; err != nil {
			return err
		}
		for _, artifact := range run.Artifacts {
			artifactModel := GormGitHubActionArtifact{
				GitRemoteID:        syncResult.RemoteID,
				GitHubActionRunID:  actionRun.ID,
				ExternalArtifactID: artifact.ExternalArtifactID,
				Name:               artifact.Name,
				SizeInBytes:        artifact.SizeInBytes,
				Expired:            artifact.Expired,
				Metadata:           JSONValue{Data: artifact.Metadata},
				CreatedAt:          validNullTimePtr(artifact.CreatedAt),
				UpdatedAt:          validNullTimePtr(artifact.UpdatedAt),
				ExpiresAt:          validNullTimePtr(artifact.ExpiresAt),
				SyncedAt:           time.Now(),
			}
			if err := tx.WithContext(ctx).Create(&artifactModel).Error; err != nil {
				return err
			}
			artifactCount++
		}
	}
	result["artifact_count"] = artifactCount
	return nil
}

func (w *ControlWorker) recordGitHubRepositoryLabelsAdapterRun(ctx context.Context, tx *gorm.DB, opID string, result map[string]any) error {
	syncResult, ok := result["_github_repository_labels_result"].(*GitHubRepositoryLabelsSyncResult)
	delete(result, "_github_repository_labels_result")
	if !ok || syncResult == nil || syncResult.RemoteID == "" {
		return nil
	}
	result["remote_id"] = syncResult.RemoteID
	result["repository"] = syncResult.Owner + "/" + syncResult.Repo
	result["count"] = len(syncResult.Labels)
	result["provider_response_included"] = false
	result["credential_included"] = false
	if err := tx.WithContext(ctx).Where(&GormGitHubRepositoryLabel{GitRemoteID: syncResult.RemoteID}).Delete(&GormGitHubRepositoryLabel{}).Error; err != nil {
		return err
	}
	for _, label := range syncResult.Labels {
		model := GormGitHubRepositoryLabel{
			OperationRunID:  validNullString(opID),
			GitRemoteID:     syncResult.RemoteID,
			ExternalLabelID: label.ExternalLabelID,
			NodeID:          label.NodeID,
			Name:            label.Name,
			Color:           label.Color,
			Description:     label.Description,
			IsDefault:       label.IsDefault,
			SyncedAt:        time.Now(),
		}
		if err := tx.WithContext(ctx).Create(&model).Error; err != nil {
			return err
		}
	}
	return nil
}

func markTemplateRepositoryProvisionedGorm(ctx context.Context, tx *gorm.DB, repo map[string]any, files []map[string]any, provision *gitExecutionResult) error {
	if provision == nil || provision.Details == nil {
		return nil
	}
	sha := provision.AfterSHA
	remoteID := strings.TrimSpace(fmt.Sprint(provision.Details["remote_id"]))
	if err := tx.WithContext(ctx).Model(&GormProjectGitRepository{}).
		Where(&GormProjectGitRepository{GormBase: GormBase{ID: cleanOptionalID(fmt.Sprint(repo["id"]))}}).
		Updates(map[string]any{"status": "active", "description": "Created from project template and initialized in provider repository."}).Error; err != nil {
		return err
	}
	if remoteID != "" && remoteID != "<nil>" {
		var remote GormGitRemote
		if err := tx.WithContext(ctx).First(&remote, &GormGitRemote{GormBase: GormBase{ID: remoteID}}).Error; err != nil {
			return err
		}
		remote.LatestSHA = sha
		remote.LastSyncStatus = "completed"
		if remoteURL := cleanOptionalText(fmt.Sprint(provision.Details["remote_url"])); remoteURL != "" {
			remote.RemoteURL = remoteURL
		}
		if webURL := cleanOptionalText(fmt.Sprint(provision.Details["web_url"])); webURL != "" {
			remote.WebURL = webURL
		}
		metadata := mapFromAny(remote.Metadata.Data)
		metadata["template_placeholder"] = false
		metadata["repository_provisioned"] = true
		metadata["provider_type"] = cleanOptionalText(fmt.Sprint(provision.Details["provider_type"]))
		metadata["repository_name"] = cleanOptionalText(fmt.Sprint(provision.Details["repository_name"]))
		remote.Metadata = JSONValue{Data: metadata}
		if err := tx.WithContext(ctx).Save(&remote).Error; err != nil {
			return err
		}
	}
	if len(files) == 0 {
		return nil
	}
	ids := mapTemplateFileIDs(files)
	if len(ids) == 0 {
		return nil
	}
	var models []GormProjectTemplateFile
	if err := tx.WithContext(ctx).Find(&models, ids).Error; err != nil {
		return err
	}
	for i := range models {
		models[i].Status = "pushed"
		metadata := mapFromAny(models[i].Metadata.Data)
		metadata["repository_provisioned"] = true
		metadata["commit_sha"] = sha
		models[i].Metadata = JSONValue{Data: metadata}
		if err := tx.WithContext(ctx).Save(&models[i]).Error; err != nil {
			return err
		}
	}
	return nil
}

func (w *ControlWorker) markProjectTemplateRunCompleted(ctx context.Context, opID string, repo map[string]any, remotes []map[string]any, files []map[string]any, steps []map[string]any, result map[string]any, provision *gitExecutionResult) error {
	return w.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if provision != nil {
			if provisioned, _ := provision.Details["provisioned"].(bool); provisioned {
				if err := markTemplateRepositoryProvisionedGorm(ctx, tx, repo, files, provision); err != nil {
					return err
				}
			}
		}
		updated := tx.Model(&GormProjectTemplateRun{}).
			Where(&GormProjectTemplateRun{OperationRunID: validNullString(opID)}).
			Updates(map[string]any{"status": "completed", "steps": JSONValue{Data: steps}, "result": JSONValue{Data: result}, "finished_at": time.Now()})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 0 {
			return ErrNotFound
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for completed project template creation: %w", err)
		}
		return nil
	})
}

func (w *ControlWorker) markProjectTemplateRunFailed(ctx context.Context, opID string, result map[string]any, cause error) error {
	stepsValue := result["steps"]
	if !hasTemplateSteps(stepsValue) {
		var run GormProjectTemplateRun
		runErr := w.store.Gorm.WithContext(ctx).Where(&GormProjectTemplateRun{OperationRunID: validNullString(opID)}).First(&run).Error
		if runErr != nil {
			return runErr
		}
		stepsValue = mapSliceFromAny(run.Steps.Data)
	}
	failedSteps := templateStepsWithStatus(stepsValue, "failed")
	result["steps"] = failedSteps
	errorMessage := truncateProviderError(cause.Error(), providerRunErrorLimit)
	result["error"] = errorMessage
	updated := w.store.Gorm.WithContext(ctx).Model(&GormProjectTemplateRun{}).
		Where(&GormProjectTemplateRun{OperationRunID: validNullString(opID)}).
		Updates(map[string]any{"status": "failed", "steps": JSONValue{Data: failedSteps}, "result": JSONValue{Data: result}, "error_message": errorMessage, "finished_at": time.Now()})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

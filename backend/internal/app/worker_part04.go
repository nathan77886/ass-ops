package app

import (
	"context"
	"fmt"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"strings"
	"time"
)

func (w *ControlWorker) updateOperationRemoteSyncStatusGorm(ctx context.Context, opID, status, afterSHA string) error {
	op, err := operationRunByID(ctx, w.store.Gorm, opID)
	if err != nil {
		return err
	}
	if !op.GitRemoteID.Valid {
		return nil
	}
	updates := map[string]any{"last_sync_status": status}
	if afterSHA != "" {
		updates["latest_sha"] = afterSHA
	}
	return w.store.Gorm.WithContext(ctx).Model(&GormGitRemote{}).Where(&GormGitRemote{GormBase: GormBase{ID: op.GitRemoteID.String}}).Updates(updates).Error
}

func (w *ControlWorker) recordRepoTagRunFailedGorm(ctx context.Context, opID, runID, stdout, stderr, errorMessage string) error {
	var run GormRepoTagRun
	query := w.store.Gorm.WithContext(ctx)
	if runID != "" {
		query = query.Where(&GormRepoTagRun{ID: runID})
	} else {
		query = query.Where(&GormRepoTagRun{OperationRunID: opID})
	}
	if err := query.First(&run).Error; err != nil {
		return err
	}
	run.Status = "failed"
	run.Stdout = stdout
	run.Stderr = stderr
	run.ErrorMessage = errorMessage
	run.FinishedAt = validNullTime(time.Now())
	return w.store.Gorm.WithContext(ctx).Save(&run).Error
}

func (w *ControlWorker) recordRepoTagLookupCompletedGorm(ctx context.Context, runID string, found bool, afterSHA string) error {
	if runID == "" {
		return nil
	}
	var run GormRepoTagRun
	if err := w.store.Gorm.WithContext(ctx).Where(&GormRepoTagRun{ID: runID}).First(&run).Error; err != nil {
		return err
	}
	if found {
		run.Status = "completed"
		run.ErrorMessage = ""
		if afterSHA != "" {
			run.TargetSHA = afterSHA
		}
	} else {
		run.Status = "failed"
		run.ErrorMessage = "remote tag not found"
	}
	run.FinishedAt = validNullTime(time.Now())
	return w.store.Gorm.WithContext(ctx).Save(&run).Error
}

func (w *ControlWorker) recordRepoTagRunCompletedGorm(ctx context.Context, opID, stdout, stderr, afterSHA string) error {
	return w.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run GormRepoTagRun
		if err := tx.Where(&GormRepoTagRun{OperationRunID: opID}).First(&run).Error; err != nil {
			return err
		}
		run.Status = "completed"
		run.Stdout = stdout
		run.Stderr = stderr
		if run.TargetSHA == "" && afterSHA != "" {
			run.TargetSHA = afterSHA
		}
		run.FinishedAt = validNullTime(time.Now())
		if err := tx.Save(&run).Error; err != nil {
			return err
		}
		remoteID := cleanOptionalID(run.TargetRemoteID.String)
		if remoteID == "" {
			remoteID = cleanOptionalID(run.GitRemoteID)
		}
		if remoteID != "" {
			if err := tx.Model(&GormGitRemote{}).Where(&GormGitRemote{GormBase: GormBase{ID: remoteID}}).Update("updated_at", time.Now()).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (w *ControlWorker) enqueueRepoTagPostSuccessOperationsGorm(ctx context.Context, opID string) error {
	return w.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run GormRepoTagRun
		if err := tx.Where(&GormRepoTagRun{OperationRunID: opID}).First(&run).Error; err != nil {
			return err
		}
		runID := cleanOptionalID(run.ID)
		projectID := cleanOptionalID(run.ProjectID.String)
		if projectID == "" && run.ProjectGitRepositoryID.Valid {
			var repo GormProjectGitRepository
			if err := tx.First(&repo, &GormProjectGitRepository{GormBase: GormBase{ID: run.ProjectGitRepositoryID.String}}).Error; err == nil {
				projectID = repo.ProjectID
			} else if !errorsIsRecordNotFound(err) {
				return err
			}
		}
		targetRemoteID := cleanOptionalID(run.TargetRemoteID.String)
		if targetRemoteID == "" {
			targetRemoteID = cleanOptionalID(run.GitRemoteID)
		}
		tagName := strings.TrimSpace(run.TagName)
		if runID == "" || projectID == "" || targetRemoteID == "" || tagName == "" || !isSafeGitRefPart(tagName) {
			return nil
		}
		if err := enqueueRepoTagLookupAfterSuccessGorm(ctx, tx, projectID, targetRemoteID, runID, tagName); err != nil {
			return err
		}
		var remote GormGitRemote
		if err := tx.First(&remote, &GormGitRemote{GormBase: GormBase{ID: targetRemoteID}}).Error; err != nil {
			return err
		}
		if _, _, err := gitHubRepositoryFromRemote(gitRemoteMap(remote, nil, "")); err != nil {
			return nil
		}
		targetSHA := strings.TrimSpace(run.TargetSHA)
		if !isFullHexSHA(targetSHA) {
			targetSHA = ""
		}
		return enqueueRepoTagGitHubActionsRefreshAfterSuccessGorm(ctx, tx, projectID, targetRemoteID, runID, tagName, targetSHA)
	})
}

func enqueueRepoTagLookupAfterSuccessGorm(ctx context.Context, tx *gorm.DB, projectID, targetRemoteID, runID, tagName string) error {
	existing, err := repoTagLookupExistingOperationForAutoGorm(ctx, tx, runID)
	if err != nil || existing != nil {
		return err
	}
	input := map[string]any{"repo_tag_run_id": runID, "target_remote_id": targetRemoteID, "tag_name": tagName, "trigger": "repo_tag_success"}
	_, err = enqueueOperationGorm(ctx, tx, projectID, targetRemoteID, "repo.tag.lookup", "lookup repository tag after successful tag push", input, []string{"git"}, "")
	return err
}

func enqueueRepoTagGitHubActionsRefreshAfterSuccessGorm(ctx context.Context, tx *gorm.DB, projectID, targetRemoteID, runID, tagName, targetSHA string) error {
	existing, err := repoTagActionsRefreshExistingOperationForAutoGorm(ctx, tx, runID)
	if err != nil || existing != nil {
		return err
	}
	input := map[string]any{"repo_tag_run_id": runID, "target_remote_id": targetRemoteID, "refresh_kind": "repo_tag_actions_refresh", "commit_sha": targetSHA, "tag_name": tagName, "limit": 50, "trigger": "repo_tag_success"}
	_, err = enqueueOperationGorm(ctx, tx, projectID, targetRemoteID, "github.actions.sync", "refresh GitHub Actions after successful repository tag", input, []string{"github", "git"}, "")
	return err
}

func repoTagLookupExistingOperationForAutoGorm(ctx context.Context, db *gorm.DB, runID string) (map[string]any, error) {
	return repoTagFollowUpOperationForAutoGorm(ctx, db, runID, "repo.tag.lookup", "")
}

func repoTagActionsRefreshExistingOperationForAutoGorm(ctx context.Context, db *gorm.DB, runID string) (map[string]any, error) {
	return repoTagFollowUpOperationForAutoGorm(ctx, db, runID, "github.actions.sync", "repo_tag_actions_refresh")
}

func repoTagFollowUpOperationForAutoGorm(ctx context.Context, db *gorm.DB, runID, operationType, refreshKind string) (map[string]any, error) {
	var ops []GormOperationRun
	if err := db.WithContext(ctx).Where(&GormOperationRun{OperationType: operationType}).Where(gormField("status", []string{"queued", "running", "completed", "succeeded", "success", "failed", "canceled"})).Order(gormOrderDesc("created_at")).Find(&ops).Error; err != nil {
		return nil, err
	}
	for _, op := range ops {
		input := mapFromAny(op.Input.Data)
		if cleanOptionalID(fmt.Sprint(input["repo_tag_run_id"])) != runID {
			continue
		}
		if refreshKind != "" && strings.TrimSpace(fmt.Sprint(input["refresh_kind"])) != refreshKind {
			continue
		}
		return map[string]any{"id": op.ID, "operation_type": op.OperationType, "status": op.Status, "error": op.Error, "started_at": nullableTimeAny(op.StartedAt), "finished_at": nullableTimeAny(op.FinishedAt), "created_at": op.CreatedAt, "updated_at": op.UpdatedAt}, nil
	}
	return nil, nil
}

func enqueueOperationGorm(ctx context.Context, tx *gorm.DB, projectID, remoteID, tool, title string, input map[string]any, capabilities []string, preferredKind string) (map[string]any, error) {
	op := GormOperationRun{ProjectID: validNullString(projectID), GitRemoteID: validNullString(remoteID), OperationType: tool, Title: title, Input: JSONValue{Data: input}, Result: JSONValue{Data: map[string]any{}}}
	if err := tx.WithContext(ctx).Create(&op).Error; err != nil {
		return nil, err
	}
	job := GormWorkerJob{OperationRunID: validNullString(op.ID), ToolName: tool, Payload: JSONValue{Data: input}, Result: JSONValue{Data: map[string]any{}}, RequiredCapabilities: pq.StringArray(capabilities), PreferredNodeKind: preferredKind}
	if err := tx.WithContext(ctx).Create(&job).Error; err != nil {
		return nil, err
	}
	return operationRunGormMap(op), nil
}

func operationRunGormMap(op GormOperationRun) map[string]any {
	return map[string]any{"id": op.ID, "project_id": nullableStringValue(op.ProjectID), "git_remote_id": nullableStringValue(op.GitRemoteID), "operation_type": op.OperationType, "status": op.Status, "title": op.Title, "input": mapFromAny(op.Input.Data), "result": mapFromAny(op.Result.Data), "error": op.Error, "started_at": nullableTimeAny(op.StartedAt), "finished_at": nullableTimeAny(op.FinishedAt), "created_at": op.CreatedAt, "updated_at": op.UpdatedAt}
}

func (w *ControlWorker) executeAdapterRun(ctx context.Context, job map[string]any) (map[string]any, error) {
	opID := fmt.Sprint(job["operation_run_id"])
	tool := fmt.Sprint(job["tool_name"])
	result := map[string]any{
		"adapter": true,
		"tool":    tool,
		"message": "adapter completed",
	}
	switch tool {
	case "repo.sync", "repo.sync_remote":
		execution, err := w.newGitExecutor("").Sync(ctx, w.store.Gorm, opID)
		mergeGitExecutionResult(result, execution)
		return result, err
	case "git.refs.refresh":
		execution, err := w.newGitExecutor("").RefreshRemoteRefs(ctx, w.store.Gorm, opID)
		mergeGitExecutionResult(result, execution)
		return result, err
	case "repo.tag", "repo.create_tag":
		execution, err := w.newGitExecutor("").Tag(ctx, w.store.Gorm, opID)
		mergeGitExecutionResult(result, execution)
		return result, err
	case "repo.tag.lookup":
		execution, err := w.newGitExecutor("").LookupTag(ctx, w.store.Gorm, opID)
		mergeRepoTagLookupExecutionResult(result, execution)
		return result, err
	case "github.actions.sync":
		syncResult, err := NewGitHubActionsSyncer().Sync(ctx, w.store.Gorm, opID)
		mergeGitHubActionsResult(result, syncResult)
		return result, err
	case "github.labels.sync":
		syncResult, err := NewGitHubActionsSyncer().SyncLabels(ctx, w.store.Gorm, opID)
		mergeGitHubRepositoryLabelsResult(result, syncResult)
		return result, err
	case "argo.apps.sync":
		syncResult, err := NewArgoSyncer().SyncApps(ctx, w.store.Gorm, opID)
		mergeArgoSyncResult(result, syncResult)
		return result, err
	case "argo.token_create":
		return w.executeArgoTokenCreate(ctx, opID, result)
	case "argo.pod_logs":
		return w.executeArgoPodLogAudit(ctx, opID, result)
	case "argo.pod_restart":
		return w.executeArgoPodRestart(ctx, opID, result)
	case "config.git_commit":
		return w.executeConfigGitWorkflow(ctx, opID, result)
	case "project_version.validation_rerun":
		return w.executeProjectVersionValidationRerun(ctx, opID, result)
	case "ssh.exec", "ssh.verify":
		execution, err := NewSSHExecutor().Execute(ctx, w.store.Gorm, opID)
		mergeSSHExecutionResult(result, execution)
		return result, err
	case "project.create_from_template":
		return result, fmt.Errorf("project template flow is disabled; add repositories manually")
	case "project.template_provision_retry":
		return result, fmt.Errorf("project template flow is disabled; add repositories manually")
	case "agent.execute":
		return w.executeAgentTaskAudit(ctx, opID, result)
	default:
		return result, nil
	}
}

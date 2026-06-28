package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

func (w *ControlWorker) recoverStaleRunningJobs(ctx context.Context) error {
	cutoff := time.Now().Add(-30 * time.Minute)
	return w.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var jobs []GormWorkerJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where(&GormWorkerJob{Status: "running"}).
			Where("started_at < ?", cutoff).
			Find(&jobs).Error; err != nil {
			return err
		}
		for i := range jobs {
			jobs[i].Status = "failed"
			jobs[i].Result = JSONValue{Data: workerTimeoutResult()}
			jobs[i].Error = "worker timed out while running"
			jobs[i].FinishedAt = validNullTime(time.Now())
			if err := tx.Save(&jobs[i]).Error; err != nil {
				return err
			}
			if opID := cleanOptionalID(jobs[i].OperationRunID.String); opID != "" {
				if err := failTimedOutOperationGorm(ctx, tx, opID); err != nil {
					return err
				}
			}
		}

		var ops []GormOperationRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where(&GormOperationRun{Status: "running"}).
			Where("started_at < ?", cutoff).
			Find(&ops).Error; err != nil {
			return err
		}
		for _, op := range ops {
			var activeJobs int64
			if err := tx.Model(&GormWorkerJob{}).
				Where(&GormWorkerJob{OperationRunID: validNullString(op.ID)}).
				Where("status IN ?", []string{"queued", "running"}).
				Count(&activeJobs).Error; err != nil {
				return err
			}
			if activeJobs > 0 {
				continue
			}
			if err := failTimedOutOperationGorm(ctx, tx, op.ID); err != nil {
				return err
			}
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for stale worker recovery: %w", err)
		}
		return nil
	})
}

func workerTimeoutResult() map[string]any {
	return map[string]any{"adapter": true, "recovered": true, "reason": "worker timeout"}
}

func failTimedOutOperationGorm(ctx context.Context, tx *gorm.DB, opID string) error {
	now := time.Now()
	var op GormOperationRun
	if err := tx.WithContext(ctx).First(&op, &GormOperationRun{GormBase: GormBase{ID: opID}}).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return nil
		}
		return err
	}
	op.Status = "failed"
	op.Result = JSONValue{Data: workerTimeoutResult()}
	op.Error = "worker timed out while running"
	op.FinishedAt = validNullTime(now)
	if err := tx.WithContext(ctx).Save(&op).Error; err != nil {
		return err
	}
	if err := failTimedOutRepoSyncGorm(ctx, tx, opID); err != nil {
		return err
	}
	if err := failTimedOutRepoTagGorm(ctx, tx, op); err != nil {
		return err
	}
	if err := tx.WithContext(ctx).Model(&GormSSHCommandRun{}).Where(&GormSSHCommandRun{OperationRunID: validNullString(opID)}).Where(gormField("status", []string{"queued", "running"})).Updates(map[string]any{"status": "failed", "error_message": "worker timed out while running", "finished_at": validNullTime(now)}).Error; err != nil {
		return err
	}
	if op.OperationType == "argo.apps.sync" {
		connectionID := cleanOptionalID(stringFromMap(mapFromAny(op.Input.Data), "argo_connection_id"))
		if connectionID != "" {
			if err := tx.WithContext(ctx).Model(&GormArgoConnection{}).Where(&GormArgoConnection{GormBase: GormBase{ID: connectionID}}).Updates(map[string]any{"last_sync_status": "failed", "last_sync_error": "worker timed out while running"}).Error; err != nil {
				return err
			}
		}
	}
	if err := failTimedOutTemplateRunGorm(ctx, tx, op); err != nil {
		return err
	}
	if err := tx.WithContext(ctx).Model(&GormAgentToolCall{}).Where(&GormAgentToolCall{OperationRunID: validNullString(opID)}).Where(gormField("status", []string{"queued", "planned", "running"})).Updates(map[string]any{"status": "failed", "error_message": "worker timed out while running", "finished_at": validNullTime(now)}).Error; err != nil {
		return err
	}
	if op.OperationType == "agent.execute" {
		taskID := cleanOptionalID(stringFromMap(mapFromAny(op.Input.Data), "agent_task_id"))
		if taskID != "" {
			if err := tx.WithContext(ctx).Model(&GormAgentTask{}).Where(&GormAgentTask{GormBase: GormBase{ID: taskID}}).Where(gormField("status", []string{"queued", "running"})).Updates(map[string]any{"status": "failed"}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func failTimedOutRepoSyncGorm(ctx context.Context, tx *gorm.DB, opID string) error {
	var runs []GormRepoSyncRun
	if err := tx.WithContext(ctx).Where(&GormRepoSyncRun{OperationRunID: opID}).Where(gormField("status", []string{"queued", "running", "provisioning"})).Find(&runs).Error; err != nil {
		return err
	}
	now := time.Now()
	for i := range runs {
		runs[i].Status = "failed"
		runs[i].ErrorMessage = "worker timed out while running"
		runs[i].FinishedAt = validNullTime(now)
		if err := tx.WithContext(ctx).Save(&runs[i]).Error; err != nil {
			return err
		}
		if runs[i].RepoSyncAssetID.Valid {
			if err := tx.WithContext(ctx).Model(&GormRepoSyncAsset{}).Where(&GormRepoSyncAsset{GormBase: GormBase{ID: runs[i].RepoSyncAssetID.String}}).Updates(map[string]any{"last_sync_status": "failed"}).Error; err != nil {
				return err
			}
		}
		if runs[i].TargetRemoteID.Valid {
			if err := tx.WithContext(ctx).Model(&GormGitRemote{}).Where(&GormGitRemote{GormBase: GormBase{ID: runs[i].TargetRemoteID.String}}).Updates(map[string]any{"last_sync_status": "failed"}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func failTimedOutRepoTagGorm(ctx context.Context, tx *gorm.DB, op GormOperationRun) error {
	now := time.Now()
	if err := tx.WithContext(ctx).Model(&GormRepoTagRun{}).Where(&GormRepoTagRun{OperationRunID: op.ID}).Where(gormField("status", []string{"queued", "running"})).Updates(map[string]any{"status": "failed", "error_message": "worker timed out while running", "finished_at": validNullTime(now)}).Error; err != nil {
		return err
	}
	if op.OperationType == "repo.tag.lookup" {
		runID := cleanOptionalID(stringFromMap(mapFromAny(op.Input.Data), "repo_tag_run_id"))
		if runID != "" {
			return tx.WithContext(ctx).Model(&GormRepoTagRun{}).Where(&GormRepoTagRun{ID: runID}).Where(gormField("status", []string{"queued", "running"})).Updates(map[string]any{"status": "failed", "error_message": "worker timed out while running", "finished_at": validNullTime(now)}).Error
		}
	}
	return nil
}

func failTimedOutTemplateRunGorm(ctx context.Context, tx *gorm.DB, op GormOperationRun) error {
	now := time.Now()
	if err := tx.WithContext(ctx).Model(&GormProjectTemplateRun{}).Where(&GormProjectTemplateRun{OperationRunID: validNullString(op.ID)}).Where(gormField("status", []string{"queued", "running", "provisioning"})).Updates(map[string]any{"status": "failed", "error_message": "worker timed out while running", "finished_at": validNullTime(now)}).Error; err != nil {
		return err
	}
	if op.OperationType == "project.template_provision_retry" {
		runID := cleanOptionalID(stringFromMap(mapFromAny(op.Input.Data), "project_template_run_id"))
		if runID != "" {
			return tx.WithContext(ctx).Model(&GormProjectTemplateRun{}).Where(&GormProjectTemplateRun{GormBase: GormBase{ID: runID}}).Where(gormField("status", []string{"queued", "running", "provisioning"})).Updates(map[string]any{"status": "failed", "error_message": "worker timed out while running", "finished_at": validNullTime(now)}).Error
		}
	}
	return nil
}

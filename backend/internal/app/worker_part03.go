package app

import (
	"context"
	"database/sql"
	"fmt"
	"gorm.io/gorm"
	"time"
)

func (w *ControlWorker) markAdapterRunning(ctx context.Context, db *gorm.DB, job map[string]any) error {
	opID := fmt.Sprint(job["operation_run_id"])
	tool := fmt.Sprint(job["tool_name"])
	now := time.Now()
	switch tool {
	case "repo.sync", "repo.sync_remote":
		var run GormRepoSyncRun
		if err := db.WithContext(ctx).Where(&GormRepoSyncRun{OperationRunID: opID}).First(&run).Error; err != nil {
			return err
		}
		run.Status = "running"
		if !run.StartedAt.Valid {
			run.StartedAt = validNullTime(now)
		}
		if err := db.WithContext(ctx).Save(&run).Error; err != nil {
			return err
		}
		if run.RepoSyncAssetID.Valid {
			if err := db.WithContext(ctx).Model(&GormRepoSyncAsset{}).
				Where(&GormRepoSyncAsset{GormBase: GormBase{ID: run.RepoSyncAssetID.String}}).
				Updates(map[string]any{"last_sync_status": "running"}).Error; err != nil {
				return err
			}
		}
		if _, err := syncCanonicalAssetsGorm(ctx, db); err != nil {
			return fmt.Errorf("syncing canonical assets for running repo sync: %w", err)
		}
		return nil
	case "git.refs.refresh":
		op, err := operationRunByID(ctx, db, opID)
		if err != nil {
			return err
		}
		if op.GitRemoteID.Valid {
			if err := db.WithContext(ctx).Model(&GormGitRemote{}).
				Where(&GormGitRemote{GormBase: GormBase{ID: op.GitRemoteID.String}}).
				Updates(map[string]any{"last_sync_status": "running"}).Error; err != nil {
				return err
			}
		}
		if _, err := syncCanonicalAssetsGorm(ctx, db); err != nil {
			return fmt.Errorf("syncing canonical assets for running Git ref refresh: %w", err)
		}
		return nil
	case "repo.tag", "repo.create_tag":
		return markRepoTagRunRunning(ctx, db, opID, "")
	case "repo.tag.lookup":
		op, err := operationRunByID(ctx, db, opID)
		if err != nil {
			return err
		}
		if err := markRepoTagRunRunning(ctx, db, "", cleanOptionalID(stringFromMap(mapFromAny(op.Input.Data), "repo_tag_run_id"))); err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, db); err != nil {
			return fmt.Errorf("syncing canonical assets for running repo tag lookup: %w", err)
		}
		return nil
	case "ssh.exec", "ssh.verify":
		// Verify is audited through the same SSH run table, but the executor
		// defensively forces the command to a no-op connectivity check.
		if err := db.WithContext(ctx).Model(&GormSSHCommandRun{}).
			Where(&GormSSHCommandRun{OperationRunID: validNullString(opID)}).
			Updates(map[string]any{"status": "running", "started_at": validNullTime(now)}).Error; err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, db); err != nil {
			return fmt.Errorf("syncing canonical assets for running SSH command: %w", err)
		}
		return nil
	case "argo.apps.sync":
		op, err := operationRunByID(ctx, db, opID)
		if err != nil {
			return err
		}
		if connectionID := cleanOptionalID(stringFromMap(mapFromAny(op.Input.Data), "argo_connection_id")); connectionID != "" {
			if err := db.WithContext(ctx).Model(&GormArgoConnection{}).
				Where(&GormArgoConnection{GormBase: GormBase{ID: connectionID}}).
				Updates(map[string]any{"last_sync_status": "running", "last_sync_error": ""}).Error; err != nil {
				return err
			}
		}
		if _, err := syncCanonicalAssetsGorm(ctx, db); err != nil {
			return fmt.Errorf("syncing canonical assets for running Argo app sync: %w", err)
		}
		return nil
	case "argo.pod_logs":
		backend := "disabled"
		if w.cfg.KubernetesPodLogsEnabled {
			backend = "kubectl_logs"
		}
		fields := map[string]any{"live_log_backend": backend, "kubeconfig_bound": false, "log_body_included": false}
		if err := db.WithContext(ctx).Create(&GormOperationLog{OperationRunID: validNullString(opID), WorkerJobID: validNullString(fmt.Sprint(job["id"])), Level: "info", Message: "pod log audit worker started", Fields: JSONValue{Data: fields}}).Error; err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, db); err != nil {
			return fmt.Errorf("syncing canonical assets for running Argo pod log audit: %w", err)
		}
		return nil
	case "argo.pod_restart":
		backend := "disabled"
		if w.cfg.KubernetesRestartsEnabled {
			backend = "kubectl_rollout_restart"
		}
		fields := map[string]any{"restart_backend": backend, "kubeconfig_bound": false, "raw_response_included": false, "stdout_included": false, "stderr_included": false}
		if err := db.WithContext(ctx).Create(&GormOperationLog{OperationRunID: validNullString(opID), WorkerJobID: validNullString(fmt.Sprint(job["id"])), Level: "info", Message: "pod restart worker started", Fields: JSONValue{Data: fields}}).Error; err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, db); err != nil {
			return fmt.Errorf("syncing canonical assets for running Argo pod restart: %w", err)
		}
		return nil
	case "config.git_commit":
		backend := "approval_gated_audit_only"
		if w.cfg.ConfigGitLocalBareWritesEnabled {
			backend = "local_bare_git_write_when_eligible"
		}
		fields := map[string]any{"backend": backend, "git_write_performed": false, "external_call_made": false, "file_content_included": false, "secret_included": false}
		if err := db.WithContext(ctx).Create(&GormOperationLog{OperationRunID: validNullString(opID), WorkerJobID: validNullString(fmt.Sprint(job["id"])), Level: "info", Message: "config git workflow worker started", Fields: JSONValue{Data: fields}}).Error; err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, db); err != nil {
			return fmt.Errorf("syncing canonical assets for running config Git workflow audit: %w", err)
		}
		return nil
	case "project_version.validation_rerun":
		fields := map[string]any{"validation_source": "local_synced_database_state", "external_call_made": false, "provider_api_called": false, "raw_provider_response_recorded": false}
		if err := db.WithContext(ctx).Create(&GormOperationLog{OperationRunID: validNullString(opID), WorkerJobID: validNullString(fmt.Sprint(job["id"])), Level: "info", Message: "project version validation rerun worker started", Fields: JSONValue{Data: fields}}).Error; err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, db); err != nil {
			return fmt.Errorf("syncing canonical assets for running project version validation rerun: %w", err)
		}
		return nil
	case "project.create_from_template":
		return db.WithContext(ctx).Model(&GormProjectTemplateRun{}).
			Where(&GormProjectTemplateRun{OperationRunID: validNullString(opID)}).
			Updates(map[string]any{"status": "running", "started_at": validNullTime(now)}).Error
	case "project.template_provision_retry":
		op, err := operationRunByID(ctx, db, opID)
		if err != nil {
			return err
		}
		runID := cleanOptionalID(stringFromMap(mapFromAny(op.Input.Data), "project_template_run_id"))
		var run GormProjectTemplateRun
		if err := db.WithContext(ctx).First(&run, &GormProjectTemplateRun{GormBase: GormBase{ID: runID}}).Error; err != nil {
			return err
		}
		result := mapFromAny(run.Result.Data)
		result["provision_retry"] = map[string]any{"operation_run_id": opID, "started_at": now.Format(time.RFC3339)}
		run.Status = "provisioning"
		if !run.StartedAt.Valid {
			run.StartedAt = validNullTime(now)
		}
		run.FinishedAt = sql.NullTime{}
		run.ErrorMessage = ""
		run.Result = JSONValue{Data: result}
		return db.WithContext(ctx).Save(&run).Error
	case "agent.execute":
		if err := db.WithContext(ctx).Model(&GormAgentToolCall{}).
			Where(&GormAgentToolCall{OperationRunID: validNullString(opID)}).
			Where("status IN ?", []string{"queued", "planned"}).
			Updates(map[string]any{"status": "running", "started_at": validNullTime(now)}).Error; err != nil {
			return err
		}
		op, err := operationRunByID(ctx, db, opID)
		if err != nil {
			return err
		}
		if taskID := cleanOptionalID(stringFromMap(mapFromAny(op.Input.Data), "agent_task_id")); taskID != "" {
			if err := db.WithContext(ctx).Model(&GormAgentTask{}).
				Where(&GormAgentTask{GormBase: GormBase{ID: taskID}}).
				Where("status IN ?", []string{"queued", "planned"}).
				Updates(map[string]any{"status": "running"}).Error; err != nil {
				return err
			}
		}
		if _, err := syncCanonicalAssetsGorm(ctx, db); err != nil {
			return fmt.Errorf("syncing canonical assets for running agent execution: %w", err)
		}
		return nil
	default:
		return nil
	}
}

func markRepoTagRunRunning(ctx context.Context, db *gorm.DB, opID, runID string) error {
	var run GormRepoTagRun
	query := db.WithContext(ctx)
	if runID != "" {
		query = query.Where(&GormRepoTagRun{ID: runID})
	} else {
		query = query.Where(&GormRepoTagRun{OperationRunID: opID})
	}
	if err := query.First(&run).Error; err != nil {
		return err
	}
	run.Status = "running"
	if !run.StartedAt.Valid {
		run.StartedAt = validNullTime(time.Now())
	}
	run.ErrorMessage = ""
	return db.WithContext(ctx).Save(&run).Error
}

func (w *ControlWorker) recordRepoSyncRunFailedGorm(ctx context.Context, opID, stdout, stderr, errorMessage string) error {
	return w.updateRepoSyncRunGorm(ctx, opID, "failed", stdout, stderr, "", errorMessage)
}

func (w *ControlWorker) recordRepoSyncRunCompletedGorm(ctx context.Context, opID, stdout, stderr, afterSHA string) error {
	return w.updateRepoSyncRunGorm(ctx, opID, "completed", stdout, stderr, afterSHA, "")
}

func (w *ControlWorker) updateRepoSyncRunGorm(ctx context.Context, opID, status, stdout, stderr, afterSHA, errorMessage string) error {
	return w.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run GormRepoSyncRun
		if err := tx.Where(&GormRepoSyncRun{OperationRunID: opID}).First(&run).Error; err != nil {
			return err
		}
		run.Status = status
		run.Stdout = stdout
		run.Stderr = stderr
		if afterSHA != "" {
			run.AfterSHA = afterSHA
		}
		run.ErrorMessage = errorMessage
		run.FinishedAt = validNullTime(time.Now())
		if err := tx.Save(&run).Error; err != nil {
			return err
		}
		if run.TargetRemoteID.Valid {
			updates := map[string]any{"last_sync_status": status}
			if status == "completed" && afterSHA != "" {
				updates["latest_sha"] = afterSHA
			}
			if err := tx.Model(&GormGitRemote{}).Where(&GormGitRemote{GormBase: GormBase{ID: run.TargetRemoteID.String}}).Updates(updates).Error; err != nil {
				return err
			}
		}
		if run.RepoSyncAssetID.Valid {
			updates := map[string]any{"last_sync_status": status}
			if status == "completed" {
				updates["last_synced_at"] = validNullTime(time.Now())
			}
			if err := tx.Model(&GormRepoSyncAsset{}).Where(&GormRepoSyncAsset{GormBase: GormBase{ID: run.RepoSyncAssetID.String}}).Updates(updates).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

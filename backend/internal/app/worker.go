package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ControlWorker struct {
	store    *Store
	cfg      Config
	interval time.Duration
	log      *slog.Logger
	server   *Server
}

func NewControlWorker(store *Store, cfg Config, log *slog.Logger) *ControlWorker {
	return &ControlWorker{store: store, cfg: cfg, interval: cfg.WorkerInterval, log: log, server: NewServer(cfg, store, log)}
}

func (w *ControlWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if err := w.processOne(ctx); err != nil && err != ErrNotFound {
			w.log.Error("worker process failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *ControlWorker) processOne(ctx context.Context) error {
	if err := w.sweepExpiredApprovals(ctx); err != nil {
		w.log.Warn("approval expiry sweep failed", "error", err)
	}
	if err := w.dispatchDueApprovalEscalations(ctx); err != nil {
		w.log.Warn("approval escalation sweep failed", "error", err)
	}
	if err := w.dispatchDueApprovalReminders(ctx); err != nil {
		w.log.Warn("approval reminder sweep failed", "error", err)
	}
	if err := w.recoverStaleRunningJobs(ctx); err != nil {
		w.log.Warn("stale job recovery failed", "error", err)
	}
	var job map[string]any
	err := w.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var jobModel GormWorkerJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ? AND preferred_node_kind IN ?", "queued", []string{"", "control-worker"}).
			Order("created_at ASC").
			First(&jobModel).Error; err != nil {
			if errorsIsRecordNotFound(err) {
				return ErrNotFound
			}
			return err
		}
		now := time.Now()
		jobModel.Status = "running"
		jobModel.StartedAt = validNullTime(now)
		if err := tx.Save(&jobModel).Error; err != nil {
			return err
		}
		job = workerJobMap(jobModel)
		opID := cleanOptionalID(jobModel.OperationRunID.String)
		if opID == "" {
			return fmt.Errorf("worker job %s is missing operation_run_id", jobModel.ID)
		}
		if err := tx.Model(&GormOperationRun{}).
			Where(&GormOperationRun{GormBase: GormBase{ID: opID}}).
			Updates(map[string]any{"status": "running", "started_at": validNullTime(now)}).Error; err != nil {
			return err
		}
		if err := tx.Create(&GormOperationLog{OperationRunID: validNullString(opID), WorkerJobID: validNullString(jobModel.ID), Level: "info", Message: "dispatching " + jobModel.ToolName, Fields: JSONValue{Data: map[string]any{}}}).Error; err != nil {
			return err
		}
		return w.markAdapterRunning(ctx, tx, job)
	})
	if err != nil {
		return err
	}
	opID := fmt.Sprint(job["operation_run_id"])

	result, adapterErr := w.executeAdapterRun(ctx, job)

	if adapterErr != nil {
		if result == nil {
			result = map[string]any{"adapter": true}
		}
		adapterErrorMessage := adapterErr.Error()
		if fmt.Sprint(job["tool_name"]) == "repo.tag.lookup" {
			adapterErrorMessage = sanitizeLookupError(adapterErr)
		}
		result["error"] = adapterErrorMessage
		if err := w.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return w.recordAdapterFailure(ctx, tx, job, result, adapterErr)
		}); err != nil {
			return err
		}
		if err := w.markWorkerOperationFinished(ctx, job, opID, "failed", result, adapterErrorMessage); err != nil {
			return err
		}
		w.refreshCanonicalAssetsAfterOperation(ctx, job, opID, "failed")
		w.autoRecordProjectVersionValidationSnapshotAfterRefresh(ctx, opID, "failed")
		return adapterErr
	}
	if err := w.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return w.recordAdapterSuccess(ctx, tx, job, result)
	}); err != nil {
		if result == nil {
			result = map[string]any{"adapter": true}
		}
		result["error"] = err.Error()
		if failErr := w.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return w.recordAdapterFailure(ctx, tx, job, result, err)
		}); failErr != nil {
			return errors.Join(err, failErr)
		}
		if failErr := w.markWorkerOperationFinished(ctx, job, opID, "failed", result, err.Error()); failErr != nil {
			return errors.Join(err, failErr)
		}
		w.refreshCanonicalAssetsAfterOperation(ctx, job, opID, "failed")
		w.autoRecordProjectVersionValidationSnapshotAfterRefresh(ctx, opID, "failed")
		return err
	}
	if err := w.markWorkerOperationFinished(ctx, job, opID, "completed", result, ""); err != nil {
		return err
	}
	w.refreshCanonicalAssetsAfterOperation(ctx, job, opID, "completed")
	w.autoRecordProjectVersionValidationSnapshotAfterRefresh(ctx, opID, "completed")
	return nil
}

func (w *ControlWorker) markWorkerOperationFinished(ctx context.Context, job map[string]any, opID, status string, result map[string]any, errorMessage string) error {
	now := time.Now()
	return w.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		jobUpdates := map[string]any{"status": status, "result": JSONValue{Data: result}, "finished_at": validNullTime(now)}
		if errorMessage != "" {
			jobUpdates["error"] = errorMessage
		} else {
			jobUpdates["error"] = ""
		}
		if err := tx.Model(&GormWorkerJob{}).Where(&GormWorkerJob{GormBase: GormBase{ID: cleanOptionalID(fmt.Sprint(job["id"]))}}).Updates(jobUpdates).Error; err != nil {
			return err
		}
		opUpdates := map[string]any{"status": status, "result": JSONValue{Data: operationRunResult(job, result)}, "finished_at": validNullTime(now)}
		if errorMessage != "" {
			opUpdates["error"] = errorMessage
		} else {
			opUpdates["error"] = ""
		}
		return tx.Model(&GormOperationRun{}).Where(&GormOperationRun{GormBase: GormBase{ID: opID}}).Updates(opUpdates).Error
	})
}

func (w *ControlWorker) recordOperationLogGorm(ctx context.Context, opID string, job map[string]any, level, message string, fields map[string]any) error {
	return w.store.Gorm.WithContext(ctx).Create(&GormOperationLog{
		OperationRunID: validNullString(opID),
		WorkerJobID:    validNullString(cleanOptionalID(fmt.Sprint(job["id"]))),
		Level:          level,
		Message:        message,
		Fields:         JSONValue{Data: fields},
	}).Error
}

func workerJobMap(job GormWorkerJob) map[string]any {
	return map[string]any{
		"id":                      job.ID,
		"operation_run_id":        nullableStringValue(job.OperationRunID),
		"tool_name":               job.ToolName,
		"status":                  job.Status,
		"payload":                 mapFromAny(job.Payload.Data),
		"result":                  mapFromAny(job.Result.Data),
		"error":                   job.Error,
		"required_capabilities":   []string(job.RequiredCapabilities),
		"preferred_node_kind":     job.PreferredNodeKind,
		"assigned_worker_node_id": nullableStringValue(job.AssignedWorkerNodeID),
		"claimed_at":              nullableTimeAny(job.ClaimedAt),
		"started_at":              nullableTimeAny(job.StartedAt),
		"finished_at":             nullableTimeAny(job.FinishedAt),
		"created_at":              job.CreatedAt,
		"updated_at":              job.UpdatedAt,
	}
}

func (w *ControlWorker) autoRecordProjectVersionValidationSnapshotAfterRefresh(ctx context.Context, operationID, operationStatus string) {
	if w == nil || w.store == nil || w.store.Gorm == nil {
		return
	}
	var op GormOperationRun
	err := w.store.Gorm.WithContext(ctx).First(&op, &GormOperationRun{GormBase: GormBase{ID: operationID}}).Error
	if err != nil {
		if errorsIsRecordNotFound(err) {
			return
		}
		if w.log != nil {
			w.log.Warn("project version validation auto-record lookup failed", "operation_id", operationID, "status", operationStatus, "error", err)
		}
		return
	}
	if op.OperationType != "git.refs.refresh" && op.OperationType != "github.actions.sync" && op.OperationType != "argo.apps.sync" {
		return
	}
	versionID := cleanOptionalID(stringFromMap(mapFromAny(op.Input.Data), "project_version_id"))
	if versionID == "" || versionID == "<nil>" {
		return
	}
	result, err := RecordProjectVersionValidationSnapshot(ctx, w.store, ProjectVersionValidationSnapshotOptions{
		ProjectVersionID:       versionID,
		RequireRecordedRefresh: true,
		RecordingTrigger:       "control_worker_refresh_completion",
	})
	if err != nil {
		if w.log != nil {
			w.log.Warn("project version validation auto-record failed", "operation_id", operationID, "project_version_id", versionID, "status", operationStatus, "error", err)
		}
		return
	}
	if w.log != nil {
		w.log.Debug("project version validation auto-record checked", "operation_id", operationID, "project_version_id", versionID, "status", operationStatus, "recording_state", result["recording_state"], "snapshot_written", result["validation_snapshot_written"])
	}
}

func (w *ControlWorker) refreshCanonicalAssetsAfterOperation(ctx context.Context, job map[string]any, operationID, status string) {
	if canonicalAssetsSyncedInAdapterTransaction(job) {
		return
	}
	result, err := w.store.SyncCanonicalAssets(ctx)
	if err != nil {
		if w.log != nil {
			w.log.Warn("canonical asset refresh failed after operation", "operation_id", operationID, "status", status, "error", err)
		}
		return
	}
	if w.log != nil {
		w.log.Debug("canonical assets refreshed after operation", "operation_id", operationID, "status", status, "synced_assets", result.SyncedAssets, "inserted_relations", result.InsertedRelations, "pruned_relations", result.PrunedRelations)
	}
}

func canonicalAssetsSyncedInAdapterTransaction(job map[string]any) bool {
	tool, _ := job["tool_name"].(string)
	switch tool {
	case "repo.sync", "repo.sync_remote", "git.refs.refresh", "repo.tag", "repo.create_tag", "repo.tag.lookup", "ssh.exec", "ssh.verify", "argo.apps.sync", "argo.pod_logs", "argo.pod_restart", "github.actions.sync", "github.labels.sync", "project.create_from_template", "project.template_provision_retry", "agent.execute", "config.git_commit", "project_version.validation_rerun":
		return true
	default:
		return false
	}
}

func (w *ControlWorker) sweepExpiredApprovals(ctx context.Context) error {
	return w.server.expirePendingOperationApprovalsGorm(ctx, w.store.Gorm)
}

func (w *ControlWorker) dispatchDueApprovalReminders(ctx context.Context) error {
	return w.server.dispatchDueOperationApprovalReminders(ctx)
}

func (w *ControlWorker) dispatchDueApprovalEscalations(ctx context.Context) error {
	return w.server.dispatchDueOperationApprovalEscalations(ctx)
}

func operationRunResult(job map[string]any, result map[string]any) map[string]any {
	toolName := fmt.Sprint(job["tool_name"])
	if toolName != "ssh.exec" && toolName != "ssh.verify" {
		return result
	}
	safe := map[string]any{
		"adapter": true,
		"tool":    toolName,
		"message": "ssh command output is available only through the SSH command run API",
	}
	for _, key := range []string{"exit_code", "duration_ms", "error"} {
		if value, ok := result[key]; ok {
			safe[key] = value
		}
	}
	return safe
}

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
		templateResult, err := w.executeProjectTemplateRun(ctx, opID)
		for key, value := range templateResult {
			result[key] = value
		}
		return result, err
	case "project.template_provision_retry":
		templateResult, err := w.executeProjectTemplateProvisionRetry(ctx, opID)
		for key, value := range templateResult {
			result[key] = value
		}
		return result, err
	case "agent.execute":
		return w.executeAgentTaskAudit(ctx, opID, result)
	default:
		return result, nil
	}
}

func (w *ControlWorker) recordAdapterFailure(ctx context.Context, rawTx any, job map[string]any, result map[string]any, adapterErr error) error {
	tx, err := workerGormTx(rawTx)
	if err != nil {
		return err
	}
	opID, _ := job["operation_run_id"].(string)
	tool, _ := job["tool_name"].(string)
	stdout, stderr := gitExecutionOutputFromMap(result)
	switch tool {
	case "repo.sync", "repo.sync_remote":
		if err := w.recordRepoSyncRunFailedGorm(ctx, opID, stdout, stderr, adapterErr.Error()); err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for failed repo sync: %w", err)
		}
		return nil
	case "git.refs.refresh":
		if err := w.updateOperationRemoteSyncStatusGorm(ctx, opID, "failed", ""); err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for failed Git ref refresh: %w", err)
		}
		return nil
	case "repo.tag", "repo.create_tag":
		return w.recordRepoTagRunFailedGorm(ctx, opID, "", stdout, stderr, adapterErr.Error())
	case "repo.tag.lookup":
		safeError := sanitizeLookupError(adapterErr)
		op, err := operationRunByID(ctx, w.store.Gorm, opID)
		if err != nil {
			return err
		}
		if err := w.recordRepoTagRunFailedGorm(ctx, "", cleanOptionalID(stringFromMap(mapFromAny(op.Input.Data), "repo_tag_run_id")), "", "", safeError); err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for failed repo tag lookup: %w", err)
		}
		return nil
	case "github.actions.sync":
		remoteID, _ := result["remote_id"].(string)
		if remoteID == "" {
			op, err := operationRunByID(ctx, w.store.Gorm, opID)
			if err != nil {
				return err
			}
			remoteID = cleanOptionalID(op.GitRemoteID.String)
			if remoteID == "" {
				if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
					return fmt.Errorf("syncing canonical assets for failed GitHub Actions sync without remote: %w", err)
				}
				return nil
			}
		}
		if err := w.store.Gorm.WithContext(ctx).Where(&GormGitHubActionRun{GitRemoteID: remoteID}).Delete(&GormGitHubActionRun{}).Error; err != nil {
			return err
		}
		if err := w.store.Gorm.WithContext(ctx).Model(&GormGitRemote{}).Where(&GormGitRemote{GormBase: GormBase{ID: remoteID}}).Updates(map[string]any{"last_sync_status": "failed"}).Error; err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for failed GitHub Actions sync: %w", err)
		}
		return nil
	case "github.labels.sync":
		remoteID, _ := result["remote_id"].(string)
		if remoteID == "" {
			op, err := operationRunByID(ctx, w.store.Gorm, opID)
			if err != nil {
				return err
			}
			remoteID = cleanOptionalID(op.GitRemoteID.String)
			if remoteID == "" {
				if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
					return fmt.Errorf("syncing canonical assets for failed GitHub labels sync without remote: %w", err)
				}
				return nil
			}
		}
		if err := w.store.Gorm.WithContext(ctx).Where(&GormGitHubRepositoryLabel{GitRemoteID: remoteID}).Delete(&GormGitHubRepositoryLabel{}).Error; err != nil {
			return err
		}
		if err := w.store.Gorm.WithContext(ctx).Model(&GormGitRemote{}).Where(&GormGitRemote{GormBase: GormBase{ID: remoteID}}).Updates(map[string]any{"last_sync_status": "failed"}).Error; err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for failed GitHub labels sync: %w", err)
		}
		return nil
	case "argo.apps.sync":
		connectionID := argoConnectionIDFromResult(result)
		delete(result, "_argo_sync_result")
		if connectionID == "" {
			op, err := operationRunByID(ctx, w.store.Gorm, opID)
			if err != nil {
				return err
			}
			connectionID = cleanOptionalID(stringFromMap(mapFromAny(op.Input.Data), "argo_connection_id"))
		}
		if connectionID != "" {
			if err := w.store.Gorm.WithContext(ctx).Model(&GormArgoConnection{}).Where(&GormArgoConnection{GormBase: GormBase{ID: connectionID}}).Updates(map[string]any{"last_sync_status": "failed", "last_sync_error": adapterErr.Error()}).Error; err != nil {
				return err
			}
		}
		if connectionID == "" {
			if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
				return fmt.Errorf("syncing canonical assets for failed Argo app sync: %w", err)
			}
			return nil
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for failed Argo app sync: %w", err)
		}
		return nil
	case "argo.pod_logs":
		safeError := "pod log audit worker failed; details are withheld from operation logs"
		backend := cleanPreviewString(result["live_log_backend"])
		if backend == "" {
			backend = "disabled"
		}
		if err := w.recordOperationLogGorm(ctx, opID, job, "error", "pod log audit worker failed", map[string]any{
			"error":                   safeError,
			"live_log_backend":        backend,
			"backend_state":           cleanPreviewString(result["backend_state"]),
			"kubectl_command_invoked": boolOnlyFromAny(result["kubectl_command_invoked"]),
			"kubernetes_api_call":     boolOnlyFromAny(result["kubernetes_api_call"]),
			"log_body_included":       false,
		}); err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for failed Argo pod log audit: %w", err)
		}
		return nil
	case "argo.pod_restart":
		safeError := "pod restart worker failed; details are withheld from operation logs"
		backend := cleanPreviewString(result["backend"])
		if backend == "" {
			backend = "disabled"
		}
		if err := w.recordOperationLogGorm(ctx, opID, job, "error", "pod restart worker failed", map[string]any{
			"error":                   safeError,
			"restart_backend":         backend,
			"backend_state":           cleanPreviewString(result["backend_state"]),
			"kubectl_command_invoked": boolOnlyFromAny(result["kubectl_command_invoked"]),
			"kubernetes_api_call":     boolOnlyFromAny(result["kubernetes_api_call"]),
			"rollout_restart_invoked": boolOnlyFromAny(result["rollout_restart_invoked"]),
			"raw_response_included":   false,
			"stdout_included":         false,
			"stderr_included":         false,
		}); err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for failed Argo pod restart: %w", err)
		}
		return nil
	case "config.git_commit":
		safeError := "config git workflow worker failed; details are withheld from operation logs"
		if err := w.recordOperationLogGorm(ctx, opID, job, "error", "config git workflow worker failed", map[string]any{
			"error":                 safeError,
			"git_write_performed":   false,
			"external_call_made":    false,
			"file_content_included": false,
			"secret_included":       false,
		}); err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for failed config Git workflow audit: %w", err)
		}
		return nil
	case "project_version.validation_rerun":
		safeError := "project version validation rerun worker failed; details are withheld from operation logs"
		if err := w.recordOperationLogGorm(ctx, opID, job, "error", "project version validation rerun worker failed", map[string]any{
			"error":                          safeError,
			"validation_source":              "local_synced_database_state",
			"external_call_made":             false,
			"provider_api_called":            false,
			"git_fetch_performed":            false,
			"argocd_api_called":              false,
			"raw_provider_response_recorded": false,
			"secret_included":                false,
		}); err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for failed project version validation rerun: %w", err)
		}
		return nil
	case "ssh.exec", "ssh.verify":
		stdout, stderr := gitExecutionOutputFromMap(result)
		exitCode := nullableIntFromMap(result, "exit_code")
		if err := w.store.Gorm.WithContext(ctx).Model(&GormSSHCommandRun{}).
			Where(&GormSSHCommandRun{OperationRunID: validNullString(opID)}).
			Updates(map[string]any{"status": "failed", "exit_code": exitCode, "stdout": stdout, "stderr": stderr, "error_message": adapterErr.Error(), "finished_at": validNullTime(time.Now())}).Error; err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for failed SSH command: %w", err)
		}
		return nil
	case "project.create_from_template":
		if result["_template_run_completion_pending"] == true || result["_template_run_failure_recorded"] == true {
			delete(result, "_template_run_completion_pending")
			delete(result, "_template_run_failure_recorded")
			return nil
		}
		stepsValue := mapFromAny(result)["steps"]
		if !hasTemplateSteps(stepsValue) {
			var run GormProjectTemplateRun
			runErr := w.store.Gorm.WithContext(ctx).Where(&GormProjectTemplateRun{OperationRunID: validNullString(opID)}).First(&run).Error
			if runErr != nil {
				return runErr
			}
			stepsValue = mapSliceFromAny(run.Steps.Data)
		}
		if err := w.store.Gorm.WithContext(ctx).Model(&GormProjectTemplateRun{}).
			Where(&GormProjectTemplateRun{OperationRunID: validNullString(opID)}).
			Updates(map[string]any{"status": "failed", "steps": JSONValue{Data: templateStepsWithStatus(stepsValue, "failed")}, "error_message": adapterErr.Error(), "finished_at": validNullTime(time.Now())}).Error; err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for failed project template creation: %w", err)
		}
		return nil
	case "project.template_provision_retry":
		if result["_template_retry_recorded"] == true {
			delete(result, "_template_retry_recorded")
			return nil
		}
		op, err := operationRunByID(ctx, w.store.Gorm, opID)
		if err != nil {
			return err
		}
		runID := cleanOptionalID(stringFromMap(mapFromAny(op.Input.Data), "project_template_run_id"))
		if runID != "" {
			if err := w.store.Gorm.WithContext(ctx).Model(&GormProjectTemplateRun{}).
				Where(&GormProjectTemplateRun{GormBase: GormBase{ID: runID}}).
				Updates(map[string]any{"status": "failed", "error_message": adapterErr.Error(), "finished_at": validNullTime(time.Now())}).Error; err != nil {
				return err
			}
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for failed project template provision retry: %w", err)
		}
		return nil
	case "agent.execute":
		if err := w.store.Gorm.WithContext(ctx).Model(&GormAgentToolCall{}).
			Where(&GormAgentToolCall{OperationRunID: validNullString(opID)}).
			Where("status IN ?", []string{"queued", "planned", "running"}).
			Updates(map[string]any{"status": "failed", "error_message": adapterErr.Error(), "finished_at": validNullTime(time.Now())}).Error; err != nil {
			return err
		}
		op, err := operationRunByID(ctx, w.store.Gorm, opID)
		if err != nil {
			return err
		}
		if taskID := cleanOptionalID(stringFromMap(mapFromAny(op.Input.Data), "agent_task_id")); taskID != "" {
			if err := w.store.Gorm.WithContext(ctx).Model(&GormAgentTask{}).
				Where(&GormAgentTask{GormBase: GormBase{ID: taskID}}).
				Where("status IN ?", []string{"queued", "running"}).
				Updates(map[string]any{"status": "failed"}).Error; err != nil {
				return err
			}
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for failed agent execution: %w", err)
		}
		return nil
	}
	return nil
}

func workerGormTx(rawTx any) (*gorm.DB, error) {
	if tx, ok := rawTx.(*gorm.DB); ok && tx != nil {
		return tx, nil
	}
	return nil, fmt.Errorf("gorm transaction is required")
}

func (w *ControlWorker) recordAdapterSuccess(ctx context.Context, rawTx any, job map[string]any, result map[string]any) error {
	tx, err := workerGormTx(rawTx)
	if err != nil {
		return err
	}
	opID, _ := job["operation_run_id"].(string)
	tool, _ := job["tool_name"].(string)
	stdout, stderr := gitExecutionOutputFromMap(result)
	afterSHA, _ := result["after_sha"].(string)
	switch tool {
	case "repo.sync", "repo.sync_remote":
		if err := w.recordRepoSyncRunCompletedGorm(ctx, opID, stdout, stderr, afterSHA); err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for completed repo sync: %w", err)
		}
		return nil
	case "git.refs.refresh":
		if err := w.updateOperationRemoteSyncStatusGorm(ctx, opID, "completed", afterSHA); err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for completed Git ref refresh: %w", err)
		}
		return nil
	case "repo.tag", "repo.create_tag":
		if err := w.recordRepoTagRunCompletedGorm(ctx, opID, stdout, stderr, afterSHA); err != nil {
			return err
		}
		if err := w.enqueueRepoTagPostSuccessOperationsGorm(ctx, opID); err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for completed repo tag: %w", err)
		}
		return nil
	case "repo.tag.lookup":
		afterSHA, _ := result["matched_sha"].(string)
		found := boolOnlyFromAny(result["remote_tag_found"])
		op, err := operationRunByID(ctx, w.store.Gorm, opID)
		if err != nil {
			return err
		}
		if err := w.recordRepoTagLookupCompletedGorm(ctx, cleanOptionalID(stringFromMap(mapFromAny(op.Input.Data), "repo_tag_run_id")), found, afterSHA); err != nil {
			return err
		}
		result["repo_tag_run_update_performed"] = true
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for completed repo tag lookup: %w", err)
		}
		return nil
	case "github.actions.sync":
		if err := w.recordGitHubActionsAdapterRun(ctx, tx, opID, result); err != nil {
			return err
		}
		remoteID, _ := result["remote_id"].(string)
		if remoteID == "" {
			return nil
		}
		if err := tx.WithContext(ctx).Model(&GormGitRemote{}).
			Where(&GormGitRemote{GormBase: GormBase{ID: remoteID}}).
			Updates(map[string]any{"last_sync_status": "completed"}).Error; err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for GitHub Actions sync: %w", err)
		}
		return nil
	case "github.labels.sync":
		if err := w.recordGitHubRepositoryLabelsAdapterRun(ctx, tx, opID, result); err != nil {
			return err
		}
		remoteID, _ := result["remote_id"].(string)
		if remoteID == "" {
			return nil
		}
		if err := tx.WithContext(ctx).Model(&GormGitRemote{}).
			Where(&GormGitRemote{GormBase: GormBase{ID: remoteID}}).
			Updates(map[string]any{"last_sync_status": "completed"}).Error; err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for GitHub labels sync: %w", err)
		}
		return nil
	case "argo.apps.sync":
		return w.recordArgoSyncAdapterRun(ctx, tx, result)
	case "argo.pod_logs":
		fields := map[string]any{
			"deployment_target_id":          result["deployment_target_id"],
			"pod_name":                      result["pod_name"],
			"container_name":                result["container_name"],
			"namespace":                     result["namespace"],
			"cluster_name":                  result["cluster_name"],
			"result_scope":                  result["result_scope"],
			"line_count":                    result["line_count"],
			"truncated":                     result["truncated"],
			"backend_state":                 result["backend_state"],
			"live_log_backend":              result["live_log_backend"],
			"kubeconfig_bound":              result["kubeconfig_bound"],
			"kubeconfig_secret_ref_present": result["kubeconfig_secret_ref_present"],
			"kubernetes_api_call":           result["kubernetes_api_call"],
			"kubectl_command_invoked":       result["kubectl_command_invoked"],
			"log_stream_opened":             result["log_stream_opened"],
			"log_body_included":             false,
			"raw_response_included":         false,
		}
		if err := w.recordOperationLogGorm(ctx, opID, job, "info", "pod log audit completed with sanitized metadata", fields); err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for completed Argo pod log audit: %w", err)
		}
		return nil
	case "argo.pod_restart":
		fields := map[string]any{
			"deployment_target_id":    result["deployment_target_id"],
			"deployment_name":         result["deployment_name"],
			"namespace":               result["namespace"],
			"cluster_name":            result["cluster_name"],
			"result_scope":            result["result_scope"],
			"backend_state":           result["backend_state"],
			"restart_backend":         result["backend"],
			"kubeconfig_bound":        result["kubeconfig_bound"],
			"kubernetes_api_call":     result["kubernetes_api_call"],
			"kubectl_command_invoked": result["kubectl_command_invoked"],
			"rbac_can_i_checked":      result["rbac_can_i_checked"],
			"server_dry_run_checked":  result["server_dry_run_checked"],
			"rollout_restart_invoked": result["rollout_restart_invoked"],
			"log_body_included":       false,
			"raw_response_included":   false,
			"stdout_included":         false,
			"stderr_included":         false,
		}
		if err := w.recordOperationLogGorm(ctx, opID, job, "info", "pod restart completed with sanitized metadata", fields); err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for completed Argo pod restart: %w", err)
		}
		return nil
	case "config.git_commit":
		if sha := strings.TrimSpace(fmt.Sprint(result["config_commit_sha_internal"])); sha != "" && sha != "<nil>" {
			if err := tx.WithContext(ctx).Model(&GormGitRemote{}).
				Where(&GormGitRemote{GormBase: GormBase{ID: cleanOptionalID(fmt.Sprint(result["config_remote_id"]))}, ProjectGitRepositoryID: cleanOptionalID(fmt.Sprint(result["project_git_repository_id"]))}).
				Updates(map[string]any{"latest_sha": sha, "last_sync_status": "synced"}).Error; err != nil {
				delete(result, "config_commit_sha_internal")
				return fmt.Errorf("updating config remote synced state: %w", err)
			}
		}
		delete(result, "config_commit_sha_internal")
		fields := map[string]any{
			"result_scope":                   result["result_scope"],
			"project_git_repository_id":      result["project_git_repository_id"],
			"config_remote_id":               result["config_remote_id"],
			"provider_type":                  result["provider_type"],
			"scaffold_file_count":            result["scaffold_file_count"],
			"remote_count":                   result["remote_count"],
			"git_write_performed":            boolOnlyFromAny(result["git_write_performed"]),
			"git_clone_performed":            boolOnlyFromAny(result["git_clone_performed"]),
			"git_fetch_performed":            boolOnlyFromAny(result["git_fetch_performed"]),
			"file_content_materialized":      boolOnlyFromAny(result["file_content_materialized"]),
			"secret_scan_performed":          boolOnlyFromAny(result["secret_scan_performed"]),
			"git_commit_created":             boolOnlyFromAny(result["git_commit_created"]),
			"git_push_performed":             boolOnlyFromAny(result["git_push_performed"]),
			"commit_sha_present":             boolOnlyFromAny(result["commit_sha_present"]),
			"commit_sha_included":            false,
			"external_call_made":             boolOnlyFromAny(result["external_call_made"]),
			"file_content_included":          false,
			"secret_included":                false,
			"project_version_pin_written":    false,
			"live_commit_validation":         result["live_commit_validation"],
			"raw_git_output_recorded":        false,
			"raw_provider_response_recorded": false,
		}
		message := "config git workflow completed with sanitized metadata"
		if !boolOnlyFromAny(result["git_write_performed"]) {
			message = "config git workflow audit completed without Git mutation"
		}
		if err := w.recordOperationLogGorm(ctx, opID, job, "info", message, fields); err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for completed config Git workflow audit: %w", err)
		}
		return nil
	case "project_version.validation_rerun":
		fields := map[string]any{
			"project_version_id":             result["project_version_id"],
			"recording_state":                result["recording_state"],
			"validation_snapshot_written":    result["validation_snapshot_written"],
			"asset_status_snapshot_written":  result["asset_status_snapshot_written"],
			"validation_source":              "local_synced_database_state",
			"external_call_made":             false,
			"provider_api_called":            false,
			"git_fetch_performed":            false,
			"argocd_api_called":              false,
			"raw_provider_response_recorded": false,
			"secret_included":                false,
		}
		if err := w.recordOperationLogGorm(ctx, opID, job, "info", "project version validation rerun completed from local synced state", fields); err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for completed project version validation rerun: %w", err)
		}
		return nil
	case "ssh.exec", "ssh.verify":
		stdout, stderr := gitExecutionOutputFromMap(result)
		exitCode := nullableIntFromMap(result, "exit_code")
		if err := w.store.Gorm.WithContext(ctx).Model(&GormSSHCommandRun{}).
			Where(&GormSSHCommandRun{OperationRunID: validNullString(opID)}).
			Updates(map[string]any{"status": "completed", "exit_code": exitCode, "stdout": stdout, "stderr": stderr, "finished_at": validNullTime(time.Now())}).Error; err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for completed SSH command: %w", err)
		}
		return nil
	case "project.create_from_template":
		if result["_template_run_recorded"] == true {
			delete(result, "_template_run_recorded")
			return nil
		}
		return fmt.Errorf("project template operation was not recorded by GORM execution path")
	case "project.template_provision_retry":
		if result["_template_retry_recorded"] == true {
			delete(result, "_template_retry_recorded")
		}
		return nil
	case "agent.execute":
		output := map[string]any{
			"message": "agent execution audit completed; first-version mutation is disabled",
			"result":  result,
		}
		if err := w.store.Gorm.WithContext(ctx).Transaction(func(gormTx *gorm.DB) error {
			var calls []GormAgentToolCall
			if err := gormTx.Where(&GormAgentToolCall{OperationRunID: validNullString(opID)}).Where(gormField("status", []string{"queued", "planned", "running"})).Find(&calls).Error; err != nil {
				return err
			}
			for i := range calls {
				calls[i].Status = "completed"
				merged := mapFromAny(calls[i].Output.Data)
				for key, value := range output {
					merged[key] = value
				}
				calls[i].Output = JSONValue{Data: merged}
				calls[i].FinishedAt = validNullTime(time.Now())
				if err := gormTx.Save(&calls[i]).Error; err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
		op, err := operationRunByID(ctx, w.store.Gorm, opID)
		if err != nil {
			return err
		}
		if taskID := cleanOptionalID(stringFromMap(mapFromAny(op.Input.Data), "agent_task_id")); taskID != "" {
			if err := w.store.Gorm.WithContext(ctx).Model(&GormAgentTask{}).
				Where(&GormAgentTask{GormBase: GormBase{ID: taskID}}).
				Where("status IN ?", []string{"queued", "running"}).
				Updates(map[string]any{"status": "executed"}).Error; err != nil {
				return err
			}
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for completed agent execution: %w", err)
		}
		return nil
	default:
		return nil
	}
}

func (w *ControlWorker) executeArgoPodLogAudit(ctx context.Context, opID string, result map[string]any) (map[string]any, error) {
	op, err := operationRunMapByID(ctx, w.store.Gorm, opID)
	if err != nil {
		return result, fmt.Errorf("loading pod log operation: %w", err)
	}
	input := mapFromAny(op["input"])
	targetID := cleanOptionalID(fmt.Sprint(input["deployment_target_id"]))
	podName := cleanOptionalText(fmt.Sprint(input["pod_name"]))
	namespace := cleanOptionalText(fmt.Sprint(input["namespace"]))
	clusterName := cleanOptionalText(fmt.Sprint(input["cluster_name"]))
	if targetID == "" || podName == "" || namespace == "" || clusterName == "" {
		return result, fmt.Errorf("pod log audit operation is missing target metadata")
	}
	result["deployment_target_id"] = targetID
	result["project_id"] = cleanOptionalID(fmt.Sprint(input["project_id"]))
	result["deployment_target_name"] = cleanOptionalText(fmt.Sprint(input["deployment_target_name"]))
	result["environment"] = cleanOptionalText(fmt.Sprint(input["environment"]))
	result["cluster_name"] = clusterName
	result["namespace"] = namespace
	result["pod_name"] = podName
	result["container_name"] = cleanOptionalText(fmt.Sprint(input["container_name"]))
	result["tail_lines"] = intFromAny(input["tail_lines"], 200)
	result["since_seconds"] = intFromAny(input["since_seconds"], 0)
	kubernetesEnv, err := loadKubernetesEnvironmentForPodLogs(ctx, w.store.Gorm, result)
	if err != nil {
		if w.cfg.KubernetesPodLogsEnabled {
			return result, err
		}
	}
	kubeconfigRef := ""
	if kubernetesEnv != nil {
		result["kubernetes_environment_id"] = cleanOptionalID(fmt.Sprint(kubernetesEnv["id"]))
		result["kubernetes_environment_name"] = cleanOptionalText(fmt.Sprint(kubernetesEnv["name"]))
		result["kubernetes_environment_status"] = cleanOptionalText(fmt.Sprint(kubernetesEnv["status"]))
		result["kubeconfig_secret_ref_present"] = cleanOptionalText(fmt.Sprint(kubernetesEnv["kubeconfig_secret_ref"])) != ""
		kubeconfigRef = cleanOptionalText(fmt.Sprint(kubernetesEnv["kubeconfig_secret_ref"]))
	}
	req := kubernetesPodLogRequest{
		ProjectID:          cleanOptionalID(fmt.Sprint(input["project_id"])),
		DeploymentTargetID: targetID,
		Environment:        cleanOptionalText(fmt.Sprint(input["environment"])),
		ClusterName:        clusterName,
		Namespace:          namespace,
		PodName:            podName,
		ContainerName:      cleanOptionalText(fmt.Sprint(input["container_name"])),
		TailLines:          intFromAny(input["tail_lines"], 200),
		SinceSeconds:       intFromAny(input["since_seconds"], 0),
		KubeconfigRef:      kubeconfigRef,
	}
	if w.cfg.KubernetesPodLogsEnabled {
		if err := validateKubernetesPodLogRequest(req); err != nil {
			return result, err
		}
	}
	liveResult, err := runKubernetesPodLogs(ctx, w.cfg, req)
	copySafeArgoPodLogLiveResult(result, liveResult)
	result["kubernetes_client_created"] = false
	result["argocd_api_call"] = false
	result["log_body_included"] = false
	result["redacted_log_body_included"] = boolOnlyFromAny(liveResult["redacted_log_body_included"])
	result["raw_response_included"] = false
	result["secret_included"] = false
	return result, err
}

func copySafeArgoPodLogLiveResult(result, liveResult map[string]any) {
	for _, key := range []string{
		"deployment_target_id",
		"environment",
		"cluster_name",
		"namespace",
		"pod_name",
		"container_name",
		"tail_lines",
		"since_seconds",
		"kubeconfig_secret_ref_present",
		"kubeconfig_secret_read",
		"kubeconfig_bound",
		"backend_state",
		"live_log_backend",
		"live_backend_ready",
		"kubernetes_api_call",
		"kubectl_command_invoked",
		"log_stream_opened",
		"result_scope",
		"line_count",
		"truncated",
		"preview_line_count",
		"preview_truncated",
		"redaction_performed",
		"redacted_log_preview",
		"redacted_log_body_included",
		"started_at",
		"finished_at",
		"message",
		"prerequisite_state",
		"missing_evidence",
		"blockers",
		"disabled_backends",
		"suppressed_fields",
	} {
		if value, ok := liveResult[key]; ok {
			result[key] = value
		}
	}
}

func (w *ControlWorker) executeArgoPodRestart(ctx context.Context, opID string, result map[string]any) (map[string]any, error) {
	op, err := operationRunMapByID(ctx, w.store.Gorm, opID)
	if err != nil {
		return result, fmt.Errorf("loading pod restart operation: %w", err)
	}
	input := mapFromAny(op["input"])
	targetID := cleanOptionalID(fmt.Sprint(input["deployment_target_id"]))
	deploymentName := cleanOptionalText(fmt.Sprint(input["deployment_name"]))
	namespace := cleanOptionalText(fmt.Sprint(input["namespace"]))
	clusterName := cleanOptionalText(fmt.Sprint(input["cluster_name"]))
	if targetID == "" || deploymentName == "" || namespace == "" || clusterName == "" {
		return result, fmt.Errorf("pod restart operation is missing target metadata")
	}
	result["deployment_target_id"] = targetID
	result["project_id"] = cleanOptionalID(fmt.Sprint(input["project_id"]))
	result["deployment_target_name"] = cleanOptionalText(fmt.Sprint(input["deployment_target_name"]))
	result["environment"] = cleanOptionalText(fmt.Sprint(input["environment"]))
	result["cluster_name"] = clusterName
	result["namespace"] = namespace
	result["deployment_name"] = deploymentName
	req := kubernetesPodRestartRequest{
		ProjectID:          cleanOptionalID(fmt.Sprint(input["project_id"])),
		DeploymentTargetID: targetID,
		Environment:        cleanOptionalText(fmt.Sprint(input["environment"])),
		ClusterName:        clusterName,
		Namespace:          namespace,
		DeploymentName:     deploymentName,
	}
	if !w.cfg.KubernetesRestartsEnabled {
		liveResult, err := runKubernetesPodRestart(ctx, w.cfg, req)
		for key, value := range liveResult {
			result[key] = value
		}
		result["argocd_api_call"] = false
		result["log_body_included"] = false
		result["raw_response_included"] = false
		result["stdout_included"] = false
		result["stderr_included"] = false
		result["secret_included"] = false
		return result, err
	}
	if err := ensureNoActiveKubernetesPodRestart(ctx, w.store.Gorm, opID, result); err != nil {
		return result, err
	}
	kubernetesEnv, err := loadKubernetesEnvironmentForPodRestart(ctx, w.store.Gorm, result)
	if err != nil {
		return result, err
	}
	kubeconfigRef := ""
	if kubernetesEnv != nil {
		result["kubernetes_environment_id"] = cleanOptionalID(fmt.Sprint(kubernetesEnv["id"]))
		result["kubernetes_environment_name"] = cleanOptionalText(fmt.Sprint(kubernetesEnv["name"]))
		result["kubernetes_environment_status"] = cleanOptionalText(fmt.Sprint(kubernetesEnv["status"]))
		result["kubeconfig_secret_ref_present"] = cleanOptionalText(fmt.Sprint(kubernetesEnv["kubeconfig_secret_ref"])) != ""
		kubeconfigRef = cleanOptionalText(fmt.Sprint(kubernetesEnv["kubeconfig_secret_ref"]))
	}
	req.KubeconfigRef = kubeconfigRef
	if err := validateKubernetesPodRestartRequest(req); err != nil {
		return result, err
	}
	liveResult, err := runKubernetesPodRestart(ctx, w.cfg, req)
	for key, value := range liveResult {
		result[key] = value
	}
	result["argocd_api_call"] = false
	result["log_body_included"] = false
	result["raw_response_included"] = false
	result["stdout_included"] = false
	result["stderr_included"] = false
	result["secret_included"] = false
	return result, err
}

func operationRunMapByID(ctx context.Context, db *gorm.DB, opID string) (map[string]any, error) {
	if db == nil {
		return nil, fmt.Errorf("gorm database is not configured")
	}
	var op GormOperationRun
	if err := db.WithContext(ctx).First(&op, &GormOperationRun{GormBase: GormBase{ID: opID}}).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return map[string]any{
		"id":             op.ID,
		"project_id":     nullableStringValue(op.ProjectID),
		"git_remote_id":  nullableStringValue(op.GitRemoteID),
		"operation_type": op.OperationType,
		"status":         op.Status,
		"title":          op.Title,
		"input":          op.Input,
		"result":         op.Result,
		"error":          op.Error,
		"started_at":     nullableTimeAny(op.StartedAt),
		"finished_at":    nullableTimeAny(op.FinishedAt),
		"created_at":     op.CreatedAt,
		"updated_at":     op.UpdatedAt,
	}, nil
}

func agentToolCallStatusMapsByOperation(ctx context.Context, db *gorm.DB, opID string) ([]map[string]any, error) {
	if db == nil {
		return nil, fmt.Errorf("gorm database is not configured")
	}
	var calls []GormAgentToolCall
	if err := db.WithContext(ctx).
		Where(&GormAgentToolCall{OperationRunID: validNullString(opID)}).
		Order("created_at ASC").
		Find(&calls).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		items = append(items, map[string]any{"tool_name": call.ToolName, "status": call.Status})
	}
	return items, nil
}

func ensureNoActiveKubernetesPodRestart(ctx context.Context, db *gorm.DB, opID string, opResult map[string]any) error {
	projectID := cleanOptionalID(fmt.Sprint(opResult["project_id"]))
	environment := cleanOptionalText(fmt.Sprint(opResult["environment"]))
	clusterName := cleanOptionalText(fmt.Sprint(opResult["cluster_name"]))
	namespace := cleanOptionalText(fmt.Sprint(opResult["namespace"]))
	deploymentName := cleanOptionalText(fmt.Sprint(opResult["deployment_name"]))
	if projectID == "" || environment == "" || clusterName == "" || namespace == "" || deploymentName == "" {
		return fmt.Errorf("pod restart operation is missing concurrency guard metadata")
	}
	var runs []GormOperationRun
	if err := db.WithContext(ctx).Where(&GormOperationRun{ProjectID: validNullString(projectID), OperationType: "argo.pod_restart"}).Find(&runs).Error; err != nil {
		return fmt.Errorf("checking active pod restart operation: %w", err)
	}
	for _, run := range runs {
		if run.ID == opID || (run.Status != "queued" && run.Status != "running") {
			continue
		}
		input := mapFromAny(run.Input.Data)
		if cleanOptionalText(fmt.Sprint(input["environment"])) == environment &&
			cleanOptionalText(fmt.Sprint(input["cluster_name"])) == clusterName &&
			cleanOptionalText(fmt.Sprint(input["namespace"])) == namespace &&
			cleanOptionalText(fmt.Sprint(input["deployment_name"])) == deploymentName {
			return fmt.Errorf("another pod restart operation is already active for this deployment")
		}
	}
	return nil
}

func loadKubernetesEnvironmentForPodLogs(ctx context.Context, db *gorm.DB, opResult map[string]any) (map[string]any, error) {
	projectID := cleanOptionalID(fmt.Sprint(opResult["project_id"]))
	environment := cleanOptionalText(fmt.Sprint(opResult["environment"]))
	clusterName := cleanOptionalText(fmt.Sprint(opResult["cluster_name"]))
	namespace := cleanOptionalText(fmt.Sprint(opResult["namespace"]))
	if projectID == "" || environment == "" || clusterName == "" || namespace == "" {
		return nil, fmt.Errorf("pod log operation is missing Kubernetes environment binding metadata")
	}
	var kube GormKubernetesEnvironment
	if err := db.WithContext(ctx).Where(&GormKubernetesEnvironment{ProjectID: projectID, Environment: environment, ClusterName: clusterName, Namespace: namespace}).First(&kube).Error; err != nil {
		return nil, fmt.Errorf("loading Kubernetes environment for pod logs: %w", err)
	}
	env := map[string]any{
		"id":                          kube.ID,
		"name":                        kube.Name,
		"kubeconfig_secret_ref":       kube.KubeconfigSecretRef,
		"service_account":             kube.ServiceAccount,
		"token_subject_review_status": kube.TokenSubjectReviewStatus,
		"rbac_read_logs_status":       kube.RBACReadLogsStatus,
		"status":                      kube.Status,
	}
	if cleanPreviewString(env["status"]) != "ready" {
		return nil, fmt.Errorf("Kubernetes environment is not ready")
	}
	if cleanPreviewString(env["token_subject_review_status"]) != "reviewed" {
		return nil, fmt.Errorf("Kubernetes token subject review is not complete")
	}
	if cleanPreviewString(env["rbac_read_logs_status"]) != "reviewed" {
		return nil, fmt.Errorf("Kubernetes logs RBAC review is not complete")
	}
	return env, nil
}

func loadKubernetesEnvironmentForPodRestart(ctx context.Context, db *gorm.DB, opResult map[string]any) (map[string]any, error) {
	projectID := cleanOptionalID(fmt.Sprint(opResult["project_id"]))
	environment := cleanOptionalText(fmt.Sprint(opResult["environment"]))
	clusterName := cleanOptionalText(fmt.Sprint(opResult["cluster_name"]))
	namespace := cleanOptionalText(fmt.Sprint(opResult["namespace"]))
	if projectID == "" || environment == "" || clusterName == "" || namespace == "" {
		return nil, fmt.Errorf("pod restart operation is missing Kubernetes environment binding metadata")
	}
	var kube GormKubernetesEnvironment
	if err := db.WithContext(ctx).Where(&GormKubernetesEnvironment{ProjectID: projectID, Environment: environment, ClusterName: clusterName, Namespace: namespace}).First(&kube).Error; err != nil {
		return nil, fmt.Errorf("loading Kubernetes environment for pod restart: %w", err)
	}
	env := map[string]any{
		"id":                          kube.ID,
		"name":                        kube.Name,
		"kubeconfig_secret_ref":       kube.KubeconfigSecretRef,
		"service_account":             kube.ServiceAccount,
		"token_subject_review_status": kube.TokenSubjectReviewStatus,
		"rbac_restart_pods_status":    kube.PodRestartStatus,
		"status":                      kube.Status,
	}
	if cleanPreviewString(env["status"]) != "ready" {
		return nil, fmt.Errorf("Kubernetes environment is not ready")
	}
	if cleanPreviewString(env["token_subject_review_status"]) != "reviewed" {
		return nil, fmt.Errorf("Kubernetes token subject review is not complete")
	}
	if cleanPreviewString(env["rbac_restart_pods_status"]) != "reviewed" {
		return nil, fmt.Errorf("Kubernetes restart RBAC review is not complete")
	}
	return env, nil
}

func (w *ControlWorker) executeConfigGitWorkflowAudit(ctx context.Context, opID string, result map[string]any) (map[string]any, error) {
	// Audit-only entrypoint for the sanitized config Git workflow result shape.
	op, err := operationRunMapByID(ctx, w.store.Gorm, opID)
	if err != nil {
		return result, fmt.Errorf("loading config git workflow operation: %w", err)
	}
	input := mapFromAny(op["input"])
	repoID := cleanOptionalID(fmt.Sprint(input["project_git_repository_id"]))
	remoteID := cleanOptionalID(fmt.Sprint(input["config_remote_id"]))
	if repoID == "" {
		return result, fmt.Errorf("config git workflow operation is missing repository metadata")
	}
	result["result_scope"] = "sanitized_config_git_workflow_intent"
	result["project_git_repository_id"] = repoID
	result["config_remote_id"] = remoteID
	result["provider_type"] = cleanOptionalText(fmt.Sprint(input["provider_type"]))
	result["scaffold_file_count"] = intFromAny(input["scaffold_file_count"], 0)
	result["remote_count"] = intFromAny(input["remote_count"], 0)
	result["default_branch_configured"] = boolOnlyFromAny(input["default_branch_configured"])
	result["workflow_intent_recorded"] = true
	result["git_write_performed"] = false
	result["git_clone_performed"] = false
	result["git_fetch_performed"] = false
	result["file_content_materialized"] = false
	result["secret_scan_performed"] = false
	result["git_commit_created"] = false
	result["git_push_performed"] = false
	result["provider_review_created"] = false
	result["project_version_pin_written"] = false
	result["live_commit_validation"] = "disabled"
	result["external_call_made"] = false
	result["file_content_included"] = false
	result["secret_included"] = false
	result["raw_git_output_recorded"] = false
	result["raw_provider_response_recorded"] = false
	result["suppressed_fields"] = []string{"file_content", "secret_values", "git_credentials", "provider_token", "remote_url", "branch_name", "commit_message", "commit_sha", "provider_response_body", "provider_response_headers"}
	result["disabled_backends"] = []string{"git_clone", "git_fetch", "file_write", "git_commit", "git_push", "pull_request_create", "project_version_update", "live_commit_validation"}
	result["message"] = "config git workflow audit completed without Git mutation"
	return result, nil
}

func (w *ControlWorker) executeConfigGitWorkflow(ctx context.Context, opID string, result map[string]any) (map[string]any, error) {
	op, err := operationRunMapByID(ctx, w.store.Gorm, opID)
	if err != nil {
		return result, fmt.Errorf("loading config git workflow operation: %w", err)
	}
	input := mapFromAny(op["input"])
	if !w.cfg.ConfigGitLocalBareWritesEnabled ||
		!boolOnlyFromAny(input["local_bare_write_eligible"]) ||
		!strings.EqualFold(cleanOptionalText(fmt.Sprint(input["provider_type"])), "local_bare") {
		return w.executeConfigGitWorkflowAuditFromInput(input, result)
	}
	repoID := cleanOptionalID(fmt.Sprint(input["project_git_repository_id"]))
	remoteID := cleanOptionalID(fmt.Sprint(input["config_remote_id"]))
	if repoID == "" || remoteID == "" {
		return result, fmt.Errorf("config git workflow operation is missing local_bare repository metadata")
	}
	executor := NewGitExecutor("")
	executor.LocalBareBaseDirs = w.cfg.LocalBareBaseDirs
	execution, err := executor.CommitConfigScaffold(ctx, w.store.Gorm, repoID, remoteID)
	if err != nil {
		return result, err
	}
	details := map[string]any{}
	if execution != nil {
		details = execution.Details
	}
	result["result_scope"] = "sanitized_config_git_workflow_local_bare"
	result["project_git_repository_id"] = repoID
	result["config_remote_id"] = remoteID
	result["provider_type"] = "local_bare"
	result["scaffold_file_count"] = details["scaffold_file_count"]
	result["remote_count"] = intFromAny(input["remote_count"], 0)
	result["default_branch_configured"] = true
	result["workflow_intent_recorded"] = true
	result["git_write_performed"] = true
	result["git_clone_performed"] = true
	result["git_fetch_performed"] = boolOnlyFromAny(details["remote_existed"])
	result["file_content_materialized"] = true
	result["secret_scan_performed"] = true
	result["secret_scan_kind"] = "template_secret_marker_scan"
	result["git_commit_created"] = boolOnlyFromAny(details["git_commit_created"])
	result["git_push_performed"] = boolOnlyFromAny(details["git_push_performed"])
	result["provider_review_created"] = false
	result["project_version_pin_written"] = false
	result["live_commit_validation"] = "synced_state_updated"
	result["external_call_made"] = false
	result["file_content_included"] = false
	result["secret_included"] = false
	result["raw_git_output_recorded"] = false
	result["raw_provider_response_recorded"] = false
	result["commit_sha_present"] = execution != nil && execution.AfterSHA != ""
	if execution != nil && execution.AfterSHA != "" {
		result["config_commit_sha_internal"] = execution.AfterSHA
	}
	result["commit_sha_included"] = false
	result["suppressed_fields"] = []string{"file_content", "secret_values", "git_credentials", "provider_token", "remote_url", "branch_name", "commit_message", "commit_sha", "git_output", "provider_response_body", "provider_response_headers"}
	result["enabled_backends"] = []string{"local_bare_git_init", "file_write", "secret_scan", "git_commit", "git_push", "synced_state_update"}
	result["disabled_backends"] = []string{"pull_request_create", "project_version_update", "provider_review"}
	result["message"] = "config git workflow completed with sanitized local_bare Git metadata"
	return result, nil
}

func (w *ControlWorker) executeConfigGitWorkflowAuditFromInput(input map[string]any, result map[string]any) (map[string]any, error) {
	repoID := cleanOptionalID(fmt.Sprint(input["project_git_repository_id"]))
	remoteID := cleanOptionalID(fmt.Sprint(input["config_remote_id"]))
	if repoID == "" {
		return result, fmt.Errorf("config git workflow operation is missing repository metadata")
	}
	result["result_scope"] = "sanitized_config_git_workflow_intent"
	result["project_git_repository_id"] = repoID
	result["config_remote_id"] = remoteID
	result["provider_type"] = cleanOptionalText(fmt.Sprint(input["provider_type"]))
	result["scaffold_file_count"] = intFromAny(input["scaffold_file_count"], 0)
	result["remote_count"] = intFromAny(input["remote_count"], 0)
	result["default_branch_configured"] = boolOnlyFromAny(input["default_branch_configured"])
	result["workflow_intent_recorded"] = true
	result["git_write_performed"] = false
	result["git_clone_performed"] = false
	result["git_fetch_performed"] = false
	result["file_content_materialized"] = false
	result["secret_scan_performed"] = false
	result["git_commit_created"] = false
	result["git_push_performed"] = false
	result["provider_review_created"] = false
	result["project_version_pin_written"] = false
	result["live_commit_validation"] = "disabled"
	result["external_call_made"] = false
	result["file_content_included"] = false
	result["secret_included"] = false
	result["raw_git_output_recorded"] = false
	result["raw_provider_response_recorded"] = false
	result["suppressed_fields"] = []string{"file_content", "secret_values", "git_credentials", "provider_token", "remote_url", "branch_name", "commit_message", "commit_sha", "provider_response_body", "provider_response_headers"}
	result["disabled_backends"] = []string{"git_clone", "git_fetch", "file_write", "git_commit", "git_push", "pull_request_create", "project_version_update", "live_commit_validation"}
	result["message"] = "config git workflow audit completed without Git mutation"
	return result, nil
}

func (w *ControlWorker) executeProjectVersionValidationRerun(ctx context.Context, opID string, result map[string]any) (map[string]any, error) {
	op, err := operationRunMapByID(ctx, w.store.Gorm, opID)
	if err != nil {
		return result, fmt.Errorf("loading project version validation rerun operation: %w", err)
	}
	if cleanOptionalText(fmt.Sprint(op["operation_type"])) != "project_version.validation_rerun" {
		return result, ErrNotFound
	}
	input := mapFromAny(op["input"])
	versionID := cleanOptionalID(stringFromMap(input, "project_version_id"))
	if versionID == "" {
		return result, fmt.Errorf("project version validation rerun operation is missing project_version_id")
	}
	recording, err := RecordProjectVersionValidationSnapshot(ctx, w.store, ProjectVersionValidationSnapshotOptions{
		ProjectVersionID:       versionID,
		RequireRecordedRefresh: true,
		RecordingTrigger:       "standalone_background_validation_rerun",
	})
	if err != nil {
		return result, err
	}
	for key, value := range recording {
		result[key] = value
	}
	result["project_version_id"] = versionID
	result["operation_id"] = opID
	result["operation_result"] = recording
	result["validation_source"] = "local_synced_database_state"
	result["standalone_background_worker"] = true
	result["external_call_made"] = false
	result["provider_api_called"] = false
	result["git_fetch_performed"] = false
	result["argocd_api_called"] = false
	result["raw_provider_response_recorded"] = false
	result["secret_included"] = false
	result["suppressed_fields"] = []string{"remote_url", "provider_token", "authorization_header", "git_credentials", "raw_provider_response", "raw_git_output", "raw_argo_response", "workflow_logs", "commit_body"}
	return result, nil
}

func (w *ControlWorker) executeAgentTaskAudit(ctx context.Context, opID string, result map[string]any) (map[string]any, error) {
	op, err := operationRunMapByID(ctx, w.store.Gorm, opID)
	if err != nil {
		return result, fmt.Errorf("loading agent operation: %w", err)
	}
	input := mapFromAny(op["input"])
	taskID := strings.TrimSpace(fmt.Sprint(input["agent_task_id"]))
	if taskID == "" || taskID == "<nil>" {
		return result, fmt.Errorf("agent operation has no task id")
	}
	calls, err := agentToolCallStatusMapsByOperation(ctx, w.store.Gorm, opID)
	if err != nil {
		return result, fmt.Errorf("loading agent tool call audit: %w", err)
	}
	result["agent_task_id"] = taskID
	result["tool_call_count"] = len(calls)
	result["mutation_enabled"] = false
	result["message"] = "agent execution audit recorded; code mutation is disabled in this first version"
	result["tool_calls"] = calls
	return result, nil
}

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

func createTemplateRemoteGorm(ctx context.Context, store *Store, tx *gorm.DB, repo, item map[string]any) (map[string]any, error) {
	remoteKey := firstNonEmptyString(stringFromMap(item, "remote_key"), stringFromMap(item, "name"))
	name := firstNonEmptyString(stringFromMap(item, "name"), remoteKey)
	kind := firstNonEmptyString(stringFromMap(item, "kind"), stringFromMap(item, "provider_type"), "git")
	providerType := firstNonEmptyString(stringFromMap(item, "provider_type"), kind)
	remoteRole := firstNonEmptyString(stringFromMap(item, "remote_role"), stringFromMap(item, "role"), "mirror")
	defaultBranch := firstNonEmptyString(stringFromMap(item, "default_branch"), fmt.Sprint(repo["default_branch"]), "main")
	remoteURL := stringFromMap(item, "remote_url")
	urls := stringSliceFromAny(item["urls"])
	if remoteURL == "" && len(urls) > 0 {
		remoteURL = urls[0]
	}
	account, hasAccount, err := resolveTemplateProviderAccount(ctx, store, item, providerType)
	if err != nil {
		return nil, err
	}
	metadata := mapFromAny(item["metadata"])
	metadata["source"] = "project_template"
	metadata["template_placeholder"] = true
	sourceAccountID := sql.NullString{}
	if hasAccount {
		sourceAccountID = validNullString(account.ID)
		metadata["provider_account_id"] = account.ID
		metadata["provider_account_name"] = account.Name
		metadata["api_base_url"] = account.APIBaseURL
		metadata["token_env"] = account.TokenEnv
		if stringFromMap(metadata, "owner", "org") == "" && account.DefaultOwner != "" {
			metadata["owner"] = account.DefaultOwner
		}
		if stringFromMap(metadata, "visibility") == "" && account.Visibility != "" {
			metadata["visibility"] = account.Visibility
		}
	}
	remote := GormGitRemote{
		ProjectGitRepositoryID: cleanOptionalID(fmt.Sprint(repo["id"])),
		Name:                   name,
		Kind:                   kind,
		RemoteKey:              remoteKey,
		ProviderType:           providerType,
		RemoteURL:              remoteURL,
		WebURL:                 stringFromMap(item, "web_url"),
		RemoteRole:             remoteRole,
		IsPrimary:              boolFromMap(item, "is_primary"),
		SyncEnabled:            boolDefaultFromMap(item, "sync_enabled", true),
		Protected:              boolFromMap(item, "protected"),
		LatestSHA:              stringFromMap(item, "latest_sha"),
		LastSyncStatus:         "never",
		SourceAccountID:        sourceAccountID,
		URLs:                   JSONValue{Data: urls},
		DefaultBranch:          defaultBranch,
		Metadata:               JSONValue{Data: metadata},
	}
	if err := tx.WithContext(ctx).Create(&remote).Error; err != nil {
		return nil, err
	}
	return gitRemoteMap(remote, nil, ""), nil
}

func createTemplateRepoSyncAssetGorm(ctx context.Context, tx *gorm.DB, opID string, project, repo map[string]any, remotes []map[string]any, defaults, parameters map[string]any) (map[string]any, error) {
	syncParams := mapFromAny(parameters["repo_sync"])
	syncDefaults := mapFromAny(defaults["repo_sync"])
	sourceRemoteID := firstNonEmptyString(stringFromMap(syncParams, "source_remote_id"), remoteIDByKey(remotes, firstNonEmptyString(stringFromMap(syncParams, "source_remote_key"), stringFromMap(syncDefaults, "source_remote_key"))))
	targetRemoteID := firstNonEmptyString(stringFromMap(syncParams, "target_remote_id"), remoteIDByKey(remotes, firstNonEmptyString(stringFromMap(syncParams, "target_remote_key"), stringFromMap(syncDefaults, "target_remote_key"))))
	if sourceRemoteID == "" || targetRemoteID == "" {
		if err := logTemplateRepoSyncSkippedGorm(ctx, tx, opID, map[string]any{"reason": "source and target remotes are required", "source_remote_id": sourceRemoteID, "target_remote_id": targetRemoteID}); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if sourceRemoteID == targetRemoteID {
		if err := logTemplateRepoSyncSkippedGorm(ctx, tx, opID, map[string]any{"reason": "source and target remotes must differ", "remote_id": sourceRemoteID}); err != nil {
			return nil, err
		}
		return nil, nil
	}
	repoID := cleanOptionalID(fmt.Sprint(repo["id"]))
	if ok, err := verifyTemplateRemoteForRepositoryGorm(ctx, tx, opID, repoID, sourceRemoteID, "source_remote_id"); err != nil || !ok {
		return nil, err
	}
	if ok, err := verifyTemplateRemoteForRepositoryGorm(ctx, tx, opID, repoID, targetRemoteID, "target_remote_id"); err != nil || !ok {
		return nil, err
	}
	enabled := false
	if value, ok := syncParams["enabled"].(bool); ok {
		enabled = value
	} else if value, ok := syncDefaults["enabled"].(bool); ok {
		enabled = value
	}
	asset := GormRepoSyncAsset{
		ProjectID:              cleanOptionalID(fmt.Sprint(project["id"])),
		ProjectGitRepositoryID: repoID,
		Name:                   firstNonEmptyString(stringFromMap(syncParams, "name"), stringFromMap(syncDefaults, "name"), "default mirror"),
		SourceRemoteID:         sourceRemoteID,
		TargetRemoteID:         targetRemoteID,
		TriggerMode:            firstNonEmptyString(stringFromMap(syncParams, "trigger_mode"), stringFromMap(syncDefaults, "trigger_mode"), "manual"),
		SyncMode:               firstNonEmptyString(stringFromMap(syncParams, "sync_mode"), stringFromMap(syncDefaults, "sync_mode"), "selected_refs"),
		Transport:              firstNonEmptyString(stringFromMap(syncParams, "transport"), stringFromMap(syncDefaults, "transport"), "ssh"),
		Driver:                 firstNonEmptyString(stringFromMap(syncParams, "driver"), stringFromMap(syncDefaults, "driver"), "projectops_worker_git_ssh"),
		Refs:                   JSONValue{Data: mapFromAny(syncParams["refs"])},
		Enabled:                enabled,
		Metadata:               JSONValue{Data: map[string]any{"source": "project_template", "template_placeholder": true}},
	}
	if err := tx.WithContext(ctx).Create(&asset).Error; err != nil {
		return nil, err
	}
	return repoSyncAssetMap(asset), nil
}

func logTemplateRepoSyncSkippedGorm(ctx context.Context, tx *gorm.DB, opID string, fields map[string]any) error {
	return tx.WithContext(ctx).Create(&GormOperationLog{OperationRunID: validNullString(opID), Level: "warn", Message: "template repo sync asset was not created", Fields: JSONValue{Data: fields}}).Error
}

func createTemplateFilesGorm(ctx context.Context, tx *gorm.DB, run, project, repo, defaults, parameters map[string]any) ([]map[string]any, error) {
	items := templateFileItems(defaults, parameters)
	files := make([]map[string]any, 0, len(items))
	for _, item := range items {
		file, err := createTemplateFileGorm(ctx, tx, run, project, repo, item)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func createTemplateFileGorm(ctx context.Context, tx *gorm.DB, run, project, repo, item map[string]any) (map[string]any, error) {
	path := safeTemplateFilePath(stringFromMap(item, "path"))
	if path == "" {
		return nil, fmt.Errorf("template file path is required")
	}
	metadata := mapFromAny(item["metadata"])
	metadata["source"] = "project_template"
	metadata["template_placeholder"] = true
	file := GormProjectTemplateFile{
		ProjectTemplateRunID:   validNullString(cleanOptionalID(fmt.Sprint(run["id"]))),
		ProjectTemplateID:      validNullString(cleanOptionalID(fmt.Sprint(run["project_template_id"]))),
		ProjectID:              validNullString(cleanOptionalID(fmt.Sprint(project["id"]))),
		ProjectGitRepositoryID: validNullString(cleanOptionalID(fmt.Sprint(repo["id"]))),
		Path:                   path,
		Kind:                   firstNonEmptyString(stringFromMap(item, "kind"), "text"),
		Content:                renderTemplateFileContent(stringFromMap(item, "content"), run, project, repo),
		Status:                 "planned",
		Metadata:               JSONValue{Data: metadata},
	}
	if err := tx.WithContext(ctx).Create(&file).Error; err != nil {
		return nil, err
	}
	return projectTemplateFileMap(file), nil
}

func verifyTemplateRemoteForRepositoryGorm(ctx context.Context, tx *gorm.DB, opID, repoID, remoteID, field string) (bool, error) {
	var remote GormGitRemote
	err := tx.WithContext(ctx).Where(&GormGitRemote{ProjectGitRepositoryID: repoID}).Where(map[string]any{"id": remoteID}).First(&remote).Error
	if err == nil {
		return true, nil
	}
	if !errorsIsRecordNotFound(err) {
		return false, err
	}
	fields := map[string]any{"field": field, "remote_id": remoteID, "repo_id": repoID}
	if logErr := tx.WithContext(ctx).Create(&GormOperationLog{OperationRunID: validNullString(opID), Level: "warn", Message: "template repo sync remote does not belong to the created repository", Fields: JSONValue{Data: fields}}).Error; logErr != nil {
		return false, logErr
	}
	return false, nil
}

func templateRemoteItems(defaults, parameters map[string]any) []map[string]any {
	items := mapSliceFromAny(parameters["remotes"])
	if len(items) == 0 {
		items = mapSliceFromAny(defaults["remotes"])
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if firstNonEmptyString(stringFromMap(item, "remote_key"), stringFromMap(item, "name")) == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func resolveTemplateProviderAccount(ctx context.Context, store *Store, item map[string]any, providerType string) (providerAccountConfig, bool, error) {
	accountID := strings.TrimSpace(stringFromMap(item, "provider_account_id"))
	accountName := strings.TrimSpace(stringFromMap(item, "provider_account_name"))
	if accountID == "" && accountName == "" {
		return providerAccountConfig{}, false, nil
	}
	if store == nil || store.Gorm == nil {
		return providerAccountConfig{}, false, fmt.Errorf("gorm store is not initialized")
	}
	var accountModel GormProviderAccount
	query := store.Gorm.WithContext(ctx)
	var err error
	if accountID != "" {
		err = query.Where(map[string]any{"id": accountID}).First(&accountModel).Error
	} else {
		err = query.Where(map[string]any{"name": accountName}).First(&accountModel).Error
	}
	account := providerAccountConfigFromGorm(accountModel)
	if err != nil {
		return account, false, fmt.Errorf("loading provider account for template remote: %w", err)
	}
	if !account.Enabled {
		return account, false, fmt.Errorf("provider account %q is disabled", account.Name)
	}
	provider := strings.ToLower(strings.TrimSpace(providerType))
	if provider != account.ProviderType {
		return account, false, fmt.Errorf("provider account %q is %s but template remote is %s", account.Name, account.ProviderType, provider)
	}
	if !safeTemplateProviderTokenEnv(account.ProviderType, account.TokenEnv) {
		return account, false, fmt.Errorf("provider account %q has an unsafe token_env", account.Name)
	}
	return account, true, nil
}

func remoteIDByKey(remotes []map[string]any, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	for _, remote := range remotes {
		if stringFromMap(remote, "remote_key") == key || stringFromMap(remote, "name") == key {
			return strings.TrimSpace(fmt.Sprint(remote["id"]))
		}
	}
	return ""
}

func templateFileItems(defaults, parameters map[string]any) []map[string]any {
	items := mapSliceFromAny(parameters["files"])
	if len(items) == 0 {
		items = mapSliceFromAny(defaults["files"])
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if safeTemplateFilePath(stringFromMap(item, "path")) == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func renderTemplateFileContent(content string, run, project, repo map[string]any) string {
	replacements := map[string]string{
		"{{project_name}}":   strings.TrimSpace(fmt.Sprint(project["name"])),
		"{{project_slug}}":   strings.TrimSpace(fmt.Sprint(project["slug"])),
		"{{template_slug}}":  strings.TrimSpace(fmt.Sprint(run["template_slug"])),
		"{{repository_key}}": strings.TrimSpace(fmt.Sprint(repo["repo_key"])),
	}
	for token, value := range replacements {
		content = strings.ReplaceAll(content, token, value)
	}
	return content
}

func safeTemplateFilePath(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	path = strings.TrimPrefix(path, "/")
	if path == "" || path == "." || strings.Contains(path, "\x00") {
		return ""
	}
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return ""
		}
	}
	return path
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func templateNameWithSuffix(base, suffix, fallbackSuffix string) string {
	suffix = firstNonEmptyString(suffix, fallbackSuffix)
	if base == "" {
		return suffix
	}
	if suffix == "" || strings.HasSuffix(base, "-"+suffix) {
		return base
	}
	return base + "-" + suffix
}

func templateDisplayName(base, suffix, fallbackSuffix string) string {
	suffix = firstNonEmptyString(suffix, fallbackSuffix)
	if base == "" {
		return suffix
	}
	if suffix == "" || strings.HasSuffix(base, " "+suffix) {
		return base
	}
	return base + " " + suffix
}

func mapRemoteIDs(remotes []map[string]any) []string {
	ids := make([]string, 0, len(remotes))
	for _, remote := range remotes {
		id := strings.TrimSpace(fmt.Sprint(remote["id"]))
		if id != "" && id != "<nil>" {
			ids = append(ids, id)
		}
	}
	return ids
}

func mapTemplateFileIDs(files []map[string]any) []string {
	ids := make([]string, 0, len(files))
	for _, file := range files {
		id := strings.TrimSpace(fmt.Sprint(file["id"]))
		if id != "" && id != "<nil>" {
			ids = append(ids, id)
		}
	}
	return ids
}

func templateFileSummaries(files []map[string]any) []map[string]any {
	summaries := make([]map[string]any, 0, len(files))
	for _, file := range files {
		summaries = append(summaries, map[string]any{
			"id":     file["id"],
			"path":   file["path"],
			"kind":   file["kind"],
			"status": file["status"],
		})
	}
	return summaries
}

func mapSliceFromAny(value any) []map[string]any {
	if typed, ok := value.([]map[string]any); ok {
		return typed
	}
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		mapped := mapFromAny(item)
		if len(mapped) > 0 {
			out = append(out, mapped)
		}
	}
	return out
}

func boolFromMap(input map[string]any, key string) bool {
	return boolDefaultFromMap(input, key, false)
}

func boolDefaultFromMap(input map[string]any, key string, fallback bool) bool {
	value, ok := input[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	}
	return fallback
}

func completeTemplateSteps(value any, project, repo map[string]any, remotes []map[string]any, syncAsset map[string]any, files []map[string]any) []map[string]any {
	stepsAny, _ := value.([]any)
	if len(stepsAny) == 0 {
		stepsAny = []any{
			map[string]any{"key": "project", "title": "Create project asset"},
			map[string]any{"key": "repository", "title": "Create repository metadata"},
			map[string]any{"key": "remotes", "title": "Bind repository remotes"},
			map[string]any{"key": "repo_sync", "title": "Configure repo sync asset"},
			map[string]any{"key": "files", "title": "Plan starter repository files"},
		}
	}
	steps := make([]map[string]any, 0, len(stepsAny))
	for _, item := range stepsAny {
		step := mapFromAny(item)
		key := stringFromMap(step, "key")
		switch key {
		case "project":
			step["status"] = "completed"
			step["project_id"] = project["id"]
		case "repository":
			if repo != nil {
				step["status"] = "completed"
				step["repository_id"] = repo["id"]
			} else {
				step["status"] = "planned"
			}
		case "remotes":
			if len(remotes) > 0 {
				step["status"] = "completed"
				step["remote_ids"] = mapRemoteIDs(remotes)
			} else {
				step["status"] = "planned"
				step["reason"] = "template parameters must include remotes before repo sync can be attached automatically"
			}
		case "repo_sync":
			if syncAsset != nil {
				step["status"] = "completed"
				step["repo_sync_asset_id"] = syncAsset["id"]
			} else {
				step["status"] = "planned"
				step["reason"] = "source_remote_id and target_remote_id are required after remotes are attached"
			}
		case "files":
			if len(files) > 0 {
				step["status"] = "completed"
				step["template_file_ids"] = mapTemplateFileIDs(files)
			} else {
				step["status"] = "planned"
				step["reason"] = "template defaults or parameters must include files"
			}
		default:
			step["status"] = "planned"
		}
		steps = append(steps, step)
	}
	return steps
}

func completeTemplateStepsWithRepositoryProvision(steps []map[string]any, provision *gitExecutionResult) []map[string]any {
	if provision == nil || provision.Details == nil {
		return steps
	}
	for _, step := range steps {
		switch stringFromMap(step, "key") {
		case "repository":
			step["status"] = "completed"
			step["repository_provisioned"] = true
			step["commit_sha"] = provision.AfterSHA
			step["remote_id"] = provision.Details["remote_id"]
		case "files":
			if count, ok := provision.Details["file_count"].(int); ok && count > 0 {
				step["status"] = "completed"
				step["pushed"] = true
				step["commit_sha"] = provision.AfterSHA
			}
		}
	}
	return steps
}

func templateStepsWithProvisionRetry(value any) []map[string]any {
	input := mapSliceFromAny(value)
	steps := make([]map[string]any, 0, len(input))
	for _, item := range input {
		step := mapFromAny(item)
		switch stringFromMap(step, "key") {
		case "repository", "files":
			step["status"] = "provisioning"
			delete(step, "error")
		}
		steps = append(steps, step)
	}
	return steps
}

func templateStepsWithProvisionFailure(value any) []map[string]any {
	input := mapSliceFromAny(value)
	steps := make([]map[string]any, 0, len(input))
	for _, item := range input {
		step := mapFromAny(item)
		switch stringFromMap(step, "key") {
		case "repository", "files":
			step["status"] = "failed"
		}
		steps = append(steps, step)
	}
	return steps
}

func templateStepsWithStatus(value any, status string) []map[string]any {
	stepsAny, _ := value.([]any)
	steps := make([]map[string]any, 0, len(stepsAny))
	for _, item := range stepsAny {
		step := mapFromAny(item)
		step["status"] = status
		steps = append(steps, step)
	}
	return steps
}

func hasTemplateSteps(value any) bool {
	steps, ok := value.([]any)
	return ok && len(steps) > 0
}

func mergeGitExecutionResult(result map[string]any, execution *gitExecutionResult) {
	if execution == nil {
		return
	}
	result["stdout"] = execution.Stdout
	result["stderr"] = execution.Stderr
	result["after_sha"] = execution.AfterSHA
	result["details"] = execution.Details
}

func (w *ControlWorker) newGitExecutor(workDir string) *GitExecutor {
	executor := NewGitExecutor(workDir)
	executor.LocalBareBaseDirs = w.cfg.LocalBareBaseDirs
	return executor
}

func mergeRepoTagLookupExecutionResult(result map[string]any, execution *gitExecutionResult) {
	if execution == nil {
		return
	}
	for key, value := range execution.Details {
		result[key] = value
	}
	result["matched_sha"] = execution.AfterSHA
	result["matched_sha_present"] = execution.AfterSHA != ""
	result["raw_git_output_recorded"] = false
	result["remote_url_recorded"] = false
	result["credentials_recorded"] = false
	result["contains_token"] = false
}

func gitExecutionOutputFromMap(result map[string]any) (string, string) {
	stdout, _ := result["stdout"].(string)
	stderr, _ := result["stderr"].(string)
	return stdout, stderr
}

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

func (w *ControlWorker) recordArgoSyncAdapterRun(ctx context.Context, tx *gorm.DB, result map[string]any) error {
	syncResult, ok := result["_argo_sync_result"].(*ArgoSyncResult)
	delete(result, "_argo_sync_result")
	if !ok || syncResult == nil || syncResult.ProjectID == "" || syncResult.ConnectionID == "" {
		return nil
	}
	result["project_id"] = syncResult.ProjectID
	result["connection_id"] = syncResult.ConnectionID
	result["server_url"] = syncResult.ServerURL
	result["count"] = len(syncResult.Apps)
	if err := tx.WithContext(ctx).Where(&GormArgoApp{ArgoConnectionID: validNullString(syncResult.ConnectionID)}).Delete(&GormArgoApp{}).Error; err != nil {
		return err
	}
	for _, app := range syncResult.Apps {
		target, err := upsertDeploymentTargetForArgoApp(ctx, tx, syncResult, app)
		if err != nil {
			return err
		}
		argoApp := GormArgoApp{
			ProjectID:          syncResult.ProjectID,
			ArgoConnectionID:   validNullString(syncResult.ConnectionID),
			DeploymentTargetID: validNullString(target.ID),
			Name:               app.Name,
			Namespace:          app.Namespace,
			Status:             app.Status,
			Metadata:           JSONValue{Data: app.Metadata},
			SyncedAt:           validNullTime(time.Now()),
		}
		if err := tx.WithContext(ctx).Create(&argoApp).Error; err != nil {
			return err
		}
		if err := upsertDeploymentRecordForArgoApp(ctx, tx, syncResult, app, target, argoApp); err != nil {
			return err
		}
	}
	if err := refreshArgoDeploymentTargetStatus(ctx, tx, syncResult.ProjectID, syncResult.ConnectionID); err != nil {
		return err
	}
	if err := cleanupOrphanArgoDeploymentTargets(ctx, tx, syncResult.ConnectionID); err != nil {
		return err
	}
	if err := tx.WithContext(ctx).Model(&GormArgoConnection{}).
		Where(&GormArgoConnection{GormBase: GormBase{ID: syncResult.ConnectionID}}).
		Updates(map[string]any{"last_sync_status": "completed", "last_sync_error": ""}).Error; err != nil {
		return err
	}
	if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
		return fmt.Errorf("syncing canonical assets for Argo app sync: %w", err)
	}
	return nil
}

func upsertDeploymentRecordForArgoApp(ctx context.Context, tx *gorm.DB, syncResult *ArgoSyncResult, app ArgoAppInput, target GormDeploymentTarget, argoApp GormArgoApp) error {
	metadata := mapFromAny(app.Metadata)
	revision := firstNonEmptyString(stringFromMap(metadata, "revision"), stringFromMap(metadata, "target_revision"))
	images := stringSliceFromAny(metadata["images"])
	recordMetadata := map[string]any{
		"source":             "argocd",
		"argo_connection_id": syncResult.ConnectionID,
		"server_url":         syncResult.ServerURL,
		"health_status":      stringFromMap(metadata, "health_status"),
		"sync_status":        stringFromMap(metadata, "sync_status"),
	}
	environment := firstNonEmptyString(app.Environment, target.Environment)
	namespace := firstNonEmptyString(app.Namespace, target.Namespace)
	clusterName := firstNonEmptyString(app.ClusterName, target.ClusterName)
	var record GormDeploymentRecord
	where := GormDeploymentRecord{ProjectID: syncResult.ProjectID, Source: "argocd", Name: app.Name, Environment: environment, Namespace: namespace, ClusterName: clusterName}
	if err := tx.WithContext(ctx).Where(&where).First(&record).Error; err != nil && !errorsIsRecordNotFound(err) {
		return err
	}
	record.ProjectID = syncResult.ProjectID
	record.DeploymentTargetID = validNullString(target.ID)
	record.ArgoConnectionID = validNullString(syncResult.ConnectionID)
	record.ArgoAppID = validNullString(argoApp.ID)
	record.Name = app.Name
	record.Environment = environment
	record.Namespace = namespace
	record.ClusterName = clusterName
	record.Source = "argocd"
	record.Status = app.Status
	record.Revision = revision
	record.ImageRefs = JSONValue{Data: images}
	record.Metadata = JSONValue{Data: recordMetadata}
	record.ObservedAt = time.Now()
	if err := tx.WithContext(ctx).Save(&record).Error; err != nil {
		return err
	}
	if revision == "" && len(images) == 0 {
		return nil
	}
	rollbackMetadata := map[string]any{
		"source":               "argocd",
		"deployment_record_id": record.ID,
		"argo_app_id":          argoApp.ID,
	}
	var point GormRollbackPoint
	pointWhere := GormRollbackPoint{ProjectID: syncResult.ProjectID, Source: "argocd", Name: app.Name, Environment: environment, Revision: revision}
	if err := tx.WithContext(ctx).Where(&pointWhere).First(&point).Error; err != nil && !errorsIsRecordNotFound(err) {
		return err
	}
	point.ProjectID = syncResult.ProjectID
	point.DeploymentRecordID = validNullString(record.ID)
	point.DeploymentTargetID = validNullString(target.ID)
	point.Name = app.Name
	point.Environment = environment
	point.Revision = revision
	point.ImageRefs = JSONValue{Data: images}
	point.Source = "argocd"
	point.Status = "available"
	point.Metadata = JSONValue{Data: rollbackMetadata}
	point.CapturedAt = time.Now()
	return tx.WithContext(ctx).Save(&point).Error
}

func upsertDeploymentTargetForArgoApp(ctx context.Context, tx *gorm.DB, syncResult *ArgoSyncResult, app ArgoAppInput) (GormDeploymentTarget, error) {
	environment := strings.TrimSpace(app.Environment)
	if environment == "" {
		environment = strings.TrimSpace(app.Namespace)
	}
	if environment == "" {
		environment = "default"
	}
	namespace := strings.TrimSpace(app.Namespace)
	clusterName := strings.TrimSpace(app.ClusterName)
	name := environment
	if namespace != "" && namespace != environment {
		name = environment + "/" + namespace
	}
	metadata := map[string]any{
		"source":             "argocd",
		"argo_connection_id": syncResult.ConnectionID,
		"server_url":         syncResult.ServerURL,
	}
	var target GormDeploymentTarget
	where := GormDeploymentTarget{ProjectID: syncResult.ProjectID, Environment: environment, ClusterName: clusterName, Namespace: namespace}
	if err := tx.WithContext(ctx).Where(&where).First(&target).Error; err != nil && !errorsIsRecordNotFound(err) {
		return target, err
	}
	target.ProjectID = syncResult.ProjectID
	target.Name = name
	target.Environment = environment
	target.ClusterName = clusterName
	target.Namespace = namespace
	target.Source = "argocd"
	target.ArgoConnectionID = validNullString(syncResult.ConnectionID)
	if target.Status == "" {
		target.Status = "unknown"
	}
	target.Metadata = JSONValue{Data: metadata}
	return target, tx.WithContext(ctx).Save(&target).Error
}

func refreshArgoDeploymentTargetStatus(ctx context.Context, tx *gorm.DB, projectID, connectionID string) error {
	var apps []GormArgoApp
	if err := tx.WithContext(ctx).Where(&GormArgoApp{ProjectID: projectID, ArgoConnectionID: validNullString(connectionID)}).Find(&apps).Error; err != nil {
		return err
	}
	statusesByTarget := map[string][]string{}
	for _, app := range apps {
		targetID := cleanOptionalID(app.DeploymentTargetID.String)
		if targetID == "" {
			continue
		}
		statusesByTarget[targetID] = append(statusesByTarget[targetID], app.Status)
	}
	for targetID, statuses := range statusesByTarget {
		if err := tx.WithContext(ctx).Model(&GormDeploymentTarget{}).
			Where(&GormDeploymentTarget{GormBase: GormBase{ID: targetID}, ProjectID: projectID, Source: "argocd"}).
			Updates(map[string]any{"status": argoDeploymentTargetStatusFromApps(statuses)}).Error; err != nil {
			return err
		}
	}
	return nil
}

func argoDeploymentTargetStatusFromApps(statuses []string) string {
	if len(statuses) == 0 {
		return "unknown"
	}
	allSynced := true
	for _, status := range statuses {
		switch strings.ToLower(strings.TrimSpace(status)) {
		case "outofsync", "failed", "error", "degraded":
			return "OutOfSync"
		case "synced":
		default:
			allSynced = false
		}
	}
	if allSynced {
		return "Synced"
	}
	return "Unknown"
}

func cleanupOrphanArgoDeploymentTargets(ctx context.Context, tx *gorm.DB, connectionID string) error {
	var targets []GormDeploymentTarget
	if err := tx.WithContext(ctx).Where(&GormDeploymentTarget{Source: "argocd", ArgoConnectionID: validNullString(connectionID)}).Find(&targets).Error; err != nil {
		return err
	}
	for _, target := range targets {
		var count int64
		if err := tx.WithContext(ctx).Model(&GormArgoApp{}).Where(&GormArgoApp{DeploymentTargetID: validNullString(target.ID)}).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			if err := tx.WithContext(ctx).Delete(&target).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func argoConnectionIDFromResult(result map[string]any) string {
	if syncResult, ok := result["_argo_sync_result"].(*ArgoSyncResult); ok && syncResult != nil {
		return syncResult.ConnectionID
	}
	if value, ok := result["connection_id"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

type NodeWorker struct {
	cfg          Config
	name         string
	kind         string
	capabilities []string
	log          *slog.Logger
	client       *http.Client
	token        string
}

func NewNodeWorker(cfg Config, name, kind string, capabilities []string, log *slog.Logger) *NodeWorker {
	return &NodeWorker{
		cfg:          cfg,
		name:         name,
		kind:         kind,
		capabilities: capabilities,
		log:          log,
		client:       &http.Client{Timeout: 10 * time.Second},
	}
}

func (n *NodeWorker) Run(ctx context.Context) error {
	if err := n.register(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(n.cfg.WorkerInterval)
	defer ticker.Stop()
	for {
		if err := n.heartbeat(ctx); err != nil {
			n.log.Error("heartbeat failed", "error", err)
		}
		if err := n.claimAndRun(ctx); err != nil {
			n.log.Error("claim failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (n *NodeWorker) register(ctx context.Context) error {
	var resp map[string]any
	err := n.post(ctx, "/api/worker-nodes/register", map[string]any{
		"name":         n.name,
		"kind":         n.kind,
		"capabilities": n.capabilities,
	}, &resp, false)
	if err != nil {
		return err
	}
	token, ok := resp["token"].(string)
	if !ok || token == "" {
		return fmt.Errorf("register response missing token")
	}
	n.token = token
	n.log.Info("node registered", "name", n.name)
	return nil
}

func (n *NodeWorker) heartbeat(ctx context.Context) error {
	var resp map[string]any
	return n.post(ctx, "/api/worker-nodes/heartbeat", map[string]any{}, &resp, true)
}

func (n *NodeWorker) claimAndRun(ctx context.Context) error {
	var resp struct {
		Job map[string]any `json:"job"`
	}
	if err := n.post(ctx, "/api/worker-nodes/jobs/claim", map[string]any{}, &resp, true); err != nil {
		return err
	}
	if resp.Job == nil {
		return nil
	}
	jobID := fmt.Sprint(resp.Job["id"])
	_ = n.post(ctx, "/api/worker-nodes/jobs/"+jobID+"/logs", map[string]any{"level": "info", "message": "node-worker executing echo adapter"}, nil, true)
	result := map[string]any{"echo": resp.Job["payload"], "node": n.name}
	return n.post(ctx, "/api/worker-nodes/jobs/"+jobID+"/complete", map[string]any{"result": result}, nil, true)
}

func (n *NodeWorker) post(ctx context.Context, path string, body any, dst any, auth bool) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.cfg.GatewayURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if auth {
		req.Header.Set("Authorization", "Bearer "+n.token)
	}
	res, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("gateway returned %s for %s", res.Status, path)
	}
	if dst != nil {
		return json.NewDecoder(res.Body).Decode(dst)
	}
	return nil
}

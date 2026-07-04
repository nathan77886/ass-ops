package app

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"log/slog"
	"time"
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
			Where("status = ? AND preferred_node_kind IN ?", "queued", localWorkerPreferredKinds()).
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
	case "repo.sync", "repo.sync_remote", "git.refs.refresh", "repo.tag", "repo.create_tag", "repo.tag.lookup", "ssh.exec", "ssh.verify", "argo.apps.sync", "argo.pod_logs", "argo.pod_restart", "github.actions.sync", "github.labels.sync", "agent.execute", "config.git_commit", "project_version.validation_rerun":
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

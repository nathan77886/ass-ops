package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
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
	claimTx, err := w.store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	job, err := queryOne(ctx, claimTx, `
		UPDATE worker_jobs
		SET status='running', started_at=now(), updated_at=now()
		WHERE id = (
			SELECT id FROM worker_jobs
			WHERE status='queued' AND preferred_node_kind IN ('', 'control-worker')
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING *`)
	if err != nil {
		_ = claimTx.Rollback()
		return err
	}
	opID := fmt.Sprint(job["operation_run_id"])
	if _, err := claimTx.ExecContext(ctx, "UPDATE operation_runs SET status='running', started_at=COALESCE(started_at, now()), updated_at=now() WHERE id=$1", opID); err != nil {
		_ = claimTx.Rollback()
		return err
	}
	if _, err := claimTx.ExecContext(ctx, `
		INSERT INTO operation_logs(operation_run_id, worker_job_id, level, message)
		VALUES ($1, $2, 'info', $3)`, opID, job["id"], "dispatching "+fmt.Sprint(job["tool_name"])); err != nil {
		_ = claimTx.Rollback()
		return err
	}
	if err := w.markAdapterRunning(ctx, claimTx, job); err != nil {
		_ = claimTx.Rollback()
		return err
	}
	if err := claimTx.Commit(); err != nil {
		return err
	}

	result, adapterErr := w.executeAdapterRun(ctx, job)

	tx, err := w.store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if adapterErr != nil {
		if result == nil {
			result = map[string]any{"adapter": true}
		}
		result["error"] = adapterErr.Error()
		if err := w.recordAdapterFailure(ctx, tx, job, result, adapterErr); err != nil {
			return err
		}
		errJSON, _ := jsonParam(result)
		opErrJSON, _ := jsonParam(operationRunResult(job, result))
		if _, err := tx.ExecContext(ctx, `
			UPDATE worker_jobs SET status='failed', result=$2::jsonb, error=$3, finished_at=now(), updated_at=now()
			WHERE id=$1`, job["id"], errJSON, adapterErr.Error()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE operation_runs SET status='failed', result=$2::jsonb, error=$3, finished_at=now(), updated_at=now()
			WHERE id=$1`, opID, opErrJSON, adapterErr.Error()); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		w.refreshCanonicalAssetsAfterOperation(ctx, opID, "failed")
		return adapterErr
	}
	if err := w.recordAdapterSuccess(ctx, tx, job, result); err != nil {
		_ = tx.Rollback()
		if result == nil {
			result = map[string]any{"adapter": true}
		}
		result["error"] = err.Error()
		failTx, beginErr := w.store.DB.BeginTxx(ctx, nil)
		if beginErr != nil {
			return errors.Join(err, beginErr)
		}
		defer failTx.Rollback()
		if failErr := w.recordAdapterFailure(ctx, failTx, job, result, err); failErr != nil {
			return errors.Join(err, failErr)
		}
		errJSON, _ := jsonParam(result)
		opErrJSON, _ := jsonParam(operationRunResult(job, result))
		if _, failErr := failTx.ExecContext(ctx, `
			UPDATE worker_jobs SET status='failed', result=$2::jsonb, error=$3, finished_at=now(), updated_at=now()
			WHERE id=$1`, job["id"], errJSON, err.Error()); failErr != nil {
			return errors.Join(err, failErr)
		}
		if _, failErr := failTx.ExecContext(ctx, `
			UPDATE operation_runs SET status='failed', result=$2::jsonb, error=$3, finished_at=now(), updated_at=now()
			WHERE id=$1`, opID, opErrJSON, err.Error()); failErr != nil {
			return errors.Join(err, failErr)
		}
		if failErr := failTx.Commit(); failErr != nil {
			return errors.Join(err, failErr)
		}
		w.refreshCanonicalAssetsAfterOperation(ctx, opID, "failed")
		return err
	}
	resultJSON, _ := jsonParam(result)
	opResultJSON, _ := jsonParam(operationRunResult(job, result))
	_, err = tx.ExecContext(ctx, `
		UPDATE worker_jobs SET status='completed', result=$2::jsonb, finished_at=now(), updated_at=now()
		WHERE id=$1`, job["id"], resultJSON)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE operation_runs SET status='completed', result=$2::jsonb, finished_at=now(), updated_at=now()
		WHERE id=$1`, opID, opResultJSON)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	w.refreshCanonicalAssetsAfterOperation(ctx, opID, "completed")
	return nil
}

func (w *ControlWorker) refreshCanonicalAssetsAfterOperation(ctx context.Context, operationID, status string) {
	result, err := w.store.SyncCanonicalAssets(ctx)
	if err != nil {
		if w.log != nil {
			w.log.Warn("canonical asset refresh failed after operation", "operation_id", operationID, "status", status, "error", err)
		}
		return
	}
	if w.log != nil {
		w.log.Debug("canonical assets refreshed after operation", "operation_id", operationID, "status", status, "synced_assets", result.SyncedAssets, "inserted_relations", result.InsertedRelations)
	}
}

func (w *ControlWorker) sweepExpiredApprovals(ctx context.Context) error {
	return w.server.expirePendingOperationApprovals(ctx, w.store.DB)
}

func (w *ControlWorker) dispatchDueApprovalReminders(ctx context.Context) error {
	return w.server.dispatchDueOperationApprovalReminders(ctx)
}

func (w *ControlWorker) dispatchDueApprovalEscalations(ctx context.Context) error {
	return w.server.dispatchDueOperationApprovalEscalations(ctx)
}

func operationRunResult(job map[string]any, result map[string]any) map[string]any {
	if fmt.Sprint(job["tool_name"]) != "ssh.exec" {
		return result
	}
	safe := map[string]any{
		"adapter": true,
		"tool":    "ssh.exec",
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
	tx, err := w.store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	recoveryResult, err := jsonParam(map[string]any{"adapter": true, "recovered": true, "reason": "worker timeout"})
	if err != nil {
		return err
	}
	rows, err := queryMaps(ctx, tx, `
		WITH stale AS (
			SELECT id, operation_run_id
			FROM worker_jobs
			WHERE status='running'
			  AND started_at < now() - interval '30 minutes'
			FOR UPDATE SKIP LOCKED
		),
		updated_jobs AS (
			UPDATE worker_jobs wj
			SET status='failed',
				result=$1::jsonb,
				error='worker timed out while running',
				finished_at=now(),
				updated_at=now()
			FROM stale
			WHERE wj.id=stale.id
			RETURNING stale.operation_run_id
		)
		SELECT operation_run_id FROM updated_jobs`, recoveryResult)
	if err != nil {
		return err
	}
	for _, row := range rows {
		opID := row["operation_run_id"]
		if _, err := tx.ExecContext(ctx, `
			UPDATE operation_runs
			SET status='failed',
				result=$2::jsonb,
				error='worker timed out while running',
				finished_at=now(),
				updated_at=now()
			WHERE id=$1`, opID, recoveryResult); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE repo_sync_runs
			SET status='failed',
				error_message='worker timed out while running',
				finished_at=now()
			WHERE operation_run_id=$1 AND status IN ('queued', 'running', 'provisioning')`, opID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE repo_sync_assets
			SET last_sync_status='failed',
				updated_at=now()
			WHERE id=(SELECT repo_sync_asset_id FROM repo_sync_runs WHERE operation_run_id=$1 LIMIT 1)`, opID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE repo_tag_runs
			SET status='failed',
				error_message='worker timed out while running',
				finished_at=now()
			WHERE operation_run_id=$1 AND status IN ('queued', 'running')`, opID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE ssh_command_runs
			SET status='failed',
				error_message='worker timed out while running',
				finished_at=now()
			WHERE operation_run_id=$1 AND status IN ('queued', 'running')`, opID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE argo_connections
			SET last_sync_status='failed',
				last_sync_error='worker timed out while running',
				updated_at=now()
			WHERE id=(SELECT (input->>'argo_connection_id')::uuid FROM operation_runs WHERE id=$1 AND operation_type='argo.apps.sync' LIMIT 1)`, opID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE project_template_runs
			SET status='failed',
				error_message='worker timed out while running',
				finished_at=now(),
				updated_at=now()
			WHERE operation_run_id=$1 AND status IN ('queued', 'running', 'provisioning')`, opID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE project_template_runs ptr
			SET status='failed',
				error_message='worker timed out while running',
				finished_at=now(),
				updated_at=now()
			FROM operation_runs op
			WHERE op.id=$1
				AND op.operation_type='project.template_provision_retry'
				AND ptr.id=NULLIF(op.input->>'project_template_run_id', '')::uuid
				AND ptr.status IN ('queued', 'running', 'provisioning')`, opID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		WITH stale_ops AS (
			SELECT opr.id
			FROM operation_runs opr
			WHERE opr.status='running'
				AND opr.started_at < now() - interval '30 minutes'
				AND NOT EXISTS (
					SELECT 1
					FROM worker_jobs wj
					WHERE wj.operation_run_id=opr.id
						AND wj.status IN ('queued', 'running')
				)
			FOR UPDATE SKIP LOCKED
		),
		updated_ops AS (
			UPDATE operation_runs opr
			SET status='failed',
				result=$1::jsonb,
				error='worker timed out while running',
				finished_at=now(),
				updated_at=now()
			FROM stale_ops
			WHERE opr.id=stale_ops.id
			RETURNING opr.id, opr.operation_type, opr.input
		),
		template_create AS (
			UPDATE project_template_runs ptr
			SET status='failed',
				error_message='worker timed out while running',
				finished_at=now(),
				updated_at=now()
			FROM updated_ops
			WHERE ptr.operation_run_id=updated_ops.id
				AND ptr.status IN ('queued', 'running', 'provisioning')
			RETURNING ptr.id
		),
		template_retry AS (
			UPDATE project_template_runs ptr
			SET status='failed',
				error_message='worker timed out while running',
				finished_at=now(),
				updated_at=now()
			FROM updated_ops
			WHERE updated_ops.operation_type='project.template_provision_retry'
				AND ptr.id=NULLIF(updated_ops.input->>'project_template_run_id', '')::uuid
				AND ptr.status IN ('queued', 'running', 'provisioning')
			RETURNING ptr.id
		)
		SELECT
			(SELECT count(*) FROM template_create) AS template_create_count,
			(SELECT count(*) FROM template_retry) AS template_retry_count`, recoveryResult); err != nil {
		return err
	}
	return tx.Commit()
}

func (w *ControlWorker) markAdapterRunning(ctx context.Context, db sqlx.ExtContext, job map[string]any) error {
	opID := fmt.Sprint(job["operation_run_id"])
	tool := fmt.Sprint(job["tool_name"])
	switch tool {
	case "repo.sync", "repo.sync_remote":
		if _, err := db.ExecContext(ctx, "UPDATE repo_sync_runs SET status='running', started_at=COALESCE(started_at, now()) WHERE operation_run_id=$1", opID); err != nil {
			return err
		}
		_, err := db.ExecContext(ctx, `
			UPDATE repo_sync_assets
			SET last_sync_status='running',
				updated_at=now()
			WHERE id=(SELECT repo_sync_asset_id FROM repo_sync_runs WHERE operation_run_id=$1 LIMIT 1)`, opID)
		return err
	case "repo.tag", "repo.create_tag":
		_, err := db.ExecContext(ctx, "UPDATE repo_tag_runs SET status='running', started_at=COALESCE(started_at, now()) WHERE operation_run_id=$1", opID)
		return err
	case "ssh.exec":
		_, err := db.ExecContext(ctx, "UPDATE ssh_command_runs SET status='running', started_at=COALESCE(started_at, now()) WHERE operation_run_id=$1", opID)
		return err
	case "argo.apps.sync":
		_, err := db.ExecContext(ctx, `
			UPDATE argo_connections
			SET last_sync_status='running',
				last_sync_error='',
				updated_at=now()
			WHERE id=(SELECT (input->>'argo_connection_id')::uuid FROM operation_runs WHERE id=$1 LIMIT 1)`, opID)
		return err
	case "project.create_from_template":
		_, err := db.ExecContext(ctx, "UPDATE project_template_runs SET status='running', started_at=COALESCE(started_at, now()), updated_at=now() WHERE operation_run_id=$1", opID)
		return err
	case "project.template_provision_retry":
		_, err := db.ExecContext(ctx, `
			UPDATE project_template_runs ptr
			SET status='provisioning',
				started_at=COALESCE(started_at, now()),
				finished_at=NULL,
				error_message='',
				result=result || jsonb_build_object(
					'provision_retry',
					jsonb_build_object('operation_run_id', $1, 'started_at', now())
				),
				updated_at=now()
			FROM operation_runs op
			WHERE op.id=$1
				AND ptr.id=NULLIF(op.input->>'project_template_run_id', '')::uuid`, opID)
		return err
	case "agent.execute":
		_, err := db.ExecContext(ctx, `
			UPDATE agent_tool_calls
			SET status='running',
				started_at=COALESCE(started_at, now()),
				updated_at=now()
			WHERE operation_run_id=$1
				AND status IN ('queued', 'planned')`, opID)
		return err
	default:
		return nil
	}
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
		execution, err := NewGitExecutor("").Sync(ctx, w.store.DB, opID)
		mergeGitExecutionResult(result, execution)
		return result, err
	case "repo.tag", "repo.create_tag":
		execution, err := NewGitExecutor("").Tag(ctx, w.store.DB, opID)
		mergeGitExecutionResult(result, execution)
		return result, err
	case "github.actions.sync":
		syncResult, err := NewGitHubActionsSyncer().Sync(ctx, w.store.DB, opID)
		mergeGitHubActionsResult(result, syncResult)
		return result, err
	case "argo.apps.sync":
		syncResult, err := NewArgoSyncer().SyncApps(ctx, w.store.DB, opID)
		mergeArgoSyncResult(result, syncResult)
		return result, err
	case "ssh.exec":
		execution, err := NewSSHExecutor().Execute(ctx, w.store.DB, opID)
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

func (w *ControlWorker) recordAdapterFailure(ctx context.Context, tx *sqlx.Tx, job map[string]any, result map[string]any, adapterErr error) error {
	opID := fmt.Sprint(job["operation_run_id"])
	tool := fmt.Sprint(job["tool_name"])
	stdout, stderr := gitExecutionOutputFromMap(result)
	switch tool {
	case "repo.sync", "repo.sync_remote":
		if _, err := tx.ExecContext(ctx, `
			UPDATE repo_sync_runs
			SET status='failed', stdout=$2, stderr=$3, error_message=$4, finished_at=now()
			WHERE operation_run_id=$1`, opID, stdout, stderr, adapterErr.Error()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE git_remotes
			SET last_sync_status='failed',
				updated_at=now()
			WHERE id=(SELECT target_remote_id FROM repo_sync_runs WHERE operation_run_id=$1 LIMIT 1)`, opID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE repo_sync_assets
			SET last_sync_status='failed',
				updated_at=now()
			WHERE id=(SELECT repo_sync_asset_id FROM repo_sync_runs WHERE operation_run_id=$1 LIMIT 1)`, opID)
		return err
	case "repo.tag", "repo.create_tag":
		_, err := tx.ExecContext(ctx, `
			UPDATE repo_tag_runs
			SET status='failed', stdout=$2, stderr=$3, error_message=$4, finished_at=now()
			WHERE operation_run_id=$1`, opID, stdout, stderr, adapterErr.Error())
		return err
	case "github.actions.sync":
		remoteID, _ := result["remote_id"].(string)
		if remoteID == "" {
			op, err := queryOne(ctx, tx, "SELECT git_remote_id FROM operation_runs WHERE id=$1", opID)
			if err != nil {
				return err
			}
			remoteID = strings.TrimSpace(fmt.Sprint(op["git_remote_id"]))
			if remoteID == "" || remoteID == "<nil>" {
				return nil
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM github_action_runs WHERE git_remote_id=$1", remoteID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE git_remotes
			SET last_sync_status='failed',
				updated_at=now()
			WHERE id=$1`, remoteID)
		return err
	case "argo.apps.sync":
		connectionID := argoConnectionIDFromResult(result)
		delete(result, "_argo_sync_result")
		if connectionID == "" {
			_, err := tx.ExecContext(ctx, `
				UPDATE argo_connections
				SET last_sync_status='failed',
					last_sync_error=$2,
					updated_at=now()
				WHERE id=(SELECT (input->>'argo_connection_id')::uuid FROM operation_runs WHERE id=$1 LIMIT 1)`, opID, adapterErr.Error())
			return err
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE argo_connections
			SET last_sync_status='failed',
				last_sync_error=$2,
				updated_at=now()
			WHERE id=$1`, connectionID, adapterErr.Error())
		return err
	case "ssh.exec":
		stdout, stderr := gitExecutionOutputFromMap(result)
		exitCode := nullableIntFromMap(result, "exit_code")
		_, err := tx.ExecContext(ctx, `
			UPDATE ssh_command_runs
			SET status='failed',
				exit_code=$2,
				stdout=$3,
				stderr=$4,
				error_message=$5,
				finished_at=now()
			WHERE operation_run_id=$1`, opID, exitCode, stdout, stderr, adapterErr.Error())
		return err
	case "project.create_from_template":
		if result["_template_run_completion_pending"] == true || result["_template_run_failure_recorded"] == true {
			delete(result, "_template_run_completion_pending")
			delete(result, "_template_run_failure_recorded")
			return nil
		}
		stepsValue := mapFromAny(result)["steps"]
		if !hasTemplateSteps(stepsValue) {
			run, runErr := queryOne(ctx, tx, "SELECT steps FROM project_template_runs WHERE operation_run_id=$1", opID)
			if runErr != nil {
				return runErr
			}
			stepsValue = run["steps"]
		}
		steps, err := jsonParam(templateStepsWithStatus(stepsValue, "failed"))
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE project_template_runs
			SET status='failed',
				steps=$2::jsonb,
				error_message=$3,
				finished_at=now(),
				updated_at=now()
			WHERE operation_run_id=$1`, opID, steps, adapterErr.Error())
		return err
	case "project.template_provision_retry":
		if result["_template_retry_recorded"] == true {
			delete(result, "_template_retry_recorded")
			return nil
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE project_template_runs ptr
			SET status='failed',
				error_message=$2,
				finished_at=now(),
				updated_at=now()
			FROM operation_runs op
			WHERE op.id=$1
				AND ptr.id=NULLIF(op.input->>'project_template_run_id', '')::uuid`, opID, adapterErr.Error())
		return err
	case "agent.execute":
		_, err := tx.ExecContext(ctx, `
			UPDATE agent_tool_calls
			SET status='failed',
				error_message=$2,
				finished_at=now(),
				updated_at=now()
			WHERE operation_run_id=$1
				AND status IN ('queued', 'planned', 'running')`, opID, adapterErr.Error())
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE agent_tasks
			SET status='failed',
				updated_at=now()
			WHERE id=(SELECT NULLIF(input->>'agent_task_id', '')::uuid FROM operation_runs WHERE id=$1)`, opID)
		return err
	}
	return nil
}

func (w *ControlWorker) recordAdapterSuccess(ctx context.Context, tx *sqlx.Tx, job map[string]any, result map[string]any) error {
	opID := fmt.Sprint(job["operation_run_id"])
	tool := fmt.Sprint(job["tool_name"])
	stdout, stderr := gitExecutionOutputFromMap(result)
	afterSHA, _ := result["after_sha"].(string)
	switch tool {
	case "repo.sync", "repo.sync_remote":
		if _, err := tx.ExecContext(ctx, `
			UPDATE repo_sync_runs
			SET status='completed',
				stdout=$2,
				stderr=$3,
				after_sha=$4,
				finished_at=now()
			WHERE operation_run_id=$1`, opID, stdout, stderr, afterSHA); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE git_remotes
			SET latest_sha=COALESCE(NULLIF($2, ''), latest_sha),
				last_sync_status='completed',
				updated_at=now()
			WHERE id=(SELECT target_remote_id FROM repo_sync_runs WHERE operation_run_id=$1 LIMIT 1)`, opID, afterSHA)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE repo_sync_assets
			SET last_sync_status='completed',
				last_synced_at=now(),
				updated_at=now()
			WHERE id=(SELECT repo_sync_asset_id FROM repo_sync_runs WHERE operation_run_id=$1 LIMIT 1)`, opID)
		return err
	case "repo.tag", "repo.create_tag":
		if _, err := tx.ExecContext(ctx, `
			UPDATE repo_tag_runs
			SET status='completed',
				stdout=$2,
				stderr=$3,
				target_sha=COALESCE(NULLIF(target_sha, ''), $4),
				finished_at=now()
			WHERE operation_run_id=$1`, opID, stdout, stderr, afterSHA); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE git_remotes
			SET updated_at=now()
			WHERE id=(SELECT target_remote_id FROM repo_tag_runs WHERE operation_run_id=$1 LIMIT 1)`, opID)
		return err
	case "github.actions.sync":
		if err := w.recordGitHubActionsAdapterRun(ctx, tx, opID, result); err != nil {
			return err
		}
		remoteID, _ := result["remote_id"].(string)
		if remoteID == "" {
			return nil
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE git_remotes
			SET last_sync_status='completed',
				updated_at=now()
			WHERE id=$1`, remoteID)
		return err
	case "argo.apps.sync":
		return w.recordArgoSyncAdapterRun(ctx, tx, result)
	case "ssh.exec":
		stdout, stderr := gitExecutionOutputFromMap(result)
		exitCode := nullableIntFromMap(result, "exit_code")
		_, err := tx.ExecContext(ctx, `
			UPDATE ssh_command_runs
			SET status='completed',
				exit_code=$2,
				stdout=$3,
				stderr=$4,
				finished_at=now()
			WHERE operation_run_id=$1`, opID, exitCode, stdout, stderr)
		return err
	case "project.create_from_template":
		if result["_template_run_recorded"] == true {
			delete(result, "_template_run_recorded")
			return nil
		}
		project, repo, remotes, syncAsset, files, steps, err := createProjectFromTemplateTx(ctx, tx, opID)
		if err != nil {
			return err
		}
		result["project_id"] = project["id"]
		result["project_slug"] = project["slug"]
		result["project_name"] = project["name"]
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
		result["steps"] = steps
		resultJSON, err := jsonParam(result)
		if err != nil {
			return err
		}
		stepsJSON, err := jsonParam(result["steps"])
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE project_template_runs
			SET status='completed',
				project_id=NULLIF($2,'')::uuid,
				steps=$3::jsonb,
				result=$4::jsonb,
				finished_at=now(),
				updated_at=now()
			WHERE operation_run_id=$1`, opID, stringFromMap(result, "project_id"), stepsJSON, resultJSON)
		return err
	case "project.template_provision_retry":
		if result["_template_retry_recorded"] == true {
			delete(result, "_template_retry_recorded")
		}
		return nil
	case "agent.execute":
		output, err := jsonParam(map[string]any{
			"message": "agent execution audit completed; first-version mutation is disabled",
			"result":  result,
		})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE agent_tool_calls
			SET status='completed',
				output=COALESCE(NULLIF(output, '{}'::jsonb), '{}'::jsonb) || $2::jsonb,
				finished_at=now(),
				updated_at=now()
			WHERE operation_run_id=$1
				AND status IN ('queued', 'planned', 'running')`, opID, output); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE agent_tasks
			SET status='executed',
				updated_at=now()
			WHERE id=(SELECT NULLIF(input->>'agent_task_id', '')::uuid FROM operation_runs WHERE id=$1)`, opID)
		return err
	default:
		return nil
	}
}

func (w *ControlWorker) executeAgentTaskAudit(ctx context.Context, opID string, result map[string]any) (map[string]any, error) {
	op, err := queryOne(ctx, w.store.DB, "SELECT * FROM operation_runs WHERE id=$1", opID)
	if err != nil {
		return result, fmt.Errorf("loading agent operation: %w", err)
	}
	input := mapFromAny(op["input"])
	taskID := strings.TrimSpace(fmt.Sprint(input["agent_task_id"]))
	if taskID == "" || taskID == "<nil>" {
		return result, fmt.Errorf("agent operation has no task id")
	}
	calls, err := queryMaps(ctx, w.store.DB, `
		SELECT tool_name, status
		FROM agent_tool_calls
		WHERE operation_run_id=$1
		ORDER BY created_at`, opID)
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
	run, err := queryOne(ctx, w.store.DB, `
		SELECT ptr.*, pt.slug AS template_slug, pt.name AS template_name
		FROM project_template_runs ptr
		LEFT JOIN project_templates pt ON pt.id=ptr.project_template_id
		WHERE ptr.operation_run_id=$1`, opID)
	if err != nil {
		return nil, err
	}
	projectSlug := strings.TrimSpace(fmt.Sprint(run["project_slug"]))
	projectName := strings.TrimSpace(fmt.Sprint(run["project_name"]))
	if projectName == "" || projectSlug == "" {
		return nil, fmt.Errorf("template run is missing project name or slug")
	}
	return map[string]any{
		"project_slug":  projectSlug,
		"project_name":  projectName,
		"template_id":   run["project_template_id"],
		"template_slug": run["template_slug"],
		"steps":         run["steps"],
	}, nil
}

func (w *ControlWorker) executeProjectTemplateRun(ctx context.Context, opID string) (map[string]any, error) {
	tx, err := w.store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	project, repo, remotes, syncAsset, files, steps, err := createProjectFromTemplateTx(ctx, tx, opID)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	result := templateRunResult(project, repo, remotes, syncAsset, files, steps)
	result["repository_provisioned"] = false
	resultJSON, err := jsonParam(result)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	stepsJSON, err := jsonParam(steps)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE project_template_runs
		SET status='provisioning',
			project_id=NULLIF($2,'')::uuid,
			steps=$3::jsonb,
			result=$4::jsonb,
			updated_at=now()
		WHERE operation_run_id=$1`,
		opID,
		cleanOptionalID(fmt.Sprint(project["id"])),
		stepsJSON,
		resultJSON,
	); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

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
	runID, err := projectTemplateRunIDForRetryOperation(ctx, w.store.DB, opID)
	if err != nil {
		return nil, err
	}
	run, project, repo, remotes, syncAsset, files, steps, err := loadProjectTemplateProvisionResources(ctx, w.store.DB, runID)
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

func projectTemplateRunIDForRetryOperation(ctx context.Context, db sqlx.ExtContext, opID string) (string, error) {
	op, err := queryOne(ctx, db, "SELECT input FROM operation_runs WHERE id=$1", opID)
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

func loadProjectTemplateProvisionResources(ctx context.Context, db sqlx.ExtContext, runID string) (map[string]any, map[string]any, map[string]any, []map[string]any, map[string]any, []map[string]any, []map[string]any, error) {
	run, err := queryOne(ctx, db, "SELECT * FROM project_template_runs WHERE id=$1", runID)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, nil, err
	}
	projectID := cleanOptionalID(fmt.Sprint(run["project_id"]))
	if projectID == "" {
		return nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("template run has no project to reconcile")
	}
	project, err := queryOne(ctx, db, "SELECT * FROM projects WHERE id=$1", projectID)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, nil, err
	}
	result := mapFromAny(run["result"])
	repoID := cleanOptionalID(fmt.Sprint(result["repository_id"]))
	var repo map[string]any
	if repoID != "" {
		repo, err = queryOne(ctx, db, "SELECT * FROM project_git_repositories WHERE id=$1", repoID)
	} else {
		repo, err = queryOne(ctx, db, "SELECT * FROM project_git_repositories WHERE project_id=$1 ORDER BY created_at DESC LIMIT 1", projectID)
	}
	if err != nil {
		return nil, nil, nil, nil, nil, nil, nil, err
	}
	remotes, err := queryMaps(ctx, db, "SELECT * FROM git_remotes WHERE project_git_repository_id=$1 ORDER BY created_at, name", repo["id"])
	if err != nil {
		return nil, nil, nil, nil, nil, nil, nil, err
	}
	files, err := queryMaps(ctx, db, "SELECT * FROM project_template_files WHERE project_template_run_id=$1 ORDER BY created_at, path", runID)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, nil, err
	}
	var syncAsset map[string]any
	syncAssetID := cleanOptionalID(fmt.Sprint(result["repo_sync_asset_id"]))
	if syncAssetID != "" {
		syncAsset, err = queryOne(ctx, db, "SELECT * FROM repo_sync_assets WHERE id=$1", syncAssetID)
		if errors.Is(err, ErrNotFound) {
			syncAsset = nil
		} else if err != nil {
			return nil, nil, nil, nil, nil, nil, nil, err
		}
	}
	steps := templateStepsWithProvisionRetry(run["steps"])
	return run, project, repo, remotes, syncAsset, files, steps, nil
}

func (w *ControlWorker) markProjectTemplateProvisionRetryCompleted(ctx context.Context, runID string, repo map[string]any, remotes []map[string]any, files []map[string]any, steps []map[string]any, result map[string]any, provision *gitExecutionResult) error {
	tx, err := w.store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if provision != nil {
		if provisioned, _ := provision.Details["provisioned"].(bool); provisioned {
			if err := markTemplateRepositoryProvisionedTx(ctx, tx, repo, remotes, files, provision); err != nil {
				return err
			}
		}
	}
	resultJSON, err := jsonParam(result)
	if err != nil {
		return err
	}
	stepsJSON, err := jsonParam(steps)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE project_template_runs
		SET status='completed',
			steps=$2::jsonb,
			result=$3::jsonb,
			error_message='',
			finished_at=now(),
			updated_at=now()
		WHERE id=$1`, runID, stepsJSON, resultJSON); err != nil {
		return err
	}
	return tx.Commit()
}

func (w *ControlWorker) markProjectTemplateProvisionRetryFailed(ctx context.Context, runID string, result map[string]any, cause error) error {
	tx, err := w.store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	failedSteps := templateStepsWithProvisionFailure(result["steps"])
	result["steps"] = failedSteps
	errorMessage := truncateProviderError(cause.Error(), providerRunErrorLimit)
	result["error"] = errorMessage
	resultJSON, err := jsonParam(result)
	if err != nil {
		return err
	}
	stepsJSON, err := jsonParam(failedSteps)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE project_template_runs
		SET status='failed',
			steps=$2::jsonb,
			result=$3::jsonb,
			error_message=$4,
			finished_at=now(),
			updated_at=now()
		WHERE id=$1`, runID, stepsJSON, resultJSON, errorMessage); err != nil {
		return err
	}
	return tx.Commit()
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

func createProjectFromTemplateTx(ctx context.Context, tx *sqlx.Tx, opID string) (map[string]any, map[string]any, []map[string]any, map[string]any, []map[string]any, []map[string]any, error) {
	run, err := queryOne(ctx, tx, `
		SELECT ptr.*, pt.defaults AS template_defaults, pt.slug AS template_slug
		FROM project_template_runs ptr
		LEFT JOIN project_templates pt ON pt.id=ptr.project_template_id
		WHERE ptr.operation_run_id=$1
		FOR UPDATE`, opID)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	projectSlug := strings.TrimSpace(fmt.Sprint(run["project_slug"]))
	projectName := strings.TrimSpace(fmt.Sprint(run["project_name"]))
	if projectName == "" || projectSlug == "" {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("template run is missing project name or slug")
	}
	input := mapFromAny(run["input"])
	parameters := mapFromAny(input["parameters"])
	defaults := mapFromAny(run["template_defaults"])
	description := stringFromMap(input, "description")
	project, err := queryOne(ctx, tx, `
		INSERT INTO projects(name, slug, description)
		VALUES ($1, $2, $3)
		RETURNING *`, projectName, projectSlug, description)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	requestedBy := cleanOptionalID(fmt.Sprint(run["requested_by"]))
	if requestedBy != "" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO project_members(project_id, user_id, role)
			VALUES ($1, $2, 'owner')
			ON CONFLICT DO NOTHING`, project["id"], requestedBy); err != nil {
			return nil, nil, nil, nil, nil, nil, err
		}
	}
	repo, err := createTemplateRepositoryTx(ctx, tx, project, defaults, parameters)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	remotes, err := createTemplateRemotesTx(ctx, tx, repo, defaults, parameters)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	syncAsset, err := createTemplateRepoSyncAssetTx(ctx, tx, opID, project, repo, remotes, defaults, parameters)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	files, err := createTemplateFilesTx(ctx, tx, run, project, repo, defaults, parameters)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	steps := completeTemplateSteps(run["steps"], project, repo, remotes, syncAsset, files)
	return project, repo, remotes, syncAsset, files, steps, nil
}

func createTemplateRepositoryTx(ctx context.Context, tx *sqlx.Tx, project map[string]any, defaults, parameters map[string]any) (map[string]any, error) {
	repoDefaults := mapFromAny(defaults["repository"])
	repoParams := mapFromAny(parameters["repository"])
	projectSlug := strings.TrimSpace(fmt.Sprint(project["slug"]))
	projectName := strings.TrimSpace(fmt.Sprint(project["name"]))
	name := firstNonEmptyString(stringFromMap(repoParams, "name"), templateNameWithSuffix(projectSlug, stringFromMap(repoDefaults, "name_suffix"), "service"))
	repoKey := firstNonEmptyString(stringFromMap(repoParams, "repo_key"), templateNameWithSuffix(projectSlug, stringFromMap(repoDefaults, "repo_key_suffix"), "service"))
	displayName := firstNonEmptyString(stringFromMap(repoParams, "display_name"), templateDisplayName(projectName, stringFromMap(repoDefaults, "display_name_suffix"), "Service"))
	repoRole := firstNonEmptyString(stringFromMap(repoParams, "repo_role"), stringFromMap(defaults, "repo_role"), "code")
	defaultBranch := firstNonEmptyString(stringFromMap(repoParams, "default_branch"), stringFromMap(defaults, "default_branch"), "main")
	return queryOne(ctx, tx, `
		INSERT INTO project_git_repositories(project_id, name, repo_key, display_name, repo_role, status, description, default_branch)
		VALUES ($1, $2, $3, $4, $5, 'planned', $6, $7)
		RETURNING *`,
		project["id"],
		name,
		repoKey,
		displayName,
		repoRole,
		"Created from project template; repository provider binding is pending.",
		defaultBranch,
	)
}

func createTemplateRemotesTx(ctx context.Context, tx *sqlx.Tx, repo, defaults, parameters map[string]any) ([]map[string]any, error) {
	remoteItems := templateRemoteItems(defaults, parameters)
	remotes := make([]map[string]any, 0, len(remoteItems))
	for _, item := range remoteItems {
		remote, err := createTemplateRemoteTx(ctx, tx, repo, item)
		if err != nil {
			return nil, err
		}
		remotes = append(remotes, remote)
	}
	return remotes, nil
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

func createTemplateRemoteTx(ctx context.Context, tx *sqlx.Tx, repo, item map[string]any) (map[string]any, error) {
	remoteKey := firstNonEmptyString(stringFromMap(item, "remote_key"), stringFromMap(item, "name"))
	name := firstNonEmptyString(stringFromMap(item, "name"), remoteKey)
	kind := firstNonEmptyString(stringFromMap(item, "kind"), stringFromMap(item, "provider_type"), "git")
	providerType := firstNonEmptyString(stringFromMap(item, "provider_type"), kind)
	remoteRole := firstNonEmptyString(stringFromMap(item, "remote_role"), stringFromMap(item, "role"), "mirror")
	defaultBranch := firstNonEmptyString(stringFromMap(item, "default_branch"), fmt.Sprint(repo["default_branch"]), "main")
	remoteURL := stringFromMap(item, "remote_url")
	urlsValue := item["urls"]
	urls := stringSliceFromAny(urlsValue)
	if remoteURL == "" && len(urls) > 0 {
		remoteURL = urls[0]
	}
	account, hasAccount, err := resolveTemplateProviderAccountTx(ctx, tx, item, providerType)
	if err != nil {
		return nil, err
	}
	urlsJSON, err := jsonParam(urls)
	if err != nil {
		return nil, err
	}
	metadata := mapFromAny(item["metadata"])
	metadata["source"] = "project_template"
	metadata["template_placeholder"] = true
	sourceAccountID := any(nil)
	if hasAccount {
		sourceAccountID = account.ID
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
	metadataJSON, err := jsonParam(metadata)
	if err != nil {
		return nil, err
	}
	remote, err := queryOne(ctx, tx, `
		INSERT INTO git_remotes(
			project_git_repository_id, name, kind, remote_key, provider_type, remote_url, web_url,
			remote_role, is_primary, sync_enabled, protected, latest_sha, last_sync_status, source_account_id,
			urls, default_branch, metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'never', $13, $14::jsonb, $15, $16::jsonb)
		RETURNING *`,
		repo["id"],
		name,
		kind,
		remoteKey,
		providerType,
		remoteURL,
		stringFromMap(item, "web_url"),
		remoteRole,
		boolFromMap(item, "is_primary"),
		boolDefaultFromMap(item, "sync_enabled", true),
		boolFromMap(item, "protected"),
		stringFromMap(item, "latest_sha"),
		sourceAccountID,
		urlsJSON,
		defaultBranch,
		metadataJSON,
	)
	if err != nil {
		return nil, err
	}
	if hasAccount {
		remote["metadata"] = metadata
	}
	return remote, nil
}

func resolveTemplateProviderAccountTx(ctx context.Context, tx *sqlx.Tx, item map[string]any, providerType string) (providerAccountConfig, bool, error) {
	accountID := strings.TrimSpace(stringFromMap(item, "provider_account_id"))
	accountName := strings.TrimSpace(stringFromMap(item, "provider_account_name"))
	if accountID == "" && accountName == "" {
		return providerAccountConfig{}, false, nil
	}
	var (
		account providerAccountConfig
		err     error
	)
	if accountID != "" {
		account, err = loadProviderAccountConfigByID(ctx, tx, accountID)
	} else {
		account, err = loadProviderAccountConfigByName(ctx, tx, accountName)
	}
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

func createTemplateRepoSyncAssetTx(ctx context.Context, tx *sqlx.Tx, opID string, project, repo map[string]any, remotes []map[string]any, defaults, parameters map[string]any) (map[string]any, error) {
	syncParams := mapFromAny(parameters["repo_sync"])
	syncDefaults := mapFromAny(defaults["repo_sync"])
	sourceRemoteID := firstNonEmptyString(
		stringFromMap(syncParams, "source_remote_id"),
		remoteIDByKey(remotes, firstNonEmptyString(stringFromMap(syncParams, "source_remote_key"), stringFromMap(syncDefaults, "source_remote_key"))),
	)
	targetRemoteID := firstNonEmptyString(
		stringFromMap(syncParams, "target_remote_id"),
		remoteIDByKey(remotes, firstNonEmptyString(stringFromMap(syncParams, "target_remote_key"), stringFromMap(syncDefaults, "target_remote_key"))),
	)
	if sourceRemoteID == "" || targetRemoteID == "" {
		if err := logTemplateRepoSyncSkipped(ctx, tx, opID, map[string]any{
			"reason":           "source and target remotes are required",
			"source_remote_id": sourceRemoteID,
			"target_remote_id": targetRemoteID,
		}); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if sourceRemoteID == targetRemoteID {
		if err := logTemplateRepoSyncSkipped(ctx, tx, opID, map[string]any{
			"reason":    "source and target remotes must differ",
			"remote_id": sourceRemoteID,
		}); err != nil {
			return nil, err
		}
		return nil, nil
	}
	repoID := strings.TrimSpace(fmt.Sprint(repo["id"]))
	if ok, err := verifyTemplateRemoteForRepository(ctx, tx, opID, repoID, sourceRemoteID, "source_remote_id"); err != nil || !ok {
		return nil, err
	}
	if ok, err := verifyTemplateRemoteForRepository(ctx, tx, opID, repoID, targetRemoteID, "target_remote_id"); err != nil || !ok {
		return nil, err
	}
	enabled := false
	if value, ok := syncParams["enabled"].(bool); ok {
		enabled = value
	} else if value, ok := syncDefaults["enabled"].(bool); ok {
		enabled = value
	}
	refs, err := jsonParam(mapFromAny(syncParams["refs"]))
	if err != nil {
		return nil, err
	}
	metadata, err := jsonParam(map[string]any{"source": "project_template", "template_placeholder": true})
	if err != nil {
		return nil, err
	}
	return queryOne(ctx, tx, `
		INSERT INTO repo_sync_assets(
			project_id, project_git_repository_id, name, source_remote_id, target_remote_id,
			trigger_mode, sync_mode, transport, driver, refs, enabled, metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11, $12::jsonb)
		RETURNING *`,
		project["id"],
		repo["id"],
		firstNonEmptyString(stringFromMap(syncParams, "name"), stringFromMap(syncDefaults, "name"), "default mirror"),
		sourceRemoteID,
		targetRemoteID,
		firstNonEmptyString(stringFromMap(syncParams, "trigger_mode"), stringFromMap(syncDefaults, "trigger_mode"), "manual"),
		firstNonEmptyString(stringFromMap(syncParams, "sync_mode"), stringFromMap(syncDefaults, "sync_mode"), "selected_refs"),
		firstNonEmptyString(stringFromMap(syncParams, "transport"), stringFromMap(syncDefaults, "transport"), "ssh"),
		firstNonEmptyString(stringFromMap(syncParams, "driver"), stringFromMap(syncDefaults, "driver"), "projectops_worker_git_ssh"),
		refs,
		enabled,
		metadata,
	)
}

func logTemplateRepoSyncSkipped(ctx context.Context, tx *sqlx.Tx, opID string, fields map[string]any) error {
	payload, err := jsonParam(fields)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO operation_logs(operation_run_id, level, message, fields)
		VALUES ($1, 'warn', 'template repo sync asset was not created', $2::jsonb)`, opID, payload)
	return err
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

func createTemplateFilesTx(ctx context.Context, tx *sqlx.Tx, run, project, repo, defaults, parameters map[string]any) ([]map[string]any, error) {
	items := templateFileItems(defaults, parameters)
	files := make([]map[string]any, 0, len(items))
	for _, item := range items {
		file, err := createTemplateFileTx(ctx, tx, run, project, repo, item)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
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

func createTemplateFileTx(ctx context.Context, tx *sqlx.Tx, run, project, repo, item map[string]any) (map[string]any, error) {
	path := safeTemplateFilePath(stringFromMap(item, "path"))
	if path == "" {
		return nil, fmt.Errorf("template file path is required")
	}
	metadata := mapFromAny(item["metadata"])
	metadata["source"] = "project_template"
	metadata["template_placeholder"] = true
	metadataJSON, err := jsonParam(metadata)
	if err != nil {
		return nil, err
	}
	return queryOne(ctx, tx, `
		INSERT INTO project_template_files(
			project_template_run_id, project_template_id, project_id, project_git_repository_id,
			path, kind, content, status, metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'planned', $8::jsonb)
		RETURNING *`,
		run["id"],
		run["project_template_id"],
		project["id"],
		repo["id"],
		path,
		firstNonEmptyString(stringFromMap(item, "kind"), "text"),
		renderTemplateFileContent(stringFromMap(item, "content"), run, project, repo),
		metadataJSON,
	)
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

func verifyTemplateRemoteForRepository(ctx context.Context, tx *sqlx.Tx, opID, repoID, remoteID, field string) (bool, error) {
	if _, err := remoteForRepository(ctx, tx, repoID, remoteID); err != nil {
		if !errors.Is(err, ErrNotFound) {
			return false, err
		}
		fields, jsonErr := jsonParam(map[string]any{
			"field":     field,
			"remote_id": remoteID,
			"repo_id":   repoID,
		})
		if jsonErr != nil {
			return false, jsonErr
		}
		if _, logErr := tx.ExecContext(ctx, `
			INSERT INTO operation_logs(operation_run_id, level, message, fields)
			VALUES ($1, 'warn', 'template repo sync remote does not belong to the created repository', $2::jsonb)`, opID, fields); logErr != nil {
			return false, logErr
		}
		return false, nil
	}
	return true, nil
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

func mergeGitHubActionsResult(result map[string]any, syncResult *GitHubActionsSyncResult) {
	if syncResult == nil {
		return
	}
	result["_github_actions_result"] = syncResult
	result["remote_id"] = syncResult.RemoteID
	result["repository"] = syncResult.Owner + "/" + syncResult.Repo
	result["count"] = len(syncResult.Runs)
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

func (w *ControlWorker) recordGitHubActionsAdapterRun(ctx context.Context, tx *sqlx.Tx, opID string, result map[string]any) error {
	syncResult, ok := result["_github_actions_result"].(*GitHubActionsSyncResult)
	delete(result, "_github_actions_result")
	if !ok || syncResult == nil || syncResult.RemoteID == "" {
		return nil
	}
	result["remote_id"] = syncResult.RemoteID
	result["repository"] = syncResult.Owner + "/" + syncResult.Repo
	result["count"] = len(syncResult.Runs)
	if _, err := tx.ExecContext(ctx, "DELETE FROM github_action_runs WHERE git_remote_id=$1", syncResult.RemoteID); err != nil {
		return err
	}
	for _, run := range syncResult.Runs {
		metadata, err := jsonParam(run.Metadata)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
		INSERT INTO github_action_runs(
			operation_run_id, git_remote_id, external_run_id, workflow_name, run_id,
			branch, commit_sha, status, conclusion, html_url, metadata, started_at, updated_at, synced_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10, $11::jsonb, $12, $13, now()
		)`,
			opID,
			syncResult.RemoteID,
			run.ExternalRunID,
			run.WorkflowName,
			run.RunID,
			run.Branch,
			run.CommitSHA,
			run.Status,
			run.Conclusion,
			run.HTMLURL,
			metadata,
			run.StartedAt,
			run.UpdatedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func markTemplateRepositoryProvisionedTx(ctx context.Context, tx *sqlx.Tx, repo map[string]any, remotes []map[string]any, files []map[string]any, provision *gitExecutionResult) error {
	if provision == nil || provision.Details == nil {
		return nil
	}
	sha := provision.AfterSHA
	remoteID := strings.TrimSpace(fmt.Sprint(provision.Details["remote_id"]))
	if _, err := tx.ExecContext(ctx, `
		UPDATE project_git_repositories
		SET status='active',
			description='Created from project template and initialized in provider repository.',
			updated_at=now()
		WHERE id=$1`, repo["id"]); err != nil {
		return err
	}
	if remoteID != "" && remoteID != "<nil>" {
		remoteURL := strings.TrimSpace(fmt.Sprint(provision.Details["remote_url"]))
		webURL := strings.TrimSpace(fmt.Sprint(provision.Details["web_url"]))
		if _, err := tx.ExecContext(ctx, `
			UPDATE git_remotes
			SET latest_sha=$2,
				last_sync_status='completed',
				remote_url=COALESCE(NULLIF($3, ''), remote_url),
				web_url=COALESCE(NULLIF($4, ''), web_url),
				metadata=metadata || jsonb_build_object(
					'template_placeholder', false,
					'repository_provisioned', true,
					'provider_type', NULLIF($5, ''),
					'repository_name', NULLIF($6, '')
				),
				updated_at=now()
			WHERE id=$1`,
			remoteID,
			sha,
			cleanOptionalText(remoteURL),
			cleanOptionalText(webURL),
			cleanOptionalText(fmt.Sprint(provision.Details["provider_type"])),
			cleanOptionalText(fmt.Sprint(provision.Details["repository_name"])),
		); err != nil {
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
	if _, err := tx.ExecContext(ctx, `
		UPDATE project_template_files
		SET status='pushed',
			metadata=metadata || jsonb_build_object('repository_provisioned', true, 'commit_sha', $2),
			updated_at=now()
		WHERE id = ANY($1::uuid[])`, pq.Array(ids), sha); err != nil {
		return err
	}
	return nil
}

func (w *ControlWorker) markProjectTemplateRunCompleted(ctx context.Context, opID string, repo map[string]any, remotes []map[string]any, files []map[string]any, steps []map[string]any, result map[string]any, provision *gitExecutionResult) error {
	tx, err := w.store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if provision != nil {
		if provisioned, _ := provision.Details["provisioned"].(bool); provisioned {
			if err := markTemplateRepositoryProvisionedTx(ctx, tx, repo, remotes, files, provision); err != nil {
				return err
			}
		}
	}
	resultJSON, err := jsonParam(result)
	if err != nil {
		return err
	}
	stepsJSON, err := jsonParam(steps)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE project_template_runs
		SET status='completed',
			steps=$2::jsonb,
			result=$3::jsonb,
			finished_at=now(),
			updated_at=now()
		WHERE operation_run_id=$1`, opID, stepsJSON, resultJSON); err != nil {
		return err
	}
	return tx.Commit()
}

func (w *ControlWorker) markProjectTemplateRunFailed(ctx context.Context, opID string, result map[string]any, cause error) error {
	tx, err := w.store.DB.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stepsValue := result["steps"]
	if !hasTemplateSteps(stepsValue) {
		run, runErr := queryOne(ctx, tx, "SELECT steps FROM project_template_runs WHERE operation_run_id=$1", opID)
		if runErr != nil {
			return runErr
		}
		stepsValue = run["steps"]
	}
	failedSteps := templateStepsWithStatus(stepsValue, "failed")
	stepsJSON, err := jsonParam(failedSteps)
	if err != nil {
		return err
	}
	result["steps"] = failedSteps
	errorMessage := truncateProviderError(cause.Error(), providerRunErrorLimit)
	result["error"] = errorMessage
	resultJSON, err := jsonParam(result)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE project_template_runs
		SET status='failed',
			steps=$2::jsonb,
			result=$3::jsonb,
			error_message=$4,
			finished_at=now(),
			updated_at=now()
		WHERE operation_run_id=$1`, opID, stepsJSON, resultJSON, errorMessage); err != nil {
		return err
	}
	return tx.Commit()
}

func (w *ControlWorker) recordArgoSyncAdapterRun(ctx context.Context, tx *sqlx.Tx, result map[string]any) error {
	syncResult, ok := result["_argo_sync_result"].(*ArgoSyncResult)
	delete(result, "_argo_sync_result")
	if !ok || syncResult == nil || syncResult.ProjectID == "" || syncResult.ConnectionID == "" {
		return nil
	}
	result["project_id"] = syncResult.ProjectID
	result["connection_id"] = syncResult.ConnectionID
	result["server_url"] = syncResult.ServerURL
	result["count"] = len(syncResult.Apps)
	if _, err := tx.ExecContext(ctx, "DELETE FROM argo_apps WHERE argo_connection_id=$1", syncResult.ConnectionID); err != nil {
		return err
	}
	for _, app := range syncResult.Apps {
		metadata, err := jsonParam(app.Metadata)
		if err != nil {
			return err
		}
		target, err := upsertDeploymentTargetForArgoApp(ctx, tx, syncResult, app)
		if err != nil {
			return err
		}
		argoApp, err := queryOne(ctx, tx, `
			INSERT INTO argo_apps(project_id, argo_connection_id, deployment_target_id, name, namespace, status, metadata, synced_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, now(), now())
			RETURNING *`,
			syncResult.ProjectID,
			syncResult.ConnectionID,
			target["id"],
			app.Name,
			app.Namespace,
			app.Status,
			metadata,
		)
		if err != nil {
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
	_, err := tx.ExecContext(ctx, `
		UPDATE argo_connections
		SET last_sync_status='completed',
			last_sync_error='',
			updated_at=now()
		WHERE id=$1`, syncResult.ConnectionID)
	if err != nil {
		return err
	}
	return nil
}

func upsertDeploymentRecordForArgoApp(ctx context.Context, tx *sqlx.Tx, syncResult *ArgoSyncResult, app ArgoAppInput, target, argoApp map[string]any) error {
	metadata := mapFromAny(app.Metadata)
	revision := firstNonEmptyString(stringFromMap(metadata, "revision"), stringFromMap(metadata, "target_revision"))
	images := stringSliceFromAny(metadata["images"])
	imagesJSON, err := jsonParam(images)
	if err != nil {
		return err
	}
	recordMetadata, err := jsonParam(map[string]any{
		"source":             "argocd",
		"argo_connection_id": syncResult.ConnectionID,
		"server_url":         syncResult.ServerURL,
		"health_status":      stringFromMap(metadata, "health_status"),
		"sync_status":        stringFromMap(metadata, "sync_status"),
	})
	if err != nil {
		return err
	}
	environment := firstNonEmptyString(app.Environment, stringFromMap(target, "environment"))
	namespace := firstNonEmptyString(app.Namespace, stringFromMap(target, "namespace"))
	clusterName := firstNonEmptyString(app.ClusterName, stringFromMap(target, "cluster_name"))
	record, err := queryOne(ctx, tx, `
		INSERT INTO deployment_records(
			project_id, deployment_target_id, argo_connection_id, argo_app_id, name,
			environment, namespace, cluster_name, source, status, revision, image_refs, metadata,
			observed_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'argocd', $9, $10, $11::jsonb, $12::jsonb, now(), now())
		ON CONFLICT(project_id, source, name, environment, namespace, cluster_name)
		DO UPDATE SET
			deployment_target_id=EXCLUDED.deployment_target_id,
			argo_connection_id=EXCLUDED.argo_connection_id,
			argo_app_id=EXCLUDED.argo_app_id,
			status=EXCLUDED.status,
			revision=EXCLUDED.revision,
			image_refs=EXCLUDED.image_refs,
			metadata=EXCLUDED.metadata,
			observed_at=now(),
			updated_at=now()
		RETURNING *`,
		syncResult.ProjectID,
		target["id"],
		syncResult.ConnectionID,
		argoApp["id"],
		app.Name,
		environment,
		namespace,
		clusterName,
		app.Status,
		revision,
		imagesJSON,
		recordMetadata,
	)
	if err != nil {
		return err
	}
	if revision == "" && len(images) == 0 {
		return nil
	}
	rollbackMetadata, err := jsonParam(map[string]any{
		"source":               "argocd",
		"deployment_record_id": record["id"],
		"argo_app_id":          argoApp["id"],
	})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO rollback_points(
			project_id, deployment_record_id, deployment_target_id, name, environment,
			revision, image_refs, source, status, metadata, captured_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, 'argocd', 'available', $8::jsonb, now())
		ON CONFLICT(project_id, source, name, environment, revision)
		DO UPDATE SET
			deployment_record_id=EXCLUDED.deployment_record_id,
			deployment_target_id=EXCLUDED.deployment_target_id,
			image_refs=EXCLUDED.image_refs,
			status='available',
			metadata=EXCLUDED.metadata,
			captured_at=now()`,
		syncResult.ProjectID,
		record["id"],
		target["id"],
		app.Name,
		environment,
		revision,
		imagesJSON,
		rollbackMetadata,
	)
	return err
}

func upsertDeploymentTargetForArgoApp(ctx context.Context, tx *sqlx.Tx, syncResult *ArgoSyncResult, app ArgoAppInput) (map[string]any, error) {
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
	metadata, err := jsonParam(map[string]any{
		"source":             "argocd",
		"argo_connection_id": syncResult.ConnectionID,
		"server_url":         syncResult.ServerURL,
	})
	if err != nil {
		return nil, err
	}
	return queryOne(ctx, tx, `
		INSERT INTO deployment_targets(project_id, name, environment, cluster_name, namespace, source, argo_connection_id, status, metadata, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'argocd', $6, 'unknown', $7::jsonb, now())
		ON CONFLICT(project_id, environment, cluster_name, namespace)
		DO UPDATE SET
			name=EXCLUDED.name,
			source=EXCLUDED.source,
			argo_connection_id=EXCLUDED.argo_connection_id,
			metadata=EXCLUDED.metadata,
			updated_at=now()
		RETURNING *`,
		syncResult.ProjectID,
		name,
		environment,
		clusterName,
		namespace,
		syncResult.ConnectionID,
		metadata,
	)
}

func refreshArgoDeploymentTargetStatus(ctx context.Context, tx *sqlx.Tx, projectID, connectionID string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE deployment_targets dt
		SET status=COALESCE((
			SELECT CASE
				WHEN bool_or(lower(aa.status) IN ('outofsync', 'failed', 'error', 'degraded')) THEN 'OutOfSync'
				WHEN bool_and(lower(aa.status) = 'synced') THEN 'Synced'
				ELSE 'Unknown'
			END
			FROM argo_apps aa
			WHERE aa.deployment_target_id=dt.id
		), 'unknown'),
		updated_at=now()
		WHERE dt.project_id=$1
			AND dt.source='argocd'
			AND EXISTS (
				SELECT 1 FROM argo_apps aa
				WHERE aa.deployment_target_id=dt.id
					AND aa.argo_connection_id=$2
			)`, projectID, connectionID)
	return err
}

func cleanupOrphanArgoDeploymentTargets(ctx context.Context, tx *sqlx.Tx, connectionID string) error {
	_, err := tx.ExecContext(ctx, `
		DELETE FROM deployment_targets dt
		WHERE dt.source='argocd'
			AND dt.argo_connection_id=$1
			AND NOT EXISTS (
				SELECT 1 FROM argo_apps aa
				WHERE aa.deployment_target_id=dt.id
			)`, connectionID)
	return err
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

package app

import (
	"context"
	"fmt"
	"time"
)

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

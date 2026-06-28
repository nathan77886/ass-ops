package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"strings"
	"time"
)

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
			Updates(map[string]any{"status": "completed", "exit_code": exitCode, "stdout": stdout, "stderr": stderr, "error_message": "", "finished_at": validNullTime(time.Now())}).Error; err != nil {
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

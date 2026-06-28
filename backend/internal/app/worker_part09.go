package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"strings"
)

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

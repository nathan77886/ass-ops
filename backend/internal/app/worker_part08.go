package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
)

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

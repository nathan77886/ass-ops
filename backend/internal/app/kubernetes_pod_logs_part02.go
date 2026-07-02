package app

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func runKubernetesPodLogs(ctx context.Context, cfg Config, req kubernetesPodLogRequest) (map[string]any, error) {
	started := time.Now().UTC()
	result := map[string]any{
		"backend":                       "kubernetes_client_logs",
		"backend_state":                 "blocked",
		"live_log_backend":              "kubernetes_client_logs",
		"result_scope":                  "sanitized_live_log_metadata",
		"deployment_target_id":          req.DeploymentTargetID,
		"environment":                   req.Environment,
		"cluster_name":                  req.ClusterName,
		"namespace":                     req.Namespace,
		"pod_name":                      req.PodName,
		"container_name":                req.ContainerName,
		"tail_lines":                    req.TailLines,
		"since_seconds":                 req.SinceSeconds,
		"kubeconfig_bound":              false,
		"kubeconfig_secret_ref_present": req.KubeconfigRef != "",
		"kubeconfig_secret_read":        false,
		"kubernetes_client_created":     false,
		"kubernetes_api_call":           false,
		"argocd_api_call":               false,
		"kubectl_command_invoked":       false,
		"kubernetes_client_invoked":     false,
		"log_stream_opened":             false,
		"log_body_included":             false,
		"redacted_log_body_included":    false,
		"raw_response_included":         false,
		"secret_included":               false,
		"line_count":                    0,
		"preview_line_count":            0,
		"truncated":                     false,
		"preview_truncated":             false,
		"redaction_performed":           false,
		"started_at":                    started.Format(time.RFC3339),
	}
	if !cfg.KubernetesPodLogsEnabled {
		result["live_log_backend"] = "disabled"
		result["backend_state"] = "disabled"
		result["message"] = "pod log audit completed; live Kubernetes log retrieval is disabled"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		return result, nil
	}
	if err := validateKubernetesPodLogRequest(req); err != nil {
		result["backend_state"] = "blocked"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		return result, err
	}
	if strings.TrimSpace(req.KubeconfigSecret) == "" {
		result["backend_state"] = "blocked"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		return result, fmt.Errorf("kubeconfig secret is required")
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result["kubeconfig_bound"] = true
	result["kubeconfig_secret_read"] = true
	result["kubernetes_client_invoked"] = true
	result["kubernetes_client_created"] = true
	result["log_stream_opened"] = true
	result["kubernetes_api_call"] = true
	output, err := kubernetesPodLogsRun(runCtx, req.KubeconfigSecret, req)
	if err != nil {
		result["backend_state"] = "failed"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		return result, err
	}
	result["backend_state"] = "completed"
	result["line_count"] = countNonEmptyLines(output)
	if cfg.KubernetesLogPreviewEnabled {
		preview, truncated := sanitizedKubernetesLogPreview(output, kubernetesLogPreviewMaxBytes)
		result["redacted_log_preview"] = preview
		result["redacted_log_body_included"] = preview != ""
		result["preview_line_count"] = countNonEmptyLines(preview)
		result["preview_truncated"] = truncated
		result["redaction_performed"] = true
		result["result_scope"] = "redacted_live_log_preview"
	} else {
		result["redacted_log_body_included"] = false
	}
	result["truncated"] = boolOnlyFromAny(result["preview_truncated"])
	result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
	result["message"] = "pod log audit completed with live Kubernetes metadata; log body was not stored"
	if cfg.KubernetesLogPreviewEnabled {
		result["message"] = "pod log audit completed with a redacted preview; raw log body was not stored"
	}
	return result, nil
}

func runKubernetesPodRestart(ctx context.Context, cfg Config, req kubernetesPodRestartRequest) (map[string]any, error) {
	started := time.Now().UTC()
	result := map[string]any{
		"backend":                       "kubernetes_client_rollout_restart",
		"backend_state":                 "blocked",
		"result_scope":                  "sanitized_rollout_restart_metadata",
		"deployment_target_id":          req.DeploymentTargetID,
		"environment":                   req.Environment,
		"cluster_name":                  req.ClusterName,
		"namespace":                     req.Namespace,
		"deployment_name":               req.DeploymentName,
		"kubeconfig_bound":              false,
		"kubeconfig_secret_ref_present": req.KubeconfigRef != "",
		"kubeconfig_secret_read":        false,
		"kubernetes_client_created":     false,
		"kubernetes_api_call":           false,
		"argocd_api_call":               false,
		"kubectl_command_invoked":       false,
		"kubernetes_client_invoked":     false,
		"rollout_restart_invoked":       false,
		"rollout_status_checked":        false,
		"server_dry_run_checked":        false,
		"stdout_included":               false,
		"stderr_included":               false,
		"raw_response_included":         false,
		"secret_included":               false,
		"log_body_included":             false,
		"started_at":                    started.Format(time.RFC3339),
		"suppressed_fields":             []string{"kubeconfig", "cluster_token", "authorization_header", "client_certificate", "client_key", "raw_kubernetes_response", "stdout", "stderr", "pod_env", "secret_env", "volume_secret"},
	}
	if !cfg.KubernetesRestartsEnabled {
		result["backend_state"] = "disabled"
		result["message"] = "pod restart is disabled"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		return result, nil
	}
	if err := validateKubernetesPodRestartRequest(req); err != nil {
		result["backend_state"] = "blocked"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		return result, err
	}
	if strings.TrimSpace(req.KubeconfigSecret) == "" {
		result["backend_state"] = "blocked"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		return result, fmt.Errorf("kubeconfig secret is required")
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result["kubeconfig_bound"] = true
	result["kubeconfig_secret_read"] = true
	result["kubernetes_client_invoked"] = true
	result["kubernetes_client_created"] = true
	result["kubernetes_api_call"] = true
	if err := kubernetesRestartDeploymentRun(runCtx, req.KubeconfigSecret, req); err != nil {
		result["backend_state"] = "failed"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		return result, err
	}
	result["rbac_can_i_checked"] = true
	result["server_dry_run_checked"] = true
	result["rollout_restart_invoked"] = true
	result["backend_state"] = "completed"
	result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
	result["message"] = "deployment rollout restart requested; raw Kubernetes response was not stored"
	return result, nil
}

func sanitizedKubernetesLogPreview(output string, maxBytes int) (string, bool) {
	if maxBytes <= 0 {
		maxBytes = kubernetesLogPreviewMaxBytes
	}
	output = strings.ReplaceAll(output, "\r\n", "\n")
	output = strings.ReplaceAll(output, "\r", "\n")
	lines := strings.Split(output, "\n")
	var builder strings.Builder
	truncated := false
	for index, line := range lines {
		if strings.Contains(strings.ToUpper(line), "BEGIN ") && strings.Contains(strings.ToUpper(line), "PRIVATE KEY") {
			line = "<redacted-private-key>"
		} else {
			line = kubernetesLogSecretPattern.ReplaceAllString(line, "${1}${2}${3}${4}<redacted>")
		}
		if builder.Len() > 0 || index > 0 {
			if builder.Len()+1 > maxBytes {
				truncated = true
				break
			}
			builder.WriteByte('\n')
		}
		if builder.Len()+len(line) > maxBytes {
			remaining := maxBytes - builder.Len()
			if remaining > 0 {
				builder.WriteString(validUTF8Prefix(line, remaining))
			}
			truncated = true
			break
		}
		builder.WriteString(line)
	}
	return builder.String(), truncated
}

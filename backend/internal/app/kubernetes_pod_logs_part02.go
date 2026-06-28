package app

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func runKubernetesPodLogs(ctx context.Context, cfg Config, req kubernetesPodLogRequest) (map[string]any, error) {
	started := time.Now().UTC()
	result := map[string]any{
		"backend":                       "kubectl_logs",
		"backend_state":                 "blocked",
		"live_log_backend":              "kubectl_logs",
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
	kubeconfigPath, err := resolveKubeconfigRef(cfg, req.KubeconfigRef)
	if err != nil {
		result["backend_state"] = "blocked"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		return result, err
	}
	args := kubectlLogsArgs(kubeconfigPath, req)
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(runCtx, kubectlBinary(cfg), args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	result["kubeconfig_bound"] = true
	result["kubectl_command_invoked"] = true
	result["log_stream_opened"] = true
	result["kubernetes_api_call"] = true
	if err := cmd.Run(); err != nil {
		result["backend_state"] = "failed"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		return result, fmt.Errorf("kubectl logs failed")
	}
	result["backend_state"] = "completed"
	result["line_count"] = countNonEmptyLines(stdout.String())
	if cfg.KubernetesLogPreviewEnabled {
		preview, truncated := sanitizedKubernetesLogPreview(stdout.String(), kubernetesLogPreviewMaxBytes)
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
	_ = stderr
	return result, nil
}

func runKubernetesPodRestart(ctx context.Context, cfg Config, req kubernetesPodRestartRequest) (map[string]any, error) {
	started := time.Now().UTC()
	result := map[string]any{
		"backend":                       "kubectl_rollout_restart",
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
	kubeconfigPath, err := resolveKubeconfigRef(cfg, req.KubeconfigRef)
	if err != nil {
		result["backend_state"] = "blocked"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		return result, err
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"--kubeconfig", kubeconfigPath, "-n", req.Namespace, "rollout", "restart", "deployment/" + req.DeploymentName}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(runCtx, kubectlBinary(cfg), args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	result["kubeconfig_bound"] = true
	result["kubectl_command_invoked"] = true
	result["kubernetes_client_created"] = true
	result["kubernetes_api_call"] = true
	canIArgs := []string{"--kubeconfig", kubeconfigPath, "-n", req.Namespace, "auth", "can-i", "patch", "deployment/" + req.DeploymentName}
	canICmd := exec.CommandContext(runCtx, kubectlBinary(cfg), canIArgs...)
	canICmd.Stdout = &stdout
	canICmd.Stderr = &stderr
	if err := canICmd.Run(); err != nil || !kubectlCanIAllowed(stdout.String()) {
		result["backend_state"] = "failed"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		_ = stdout
		_ = stderr
		return result, fmt.Errorf("kubectl auth can-i patch deployment failed")
	}
	result["rbac_can_i_checked"] = true
	stdout.Reset()
	stderr.Reset()
	dryRunArgs := append(args, "--dry-run=server")
	dryRunCmd := exec.CommandContext(runCtx, kubectlBinary(cfg), dryRunArgs...)
	dryRunCmd.Stdout = &stdout
	dryRunCmd.Stderr = &stderr
	if err := dryRunCmd.Run(); err != nil {
		result["backend_state"] = "failed"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		_ = stdout
		_ = stderr
		return result, fmt.Errorf("kubectl rollout restart dry-run failed")
	}
	result["server_dry_run_checked"] = true
	stdout.Reset()
	stderr.Reset()
	cmd = exec.CommandContext(runCtx, kubectlBinary(cfg), args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	result["rollout_restart_invoked"] = true
	if err := cmd.Run(); err != nil {
		result["backend_state"] = "failed"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		_ = stdout
		_ = stderr
		return result, fmt.Errorf("kubectl rollout restart failed")
	}
	result["backend_state"] = "completed"
	result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
	result["message"] = "deployment rollout restart requested; command output and raw Kubernetes response were not stored"
	_ = stdout
	_ = stderr
	return result, nil
}

func kubectlCanIAllowed(output string) bool {
	fields := strings.Fields(strings.ToLower(output))
	return len(fields) > 0 && fields[0] == "yes"
}

func kubectlLogsArgs(kubeconfigPath string, req kubernetesPodLogRequest) []string {
	args := []string{"--kubeconfig", kubeconfigPath, "-n", req.Namespace, "logs", req.PodName}
	if req.ContainerName != "" {
		args = append(args, "-c", req.ContainerName)
	}
	tailLines := req.TailLines
	if tailLines <= 0 {
		tailLines = 200
	}
	if tailLines > 200 {
		tailLines = 200
	}
	args = append(args, "--tail", fmt.Sprint(tailLines))
	if req.SinceSeconds > 0 {
		since := req.SinceSeconds
		if since > 86400 {
			since = 86400
		}
		args = append(args, "--since", fmt.Sprintf("%ds", since))
	}
	return args
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

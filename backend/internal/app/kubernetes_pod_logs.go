package app

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	kubernetesNamespacePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	kubernetesPodPattern       = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)
	kubernetesContainerPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	kubeconfigRefPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,252}$`)
)

type kubernetesPodLogRequest struct {
	ProjectID          string
	DeploymentTargetID string
	Environment        string
	ClusterName        string
	Namespace          string
	PodName            string
	ContainerName      string
	TailLines          int
	SinceSeconds       int
	KubeconfigRef      string
}

func kubernetesPodLogBackendPlan(cfg Config, target map[string]any) map[string]any {
	enabled := cfg.KubernetesPodLogsEnabled
	refPresent := boolOnlyFromAny(target["kubeconfig_secret_ref_present"])
	ref := cleanOptionalText(fmt.Sprint(target["kubeconfig_secret_ref"]))
	tokenReviewed := cleanPreviewString(target["token_subject_review_status"]) == "reviewed"
	rbacReviewed := cleanPreviewString(target["rbac_read_logs_status"]) == "reviewed"
	kubeEnvBound := cleanOptionalID(fmt.Sprint(target["kubernetes_environment_id"])) != ""
	kubeEnvReady := cleanPreviewString(target["kubernetes_environment_status"]) == "ready"
	blockers := []string{}
	if !enabled {
		blockers = append(blockers, "kubernetes_logs_backend_disabled")
	}
	if !kubeEnvBound {
		blockers = append(blockers, "kubernetes_environment_not_configured")
	}
	if !refPresent {
		blockers = append(blockers, "kubeconfig_secret_ref_missing")
	}
	if !tokenReviewed {
		blockers = append(blockers, "token_subject_review_not_performed")
	}
	if !rbacReviewed {
		blockers = append(blockers, "rbac_read_logs_review_not_performed")
	}
	if !kubeEnvReady {
		blockers = append(blockers, "kubernetes_environment_not_ready")
	}
	kubeconfigResolved := false
	if enabled && ref != "" {
		if _, err := resolveKubeconfigRefMetadata(cfg, ref, false); err == nil {
			kubeconfigResolved = true
		} else {
			blockers = append(blockers, "kubeconfig_secret_ref_not_resolvable")
		}
	}
	kubectlAvailable := false
	if enabled {
		if _, err := exec.LookPath(kubectlBinary(cfg)); err == nil {
			kubectlAvailable = true
		} else {
			blockers = append(blockers, "kubectl_binary_not_available")
		}
	}
	ready := enabled && kubeEnvBound && refPresent && tokenReviewed && rbacReviewed && kubeEnvReady && kubeconfigResolved && kubectlAvailable
	return map[string]any{
		"mode":                           "kubernetes_pod_log_backend_readiness",
		"backend":                        "kubectl_logs",
		"enabled":                        enabled,
		"ready":                          ready,
		"result_scope":                   "sanitized_live_log_metadata",
		"kubernetes_environment_bound":   kubeEnvBound,
		"kubernetes_environment_ready":   kubeEnvReady,
		"kubeconfig_secret_ref_present":  refPresent,
		"kubeconfig_secret_ref_resolved": kubeconfigResolved,
		"kubectl_binary_available":       kubectlAvailable,
		"token_subject_reviewed":         tokenReviewed,
		"rbac_read_logs_reviewed":        rbacReviewed,
		"kubeconfig_secret_read":         false,
		"kubeconfig_included":            false,
		"log_body_included":              false,
		"raw_response_included":          false,
		"blocked_reasons":                blockers,
		"suppressed_fields":              []string{"kubeconfig", "cluster_token", "authorization_header", "client_certificate", "client_key", "log_body", "redacted_log_body", "raw_kubernetes_response"},
	}
}

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
		"truncated":                     false,
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
	result["truncated"] = false
	result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
	result["message"] = "pod log audit completed with live Kubernetes metadata; log body was not stored"
	_ = stderr
	return result, nil
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
	if tailLines > 1000 {
		tailLines = 1000
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

func validateKubernetesPodLogRequest(req kubernetesPodLogRequest) error {
	if !kubernetesNamespacePattern.MatchString(req.Namespace) || len(req.Namespace) > 63 {
		return fmt.Errorf("invalid Kubernetes namespace")
	}
	if !kubernetesPodPattern.MatchString(req.PodName) || len(req.PodName) > 253 {
		return fmt.Errorf("invalid Kubernetes pod name")
	}
	if req.ContainerName != "" && (!kubernetesContainerPattern.MatchString(req.ContainerName) || len(req.ContainerName) > 63) {
		return fmt.Errorf("invalid Kubernetes container name")
	}
	if req.KubeconfigRef == "" {
		return fmt.Errorf("kubeconfig secret ref is required")
	}
	return nil
}

func resolveKubeconfigRef(cfg Config, ref string) (string, error) {
	return resolveKubeconfigRefMetadata(cfg, ref, true)
}

func resolveKubeconfigRefMetadata(cfg Config, ref string, validateContent bool) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("kubeconfig secret ref is required")
	}
	if containsSecretLikeMaterial(ref) || strings.Contains(ref, "\x00") || strings.HasPrefix(ref, "/") || strings.Contains(ref, `\`) || strings.Contains(ref, "..") || !kubeconfigRefPattern.MatchString(ref) {
		return "", fmt.Errorf("invalid kubeconfig secret ref")
	}
	cleaned := filepath.Clean(ref)
	if cleaned == "." || strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("invalid kubeconfig secret ref")
	}
	baseDir := strings.TrimSpace(cfg.KubeconfigSecretDir)
	if baseDir == "" {
		baseDir = "/etc/assops/kubeconfigs"
	}
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("invalid kubeconfig secret dir")
	}
	pathAbs, err := filepath.Abs(filepath.Join(baseAbs, cleaned))
	if err != nil {
		return "", fmt.Errorf("invalid kubeconfig secret ref")
	}
	rel, err := filepath.Rel(baseAbs, pathAbs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("kubeconfig secret ref escapes configured dir")
	}
	realPath, err := filepath.EvalSymlinks(pathAbs)
	if err != nil {
		return "", fmt.Errorf("kubeconfig secret ref is not resolvable")
	}
	realAbs, err := filepath.Abs(realPath)
	if err != nil {
		return "", fmt.Errorf("invalid kubeconfig secret ref")
	}
	relReal, err := filepath.Rel(baseAbs, realAbs)
	if err != nil || relReal == "." || strings.HasPrefix(relReal, "..") || filepath.IsAbs(relReal) {
		return "", fmt.Errorf("kubeconfig secret ref escapes configured dir")
	}
	info, err := os.Stat(realAbs)
	if err != nil {
		return "", fmt.Errorf("kubeconfig secret ref is not resolvable")
	}
	if info.IsDir() {
		return "", fmt.Errorf("kubeconfig secret ref points to a directory")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return "", fmt.Errorf("kubeconfig secret ref is group/world writable")
	}
	if info.Size() <= 0 || info.Size() > 1024*1024 {
		return "", fmt.Errorf("kubeconfig secret ref has invalid size")
	}
	if validateContent {
		content, err := os.ReadFile(realAbs)
		if err != nil {
			return "", fmt.Errorf("kubeconfig secret ref is not readable")
		}
		if !looksLikeKubeconfig(content, info.Mode()) {
			return "", fmt.Errorf("kubeconfig secret ref is not a valid kubeconfig shape")
		}
	}
	return realAbs, nil
}

func kubectlBinary(cfg Config) string {
	if strings.TrimSpace(cfg.KubectlPath) == "" {
		return "kubectl"
	}
	return strings.TrimSpace(cfg.KubectlPath)
}

func countNonEmptyLines(output string) int {
	output = strings.TrimRight(output, "\r\n")
	if output == "" {
		return 0
	}
	count := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func looksLikeKubeconfig(content []byte, mode fs.FileMode) bool {
	if mode.Type() != 0 {
		return false
	}
	value := string(content)
	return strings.Contains(value, "apiVersion:") &&
		strings.Contains(value, "clusters:") &&
		strings.Contains(value, "contexts:") &&
		strings.Contains(value, "users:")
}

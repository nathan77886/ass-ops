package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	kubernetesNamespacePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	kubernetesPodPattern       = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)
	kubernetesContainerPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	kubeconfigRefPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,252}$`)
	kubernetesLogSecretPattern = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*)(?:(?:bearer|basic)\s+)?[^\s'",;]+|(set-cookie\s*:\s*)[^\r\n]+|((?:bearer|basic)\s+)[A-Za-z0-9._~+/=-]+|(["']?(?:password|passwd|pwd|token|secret|cookie|api[_-]?key|x[_-]?api[_-]?key|x[_-]?auth[_-]?token|private[_-]?key|client[_-]?secret|access[_-]?(?:key|token)|secret[_-]?key|auth[_-]?token|client[_-]?key[_-]?data|client[_-]?certificate[_-]?data|certificate[_-]?authority[_-]?data)["']?\s*[:=]\s*["']?)[^"',;\s}]+`)
)

const kubernetesLogPreviewMaxBytes = 64 * 1024

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
	KubeconfigSecret   string
}

type kubernetesPodListRequest struct {
	DeploymentTargetID string
	Environment        string
	ClusterName        string
	Namespace          string
	KubeconfigRef      string
	KubeconfigSecret   string
}

type kubernetesPodRestartRequest struct {
	ProjectID          string
	DeploymentTargetID string
	Environment        string
	ClusterName        string
	Namespace          string
	DeploymentName     string
	KubeconfigRef      string
	KubeconfigSecret   string
}

func kubernetesPodLogBackendPlan(cfg Config, target map[string]any) map[string]any {
	enabled := cfg.KubernetesPodLogsEnabled
	refPresent := boolOnlyFromAny(target["kubeconfig_secret_ref_present"])
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
	kubeconfigConfigured := refPresent
	ready := enabled && kubeEnvBound && refPresent && tokenReviewed && rbacReviewed && kubeEnvReady && kubeconfigConfigured
	return map[string]any{
		"mode":                          "kubernetes_pod_log_backend_readiness",
		"backend":                       "kubernetes_client_logs",
		"enabled":                       enabled,
		"ready":                         ready,
		"result_scope":                  "sanitized_live_log_metadata",
		"kubernetes_environment_bound":  kubeEnvBound,
		"kubernetes_environment_ready":  kubeEnvReady,
		"kubeconfig_secret_ref_present": refPresent,
		"kubeconfig_secret_configured":  kubeconfigConfigured,
		"kubernetes_client_available":   true,
		"token_subject_reviewed":        tokenReviewed,
		"rbac_read_logs_reviewed":       rbacReviewed,
		"kubeconfig_secret_read":        false,
		"kubeconfig_included":           false,
		"log_body_included":             false,
		"raw_response_included":         false,
		"blocked_reasons":               blockers,
		"suppressed_fields":             []string{"kubeconfig", "cluster_token", "authorization_header", "client_certificate", "client_key", "log_body", "redacted_log_body", "raw_kubernetes_response"},
	}
}

func runKubernetesPodList(ctx context.Context, cfg Config, req kubernetesPodListRequest) (map[string]any, error) {
	started := time.Now().UTC()
	result := map[string]any{
		"backend":                       "kubernetes_client_get_pods",
		"backend_state":                 "blocked",
		"result_scope":                  "sanitized_pod_metadata",
		"deployment_target_id":          req.DeploymentTargetID,
		"environment":                   req.Environment,
		"cluster_name":                  req.ClusterName,
		"namespace":                     req.Namespace,
		"kubeconfig_bound":              false,
		"kubeconfig_secret_ref_present": req.KubeconfigRef != "",
		"kubeconfig_secret_read":        false,
		"kubernetes_client_created":     false,
		"kubernetes_api_call":           false,
		"kubectl_command_invoked":       false,
		"kubernetes_client_invoked":     false,
		"raw_response_included":         false,
		"secret_included":               false,
		"log_body_included":             false,
		"items":                         []map[string]any{},
		"item_count":                    0,
		"started_at":                    started.Format(time.RFC3339),
		"suppressed_fields":             []string{"kubeconfig", "cluster_token", "authorization_header", "client_certificate", "client_key", "raw_kubernetes_response", "pod_env", "secret_env", "volume_secret"},
	}
	if !cfg.KubernetesPodLogsEnabled {
		result["backend_state"] = "disabled"
		result["message"] = "pod metadata listing is disabled"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		return result, nil
	}
	if !kubernetesNamespacePattern.MatchString(req.Namespace) || len(req.Namespace) > 63 {
		result["backend_state"] = "blocked"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		return result, fmt.Errorf("invalid Kubernetes namespace")
	}
	if req.KubeconfigRef == "" {
		result["backend_state"] = "blocked"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		return result, fmt.Errorf("kubeconfig secret ref is required")
	}
	if strings.TrimSpace(req.KubeconfigSecret) == "" {
		result["backend_state"] = "blocked"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		return result, fmt.Errorf("kubeconfig secret is required")
	}
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	result["kubeconfig_bound"] = true
	result["kubeconfig_secret_read"] = true
	result["kubernetes_client_invoked"] = true
	result["kubernetes_client_created"] = true
	result["kubernetes_api_call"] = true
	items, err := kubernetesListPodsRun(runCtx, req.KubeconfigSecret, req.Namespace)
	if err != nil {
		result["backend_state"] = "failed"
		result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
		return result, err
	}
	result["backend_state"] = "completed"
	result["items"] = items
	result["item_count"] = len(items)
	result["finished_at"] = time.Now().UTC().Format(time.RFC3339)
	result["message"] = "pod metadata listed; raw Kubernetes response and log bodies were not stored"
	return result, nil
}

func sanitizeKubernetesPodList(data []byte) ([]map[string]any, error) {
	var payload struct {
		Items []struct {
			Metadata struct {
				Name              string `json:"name"`
				CreationTimestamp string `json:"creationTimestamp"`
			} `json:"metadata"`
			Spec struct {
				Containers []struct {
					Name string `json:"name"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					Name         string `json:"name"`
					Ready        bool   `json:"ready"`
					RestartCount int    `json:"restartCount"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("invalid Kubernetes pod list response")
	}
	items := make([]map[string]any, 0, len(payload.Items))
	for _, pod := range payload.Items {
		name := strings.TrimSpace(pod.Metadata.Name)
		if name == "" || !kubernetesPodPattern.MatchString(name) || len(name) > 253 {
			continue
		}
		containers := make([]string, 0, len(pod.Spec.Containers))
		seenContainers := map[string]bool{}
		for _, container := range pod.Spec.Containers {
			containerName := strings.TrimSpace(container.Name)
			if containerName == "" || !kubernetesContainerPattern.MatchString(containerName) || len(containerName) > 63 || seenContainers[containerName] {
				continue
			}
			seenContainers[containerName] = true
			containers = append(containers, containerName)
		}
		readyContainers := 0
		restartCount := 0
		for _, status := range pod.Status.ContainerStatuses {
			if status.Ready {
				readyContainers++
			}
			if status.RestartCount > 0 {
				restartCount += status.RestartCount
			}
		}
		phase := cleanOptionalText(pod.Status.Phase)
		if phase == "" {
			phase = "unknown"
		}
		items = append(items, map[string]any{
			"name":             name,
			"phase":            phase,
			"containers":       containers,
			"container_count":  len(containers),
			"ready_containers": readyContainers,
			"restart_count":    restartCount,
			"created_at":       cleanOptionalText(pod.Metadata.CreationTimestamp),
		})
	}
	return items, nil
}

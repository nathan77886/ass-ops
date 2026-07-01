package app

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

func parseAssopsKeyValueLines(output string) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || !strings.HasPrefix(key, "ASSOPS_") {
			continue
		}
		result[key] = strings.TrimSpace(value)
	}
	return result
}

func publicURLHostOnly(value string) string {
	value = strings.TrimSpace(value)
	if ip := net.ParseIP(value); ip != nil && !isPublicIP(ip) {
		return "private-cluster-endpoint"
	}
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Hostname() == "" {
		return cleanOptionalText(value)
	}
	host := parsed.Hostname()
	if ip := net.ParseIP(host); ip != nil && !isPublicIP(ip) {
		return "private-cluster-endpoint"
	}
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return "private-cluster-endpoint"
	}
	return host
}

func safeSSHDiscoveryMap(discovery sshKubernetesDiscovery) map[string]any {
	return map[string]any{
		"status":            discovery.Status,
		"kind":              discovery.Kind,
		"context":           discovery.Context,
		"namespace":         discovery.Namespace,
		"cluster_name":      discovery.ClusterName,
		"server_host":       discovery.ServerHost,
		"service_account":   discovery.ServiceAccount,
		"blocked_reasons":   discovery.BlockedReasons,
		"suppressed_fields": discovery.SuppressedFields,
	}
}

func kubernetesImportPreviewPayload(machine GormSSHMachine, discovery sshKubernetesDiscovery) map[string]any {
	return map[string]any{
		"status":                   discovery.Status,
		"source_ssh_machine_id":    machine.ID,
		"source_ssh_machine_name":  machine.Name,
		"discovery":                safeSSHDiscoveryMap(discovery),
		"suggested_environment":    suggestedKubernetesEnvironment(machine, discovery),
		"stdout_included":          false,
		"stderr_included":          false,
		"kubeconfig_body_included": false,
	}
}

func suggestedKubernetesEnvironment(machine GormSSHMachine, discovery sshKubernetesDiscovery) map[string]any {
	return map[string]any{
		"name":                        cleanOptionalText(firstNonEmptyString(machine.Name+" "+discovery.Namespace, discovery.ClusterName)),
		"environment":                 cleanOptionalText(firstNonEmptyString(discovery.Kind, "kubernetes")),
		"cluster_name":                discovery.ClusterName,
		"namespace":                   discovery.Namespace,
		"kubeconfig_secret_ref":       sshMachineKubeconfigSecretRef(machine),
		"service_account":             discovery.ServiceAccount,
		"token_subject_review_status": "not_reviewed",
		"rbac_read_logs_status":       "not_reviewed",
		"rbac_restart_pods_status":    "not_reviewed",
		"status":                      "metadata_only",
	}
}

func sshMachineKubeconfigSecretRef(machine GormSSHMachine) string {
	metadata := mapFromAny(machine.Metadata.Data)
	if ref := cleanOptionalText(firstNonEmptyString(
		metadataString(metadata["kubeconfig_secret_ref"]),
		metadataString(metadata["kubeconfig_ref"]),
	)); ref != "" {
		return ref
	}
	kubernetes := mapFromAny(metadata["kubernetes"])
	return cleanOptionalText(firstNonEmptyString(
		metadataString(kubernetes["kubeconfig_secret_ref"]),
		metadataString(kubernetes["kubeconfig_ref"]),
	))
}

func metadataString(value any) string {
	return cleanOptionalText(fmt.Sprint(value))
}

func looksLikeArgoService(name string, labels map[string]string) bool {
	lowerName := strings.ToLower(strings.TrimSpace(name))
	if strings.Contains(lowerName, "argocd") || strings.Contains(lowerName, "argo-cd") {
		return true
	}
	for key, value := range labels {
		lowerKey := strings.ToLower(strings.TrimSpace(key))
		lowerValue := strings.ToLower(strings.TrimSpace(value))
		if (lowerKey == "app.kubernetes.io/part-of" || lowerKey == "app.kubernetes.io/name" || lowerKey == "app") && strings.Contains(lowerValue, "argocd") {
			return true
		}
	}
	return false
}

func uniqueArgoCandidates(items []argoServiceCandidate) []argoServiceCandidate {
	out := []argoServiceCandidate{}
	seen := map[string]bool{}
	for _, item := range items {
		key := item.Kind + "\x00" + item.Namespace + "\x00" + item.Name + "\x00" + item.URL
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

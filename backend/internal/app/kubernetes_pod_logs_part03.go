package app

import (
	"fmt"
	"io/fs"
	"strings"
	"unicode/utf8"
)

func validUTF8Prefix(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		if maxBytes <= 0 {
			return ""
		}
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

func validateKubernetesPodRestartRequest(req kubernetesPodRestartRequest) error {
	if !kubernetesNamespacePattern.MatchString(req.Namespace) || len(req.Namespace) > 63 {
		return fmt.Errorf("invalid Kubernetes namespace")
	}
	if !kubernetesPodPattern.MatchString(req.DeploymentName) || len(req.DeploymentName) > 253 {
		return fmt.Errorf("invalid Kubernetes deployment name")
	}
	if req.KubeconfigRef == "" {
		return fmt.Errorf("kubeconfig secret ref is required")
	}
	return nil
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

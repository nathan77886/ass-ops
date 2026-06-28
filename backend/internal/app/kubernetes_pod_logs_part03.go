package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm/clause"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var (
	importedKubeconfigConnectionTest = runImportedKubeconfigConnectionTest
	importedKubeconfigSSHRun         = func(ctx context.Context, request sshRunRequest) (string, string, int, error) {
		return nativeSSHRunner{}.Run(ctx, request)
	}
)

const importedKubeconfigMaxBytes = 1024 * 1024

func (s *Server) upsertImportedKubernetesEnvironment(ctx context.Context, machine GormSSHMachine, discovery sshKubernetesDiscovery, req struct {
	Name                string `json:"name"`
	Environment         string `json:"environment"`
	KubeconfigSecretRef string `json:"kubeconfig_secret_ref"`
	ServiceAccount      string `json:"service_account"`
	Status              string `json:"status"`
}) (GormKubernetesEnvironment, error) {
	name := cleanOptionalText(firstNonEmptyString(req.Name, machine.Name+" "+discovery.Namespace))
	environment := cleanOptionalText(firstNonEmptyString(req.Environment, discovery.Kind))
	kubeconfigRef := cleanOptionalText(req.KubeconfigSecretRef)
	accessMode := "local_kubeconfig"
	if s.cfg.KubernetesSSHKubectlEnabled {
		accessMode = "ssh_kubectl"
	} else {
		ref, err := s.materializeImportedKubeconfig(ctx, machine, discovery)
		if err != nil {
			return GormKubernetesEnvironment{}, err
		}
		kubeconfigRef = ref
	}
	serviceAccount := cleanOptionalText(firstNonEmptyString(req.ServiceAccount, discovery.ServiceAccount))
	status := cleanKubernetesEnvironmentStatus(req.Status)
	if name == "" || environment == "" || discovery.ClusterName == "" || discovery.Namespace == "" {
		return GormKubernetesEnvironment{}, fmt.Errorf("name, environment, cluster_name, and namespace are required")
	}
	if len(name) > 253 || len(environment) > 63 || len(discovery.ClusterName) > 253 || len(discovery.Namespace) > 63 || len(kubeconfigRef) > 253 || len(serviceAccount) > 253 {
		return GormKubernetesEnvironment{}, fmt.Errorf("kubernetes environment fields exceed allowed length")
	}
	if containsSecretLikeMaterial(kubeconfigRef) || containsSecretLikeMaterial(serviceAccount) {
		return GormKubernetesEnvironment{}, fmt.Errorf("kubernetes environment metadata must reference names only, not credential material")
	}
	model := GormKubernetesEnvironment{
		ProjectID:                machine.ProjectID,
		Name:                     name,
		Environment:              environment,
		ClusterName:              discovery.ClusterName,
		Namespace:                discovery.Namespace,
		KubeconfigSecretRef:      kubeconfigRef,
		ServiceAccount:           serviceAccount,
		TokenSubjectReviewStatus: "not_reviewed",
		RBACReadLogsStatus:       "not_reviewed",
		PodRestartStatus:         "not_reviewed",
		Status:                   status,
		Metadata: JSONValue{Data: map[string]any{
			"source":                  "ssh_machine_import",
			"source_ssh_machine_id":   machine.ID,
			"source_ssh_machine_name": machine.Name,
			"kubernetes_kind":         discovery.Kind,
			"kubernetes_access_mode":  accessMode,
			"context":                 discovery.Context,
			"server_host":             discovery.ServerHost,
		}},
	}
	err := s.store.Gorm.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "project_id"}, {Name: "environment"}, {Name: "cluster_name"}, {Name: "namespace"}},
		DoUpdates: clause.Assignments(map[string]any{"name": model.Name, "kubeconfig_secret_ref": model.KubeconfigSecretRef, "service_account": model.ServiceAccount, "status": model.Status, "metadata": model.Metadata}),
	}).Create(&model).Error
	if err != nil {
		return model, err
	}
	err = s.store.Gorm.WithContext(ctx).Where(&GormKubernetesEnvironment{ProjectID: machine.ProjectID, Environment: environment, ClusterName: discovery.ClusterName, Namespace: discovery.Namespace}).First(&model).Error
	return model, err
}

func (s *Server) materializeImportedKubeconfig(ctx context.Context, machine GormSSHMachine, discovery sshKubernetesDiscovery) (string, error) {
	if discovery.RemoteKubeconfig == "" && discovery.Kind != "k3s" {
		return "", fmt.Errorf("kubeconfig_not_found")
	}
	request, err := sshCommandInvocation(ctx, s.store.Gorm, machine, sshMachineMap(machine, nil), remoteKubeconfigReadCommand(discovery))
	if err != nil {
		return "", err
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stdout, _, _, err := importedKubeconfigSSHRun(runCtx, request)
	if err != nil {
		return "", fmt.Errorf("kubeconfig_read_failed")
	}
	if len(stdout) > importedKubeconfigMaxBytes {
		return "", fmt.Errorf("kubeconfig_too_large")
	}
	content := []byte(rewriteKubeconfigServerHost(stdout, machine.Host))
	if !looksLikeKubeconfig(content, 0) {
		return "", fmt.Errorf("kubeconfig_invalid")
	}
	if err := validateImportedKubeconfigContent(content); err != nil {
		return "", err
	}
	ref := importedKubeconfigRef(machine, discovery)
	path, finalPath, err := writeImportedKubeconfigTemp(s.cfg, ref, content)
	if err != nil {
		return "", err
	}
	if err := importedKubeconfigConnectionTest(ctx, s.cfg, path); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("kubeconfig_connection_test_failed")
	}
	if err := os.Rename(path, finalPath); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("publishing kubeconfig secret failed")
	}
	return ref, nil
}

func remoteKubeconfigReadCommand(discovery sshKubernetesDiscovery) string {
	kubectl := "kubectl"
	if discovery.Kind == "k3s" {
		kubectl = "k3s kubectl"
	}
	return "set -eu\nOUT=$(" + kubectl + " config view --raw --minify 2>/dev/null)\nBYTES=$(printf '%s' \"$OUT\" | wc -c)\n[ \"$BYTES\" -le 1048576 ] || exit 23\nprintf '%s\\n' \"$OUT\""
}

func writeImportedKubeconfigTemp(cfg Config, ref string, content []byte) (string, string, error) {
	ref, err := cleanImportedKubeconfigRef(ref)
	if err != nil {
		return "", "", err
	}
	baseDir := strings.TrimSpace(cfg.KubeconfigSecretDir)
	if baseDir == "" {
		baseDir = "/etc/assops/kubeconfigs"
	}
	path := filepath.Join(baseDir, filepath.Clean(ref))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", "", fmt.Errorf("creating kubeconfig secret dir failed")
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "import-*.kubeconfig")
	if err != nil {
		return "", "", fmt.Errorf("creating kubeconfig secret temp failed")
	}
	tempPath := temp.Name()
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return "", "", fmt.Errorf("writing kubeconfig secret failed")
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", "", fmt.Errorf("writing kubeconfig secret failed")
	}
	if err := os.Chmod(tempPath, 0o600); err != nil {
		_ = os.Remove(tempPath)
		return "", "", fmt.Errorf("chmod kubeconfig secret failed")
	}
	tempDir := filepath.Dir(ref)
	tempRef := filepath.Base(tempPath)
	if tempDir != "." {
		tempRef = filepath.ToSlash(filepath.Join(tempDir, tempRef))
	}
	resolved, err := resolveKubeconfigRef(cfg, tempRef)
	if err != nil {
		_ = os.Remove(tempPath)
		return "", "", err
	}
	return resolved, path, nil
}

func cleanImportedKubeconfigRef(ref string) (string, error) {
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
	return cleaned, nil
}

func runImportedKubeconfigConnectionTest(ctx context.Context, cfg Config, kubeconfigPath string) error {
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, kubectlBinary(cfg), "--kubeconfig", kubeconfigPath, "cluster-info")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl cluster-info failed")
	}
	return nil
}

func validateImportedKubeconfigContent(content []byte) error {
	if len(content) == 0 || len(content) > importedKubeconfigMaxBytes {
		return fmt.Errorf("kubeconfig has invalid size")
	}
	for _, line := range strings.Split(string(content), "\n") {
		key, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "exec", "auth-provider", "tokenFile", "client-certificate", "client-key", "certificate-authority":
			return fmt.Errorf("kubeconfig contains unsupported credential source")
		}
	}
	return nil
}

func (s *Server) discoverArgoFromKubernetesEnvironment(ctx context.Context, env GormKubernetesEnvironment) map[string]any {
	result := map[string]any{
		"status":                        "blocked",
		"kubernetes_environment_id":     env.ID,
		"kubernetes_environment_name":   env.Name,
		"namespace":                     env.Namespace,
		"cluster_name":                  env.ClusterName,
		"kubeconfig_secret_ref_present": env.KubeconfigSecretRef != "",
		"kubeconfig_secret_read":        false,
		"kubernetes_api_call":           false,
		"kubectl_command_invoked":       false,
		"raw_response_included":         false,
		"secret_included":               false,
		"candidates":                    []argoServiceCandidate{},
		"blocked_reasons":               []string{},
		"suppressed_fields":             []string{"kubeconfig", "cluster_token", "authorization_header", "client_certificate", "client_key", "raw_kubernetes_response"},
	}
	if metadataString(mapFromAny(env.Metadata.Data)["kubernetes_access_mode"]) == "ssh_kubectl" {
		result["blocked_reasons"] = []string{"ssh_kubectl_argo_discovery_not_implemented"}
		return result
	}
	if env.KubeconfigSecretRef == "" {
		result["blocked_reasons"] = []string{"kubeconfig_secret_ref_missing"}
		return result
	}
	kubeconfigPath, err := resolveKubeconfigRef(s.cfg, env.KubeconfigSecretRef)
	if err != nil {
		result["blocked_reasons"] = []string{err.Error()}
		return result
	}
	result["kubeconfig_secret_read"] = true
	candidates, warnings, err := discoverArgoCandidates(ctx, s.cfg, kubeconfigPath, env.Namespace)
	result["kubernetes_api_call"] = true
	result["kubectl_command_invoked"] = true
	result["warnings"] = warnings
	if err != nil {
		result["status"] = "failed"
		result["blocked_reasons"] = []string{err.Error()}
		return result
	}
	result["candidates"] = candidates
	result["candidate_count"] = len(candidates)
	if len(candidates) == 0 {
		result["blocked_reasons"] = []string{"argocd_service_not_found"}
		return result
	}
	result["status"] = "ok"
	result["message"] = "Argo CD service candidates discovered"
	return result
}

func discoverArgoCandidates(ctx context.Context, cfg Config, kubeconfigPath, namespace string) ([]argoServiceCandidate, []string, error) {
	namespaces := []string{cleanOptionalText(namespace), "argocd"}
	seenNS := map[string]bool{}
	var candidates []argoServiceCandidate
	warnings := []string{}
	for _, ns := range namespaces {
		if ns == "" || seenNS[ns] {
			continue
		}
		seenNS[ns] = true
		items, err := kubectlServiceCandidates(ctx, cfg, kubeconfigPath, ns)
		if err != nil && ns == namespace {
			return nil, warnings, err
		}
		if err != nil {
			warnings = append(warnings, "service_scan_failed:"+ns)
		}
		candidates = append(candidates, items...)
		ingressItems, err := kubectlIngressCandidates(ctx, cfg, kubeconfigPath, ns)
		if err == nil {
			candidates = append(candidates, ingressItems...)
		} else {
			warnings = append(warnings, "ingress_scan_failed:"+ns)
		}
	}
	return uniqueArgoCandidates(candidates), warnings, nil
}

func kubectlServiceCandidates(ctx context.Context, cfg Config, kubeconfigPath, namespace string) ([]argoServiceCandidate, error) {
	out, err := runKubectlJSON(ctx, cfg, kubeconfigPath, namespace, "get", "svc", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("kubectl get services failed")
	}
	var payload struct {
		Items []struct {
			Metadata struct {
				Name      string            `json:"name"`
				Namespace string            `json:"namespace"`
				Labels    map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Type           string `json:"type"`
				ClusterIP      string `json:"clusterIP"`
				LoadBalancerIP string `json:"loadBalancerIP"`
				Ports          []struct {
					Port     int    `json:"port"`
					NodePort int    `json:"nodePort"`
					Name     string `json:"name"`
				} `json:"ports"`
			} `json:"spec"`
			Status struct {
				LoadBalancer struct {
					Ingress []struct {
						IP       string `json:"ip"`
						Hostname string `json:"hostname"`
					} `json:"ingress"`
				} `json:"loadBalancer"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, fmt.Errorf("invalid Kubernetes service response")
	}
	candidates := []argoServiceCandidate{}
	for _, item := range payload.Items {
		name := cleanOptionalText(item.Metadata.Name)
		if !looksLikeArgoService(name, item.Metadata.Labels) {
			continue
		}
		ns := cleanOptionalText(firstNonEmptyString(item.Metadata.Namespace, namespace))
		reason := "service_detected"
		candidateURL := ""
		for _, ingress := range item.Status.LoadBalancer.Ingress {
			host := firstNonEmptyString(ingress.Hostname, ingress.IP)
			if host != "" {
				candidateURL = "https://" + host
				reason = "load_balancer"
				break
			}
		}
		if candidateURL == "" && item.Spec.LoadBalancerIP != "" {
			candidateURL = "https://" + item.Spec.LoadBalancerIP
			reason = "load_balancer_ip"
		}
		if candidateURL == "" && strings.EqualFold(item.Spec.Type, "NodePort") {
			for _, port := range item.Spec.Ports {
				if port.NodePort > 0 {
					candidateURL = fmt.Sprintf("https://%s:%d", publicURLHostOnly(item.Spec.ClusterIP), port.NodePort)
					reason = "node_port_needs_review"
					break
				}
			}
		}
		candidates = append(candidates, argoServiceCandidate{Name: name, Namespace: ns, Kind: "service", URL: candidateURL, Reason: reason})
	}
	return candidates, nil
}

func kubectlIngressCandidates(ctx context.Context, cfg Config, kubeconfigPath, namespace string) ([]argoServiceCandidate, error) {
	out, err := runKubectlJSON(ctx, cfg, kubeconfigPath, namespace, "get", "ingress", "-o", "json")
	if err != nil {
		return nil, err
	}
	var payload struct {
		Items []struct {
			Metadata struct {
				Name      string            `json:"name"`
				Namespace string            `json:"namespace"`
				Labels    map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Rules []struct {
					Host string `json:"host"`
				} `json:"rules"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, fmt.Errorf("invalid Kubernetes ingress response")
	}
	candidates := []argoServiceCandidate{}
	for _, item := range payload.Items {
		name := cleanOptionalText(item.Metadata.Name)
		if !looksLikeArgoService(name, item.Metadata.Labels) {
			continue
		}
		for _, rule := range item.Spec.Rules {
			host := cleanOptionalText(rule.Host)
			if host != "" {
				candidates = append(candidates, argoServiceCandidate{Name: name, Namespace: firstNonEmptyString(item.Metadata.Namespace, namespace), Kind: "ingress", URL: "https://" + host, Reason: "ingress_host"})
			}
		}
	}
	return candidates, nil
}

func runKubectlJSON(ctx context.Context, cfg Config, kubeconfigPath, namespace string, args ...string) ([]byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	fullArgs := []string{"--kubeconfig", kubeconfigPath}
	if namespace != "" {
		fullArgs = append(fullArgs, "-n", namespace)
	}
	fullArgs = append(fullArgs, args...)
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(runCtx, kubectlBinary(cfg), fullArgs...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		preview, _ := sanitizedKubernetesLogPreview(stderr.String(), 2048)
		if preview != "" {
			return nil, fmt.Errorf("%w: %s", err, preview)
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

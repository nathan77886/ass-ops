package app

import (
	"context"
	"fmt"
	"gorm.io/gorm/clause"
	"path/filepath"
	"strings"
	"time"
)

var (
	importedKubeconfigSSHRun = func(ctx context.Context, request sshRunRequest) (string, string, int, error) {
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
	ref, kubeconfigCiphertext, err := s.importedKubeconfigSecret(ctx, machine, discovery)
	if err != nil {
		return GormKubernetesEnvironment{}, err
	}
	kubeconfigRef = firstNonEmptyString(kubeconfigRef, ref)
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
		ProjectID:                  machine.ProjectID,
		Name:                       name,
		Environment:                environment,
		ClusterName:                discovery.ClusterName,
		Namespace:                  discovery.Namespace,
		KubeconfigSecretRef:        kubeconfigRef,
		KubeconfigSecretCiphertext: kubeconfigCiphertext,
		ServiceAccount:             serviceAccount,
		TokenSubjectReviewStatus:   "not_reviewed",
		RBACReadLogsStatus:         "not_reviewed",
		PodRestartStatus:           "not_reviewed",
		Status:                     status,
		Metadata: JSONValue{Data: map[string]any{
			"source":                  "ssh_machine_import",
			"source_ssh_machine_id":   machine.ID,
			"source_ssh_machine_name": machine.Name,
			"kubernetes_kind":         discovery.Kind,
			"kubernetes_access_mode":  "database_kubeconfig",
			"context":                 discovery.Context,
			"server_host":             discovery.ServerHost,
		}},
	}
	err = s.store.Gorm.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "project_id"}, {Name: "environment"}, {Name: "cluster_name"}, {Name: "namespace"}},
		DoUpdates: clause.Assignments(map[string]any{"name": model.Name, "kubeconfig_secret_ref": model.KubeconfigSecretRef, "kubeconfig_secret_ciphertext": model.KubeconfigSecretCiphertext, "service_account": model.ServiceAccount, "status": model.Status, "metadata": model.Metadata}),
	}).Create(&model).Error
	if err != nil {
		return model, err
	}
	err = s.store.Gorm.WithContext(ctx).Where(&GormKubernetesEnvironment{ProjectID: machine.ProjectID, Environment: environment, ClusterName: discovery.ClusterName, Namespace: discovery.Namespace}).First(&model).Error
	return model, err
}

func (s *Server) importedKubeconfigSecret(ctx context.Context, machine GormSSHMachine, discovery sshKubernetesDiscovery) (string, string, error) {
	if discovery.RemoteKubeconfig == "" && discovery.Kind != "k3s" {
		return "", "", fmt.Errorf("kubeconfig_not_found")
	}
	request, err := sshCommandInvocation(ctx, s.store.Gorm, machine, sshMachineMap(machine, nil), remoteKubeconfigReadCommand(discovery))
	if err != nil {
		return "", "", err
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stdout, _, _, err := importedKubeconfigSSHRun(runCtx, request)
	if err != nil {
		return "", "", fmt.Errorf("kubeconfig_read_failed")
	}
	if len(stdout) > importedKubeconfigMaxBytes {
		return "", "", fmt.Errorf("kubeconfig_too_large")
	}
	content := []byte(rewriteKubeconfigServerHost(stdout, machine.Host))
	if !looksLikeKubeconfig(content, 0) {
		return "", "", fmt.Errorf("kubeconfig_invalid")
	}
	if err := validateImportedKubeconfigContent(content); err != nil {
		return "", "", err
	}
	ciphertext, err := s.encryptWebhookSecret(string(content))
	if err != nil {
		return "", "", fmt.Errorf("encrypting kubeconfig secret failed")
	}
	return importedKubeconfigRef(machine, discovery), ciphertext, nil
}

func remoteKubeconfigReadCommand(discovery sshKubernetesDiscovery) string {
	kubectl := "kubectl"
	if discovery.Kind == "k3s" {
		kubectl = "k3s kubectl"
	}
	return "set -eu\nOUT=$(" + kubectl + " config view --raw --minify 2>/dev/null)\nBYTES=$(printf '%s' \"$OUT\" | wc -c)\n[ \"$BYTES\" -le 1048576 ] || exit 23\nprintf '%s\\n' \"$OUT\""
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
		"kubernetes_client_invoked":     false,
		"raw_response_included":         false,
		"secret_included":               false,
		"candidates":                    []argoServiceCandidate{},
		"blocked_reasons":               []string{},
		"suppressed_fields":             []string{"kubeconfig", "cluster_token", "authorization_header", "client_certificate", "client_key", "raw_kubernetes_response"},
	}
	if env.KubeconfigSecretRef == "" || strings.TrimSpace(env.KubeconfigSecretCiphertext) == "" {
		result["blocked_reasons"] = []string{"kubeconfig_secret_ref_missing"}
		return result
	}
	kubeconfig, err := s.decryptWebhookSecret(env.KubeconfigSecretCiphertext)
	if err != nil {
		result["blocked_reasons"] = []string{"decrypting kubeconfig secret failed"}
		return result
	}
	result["kubeconfig_secret_read"] = true
	candidates, warnings, err := discoverArgoCandidates(ctx, kubeconfig, env.Namespace)
	result["kubernetes_api_call"] = true
	result["kubernetes_client_invoked"] = true
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

func discoverArgoCandidates(ctx context.Context, kubeconfig, namespace string) ([]argoServiceCandidate, []string, error) {
	client, err := kubernetesClientFromSecret(kubeconfig)
	if err != nil {
		return nil, nil, err
	}
	namespaces := []string{cleanOptionalText(namespace), "argocd"}
	seenNS := map[string]bool{}
	var candidates []argoServiceCandidate
	warnings := []string{}
	for _, ns := range namespaces {
		if ns == "" || seenNS[ns] {
			continue
		}
		seenNS[ns] = true
		items, err := kubernetesServiceCandidates(ctx, client, ns)
		if err != nil && ns == namespace {
			return nil, warnings, err
		}
		if err != nil {
			warnings = append(warnings, "service_scan_failed:"+ns)
		}
		candidates = append(candidates, items...)
		ingressItems, err := kubernetesIngressCandidates(ctx, client, ns)
		if err == nil {
			candidates = append(candidates, ingressItems...)
		} else {
			warnings = append(warnings, "ingress_scan_failed:"+ns)
		}
	}
	return uniqueArgoCandidates(candidates), warnings, nil
}

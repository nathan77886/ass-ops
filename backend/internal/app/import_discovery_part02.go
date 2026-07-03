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
	kubernetesArgoPodTokenRun             = kubernetesArgoPodToken
	discoverArgoTokenFromKubernetesPodRun = discoverArgoTokenFromKubernetesPod
)

const importedKubeconfigMaxBytes = 1024 * 1024
const argoPodTokenCommand = `set -eu
if ! command -v argocd >/dev/null 2>&1; then exit 21; fi
TOKEN="$(argocd account generate-token --account admin 2>/dev/null || true)"
[ -n "$TOKEN" ] || exit 22
printf 'ASSOPS_ARGO_TOKEN=%s\n' "$TOKEN"`

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
		"status":                           "blocked",
		"kubernetes_environment_id":        env.ID,
		"kubernetes_environment_name":      env.Name,
		"namespace":                        env.Namespace,
		"cluster_name":                     env.ClusterName,
		"kubeconfig_secret_ref_present":    env.KubeconfigSecretRef != "",
		"kubeconfig_secret_read":           false,
		"kubernetes_api_call":              false,
		"kubectl_command_invoked":          false,
		"kubernetes_client_invoked":        false,
		"raw_response_included":            false,
		"secret_included":                  false,
		"candidates":                       []argoServiceCandidate{},
		"credential_candidates":            []argoCredentialPodCandidate{},
		"credential_auto_create_available": false,
		"pod_exec_token_command_invoked":   false,
		"blocked_reasons":                  []string{},
		"suppressed_fields":                []string{"kubeconfig", "cluster_token", "authorization_header", "client_certificate", "client_key", "raw_kubernetes_response", "argo_token", "pod_exec_stdout", "pod_exec_stderr"},
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
	candidates, credentialCandidates, warnings, err := discoverArgoCandidates(ctx, kubeconfig, env.Namespace)
	result["kubernetes_api_call"] = true
	result["kubernetes_client_invoked"] = true
	result["warnings"] = warnings
	if err != nil {
		result["status"] = "failed"
		result["blocked_reasons"] = []string{err.Error()}
		return result
	}
	result["candidates"] = candidates
	result["credential_candidates"] = credentialCandidates
	result["candidate_count"] = len(candidates)
	result["credential_candidate_count"] = len(credentialCandidates)
	result["credential_auto_create_available"] = len(credentialCandidates) > 0
	if len(candidates) == 0 {
		result["blocked_reasons"] = []string{"argocd_service_not_found"}
		return result
	}
	result["status"] = "ok"
	result["message"] = "Argo CD service candidates discovered"
	return result
}

func discoverArgoCandidates(ctx context.Context, kubeconfig, namespace string) ([]argoServiceCandidate, []argoCredentialPodCandidate, []string, error) {
	client, err := kubernetesClientFromSecret(kubeconfig)
	if err != nil {
		return nil, nil, nil, err
	}
	namespaces := []string{cleanOptionalText(namespace), "argocd"}
	seenNS := map[string]bool{}
	var candidates []argoServiceCandidate
	var credentialCandidates []argoCredentialPodCandidate
	warnings := []string{}
	for _, ns := range namespaces {
		if ns == "" || seenNS[ns] {
			continue
		}
		seenNS[ns] = true
		items, err := kubernetesServiceCandidates(ctx, client, ns)
		if err != nil && ns == namespace {
			return nil, nil, warnings, err
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
		podItems, err := kubernetesArgoPodCandidates(ctx, client, ns)
		if err == nil {
			credentialCandidates = append(credentialCandidates, podItems...)
		} else {
			warnings = append(warnings, "pod_scan_failed:"+ns)
		}
	}
	return uniqueArgoCandidates(candidates), uniqueArgoCredentialPodCandidates(credentialCandidates), warnings, nil
}

func (s *Server) argoCredentialFromKubernetesPod(ctx context.Context, env GormKubernetesEnvironment, connectionName string) (GormConnectionCredential, error) {
	if env.KubeconfigSecretRef == "" || strings.TrimSpace(env.KubeconfigSecretCiphertext) == "" {
		return GormConnectionCredential{}, fmt.Errorf("kubeconfig_secret_ref_missing")
	}
	kubeconfig, err := s.decryptWebhookSecret(env.KubeconfigSecretCiphertext)
	if err != nil {
		return GormConnectionCredential{}, fmt.Errorf("decrypting kubeconfig secret failed")
	}
	token, source, err := discoverArgoTokenFromKubernetesPodRun(ctx, kubeconfig, env.Namespace)
	if err != nil {
		return GormConnectionCredential{}, err
	}
	ciphertext, err := s.encryptWebhookSecret(token)
	if err != nil {
		return GormConnectionCredential{}, fmt.Errorf("could not encrypt Argo token credential")
	}
	name := cleanOptionalText(connectionName)
	if name == "" {
		name = env.Name
	}
	return GormConnectionCredential{
		ProjectID:        validNullString(env.ProjectID),
		Name:             name + " auto Argo token",
		Kind:             "argo_token",
		SecretCiphertext: ciphertext,
		Metadata: JSONValue{Data: map[string]any{
			"source":                             "kubernetes_argocd_pod_exec",
			"source_kubernetes_environment_id":   env.ID,
			"source_kubernetes_environment_name": env.Name,
			"namespace":                          source.Namespace,
			"pod_name":                           source.Name,
			"container_name":                     source.Container,
			"token_command":                      "argocd account generate-token",
			"secret_included":                    false,
			"stdout_included":                    false,
			"stderr_included":                    false,
		}},
	}, nil
}

func (s *Server) existingAutoArgoCredential(ctx context.Context, env GormKubernetesEnvironment, serverURL string) (*GormConnectionCredential, error) {
	var connection GormArgoConnection
	err := s.store.Gorm.WithContext(ctx).
		Where(&GormArgoConnection{ProjectID: env.ProjectID, ServerURL: serverURL}).
		First(&connection).Error
	if errorsIsRecordNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !connection.CredentialID.Valid {
		return nil, nil
	}
	var credential GormConnectionCredential
	err = s.store.Gorm.WithContext(ctx).
		Where(&GormConnectionCredential{Kind: "argo_token"}).
		Where("id = ? AND project_id = ?", connection.CredentialID.String, env.ProjectID).
		First(&credential).Error
	if errorsIsRecordNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	metadata := mapFromAny(credential.Metadata.Data)
	if credential.SecretCiphertext == "" ||
		metadataString(metadata["source"]) != "kubernetes_argocd_pod_exec" ||
		cleanOptionalID(metadataString(metadata["source_kubernetes_environment_id"])) != env.ID {
		return nil, nil
	}
	return &credential, nil
}

func discoverArgoTokenFromKubernetesPod(ctx context.Context, kubeconfig, namespace string) (string, argoCredentialPodCandidate, error) {
	client, err := kubernetesClientFromSecret(kubeconfig)
	if err != nil {
		return "", argoCredentialPodCandidate{}, err
	}
	namespaces := []string{cleanOptionalText(namespace), "argocd"}
	seenNS := map[string]bool{}
	var candidates []argoCredentialPodCandidate
	for _, ns := range namespaces {
		if ns == "" || seenNS[ns] {
			continue
		}
		seenNS[ns] = true
		items, err := kubernetesArgoPodCandidates(ctx, client, ns)
		if err == nil {
			candidates = append(candidates, items...)
		}
	}
	candidates = uniqueArgoCredentialPodCandidates(candidates)
	if len(candidates) == 0 {
		return "", argoCredentialPodCandidate{}, fmt.Errorf("argocd_pod_not_found")
	}
	var lastErr error
	for _, candidate := range candidates {
		token, err := kubernetesArgoPodTokenRun(ctx, kubeconfig, candidate)
		if err == nil {
			return token, candidate, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return "", argoCredentialPodCandidate{}, fmt.Errorf("argocd_token_exec_failed")
	}
	return "", argoCredentialPodCandidate{}, fmt.Errorf("argocd_token_not_found")
}

func kubernetesArgoPodToken(ctx context.Context, kubeconfig string, candidate argoCredentialPodCandidate) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stdout, _, err := kubernetesPodExec(runCtx, kubeconfig, kubernetesPodExecRequest{
		Namespace:     candidate.Namespace,
		PodName:       candidate.Name,
		ContainerName: candidate.Container,
		Command:       []string{"sh", "-c", argoPodTokenCommand},
	})
	if err != nil {
		return "", err
	}
	fields := parseAssopsKeyValueLines(stdout)
	token := strings.TrimSpace(fields["ASSOPS_ARGO_TOKEN"])
	if token == "" || len(token) > 128*1024 || len(strings.Fields(token)) != 1 {
		return "", fmt.Errorf("argocd_token_invalid")
	}
	return token, nil
}

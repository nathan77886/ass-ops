package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const sshKubernetesDiscoveryCommand = `set -eu
if command -v kubectl >/dev/null 2>&1; then K="kubectl"; KIND="kubernetes"; elif command -v k3s >/dev/null 2>&1; then K="k3s kubectl"; KIND="k3s"; else echo "ASSOPS_STATUS=kubectl_missing"; exit 21; fi
CTX=$($K config current-context 2>/dev/null || true)
NS=$($K config view --minify -o 'jsonpath={..namespace}' 2>/dev/null || true)
CLUSTER=$($K config view --minify -o 'jsonpath={.contexts[0].context.cluster}' 2>/dev/null || true)
SERVER=$($K config view --minify -o 'jsonpath={.clusters[0].cluster.server}' 2>/dev/null || true)
USER=$($K config view --minify -o 'jsonpath={.contexts[0].context.user}' 2>/dev/null || true)
[ -n "$NS" ] || NS=default
printf 'ASSOPS_STATUS=ok\nASSOPS_KIND=%s\nASSOPS_CONTEXT=%s\nASSOPS_NAMESPACE=%s\nASSOPS_CLUSTER=%s\nASSOPS_SERVER=%s\nASSOPS_USER=%s\n' "$KIND" "$CTX" "$NS" "$CLUSTER" "$SERVER" "$USER"`

type sshKubernetesDiscovery struct {
	Status           string
	Kind             string
	Context          string
	Namespace        string
	ClusterName      string
	ServerHost       string
	ServiceAccount   string
	BlockedReasons   []string
	SuppressedFields []string
}

type argoServiceCandidate struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	URL       string `json:"url"`
	Reason    string `json:"reason"`
}

func (s *Server) previewKubernetesImportFromSSHMachine(w http.ResponseWriter, r *http.Request) {
	machine, ok := s.sshMachineForImport(w, r, "read")
	if !ok {
		return
	}
	result := s.discoverKubernetesFromSSH(r.Context(), machine)
	status := http.StatusOK
	if result.Status != "ok" {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, kubernetesImportPreviewPayload(machine, result))
}

func (s *Server) importKubernetesFromSSHMachine(w http.ResponseWriter, r *http.Request) {
	machine, ok := s.sshMachineForImport(w, r, "read")
	if !ok {
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "kubernetes_environment", ProjectID: machine.ProjectID}, "create") {
		return
	}
	var req struct {
		Name                string `json:"name"`
		Environment         string `json:"environment"`
		KubeconfigSecretRef string `json:"kubeconfig_secret_ref"`
		ServiceAccount      string `json:"service_account"`
		Status              string `json:"status"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	discovery := s.discoverKubernetesFromSSH(r.Context(), machine)
	if discovery.Status != "ok" {
		writeJSON(w, http.StatusBadRequest, kubernetesImportPreviewPayload(machine, discovery))
		return
	}
	env, err := s.upsertImportedKubernetesEnvironment(r.Context(), machine, discovery, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"imported":                 true,
		"source_ssh_machine_id":    machine.ID,
		"source_ssh_machine_name":  machine.Name,
		"kubernetes_environment":   kubernetesEnvironmentMap(env),
		"discovery":                safeSSHDiscoveryMap(discovery),
		"stdout_included":          false,
		"stderr_included":          false,
		"kubeconfig_body_included": false,
	})
}

func (s *Server) previewArgoImportFromKubernetesEnvironment(w http.ResponseWriter, r *http.Request) {
	env, ok := s.kubernetesEnvironmentForArgoImport(w, r, "read")
	if !ok {
		return
	}
	result := s.discoverArgoFromKubernetesEnvironment(r.Context(), env)
	status := http.StatusOK
	if result["status"] != "ok" {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, result)
}

func (s *Server) importArgoFromKubernetesEnvironment(w http.ResponseWriter, r *http.Request) {
	env, ok := s.kubernetesEnvironmentForArgoImport(w, r, "read")
	if !ok {
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "argo_connection", ProjectID: env.ProjectID}, "create") {
		return
	}
	var req struct {
		Name         string         `json:"name"`
		ServerURL    string         `json:"server_url"`
		CredentialID string         `json:"credential_id"`
		Config       map[string]any `json:"config"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = cleanOptionalText(req.Name)
	req.ServerURL = cleanOptionalText(req.ServerURL)
	if req.Name == "" || req.ServerURL == "" {
		writeError(w, http.StatusBadRequest, "name and server_url are required")
		return
	}
	if !validPublicHTTPURL(r.Context(), req.ServerURL) {
		writeError(w, http.StatusBadRequest, "server_url must be a public http or https URL")
		return
	}
	if (boolConfig(req.Config, "insecure_skip_verify") || boolConfig(req.Config, "use_env_token")) && !canUseSensitiveArgoConfig(currentUser(r)) {
		writeError(w, http.StatusForbidden, "sensitive Argo connection config requires an owner role")
		return
	}
	credential, err := s.connectionCredentialForProjectOrGlobal(r.Context(), env.ProjectID, req.CredentialID, "argo_token")
	if err != nil {
		writeError(w, http.StatusBadRequest, "credential_id must reference an Argo token credential in this project")
		return
	}
	config := map[string]any{
		"insecure_skip_verify": boolConfig(req.Config, "insecure_skip_verify"),
		"use_env_token":        boolConfig(req.Config, "use_env_token"),
	}
	config["source"] = "kubernetes_environment_import"
	config["source_kubernetes_environment_id"] = env.ID
	config["source_kubernetes_environment_name"] = env.Name
	connection := GormArgoConnection{
		ProjectID:    env.ProjectID,
		Name:         req.Name,
		ServerURL:    req.ServerURL,
		AuthType:     "token",
		CredentialID: validNullString(req.CredentialID),
		Config:       JSONValue{Data: config},
	}
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var existing GormArgoConnection
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(&GormArgoConnection{ProjectID: env.ProjectID, ServerURL: req.ServerURL}).First(&existing).Error
		if err == nil {
			connection.GormBase = existing.GormBase
			connection.ProjectID = existing.ProjectID
			if err := tx.Save(&connection).Error; err != nil {
				return err
			}
		} else if errorsIsRecordNotFound(err) {
			if err := tx.Create(&connection).Error; err != nil {
				return err
			}
		} else {
			return err
		}
		_, err = syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	}); err != nil {
		writeError(w, http.StatusBadRequest, "could not import Argo connection")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"imported":                  true,
		"kubernetes_environment_id": env.ID,
		"argo_connection":           argoConnectionMap(connection, credential),
	})
}

func (s *Server) sshMachineForImport(w http.ResponseWriter, r *http.Request, action string) (GormSSHMachine, bool) {
	machineID := cleanOptionalID(chi.URLParam(r, "id"))
	var machine GormSSHMachine
	if err := s.store.Gorm.WithContext(r.Context()).First(&machine, &GormSSHMachine{GormBase: GormBase{ID: machineID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return machine, false
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "ssh_machine", ID: machineID, ProjectID: machine.ProjectID}, action) {
		return machine, false
	}
	return machine, true
}

func (s *Server) kubernetesEnvironmentForArgoImport(w http.ResponseWriter, r *http.Request, action string) (GormKubernetesEnvironment, bool) {
	envID := cleanOptionalID(chi.URLParam(r, "id"))
	var env GormKubernetesEnvironment
	if err := s.store.Gorm.WithContext(r.Context()).First(&env, &GormKubernetesEnvironment{GormBase: GormBase{ID: envID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return env, false
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "kubernetes_environment", ID: envID, ProjectID: env.ProjectID}, action) {
		return env, false
	}
	return env, true
}

func (s *Server) discoverKubernetesFromSSH(ctx context.Context, machine GormSSHMachine) sshKubernetesDiscovery {
	result := sshKubernetesDiscovery{
		Status:           "blocked",
		BlockedReasons:   []string{},
		SuppressedFields: []string{"stdout", "stderr", "kubeconfig", "cluster_token", "client_certificate", "client_key", "private_key", "password"},
	}
	request, err := sshCommandInvocation(ctx, s.store.Gorm, machine, sshMachineMap(machine, nil), sshKubernetesDiscoveryCommand)
	if err != nil {
		result.BlockedReasons = append(result.BlockedReasons, err.Error())
		return result
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stdout, _, exitCode, err := nativeSSHRunner{}.Run(runCtx, request)
	if err != nil {
		result.Status = "failed"
		if exitCode == 21 {
			result.BlockedReasons = append(result.BlockedReasons, "kubectl_or_k3s_not_found")
		} else {
			result.BlockedReasons = append(result.BlockedReasons, "ssh_probe_failed")
		}
		return result
	}
	fields := parseAssopsKeyValueLines(stdout)
	if fields["ASSOPS_STATUS"] != "ok" {
		result.Status = firstNonEmptyString(fields["ASSOPS_STATUS"], "blocked")
		result.BlockedReasons = append(result.BlockedReasons, "kubernetes_environment_not_detected")
		return result
	}
	result.Status = "ok"
	result.Kind = firstNonEmptyString(fields["ASSOPS_KIND"], "kubernetes")
	result.Context = cleanOptionalText(fields["ASSOPS_CONTEXT"])
	result.Namespace = cleanOptionalText(firstNonEmptyString(fields["ASSOPS_NAMESPACE"], "default"))
	result.ClusterName = cleanOptionalText(fields["ASSOPS_CLUSTER"])
	result.ServerHost = publicURLHostOnly(fields["ASSOPS_SERVER"])
	result.ServiceAccount = cleanOptionalText(fields["ASSOPS_USER"])
	if result.Context == "" {
		result.BlockedReasons = append(result.BlockedReasons, "current_context_empty")
	}
	if result.ClusterName == "" {
		result.BlockedReasons = append(result.BlockedReasons, "cluster_name_empty")
	}
	if result.Namespace == "" {
		result.BlockedReasons = append(result.BlockedReasons, "namespace_empty")
	}
	if containsSecretLikeMaterial(result.Context) || containsSecretLikeMaterial(result.ClusterName) || containsSecretLikeMaterial(result.Namespace) || containsSecretLikeMaterial(result.ServiceAccount) {
		result.Status = "blocked"
		result.BlockedReasons = append(result.BlockedReasons, "secret_like_material_detected")
	}
	if len(result.BlockedReasons) > 0 {
		result.Status = "blocked"
	}
	return result
}

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
		"kubeconfig_secret_ref":       "",
		"service_account":             discovery.ServiceAccount,
		"token_subject_review_status": "not_reviewed",
		"rbac_read_logs_status":       "not_reviewed",
		"rbac_restart_pods_status":    "not_reviewed",
		"status":                      "metadata_only",
	}
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

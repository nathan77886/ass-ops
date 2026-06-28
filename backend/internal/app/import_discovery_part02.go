package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm/clause"
	"os/exec"
	"strings"
	"time"
)

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

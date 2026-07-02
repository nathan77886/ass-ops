package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	kubernetesListPodsRun          = kubernetesListPods
	kubernetesPodLogsRun           = kubernetesPodLogs
	kubernetesRestartDeploymentRun = kubernetesRestartDeployment
)

func kubernetesClientFromSecret(kubeconfig string) (*kubernetes.Clientset, error) {
	kubeconfig = strings.TrimSpace(kubeconfig)
	if kubeconfig == "" {
		return nil, fmt.Errorf("kubeconfig secret is required")
	}
	content := []byte(kubeconfig)
	if !looksLikeKubeconfig(content, 0) {
		return nil, fmt.Errorf("kubeconfig secret is invalid")
	}
	cfg, err := clientcmd.RESTConfigFromKubeConfig(content)
	if err != nil {
		return nil, fmt.Errorf("parsing kubeconfig secret: %w", err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating Kubernetes client: %w", err)
	}
	return client, nil
}

func kubernetesListPods(ctx context.Context, kubeconfig, namespace string) ([]map[string]any, error) {
	client, err := kubernetesClientFromSecret(kubeconfig)
	if err != nil {
		return nil, err
	}
	list, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing Kubernetes pods: %w", err)
	}
	items := make([]map[string]any, 0, len(list.Items))
	for _, pod := range list.Items {
		containers := make([]string, 0, len(pod.Spec.Containers))
		seenContainers := map[string]bool{}
		for _, container := range pod.Spec.Containers {
			containerName := cleanOptionalText(container.Name)
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
				restartCount += int(status.RestartCount)
			}
		}
		phase := cleanOptionalText(string(pod.Status.Phase))
		if phase == "" {
			phase = "unknown"
		}
		items = append(items, map[string]any{
			"name":             pod.Name,
			"phase":            phase,
			"containers":       containers,
			"container_count":  len(containers),
			"ready_containers": readyContainers,
			"restart_count":    restartCount,
			"created_at":       pod.CreationTimestamp.Time.Format(time.RFC3339),
		})
	}
	return items, nil
}

func kubernetesPodLogs(ctx context.Context, kubeconfig string, req kubernetesPodLogRequest) (string, error) {
	client, err := kubernetesClientFromSecret(kubeconfig)
	if err != nil {
		return "", err
	}
	tailLines := int64(req.TailLines)
	if tailLines <= 0 {
		tailLines = 200
	}
	if tailLines > 200 {
		tailLines = 200
	}
	opts := &corev1.PodLogOptions{TailLines: &tailLines}
	if req.ContainerName != "" {
		opts.Container = req.ContainerName
	}
	if req.SinceSeconds > 0 {
		since := int64(req.SinceSeconds)
		if since > 86400 {
			since = 86400
		}
		opts.SinceSeconds = &since
	}
	stream, err := client.CoreV1().Pods(req.Namespace).GetLogs(req.PodName, opts).Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("opening Kubernetes pod log stream: %w", err)
	}
	defer stream.Close()
	out, err := io.ReadAll(io.LimitReader(stream, kubernetesLogPreviewMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("reading Kubernetes pod logs: %w", err)
	}
	return string(out), nil
}

func kubernetesRestartDeployment(ctx context.Context, kubeconfig string, req kubernetesPodRestartRequest) error {
	client, err := kubernetesClientFromSecret(kubeconfig)
	if err != nil {
		return err
	}
	review := &authorizationv1.SelfSubjectAccessReview{
		Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: req.Namespace,
				Verb:      "patch",
				Group:     "apps",
				Resource:  "deployments",
				Name:      req.DeploymentName,
			},
		},
	}
	access, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("checking Kubernetes deployment patch access: %w", err)
	}
	if !access.Status.Allowed {
		return fmt.Errorf("Kubernetes deployment patch access denied")
	}
	patch := map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]string{
						"kubectl.kubernetes.io/restartedAt": time.Now().UTC().Format(time.RFC3339),
					},
				},
			},
		},
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("building Kubernetes deployment restart patch: %w", err)
	}
	deployments := client.AppsV1().Deployments(req.Namespace)
	if _, err := deployments.Patch(ctx, req.DeploymentName, types.StrategicMergePatchType, body, metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}}); err != nil {
		return fmt.Errorf("dry-running Kubernetes deployment restart: %w", err)
	}
	if _, err := deployments.Patch(ctx, req.DeploymentName, types.StrategicMergePatchType, body, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("patching Kubernetes deployment restart: %w", err)
	}
	return nil
}

func kubernetesServiceCandidates(ctx context.Context, client *kubernetes.Clientset, namespace string) ([]argoServiceCandidate, error) {
	list, err := client.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing Kubernetes services: %w", err)
	}
	candidates := []argoServiceCandidate{}
	for _, item := range list.Items {
		name := cleanOptionalText(item.Name)
		if !looksLikeArgoService(name, item.Labels) {
			continue
		}
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
		if candidateURL == "" && item.Spec.Type == corev1.ServiceTypeNodePort {
			for _, port := range item.Spec.Ports {
				if port.NodePort > 0 {
					candidateURL = fmt.Sprintf("https://%s:%d", publicURLHostOnly(item.Spec.ClusterIP), port.NodePort)
					reason = "node_port_needs_review"
					break
				}
			}
		}
		candidates = append(candidates, argoServiceCandidate{Name: name, Namespace: firstNonEmptyString(item.Namespace, namespace), Kind: "service", URL: candidateURL, Reason: reason})
	}
	return candidates, nil
}

func kubernetesIngressCandidates(ctx context.Context, client *kubernetes.Clientset, namespace string) ([]argoServiceCandidate, error) {
	list, err := client.NetworkingV1().Ingresses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing Kubernetes ingresses: %w", err)
	}
	candidates := []argoServiceCandidate{}
	for _, item := range list.Items {
		name := cleanOptionalText(item.Name)
		if !looksLikeArgoService(name, item.Labels) {
			continue
		}
		for _, rule := range item.Spec.Rules {
			host := cleanOptionalText(rule.Host)
			if host != "" {
				candidates = append(candidates, argoServiceCandidate{Name: name, Namespace: firstNonEmptyString(item.Namespace, namespace), Kind: "ingress", URL: "https://" + host, Reason: "ingress_host"})
			}
		}
	}
	return candidates, nil
}

package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestArgoPodLogPreviewReportsReadyKubernetesClientBackend(t *testing.T) {
	preview := argoPodLogQueryPreviewWithConfig(Config{
		KubernetesPodLogsEnabled: true,
	}, "api-7d9f", "web", 50, 60, map[string]any{
		"id":                            "target-1",
		"name":                          "billing",
		"environment":                   "test",
		"cluster_name":                  "test-cluster",
		"namespace":                     "billing",
		"status":                        "synced",
		"kubernetes_environment_id":     "kube-env-1",
		"kubernetes_environment_name":   "billing",
		"kubernetes_environment_status": "ready",
		"kubeconfig_secret_ref_present": true,
		"kubeconfig_secret_ref":         "billing-reader",
		"token_subject_review_status":   "reviewed",
		"rbac_read_logs_status":         "reviewed",
	})
	retrievalPlan := mapFromAny(preview["retrieval_plan"])
	executionPlan := mapFromAny(retrievalPlan["execution_plan"])
	liveBackendPlan := mapFromAny(executionPlan["live_backend_plan"])
	if liveBackendPlan["ready"] != true ||
		executionPlan["live_backend_ready"] != true ||
		!stringListContains(stringSliceFromAny(executionPlan["disabled_backends"]), "raw_log_body_recording") ||
		stringListContains(stringSliceFromAny(executionPlan["disabled_backends"]), "kubernetes_client_logs") ||
		stringListContains(stringSliceFromAny(preview["blocked_reasons"]), "pod_log_backend_disabled") {
		t.Fatalf("ready pod log preview = %#v", preview)
	}
	steps := mapSliceFromAny(retrievalPlan["steps"])
	foundLiveStep := false
	for _, step := range steps {
		if step["kind"] == "live_log_stream" {
			foundLiveStep = true
			if step["status"] != "planned" {
				t.Fatalf("live log step should be planned when kubectl backend is ready: %#v", step)
			}
		}
	}
	if !foundLiveStep {
		t.Fatalf("live log stream step missing: %#v", steps)
	}
	encoded, _ := json.Marshal(preview)
	for _, forbidden := range []string{"billing-reader", "apiVersion:", "clusters:"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("pod log preview leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestArgoPodLogPreviewBlockedBackendDoesNotLeakKubeconfigRef(t *testing.T) {
	preview := argoPodLogQueryPreviewWithConfig(Config{
		KubernetesPodLogsEnabled: false,
	}, "api-7d9f", "web", 50, 60, map[string]any{
		"id":                            "target-1",
		"name":                          "billing",
		"environment":                   "test",
		"cluster_name":                  "test-cluster",
		"namespace":                     "billing",
		"status":                        "synced",
		"kubernetes_environment_id":     "kube-env-1",
		"kubernetes_environment_name":   "billing",
		"kubernetes_environment_status": "ready",
		"kubeconfig_secret_ref_present": true,
		"kubeconfig_secret_ref":         "billing-reader",
		"token_subject_review_status":   "reviewed",
		"rbac_read_logs_status":         "reviewed",
	})
	retrievalPlan := mapFromAny(preview["retrieval_plan"])
	executionPlan := mapFromAny(retrievalPlan["execution_plan"])
	liveBackendPlan := mapFromAny(executionPlan["live_backend_plan"])
	if liveBackendPlan["ready"] != false ||
		executionPlan["live_backend_ready"] != false ||
		!stringListContains(stringSliceFromAny(executionPlan["disabled_backends"]), "kubernetes_client_logs") ||
		!stringListContains(stringSliceFromAny(preview["blocked_reasons"]), "pod_log_backend_disabled") {
		t.Fatalf("blocked pod log preview = %#v", preview)
	}
	encoded, _ := json.Marshal(preview)
	for _, forbidden := range []string{"billing-reader", "apiVersion:", "clusters:"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("blocked pod log preview leaked %q: %s", forbidden, encoded)
		}
	}
}

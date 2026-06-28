package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArgoPodLogPreviewReportsReadyKubectlBackend(t *testing.T) {
	dir := t.TempDir()
	writeKubeconfig(t, filepath.Join(dir, "billing-reader"), 0o600)
	kubectlPath := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(kubectlPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
	preview := argoPodLogQueryPreviewWithConfig(Config{
		KubernetesPodLogsEnabled: true,
		KubeconfigSecretDir:      dir,
		KubectlPath:              kubectlPath,
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
		stringListContains(stringSliceFromAny(executionPlan["disabled_backends"]), "kubectl_logs") ||
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
	for _, forbidden := range []string{"billing-reader", filepath.Join(dir, "billing-reader"), "apiVersion:", "clusters:"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("pod log preview leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestArgoPodLogPreviewBlockedBackendDoesNotLeakKubeconfigRef(t *testing.T) {
	dir := t.TempDir()
	writeKubeconfig(t, filepath.Join(dir, "billing-reader"), 0o600)
	preview := argoPodLogQueryPreviewWithConfig(Config{
		KubernetesPodLogsEnabled: false,
		KubeconfigSecretDir:      dir,
		KubectlPath:              "kubectl",
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
		!stringListContains(stringSliceFromAny(executionPlan["disabled_backends"]), "kubectl_logs") ||
		!stringListContains(stringSliceFromAny(preview["blocked_reasons"]), "pod_log_backend_disabled") {
		t.Fatalf("blocked pod log preview = %#v", preview)
	}
	encoded, _ := json.Marshal(preview)
	for _, forbidden := range []string{"billing-reader", filepath.Join(dir, "billing-reader"), "apiVersion:", "clusters:"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("blocked pod log preview leaked %q: %s", forbidden, encoded)
		}
	}
}

func writeKubeconfig(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	content := []byte("apiVersion: v1\nclusters:\n- name: test\ncontexts:\n- name: test\nusers:\n- name: test\n")
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod kubeconfig: %v", err)
	}
}

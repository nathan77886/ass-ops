package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestResolveKubeconfigRefRejectsUnsafeRefsAndModes(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{KubeconfigSecretDir: dir, KubectlPath: "kubectl"}
	writeKubeconfig(t, filepath.Join(dir, "valid"), 0o600)
	if _, err := resolveKubeconfigRef(cfg, "valid"); err != nil {
		t.Fatalf("valid kubeconfig ref rejected: %v", err)
	}
	for _, ref := range []string{"../secret", "/tmp/secret", "bad\\path", "apiVersion: v1", ""} {
		if _, err := resolveKubeconfigRef(cfg, ref); err == nil {
			t.Fatalf("unsafe kubeconfig ref %q was accepted", ref)
		}
	}
	writeKubeconfig(t, filepath.Join(dir, "wide"), 0o622)
	if _, err := resolveKubeconfigRef(cfg, "wide"); err == nil {
		t.Fatalf("group/world writable kubeconfig was accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "not-kubeconfig"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write invalid kubeconfig: %v", err)
	}
	if _, err := resolveKubeconfigRef(cfg, "not-kubeconfig"); err == nil {
		t.Fatalf("invalid kubeconfig shape was accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "empty"), nil, 0o600); err != nil {
		t.Fatalf("write empty kubeconfig: %v", err)
	}
	if _, err := resolveKubeconfigRef(cfg, "empty"); err == nil {
		t.Fatalf("empty kubeconfig was accepted")
	}
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside")
	writeKubeconfig(t, outsidePath, 0o600)
	if err := os.Symlink(outsidePath, filepath.Join(dir, "link-outside")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	if _, err := resolveKubeconfigRef(cfg, "link-outside"); err == nil {
		t.Fatalf("symlink escaping kubeconfig dir was accepted")
	}
}

func TestRunKubernetesPodLogsDisabledDoesNotInvokeKubectl(t *testing.T) {
	result, err := runKubernetesPodLogs(context.Background(), Config{}, kubernetesPodLogRequest{
		Namespace:     "billing",
		PodName:       "api-7d9f",
		KubeconfigRef: "missing",
	})
	if err != nil {
		t.Fatalf("disabled pod logs should not fail: %v", err)
	}
	if result["backend_state"] != "disabled" ||
		result["kubectl_command_invoked"] != false ||
		result["log_body_included"] != false {
		t.Fatalf("disabled pod log result = %#v", result)
	}
}

func TestRunKubernetesPodLogsRecordsMetadataOnly(t *testing.T) {
	dir := t.TempDir()
	kubeconfigPath := filepath.Join(dir, "billing-reader")
	writeKubeconfig(t, kubeconfigPath, 0o600)
	kubectlPath := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(kubectlPath, []byte("#!/bin/sh\nprintf 'secret log line 1\\nsecret log line 2\\n'\n"), 0o700); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
	result, err := runKubernetesPodLogs(context.Background(), Config{
		KubernetesPodLogsEnabled: true,
		KubeconfigSecretDir:      dir,
		KubectlPath:              kubectlPath,
	}, kubernetesPodLogRequest{
		DeploymentTargetID: "target-1",
		Environment:        "test",
		ClusterName:        "test-cluster",
		Namespace:          "billing",
		PodName:            "api-7d9f",
		ContainerName:      "web",
		TailLines:          50,
		SinceSeconds:       60,
		KubeconfigRef:      "billing-reader",
	})
	if err != nil {
		t.Fatalf("runKubernetesPodLogs: %v", err)
	}
	if result["backend_state"] != "completed" ||
		result["kubeconfig_bound"] != true ||
		result["kubectl_command_invoked"] != true ||
		result["kubernetes_api_call"] != true ||
		result["log_stream_opened"] != true ||
		result["log_body_included"] != false ||
		result["raw_response_included"] != false ||
		result["line_count"] != 2 {
		t.Fatalf("live pod log metadata result = %#v", result)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"secret log line", kubeconfigPath, "apiVersion:", "clusters:"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("pod log metadata leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestRunKubernetesPodListRecordsMetadataOnly(t *testing.T) {
	dir := t.TempDir()
	kubeconfigPath := filepath.Join(dir, "billing-reader")
	writeKubeconfig(t, kubeconfigPath, 0o600)
	kubectlPath := filepath.Join(dir, "kubectl")
	podsJSON := `{"items":[{"metadata":{"name":"api-7d9f","creationTimestamp":"2026-06-26T05:00:00Z","labels":{"secret":"do-not-return"}},"spec":{"containers":[{"name":"web"},{"name":"sidecar"}]},"status":{"phase":"Running","containerStatuses":[{"name":"web","ready":true,"restartCount":1},{"name":"sidecar","ready":false,"restartCount":2}]}}]}`
	if err := os.WriteFile(kubectlPath, []byte("#!/bin/sh\ncat <<'JSON'\n"+podsJSON+"\nJSON\n"), 0o700); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
	result, err := runKubernetesPodList(context.Background(), Config{
		KubernetesPodLogsEnabled: true,
		KubeconfigSecretDir:      dir,
		KubectlPath:              kubectlPath,
	}, kubernetesPodListRequest{
		DeploymentTargetID: "target-1",
		Environment:        "test",
		ClusterName:        "test-cluster",
		Namespace:          "billing",
		KubeconfigRef:      "billing-reader",
	})
	if err != nil {
		t.Fatalf("runKubernetesPodList: %v", err)
	}
	if result["backend_state"] != "completed" ||
		result["result_scope"] != "sanitized_pod_metadata" ||
		result["kubeconfig_bound"] != true ||
		result["kubectl_command_invoked"] != true ||
		result["kubernetes_api_call"] != true ||
		result["log_body_included"] != false ||
		result["raw_response_included"] != false ||
		result["secret_included"] != false ||
		result["item_count"] != 1 {
		t.Fatalf("pod list metadata result = %#v", result)
	}
	items := mapSliceFromAny(result["items"])
	if len(items) != 1 ||
		items[0]["name"] != "api-7d9f" ||
		items[0]["phase"] != "Running" ||
		items[0]["ready_containers"] != 1 ||
		items[0]["restart_count"] != 3 {
		t.Fatalf("pod list items = %#v", items)
	}
	containers := stringSliceFromAny(items[0]["containers"])
	if len(containers) != 2 || containers[0] != "web" || containers[1] != "sidecar" {
		t.Fatalf("containers = %#v", containers)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"do-not-return", kubeconfigPath, "billing-reader", "apiVersion:", "clusters:"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("pod list metadata leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestRunKubernetesPodListDisabledDoesNotInvokeKubectl(t *testing.T) {
	result, err := runKubernetesPodList(context.Background(), Config{}, kubernetesPodListRequest{
		Namespace:     "billing",
		KubeconfigRef: "missing",
	})
	if err != nil {
		t.Fatalf("disabled pod list should not fail: %v", err)
	}
	if result["backend_state"] != "disabled" ||
		result["kubectl_command_invoked"] != false ||
		result["kubernetes_api_call"] != false ||
		result["log_body_included"] != false {
		t.Fatalf("disabled pod list result = %#v", result)
	}
}

func TestKubernetesPodLogBackendPlanReportsReadyWithoutReadingLogs(t *testing.T) {
	dir := t.TempDir()
	writeKubeconfig(t, filepath.Join(dir, "billing-reader"), 0o600)
	kubectlPath := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(kubectlPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
	plan := kubernetesPodLogBackendPlan(Config{
		KubernetesPodLogsEnabled: true,
		KubeconfigSecretDir:      dir,
		KubectlPath:              kubectlPath,
	}, map[string]any{
		"kubernetes_environment_id":     "kube-env-1",
		"kubernetes_environment_status": "ready",
		"kubeconfig_secret_ref_present": true,
		"kubeconfig_secret_ref":         "billing-reader",
		"token_subject_review_status":   "reviewed",
		"rbac_read_logs_status":         "reviewed",
	})
	if plan["ready"] != true ||
		plan["kubeconfig_secret_ref_resolved"] != true ||
		plan["kubectl_binary_available"] != true ||
		plan["kubeconfig_secret_read"] != false ||
		plan["log_body_included"] != false {
		t.Fatalf("backend plan = %#v", plan)
	}
}

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

func TestExecuteArgoPodLogAuditRunsEnabledKubectlMetadataOnly(t *testing.T) {
	dir := t.TempDir()
	writeKubeconfig(t, filepath.Join(dir, "billing-reader"), 0o600)
	kubectlPath := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(kubectlPath, []byte("#!/bin/sh\nprintf 'secret log line 1\\nsecret log line 2\\n'\n"), 0o700); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	worker := &ControlWorker{
		store: &Store{DB: sqlx.NewDb(db, "sqlmock")},
		cfg: Config{
			KubernetesPodLogsEnabled: true,
			KubeconfigSecretDir:      dir,
			KubectlPath:              kubectlPath,
		},
	}
	input := []byte(`{"project_id":"project-1","deployment_target_id":"target-1","deployment_target_name":"prod","environment":"test","cluster_name":"test-cluster","namespace":"billing","pod_name":"api-7d9f","container_name":"web","tail_lines":50,"since_seconds":60}`)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM operation_runs WHERE id=$1")).
		WithArgs("op-pod-logs").
		WillReturnRows(sqlmock.NewRows([]string{"id", "input"}).AddRow("op-pod-logs", input))
	mock.ExpectQuery(`(?s)SELECT id, name, kubeconfig_secret_ref, service_account, token_subject_review_status, rbac_read_logs_status, status\s+FROM kubernetes_environments`).
		WithArgs("project-1", "test", "test-cluster", "billing").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "kubeconfig_secret_ref", "service_account", "token_subject_review_status", "rbac_read_logs_status", "status"}).
			AddRow("kube-env-1", "billing", "billing-reader", "system:serviceaccount:billing:reader", "reviewed", "reviewed", "ready"))
	result, err := worker.executeArgoPodLogAudit(context.Background(), "op-pod-logs", map[string]any{})
	if err != nil {
		t.Fatalf("executeArgoPodLogAudit: %v", err)
	}
	if result["backend_state"] != "completed" ||
		result["result_scope"] != "sanitized_live_log_metadata" ||
		result["line_count"] != 2 ||
		result["kubectl_command_invoked"] != true ||
		result["kubernetes_api_call"] != true ||
		result["log_stream_opened"] != true ||
		result["log_body_included"] != false ||
		result["raw_response_included"] != false ||
		result["secret_included"] != false ||
		result["kubeconfig_secret_ref_present"] != true {
		t.Fatalf("enabled worker pod log metadata result = %#v", result)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"secret log line", filepath.Join(dir, "billing-reader"), "billing-reader", "apiVersion:", "clusters:"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("worker pod log metadata leaked %q: %s", forbidden, encoded)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
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

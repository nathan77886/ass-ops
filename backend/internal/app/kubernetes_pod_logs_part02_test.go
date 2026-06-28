package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestRunKubernetesPodRestartDisabledDoesNotInvokeKubectl(t *testing.T) {
	result, err := runKubernetesPodRestart(context.Background(), Config{}, kubernetesPodRestartRequest{
		Namespace:      "billing",
		DeploymentName: "api",
		KubeconfigRef:  "missing",
	})
	if err != nil {
		t.Fatalf("disabled pod restart should not fail: %v", err)
	}
	if result["backend_state"] != "disabled" ||
		result["kubectl_command_invoked"] != false ||
		result["kubernetes_api_call"] != false ||
		result["rollout_restart_invoked"] != false ||
		result["raw_response_included"] != false {
		t.Fatalf("disabled pod restart result = %#v", result)
	}
}

func TestRunKubernetesPodRestartRecordsMetadataOnly(t *testing.T) {
	dir := t.TempDir()
	kubeconfigPath := filepath.Join(dir, "billing-restarter")
	writeKubeconfig(t, kubeconfigPath, 0o600)
	kubectlPath := filepath.Join(dir, "kubectl")
	script := `#!/bin/sh
case "$*" in
  *"auth can-i patch deployment/api"*) printf 'yes\n'; exit 0 ;;
  *"--dry-run=server"*) printf 'dry-run-secret-output\n'; exit 0 ;;
  *"rollout restart deployment/api"*) printf 'restart-secret-output\n'; exit 0 ;;
esac
exit 1
`
	if err := os.WriteFile(kubectlPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
	result, err := runKubernetesPodRestart(context.Background(), Config{
		KubernetesRestartsEnabled: true,
		KubeconfigSecretDir:       dir,
		KubectlPath:               kubectlPath,
	}, kubernetesPodRestartRequest{
		DeploymentTargetID: "target-1",
		Environment:        "test",
		ClusterName:        "test-cluster",
		Namespace:          "billing",
		DeploymentName:     "api",
		KubeconfigRef:      "billing-restarter",
	})
	if err != nil {
		t.Fatalf("runKubernetesPodRestart: %v", err)
	}
	if result["backend_state"] != "completed" ||
		result["result_scope"] != "sanitized_rollout_restart_metadata" ||
		result["kubeconfig_bound"] != true ||
		result["kubectl_command_invoked"] != true ||
		result["kubernetes_api_call"] != true ||
		result["rbac_can_i_checked"] != true ||
		result["server_dry_run_checked"] != true ||
		result["rollout_restart_invoked"] != true ||
		result["stdout_included"] != false ||
		result["stderr_included"] != false ||
		result["raw_response_included"] != false ||
		result["secret_included"] != false {
		t.Fatalf("pod restart metadata result = %#v", result)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"secret-output", kubeconfigPath, "billing-restarter", "apiVersion:", "clusters:"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("pod restart metadata leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestRunKubernetesPodRestartRejectsAuthCanIDeniedWithoutLeakingOutput(t *testing.T) {
	dir := t.TempDir()
	kubeconfigPath := filepath.Join(dir, "billing-restarter")
	writeKubeconfig(t, kubeconfigPath, 0o600)
	kubectlPath := filepath.Join(dir, "kubectl")
	script := "#!/bin/sh\nprintf 'no secret-denied-output\\n'\nexit 0\n"
	if err := os.WriteFile(kubectlPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
	result, err := runKubernetesPodRestart(context.Background(), Config{
		KubernetesRestartsEnabled: true,
		KubeconfigSecretDir:       dir,
		KubectlPath:               kubectlPath,
	}, kubernetesPodRestartRequest{
		DeploymentTargetID: "target-1",
		Environment:        "test",
		ClusterName:        "test-cluster",
		Namespace:          "billing",
		DeploymentName:     "api",
		KubeconfigRef:      "billing-restarter",
	})
	if err == nil {
		t.Fatalf("auth denied pod restart should fail")
	}
	if result["backend_state"] != "failed" ||
		result["rollout_restart_invoked"] != false ||
		result["raw_response_included"] != false ||
		result["stdout_included"] != false ||
		result["stderr_included"] != false {
		t.Fatalf("auth denied pod restart result = %#v", result)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"secret-denied-output", kubeconfigPath, "billing-restarter"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("auth denied pod restart leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestValidateKubernetesPodRestartRequestRejectsInvalidNames(t *testing.T) {
	for _, req := range []kubernetesPodRestartRequest{
		{Namespace: "Billing", DeploymentName: "api", KubeconfigRef: "ref"},
		{Namespace: "billing", DeploymentName: "api/name", KubeconfigRef: "ref"},
		{Namespace: "billing", DeploymentName: "api", KubeconfigRef: ""},
	} {
		if err := validateKubernetesPodRestartRequest(req); err == nil {
			t.Fatalf("invalid restart request accepted: %#v", req)
		}
	}
}

func TestKubectlCanIAllowedAcceptsYesWithWhitespaceOnly(t *testing.T) {
	if !kubectlCanIAllowed(" yes\r\n") {
		t.Fatalf("expected yes output to be allowed")
	}
	for _, output := range []string{"no", "yesterday", ""} {
		if kubectlCanIAllowed(output) {
			t.Fatalf("unexpected auth can-i allow for %q", output)
		}
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

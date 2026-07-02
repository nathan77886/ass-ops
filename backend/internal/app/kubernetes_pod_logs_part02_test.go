package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestRunKubernetesPodListRecordsMetadataOnly(t *testing.T) {
	oldRun := kubernetesListPodsRun
	t.Cleanup(func() { kubernetesListPodsRun = oldRun })
	kubernetesListPodsRun = func(_ context.Context, _, _ string) ([]map[string]any, error) {
		return []map[string]any{{"name": "api-7d9f", "phase": "Running", "containers": []string{"web", "sidecar"}, "container_count": 2, "ready_containers": 1, "restart_count": 3, "created_at": "2026-06-26T05:00:00Z"}}, nil
	}
	result, err := runKubernetesPodList(context.Background(), Config{
		KubernetesPodLogsEnabled: true,
	}, kubernetesPodListRequest{
		DeploymentTargetID: "target-1",
		Environment:        "test",
		ClusterName:        "test-cluster",
		Namespace:          "billing",
		KubeconfigRef:      "billing-reader",
		KubeconfigSecret:   "apiVersion: v1\nclusters:\n- name: test\ncontexts:\n- name: test\nusers:\n- name: test\n",
	})
	if err != nil {
		t.Fatalf("runKubernetesPodList: %v", err)
	}
	if result["backend_state"] != "completed" ||
		result["result_scope"] != "sanitized_pod_metadata" ||
		result["kubeconfig_bound"] != true ||
		result["kubectl_command_invoked"] != false ||
		result["kubernetes_client_invoked"] != true ||
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
	for _, forbidden := range []string{"do-not-return", "billing-reader", "apiVersion:", "clusters:"} {
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
	oldRun := kubernetesRestartDeploymentRun
	t.Cleanup(func() { kubernetesRestartDeploymentRun = oldRun })
	kubernetesRestartDeploymentRun = func(_ context.Context, _ string, _ kubernetesPodRestartRequest) error {
		return nil
	}
	result, err := runKubernetesPodRestart(context.Background(), Config{
		KubernetesRestartsEnabled: true,
	}, kubernetesPodRestartRequest{
		DeploymentTargetID: "target-1",
		Environment:        "test",
		ClusterName:        "test-cluster",
		Namespace:          "billing",
		DeploymentName:     "api",
		KubeconfigRef:      "billing-restarter",
		KubeconfigSecret:   "apiVersion: v1\nclusters:\n- name: test\ncontexts:\n- name: test\nusers:\n- name: test\n",
	})
	if err != nil {
		t.Fatalf("runKubernetesPodRestart: %v", err)
	}
	if result["backend_state"] != "completed" ||
		result["result_scope"] != "sanitized_rollout_restart_metadata" ||
		result["kubeconfig_bound"] != true ||
		result["kubectl_command_invoked"] != false ||
		result["kubernetes_client_invoked"] != true ||
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
	for _, forbidden := range []string{"secret-output", "billing-restarter", "apiVersion:", "clusters:"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("pod restart metadata leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestRunKubernetesPodRestartRejectsAuthCanIDeniedWithoutLeakingOutput(t *testing.T) {
	oldRun := kubernetesRestartDeploymentRun
	t.Cleanup(func() { kubernetesRestartDeploymentRun = oldRun })
	kubernetesRestartDeploymentRun = func(_ context.Context, _ string, _ kubernetesPodRestartRequest) error {
		return fmt.Errorf("Kubernetes deployment patch access denied")
	}
	result, err := runKubernetesPodRestart(context.Background(), Config{
		KubernetesRestartsEnabled: true,
	}, kubernetesPodRestartRequest{
		DeploymentTargetID: "target-1",
		Environment:        "test",
		ClusterName:        "test-cluster",
		Namespace:          "billing",
		DeploymentName:     "api",
		KubeconfigRef:      "billing-restarter",
		KubeconfigSecret:   "apiVersion: v1\nclusters:\n- name: test\ncontexts:\n- name: test\nusers:\n- name: test\n",
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
	for _, forbidden := range []string{"secret-denied-output", "billing-restarter"} {
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

func TestKubernetesPodLogBackendPlanReportsReadyWithoutReadingLogs(t *testing.T) {
	plan := kubernetesPodLogBackendPlan(Config{
		KubernetesPodLogsEnabled: true,
	}, map[string]any{
		"kubernetes_environment_id":     "kube-env-1",
		"kubernetes_environment_status": "ready",
		"kubeconfig_secret_ref_present": true,
		"kubeconfig_secret_ref":         "billing-reader",
		"token_subject_review_status":   "reviewed",
		"rbac_read_logs_status":         "reviewed",
	})
	if plan["ready"] != true ||
		plan["kubeconfig_secret_configured"] != true ||
		plan["kubernetes_client_available"] != true ||
		plan["kubeconfig_secret_read"] != false ||
		plan["log_body_included"] != false {
		t.Fatalf("backend plan = %#v", plan)
	}
}

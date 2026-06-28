package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
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

func TestSanitizedKubernetesLogPreviewRedactsCommonSecretShapes(t *testing.T) {
	input := strings.Join([]string{
		`{"password":"secret123","client_secret":"client-value","access_key":"access-value","secret_key":"secret-value"}`,
		"Authorization: Bearer auth-token",
		"Authorization: Basic basic-auth-value",
		"X-Api-Key: x-api-key-value",
		"X-Auth-Token: x-auth-token-value",
		"access_token: access-token-value auth_token: auth-token-value",
		"client-key-data: client-key-value",
		"client-certificate-data: client-cert-value",
		"certificate-authority-data: ca-cert-value",
		"Basic bare-basic-value",
		"Set-Cookie: session=abc123; HttpOnly",
		"plain token=token-value api_key=api-value",
		"-----BEGIN PRIVATE KEY-----",
		"safe line",
	}, "\n")
	preview, truncated := sanitizedKubernetesLogPreview(input, 4096)
	if truncated {
		t.Fatalf("preview should not be truncated")
	}
	for _, forbidden := range []string{
		"secret123",
		"client-value",
		"access-value",
		"secret-value",
		"auth-token",
		"basic-auth-value",
		"x-api-key-value",
		"x-auth-token-value",
		"access-token-value",
		"auth-token-value",
		"client-key-value",
		"client-cert-value",
		"ca-cert-value",
		"bare-basic-value",
		"session=abc123",
		"token-value",
		"api-value",
		"BEGIN PRIVATE KEY",
	} {
		if strings.Contains(preview, forbidden) {
			t.Fatalf("preview leaked %q: %s", forbidden, preview)
		}
	}
	if !strings.Contains(preview, "<redacted>") || !strings.Contains(preview, "<redacted-private-key>") || !strings.Contains(preview, "safe line") {
		t.Fatalf("preview did not preserve redaction markers/safe text: %s", preview)
	}
}

func TestSanitizedKubernetesLogPreviewTruncatesAtUTF8Boundary(t *testing.T) {
	preview, truncated := sanitizedKubernetesLogPreview("prefix 日本語 suffix", 10)
	if !truncated {
		t.Fatal("preview should be truncated")
	}
	if !strings.HasPrefix(preview, "prefix ") {
		t.Fatalf("preview prefix = %q", preview)
	}
	if !utf8.ValidString(preview) {
		t.Fatalf("preview is not valid UTF-8: %q", preview)
	}
}

func TestRunKubernetesPodLogsReturnsRedactedPreviewWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	kubeconfigPath := filepath.Join(dir, "billing-reader")
	writeKubeconfig(t, kubeconfigPath, 0o600)
	kubectlPath := filepath.Join(dir, "kubectl")
	argsPath := filepath.Join(dir, "args.txt")
	if err := os.WriteFile(kubectlPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > '"+argsPath+"'\nprintf 'hello\\npassword=secret123\\nAuthorization: Bearer abc123\\n'\n"), 0o700); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
	result, err := runKubernetesPodLogs(context.Background(), Config{
		KubernetesPodLogsEnabled:    true,
		KubernetesLogPreviewEnabled: true,
		KubeconfigSecretDir:         dir,
		KubectlPath:                 kubectlPath,
	}, kubernetesPodLogRequest{
		DeploymentTargetID: "target-1",
		Environment:        "test",
		ClusterName:        "test-cluster",
		Namespace:          "billing",
		PodName:            "api-7d9f",
		ContainerName:      "web",
		TailLines:          500,
		SinceSeconds:       60,
		KubeconfigRef:      "billing-reader",
	})
	if err != nil {
		t.Fatalf("runKubernetesPodLogs: %v", err)
	}
	preview := stringFromMap(result, "redacted_log_preview")
	if result["backend_state"] != "completed" ||
		result["result_scope"] != "redacted_live_log_preview" ||
		result["redacted_log_body_included"] != true ||
		result["log_body_included"] != false ||
		result["raw_response_included"] != false ||
		result["preview_line_count"] != 3 ||
		!strings.Contains(preview, "hello") ||
		!strings.Contains(preview, "password=<redacted>") {
		t.Fatalf("live pod log preview result = %#v", result)
	}
	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read kubectl args: %v", err)
	}
	joinedArgs := string(argsBytes)
	if strings.Contains(joinedArgs, "--tail 500") || !strings.Contains(joinedArgs, "--tail 200") {
		t.Fatalf("kubectl args should cap tail at 200, got %q", joinedArgs)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"secret123", "abc123", kubeconfigPath, "billing-reader", "apiVersion:", "clusters:"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("pod log preview leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestSafePodLogPreviewFromOperationResultRequiresSafeFlags(t *testing.T) {
	preview, lineCount, truncated := safePodLogPreviewFromOperationResult(map[string]any{
		"redacted_log_body_included": true,
		"log_body_included":          false,
		"raw_response_included":      false,
		"secret_included":            false,
		"redacted_log_preview":       "hello\ntoken=secret-token",
		"preview_truncated":          true,
	})
	if !strings.Contains(preview, "token=<redacted>") || strings.Contains(preview, "secret-token") || lineCount != 2 || !truncated {
		t.Fatalf("safe preview = %q lineCount=%d truncated=%v", preview, lineCount, truncated)
	}
	for _, result := range []map[string]any{
		{"redacted_log_body_included": true, "log_body_included": true, "redacted_log_preview": "raw"},
		{"redacted_log_body_included": true, "raw_response_included": true, "redacted_log_preview": "raw"},
		{"redacted_log_body_included": true, "secret_included": true, "redacted_log_preview": "raw"},
		{"redacted_log_body_included": false, "redacted_log_preview": "raw"},
	} {
		if preview, _, _ := safePodLogPreviewFromOperationResult(result); preview != "" {
			t.Fatalf("unsafe result exposed preview: %#v -> %q", result, preview)
		}
	}
}

func TestCopySafeArgoPodLogLiveResultRejectsUnexpectedSensitiveFields(t *testing.T) {
	result := map[string]any{"deployment_target_id": "target-1"}
	copySafeArgoPodLogLiveResult(result, map[string]any{
		"backend_state":              "completed",
		"redacted_log_preview":       "hello",
		"redacted_log_body_included": true,
		"raw_stdout":                 "secret stdout",
		"kubeconfig_token":           "secret token",
		"pod_env":                    map[string]any{"PASSWORD": "secret"},
	})
	if result["backend_state"] != "completed" ||
		result["redacted_log_preview"] != "hello" ||
		result["redacted_log_body_included"] != true {
		t.Fatalf("safe fields were not copied: %#v", result)
	}
	for _, key := range []string{"raw_stdout", "kubeconfig_token", "pod_env"} {
		if _, ok := result[key]; ok {
			t.Fatalf("unexpected sensitive field %s copied: %#v", key, result)
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

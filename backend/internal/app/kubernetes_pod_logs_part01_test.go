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

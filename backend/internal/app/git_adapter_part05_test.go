package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvisionTemplateRepositoryRejectsPrivateProviderAPI(t *testing.T) {
	t.Setenv("ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST", "secret-token")
	_, err := (&GitExecutor{}).ProvisionTemplateRepository(context.Background(),
		map[string]any{"id": "repo-1", "repo_key": "billing-service", "default_branch": "main"},
		[]map[string]any{{
			"id":            "remote-1",
			"provider_type": "github",
			"metadata": map[string]any{
				"api_base_url": "http://127.0.0.1:1",
				"owner":        "acme",
				"token_env":    "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST",
			},
		}},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "non-public address") {
		t.Fatalf("error = %v, want non-public address rejection", err)
	}
}

func TestProvisionTemplateRepositoryProviderErrorDoesNotPersistBody(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST", "secret-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"provider rejected request"}`))
	}))
	defer server.Close()

	result, err := (&GitExecutor{HTTPClient: server.Client()}).ProvisionTemplateRepository(context.Background(),
		map[string]any{"id": "repo-1", "repo_key": "billing-service", "default_branch": "main"},
		[]map[string]any{{
			"id":            "remote-1",
			"provider_type": "github",
			"metadata": map[string]any{
				"api_base_url": server.URL,
				"owner":        "acme",
				"token_env":    "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST",
			},
		}},
		nil,
	)
	if err == nil {
		t.Fatal("expected provider error")
	}
	if strings.Contains(err.Error(), "{") || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error should not include raw body or token: %v", err)
	}
	if !strings.Contains(err.Error(), "provider rejected request") {
		t.Fatalf("error should include sanitized provider message: %v", err)
	}
	if result == nil || result.Details == nil {
		t.Fatal("provider error should return diagnostic details")
	}
	if result.Details["provider_status"] != http.StatusInternalServerError {
		t.Fatalf("provider_status = %v", result.Details["provider_status"])
	}
	if result.Details["provider_error"] != "provider rejected request" {
		t.Fatalf("provider_error = %v", result.Details["provider_error"])
	}
	encoded, _ := json.Marshal(result.Details)
	if strings.Contains(string(encoded), "secret-token") {
		t.Fatalf("details leaked token: %s", encoded)
	}
}

func TestTemplateFileContentTreatsNilAsEmpty(t *testing.T) {
	if got := templateFileContent(map[string]any{"content": nil}); got != "" {
		t.Fatalf("templateFileContent(nil) = %q, want empty", got)
	}
	if got := templateFileContent(map[string]any{"content": "hello"}); got != "hello" {
		t.Fatalf("templateFileContent = %q, want hello", got)
	}
}

func TestTemplateProviderAlreadyExistsParsesStructuredErrors(t *testing.T) {
	body := []byte(`{"errors":[{"message":"name already exists"}]}`)
	if !templateProviderAlreadyExists(http.StatusUnprocessableEntity, body) {
		t.Fatal("structured already-exists error should be accepted")
	}
	body = []byte(`{"message":"schema mentions already_exists but failed for another reason"}`)
	if templateProviderAlreadyExists(http.StatusUnprocessableEntity, body) {
		t.Fatal("free-form mention should not be treated as already exists")
	}
	if !templateProviderAlreadyExists(http.StatusConflict, []byte(`not json`)) {
		t.Fatal("409 conflict should be treated as already exists")
	}
}

func TestProvisionTemplateRepositoryAlreadyExistsIncludesDiagnostics(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST", "secret-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"errors":[{"message":"name already exists"}]}`))
	}))
	defer server.Close()

	result, err := (&GitExecutor{HTTPClient: server.Client()}).ProvisionTemplateRepository(context.Background(),
		map[string]any{"id": "repo-1", "repo_key": "billing-service", "default_branch": "main"},
		[]map[string]any{{
			"id":            "remote-1",
			"provider_type": "github",
			"remote_url":    "git@github.com:acme/billing-service.git",
			"metadata": map[string]any{
				"api_base_url": server.URL,
				"owner":        "acme",
				"token_env":    "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST",
			},
		}},
		nil,
	)
	if err != nil {
		t.Fatalf("already exists should not fail: %v", err)
	}
	if result == nil || result.Details == nil {
		t.Fatal("expected diagnostics")
	}
	if result.Details["already_provisioned"] != true || result.Details["provider_status"] != http.StatusUnprocessableEntity {
		t.Fatalf("details = %#v", result.Details)
	}
	if result.Details["token_configured"] != true || result.Details["provider_error"] != "already exists" {
		t.Fatalf("diagnostics = %#v", result.Details)
	}
}

func TestProvisionTemplateRepositorySkipsStarterPushWhenExternalRepositoryExists(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST", "secret-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"errors":[{"message":"name already exists"}]}`))
	}))
	defer server.Close()

	result, err := (&GitExecutor{HTTPClient: server.Client()}).ProvisionTemplateRepository(context.Background(),
		map[string]any{"id": "repo-1", "repo_key": "billing-service", "default_branch": "main"},
		[]map[string]any{{
			"id":            "remote-1",
			"provider_type": "github",
			"remote_url":    "git@github.com:acme/billing-service.git",
			"metadata": map[string]any{
				"api_base_url": server.URL,
				"owner":        "acme",
				"token_env":    "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST",
			},
		}},
		[]map[string]any{{"path": "README.md", "content": "# Billing\n"}},
	)
	if err != nil {
		t.Fatalf("already exists should not fail: %v", err)
	}
	if result.Details["provisioned"] != false || result.Details["repository_exists"] != true || result.Details["starter_push_skipped"] != true {
		t.Fatalf("existing repository skip details = %#v", result.Details)
	}
	if !strings.Contains(fmt.Sprint(result.Details["reason"]), "already exists") {
		t.Fatalf("reason = %v", result.Details["reason"])
	}
	reconcile := mapFromAny(result.Details["repository_reconciliation"])
	if reconcile["kind"] != "existing_repository" || reconcile["guardrail"] != "existing_repository_push_blocked" {
		t.Fatalf("reconciliation = %#v", reconcile)
	}
	credentialStrategy := mapFromAny(reconcile["credential_strategy"])
	if credentialStrategy["token_stored"] != false {
		t.Fatalf("credential strategy should not expose token values: %#v", credentialStrategy)
	}
	encoded, _ := json.Marshal(reconcile)
	if strings.Contains(string(encoded), "secret-token") || strings.Contains(string(encoded), "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST") {
		t.Fatalf("reconciliation leaked token material: %s", encoded)
	}
}

func TestTemplateRepositoryReconciliationUnknownKind(t *testing.T) {
	got := templateRepositoryReconciliation(
		"provider_policy",
		map[string]any{"repo_key": "billing-service"},
		map[string]any{"id": "remote-1", "provider_type": "github"},
		"main",
		2,
	)
	if got["kind"] != "provider_policy" || got["guardrail"] != "manual_reconciliation_required" {
		t.Fatalf("reconciliation = %#v", got)
	}
	if got["provider_type"] != "github" || got["repository_key"] != "billing-service" {
		t.Fatalf("reconciliation identifiers = %#v", got)
	}
	credentialStrategy := mapFromAny(got["credential_strategy"])
	if credentialStrategy["token_stored"] != false {
		t.Fatalf("credential strategy should not store token values: %#v", credentialStrategy)
	}
}

func TestProvisionTemplateRepositoryAllowsStarterPushWhenExistingRepositoryOptedIn(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary is required for template repository provisioning test")
	}
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST", "secret-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"errors":[{"message":"name already exists"}]}`))
	}))
	defer server.Close()
	root := t.TempDir()
	remotePath := filepath.Join(root, "repos", "billing.git")
	if err := os.MkdirAll(filepath.Dir(remotePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "--bare", remotePath).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}

	result, err := (&GitExecutor{HTTPClient: server.Client(), WorkDir: filepath.Join(root, "work")}).ProvisionTemplateRepository(context.Background(),
		map[string]any{"id": "repo-1", "repo_key": "billing-service", "default_branch": "main"},
		[]map[string]any{{
			"id":            "remote-1",
			"provider_type": "github",
			"remote_url":    remotePath,
			"metadata": map[string]any{
				"api_base_url":                   server.URL,
				"owner":                          "acme",
				"token_env":                      "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST",
				"allow_existing_repository_push": true,
			},
		}},
		[]map[string]any{{"path": "README.md", "content": "# Billing\n"}},
	)
	if err != nil {
		t.Fatalf("existing repository opt-in should push starter files: %v\nstdout=%s\nstderr=%s", err, result.Stdout, result.Stderr)
	}
	if result.Details["provisioned"] != true || result.Details["starter_push_skipped"] == true {
		t.Fatalf("existing repository opt-in details = %#v", result.Details)
	}
	out, err := exec.Command("git", "--git-dir", remotePath, "show", "refs/heads/main:README.md").CombinedOutput()
	if err != nil {
		t.Fatalf("git show pushed README: %v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "# Billing" {
		t.Fatalf("README content = %q", out)
	}
}

func TestProviderErrorSuffixTruncatesLongMessages(t *testing.T) {
	longMessage := strings.Repeat("x", providerDiagnosticErrorLimit+20)
	suffix := providerErrorSuffix([]byte(`{"message":"` + longMessage + `"}`))
	if !strings.HasSuffix(suffix, "...") {
		t.Fatalf("suffix should be truncated: %q", suffix)
	}
	if len(strings.TrimPrefix(suffix, ": ")) != providerDiagnosticErrorLimit+3 {
		t.Fatalf("suffix length = %d", len(strings.TrimPrefix(suffix, ": ")))
	}
}

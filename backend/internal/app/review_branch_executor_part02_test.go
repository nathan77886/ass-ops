package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReviewBranchExecutorMarksManualCleanupWhenBranchDeleteFails(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_GITHUB_TEMPLATE_TOKEN", "test-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			writeJSONResponse(t, w, http.StatusOK, map[string]any{"object": map[string]any{"sha": "0123456789abcdef0123456789abcdef01234567"}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/billing/git/refs":
			writeJSONResponse(t, w, http.StatusCreated, map[string]any{"ref": "refs/heads/assops/review/attempt-1"})
		case r.Method == http.MethodPut:
			writeJSONResponse(t, w, http.StatusInternalServerError, map[string]any{"message": "file content rejected: secret detail"})
		case r.Method == http.MethodDelete && r.URL.Path == "/repos/acme/billing/git/refs/heads/assops/review/attempt-1":
			writeJSONResponse(t, w, http.StatusInternalServerError, map[string]any{"message": "delete failed: secret branch detail"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	result, err := (reviewBranchExecutor{HTTPClient: server.Client()}).Execute(context.Background(), reviewBranchExecutionInput{
		ProviderType: "github",
		APIBase:      server.URL,
		Owner:        "acme",
		Repository:   "billing",
		BaseBranch:   "main",
		ReviewBranch: "assops/review/attempt-1",
		TokenEnv:     "ASSOPS_GITHUB_TEMPLATE_TOKEN",
		Files:        map[string]string{"README.md": "# hello\n"},
	})
	if err == nil {
		t.Fatalf("expected file commit error")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("provider response leaked in error: %v", err)
	}
	if result.ExecutionPhase != "commit_starter_files" ||
		result.ProviderStatusClass != "5xx" ||
		result.Retryable != false ||
		result.CleanupAttempted != true ||
		result.CleanupSucceeded != false ||
		result.CleanupRequired != true ||
		result.ManualCleanupHint != "review_branch_delete_required" {
		t.Fatalf("unexpected manual cleanup result: %#v", result)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"test-token", "# hello", "secret branch detail", "secret detail"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("manual cleanup result leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestReviewBranchExecutorCleanupDeletesReviewBranch(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_GITHUB_TEMPLATE_TOKEN", "test-token")
	var deleteCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("missing auth header on %s %s", r.Method, r.URL.Path)
		}
		if r.Method != http.MethodDelete || r.URL.Path != "/repos/acme/billing/git/refs/heads/assops/review/attempt-1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		deleteCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	result, err := (reviewBranchExecutor{HTTPClient: server.Client()}).Cleanup(context.Background(), reviewBranchExecutionInput{
		ProviderType: "github",
		APIBase:      server.URL,
		Owner:        "acme",
		Repository:   "billing",
		ReviewBranch: "assops/review/attempt-1",
		TokenEnv:     "ASSOPS_GITHUB_TEMPLATE_TOKEN",
	})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !deleteCalled ||
		result.ExecutionPhase != "cleanup_review_branch" ||
		result.ProviderStatusClass != "2xx" ||
		result.ProviderAPIMutation != true ||
		result.ExternalCallMade != true ||
		result.CleanupAttempted != true ||
		result.CleanupSucceeded != true ||
		result.CleanupRequired != false ||
		result.ManualCleanupHint != "" ||
		result.Retryable != false {
		t.Fatalf("unexpected cleanup result: %#v", result)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"test-token", "Authorization"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("cleanup result leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestReviewBranchExecutorCleanupFailureKeepsManualCleanupHint(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_GITHUB_TEMPLATE_TOKEN", "test-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		writeJSONResponse(t, w, http.StatusInternalServerError, map[string]any{"message": "secret provider cleanup detail"})
	}))
	defer server.Close()
	result, err := (reviewBranchExecutor{HTTPClient: server.Client()}).Cleanup(context.Background(), reviewBranchExecutionInput{
		ProviderType: "github",
		APIBase:      server.URL,
		Owner:        "acme",
		Repository:   "billing",
		ReviewBranch: "assops/review/attempt-1",
		TokenEnv:     "ASSOPS_GITHUB_TEMPLATE_TOKEN",
	})
	if err == nil {
		t.Fatalf("expected cleanup error")
	}
	if strings.Contains(err.Error(), "secret provider cleanup detail") {
		t.Fatalf("provider cleanup response leaked in error: %v", err)
	}
	if result.ExecutionPhase != "cleanup_review_branch" ||
		result.ProviderStatusClass != "5xx" ||
		result.Retryable != true ||
		result.CleanupAttempted != true ||
		result.CleanupSucceeded != false ||
		result.CleanupRequired != true ||
		result.ManualCleanupHint != "review_branch_delete_required" {
		t.Fatalf("unexpected failed cleanup result: %#v", result)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"test-token", "secret provider cleanup detail"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("failed cleanup result leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestReviewBranchExecutorRejectsInvalidBaseRefShape(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_GITHUB_TEMPLATE_TOKEN", "test-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSONResponse(t, w, http.StatusOK, map[string]any{"object": map[string]any{"sha": "not-a-sha"}})
	}))
	defer server.Close()
	result, err := (reviewBranchExecutor{HTTPClient: server.Client()}).Execute(context.Background(), reviewBranchExecutionInput{
		ProviderType: "github",
		APIBase:      server.URL,
		Owner:        "acme",
		Repository:   "billing",
		BaseBranch:   "main",
		ReviewBranch: "assops/review/attempt-1",
		TokenEnv:     "ASSOPS_GITHUB_TEMPLATE_TOKEN",
		Files:        map[string]string{"README.md": "# hello\n"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid base ref") {
		t.Fatalf("expected invalid base ref error, got %v", err)
	}
	if result.ProviderAPIMutation != false || result.CleanupAttempted != false {
		t.Fatalf("unexpected invalid base ref result: %#v", result)
	}
}

func TestReviewBranchExecutorRequiresSafeTokenEnvAndRefs(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_GITHUB_TEMPLATE_TOKEN", "")
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	_, err := (reviewBranchExecutor{HTTPClient: server.Client()}).Execute(context.Background(), reviewBranchExecutionInput{
		ProviderType: "github",
		APIBase:      server.URL,
		Owner:        "acme",
		Repository:   "billing",
		BaseBranch:   "main",
		ReviewBranch: "assops/review/attempt-1",
		TokenEnv:     "ASSOPS_GITHUB_TEMPLATE_TOKEN",
		Files:        map[string]string{"README.md": "# hello\n"},
	})
	if err == nil || !strings.Contains(err.Error(), "token environment") {
		t.Fatalf("expected missing token env error, got %v", err)
	}
	_, err = (reviewBranchExecutor{HTTPClient: server.Client()}).Execute(context.Background(), reviewBranchExecutionInput{
		ProviderType: "github",
		APIBase:      server.URL,
		Owner:        "acme",
		Repository:   "billing",
		BaseBranch:   "main",
		ReviewBranch: "main",
		TokenEnv:     "UNSAFE_TOKEN_ENV",
		Files:        map[string]string{"../README.md": "# hello\n"},
	})
	if err == nil {
		t.Fatalf("expected unsafe input error")
	}
}

func decodeJSONRequest(t *testing.T, r *http.Request, out any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		t.Fatalf("decode request: %v", err)
	}
}

func writeJSONResponse(t *testing.T, w http.ResponseWriter, status int, payload map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

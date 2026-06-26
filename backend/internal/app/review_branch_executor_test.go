package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReviewBranchExecutorGitHubCreatesBranchFilesAndPullRequest(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_GITHUB_TEMPLATE_TOKEN", "test-token")
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("missing auth header on %s %s", r.Method, r.URL.Path)
		}
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/billing/git/ref/heads/main":
			writeJSONResponse(t, w, http.StatusOK, map[string]any{"object": map[string]any{"sha": "0123456789abcdef0123456789abcdef01234567"}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/billing/git/refs":
			var payload map[string]any
			decodeJSONRequest(t, r, &payload)
			if payload["ref"] != "refs/heads/assops/review/attempt-1" || payload["sha"] != "0123456789abcdef0123456789abcdef01234567" {
				t.Fatalf("unexpected create ref payload: %#v", payload)
			}
			writeJSONResponse(t, w, http.StatusCreated, map[string]any{"ref": payload["ref"]})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/repos/acme/billing/contents/"):
			var payload map[string]any
			decodeJSONRequest(t, r, &payload)
			if payload["branch"] != "assops/review/attempt-1" {
				t.Fatalf("unexpected file branch: %#v", payload)
			}
			content, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload["content"].(string)))
			if err != nil {
				t.Fatalf("decode file content: %v", err)
			}
			if !strings.Contains(string(content), "hello") && !strings.Contains(string(content), "service") {
				t.Fatalf("unexpected file content: %s", content)
			}
			writeJSONResponse(t, w, http.StatusCreated, map[string]any{"content": map[string]any{"path": strings.TrimPrefix(r.URL.Path, "/repos/acme/billing/contents/")}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/billing/pulls":
			var payload map[string]any
			decodeJSONRequest(t, r, &payload)
			if payload["head"] != "assops/review/attempt-1" || payload["base"] != "main" || payload["title"] != "Initialize service" {
				t.Fatalf("unexpected pull payload: %#v", payload)
			}
			writeJSONResponse(t, w, http.StatusCreated, map[string]any{"html_url": "https://github.com/acme/billing/pull/7"})
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
		Title:        "Initialize service",
		Body:         "Created by ASSOPS.",
		Files:        map[string]string{"README.md": "# hello\n", "src/service.go": "package service\n"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ProviderAPIMutation != true ||
		result.ExternalCallMade != true ||
		result.ProviderStatusClass != "2xx" ||
		result.ReviewURL != "https://github.com/acme/billing/pull/7" ||
		result.FileCount != 2 ||
		result.TokenIncluded != false ||
		result.RequestBodiesIncluded != false ||
		result.ResponseBodyIncluded != false {
		t.Fatalf("unexpected result: %#v", result)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"test-token", "# hello", "package service"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("result leaked %q: %s", forbidden, encoded)
		}
	}
	wantCalls := []string{
		"GET /repos/acme/billing/git/ref/heads/main",
		"POST /repos/acme/billing/git/refs",
		"PUT /repos/acme/billing/contents/README.md",
		"PUT /repos/acme/billing/contents/src/service.go",
		"POST /repos/acme/billing/pulls",
	}
	if strings.Join(calls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestReviewBranchExecutorRejectsExistingBranchWithoutForceUpdate(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_GITHUB_TEMPLATE_TOKEN", "test-token")
	var putCalled bool
	var deleteCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			writeJSONResponse(t, w, http.StatusOK, map[string]any{"object": map[string]any{"sha": "0123456789abcdef0123456789abcdef01234567"}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/billing/git/refs":
			writeJSONResponse(t, w, http.StatusUnprocessableEntity, map[string]any{"message": "secret provider detail"})
		case r.Method == http.MethodPut:
			putCalled = true
			t.Fatalf("file commit should not run after branch creation failure")
		case r.Method == http.MethodDelete:
			deleteCalled = true
			t.Fatalf("cleanup should not run when branch was not created")
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
		t.Fatalf("expected existing branch error")
	}
	if strings.Contains(err.Error(), "secret provider detail") {
		t.Fatalf("provider message leaked in error: %v", err)
	}
	if result.ProviderStatusClass != "4xx" || result.ProviderAPIMutation != false || putCalled || deleteCalled {
		t.Fatalf("unexpected result after branch conflict: %#v", result)
	}
}

func TestReviewBranchExecutorCleansReviewBranchAfterFileFailure(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_GITHUB_TEMPLATE_TOKEN", "test-token")
	var deleteCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			writeJSONResponse(t, w, http.StatusOK, map[string]any{"object": map[string]any{"sha": "0123456789abcdef0123456789abcdef01234567"}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/billing/git/refs":
			writeJSONResponse(t, w, http.StatusCreated, map[string]any{"ref": "refs/heads/assops/review/attempt-1"})
		case r.Method == http.MethodPut:
			writeJSONResponse(t, w, http.StatusInternalServerError, map[string]any{"message": "file content rejected: secret detail"})
		case r.Method == http.MethodDelete && r.URL.Path == "/repos/acme/billing/git/refs/heads/assops/review/attempt-1":
			deleteCalled = true
			w.WriteHeader(http.StatusNoContent)
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
	if strings.Contains(err.Error(), "secret detail") {
		t.Fatalf("provider response leaked in error: %v", err)
	}
	if !deleteCalled ||
		result.ProviderStatusClass != "5xx" ||
		result.ProviderAPIMutation != true ||
		result.CleanupAttempted != true ||
		result.CleanupSucceeded != true ||
		result.CleanupRequired != false {
		t.Fatalf("unexpected cleanup result: %#v", result)
	}
}

func TestReviewBranchExecutorCleansReviewBranchAfterPullRequestFailure(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_GITHUB_TEMPLATE_TOKEN", "test-token")
	var deleteCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			writeJSONResponse(t, w, http.StatusOK, map[string]any{"object": map[string]any{"sha": "0123456789abcdef0123456789abcdef01234567"}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/billing/git/refs":
			writeJSONResponse(t, w, http.StatusCreated, map[string]any{"ref": "refs/heads/assops/review/attempt-1"})
		case r.Method == http.MethodPut:
			writeJSONResponse(t, w, http.StatusCreated, map[string]any{"content": map[string]any{"path": "README.md"}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/billing/pulls":
			writeJSONResponse(t, w, http.StatusInternalServerError, map[string]any{"message": "pull request failed: secret detail"})
		case r.Method == http.MethodDelete && r.URL.Path == "/repos/acme/billing/git/refs/heads/assops/review/attempt-1":
			deleteCalled = true
			w.WriteHeader(http.StatusNoContent)
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
		t.Fatalf("expected pull request error")
	}
	if strings.Contains(err.Error(), "secret detail") {
		t.Fatalf("provider response leaked in error: %v", err)
	}
	if !deleteCalled ||
		result.ProviderStatusClass != "5xx" ||
		result.ProviderAPIMutation != true ||
		result.CleanupAttempted != true ||
		result.CleanupSucceeded != true ||
		result.CleanupRequired != false {
		t.Fatalf("unexpected cleanup result: %#v", result)
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

package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseGitHubRepository(t *testing.T) {
	tests := []struct {
		raw       string
		wantOwner string
		wantRepo  string
	}{
		{raw: "https://github.com/acme/api.git", wantOwner: "acme", wantRepo: "api"},
		{raw: "git@github.com:acme/api.git", wantOwner: "acme", wantRepo: "api"},
		{raw: "git@www.github.com:acme/api.git", wantOwner: "acme", wantRepo: "api"},
		{raw: "ssh://git@github.com/acme/api.git", wantOwner: "acme", wantRepo: "api"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			owner, repo, ok := parseGitHubRepository(tt.raw)
			if !ok {
				t.Fatal("expected repository to parse")
			}
			if owner != tt.wantOwner || repo != tt.wantRepo {
				t.Fatalf("parseGitHubRepository = %s/%s, want %s/%s", owner, repo, tt.wantOwner, tt.wantRepo)
			}
		})
	}
}

func TestParseGitHubRepositoryRejectsNonGitHub(t *testing.T) {
	for _, raw := range []string{"", "<nil>", "https://gitlab.com/acme/api.git", "not-a-url"} {
		if owner, repo, ok := parseGitHubRepository(raw); ok {
			t.Fatalf("parseGitHubRepository(%q) = %s/%s, want rejected", raw, owner, repo)
		}
	}
}

func TestTokenFromRemote(t *testing.T) {
	t.Setenv("ASSOPS_GITHUB_ACTIONS_READ_TOKEN", "env-token")
	got := tokenFromRemote(map[string]any{"metadata": map[string]any{"github_token": "remote-token"}})
	if got != "env-token" {
		t.Fatalf("tokenFromRemote = %q, want env-token", got)
	}
}

func TestValidateGitHubTokenScopes(t *testing.T) {
	if err := validateGitHubTokenScopes("read:org, actions:read"); err != nil {
		t.Fatalf("validateGitHubTokenScopes returned error: %v", err)
	}
	if err := validateGitHubTokenScopes("actions:read, delete_repo"); err == nil {
		t.Fatal("expected disallowed scope to fail")
	}
	if err := validateGitHubTokenScopes("repo"); err == nil {
		t.Fatal("expected repo scope to fail")
	}
}

func TestFetchWorkflowRuns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/repos/acme/api/actions/runs" {
			t.Fatalf("path = %q", got)
		}
		if got := r.URL.Query().Get("branch"); got != "main" {
			t.Fatalf("branch = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{
			"workflow_runs": [{
				"id": 123,
				"name": "CI",
				"run_number": 7,
				"head_branch": "main",
				"head_sha": "0123456789abcdef0123456789abcdef01234567",
				"status": "completed",
				"conclusion": "success",
				"html_url": "https://github.com/acme/api/actions/runs/123",
				"event": "push"
			}]
		}`))
	}))
	defer server.Close()

	syncer := &GitHubActionsSyncer{HTTPClient: server.Client(), APIBase: server.URL}
	runs, err := syncer.fetchWorkflowRuns(context.Background(), "acme", "api", "main", 10, "test-token")
	if err != nil {
		t.Fatalf("fetchWorkflowRuns returned error: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("len(runs) = %d, want 1", len(runs))
	}
	if runs[0].WorkflowName != "CI" || runs[0].Conclusion != "success" || runs[0].RunID != "123" {
		t.Fatalf("unexpected run: %+v", runs[0])
	}
}

package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q", got)
		}
		switch got := r.URL.Path; got {
		case "/repos/acme/api/actions/runs":
			if got := r.URL.Query().Get("branch"); got != "main" {
				t.Fatalf("branch = %q", got)
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
		case "/repos/acme/api/actions/runs/123/artifacts":
			_, _ = w.Write([]byte(`{
				"total_count": 1,
				"artifacts": [{
					"id": 456,
					"node_id": "artifact-node",
					"name": "linux-build",
					"size_in_bytes": 2048,
					"expired": false
				}]
			}`))
		default:
			t.Fatalf("path = %q", got)
		}
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
	if len(runs[0].Artifacts) != 1 || runs[0].Artifacts[0].Name != "linux-build" || runs[0].Artifacts[0].SizeInBytes != 2048 {
		t.Fatalf("unexpected artifacts: %+v", runs[0].Artifacts)
	}
}

func TestFetchWorkflowRunArtifactsPaginates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/repos/acme/api/actions/runs/123/artifacts" {
			t.Fatalf("path = %q", got)
		}
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = fmt.Fprint(w, `{"total_count":2,"artifacts":[{"id":456,"name":"linux-build","size_in_bytes":2048}]}`)
		case "2":
			_, _ = fmt.Fprint(w, `{"total_count":2,"artifacts":[{"id":789,"name":"darwin-build","size_in_bytes":4096}]}`)
		default:
			t.Fatalf("unexpected page %q", r.URL.Query().Get("page"))
		}
	}))
	defer server.Close()

	syncer := &GitHubActionsSyncer{HTTPClient: server.Client(), APIBase: server.URL}
	artifacts, err := syncer.fetchWorkflowRunArtifacts(context.Background(), "acme", "api", "123", "")
	if err != nil {
		t.Fatalf("fetchWorkflowRunArtifacts returned error: %v", err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("len(artifacts) = %d, want 2: %+v", len(artifacts), artifacts)
	}
	if artifacts[0].Name != "linux-build" || artifacts[1].Name != "darwin-build" {
		t.Fatalf("unexpected artifacts: %+v", artifacts)
	}
}

func TestFetchRepositoryLabelsPaginatesAndSanitizes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/repos/acme/api/labels" {
			t.Fatalf("path = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q", got)
		}
		switch r.URL.Query().Get("page") {
		case "1":
			w.Header().Set("Link", `<https://api.github.test/repos/acme/api/labels?page=2>; rel="next"`)
			_, _ = fmt.Fprint(w, `[{"id":1,"node_id":"label-node-1","name":"bug","color":"D73A4A","description":"Bug reports","default":true}]`)
		case "2":
			_, _ = fmt.Fprint(w, `[{"id":2,"node_id":"label-node-2","name":"release","color":"not-a-color","description":"Release work","default":false}]`)
		case "3":
			_, _ = fmt.Fprint(w, `[]`)
		default:
			t.Fatalf("unexpected page %q", r.URL.Query().Get("page"))
		}
	}))
	defer server.Close()

	syncer := &GitHubActionsSyncer{HTTPClient: server.Client(), APIBase: server.URL}
	labels, err := syncer.fetchRepositoryLabels(context.Background(), "acme", "api", "test-token")
	if err != nil {
		t.Fatalf("fetchRepositoryLabels returned error: %v", err)
	}
	if len(labels) != 2 {
		t.Fatalf("len(labels) = %d, want 2: %+v", len(labels), labels)
	}
	if labels[0].Name != "bug" || labels[0].Color != "d73a4a" || !labels[0].IsDefault {
		t.Fatalf("unexpected first label: %+v", labels[0])
	}
	if labels[1].Name != "release" || labels[1].Color != "not-a-color" || labels[1].Description != "Release work" {
		t.Fatalf("unexpected second label: %+v", labels[1])
	}
}

func TestFetchRepositoryLabelsErrorDoesNotReturnResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Authorization: Bearer leaked-token"}`, http.StatusBadGateway)
	}))
	defer server.Close()

	syncer := &GitHubActionsSyncer{HTTPClient: server.Client(), APIBase: server.URL}
	_, err := syncer.fetchRepositoryLabels(context.Background(), "acme", "api", "test-token")
	if err == nil {
		t.Fatal("expected fetchRepositoryLabels to return error")
	}
	if strings.Contains(err.Error(), "Bearer") || strings.Contains(err.Error(), "leaked-token") || strings.Contains(err.Error(), "Authorization") {
		t.Fatalf("error leaked response body: %v", err)
	}
}

func TestFetchWorkflowRunsKeepsRunWhenArtifactsFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch got := r.URL.Path; got {
		case "/repos/acme/api/actions/runs":
			_, _ = fmt.Fprint(w, `{
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
			}`)
		case "/repos/acme/api/actions/runs/123/artifacts":
			http.Error(w, "temporarily unavailable", http.StatusInternalServerError)
		default:
			t.Fatalf("path = %q", got)
		}
	}))
	defer server.Close()

	syncer := &GitHubActionsSyncer{HTTPClient: server.Client(), APIBase: server.URL}
	runs, err := syncer.fetchWorkflowRuns(context.Background(), "acme", "api", "main", 10, "")
	if err != nil {
		t.Fatalf("fetchWorkflowRuns returned error: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("len(runs) = %d, want 1", len(runs))
	}
	if len(runs[0].Artifacts) != 0 {
		t.Fatalf("artifacts = %+v, want empty", runs[0].Artifacts)
	}
	if runs[0].Metadata["artifact_sync_status"] != "unavailable" {
		t.Fatalf("metadata = %+v, want unavailable artifact sync status", runs[0].Metadata)
	}
}

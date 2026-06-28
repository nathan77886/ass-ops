package app

import (
	"fmt"
	"strings"
	"testing"
)

func TestTemplateRemoteItemsAndRemoteIDByKey(t *testing.T) {
	defaults := map[string]any{
		"remotes": []any{
			map[string]any{"remote_key": "gitea", "name": "Gitea origin"},
			map[string]any{"name": "github"},
			map[string]any{"provider_type": "ignored"},
		},
	}
	items := templateRemoteItems(defaults, nil)
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	remotes := []map[string]any{
		{"id": "remote-1", "remote_key": "gitea", "name": "Gitea origin"},
		{"id": "remote-2", "remote_key": "github", "name": "GitHub mirror"},
	}
	if got := remoteIDByKey(remotes, "gitea"); got != "remote-1" {
		t.Fatalf("remoteIDByKey(gitea) = %q, want remote-1", got)
	}
	if got := remoteIDByKey(remotes, "GitHub mirror"); got != "remote-2" {
		t.Fatalf("remoteIDByKey(name) = %q, want remote-2", got)
	}
}

func TestTemplateFileItemsAndSafePath(t *testing.T) {
	defaults := map[string]any{
		"files": []any{
			map[string]any{"path": "README.md"},
			map[string]any{"path": "docs/ASSOPS_CONTEXT.md"},
			map[string]any{"path": "../secret"},
			map[string]any{"content": "missing path"},
		},
	}
	items := templateFileItems(defaults, nil)
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if got := safeTemplateFilePath("/docs/README.md"); got != "docs/README.md" {
		t.Fatalf("safeTemplateFilePath = %q, want docs/README.md", got)
	}
	for _, path := range []string{"", ".", "../secret", "docs/../secret", "docs//file"} {
		if got := safeTemplateFilePath(path); got != "" {
			t.Fatalf("safeTemplateFilePath(%q) = %q, want empty", path, got)
		}
	}
}

func TestRenderTemplateFileContentAndTemplateFileSummaries(t *testing.T) {
	run := map[string]any{"template_slug": "go-service-basic"}
	project := map[string]any{"name": "Billing", "slug": "billing"}
	repo := map[string]any{"repo_key": "billing-service"}
	got := renderTemplateFileContent("{{project_name}}/{{project_slug}}/{{template_slug}}/{{repository_key}}", run, project, repo)
	want := "Billing/billing/go-service-basic/billing-service"
	if got != want {
		t.Fatalf("renderTemplateFileContent = %q, want %q", got, want)
	}
	ids := mapTemplateFileIDs([]map[string]any{{"id": "file-1"}, {"id": "<nil>"}, {"id": ""}, {"id": "file-2"}})
	assertStringSlice(t, ids, []string{"file-1", "file-2"})
	summaries := templateFileSummaries([]map[string]any{{"id": "file-1", "path": "README.md", "kind": "markdown", "status": "planned", "content": "secret"}})
	if _, ok := summaries[0]["content"]; ok {
		t.Fatal("templateFileSummaries should not include content")
	}
}

func TestCompleteTemplateStepsMarksFilesCompleted(t *testing.T) {
	steps := completeTemplateSteps(
		[]any{map[string]any{"key": "files", "title": "Plan files"}},
		map[string]any{"id": "project-1"},
		map[string]any{"id": "repo-1"},
		nil,
		nil,
		[]map[string]any{{"id": "file-1"}},
	)
	if len(steps) != 1 {
		t.Fatalf("len(steps) = %d, want 1", len(steps))
	}
	if steps[0]["status"] != "completed" {
		t.Fatalf("files step status = %v, want completed", steps[0]["status"])
	}
	got, ok := steps[0]["template_file_ids"].([]string)
	if !ok {
		t.Fatalf("template_file_ids = %#v, want []string", steps[0]["template_file_ids"])
	}
	assertStringSlice(t, got, []string{"file-1"})
}

func TestTemplateStepsWithProvisionRetryAndFailureOnlyTouchProvisionSteps(t *testing.T) {
	input := []any{
		map[string]any{"key": "project", "status": "completed"},
		map[string]any{"key": "repository", "status": "completed", "error": "old"},
		map[string]any{"key": "remotes", "status": "completed"},
		map[string]any{"key": "files", "status": "failed"},
	}
	retrying := templateStepsWithProvisionRetry(input)
	if retrying[0]["status"] != "completed" || retrying[2]["status"] != "completed" {
		t.Fatalf("non-provision steps changed: %#v", retrying)
	}
	if retrying[1]["status"] != "provisioning" || retrying[3]["status"] != "provisioning" {
		t.Fatalf("provision steps not marked provisioning: %#v", retrying)
	}
	if _, ok := retrying[1]["error"]; ok {
		t.Fatalf("retry should clear old repository error: %#v", retrying[1])
	}
	failed := templateStepsWithProvisionFailure(retrying)
	if failed[0]["status"] != "completed" || failed[2]["status"] != "completed" {
		t.Fatalf("non-provision steps changed after failure: %#v", failed)
	}
	if failed[1]["status"] != "failed" || failed[3]["status"] != "failed" {
		t.Fatalf("provision steps not marked failed: %#v", failed)
	}
}

func TestIsSafeGitRefPart(t *testing.T) {
	for _, ref := range []string{"main", "release/2026.06", "v1.0.0"} {
		if !isSafeGitRefPart(ref) {
			t.Fatalf("expected %q to be safe", ref)
		}
	}
	for _, ref := range []string{"", "-main", "../main", "refs/heads/main.lock", "main;rm -rf"} {
		if isSafeGitRefPart(ref) {
			t.Fatalf("expected %q to be unsafe", ref)
		}
	}
}

func TestIsFullHexSHA(t *testing.T) {
	if !isFullHexSHA("0123456789abcdef0123456789abcdef01234567") {
		t.Fatal("expected 40-character hex SHA to be accepted")
	}
	if !isFullHexSHA("0123456789abcdef0123456789abcdef012345670123456789abcdef01234567") {
		t.Fatal("expected 64-character hex SHA to be accepted")
	}
	for _, value := range []string{"HEAD", "refs/heads/main", "01234", "xyz3456789abcdef0123456789abcdef01234567"} {
		if isFullHexSHA(value) {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}

func TestSanitizeGitOutput(t *testing.T) {
	got := sanitizeGitOutput("fatal: could not read from https://token@example.com/org/repo.git and git@example.com:org/repo.git and git://example.com/org/repo.git")
	want := "fatal: could not read from <remote> and <remote> and <remote>"
	if got != want {
		t.Fatalf("sanitizeGitOutput = %q, want %q", got, want)
	}
}

func TestLookupTagSanitizesGitError(t *testing.T) {
	err := fmt.Errorf("fatal: authentication failed for https://token:secret@example.com/org/repo.git")
	got := sanitizeLookupError(err)
	for _, leak := range []string{"token", "secret", "example.com/org/repo.git", "https://"} {
		if strings.Contains(got, leak) {
			t.Fatalf("sanitizeLookupError leaked %q in %q", leak, got)
		}
	}
	if !strings.Contains(got, "<remote>") {
		t.Fatalf("sanitizeLookupError should preserve redacted remote marker: %q", got)
	}
}

func assertStringSlice(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item %d = %q, want %q", i, got[i], want[i])
		}
	}
}

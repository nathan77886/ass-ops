package app

import (
	"fmt"
	"strings"
	"testing"
)

func statusByKind(items []map[string]any, kind string) string {
	for _, item := range items {
		if fmt.Sprint(item["kind"]) == kind {
			return fmt.Sprint(item["status"])
		}
	}
	return ""
}

func TestAssetGraphViewFiltersAreSanitized(t *testing.T) {
	filters, err := sanitizeAssetGraphViewFilters(map[string]any{
		"project_id":        " project-1 ",
		"asset_type":        " repository ",
		"q":                 " checkout ",
		"selected_asset_id": "repository:repo-1",
		"unexpected":        "ignored",
		"bad_type":          []string{"ignored"},
	})
	if err != nil {
		t.Fatalf("sanitizeAssetGraphViewFilters returned error: %v", err)
	}
	want := map[string]string{
		"project_id":        "project-1",
		"asset_type":        "repository",
		"q":                 "checkout",
		"selected_asset_id": "repository:repo-1",
	}
	for key, value := range want {
		if filters[key] != value {
			t.Fatalf("filters[%s] = %v, want %s", key, filters[key], value)
		}
	}
	if _, ok := filters["unexpected"]; ok {
		t.Fatal("unexpected filter key should be dropped")
	}
}

func TestWorkerQueueSummarySQLIncludesVisibilityAndRiskMetrics(t *testing.T) {
	t.Skip("worker queue summary now uses GORM models and Go aggregation; replace SQL-shape assertion with GORM fixture coverage")
}

func TestWorkerQueueBackendSummaryDocumentsLocalDirectMode(t *testing.T) {
	summary := workerQueueBackendSummary()
	for key, want := range map[string]string{
		"backend":          "postgres_local_direct",
		"claiming":         "select_for_update_skip_locked",
		"pubsub":           "cloudflare_queues",
		"log_fanout":       "sse_polling",
		"websocket_fanout": "deferred",
	} {
		if got, _ := summary[key].(string); got != want {
			t.Fatalf("workerQueueBackendSummary[%s] = %q, want %q", key, got, want)
		}
	}
	if summary["pubsub_enabled"] != true {
		t.Fatalf("workerQueueBackendSummary should enable pubsub: %#v", summary)
	}
	activeComponents := stringSliceFromAny(summary["active_components"])
	if len(activeComponents) != 4 {
		t.Fatalf("workerQueueBackendSummary active_components length = %d: %#v", len(activeComponents), activeComponents)
	}
	for _, component := range []string{"gateway_local_worker", "postgres_row_lock_claiming", "cloudflare_queues_remote_workers", "sse_polling_log_fanout"} {
		if !containsString(activeComponents, component) {
			t.Fatalf("workerQueueBackendSummary active_components missing %q: %#v", component, activeComponents)
		}
	}
	deferredBackends := stringSliceFromAny(summary["deferred_backends"])
	if len(deferredBackends) != 1 {
		t.Fatalf("workerQueueBackendSummary deferred_backends length = %d: %#v", len(deferredBackends), deferredBackends)
	}
	for _, backend := range []string{"websocket_fanout"} {
		if !containsString(deferredBackends, backend) {
			t.Fatalf("workerQueueBackendSummary deferred_backends missing %q: %#v", backend, deferredBackends)
		}
	}
	message, _ := summary["message"].(string)
	if !strings.Contains(message, "Gateway") || !strings.Contains(message, "Cloudflare Queues") {
		t.Fatalf("workerQueueBackendSummary message should document local/queue mode: %q", message)
	}
}

func TestContextFileModesArePrivate(t *testing.T) {
	if contextDirMode != 0o750 {
		t.Fatalf("contextDirMode = %#o, want 0750", contextDirMode)
	}
	if contextFileMode != 0o600 {
		t.Fatalf("contextFileMode = %#o, want 0600", contextFileMode)
	}
}

func TestTemplateRunStepsFallbackIncludesRepositoryAndRepoSync(t *testing.T) {
	for _, input := range []any{nil, []any{}} {
		steps := templateRunSteps(input)
		if len(steps) != 5 {
			t.Fatalf("len(steps) = %d, want 5", len(steps))
		}
		want := []string{"project", "repository", "remotes", "repo_sync", "files"}
		for i, key := range want {
			if steps[i]["key"] != key {
				t.Fatalf("step %d key = %v, want %s", i, steps[i]["key"], key)
			}
			if steps[i]["status"] != "queued" {
				t.Fatalf("step %d status = %v, want queued", i, steps[i]["status"])
			}
		}
	}
}

func TestTemplateRunStepsPreservesCustomStepsAndDefaultsStatus(t *testing.T) {
	steps := templateRunSteps([]any{
		map[string]any{"key": "project", "title": "Create project"},
		map[string]any{"key": "repository", "title": "Create repository", "status": "planned"},
	})
	if steps[0]["status"] != "queued" {
		t.Fatalf("first status = %v, want queued", steps[0]["status"])
	}
	if steps[1]["status"] != "planned" {
		t.Fatalf("second status = %v, want planned", steps[1]["status"])
	}
}

func TestProjectTemplatePreviewDerivesRepositoryAndRepoSync(t *testing.T) {
	template := map[string]any{
		"id":      "template-1",
		"slug":    "go-service-basic",
		"name":    "Go Service Basic",
		"version": "0.1.0",
		"status":  "active",
		"defaults": map[string]any{
			"repo_role":      "service",
			"default_branch": "main",
			"repository": map[string]any{
				"name_suffix":         "service",
				"repo_key_suffix":     "service",
				"display_name_suffix": "Service",
			},
			"repo_sync": map[string]any{
				"name":         "default mirror",
				"trigger_mode": "manual",
				"sync_mode":    "selected_refs",
				"transport":    "ssh",
				"driver":       "projectops_worker_git_ssh",
				"enabled":      false,
			},
		},
	}
	preview := projectTemplatePreview(template, "Billing", "billing", "payments service", nil)
	repo := mapFromAny(preview["repository"])
	if repo["repo_key"] != "billing-service" {
		t.Fatalf("repo_key = %v, want billing-service", repo["repo_key"])
	}
	if repo["display_name"] != "Billing Service" {
		t.Fatalf("display_name = %v, want Billing Service", repo["display_name"])
	}
	sync := mapFromAny(preview["repo_sync"])
	if sync["status"] != "planned" {
		t.Fatalf("repo_sync status = %v, want planned", sync["status"])
	}
	if sync["enabled"] != false {
		t.Fatalf("repo_sync enabled = %v, want false", sync["enabled"])
	}
}

func TestProjectTemplatePreviewHonorsParameters(t *testing.T) {
	template := map[string]any{
		"defaults": map[string]any{
			"repo_role":      "service",
			"default_branch": "main",
			"repository": map[string]any{
				"name_suffix":         "service",
				"repo_key_suffix":     "service",
				"display_name_suffix": "Service",
			},
			"repo_sync": map[string]any{
				"name":         "default mirror",
				"trigger_mode": "manual",
				"sync_mode":    "selected_refs",
				"transport":    "ssh",
				"driver":       "projectops_worker_git_ssh",
				"enabled":      false,
			},
		},
	}
	parameters := map[string]any{
		"repository": map[string]any{
			"repo_key":       "billing-api",
			"display_name":   "Billing API",
			"default_branch": "develop",
		},
		"repo_sync": map[string]any{
			"enabled":          true,
			"source_remote_id": "source-remote",
			"target_remote_id": "target-remote",
		},
	}
	preview := projectTemplatePreview(template, "Billing", "billing", "", parameters)
	repo := mapFromAny(preview["repository"])
	if repo["repo_key"] != "billing-api" {
		t.Fatalf("repo_key = %v, want billing-api", repo["repo_key"])
	}
	if repo["default_branch"] != "develop" {
		t.Fatalf("default_branch = %v, want develop", repo["default_branch"])
	}
	sync := mapFromAny(preview["repo_sync"])
	if sync["status"] != "ready_for_remote_validation" {
		t.Fatalf("repo_sync status = %v, want ready_for_remote_validation", sync["status"])
	}
	if sync["enabled"] != true {
		t.Fatalf("repo_sync enabled = %v, want true", sync["enabled"])
	}
}

func TestProjectTemplatePreviewUsesRemoteKeysFromTemplateDefaults(t *testing.T) {
	template := map[string]any{
		"defaults": map[string]any{
			"repository": map[string]any{"repo_key_suffix": "service"},
			"remotes": []any{
				map[string]any{"remote_key": "gitea", "name": "Gitea origin", "provider_type": "gitea", "remote_role": "source"},
				map[string]any{"remote_key": "github", "name": "GitHub mirror", "provider_type": "github", "remote_role": "mirror"},
			},
			"repo_sync": map[string]any{
				"source_remote_key": "gitea",
				"target_remote_key": "github",
			},
			"files": []any{
				map[string]any{"path": "README.md", "kind": "markdown", "content": "# {{project_name}}\nRepo: {{repository_key}}\n"},
				map[string]any{"path": "../secret", "content": "ignored"},
			},
		},
		"slug": "go-service-basic",
	}
	preview := projectTemplatePreview(template, "Billing", "billing", "", nil)
	remotes, ok := preview["remotes"].([]map[string]any)
	if !ok || len(remotes) != 2 {
		t.Fatalf("remotes = %#v, want two preview remotes", preview["remotes"])
	}
	sync := mapFromAny(preview["repo_sync"])
	if sync["source_remote_id"] != "remote_key:gitea" {
		t.Fatalf("source_remote_id = %v, want remote_key:gitea", sync["source_remote_id"])
	}
	if sync["target_remote_id"] != "remote_key:github" {
		t.Fatalf("target_remote_id = %v, want remote_key:github", sync["target_remote_id"])
	}
	if sync["status"] != "ready_for_remote_validation" {
		t.Fatalf("repo_sync status = %v, want ready_for_remote_validation", sync["status"])
	}
	files, ok := preview["files"].([]map[string]any)
	if !ok || len(files) != 1 {
		t.Fatalf("files = %#v, want one safe preview file", preview["files"])
	}
	if files[0]["path"] != "README.md" {
		t.Fatalf("file path = %v, want README.md", files[0]["path"])
	}
	if files[0]["content"] != "# Billing\nRepo: billing-service\n" {
		t.Fatalf("file content = %q", files[0]["content"])
	}
}

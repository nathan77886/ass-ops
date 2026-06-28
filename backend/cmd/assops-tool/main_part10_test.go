package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCountContextGenerationEvidence(t *testing.T) {
	assets := []map[string]any{
		{"asset_type": "agent_tool_call", "status": "queued", "metadata": map[string]any{"tool_name": "context.generate"}},
		{"asset_type": "agent_tool_call", "status": "completed", "metadata": map[string]any{"tool_name": "context.generate"}},
		{"asset_type": "agent_tool_call", "status": "running", "metadata": map[string]any{"tool_name": "context.generate"}},
		{"asset_type": "agent_tool_call", "status": "failed", "metadata": map[string]any{"tool_name": "context.generate"}},
		{"asset_type": "agent_tool_call", "status": "completed", "metadata": map[string]any{"tool_name": "plan.review"}},
	}
	if got := countContextGenerationEvidence(assets); got != 1 {
		t.Fatalf("countContextGenerationEvidence = %d, want 1", got)
	}
}

func TestCountContextGraphLinks(t *testing.T) {
	assets := []map[string]any{
		{"asset_type": "agent_task", "source_id": "10"},
		{"asset_type": "ai_runtime", "source_id": "30"},
		{"asset_type": "agent_tool_call", "id": "agent_tool_call:20", "status": "completed", "metadata": map[string]any{"tool_name": "context.generate"}},
		{"asset_type": "agent_tool_call", "id": "agent_tool_call:21", "status": "failed", "metadata": map[string]any{"tool_name": "context.generate"}},
		{"asset_type": "agent_tool_call", "source_id": "22", "status": "queued", "metadata": map[string]any{"tool_name": "context.generate"}},
	}
	graph := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "agent_task:10", "to_asset_id": "ai_runtime:30", "relation_type": "uses_runtime"},
			map[string]any{"from_asset_id": "agent_task:10", "to_asset_id": "agent_tool_call:20", "relation_type": "records_tool_call"},
			map[string]any{"from_asset_id": "agent_task:11", "to_asset_id": "agent_tool_call:21", "relation_type": "records_tool_call"},
			map[string]any{"from_asset_id": "agent_task:12", "to_asset_id": "agent_tool_call:22", "relation_type": "records_tool_call"},
			map[string]any{"from_asset_id": "operation_run:1", "to_asset_id": "agent_tool_call:20", "relation_type": "records_tool_call"},
		},
	}
	got := countContextGraphLinks(assets, graph)
	if got.TaskRuntimes != 1 || got.TaskContextToolCalls != 1 || got.CompleteContextTasks != 1 || got.CompleteContextTaskAssets != 1 {
		t.Fatalf("countContextGraphLinks = %#v, want one runtime, one completed context tool link, one complete context task, and one context asset task", got)
	}

	graphOnlyComplete := map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "agent_task:10", "to_asset_id": "ai_runtime:30", "relation_type": "uses_runtime"},
			map[string]any{"from_asset_id": "agent_task:10", "to_asset_id": "agent_tool_call:20", "relation_type": "records_tool_call"},
			map[string]any{"from_asset_id": "agent_task:99", "to_asset_id": "ai_runtime:30", "relation_type": "uses_runtime"},
			map[string]any{"from_asset_id": "agent_task:99", "to_asset_id": "agent_tool_call:22", "relation_type": "records_tool_call"},
		},
	}
	got = countContextGraphLinks(assets, graphOnlyComplete)
	if got.CompleteContextTasks != 1 || got.CompleteContextTaskAssets != 1 {
		t.Fatalf("countContextGraphLinks with graph-only queued task = %#v, want only the completed context task", got)
	}

	got = countContextGraphLinks(assets, map[string]any{"edges": []any{}})
	if got.TaskRuntimes != 0 || got.TaskContextToolCalls != 0 || got.CompleteContextTasks != 0 || got.CompleteContextTaskAssets != 0 {
		t.Fatalf("countContextGraphLinks with empty graph = %#v, want zero counts", got)
	}

	withoutContextGeneration := countContextGraphLinks([]map[string]any{
		{"asset_type": "agent_task", "source_id": "10"},
		{"asset_type": "ai_runtime", "source_id": "30"},
		{"asset_type": "agent_tool_call", "id": "agent_tool_call:20", "status": "completed", "metadata": map[string]any{"tool_name": "plan.review"}},
	}, map[string]any{
		"edges": []any{
			map[string]any{"from_asset_id": "agent_task:10", "to_asset_id": "ai_runtime:30", "relation_type": "uses_runtime"},
			map[string]any{"from_asset_id": "agent_task:10", "to_asset_id": "agent_tool_call:20", "relation_type": "records_tool_call"},
		},
	})
	if withoutContextGeneration.TaskRuntimes != 1 || withoutContextGeneration.TaskContextToolCalls != 0 || withoutContextGeneration.CompleteContextTasks != 0 || withoutContextGeneration.CompleteContextTaskAssets != 0 {
		t.Fatalf("countContextGraphLinks without context.generate = %#v, want runtime only", withoutContextGeneration)
	}
}

func TestHasAnyKnownID(t *testing.T) {
	cases := []struct {
		name     string
		ids      map[string]bool
		knownIDs map[string]bool
		want     bool
	}{
		{name: "nil ids", ids: nil, knownIDs: map[string]bool{"a": true}, want: false},
		{name: "nil known ids", ids: map[string]bool{"a": true}, knownIDs: nil, want: false},
		{name: "empty ids", ids: map[string]bool{}, knownIDs: map[string]bool{"a": true}, want: false},
		{name: "disjoint", ids: map[string]bool{"a": true}, knownIDs: map[string]bool{"b": true}, want: false},
		{name: "overlap", ids: map[string]bool{"a": true, "b": true}, knownIDs: map[string]bool{"b": true}, want: true},
		{name: "contained", ids: map[string]bool{"a": true}, knownIDs: map[string]bool{"a": true, "b": true}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasAnyKnownID(tc.ids, tc.knownIDs); got != tc.want {
				t.Fatalf("hasAnyKnownID() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAssetIDsByTypeMetadataMatchesProviderCaseInsensitively(t *testing.T) {
	rows := []map[string]any{
		{"asset_type": "webhook_connection", "source_id": "1", "metadata": map[string]any{"provider": "Gitea"}},
		{"asset_type": "webhook_connection", "source_id": "2", "metadata": map[string]any{"provider": "GITEA"}},
		{"asset_type": "webhook_connection", "source_id": "3", "metadata": map[string]any{"provider": "github"}},
		{"asset_type": "webhook_connection", "source_id": "4"},
		{"asset_type": "webhook_event", "source_id": "5", "metadata": map[string]any{"provider": "gitea"}},
	}

	got := assetIDsByTypeMetadata(rows, "webhook_connection", "provider", "gitea")
	if len(got) != 2 || !got["webhook_connection:1"] || !got["webhook_connection:2"] {
		t.Fatalf("assetIDsByTypeMetadata = %#v, want mixed-case Gitea connection ids only", got)
	}
	if count := countAPITypeMetadata(rows, "webhook_connection", "provider", "gitea"); count != 2 {
		t.Fatalf("countAPITypeMetadata = %d, want 2 mixed-case Gitea connections", count)
	}
}

func TestAssetGraphPayloadAvailableRequiresNodesOrEdgesKey(t *testing.T) {
	cases := []struct {
		name  string
		graph map[string]any
		want  bool
	}{
		{name: "nil", graph: nil, want: false},
		{name: "empty", graph: map[string]any{}, want: false},
		{name: "nodes", graph: map[string]any{"nodes": []any{}}, want: true},
		{name: "edges", graph: map[string]any{"edges": []any{}}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := assetGraphPayloadAvailable(tc.graph); got != tc.want {
				t.Fatalf("assetGraphPayloadAvailable() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCountOperationRowsWithLogsHandlesMissingLogCount(t *testing.T) {
	rows := []map[string]any{
		{"operation_type": "repo.sync"},
		{"id": "op-1", "operation_type": "ssh.exec", "log_count": 1},
	}
	operationAssetIDs := map[string]bool{"operation_run:op-1": true}
	if got := countOperationRowsWithLogs(rows, operationAssetIDs); got != 1 {
		t.Fatalf("countOperationRowsWithLogs with missing log_count = %d, want 1", got)
	}
}

func TestCountOperationRowsWithLogsRequiresMatchingCanonicalAsset(t *testing.T) {
	rows := []map[string]any{
		{"id": "op-1", "operation_type": "repo.sync", "log_count": 2},
		{"asset_id": "operation_run:op-2", "operation_type": "ssh.exec", "log_count": 1},
		{"id": "op-3", "operation_type": "argo.apps.sync", "log_count": 4},
	}
	operationAssetIDs := map[string]bool{
		"operation_run:op-1": true,
		"operation_run:op-2": true,
	}
	if got := countOperationRowsWithLogs(rows, operationAssetIDs); got != 2 {
		t.Fatalf("countOperationRowsWithLogs with mixed canonical assets = %d, want 2", got)
	}
}

func TestAssetIDsByTypeUsesCanonicalOperationSourceID(t *testing.T) {
	rows := []map[string]any{
		{"id": "asset-row-1", "asset_type": "operation_run", "source_id": "op-1"},
		{"id": "op-2", "asset_type": "operation_run"},
		{"asset_id": "operation_run:op-3", "asset_type": "operation_run"},
		{"asset_id": "<nil>", "asset_type": "operation_run", "source_id": "<nil>"},
		{"id": "asset-row-4", "asset_type": "repository", "source_id": "repo-1"},
	}
	got := assetIDsByType(rows, "operation_run")
	for _, want := range []string{"operation_run:op-1", "operation_run:op-2", "operation_run:op-3"} {
		if !got[want] {
			t.Fatalf("assetIDsByType missing %q from %#v", want, got)
		}
	}
	if got["operation_run:asset-row-1"] || got["operation_run:<nil>"] || got["repository:repo-1"] {
		t.Fatalf("assetIDsByType included non-canonical ids: %#v", got)
	}
}

func TestOperationIDsByTypePrefersOperationID(t *testing.T) {
	rows := []map[string]any{
		{"id": "op-1", "asset_id": "operation_run:wrong", "operation_type": "argo.apps.sync"},
		{"asset_id": "operation_run:op-2", "operation_type": "argo.apps.sync"},
		{"id": "op-3", "operation_type": "repo.sync"},
		{"operation_type": "argo.apps.sync"},
	}
	got := operationIDsByType(rows, "argo.apps.sync")
	for _, want := range []string{"operation_run:op-1", "operation_run:op-2"} {
		if !got[want] {
			t.Fatalf("operationIDsByType missing %q from %#v", want, got)
		}
	}
	if got["operation_run:wrong"] || got["operation_run:op-3"] || got[""] {
		t.Fatalf("operationIDsByType included wrong ids: %#v", got)
	}
}

func TestReleasePromotionPlanIncludesVerificationAndRollout(t *testing.T) {
	artifactDir := t.TempDir()
	files := map[string]string{
		"assops-v0.1.0-linux-amd64.tar.gz": "binary",
		"assops-web-v0.1.0.tar.gz":         "web",
		"assops-0.1.0.tgz":                 "helm",
	}
	writeSHA256SUMS(t, artifactDir, files)
	reportPath := writeValidRehearsalReport(t, artifactDir, "postgres://assops@postgres:5432/assops_restore_test?sslmode=disable")
	valuesPath := filepath.Join(artifactDir, "helm-values.yaml")
	values, err := releaseHelmValues("Nathan77886", "v0.1.0")
	if err != nil {
		t.Fatalf("releaseHelmValues: %v", err)
	}
	if err := writeTextFile(valuesPath, values); err != nil {
		t.Fatalf("write values: %v", err)
	}

	plan, err := releasePromotionPlan("nathan77886/ass-ops", "Nathan77886", "v0.1.0", artifactDir, reportPath, valuesPath)
	if err != nil {
		t.Fatalf("releasePromotionPlan: %v", err)
	}
	for _, want := range []string{
		"# ASSOPS Promotion Plan v0.1.0",
		"Helm values sha256:",
		"assops-tool release validate-bundle",
		"gh attestation verify",
		"oci://ghcr.io/nathan77886/assops-gateway:v0.1.0",
		"reviewed environment values overlay",
		"## Rollout Guardrails",
		"preflight-only",
		"protected environment",
		"namespace-scoped kubeconfig",
		"rollback point",
		"operator approval",
		"deploy=true",
		"smoke_url",
		"smoke_via_port_forward=true",
		"ENVIRONMENT_VALUES=<reviewed-environment-values.yaml>",
		`-f "$ENVIRONMENT_VALUES"`,
		"helm template assops deploy/helm/assops",
		"helm upgrade --install assops deploy/helm/assops",
		"--wait-for-jobs",
		"scripts/api-smoke.sh",
		"kubectl -n assops port-forward svc/assops-web",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("promotion plan missing %q in:\n%s", want, plan)
		}
	}
}

func TestReleasePromotionPlanRejectsInvalidRepo(t *testing.T) {
	if _, err := releasePromotionPlan("owner", "owner", "v0.1.0", "missing", "missing", "missing"); err == nil {
		t.Fatal("expected invalid owner/repo to be rejected")
	}
}

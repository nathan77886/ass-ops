package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNonNilMap(t *testing.T) {
	if got := nonNilMap(nil); got == nil || len(got) != 0 {
		t.Fatalf("nonNilMap(nil) = %#v, want empty map", got)
	}
	input := map[string]any{"action": "ssh.exec"}
	if got := nonNilMap(input); got["action"] != "ssh.exec" {
		t.Fatalf("nonNilMap(input) = %#v", got)
	}
}

func TestCanRevokeOperationApprovalDelegation(t *testing.T) {
	server := &Server{}
	approval := map[string]any{"id": "approval-1", "required_approver_roles": []string{"security"}}
	delegation := map[string]any{"from_user_id": "delegator-1", "to_user_id": "delegate-1"}
	if !server.canRevokeOperationApprovalDelegation(context.Background(), &User{ID: "admin-1", Role: "admin"}, approval, delegation) {
		t.Fatal("admin should revoke delegation")
	}
	if !server.canRevokeOperationApprovalDelegation(context.Background(), &User{ID: "delegator-1", Role: "developer"}, approval, delegation) {
		t.Fatal("delegator should revoke delegation")
	}
	if !server.canRevokeOperationApprovalDelegation(context.Background(), &User{ID: "approver-1", Role: "security"}, approval, delegation) {
		t.Fatal("configured approver should revoke delegation")
	}
	if server.canRevokeOperationApprovalDelegation(context.Background(), nil, approval, delegation) {
		t.Fatal("nil user should not revoke delegation")
	}
	if server.canRevokeOperationApprovalDelegation(context.Background(), &User{ID: "delegate-1", Role: "developer"}, approval, delegation) {
		t.Fatal("delegated user should not revoke another user's delegation just because they can decide")
	}
	if server.canRevokeOperationApprovalDelegation(context.Background(), &User{ID: "other-1", Role: "developer"}, approval, delegation) {
		t.Fatal("unrelated developer should not revoke delegation")
	}
}

func TestDecodeOperationApprovalRuleRequestValidatesApprovalCount(t *testing.T) {
	body := strings.NewReader(`{"action":"ssh.exec","required_approver_roles":["admin"],"required_approval_count":2}`)
	req := httptest.NewRequest(http.MethodPost, "/api/operation-approval-rules", body)
	rr := httptest.NewRecorder()
	if _, ok := decodeOperationApprovalRuleRequest(rr, req, true); ok {
		t.Fatal("request should be rejected when approval count exceeds role count")
	}
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestRequiredApprovalCountDefaultsToOne(t *testing.T) {
	for _, input := range []any{nil, 0, -2, "0", "not-a-number"} {
		if got := requiredApprovalCount(input); got != 1 {
			t.Fatalf("requiredApprovalCount(%#v) = %d, want 1", input, got)
		}
	}
	if got := requiredApprovalCount(int64(3)); got != 3 {
		t.Fatalf("requiredApprovalCount(int64(3)) = %d, want 3", got)
	}
}

func TestOperationApprovalFiltersRejectInvalidTime(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/operation-approvals?until=not-a-time", nil)
	_, err := operationApprovalFiltersFromRequest(req)
	if err == nil || !strings.Contains(err.Error(), "until must be RFC3339") {
		t.Fatalf("error = %v, want RFC3339 error", err)
	}
}

func TestSanitizeOperationApprovalViewFilters(t *testing.T) {
	got, err := sanitizeOperationApprovalViewFilters(map[string]any{
		"status":        " pending ",
		"action":        " ssh.exec ",
		"resource_type": " ssh_machine ",
		"q":             " deploy ",
		"requested_by":  " ops@example.com ",
		"since":         "2026-01-01T00:00:00Z",
		"unknown":       "drop me",
		"until":         123,
	})
	if err != nil {
		t.Fatalf("sanitizeOperationApprovalViewFilters: %v", err)
	}
	want := map[string]any{
		"status":        "pending",
		"action":        "ssh.exec",
		"resource_type": "ssh_machine",
		"q":             "deploy",
		"requested_by":  "ops@example.com",
		"since":         "2026-01-01T00:00:00Z",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("filters = %#v, want %#v", got, want)
	}
}

func TestSanitizeOperationApprovalViewFiltersRejectsInvalidValues(t *testing.T) {
	if _, err := sanitizeOperationApprovalViewFilters(map[string]any{"status": "done"}); err == nil || !strings.Contains(err.Error(), "status is invalid") {
		t.Fatalf("status error = %v", err)
	}
	if _, err := sanitizeOperationApprovalViewFilters(map[string]any{"since": "yesterday"}); err == nil || !strings.Contains(err.Error(), "since must be RFC3339") {
		t.Fatalf("since error = %v", err)
	}
}

func TestCanRetryTemplateProvision(t *testing.T) {
	tests := []struct {
		name string
		run  map[string]any
		want bool
	}{
		{name: "failed unprovisioned", run: map[string]any{"project_id": "project-1", "status": "failed", "result": map[string]any{"repository_provisioned": false}}, want: true},
		{name: "completed unprovisioned", run: map[string]any{"project_id": "project-1", "status": "completed", "result": map[string]any{"repository_provisioned": false}}, want: true},
		{name: "already provisioned", run: map[string]any{"project_id": "project-1", "status": "failed", "result": map[string]any{"repository_provisioned": true}}, want: false},
		{name: "missing project", run: map[string]any{"status": "failed", "result": map[string]any{"repository_provisioned": false}}, want: false},
		{name: "running", run: map[string]any{"project_id": "project-1", "status": "running", "result": map[string]any{}}, want: false},
		{name: "queued", run: map[string]any{"project_id": "project-1", "status": "queued", "result": map[string]any{}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canRetryTemplateProvision(tt.run); got != tt.want {
				t.Fatalf("canRetryTemplateProvision = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLikeContainsEscapesWildcards(t *testing.T) {
	if got := likeContains("  deploy_%\\prod  "); got != `%deploy\_\%\\prod%` {
		t.Fatalf("likeContains = %q", got)
	}
	if got := likeContains(""); got != "" {
		t.Fatalf("empty likeContains = %q", got)
	}
}

func TestBoolQuery(t *testing.T) {
	if !boolQuery(httptest.NewRequest(http.MethodGet, "/?include_archived=yes", nil), "include_archived") {
		t.Fatal("include_archived=yes should be true")
	}
	if boolQuery(httptest.NewRequest(http.MethodGet, "/?include_archived=false", nil), "include_archived") {
		t.Fatal("include_archived=false should be false")
	}
}

func TestRepoSyncAssetArchived(t *testing.T) {
	tests := []struct {
		name  string
		asset map[string]any
		want  bool
	}{
		{name: "nil", asset: nil, want: false},
		{name: "empty", asset: map[string]any{"archived_at": ""}, want: false},
		{name: "null text", asset: map[string]any{"archived_at": "<nil>"}, want: false},
		{name: "timestamp", asset: map[string]any{"archived_at": "2026-01-01T00:00:00Z"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := repoSyncAssetArchived(tt.asset); got != tt.want {
				t.Fatalf("repoSyncAssetArchived = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAgentPlanContentUsesContextSnapshot(t *testing.T) {
	content := agentPlanContent(
		map[string]any{"title": "Review release", "prompt": "Summarize current state"},
		map[string]any{
			"created_at": "2026-01-01T00:00:00Z",
			"context_json": map[string]any{
				"project":      map[string]any{"name": "Billing", "slug": "billing"},
				"repositories": []any{map[string]any{"name": "api"}},
				"remotes":      []any{map[string]any{"name": "GitHub"}, map[string]any{"name": "Gitea"}},
				"operations":   []any{map[string]any{"status": "completed"}},
				"approvals":    []any{map[string]any{"status": "pending"}},
				"deployment_targets": []any{
					map[string]any{"name": "prod", "deployment_execution_readiness": map[string]any{"status": "planned"}},
				},
				"rollback_points": []any{
					map[string]any{"name": "prod@abc123", "rollback_readiness": "previewable"},
					map[string]any{"name": "prod@old", "rollback_readiness": "blocked"},
				},
				"ssh_machines":       []any{map[string]any{"name": "deploy-host"}},
				"github_action_runs": []any{map[string]any{"status": "completed"}},
				"asset_graph": map[string]any{
					"assets": []any{
						map[string]any{"asset_type": "repository", "name": "api"},
						map[string]any{"asset_type": "git_remote", "name": "origin"},
					},
					"relations": []any{
						map[string]any{"relation_type": "has_remote"},
					},
					"status_snapshots": []any{
						map[string]any{"health": "high", "status": "failed"},
						map[string]any{"health": "normal", "status": "active"},
					},
				},
			},
		},
	)
	for _, token := range []string{
		"Task: Review release",
		"Prompt: Summarize current state",
		"Project: Billing (`billing`)",
		"Repositories: 1",
		"Git remotes: 2",
		"Recent operations: 1",
		"Deployment targets: 1",
		"Deployment execution readiness: planned=1",
		"Rollback points: 2",
		"Rollback readiness: blocked=1, previewable=1",
		"Rollback execution: read_only_preview (1 previewable, 0 executable)",
		"SSH machines: 1",
		"GitHub Actions runs: 1",
		"Asset graph assets: 2",
		"Asset graph relations: 1",
		"Asset status snapshots: 2",
		"Asset types: git_remote=1, repository=1",
		"Asset health: high=1, normal=1",
		"Review canonical asset graph entries, status snapshots",
		"No code changes, deployments, SSH execution",
		"Deployment execution readiness is dry-run only",
		"Rollback execution is disabled in this first version",
		"Agent patch workflow is audit-only",
		"Codex CLI execution is still a redacted audit plan",
		"High-risk follow-up actions must use operation approvals",
	} {
		if !strings.Contains(content, token) {
			t.Fatalf("agentPlanContent missing %q in %s", token, content)
		}
	}
}

func TestAgentPlanContentHandlesDirectMapSlices(t *testing.T) {
	content := agentPlanContent(
		map[string]any{"title": "Review graph"},
		map[string]any{
			"context_json": map[string]any{
				"project":      map[string]any{"name": "Ops", "slug": "ops"},
				"repositories": []map[string]any{{"name": "api"}},
				"asset_graph": map[string]any{
					"assets": []map[string]any{
						{"asset_type": "project"},
						{"asset_type": "repository"},
						{"asset_type": "repository"},
					},
					"relations": []map[string]any{{"relation_type": "contains"}},
				},
			},
		},
	)
	for _, token := range []string{
		"Repositories: 1",
		"Asset graph assets: 3",
		"Asset graph relations: 1",
		"Asset types: project=1, repository=2",
	} {
		if !strings.Contains(content, token) {
			t.Fatalf("agentPlanContent missing %q in %s", token, content)
		}
	}
}

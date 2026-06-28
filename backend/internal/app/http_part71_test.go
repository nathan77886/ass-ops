package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAgentPlanContentHandlesEmptyAssetGraph(t *testing.T) {
	content := agentPlanContent(
		map[string]any{"title": "Review empty graph"},
		map[string]any{
			"context_json": map[string]any{
				"project": map[string]any{"name": "Ops", "slug": "ops"},
				"asset_graph": map[string]any{
					"assets":    []any{},
					"relations": []any{},
				},
			},
		},
	)
	for _, token := range []string{
		"Asset graph assets: 0",
		"Asset graph relations: 0",
		"Asset types: none",
	} {
		if !strings.Contains(content, token) {
			t.Fatalf("agentPlanContent missing %q in %s", token, content)
		}
	}
}

func TestFormatCountMap(t *testing.T) {
	rows := []map[string]any{
		{"asset_type": "repository"},
		{"asset_type": "git_remote"},
		{"asset_type": "repository"},
		{"asset_type": ""},
		{"asset_type": nil},
	}
	if got := formatCountMap(countByStringField(rows, "asset_type")); got != "git_remote=1, repository=2" {
		t.Fatalf("formatCountMap = %q", got)
	}
	if got := formatCountMap(countByStringField(nil, "asset_type")); got != "" {
		t.Fatalf("empty formatCountMap = %q", got)
	}
}

func TestSanitizeContextRowsMetadataRedactsSensitiveKeys(t *testing.T) {
	rows := []map[string]any{
		{
			"id": "rollback-1",
			"metadata": map[string]any{
				"source":       "argocd",
				"access_token": "secret",
				"nested": map[string]any{
					"secret": "nested-secret",
					"team":   "platform",
				},
			},
		},
	}
	sanitizeContextRowsMetadata(rows)
	metadata := mapFromAny(rows[0]["metadata"])
	if metadata["access_token"] != "<redacted>" {
		t.Fatalf("access_token = %v, want redacted", metadata["access_token"])
	}
	nested := mapFromAny(metadata["nested"])
	if nested["secret"] != "<redacted>" {
		t.Fatalf("nested secret = %v, want redacted", nested["secret"])
	}
	if metadata["source"] != "argocd" || nested["team"] != "platform" {
		t.Fatalf("non-sensitive metadata changed: %#v", metadata)
	}
}

func TestCanonicalAssetRefreshHooksAreWired(t *testing.T) {
	httpSource := []byte(readSourceGlob(t, "http_part*.go"))
	for _, reason := range []string{
		`could not sync project asset`,
		`could not sync git repository asset`,
		`s.projectByIDGorm(r.Context(), projectID)`,
		`s.gitRepositoryByIDGorm(r.Context(), repoID)`,
		`enqueueConfigRepositoryGitWorkflowGorm(r.Context(), tx, projectID, repo, remotes, preview, currentUser(r).ID)`,
		`could not sync git remote asset`,
		`s.projectIDForRepositoryGorm(r.Context(), chi.URLParam(r, "id"))`,
		`syncCanonicalAssetsGorm(r.Context(), tx)`,
		`could not sync provider account asset`,
		`s.refreshGitRemotesForProviderAccountGorm(r.Context(), input, chi.URLParam(r, "id"))`,
		`metadata["provider_check"] = check`,
		`s.refreshGitRemotesForProviderAccountGorm(r.Context(), next, accountID)`,
		`provider account changed during token rotation execution; retry`,
		`syncing canonical assets for webhook_connection.create`,
		`syncing canonical assets for webhook_connection.rotate_secret`,
		`syncing canonical assets for webhook event`,
		`failed to record webhook diagnostic event`,
		`syncCanonicalAssetsInGormTransaction(w, r, tx, "webhook_event.replay")`,
		`syncCanonicalAssetsInGormTransaction(w, r, tx, "webhook_event.github_workflow_run")`,
		`syncCanonicalAssetsInGormTransaction(w, r, tx, "webhook_event.gitea_push")`,
		`could not sync AI runtime asset`,
		`syncing canonical assets for agent task create`,
		`syncing canonical assets for agent plan generate`,
		`syncing canonical assets for agent plan approve`,
		`syncing canonical assets for agent task execute`,
		`syncing canonical assets for operation approval create`,
		`syncing canonical assets for approval rule create`,
		`syncing canonical assets for approval rule update`,
		`syncing canonical assets for operation_approval.reject`,
		`syncing canonical assets for operation_approval.delegation.create`,
		`syncing canonical assets for operation_approval.delegation.revoke`,
		`syncing canonical assets for provider_review_attempt.claim`,
		`syncing canonical assets for provider_review_attempt.local_result`,
		`syncing canonical assets for operation cancel`,
		`syncing canonical assets for expired operation approvals`,
		`could not sync canonical assets after approval notification`,
		`could not sync argo connection asset`,
		`could not sync ssh machine asset`,
		`syncCanonicalAssetsGorm(r.Context(), tx)`,
	} {
		if !strings.Contains(string(httpSource), reason) {
			t.Fatalf("http.go missing transactional canonical sync hook %q", reason)
		}
	}
	if got := strings.Count(string(httpSource), `errors.Is(err, ErrNotFound)`); got < 2 {
		t.Fatalf("repo sync asset update paths should preserve ErrNotFound -> 404 handling, found %d branches", got)
	}

	workerSource := []byte(readSourceGlob(t, "worker_part*.go"))
	for _, token := range []string{
		`refreshCanonicalAssetsAfterOperation(ctx, job, opID, "completed")`,
		`refreshCanonicalAssetsAfterOperation(ctx, job, opID, "failed")`,
		`canonicalAssetsSyncedInAdapterTransaction(job)`,
		`"repo.sync", "repo.sync_remote", "git.refs.refresh", "repo.tag", "repo.create_tag"`,
		`"github.actions.sync", "github.labels.sync", "agent.execute"`,
		`syncCanonicalAssetsGorm(ctx, tx)`,
		`syncing canonical assets for running repo sync`,
		`syncing canonical assets for completed repo sync`,
		`syncing canonical assets for failed repo sync`,
		`syncing canonical assets for running Git ref refresh`,
		`syncing canonical assets for completed Git ref refresh`,
		`syncing canonical assets for failed Git ref refresh`,
		`syncing canonical assets for completed repo tag`,
		`syncing canonical assets for stale worker recovery`,
		`failTimedOutRepoSyncGorm(ctx, tx, opID)`,
		`syncing canonical assets for GitHub Actions sync`,
		`syncing canonical assets for failed GitHub Actions sync`,
		`syncing canonical assets for failed GitHub Actions sync without remote`,
		`syncing canonical assets for running Argo app sync`,
		`syncing canonical assets for Argo app sync`,
		`syncing canonical assets for failed Argo app sync`,
		`"argo.apps.sync", "argo.pod_logs", "argo.pod_restart"`,
		`syncing canonical assets for running Argo pod log audit`,
		`syncing canonical assets for completed Argo pod log audit`,
		`syncing canonical assets for failed Argo pod log audit`,
		`syncing canonical assets for running Argo pod restart`,
		`syncing canonical assets for completed Argo pod restart`,
		`syncing canonical assets for failed Argo pod restart`,
		`"github.actions.sync", "github.labels.sync"`,
		`syncing canonical assets for running agent execution`,
		`syncing canonical assets for completed agent execution`,
		`syncing canonical assets for failed agent execution`,
		`syncing canonical assets for running SSH command`,
		`syncing canonical assets for completed SSH command`,
		`syncing canonical assets for failed SSH command`,
		`failTimedOutOperationGorm(ctx, tx, opID)`,
	} {
		if !strings.Contains(string(workerSource), token) {
			t.Fatalf("worker.go missing canonical asset refresh hook %q", token)
		}
	}
}

func TestAgentPlanStatusApproved(t *testing.T) {
	if !agentPlanStatusApproved("approved") {
		t.Fatal("approved status should be executable")
	}
	for _, status := range []any{"pending", "generated", "", nil} {
		if agentPlanStatusApproved(status) {
			t.Fatalf("status %v should not be executable", status)
		}
	}
}

func TestOperationLogCursorIDRoundTrip(t *testing.T) {
	createdAt := time.Date(2026, 6, 22, 10, 30, 45, 123, time.UTC)
	item := map[string]any{
		"id":         "log-1",
		"created_at": createdAt,
	}
	cursorID := operationLogCursorID(item)
	if !strings.Contains(cursorID, "|log-1") {
		t.Fatalf("operationLogCursorID = %q", cursorID)
	}
	cursor, ok := parseOperationLogCursorID(cursorID)
	if !ok {
		t.Fatalf("parseOperationLogCursorID(%q) failed", cursorID)
	}
	if cursor.CreatedAt != createdAt.Format(time.RFC3339Nano) || cursor.ID != "log-1" {
		t.Fatalf("cursor = %+v", cursor)
	}
}

func TestCancelOperationRunGuardsTerminalStateAndQueuedJobs(t *testing.T) {
	source := readSourceGlob(t, "http_part*.go")
	for _, token := range []string{
		"func (s *Server) cancelOperationRunGorm",
		"operationStreamTerminal(run.Status)",
		`Where(&GormWorkerJob{OperationRunID: validNullString(operationID), Status: "queued"})`,
		`jobs[i].Status = "canceled"`,
	} {
		if !strings.Contains(source, token) {
			t.Fatalf("cancelOperationRunGorm missing %q", token)
		}
	}
}

func TestOperationLogCursorFromRequestPrefersLastEventID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/operations/op-1/logs/stream?cursor=2026-06-22T10:00:00Z%7Cquery-log", nil)
	req.Header.Set("Last-Event-ID", "2026-06-22T11:00:00Z|header-log")
	cursor := operationLogCursorFromRequest(req)
	if cursor.CreatedAt != "2026-06-22T11:00:00Z" || cursor.ID != "header-log" {
		t.Fatalf("cursor = %+v", cursor)
	}
}

func TestOperationLogCursorFromRequestAcceptsQueryFallbacks(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/operations/op-1/logs/stream?last_event_id=2026-06-22T09:00:00Z%7Clast-event-query", nil)
	cursor := operationLogCursorFromRequest(req)
	if cursor.CreatedAt != "2026-06-22T09:00:00Z" || cursor.ID != "last-event-query" {
		t.Fatalf("last_event_id cursor = %+v", cursor)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/operations/op-1/logs/stream?cursor=2026-06-22T10:00:00Z%7Ccursor-query&last_event_id=2026-06-22T09:00:00Z%7Clast-event-query", nil)
	cursor = operationLogCursorFromRequest(req)
	if cursor.CreatedAt != "2026-06-22T10:00:00Z" || cursor.ID != "cursor-query" {
		t.Fatalf("cursor query should win over last_event_id query: %+v", cursor)
	}
}

func TestParseOperationLogCursorIDRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{"", "|", "created-only|", "|id-only", "missing-delimiter", "<nil>|<nil>", "2026-06-22T10:00:00Z|<nil>", "<nil>|log-1"} {
		if cursor, ok := parseOperationLogCursorID(input); ok {
			t.Fatalf("parseOperationLogCursorID(%q) = %+v, true; want false", input, cursor)
		}
	}
}

func TestOperationLogCursorIDSkipsInvalidItems(t *testing.T) {
	for _, item := range []map[string]any{
		{"id": "log-1"},
		{"id": "", "created_at": time.Now()},
		{"id": nil, "created_at": time.Now()},
	} {
		if got := operationLogCursorID(item); got != "" {
			t.Fatalf("operationLogCursorID(%v) = %q, want empty", item, got)
		}
	}
}

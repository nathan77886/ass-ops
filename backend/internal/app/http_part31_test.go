package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunProviderAccountCheckVerifiesTokenWithoutLeakingEnv(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN", "secret-token")
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Fatalf("path = %s, want /user", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"login":"assops-bot"}`))
	}))
	defer server.Close()

	check := runProviderAccountCheck(context.Background(), providerAccountConfig{
		ProviderType: "github",
		APIBaseURL:   server.URL,
		TokenEnv:     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN",
	}, server.Client())

	if check["status"] != "ok" || check["actor"] != "assops-bot" {
		t.Fatalf("check = %#v", check)
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	encoded, _ := json.Marshal(check)
	if strings.Contains(string(encoded), "secret-token") || strings.Contains(string(encoded), "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN") {
		t.Fatalf("provider check leaked token material: %s", encoded)
	}
}

func TestRunProviderAccountCheckMissingTokenDoesNotCallProvider(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	check := runProviderAccountCheck(context.Background(), providerAccountConfig{
		ProviderType: "gitea",
		APIBaseURL:   server.URL,
		TokenEnv:     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_MAIN",
	}, server.Client())

	if called {
		t.Fatal("provider should not be called when token env is unset")
	}
	if check["status"] != "error" || check["token_env_present"] != false {
		t.Fatalf("check = %#v", check)
	}
	if !strings.Contains(fmt.Sprint(check["message"]), "environment variable is not set") {
		t.Fatalf("message = %v", check["message"])
	}
}

func TestAssetDependencySQLDirectionColumns(t *testing.T) {
	t.Skip("asset dependency traversal now uses GORM models and Go BFS; replace SQL-shape assertion with GORM fixture coverage")
}

func TestAssetDependencySQLIncludesRecursiveWalk(t *testing.T) {
	t.Skip("asset dependency traversal now uses GORM models and Go BFS; replace SQL-shape assertion with GORM fixture coverage")
}

func TestOperationRunResultRedactsSSHOutput(t *testing.T) {
	result := operationRunResult(
		map[string]any{"tool_name": "ssh.exec"},
		map[string]any{
			"adapter":   true,
			"tool":      "ssh.exec",
			"stdout":    "secret output",
			"stderr":    "private error",
			"exit_code": 0,
		},
	)
	if _, ok := result["stdout"]; ok {
		t.Fatal("ssh stdout should not be copied to operation_runs.result")
	}
	if _, ok := result["stderr"]; ok {
		t.Fatal("ssh stderr should not be copied to operation_runs.result")
	}
	if result["exit_code"] != 0 {
		t.Fatalf("exit_code = %v, want 0", result["exit_code"])
	}
}

func TestSafeOperationForAuditOmitsInputAndResult(t *testing.T) {
	got := safeOperationForAudit(map[string]any{
		"id":             "op-1",
		"operation_type": "ssh.exec",
		"input":          map[string]any{"command": "secret command"},
		"result":         map[string]any{"stdout": "secret output"},
		"status":         "completed",
	})
	if _, ok := got["input"]; ok {
		t.Fatal("audit operation should not expose input")
	}
	if _, ok := got["result"]; ok {
		t.Fatal("audit operation should not expose result")
	}
	if got["operation_type"] != "ssh.exec" {
		t.Fatalf("operation_type = %v", got["operation_type"])
	}
}

func TestBearerTokenFromRequestAllowsQueryOnlyForLogStream(t *testing.T) {
	streamReq := httptest.NewRequest(http.MethodGet, "/api/operations/op-1/logs/stream?token=query-token", nil)
	if got := bearerTokenFromRequest(streamReq); got != "query-token" {
		t.Fatalf("stream query token = %q", got)
	}
	apiReq := httptest.NewRequest(http.MethodGet, "/api/operations?token=query-token", nil)
	if got := bearerTokenFromRequest(apiReq); got != "" {
		t.Fatalf("non-stream query token = %q, want empty", got)
	}
	headerReq := httptest.NewRequest(http.MethodGet, "/api/operations", nil)
	headerReq.Header.Set("Authorization", "Bearer header-token")
	if got := bearerTokenFromRequest(headerReq); got != "header-token" {
		t.Fatalf("header token = %q", got)
	}
}

func TestWriteSSEFormatsJSONEvent(t *testing.T) {
	var b strings.Builder
	if err := writeSSE(&b, "log", map[string]any{"message": "hello"}); err != nil {
		t.Fatalf("writeSSE: %v", err)
	}
	got := b.String()
	if !strings.HasPrefix(got, "event: log\n") {
		t.Fatalf("SSE missing event line: %q", got)
	}
	if !strings.Contains(got, `data: {"message":"hello"}`+"\n\n") {
		t.Fatalf("SSE missing JSON data: %q", got)
	}
}

func TestOperationStreamTerminalStatuses(t *testing.T) {
	for _, status := range []string{"completed", "failed", "canceled", "cancelled", " COMPLETED "} {
		if !operationStreamTerminal(status) {
			t.Fatalf("%q should be terminal", status)
		}
	}
	for _, status := range []string{"queued", "running", "pending", ""} {
		if operationStreamTerminal(status) {
			t.Fatalf("%q should not be terminal", status)
		}
	}
}

func TestOperationLogCursorTimeFormatsTime(t *testing.T) {
	timestamp := time.Date(2026, 6, 22, 12, 34, 56, 123456789, time.FixedZone("UTC+8", 8*60*60))
	got := operationLogCursorTime(timestamp)
	if got != "2026-06-22T04:34:56.123456789Z" {
		t.Fatalf("cursor time = %q", got)
	}
}

func TestOperationLogStreamShouldCloseOnlyAfterDrainingBatch(t *testing.T) {
	if operationLogStreamShouldClose("completed", 200, 200) {
		t.Fatal("terminal stream should not close on a full batch")
	}
	if !operationLogStreamShouldClose("completed", 199, 200) {
		t.Fatal("terminal stream should close after a partial batch")
	}
	if operationLogStreamShouldClose("running", 0, 200) {
		t.Fatal("non-terminal stream should stay open")
	}
}

func TestPostApprovalWebhookSendsSafePayload(t *testing.T) {
	var gotAuth string
	var gotPayload map[string]any
	previousClient := approvalWebhookHTTPClient
	approvalWebhookHTTPClient = &http.Client{Transport: approvalRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotAuth = r.Header.Get("Authorization")
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("content type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode webhook payload: %v", err)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	defer func() { approvalWebhookHTTPClient = previousClient }()

	server := &Server{cfg: Config{ApprovalWebhookURL: "https://93.184.216.34/approval", ApprovalWebhookToken: "token-123"}}
	err := server.postApprovalWebhook(context.Background(), map[string]any{
		"id":                       "approval-1",
		"project_id":               "project-1",
		"resource_type":            "ssh_machine",
		"resource_id":              "machine-1",
		"action":                   "ssh.exec",
		"title":                    "Run SSH command",
		"status":                   "pending",
		"required_approver_roles":  []string{"admin", "owner"},
		"required_approval_count":  2,
		"escalation_after_minutes": 30,
		"escalation_channels":      []string{"email:ops@example.com", "slack:#deploys", "pagerduty"},
		"last_escalated_at":        "2026-01-01T00:00:00Z",
		"escalation_count":         1,
		"request_payload":          map[string]any{"command": "secret command"},
	}, "pending")
	if err != nil {
		t.Fatalf("postApprovalWebhook: %v", err)
	}
	if gotAuth != "Bearer token-123" {
		t.Fatalf("authorization = %q, want bearer token", gotAuth)
	}
	if gotPayload["event"] != "pending" {
		t.Fatalf("event = %v, want pending", gotPayload["event"])
	}
	approval, ok := gotPayload["approval"].(map[string]any)
	if !ok {
		t.Fatalf("approval payload = %#v", gotPayload["approval"])
	}
	if _, ok := approval["request_payload"]; ok {
		t.Fatal("approval webhook must not include request_payload")
	}
	for _, field := range []string{"required_approver_roles", "required_approval_count", "escalation_after_minutes", "escalation_channels", "last_escalated_at", "escalation_count"} {
		if _, ok := approval[field]; ok {
			t.Fatalf("approval webhook must not include rule metadata field %q: %#v", field, approval)
		}
	}
	if approval["action"] != "ssh.exec" {
		t.Fatalf("action = %v, want ssh.exec", approval["action"])
	}
	encoded, _ := json.Marshal(gotPayload)
	for _, leaked := range []string{"secret command", "ops@example.com", "#deploys", "pagerduty"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("approval webhook leaked %q: %s", leaked, encoded)
		}
	}
}

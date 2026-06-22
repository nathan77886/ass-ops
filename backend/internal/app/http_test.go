package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

type approvalRoundTripFunc func(*http.Request) (*http.Response, error)

func (f approvalRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestRefsSummary(t *testing.T) {
	tests := []struct {
		name string
		refs map[string]any
		want string
	}{
		{name: "empty refs", refs: nil, want: "default"},
		{name: "branches", refs: map[string]any{"branches": []any{"main"}}, want: `{"branches":["main"]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := refsSummary(tt.refs)
			if got != tt.want {
				t.Fatalf("refsSummary = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRefsFromRunRef(t *testing.T) {
	fallback := map[string]any{"branches": []any{"main"}}
	got := refsFromRunRef(`{"branches":["release"],"tags":["v1"]}`, fallback)
	if branches := stringSliceFromAny(got["branches"]); len(branches) != 1 || branches[0] != "release" {
		t.Fatalf("branches = %#v, want release", branches)
	}
	if tags := stringSliceFromAny(got["tags"]); len(tags) != 1 || tags[0] != "v1" {
		t.Fatalf("tags = %#v, want v1", tags)
	}
	if refsFromRunRef("default", fallback)["branches"] == nil {
		t.Fatal("default run ref should fall back to asset refs")
	}
	if refsFromRunRef("not-json", fallback)["branches"] == nil {
		t.Fatal("invalid run ref should fall back to asset refs")
	}
}

func TestValidPublicHTTPURLRejectsUnsafeHosts(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "localhost", url: "http://localhost:8080"},
		{name: "loopback ip", url: "http://127.0.0.1:8080"},
		{name: "link local ip", url: "http://169.254.169.254"},
		{name: "private ip", url: "https://10.0.0.10"},
		{name: "userinfo", url: "https://token@example.com"},
		{name: "unresolvable host", url: "https://assops.invalid"},
		{name: "unsupported scheme", url: "file:///tmp/argocd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if validPublicHTTPURL(context.Background(), tt.url) {
				t.Fatalf("validPublicHTTPURL(%q) = true, want false", tt.url)
			}
		})
	}
}

func TestSensitiveArgoConfigRequiresElevatedRole(t *testing.T) {
	if !boolConfig(map[string]any{"insecure_skip_verify": true}, "insecure_skip_verify") {
		t.Fatal("expected insecure_skip_verify to parse as true")
	}
	if canUseSensitiveArgoConfig(&User{Role: "developer"}) {
		t.Fatal("developer should not be allowed to use sensitive Argo config")
	}
	if !canUseSensitiveArgoConfig(&User{Role: "owner"}) || !canUseSensitiveArgoConfig(&User{Role: "admin"}) {
		t.Fatal("owner and admin should be allowed to use sensitive Argo config")
	}
}

func TestAssetInventorySQLIncludesCoreAssetTypes(t *testing.T) {
	sql := assetInventorySQL()
	for _, token := range []string{
		"'project' AS asset_type",
		"'project_template'",
		"'provider_account'",
		"'template_file'",
		"'repository'",
		"'git_remote'",
		"'repo_sync'",
		"'webhook_connection'",
		"'pipeline_run'",
		"'host'",
		"'argo_connection'",
		"'deployment_target'",
		"'deployment_record'",
		"'rollback_point'",
		"'argo_app'",
		"'ai_runtime'",
		"'node_agent'",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("assetInventorySQL missing %s", token)
		}
	}
}

func TestAssetGraphNodesSQLIncludesVisibilityAndSearch(t *testing.T) {
	sql := assetGraphNodesSQL()
	for _, token := range []string{
		"FROM asset_inventory",
		"($1='' OR project_id=$1)",
		"($2='' OR asset_type=$2)",
		"name ILIKE $5",
		"pm.project_id::text=asset_inventory.project_id AND pm.user_id=$4",
		"LIMIT $6",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("assetGraphNodesSQL missing %s", token)
		}
	}
}

func TestAssetGraphLimitBounds(t *testing.T) {
	tests := map[string]int{
		"":     80,
		"25":   25,
		"0":    1,
		"-10":  1,
		"9999": 200,
		"bad":  80,
	}
	for input, want := range tests {
		if got := assetGraphLimit(input); got != want {
			t.Fatalf("assetGraphLimit(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestAssetRelationInventorySQLIncludesCoreRelations(t *testing.T) {
	sql := assetRelationInventorySQL()
	for _, token := range []string{
		"'owns' AS relation_type",
		"'provider_account:' || pa.id::text || ':manages:git_remote:' || gr.id::text",
		"'has_remote'",
		"'has_sync'",
		"'synced_from'",
		"'mirrors_to'",
		"'receives'",
		"'triggered_by'",
		"'manages'",
		"'deployed_to'",
		"'hosts'",
		"'has_rollback'",
		"FROM asset_relations ar",
		"ar.metadata->>'source'='manual'",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("assetRelationInventorySQL missing %s", token)
		}
	}
}

func TestAssetRelationsMigrationIncludesUniqueRelationIndex(t *testing.T) {
	content, err := os.ReadFile("../../migrations/002_git_first_version.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(content)
	if !strings.Contains(sql, "idx_asset_relations_unique_relation") ||
		!strings.Contains(sql, "ON asset_relations(from_asset_id, to_asset_id, relation_type)") {
		t.Fatal("asset_relations migration should include a unique relation index")
	}
}

func TestCleanAssetRelationType(t *testing.T) {
	tests := map[string]string{
		" Depends On ":      "depends_on",
		"deploys/to":        "deploysto",
		"uses.service-v1":   "uses.service-v1",
		"___observes---":    "observes",
		"contains spaces":   "contains_spaces",
		"DROP TABLE assets": "drop_table_assets",
	}
	for input, want := range tests {
		if got := cleanAssetRelationType(input); got != want {
			t.Fatalf("cleanAssetRelationType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRelationProjectID(t *testing.T) {
	if got := relationProjectID(map[string]any{"project_id": "project-1"}, map[string]any{"project_id": "project-1"}); got != "project-1" {
		t.Fatalf("same project = %q", got)
	}
	if got := relationProjectID(map[string]any{"project_id": "project-1"}, map[string]any{"project_id": ""}); got != "project-1" {
		t.Fatalf("from project = %q", got)
	}
	if got := relationProjectID(map[string]any{"project_id": ""}, map[string]any{"project_id": "project-2"}); got != "project-2" {
		t.Fatalf("to project = %q", got)
	}
	if got := relationProjectID(map[string]any{"project_id": "project-1"}, map[string]any{"project_id": "project-2"}); got != "" {
		t.Fatalf("cross project = %q, want empty", got)
	}
}

func TestCreateAssetRelationRejectsSameAssetBeforeTransaction(t *testing.T) {
	server := &Server{}
	body := strings.NewReader(`{"from_asset_id":"asset-1","to_asset_id":"asset-1","relation_type":"depends_on"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/asset-relations", body)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
	rr := httptest.NewRecorder()

	server.createAssetRelation(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestCreateAssetRelationRollsBackWhenCanonicalSyncFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	server := &Server{store: &Store{DB: sqlx.NewDb(db, "sqlmock")}}
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)WITH asset_inventory AS`).WillReturnError(fmt.Errorf("sync failed"))
	mock.ExpectRollback()

	body := strings.NewReader(`{"from_asset_id":"project:11111111-1111-1111-1111-111111111111","to_asset_id":"repository:22222222-2222-2222-2222-222222222222","relation_type":"depends_on"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/asset-relations", body)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
	rr := httptest.NewRecorder()

	server.createAssetRelation(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestProviderAccountsMigrationIncludesTableAndRemoteFK(t *testing.T) {
	content, err := os.ReadFile("../../migrations/003_provider_accounts.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(content)
	for _, token := range []string{
		"CREATE TABLE IF NOT EXISTS provider_accounts",
		"token_env TEXT NOT NULL DEFAULT ''",
		"idx_provider_accounts_provider_enabled",
		"fk_git_remotes_source_account_provider_accounts",
		"FOREIGN KEY (source_account_id) REFERENCES provider_accounts(id)",
		"CHECK (NOT enabled OR token_env <> '')",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("provider account migration missing %s", token)
		}
	}
}

func TestProviderAccountSanitizeDoesNotReturnRawTokenEnv(t *testing.T) {
	item := sanitizeProviderAccount(map[string]any{
		"id":            "account-1",
		"name":          "github-main",
		"provider_type": "github",
		"token_env":     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN",
	})
	if _, ok := item["token_env"]; ok {
		t.Fatal("sanitizeProviderAccount should remove token_env")
	}
	if item["token_configured"] != true {
		t.Fatalf("token_configured = %v, want true", item["token_configured"])
	}
	if got := fmt.Sprint(item["masked_token_env"]); strings.Contains(got, "GITHUB_MAIN") {
		t.Fatalf("masked token env leaked suffix: %q", got)
	}
}

func TestValidateProviderAccountInputRejectsWrongTokenEnv(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	_, err := validateProviderAccountInput(context.Background(), "bad", "github", server.URL, "", "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_MAIN", "", "private", nil)
	if err == nil {
		t.Fatal("github account should reject gitea token env")
	}
}

func TestProviderAccountMetadataMergePreservesExistingKeys(t *testing.T) {
	got := mergeMaps(cloneMap(map[string]any{"region": "us", "team": "platform"}), map[string]any{"team": "ops"})
	if got["region"] != "us" || got["team"] != "ops" {
		t.Fatalf("merged metadata = %#v", got)
	}
}

func TestProviderAccountRotationMetadataDoesNotLeakEnvNames(t *testing.T) {
	got := providerAccountRotationMetadata(
		map[string]any{"team": "platform", "token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_OLD"},
		"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_OLD",
		"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_NEW",
		"quarterly rotation",
		&User{ID: "user-1"},
	)
	if got["team"] != "platform" {
		t.Fatalf("existing metadata should be preserved: %#v", got)
	}
	if _, ok := got["token_env"]; ok {
		t.Fatalf("token_env should be removed from metadata: %#v", got)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "GITHUB_OLD") || strings.Contains(string(encoded), "GITHUB_NEW") {
		t.Fatalf("rotation metadata leaked env names: %s", encoded)
	}
	rotation := mapFromAny(got["token_rotation"])
	if rotation["previous_token_present"] != true || rotation["new_token_present"] != true || rotation["rotated_by"] != "user-1" {
		t.Fatalf("rotation metadata = %#v", rotation)
	}
}

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
	downstream := assetDependencySQL("downstream")
	for _, token := range []string{
		"WHERE ari.from_asset_id=$1",
		"JOIN asset_relation_inventory next ON next.from_asset_id=walk.current_asset_id",
		"next.to_asset_id",
		"NOT next.to_asset_id = ANY(walk.path_assets)",
	} {
		if !strings.Contains(downstream, token) {
			t.Fatalf("downstream assetDependencySQL missing %s", token)
		}
	}

	upstream := assetDependencySQL("upstream")
	for _, token := range []string{
		"WHERE ari.to_asset_id=$1",
		"JOIN asset_relation_inventory next ON next.to_asset_id=walk.current_asset_id",
		"next.from_asset_id",
		"NOT next.from_asset_id = ANY(walk.path_assets)",
	} {
		if !strings.Contains(upstream, token) {
			t.Fatalf("upstream assetDependencySQL missing %s", token)
		}
	}
}

func TestAssetDependencySQLIncludesRecursiveWalk(t *testing.T) {
	sql := assetDependencySQL("downstream")
	for _, token := range []string{
		"asset_dependency_walk AS",
		"UNION ALL",
		"walk.depth < $3",
		"asset_dependency_paths AS",
		"LIMIT 501",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("assetDependencySQL missing %s", token)
		}
	}
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
		"id":              "approval-1",
		"project_id":      "project-1",
		"resource_type":   "ssh_machine",
		"resource_id":     "machine-1",
		"action":          "ssh.exec",
		"title":           "Run SSH command",
		"status":          "pending",
		"request_payload": map[string]any{"command": "secret command"},
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
	if approval["action"] != "ssh.exec" {
		t.Fatalf("action = %v, want ssh.exec", approval["action"])
	}
}

func TestPostApprovalWebhookReminderUsesSafePayload(t *testing.T) {
	var gotPayload map[string]any
	previousClient := approvalWebhookHTTPClient
	approvalWebhookHTTPClient = &http.Client{Transport: approvalRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode webhook payload: %v", err)
		}
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	defer func() { approvalWebhookHTTPClient = previousClient }()

	server := &Server{cfg: Config{ApprovalWebhookURL: "https://93.184.216.34/approval"}}
	err := server.postApprovalWebhook(context.Background(), map[string]any{
		"id":                      "approval-1",
		"action":                  "agent.execute",
		"title":                   "Execute agent task",
		"status":                  "pending",
		"required_approver_roles": []string{"admin", "owner"},
		"required_approval_count": 2,
		"approved_count":          1,
		"request_payload":         map[string]any{"private": "context"},
	}, "reminder")
	if err != nil {
		t.Fatalf("postApprovalWebhook reminder: %v", err)
	}
	if gotPayload["event"] != "reminder" {
		t.Fatalf("event = %v, want reminder", gotPayload["event"])
	}
	approval, ok := gotPayload["approval"].(map[string]any)
	if !ok {
		t.Fatalf("approval payload = %#v", gotPayload["approval"])
	}
	if _, ok := approval["request_payload"]; ok {
		t.Fatal("reminder webhook must not include request_payload")
	}
	if approval["approved_count"] != float64(1) || approval["required_approval_count"] != float64(2) {
		t.Fatalf("approval progress = %#v", approval)
	}
}

func TestPostApprovalWebhookRejectsUnsupportedScheme(t *testing.T) {
	server := &Server{cfg: Config{ApprovalWebhookURL: "ftp://example.com/hook"}}
	err := server.postApprovalWebhook(context.Background(), map[string]any{"id": "approval-1"}, "pending")
	if err == nil || !strings.Contains(err.Error(), "public http or https") {
		t.Fatalf("postApprovalWebhook error = %v, want scheme error", err)
	}
}

func TestPostApprovalWebhookRejectsMissingHost(t *testing.T) {
	server := &Server{cfg: Config{ApprovalWebhookURL: "http://"}}
	err := server.postApprovalWebhook(context.Background(), map[string]any{"id": "approval-1"}, "pending")
	if err == nil || !strings.Contains(err.Error(), "include a host") {
		t.Fatalf("postApprovalWebhook error = %v, want host error", err)
	}
}

func TestPostApprovalWebhookRejectsLocalhost(t *testing.T) {
	server := &Server{cfg: Config{ApprovalWebhookURL: "http://127.0.0.1:8080/approval"}}
	err := server.postApprovalWebhook(context.Background(), map[string]any{"id": "approval-1"}, "pending")
	if err == nil || !strings.Contains(err.Error(), "public http or https") {
		t.Fatalf("postApprovalWebhook error = %v, want public URL error", err)
	}
}

func TestApprovalExpirySQLOnlyExpiresPendingDueRows(t *testing.T) {
	sql := approvalExpirySQL()
	for _, token := range []string{
		"UPDATE operation_approvals",
		"SET status='expired'",
		"WHERE status='pending'",
		"expires_at IS NOT NULL",
		"expires_at <= now()",
		"RETURNING *",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("approvalExpirySQL missing %q in %s", token, sql)
		}
	}
}

func TestApprovalNotificationStatusSuccessAndFailure(t *testing.T) {
	previousClient := approvalWebhookHTTPClient
	defer func() { approvalWebhookHTTPClient = previousClient }()

	approval := map[string]any{"id": "approval-1", "action": "ssh.exec"}
	server := &Server{cfg: Config{ApprovalWebhookURL: "https://93.184.216.34/approval"}}

	approvalWebhookHTTPClient = &http.Client{Transport: approvalRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	status, lastError := server.approvalNotificationStatus(context.Background(), approval, "expired")
	if status != "delivered" || lastError != "" {
		t.Fatalf("success status = %q error = %q", status, lastError)
	}

	approvalWebhookHTTPClient = &http.Client{Transport: approvalRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	status, lastError = server.approvalNotificationStatus(context.Background(), approval, "expired")
	if status != "failed" || !strings.Contains(lastError, "status 502") {
		t.Fatalf("failure status = %q error = %q", status, lastError)
	}
}

func TestRepoSyncRunFiltersFromRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/repo-sync-runs?asset_id=%20asset-1%20&status=%20failed%20&ref=%20refs/heads/main%20&since=2026-01-01T00:00:00Z&until=2026-01-02T00:00:00Z", nil)
	got, err := repoSyncRunFiltersFromRequest(req)
	if err != nil {
		t.Fatalf("repoSyncRunFiltersFromRequest: %v", err)
	}
	if got.AssetID != "asset-1" || got.Status != "failed" || got.Ref != "refs/heads/main" {
		t.Fatalf("filters = %#v", got)
	}
	if got.Since != "2026-01-01T00:00:00Z" || got.Until != "2026-01-02T00:00:00Z" {
		t.Fatalf("date filters = %#v", got)
	}
}

func TestRepoSyncRunFiltersRejectInvalidTime(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/repo-sync-runs?since=yesterday", nil)
	_, err := repoSyncRunFiltersFromRequest(req)
	if err == nil || !strings.Contains(err.Error(), "since must be RFC3339") {
		t.Fatalf("error = %v, want RFC3339 error", err)
	}
}

func TestRepoSyncAssetAnalyticsSQLIncludesCoreMetrics(t *testing.T) {
	sql := repoSyncAssetAnalyticsSQL("rsa")
	for _, token := range []string{
		"count(rsr.id)::int AS total_runs",
		"rsr.status='completed'",
		"rsr.status='failed'",
		"success_rate",
		"recent.repo_sync_asset_id=rsa.id",
		"last_failure_message",
		"avg_duration_seconds",
		"WHERE rsr.repo_sync_asset_id=rsa.id",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("analytics SQL missing %q in %s", token, sql)
		}
	}
}

func TestRepoSyncAssetTrendSQLIncludesDailyMetrics(t *testing.T) {
	sql := repoSyncAssetTrendSQL()
	for _, token := range []string{
		"to_char(day_bucket, 'YYYY-MM-DD') AS day",
		"count(*)::int AS total_runs",
		"status='completed'",
		"status='failed'",
		"created_at >= now() - interval '14 days'",
		"ORDER BY day_bucket DESC",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("trend SQL missing %q in %s", token, sql)
		}
	}
}

func TestRepoSyncCapacitySignals(t *testing.T) {
	signals := repoSyncCapacitySignals(
		map[string]any{"id": "asset-1", "enabled": false},
		map[string]any{
			"source_provider":         "gitea",
			"target_provider":         "github",
			"source_last_sync_status": "completed",
			"target_last_sync_status": "failed",
			"active_runs":             int64(2),
			"failed_runs_7d":          int64(6),
			"webhook_failures_7d":     int64(1),
			"github_runs_24h":         int64(55),
			"last_webhook_error":      "bad signature",
		},
		"source-1",
		"target-1",
	)
	byName := map[string]map[string]any{}
	for _, signal := range signals {
		byName[fmt.Sprint(signal["name"])] = signal
	}
	if byName["target provider"]["severity"] != "danger" {
		t.Fatalf("target provider severity = %v", byName["target provider"]["severity"])
	}
	if byName["sync capacity"]["severity"] != "warning" {
		t.Fatalf("sync capacity severity = %v", byName["sync capacity"]["severity"])
	}
	if byName["7d sync failures"]["severity"] != "danger" {
		t.Fatalf("7d sync failures severity = %v", byName["7d sync failures"]["severity"])
	}
	if byName["webhook delivery"]["severity"] != "warning" || !strings.Contains(fmt.Sprint(byName["webhook delivery"]["detail"]), "bad signature") {
		t.Fatalf("webhook signal = %#v", byName["webhook delivery"])
	}
	if byName["GitHub Actions volume"]["severity"] != "warning" {
		t.Fatalf("GitHub Actions volume severity = %v", byName["GitHub Actions volume"]["severity"])
	}
	if byName["asset state"]["status"] != "disabled" {
		t.Fatalf("asset state signal = %#v", byName["asset state"])
	}
}

func TestRepoSyncAssetRisk(t *testing.T) {
	cases := []struct {
		name        string
		asset       map[string]any
		wantRisk    string
		wantSummary string
	}{
		{
			name:        "archived",
			asset:       map[string]any{"archived_at": "2026-01-01T00:00:00Z", "enabled": true},
			wantRisk:    "warning",
			wantSummary: "archived",
		},
		{
			name:        "last sync failed",
			asset:       map[string]any{"enabled": true, "last_sync_status": "failed"},
			wantRisk:    "danger",
			wantSummary: "last sync failed",
		},
		{
			name:        "queue saturated",
			asset:       map[string]any{"enabled": true, "running_runs": int64(3)},
			wantRisk:    "danger",
			wantSummary: "3 active runs",
		},
		{
			name:        "low success rate",
			asset:       map[string]any{"enabled": true, "total_runs": int64(8), "success_rate": "42.5"},
			wantRisk:    "danger",
			wantSummary: "42% success rate",
		},
		{
			name:        "healthy",
			asset:       map[string]any{"enabled": true, "total_runs": int64(4), "success_rate": 100.0},
			wantRisk:    "ok",
			wantSummary: "healthy",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRisk, gotSummary := repoSyncAssetRisk(tc.asset)
			if gotRisk != tc.wantRisk || !strings.Contains(gotSummary, tc.wantSummary) {
				t.Fatalf("repoSyncAssetRisk = %q, %q; want %q containing %q", gotRisk, gotSummary, tc.wantRisk, tc.wantSummary)
			}
		})
	}
}

func TestWebhookConnectionHealth(t *testing.T) {
	cases := []struct {
		name        string
		row         map[string]any
		wantHealth  string
		wantSummary string
	}{
		{
			name:        "disabled",
			row:         map[string]any{"enabled": false},
			wantHealth:  "warning",
			wantSummary: "disabled",
		},
		{
			name:        "many failures",
			row:         map[string]any{"enabled": true, "failures_7d": int64(3)},
			wantHealth:  "danger",
			wantSummary: "3 failed",
		},
		{
			name:        "last rejected",
			row:         map[string]any{"enabled": true, "last_delivery_status": "rejected", "last_error_message": "invalid signature"},
			wantHealth:  "danger",
			wantSummary: "invalid signature",
		},
		{
			name:        "some failures",
			row:         map[string]any{"enabled": true, "failures_7d": int64(1), "deliveries_7d": int64(5)},
			wantHealth:  "warning",
			wantSummary: "1 failed",
		},
		{
			name:        "healthy",
			row:         map[string]any{"enabled": true, "deliveries_7d": int64(4), "failures_7d": int64(0)},
			wantHealth:  "ok",
			wantSummary: "4 deliveries",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotHealth, gotSummary := webhookConnectionHealth(tc.row)
			if gotHealth != tc.wantHealth || !strings.Contains(gotSummary, tc.wantSummary) {
				t.Fatalf("webhookConnectionHealth = %q, %q; want %q containing %q", gotHealth, gotSummary, tc.wantHealth, tc.wantSummary)
			}
		})
	}
}

func TestOperationApprovalFiltersFromRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/operation-approvals?status=%20pending%20&action=ssh.exec&resource_type=ssh_machine&q=%20deploy%20&requested_by=%20ops@example.com%20&since=2026-01-01T00:00:00Z&until=2026-01-02T00:00:00Z", nil)
	got, err := operationApprovalFiltersFromRequest(req)
	if err != nil {
		t.Fatalf("operationApprovalFiltersFromRequest: %v", err)
	}
	if got.Status != "pending" || got.Action != "ssh.exec" || got.ResourceType != "ssh_machine" {
		t.Fatalf("filters = %#v", got)
	}
	if got.Query != "deploy" || got.RequestedBy != "ops@example.com" {
		t.Fatalf("text filters = %#v", got)
	}
}

func TestOperationApprovalSummarySQLIncludesVisibilityAndMetrics(t *testing.T) {
	sql := operationApprovalSummarySQL()
	for _, token := range []string{
		"FROM operation_approvals oa",
		"pm.project_id=oa.project_id AND pm.user_id=$2",
		"status='pending'",
		"status='approved'",
		"status='rejected'",
		"status='expired'",
		"expires_at <= now() + interval '1 hour'",
		"notification_status='failed'",
		"jsonb_object_agg(status, count)",
		"jsonb_agg(jsonb_build_object('action', action, 'count', count))",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("summary SQL missing %q in %s", token, sql)
		}
	}
}

func TestOperationApprovalReminderCandidatesSQLIncludesSLAAndVisibility(t *testing.T) {
	sql := operationApprovalReminderCandidatesSQL()
	for _, token := range []string{
		"oa.status='pending'",
		"pm.project_id=oa.project_id AND pm.user_id=$2",
		"operation_approval_decisions oad",
		"notification_status='failed'",
		"expires_at <= now() + interval '15 minutes'",
		"created_at <= now() - interval '30 minutes'",
		"approved_count < required_approval_count",
		"operation_approval_delegations oadel",
		"can_current_user_decide",
		"reminder_reason",
		"escalation_level",
		"LIMIT 50",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("operationApprovalReminderCandidatesSQL missing %q in %s", token, sql)
		}
	}
}

func TestDueOperationApprovalRemindersSQLIncludesThrottleAndLocking(t *testing.T) {
	sql := dueOperationApprovalRemindersSQL()
	for _, token := range []string{
		"oa.status='pending'",
		"oa.last_reminded_at IS NULL OR oa.last_reminded_at <= now() - interval '60 minutes'",
		"oa.notification_status='failed'",
		"oa.expires_at IS NOT NULL AND oa.expires_at <= now() + interval '1 hour'",
		"oa.created_at <= now() - interval '30 minutes'",
		"COALESCE(decision_counts.approved_count, 0) < oa.required_approval_count",
		"FOR UPDATE SKIP LOCKED",
		"SET last_reminded_at=now()",
		"reminder_count=reminder_count + 1",
		"RETURNING oa.*, due.approved_count",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("dueOperationApprovalRemindersSQL missing %q in %s", token, sql)
		}
	}
}

func TestDueOperationApprovalEscalationsSQLIncludesThrottleAndLocking(t *testing.T) {
	sql := dueOperationApprovalEscalationsSQL()
	for _, token := range []string{
		"oa.status='pending'",
		"oa.escalation_after_minutes > 0",
		"oa.created_at <= now() - (oa.escalation_after_minutes * interval '1 minute')",
		"COALESCE(decision_counts.approved_count, 0) < oa.required_approval_count",
		"oa.last_escalated_at IS NULL OR oa.last_escalated_at <= now() - interval '120 minutes'",
		"FOR UPDATE SKIP LOCKED",
		"SET last_escalated_at=now()",
		"escalation_count=escalation_count + 1",
		"RETURNING oa.*, due.approved_count",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("dueOperationApprovalEscalationsSQL missing %q in %s", token, sql)
		}
	}
}

func TestOperationApprovalRulesSQLIncludesPolicyFields(t *testing.T) {
	sql := operationApprovalRulesSQL()
	for _, token := range []string{
		"resource_type",
		"action",
		"required_approver_roles",
		"required_approval_count",
		"expires_after_minutes",
		"notification_channels",
		"escalation_after_minutes",
		"escalation_channels",
		"enabled",
		"ORDER BY enabled DESC, priority ASC",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("operationApprovalRulesSQL missing %q in %s", token, sql)
		}
	}
}

func TestNormalizeRuleStringList(t *testing.T) {
	got := normalizeRuleStringList([]string{" Admin ", "admin", "OWNER", ""}, []string{"fallback"})
	want := []string{"admin", "owner"}
	if len(got) != len(want) {
		t.Fatalf("roles = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("roles = %#v, want %#v", got, want)
		}
	}
	fallback := normalizeRuleStringList(nil, []string{"admin"})
	if len(fallback) != 1 || fallback[0] != "admin" {
		t.Fatalf("fallback = %#v", fallback)
	}
}

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

func TestOperationApprovalRuleIncludesRequiredApprovalCount(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	server := &Server{store: &Store{DB: sqlx.NewDb(db, "sqlmock")}}
	mock.ExpectQuery(`(?s)SELECT id, required_approver_roles, required_approval_count, expires_after_minutes, notification_channels, escalation_after_minutes, escalation_channels`).
		WithArgs("ssh_machine", "ssh.exec").
		WillReturnRows(sqlmock.NewRows([]string{
			"id",
			"required_approver_roles",
			"required_approval_count",
			"expires_after_minutes",
			"notification_channels",
			"escalation_after_minutes",
			"escalation_channels",
		}).AddRow("rule-1", "{admin,owner}", 2, 60, "{ui,webhook}", 30, "{webhook}"))

	rule, err := server.operationApprovalRule(context.Background(), server.store.DB, PolicyResource{Type: "ssh_machine"}, "ssh.exec")
	if err != nil {
		t.Fatalf("operationApprovalRule: %v", err)
	}
	if rule.RequiredApprovalCount != 2 {
		t.Fatalf("RequiredApprovalCount = %d, want 2", rule.RequiredApprovalCount)
	}
	if len(rule.RequiredApproverRoles) != 2 || rule.RequiredApproverRoles[0] != "admin" || rule.RequiredApproverRoles[1] != "owner" {
		t.Fatalf("RequiredApproverRoles = %#v", rule.RequiredApproverRoles)
	}
	if rule.EscalationAfterMinutes != 30 || len(rule.EscalationChannels) != 1 || rule.EscalationChannels[0] != "webhook" {
		t.Fatalf("escalation = %d %#v, want 30 [webhook]", rule.EscalationAfterMinutes, rule.EscalationChannels)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestGetOperationApprovalSummaryUsesUserVisibility(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	server := &Server{store: &Store{DB: sqlx.NewDb(db, "sqlmock")}}
	mock.ExpectQuery(`(?s)UPDATE operation_approvals.*RETURNING \*`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`(?s)WITH visible AS .*jsonb_agg`).
		WithArgs(false, "user-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"total",
			"pending",
			"approved",
			"rejected",
			"expired",
			"expiring_soon",
			"notification_failed",
			"by_status",
			"by_action",
		}).AddRow(3, 2, 1, 0, 0, 1, 1, []byte(`{"pending":2,"approved":1}`), []byte(`[{"action":"ssh.exec","count":2}]`)))
	req := httptest.NewRequest(http.MethodGet, "/api/operation-approvals/summary", nil)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "user-1", Role: "developer"}))
	rr := httptest.NewRecorder()

	server.getOperationApprovalSummary(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["pending"] != float64(2) || body["expiring_soon"] != float64(1) || body["notification_failed"] != float64(1) {
		t.Fatalf("summary body = %#v", body)
	}
	if actions, ok := body["by_action"].([]any); !ok || len(actions) != 1 {
		t.Fatalf("by_action = %#v", body["by_action"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
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
					map[string]any{"name": "prod"},
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
		"SSH machines: 1",
		"GitHub Actions runs: 1",
		"Asset graph assets: 2",
		"Asset graph relations: 1",
		"Asset status snapshots: 2",
		"Asset types: git_remote=1, repository=1",
		"Asset health: high=1, normal=1",
		"Review canonical asset graph entries, status snapshots",
		"No code changes, deployments, SSH execution",
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

func TestCanonicalAssetRefreshHooksAreWired(t *testing.T) {
	httpSource, err := os.ReadFile("http.go")
	if err != nil {
		t.Fatalf("read http.go: %v", err)
	}
	for _, reason := range []string{
		`syncCanonicalAssetsInTransaction(w, r, tx, "project.create")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "project.update")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "git_repository.create")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "git_repository.update")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "git_remote.create")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "git_remote.update")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "repo_sync_asset.create")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "repo_sync_asset.update")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "repo_sync_asset.archive")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "repo_sync_asset.restore")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "provider_account.create")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "provider_account.update")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "provider_account.check")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "provider_account.rotate_token_env")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "webhook_connection.create")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "webhook_connection.rotate_secret")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "ai_runtime.create")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "ai_runtime.verify")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "worker_node.register")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "argo_connection.create")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "ssh_machine.create")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "asset_relation.create")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "asset_relation.delete")`,
		`SyncCanonicalAssetsWith(r.Context(), tx)`,
	} {
		if !strings.Contains(string(httpSource), reason) {
			t.Fatalf("http.go missing transactional canonical sync hook %q", reason)
		}
	}
	if got := strings.Count(string(httpSource), `if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}`); got < 3 {
		t.Fatalf("repo sync asset update paths should preserve ErrNotFound -> 404 handling, found %d branches", got)
	}

	workerSource, err := os.ReadFile("worker.go")
	if err != nil {
		t.Fatalf("read worker.go: %v", err)
	}
	for _, token := range []string{
		`refreshCanonicalAssetsAfterOperation(ctx, job, opID, "completed")`,
		`refreshCanonicalAssetsAfterOperation(ctx, job, opID, "failed")`,
		`canonicalAssetsSyncedInAdapterTransaction(job)`,
		`SyncCanonicalAssetsWith(ctx, tx)`,
		`syncing canonical assets for GitHub Actions sync`,
		`syncing canonical assets for failed GitHub Actions sync`,
		`syncing canonical assets for failed GitHub Actions sync without remote`,
		`syncing canonical assets for Argo app sync`,
		`syncing canonical assets for failed Argo app sync`,
		`syncing canonical assets for failed project template creation`,
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

func TestAgentExecutionAuditSteps(t *testing.T) {
	steps := agentExecutionAuditSteps(
		map[string]any{"id": "task-1"},
		map[string]any{"id": "plan-1", "content": "approved plan"},
		map[string]any{"id": "op-1"},
		map[string]any{
			"id":           "runtime-1",
			"name":         "Demo Codex",
			"runtime_type": "codex-cli",
			"codex_binary": "codex",
			"model":        "gpt-5-codex",
			"status":       "verified",
		},
	)
	if len(steps) != 4 {
		t.Fatalf("len(steps) = %d, want 4", len(steps))
	}
	wantTools := []string{"context.generate", "plan.review", "runtime.check", "patch.prepare"}
	for i, tool := range wantTools {
		if steps[i]["tool_name"] != tool {
			t.Fatalf("step %d tool = %v, want %s", i, steps[i]["tool_name"], tool)
		}
	}
	runtimeInput := mapFromAny(steps[2]["input"])
	if runtimeInput["runtime_id"] != "runtime-1" || runtimeInput["status"] != "verified" {
		t.Fatalf("runtime.check input missing runtime readiness: %#v", runtimeInput)
	}
	runtimeOutput := mapFromAny(steps[2]["output"])
	if runtimeOutput["mutation_enabled"] != false {
		t.Fatalf("runtime.check should keep mutation disabled: %#v", runtimeOutput)
	}
	if _, ok := runtimeInput["config"]; ok {
		t.Fatalf("runtime.check input should not expose runtime config: %#v", runtimeInput)
	}
	patchInput := mapFromAny(steps[3]["input"])
	if patchInput["mode"] != "simulation_only" {
		t.Fatalf("patch.prepare mode = %v, want simulation_only", patchInput["mode"])
	}
	patchOutput := mapFromAny(steps[3]["output"])
	if !strings.Contains(fmt.Sprint(patchOutput["message"]), "code mutation remains disabled") {
		t.Fatalf("patch.prepare output should document disabled mutation: %#v", patchOutput)
	}
	planInput := mapFromAny(steps[1]["input"])
	if planInput["plan_bytes"] != len("approved plan") {
		t.Fatalf("plan_bytes = %v, want %d", planInput["plan_bytes"], len("approved plan"))
	}
}

func TestAgentToolCallAuditMigrationAndFreshInit(t *testing.T) {
	migration, err := os.ReadFile("../../migrations/005_agent_tool_call_audit.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	for _, token := range []string{
		"ADD COLUMN IF NOT EXISTS operation_run_id",
		"ADD COLUMN IF NOT EXISTS project_id",
		"idx_agent_tool_calls_operation",
		"idx_agent_tool_calls_project",
	} {
		if !strings.Contains(string(migration), token) {
			t.Fatalf("migration missing %q", token)
		}
	}
	for _, path := range []string{"../../../deploy/docker-compose.yml", "../../../deploy/compose.prod.yml"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(content), "005_agent_tool_call_audit.sql") {
			t.Fatalf("%s missing 005_agent_tool_call_audit.sql init mount", path)
		}
	}
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

func TestAssetGraphSavedViewMigrationAndFreshInit(t *testing.T) {
	migration, err := os.ReadFile("../../migrations/006_asset_graph_saved_views.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	for _, token := range []string{
		"CREATE TABLE IF NOT EXISTS asset_graph_views",
		"UNIQUE (user_id, name)",
		"idx_asset_graph_views_user_updated",
	} {
		if !strings.Contains(string(migration), token) {
			t.Fatalf("migration missing %q", token)
		}
	}
	for _, path := range []string{"../../../deploy/docker-compose.yml", "../../../deploy/compose.prod.yml"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(content), "006_asset_graph_saved_views.sql") {
			t.Fatalf("%s missing 006_asset_graph_saved_views.sql init mount", path)
		}
	}
}

func TestMultiApproverApprovalMigrationAndFreshInit(t *testing.T) {
	migration, err := os.ReadFile("../../migrations/007_multi_approver_approvals.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	for _, token := range []string{
		"ADD COLUMN IF NOT EXISTS required_approval_count",
		"CREATE TABLE IF NOT EXISTS operation_approval_decisions",
		"UNIQUE (operation_approval_id, user_id)",
		"idx_operation_approval_decisions_approval",
	} {
		if !strings.Contains(string(migration), token) {
			t.Fatalf("migration missing %q", token)
		}
	}
	for _, path := range []string{"../../../deploy/docker-compose.yml", "../../../deploy/compose.prod.yml"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(content), "007_multi_approver_approvals.sql") {
			t.Fatalf("%s missing 007_multi_approver_approvals.sql init mount", path)
		}
	}
}

func TestAssetStatusSnapshotMigrationAndFreshInit(t *testing.T) {
	migration, err := os.ReadFile("../../migrations/008_asset_status_snapshots.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	for _, token := range []string{
		"idx_asset_status_snapshots_asset_collected",
		"asset_status_snapshots(asset_id, collected_at DESC)",
	} {
		if !strings.Contains(string(migration), token) {
			t.Fatalf("migration missing %q", token)
		}
	}
	for _, path := range []string{"../../../deploy/docker-compose.yml", "../../../deploy/compose.prod.yml"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(content), "008_asset_status_snapshots.sql") {
			t.Fatalf("%s missing 008_asset_status_snapshots.sql init mount", path)
		}
	}
}

func TestWorkerQueueSummarySQLIncludesVisibilityAndRiskMetrics(t *testing.T) {
	sql := workerQueueSummarySQL()
	for _, token := range []string{
		"FROM worker_jobs wj",
		"LEFT JOIN operation_runs op ON op.id=wj.operation_run_id",
		"pm.project_id=op.project_id AND pm.user_id=$2",
		"last_heartbeat_at >= now() - interval '2 minutes'",
		"status='queued'",
		"status='running'",
		"status='failed'",
		"created_at < now() - interval '15 minutes'",
		"started_at < now() - interval '15 minutes'",
		"jsonb_object_agg(status, count)",
		"jsonb_build_object('tool_name', tool_name, 'queued', queued)",
		"recent_failures",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("workerQueueSummarySQL missing %q in %s", token, sql)
		}
	}
}

func TestApprovalReminderMigrationAndFreshInit(t *testing.T) {
	migration, err := os.ReadFile("../../migrations/009_approval_reminders.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	for _, token := range []string{
		"ADD COLUMN IF NOT EXISTS last_reminded_at",
		"ADD COLUMN IF NOT EXISTS reminder_count",
		"idx_operation_approvals_reminder_due",
		"WHERE status='pending'",
	} {
		if !strings.Contains(string(migration), token) {
			t.Fatalf("migration missing %q", token)
		}
	}
	for _, path := range []string{"../../../deploy/docker-compose.yml", "../../../deploy/compose.prod.yml"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(content), "009_approval_reminders.sql") {
			t.Fatalf("%s missing 009_approval_reminders.sql init mount", path)
		}
	}
}

func TestApprovalRuleAuditMigrationAndFreshInit(t *testing.T) {
	migration, err := os.ReadFile("../../migrations/010_approval_rule_audit.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	for _, token := range []string{
		"CREATE TABLE IF NOT EXISTS operation_approval_rule_audits",
		"operation_approval_rule_id UUID REFERENCES operation_approval_rules",
		"actor_user_id UUID REFERENCES users",
		"before_state JSONB",
		"after_state JSONB",
		"idx_operation_approval_rule_audits_rule",
	} {
		if !strings.Contains(string(migration), token) {
			t.Fatalf("migration missing %q", token)
		}
	}
	for _, path := range []string{"../../../deploy/docker-compose.yml", "../../../deploy/compose.prod.yml"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(content), "010_approval_rule_audit.sql") {
			t.Fatalf("%s missing 010_approval_rule_audit.sql init mount", path)
		}
	}
}

func TestApprovalEscalationMigrationAndFreshInit(t *testing.T) {
	migration, err := os.ReadFile("../../migrations/011_approval_escalations.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	for _, token := range []string{
		"ADD COLUMN IF NOT EXISTS escalation_after_minutes",
		"ADD COLUMN IF NOT EXISTS escalation_channels",
		"ADD COLUMN IF NOT EXISTS last_escalated_at",
		"ADD COLUMN IF NOT EXISTS escalation_count",
		"idx_operation_approvals_escalation_due",
	} {
		if !strings.Contains(string(migration), token) {
			t.Fatalf("migration missing %q", token)
		}
	}
	for _, path := range []string{"../../../deploy/docker-compose.yml", "../../../deploy/compose.prod.yml"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(content), "011_approval_escalations.sql") {
			t.Fatalf("%s missing 011_approval_escalations.sql init mount", path)
		}
	}
}

func TestApprovalDelegationMigrationAndFreshInit(t *testing.T) {
	migration, err := os.ReadFile("../../migrations/012_approval_delegations.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	for _, token := range []string{
		"CREATE TABLE IF NOT EXISTS operation_approval_delegations",
		"operation_approval_id UUID NOT NULL REFERENCES operation_approvals",
		"from_user_id UUID REFERENCES users",
		"to_user_id UUID NOT NULL REFERENCES users",
		"UNIQUE(operation_approval_id, to_user_id)",
		"idx_operation_approval_delegations_user",
	} {
		if !strings.Contains(string(migration), token) {
			t.Fatalf("migration missing %q", token)
		}
	}
	for _, path := range []string{"../../../deploy/docker-compose.yml", "../../../deploy/compose.prod.yml"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(content), "012_approval_delegations.sql") {
			t.Fatalf("%s missing 012_approval_delegations.sql init mount", path)
		}
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

func TestProjectTemplatePreviewFlagsSameRemoteIDs(t *testing.T) {
	preview := projectTemplatePreview(map[string]any{}, "Billing", "billing", "", map[string]any{
		"repo_sync": map[string]any{
			"source_remote_id": "remote-1",
			"target_remote_id": "remote-1",
		},
	})
	sync := mapFromAny(preview["repo_sync"])
	if sync["status"] != "planned" {
		t.Fatalf("repo_sync status = %v, want planned", sync["status"])
	}
	if sync["reason"] != "source_remote_id and target_remote_id must be different" {
		t.Fatalf("repo_sync reason = %v", sync["reason"])
	}
	repo := mapFromAny(preview["repository"])
	if repo["repo_key"] != "billing-service" {
		t.Fatalf("repo_key = %v, want billing-service", repo["repo_key"])
	}
}

func TestVerifyWebhookSignature(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	secret := "top-secret"
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))

	header := make(http.Header)
	header.Set("X-Gitea-Signature", signature)
	if !verifyWebhookSignature(header, secret, body) {
		t.Fatal("expected X-Gitea-Signature to verify")
	}
	header = make(http.Header)
	header.Set("X-Hub-Signature-256", "sha256="+signature)
	if !verifyWebhookSignature(header, secret, body) {
		t.Fatal("expected X-Hub-Signature-256 to verify")
	}
	header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	if verifyWebhookSignature(header, secret, body) {
		t.Fatal("invalid signature should fail")
	}
}

func TestWebhookSecretEncryptionAndLegacyFallback(t *testing.T) {
	server := &Server{cfg: Config{JWTSecret: "jwt-secret", WebhookSecretKey: "webhook-key"}}
	ciphertext, err := server.encryptWebhookSecret("shared-secret")
	if err != nil {
		t.Fatalf("encryptWebhookSecret error: %v", err)
	}
	if !strings.HasPrefix(ciphertext, "v1:") || strings.Contains(ciphertext, "shared-secret") {
		t.Fatalf("ciphertext should not contain plaintext secret: %q", ciphertext)
	}
	got, err := server.webhookSecretFromConnection(map[string]any{"secret_ciphertext": ciphertext})
	if err != nil {
		t.Fatalf("webhookSecretFromConnection error: %v", err)
	}
	if got != "shared-secret" {
		t.Fatalf("secret = %q, want shared-secret", got)
	}
	legacy, err := server.webhookSecretFromConnection(map[string]any{"secret_token": "legacy-secret"})
	if err != nil {
		t.Fatalf("legacy webhookSecretFromConnection error: %v", err)
	}
	if legacy != "legacy-secret" {
		t.Fatalf("legacy secret = %q, want legacy-secret", legacy)
	}
	if _, err := server.webhookSecretFromConnection(map[string]any{}); err == nil {
		t.Fatal("empty webhook connection secret should return an error")
	}
}

func TestPublicBaseURLTrimsTrailingSlash(t *testing.T) {
	server := &Server{cfg: Config{GatewayURL: "https://assops.example.com/"}}
	if got := server.publicBaseURL(); got != "https://assops.example.com" {
		t.Fatalf("publicBaseURL = %q, want https://assops.example.com", got)
	}
}

func TestPublicBaseURLKeepsOnlyHTTPOrigin(t *testing.T) {
	server := &Server{cfg: Config{GatewayURL: "https://assops.example.com/nested/path?token=bad#fragment"}}
	if got := server.publicBaseURL(); got != "https://assops.example.com" {
		t.Fatalf("publicBaseURL = %q, want https://assops.example.com", got)
	}
	for _, input := range []string{"ftp://assops.example.com", "https://", "://bad", "assops.example.com"} {
		server.cfg.GatewayURL = input
		if got := server.publicBaseURL(); got != "http://localhost:8080" {
			t.Fatalf("publicBaseURL(%q) = %q, want localhost fallback", input, got)
		}
	}
}

func TestWebhookDeliveryIDIgnoresRequestIDFallback(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/api/webhooks/github/id", nil)
	req.Header.Set("X-Request-Id", "request-id")
	if got := webhookDeliveryID(req); got != "" {
		t.Fatalf("webhookDeliveryID with only X-Request-Id = %q, want empty", got)
	}
	req.Header.Set("X-GitHub-Delivery", "delivery-id")
	if got := webhookDeliveryID(req); got != "delivery-id" {
		t.Fatalf("webhookDeliveryID = %q, want delivery-id", got)
	}
}

func TestRepoSyncAssetMatchesWebhookRef(t *testing.T) {
	tests := []struct {
		name string
		refs map[string]any
		ref  string
		want bool
	}{
		{name: "matching branch", refs: map[string]any{"branches": []any{"main"}}, ref: "refs/heads/main", want: true},
		{name: "wildcard tag", refs: map[string]any{"tags": []any{"*"}}, ref: "refs/tags/v1.0.0", want: true},
		{name: "wrong branch", refs: map[string]any{"branches": []any{"develop"}}, ref: "refs/heads/main", want: false},
		{name: "empty refs", refs: nil, ref: "refs/heads/main", want: false},
		{name: "unsupported ref", refs: map[string]any{"branches": []any{"main"}}, ref: "main", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := repoSyncAssetMatchesWebhookRef(tt.refs, tt.ref); got != tt.want {
				t.Fatalf("repoSyncAssetMatchesWebhookRef = %v, want %v", got, tt.want)
			}
		})
	}
}

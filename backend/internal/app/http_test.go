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
	"regexp"
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

func TestRollbackPointReadinessSQLIncludesPreviewOnlyFields(t *testing.T) {
	sql := rollbackPointReadinessSQL(20)
	for _, token := range []string{
		"false AS rollback_executable",
		"'read_only_preview' AS rollback_execution_mode",
		"AS rollback_readiness",
		"AS rollback_readiness_reason",
		"rp.status, '')='expired'",
		"rp.revision, '')=''",
		"THEN 'previewable'",
		"rollback point has revision metadata; execution remains disabled in this first version",
		"dt.namespace AS deployment_namespace",
		"dt.cluster_name AS deployment_cluster_name",
		"LIMIT 20",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("rollbackPointReadinessSQL missing %s", token)
		}
	}
}

func TestRollbackGuardrailSummary(t *testing.T) {
	empty := rollbackGuardrailSummary(nil)
	if empty["execution_enabled"] != false || empty["execution_mode"] != "read_only_preview" || empty["total"] != 0 {
		t.Fatalf("empty summary = %#v", empty)
	}

	summary := rollbackGuardrailSummary([]map[string]any{
		{
			"rollback_readiness":        "previewable",
			"rollback_executable":       false,
			"rollback_execution_mode":   "read_only_preview",
			"rollback_readiness_reason": "revision metadata is available",
		},
		{
			"rollback_readiness":  "blocked",
			"rollback_executable": false,
		},
	})
	if summary["execution_enabled"] != false || summary["execution_mode"] != "read_only_preview" {
		t.Fatalf("summary execution fields = %#v", summary)
	}
	if summary["previewable_count"] != 1 || summary["executable_count"] != 0 || summary["total"] != 2 {
		t.Fatalf("summary counts = %#v", summary)
	}
	if !strings.Contains(fmt.Sprint(summary["message"]), "disabled") {
		t.Fatalf("summary message = %#v", summary["message"])
	}

	executable := rollbackGuardrailSummary([]map[string]any{
		{
			"rollback_readiness":      "previewable",
			"rollback_executable":     true,
			"rollback_execution_mode": "approval_required",
		},
	})
	if executable["execution_enabled"] != true || executable["executable_count"] != 1 {
		t.Fatalf("executable summary = %#v", executable)
	}
	if !strings.Contains(fmt.Sprint(executable["message"]), "explicit approval") {
		t.Fatalf("executable summary message = %#v", executable["message"])
	}

	mixed := rollbackGuardrailSummary([]map[string]any{
		{"rollback_execution_mode": "read_only_preview"},
		{"rollback_execution_mode": "approval_required"},
	})
	if mixed["execution_mode"] != "mixed" {
		t.Fatalf("mixed execution mode = %#v", mixed)
	}
}

func TestDeploymentExecutionReadinessDryRun(t *testing.T) {
	ready := deploymentExecutionReadiness(map[string]any{
		"name":           "prod",
		"status":         "healthy",
		"cluster_name":   "prod-cluster",
		"namespace":      "billing",
		"argo_app_count": int64(2),
	})
	if ready["status"] != "planned" || ready["mode"] != "dry_run" || ready["execution_enabled"] != false || ready["external_call_made"] != false {
		t.Fatalf("ready deployment execution readiness = %#v", ready)
	}
	if ready["requires_approval"] != true || ready["execution_backend"] != "disabled" {
		t.Fatalf("ready deployment execution guardrails = %#v", ready)
	}
	steps := sliceOfMapsFromAny(ready["steps"])
	if len(steps) != 4 {
		t.Fatalf("ready deployment execution steps = %#v", steps)
	}
	for _, step := range steps {
		if step["execution"] != false {
			t.Fatalf("deployment execution step should be disabled: %#v", step)
		}
	}

	blocked := deploymentExecutionReadiness(map[string]any{
		"name":           "broken",
		"status":         "degraded",
		"cluster_name":   "",
		"namespace":      "",
		"argo_app_count": int64(0),
	})
	if blocked["status"] != "blocked" || blocked["execution_enabled"] != false {
		t.Fatalf("blocked deployment execution readiness = %#v", blocked)
	}
	reasons := stringSliceFromAny(blocked["blocked_reasons"])
	for _, want := range []string{"status needs review", "cluster name is missing", "namespace is missing", "no Argo apps"} {
		found := false
		for _, reason := range reasons {
			if strings.Contains(reason, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("blocked reasons missing %q in %#v", want, reasons)
		}
	}
}

func TestAssetInventorySQLIncludesCoreAssetTypes(t *testing.T) {
	sql := assetInventorySQL()
	for _, token := range []string{
		"'project' AS asset_type",
		"'project_template'",
		"'project_template_run'",
		"FROM project_template_runs ptr",
		"'step_count', jsonb_array_length(ptr.steps)",
		"'has_error', ptr.error_message <> ''",
		"'provider_account'",
		"'template_file'",
		"'repository'",
		"'git_remote'",
		"'operation_run'",
		"FROM operation_runs op",
		"'has_error', op.error <> ''",
		"'operation_approval'",
		"FROM operation_approvals oa",
		"'required_approval_count', oa.required_approval_count",
		"'approved_count', COALESCE(decision_counts.approved_count, 0)",
		"WHEN oa.status IN ('rejected', 'expired') THEN 'high'",
		"'operation_approval_rule'",
		"FROM operation_approval_rules oar",
		"'required_approval_count', oar.required_approval_count",
		"CASE WHEN oar.enabled THEN 'active' ELSE 'disabled' END",
		"'repo_sync'",
		"'webhook_connection'",
		"WHEN wc.last_delivery_status IN ('failed', 'rejected') THEN 'high'",
		"WHEN NOT wc.enabled THEN 'warning'",
		"'has_last_delivery_error', wc.last_delivery_error <> ''",
		"'webhook_event'",
		"FROM webhook_events we",
		"we.id::text",
		"'has_payload', we.payload <> '{}'::jsonb",
		"'has_result', we.result <> '{}'::jsonb",
		"'has_error', we.error_message <> ''",
		"'pipeline_run'",
		"'host'",
		"'ssh_command_run'",
		"FROM ssh_command_runs scr",
		"COALESCE(sm.name, 'SSH command run')",
		"'has_command', scr.command <> ''",
		"'has_stdout', scr.stdout <> ''",
		"'has_stderr', scr.stderr <> ''",
		"'has_error', scr.error_message <> ''",
		"'argo_connection'",
		"'deployment_target'",
		"'deployment_record'",
		"'rollback_point'",
		"'argo_app'",
		"'ai_runtime'",
		"'agent_task'",
		"FROM agent_tasks at",
		"'latest_plan_status', latest_plan.status",
		"'agent_tool_call'",
		"FROM agent_tool_calls atc",
		"'has_input', atc.input <> '{}'::jsonb",
		"'has_output', atc.output <> '{}'::jsonb",
		"'has_error', atc.error_message <> ''",
		"'worker_job'",
		"FROM worker_jobs wj",
		"'has_payload', wj.payload <> '{}'::jsonb",
		"'has_result', wj.result <> '{}'::jsonb",
		"'has_error', wj.error <> ''",
		"'node_agent'",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("assetInventorySQL missing %s", token)
		}
	}
	if regexp.MustCompile(`\boar\.metadata\b`).MatchString(sql) {
		t.Fatalf("operation approval rule metadata should not be exposed in assetInventorySQL")
	}
	for _, forbidden := range []*regexp.Regexp{
		regexp.MustCompile(`'input'\s*,\s*atc\.input`),
		regexp.MustCompile(`'output'\s*,\s*atc\.output`),
		regexp.MustCompile(`'error_message'\s*,\s*atc\.error_message`),
	} {
		if forbidden.MatchString(sql) {
			t.Fatalf("agent tool call sensitive payload should not be exposed in assetInventorySQL: %s", forbidden)
		}
	}
	for _, forbidden := range []*regexp.Regexp{
		regexp.MustCompile(`'last_delivery_error'\s*,\s*wc\.last_delivery_error`),
		regexp.MustCompile(`'payload'\s*,\s*we\.payload`),
		regexp.MustCompile(`'result'\s*,\s*we\.result`),
		regexp.MustCompile(`'error_message'\s*,\s*we\.error_message`),
	} {
		if forbidden.MatchString(sql) {
			t.Fatalf("webhook event sensitive payload should not be exposed in assetInventorySQL: %s", forbidden)
		}
	}
	for _, forbidden := range []*regexp.Regexp{
		regexp.MustCompile(`'payload'\s*,\s*wj\.payload`),
		regexp.MustCompile(`'result'\s*,\s*wj\.result`),
		regexp.MustCompile(`'error'\s*,\s*wj\.error`),
	} {
		if forbidden.MatchString(sql) {
			t.Fatalf("worker job sensitive payload should not be exposed in assetInventorySQL: %s", forbidden)
		}
	}
	for _, forbidden := range []*regexp.Regexp{
		regexp.MustCompile(`'command'\s*,\s*scr\.command`),
		regexp.MustCompile(`'stdout'\s*,\s*scr\.stdout`),
		regexp.MustCompile(`'stderr'\s*,\s*scr\.stderr`),
		regexp.MustCompile(`'error_message'\s*,\s*scr\.error_message`),
	} {
		if forbidden.MatchString(sql) {
			t.Fatalf("SSH command run sensitive payload should not be exposed in assetInventorySQL: %s", forbidden)
		}
	}
	for _, forbidden := range []*regexp.Regexp{
		regexp.MustCompile(`\bptr\.input\b`),
		regexp.MustCompile(`\bptr\.result\b`),
		regexp.MustCompile(`'error_message'\s*,`),
	} {
		if forbidden.MatchString(sql) {
			t.Fatalf("project template run sensitive payload should not be exposed in assetInventorySQL: %s", forbidden)
		}
	}
}

func TestAssetRelationInventorySQLIncludesAgentTaskEdges(t *testing.T) {
	sql := assetRelationInventorySQL()
	for _, token := range []string{
		"'project:' || p.id::text || ':owns:agent_task:' || at.id::text",
		"'agent_task:' || at.id::text || ':uses_runtime:ai_runtime:' || runtime.id::text",
		"'uses_runtime'",
		"'agent_task:' || at.id::text || ':records_tool_call:agent_tool_call:' || atc.id::text",
		"'operation_run:' || op.id::text || ':ran_tool_call:agent_tool_call:' || atc.id::text",
		"COALESCE(atc.project_id::text, at.project_id::text, '')",
		"JOIN agent_tasks at ON at.id=atc.agent_task_id",
		"'records_tool_call'",
		"'ran_tool_call'",
		"WHERE ar.project_id=at.project_id OR ar.project_id IS NULL",
		"CASE WHEN ar.status='verified' THEN 0 ELSE 1 END",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("assetRelationInventorySQL missing %s", token)
		}
	}
}

func TestAssetRelationInventorySQLIncludesOperationRunEdges(t *testing.T) {
	sql := assetRelationInventorySQL()
	for _, token := range []string{
		"'project:' || p.id::text || ':owns:operation_run:' || op.id::text",
		"'operation_run:' || op.id::text || ':dispatched_worker_job:worker_job:' || wj.id::text",
		"'worker_job:' || wj.id::text || ':assigned_to:worker_node:' || wn.id::text",
		"'project:' || p.id::text || ':owns:operation_approval:' || oa.id::text",
		"'operation_approval:' || oa.id::text || ':gates_operation:operation_run:' || op.id::text",
		"'operation_approval_rule:' || oar.id::text || ':governs:operation_approval:' || oa.id::text",
		"'operation_approval_rule:' || oar.id::text",
		"'governs'",
		"JOIN operation_approvals oa ON oa.approval_rule_id=oar.id",
		"'operation_approval:' || oa.id::text || ':targets:' || approval_resource.asset_id",
		"WHEN 'ssh_machine' THEN 'ssh_machine:' || oa.resource_id",
		"'git_remote:' || gr.id::text || ':triggered:operation_run:' || op.id::text",
		"'operation_run:' || op.id::text || ':ran_repo_sync:repo_sync:' || rsa.id::text",
		"'operation_run:' || op.id::text || ':used_source_remote:git_remote:' || source.id::text",
		"'operation_run:' || op.id::text || ':used_target_remote:git_remote:' || target.id::text",
		"'operation_run:' || op.id::text || ':tagged_remote:git_remote:' || target.id::text",
		"'operation_run:' || op.id::text || ':executed_on:ssh_machine:' || sm.id::text",
		"'operation_run:' || op.id::text || ':ran_ssh_command:ssh_command_run:' || scr.id::text",
		"'ssh_command_run:' || scr.id::text || ':executed_on:ssh_machine:' || sm.id::text",
		"'operation_run:' || op.id::text || ':executed_agent_task:agent_task:' || at.id::text",
		"'operation_run:' || op.id::text || ':synced_argo_connection:argo_connection:' || ac.id::text",
		"'operation_run:' || op.id::text || ':created_template_run:project_template_run:' || ptr.id::text",
		"'operation_run:' || op.id::text || ':created_from_template:project_template:' || pt.id::text",
		"'project:' || p.id::text || ':owns:project_template_run:' || ptr.id::text",
		"'project_template_run:' || ptr.id::text || ':instantiates:project_template:' || pt.id::text",
		"'project_template_run:' || ptr.id::text || ':produced_file:template_file:' || ptf.id::text",
		"'webhook_connection:' || wc.id::text || ':received:webhook_event:' || we.id::text",
		"'webhook_event:' || we.id::text || ':matched_repo_sync:repo_sync:' || rsa.id::text",
		"'webhook_event:' || we.id::text || ':triggered_operation:operation_run:' || op.id::text",
		"'webhook_connection:' || wc.id::text || ':triggered_operation:operation_run:' || op.id::text",
		"'owns_operation'",
		"'dispatched_worker_job'",
		"'assigned_to_worker_node'",
		"'owns_approval'",
		"'gates_operation'",
		"'targets'",
		"'triggered'",
		"'ran_repo_sync'",
		"'used_source_remote'",
		"'used_target_remote'",
		"'tagged_remote'",
		"'executed_on'",
		"'ran_ssh_command'",
		"'executed_agent_task'",
		"'synced_argo_connection'",
		"'created_template_run'",
		"'created_from_template'",
		"'owns_template_run'",
		"'instantiates_template'",
		"'produced_template_file'",
		"'received_webhook_event'",
		"'matched_repo_sync'",
		"'triggered_operation'",
		"JOIN operation_runs op ON op.project_id=p.id",
		"JOIN git_remotes gr ON gr.id=op.git_remote_id",
		"JOIN ssh_machines sm ON sm.id=scr.ssh_machine_id",
		"WHEN (op.input->>'agent_task_id') ~*",
		"THEN (op.input->>'agent_task_id')::uuid",
		"WHEN (op.input->>'argo_connection_id') ~*",
		"THEN (op.input->>'argo_connection_id')::uuid",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("assetRelationInventorySQL missing %s", token)
		}
	}
	for _, forbidden := range []*regexp.Regexp{
		regexp.MustCompile(`\bscr\.command\b\s*(,|\)|AS|\n)`),
		regexp.MustCompile(`\bscr\.stdout\b\s*(,|\)|AS|\n)`),
		regexp.MustCompile(`\bscr\.stderr\b\s*(,|\)|AS|\n)`),
		regexp.MustCompile(`\bscr\.error_message\b\s*(,|\)|AS|\n)`),
		regexp.MustCompile(`\bop\.input\b\s*(,|\)|AS|\n)`),
		regexp.MustCompile(`\bop\.result\b\s*(,|\)|AS|\n)`),
		regexp.MustCompile(`\bop\.error\b\s*(,|\)|AS|\n)`),
		regexp.MustCompile(`\bwe\.payload\b\s*(,|\)|AS|\n)`),
		regexp.MustCompile(`\bwc\.secret_token\b\s*(,|\)|AS|\n)`),
		regexp.MustCompile(`\bwc\.secret_ciphertext\b\s*(,|\)|AS|\n)`),
		regexp.MustCompile(`\bptr\.input\b\s*(,|\)|AS|\n)`),
		regexp.MustCompile(`\bptr\.result\b\s*(,|\)|AS|\n)`),
		regexp.MustCompile(`\bptr\.error_message\b\s*(,|\)|AS|\n)`),
		regexp.MustCompile(`\brsr\.stdout\b`),
		regexp.MustCompile(`\brsr\.stderr\b`),
		regexp.MustCompile(`\brtr\.stdout\b`),
		regexp.MustCompile(`\brtr\.stderr\b`),
		regexp.MustCompile(`\bat\.prompt\b`),
		regexp.MustCompile(`\bac\.config\b`),
		regexp.MustCompile(`\bsm\.metadata\b`),
	} {
		if forbidden.MatchString(sql) {
			t.Fatalf("assetRelationInventorySQL should not expose sensitive operation details matching %q", forbidden.String())
		}
	}
}

func TestAssetGraphNodesSQLIncludesVisibilityAndSearch(t *testing.T) {
	sql := assetGraphNodesSQL()
	for _, token := range []string{
		"FROM asset_inventory",
		"asset_relation_inventory AS",
		"relation_degree_endpoints AS",
		"relation_degrees AS",
		"ranked_asset_inventory AS",
		"outgoing_relation_count",
		"incoming_relation_count",
		"relation_count",
		"graph_rank",
		"WHEN ai.risk_level='high' THEN 300",
		"WHEN ai.risk_level='normal' THEN 100",
		"ELSE 0",
		"($1='' OR project_id=$1)",
		"($2='' OR asset_type=$2)",
		"name ILIKE $5",
		"pm.project_id::text=ranked_asset_inventory.project_id AND pm.user_id=$4",
		"ORDER BY graph_rank DESC, relation_count DESC, updated_at DESC",
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
		"'created_template_run'",
		"'owns_template_run'",
		"'instantiates_template'",
		"'produced_template_file'",
		"'records_tool_call'",
		"'ran_tool_call'",
		"'ran_ssh_command'",
		"'received_webhook_event'",
		"'matched_repo_sync'",
		"'dispatched_worker_job'",
		"'assigned_to_worker_node'",
		"FROM asset_relations ar",
		"ar.metadata->>'source'='manual'",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("assetRelationInventorySQL missing %s", token)
		}
	}
	for _, forbidden := range []*regexp.Regexp{
		regexp.MustCompile(`\bptr\.input\b`),
		regexp.MustCompile(`\bptr\.result\b`),
		regexp.MustCompile(`'error_message'\s*,`),
		regexp.MustCompile(`\bptf\.content\b`),
		regexp.MustCompile(`'input'\s*,\s*atc\.input`),
		regexp.MustCompile(`'output'\s*,\s*atc\.output`),
		regexp.MustCompile(`'error_message'\s*,\s*atc\.error_message`),
		regexp.MustCompile(`'payload'\s*,\s*we\.payload`),
		regexp.MustCompile(`'result'\s*,\s*we\.result`),
		regexp.MustCompile(`'error_message'\s*,\s*we\.error_message`),
		regexp.MustCompile(`'payload'\s*,\s*wj\.payload`),
		regexp.MustCompile(`'result'\s*,\s*wj\.result`),
		regexp.MustCompile(`'error'\s*,\s*wj\.error`),
	} {
		if forbidden.MatchString(sql) {
			t.Fatalf("project template run relation metadata should not expose sensitive payload: %s", forbidden)
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
		"created_at":    time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
		"metadata": map[string]any{
			"rotation_candidate_token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_NEXT",
			"next_token_env":               "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_OTHER",
			"note":                         "safe",
		},
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
	encoded, _ := json.Marshal(item)
	if strings.Contains(string(encoded), "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN") {
		t.Fatalf("sanitized account leaked token env: %s", encoded)
	}
	if strings.Contains(string(encoded), "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_NEXT") ||
		strings.Contains(string(encoded), "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_OTHER") {
		t.Fatalf("sanitized account leaked candidate token env: %s", encoded)
	}
	if status := mapFromAny(item["token_rotation_status"]); status["status"] == "" {
		t.Fatalf("token_rotation_status missing: %#v", item)
	}
	metadata := mapFromAny(item["metadata"])
	if metadata["note"] != "safe" {
		t.Fatalf("metadata note should be preserved without token env fields: %#v", metadata)
	}
	candidate := mapFromAny(item["token_rotation_candidate"])
	if candidate["safe"] != true || candidate["same_as_current"] != false {
		t.Fatalf("candidate status = %#v", candidate)
	}
}

func TestProviderAccountTokenRotationStatus(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		item map[string]any
		want string
		src  string
	}{
		{
			name: "fresh from rotation metadata",
			item: map[string]any{
				"token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN",
				"metadata":  map[string]any{"token_rotation": map[string]any{"rotated_at": now.AddDate(0, 0, -10).Format(time.RFC3339)}},
			},
			want: "fresh",
			src:  "token_rotation",
		},
		{
			name: "soon from created at fallback",
			item: map[string]any{
				"token_env":  "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_MAIN",
				"metadata":   map[string]any{},
				"created_at": now.AddDate(0, 0, -80),
			},
			want: "soon",
			src:  "created_at",
		},
		{
			name: "due from created at fallback",
			item: map[string]any{
				"token_env":  "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN",
				"metadata":   map[string]any{},
				"created_at": now.AddDate(0, 0, -120),
			},
			want: "due",
			src:  "created_at",
		},
		{
			name: "missing token env",
			item: map[string]any{
				"metadata":   map[string]any{},
				"created_at": now.AddDate(0, 0, -120),
			},
			want: "missing",
			src:  "unknown",
		},
		{
			name: "unknown without timestamps",
			item: map[string]any{
				"token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN",
				"metadata":  map[string]any{},
			},
			want: "unknown",
			src:  "unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := providerAccountTokenRotationStatus(tt.item, now)
			if got["status"] != tt.want || got["source"] != tt.src {
				t.Fatalf("status = %#v, want status=%s source=%s", got, tt.want, tt.src)
			}
			encoded, _ := json.Marshal(got)
			if strings.Contains(string(encoded), "ASSOPS_TEMPLATE_PROVIDER_TOKEN") {
				t.Fatalf("rotation status leaked token env: %s", encoded)
			}
		})
	}
}

func TestProviderAccountTokenRotationPlanSummaryDoesNotLeakTokenEnv(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	summary := providerAccountTokenRotationPlanSummary([]map[string]any{
		{
			"token_env":  "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN",
			"metadata":   map[string]any{},
			"created_at": now.AddDate(0, 0, -120),
		},
		{
			"token_env":  "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_MAIN",
			"metadata":   map[string]any{},
			"created_at": now.AddDate(0, 0, -80),
		},
		{
			"metadata":   map[string]any{},
			"created_at": now,
		},
		{
			"token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_UNKNOWN",
			"metadata":  map[string]any{},
		},
	}, now)
	if summary["total"] != 4 || summary["due"] != 1 || summary["soon"] != 1 ||
		summary["missing"] != 1 || summary["unknown"] != 1 || summary["action_required"] != 2 {
		t.Fatalf("summary = %#v", summary)
	}
	encoded, _ := json.Marshal(summary)
	if strings.Contains(string(encoded), "ASSOPS_TEMPLATE_PROVIDER_TOKEN") {
		t.Fatalf("rotation plan summary leaked token env: %s", encoded)
	}
	if !strings.Contains(fmt.Sprint(summary["next_action"]), "Rotate due or missing") {
		t.Fatalf("next action = %v", summary["next_action"])
	}
}

func TestProviderAccountTokenRotationPlanSummaryNextActions(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		rows []map[string]any
		want string
	}{
		{
			name: "empty",
			rows: nil,
			want: "No provider accounts configured.",
		},
		{
			name: "due",
			rows: []map[string]any{
				{
					"token_env":  "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN",
					"metadata":   map[string]any{},
					"created_at": now.AddDate(0, 0, -120),
				},
				{
					"token_env":  "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_MAIN",
					"metadata":   map[string]any{},
					"created_at": now.AddDate(0, 0, -10),
				},
			},
			want: "Rotate due provider token env values before external template provisioning.",
		},
		{
			name: "missing",
			rows: []map[string]any{{
				"metadata":   map[string]any{},
				"created_at": now,
			}},
			want: "Configure missing provider token env values before external template provisioning.",
		},
		{
			name: "soon",
			rows: []map[string]any{{
				"token_env":  "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_MAIN",
				"metadata":   map[string]any{},
				"created_at": now.AddDate(0, 0, -80),
			}},
			want: "Schedule provider token env rotation before the next due window.",
		},
		{
			name: "unknown",
			rows: []map[string]any{{
				"token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_UNKNOWN",
				"metadata":  map[string]any{},
			}},
			want: "Run a provider account check or rotate token env to establish rotation evidence.",
		},
		{
			name: "fresh",
			rows: []map[string]any{{
				"token_env":  "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_MAIN",
				"metadata":   map[string]any{},
				"created_at": now.AddDate(0, 0, -10),
			}},
			want: "Provider token rotation evidence is fresh.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := providerAccountTokenRotationPlanSummary(tt.rows, now)
			if summary["next_action"] != tt.want {
				t.Fatalf("next_action = %v, want %s", summary["next_action"], tt.want)
			}
		})
	}
}

func TestProviderAccountAutomatedRotationPlan(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	plan := providerAccountAutomatedRotationPlan([]map[string]any{
		{
			"id":            "github-due",
			"name":          "github due",
			"provider_type": "github",
			"token_env":     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_CURRENT",
			"metadata":      map[string]any{"rotation_candidate_token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_NEXT"},
			"created_at":    now.AddDate(0, 0, -120),
		},
		{
			"id":            "gitea-soon-missing-candidate",
			"name":          "gitea soon",
			"provider_type": "gitea",
			"token_env":     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_CURRENT",
			"metadata":      map[string]any{},
			"created_at":    now.AddDate(0, 0, -80),
		},
		{
			"id":            "github-fresh",
			"name":          "github fresh",
			"provider_type": "github",
			"token_env":     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_FRESH",
			"metadata":      map[string]any{},
			"created_at":    now.AddDate(0, 0, -10),
		},
		{
			"id":            "github-unsafe",
			"name":          "github unsafe",
			"provider_type": "github",
			"token_env":     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_CURRENT",
			"metadata":      map[string]any{"rotation_candidate_token_env": "BAD_TOKEN_ENV"},
			"created_at":    now.AddDate(0, 0, -120),
		},
		{
			"id":            "github-same",
			"name":          "github same",
			"provider_type": "github",
			"token_env":     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_CURRENT",
			"metadata":      map[string]any{"rotation_candidate_token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_CURRENT"},
			"created_at":    now.AddDate(0, 0, -120),
		},
	}, now)
	if plan["mode"] != "dry_run" || plan["automation_enabled"] != false || plan["external_call_made"] != false {
		t.Fatalf("plan should be dry-run only: %#v", plan)
	}
	if plan["execution_available"] != true {
		t.Fatalf("plan should expose ready execution availability: %#v", plan)
	}
	if plan["ready"] != 1 || plan["blocked"] != 3 || plan["not_needed"] != 1 {
		t.Fatalf("plan counts = %#v", plan)
	}
	items := sliceOfMapsFromAny(plan["items"])
	byID := map[string]map[string]any{}
	for _, item := range items {
		byID[fmt.Sprint(item["provider_account_id"])] = item
	}
	if byID["github-due"]["status"] != "ready" {
		t.Fatalf("github due should be ready: %#v", byID["github-due"])
	}
	if byID["gitea-soon-missing-candidate"]["status"] != "blocked" {
		t.Fatalf("missing candidate should be blocked: %#v", byID["gitea-soon-missing-candidate"])
	}
	if byID["github-fresh"]["status"] != "not_needed" {
		t.Fatalf("fresh account should not need rotation: %#v", byID["github-fresh"])
	}
	if byID["github-unsafe"]["status"] != "blocked" || byID["github-same"]["status"] != "blocked" {
		t.Fatalf("unsafe/same candidates should be blocked: %#v %#v", byID["github-unsafe"], byID["github-same"])
	}
	if !strings.Contains(fmt.Sprint(byID["github-unsafe"]["blocked_reason"]), "not allowed") {
		t.Fatalf("unsafe candidate should explain allowlist failure: %#v", byID["github-unsafe"])
	}
	encoded, _ := json.Marshal(plan)
	for _, leak := range []string{
		"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_CURRENT",
		"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_NEXT",
		"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_CURRENT",
		"BAD_TOKEN_ENV",
	} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("automated rotation plan leaked %q: %s", leak, encoded)
		}
	}
}

func TestProviderAccountAutomatedRotationExecutionCandidates(t *testing.T) {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	items := []map[string]any{
		{
			"id":            "github-ready",
			"name":          "github ready",
			"provider_type": "github",
			"token_env":     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_CURRENT",
			"metadata":      map[string]any{"rotation_candidate_token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_NEXT"},
			"created_at":    now.AddDate(0, 0, -120),
		},
		{
			"id":            "github-fresh",
			"name":          "github fresh",
			"provider_type": "github",
			"token_env":     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_FRESH",
			"metadata":      map[string]any{"rotation_candidate_token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_OTHER"},
			"created_at":    now.AddDate(0, 0, -10),
		},
		{
			"id":            "gitea-unsafe",
			"name":          "gitea unsafe",
			"provider_type": "gitea",
			"token_env":     "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_CURRENT",
			"metadata":      map[string]any{"rotation_candidate_token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_WRONG"},
			"created_at":    now.AddDate(0, 0, -120),
		},
	}
	candidates := providerAccountAutomatedRotationExecutionCandidates(items, now)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %#v", candidates)
	}
	if rawStringFromMap(candidates[0].account, "id") != "github-ready" ||
		candidates[0].tokenEnv != "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_NEXT" ||
		providerAccountAutomatedRotationPlanItem(candidates[0].account, now)["status"] != "ready" {
		t.Fatalf("candidate = %#v", candidates[0])
	}
	plan := providerAccountAutomatedRotationPlan(items, now)
	encoded, _ := json.Marshal(plan)
	for _, leak := range []string{
		"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_CURRENT",
		"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_NEXT",
		"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_CURRENT",
		"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_WRONG",
	} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("execution plan leaked %q: %s", leak, encoded)
		}
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

func TestProjectTemplateProviderReviewApprovalPayload(t *testing.T) {
	t.Setenv("ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_ACCOUNT", "account-token")
	plan := templateProviderReviewExecutionPlan("github", map[string]any{
		"mode":            "pull_request",
		"provider_type":   "github",
		"proposed_branch": "assops/template/demo-main",
		"target_branch":   "main",
	})
	run := map[string]any{
		"id":         "11111111-1111-1111-1111-111111111111",
		"project_id": "22222222-2222-2222-2222-222222222222",
		"result": map[string]any{
			"template_files": []map[string]any{
				{"id": "33333333-3333-3333-3333-333333333333", "path": "README.md", "kind": "text", "status": "planned", "content": "do-not-include"},
			},
			"details": map[string]any{
				"repository_reconciliation": map[string]any{
					"credential_strategy": map[string]any{
						"mode":                      "provider_account_token_env",
						"provider_account_attached": true,
						"token_env_configured":      true,
						"token_env_present":         true,
						"token_stored":              false,
						"external_call_made":        false,
					},
					"provider_review_readiness": map[string]any{
						"execution_plan": plan,
					},
				},
			},
		},
	}
	payload, err := projectTemplateProviderReviewApprovalPayload(run)
	if err != nil {
		t.Fatalf("projectTemplateProviderReviewApprovalPayload: %v", err)
	}
	if payload["kind"] != "project_template_provider_review_execute" ||
		payload["project_template_run_id"] != "11111111-1111-1111-1111-111111111111" ||
		payload["provider_api_call_made"] != false ||
		payload["provider_api_mutation"] != "disabled" {
		t.Fatalf("payload = %#v", payload)
	}
	guardrail := mapFromAny(payload["execution_guardrail"])
	if guardrail["execution_mode"] != "disabled" ||
		guardrail["execution_enabled"] != false ||
		guardrail["execution_enabled_config"] != false ||
		guardrail["provider_api_call_made"] != false {
		t.Fatalf("payload guardrail = %#v", guardrail)
	}
	if !containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "provider_review_execution_enabled") {
		t.Fatalf("payload guardrail blocked reasons = %#v", guardrail)
	}
	if containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "starter_file_payload_staged") {
		t.Fatalf("starter file staging should be ready in approval payload: %#v", guardrail)
	}
	credential := mapFromAny(payload["credential_strategy"])
	if credential["mode"] != "provider_account_token_env" ||
		credential["provider_account_attached"] != true ||
		credential["token_env_configured"] != true ||
		credential["token_env_present"] != true ||
		credential["token_stored"] != false {
		t.Fatalf("credential strategy = %#v", credential)
	}
	starterPayload := mapFromAny(payload["starter_file_payload"])
	if starterPayload["status"] != "ready" || starterPayload["file_count"] != 1 || starterPayload["content_included"] != false {
		t.Fatalf("starter file payload = %#v", starterPayload)
	}
	starterFiles := sliceOfMapsFromAny(starterPayload["files"])
	if len(starterFiles) != 1 || starterFiles[0]["path"] != "README.md" {
		t.Fatalf("starter file summaries = %#v", starterFiles)
	}
	apiPlan := mapFromAny(payload["provider_api_request_plan"])
	if apiPlan["status"] != "ready" ||
		apiPlan["payload_redacted"] != true ||
		apiPlan["contains_token"] != false ||
		apiPlan["contains_file_content"] != false ||
		apiPlan["provider_api_call_made"] != false ||
		apiPlan["provider_api_mutation"] != "disabled" {
		t.Fatalf("provider api request plan = %#v", apiPlan)
	}
	operations := sliceOfMapsFromAny(apiPlan["operations"])
	if len(operations) != 3 || operations[0]["name"] != "create_branch_ref" || operations[1]["name"] != "commit_starter_files" || operations[2]["name"] != "open_review_request" {
		t.Fatalf("provider api request plan operations = %#v", operations)
	}
	for _, operation := range operations {
		if operation["api_call"] != false || operation["contains_token"] != false || operation["contains_file_content"] != false {
			t.Fatalf("provider api request plan operation should be redacted/no-call: %#v", operation)
		}
	}
	reconciliation := mapFromAny(payload["provider_review_reconciliation"])
	if reconciliation["status"] != "blocked" ||
		reconciliation["adapter_status"] != "missing" ||
		reconciliation["external_call_made"] != false ||
		reconciliation["provider_api_mutation"] != "disabled" {
		t.Fatalf("provider review reconciliation = %#v", reconciliation)
	}
	if !containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_review_api_adapter") {
		t.Fatalf("provider review reconciliation blocked reasons = %#v", reconciliation)
	}
	if containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "starter_file_payload_staged") ||
		containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_api_request_plan_ready") ||
		containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_credential_configured") ||
		containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_token_env_present") {
		t.Fatalf("provider review reconciliation should see staged payload, ready request plan, and credential preflight: %#v", reconciliation)
	}
	reconcileOperations := sliceOfMapsFromAny(reconciliation["operations"])
	if len(reconcileOperations) != 3 || reconcileOperations[0]["endpoint_key"] != "github.create_branch_ref" {
		t.Fatalf("provider review reconciliation operations = %#v", reconcileOperations)
	}
	request := mapFromAny(payload["execution_request"])
	if request["status"] != "approval_ready" ||
		request["approval_action"] != templateProviderReviewExecuteApprovalAction ||
		request["payload_redacted"] != true ||
		request["contains_token"] != false {
		t.Fatalf("execution request = %#v", request)
	}
	encoded, _ := json.Marshal(payload)
	for _, leak := range []string{"ASSOPS_TEMPLATE_PROVIDER_TOKEN", "secret-token", "api_base_url", "do-not-include"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("provider review approval payload leaked %q: %s", leak, encoded)
		}
	}

	blockedRun := map[string]any{
		"id": "11111111-1111-1111-1111-111111111111",
		"result": map[string]any{
			"details": map[string]any{
				"repository_reconciliation": map[string]any{
					"provider_review_readiness": map[string]any{
						"execution_plan": map[string]any{
							"execution_request": map[string]any{"status": "blocked"},
						},
					},
				},
			},
		},
	}
	if _, err := projectTemplateProviderReviewApprovalPayload(blockedRun); err == nil {
		t.Fatal("blocked provider review execution request should not build an approval payload")
	}
}

func TestProjectTemplateProviderReviewApprovalPayloadUsesRuntimeGuardrailConfig(t *testing.T) {
	plan := templateProviderReviewExecutionPlan("github", map[string]any{
		"mode":            "pull_request",
		"provider_type":   "github",
		"proposed_branch": "assops/template/demo-main",
		"target_branch":   "main",
	})
	payload, err := projectTemplateProviderReviewApprovalPayloadForConfig(map[string]any{
		"id":         "11111111-1111-1111-1111-111111111111",
		"project_id": "22222222-2222-2222-2222-222222222222",
		"result": map[string]any{
			"template_files": []map[string]any{
				{"id": "33333333-3333-3333-3333-333333333333", "path": "README.md", "kind": "text", "status": "planned"},
			},
			"details": map[string]any{
				"repository_reconciliation": map[string]any{
					"provider_review_readiness": map[string]any{"execution_plan": plan},
				},
			},
		},
	}, true)
	if err != nil {
		t.Fatalf("projectTemplateProviderReviewApprovalPayloadForConfig: %v", err)
	}
	guardrail := mapFromAny(payload["execution_guardrail"])
	if guardrail["execution_mode"] != "adapter_blocked" || guardrail["execution_enabled_config"] != true || guardrail["execution_enabled"] != false {
		t.Fatalf("runtime guardrail should reflect enabled config while staying blocked: %#v", guardrail)
	}
	apiPlan := mapFromAny(payload["provider_api_request_plan"])
	if apiPlan["status"] != "ready" || apiPlan["file_count"] != 1 {
		t.Fatalf("runtime api request plan = %#v", apiPlan)
	}
	if !containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "provider_review_api_adapter") {
		t.Fatalf("runtime guardrail should remain adapter-blocked: %#v", guardrail)
	}
}

func TestProjectTemplateStarterFilePayloadSummaryBlocked(t *testing.T) {
	missing := projectTemplateStarterFilePayloadSummary(map[string]any{"result": map[string]any{}})
	if missing["status"] != "blocked" || starterFilePayloadReady(missing) {
		t.Fatalf("missing files should block starter payload: %#v", missing)
	}
	unsafe := projectTemplateStarterFilePayloadSummary(map[string]any{
		"result": map[string]any{
			"template_files": []map[string]any{
				{"path": "../secret.txt", "kind": "text", "content": "do-not-include"},
				{"path": "", "kind": "text", "content": "do-not-include"},
			},
		},
	})
	if unsafe["status"] != "blocked" || starterFilePayloadReady(unsafe) {
		t.Fatalf("unsafe files should block starter payload: %#v", unsafe)
	}
	encoded, _ := json.Marshal(unsafe)
	if strings.Contains(string(encoded), "do-not-include") {
		t.Fatalf("blocked starter payload leaked content: %s", encoded)
	}
}

func TestExecuteApprovedOperationProviderReviewIsAuditOnly(t *testing.T) {
	server := &Server{cfg: Config{ProviderReviewExecutionEnabled: true}}
	result, operationID, err := server.executeApprovedOperation(context.Background(), nil, map[string]any{
		"requested_by": "11111111-1111-1111-1111-111111111111",
		"request_payload": map[string]any{
			"kind":                    "project_template_provider_review_execute",
			"project_template_run_id": "22222222-2222-2222-2222-222222222222",
			"execution_request": map[string]any{
				"status":                "approval_ready",
				"provider_type":         "github",
				"review_kind":           "pull_request",
				"source_branch":         "assops/template/demo-main",
				"target_branch":         "main",
				"provider_api_mutation": "disabled",
			},
			"credential_strategy": map[string]any{
				"mode":                      "provider_account_token_env",
				"provider_account_attached": true,
				"token_env_configured":      true,
				"token_env_present":         true,
				"token_stored":              false,
				"external_call_made":        false,
			},
			"starter_file_payload": map[string]any{
				"status":           "ready",
				"file_count":       1,
				"content_included": false,
				"payload_redacted": true,
				"files": []map[string]any{
					{"id": "33333333-3333-3333-3333-333333333333", "path": "README.md", "kind": "text", "status": "planned", "content": "forged-content"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("executeApprovedOperation: %v", err)
	}
	if operationID != "" {
		t.Fatalf("provider review approval should not create an operation id, got %q", operationID)
	}
	if result["provider_api_call_made"] != false ||
		result["provider_api_mutation"] != "disabled" ||
		result["execution_enabled"] != false {
		t.Fatalf("provider review approval result should remain audit-only: %#v", result)
	}
	guardrail := mapFromAny(result["execution_guardrail"])
	if guardrail["execution_mode"] != "adapter_blocked" ||
		guardrail["execution_enabled_config"] != true ||
		guardrail["branch_creation_allowed"] != false ||
		guardrail["review_request_allowed"] != false {
		t.Fatalf("provider review execution guardrail should stay blocked: %#v", guardrail)
	}
	if !containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "provider_review_api_adapter") ||
		containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "starter_file_payload_staged") {
		t.Fatalf("provider review execution blocked reasons = %#v", guardrail)
	}
	starterPayload := mapFromAny(result["starter_file_payload"])
	if starterPayload["status"] != "ready" || starterPayload["content_included"] != false {
		t.Fatalf("provider review execution starter file payload = %#v", starterPayload)
	}
	apiPlan := mapFromAny(result["provider_api_request_plan"])
	if apiPlan["status"] != "ready" ||
		apiPlan["provider_api_call_made"] != false ||
		apiPlan["provider_api_mutation"] != "disabled" ||
		apiPlan["contains_file_content"] != false {
		t.Fatalf("provider review execution api request plan = %#v", apiPlan)
	}
	reconciliation := mapFromAny(result["provider_review_reconciliation"])
	if reconciliation["status"] != "blocked" ||
		reconciliation["adapter_status"] != "missing" ||
		reconciliation["external_call_made"] != false ||
		reconciliation["provider_api_mutation"] != "disabled" {
		t.Fatalf("provider review execution reconciliation = %#v", reconciliation)
	}
	if !containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_review_api_adapter") {
		t.Fatalf("provider review execution reconciliation blocked reasons = %#v", reconciliation)
	}
	if containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_credential_configured") ||
		containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_token_env_present") {
		t.Fatalf("provider review execution reconciliation should preserve credential preflight: %#v", reconciliation)
	}
	encoded, _ := json.Marshal(result)
	for _, leak := range []string{"forged-content", "api_base_url", "secret-token"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("provider review execution result leaked %q: %s", leak, encoded)
		}
	}
}

func TestOperationApprovalPayloadAuditProviderReviewRedactsSensitiveFields(t *testing.T) {
	approval := map[string]any{
		"request_payload": map[string]any{
			"kind":                    "project_template_provider_review_execute",
			"project_template_run_id": "22222222-2222-2222-2222-222222222222",
			"project_id":              "11111111-1111-1111-1111-111111111111",
			"execution_request": map[string]any{
				"status":          "approval_ready",
				"approval_action": templateProviderReviewExecuteApprovalAction,
				"resource_type":   "project_template_run",
				"provider_type":   "github",
				"review_kind":     "pull_request",
				"source_branch":   "assops/template/demo-main",
				"target_branch":   "main",
				"token":           "secret-token",
			},
			"execution_guardrail": map[string]any{
				"execution_mode":           "adapter_blocked",
				"execution_enabled_config": true,
				"provider_type":            "github",
				"review_kind":              "pull_request",
				"source_branch":            "assops/template/demo-main",
				"target_branch":            "main",
				"api_base_url":             "https://api.github.example.test",
				"blocked_reasons":          []any{"provider_review_api_adapter"},
				"gates": []map[string]any{
					{"gate": "provider_review_api_adapter", "status": "blocked", "message": "adapter blocked", "token": "secret-token"},
				},
			},
			"credential_strategy": map[string]any{
				"mode":                      "provider_account_token_env",
				"provider_account_attached": true,
				"token_env_configured":      true,
				"token_env_present":         true,
				"token_stored":              true,
				"external_call_made":        true,
				"token_env":                 "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_SECRET",
				"token":                     "secret-token",
			},
			"starter_file_payload": map[string]any{
				"status":           "ready",
				"file_count":       1,
				"content_included": false,
				"files": []map[string]any{
					{"id": "33333333-3333-3333-3333-333333333333", "path": "README.md", "kind": "text", "status": "planned", "content": "do-not-include"},
				},
			},
			"provider_api_request_plan": map[string]any{
				"status":                "ready",
				"mode":                  "redacted_request_plan",
				"provider_type":         "github",
				"review_kind":           "pull_request",
				"source_branch":         "assops/template/demo-main",
				"target_branch":         "main",
				"file_count":            1,
				"api_base_url":          "https://api.github.example.test",
				"owner":                 "acme",
				"repo":                  "secret-repo",
				"provider_api_mutation": "enabled",
				"operations": []map[string]any{
					{"name": "commit_starter_files", "method": "PUT", "endpoint_key": "github.commit_files", "payload_shape": "content_redacted_file_batch", "file_count": 1, "url": "https://api.github.example.test/repos/acme/secret-repo", "content": "do-not-include", "api_call": true},
				},
			},
			"provider_review_reconciliation": map[string]any{
				"status":                "ready",
				"mode":                  "preflight_reconciliation",
				"provider_type":         "github",
				"review_kind":           "pull_request",
				"adapter_status":        "ready",
				"external_call_made":    true,
				"provider_api_mutation": "enabled",
				"api_base_url":          "https://api.github.example.test",
				"blocked_reasons":       []any{"provider_review_api_adapter"},
				"gates":                 []map[string]any{{"gate": "provider_review_api_adapter", "status": "blocked", "token": "secret-token"}},
				"operations":            []map[string]any{{"name": "open_review_request", "endpoint_key": "github.open_review", "status": "ready", "url": "https://api.github.example.test/repos/acme/secret-repo/pulls", "external_call_made": true}},
			},
			"approval_result": map[string]any{
				"execution_enabled":         true,
				"provider_api_call_made":    true,
				"provider_api_mutation":     "enabled",
				"credential_strategy":       map[string]any{"token_stored": true, "external_call_made": true, "token_env": "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_SECRET", "token": "secret-token"},
				"starter_file_payload":      map[string]any{"files": []map[string]any{{"path": "README.md", "content": "do-not-include"}}},
				"provider_api_request_plan": map[string]any{"operations": []map[string]any{{"url": "https://api.github.example.test", "content": "do-not-include", "api_call": true}}},
				"provider_review_reconciliation": map[string]any{
					"status":                "ready",
					"adapter_status":        "ready",
					"external_call_made":    true,
					"provider_api_mutation": "enabled",
					"operations":            []map[string]any{{"url": "https://api.github.example.test", "external_call_made": true}},
				},
			},
		},
	}
	audit := operationApprovalPayloadAudit(approval)
	if audit["kind"] != "project_template_provider_review_execute" ||
		audit["provider_api_call_made"] != false ||
		audit["provider_api_mutation"] != "disabled" {
		t.Fatalf("audit summary = %#v", audit)
	}
	result := mapFromAny(audit["approval_result"])
	if result["execution_enabled"] != false || result["provider_api_call_made"] != false || result["provider_api_mutation"] != "disabled" {
		t.Fatalf("approval result audit should force disabled/no-call: %#v", result)
	}
	reconciliation := mapFromAny(audit["provider_review_reconciliation"])
	if reconciliation["external_call_made"] != false || reconciliation["provider_api_mutation"] != "disabled" {
		t.Fatalf("provider review reconciliation audit should force disabled/no-call: %#v", reconciliation)
	}
	credential := mapFromAny(audit["credential_strategy"])
	if credential["token_stored"] != false || credential["external_call_made"] != false || credential["token_env_present"] != true {
		t.Fatalf("credential audit should force no stored token/no external call while preserving safe presence: %#v", credential)
	}
	injectedCredential := sanitizedProviderReviewCredentialStrategy(map[string]any{
		"provider_account_attached": "yes",
		"token_env_configured":      "true",
		"token_env_present":         1,
		"token_stored":              true,
		"external_call_made":        true,
	})
	if injectedCredential["provider_account_attached"] != false ||
		injectedCredential["token_env_configured"] != false ||
		injectedCredential["token_env_present"] != false ||
		injectedCredential["token_stored"] != false ||
		injectedCredential["external_call_made"] != false {
		t.Fatalf("credential sanitizer should only trust bool values and force safe flags: %#v", injectedCredential)
	}
	resultReconciliation := mapFromAny(result["provider_review_reconciliation"])
	if resultReconciliation["external_call_made"] != false || resultReconciliation["provider_api_mutation"] != "disabled" {
		t.Fatalf("approval result reconciliation audit should force disabled/no-call: %#v", resultReconciliation)
	}
	encoded, _ := json.Marshal(audit)
	for _, leak := range []string{"secret-token", "do-not-include", "api.github.example.test", "secret-repo", "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_SECRET", `"api_call":true`, `"enabled"`} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("approval payload audit leaked %q: %s", leak, encoded)
		}
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
			"source_provider":               "gitea",
			"target_provider":               "github",
			"source_last_sync_status":       "completed",
			"target_last_sync_status":       "failed",
			"active_runs":                   int64(2),
			"failed_runs_7d":                int64(6),
			"webhook_failures_7d":           int64(1),
			"github_runs_24h":               int64(55),
			"provider_pair_active_runs":     int64(4),
			"provider_pair_runs_24h":        int64(20),
			"provider_pair_failed_runs_24h": int64(2),
			"last_webhook_error":            "bad signature",
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
	if !strings.Contains(fmt.Sprint(byName["sync capacity"]["threshold"]), "warning >= 1 active runs") {
		t.Fatalf("sync capacity threshold = %#v", byName["sync capacity"]["threshold"])
	}
	if byName["7d sync failures"]["severity"] != "danger" {
		t.Fatalf("7d sync failures severity = %v", byName["7d sync failures"]["severity"])
	}
	if !strings.Contains(fmt.Sprint(byName["7d sync failures"]["threshold"]), "warning >= 1 failures") {
		t.Fatalf("7d sync failures threshold = %#v", byName["7d sync failures"]["threshold"])
	}
	if byName["webhook delivery"]["severity"] != "warning" || !strings.Contains(fmt.Sprint(byName["webhook delivery"]["detail"]), "bad signature") {
		t.Fatalf("webhook signal = %#v", byName["webhook delivery"])
	}
	if !strings.Contains(fmt.Sprint(byName["webhook delivery"]["threshold"]), "danger >= 3 failed events") {
		t.Fatalf("webhook threshold = %#v", byName["webhook delivery"]["threshold"])
	}
	if byName["GitHub Actions volume"]["severity"] != "warning" {
		t.Fatalf("GitHub Actions volume severity = %v", byName["GitHub Actions volume"]["severity"])
	}
	if !strings.Contains(fmt.Sprint(byName["GitHub Actions volume"]["threshold"]), "warning >= 50 runs") {
		t.Fatalf("GitHub Actions volume threshold = %#v", byName["GitHub Actions volume"]["threshold"])
	}
	if byName["provider pair pressure"]["severity"] != "warning" || !strings.Contains(fmt.Sprint(byName["provider pair pressure"]["detail"]), "gitea -> github") {
		t.Fatalf("provider pair pressure signal = %#v", byName["provider pair pressure"])
	}
	if !strings.Contains(fmt.Sprint(byName["provider pair pressure"]["threshold"]), "active warning >= 3") {
		t.Fatalf("provider pair pressure threshold = %#v", byName["provider pair pressure"]["threshold"])
	}
	if byName["asset state"]["status"] != "disabled" {
		t.Fatalf("asset state signal = %#v", byName["asset state"])
	}
}

func TestRepoSyncCapacityThresholdDetail(t *testing.T) {
	got := thresholdDetail(2, 4, "items")
	if got != "warning >= 2 items / danger >= 4 items" {
		t.Fatalf("thresholdDetail = %q", got)
	}
}

func TestRepoSyncProviderPairPressureSeverity(t *testing.T) {
	cases := []struct {
		name        string
		active      int64
		failures24h int64
		want        string
	}{
		{name: "empty", want: "ok"},
		{name: "failure warning", failures24h: int64(repoSyncCapacityPairFailureWarningThreshold), want: "warning"},
		{name: "failure danger", failures24h: int64(repoSyncCapacityPairFailureDangerThreshold), want: "danger"},
		{name: "active danger", active: int64(repoSyncCapacityPairActiveDangerThreshold), want: "danger"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			signals := repoSyncCapacitySignals(
				map[string]any{"id": "asset-1", "enabled": true},
				map[string]any{
					"source_provider":               "gitea",
					"target_provider":               "github",
					"source_last_sync_status":       "completed",
					"target_last_sync_status":       "completed",
					"provider_pair_active_runs":     tc.active,
					"provider_pair_runs_24h":        tc.active + tc.failures24h,
					"provider_pair_failed_runs_24h": tc.failures24h,
				},
				"source-1",
				"target-1",
			)
			byName := map[string]map[string]any{}
			for _, signal := range signals {
				byName[fmt.Sprint(signal["name"])] = signal
			}
			if byName["provider pair pressure"]["severity"] != tc.want {
				t.Fatalf("provider pair pressure severity = %v, want %s", byName["provider pair pressure"]["severity"], tc.want)
			}
		})
	}
}

func TestRepoSyncCapacitySignalsSQLIncludesProviderPairPressure(t *testing.T) {
	sql := repoSyncAssetCapacitySQL()
	for _, token := range []string{
		"provider_pair_active_runs",
		"provider_pair_runs_24h",
		"provider_pair_failed_runs_24h",
		"LEFT JOIN LATERAL",
		"pair_source.provider_type=source.provider_type",
		"pair_target.provider_type=target.provider_type",
	} {
		if !strings.Contains(sql, token) {
			t.Fatalf("repo sync capacity SQL/source missing %q", token)
		}
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

func TestWebhookCallbackRehearsalReadiness(t *testing.T) {
	cases := []struct {
		name        string
		baseURL     string
		row         map[string]any
		wantStatus  string
		wantMessage string
	}{
		{
			name:    "ready with public origin",
			baseURL: "https://assops.example.com",
			row: map[string]any{
				"provider":         "gitea",
				"webhook_url":      "https://assops.example.com/api/webhooks/gitea/hook-1",
				"enabled":          true,
				"source_remote_id": "remote-1",
				"event_types":      []any{"push"},
				"failures_7d":      0,
			},
			wantStatus:  "ready",
			wantMessage: "local prerequisites are ready",
		},
		{
			name:    "localhost blocks rehearsal",
			baseURL: "http://localhost:8080",
			row: map[string]any{
				"provider":         "gitea",
				"webhook_url":      "http://localhost:8080/api/webhooks/gitea/hook-1",
				"enabled":          true,
				"source_remote_id": "remote-1",
				"event_types":      []any{"push"},
			},
			wantStatus:  "blocked",
			wantMessage: "ASSOPS_GATEWAY_URL",
		},
		{
			name:    "private ip blocks rehearsal",
			baseURL: "http://192.168.1.10",
			row: map[string]any{
				"provider":         "gitea",
				"webhook_url":      "http://192.168.1.10/api/webhooks/gitea/hook-1",
				"enabled":          true,
				"source_remote_id": "remote-1",
				"event_types":      []any{"push"},
			},
			wantStatus:  "blocked",
			wantMessage: "ASSOPS_GATEWAY_URL",
		},
		{
			name:    "internal hostname blocks rehearsal",
			baseURL: "https://assops.svc.cluster.local",
			row: map[string]any{
				"provider":         "gitea",
				"webhook_url":      "https://assops.svc.cluster.local/api/webhooks/gitea/hook-1",
				"enabled":          true,
				"source_remote_id": "remote-1",
				"event_types":      []any{"push"},
			},
			wantStatus:  "blocked",
			wantMessage: "ASSOPS_GATEWAY_URL",
		},
		{
			name:    "single label hostname blocks rehearsal",
			baseURL: "https://private-vpn-host",
			row: map[string]any{
				"provider":         "gitea",
				"webhook_url":      "https://private-vpn-host/api/webhooks/gitea/hook-1",
				"enabled":          true,
				"source_remote_id": "remote-1",
				"event_types":      []any{"push"},
			},
			wantStatus:  "blocked",
			wantMessage: "ASSOPS_GATEWAY_URL",
		},
		{
			name:    "unsupported scheme blocks rehearsal",
			baseURL: "ftp://assops.example.com",
			row: map[string]any{
				"provider":         "gitea",
				"webhook_url":      "ftp://assops.example.com/api/webhooks/gitea/hook-1",
				"enabled":          true,
				"source_remote_id": "remote-1",
				"event_types":      []any{"push"},
			},
			wantStatus:  "blocked",
			wantMessage: "ASSOPS_GATEWAY_URL",
		},
		{
			name:    "disabled and failed delivery block rehearsal",
			baseURL: "https://assops.example.com",
			row: map[string]any{
				"provider":             "github",
				"webhook_url":          "https://assops.example.com/api/webhooks/github/hook-1",
				"enabled":              false,
				"source_remote_id":     "remote-1",
				"event_types":          []any{"workflow_run"},
				"failures_7d":          int64(2),
				"last_delivery_status": "failed",
			},
			wantStatus:  "blocked",
			wantMessage: "last delivery was failed",
		},
		{
			name:    "missing source and event types block rehearsal",
			baseURL: "https://assops.example.com",
			row: map[string]any{
				"provider":    "gitea",
				"webhook_url": "https://assops.example.com/api/webhooks/gitea/hook-1",
				"enabled":     true,
			},
			wantStatus:  "blocked",
			wantMessage: "source remote is missing",
		},
		{
			name:    "zero source and empty event types block rehearsal",
			baseURL: "https://assops.example.com",
			row: map[string]any{
				"provider":         "gitea",
				"webhook_url":      "https://assops.example.com/api/webhooks/gitea/hook-1",
				"enabled":          true,
				"source_remote_id": 0,
				"event_types":      []any{},
			},
			wantStatus:  "blocked",
			wantMessage: "event types are missing",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := webhookCallbackRehearsalReadiness(tc.row, tc.baseURL)
			if got["status"] != tc.wantStatus || !strings.Contains(fmt.Sprint(got["message"]), tc.wantMessage) {
				t.Fatalf("webhookCallbackRehearsalReadiness = %#v; want status %q message containing %q", got, tc.wantStatus, tc.wantMessage)
			}
			if got["external_call_made"] != false {
				t.Fatalf("readiness must not claim external callback rehearsal was performed: %#v", got)
			}
		})
	}
}

func TestAnnotateWebhookCallbackReadinessAllowsNilItems(t *testing.T) {
	annotateWebhookCallbackReadiness(nil, "https://assops.example.com")
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

func TestListOperationApprovalsDoesNotReturnRequestPayload(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	server := &Server{store: &Store{DB: sqlx.NewDb(db, "sqlmock")}}
	mock.ExpectQuery(`(?s)UPDATE operation_approvals.*RETURNING \*`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`(?s)SELECT\s+oa\.id,.*FROM operation_approvals oa`).
		WithArgs(true, "admin-1", "", "", "", "", "", "", "", "admin-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id",
			"project_id",
			"operation_run_id",
			"approval_rule_id",
			"resource_type",
			"resource_id",
			"action",
			"title",
			"status",
			"required_approver_roles",
			"required_approval_count",
			"notification_channels",
			"escalation_after_minutes",
			"escalation_channels",
			"last_escalated_at",
			"escalation_count",
			"notification_status",
			"requested_by",
			"decided_by",
			"decision_reason",
			"decided_at",
			"expires_at",
			"expired_at",
			"created_at",
			"updated_at",
			"requested_by_email",
			"decided_by_email",
			"project_name",
			"approved_count",
			"rejected_count",
			"can_current_user_decide",
		}).AddRow(
			"approval-1",
			"project-1",
			nil,
			"rule-1",
			"project_template_run",
			"run-1",
			"project_template_provider_review.execute",
			"Provider review execution",
			"pending",
			[]byte(`["admin"]`),
			1,
			[]byte(`["ui"]`),
			nil,
			[]byte(`[]`),
			nil,
			0,
			"pending",
			"admin-1",
			nil,
			nil,
			nil,
			nil,
			nil,
			"2026-01-01T00:00:00Z",
			"2026-01-01T00:00:00Z",
			"admin@example.com",
			nil,
			"ASSOPS",
			0,
			0,
			true,
		))
	req := httptest.NewRequest(http.MethodGet, "/api/operation-approvals", nil)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
	rr := httptest.NewRecorder()

	server.listOperationApprovals(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "request_payload") || strings.Contains(rr.Body.String(), "secret-token") {
		t.Fatalf("list response leaked approval request payload: %s", rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v", body["items"])
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("item = %#v", items[0])
	}
	if _, ok := item["request_payload"]; ok {
		t.Fatalf("list item should not include request_payload: %#v", item)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
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

func TestApprovalChannelDestinationsPreviewKinds(t *testing.T) {
	destinations := approvalChannelDestinations([]string{"ui", "webhook", "email:ops@example.com", "slack:#deploys", "pagerduty"})
	if len(destinations) != 5 {
		t.Fatalf("destinations = %#v", destinations)
	}
	if destinations[0]["kind"] != "ui" || destinations[0]["label"] != "Operations UI" || destinations[0]["needs_config"] != false {
		t.Fatalf("ui destination = %#v", destinations[0])
	}
	if destinations[0]["adapter"] != "operations_ui" || destinations[0]["adapter_status"] != "enabled" || destinations[0]["delivery_mode"] != "in_app" {
		t.Fatalf("ui adapter readiness = %#v", destinations[0])
	}
	if destinations[1]["kind"] != "webhook" || destinations[1]["target"] != "" || destinations[1]["needs_config"] != false {
		t.Fatalf("webhook destination = %#v", destinations[1])
	}
	if destinations[1]["adapter"] != "approval_webhook" || destinations[1]["adapter_status"] != "environment_backed" || destinations[1]["delivery_mode"] != "http_post" || destinations[1]["requires_external_call"] != true {
		t.Fatalf("webhook adapter readiness = %#v", destinations[1])
	}
	if destinations[2]["kind"] != "email" || destinations[2]["target"] != "ops@example.com" || destinations[2]["needs_config"] != true {
		t.Fatalf("email destination = %#v", destinations[2])
	}
	if destinations[3]["kind"] != "slack" || destinations[3]["target"] != "#deploys" || destinations[3]["needs_config"] != true {
		t.Fatalf("slack destination = %#v", destinations[3])
	}
	if destinations[4]["kind"] != "pagerduty" || destinations[4]["needs_config"] != true {
		t.Fatalf("pagerduty destination = %#v", destinations[4])
	}
	for _, index := range []int{2, 3, 4} {
		if destinations[index]["adapter_status"] != "planned" || destinations[index]["delivery_mode"] != "preview_only" || destinations[index]["requires_external_call"] != true {
			t.Fatalf("future adapter should be preview-only: %#v", destinations[index])
		}
	}
	for _, kind := range []string{"ui", "webhook", "email", "slack", "pagerduty"} {
		if !approvalDestinationKnownKind(kind) || approvalDestinationAdapterReadiness(kind, "")["adapter_status"] == "unknown" {
			t.Fatalf("known destination kind missing adapter readiness: %s", kind)
		}
	}
}

func TestApprovalChannelDestinationsHideUnknownTargets(t *testing.T) {
	destinations := approvalChannelDestinations([]string{" sms:+1234567890 ", "custom:target:extra"})
	if len(destinations) != 2 {
		t.Fatalf("destinations = %#v", destinations)
	}
	for _, destination := range destinations {
		if destination["needs_config"] != true {
			t.Fatalf("destination should need config: %#v", destination)
		}
		label := fmt.Sprint(destination["label"])
		if strings.Contains(label, "+1234567890") || strings.Contains(label, "target") || strings.Contains(label, "extra") {
			t.Fatalf("unknown destination label leaked target: %#v", destination)
		}
		if fmt.Sprint(destination["target"]) != "" || destination["redacted_target"] != true {
			t.Fatalf("unknown destination should redact target: %#v", destination)
		}
		if destination["adapter_status"] != "unknown" || destination["delivery_mode"] != "preview_only" || destination["requires_external_call"] != true {
			t.Fatalf("unknown destination should remain preview-only: %#v", destination)
		}
	}
	if len(approvalChannelDestinations(nil)) != 0 {
		t.Fatal("nil channel list should produce no destinations")
	}
}

func TestEnrichOperationApprovalRuleDoesNotExposeWebhookSecretConfig(t *testing.T) {
	t.Setenv("ASSOPS_APPROVAL_WEBHOOK_URL", "https://example.test/secret-hook")
	t.Setenv("ASSOPS_APPROVAL_WEBHOOK_TOKEN", "secret-token")
	item := enrichOperationApprovalRule(map[string]any{
		"notification_channels": []string{"ui", "webhook"},
		"escalation_channels":   []string{"email:ops@example.com"},
	})
	encoded, _ := json.Marshal(item)
	if strings.Contains(string(encoded), "secret-hook") || strings.Contains(string(encoded), "secret-token") {
		t.Fatalf("enriched approval rule leaked webhook config: %s", encoded)
	}
	if _, ok := item["notification_destinations"]; !ok {
		t.Fatalf("notification_destinations missing: %#v", item)
	}
	notifications := sliceOfMapsFromAny(item["notification_destinations"])
	if len(notifications) != 2 ||
		notifications[1]["adapter"] != "approval_webhook" ||
		notifications[1]["adapter_status"] != "environment_backed" ||
		notifications[1]["delivery_mode"] != "http_post" ||
		notifications[1]["requires_external_call"] != true {
		t.Fatalf("notification destination adapter readiness missing: %#v", notifications)
	}
	if _, ok := item["escalation_destinations"]; !ok {
		t.Fatalf("escalation_destinations missing: %#v", item)
	}
	escalation := sliceOfMapsFromAny(item["escalation_destinations"])
	if len(escalation) != 1 || escalation[0]["adapter"] != "email" || escalation[0]["adapter_status"] != "planned" {
		t.Fatalf("escalation destination adapter readiness missing: %#v", escalation)
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
		`syncCanonicalAssetsInTransaction(w, r, tx, "project_template.create_operation")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "project_template.retry_operation")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "remote_operation.enqueue")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "repository_sync.enqueue")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "repository_tag.enqueue")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "repo_sync_asset.create")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "repo_sync_asset.update")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "repo_sync_asset.archive")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "repo_sync_asset.restore")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "repo_sync_asset.run")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "repo_sync_asset.rerun")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "provider_account.create")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "provider_account.update")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "provider_account.check")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "provider_account.rotate_token_env")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "provider_account.execute_token_rotation_plan")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "webhook_connection.create")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "webhook_connection.rotate_secret")`,
		`syncing canonical assets for webhook event`,
		`failed to record webhook diagnostic event`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "webhook_event.replay")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "webhook_event.github_workflow_run")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "webhook_event.gitea_push")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "ai_runtime.create")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "ai_runtime.verify")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "agent_task.create")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "agent_task.generate_plan")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "agent_task.approve_plan")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "agent_task.execute")`,
		`syncing canonical assets for operation approval create`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "operation_approval_rule.create")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "operation_approval_rule.update")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "operation_approval.progress")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "operation_approval.execute")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "operation_approval.reject")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "operation.cancel")`,
		`syncing canonical assets for expired operation approvals`,
		`could not sync canonical assets after approval notification`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "worker_node.register")`,
		`SyncWorkerNodeCanonicalAssetWith(r.Context(), tx, node["id"])`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "worker_job.claim")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "worker_job.finish")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "argo_connection.create")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "argo_apps_sync.enqueue")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "ssh_machine.create")`,
		`syncCanonicalAssetsInTransaction(w, r, tx, "ssh_command.enqueue")`,
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
		`"repo.sync", "repo.sync_remote", "repo.tag", "repo.create_tag"`,
		`"project.template_provision_retry", "agent.execute"`,
		`SyncCanonicalAssetsWith(ctx, tx)`,
		`syncing canonical assets for running repo sync`,
		`syncing canonical assets for completed repo sync`,
		`syncing canonical assets for failed repo sync`,
		`syncing canonical assets for completed repo tag`,
		`syncing canonical assets for stale worker recovery`,
		`repo_sync_run_failures AS`,
		`syncing canonical assets for GitHub Actions sync`,
		`syncing canonical assets for failed GitHub Actions sync`,
		`syncing canonical assets for failed GitHub Actions sync without remote`,
		`syncing canonical assets for running Argo app sync`,
		`syncing canonical assets for Argo app sync`,
		`syncing canonical assets for failed Argo app sync`,
		`syncing canonical assets for failed project template creation`,
		`syncing canonical assets for completed project template creation`,
		`syncing canonical assets for completed project template provision retry`,
		`syncing canonical assets for failed project template provision retry`,
		`syncing canonical assets for running agent execution`,
		`syncing canonical assets for completed agent execution`,
		`syncing canonical assets for failed agent execution`,
		`syncing canonical assets for running SSH command`,
		`syncing canonical assets for completed SSH command`,
		`syncing canonical assets for failed SSH command`,
		`agent_call_failures AS`,
		`agent_task_failures AS`,
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
	content, err := os.ReadFile("http.go")
	if err != nil {
		t.Fatalf("read http.go: %v", err)
	}
	source := string(content)
	for _, token := range []string{
		"status NOT IN ('completed', 'failed', 'canceled', 'cancelled')",
		"UPDATE worker_jobs",
		"SET status='canceled'",
		"AND status='queued'",
	} {
		if !strings.Contains(source, token) {
			t.Fatalf("cancelOperationRun missing %q", token)
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

func TestWriteSSEWithIDIncludesReplayCursor(t *testing.T) {
	var b strings.Builder
	if err := writeSSEWithID(&b, "log", "2026-06-22T10:00:00Z|log-1", map[string]any{"id": "log-1"}); err != nil {
		t.Fatalf("writeSSEWithID: %v", err)
	}
	got := b.String()
	for _, token := range []string{
		"id: 2026-06-22T10:00:00Z|log-1\n",
		"event: log\n",
		`data: {"id":"log-1"}`,
	} {
		if !strings.Contains(got, token) {
			t.Fatalf("SSE payload missing %q in %q", token, got)
		}
	}
}

func TestOperationLogsMigrationUsesUUIDIDs(t *testing.T) {
	content, err := os.ReadFile("../../migrations/001_init.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	match := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS operation_logs \((.*?)\);`).FindStringSubmatch(string(content))
	if len(match) != 2 {
		t.Fatal("operation_logs migration block not found")
	}
	if !strings.Contains(match[1], "id UUID PRIMARY KEY DEFAULT gen_random_uuid()") {
		t.Fatal("operation_logs should use UUID ids so cursor text comparison matches id ordering")
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
	cliReadiness := mapFromAny(runtimeOutput["codex_cli_readiness"])
	if cliReadiness["readiness"] != "metadata_ready" ||
		cliReadiness["execution_enabled"] != false ||
		cliReadiness["process_spawn_enabled"] != false ||
		cliReadiness["repository_mutation_allowed"] != false {
		t.Fatalf("runtime.check should expose disabled Codex CLI readiness: %#v", cliReadiness)
	}
	if statusByGate(sliceOfMapsFromAny(cliReadiness["gates"]), "runtime_verified") != "ready" ||
		statusByGate(sliceOfMapsFromAny(cliReadiness["gates"]), "codex_cli_process") != "blocked" {
		t.Fatalf("runtime.check Codex CLI gates should keep process execution blocked: %#v", cliReadiness["gates"])
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
	patchGuardrail := mapFromAny(patchOutput["patch_workflow_guardrail"])
	if patchGuardrail["mutation_enabled"] != false || patchGuardrail["repository_mutation_allowed"] != false {
		t.Fatalf("patch guardrail should keep mutation disabled: %#v", patchGuardrail)
	}
	if patchGuardrail["codex_cli_invocation"] != "disabled" || patchGuardrail["pull_request_creation"] != "disabled" {
		t.Fatalf("patch guardrail should disable Codex CLI and PR creation: %#v", patchGuardrail)
	}
	blockedReasons := stringSliceFromAny(patchGuardrail["blocked_reasons"])
	if len(blockedReasons) < 3 {
		t.Fatalf("patch guardrail should expose blocked reasons: %#v", patchGuardrail)
	}
	readiness := sliceOfMapsFromAny(patchGuardrail["execution_readiness"])
	if len(readiness) < 5 {
		t.Fatalf("patch guardrail should expose execution readiness gates: %#v", patchGuardrail)
	}
	if statusByGate(readiness, "codex_cli_process") != "blocked" ||
		statusByGate(readiness, "repository_mutation") != "blocked" ||
		statusByGate(readiness, "pull_request_workflow") != "blocked" {
		t.Fatalf("mutation-related readiness gates should remain blocked: %#v", readiness)
	}
	if statusByGate(readiness, "agent_execute_approval") != "audit_ready" ||
		statusByGate(readiness, "runtime_metadata") != "audit_checked" {
		t.Fatalf("audit readiness gates missing approved/check states: %#v", readiness)
	}
	planInput := mapFromAny(steps[1]["input"])
	if planInput["plan_bytes"] != len("approved plan") {
		t.Fatalf("plan_bytes = %v, want %d", planInput["plan_bytes"], len("approved plan"))
	}
}

func TestAgentExecutionReadinessGatesKeepMutationBlocked(t *testing.T) {
	gates := agentExecutionReadinessGates()
	if len(gates) != 5 {
		t.Fatalf("gates = %#v", gates)
	}
	for _, gate := range []string{"codex_cli_process", "repository_mutation", "pull_request_workflow"} {
		if statusByGate(gates, gate) != "blocked" {
			t.Fatalf("%s gate should be blocked: %#v", gate, gates)
		}
	}
	encoded, _ := json.Marshal(gates)
	for _, forbidden := range []string{"ASSOPS_", "OPENAI_", "GITHUB_TOKEN", "PRIVATE KEY"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("readiness gates should not expose sensitive config hints: %s", encoded)
		}
	}
}

func TestAgentCodexCLIReadiness(t *testing.T) {
	tests := []struct {
		name           string
		runtime        map[string]any
		wantReadiness  string
		wantConfigured string
		wantVerified   string
		wantBinary     string
	}{
		{
			name:           "missing runtime blocks all execution",
			runtime:        map[string]any{},
			wantReadiness:  "blocked",
			wantConfigured: "blocked",
			wantVerified:   "blocked",
			wantBinary:     "blocked",
		},
		{
			name:           "config-only runtime is not configured",
			runtime:        map[string]any{"config": map[string]any{"token": "do-not-serialize"}},
			wantReadiness:  "blocked",
			wantConfigured: "blocked",
			wantVerified:   "blocked",
			wantBinary:     "blocked",
		},
		{
			name: "runtime with error status remains blocked",
			runtime: map[string]any{
				"id":           "runtime-2",
				"name":         "Broken Codex",
				"runtime_type": "codex-cli",
				"codex_binary": "codex",
				"model":        "gpt-5-codex",
				"status":       "error",
			},
			wantReadiness:  "blocked",
			wantConfigured: "ready",
			wantVerified:   "blocked",
			wantBinary:     "ready",
		},
		{
			name: "verified runtime is metadata ready but still cannot spawn processes",
			runtime: map[string]any{
				"id":           "runtime-1",
				"name":         "Demo Codex",
				"runtime_type": "codex-cli",
				"codex_binary": "codex",
				"model":        "gpt-5-codex",
				"status":       "verified",
				"config":       map[string]any{"token": "do-not-serialize"},
			},
			wantReadiness:  "metadata_ready",
			wantConfigured: "ready",
			wantVerified:   "ready",
			wantBinary:     "ready",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentCodexCLIReadiness(tt.runtime)
			if got["readiness"] != tt.wantReadiness {
				t.Fatalf("readiness = %v, want %s: %#v", got["readiness"], tt.wantReadiness, got)
			}
			if got["execution_enabled"] != false ||
				got["process_spawn_enabled"] != false ||
				got["external_call_made"] != false ||
				got["repository_mutation_allowed"] != false ||
				got["pull_request_creation"] != false {
				t.Fatalf("Codex CLI readiness should keep external execution disabled: %#v", got)
			}
			gates := sliceOfMapsFromAny(got["gates"])
			if statusByGate(gates, "runtime_configured") != tt.wantConfigured ||
				statusByGate(gates, "runtime_verified") != tt.wantVerified ||
				statusByGate(gates, "codex_binary_configured") != tt.wantBinary {
				t.Fatalf("runtime metadata gates mismatch: %#v", gates)
			}
			for _, gate := range []string{"codex_cli_process", "repository_mutation", "pull_request_workflow"} {
				if statusByGate(gates, gate) != "blocked" {
					t.Fatalf("%s should stay blocked: %#v", gate, gates)
				}
			}
			encoded, _ := json.Marshal(got)
			for _, forbidden := range []string{"do-not-serialize", "ASSOPS_", "OPENAI_", "GITHUB_TOKEN", "PRIVATE KEY"} {
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("Codex CLI readiness should not expose sensitive config hints: %s", encoded)
				}
			}
		})
	}
}

func TestAgentPatchWorkflowGuardrail(t *testing.T) {
	got := agentPatchWorkflowGuardrail()
	if got["execution_mode"] != "simulation_only" || got["mutation_enabled"] != false {
		t.Fatalf("guardrail mode = %#v", got)
	}
	required := stringSliceFromAny(got["required_approvals"])
	if !containsString(required, "agent.execute") || !containsString(required, "future.patch.apply") {
		t.Fatalf("guardrail required approvals = %#v", required)
	}
	encoded, _ := json.Marshal(got)
	for _, forbidden := range []string{"ASSOPS_", "OPENAI_", "GITHUB_TOKEN", "PRIVATE KEY"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("guardrail should not include sensitive config hints: %s", encoded)
		}
	}
	if statusByGate(sliceOfMapsFromAny(got["execution_readiness"]), "repository_mutation") != "blocked" {
		t.Fatalf("execution_readiness should keep repository mutation blocked: %#v", got["execution_readiness"])
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func sliceOfMapsFromAny(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, mapFromAny(item))
		}
		return out
	default:
		return nil
	}
}

func statusByGate(gates []map[string]any, gate string) string {
	for _, item := range gates {
		if fmt.Sprint(item["gate"]) == gate {
			return fmt.Sprint(item["status"])
		}
	}
	return ""
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

func TestGetWorkerQueueSummaryIncludesBackendSummary(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	server := &Server{store: &Store{DB: sqlx.NewDb(db, "sqlmock")}}
	mock.ExpectQuery(`(?s).*WITH visible_jobs AS .*recent_failures`).
		WithArgs(true, "admin-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"total_nodes",
			"online_nodes",
			"stale_nodes",
			"total_jobs",
			"queued_jobs",
			"running_jobs",
			"failed_jobs",
			"completed_24h",
			"failed_24h",
			"aged_queued_jobs",
			"stale_running_jobs",
			"jobs_by_status",
			"nodes_by_kind",
			"queue_by_tool",
			"recent_failures",
		}).AddRow(1, 1, 0, 2, 1, 1, 0, 1, 0, 0, 0, []byte(`{"queued":1,"running":1}`), []byte(`[{"kind":"control","count":1}]`), []byte(`[{"tool_name":"repo.sync","queued":1}]`), []byte(`[]`)))
	req := httptest.NewRequest(http.MethodGet, "/api/worker-queue/summary", nil)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
	rr := httptest.NewRecorder()

	server.getWorkerQueueSummary(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	backend, ok := body["backend_summary"].(map[string]any)
	if !ok {
		t.Fatalf("backend_summary missing or wrong type: %#v", body["backend_summary"])
	}
	if backend["backend"] != "postgres" || backend["redis_locking"] != "disabled" || backend["pubsub"] != "disabled" || backend["log_fanout"] != "sse_polling" {
		t.Fatalf("backend_summary = %#v", backend)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestGetWorkerQueueSummaryErrorDoesNotExposeBackendSummary(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	server := &Server{store: &Store{DB: sqlx.NewDb(db, "sqlmock")}}
	mock.ExpectQuery(`(?s).*WITH visible_jobs AS .*recent_failures`).
		WithArgs(true, "admin-1").
		WillReturnError(fmt.Errorf("database offline"))
	req := httptest.NewRequest(http.MethodGet, "/api/worker-queue/summary", nil)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
	rr := httptest.NewRecorder()

	server.getWorkerQueueSummary(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body = %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "backend_summary") {
		t.Fatalf("error response should not expose backend_summary: %s", rr.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestWorkerQueueBackendSummaryDocumentsPostgresOnlyMode(t *testing.T) {
	summary := workerQueueBackendSummary()
	for key, want := range map[string]string{
		"backend":       "postgres",
		"claiming":      "select_for_update_skip_locked",
		"redis_locking": "disabled",
		"pubsub":        "disabled",
		"log_fanout":    "sse_polling",
	} {
		if got, _ := summary[key].(string); got != want {
			t.Fatalf("workerQueueBackendSummary[%s] = %q, want %q", key, got, want)
		}
	}
	message, _ := summary["message"].(string)
	if !strings.Contains(message, "PostgreSQL") || !strings.Contains(message, "Redis") {
		t.Fatalf("workerQueueBackendSummary message should document PostgreSQL/Redis boundary: %q", message)
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

func TestProviderReviewApprovalRuleMigrationAndFreshInit(t *testing.T) {
	migration, err := os.ReadFile("../../migrations/013_provider_review_approval_rule.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	for _, token := range []string{
		"project_template_run",
		"project_template.provider_review.execute",
		"provider_review_execution",
		"provider_api_mutation",
		"required_approval_count",
		"escalation_after_minutes",
		"ON CONFLICT (resource_type, action) DO UPDATE",
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
		if !strings.Contains(string(content), "013_provider_review_approval_rule.sql") {
			t.Fatalf("%s missing 013_provider_review_approval_rule.sql init mount", path)
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

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
	"reflect"
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
		reconciliation["adapter_status"] != "planned" ||
		reconciliation["external_call_made"] != false ||
		reconciliation["provider_api_mutation"] != "disabled" {
		t.Fatalf("provider review reconciliation = %#v", reconciliation)
	}
	if containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_review_api_adapter") ||
		!containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_review_mutation_armed") {
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
	adapterRehearsal := mapFromAny(reconciliation["adapter_rehearsal"])
	if adapterRehearsal["status"] != "ready" ||
		adapterRehearsal["adapter_status"] != "planned" ||
		adapterRehearsal["operation_count"] != 3 ||
		adapterRehearsal["ready_operation_count"] != 3 ||
		adapterRehearsal["blocked_operation_count"] != 0 ||
		adapterRehearsal["mutation_arming_candidate"] != true ||
		adapterRehearsal["provider_api_call_made"] != false ||
		adapterRehearsal["provider_api_mutation"] != "disabled" {
		t.Fatalf("provider review adapter rehearsal = %#v", adapterRehearsal)
	}
	mutationArmingPlan := mapFromAny(reconciliation["mutation_arming_plan"])
	if mutationArmingPlan["status"] != "blocked" ||
		mutationArmingPlan["execution_enabled_config"] != false ||
		mutationArmingPlan["adapter_rehearsal_ready"] != true ||
		mutationArmingPlan["mutation_armed_config"] != false ||
		mutationArmingPlan["mutation_armed"] != false ||
		mutationArmingPlan["provider_api_call_made"] != false ||
		mutationArmingPlan["provider_api_mutation"] != "disabled" {
		t.Fatalf("provider review mutation arming plan = %#v", mutationArmingPlan)
	}
	armingReasons := stringSliceFromAny(mutationArmingPlan["blocked_reasons"])
	if !containsString(armingReasons, "provider_review_execution_enabled") ||
		!containsString(armingReasons, "provider_review_mutation_armed") ||
		containsString(armingReasons, "provider_review_adapter_rehearsal") {
		t.Fatalf("provider review mutation arming reasons = %#v", armingReasons)
	}
	targetSummary := mapFromAny(payload["provider_review_target_summary"])
	if targetSummary["status"] != "mutation_blocked" ||
		targetSummary["mode"] != "redacted_execution_target_summary" ||
		targetSummary["branch_refs_ready"] != true ||
		targetSummary["starter_file_payload_ready"] != true ||
		targetSummary["provider_api_request_ready"] != true ||
		targetSummary["provider_api_mutation"] != "disabled" ||
		targetSummary["contains_token"] != false ||
		targetSummary["contains_provider_url"] != false ||
		targetSummary["contains_repository_ref"] != false ||
		targetSummary["contains_file_content"] != false {
		t.Fatalf("provider review target summary = %#v", targetSummary)
	}
	targetOperations := sliceOfMapsFromAny(targetSummary["operations"])
	if len(targetOperations) != 3 || targetOperations[0]["endpoint_key"] != "github.create_branch_ref" || targetOperations[1]["contains_file_content"] != false {
		t.Fatalf("provider review target operations = %#v", targetOperations)
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
	}, true, false)
	if err != nil {
		t.Fatalf("projectTemplateProviderReviewApprovalPayloadForConfig: %v", err)
	}
	guardrail := mapFromAny(payload["execution_guardrail"])
	if guardrail["execution_mode"] != "mutation_blocked" || guardrail["execution_enabled_config"] != true || guardrail["execution_enabled"] != false {
		t.Fatalf("runtime guardrail should reflect enabled config while staying blocked: %#v", guardrail)
	}
	apiPlan := mapFromAny(payload["provider_api_request_plan"])
	if apiPlan["status"] != "ready" || apiPlan["file_count"] != 1 {
		t.Fatalf("runtime api request plan = %#v", apiPlan)
	}
	if containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "provider_review_api_adapter") ||
		!containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "provider_review_mutation_armed") {
		t.Fatalf("runtime guardrail should remain mutation-blocked: %#v", guardrail)
	}
	reconciliation := mapFromAny(payload["provider_review_reconciliation"])
	mutationArmingPlan := mapFromAny(reconciliation["mutation_arming_plan"])
	if mutationArmingPlan["status"] != "blocked" ||
		mutationArmingPlan["execution_enabled_config"] != true ||
		mutationArmingPlan["adapter_rehearsal_ready"] != false ||
		mutationArmingPlan["mutation_armed_config"] != false ||
		mutationArmingPlan["mutation_armed"] != false ||
		mutationArmingPlan["provider_api_mutation"] != "disabled" {
		t.Fatalf("runtime mutation arming plan should still require rehearsal and stay mutation-off: %#v", mutationArmingPlan)
	}
}

func TestProjectTemplateProviderReviewApprovalPayloadUsesRuntimeArmingConfig(t *testing.T) {
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
					"credential_strategy": map[string]any{
						"mode":                      "provider_account_token_env",
						"provider_account_attached": true,
						"token_env_configured":      true,
						"token_env_present":         true,
						"token_stored":              false,
						"external_call_made":        false,
					},
					"provider_review_readiness": map[string]any{"execution_plan": plan},
				},
			},
		},
	}, true, true)
	if err != nil {
		t.Fatalf("projectTemplateProviderReviewApprovalPayloadForConfig: %v", err)
	}
	if payload["provider_api_call_made"] != false || payload["provider_api_mutation"] != "disabled" {
		t.Fatalf("armed approval payload should remain no-call: %#v", payload)
	}
	guardrail := mapFromAny(payload["execution_guardrail"])
	if guardrail["execution_mode"] != "mutation_armed_audit_only" ||
		guardrail["execution_enabled"] != false ||
		guardrail["execution_enabled_config"] != true ||
		guardrail["mutation_armed_config"] != true ||
		guardrail["provider_api_mutation"] != "disabled" ||
		containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "provider_review_mutation_armed") {
		t.Fatalf("armed approval guardrail should expose config while staying audit-only: %#v", guardrail)
	}
	reconciliation := mapFromAny(payload["provider_review_reconciliation"])
	if reconciliation["status"] != "ready" ||
		reconciliation["provider_api_call_made"] != false ||
		reconciliation["provider_api_mutation"] != "disabled" {
		t.Fatalf("armed approval reconciliation should be ready but no-call: %#v", reconciliation)
	}
	mutationArmingPlan := mapFromAny(reconciliation["mutation_arming_plan"])
	if mutationArmingPlan["status"] != "armed" ||
		mutationArmingPlan["execution_enabled_config"] != true ||
		mutationArmingPlan["adapter_rehearsal_ready"] != true ||
		mutationArmingPlan["mutation_armed_config"] != true ||
		mutationArmingPlan["mutation_armed"] != true ||
		mutationArmingPlan["provider_api_call_made"] != false ||
		mutationArmingPlan["provider_api_mutation"] != "disabled" {
		t.Fatalf("armed approval mutation arming plan should remain no-call: %#v", mutationArmingPlan)
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
	if guardrail["execution_mode"] != "mutation_blocked" ||
		guardrail["execution_enabled_config"] != true ||
		guardrail["branch_creation_allowed"] != false ||
		guardrail["review_request_allowed"] != false {
		t.Fatalf("provider review execution guardrail should stay blocked: %#v", guardrail)
	}
	if containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "provider_review_api_adapter") ||
		!containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "provider_review_mutation_armed") ||
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
		reconciliation["adapter_status"] != "planned" ||
		reconciliation["external_call_made"] != false ||
		reconciliation["provider_api_mutation"] != "disabled" {
		t.Fatalf("provider review execution reconciliation = %#v", reconciliation)
	}
	if containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_review_api_adapter") ||
		!containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_review_mutation_armed") {
		t.Fatalf("provider review execution reconciliation blocked reasons = %#v", reconciliation)
	}
	if containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_credential_configured") ||
		containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_token_env_present") {
		t.Fatalf("provider review execution reconciliation should preserve credential preflight: %#v", reconciliation)
	}
	targetSummary := mapFromAny(result["provider_review_target_summary"])
	if targetSummary["status"] != "mutation_blocked" ||
		targetSummary["provider_api_call_made"] != false ||
		targetSummary["provider_api_mutation"] != "disabled" ||
		targetSummary["requires_provider_api_adapter"] != true ||
		targetSummary["contains_token"] != false ||
		targetSummary["contains_provider_url"] != false ||
		targetSummary["contains_repository_ref"] != false ||
		targetSummary["contains_file_content"] != false {
		t.Fatalf("provider review execution target summary = %#v", targetSummary)
	}
	encoded, _ := json.Marshal(result)
	for _, leak := range []string{"forged-content", "api_base_url", "secret-token"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("provider review execution result leaked %q: %s", leak, encoded)
		}
	}
}

func TestExecuteApprovedOperationProviderReviewArmedStillNoCall(t *testing.T) {
	server := &Server{cfg: Config{ProviderReviewExecutionEnabled: true, ProviderReviewMutationArmed: true}}
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
		t.Fatalf("armed provider review approval result should remain audit-only: %#v", result)
	}
	guardrail := mapFromAny(result["execution_guardrail"])
	if guardrail["execution_mode"] != "mutation_armed_audit_only" ||
		guardrail["execution_enabled"] != false ||
		guardrail["execution_enabled_config"] != true ||
		guardrail["mutation_armed_config"] != true ||
		guardrail["branch_creation_allowed"] != false ||
		guardrail["review_request_allowed"] != false ||
		containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "provider_review_mutation_armed") {
		t.Fatalf("armed provider review execution guardrail should stay no-call: %#v", guardrail)
	}
	reconciliation := mapFromAny(result["provider_review_reconciliation"])
	if reconciliation["status"] != "ready" ||
		reconciliation["external_call_made"] != false ||
		reconciliation["provider_api_call_made"] != false ||
		reconciliation["provider_api_mutation"] != "disabled" {
		t.Fatalf("armed provider review execution reconciliation = %#v", reconciliation)
	}
	mutationArmingPlan := mapFromAny(reconciliation["mutation_arming_plan"])
	if mutationArmingPlan["status"] != "armed" ||
		mutationArmingPlan["mutation_armed_config"] != true ||
		mutationArmingPlan["mutation_armed"] != true ||
		mutationArmingPlan["provider_api_call_made"] != false ||
		mutationArmingPlan["provider_api_mutation"] != "disabled" {
		t.Fatalf("armed provider review mutation arming plan = %#v", mutationArmingPlan)
	}
	encoded, _ := json.Marshal(result)
	for _, leak := range []string{"forged-content", "api_base_url", "secret-token"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("armed provider review execution result leaked %q: %s", leak, encoded)
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
				"execution_mode":           "mutation_blocked",
				"execution_enabled_config": true,
				"provider_type":            "github",
				"review_kind":              "pull_request",
				"source_branch":            "assops/template/demo-main",
				"target_branch":            "main",
				"api_base_url":             "https://api.github.example.test",
				"blocked_reasons":          []any{"provider_review_mutation_armed"},
				"gates": []map[string]any{
					{"gate": "provider_review_mutation_armed", "status": "blocked", "message": "mutation blocked", "token": "secret-token"},
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
				"status":        "ready",
				"mode":          "preflight_reconciliation",
				"provider_type": "github",
				"review_kind":   "pull_request",
				"adapter_contract": map[string]any{
					"status":                "ready",
					"adapter_status":        "ready",
					"contract_version":      "provider-review-v1",
					"provider_api_mutation": "enabled",
					"external_call_made":    true,
					"contains_token":        true,
					"contains_file_content": true,
					"api_base_url":          "https://api.github.example.test",
					"operations": []map[string]any{
						{"name": "open_review_request", "endpoint_key": "github.open_review", "required_capability": "review_request_write", "required_scope": "pull_requests:write", "payload_shape": "pull_request", "adapter_status": "ready", "execution_status": "ready", "external_call_made": true, "provider_api_mutation": "enabled", "url": "https://api.github.example.test/repos/acme/secret-repo/pulls", "token": "secret-token", "content": "do-not-include"},
					},
					"request_envelopes": []map[string]any{
						{
							"name":                    "commit_starter_files",
							"method":                  "PUT",
							"endpoint_key":            "github.commit_files",
							"payload_shape":           "content_redacted_file_batch",
							"file_count":              1,
							"payload_redacted":        false,
							"contains_token":          true,
							"contains_file_content":   true,
							"contains_provider_url":   true,
							"contains_repository_ref": true,
							"api_call":                true,
							"provider_api_mutation":   "enabled",
							"execution_status":        "ready",
							"blocked_reason":          "none",
							"url":                     "https://api.github.example.test/repos/acme/secret-repo/contents/README.md",
							"token":                   "secret-token",
							"content":                 "do-not-include",
							"readiness": []map[string]any{
								{"evidence": "starter_file_payload_staged", "status": "ready", "content": "do-not-include"},
							},
						},
					},
					"response_diagnostics": map[string]any{
						"status":                 "ready",
						"mode":                   "raw_response_diagnostics",
						"provider_type":          "github",
						"review_kind":            "pull_request",
						"adapter_status":         "ready",
						"external_call_made":     true,
						"provider_api_call_made": true,
						"provider_api_mutation":  "enabled",
						"response_body_included": true,
						"headers_included":       true,
						"contains_token":         true,
						"contains_provider_url":  true,
						"diagnostic_fields":      []any{"status_code_class", "provider_request_id_present"},
						"response_body":          `{"token":"secret-token"}`,
						"headers":                map[string]any{"Authorization": "Bearer secret-token"},
						"url":                    "https://api.github.example.test/repos/acme/secret-repo",
						"operations": []map[string]any{
							{
								"name":                     "commit_starter_files",
								"endpoint_key":             "github.commit_files",
								"status":                   "ready",
								"success_status_class":     "2xx",
								"retryable_status_classes": []any{"429", "5xx"},
								"response_body_included":   true,
								"headers_included":         true,
								"contains_token":           true,
								"contains_provider_url":    true,
								"external_call_made":       true,
								"provider_api_mutation":    "enabled",
								"response_body":            `{"content":"do-not-include"}`,
								"headers":                  map[string]any{"Authorization": "Bearer secret-token"},
							},
						},
					},
					"idempotency_plan": map[string]any{
						"status":                     "ready",
						"mode":                       "raw_idempotency_plan",
						"provider_type":              "github",
						"review_kind":                "pull_request",
						"adapter_status":             "ready",
						"external_call_made":         true,
						"provider_api_call_made":     true,
						"provider_api_mutation":      "enabled",
						"contains_token":             true,
						"contains_provider_url":      true,
						"contains_repository_ref":    true,
						"contains_branch_name":       true,
						"contains_file_content":      true,
						"idempotency_key_included":   true,
						"idempotency_key_material":   "fake-repo:fake/namespace/fake-ref:fake-token",
						"requires_persisted_attempt": true,
						"retry_after_diagnostics":    true,
						"operations": []map[string]any{
							{
								"name":                          "commit_starter_files",
								"endpoint_key":                  "github.commit_files",
								"status":                        "ready",
								"idempotency_key_kind":          "raw_key",
								"idempotency_key_included":      true,
								"idempotency_key_material":      "fake-repo:fake/namespace/fake-ref:fake-token",
								"replay_check":                  "detect_existing_commit_batch",
								"conflict_policy":               "block_on_content_or_parent_conflict",
								"retry_policy":                  "retry_only_after_response_diagnostics",
								"requires_persisted_attempt":    true,
								"contains_token":                true,
								"contains_provider_url":         true,
								"contains_repository_ref":       true,
								"contains_branch_name":          true,
								"contains_file_content":         true,
								"external_call_made":            true,
								"provider_api_call_made":        true,
								"provider_api_mutation":         "enabled",
								"response_diagnostics_required": true,
								"branch":                        "assops/template/demo-main",
								"repo":                          "secret-repo",
								"token":                         "secret-token",
							},
						},
					},
				},
				"adapter_status":        "ready",
				"external_call_made":    true,
				"provider_api_mutation": "enabled",
				"api_base_url":          "https://api.github.example.test",
				"blocked_reasons":       []any{"provider_review_mutation_armed"},
				"gates":                 []map[string]any{{"gate": "provider_review_mutation_armed", "status": "blocked", "token": "secret-token"}},
				"operations":            []map[string]any{{"name": "open_review_request", "endpoint_key": "github.open_review", "status": "ready", "url": "https://api.github.example.test/repos/acme/secret-repo/pulls", "external_call_made": true}},
				"request_envelopes": []map[string]any{
					{
						"name":                    "open_review_request",
						"method":                  "POST",
						"endpoint_key":            "github.open_review",
						"payload_shape":           "pull_request",
						"file_count":              1,
						"payload_redacted":        false,
						"contains_token":          true,
						"contains_file_content":   true,
						"contains_provider_url":   true,
						"contains_repository_ref": true,
						"api_call":                true,
						"provider_api_mutation":   "enabled",
						"execution_status":        "ready",
						"blocked_reason":          "none",
						"url":                     "https://api.github.example.test/repos/acme/secret-repo/pulls",
						"token":                   "secret-token",
						"content":                 "do-not-include",
						"readiness": []map[string]any{
							{"evidence": "provider_api_request_plan_ready", "status": "ready", "token": "secret-token"},
						},
					},
				},
				"adapter_rehearsal": map[string]any{
					"status":                    "ready",
					"mode":                      "raw_adapter_rehearsal",
					"provider_type":             "github",
					"review_kind":               "pull_request",
					"adapter_status":            "ready",
					"operation_count":           99,
					"ready_operation_count":     98,
					"blocked_operation_count":   97,
					"blocked_reasons":           []any{"provider_review_mutation_armed", "<script>alert(1)</script>"},
					"mutation_arming_candidate": true,
					"external_call_made":        true,
					"provider_api_call_made":    true,
					"provider_api_mutation":     "enabled",
					"payload_redacted":          false,
					"contains_token":            true,
					"contains_provider_url":     true,
					"contains_repository_ref":   true,
					"contains_file_content":     true,
					"token":                     "secret-token",
					"url":                       "https://api.github.example.test/repos/acme/secret-repo/pulls",
					"content":                   "do-not-include",
					"operations": []map[string]any{
						{
							"name":                   "open_review_request",
							"endpoint_key":           "github.open_review",
							"status":                 "ready",
							"blocked_reasons":        []any{"provider_review_mutation_armed", "raw_block"},
							"external_call_made":     true,
							"provider_api_call_made": true,
							"provider_api_mutation":  "enabled",
							"token":                  "secret-token",
							"url":                    "https://api.github.example.test/repos/acme/secret-repo/pulls",
							"content":                "do-not-include",
						},
					},
				},
				"mutation_arming_plan": map[string]any{
					"status":                         "armed",
					"mode":                           "raw_mutation_arming_plan",
					"provider_type":                  "github",
					"review_kind":                    "pull_request",
					"required_config":                "SECRET_CONFIG",
					"execution_enabled_config":       true,
					"adapter_rehearsal_ready":        true,
					"mutation_armed":                 true,
					"blocked_reasons":                []any{"provider_review_mutation_armed", "provider_review_adapter_rehearsal", "<script>alert(1)</script>"},
					"external_call_made":             true,
					"provider_api_call_made":         true,
					"provider_api_mutation":          "enabled",
					"contains_token":                 true,
					"contains_provider_url":          true,
					"contains_repository_ref":        true,
					"contains_file_content":          true,
					"requires_operator_review":       false,
					"requires_adapter_rehearsal":     false,
					"adapter_mutation_currently_off": false,
					"token":                          "secret-token",
					"url":                            "https://api.github.example.test/repos/acme/secret-repo",
					"content":                        "do-not-include",
				},
				"execution_blueprint": map[string]any{
					"status":                   "ready_for_adapter_implementation",
					"mode":                     "raw_adapter_execution_blueprint",
					"provider_type":            "github",
					"review_kind":              "pull_request",
					"adapter_status":           "ready",
					"operation_count":          99,
					"execution_stage":          "raw_stage",
					"live_adapter_implemented": true,
					"external_call_made":       true,
					"provider_api_call_made":   true,
					"provider_api_mutation":    "enabled",
					"payload_redacted":         false,
					"contains_token":           true,
					"contains_provider_url":    true,
					"contains_repository_ref":  true,
					"contains_branch_name":     true,
					"contains_file_content":    true,
					"token":                    "secret-token",
					"url":                      "https://api.github.example.test/repos/acme/secret-repo",
					"content":                  "do-not-include",
					"operations": []map[string]any{
						{
							"name":                        "open_review_request",
							"endpoint_key":                "github.open_review",
							"method":                      "POST",
							"payload_shape":               "pull_request",
							"execution_status":            "ready_for_adapter_implementation",
							"payload_builder":             "raw_builder",
							"response_handler":            "raw_handler",
							"idempotency_scope":           "raw_key",
							"request_body_included":       true,
							"response_body_included":      true,
							"headers_included":            true,
							"payload_redacted":            false,
							"contains_token":              true,
							"contains_provider_url":       true,
							"contains_repository_ref":     true,
							"contains_branch_name":        true,
							"contains_file_content":       true,
							"api_call":                    true,
							"external_call_made":          true,
							"provider_api_call_made":      true,
							"provider_api_mutation":       "enabled",
							"requires_provider_client":    false,
							"requires_request_builder":    false,
							"requires_response_handler":   false,
							"requires_idempotency_ledger": false,
							"token":                       "secret-token",
							"url":                         "https://api.github.example.test/repos/acme/secret-repo/pulls",
							"content":                     "do-not-include",
						},
					},
				},
				"response_diagnostics": map[string]any{
					"status":                 "ready",
					"mode":                   "raw_response_diagnostics",
					"provider_type":          "github",
					"review_kind":            "pull_request",
					"adapter_status":         "ready",
					"external_call_made":     true,
					"provider_api_call_made": true,
					"provider_api_mutation":  "enabled",
					"response_body_included": true,
					"headers_included":       true,
					"contains_token":         true,
					"contains_provider_url":  true,
					"diagnostic_fields":      []any{"status_code_class"},
					"response_body":          `{"url":"https://api.github.example.test/repos/acme/secret-repo"}`,
					"headers":                map[string]any{"Authorization": "Bearer secret-token"},
					"operations": []map[string]any{
						{
							"name":                     "open_review_request",
							"endpoint_key":             "github.open_review",
							"status":                   "ready",
							"success_status_class":     "2xx_or_already_exists",
							"retryable_status_classes": []any{"429", "5xx"},
							"response_body_included":   true,
							"headers_included":         true,
							"contains_token":           true,
							"contains_provider_url":    true,
							"external_call_made":       true,
							"provider_api_mutation":    "enabled",
							"url":                      "https://api.github.example.test/repos/acme/secret-repo/pulls",
						},
					},
				},
				"idempotency_plan": map[string]any{
					"status":                     "ready",
					"mode":                       "raw_idempotency_plan",
					"provider_type":              "github",
					"review_kind":                "pull_request",
					"adapter_status":             "ready",
					"external_call_made":         true,
					"provider_api_call_made":     true,
					"provider_api_mutation":      "enabled",
					"contains_token":             true,
					"contains_provider_url":      true,
					"contains_repository_ref":    true,
					"contains_branch_name":       true,
					"contains_file_content":      true,
					"idempotency_key_included":   true,
					"idempotency_key_material":   "fake-repo:fake/namespace/fake-ref:fake-token",
					"requires_persisted_attempt": true,
					"retry_after_diagnostics":    true,
					"operations": []map[string]any{
						{
							"name":                          "open_review_request",
							"endpoint_key":                  "github.open_review",
							"status":                        "ready",
							"idempotency_key_kind":          "raw_key",
							"idempotency_key_included":      true,
							"idempotency_key_material":      "fake-repo:fake/namespace/fake-ref:fake-token",
							"replay_check":                  "detect_existing_open_review",
							"conflict_policy":               "reuse_existing_review_request",
							"retry_policy":                  "retry_only_after_response_diagnostics",
							"requires_persisted_attempt":    true,
							"contains_token":                true,
							"contains_provider_url":         true,
							"contains_repository_ref":       true,
							"contains_branch_name":          true,
							"contains_file_content":         true,
							"external_call_made":            true,
							"provider_api_call_made":        true,
							"provider_api_mutation":         "enabled",
							"response_diagnostics_required": true,
							"branch":                        "assops/template/demo-main",
							"repo":                          "secret-repo",
							"token":                         "secret-token",
						},
					},
				},
			},
			"provider_review_target_summary": map[string]any{
				"status":                     "ready",
				"mode":                       "raw_execution_target_summary",
				"provider_type":              "github",
				"review_kind":                "pull_request",
				"source_branch":              "assops/template/demo-main",
				"target_branch":              "main",
				"branch_refs_ready":          true,
				"starter_file_payload_ready": true,
				"provider_api_request_ready": true,
				"file_count":                 1,
				"adapter_status":             "<script>alert(1)</script>",
				"blocked_reasons":            []any{"provider_review_mutation_armed", "<script>alert(1)</script>", strings.Repeat("x", 140)},
				"external_call_made":         true,
				"provider_api_call_made":     true,
				"provider_api_mutation":      "enabled",
				"contains_token":             true,
				"contains_provider_url":      true,
				"contains_repository_ref":    true,
				"contains_file_content":      true,
				"idempotency_key_included":   true,
				"url":                        "https://api.github.example.test/repos/acme/secret-repo",
				"repo":                       "secret-repo",
				"token":                      "secret-token",
				"content":                    "do-not-include",
				"operations": []map[string]any{
					{"name": "open_review_request", "endpoint_key": "github.open_review", "payload_shape": "pull_request", "status": "ready", "api_call": true, "provider_api_mutation": "enabled", "contains_token": true, "contains_file_content": true, "url": "https://api.github.example.test/repos/acme/secret-repo/pulls", "token": "secret-token", "content": "do-not-include"},
				},
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
				"provider_review_target_summary": map[string]any{
					"status":                     "ready",
					"mode":                       "raw_execution_target_summary",
					"provider_type":              "github",
					"review_kind":                "pull_request",
					"source_branch":              "assops/template/demo-main",
					"target_branch":              "main",
					"branch_refs_ready":          true,
					"starter_file_payload_ready": true,
					"provider_api_request_ready": true,
					"file_count":                 1,
					"adapter_status":             "<script>alert(1)</script>",
					"blocked_reasons":            []any{"provider_review_mutation_armed", "<script>alert(1)</script>", strings.Repeat("x", 140)},
					"external_call_made":         true,
					"provider_api_call_made":     true,
					"provider_api_mutation":      "enabled",
					"contains_token":             true,
					"contains_provider_url":      true,
					"contains_repository_ref":    true,
					"contains_file_content":      true,
					"idempotency_key_included":   true,
					"operations": []map[string]any{
						{"name": "open_review_request", "endpoint_key": "github.open_review", "payload_shape": "pull_request", "status": "ready", "api_call": true, "provider_api_mutation": "enabled", "contains_token": true, "contains_file_content": true, "url": "https://api.github.example.test/repos/acme/secret-repo/pulls", "token": "secret-token", "content": "do-not-include"},
					},
				},
				"provider_review_attempt_ledger": map[string]any{
					"status":                   "recorded",
					"mode":                     "raw_attempt_ledger",
					"attempt_count":            1,
					"external_call_made":       true,
					"provider_api_call_made":   true,
					"provider_api_mutation":    "enabled",
					"idempotency_key_included": true,
					"contains_token":           true,
					"contains_provider_url":    true,
					"contains_repository_ref":  true,
					"contains_branch_name":     true,
					"contains_file_content":    true,
					"orchestration": map[string]any{
						"status":                     "<script>alert(1)</script>",
						"mode":                       "raw_attempt_orchestration",
						"next_operation":             "open_review_request",
						"ready_count":                99,
						"waiting_count":              98,
						"blocked_count":              97,
						"completed_count":            96,
						"dependency_chain_status":    "ready",
						"external_call_made":         true,
						"provider_api_call_made":     true,
						"provider_api_mutation":      "enabled",
						"idempotency_key_included":   true,
						"requires_operator_review":   false,
						"requires_adapter_execution": false,
						"token":                      "secret-token",
					},
					"operations": []map[string]any{
						{
							"id":                       "44444444-4444-4444-4444-444444444444",
							"name":                     "open_review_request",
							"endpoint_key":             "github.open_review",
							"status":                   "planned",
							"replay_check":             "detect_existing_open_review",
							"conflict_policy":          "reuse_existing_review_request",
							"retry_policy":             "retry_only_after_response_diagnostics",
							"external_call_made":       true,
							"provider_api_call_made":   true,
							"provider_api_mutation":    "enabled",
							"idempotency_key_included": true,
							"idempotency_key_material": "fake-repo:fake/namespace/fake-ref:fake-token",
							"request_summary": map[string]any{
								"mode":                     "raw_attempt_request_summary",
								"operation_name":           "open_review_request",
								"endpoint_key":             "github.open_review",
								"payload_builder":          "raw_builder",
								"response_handler":         "raw_handler",
								"execution_status":         "ready",
								"request_body_included":    true,
								"headers_included":         true,
								"idempotency_key_included": true,
								"external_call_made":       true,
								"provider_api_call_made":   true,
								"provider_api_mutation":    "enabled",
								"contains_token":           true,
								"contains_provider_url":    true,
								"contains_repository_ref":  true,
								"contains_branch_name":     true,
								"contains_file_content":    true,
								"token":                    "secret-token",
								"url":                      "https://api.github.example.test/repos/acme/secret-repo/pulls",
								"repo":                     "secret-repo",
								"content":                  "do-not-include",
							},
							"response_diagnostics": map[string]any{
								"mode":                     "raw_attempt_response_diagnostics",
								"endpoint_key":             "github.open_review",
								"status":                   "ready",
								"success_status_class":     "2xx",
								"retryable_status_classes": []any{"5xx", "secret-token"},
								"response_body_included":   true,
								"headers_included":         true,
								"external_call_made":       true,
								"provider_api_call_made":   true,
								"provider_api_mutation":    "enabled",
								"contains_token":           true,
								"contains_provider_url":    true,
								"token":                    "secret-token",
								"url":                      "https://api.github.example.test/repos/acme/secret-repo/pulls",
								"body":                     "do-not-include",
							},
							"branch": "assops/template/demo-main",
							"repo":   "secret-repo",
							"token":  "secret-token",
						},
					},
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
	if reconciliation["external_call_made"] != false ||
		reconciliation["mode"] != "preflight_reconciliation" ||
		reconciliation["provider_api_mutation"] != "disabled" {
		t.Fatalf("provider review reconciliation audit should force disabled/no-call: %#v", reconciliation)
	}
	apiPlan := mapFromAny(audit["provider_api_request_plan"])
	if apiPlan["mode"] != "redacted_request_plan" ||
		apiPlan["provider_api_call_made"] != false ||
		apiPlan["provider_api_mutation"] != "disabled" {
		t.Fatalf("api request plan audit should preserve plan mode and force disabled/no-call: %#v", apiPlan)
	}
	targetSummary := mapFromAny(audit["provider_review_target_summary"])
	if targetSummary["mode"] != "redacted_execution_target_summary" ||
		targetSummary["external_call_made"] != false ||
		targetSummary["provider_api_call_made"] != false ||
		targetSummary["provider_api_mutation"] != "disabled" ||
		targetSummary["payload_redacted"] != true ||
		targetSummary["contains_token"] != false ||
		targetSummary["contains_provider_url"] != false ||
		targetSummary["contains_repository_ref"] != false ||
		targetSummary["contains_file_content"] != false ||
		targetSummary["idempotency_key_included"] != false ||
		targetSummary["requires_provider_api_adapter"] != true ||
		targetSummary["adapter_status"] != "missing" {
		t.Fatalf("target summary audit should be sanitized: %#v", targetSummary)
	}
	targetBlockedReasons := stringSliceFromAny(targetSummary["blocked_reasons"])
	if len(targetBlockedReasons) != 1 || targetBlockedReasons[0] != "provider_review_mutation_armed" {
		t.Fatalf("target summary blocked reasons should be allowlisted: %#v", targetBlockedReasons)
	}
	targetOperations := sliceOfMapsFromAny(targetSummary["operations"])
	if len(targetOperations) != 1 ||
		targetOperations[0]["api_call"] != false ||
		targetOperations[0]["provider_api_mutation"] != "disabled" ||
		targetOperations[0]["contains_token"] != false ||
		targetOperations[0]["contains_file_content"] != false {
		t.Fatalf("target summary operations should be sanitized: %#v", targetOperations)
	}
	for _, field := range []string{"url", "repo", "token", "content"} {
		if _, ok := targetSummary[field]; ok {
			t.Fatalf("target summary should not expose %s: %#v", field, targetSummary)
		}
		if _, ok := targetOperations[0][field]; ok {
			t.Fatalf("target summary operation should not expose %s: %#v", field, targetOperations[0])
		}
	}
	adapterContract := mapFromAny(reconciliation["adapter_contract"])
	if adapterContract["external_call_made"] != false ||
		adapterContract["provider_api_mutation"] != "disabled" ||
		adapterContract["contains_token"] != false ||
		adapterContract["contains_file_content"] != false {
		t.Fatalf("adapter contract audit should force disabled/no-call/redacted flags: %#v", adapterContract)
	}
	adapterOperations := sliceOfMapsFromAny(adapterContract["operations"])
	if len(adapterOperations) != 1 ||
		adapterOperations[0]["external_call_made"] != false ||
		adapterOperations[0]["provider_api_mutation"] != "disabled" ||
		adapterOperations[0]["contains_token"] != false ||
		adapterOperations[0]["contains_file_content"] != false {
		t.Fatalf("adapter contract operations should be sanitized: %#v", adapterOperations)
	}
	contractRequestEnvelopes := sliceOfMapsFromAny(adapterContract["request_envelopes"])
	if len(contractRequestEnvelopes) != 1 ||
		contractRequestEnvelopes[0]["api_call"] != false ||
		contractRequestEnvelopes[0]["provider_api_mutation"] != "disabled" ||
		contractRequestEnvelopes[0]["contains_token"] != false ||
		contractRequestEnvelopes[0]["contains_file_content"] != false ||
		contractRequestEnvelopes[0]["contains_provider_url"] != false ||
		contractRequestEnvelopes[0]["contains_repository_ref"] != false {
		t.Fatalf("adapter contract request envelopes should be sanitized: %#v", contractRequestEnvelopes)
	}
	contractResponseDiagnostics := mapFromAny(adapterContract["response_diagnostics"])
	if contractResponseDiagnostics["external_call_made"] != false ||
		contractResponseDiagnostics["mode"] != "redacted_response_diagnostics" ||
		contractResponseDiagnostics["provider_api_call_made"] != false ||
		contractResponseDiagnostics["provider_api_mutation"] != "disabled" ||
		contractResponseDiagnostics["response_body_included"] != false ||
		contractResponseDiagnostics["headers_included"] != false ||
		contractResponseDiagnostics["contains_token"] != false ||
		contractResponseDiagnostics["contains_provider_url"] != false {
		t.Fatalf("adapter contract response diagnostics should be sanitized: %#v", contractResponseDiagnostics)
	}
	contractResponseOperations := sliceOfMapsFromAny(contractResponseDiagnostics["operations"])
	if len(contractResponseOperations) != 1 ||
		contractResponseOperations[0]["response_body_included"] != false ||
		contractResponseOperations[0]["headers_included"] != false ||
		contractResponseOperations[0]["contains_token"] != false ||
		contractResponseOperations[0]["contains_provider_url"] != false ||
		contractResponseOperations[0]["external_call_made"] != false ||
		contractResponseOperations[0]["provider_api_mutation"] != "disabled" {
		t.Fatalf("adapter contract response diagnostic operations should be sanitized: %#v", contractResponseOperations)
	}
	contractIdempotencyPlan := mapFromAny(adapterContract["idempotency_plan"])
	if contractIdempotencyPlan["external_call_made"] != false ||
		contractIdempotencyPlan["mode"] != "redacted_idempotency_plan" ||
		contractIdempotencyPlan["provider_api_call_made"] != false ||
		contractIdempotencyPlan["provider_api_mutation"] != "disabled" ||
		contractIdempotencyPlan["contains_token"] != false ||
		contractIdempotencyPlan["contains_provider_url"] != false ||
		contractIdempotencyPlan["contains_repository_ref"] != false ||
		contractIdempotencyPlan["contains_branch_name"] != false ||
		contractIdempotencyPlan["contains_file_content"] != false ||
		contractIdempotencyPlan["idempotency_key_included"] != false ||
		contractIdempotencyPlan["idempotency_key_material"] != "redacted_required_material_only" {
		t.Fatalf("adapter contract idempotency plan should be sanitized: %#v", contractIdempotencyPlan)
	}
	contractIdempotencyOperations := sliceOfMapsFromAny(contractIdempotencyPlan["operations"])
	if len(contractIdempotencyOperations) != 1 ||
		contractIdempotencyOperations[0]["idempotency_key_included"] != false ||
		contractIdempotencyOperations[0]["idempotency_key_kind"] != "operation_scope_hash" ||
		contractIdempotencyOperations[0]["idempotency_key_material"] != "redacted_required_material_only" ||
		contractIdempotencyOperations[0]["contains_repository_ref"] != false ||
		contractIdempotencyOperations[0]["contains_branch_name"] != false ||
		contractIdempotencyOperations[0]["contains_file_content"] != false ||
		contractIdempotencyOperations[0]["external_call_made"] != false ||
		contractIdempotencyOperations[0]["provider_api_mutation"] != "disabled" {
		t.Fatalf("adapter contract idempotency operations should be sanitized: %#v", contractIdempotencyOperations)
	}
	for _, field := range []string{"branch", "repo", "token"} {
		if _, ok := contractIdempotencyOperations[0][field]; ok {
			t.Fatalf("adapter contract idempotency operation should not expose %s: %#v", field, contractIdempotencyOperations[0])
		}
	}
	requestEnvelopes := sliceOfMapsFromAny(reconciliation["request_envelopes"])
	if len(requestEnvelopes) != 1 ||
		requestEnvelopes[0]["api_call"] != false ||
		requestEnvelopes[0]["provider_api_mutation"] != "disabled" ||
		requestEnvelopes[0]["contains_token"] != false ||
		requestEnvelopes[0]["contains_file_content"] != false ||
		requestEnvelopes[0]["contains_provider_url"] != false ||
		requestEnvelopes[0]["contains_repository_ref"] != false {
		t.Fatalf("request envelopes should be sanitized: %#v", requestEnvelopes)
	}
	requestReadiness := sliceOfMapsFromAny(requestEnvelopes[0]["readiness"])
	if len(requestReadiness) != 1 || requestReadiness[0]["evidence"] != "provider_api_request_plan_ready" {
		t.Fatalf("request envelope readiness should preserve safe evidence only: %#v", requestReadiness)
	}
	adapterRehearsal := mapFromAny(reconciliation["adapter_rehearsal"])
	if adapterRehearsal["mode"] != "redacted_adapter_rehearsal" ||
		adapterRehearsal["status"] != "ready" ||
		adapterRehearsal["operation_count"] != 1 ||
		adapterRehearsal["ready_operation_count"] != 1 ||
		adapterRehearsal["blocked_operation_count"] != 0 ||
		adapterRehearsal["external_call_made"] != false ||
		adapterRehearsal["provider_api_call_made"] != false ||
		adapterRehearsal["provider_api_mutation"] != "disabled" ||
		adapterRehearsal["contains_token"] != false ||
		adapterRehearsal["contains_provider_url"] != false ||
		adapterRehearsal["contains_repository_ref"] != false ||
		adapterRehearsal["contains_file_content"] != false ||
		adapterRehearsal["adapter_mutation_currently_off"] != true {
		t.Fatalf("adapter rehearsal should be sanitized: %#v", adapterRehearsal)
	}
	adapterRehearsalOperations := sliceOfMapsFromAny(adapterRehearsal["operations"])
	if len(adapterRehearsalOperations) != 1 ||
		adapterRehearsalOperations[0]["external_call_made"] != false ||
		adapterRehearsalOperations[0]["provider_api_call_made"] != false ||
		adapterRehearsalOperations[0]["provider_api_mutation"] != "disabled" {
		t.Fatalf("adapter rehearsal operations should be sanitized: %#v", adapterRehearsalOperations)
	}
	mutationArmingPlan := mapFromAny(reconciliation["mutation_arming_plan"])
	if mutationArmingPlan["mode"] != "redacted_mutation_arming_plan" ||
		mutationArmingPlan["status"] != "ready_to_arm" ||
		mutationArmingPlan["required_config"] != "ASSOPS_ARM_PROVIDER_REVIEW_MUTATION" ||
		mutationArmingPlan["mutation_armed"] != false ||
		mutationArmingPlan["external_call_made"] != false ||
		mutationArmingPlan["provider_api_call_made"] != false ||
		mutationArmingPlan["provider_api_mutation"] != "disabled" ||
		mutationArmingPlan["contains_token"] != false ||
		mutationArmingPlan["contains_provider_url"] != false ||
		mutationArmingPlan["contains_repository_ref"] != false ||
		mutationArmingPlan["contains_file_content"] != false ||
		mutationArmingPlan["requires_operator_review"] != true ||
		mutationArmingPlan["requires_adapter_rehearsal"] != true ||
		mutationArmingPlan["adapter_mutation_currently_off"] != true {
		t.Fatalf("mutation arming plan should be sanitized: %#v", mutationArmingPlan)
	}
	mutationArmingReasons := stringSliceFromAny(mutationArmingPlan["blocked_reasons"])
	if !containsString(mutationArmingReasons, "provider_review_mutation_armed") ||
		!containsString(mutationArmingReasons, "provider_review_adapter_rehearsal") ||
		containsString(mutationArmingReasons, "<script>alert(1)</script>") {
		t.Fatalf("mutation arming reasons should be allowlisted: %#v", mutationArmingReasons)
	}
	executionBlueprint := mapFromAny(reconciliation["execution_blueprint"])
	if executionBlueprint["mode"] != "redacted_adapter_execution_blueprint" ||
		executionBlueprint["status"] != "ready_for_adapter_implementation" ||
		executionBlueprint["operation_count"] != 1 ||
		executionBlueprint["execution_stage"] != "adapter_implementation_required" ||
		executionBlueprint["live_adapter_implemented"] != false ||
		executionBlueprint["requires_provider_client"] != true ||
		executionBlueprint["requires_request_builder"] != true ||
		executionBlueprint["requires_response_handler"] != true ||
		executionBlueprint["requires_idempotency_ledger"] != true ||
		executionBlueprint["provider_api_call_made"] != false ||
		executionBlueprint["provider_api_mutation"] != "disabled" ||
		executionBlueprint["payload_redacted"] != true ||
		executionBlueprint["contains_token"] != false ||
		executionBlueprint["contains_provider_url"] != false ||
		executionBlueprint["contains_repository_ref"] != false ||
		executionBlueprint["contains_branch_name"] != false ||
		executionBlueprint["contains_file_content"] != false ||
		executionBlueprint["adapter_mutation_currently_off"] != true {
		t.Fatalf("execution blueprint should be sanitized: %#v", executionBlueprint)
	}
	executionBlueprintOperations := sliceOfMapsFromAny(executionBlueprint["operations"])
	if len(executionBlueprintOperations) != 1 ||
		executionBlueprintOperations[0]["payload_builder"] != "build_redacted_provider_request" ||
		executionBlueprintOperations[0]["response_handler"] != "handle_provider_response" ||
		executionBlueprintOperations[0]["request_body_included"] != false ||
		executionBlueprintOperations[0]["headers_included"] != false ||
		executionBlueprintOperations[0]["api_call"] != false ||
		executionBlueprintOperations[0]["provider_api_mutation"] != "disabled" ||
		executionBlueprintOperations[0]["contains_file_content"] != false ||
		executionBlueprintOperations[0]["requires_idempotency_ledger"] != true {
		t.Fatalf("execution blueprint operations should be sanitized: %#v", executionBlueprintOperations)
	}
	responseDiagnostics := mapFromAny(reconciliation["response_diagnostics"])
	if responseDiagnostics["external_call_made"] != false ||
		responseDiagnostics["mode"] != "redacted_response_diagnostics" ||
		responseDiagnostics["provider_api_call_made"] != false ||
		responseDiagnostics["provider_api_mutation"] != "disabled" ||
		responseDiagnostics["response_body_included"] != false ||
		responseDiagnostics["headers_included"] != false ||
		responseDiagnostics["contains_token"] != false ||
		responseDiagnostics["contains_provider_url"] != false {
		t.Fatalf("response diagnostics should be sanitized: %#v", responseDiagnostics)
	}
	responseOperations := sliceOfMapsFromAny(responseDiagnostics["operations"])
	if len(responseOperations) != 1 ||
		responseOperations[0]["response_body_included"] != false ||
		responseOperations[0]["headers_included"] != false ||
		responseOperations[0]["contains_token"] != false ||
		responseOperations[0]["contains_provider_url"] != false ||
		responseOperations[0]["external_call_made"] != false ||
		responseOperations[0]["provider_api_mutation"] != "disabled" {
		t.Fatalf("response diagnostic operations should be sanitized: %#v", responseOperations)
	}
	idempotencyPlan := mapFromAny(reconciliation["idempotency_plan"])
	if idempotencyPlan["external_call_made"] != false ||
		idempotencyPlan["mode"] != "redacted_idempotency_plan" ||
		idempotencyPlan["provider_api_call_made"] != false ||
		idempotencyPlan["provider_api_mutation"] != "disabled" ||
		idempotencyPlan["contains_token"] != false ||
		idempotencyPlan["contains_provider_url"] != false ||
		idempotencyPlan["contains_repository_ref"] != false ||
		idempotencyPlan["contains_branch_name"] != false ||
		idempotencyPlan["contains_file_content"] != false ||
		idempotencyPlan["idempotency_key_included"] != false ||
		idempotencyPlan["idempotency_key_material"] != "redacted_required_material_only" {
		t.Fatalf("idempotency plan should be sanitized: %#v", idempotencyPlan)
	}
	idempotencyOperations := sliceOfMapsFromAny(idempotencyPlan["operations"])
	if len(idempotencyOperations) != 1 ||
		idempotencyOperations[0]["idempotency_key_included"] != false ||
		idempotencyOperations[0]["idempotency_key_kind"] != "operation_scope_hash" ||
		idempotencyOperations[0]["idempotency_key_material"] != "redacted_required_material_only" ||
		idempotencyOperations[0]["contains_repository_ref"] != false ||
		idempotencyOperations[0]["contains_branch_name"] != false ||
		idempotencyOperations[0]["contains_file_content"] != false ||
		idempotencyOperations[0]["external_call_made"] != false ||
		idempotencyOperations[0]["provider_api_mutation"] != "disabled" {
		t.Fatalf("idempotency operations should be sanitized: %#v", idempotencyOperations)
	}
	for _, field := range []string{"branch", "repo", "token"} {
		if _, ok := idempotencyOperations[0][field]; ok {
			t.Fatalf("idempotency operation should not expose %s: %#v", field, idempotencyOperations[0])
		}
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
	resultTargetSummary := mapFromAny(result["provider_review_target_summary"])
	if resultTargetSummary["mode"] != "redacted_execution_target_summary" ||
		resultTargetSummary["external_call_made"] != false ||
		resultTargetSummary["provider_api_call_made"] != false ||
		resultTargetSummary["provider_api_mutation"] != "disabled" ||
		resultTargetSummary["contains_token"] != false ||
		resultTargetSummary["contains_provider_url"] != false ||
		resultTargetSummary["contains_repository_ref"] != false ||
		resultTargetSummary["contains_file_content"] != false ||
		resultTargetSummary["idempotency_key_included"] != false {
		t.Fatalf("approval result target summary should be sanitized: %#v", resultTargetSummary)
	}
	resultTargetOperations := sliceOfMapsFromAny(resultTargetSummary["operations"])
	if len(resultTargetOperations) != 1 ||
		resultTargetOperations[0]["api_call"] != false ||
		resultTargetOperations[0]["provider_api_mutation"] != "disabled" {
		t.Fatalf("approval result target operations should be sanitized: %#v", resultTargetOperations)
	}
	resultAttemptLedger := mapFromAny(result["provider_review_attempt_ledger"])
	if resultAttemptLedger["mode"] != "redacted_attempt_ledger" ||
		resultAttemptLedger["external_call_made"] != false ||
		resultAttemptLedger["provider_api_call_made"] != false ||
		resultAttemptLedger["provider_api_mutation"] != "disabled" ||
		resultAttemptLedger["idempotency_key_included"] != false ||
		resultAttemptLedger["contains_token"] != false ||
		resultAttemptLedger["contains_provider_url"] != false ||
		resultAttemptLedger["contains_repository_ref"] != false ||
		resultAttemptLedger["contains_branch_name"] != false ||
		resultAttemptLedger["contains_file_content"] != false {
		t.Fatalf("approval result attempt ledger should be sanitized: %#v", resultAttemptLedger)
	}
	resultAttemptOrchestration := mapFromAny(resultAttemptLedger["orchestration"])
	if resultAttemptOrchestration["mode"] != "redacted_attempt_orchestration" ||
		resultAttemptOrchestration["status"] != "not_recorded" ||
		resultAttemptOrchestration["next_operation"] != "open_review_request" ||
		resultAttemptOrchestration["ready_count"] != 1 ||
		resultAttemptOrchestration["waiting_count"] != 0 ||
		resultAttemptOrchestration["blocked_count"] != 0 ||
		resultAttemptOrchestration["completed_count"] != 0 ||
		resultAttemptOrchestration["dependency_chain_status"] != "ready" ||
		resultAttemptOrchestration["external_call_made"] != false ||
		resultAttemptOrchestration["provider_api_call_made"] != false ||
		resultAttemptOrchestration["provider_api_mutation"] != "disabled" ||
		resultAttemptOrchestration["idempotency_key_included"] != false ||
		resultAttemptOrchestration["requires_operator_review"] != true ||
		resultAttemptOrchestration["requires_adapter_execution"] != true {
		t.Fatalf("approval result attempt orchestration should be sanitized: %#v", resultAttemptOrchestration)
	}
	resultAttemptCandidate := mapFromAny(resultAttemptOrchestration["execution_candidate"])
	if resultAttemptCandidate["mode"] != "redacted_attempt_execution_candidate" ||
		resultAttemptCandidate["status"] != "blocked" ||
		resultAttemptCandidate["next_operation"] != "open_review_request" ||
		resultAttemptCandidate["endpoint_key"] != "github.open_review" ||
		resultAttemptCandidate["provider_api_call_made"] != false ||
		resultAttemptCandidate["provider_api_mutation"] != "disabled" ||
		resultAttemptCandidate["mutation_armed"] != false ||
		resultAttemptCandidate["adapter_implemented"] != false {
		t.Fatalf("approval result attempt candidate should be blocked/redacted: %#v", resultAttemptCandidate)
	}
	resultAttemptAdapterContract := mapFromAny(resultAttemptCandidate["adapter_contract"])
	if resultAttemptAdapterContract["mode"] != "redacted_attempt_adapter_contract" ||
		resultAttemptAdapterContract["operation_name"] != "open_review_request" ||
		resultAttemptAdapterContract["endpoint_key"] != "github.open_review" ||
		resultAttemptAdapterContract["payload_builder"] != "build_redacted_provider_request" ||
		resultAttemptAdapterContract["response_handler"] != "handle_provider_response" ||
		resultAttemptAdapterContract["adapter_call_state"] != "blocked" ||
		resultAttemptAdapterContract["provider_api_call_made"] != false ||
		resultAttemptAdapterContract["provider_api_mutation"] != "disabled" ||
		resultAttemptAdapterContract["contains_token"] != false ||
		resultAttemptAdapterContract["contains_provider_url"] != false ||
		resultAttemptAdapterContract["contains_repository_ref"] != false ||
		resultAttemptAdapterContract["contains_branch_name"] != false ||
		resultAttemptAdapterContract["contains_file_content"] != false {
		t.Fatalf("approval result attempt adapter contract should be blocked/redacted: %#v", resultAttemptAdapterContract)
	}
	resultAttemptRetryable := stringSliceFromAny(resultAttemptAdapterContract["retryable_status_classes"])
	if len(resultAttemptRetryable) != 1 || resultAttemptRetryable[0] != "5xx" {
		t.Fatalf("approval result attempt adapter contract retry classes should be redacted/allowlisted: %#v", resultAttemptRetryable)
	}
	resultAttemptOperations := sliceOfMapsFromAny(resultAttemptLedger["operations"])
	if len(resultAttemptOperations) != 1 ||
		resultAttemptOperations[0]["idempotency_key_included"] != false ||
		resultAttemptOperations[0]["external_call_made"] != false ||
		resultAttemptOperations[0]["provider_api_mutation"] != "disabled" {
		t.Fatalf("approval result attempt operations should be sanitized: %#v", resultAttemptOperations)
	}
	resultAttemptRequestSummary := mapFromAny(resultAttemptOperations[0]["request_summary"])
	if resultAttemptRequestSummary["mode"] != "redacted_attempt_request_summary" ||
		resultAttemptRequestSummary["payload_builder"] != "build_redacted_provider_request" ||
		resultAttemptRequestSummary["response_handler"] != "handle_provider_response" ||
		resultAttemptRequestSummary["execution_status"] != "blocked" ||
		resultAttemptRequestSummary["request_body_included"] != false ||
		resultAttemptRequestSummary["headers_included"] != false ||
		resultAttemptRequestSummary["provider_api_call_made"] != false ||
		resultAttemptRequestSummary["provider_api_mutation"] != "disabled" ||
		resultAttemptRequestSummary["idempotency_key_included"] != false ||
		resultAttemptRequestSummary["contains_token"] != false ||
		resultAttemptRequestSummary["contains_provider_url"] != false ||
		resultAttemptRequestSummary["contains_repository_ref"] != false ||
		resultAttemptRequestSummary["contains_branch_name"] != false ||
		resultAttemptRequestSummary["contains_file_content"] != false {
		t.Fatalf("approval result attempt request summary should be sanitized: %#v", resultAttemptRequestSummary)
	}
	resultAttemptResponseDiagnostics := mapFromAny(resultAttemptOperations[0]["response_diagnostics"])
	if resultAttemptResponseDiagnostics["mode"] != "redacted_attempt_response_diagnostics" ||
		resultAttemptResponseDiagnostics["status"] != "blocked" ||
		resultAttemptResponseDiagnostics["success_status_class"] != "2xx" ||
		resultAttemptResponseDiagnostics["response_body_included"] != false ||
		resultAttemptResponseDiagnostics["headers_included"] != false ||
		resultAttemptResponseDiagnostics["provider_api_call_made"] != false ||
		resultAttemptResponseDiagnostics["provider_api_mutation"] != "disabled" ||
		resultAttemptResponseDiagnostics["contains_token"] != false ||
		resultAttemptResponseDiagnostics["contains_provider_url"] != false {
		t.Fatalf("approval result attempt response diagnostics should be sanitized: %#v", resultAttemptResponseDiagnostics)
	}
	for _, field := range []string{"branch", "repo", "token", "idempotency_key_material"} {
		if _, ok := resultAttemptOperations[0][field]; ok {
			t.Fatalf("approval result attempt operation should not expose %s: %#v", field, resultAttemptOperations[0])
		}
	}
	encoded, _ := json.Marshal(audit)
	for _, leak := range []string{"secret-token", "do-not-include", "api.github.example.test", "secret-repo", "fake-repo", "fake-token", "fake/namespace/fake-ref", "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_SECRET", `"api_call":true`, `"enabled"`, "raw_execution_target_summary", "raw_idempotency_plan", "raw_adapter_rehearsal", "raw_mutation_arming_plan", "raw_adapter_execution_blueprint", "raw_attempt_response_diagnostics", "raw_builder", "raw_handler", "raw_stage", "SECRET_CONFIG", "raw_attempt_ledger", "raw_attempt_orchestration", "raw_key"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("approval payload audit leaked %q: %s", leak, encoded)
		}
	}
}

func TestOperationApprovalPayloadAuditProviderReviewAllowsMissingResponseDiagnostics(t *testing.T) {
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
			},
			"provider_review_reconciliation": map[string]any{
				"status":        "blocked",
				"mode":          "preflight_reconciliation",
				"provider_type": "github",
				"review_kind":   "pull_request",
				"adapter_contract": map[string]any{
					"status":           "planned",
					"adapter_status":   "missing",
					"contract_version": "provider-review-v1",
				},
				"adapter_status":         "missing",
				"external_call_made":     true,
				"provider_api_call_made": true,
				"provider_api_mutation":  "enabled",
			},
		},
	}
	audit := operationApprovalPayloadAudit(approval)
	reconciliation := mapFromAny(audit["provider_review_reconciliation"])
	if reconciliation["external_call_made"] != false ||
		reconciliation["provider_api_call_made"] != false ||
		reconciliation["provider_api_mutation"] != "disabled" {
		t.Fatalf("reconciliation should still be sanitized when response diagnostics are missing: %#v", reconciliation)
	}
	responseDiagnostics := mapFromAny(reconciliation["response_diagnostics"])
	if len(responseDiagnostics) != 0 {
		t.Fatalf("missing response diagnostics should remain empty: %#v", responseDiagnostics)
	}
	adapterContract := mapFromAny(reconciliation["adapter_contract"])
	contractResponseDiagnostics := mapFromAny(adapterContract["response_diagnostics"])
	if len(contractResponseDiagnostics) != 0 {
		t.Fatalf("missing contract response diagnostics should remain empty: %#v", contractResponseDiagnostics)
	}
	idempotencyPlan := mapFromAny(reconciliation["idempotency_plan"])
	if len(idempotencyPlan) != 0 {
		t.Fatalf("missing idempotency plan should remain empty: %#v", idempotencyPlan)
	}
	contractIdempotencyPlan := mapFromAny(adapterContract["idempotency_plan"])
	if len(contractIdempotencyPlan) != 0 {
		t.Fatalf("missing contract idempotency plan should remain empty: %#v", contractIdempotencyPlan)
	}
}

func TestRecordProviderReviewAttemptLedgerCreatesPlannedAttempts(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	sqlxDB := sqlx.NewDb(db, "sqlmock")
	mock.ExpectBegin()
	tx, err := sqlxDB.BeginTxx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTxx: %v", err)
	}
	defer tx.Rollback()

	reconciliation := templateProviderReviewExecutionReconciliation(
		"github",
		"pull_request",
		map[string]any{"status": "ready", "file_count": 1, "content_included": false},
		map[string]any{"execution_enabled_config": true},
		templateProviderReviewAPIRequestPlan(
			"github",
			"pull_request",
			"assops/template/demo-main",
			"main",
			map[string]any{"status": "ready", "file_count": 1, "content_included": false},
		),
		map[string]any{"token_env_configured": true, "token_env_present": true},
	)
	for _, item := range []struct {
		name     string
		endpoint string
		replay   string
		conflict string
		order    int
		depends  string
		depStat  string
		request  []byte
		response []byte
	}{
		{"create_branch_ref", "github.create_branch_ref", "detect_existing_branch_ref", "treat_existing_matching_ref_as_success", 10, "", "independent", []byte(`{"mode":"redacted_attempt_request_summary","operation_name":"create_branch_ref","endpoint_key":"github.create_branch_ref","payload_builder":"build_redacted_branch_ref_request","response_handler":"handle_branch_ref_response","execution_status":"ready_for_adapter_implementation","request_body_included":false,"headers_included":false,"idempotency_key_kind":"operation_scope_hash","idempotency_key_included":false,"requires_provider_client":true,"requires_request_builder":true,"requires_response_handler":true,"requires_idempotency_ledger":true,"provider_api_call_made":false,"provider_api_mutation":"disabled","external_call_made":false,"payload_redacted":true,"contains_token":false,"contains_provider_url":false,"contains_repository_ref":false,"contains_branch_name":false,"contains_file_content":false}`), []byte(`{"mode":"redacted_attempt_response_diagnostics","endpoint_key":"github.create_branch_ref","status":"pending","response_body_included":false,"headers_included":false,"contains_token":false,"contains_provider_url":false,"provider_api_call_made":false,"provider_api_mutation":"disabled","external_call_made":false}`)},
		{"commit_starter_files", "github.commit_files", "detect_existing_commit_batch", "block_on_content_or_parent_conflict", 20, "create_branch_ref", "waiting_for_dependency", []byte(`{"mode":"redacted_attempt_request_summary","operation_name":"commit_starter_files","endpoint_key":"github.commit_files","payload_builder":"build_redacted_file_batch_request","response_handler":"handle_commit_files_response","execution_status":"ready_for_adapter_implementation","request_body_included":false,"headers_included":false,"idempotency_key_kind":"operation_scope_hash","idempotency_key_included":false,"requires_provider_client":true,"requires_request_builder":true,"requires_response_handler":true,"requires_idempotency_ledger":true,"provider_api_call_made":false,"provider_api_mutation":"disabled","external_call_made":false,"payload_redacted":true,"contains_token":false,"contains_provider_url":false,"contains_repository_ref":false,"contains_branch_name":false,"contains_file_content":false}`), []byte(`{"mode":"redacted_attempt_response_diagnostics","endpoint_key":"github.commit_files","status":"pending","response_body_included":false,"headers_included":false,"contains_token":false,"contains_provider_url":false,"provider_api_call_made":false,"provider_api_mutation":"disabled","external_call_made":false}`)},
		{"open_review_request", "github.open_review", "detect_existing_open_review", "reuse_existing_review_request", 30, "commit_starter_files", "waiting_for_dependency", []byte(`{"mode":"redacted_attempt_request_summary","operation_name":"open_review_request","endpoint_key":"github.open_review","payload_builder":"build_redacted_review_request","response_handler":"handle_review_request_response","execution_status":"ready_for_adapter_implementation","request_body_included":false,"headers_included":false,"idempotency_key_kind":"operation_scope_hash","idempotency_key_included":false,"requires_provider_client":true,"requires_request_builder":true,"requires_response_handler":true,"requires_idempotency_ledger":true,"provider_api_call_made":false,"provider_api_mutation":"disabled","external_call_made":false,"payload_redacted":true,"contains_token":false,"contains_provider_url":false,"contains_repository_ref":false,"contains_branch_name":false,"contains_file_content":false}`), []byte(`{"mode":"redacted_attempt_response_diagnostics","endpoint_key":"github.open_review","status":"pending","response_body_included":false,"headers_included":false,"contains_token":false,"contains_provider_url":false,"provider_api_call_made":false,"provider_api_mutation":"disabled","external_call_made":false}`)},
	} {
		mock.ExpectQuery(`(?s)INSERT INTO provider_review_attempts.*RETURNING id, operation_name`).
			WillReturnRows(sqlmock.NewRows([]string{
				"id",
				"operation_name",
				"endpoint_key",
				"status",
				"replay_check",
				"conflict_policy",
				"retry_policy",
				"operation_order",
				"depends_on_operation",
				"dependency_status",
				"request_summary",
				"response_diagnostics",
				"provider_api_call_made",
				"provider_api_mutation",
				"external_call_made",
			}).AddRow(
				"44444444-4444-4444-4444-444444444444",
				item.name,
				item.endpoint,
				"planned",
				item.replay,
				item.conflict,
				"retry_only_after_response_diagnostics",
				item.order,
				item.depends,
				item.depStat,
				item.request,
				item.response,
				false,
				"disabled",
				false,
			))
	}
	server := &Server{}
	summary, err := server.recordProviderReviewAttemptLedger(
		context.Background(),
		tx,
		"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		reconciliation,
	)
	if err != nil {
		t.Fatalf("recordProviderReviewAttemptLedger: %v", err)
	}
	if summary["status"] != "recorded" ||
		summary["attempt_count"] != 3 ||
		summary["provider_api_call_made"] != false ||
		summary["provider_api_mutation"] != "disabled" ||
		summary["idempotency_key_included"] != false {
		t.Fatalf("attempt ledger summary = %#v", summary)
	}
	orchestration := mapFromAny(summary["orchestration"])
	if orchestration["mode"] != "redacted_attempt_orchestration" ||
		orchestration["next_operation"] != "create_branch_ref" ||
		orchestration["ready_count"] != 1 ||
		orchestration["waiting_count"] != 2 ||
		orchestration["blocked_count"] != 0 ||
		orchestration["dependency_chain_status"] != "waiting_for_dependency" ||
		orchestration["provider_api_mutation"] != "disabled" {
		t.Fatalf("attempt orchestration summary = %#v", orchestration)
	}
	candidate := mapFromAny(orchestration["execution_candidate"])
	if candidate["mode"] != "redacted_attempt_execution_candidate" ||
		candidate["status"] != "blocked" ||
		candidate["next_operation"] != "create_branch_ref" ||
		candidate["endpoint_key"] != "github.create_branch_ref" ||
		candidate["operation_order"] != 10 ||
		candidate["provider_api_call_made"] != false ||
		candidate["provider_api_mutation"] != "disabled" ||
		candidate["idempotency_key_included"] != false ||
		candidate["contains_token"] != false ||
		candidate["contains_provider_url"] != false {
		t.Fatalf("attempt execution candidate = %#v", candidate)
	}
	candidateAdapterContract := mapFromAny(candidate["adapter_contract"])
	if candidateAdapterContract["mode"] != "redacted_attempt_adapter_contract" ||
		candidateAdapterContract["operation_name"] != "create_branch_ref" ||
		candidateAdapterContract["endpoint_key"] != "github.create_branch_ref" ||
		candidateAdapterContract["operation_order"] != 10 ||
		candidateAdapterContract["payload_builder"] != "build_redacted_branch_ref_request" ||
		candidateAdapterContract["response_handler"] != "handle_branch_ref_response" ||
		candidateAdapterContract["idempotency_key_kind"] != "operation_scope_hash" ||
		candidateAdapterContract["response_status"] != "pending" ||
		candidateAdapterContract["success_status_class"] != "" ||
		candidateAdapterContract["adapter_call_state"] != "blocked" ||
		candidateAdapterContract["adapter_implemented"] != false ||
		candidateAdapterContract["mutation_armed"] != false ||
		candidateAdapterContract["request_body_included"] != false ||
		candidateAdapterContract["headers_included"] != false ||
		candidateAdapterContract["provider_api_call_made"] != false ||
		candidateAdapterContract["provider_api_mutation"] != "disabled" ||
		candidateAdapterContract["contains_token"] != false ||
		candidateAdapterContract["contains_provider_url"] != false ||
		candidateAdapterContract["contains_repository_ref"] != false ||
		candidateAdapterContract["contains_branch_name"] != false ||
		candidateAdapterContract["contains_file_content"] != false {
		t.Fatalf("attempt execution candidate adapter contract = %#v", candidateAdapterContract)
	}
	candidateActivationRequirements := stringSliceFromAny(candidateAdapterContract["activation_requirements"])
	if len(candidateActivationRequirements) != 4 ||
		candidateActivationRequirements[0] != "provider_api_adapter_implemented" ||
		candidateActivationRequirements[1] != "provider_review_mutation_armed" ||
		candidateActivationRequirements[2] != "operator_approval_still_valid" ||
		candidateActivationRequirements[3] != "idempotency_ledger_claim" {
		t.Fatalf("attempt execution candidate activation requirements = %#v", candidateActivationRequirements)
	}
	candidateClaimPlan := mapFromAny(candidate["claim_plan"])
	if candidateClaimPlan["mode"] != "redacted_attempt_execution_claim_plan" ||
		candidateClaimPlan["claim_state"] != "blocked" ||
		candidateClaimPlan["claim_ready"] != false ||
		candidateClaimPlan["claim_metadata_ready"] != true ||
		candidateClaimPlan["operation_name"] != "create_branch_ref" ||
		candidateClaimPlan["endpoint_key"] != "github.create_branch_ref" ||
		candidateClaimPlan["operation_order"] != 10 ||
		candidateClaimPlan["attempt_status"] != "planned" ||
		candidateClaimPlan["dependency_status"] != "independent" ||
		candidateClaimPlan["dependency_ready"] != true ||
		candidateClaimPlan["claim_status_from"] != "planned" ||
		candidateClaimPlan["claim_status_to"] != "running" ||
		candidateClaimPlan["replay_check"] != "detect_existing_branch_ref" ||
		candidateClaimPlan["conflict_policy"] != "treat_existing_matching_ref_as_success" ||
		candidateClaimPlan["retry_policy"] != "retry_only_after_response_diagnostics" ||
		candidateClaimPlan["requires_optimistic_lock"] != true ||
		candidateClaimPlan["idempotency_metadata_ready"] != true ||
		candidateClaimPlan["response_diagnostics_ready"] != true ||
		candidateClaimPlan["claim_recorded"] != false ||
		candidateClaimPlan["idempotency_claim_recorded"] != false ||
		candidateClaimPlan["provider_api_call_made"] != false ||
		candidateClaimPlan["provider_api_mutation"] != "disabled" ||
		candidateClaimPlan["idempotency_key_included"] != false ||
		candidateClaimPlan["contains_token"] != false ||
		candidateClaimPlan["contains_provider_url"] != false ||
		candidateClaimPlan["contains_repository_ref"] != false ||
		candidateClaimPlan["contains_branch_name"] != false ||
		candidateClaimPlan["contains_file_content"] != false {
		t.Fatalf("attempt execution candidate claim plan = %#v", candidateClaimPlan)
	}
	candidateClaimBlockedReasons := stringSliceFromAny(candidateClaimPlan["blocked_reasons"])
	if len(candidateClaimBlockedReasons) != 2 ||
		candidateClaimBlockedReasons[0] != "provider_review_adapter_not_implemented" ||
		candidateClaimBlockedReasons[1] != "provider_review_mutation_not_armed" {
		t.Fatalf("attempt execution candidate claim blocked reasons = %#v", candidateClaimBlockedReasons)
	}
	candidateDispatchPlan := mapFromAny(candidate["dispatch_plan"])
	if candidateDispatchPlan["mode"] != "redacted_attempt_adapter_dispatch_plan" ||
		candidateDispatchPlan["dispatch_state"] != "blocked" ||
		candidateDispatchPlan["dispatch_ready"] != false ||
		candidateDispatchPlan["dispatch_ready_reason"] != "provider_api_adapter_dispatch_not_armed" ||
		candidateDispatchPlan["dispatch_metadata_ready"] != true ||
		candidateDispatchPlan["provider_type"] != "github" ||
		candidateDispatchPlan["adapter_kind"] != "github_provider_review_adapter" ||
		candidateDispatchPlan["operation_name"] != "create_branch_ref" ||
		candidateDispatchPlan["endpoint_key"] != "github.create_branch_ref" ||
		candidateDispatchPlan["operation_order"] != 10 ||
		candidateDispatchPlan["method"] != "POST" ||
		candidateDispatchPlan["payload_shape"] != "ref_from_target_branch" ||
		candidateDispatchPlan["payload_builder"] != "build_redacted_branch_ref_request" ||
		candidateDispatchPlan["response_handler"] != "handle_branch_ref_response" ||
		candidateDispatchPlan["idempotency_key_kind"] != "operation_scope_hash" ||
		candidateDispatchPlan["requires_attempt_claim"] != true ||
		candidateDispatchPlan["requires_idempotency_claim"] != true ||
		candidateDispatchPlan["requires_provider_client"] != true ||
		candidateDispatchPlan["claim_recorded"] != false ||
		candidateDispatchPlan["idempotency_claim_recorded"] != false ||
		candidateDispatchPlan["adapter_implemented"] != false ||
		candidateDispatchPlan["mutation_armed"] != false ||
		candidateDispatchPlan["request_body_included"] != false ||
		candidateDispatchPlan["response_body_included"] != false ||
		candidateDispatchPlan["headers_included"] != false ||
		candidateDispatchPlan["provider_api_call_made"] != false ||
		candidateDispatchPlan["provider_api_mutation"] != "disabled" ||
		candidateDispatchPlan["contains_token"] != false ||
		candidateDispatchPlan["contains_provider_url"] != false ||
		candidateDispatchPlan["contains_repository_ref"] != false ||
		candidateDispatchPlan["contains_branch_name"] != false ||
		candidateDispatchPlan["contains_file_content"] != false ||
		candidateDispatchPlan["dispatch_boundary_redacted"] != true {
		t.Fatalf("attempt execution candidate dispatch plan = %#v", candidateDispatchPlan)
	}
	candidateTransportPlan := mapFromAny(candidateDispatchPlan["transport_plan"])
	if candidateTransportPlan["mode"] != "redacted_attempt_adapter_transport_plan" ||
		candidateTransportPlan["transport_ready"] != true ||
		candidateTransportPlan["transport_ready_reason"] != "ready" ||
		candidateTransportPlan["provider_type"] != "github" ||
		candidateTransportPlan["operation_name"] != "create_branch_ref" ||
		candidateTransportPlan["method"] != "POST" ||
		candidateTransportPlan["endpoint_key"] != "github.create_branch_ref" ||
		candidateTransportPlan["payload_shape"] != "ref_from_target_branch" ||
		candidateTransportPlan["auth_scheme"] != "bearer_token" ||
		candidateTransportPlan["accept_header"] != "application/vnd.github+json" ||
		candidateTransportPlan["content_type"] != "application/json" ||
		candidateTransportPlan["timeout_seconds"] != 15 ||
		candidateTransportPlan["request_body_included"] != false ||
		candidateTransportPlan["response_body_included"] != false ||
		candidateTransportPlan["headers_included"] != false ||
		candidateTransportPlan["authorization_header_included"] != false ||
		candidateTransportPlan["auth_header_redacted"] != true ||
		candidateTransportPlan["provider_url_included"] != false ||
		candidateTransportPlan["provider_api_call_made"] != false ||
		candidateTransportPlan["provider_api_mutation"] != "disabled" ||
		candidateTransportPlan["contains_token"] != false ||
		candidateTransportPlan["contains_provider_url"] != false ||
		candidateTransportPlan["contains_repository_ref"] != false ||
		candidateTransportPlan["contains_branch_name"] != false ||
		candidateTransportPlan["contains_file_content"] != false {
		t.Fatalf("attempt execution candidate transport plan = %#v", candidateTransportPlan)
	}
	candidateTransportSuccessClasses := stringSliceFromAny(candidateTransportPlan["expected_success_classes"])
	if len(candidateTransportSuccessClasses) != 1 || candidateTransportSuccessClasses[0] != "2xx" {
		t.Fatalf("attempt execution candidate transport success classes = %#v", candidateTransportSuccessClasses)
	}
	candidateTransportRetryClasses := stringSliceFromAny(candidateTransportPlan["retryable_status_classes"])
	if len(candidateTransportRetryClasses) != 1 || candidateTransportRetryClasses[0] != "5xx" {
		t.Fatalf("attempt execution candidate transport retry classes = %#v", candidateTransportRetryClasses)
	}
	candidateTransportDiagnostics := stringSliceFromAny(candidateTransportPlan["diagnostic_fields"])
	if len(candidateTransportDiagnostics) != 5 ||
		candidateTransportDiagnostics[0] != "status_code_class" ||
		candidateTransportDiagnostics[1] != "provider_request_id_present" ||
		candidateTransportDiagnostics[2] != "rate_limit_remaining_present" ||
		candidateTransportDiagnostics[3] != "retry_after_present" ||
		candidateTransportDiagnostics[4] != "provider_error_code_present" {
		t.Fatalf("attempt execution candidate transport diagnostics = %#v", candidateTransportDiagnostics)
	}
	candidateDispatchBlockedReasons := stringSliceFromAny(candidateDispatchPlan["blocked_reasons"])
	if len(candidateDispatchBlockedReasons) != 3 ||
		candidateDispatchBlockedReasons[0] != "provider_review_attempt_claim_not_recorded" ||
		candidateDispatchBlockedReasons[1] != "provider_review_adapter_not_implemented" ||
		candidateDispatchBlockedReasons[2] != "provider_review_mutation_not_armed" {
		t.Fatalf("attempt execution candidate dispatch blocked reasons = %#v", candidateDispatchBlockedReasons)
	}
	candidateRequestPlan := mapFromAny(candidateDispatchPlan["request_materialization_plan"])
	if candidateRequestPlan["mode"] != "redacted_attempt_adapter_request_materialization_plan" ||
		candidateRequestPlan["request_materialization_state"] != "blocked" ||
		candidateRequestPlan["request_materialization_ready"] != false ||
		candidateRequestPlan["request_materialization_ready_reason"] != "provider_request_materialization_not_armed" ||
		candidateRequestPlan["provider_type"] != "github" ||
		candidateRequestPlan["operation_name"] != "create_branch_ref" ||
		candidateRequestPlan["endpoint_key"] != "github.create_branch_ref" ||
		candidateRequestPlan["operation_order"] != 10 ||
		candidateRequestPlan["method"] != "POST" ||
		candidateRequestPlan["endpoint_path_template_key"] != "github_git_refs_path_template" ||
		candidateRequestPlan["payload_shape"] != "ref_from_target_branch" ||
		candidateRequestPlan["payload_builder"] != "build_redacted_branch_ref_request" ||
		candidateRequestPlan["requires_request_builder"] != true ||
		candidateRequestPlan["requires_provider_repository_context"] != true ||
		candidateRequestPlan["requires_redacted_payload_summary"] != true ||
		candidateRequestPlan["requires_starter_file_manifest"] != false ||
		candidateRequestPlan["request_builder_implemented"] != false ||
		candidateRequestPlan["provider_repository_context_resolved"] != false ||
		candidateRequestPlan["request_path_materialized"] != false ||
		candidateRequestPlan["request_url_materialized"] != false ||
		candidateRequestPlan["request_body_materialized"] != false ||
		candidateRequestPlan["payload_materialized"] != false ||
		candidateRequestPlan["headers_materialized"] != false ||
		candidateRequestPlan["starter_file_manifest_materialized"] != false ||
		candidateRequestPlan["authorization_header_materialized"] != false ||
		candidateRequestPlan["external_call_made"] != false ||
		candidateRequestPlan["provider_api_call_made"] != false ||
		candidateRequestPlan["provider_api_mutation"] != "disabled" ||
		candidateRequestPlan["request_body_included"] != false ||
		candidateRequestPlan["headers_included"] != false ||
		candidateRequestPlan["provider_url_included"] != false ||
		candidateRequestPlan["repository_ref_included"] != false ||
		candidateRequestPlan["branch_name_included"] != false ||
		candidateRequestPlan["file_content_included"] != false ||
		candidateRequestPlan["contains_token"] != false ||
		candidateRequestPlan["contains_provider_url"] != false ||
		candidateRequestPlan["contains_repository_ref"] != false ||
		candidateRequestPlan["contains_branch_name"] != false ||
		candidateRequestPlan["contains_file_content"] != false ||
		candidateRequestPlan["request_materialization_boundary_redacted"] != true {
		t.Fatalf("attempt execution candidate request materialization plan = %#v", candidateRequestPlan)
	}
	candidateRequestBlockedReasons := stringSliceFromAny(candidateRequestPlan["blocked_reasons"])
	if len(candidateRequestBlockedReasons) != 3 ||
		candidateRequestBlockedReasons[0] != "provider_request_not_materialized" ||
		candidateRequestBlockedReasons[1] != "provider_review_adapter_not_implemented" ||
		candidateRequestBlockedReasons[2] != "provider_review_mutation_not_armed" {
		t.Fatalf("attempt execution candidate request blocked reasons = %#v", candidateRequestBlockedReasons)
	}
	candidateResponsePlan := mapFromAny(candidateDispatchPlan["response_plan"])
	if candidateResponsePlan["mode"] != "redacted_attempt_adapter_response_plan" ||
		candidateResponsePlan["response_recording_state"] != "blocked" ||
		candidateResponsePlan["response_recording_ready"] != false ||
		candidateResponsePlan["response_recording_ready_reason"] != "provider_api_adapter_response_not_recorded" ||
		candidateResponsePlan["operation_name"] != "create_branch_ref" ||
		candidateResponsePlan["endpoint_key"] != "github.create_branch_ref" ||
		candidateResponsePlan["operation_order"] != 10 ||
		candidateResponsePlan["response_handler"] != "handle_branch_ref_response" ||
		candidateResponsePlan["response_status"] != "pending" ||
		candidateResponsePlan["success_attempt_status"] != "completed" ||
		candidateResponsePlan["retry_attempt_status"] != "planned" ||
		candidateResponsePlan["failure_attempt_status"] != "failed" ||
		candidateResponsePlan["dependency_unlocks_operation"] != "commit_starter_files" ||
		candidateResponsePlan["dependency_update_status"] != "dependency_satisfied" ||
		candidateResponsePlan["requires_dependency_update"] != true ||
		len(mapFromAny(candidateResponsePlan["result_recording_plan"])) == 0 ||
		candidateResponsePlan["response_recorded"] != false ||
		candidateResponsePlan["dependency_update_recorded"] != false ||
		candidateResponsePlan["provider_api_call_made"] != false ||
		candidateResponsePlan["provider_api_mutation"] != "disabled" ||
		candidateResponsePlan["response_body_included"] != false ||
		candidateResponsePlan["headers_included"] != false ||
		candidateResponsePlan["provider_request_id_included"] != false ||
		candidateResponsePlan["contains_token"] != false ||
		candidateResponsePlan["contains_provider_url"] != false ||
		candidateResponsePlan["contains_repository_ref"] != false ||
		candidateResponsePlan["contains_branch_name"] != false ||
		candidateResponsePlan["contains_file_content"] != false ||
		candidateResponsePlan["response_boundary_redacted"] != true {
		t.Fatalf("attempt execution candidate response plan = %#v", candidateResponsePlan)
	}
	candidateResponseSuccessClasses := stringSliceFromAny(candidateResponsePlan["expected_success_classes"])
	if len(candidateResponseSuccessClasses) != 1 || candidateResponseSuccessClasses[0] != "2xx" {
		t.Fatalf("attempt execution candidate response success classes = %#v", candidateResponseSuccessClasses)
	}
	candidateResponseRetryClasses := stringSliceFromAny(candidateResponsePlan["retryable_status_classes"])
	if len(candidateResponseRetryClasses) != 1 || candidateResponseRetryClasses[0] != "5xx" {
		t.Fatalf("attempt execution candidate response retry classes = %#v", candidateResponseRetryClasses)
	}
	candidateResponseFailureClasses := stringSliceFromAny(candidateResponsePlan["terminal_failure_status_classes"])
	if len(candidateResponseFailureClasses) != 1 || candidateResponseFailureClasses[0] != "4xx" {
		t.Fatalf("attempt execution candidate response failure classes = %#v", candidateResponseFailureClasses)
	}
	candidateResponseBlockedReasons := stringSliceFromAny(candidateResponsePlan["blocked_reasons"])
	if len(candidateResponseBlockedReasons) != 3 ||
		candidateResponseBlockedReasons[0] != "provider_api_call_not_made" ||
		candidateResponseBlockedReasons[1] != "provider_review_adapter_not_implemented" ||
		candidateResponseBlockedReasons[2] != "provider_review_mutation_not_armed" {
		t.Fatalf("attempt execution candidate response blocked reasons = %#v", candidateResponseBlockedReasons)
	}
	candidateResultPlan := mapFromAny(candidateResponsePlan["result_recording_plan"])
	if candidateResultPlan["mode"] != "redacted_attempt_adapter_result_recording_plan" ||
		candidateResultPlan["result_recording_state"] != "blocked" ||
		candidateResultPlan["result_recording_ready"] != false ||
		candidateResultPlan["result_recording_ready_reason"] != "provider_review_result_recording_not_armed" ||
		candidateResultPlan["result_recording_metadata_ready"] != true ||
		candidateResultPlan["operation_name"] != "create_branch_ref" ||
		candidateResultPlan["endpoint_key"] != "github.create_branch_ref" ||
		candidateResultPlan["operation_order"] != 10 ||
		candidateResultPlan["response_status"] != "pending" ||
		candidateResultPlan["success_attempt_status"] != "completed" ||
		candidateResultPlan["retry_attempt_status"] != "planned" ||
		candidateResultPlan["failure_attempt_status"] != "failed" ||
		candidateResultPlan["dependency_unlocks_operation"] != "commit_starter_files" ||
		candidateResultPlan["dependency_update_status"] != "dependency_satisfied" ||
		candidateResultPlan["requires_response_handler"] != true ||
		candidateResultPlan["requires_response_diagnostics"] != true ||
		candidateResultPlan["requires_transaction_boundary"] != true ||
		candidateResultPlan["requires_dependency_update"] != true ||
		candidateResultPlan["requires_mutation_arming"] != true ||
		candidateResultPlan["result_recorded"] != false ||
		candidateResultPlan["response_classified"] != false ||
		candidateResultPlan["attempt_status_mapped"] != false ||
		candidateResultPlan["attempt_result_persisted"] != false ||
		candidateResultPlan["dependency_update_staged"] != false ||
		candidateResultPlan["provider_request_id_recorded"] != false ||
		candidateResultPlan["provider_response_status_recorded"] != false ||
		candidateResultPlan["provider_response_body_recorded"] != false ||
		candidateResultPlan["provider_response_headers_recorded"] != false ||
		candidateResultPlan["external_call_made"] != false ||
		candidateResultPlan["provider_api_call_made"] != false ||
		candidateResultPlan["provider_api_mutation"] != "disabled" ||
		candidateResultPlan["response_body_included"] != false ||
		candidateResultPlan["headers_included"] != false ||
		candidateResultPlan["provider_request_id_included"] != false ||
		candidateResultPlan["provider_response_status_included"] != false ||
		candidateResultPlan["provider_url_included"] != false ||
		candidateResultPlan["idempotency_key_included"] != false ||
		candidateResultPlan["contains_token"] != false ||
		candidateResultPlan["contains_provider_url"] != false ||
		candidateResultPlan["contains_repository_ref"] != false ||
		candidateResultPlan["contains_branch_name"] != false ||
		candidateResultPlan["contains_file_content"] != false ||
		candidateResultPlan["result_recording_boundary_redacted"] != true {
		t.Fatalf("attempt execution candidate result plan = %#v", candidateResultPlan)
	}
	candidateResultSequence := stringSliceFromAny(candidateResultPlan["result_recording_sequence"])
	if len(candidateResultSequence) != 5 ||
		candidateResultSequence[0] != "classify_provider_response" ||
		candidateResultSequence[4] != "persist_attempt_result" {
		t.Fatalf("attempt execution candidate result sequence = %#v", candidateResultSequence)
	}
	candidateResultDiagnosticFields := stringSliceFromAny(candidateResultPlan["result_recording_diagnostic_fields"])
	if len(candidateResultDiagnosticFields) != 4 ||
		candidateResultDiagnosticFields[0] != "status_class" ||
		candidateResultDiagnosticFields[3] != "provider_request_id_present" {
		t.Fatalf("attempt execution candidate result diagnostic fields = %#v", candidateResultDiagnosticFields)
	}
	candidateResultPersistedFields := stringSliceFromAny(candidateResultPlan["result_recording_persisted_fields"])
	if len(candidateResultPersistedFields) != 4 ||
		candidateResultPersistedFields[0] != "attempt_status" ||
		candidateResultPersistedFields[3] != "retry_class" {
		t.Fatalf("attempt execution candidate result persisted fields = %#v", candidateResultPersistedFields)
	}
	candidateResultSuppressedFields := stringSliceFromAny(candidateResultPlan["result_recording_suppressed_fields"])
	if len(candidateResultSuppressedFields) != 9 ||
		candidateResultSuppressedFields[0] != "provider_request_id" ||
		candidateResultSuppressedFields[8] != "file_content" {
		t.Fatalf("attempt execution candidate result suppressed fields = %#v", candidateResultSuppressedFields)
	}
	candidateResultBlockedReasons := stringSliceFromAny(candidateResultPlan["blocked_reasons"])
	if len(candidateResultBlockedReasons) != 4 ||
		candidateResultBlockedReasons[0] != "provider_review_result_recording_not_armed" ||
		candidateResultBlockedReasons[1] != "provider_api_call_not_made" ||
		candidateResultBlockedReasons[2] != "provider_review_adapter_not_implemented" ||
		candidateResultBlockedReasons[3] != "provider_review_mutation_not_armed" {
		t.Fatalf("attempt execution candidate result blocked reasons = %#v", candidateResultBlockedReasons)
	}
	candidateCredentialPlan := mapFromAny(candidateDispatchPlan["credential_binding_plan"])
	if candidateCredentialPlan["mode"] != "redacted_attempt_adapter_credential_binding_plan" ||
		candidateCredentialPlan["credential_binding_state"] != "blocked" ||
		candidateCredentialPlan["credential_binding_ready"] != false ||
		candidateCredentialPlan["credential_binding_ready_reason"] != "provider_credential_runtime_binding_not_armed" ||
		candidateCredentialPlan["provider_type"] != "github" ||
		candidateCredentialPlan["operation_name"] != "create_branch_ref" ||
		candidateCredentialPlan["endpoint_key"] != "github.create_branch_ref" ||
		candidateCredentialPlan["auth_scheme"] != "bearer_token" ||
		candidateCredentialPlan["credential_source_kind"] != "provider_account_token_env" ||
		candidateCredentialPlan["requires_provider_account"] != true ||
		candidateCredentialPlan["requires_allowed_token_env"] != true ||
		candidateCredentialPlan["requires_runtime_token_present"] != true ||
		candidateCredentialPlan["requires_mutation_arming"] != true ||
		candidateCredentialPlan["credential_bound"] != false ||
		candidateCredentialPlan["authorization_header_materialized"] != false ||
		candidateCredentialPlan["token_env_name_included"] != false ||
		candidateCredentialPlan["token_value_included"] != false ||
		candidateCredentialPlan["token_stored"] != false ||
		candidateCredentialPlan["headers_included"] != false ||
		candidateCredentialPlan["external_call_made"] != false ||
		candidateCredentialPlan["provider_api_call_made"] != false ||
		candidateCredentialPlan["provider_api_mutation"] != "disabled" ||
		candidateCredentialPlan["contains_token"] != false ||
		candidateCredentialPlan["contains_provider_url"] != false ||
		candidateCredentialPlan["contains_repository_ref"] != false ||
		candidateCredentialPlan["contains_branch_name"] != false ||
		candidateCredentialPlan["contains_file_content"] != false ||
		candidateCredentialPlan["credential_boundary_redacted"] != true {
		t.Fatalf("attempt execution candidate credential plan = %#v", candidateCredentialPlan)
	}
	candidateCredentialBlockedReasons := stringSliceFromAny(candidateCredentialPlan["blocked_reasons"])
	if len(candidateCredentialBlockedReasons) != 3 ||
		candidateCredentialBlockedReasons[0] != "provider_credential_runtime_binding_not_armed" ||
		candidateCredentialBlockedReasons[1] != "provider_review_adapter_not_implemented" ||
		candidateCredentialBlockedReasons[2] != "provider_review_mutation_not_armed" {
		t.Fatalf("attempt execution candidate credential blocked reasons = %#v", candidateCredentialBlockedReasons)
	}
	candidateRuntimePlan := mapFromAny(candidateDispatchPlan["adapter_runtime_plan"])
	if candidateRuntimePlan["mode"] != "redacted_attempt_adapter_runtime_plan" ||
		candidateRuntimePlan["runtime_state"] != "blocked" ||
		candidateRuntimePlan["runtime_ready"] != false ||
		candidateRuntimePlan["runtime_ready_reason"] != "provider_review_adapter_runtime_not_armed" ||
		candidateRuntimePlan["provider_type"] != "github" ||
		candidateRuntimePlan["adapter_kind"] != "github_provider_review_adapter" ||
		candidateRuntimePlan["operation_name"] != "create_branch_ref" ||
		candidateRuntimePlan["endpoint_key"] != "github.create_branch_ref" ||
		candidateRuntimePlan["adapter_interface_registered"] != true ||
		candidateRuntimePlan["adapter_dispatch_registered"] != true ||
		candidateRuntimePlan["runtime_adapter_selected"] != true ||
		candidateRuntimePlan["operation_supported"] != true ||
		candidateRuntimePlan["live_adapter_implemented"] != false ||
		candidateRuntimePlan["provider_client_constructed"] != false ||
		len(mapFromAny(candidateRuntimePlan["provider_client_plan"])) == 0 ||
		candidateRuntimePlan["execute_method_bound"] != false ||
		len(mapFromAny(candidateRuntimePlan["execute_method_plan"])) == 0 ||
		candidateRuntimePlan["request_builder_bound"] != false ||
		len(mapFromAny(candidateRuntimePlan["request_builder_plan"])) == 0 ||
		candidateRuntimePlan["response_handler_bound"] != false ||
		len(mapFromAny(candidateRuntimePlan["response_handler_plan"])) == 0 ||
		candidateRuntimePlan["transaction_handler_bound"] != false ||
		candidateRuntimePlan["requires_provider_client"] != true ||
		candidateRuntimePlan["requires_request_builder"] != true ||
		candidateRuntimePlan["requires_response_handler"] != true ||
		candidateRuntimePlan["requires_transaction_handler"] != true ||
		candidateRuntimePlan["requires_mutation_arming"] != true ||
		candidateRuntimePlan["runtime_boundary_redacted"] != true ||
		candidateRuntimePlan["external_call_made"] != false ||
		candidateRuntimePlan["provider_api_call_made"] != false ||
		candidateRuntimePlan["provider_api_mutation"] != "disabled" ||
		candidateRuntimePlan["contains_token"] != false ||
		candidateRuntimePlan["contains_provider_url"] != false ||
		candidateRuntimePlan["contains_repository_ref"] != false ||
		candidateRuntimePlan["contains_branch_name"] != false ||
		candidateRuntimePlan["contains_file_content"] != false {
		t.Fatalf("attempt execution candidate runtime plan = %#v", candidateRuntimePlan)
	}
	candidateRuntimeMethods := stringSliceFromAny(candidateRuntimePlan["required_runtime_methods"])
	if len(candidateRuntimeMethods) != 4 ||
		candidateRuntimeMethods[0] != "build_request" ||
		candidateRuntimeMethods[3] != "record_attempt_transaction" {
		t.Fatalf("attempt execution candidate runtime methods = %#v", candidateRuntimeMethods)
	}
	candidateRuntimeBlockedReasons := stringSliceFromAny(candidateRuntimePlan["blocked_reasons"])
	if len(candidateRuntimeBlockedReasons) != 3 ||
		candidateRuntimeBlockedReasons[0] != "provider_review_live_adapter_not_implemented" ||
		candidateRuntimeBlockedReasons[1] != "provider_review_adapter_runtime_not_armed" ||
		candidateRuntimeBlockedReasons[2] != "provider_review_mutation_not_armed" {
		t.Fatalf("attempt execution candidate runtime blocked reasons = %#v", candidateRuntimeBlockedReasons)
	}
	candidateTransactionPlan := mapFromAny(candidateDispatchPlan["transaction_plan"])
	if candidateTransactionPlan["mode"] != "redacted_attempt_adapter_transaction_plan" ||
		candidateTransactionPlan["transaction_state"] != "blocked" ||
		candidateTransactionPlan["transaction_ready"] != false ||
		candidateTransactionPlan["transaction_ready_reason"] != "provider_review_transaction_not_armed" ||
		candidateTransactionPlan["transaction_metadata_ready"] != true ||
		candidateTransactionPlan["operation_name"] != "create_branch_ref" ||
		candidateTransactionPlan["endpoint_key"] != "github.create_branch_ref" ||
		candidateTransactionPlan["operation_order"] != 10 ||
		candidateTransactionPlan["claim_status_from"] != "planned" ||
		candidateTransactionPlan["claim_status_to"] != "running" ||
		candidateTransactionPlan["success_attempt_status"] != "completed" ||
		candidateTransactionPlan["retry_attempt_status"] != "planned" ||
		candidateTransactionPlan["failure_attempt_status"] != "failed" ||
		candidateTransactionPlan["dependency_unlocks_operation"] != "commit_starter_files" ||
		candidateTransactionPlan["dependency_update_status"] != "dependency_satisfied" ||
		candidateTransactionPlan["requires_database_transaction"] != true ||
		candidateTransactionPlan["requires_optimistic_lock"] != true ||
		candidateTransactionPlan["requires_idempotency_ledger"] != true ||
		candidateTransactionPlan["requires_provider_call_boundary"] != true ||
		candidateTransactionPlan["requires_response_diagnostics"] != true ||
		candidateTransactionPlan["requires_dependency_update"] != true ||
		len(mapFromAny(candidateTransactionPlan["provider_call_boundary_plan"])) == 0 ||
		candidateTransactionPlan["transaction_opened"] != false ||
		candidateTransactionPlan["provider_call_boundary_recorded"] != false ||
		candidateTransactionPlan["provider_response_classified"] != false ||
		candidateTransactionPlan["attempt_status_updated"] != false ||
		candidateTransactionPlan["response_recorded"] != false ||
		candidateTransactionPlan["dependency_update_recorded"] != false ||
		candidateTransactionPlan["provider_request_id_recorded"] != false ||
		candidateTransactionPlan["provider_response_body_recorded"] != false ||
		candidateTransactionPlan["provider_response_headers_recorded"] != false ||
		candidateTransactionPlan["provider_api_call_made"] != false ||
		candidateTransactionPlan["provider_api_mutation"] != "disabled" ||
		candidateTransactionPlan["contains_token"] != false ||
		candidateTransactionPlan["contains_provider_url"] != false ||
		candidateTransactionPlan["contains_repository_ref"] != false ||
		candidateTransactionPlan["contains_branch_name"] != false ||
		candidateTransactionPlan["contains_file_content"] != false ||
		candidateTransactionPlan["transaction_boundary_redacted"] != true {
		t.Fatalf("attempt execution candidate transaction plan = %#v", candidateTransactionPlan)
	}
	candidateTransactionSequence := stringSliceFromAny(candidateTransactionPlan["transaction_sequence"])
	if len(candidateTransactionSequence) != 6 ||
		candidateTransactionSequence[0] != "verify_attempt_claim" ||
		candidateTransactionSequence[5] != "update_dependency_status" {
		t.Fatalf("attempt execution candidate transaction sequence = %#v", candidateTransactionSequence)
	}
	candidateTransactionBlockedReasons := stringSliceFromAny(candidateTransactionPlan["blocked_reasons"])
	if len(candidateTransactionBlockedReasons) != 5 ||
		candidateTransactionBlockedReasons[0] != "provider_review_attempt_claim_not_recorded" ||
		candidateTransactionBlockedReasons[4] != "provider_review_mutation_not_armed" {
		t.Fatalf("attempt execution candidate transaction blocked reasons = %#v", candidateTransactionBlockedReasons)
	}
	candidateInvocationPlan := mapFromAny(candidateDispatchPlan["invocation_plan"])
	if candidateInvocationPlan["mode"] != "redacted_attempt_adapter_invocation_plan" ||
		candidateInvocationPlan["invocation_state"] != "blocked" ||
		candidateInvocationPlan["invocation_ready"] != false ||
		candidateInvocationPlan["invocation_ready_reason"] != "provider_api_invocation_not_armed" ||
		candidateInvocationPlan["operation_name"] != "create_branch_ref" ||
		candidateInvocationPlan["endpoint_key"] != "github.create_branch_ref" ||
		candidateInvocationPlan["operation_order"] != 10 ||
		candidateInvocationPlan["claim_metadata_ready"] != true ||
		candidateInvocationPlan["execution_lock_metadata_ready"] != true ||
		candidateInvocationPlan["credential_binding_ready"] != false ||
		candidateInvocationPlan["adapter_runtime_ready"] != false ||
		candidateInvocationPlan["request_materialization_ready"] != false ||
		candidateInvocationPlan["transport_metadata_ready"] != true ||
		candidateInvocationPlan["provider_send_metadata_ready"] != false ||
		candidateInvocationPlan["response_recording_ready"] != false ||
		candidateInvocationPlan["transaction_metadata_ready"] != true ||
		candidateInvocationPlan["claim_metadata_ready_reason"] != "ready" ||
		candidateInvocationPlan["execution_lock_ready_reason"] != "ready" ||
		candidateInvocationPlan["adapter_runtime_ready_reason"] != "provider_review_adapter_runtime_not_ready" ||
		candidateInvocationPlan["transport_metadata_ready_reason"] != "ready" ||
		candidateInvocationPlan["provider_send_ready_reason"] != "provider_request_send_not_armed" ||
		candidateInvocationPlan["transaction_metadata_ready_reason"] != "ready" ||
		candidateInvocationPlan["requires_attempt_claim"] != true ||
		candidateInvocationPlan["requires_idempotency_claim"] != true ||
		candidateInvocationPlan["requires_execution_lock"] != true ||
		candidateInvocationPlan["requires_credential_binding"] != true ||
		candidateInvocationPlan["requires_adapter_runtime"] != true ||
		candidateInvocationPlan["requires_request_materialization"] != true ||
		candidateInvocationPlan["requires_transport"] != true ||
		candidateInvocationPlan["requires_response_recording"] != true ||
		candidateInvocationPlan["requires_transaction_boundary"] != true ||
		candidateInvocationPlan["attempt_claim_recorded"] != false ||
		candidateInvocationPlan["idempotency_claim_recorded"] != false ||
		candidateInvocationPlan["execution_lock_acquired"] != false ||
		candidateInvocationPlan["duplicate_send_detected"] != false ||
		candidateInvocationPlan["credential_bound"] != false ||
		candidateInvocationPlan["adapter_runtime_bound"] != false ||
		candidateInvocationPlan["request_materialized"] != false ||
		candidateInvocationPlan["provider_request_sent"] != false ||
		candidateInvocationPlan["response_recorded"] != false ||
		candidateInvocationPlan["transaction_recorded"] != false ||
		candidateInvocationPlan["dependency_update_recorded"] != false ||
		candidateInvocationPlan["adapter_implemented"] != false ||
		candidateInvocationPlan["mutation_armed"] != false ||
		candidateInvocationPlan["external_call_made"] != false ||
		candidateInvocationPlan["provider_api_call_made"] != false ||
		candidateInvocationPlan["provider_api_mutation"] != "disabled" ||
		candidateInvocationPlan["request_body_included"] != false ||
		candidateInvocationPlan["response_body_included"] != false ||
		candidateInvocationPlan["headers_included"] != false ||
		candidateInvocationPlan["authorization_header_included"] != false ||
		candidateInvocationPlan["provider_url_included"] != false ||
		candidateInvocationPlan["idempotency_key_included"] != false ||
		candidateInvocationPlan["contains_token"] != false ||
		candidateInvocationPlan["contains_provider_url"] != false ||
		candidateInvocationPlan["contains_repository_ref"] != false ||
		candidateInvocationPlan["contains_branch_name"] != false ||
		candidateInvocationPlan["contains_file_content"] != false ||
		candidateInvocationPlan["invocation_boundary_redacted"] != true {
		t.Fatalf("attempt execution candidate invocation plan = %#v", candidateInvocationPlan)
	}
	candidateInvocationSequence := stringSliceFromAny(candidateInvocationPlan["invocation_sequence"])
	if len(candidateInvocationSequence) != 10 ||
		candidateInvocationSequence[0] != "claim_attempt" ||
		candidateInvocationSequence[1] != "claim_idempotency" ||
		candidateInvocationSequence[2] != "claim_execution_lock" ||
		candidateInvocationSequence[3] != "bind_credential" ||
		candidateInvocationSequence[4] != "select_adapter_runtime" ||
		candidateInvocationSequence[5] != "materialize_request" ||
		candidateInvocationSequence[6] != "send_provider_request" ||
		candidateInvocationSequence[7] != "record_response" ||
		candidateInvocationSequence[8] != "record_transaction_boundary" ||
		candidateInvocationSequence[9] != "unlock_dependency" {
		t.Fatalf("attempt execution candidate invocation sequence = %#v", candidateInvocationSequence)
	}
	candidateInvocationSubplans := stringSliceFromAny(candidateInvocationPlan["required_subplans"])
	if len(candidateInvocationSubplans) != 9 ||
		candidateInvocationSubplans[0] != "claim_plan" ||
		candidateInvocationSubplans[1] != "execution_lock_plan" ||
		candidateInvocationSubplans[2] != "credential_binding_plan" ||
		candidateInvocationSubplans[3] != "adapter_runtime_plan" ||
		candidateInvocationSubplans[4] != "request_materialization_plan" ||
		candidateInvocationSubplans[5] != "transport_plan" ||
		candidateInvocationSubplans[6] != "provider_send_plan" ||
		candidateInvocationSubplans[7] != "response_plan" ||
		candidateInvocationSubplans[8] != "transaction_plan" {
		t.Fatalf("attempt execution candidate invocation subplans = %#v", candidateInvocationSubplans)
	}
	candidateExecutionLockPlan := mapFromAny(candidateInvocationPlan["execution_lock_plan"])
	if candidateExecutionLockPlan["mode"] != "redacted_attempt_adapter_execution_lock_plan" ||
		candidateExecutionLockPlan["execution_lock_state"] != "blocked" ||
		candidateExecutionLockPlan["execution_lock_ready"] != false ||
		candidateExecutionLockPlan["execution_lock_ready_reason"] != "provider_review_execution_lock_not_armed" ||
		candidateExecutionLockPlan["execution_lock_metadata_ready"] != true ||
		candidateExecutionLockPlan["execution_lock_metadata_ready_reason"] != "ready" ||
		candidateExecutionLockPlan["operation_name"] != "create_branch_ref" ||
		candidateExecutionLockPlan["endpoint_key"] != "github.create_branch_ref" ||
		candidateExecutionLockPlan["operation_order"] != 10 ||
		candidateExecutionLockPlan["lock_scope"] != "provider_review_attempt_operation" ||
		candidateExecutionLockPlan["lock_key_kind"] != "attempt_operation_hash" ||
		candidateExecutionLockPlan["duplicate_send_policy"] != "block_duplicate_provider_send" ||
		candidateExecutionLockPlan["requires_database_transaction"] != true ||
		candidateExecutionLockPlan["requires_optimistic_lock"] != true ||
		candidateExecutionLockPlan["requires_idempotency_claim"] != true ||
		candidateExecutionLockPlan["execution_lock_acquired"] != false ||
		candidateExecutionLockPlan["duplicate_send_detected"] != false ||
		candidateExecutionLockPlan["provider_request_sent"] != false ||
		candidateExecutionLockPlan["external_call_made"] != false ||
		candidateExecutionLockPlan["provider_api_call_made"] != false ||
		candidateExecutionLockPlan["provider_api_mutation"] != "disabled" ||
		candidateExecutionLockPlan["provider_url_included"] != false ||
		candidateExecutionLockPlan["idempotency_key_included"] != false ||
		candidateExecutionLockPlan["contains_token"] != false ||
		candidateExecutionLockPlan["contains_provider_url"] != false ||
		candidateExecutionLockPlan["contains_repository_ref"] != false ||
		candidateExecutionLockPlan["contains_branch_name"] != false ||
		candidateExecutionLockPlan["contains_file_content"] != false ||
		candidateExecutionLockPlan["execution_lock_boundary_redacted"] != true {
		t.Fatalf("attempt execution candidate execution lock plan = %#v", candidateExecutionLockPlan)
	}
	candidateExecutionLockSequence := stringSliceFromAny(candidateExecutionLockPlan["execution_lock_sequence"])
	if len(candidateExecutionLockSequence) != 6 ||
		candidateExecutionLockSequence[0] != "verify_attempt_status_planned" ||
		candidateExecutionLockSequence[5] != "release_lock_after_transaction" {
		t.Fatalf("attempt execution candidate execution lock sequence = %#v", candidateExecutionLockSequence)
	}
	candidateExecutionLockSuppressedFields := stringSliceFromAny(candidateExecutionLockPlan["execution_lock_suppressed_fields"])
	if len(candidateExecutionLockSuppressedFields) != 9 ||
		candidateExecutionLockSuppressedFields[0] != "lock_key" ||
		candidateExecutionLockSuppressedFields[8] != "file_content" {
		t.Fatalf("attempt execution candidate execution lock suppressed fields = %#v", candidateExecutionLockSuppressedFields)
	}
	candidateExecutionLockBlockedReasons := stringSliceFromAny(candidateExecutionLockPlan["blocked_reasons"])
	if len(candidateExecutionLockBlockedReasons) != 4 ||
		candidateExecutionLockBlockedReasons[0] != "provider_review_execution_lock_not_armed" ||
		candidateExecutionLockBlockedReasons[3] != "provider_review_mutation_not_armed" {
		t.Fatalf("attempt execution candidate execution lock blocked reasons = %#v", candidateExecutionLockBlockedReasons)
	}
	candidateProviderSendPlan := mapFromAny(candidateInvocationPlan["provider_send_plan"])
	if candidateProviderSendPlan["mode"] != "redacted_attempt_adapter_provider_send_plan" ||
		candidateProviderSendPlan["provider_send_state"] != "blocked" ||
		candidateProviderSendPlan["provider_send_ready"] != false ||
		candidateProviderSendPlan["provider_send_ready_reason"] != "provider_request_send_not_armed" ||
		candidateProviderSendPlan["provider_send_metadata_ready"] != false ||
		candidateProviderSendPlan["provider_type"] != "github" ||
		candidateProviderSendPlan["operation_name"] != "create_branch_ref" ||
		candidateProviderSendPlan["endpoint_key"] != "github.create_branch_ref" ||
		candidateProviderSendPlan["operation_order"] != 10 ||
		candidateProviderSendPlan["method"] != "POST" ||
		candidateProviderSendPlan["payload_shape"] != "ref_from_target_branch" ||
		candidateProviderSendPlan["auth_scheme"] != "bearer_token" ||
		candidateProviderSendPlan["content_type"] != "application/json" ||
		candidateProviderSendPlan["timeout_seconds"] != 15 ||
		len(mapFromAny(candidateProviderSendPlan["retry_backoff_plan"])) == 0 ||
		candidateProviderSendPlan["requires_request_materialization"] != true ||
		candidateProviderSendPlan["requires_credential_binding"] != true ||
		candidateProviderSendPlan["requires_adapter_runtime"] != true ||
		candidateProviderSendPlan["requires_transport"] != true ||
		candidateProviderSendPlan["requires_retry_backoff_plan"] != true ||
		candidateProviderSendPlan["requires_mutation_arming"] != true ||
		candidateProviderSendPlan["request_materialization_ready"] != false ||
		candidateProviderSendPlan["credential_binding_ready"] != false ||
		candidateProviderSendPlan["adapter_runtime_ready"] != false ||
		candidateProviderSendPlan["transport_metadata_ready"] != true ||
		candidateProviderSendPlan["request_path_materialized"] != false ||
		candidateProviderSendPlan["request_url_materialized"] != false ||
		candidateProviderSendPlan["request_body_materialized"] != false ||
		candidateProviderSendPlan["headers_materialized"] != false ||
		candidateProviderSendPlan["authorization_header_materialized"] != false ||
		candidateProviderSendPlan["provider_client_bound"] != false ||
		candidateProviderSendPlan["credential_bound"] != false ||
		candidateProviderSendPlan["runtime_bound"] != false ||
		candidateProviderSendPlan["mutation_armed"] != false ||
		candidateProviderSendPlan["send_attempted"] != false ||
		candidateProviderSendPlan["provider_request_sent"] != false ||
		candidateProviderSendPlan["provider_response_received"] != false ||
		candidateProviderSendPlan["external_call_made"] != false ||
		candidateProviderSendPlan["provider_api_call_made"] != false ||
		candidateProviderSendPlan["provider_api_mutation"] != "disabled" ||
		candidateProviderSendPlan["request_body_included"] != false ||
		candidateProviderSendPlan["response_body_included"] != false ||
		candidateProviderSendPlan["headers_included"] != false ||
		candidateProviderSendPlan["authorization_header_included"] != false ||
		candidateProviderSendPlan["provider_url_included"] != false ||
		candidateProviderSendPlan["idempotency_key_included"] != false ||
		candidateProviderSendPlan["provider_request_id_included"] != false ||
		candidateProviderSendPlan["contains_token"] != false ||
		candidateProviderSendPlan["contains_provider_url"] != false ||
		candidateProviderSendPlan["contains_repository_ref"] != false ||
		candidateProviderSendPlan["contains_branch_name"] != false ||
		candidateProviderSendPlan["contains_file_content"] != false ||
		candidateProviderSendPlan["provider_send_boundary_redacted"] != true {
		t.Fatalf("attempt execution candidate provider send plan = %#v", candidateProviderSendPlan)
	}
	candidateProviderSendSequence := stringSliceFromAny(candidateProviderSendPlan["provider_send_sequence"])
	if len(candidateProviderSendSequence) != 6 ||
		candidateProviderSendSequence[0] != "bind_provider_client" ||
		candidateProviderSendSequence[4] != "send_provider_request" ||
		candidateProviderSendSequence[5] != "handoff_to_response_handler" {
		t.Fatalf("attempt execution candidate provider send sequence = %#v", candidateProviderSendSequence)
	}
	candidateProviderSendSuppressedFields := stringSliceFromAny(candidateProviderSendPlan["provider_send_suppressed_fields"])
	if len(candidateProviderSendSuppressedFields) != 10 ||
		candidateProviderSendSuppressedFields[0] != "request_url" ||
		candidateProviderSendSuppressedFields[9] != "file_content" {
		t.Fatalf("attempt execution candidate provider send suppressed fields = %#v", candidateProviderSendSuppressedFields)
	}
	candidateRetryBackoffPlan := mapFromAny(candidateProviderSendPlan["retry_backoff_plan"])
	if candidateRetryBackoffPlan["mode"] != "redacted_attempt_adapter_retry_backoff_plan" ||
		candidateRetryBackoffPlan["retry_backoff_state"] != "blocked" ||
		candidateRetryBackoffPlan["retry_backoff_ready"] != false ||
		candidateRetryBackoffPlan["retry_backoff_ready_reason"] != "provider_retry_backoff_not_armed" ||
		candidateRetryBackoffPlan["retry_backoff_metadata_ready"] != true ||
		candidateRetryBackoffPlan["operation_name"] != "create_branch_ref" ||
		candidateRetryBackoffPlan["endpoint_key"] != "github.create_branch_ref" ||
		candidateRetryBackoffPlan["operation_order"] != 10 ||
		candidateRetryBackoffPlan["retry_policy"] != "retry_only_after_response_diagnostics" ||
		candidateRetryBackoffPlan["max_attempts"] != 3 ||
		candidateRetryBackoffPlan["initial_backoff_seconds"] != 30 ||
		candidateRetryBackoffPlan["max_backoff_seconds"] != 300 ||
		candidateRetryBackoffPlan["jitter"] != "full" ||
		candidateRetryBackoffPlan["requires_response_diagnostics"] != true ||
		candidateRetryBackoffPlan["requires_idempotency_ledger"] != true ||
		candidateRetryBackoffPlan["requires_attempt_ledger"] != true ||
		candidateRetryBackoffPlan["requires_mutation_arming"] != true ||
		candidateRetryBackoffPlan["retry_scheduled"] != false ||
		candidateRetryBackoffPlan["retry_attempt_recorded"] != false ||
		candidateRetryBackoffPlan["retry_after_value_recorded"] != false ||
		candidateRetryBackoffPlan["retry_after_header_included"] != false ||
		candidateRetryBackoffPlan["provider_rate_limit_value_included"] != false ||
		candidateRetryBackoffPlan["provider_error_code_included"] != false ||
		candidateRetryBackoffPlan["external_call_made"] != false ||
		candidateRetryBackoffPlan["provider_api_call_made"] != false ||
		candidateRetryBackoffPlan["provider_api_mutation"] != "disabled" ||
		candidateRetryBackoffPlan["request_body_included"] != false ||
		candidateRetryBackoffPlan["response_body_included"] != false ||
		candidateRetryBackoffPlan["headers_included"] != false ||
		candidateRetryBackoffPlan["authorization_header_included"] != false ||
		candidateRetryBackoffPlan["provider_url_included"] != false ||
		candidateRetryBackoffPlan["idempotency_key_included"] != false ||
		candidateRetryBackoffPlan["contains_token"] != false ||
		candidateRetryBackoffPlan["contains_provider_url"] != false ||
		candidateRetryBackoffPlan["contains_repository_ref"] != false ||
		candidateRetryBackoffPlan["contains_branch_name"] != false ||
		candidateRetryBackoffPlan["contains_file_content"] != false ||
		candidateRetryBackoffPlan["retry_backoff_boundary_redacted"] != true {
		t.Fatalf("attempt execution candidate retry backoff plan = %#v", candidateRetryBackoffPlan)
	}
	candidateBackoffRetryClasses := stringSliceFromAny(candidateRetryBackoffPlan["retryable_status_classes"])
	candidateBackoffTransportRetryClasses := stringSliceFromAny(candidateRetryBackoffPlan["transport_retryable_status_classes"])
	if len(candidateBackoffRetryClasses) != 1 || candidateBackoffRetryClasses[0] != "5xx" || len(candidateBackoffTransportRetryClasses) != 1 || candidateBackoffTransportRetryClasses[0] != "5xx" {
		t.Fatalf("attempt execution candidate retry classes = %#v / %#v", candidateBackoffRetryClasses, candidateBackoffTransportRetryClasses)
	}
	candidateRetrySequence := stringSliceFromAny(candidateRetryBackoffPlan["retry_backoff_sequence"])
	if len(candidateRetrySequence) != 4 ||
		candidateRetrySequence[0] != "classify_retryable_response" ||
		candidateRetrySequence[3] != "schedule_backoff_retry" {
		t.Fatalf("attempt execution candidate retry sequence = %#v", candidateRetrySequence)
	}
	candidateRetrySuppressedFields := stringSliceFromAny(candidateRetryBackoffPlan["retry_backoff_suppressed_fields"])
	if len(candidateRetrySuppressedFields) != 12 ||
		candidateRetrySuppressedFields[0] != "retry_after_value" ||
		candidateRetrySuppressedFields[11] != "file_content" {
		t.Fatalf("attempt execution candidate retry suppressed fields = %#v", candidateRetrySuppressedFields)
	}
	candidateRetryBlockedReasons := stringSliceFromAny(candidateRetryBackoffPlan["blocked_reasons"])
	if len(candidateRetryBlockedReasons) != 4 ||
		candidateRetryBlockedReasons[0] != "provider_retry_backoff_not_armed" ||
		candidateRetryBlockedReasons[1] != "provider_response_diagnostics_not_recorded" ||
		candidateRetryBlockedReasons[2] != "provider_idempotency_ledger_not_claimed" ||
		candidateRetryBlockedReasons[3] != "provider_review_mutation_not_armed" {
		t.Fatalf("attempt execution candidate retry blocked reasons = %#v", candidateRetryBlockedReasons)
	}
	candidateProviderSendBlockedReasons := stringSliceFromAny(candidateProviderSendPlan["blocked_reasons"])
	if len(candidateProviderSendBlockedReasons) != 5 ||
		candidateProviderSendBlockedReasons[0] != "provider_request_send_not_armed" ||
		candidateProviderSendBlockedReasons[1] != "provider_request_not_materialized" ||
		candidateProviderSendBlockedReasons[2] != "provider_credential_runtime_binding_not_armed" ||
		candidateProviderSendBlockedReasons[3] != "provider_review_adapter_runtime_not_bound" ||
		candidateProviderSendBlockedReasons[4] != "provider_review_mutation_not_armed" {
		t.Fatalf("attempt execution candidate provider send blocked reasons = %#v", candidateProviderSendBlockedReasons)
	}
	candidateInvocationBlockedReasons := stringSliceFromAny(candidateInvocationPlan["blocked_reasons"])
	if len(candidateInvocationBlockedReasons) != 9 ||
		candidateInvocationBlockedReasons[0] != "provider_review_attempt_claim_not_recorded" ||
		candidateInvocationBlockedReasons[1] != "provider_review_execution_lock_not_acquired" ||
		candidateInvocationBlockedReasons[2] != "provider_credential_runtime_binding_not_armed" ||
		candidateInvocationBlockedReasons[3] != "provider_review_adapter_runtime_not_bound" ||
		candidateInvocationBlockedReasons[4] != "provider_request_not_materialized" ||
		candidateInvocationBlockedReasons[5] != "provider_api_call_not_made" ||
		candidateInvocationBlockedReasons[6] != "provider_review_transaction_not_recorded" ||
		candidateInvocationBlockedReasons[7] != "provider_review_adapter_not_implemented" ||
		candidateInvocationBlockedReasons[8] != "provider_review_mutation_not_armed" {
		t.Fatalf("attempt execution candidate invocation blocked reasons = %#v", candidateInvocationBlockedReasons)
	}
	candidateGates := sliceOfMapsFromAny(candidate["gates"])
	if len(candidateGates) != 5 ||
		candidateGates[0]["gate"] != "attempt_operation_ready" ||
		candidateGates[0]["category"] != "data_integrity" ||
		candidateGates[0]["status"] != "ready" ||
		candidateGates[1]["gate"] != "idempotency_metadata" ||
		candidateGates[1]["category"] != "data_integrity" ||
		candidateGates[1]["status"] != "ready" ||
		candidateGates[2]["gate"] != "response_diagnostics_metadata" ||
		candidateGates[2]["category"] != "data_integrity" ||
		candidateGates[2]["status"] != "ready" ||
		candidateGates[3]["category"] != "execution_blocker" ||
		candidateGates[3]["status"] != "blocked" ||
		candidateGates[4]["category"] != "execution_blocker" ||
		candidateGates[4]["status"] != "blocked" {
		t.Fatalf("attempt execution candidate gates = %#v", candidateGates)
	}
	operations := sliceOfMapsFromAny(summary["operations"])
	if len(operations) != 3 ||
		operations[0]["endpoint_key"] != "github.create_branch_ref" ||
		operations[0]["operation_order"] != 10 ||
		operations[1]["depends_on_operation"] != "create_branch_ref" ||
		operations[2]["replay_check"] != "detect_existing_open_review" ||
		operations[2]["dependency_status"] != "waiting_for_dependency" {
		t.Fatalf("attempt ledger operations = %#v", operations)
	}
	requestSummary := mapFromAny(operations[2]["request_summary"])
	if requestSummary["mode"] != "redacted_attempt_request_summary" ||
		requestSummary["payload_builder"] != "build_redacted_review_request" ||
		requestSummary["response_handler"] != "handle_review_request_response" ||
		requestSummary["execution_status"] != "ready_for_adapter_implementation" ||
		requestSummary["request_body_included"] != false ||
		requestSummary["headers_included"] != false ||
		requestSummary["provider_api_call_made"] != false ||
		requestSummary["provider_api_mutation"] != "disabled" ||
		requestSummary["payload_redacted"] != true ||
		requestSummary["contains_token"] != false ||
		requestSummary["contains_provider_url"] != false ||
		requestSummary["contains_repository_ref"] != false ||
		requestSummary["contains_branch_name"] != false ||
		requestSummary["contains_file_content"] != false {
		t.Fatalf("attempt request summary = %#v", requestSummary)
	}
	responseDiagnostics := mapFromAny(operations[2]["response_diagnostics"])
	if responseDiagnostics["mode"] != "redacted_attempt_response_diagnostics" ||
		responseDiagnostics["status"] != "pending" ||
		responseDiagnostics["response_body_included"] != false ||
		responseDiagnostics["headers_included"] != false ||
		responseDiagnostics["provider_api_call_made"] != false ||
		responseDiagnostics["provider_api_mutation"] != "disabled" ||
		responseDiagnostics["contains_token"] != false ||
		responseDiagnostics["contains_provider_url"] != false {
		t.Fatalf("attempt response diagnostics = %#v", responseDiagnostics)
	}
	encoded, _ := json.Marshal(summary)
	for _, leak := range []string{"assops/template/demo-main", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "idempotency_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("attempt ledger summary leaked %q: %s", leak, encoded)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestProviderReviewAttemptLedgerForApprovalRedactsPersistedAttempts(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	server := &Server{store: &Store{DB: sqlx.NewDb(db, "sqlmock")}}
	mock.ExpectQuery(`(?s)SELECT.*FROM provider_review_attempts.*WHERE operation_approval_id=\$1`).
		WithArgs("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa").
		WillReturnRows(sqlmock.NewRows([]string{
			"id",
			"operation_name",
			"endpoint_key",
			"status",
			"replay_check",
			"conflict_policy",
			"retry_policy",
			"operation_order",
			"depends_on_operation",
			"dependency_status",
			"request_summary",
			"response_diagnostics",
			"provider_api_call_made",
			"provider_api_mutation",
			"external_call_made",
		}).AddRow(
			"44444444-4444-4444-4444-444444444444",
			"open_review_request",
			"github.open_review",
			"planned",
			"detect_existing_open_review",
			"reuse_existing_review_request",
			"retry_only_after_response_diagnostics",
			30,
			"commit_starter_files",
			"waiting_for_dependency",
			[]byte(`{"mode":"raw_attempt_request_summary","operation_name":"open_review_request","endpoint_key":"github.open_review","payload_builder":"raw_builder","response_handler":"raw_handler","execution_status":"ready","request_body_included":true,"headers_included":true,"idempotency_key_included":true,"external_call_made":true,"provider_api_call_made":true,"provider_api_mutation":"enabled","contains_token":true,"contains_provider_url":true,"contains_repository_ref":true,"contains_branch_name":true,"contains_file_content":true,"token":"secret-token","url":"https://api.github.example.test/repos/acme/secret-repo/pulls","repo":"secret-repo","content":"do-not-include"}`),
			[]byte(`{"mode":"raw_attempt_response_diagnostics","endpoint_key":"github.open_review","status":"ready","success_status_class":"2xx","retryable_status_classes":["5xx","4xx","secret-token"],"response_body_included":true,"headers_included":true,"contains_token":true,"contains_provider_url":true,"provider_api_call_made":true,"provider_api_mutation":"enabled","external_call_made":true,"body":"do-not-include","url":"https://api.github.example.test/repos/acme/secret-repo/pulls","token":"secret-token"}`),
			false,
			"disabled",
			false,
		))
	summary, err := server.providerReviewAttemptLedgerForApproval(context.Background(), "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("providerReviewAttemptLedgerForApproval: %v", err)
	}
	if summary["status"] != "recorded" ||
		summary["attempt_count"] != 1 ||
		summary["provider_api_call_made"] != false ||
		summary["provider_api_mutation"] != "disabled" ||
		summary["idempotency_key_included"] != false {
		t.Fatalf("attempt ledger summary = %#v", summary)
	}
	orchestration := mapFromAny(summary["orchestration"])
	if orchestration["dependency_chain_status"] != "waiting_for_dependency" ||
		orchestration["waiting_count"] != 1 ||
		orchestration["next_operation"] != "" ||
		orchestration["provider_api_call_made"] != false {
		t.Fatalf("persisted attempt orchestration summary = %#v", orchestration)
	}
	candidate := mapFromAny(orchestration["execution_candidate"])
	if candidate["mode"] != "redacted_attempt_execution_candidate" ||
		candidate["status"] != "blocked" ||
		candidate["next_operation"] != "" ||
		candidate["provider_api_mutation"] != "disabled" {
		t.Fatalf("persisted attempt execution candidate = %#v", candidate)
	}
	blockedReasons := stringSliceFromAny(candidate["blocked_reasons"])
	if len(blockedReasons) != 1 || blockedReasons[0] != "provider_review_attempt_not_ready" {
		t.Fatalf("persisted attempt execution candidate blocked reasons = %#v", blockedReasons)
	}
	operations := sliceOfMapsFromAny(summary["operations"])
	if len(operations) != 1 ||
		operations[0]["name"] != "open_review_request" ||
		operations[0]["endpoint_key"] != "github.open_review" ||
		operations[0]["operation_order"] != 30 ||
		operations[0]["depends_on_operation"] != "commit_starter_files" ||
		operations[0]["dependency_status"] != "waiting_for_dependency" ||
		operations[0]["idempotency_key_included"] != false {
		t.Fatalf("attempt ledger operations = %#v", operations)
	}
	requestSummary := mapFromAny(operations[0]["request_summary"])
	if requestSummary["mode"] != "redacted_attempt_request_summary" ||
		requestSummary["payload_builder"] != "build_redacted_provider_request" ||
		requestSummary["response_handler"] != "handle_provider_response" ||
		requestSummary["execution_status"] != "blocked" ||
		requestSummary["request_body_included"] != false ||
		requestSummary["headers_included"] != false ||
		requestSummary["provider_api_call_made"] != false ||
		requestSummary["provider_api_mutation"] != "disabled" ||
		requestSummary["idempotency_key_included"] != false ||
		requestSummary["contains_token"] != false ||
		requestSummary["contains_provider_url"] != false ||
		requestSummary["contains_repository_ref"] != false ||
		requestSummary["contains_branch_name"] != false ||
		requestSummary["contains_file_content"] != false {
		t.Fatalf("persisted attempt request summary = %#v", requestSummary)
	}
	responseDiagnostics := mapFromAny(operations[0]["response_diagnostics"])
	if responseDiagnostics["mode"] != "redacted_attempt_response_diagnostics" ||
		responseDiagnostics["status"] != "blocked" ||
		responseDiagnostics["success_status_class"] != "2xx" ||
		responseDiagnostics["response_body_included"] != false ||
		responseDiagnostics["headers_included"] != false ||
		responseDiagnostics["provider_api_call_made"] != false ||
		responseDiagnostics["provider_api_mutation"] != "disabled" ||
		responseDiagnostics["contains_token"] != false ||
		responseDiagnostics["contains_provider_url"] != false {
		t.Fatalf("persisted attempt response diagnostics = %#v", responseDiagnostics)
	}
	retryable := stringSliceFromAny(responseDiagnostics["retryable_status_classes"])
	if len(retryable) != 2 || retryable[0] != "5xx" || retryable[1] != "4xx" {
		t.Fatalf("persisted attempt retryable classes = %#v", retryable)
	}
	persistedCandidateAdapterContract := mapFromAny(candidate["adapter_contract"])
	persistedRetryable := stringSliceFromAny(persistedCandidateAdapterContract["retryable_status_classes"])
	if len(persistedRetryable) != 0 {
		t.Fatalf("persisted attempt adapter contract retry classes = %#v", persistedRetryable)
	}
	encoded, _ := json.Marshal(summary)
	for _, leak := range []string{"idempotency_key_material", "idempotency_key_hash", "secret-token", "api.github.example.test", "secret-repo", "raw_builder", "raw_handler", "do-not-include", "raw_attempt_response_diagnostics"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("persisted attempt ledger leaked %q: %s", leak, encoded)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestProviderReviewAttemptLedgerForApprovalHandlesEmptyInputAndRows(t *testing.T) {
	serverWithoutDB := &Server{}
	emptySummary, err := serverWithoutDB.providerReviewAttemptLedgerForApproval(context.Background(), "")
	if err != nil {
		t.Fatalf("providerReviewAttemptLedgerForApproval empty id: %v", err)
	}
	if emptySummary["status"] != "not_recorded" || emptySummary["attempt_count"] != 0 {
		t.Fatalf("empty id summary = %#v", emptySummary)
	}

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	server := &Server{store: &Store{DB: sqlx.NewDb(db, "sqlmock")}}
	mock.ExpectQuery(`(?s)SELECT.*FROM provider_review_attempts.*WHERE operation_approval_id=\$1`).
		WithArgs("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa").
		WillReturnRows(sqlmock.NewRows([]string{
			"id",
			"operation_name",
			"endpoint_key",
			"status",
			"replay_check",
			"conflict_policy",
			"retry_policy",
			"operation_order",
			"depends_on_operation",
			"dependency_status",
			"request_summary",
			"response_diagnostics",
			"provider_api_call_made",
			"provider_api_mutation",
			"external_call_made",
		}))
	zeroSummary, err := server.providerReviewAttemptLedgerForApproval(context.Background(), "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("providerReviewAttemptLedgerForApproval zero rows: %v", err)
	}
	if zeroSummary["status"] != "not_recorded" || zeroSummary["attempt_count"] != 0 {
		t.Fatalf("zero rows summary = %#v", zeroSummary)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestProviderReviewAttemptResponseDiagnosticSanitizers(t *testing.T) {
	for _, item := range []struct {
		input string
		want  string
	}{
		{"pending", "pending"},
		{"success", "success"},
		{"retryable", "retryable"},
		{"failed", "failed"},
		{"blocked", "blocked"},
		{"  success  ", "success"},
		{"ready", "blocked"},
		{"rate_limited", "blocked"},
		{"FAILED", "blocked"},
		{"", "blocked"},
		{"<script>alert(1)</script>", "blocked"},
	} {
		if got := safeProviderReviewAttemptResponseStatus(item.input); got != item.want {
			t.Fatalf("safeProviderReviewAttemptResponseStatus(%q) = %q, want %q", item.input, got, item.want)
		}
	}
	for _, item := range []struct {
		input string
		want  string
	}{
		{"2xx", "2xx"},
		{"4xx", "4xx"},
		{"5xx", "5xx"},
		{"  4xx  ", "4xx"},
		{"3xx", ""},
		{"secret-token", ""},
		{"", ""},
		{"<script>alert(1)</script>", ""},
	} {
		if got := safeProviderReviewStatusClass(item.input); got != item.want {
			t.Fatalf("safeProviderReviewStatusClass(%q) = %q, want %q", item.input, got, item.want)
		}
	}
	classes := safeProviderReviewStatusClasses([]string{"5xx", "4xx", "5xx", "3xx", "secret-token", "2xx"})
	if len(classes) != 3 || classes[0] != "5xx" || classes[1] != "4xx" || classes[2] != "2xx" {
		t.Fatalf("safeProviderReviewStatusClasses mixed = %#v", classes)
	}
	if got := safeProviderReviewStatusClasses(nil); len(got) != 0 {
		t.Fatalf("safeProviderReviewStatusClasses nil = %#v", got)
	}
	for _, item := range []struct {
		input string
		want  string
	}{
		{"github.create_branch_ref", "github.create_branch_ref"},
		{"github.commit_files", "github.commit_files"},
		{"github.open_review", "github.open_review"},
		{"gitea.create_branch_ref", "gitea.create_branch_ref"},
		{"gitea.commit_files", "gitea.commit_files"},
		{"gitea.open_review", "gitea.open_review"},
		{"  github.open_review  ", "github.open_review"},
		{"github.secret", ""},
		{"secret-token", ""},
		{"<script>alert(1)</script>", ""},
		{"", ""},
	} {
		if got := safeProviderReviewEndpointKey(item.input); got != item.want {
			t.Fatalf("safeProviderReviewEndpointKey(%q) = %q, want %q", item.input, got, item.want)
		}
	}
	for _, item := range []struct {
		endpoint string
		provider string
		adapter  string
	}{
		{"github.create_branch_ref", "github", "github_provider_review_adapter"},
		{"github.commit_files", "github", "github_provider_review_adapter"},
		{"github.open_review", "github", "github_provider_review_adapter"},
		{"gitea.create_branch_ref", "gitea", "gitea_provider_review_adapter"},
		{"gitea.commit_files", "gitea", "gitea_provider_review_adapter"},
		{"gitea.open_review", "gitea", "gitea_provider_review_adapter"},
		{"provider.open_review", "", ""},
		{"", "", ""},
	} {
		if got := providerReviewProviderFromEndpointKey(item.endpoint); got != item.provider {
			t.Fatalf("providerReviewProviderFromEndpointKey(%q) = %q, want %q", item.endpoint, got, item.provider)
		}
		if got := providerReviewAdapterKindForProvider(item.provider); got != item.adapter {
			t.Fatalf("providerReviewAdapterKindForProvider(%q) = %q, want %q", item.provider, got, item.adapter)
		}
	}
	for _, item := range []struct {
		input string
		want  string
	}{
		{"github", "github"},
		{"gitea", "gitea"},
		{"GitHub", ""},
		{"raw_provider", ""},
		{"", ""},
	} {
		if got := safeProviderReviewProviderType(item.input); got != item.want {
			t.Fatalf("safeProviderReviewProviderType(%q) = %q, want %q", item.input, got, item.want)
		}
	}
	for _, item := range []struct {
		operation string
		method    string
		shape     string
	}{
		{"create_branch_ref", "POST", "ref_from_target_branch"},
		{"commit_starter_files", "PUT", "content_redacted_file_batch"},
		{"open_review_request", "POST", "review_request"},
		{"raw_operation", "", ""},
	} {
		if got := providerReviewMethodForOperation(item.operation); got != item.method {
			t.Fatalf("providerReviewMethodForOperation(%q) = %q, want %q", item.operation, got, item.method)
		}
		if got := providerReviewPayloadShapeForOperation(item.operation); got != item.shape {
			t.Fatalf("providerReviewPayloadShapeForOperation(%q) = %q, want %q", item.operation, got, item.shape)
		}
	}
	for _, item := range []struct {
		operation         string
		endpointOperation string
		successClasses    []string
		retryClasses      []string
		failureClasses    []string
		unlockOperation   string
		unlockStatus      string
	}{
		{"create_branch_ref", "create_branch_ref", []string{"2xx"}, []string{"5xx"}, []string{"4xx"}, "commit_starter_files", "dependency_satisfied"},
		{"commit_starter_files", "commit_files", []string{"2xx"}, []string{"5xx"}, []string{"4xx"}, "open_review_request", "dependency_satisfied"},
		{"open_review_request", "open_review", []string{"2xx"}, []string{"5xx"}, []string{"4xx"}, "", ""},
		{"raw_operation", "", []string{}, []string{}, []string{}, "", ""},
	} {
		if got := providerReviewEndpointOperationForAttempt(item.operation); got != item.endpointOperation {
			t.Fatalf("providerReviewEndpointOperationForAttempt(%q) = %q, want %q", item.operation, got, item.endpointOperation)
		}
		if got := providerReviewExpectedSuccessClassesForOperation(item.operation); !reflect.DeepEqual(got, item.successClasses) {
			t.Fatalf("providerReviewExpectedSuccessClassesForOperation(%q) = %#v, want %#v", item.operation, got, item.successClasses)
		}
		if got := providerReviewExpectedRetryClassesForOperation(item.operation); !reflect.DeepEqual(got, item.retryClasses) {
			t.Fatalf("providerReviewExpectedRetryClassesForOperation(%q) = %#v, want %#v", item.operation, got, item.retryClasses)
		}
		if got := providerReviewTerminalFailureClassesForOperation(item.operation); !reflect.DeepEqual(got, item.failureClasses) {
			t.Fatalf("providerReviewTerminalFailureClassesForOperation(%q) = %#v, want %#v", item.operation, got, item.failureClasses)
		}
		if got := providerReviewAttemptDependencyUnlockOperation(item.operation); got != item.unlockOperation {
			t.Fatalf("providerReviewAttemptDependencyUnlockOperation(%q) = %q, want %q", item.operation, got, item.unlockOperation)
		}
		if got := providerReviewAttemptDependencyUnlockStatus(item.unlockOperation); got != item.unlockStatus {
			t.Fatalf("providerReviewAttemptDependencyUnlockStatus(%q) = %q, want %q", item.unlockOperation, got, item.unlockStatus)
		}
	}
	for _, item := range []struct {
		provider  string
		operation string
		want      string
	}{
		{"github", "create_branch_ref", "github_git_refs_path_template"},
		{"github", "commit_starter_files", "github_repository_contents_path_template"},
		{"github", "open_review_request", "github_pull_request_path_template"},
		{"gitea", "create_branch_ref", "gitea_git_refs_path_template"},
		{"gitea", "commit_starter_files", "gitea_repository_contents_path_template"},
		{"gitea", "open_review_request", "gitea_merge_request_path_template"},
		{"raw_provider", "create_branch_ref", ""},
		{"github", "raw_operation", ""},
	} {
		if got := providerReviewEndpointPathTemplateKeyForOperation(item.provider, item.operation); got != item.want {
			t.Fatalf("providerReviewEndpointPathTemplateKeyForOperation(%q, %q) = %q, want %q", item.provider, item.operation, got, item.want)
		}
	}
	for _, item := range []struct {
		provider string
		auth     string
		accept   string
	}{
		{"github", "bearer_token", "application/vnd.github+json"},
		{"GitHub", "bearer_token", "application/vnd.github+json"},
		{"gitea", "token", "application/json"},
		{"Gitea", "token", "application/json"},
		{"raw_provider", "", ""},
	} {
		if got := providerReviewAuthSchemeForProvider(item.provider); got != item.auth {
			t.Fatalf("providerReviewAuthSchemeForProvider(%q) = %q, want %q", item.provider, got, item.auth)
		}
		if got := providerReviewAcceptHeaderForProvider(item.provider); got != item.accept {
			t.Fatalf("providerReviewAcceptHeaderForProvider(%q) = %q, want %q", item.provider, got, item.accept)
		}
	}
	for _, item := range []struct {
		provider  string
		operation string
		endpoint  string
		auth      string
		accept    string
	}{
		{"github", "create_branch_ref", "github.create_branch_ref", "bearer_token", "application/vnd.github+json"},
		{"gitea", "create_branch_ref", "gitea.create_branch_ref", "token", "application/json"},
		{"gitea", "commit_starter_files", "gitea.commit_files", "token", "application/json"},
		{"gitea", "open_review_request", "gitea.open_review", "token", "application/json"},
	} {
		transportPlan := providerReviewAttemptAdapterTransportPlan(item.provider, item.operation)
		if transportPlan["mode"] != "redacted_attempt_adapter_transport_plan" ||
			transportPlan["transport_ready"] != true ||
			transportPlan["transport_ready_reason"] != "ready" ||
			transportPlan["provider_type"] != item.provider ||
			transportPlan["operation_name"] != item.operation ||
			transportPlan["endpoint_key"] != item.endpoint ||
			transportPlan["auth_scheme"] != item.auth ||
			transportPlan["accept_header"] != item.accept ||
			transportPlan["provider_api_call_made"] != false ||
			transportPlan["provider_api_mutation"] != "disabled" ||
			transportPlan["contains_token"] != false ||
			transportPlan["contains_provider_url"] != false {
			t.Fatalf("providerReviewAttemptAdapterTransportPlan(%q, %q) = %#v", item.provider, item.operation, transportPlan)
		}
	}
}

func TestProviderReviewAttemptDependencySanitizers(t *testing.T) {
	for _, item := range []struct {
		input string
		want  string
	}{
		{"independent", "independent"},
		{"waiting_for_dependency", "waiting_for_dependency"},
		{"dependency_satisfied", "dependency_satisfied"},
		{"dependency_failed", "dependency_failed"},
		{"", "independent"},
		{"running", "independent"},
		{"<script>alert(1)</script>", "independent"},
		{strings.Repeat("x", 200), "independent"},
	} {
		if got := safeProviderReviewAttemptDependencyStatus(item.input); got != item.want {
			t.Fatalf("safeProviderReviewAttemptDependencyStatus(%q) = %q, want %q", item.input, got, item.want)
		}
	}
	for _, item := range []struct {
		input string
		want  string
		ready bool
	}{
		{"independent", "independent", true},
		{"dependency_satisfied", "dependency_satisfied", true},
		{"waiting_for_dependency", "waiting_for_dependency", false},
		{"dependency_failed", "dependency_failed", false},
		{"raw_dependency", "blocked", false},
		{"", "blocked", false},
	} {
		if got := safeProviderReviewAttemptClaimDependencyStatus(item.input); got != item.want {
			t.Fatalf("safeProviderReviewAttemptClaimDependencyStatus(%q) = %q, want %q", item.input, got, item.want)
		}
		if got := providerReviewAttemptClaimDependencyReady(item.input); got != item.ready {
			t.Fatalf("providerReviewAttemptClaimDependencyReady(%q) = %v, want %v", item.input, got, item.ready)
		}
	}
	for _, item := range []struct {
		input string
		want  string
	}{
		{"not_recorded", "not_recorded"},
		{"ready", "ready"},
		{"waiting_for_dependency", "waiting_for_dependency"},
		{"blocked", "blocked"},
		{"completed", "completed"},
		{"", "not_recorded"},
		{"<script>alert(1)</script>", "not_recorded"},
	} {
		if got := safeProviderReviewAttemptDependencyChainStatus(item.input); got != item.want {
			t.Fatalf("safeProviderReviewAttemptDependencyChainStatus(%q) = %q, want %q", item.input, got, item.want)
		}
	}
	for _, item := range []struct {
		input string
		want  string
	}{
		{"not_recorded", "not_recorded"},
		{"planned", "planned"},
		{"running", "running"},
		{"completed", "completed"},
		{"blocked", "blocked"},
		{"ready", "not_recorded"},
		{"", "not_recorded"},
		{"<script>alert(1)</script>", "not_recorded"},
	} {
		if got := safeProviderReviewAttemptOrchestrationStatus(item.input); got != item.want {
			t.Fatalf("safeProviderReviewAttemptOrchestrationStatus(%q) = %q, want %q", item.input, got, item.want)
		}
	}
	for _, item := range []struct {
		input string
		want  string
	}{
		{"", ""},
		{"create_branch_ref", "create_branch_ref"},
		{"commit_starter_files", "commit_starter_files"},
		{"open_review_request", ""},
		{"secret-repo", ""},
		{"<script>alert(1)</script>", ""},
		{strings.Repeat("x", 200), ""},
	} {
		if got := safeProviderReviewAttemptDependencyName(item.input); got != item.want {
			t.Fatalf("safeProviderReviewAttemptDependencyName(%q) = %q, want %q", item.input, got, item.want)
		}
	}
}

func TestProviderReviewAttemptOrchestrationSummaryBlocksUnknownStatus(t *testing.T) {
	summary := providerReviewAttemptOrchestrationSummary([]map[string]any{{
		"name":              "create_branch_ref",
		"status":            "retrying",
		"dependency_status": "independent",
	}})
	if summary["ready_count"] != 0 ||
		summary["blocked_count"] != 1 ||
		summary["next_operation"] != "" ||
		summary["dependency_chain_status"] != "blocked" {
		t.Fatalf("unknown status orchestration summary = %#v", summary)
	}
	candidate := mapFromAny(summary["execution_candidate"])
	if candidate["next_operation"] != "" || candidate["status"] != "blocked" {
		t.Fatalf("unknown status execution candidate = %#v", candidate)
	}
}

func TestProviderReviewAttemptAdapterResponsePlan(t *testing.T) {
	for _, item := range []struct {
		name                 string
		operationName        string
		endpointKey          string
		order                int
		handler              string
		status               string
		unlockOperation      string
		dependencyStatus     string
		requiresDependency   bool
		expectedResponseMode string
	}{
		{
			name:                 "create branch unlocks commit",
			operationName:        "create_branch_ref",
			endpointKey:          "github.create_branch_ref",
			order:                10,
			handler:              "handle_branch_ref_response",
			status:               "pending",
			unlockOperation:      "commit_starter_files",
			dependencyStatus:     "dependency_satisfied",
			requiresDependency:   true,
			expectedResponseMode: "redacted_attempt_adapter_response_plan",
		},
		{
			name:                 "commit unlocks review",
			operationName:        "commit_starter_files",
			endpointKey:          "github.commit_files",
			order:                20,
			handler:              "handle_commit_files_response",
			status:               "retryable",
			unlockOperation:      "open_review_request",
			dependencyStatus:     "dependency_satisfied",
			requiresDependency:   true,
			expectedResponseMode: "redacted_attempt_adapter_response_plan",
		},
		{
			name:                 "review request has no next dependency",
			operationName:        "open_review_request",
			endpointKey:          "gitea.open_review",
			order:                30,
			handler:              "handle_review_request_response",
			status:               "success",
			unlockOperation:      "",
			dependencyStatus:     "",
			requiresDependency:   false,
			expectedResponseMode: "redacted_attempt_adapter_response_plan",
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			responsePlan := providerReviewAttemptAdapterResponsePlan(
				map[string]any{
					"name":            item.operationName,
					"endpoint_key":    item.endpointKey,
					"operation_order": item.order,
				},
				map[string]any{
					"response_handler": item.handler,
				},
				map[string]any{
					"status": item.status,
				},
			)
			if responsePlan["mode"] != item.expectedResponseMode ||
				responsePlan["response_recording_state"] != "blocked" ||
				responsePlan["response_recording_ready"] != false ||
				responsePlan["operation_name"] != item.operationName ||
				responsePlan["endpoint_key"] != item.endpointKey ||
				responsePlan["operation_order"] != item.order ||
				responsePlan["response_handler"] != item.handler ||
				responsePlan["response_status"] != item.status ||
				responsePlan["success_attempt_status"] != "completed" ||
				responsePlan["retry_attempt_status"] != "planned" ||
				responsePlan["failure_attempt_status"] != "failed" ||
				responsePlan["dependency_unlocks_operation"] != item.unlockOperation ||
				responsePlan["dependency_update_status"] != item.dependencyStatus ||
				responsePlan["requires_dependency_update"] != item.requiresDependency ||
				len(mapFromAny(responsePlan["result_recording_plan"])) == 0 ||
				responsePlan["provider_api_call_made"] != false ||
				responsePlan["provider_api_mutation"] != "disabled" ||
				responsePlan["response_body_included"] != false ||
				responsePlan["headers_included"] != false ||
				responsePlan["provider_request_id_included"] != false ||
				responsePlan["contains_token"] != false ||
				responsePlan["contains_provider_url"] != false ||
				responsePlan["contains_repository_ref"] != false ||
				responsePlan["contains_branch_name"] != false ||
				responsePlan["contains_file_content"] != false ||
				responsePlan["response_boundary_redacted"] != true {
				t.Fatalf("providerReviewAttemptAdapterResponsePlan() = %#v", responsePlan)
			}
			if got := stringSliceFromAny(responsePlan["expected_success_classes"]); !reflect.DeepEqual(got, []string{"2xx"}) {
				t.Fatalf("response plan success classes = %#v", got)
			}
			if got := stringSliceFromAny(responsePlan["retryable_status_classes"]); !reflect.DeepEqual(got, []string{"5xx"}) {
				t.Fatalf("response plan retry classes = %#v", got)
			}
			if got := stringSliceFromAny(responsePlan["terminal_failure_status_classes"]); !reflect.DeepEqual(got, []string{"4xx"}) {
				t.Fatalf("response plan failure classes = %#v", got)
			}
			resultPlan := mapFromAny(responsePlan["result_recording_plan"])
			if resultPlan["mode"] != "redacted_attempt_adapter_result_recording_plan" ||
				resultPlan["result_recording_state"] != "blocked" ||
				resultPlan["result_recording_ready"] != false ||
				resultPlan["result_recording_ready_reason"] != "provider_review_result_recording_not_armed" ||
				resultPlan["result_recording_metadata_ready"] != true ||
				resultPlan["operation_name"] != item.operationName ||
				resultPlan["endpoint_key"] != item.endpointKey ||
				resultPlan["operation_order"] != item.order ||
				resultPlan["response_status"] != item.status ||
				resultPlan["success_attempt_status"] != "completed" ||
				resultPlan["retry_attempt_status"] != "planned" ||
				resultPlan["failure_attempt_status"] != "failed" ||
				resultPlan["dependency_unlocks_operation"] != item.unlockOperation ||
				resultPlan["dependency_update_status"] != item.dependencyStatus ||
				resultPlan["requires_response_handler"] != true ||
				resultPlan["requires_response_diagnostics"] != true ||
				resultPlan["requires_transaction_boundary"] != true ||
				resultPlan["requires_dependency_update"] != item.requiresDependency ||
				resultPlan["requires_mutation_arming"] != true ||
				resultPlan["result_recorded"] != false ||
				resultPlan["response_classified"] != false ||
				resultPlan["attempt_status_mapped"] != false ||
				resultPlan["attempt_result_persisted"] != false ||
				resultPlan["dependency_update_staged"] != false ||
				resultPlan["provider_request_id_recorded"] != false ||
				resultPlan["provider_response_status_recorded"] != false ||
				resultPlan["provider_response_body_recorded"] != false ||
				resultPlan["provider_response_headers_recorded"] != false ||
				resultPlan["external_call_made"] != false ||
				resultPlan["provider_api_call_made"] != false ||
				resultPlan["provider_api_mutation"] != "disabled" ||
				resultPlan["response_body_included"] != false ||
				resultPlan["headers_included"] != false ||
				resultPlan["provider_request_id_included"] != false ||
				resultPlan["provider_response_status_included"] != false ||
				resultPlan["provider_url_included"] != false ||
				resultPlan["idempotency_key_included"] != false ||
				resultPlan["contains_token"] != false ||
				resultPlan["contains_provider_url"] != false ||
				resultPlan["contains_repository_ref"] != false ||
				resultPlan["contains_branch_name"] != false ||
				resultPlan["contains_file_content"] != false ||
				resultPlan["result_recording_boundary_redacted"] != true {
				t.Fatalf("result recording plan = %#v", resultPlan)
			}
			resultSequence := stringSliceFromAny(resultPlan["result_recording_sequence"])
			if len(resultSequence) != 5 ||
				resultSequence[0] != "classify_provider_response" ||
				resultSequence[1] != "map_attempt_status" ||
				resultSequence[2] != "stage_dependency_update" ||
				resultSequence[3] != "record_redacted_result" ||
				resultSequence[4] != "persist_attempt_result" {
				t.Fatalf("result recording sequence = %#v", resultSequence)
			}
			resultDiagnosticFields := stringSliceFromAny(resultPlan["result_recording_diagnostic_fields"])
			if len(resultDiagnosticFields) != 4 ||
				resultDiagnosticFields[0] != "status_class" ||
				resultDiagnosticFields[1] != "retry_class" ||
				resultDiagnosticFields[2] != "dependency_update_required" ||
				resultDiagnosticFields[3] != "provider_request_id_present" {
				t.Fatalf("result recording diagnostic fields = %#v", resultDiagnosticFields)
			}
			resultPersistedFields := stringSliceFromAny(resultPlan["result_recording_persisted_fields"])
			if len(resultPersistedFields) != 4 ||
				resultPersistedFields[0] != "attempt_status" ||
				resultPersistedFields[3] != "retry_class" {
				t.Fatalf("result recording persisted fields = %#v", resultPersistedFields)
			}
			resultSuppressedFields := stringSliceFromAny(resultPlan["result_recording_suppressed_fields"])
			if len(resultSuppressedFields) != 9 ||
				resultSuppressedFields[0] != "provider_request_id" ||
				resultSuppressedFields[8] != "file_content" {
				t.Fatalf("result recording suppressed fields = %#v", resultSuppressedFields)
			}
			resultBlockedReasons := stringSliceFromAny(resultPlan["blocked_reasons"])
			if len(resultBlockedReasons) != 4 ||
				resultBlockedReasons[0] != "provider_review_result_recording_not_armed" ||
				resultBlockedReasons[1] != "provider_api_call_not_made" ||
				resultBlockedReasons[2] != "provider_review_adapter_not_implemented" ||
				resultBlockedReasons[3] != "provider_review_mutation_not_armed" {
				t.Fatalf("result recording blocked reasons = %#v", resultBlockedReasons)
			}
			encoded, _ := json.Marshal(responsePlan)
			for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("response plan leaked %q: %s", leak, encoded)
				}
			}
		})
	}
	t.Run("empty operation returns empty plan", func(t *testing.T) {
		if got := providerReviewAttemptAdapterResponsePlan(nil, nil, nil); len(got) != 0 {
			t.Fatalf("empty operation response plan = %#v", got)
		}
	})
	t.Run("redacts invalid operation name even with known endpoint", func(t *testing.T) {
		got := providerReviewAttemptAdapterResponsePlan(
			map[string]any{
				"name":         "raw_operation",
				"endpoint_key": "github.create_branch_ref",
			},
			map[string]any{
				"response_handler": "raw_handler",
			},
			map[string]any{
				"status": "raw_status",
			},
		)
		if len(got) != 0 {
			t.Fatalf("invalid operation response plan should be empty: %#v", got)
		}
	})
	t.Run("result recording plan rejects mismatched response mode", func(t *testing.T) {
		got := providerReviewAttemptAdapterResultRecordingPlan(
			map[string]any{
				"name":         "create_branch_ref",
				"endpoint_key": "github.create_branch_ref",
			},
			map[string]any{
				"mode": "raw_response_plan",
			},
		)
		if len(got) != 0 {
			t.Fatalf("mismatched response mode result plan should be empty: %#v", got)
		}
	})
}

func TestProviderReviewAttemptAdapterRequestMaterializationPlan(t *testing.T) {
	for _, item := range []struct {
		name                string
		provider            string
		operationName       string
		endpointKey         string
		order               int
		method              string
		endpointTemplateKey string
		payloadShape        string
		payloadBuilder      string
		requiresManifest    bool
		wantNonEmpty        bool
	}{
		{
			name:                "github branch ref request stays redacted",
			provider:            "github",
			operationName:       "create_branch_ref",
			endpointKey:         "github.create_branch_ref",
			order:               10,
			method:              "POST",
			endpointTemplateKey: "github_git_refs_path_template",
			payloadShape:        "ref_from_target_branch",
			payloadBuilder:      "build_redacted_branch_ref_request",
			requiresManifest:    false,
			wantNonEmpty:        true,
		},
		{
			name:                "github commit request requires file manifest without content",
			provider:            "github",
			operationName:       "commit_starter_files",
			endpointKey:         "github.commit_files",
			order:               20,
			method:              "PUT",
			endpointTemplateKey: "github_repository_contents_path_template",
			payloadShape:        "content_redacted_file_batch",
			payloadBuilder:      "build_redacted_file_batch_request",
			requiresManifest:    true,
			wantNonEmpty:        true,
		},
		{
			name:                "gitea review request uses merge request template",
			provider:            "gitea",
			operationName:       "open_review_request",
			endpointKey:         "gitea.open_review",
			order:               30,
			method:              "POST",
			endpointTemplateKey: "gitea_merge_request_path_template",
			payloadShape:        "review_request",
			payloadBuilder:      "build_redacted_review_request",
			requiresManifest:    false,
			wantNonEmpty:        true,
		},
		{
			name:          "unknown provider returns empty plan",
			provider:      "raw_provider",
			operationName: "create_branch_ref",
			endpointKey:   "github.create_branch_ref",
		},
		{
			name:          "unknown operation returns empty plan",
			provider:      "github",
			operationName: "raw_operation",
			endpointKey:   "github.create_branch_ref",
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			plan := providerReviewAttemptAdapterRequestMaterializationPlan(
				map[string]any{
					"name":            item.operationName,
					"endpoint_key":    item.endpointKey,
					"operation_order": item.order,
				},
				map[string]any{
					"payload_builder": item.payloadBuilder,
				},
				item.provider,
			)
			if !item.wantNonEmpty {
				if len(plan) != 0 {
					t.Fatalf("request materialization plan should be empty: %#v", plan)
				}
				return
			}
			if plan["mode"] != "redacted_attempt_adapter_request_materialization_plan" ||
				plan["request_materialization_state"] != "blocked" ||
				plan["request_materialization_ready"] != false ||
				plan["request_materialization_ready_reason"] != "provider_request_materialization_not_armed" ||
				plan["provider_type"] != item.provider ||
				plan["operation_name"] != item.operationName ||
				plan["endpoint_key"] != item.endpointKey ||
				plan["operation_order"] != item.order ||
				plan["method"] != item.method ||
				plan["endpoint_path_template_key"] != item.endpointTemplateKey ||
				plan["payload_shape"] != item.payloadShape ||
				plan["payload_builder"] != item.payloadBuilder ||
				plan["requires_request_builder"] != true ||
				plan["requires_provider_repository_context"] != true ||
				plan["requires_redacted_payload_summary"] != true ||
				plan["requires_starter_file_manifest"] != item.requiresManifest ||
				plan["request_builder_implemented"] != false ||
				plan["provider_repository_context_resolved"] != false ||
				plan["request_path_materialized"] != false ||
				plan["request_url_materialized"] != false ||
				plan["request_body_materialized"] != false ||
				plan["payload_materialized"] != false ||
				plan["headers_materialized"] != false ||
				plan["starter_file_manifest_materialized"] != false ||
				plan["authorization_header_materialized"] != false ||
				plan["external_call_made"] != false ||
				plan["provider_api_call_made"] != false ||
				plan["provider_api_mutation"] != "disabled" ||
				plan["request_body_included"] != false ||
				plan["headers_included"] != false ||
				plan["provider_url_included"] != false ||
				plan["repository_ref_included"] != false ||
				plan["branch_name_included"] != false ||
				plan["file_content_included"] != false ||
				plan["contains_token"] != false ||
				plan["contains_provider_url"] != false ||
				plan["contains_repository_ref"] != false ||
				plan["contains_branch_name"] != false ||
				plan["contains_file_content"] != false ||
				plan["request_materialization_boundary_redacted"] != true {
				t.Fatalf("request materialization plan = %#v", plan)
			}
			blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
			if len(blockedReasons) != 3 ||
				blockedReasons[0] != "provider_request_not_materialized" ||
				blockedReasons[1] != "provider_review_adapter_not_implemented" ||
				blockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("request materialization blocked reasons = %#v", blockedReasons)
			}
			encoded, _ := json.Marshal(plan)
			for _, leak := range []string{"https://", "api.github.example.test", "secret-token", "secret-repo", "feature/secret", "file content"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("request materialization plan leaked %q: %s", leak, encoded)
				}
			}
		})
	}
}

func TestProviderReviewAttemptAdapterInvocationPlan(t *testing.T) {
	operation := map[string]any{
		"name":            "create_branch_ref",
		"endpoint_key":    "github.create_branch_ref",
		"operation_order": 10,
	}
	claimPlan := map[string]any{"claim_metadata_ready": true}
	requestPlan := map[string]any{"request_materialization_ready": false}
	credentialPlan := map[string]any{"credential_binding_ready": false}
	runtimePlan := map[string]any{"runtime_ready": false}
	transportPlan := map[string]any{"mode": "redacted_attempt_adapter_transport_plan", "transport_ready": true, "retryable_status_classes": []string{"5xx"}}
	responsePlan := map[string]any{"response_recording_ready": false}
	transactionPlan := map[string]any{"transaction_metadata_ready": true}
	plan := providerReviewAttemptAdapterInvocationPlan(operation, claimPlan, requestPlan, credentialPlan, runtimePlan, transportPlan, responsePlan, transactionPlan)
	if plan["mode"] != "redacted_attempt_adapter_invocation_plan" ||
		plan["invocation_state"] != "blocked" ||
		plan["invocation_ready"] != false ||
		plan["invocation_ready_reason"] != "provider_api_invocation_not_armed" ||
		plan["operation_name"] != "create_branch_ref" ||
		plan["endpoint_key"] != "github.create_branch_ref" ||
		plan["operation_order"] != 10 ||
		plan["claim_metadata_ready"] != true ||
		plan["execution_lock_metadata_ready"] != true ||
		plan["credential_binding_ready"] != false ||
		plan["adapter_runtime_ready"] != false ||
		plan["request_materialization_ready"] != false ||
		plan["transport_metadata_ready"] != true ||
		plan["provider_send_metadata_ready"] != false ||
		plan["response_recording_ready"] != false ||
		plan["transaction_metadata_ready"] != true ||
		plan["claim_metadata_ready_reason"] != "ready" ||
		plan["execution_lock_ready_reason"] != "ready" ||
		plan["adapter_runtime_ready_reason"] != "provider_review_adapter_runtime_not_ready" ||
		plan["transport_metadata_ready_reason"] != "ready" ||
		plan["provider_send_ready_reason"] != "provider_request_send_not_armed" ||
		plan["transaction_metadata_ready_reason"] != "ready" ||
		plan["requires_attempt_claim"] != true ||
		plan["requires_idempotency_claim"] != true ||
		plan["requires_execution_lock"] != true ||
		plan["requires_credential_binding"] != true ||
		plan["requires_adapter_runtime"] != true ||
		plan["requires_request_materialization"] != true ||
		plan["requires_transport"] != true ||
		plan["requires_response_recording"] != true ||
		plan["requires_transaction_boundary"] != true ||
		plan["attempt_claim_recorded"] != false ||
		plan["idempotency_claim_recorded"] != false ||
		plan["execution_lock_acquired"] != false ||
		plan["duplicate_send_detected"] != false ||
		plan["credential_bound"] != false ||
		plan["adapter_runtime_bound"] != false ||
		plan["request_materialized"] != false ||
		plan["provider_request_sent"] != false ||
		plan["response_recorded"] != false ||
		plan["transaction_recorded"] != false ||
		plan["dependency_update_recorded"] != false ||
		plan["adapter_implemented"] != false ||
		plan["mutation_armed"] != false ||
		plan["external_call_made"] != false ||
		plan["provider_api_call_made"] != false ||
		plan["provider_api_mutation"] != "disabled" ||
		plan["request_body_included"] != false ||
		plan["response_body_included"] != false ||
		plan["headers_included"] != false ||
		plan["authorization_header_included"] != false ||
		plan["provider_url_included"] != false ||
		plan["idempotency_key_included"] != false ||
		plan["contains_token"] != false ||
		plan["contains_provider_url"] != false ||
		plan["contains_repository_ref"] != false ||
		plan["contains_branch_name"] != false ||
		plan["contains_file_content"] != false ||
		plan["invocation_boundary_redacted"] != true {
		t.Fatalf("providerReviewAttemptAdapterInvocationPlan() = %#v", plan)
	}
	sequence := stringSliceFromAny(plan["invocation_sequence"])
	if len(sequence) != 10 ||
		sequence[0] != "claim_attempt" ||
		sequence[2] != "claim_execution_lock" ||
		sequence[4] != "select_adapter_runtime" ||
		sequence[8] != "record_transaction_boundary" ||
		sequence[9] != "unlock_dependency" {
		t.Fatalf("invocation sequence = %#v", sequence)
	}
	subplans := stringSliceFromAny(plan["required_subplans"])
	if len(subplans) != 9 ||
		subplans[0] != "claim_plan" ||
		subplans[1] != "execution_lock_plan" ||
		subplans[6] != "provider_send_plan" ||
		subplans[8] != "transaction_plan" {
		t.Fatalf("invocation subplans = %#v", subplans)
	}
	executionLockPlan := mapFromAny(plan["execution_lock_plan"])
	if executionLockPlan["mode"] != "redacted_attempt_adapter_execution_lock_plan" ||
		executionLockPlan["execution_lock_state"] != "blocked" ||
		executionLockPlan["execution_lock_ready"] != false ||
		executionLockPlan["execution_lock_ready_reason"] != "provider_review_execution_lock_not_armed" ||
		executionLockPlan["execution_lock_metadata_ready"] != true ||
		executionLockPlan["execution_lock_metadata_ready_reason"] != "ready" ||
		executionLockPlan["operation_name"] != "create_branch_ref" ||
		executionLockPlan["endpoint_key"] != "github.create_branch_ref" ||
		executionLockPlan["operation_order"] != 10 ||
		executionLockPlan["claim_status_from"] != "planned" ||
		executionLockPlan["claim_status_to"] != "running" ||
		executionLockPlan["lock_scope"] != "provider_review_attempt_operation" ||
		executionLockPlan["lock_key_kind"] != "attempt_operation_hash" ||
		executionLockPlan["duplicate_send_policy"] != "block_duplicate_provider_send" ||
		executionLockPlan["stale_running_policy"] != "manual_recovery_required" ||
		executionLockPlan["requires_database_transaction"] != true ||
		executionLockPlan["requires_attempt_claim"] != true ||
		executionLockPlan["requires_attempt_status_planned"] != true ||
		executionLockPlan["requires_dependency_ready"] != true ||
		executionLockPlan["requires_optimistic_lock"] != true ||
		executionLockPlan["requires_idempotency_claim"] != true ||
		executionLockPlan["requires_mutation_arming"] != true ||
		executionLockPlan["claim_metadata_ready"] != true ||
		executionLockPlan["transaction_metadata_ready"] != true ||
		executionLockPlan["attempt_claim_recorded"] != false ||
		executionLockPlan["idempotency_claim_recorded"] != false ||
		executionLockPlan["execution_lock_acquired"] != false ||
		executionLockPlan["optimistic_lock_verified"] != false ||
		executionLockPlan["duplicate_send_detected"] != false ||
		executionLockPlan["stale_running_recovered"] != false ||
		executionLockPlan["provider_request_sent"] != false ||
		executionLockPlan["external_call_made"] != false ||
		executionLockPlan["provider_api_call_made"] != false ||
		executionLockPlan["provider_api_mutation"] != "disabled" ||
		executionLockPlan["request_body_included"] != false ||
		executionLockPlan["response_body_included"] != false ||
		executionLockPlan["headers_included"] != false ||
		executionLockPlan["authorization_header_included"] != false ||
		executionLockPlan["provider_url_included"] != false ||
		executionLockPlan["idempotency_key_included"] != false ||
		executionLockPlan["provider_request_id_included"] != false ||
		executionLockPlan["contains_token"] != false ||
		executionLockPlan["contains_provider_url"] != false ||
		executionLockPlan["contains_repository_ref"] != false ||
		executionLockPlan["contains_branch_name"] != false ||
		executionLockPlan["contains_file_content"] != false ||
		executionLockPlan["execution_lock_boundary_redacted"] != true {
		t.Fatalf("execution lock plan = %#v", executionLockPlan)
	}
	lockSequence := stringSliceFromAny(executionLockPlan["execution_lock_sequence"])
	if len(lockSequence) != 6 ||
		lockSequence[0] != "verify_attempt_status_planned" ||
		lockSequence[1] != "verify_dependency_ready" ||
		lockSequence[2] != "claim_attempt_running" ||
		lockSequence[3] != "claim_idempotency_scope" ||
		lockSequence[4] != "mark_duplicate_send_guard" ||
		lockSequence[5] != "release_lock_after_transaction" {
		t.Fatalf("execution lock sequence = %#v", lockSequence)
	}
	lockSuppressedFields := stringSliceFromAny(executionLockPlan["execution_lock_suppressed_fields"])
	if len(lockSuppressedFields) != 9 ||
		lockSuppressedFields[0] != "lock_key" ||
		lockSuppressedFields[4] != "authorization_header" ||
		lockSuppressedFields[8] != "file_content" {
		t.Fatalf("execution lock suppressed fields = %#v", lockSuppressedFields)
	}
	lockBoundaries := stringSliceFromAny(executionLockPlan["execution_lock_transaction_boundaries"])
	if len(lockBoundaries) != 4 ||
		lockBoundaries[0] != "claim_attempt_start" ||
		lockBoundaries[3] != "attempt_status_update" {
		t.Fatalf("execution lock transaction boundaries = %#v", lockBoundaries)
	}
	lockBlockedReasons := stringSliceFromAny(executionLockPlan["blocked_reasons"])
	if len(lockBlockedReasons) != 4 ||
		lockBlockedReasons[0] != "provider_review_execution_lock_not_armed" ||
		lockBlockedReasons[1] != "provider_review_attempt_claim_not_recorded" ||
		lockBlockedReasons[2] != "provider_idempotency_ledger_not_claimed" ||
		lockBlockedReasons[3] != "provider_review_mutation_not_armed" {
		t.Fatalf("execution lock blocked reasons = %#v", lockBlockedReasons)
	}
	providerSendPlan := mapFromAny(plan["provider_send_plan"])
	if providerSendPlan["mode"] != "redacted_attempt_adapter_provider_send_plan" ||
		providerSendPlan["provider_send_state"] != "blocked" ||
		providerSendPlan["provider_send_ready"] != false ||
		providerSendPlan["provider_send_ready_reason"] != "provider_request_send_not_armed" ||
		providerSendPlan["provider_send_metadata_ready"] != false ||
		providerSendPlan["provider_type"] != "github" ||
		providerSendPlan["operation_name"] != "create_branch_ref" ||
		providerSendPlan["endpoint_key"] != "github.create_branch_ref" ||
		providerSendPlan["operation_order"] != 10 ||
		providerSendPlan["method"] != "POST" ||
		providerSendPlan["payload_shape"] != "ref_from_target_branch" ||
		providerSendPlan["auth_scheme"] != "bearer_token" ||
		providerSendPlan["content_type"] != "application/json" ||
		providerSendPlan["timeout_seconds"] != 15 ||
		len(mapFromAny(providerSendPlan["retry_backoff_plan"])) == 0 ||
		providerSendPlan["requires_request_materialization"] != true ||
		providerSendPlan["requires_credential_binding"] != true ||
		providerSendPlan["requires_adapter_runtime"] != true ||
		providerSendPlan["requires_transport"] != true ||
		providerSendPlan["requires_retry_backoff_plan"] != true ||
		providerSendPlan["requires_mutation_arming"] != true ||
		providerSendPlan["request_materialization_ready"] != false ||
		providerSendPlan["credential_binding_ready"] != false ||
		providerSendPlan["adapter_runtime_ready"] != false ||
		providerSendPlan["transport_metadata_ready"] != true ||
		providerSendPlan["request_path_materialized"] != false ||
		providerSendPlan["request_url_materialized"] != false ||
		providerSendPlan["request_body_materialized"] != false ||
		providerSendPlan["headers_materialized"] != false ||
		providerSendPlan["authorization_header_materialized"] != false ||
		providerSendPlan["provider_client_bound"] != false ||
		providerSendPlan["credential_bound"] != false ||
		providerSendPlan["runtime_bound"] != false ||
		providerSendPlan["mutation_armed"] != false ||
		providerSendPlan["send_attempted"] != false ||
		providerSendPlan["provider_request_sent"] != false ||
		providerSendPlan["provider_response_received"] != false ||
		providerSendPlan["external_call_made"] != false ||
		providerSendPlan["provider_api_call_made"] != false ||
		providerSendPlan["provider_api_mutation"] != "disabled" ||
		providerSendPlan["request_body_included"] != false ||
		providerSendPlan["response_body_included"] != false ||
		providerSendPlan["headers_included"] != false ||
		providerSendPlan["authorization_header_included"] != false ||
		providerSendPlan["provider_url_included"] != false ||
		providerSendPlan["idempotency_key_included"] != false ||
		providerSendPlan["provider_request_id_included"] != false ||
		providerSendPlan["contains_token"] != false ||
		providerSendPlan["contains_provider_url"] != false ||
		providerSendPlan["contains_repository_ref"] != false ||
		providerSendPlan["contains_branch_name"] != false ||
		providerSendPlan["contains_file_content"] != false ||
		providerSendPlan["provider_send_boundary_redacted"] != true {
		t.Fatalf("provider send plan = %#v", providerSendPlan)
	}
	sendSequence := stringSliceFromAny(providerSendPlan["provider_send_sequence"])
	if len(sendSequence) != 6 ||
		sendSequence[0] != "bind_provider_client" ||
		sendSequence[1] != "apply_redacted_transport_metadata" ||
		sendSequence[2] != "verify_mutation_arming" ||
		sendSequence[3] != "stage_provider_request" ||
		sendSequence[4] != "send_provider_request" ||
		sendSequence[5] != "handoff_to_response_handler" {
		t.Fatalf("provider send sequence = %#v", sendSequence)
	}
	sendSuppressedFields := stringSliceFromAny(providerSendPlan["provider_send_suppressed_fields"])
	if len(sendSuppressedFields) != 10 ||
		sendSuppressedFields[0] != "request_url" ||
		sendSuppressedFields[4] != "authorization_header" ||
		sendSuppressedFields[9] != "file_content" {
		t.Fatalf("provider send suppressed fields = %#v", sendSuppressedFields)
	}
	retryBackoffPlan := mapFromAny(providerSendPlan["retry_backoff_plan"])
	if retryBackoffPlan["mode"] != "redacted_attempt_adapter_retry_backoff_plan" ||
		retryBackoffPlan["retry_backoff_state"] != "blocked" ||
		retryBackoffPlan["retry_backoff_ready"] != false ||
		retryBackoffPlan["retry_backoff_ready_reason"] != "provider_retry_backoff_not_armed" ||
		retryBackoffPlan["retry_backoff_metadata_ready"] != true ||
		retryBackoffPlan["operation_name"] != "create_branch_ref" ||
		retryBackoffPlan["endpoint_key"] != "github.create_branch_ref" ||
		retryBackoffPlan["operation_order"] != 10 ||
		retryBackoffPlan["retry_policy"] != "retry_only_after_response_diagnostics" ||
		retryBackoffPlan["max_attempts"] != 3 ||
		retryBackoffPlan["initial_backoff_seconds"] != 30 ||
		retryBackoffPlan["max_backoff_seconds"] != 300 ||
		retryBackoffPlan["jitter"] != "full" ||
		retryBackoffPlan["requires_response_diagnostics"] != true ||
		retryBackoffPlan["requires_idempotency_ledger"] != true ||
		retryBackoffPlan["requires_attempt_ledger"] != true ||
		retryBackoffPlan["requires_mutation_arming"] != true ||
		retryBackoffPlan["retry_scheduled"] != false ||
		retryBackoffPlan["retry_attempt_recorded"] != false ||
		retryBackoffPlan["retry_after_value_recorded"] != false ||
		retryBackoffPlan["retry_after_header_included"] != false ||
		retryBackoffPlan["provider_rate_limit_value_included"] != false ||
		retryBackoffPlan["provider_error_code_included"] != false ||
		retryBackoffPlan["external_call_made"] != false ||
		retryBackoffPlan["provider_api_call_made"] != false ||
		retryBackoffPlan["provider_api_mutation"] != "disabled" ||
		retryBackoffPlan["request_body_included"] != false ||
		retryBackoffPlan["response_body_included"] != false ||
		retryBackoffPlan["headers_included"] != false ||
		retryBackoffPlan["authorization_header_included"] != false ||
		retryBackoffPlan["provider_url_included"] != false ||
		retryBackoffPlan["idempotency_key_included"] != false ||
		retryBackoffPlan["contains_token"] != false ||
		retryBackoffPlan["contains_provider_url"] != false ||
		retryBackoffPlan["contains_repository_ref"] != false ||
		retryBackoffPlan["contains_branch_name"] != false ||
		retryBackoffPlan["contains_file_content"] != false ||
		retryBackoffPlan["retry_backoff_boundary_redacted"] != true {
		t.Fatalf("retry backoff plan = %#v", retryBackoffPlan)
	}
	retryClasses := stringSliceFromAny(retryBackoffPlan["retryable_status_classes"])
	transportRetryClasses := stringSliceFromAny(retryBackoffPlan["transport_retryable_status_classes"])
	if len(retryClasses) != 1 || retryClasses[0] != "5xx" || len(transportRetryClasses) != 1 || transportRetryClasses[0] != "5xx" {
		t.Fatalf("retry classes = %#v / %#v", retryClasses, transportRetryClasses)
	}
	retrySequence := stringSliceFromAny(retryBackoffPlan["retry_backoff_sequence"])
	if len(retrySequence) != 4 ||
		retrySequence[0] != "classify_retryable_response" ||
		retrySequence[1] != "verify_idempotency_ledger" ||
		retrySequence[2] != "record_retry_decision" ||
		retrySequence[3] != "schedule_backoff_retry" {
		t.Fatalf("retry sequence = %#v", retrySequence)
	}
	retrySuppressedFields := stringSliceFromAny(retryBackoffPlan["retry_backoff_suppressed_fields"])
	if len(retrySuppressedFields) != 12 ||
		retrySuppressedFields[0] != "retry_after_value" ||
		retrySuppressedFields[6] != "authorization_header" ||
		retrySuppressedFields[11] != "file_content" {
		t.Fatalf("retry suppressed fields = %#v", retrySuppressedFields)
	}
	retryBlockedReasons := stringSliceFromAny(retryBackoffPlan["blocked_reasons"])
	if len(retryBlockedReasons) != 4 ||
		retryBlockedReasons[0] != "provider_retry_backoff_not_armed" ||
		retryBlockedReasons[1] != "provider_response_diagnostics_not_recorded" ||
		retryBlockedReasons[2] != "provider_idempotency_ledger_not_claimed" ||
		retryBlockedReasons[3] != "provider_review_mutation_not_armed" {
		t.Fatalf("retry blocked reasons = %#v", retryBlockedReasons)
	}
	sendBlockedReasons := stringSliceFromAny(providerSendPlan["blocked_reasons"])
	if len(sendBlockedReasons) != 5 ||
		sendBlockedReasons[0] != "provider_request_send_not_armed" ||
		sendBlockedReasons[1] != "provider_request_not_materialized" ||
		sendBlockedReasons[2] != "provider_credential_runtime_binding_not_armed" ||
		sendBlockedReasons[3] != "provider_review_adapter_runtime_not_bound" ||
		sendBlockedReasons[4] != "provider_review_mutation_not_armed" {
		t.Fatalf("provider send blocked reasons = %#v", sendBlockedReasons)
	}
	blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
	if len(blockedReasons) != 9 ||
		blockedReasons[0] != "provider_review_attempt_claim_not_recorded" ||
		blockedReasons[1] != "provider_review_execution_lock_not_acquired" ||
		blockedReasons[3] != "provider_review_adapter_runtime_not_bound" ||
		blockedReasons[6] != "provider_review_transaction_not_recorded" ||
		blockedReasons[8] != "provider_review_mutation_not_armed" {
		t.Fatalf("invocation blocked reasons = %#v", blockedReasons)
	}
	encoded, _ := json.Marshal(plan)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("invocation plan leaked %q: %s", leak, encoded)
		}
	}
	encodedLockPlan, _ := json.Marshal(executionLockPlan)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization"} {
		if strings.Contains(string(encodedLockPlan), leak) {
			t.Fatalf("execution lock plan leaked %q: %s", leak, encodedLockPlan)
		}
	}
	if got := providerReviewAttemptAdapterInvocationPlan(nil, nil, nil, nil, nil, nil, nil, nil); len(got) != 0 {
		t.Fatalf("empty operation invocation plan = %#v", got)
	}
	if got := providerReviewAttemptAdapterRetryBackoffPlan(operation, map[string]any{"mode": "raw_transport_plan"}); len(got) != 0 {
		t.Fatalf("mismatched transport retry backoff plan = %#v", got)
	}
	if got := providerReviewAttemptAdapterExecutionLockPlan(
		map[string]any{"name": "raw_operation", "endpoint_key": "github.create_branch_ref"},
		claimPlan,
		transactionPlan,
	); len(got) != 0 {
		t.Fatalf("invalid operation execution lock plan should be empty: %#v", got)
	}
	notReadyClaimLockPlan := providerReviewAttemptAdapterExecutionLockPlan(
		operation,
		map[string]any{"claim_metadata_ready": false},
		transactionPlan,
	)
	if notReadyClaimLockPlan["execution_lock_metadata_ready"] != false ||
		notReadyClaimLockPlan["execution_lock_metadata_ready_reason"] != "provider_review_execution_lock_claim_metadata_not_ready" ||
		notReadyClaimLockPlan["claim_metadata_ready"] != false ||
		notReadyClaimLockPlan["transaction_metadata_ready"] != true {
		t.Fatalf("not ready claim execution lock plan = %#v", notReadyClaimLockPlan)
	}
	notReadyTransactionLockPlan := providerReviewAttemptAdapterExecutionLockPlan(
		operation,
		claimPlan,
		map[string]any{"transaction_metadata_ready": false},
	)
	if notReadyTransactionLockPlan["execution_lock_metadata_ready"] != false ||
		notReadyTransactionLockPlan["execution_lock_metadata_ready_reason"] != "provider_review_execution_lock_transaction_metadata_not_ready" ||
		notReadyTransactionLockPlan["claim_metadata_ready"] != true ||
		notReadyTransactionLockPlan["transaction_metadata_ready"] != false {
		t.Fatalf("not ready transaction execution lock plan = %#v", notReadyTransactionLockPlan)
	}
	got := providerReviewAttemptAdapterInvocationPlan(
		map[string]any{"name": "raw_operation", "endpoint_key": "github.create_branch_ref"},
		claimPlan,
		requestPlan,
		credentialPlan,
		runtimePlan,
		transportPlan,
		responsePlan,
		transactionPlan,
	)
	if len(got) != 0 {
		t.Fatalf("invalid operation invocation plan should be empty: %#v", got)
	}
	got = providerReviewAttemptAdapterInvocationPlan(
		map[string]any{"name": "create_branch_ref", "endpoint_key": "github.secret"},
		claimPlan,
		requestPlan,
		credentialPlan,
		runtimePlan,
		transportPlan,
		responsePlan,
		transactionPlan,
	)
	if len(got) != 0 {
		t.Fatalf("invalid endpoint invocation plan should be empty: %#v", got)
	}
	notReadyTransportPlan := providerReviewAttemptAdapterInvocationPlan(
		operation,
		claimPlan,
		requestPlan,
		credentialPlan,
		runtimePlan,
		map[string]any{"mode": "raw_transport_plan", "transport_ready": false},
		responsePlan,
		transactionPlan,
	)
	if notReadyTransportPlan["transport_metadata_ready"] != false ||
		notReadyTransportPlan["transport_metadata_ready_reason"] != "provider_review_transport_metadata_not_ready" {
		t.Fatalf("not ready transport invocation plan = %#v", notReadyTransportPlan)
	}
	notReadyClaimPlan := providerReviewAttemptAdapterInvocationPlan(
		operation,
		map[string]any{"claim_metadata_ready": false},
		requestPlan,
		credentialPlan,
		runtimePlan,
		transportPlan,
		responsePlan,
		transactionPlan,
	)
	if notReadyClaimPlan["claim_metadata_ready"] != false ||
		notReadyClaimPlan["claim_metadata_ready_reason"] != "provider_review_claim_metadata_not_ready" ||
		notReadyClaimPlan["execution_lock_metadata_ready"] != false ||
		notReadyClaimPlan["execution_lock_ready_reason"] != "provider_review_execution_lock_claim_metadata_not_ready" {
		t.Fatalf("not ready claim invocation plan = %#v", notReadyClaimPlan)
	}
	notReadyTransactionPlan := providerReviewAttemptAdapterInvocationPlan(
		operation,
		claimPlan,
		requestPlan,
		credentialPlan,
		runtimePlan,
		transportPlan,
		responsePlan,
		map[string]any{"transaction_metadata_ready": false},
	)
	if notReadyTransactionPlan["transaction_metadata_ready"] != false ||
		notReadyTransactionPlan["transaction_metadata_ready_reason"] != "provider_review_transaction_metadata_not_ready" ||
		notReadyTransactionPlan["execution_lock_metadata_ready"] != false ||
		notReadyTransactionPlan["execution_lock_ready_reason"] != "provider_review_execution_lock_transaction_metadata_not_ready" {
		t.Fatalf("not ready transaction invocation plan = %#v", notReadyTransactionPlan)
	}
}

func TestProviderReviewAttemptAdapterRuntimePlan(t *testing.T) {
	for _, item := range []struct {
		name         string
		provider     string
		operation    string
		endpoint     string
		adapterKind  string
		builderName  string
		clientKind   string
		methodName   string
		authScheme   string
		capability   string
		handlerName  string
		templateKey  string
		payloadShape string
		wantNonEmpty bool
	}{
		{
			name:         "github branch ref selects github runtime",
			provider:     "github",
			operation:    "create_branch_ref",
			endpoint:     "github.create_branch_ref",
			adapterKind:  "github_provider_review_adapter",
			builderName:  "build_redacted_branch_ref_request",
			clientKind:   "github_provider_review_api_client",
			methodName:   "execute_branch_ref_creation",
			authScheme:   "bearer_token",
			capability:   "repository_ref_write",
			handlerName:  "handle_branch_ref_response",
			templateKey:  "github_git_refs_path_template",
			payloadShape: "ref_from_target_branch",
			wantNonEmpty: true,
		},
		{
			name:         "gitea review request selects gitea runtime",
			provider:     "gitea",
			operation:    "open_review_request",
			endpoint:     "gitea.open_review",
			adapterKind:  "gitea_provider_review_adapter",
			builderName:  "build_redacted_review_request",
			clientKind:   "gitea_provider_review_api_client",
			methodName:   "execute_review_request_open",
			authScheme:   "token",
			capability:   "review_request_write",
			handlerName:  "handle_review_request_response",
			templateKey:  "gitea_merge_request_path_template",
			payloadShape: "review_request",
			wantNonEmpty: true,
		},
		{
			name:         "github commit starter files selects github runtime",
			provider:     "github",
			operation:    "commit_starter_files",
			endpoint:     "github.commit_files",
			adapterKind:  "github_provider_review_adapter",
			builderName:  "build_redacted_commit_files_request",
			clientKind:   "github_provider_review_api_client",
			methodName:   "execute_starter_file_commit",
			authScheme:   "bearer_token",
			capability:   "repository_contents_write",
			handlerName:  "handle_commit_files_response",
			templateKey:  "github_repository_contents_path_template",
			payloadShape: "content_redacted_file_batch",
			wantNonEmpty: true,
		},
		{
			name:      "unknown provider returns empty plan",
			provider:  "raw_provider",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "unknown operation returns empty plan",
			provider:  "github",
			operation: "raw_operation",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "empty operation name returns empty plan",
			provider:  "github",
			operation: "",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "empty endpoint key returns empty plan",
			provider:  "github",
			operation: "create_branch_ref",
			endpoint:  "",
		},
		{
			name:      "provider endpoint mismatch returns empty plan",
			provider:  "github",
			operation: "create_branch_ref",
			endpoint:  "gitea.create_branch_ref",
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			plan := providerReviewAttemptAdapterRuntimePlan(item.provider, item.operation, item.endpoint)
			if !item.wantNonEmpty {
				if len(plan) != 0 {
					t.Fatalf("runtime plan should be empty: %#v", plan)
				}
				return
			}
			if plan["mode"] != "redacted_attempt_adapter_runtime_plan" ||
				plan["runtime_state"] != "blocked" ||
				plan["runtime_ready"] != false ||
				plan["runtime_ready_reason"] != "provider_review_adapter_runtime_not_armed" ||
				plan["provider_type"] != item.provider ||
				plan["adapter_kind"] != item.adapterKind ||
				plan["operation_name"] != item.operation ||
				plan["endpoint_key"] != item.endpoint ||
				plan["adapter_interface_registered"] != true ||
				plan["adapter_dispatch_registered"] != true ||
				plan["runtime_adapter_selected"] != true ||
				plan["operation_supported"] != true ||
				plan["live_adapter_implemented"] != false ||
				plan["provider_client_constructed"] != false ||
				len(mapFromAny(plan["provider_client_plan"])) == 0 ||
				plan["execute_method_bound"] != false ||
				len(mapFromAny(plan["execute_method_plan"])) == 0 ||
				plan["request_builder_bound"] != false ||
				len(mapFromAny(plan["request_builder_plan"])) == 0 ||
				plan["response_handler_bound"] != false ||
				len(mapFromAny(plan["response_handler_plan"])) == 0 ||
				plan["transaction_handler_bound"] != false ||
				plan["requires_provider_client"] != true ||
				plan["requires_request_builder"] != true ||
				plan["requires_response_handler"] != true ||
				plan["requires_transaction_handler"] != true ||
				plan["requires_mutation_arming"] != true ||
				plan["runtime_boundary_redacted"] != true ||
				plan["external_call_made"] != false ||
				plan["provider_api_call_made"] != false ||
				plan["provider_api_mutation"] != "disabled" ||
				plan["request_body_included"] != false ||
				plan["response_body_included"] != false ||
				plan["headers_included"] != false ||
				plan["authorization_header_included"] != false ||
				plan["provider_url_included"] != false ||
				plan["idempotency_key_included"] != false ||
				plan["contains_token"] != false ||
				plan["contains_provider_url"] != false ||
				plan["contains_repository_ref"] != false ||
				plan["contains_branch_name"] != false ||
				plan["contains_file_content"] != false {
				t.Fatalf("runtime plan = %#v", plan)
			}
			methods := stringSliceFromAny(plan["required_runtime_methods"])
			if len(methods) != 4 ||
				methods[0] != "build_request" ||
				methods[1] != "send_provider_request" ||
				methods[2] != "handle_response" ||
				methods[3] != "record_attempt_transaction" {
				t.Fatalf("runtime methods = %#v", methods)
			}
			clientPlan := mapFromAny(plan["provider_client_plan"])
			if clientPlan["mode"] != "redacted_attempt_adapter_provider_client_plan" ||
				clientPlan["provider_client_state"] != "blocked" ||
				clientPlan["provider_client_ready"] != false ||
				clientPlan["provider_client_ready_reason"] != "provider_review_provider_client_not_armed" ||
				clientPlan["provider_type"] != item.provider ||
				clientPlan["operation_name"] != item.operation ||
				clientPlan["endpoint_key"] != item.endpoint ||
				clientPlan["client_kind"] != item.clientKind ||
				clientPlan["auth_scheme"] != item.authScheme ||
				clientPlan["base_url_source"] != "provider_account_api_base_url" ||
				clientPlan["credential_source_kind"] != "provider_account_token_env" ||
				clientPlan["timeout_seconds"] != 15 ||
				clientPlan["retry_policy"] != "retry_5xx_with_backoff" ||
				clientPlan["client_factory_interface_registered"] != true ||
				clientPlan["client_factory_registered"] != true ||
				clientPlan["client_implemented"] != false ||
				clientPlan["provider_client_constructed"] != false ||
				clientPlan["provider_account_resolved"] != false ||
				clientPlan["base_url_validated"] != false ||
				clientPlan["base_url_materialized"] != false ||
				clientPlan["token_env_allowed"] != false ||
				clientPlan["runtime_token_loaded"] != false ||
				clientPlan["authorization_header_materialized"] != false ||
				clientPlan["http_client_configured"] != false ||
				clientPlan["provider_client_boundary_redacted"] != true ||
				clientPlan["external_call_made"] != false ||
				clientPlan["provider_api_call_made"] != false ||
				clientPlan["provider_api_mutation"] != "disabled" ||
				clientPlan["base_url_included"] != false ||
				clientPlan["token_env_name_included"] != false ||
				clientPlan["token_value_included"] != false ||
				clientPlan["authorization_header_included"] != false ||
				clientPlan["provider_url_included"] != false ||
				clientPlan["request_body_included"] != false ||
				clientPlan["response_body_included"] != false ||
				clientPlan["headers_included"] != false ||
				clientPlan["contains_token"] != false ||
				clientPlan["contains_provider_url"] != false ||
				clientPlan["contains_repository_ref"] != false ||
				clientPlan["contains_branch_name"] != false ||
				clientPlan["contains_file_content"] != false {
				t.Fatalf("runtime provider client plan = %#v", clientPlan)
			}
			clientCapabilities := stringSliceFromAny(clientPlan["required_capabilities"])
			if len(clientCapabilities) != 1 || clientCapabilities[0] != item.capability {
				t.Fatalf("runtime provider client capabilities = %#v", clientCapabilities)
			}
			clientBlockedReasons := stringSliceFromAny(clientPlan["blocked_reasons"])
			if len(clientBlockedReasons) != 3 ||
				clientBlockedReasons[0] != "provider_review_provider_client_not_armed" ||
				clientBlockedReasons[1] != "provider_review_live_adapter_not_implemented" ||
				clientBlockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("runtime provider client blocked reasons = %#v", clientBlockedReasons)
			}
			executePlan := mapFromAny(plan["execute_method_plan"])
			if executePlan["mode"] != "redacted_attempt_adapter_execute_method_plan" ||
				executePlan["execute_method_state"] != "blocked" ||
				executePlan["execute_method_ready"] != false ||
				executePlan["execute_method_ready_reason"] != "provider_review_execute_method_not_armed" ||
				executePlan["provider_type"] != item.provider ||
				executePlan["operation_name"] != item.operation ||
				executePlan["endpoint_key"] != item.endpoint ||
				executePlan["method_name"] != item.methodName ||
				executePlan["http_method"] != providerReviewMethodForOperation(item.operation) ||
				executePlan["execute_method_interface_registered"] != true ||
				executePlan["execute_method_registered"] != true ||
				executePlan["execute_method_implemented"] != false ||
				executePlan["execute_method_bound"] != false ||
				executePlan["provider_client_constructed"] != false ||
				executePlan["request_materialized"] != false ||
				executePlan["provider_request_sent"] != false ||
				executePlan["response_handled"] != false ||
				executePlan["transaction_recorded"] != false ||
				executePlan["dependency_update_recorded"] != false ||
				executePlan["execute_method_boundary_redacted"] != true ||
				executePlan["external_call_made"] != false ||
				executePlan["provider_api_call_made"] != false ||
				executePlan["provider_api_mutation"] != "disabled" ||
				executePlan["request_body_included"] != false ||
				executePlan["response_body_included"] != false ||
				executePlan["headers_included"] != false ||
				executePlan["authorization_header_included"] != false ||
				executePlan["provider_url_included"] != false ||
				executePlan["idempotency_key_included"] != false ||
				executePlan["contains_token"] != false ||
				executePlan["contains_provider_url"] != false ||
				executePlan["contains_repository_ref"] != false ||
				executePlan["contains_branch_name"] != false ||
				executePlan["contains_file_content"] != false {
				t.Fatalf("runtime execute method plan = %#v", executePlan)
			}
			executeSequence := stringSliceFromAny(executePlan["execution_sequence"])
			if len(executeSequence) != 8 ||
				executeSequence[0] != "verify_attempt_claim" ||
				executeSequence[5] != "stage_provider_request_send" ||
				executeSequence[7] != "record_attempt_transaction" {
				t.Fatalf("runtime execute method sequence = %#v", executeSequence)
			}
			executeBlockedReasons := stringSliceFromAny(executePlan["blocked_reasons"])
			if len(executeBlockedReasons) != 3 ||
				executeBlockedReasons[0] != "provider_review_execute_method_not_armed" ||
				executeBlockedReasons[1] != "provider_review_live_adapter_not_implemented" ||
				executeBlockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("runtime execute method blocked reasons = %#v", executeBlockedReasons)
			}
			builderPlan := mapFromAny(plan["request_builder_plan"])
			if builderPlan["mode"] != "redacted_attempt_adapter_request_builder_plan" ||
				builderPlan["request_builder_state"] != "blocked" ||
				builderPlan["request_builder_ready"] != false ||
				builderPlan["request_builder_ready_reason"] != "provider_review_request_builder_not_armed" ||
				builderPlan["provider_type"] != item.provider ||
				builderPlan["operation_name"] != item.operation ||
				builderPlan["endpoint_key"] != item.endpoint ||
				builderPlan["builder_name"] != item.builderName ||
				builderPlan["endpoint_path_template_key"] != item.templateKey ||
				builderPlan["payload_shape"] != item.payloadShape ||
				builderPlan["builder_interface_registered"] != true ||
				builderPlan["builder_registered"] != true ||
				builderPlan["builder_implemented"] != false ||
				builderPlan["request_url_materialized"] != false ||
				builderPlan["request_body_materialized"] != false ||
				builderPlan["headers_materialized"] != false ||
				builderPlan["authorization_header_materialized"] != false ||
				builderPlan["provider_api_call_made"] != false ||
				builderPlan["provider_api_mutation"] != "disabled" ||
				builderPlan["contains_token"] != false ||
				builderPlan["contains_provider_url"] != false ||
				builderPlan["contains_repository_ref"] != false ||
				builderPlan["contains_branch_name"] != false ||
				builderPlan["contains_file_content"] != false ||
				builderPlan["request_builder_boundary_redacted"] != true {
				t.Fatalf("runtime request builder plan = %#v", builderPlan)
			}
			responseHandlerPlan := mapFromAny(plan["response_handler_plan"])
			if responseHandlerPlan["mode"] != "redacted_attempt_adapter_response_handler_plan" ||
				responseHandlerPlan["response_handler_state"] != "blocked" ||
				responseHandlerPlan["response_handler_ready"] != false ||
				responseHandlerPlan["response_handler_ready_reason"] != "provider_review_response_handler_not_armed" ||
				responseHandlerPlan["provider_type"] != item.provider ||
				responseHandlerPlan["operation_name"] != item.operation ||
				responseHandlerPlan["endpoint_key"] != item.endpoint ||
				responseHandlerPlan["handler_name"] != item.handlerName ||
				responseHandlerPlan["response_status"] != "pending" ||
				responseHandlerPlan["handler_interface_registered"] != true ||
				responseHandlerPlan["handler_registered"] != true ||
				responseHandlerPlan["handler_implemented"] != false ||
				responseHandlerPlan["provider_response_classified"] != false ||
				responseHandlerPlan["attempt_status_selected"] != false ||
				responseHandlerPlan["dependency_update_selected"] != false ||
				responseHandlerPlan["provider_request_id_recorded"] != false ||
				responseHandlerPlan["response_body_recorded"] != false ||
				responseHandlerPlan["response_headers_recorded"] != false ||
				responseHandlerPlan["provider_api_call_made"] != false ||
				responseHandlerPlan["provider_api_mutation"] != "disabled" ||
				responseHandlerPlan["response_body_included"] != false ||
				responseHandlerPlan["headers_included"] != false ||
				responseHandlerPlan["provider_request_id_included"] != false ||
				responseHandlerPlan["contains_token"] != false ||
				responseHandlerPlan["contains_provider_url"] != false ||
				responseHandlerPlan["contains_repository_ref"] != false ||
				responseHandlerPlan["contains_branch_name"] != false ||
				responseHandlerPlan["contains_file_content"] != false ||
				responseHandlerPlan["response_handler_boundary_redacted"] != true {
				t.Fatalf("runtime response handler plan = %#v", responseHandlerPlan)
			}
			blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
			if len(blockedReasons) != 3 ||
				blockedReasons[0] != "provider_review_live_adapter_not_implemented" ||
				blockedReasons[1] != "provider_review_adapter_runtime_not_armed" ||
				blockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("runtime blocked reasons = %#v", blockedReasons)
			}
			encoded, _ := json.Marshal(plan)
			for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("runtime plan leaked %q: %s", leak, encoded)
				}
			}
		})
	}
}

func TestProviderReviewAttemptAdapterProviderClientPlan(t *testing.T) {
	for _, item := range []struct {
		name         string
		provider     string
		operation    string
		endpoint     string
		clientKind   string
		authScheme   string
		capability   string
		wantNonEmpty bool
	}{
		{
			name:         "github branch ref client",
			provider:     "github",
			operation:    "create_branch_ref",
			endpoint:     "github.create_branch_ref",
			clientKind:   "github_provider_review_api_client",
			authScheme:   "bearer_token",
			capability:   "repository_ref_write",
			wantNonEmpty: true,
		},
		{
			name:         "github commit starter files client",
			provider:     "github",
			operation:    "commit_starter_files",
			endpoint:     "github.commit_files",
			clientKind:   "github_provider_review_api_client",
			authScheme:   "bearer_token",
			capability:   "repository_contents_write",
			wantNonEmpty: true,
		},
		{
			name:         "gitea review request client",
			provider:     "gitea",
			operation:    "open_review_request",
			endpoint:     "gitea.open_review",
			clientKind:   "gitea_provider_review_api_client",
			authScheme:   "token",
			capability:   "review_request_write",
			wantNonEmpty: true,
		},
		{
			name:      "unknown provider returns empty provider client plan",
			provider:  "raw_provider",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "empty operation returns empty provider client plan",
			provider:  "github",
			operation: "",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "mismatched endpoint returns empty provider client plan",
			provider:  "github",
			operation: "create_branch_ref",
			endpoint:  "gitea.create_branch_ref",
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			plan := providerReviewAttemptAdapterProviderClientPlan(item.provider, item.operation, item.endpoint)
			if !item.wantNonEmpty {
				if len(plan) != 0 {
					t.Fatalf("provider client plan should be empty: %#v", plan)
				}
				return
			}
			if plan["mode"] != "redacted_attempt_adapter_provider_client_plan" ||
				plan["provider_client_state"] != "blocked" ||
				plan["provider_client_ready"] != false ||
				plan["provider_client_ready_reason"] != "provider_review_provider_client_not_armed" ||
				plan["provider_type"] != item.provider ||
				plan["operation_name"] != item.operation ||
				plan["endpoint_key"] != item.endpoint ||
				plan["client_kind"] != item.clientKind ||
				plan["auth_scheme"] != item.authScheme ||
				plan["base_url_source"] != "provider_account_api_base_url" ||
				plan["credential_source_kind"] != "provider_account_token_env" ||
				plan["timeout_seconds"] != 15 ||
				plan["retry_policy"] != "retry_5xx_with_backoff" ||
				plan["client_factory_interface_registered"] != true ||
				plan["client_factory_registered"] != true ||
				plan["client_implemented"] != false ||
				plan["provider_client_constructed"] != false ||
				plan["provider_account_resolved"] != false ||
				plan["base_url_validated"] != false ||
				plan["base_url_materialized"] != false ||
				plan["token_env_allowed"] != false ||
				plan["runtime_token_loaded"] != false ||
				plan["authorization_header_materialized"] != false ||
				plan["http_client_configured"] != false ||
				plan["provider_client_boundary_redacted"] != true ||
				plan["external_call_made"] != false ||
				plan["provider_api_call_made"] != false ||
				plan["provider_api_mutation"] != "disabled" ||
				plan["base_url_included"] != false ||
				plan["token_env_name_included"] != false ||
				plan["token_value_included"] != false ||
				plan["authorization_header_included"] != false ||
				plan["provider_url_included"] != false ||
				plan["request_body_included"] != false ||
				plan["response_body_included"] != false ||
				plan["headers_included"] != false ||
				plan["contains_token"] != false ||
				plan["contains_provider_url"] != false ||
				plan["contains_repository_ref"] != false ||
				plan["contains_branch_name"] != false ||
				plan["contains_file_content"] != false {
				t.Fatalf("provider client plan = %#v", plan)
			}
			capabilities := stringSliceFromAny(plan["required_capabilities"])
			if len(capabilities) != 1 || capabilities[0] != item.capability {
				t.Fatalf("provider client capabilities = %#v", capabilities)
			}
			blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
			if len(blockedReasons) != 3 ||
				blockedReasons[0] != "provider_review_provider_client_not_armed" ||
				blockedReasons[1] != "provider_review_live_adapter_not_implemented" ||
				blockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("provider client blocked reasons = %#v", blockedReasons)
			}
			encoded, _ := json.Marshal(plan)
			for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "ASSOPS_TEMPLATE_PROVIDER_TOKEN"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("provider client plan leaked %q: %s", leak, encoded)
				}
			}
		})
	}
}

func TestProviderReviewAttemptAdapterExecuteMethodPlan(t *testing.T) {
	for _, item := range []struct {
		name         string
		provider     string
		operation    string
		endpoint     string
		methodName   string
		httpMethod   string
		wantNonEmpty bool
	}{
		{
			name:         "github branch ref execute method",
			provider:     "github",
			operation:    "create_branch_ref",
			endpoint:     "github.create_branch_ref",
			methodName:   "execute_branch_ref_creation",
			httpMethod:   "POST",
			wantNonEmpty: true,
		},
		{
			name:         "github commit starter files execute method",
			provider:     "github",
			operation:    "commit_starter_files",
			endpoint:     "github.commit_files",
			methodName:   "execute_starter_file_commit",
			httpMethod:   "PUT",
			wantNonEmpty: true,
		},
		{
			name:         "gitea review request execute method",
			provider:     "gitea",
			operation:    "open_review_request",
			endpoint:     "gitea.open_review",
			methodName:   "execute_review_request_open",
			httpMethod:   "POST",
			wantNonEmpty: true,
		},
		{
			name:      "unknown provider returns empty execute method plan",
			provider:  "raw_provider",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "empty operation returns empty execute method plan",
			provider:  "github",
			operation: "",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "mismatched endpoint returns empty execute method plan",
			provider:  "github",
			operation: "create_branch_ref",
			endpoint:  "gitea.create_branch_ref",
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			plan := providerReviewAttemptAdapterExecuteMethodPlan(item.provider, item.operation, item.endpoint)
			if !item.wantNonEmpty {
				if len(plan) != 0 {
					t.Fatalf("execute method plan should be empty: %#v", plan)
				}
				return
			}
			if plan["mode"] != "redacted_attempt_adapter_execute_method_plan" ||
				plan["execute_method_state"] != "blocked" ||
				plan["execute_method_ready"] != false ||
				plan["execute_method_ready_reason"] != "provider_review_execute_method_not_armed" ||
				plan["provider_type"] != item.provider ||
				plan["operation_name"] != item.operation ||
				plan["endpoint_key"] != item.endpoint ||
				plan["method_name"] != item.methodName ||
				plan["http_method"] != item.httpMethod ||
				plan["execute_method_interface_registered"] != true ||
				plan["execute_method_registered"] != true ||
				plan["execute_method_implemented"] != false ||
				plan["execute_method_bound"] != false ||
				plan["requires_attempt_claim"] != true ||
				plan["requires_idempotency_claim"] != true ||
				plan["requires_credential_binding"] != true ||
				plan["requires_provider_client"] != true ||
				plan["requires_request_builder"] != true ||
				plan["requires_transport"] != true ||
				plan["requires_response_handler"] != true ||
				plan["requires_transaction_handler"] != true ||
				plan["requires_mutation_arming"] != true ||
				plan["provider_client_constructed"] != false ||
				plan["request_materialized"] != false ||
				plan["provider_request_sent"] != false ||
				plan["response_handled"] != false ||
				plan["transaction_recorded"] != false ||
				plan["dependency_update_recorded"] != false ||
				plan["execute_method_boundary_redacted"] != true ||
				plan["external_call_made"] != false ||
				plan["provider_api_call_made"] != false ||
				plan["provider_api_mutation"] != "disabled" ||
				plan["request_body_included"] != false ||
				plan["response_body_included"] != false ||
				plan["headers_included"] != false ||
				plan["authorization_header_included"] != false ||
				plan["provider_url_included"] != false ||
				plan["idempotency_key_included"] != false ||
				plan["contains_token"] != false ||
				plan["contains_provider_url"] != false ||
				plan["contains_repository_ref"] != false ||
				plan["contains_branch_name"] != false ||
				plan["contains_file_content"] != false {
				t.Fatalf("execute method plan = %#v", plan)
			}
			sequence := stringSliceFromAny(plan["execution_sequence"])
			if len(sequence) != 8 ||
				sequence[0] != "verify_attempt_claim" ||
				sequence[1] != "verify_idempotency_claim" ||
				sequence[2] != "bind_credential" ||
				sequence[3] != "construct_provider_client" ||
				sequence[4] != "build_request" ||
				sequence[5] != "stage_provider_request_send" ||
				sequence[6] != "handle_response" ||
				sequence[7] != "record_attempt_transaction" {
				t.Fatalf("execute method sequence = %#v", sequence)
			}
			blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
			if len(blockedReasons) != 3 ||
				blockedReasons[0] != "provider_review_execute_method_not_armed" ||
				blockedReasons[1] != "provider_review_live_adapter_not_implemented" ||
				blockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("execute method blocked reasons = %#v", blockedReasons)
			}
			encoded, _ := json.Marshal(plan)
			for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "ASSOPS_TEMPLATE_PROVIDER_TOKEN"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("execute method plan leaked %q: %s", leak, encoded)
				}
			}
		})
	}
}

func TestDisabledProviderReviewAttemptExecuteMethodRejectsMismatchedEndpoint(t *testing.T) {
	method := disabledProviderReviewAttemptExecuteMethod{methodName: "execute_branch_ref_creation"}
	plan := method.BuildPlan(providerReviewAttemptExecuteMethodInput{
		ProviderType:  "github",
		OperationName: "create_branch_ref",
		EndpointKey:   "gitea.create_branch_ref",
	})
	if len(plan) != 0 {
		t.Fatalf("mismatched endpoint direct execute method plan should be empty: %#v", plan)
	}
}

func TestDisabledProviderReviewAttemptProviderClientRejectsMismatchedEndpoint(t *testing.T) {
	factory := disabledProviderReviewAttemptProviderClientFactory{clientKind: "github_provider_review_api_client"}
	plan := factory.BuildPlan(providerReviewAttemptProviderClientInput{
		ProviderType:  "github",
		OperationName: "create_branch_ref",
		EndpointKey:   "gitea.create_branch_ref",
	})
	if len(plan) != 0 {
		t.Fatalf("mismatched endpoint direct provider client plan should be empty: %#v", plan)
	}
}

func TestProviderReviewAttemptAdapterRequestBuilderPlan(t *testing.T) {
	for _, item := range []struct {
		name             string
		provider         string
		operation        string
		endpoint         string
		builderName      string
		method           string
		templateKey      string
		payloadShape     string
		requiresManifest bool
		wantNonEmpty     bool
	}{
		{
			name:         "github branch ref builder",
			provider:     "github",
			operation:    "create_branch_ref",
			endpoint:     "github.create_branch_ref",
			builderName:  "build_redacted_branch_ref_request",
			method:       "POST",
			templateKey:  "github_git_refs_path_template",
			payloadShape: "ref_from_target_branch",
			wantNonEmpty: true,
		},
		{
			name:             "github commit starter files builder",
			provider:         "github",
			operation:        "commit_starter_files",
			endpoint:         "github.commit_files",
			builderName:      "build_redacted_commit_files_request",
			method:           "PUT",
			templateKey:      "github_repository_contents_path_template",
			payloadShape:     "content_redacted_file_batch",
			requiresManifest: true,
			wantNonEmpty:     true,
		},
		{
			name:         "gitea review request builder",
			provider:     "gitea",
			operation:    "open_review_request",
			endpoint:     "gitea.open_review",
			builderName:  "build_redacted_review_request",
			method:       "POST",
			templateKey:  "gitea_merge_request_path_template",
			payloadShape: "review_request",
			wantNonEmpty: true,
		},
		{
			name:         "gitea branch ref builder",
			provider:     "gitea",
			operation:    "create_branch_ref",
			endpoint:     "gitea.create_branch_ref",
			builderName:  "build_redacted_branch_ref_request",
			method:       "POST",
			templateKey:  "gitea_git_refs_path_template",
			payloadShape: "ref_from_target_branch",
			wantNonEmpty: true,
		},
		{
			name:         "github review request builder",
			provider:     "github",
			operation:    "open_review_request",
			endpoint:     "github.open_review",
			builderName:  "build_redacted_review_request",
			method:       "POST",
			templateKey:  "github_pull_request_path_template",
			payloadShape: "review_request",
			wantNonEmpty: true,
		},
		{
			name:      "unknown provider returns empty builder plan",
			provider:  "raw_provider",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "empty operation returns empty builder plan",
			provider:  "github",
			operation: "",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "mismatched endpoint returns empty builder plan",
			provider:  "github",
			operation: "create_branch_ref",
			endpoint:  "gitea.create_branch_ref",
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			plan := providerReviewAttemptAdapterRequestBuilderPlan(item.provider, item.operation, item.endpoint)
			if !item.wantNonEmpty {
				if len(plan) != 0 {
					t.Fatalf("request builder plan should be empty: %#v", plan)
				}
				return
			}
			if plan["mode"] != "redacted_attempt_adapter_request_builder_plan" ||
				plan["request_builder_state"] != "blocked" ||
				plan["request_builder_ready"] != false ||
				plan["request_builder_ready_reason"] != "provider_review_request_builder_not_armed" ||
				plan["provider_type"] != item.provider ||
				plan["operation_name"] != item.operation ||
				plan["endpoint_key"] != item.endpoint ||
				plan["builder_name"] != item.builderName ||
				plan["method"] != item.method ||
				plan["endpoint_path_template_key"] != item.templateKey ||
				plan["payload_shape"] != item.payloadShape ||
				plan["requires_provider_repository_context"] != true ||
				plan["requires_redacted_payload_summary"] != true ||
				plan["requires_starter_file_manifest"] != item.requiresManifest ||
				plan["builder_interface_registered"] != true ||
				plan["builder_registered"] != true ||
				plan["builder_implemented"] != false ||
				plan["provider_repository_context_resolved"] != false ||
				plan["request_path_materialized"] != false ||
				plan["request_url_materialized"] != false ||
				plan["request_body_materialized"] != false ||
				plan["payload_materialized"] != false ||
				plan["headers_materialized"] != false ||
				plan["starter_file_manifest_materialized"] != false ||
				plan["authorization_header_materialized"] != false ||
				plan["request_builder_boundary_redacted"] != true ||
				plan["external_call_made"] != false ||
				plan["provider_api_call_made"] != false ||
				plan["provider_api_mutation"] != "disabled" ||
				plan["request_body_included"] != false ||
				plan["headers_included"] != false ||
				plan["provider_url_included"] != false ||
				plan["repository_ref_included"] != false ||
				plan["branch_name_included"] != false ||
				plan["file_content_included"] != false ||
				plan["idempotency_key_included"] != false ||
				plan["contains_token"] != false ||
				plan["contains_provider_url"] != false ||
				plan["contains_repository_ref"] != false ||
				plan["contains_branch_name"] != false ||
				plan["contains_file_content"] != false {
				t.Fatalf("request builder plan = %#v", plan)
			}
			blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
			if len(blockedReasons) != 3 ||
				blockedReasons[0] != "provider_review_request_builder_not_armed" ||
				blockedReasons[1] != "provider_review_live_adapter_not_implemented" ||
				blockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("request builder blocked reasons = %#v", blockedReasons)
			}
			encoded, _ := json.Marshal(plan)
			for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("request builder plan leaked %q: %s", leak, encoded)
				}
			}
		})
	}
}

func TestProviderReviewAttemptAdapterResponseHandlerPlan(t *testing.T) {
	for _, item := range []struct {
		name            string
		provider        string
		operation       string
		endpoint        string
		handlerName     string
		unlockOperation string
		unlockStatus    string
		requiresUpdate  bool
		wantNonEmpty    bool
	}{
		{
			name:            "github branch ref handler",
			provider:        "github",
			operation:       "create_branch_ref",
			endpoint:        "github.create_branch_ref",
			handlerName:     "handle_branch_ref_response",
			unlockOperation: "commit_starter_files",
			unlockStatus:    "dependency_satisfied",
			requiresUpdate:  true,
			wantNonEmpty:    true,
		},
		{
			name:            "github commit starter files handler",
			provider:        "github",
			operation:       "commit_starter_files",
			endpoint:        "github.commit_files",
			handlerName:     "handle_commit_files_response",
			unlockOperation: "open_review_request",
			unlockStatus:    "dependency_satisfied",
			requiresUpdate:  true,
			wantNonEmpty:    true,
		},
		{
			name:         "gitea review request handler",
			provider:     "gitea",
			operation:    "open_review_request",
			endpoint:     "gitea.open_review",
			handlerName:  "handle_review_request_response",
			wantNonEmpty: true,
		},
		{
			name:            "gitea branch ref handler",
			provider:        "gitea",
			operation:       "create_branch_ref",
			endpoint:        "gitea.create_branch_ref",
			handlerName:     "handle_branch_ref_response",
			wantNonEmpty:    true,
			unlockOperation: "commit_starter_files",
			unlockStatus:    "dependency_satisfied",
			requiresUpdate:  true,
		},
		{
			name:      "unknown provider returns empty response handler plan",
			provider:  "raw_provider",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "empty operation returns empty response handler plan",
			provider:  "github",
			operation: "",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "mismatched endpoint returns empty response handler plan",
			provider:  "github",
			operation: "create_branch_ref",
			endpoint:  "gitea.create_branch_ref",
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			plan := providerReviewAttemptAdapterResponseHandlerPlan(item.provider, item.operation, item.endpoint)
			if !item.wantNonEmpty {
				if len(plan) != 0 {
					t.Fatalf("response handler plan should be empty: %#v", plan)
				}
				return
			}
			if plan["mode"] != "redacted_attempt_adapter_response_handler_plan" ||
				plan["response_handler_state"] != "blocked" ||
				plan["response_handler_ready"] != false ||
				plan["response_handler_ready_reason"] != "provider_review_response_handler_not_armed" ||
				plan["provider_type"] != item.provider ||
				plan["operation_name"] != item.operation ||
				plan["endpoint_key"] != item.endpoint ||
				plan["handler_name"] != item.handlerName ||
				plan["response_status"] != "pending" ||
				plan["success_attempt_status"] != "completed" ||
				plan["retry_attempt_status"] != "planned" ||
				plan["failure_attempt_status"] != "failed" ||
				plan["dependency_unlocks_operation"] != item.unlockOperation ||
				plan["dependency_update_status"] != item.unlockStatus ||
				plan["requires_response_diagnostics"] != true ||
				plan["requires_idempotency_ledger"] != true ||
				plan["requires_dependency_update"] != item.requiresUpdate ||
				plan["requires_transaction_handler"] != true ||
				plan["requires_mutation_arming"] != true ||
				plan["handler_interface_registered"] != true ||
				plan["handler_registered"] != true ||
				plan["handler_implemented"] != false ||
				plan["provider_response_classified"] != false ||
				plan["attempt_status_selected"] != false ||
				plan["dependency_update_selected"] != false ||
				plan["provider_request_id_recorded"] != false ||
				plan["response_body_recorded"] != false ||
				plan["response_headers_recorded"] != false ||
				plan["response_handler_boundary_redacted"] != true ||
				plan["external_call_made"] != false ||
				plan["provider_api_call_made"] != false ||
				plan["provider_api_mutation"] != "disabled" ||
				plan["response_body_included"] != false ||
				plan["headers_included"] != false ||
				plan["provider_request_id_included"] != false ||
				plan["contains_token"] != false ||
				plan["contains_provider_url"] != false ||
				plan["contains_repository_ref"] != false ||
				plan["contains_branch_name"] != false ||
				plan["contains_file_content"] != false {
				t.Fatalf("response handler plan = %#v", plan)
			}
			if successClasses := stringSliceFromAny(plan["expected_success_classes"]); len(successClasses) != 1 || successClasses[0] != "2xx" {
				t.Fatalf("response handler success classes = %#v", successClasses)
			}
			if retryClasses := stringSliceFromAny(plan["retryable_status_classes"]); len(retryClasses) != 1 || retryClasses[0] != "5xx" {
				t.Fatalf("response handler retry classes = %#v", retryClasses)
			}
			if failureClasses := stringSliceFromAny(plan["terminal_failure_status_classes"]); len(failureClasses) != 1 || failureClasses[0] != "4xx" {
				t.Fatalf("response handler failure classes = %#v", failureClasses)
			}
			blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
			if len(blockedReasons) != 3 ||
				blockedReasons[0] != "provider_review_response_handler_not_armed" ||
				blockedReasons[1] != "provider_review_live_adapter_not_implemented" ||
				blockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("response handler blocked reasons = %#v", blockedReasons)
			}
			encoded, _ := json.Marshal(plan)
			for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("response handler plan leaked %q: %s", leak, encoded)
				}
			}
		})
	}
}

func TestDisabledProviderReviewAttemptResponseHandlerRejectsMismatchedEndpoint(t *testing.T) {
	handler := disabledProviderReviewAttemptResponseHandler{handlerName: "handle_branch_ref_response"}
	plan := handler.BuildPlan(providerReviewAttemptResponseHandlerInput{
		ProviderType:  "github",
		OperationName: "create_branch_ref",
		EndpointKey:   "gitea.create_branch_ref",
	})
	if len(plan) != 0 {
		t.Fatalf("mismatched endpoint direct response handler plan should be empty: %#v", plan)
	}
}

func TestDisabledProviderReviewAttemptRequestBuilderRejectsMismatchedEndpoint(t *testing.T) {
	builder := disabledProviderReviewAttemptRequestBuilder{builderName: "build_redacted_branch_ref_request"}
	plan := builder.BuildPlan(providerReviewAttemptRequestBuilderInput{
		ProviderType:  "github",
		OperationName: "create_branch_ref",
		EndpointKey:   "gitea.create_branch_ref",
	})
	if len(plan) != 0 {
		t.Fatalf("mismatched endpoint direct builder plan should be empty: %#v", plan)
	}
}

func TestProviderReviewAttemptAdapterTransactionPlan(t *testing.T) {
	operation := map[string]any{
		"name":            "create_branch_ref",
		"endpoint_key":    "github.create_branch_ref",
		"operation_order": 10,
	}
	claimPlan := map[string]any{"claim_metadata_ready": true}
	responsePlan := map[string]any{
		"mode":                         "redacted_attempt_adapter_response_plan",
		"success_attempt_status":       "completed",
		"retry_attempt_status":         "planned",
		"failure_attempt_status":       "failed",
		"dependency_unlocks_operation": "commit_starter_files",
		"dependency_update_status":     "dependency_satisfied",
		"requires_dependency_update":   true,
	}
	plan := providerReviewAttemptAdapterTransactionPlan(operation, claimPlan, responsePlan)
	if plan["mode"] != "redacted_attempt_adapter_transaction_plan" ||
		plan["transaction_state"] != "blocked" ||
		plan["transaction_ready"] != false ||
		plan["transaction_ready_reason"] != "provider_review_transaction_not_armed" ||
		plan["transaction_metadata_ready"] != true ||
		plan["operation_name"] != "create_branch_ref" ||
		plan["endpoint_key"] != "github.create_branch_ref" ||
		plan["operation_order"] != 10 ||
		plan["claim_status_from"] != "planned" ||
		plan["claim_status_to"] != "running" ||
		plan["success_attempt_status"] != "completed" ||
		plan["retry_attempt_status"] != "planned" ||
		plan["failure_attempt_status"] != "failed" ||
		plan["dependency_unlocks_operation"] != "commit_starter_files" ||
		plan["dependency_update_status"] != "dependency_satisfied" ||
		plan["requires_database_transaction"] != true ||
		plan["requires_attempt_status_planned"] != true ||
		plan["requires_attempt_status_running"] != true ||
		plan["requires_optimistic_lock"] != true ||
		plan["requires_idempotency_ledger"] != true ||
		plan["requires_provider_call_boundary"] != true ||
		plan["requires_response_diagnostics"] != true ||
		plan["requires_dependency_update"] != true ||
		plan["requires_mutation_arming"] != true ||
		len(mapFromAny(plan["provider_call_boundary_plan"])) == 0 ||
		plan["transaction_opened"] != false ||
		plan["attempt_claim_verified"] != false ||
		plan["idempotency_claim_verified"] != false ||
		plan["provider_call_boundary_recorded"] != false ||
		plan["provider_response_classified"] != false ||
		plan["attempt_status_updated"] != false ||
		plan["response_recorded"] != false ||
		plan["dependency_update_recorded"] != false ||
		plan["provider_request_id_recorded"] != false ||
		plan["provider_response_body_recorded"] != false ||
		plan["provider_response_headers_recorded"] != false ||
		plan["adapter_implemented"] != false ||
		plan["mutation_armed"] != false ||
		plan["external_call_made"] != false ||
		plan["provider_api_call_made"] != false ||
		plan["provider_api_mutation"] != "disabled" ||
		plan["request_body_included"] != false ||
		plan["response_body_included"] != false ||
		plan["headers_included"] != false ||
		plan["authorization_header_included"] != false ||
		plan["provider_url_included"] != false ||
		plan["idempotency_key_included"] != false ||
		plan["contains_token"] != false ||
		plan["contains_provider_url"] != false ||
		plan["contains_repository_ref"] != false ||
		plan["contains_branch_name"] != false ||
		plan["contains_file_content"] != false ||
		plan["transaction_boundary_redacted"] != true {
		t.Fatalf("providerReviewAttemptAdapterTransactionPlan() = %#v", plan)
	}
	sequence := stringSliceFromAny(plan["transaction_sequence"])
	if len(sequence) != 6 ||
		sequence[0] != "verify_attempt_claim" ||
		sequence[1] != "verify_idempotency_claim" ||
		sequence[2] != "record_provider_call_boundary" ||
		sequence[3] != "classify_provider_response" ||
		sequence[4] != "update_attempt_status" ||
		sequence[5] != "update_dependency_status" {
		t.Fatalf("transaction sequence = %#v", sequence)
	}
	boundaryPlan := mapFromAny(plan["provider_call_boundary_plan"])
	if boundaryPlan["mode"] != "redacted_attempt_adapter_provider_call_boundary_plan" ||
		boundaryPlan["provider_call_boundary_state"] != "blocked" ||
		boundaryPlan["provider_call_boundary_ready"] != false ||
		boundaryPlan["provider_call_boundary_ready_reason"] != "provider_review_provider_call_boundary_not_armed" ||
		boundaryPlan["provider_call_boundary_metadata_ready"] != true ||
		boundaryPlan["operation_name"] != "create_branch_ref" ||
		boundaryPlan["endpoint_key"] != "github.create_branch_ref" ||
		boundaryPlan["operation_order"] != 10 ||
		boundaryPlan["idempotency_key_kind"] != "operation_scope_hash" ||
		boundaryPlan["requires_database_transaction"] != true ||
		boundaryPlan["requires_attempt_claim"] != true ||
		boundaryPlan["requires_idempotency_claim"] != true ||
		boundaryPlan["requires_response_diagnostics"] != true ||
		boundaryPlan["requires_mutation_arming"] != true ||
		boundaryPlan["transaction_opened"] != false ||
		boundaryPlan["attempt_claim_verified"] != false ||
		boundaryPlan["idempotency_claim_verified"] != false ||
		boundaryPlan["provider_call_boundary_opened"] != false ||
		boundaryPlan["provider_call_boundary_recorded"] != false ||
		boundaryPlan["provider_call_started_recorded"] != false ||
		boundaryPlan["provider_call_finished_recorded"] != false ||
		boundaryPlan["provider_request_sent"] != false ||
		boundaryPlan["provider_response_received"] != false ||
		boundaryPlan["provider_request_id_recorded"] != false ||
		boundaryPlan["provider_response_status_recorded"] != false ||
		boundaryPlan["provider_response_body_recorded"] != false ||
		boundaryPlan["provider_response_headers_recorded"] != false ||
		boundaryPlan["provider_call_boundary_redacted"] != true ||
		boundaryPlan["external_call_made"] != false ||
		boundaryPlan["provider_api_call_made"] != false ||
		boundaryPlan["provider_api_mutation"] != "disabled" ||
		boundaryPlan["request_body_included"] != false ||
		boundaryPlan["response_body_included"] != false ||
		boundaryPlan["headers_included"] != false ||
		boundaryPlan["authorization_header_included"] != false ||
		boundaryPlan["provider_url_included"] != false ||
		boundaryPlan["idempotency_key_included"] != false ||
		boundaryPlan["provider_request_id_included"] != false ||
		boundaryPlan["contains_token"] != false ||
		boundaryPlan["contains_provider_url"] != false ||
		boundaryPlan["contains_repository_ref"] != false ||
		boundaryPlan["contains_branch_name"] != false ||
		boundaryPlan["contains_file_content"] != false {
		t.Fatalf("provider call boundary plan = %#v", boundaryPlan)
	}
	boundarySequence := stringSliceFromAny(boundaryPlan["boundary_sequence"])
	if len(boundarySequence) != 7 ||
		boundarySequence[0] != "verify_attempt_claim" ||
		boundarySequence[4] != "stage_provider_request_send" ||
		boundarySequence[6] != "commit_database_transaction" {
		t.Fatalf("provider call boundary sequence = %#v", boundarySequence)
	}
	boundaryBlockedReasons := stringSliceFromAny(boundaryPlan["blocked_reasons"])
	if len(boundaryBlockedReasons) != 4 ||
		boundaryBlockedReasons[0] != "provider_review_provider_call_boundary_not_armed" ||
		boundaryBlockedReasons[1] != "provider_api_call_not_made" ||
		boundaryBlockedReasons[2] != "provider_review_adapter_not_implemented" ||
		boundaryBlockedReasons[3] != "provider_review_mutation_not_armed" {
		t.Fatalf("provider call boundary blocked reasons = %#v", boundaryBlockedReasons)
	}
	blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
	if len(blockedReasons) != 5 ||
		blockedReasons[0] != "provider_review_attempt_claim_not_recorded" ||
		blockedReasons[1] != "provider_review_transaction_not_armed" ||
		blockedReasons[2] != "provider_api_call_not_made" ||
		blockedReasons[3] != "provider_review_adapter_not_implemented" ||
		blockedReasons[4] != "provider_review_mutation_not_armed" {
		t.Fatalf("transaction blocked reasons = %#v", blockedReasons)
	}
	encoded, _ := json.Marshal(plan)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("transaction plan leaked %q: %s", leak, encoded)
		}
	}
	if got := providerReviewAttemptAdapterTransactionPlan(nil, nil, nil); len(got) != 0 {
		t.Fatalf("empty operation transaction plan = %#v", got)
	}
	got := providerReviewAttemptAdapterTransactionPlan(
		map[string]any{"name": "raw_operation", "endpoint_key": "github.create_branch_ref"},
		claimPlan,
		responsePlan,
	)
	if len(got) != 0 {
		t.Fatalf("invalid operation transaction plan should be empty: %#v", got)
	}
	got = providerReviewAttemptAdapterTransactionPlan(
		map[string]any{"name": "create_branch_ref", "endpoint_key": "github.secret"},
		claimPlan,
		responsePlan,
	)
	if len(got) != 0 {
		t.Fatalf("invalid endpoint transaction plan should be empty: %#v", got)
	}
	notReadyPlan := providerReviewAttemptAdapterTransactionPlan(operation, map[string]any{"claim_metadata_ready": false}, responsePlan)
	if notReadyPlan["transaction_metadata_ready"] != false {
		t.Fatalf("not ready transaction plan = %#v", notReadyPlan)
	}
	nilClaimPlan := providerReviewAttemptAdapterTransactionPlan(operation, nil, responsePlan)
	if nilClaimPlan["transaction_metadata_ready"] != false {
		t.Fatalf("nil claim transaction plan = %#v", nilClaimPlan)
	}
	mismatchedResponseModePlan := providerReviewAttemptAdapterTransactionPlan(operation, claimPlan, map[string]any{"mode": "raw_response_plan"})
	if mismatchedResponseModePlan["transaction_metadata_ready"] != false {
		t.Fatalf("mismatched response mode transaction plan = %#v", mismatchedResponseModePlan)
	}
	mismatchedResponseModeBoundaryPlan := mapFromAny(mismatchedResponseModePlan["provider_call_boundary_plan"])
	if mismatchedResponseModeBoundaryPlan["provider_call_boundary_metadata_ready"] != false {
		t.Fatalf("mismatched response mode boundary plan = %#v", mismatchedResponseModeBoundaryPlan)
	}
	redactedPlan := providerReviewAttemptAdapterTransactionPlan(operation, claimPlan, map[string]any{
		"mode":                         "redacted_attempt_adapter_response_plan",
		"success_attempt_status":       "raw-success-secret",
		"retry_attempt_status":         "raw-retry-secret",
		"failure_attempt_status":       "raw-failure-secret",
		"dependency_unlocks_operation": "raw-operation-secret",
		"dependency_update_status":     "raw-dependency-secret",
	})
	if redactedPlan["success_attempt_status"] != "blocked" ||
		redactedPlan["retry_attempt_status"] != "blocked" ||
		redactedPlan["failure_attempt_status"] != "blocked" ||
		redactedPlan["dependency_unlocks_operation"] != "" ||
		redactedPlan["dependency_update_status"] != "blocked" {
		t.Fatalf("transaction plan should redact raw response values: %#v", redactedPlan)
	}
	encoded, _ = json.Marshal(redactedPlan)
	for _, leak := range []string{"raw-success-secret", "raw-retry-secret", "raw-failure-secret", "raw-operation-secret", "raw-dependency-secret"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("transaction plan leaked raw response value %q: %s", leak, encoded)
		}
	}
}

func TestProviderReviewAttemptAdapterCredentialBindingPlan(t *testing.T) {
	for _, item := range []struct {
		name         string
		provider     string
		operation    string
		endpoint     string
		authScheme   string
		wantNonEmpty bool
	}{
		{
			name:         "github branch ref uses bearer token",
			provider:     "github",
			operation:    "create_branch_ref",
			endpoint:     "github.create_branch_ref",
			authScheme:   "bearer_token",
			wantNonEmpty: true,
		},
		{
			name:         "gitea review request uses token auth",
			provider:     "gitea",
			operation:    "open_review_request",
			endpoint:     "gitea.open_review",
			authScheme:   "token",
			wantNonEmpty: true,
		},
		{
			name:         "unknown provider returns empty plan",
			provider:     "raw_provider",
			operation:    "create_branch_ref",
			wantNonEmpty: false,
		},
		{
			name:         "unknown operation returns empty plan",
			provider:     "github",
			operation:    "raw_operation",
			wantNonEmpty: false,
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			plan := providerReviewAttemptAdapterCredentialBindingPlan(item.provider, item.operation)
			if !item.wantNonEmpty {
				if len(plan) != 0 {
					t.Fatalf("credential binding plan should be empty: %#v", plan)
				}
				return
			}
			if plan["mode"] != "redacted_attempt_adapter_credential_binding_plan" ||
				plan["credential_binding_state"] != "blocked" ||
				plan["credential_binding_ready"] != false ||
				plan["credential_binding_ready_reason"] != "provider_credential_runtime_binding_not_armed" ||
				plan["provider_type"] != item.provider ||
				plan["operation_name"] != item.operation ||
				plan["endpoint_key"] != item.endpoint ||
				plan["auth_scheme"] != item.authScheme ||
				plan["credential_source_kind"] != "provider_account_token_env" ||
				plan["requires_provider_account"] != true ||
				plan["requires_allowed_token_env"] != true ||
				plan["requires_runtime_token_present"] != true ||
				plan["credential_bound"] != false ||
				plan["authorization_header_materialized"] != false ||
				plan["token_env_name_included"] != false ||
				plan["token_value_included"] != false ||
				plan["token_stored"] != false ||
				plan["headers_included"] != false ||
				plan["external_call_made"] != false ||
				plan["provider_api_call_made"] != false ||
				plan["provider_api_mutation"] != "disabled" ||
				plan["contains_token"] != false ||
				plan["contains_provider_url"] != false ||
				plan["contains_repository_ref"] != false ||
				plan["contains_branch_name"] != false ||
				plan["contains_file_content"] != false ||
				plan["credential_boundary_redacted"] != true {
				t.Fatalf("credential binding plan = %#v", plan)
			}
			blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
			if len(blockedReasons) != 3 ||
				blockedReasons[0] != "provider_credential_runtime_binding_not_armed" ||
				blockedReasons[1] != "provider_review_adapter_not_implemented" ||
				blockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("credential binding blocked reasons = %#v", blockedReasons)
			}
			encoded, _ := json.Marshal(plan)
			for _, leak := range []string{"ASSOPS_TEMPLATE_PROVIDER_TOKEN", "secret-token", "Authorization", "raw_provider"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("credential binding plan leaked %q: %s", leak, encoded)
				}
			}
		})
	}
}

func TestProviderReviewAttemptOrchestrationSummaryHandlesEdgeStates(t *testing.T) {
	t.Run("returns empty dispatch plan for empty operation", func(t *testing.T) {
		if got := providerReviewAttemptAdapterDispatchPlan(nil, nil, nil, nil, nil); len(got) != 0 {
			t.Fatalf("empty operation dispatch plan = %#v", got)
		}
	})
	t.Run("redacts unknown adapter dispatch plan fields", func(t *testing.T) {
		dispatchPlan := providerReviewAttemptAdapterDispatchPlan(
			map[string]any{
				"name":            "raw_operation",
				"endpoint_key":    "github.secret_endpoint",
				"operation_order": 99,
			},
			map[string]any{
				"payload_builder":  "raw_builder",
				"response_handler": "raw_handler",
			},
			map[string]any{
				"status": "raw_status",
			},
			map[string]any{
				"mode": "raw_adapter_contract",
			},
			map[string]any{
				"claim_metadata_ready": true,
			},
		)
		if dispatchPlan["mode"] != "redacted_attempt_adapter_dispatch_plan" ||
			dispatchPlan["dispatch_state"] != "blocked" ||
			dispatchPlan["dispatch_ready"] != false ||
			dispatchPlan["dispatch_ready_reason"] != "provider_api_adapter_dispatch_not_armed" ||
			dispatchPlan["dispatch_metadata_ready"] != false ||
			dispatchPlan["provider_type"] != "" ||
			dispatchPlan["adapter_kind"] != "" ||
			dispatchPlan["operation_name"] != "" ||
			dispatchPlan["endpoint_key"] != "" ||
			dispatchPlan["method"] != "" ||
			dispatchPlan["payload_shape"] != "" ||
			dispatchPlan["payload_builder"] != "build_redacted_provider_request" ||
			dispatchPlan["response_handler"] != "handle_provider_response" ||
			dispatchPlan["provider_api_call_made"] != false ||
			dispatchPlan["provider_api_mutation"] != "disabled" ||
			dispatchPlan["contains_token"] != false ||
			dispatchPlan["contains_provider_url"] != false ||
			dispatchPlan["contains_repository_ref"] != false ||
			dispatchPlan["contains_branch_name"] != false ||
			dispatchPlan["contains_file_content"] != false {
			t.Fatalf("unknown operation dispatch plan should be redacted: %#v", dispatchPlan)
		}
		if transportPlan := mapFromAny(dispatchPlan["transport_plan"]); len(transportPlan) != 0 {
			t.Fatalf("unknown operation transport plan should be empty: %#v", transportPlan)
		}
		if requestPlan := mapFromAny(dispatchPlan["request_materialization_plan"]); len(requestPlan) != 0 {
			t.Fatalf("unknown operation request materialization plan should be empty: %#v", requestPlan)
		}
		if responsePlan := mapFromAny(dispatchPlan["response_plan"]); len(responsePlan) != 0 {
			t.Fatalf("unknown operation response plan should be empty: %#v", responsePlan)
		}
		if credentialPlan := mapFromAny(dispatchPlan["credential_binding_plan"]); len(credentialPlan) != 0 {
			t.Fatalf("unknown operation credential binding plan should be empty: %#v", credentialPlan)
		}
		if runtimePlan := mapFromAny(dispatchPlan["adapter_runtime_plan"]); len(runtimePlan) != 0 {
			t.Fatalf("unknown operation runtime plan should be empty: %#v", runtimePlan)
		}
		if transactionPlan := mapFromAny(dispatchPlan["transaction_plan"]); len(transactionPlan) != 0 {
			t.Fatalf("unknown operation transaction plan should be empty: %#v", transactionPlan)
		}
		if invocationPlan := mapFromAny(dispatchPlan["invocation_plan"]); len(invocationPlan) != 0 {
			t.Fatalf("unknown operation invocation plan should be empty: %#v", invocationPlan)
		}
		blockedReasons := stringSliceFromAny(dispatchPlan["blocked_reasons"])
		if len(blockedReasons) != 5 ||
			blockedReasons[0] != "provider_review_dispatch_provider_unknown" ||
			blockedReasons[1] != "provider_review_dispatch_metadata_not_ready" ||
			blockedReasons[2] != "provider_review_attempt_claim_not_recorded" ||
			blockedReasons[3] != "provider_review_adapter_not_implemented" ||
			blockedReasons[4] != "provider_review_mutation_not_armed" {
			t.Fatalf("unknown operation dispatch blocked reasons = %#v", blockedReasons)
		}
		encoded, _ := json.Marshal(dispatchPlan)
		for _, leak := range []string{"raw_operation", "github.secret_endpoint", "raw_builder", "raw_handler", "raw_adapter_contract"} {
			if strings.Contains(string(encoded), leak) {
				t.Fatalf("unknown operation dispatch plan leaked %q: %s", leak, encoded)
			}
		}
	})
	t.Run("redacts unknown attempt claim plan fields", func(t *testing.T) {
		claimPlan := providerReviewAttemptExecutionClaimPlan(
			map[string]any{
				"name":              "raw_operation",
				"endpoint_key":      "github.secret_endpoint",
				"status":            "retrying",
				"dependency_status": "raw_dependency",
				"replay_check":      "raw_replay",
				"conflict_policy":   "raw_conflict",
				"retry_policy":      "raw_retry",
				"operation_order":   99,
			},
			false,
			false,
		)
		if claimPlan["mode"] != "redacted_attempt_execution_claim_plan" ||
			claimPlan["claim_state"] != "blocked" ||
			claimPlan["claim_ready"] != false ||
			claimPlan["claim_metadata_ready"] != false ||
			claimPlan["operation_name"] != "" ||
			claimPlan["endpoint_key"] != "" ||
			claimPlan["attempt_status"] != "blocked" ||
			claimPlan["dependency_status"] != "blocked" ||
			claimPlan["dependency_ready"] != false ||
			claimPlan["replay_check"] != "" ||
			claimPlan["conflict_policy"] != "" ||
			claimPlan["retry_policy"] != "" ||
			claimPlan["idempotency_metadata_ready"] != false ||
			claimPlan["response_diagnostics_ready"] != false ||
			claimPlan["provider_api_call_made"] != false ||
			claimPlan["provider_api_mutation"] != "disabled" ||
			claimPlan["contains_token"] != false {
			t.Fatalf("unknown operation claim plan should be redacted: %#v", claimPlan)
		}
		blockedReasons := stringSliceFromAny(claimPlan["blocked_reasons"])
		if len(blockedReasons) != 6 ||
			blockedReasons[0] != "provider_review_response_diagnostics_missing" ||
			blockedReasons[1] != "provider_review_idempotency_metadata_missing" ||
			blockedReasons[2] != "provider_review_dependency_not_ready" ||
			blockedReasons[3] != "provider_review_attempt_status_not_planned" ||
			blockedReasons[4] != "provider_review_adapter_not_implemented" ||
			blockedReasons[5] != "provider_review_mutation_not_armed" {
			t.Fatalf("unknown operation claim blocked reasons = %#v", blockedReasons)
		}
		encoded, _ := json.Marshal(claimPlan)
		for _, leak := range []string{"raw_operation", "github.secret_endpoint", "retrying", "raw_dependency", "raw_replay", "raw_conflict", "raw_retry"} {
			if strings.Contains(string(encoded), leak) {
				t.Fatalf("unknown operation claim plan leaked %q: %s", leak, encoded)
			}
		}
	})
	t.Run("redacts unknown adapter contract operation name", func(t *testing.T) {
		contract := providerReviewAttemptCandidateAdapterContract(
			map[string]any{
				"name":            "raw_operation",
				"endpoint_key":    "github.secret_endpoint",
				"operation_order": 99,
			},
			map[string]any{
				"payload_builder":  "raw_builder",
				"response_handler": "raw_handler",
			},
			map[string]any{
				"status":                   "raw_status",
				"success_status_class":     "3xx",
				"retryable_status_classes": []any{"5xx", "secret-token", "4xx"},
			},
		)
		if contract["operation_name"] != "" ||
			contract["endpoint_key"] != "" ||
			contract["payload_builder"] != "build_redacted_provider_request" ||
			contract["response_handler"] != "handle_provider_response" ||
			contract["response_status"] != "blocked" ||
			contract["success_status_class"] != "" ||
			contract["provider_api_call_made"] != false ||
			contract["provider_api_mutation"] != "disabled" ||
			contract["contains_token"] != false {
			t.Fatalf("unknown operation adapter contract should be redacted: %#v", contract)
		}
		retryable := stringSliceFromAny(contract["retryable_status_classes"])
		if len(retryable) != 2 || retryable[0] != "5xx" || retryable[1] != "4xx" {
			t.Fatalf("unknown operation retryable classes should be allowlisted: %#v", retryable)
		}
		encoded, _ := json.Marshal(contract)
		for _, leak := range []string{"raw_operation", "github.secret_endpoint", "raw_builder", "raw_handler", "raw_status", "secret-token"} {
			if strings.Contains(string(encoded), leak) {
				t.Fatalf("unknown operation adapter contract leaked %q: %s", leak, encoded)
			}
		}
	})
	t.Run("uses first known ready operation name", func(t *testing.T) {
		summary := providerReviewAttemptOrchestrationSummary([]map[string]any{
			{
				"name":              "unknown_operation",
				"status":            "planned",
				"dependency_status": "independent",
			},
			{
				"name":              "commit_starter_files",
				"status":            "planned",
				"dependency_status": "dependency_satisfied",
			},
		})
		if summary["ready_count"] != 2 || summary["next_operation"] != "commit_starter_files" || summary["dependency_chain_status"] != "ready" {
			t.Fatalf("mixed operation name orchestration summary = %#v", summary)
		}
		candidate := mapFromAny(summary["execution_candidate"])
		if candidate["next_operation"] != "commit_starter_files" || candidate["endpoint_key"] != "" {
			t.Fatalf("mixed operation name execution candidate = %#v", candidate)
		}
		claimPlan := mapFromAny(candidate["claim_plan"])
		if claimPlan["claim_ready"] != false || claimPlan["claim_metadata_ready"] != false || claimPlan["operation_name"] != "commit_starter_files" || claimPlan["endpoint_key"] != "" {
			t.Fatalf("mixed operation name claim plan = %#v", claimPlan)
		}
	})
	t.Run("dependency failure wins over completed status", func(t *testing.T) {
		summary := providerReviewAttemptOrchestrationSummary([]map[string]any{{
			"name":              "commit_starter_files",
			"status":            "completed",
			"dependency_status": "dependency_failed",
		}})
		if summary["completed_count"] != 0 ||
			summary["blocked_count"] != 1 ||
			summary["dependency_chain_status"] != "blocked" {
			t.Fatalf("conflicting dependency orchestration summary = %#v", summary)
		}
	})
}

func TestProviderReviewAdapterRehearsalSanitizerRecomputesStatus(t *testing.T) {
	summary := sanitizedProviderReviewAdapterRehearsal(map[string]any{
		"status":                    "ready",
		"operation_count":           99,
		"ready_operation_count":     98,
		"blocked_operation_count":   97,
		"mutation_arming_candidate": true,
		"operations": []map[string]any{
			{
				"name":               "commit_starter_files",
				"endpoint_key":       "github.commit_files",
				"status":             "blocked",
				"blocked_reasons":    []any{"starter_file_payload_staged"},
				"external_call_made": true,
			},
		},
	})
	if summary["status"] != "blocked" ||
		summary["operation_count"] != 1 ||
		summary["ready_operation_count"] != 0 ||
		summary["blocked_operation_count"] != 1 ||
		summary["mutation_arming_candidate"] != false ||
		summary["external_call_made"] != false ||
		summary["provider_api_mutation"] != "disabled" {
		t.Fatalf("sanitized rehearsal should recompute status and counts: %#v", summary)
	}
	empty := sanitizedProviderReviewAdapterRehearsal(map[string]any{"status": "ready"})
	if empty["status"] != "not_recorded" || empty["mutation_arming_candidate"] != false {
		t.Fatalf("empty rehearsal should be not recorded: %#v", empty)
	}
}

func TestProviderReviewMutationArmingPlanSanitizerKeepsMutationOff(t *testing.T) {
	armed := sanitizedProviderReviewMutationArmingPlan(map[string]any{
		"status":                   "armed",
		"mode":                     "raw_mutation_arming_plan",
		"required_config":          "SECRET_CONFIG",
		"execution_enabled_config": true,
		"adapter_rehearsal_ready":  true,
		"mutation_armed":           true,
		"external_call_made":       true,
		"provider_api_call_made":   true,
		"provider_api_mutation":    "enabled",
		"contains_token":           true,
		"contains_provider_url":    true,
		"contains_repository_ref":  true,
		"contains_file_content":    true,
		"blocked_reasons":          []any{"provider_review_mutation_armed", "<script>alert(1)</script>"},
	})
	if armed["status"] != "ready_to_arm" ||
		armed["mode"] != "redacted_mutation_arming_plan" ||
		armed["required_config"] != "ASSOPS_ARM_PROVIDER_REVIEW_MUTATION" ||
		armed["mutation_armed"] != false ||
		armed["external_call_made"] != false ||
		armed["provider_api_call_made"] != false ||
		armed["provider_api_mutation"] != "disabled" ||
		armed["contains_token"] != false ||
		armed["contains_provider_url"] != false ||
		armed["contains_repository_ref"] != false ||
		armed["contains_file_content"] != false ||
		armed["adapter_mutation_currently_off"] != true {
		t.Fatalf("armed mutation plan should be downgraded and redacted: %#v", armed)
	}
	reasons := stringSliceFromAny(armed["blocked_reasons"])
	if !containsString(reasons, "provider_review_mutation_armed") ||
		containsString(reasons, "<script>alert(1)</script>") {
		t.Fatalf("mutation arming reasons should be allowlisted: %#v", reasons)
	}

	blocked := sanitizedProviderReviewMutationArmingPlan(map[string]any{
		"status":                   "ready_to_arm",
		"execution_enabled_config": true,
		"adapter_rehearsal_ready":  false,
	})
	if blocked["status"] != "blocked" || blocked["mutation_armed"] != false || blocked["provider_api_mutation"] != "disabled" {
		t.Fatalf("blocked mutation plan should remain mutation-off: %#v", blocked)
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

func TestProviderReviewAttemptsMigrationAndFreshInit(t *testing.T) {
	migration, err := os.ReadFile("../../migrations/014_provider_review_attempts.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	dependencyMigration, err := os.ReadFile("../../migrations/015_provider_review_attempt_dependencies.sql")
	if err != nil {
		t.Fatalf("read dependency migration: %v", err)
	}
	for _, token := range []string{
		"CREATE TABLE IF NOT EXISTS provider_review_attempts",
		"operation_approval_id UUID NOT NULL REFERENCES operation_approvals",
		"project_template_run_id UUID REFERENCES project_template_runs",
		"idempotency_key_hash TEXT NOT NULL DEFAULT ''",
		"idempotency_key_material JSONB NOT NULL DEFAULT '{}'::jsonb",
		"CHECK (provider_api_call_made = false)",
		"CHECK (external_call_made = false)",
		"idx_provider_review_attempts_approval_operation",
		"idx_provider_review_attempts_template_run",
	} {
		if !strings.Contains(string(migration), token) {
			t.Fatalf("migration missing %q", token)
		}
	}
	for _, token := range []string{
		"ADD COLUMN IF NOT EXISTS operation_order",
		"ADD COLUMN IF NOT EXISTS depends_on_operation",
		"ADD COLUMN IF NOT EXISTS dependency_status",
		"pg_constraint",
		"provider_review_attempts_dependency_status_check",
		"provider_review_attempts_depends_on_operation_check",
		"WHERE operation_order = 0",
		"idx_provider_review_attempts_approval_order",
	} {
		if !strings.Contains(string(dependencyMigration), token) {
			t.Fatalf("dependency migration missing %q", token)
		}
	}
	for _, path := range []string{"../../../deploy/docker-compose.yml", "../../../deploy/compose.prod.yml"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(content), "014_provider_review_attempts.sql") {
			t.Fatalf("%s missing 014_provider_review_attempts.sql init mount", path)
		}
		if !strings.Contains(string(content), "015_provider_review_attempt_dependencies.sql") {
			t.Fatalf("%s missing 015_provider_review_attempt_dependencies.sql init mount", path)
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

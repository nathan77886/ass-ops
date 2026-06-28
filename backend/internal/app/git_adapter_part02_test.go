package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProvisionTemplateRepositoryCreatesGitHubRepository(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST", "secret-token")
	var gotPath, gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode provider body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ssh_url":"git@github.com:acme/billing-service.git","html_url":"https://github.com/acme/billing-service"}`))
	}))
	defer server.Close()

	result, err := (&GitExecutor{HTTPClient: server.Client()}).ProvisionTemplateRepository(context.Background(),
		map[string]any{"id": "repo-1", "repo_key": "billing-service", "default_branch": "main", "description": "Billing API"},
		[]map[string]any{{
			"id":            "remote-1",
			"provider_type": "github",
			"metadata": map[string]any{
				"api_base_url": server.URL,
				"owner":        "acme",
				"token_env":    "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST",
				"private":      false,
			},
		}},
		nil,
	)
	if err != nil {
		t.Fatalf("ProvisionTemplateRepository: %v", err)
	}
	if gotPath != "/orgs/acme/repos" {
		t.Fatalf("provider path = %q, want /orgs/acme/repos", gotPath)
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("Authorization header = %q", gotAuth)
	}
	if gotBody["name"] != "billing-service" || gotBody["private"] != false || gotBody["description"] != "Billing API" {
		t.Fatalf("provider body = %#v", gotBody)
	}
	if result.Details["provisioned"] != true {
		t.Fatalf("provisioned = %v, want true", result.Details["provisioned"])
	}
	if result.Details["remote_url"] != "git@github.com:acme/billing-service.git" {
		t.Fatalf("remote_url = %v", result.Details["remote_url"])
	}
}

func TestProvisionTemplateRepositorySkipsStarterPushForProtectedExternalRemote(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST", "secret-token")
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ssh_url":"git@github.com:acme/protected-service.git","html_url":"https://github.com/acme/protected-service"}`))
	}))
	defer server.Close()

	result, err := (&GitExecutor{HTTPClient: server.Client()}).ProvisionTemplateRepository(context.Background(),
		map[string]any{"id": "repo-1", "repo_key": "protected-service", "default_branch": "main"},
		[]map[string]any{{
			"id":            "remote-1",
			"provider_type": "github",
			"protected":     true,
			"metadata": map[string]any{
				"api_base_url": server.URL,
				"owner":        "acme",
				"token_env":    "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST",
			},
		}},
		[]map[string]any{{"path": "README.md", "content": "# Protected\n"}},
	)
	if err != nil {
		t.Fatalf("ProvisionTemplateRepository: %v", err)
	}
	if !called {
		t.Fatal("provider repository should still be created")
	}
	if result.Details["provisioned"] != false || result.Details["repository_created"] != true || result.Details["starter_push_skipped"] != true {
		t.Fatalf("protected branch details = %#v", result.Details)
	}
	if !strings.Contains(fmt.Sprint(result.Details["reason"]), "marked protected") {
		t.Fatalf("reason = %v", result.Details["reason"])
	}
	reconcile := mapFromAny(result.Details["repository_reconciliation"])
	if reconcile["kind"] != "protected_branch" || reconcile["guardrail"] != "protected_branch_push_blocked" {
		t.Fatalf("reconciliation = %#v", reconcile)
	}
	if reconcile["default_branch"] != "main" || reconcile["file_count"] != 1 {
		t.Fatalf("reconciliation branch/file count = %#v", reconcile)
	}
}

func TestProvisionTemplateRepositoryReportsProtectedBranchStrategy(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST", "secret-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ssh_url":"git@github.com:acme/protected-service.git","html_url":"https://github.com/acme/protected-service"}`))
	}))
	defer server.Close()

	result, err := (&GitExecutor{HTTPClient: server.Client()}).ProvisionTemplateRepository(context.Background(),
		map[string]any{"id": "repo-1", "repo_key": "Protected Service", "default_branch": "release/main"},
		[]map[string]any{{
			"id":            "remote-1",
			"provider_type": "github",
			"protected":     true,
			"metadata": map[string]any{
				"api_base_url":    server.URL,
				"owner":           "acme",
				"token_env":       "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST",
				"branch_strategy": "proposed_branch",
				"branch_prefix":   "assops/starter",
			},
		}},
		[]map[string]any{{"path": "README.md", "content": "# Protected\n"}},
	)
	if err != nil {
		t.Fatalf("ProvisionTemplateRepository: %v", err)
	}
	reconcile := mapFromAny(result.Details["repository_reconciliation"])
	strategy := mapFromAny(reconcile["branch_strategy"])
	if strategy["mode"] != "proposed_branch" || strategy["strategy_status"] != "planned" {
		t.Fatalf("branch strategy = %#v", strategy)
	}
	readiness := mapFromAny(reconcile["provider_review_readiness"])
	if readiness["status"] != "planned" || readiness["execution_enabled"] != false || readiness["external_call_made"] != false {
		t.Fatalf("provider review readiness = %#v", readiness)
	}
	if readiness["branch_creation"] != "locally_planned" || readiness["review_request"] != "locally_planned" {
		t.Fatalf("provider review branch/review readiness = %#v", readiness)
	}
	executionPlan := mapFromAny(readiness["execution_plan"])
	if executionPlan["mode"] != "dry_run" || executionPlan["execution_enabled"] != false || executionPlan["external_call_made"] != false {
		t.Fatalf("execution plan should be dry-run only: %#v", executionPlan)
	}
	if executionPlan["provider_api_mutation"] != "disabled" || executionPlan["requires_approval"] != true {
		t.Fatalf("execution plan guardrails = %#v", executionPlan)
	}
	guardrail := mapFromAny(executionPlan["execution_guardrail"])
	if guardrail["execution_mode"] != "disabled" ||
		guardrail["execution_enabled"] != false ||
		guardrail["execution_enabled_config"] != false ||
		guardrail["provider_api_call_made"] != false ||
		guardrail["provider_api_mutation"] != "disabled" {
		t.Fatalf("execution guardrail = %#v", guardrail)
	}
	if !containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "provider_review_execution_enabled") ||
		!containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "provider_review_mutation_armed") {
		t.Fatalf("execution guardrail blocked reasons = %#v", guardrail)
	}
	request := mapFromAny(executionPlan["execution_request"])
	if request["status"] != "approval_ready" ||
		request["approval_action"] != "project_template.provider_review.execute" ||
		request["resource_type"] != "project_template_run" ||
		request["payload_redacted"] != true ||
		request["contains_token"] != false ||
		request["provider_api_mutation"] != "disabled" {
		t.Fatalf("execution request = %#v", request)
	}
	if executionPlan["source_branch"] != "assops/starter/protected-service-release-main" || executionPlan["target_branch"] != "release/main" {
		t.Fatalf("execution plan branches = %#v", executionPlan)
	}
	steps := sliceOfMapsFromAny(executionPlan["steps"])
	if len(steps) != 3 || steps[0]["name"] != "create_branch" || steps[1]["name"] != "commit_starter_files" || steps[2]["name"] != "open_review" {
		t.Fatalf("execution plan steps = %#v", steps)
	}
	for _, step := range steps {
		if step["api_call"] != false {
			t.Fatalf("execution plan step should not call provider API: %#v", step)
		}
	}
	if strategy["proposed_branch"] != "assops/starter/protected-service-release-main" || strategy["target_branch"] != "release/main" {
		t.Fatalf("branch strategy branches = %#v", strategy)
	}
	if !strings.Contains(fmt.Sprint(reconcile["action_required"]), "assops/starter/protected-service-release-main") {
		t.Fatalf("action_required missing proposed branch: %#v", reconcile)
	}
}

func TestSafeTemplateBranchNameSanitizesParts(t *testing.T) {
	got := safeTemplateBranchName("assops// bad! starter/", "Billing API!", "release/main")
	if got != "assops/bad-starter/billing-api-release-main" {
		t.Fatalf("safeTemplateBranchName = %q", got)
	}
	if !isSafeGitRefPart(got) {
		t.Fatalf("generated branch should be a safe git ref: %q", got)
	}
}

func TestTemplateProviderReviewReadinessBlocksWithoutPlan(t *testing.T) {
	existing := templateProviderReviewReadiness("existing_repository", "github", nil)
	if existing["status"] != "blocked" || existing["execution_enabled"] != false || existing["external_call_made"] != false {
		t.Fatalf("existing repository readiness = %#v", existing)
	}
	if _, ok := existing["execution_plan"]; ok {
		t.Fatalf("blocked existing repository readiness should not include execution_plan: %#v", existing)
	}
	if !strings.Contains(fmt.Sprint(existing["message"]), "Review existing repository") {
		t.Fatalf("existing repository message = %#v", existing)
	}

	missingToken := templateProviderReviewReadiness("missing_token", "gitea", nil)
	if missingToken["status"] != "blocked" || !strings.Contains(fmt.Sprint(missingToken["message"]), "token") {
		t.Fatalf("missing token readiness = %#v", missingToken)
	}
	if _, ok := missingToken["execution_plan"]; ok {
		t.Fatalf("missing token readiness should not include execution_plan: %#v", missingToken)
	}

	unsupportedProtected := templateProviderReviewReadiness("protected_branch", "github", map[string]any{"mode": "custom", "strategy_status": "unsupported"})
	if unsupportedProtected["status"] != "blocked" || unsupportedProtected["execution_enabled"] != false {
		t.Fatalf("unsupported protected readiness = %#v", unsupportedProtected)
	}
	if _, ok := unsupportedProtected["execution_plan"]; ok {
		t.Fatalf("unsupported protected readiness should not include execution_plan: %#v", unsupportedProtected)
	}
	if !strings.Contains(fmt.Sprint(unsupportedProtected["message"]), "supported branch strategy") {
		t.Fatalf("unsupported protected message = %#v", unsupportedProtected)
	}

	unknown := templateProviderReviewReadiness("unknown_kind", "github", nil)
	if unknown["status"] != "blocked" || unknown["execution_enabled"] != false {
		t.Fatalf("unknown readiness = %#v", unknown)
	}
	if _, ok := unknown["execution_plan"]; ok {
		t.Fatalf("unknown readiness should not include execution_plan: %#v", unknown)
	}
	if !strings.Contains(fmt.Sprint(unknown["message"]), "Manual repository reconciliation") {
		t.Fatalf("unknown message = %#v", unknown)
	}
}

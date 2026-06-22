package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitRefsFromInput(t *testing.T) {
	tests := []struct {
		name          string
		input         any
		defaultBranch string
		wantBranches  []string
		wantTags      []string
	}{
		{
			name:          "nested refs",
			input:         map[string]any{"refs": map[string]any{"branches": []any{"main", "release"}, "tags": []any{"v1.0.0"}}},
			defaultBranch: "main",
			wantBranches:  []string{"main", "release"},
			wantTags:      []string{"v1.0.0"},
		},
		{
			name:          "default branch",
			input:         map[string]any{},
			defaultBranch: "develop",
			wantBranches:  []string{"develop"},
		},
		{
			name:          "scalar tag",
			input:         map[string]any{"refs": map[string]any{"tags": "v1.0.1"}},
			defaultBranch: "main",
			wantTags:      []string{"v1.0.1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gitRefsFromInput(tt.input, tt.defaultBranch)
			assertStringSlice(t, got.Branches, tt.wantBranches)
			assertStringSlice(t, got.Tags, tt.wantTags)
		})
	}
}

func TestRemoteURLFromRow(t *testing.T) {
	got := remoteURLFromRow(map[string]any{
		"remote_url": "",
		"urls":       []any{"git@example.com:org/repo.git"},
	})
	if got != "git@example.com:org/repo.git" {
		t.Fatalf("remoteURLFromRow = %q", got)
	}
}

func TestSafeLocalBareRemotePath(t *testing.T) {
	base := t.TempDir()
	if !safeLocalBareRemotePath(filepath.Join(base, "repo.git"), []string{base}) {
		t.Fatal("absolute local path should be accepted")
	}
	for _, path := range []string{"", "relative/repo.git", "https://example.com/repo.git", "git@example.com:org/repo.git", "/tmp/repo\x00.git"} {
		if safeLocalBareRemotePath(path, []string{base}) {
			t.Fatalf("safeLocalBareRemotePath(%q) = true, want false", path)
		}
	}
	if safeLocalBareRemotePath(filepath.Join(t.TempDir(), "repo.git"), []string{base}) {
		t.Fatal("path outside configured base dir should be rejected")
	}
	if safeLocalBareRemotePath(filepath.Join(base, "repo.git"), []string{string(filepath.Separator)}) {
		t.Fatal("root base dir should be rejected")
	}
}

func TestSafeResolvedLocalBareRemotePathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if safeResolvedLocalBareRemotePath(filepath.Join(link, "repo.git"), []string{base}) {
		t.Fatal("resolved path outside base should be rejected")
	}
	if err := os.MkdirAll(filepath.Join(base, "repos"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !safeResolvedLocalBareRemotePath(filepath.Join(base, "repos", "repo.git"), []string{base}) {
		t.Fatal("resolved path inside base should be accepted")
	}
}

func TestProvisionTemplateRepositoryCreatesLocalBareRepoAndPushesFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary is required for template repository provisioning test")
	}
	root := t.TempDir()
	remotePath := filepath.Join(root, "repos", "billing.git")
	executor := &GitExecutor{WorkDir: filepath.Join(root, "work"), LocalBareBaseDirs: []string{filepath.Join(root, "repos")}}
	result, err := executor.ProvisionTemplateRepository(context.Background(),
		map[string]any{"id": "repo-1", "repo_key": "billing-service", "default_branch": "main"},
		[]map[string]any{{"id": "remote-1", "provider_type": "local_bare", "remote_url": remotePath}},
		[]map[string]any{
			{"path": "README.md", "content": "# Billing\n"},
			{"path": "docs/context.md", "content": "asset graph\n"},
		},
	)
	if err != nil {
		t.Fatalf("ProvisionTemplateRepository: %v\nstdout=%s\nstderr=%s", err, result.Stdout, result.Stderr)
	}
	if result.AfterSHA == "" {
		t.Fatal("AfterSHA should be populated")
	}
	if result.Details["provisioned"] != true {
		t.Fatalf("provisioned = %v, want true", result.Details["provisioned"])
	}
	cmd := exec.Command("git", "--git-dir", remotePath, "show", "refs/heads/main:README.md")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git show pushed README: %v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "# Billing" {
		t.Fatalf("README content = %q", out)
	}
}

func TestProvisionTemplateRepositoryIdempotentWhenBareRepoExists(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary is required for template repository provisioning test")
	}
	root := t.TempDir()
	remotePath := filepath.Join(root, "repos", "billing.git")
	executor := &GitExecutor{WorkDir: filepath.Join(root, "work"), LocalBareBaseDirs: []string{filepath.Join(root, "repos")}}
	repo := map[string]any{"id": "repo-1", "repo_key": "billing-service", "default_branch": "main"}
	remotes := []map[string]any{{"id": "remote-1", "provider_type": "local_bare", "remote_url": remotePath}}
	files := []map[string]any{{"path": "README.md", "content": "# Billing\n"}}
	first, err := executor.ProvisionTemplateRepository(context.Background(), repo, remotes, files)
	if err != nil {
		t.Fatalf("first ProvisionTemplateRepository: %v", err)
	}
	second, err := executor.ProvisionTemplateRepository(context.Background(), repo, remotes, []map[string]any{{"path": "README.md", "content": "# Changed\n"}})
	if err != nil {
		t.Fatalf("second ProvisionTemplateRepository: %v", err)
	}
	if first.AfterSHA == "" || second.AfterSHA != first.AfterSHA {
		t.Fatalf("second SHA = %q, want first SHA %q", second.AfterSHA, first.AfterSHA)
	}
	if second.Details["already_provisioned"] != true {
		t.Fatalf("already_provisioned = %v, want true", second.Details["already_provisioned"])
	}
	cmd := exec.Command("git", "--git-dir", remotePath, "show", "refs/heads/main:README.md")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git show pushed README: %v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "# Billing" {
		t.Fatalf("README content changed on idempotent provisioning: %q", out)
	}
}

func TestProvisionTemplateRepositorySkipsWhenNoLocalBareRemote(t *testing.T) {
	result, err := (&GitExecutor{}).ProvisionTemplateRepository(context.Background(),
		map[string]any{"id": "repo-1", "repo_key": "billing-service", "default_branch": "main"},
		[]map[string]any{{"id": "remote-1", "provider_type": "github", "remote_url": "git@example.com:org/repo.git"}},
		nil,
	)
	if err != nil {
		t.Fatalf("ProvisionTemplateRepository: %v", err)
	}
	if result.Details["provisioned"] != false {
		t.Fatalf("provisioned = %v, want false", result.Details["provisioned"])
	}
	if result.Details["reason"] == "" {
		t.Fatal("skip result should include a reason")
	}
}

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

func TestTemplateProviderReviewExecutionPlanUsesProviderTerms(t *testing.T) {
	githubPlan := templateProviderReviewExecutionPlan("github", map[string]any{
		"mode":            "pull_request",
		"provider_type":   "github",
		"proposed_branch": "assops/template/demo-main",
		"target_branch":   "main",
	})
	if githubPlan["review_kind"] != "pull_request" || githubPlan["provider_api_mutation"] != "disabled" {
		t.Fatalf("github execution plan = %#v", githubPlan)
	}
	githubRequest := mapFromAny(githubPlan["execution_request"])
	if githubRequest["status"] != "approval_ready" || githubRequest["review_kind"] != "pull_request" {
		t.Fatalf("github execution request = %#v", githubRequest)
	}
	if _, ok := githubRequest["blocked_reason"]; ok {
		t.Fatalf("approval-ready execution request should not include blocked_reason: %#v", githubRequest)
	}
	giteaPlan := templateProviderReviewExecutionPlan("gitea", map[string]any{
		"mode":            "merge_request",
		"provider_type":   "gitea",
		"proposed_branch": "assops/template/demo-main",
		"target_branch":   "main",
	})
	if giteaPlan["review_kind"] != "merge_request" || giteaPlan["execution_enabled"] != false {
		t.Fatalf("gitea execution plan = %#v", giteaPlan)
	}
	for _, tt := range []struct {
		name   string
		source string
		target string
	}{
		{name: "missing source", source: "", target: "main"},
		{name: "missing target", source: "assops/template/demo-main", target: ""},
		{name: "missing both", source: "", target: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			blockedRequest := templateProviderReviewExecutionRequest("github", "pull_request", tt.source, tt.target)
			if blockedRequest["status"] != "blocked" || strings.TrimSpace(fmt.Sprint(blockedRequest["blocked_reason"])) == "" {
				t.Fatalf("blocked execution request = %#v", blockedRequest)
			}
		})
	}
	encoded, _ := json.Marshal(githubPlan)
	for _, leak := range []string{"ASSOPS_TEMPLATE_PROVIDER_TOKEN", "secret-token", "api_base_url"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("provider review execution plan leaked %q: %s", leak, encoded)
		}
	}
}

func TestTemplateProtectedBranchStrategyRejectsUnsafeProposedBranch(t *testing.T) {
	strategy := templateProtectedBranchStrategy(
		map[string]any{"repo_key": "Billing API", "default_branch": "main"},
		map[string]any{
			"provider_type": "github",
			"metadata": map[string]any{
				"branch_strategy": "proposed_branch",
				"proposed_branch": "../unsafe",
			},
		},
		"main",
	)
	if strategy["proposed_branch"] != "assops/template/billing-api-main" {
		t.Fatalf("unsafe proposed branch should fall back to safe generated branch: %#v", strategy)
	}
}

func TestTemplateBranchStrategyActionRequiredUsesProviderTerms(t *testing.T) {
	pr := templateBranchStrategyActionRequired(map[string]any{
		"mode":            "pull_request",
		"provider_type":   "github",
		"proposed_branch": "assops/template/demo-main",
	}, "main")
	if !strings.Contains(pr, "GitHub pull request") {
		t.Fatalf("pull request action = %q", pr)
	}
	mr := templateBranchStrategyActionRequired(map[string]any{
		"mode":            "merge_request",
		"provider_type":   "gitlab",
		"proposed_branch": "assops/template/demo-main",
	}, "main")
	if !strings.Contains(mr, "merge request") {
		t.Fatalf("merge request action = %q", mr)
	}
}

func TestProvisionTemplateRepositorySkipsExternalProviderWithoutToken(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	result, err := (&GitExecutor{HTTPClient: server.Client()}).ProvisionTemplateRepository(context.Background(),
		map[string]any{"id": "repo-1", "repo_key": "billing-service", "default_branch": "main"},
		[]map[string]any{{
			"id":            "remote-1",
			"provider_type": "gitea",
			"metadata": map[string]any{
				"api_base_url": server.URL + "/api/v1",
				"owner":        "acme",
				"token_env":    "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_MISSING",
			},
		}},
		nil,
	)
	if err != nil {
		t.Fatalf("ProvisionTemplateRepository: %v", err)
	}
	if called {
		t.Fatal("provider should not be called without token")
	}
	if result.Details["provisioned"] != false {
		t.Fatalf("provisioned = %v, want false", result.Details["provisioned"])
	}
	if result.Details["reason"] != "external template provider token is not configured" {
		t.Fatalf("reason = %v", result.Details["reason"])
	}
	reconcile := mapFromAny(result.Details["repository_reconciliation"])
	if reconcile["kind"] != "missing_token" || reconcile["guardrail"] != "provider_token_missing" {
		t.Fatalf("reconciliation = %#v", reconcile)
	}
	encoded, _ := json.Marshal(reconcile)
	if strings.Contains(string(encoded), "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITEA_MISSING") {
		t.Fatalf("reconciliation leaked token env: %s", encoded)
	}
}

func TestBuildExternalTemplateProviderSpecRejectsUnsafeTokenEnv(t *testing.T) {
	_, ok := buildExternalTemplateProviderSpec(
		map[string]any{"repo_key": "billing-service"},
		map[string]any{
			"provider_type": "github",
			"metadata": map[string]any{
				"api_base_url": "https://api.github.com",
				"token_env":    "DATABASE_URL",
			},
		},
	)
	if ok {
		t.Fatal("unsafe token_env should be rejected")
	}
}

func TestBuildExternalTemplateProviderSpecUsesProviderAccountTokenEnv(t *testing.T) {
	t.Setenv("ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_ACCOUNT", "account-token")
	t.Setenv("ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_INLINE", "inline-token")
	spec, ok := buildExternalTemplateProviderSpec(
		map[string]any{"repo_key": "billing-service"},
		map[string]any{
			"provider_type":     "github",
			"source_account_id": "account-1",
			"metadata": map[string]any{
				"api_base_url":        "https://api.github.com",
				"provider_account_id": "account-1",
				"token_env":           "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_ACCOUNT",
				"owner":               "acme",
				"visibility":          "public",
			},
		},
	)
	if !ok {
		t.Fatal("provider account metadata should build a provider spec")
	}
	if spec.TokenEnv != "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_ACCOUNT" || spec.Token != "account-token" {
		t.Fatalf("token env/token = %q/%q, want account env/token", spec.TokenEnv, spec.Token)
	}
	if spec.Private {
		t.Fatal("public visibility should create a non-private repository")
	}
}

func TestBuildExternalTemplateProviderSpecRejectsProviderAccountWithoutTokenEnv(t *testing.T) {
	_, ok := buildExternalTemplateProviderSpec(
		map[string]any{"repo_key": "billing-service"},
		map[string]any{
			"provider_type":     "github",
			"source_account_id": "account-1",
			"metadata": map[string]any{
				"api_base_url":        "https://api.github.com",
				"provider_account_id": "account-1",
				"owner":               "acme",
			},
		},
	)
	if ok {
		t.Fatal("provider account metadata without token_env should not build a provider spec")
	}
}

func TestProvisionTemplateRepositoryRejectsPrivateProviderAPI(t *testing.T) {
	t.Setenv("ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST", "secret-token")
	_, err := (&GitExecutor{}).ProvisionTemplateRepository(context.Background(),
		map[string]any{"id": "repo-1", "repo_key": "billing-service", "default_branch": "main"},
		[]map[string]any{{
			"id":            "remote-1",
			"provider_type": "github",
			"metadata": map[string]any{
				"api_base_url": "http://127.0.0.1:1",
				"owner":        "acme",
				"token_env":    "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST",
			},
		}},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "non-public address") {
		t.Fatalf("error = %v, want non-public address rejection", err)
	}
}

func TestProvisionTemplateRepositoryProviderErrorDoesNotPersistBody(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST", "secret-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"provider rejected request"}`))
	}))
	defer server.Close()

	result, err := (&GitExecutor{HTTPClient: server.Client()}).ProvisionTemplateRepository(context.Background(),
		map[string]any{"id": "repo-1", "repo_key": "billing-service", "default_branch": "main"},
		[]map[string]any{{
			"id":            "remote-1",
			"provider_type": "github",
			"metadata": map[string]any{
				"api_base_url": server.URL,
				"owner":        "acme",
				"token_env":    "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST",
			},
		}},
		nil,
	)
	if err == nil {
		t.Fatal("expected provider error")
	}
	if strings.Contains(err.Error(), "{") || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error should not include raw body or token: %v", err)
	}
	if !strings.Contains(err.Error(), "provider rejected request") {
		t.Fatalf("error should include sanitized provider message: %v", err)
	}
	if result == nil || result.Details == nil {
		t.Fatal("provider error should return diagnostic details")
	}
	if result.Details["provider_status"] != http.StatusInternalServerError {
		t.Fatalf("provider_status = %v", result.Details["provider_status"])
	}
	if result.Details["provider_error"] != "provider rejected request" {
		t.Fatalf("provider_error = %v", result.Details["provider_error"])
	}
	encoded, _ := json.Marshal(result.Details)
	if strings.Contains(string(encoded), "secret-token") {
		t.Fatalf("details leaked token: %s", encoded)
	}
}

func TestTemplateFileContentTreatsNilAsEmpty(t *testing.T) {
	if got := templateFileContent(map[string]any{"content": nil}); got != "" {
		t.Fatalf("templateFileContent(nil) = %q, want empty", got)
	}
	if got := templateFileContent(map[string]any{"content": "hello"}); got != "hello" {
		t.Fatalf("templateFileContent = %q, want hello", got)
	}
}

func TestTemplateProviderAlreadyExistsParsesStructuredErrors(t *testing.T) {
	body := []byte(`{"errors":[{"message":"name already exists"}]}`)
	if !templateProviderAlreadyExists(http.StatusUnprocessableEntity, body) {
		t.Fatal("structured already-exists error should be accepted")
	}
	body = []byte(`{"message":"schema mentions already_exists but failed for another reason"}`)
	if templateProviderAlreadyExists(http.StatusUnprocessableEntity, body) {
		t.Fatal("free-form mention should not be treated as already exists")
	}
	if !templateProviderAlreadyExists(http.StatusConflict, []byte(`not json`)) {
		t.Fatal("409 conflict should be treated as already exists")
	}
}

func TestProvisionTemplateRepositoryAlreadyExistsIncludesDiagnostics(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST", "secret-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"errors":[{"message":"name already exists"}]}`))
	}))
	defer server.Close()

	result, err := (&GitExecutor{HTTPClient: server.Client()}).ProvisionTemplateRepository(context.Background(),
		map[string]any{"id": "repo-1", "repo_key": "billing-service", "default_branch": "main"},
		[]map[string]any{{
			"id":            "remote-1",
			"provider_type": "github",
			"remote_url":    "git@github.com:acme/billing-service.git",
			"metadata": map[string]any{
				"api_base_url": server.URL,
				"owner":        "acme",
				"token_env":    "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST",
			},
		}},
		nil,
	)
	if err != nil {
		t.Fatalf("already exists should not fail: %v", err)
	}
	if result == nil || result.Details == nil {
		t.Fatal("expected diagnostics")
	}
	if result.Details["already_provisioned"] != true || result.Details["provider_status"] != http.StatusUnprocessableEntity {
		t.Fatalf("details = %#v", result.Details)
	}
	if result.Details["token_configured"] != true || result.Details["provider_error"] != "already exists" {
		t.Fatalf("diagnostics = %#v", result.Details)
	}
}

func TestProvisionTemplateRepositorySkipsStarterPushWhenExternalRepositoryExists(t *testing.T) {
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST", "secret-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"errors":[{"message":"name already exists"}]}`))
	}))
	defer server.Close()

	result, err := (&GitExecutor{HTTPClient: server.Client()}).ProvisionTemplateRepository(context.Background(),
		map[string]any{"id": "repo-1", "repo_key": "billing-service", "default_branch": "main"},
		[]map[string]any{{
			"id":            "remote-1",
			"provider_type": "github",
			"remote_url":    "git@github.com:acme/billing-service.git",
			"metadata": map[string]any{
				"api_base_url": server.URL,
				"owner":        "acme",
				"token_env":    "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST",
			},
		}},
		[]map[string]any{{"path": "README.md", "content": "# Billing\n"}},
	)
	if err != nil {
		t.Fatalf("already exists should not fail: %v", err)
	}
	if result.Details["provisioned"] != false || result.Details["repository_exists"] != true || result.Details["starter_push_skipped"] != true {
		t.Fatalf("existing repository skip details = %#v", result.Details)
	}
	if !strings.Contains(fmt.Sprint(result.Details["reason"]), "already exists") {
		t.Fatalf("reason = %v", result.Details["reason"])
	}
	reconcile := mapFromAny(result.Details["repository_reconciliation"])
	if reconcile["kind"] != "existing_repository" || reconcile["guardrail"] != "existing_repository_push_blocked" {
		t.Fatalf("reconciliation = %#v", reconcile)
	}
	credentialStrategy := mapFromAny(reconcile["credential_strategy"])
	if credentialStrategy["token_stored"] != false {
		t.Fatalf("credential strategy should not expose token values: %#v", credentialStrategy)
	}
	encoded, _ := json.Marshal(reconcile)
	if strings.Contains(string(encoded), "secret-token") || strings.Contains(string(encoded), "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST") {
		t.Fatalf("reconciliation leaked token material: %s", encoded)
	}
}

func TestTemplateRepositoryReconciliationUnknownKind(t *testing.T) {
	got := templateRepositoryReconciliation(
		"provider_policy",
		map[string]any{"repo_key": "billing-service"},
		map[string]any{"id": "remote-1", "provider_type": "github"},
		"main",
		2,
	)
	if got["kind"] != "provider_policy" || got["guardrail"] != "manual_reconciliation_required" {
		t.Fatalf("reconciliation = %#v", got)
	}
	if got["provider_type"] != "github" || got["repository_key"] != "billing-service" {
		t.Fatalf("reconciliation identifiers = %#v", got)
	}
	credentialStrategy := mapFromAny(got["credential_strategy"])
	if credentialStrategy["token_stored"] != false {
		t.Fatalf("credential strategy should not store token values: %#v", credentialStrategy)
	}
}

func TestProvisionTemplateRepositoryAllowsStarterPushWhenExistingRepositoryOptedIn(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary is required for template repository provisioning test")
	}
	t.Setenv("ASSOPS_ALLOW_LOCAL_TEMPLATE_PROVIDER_API", "true")
	t.Setenv("ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST", "secret-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"errors":[{"message":"name already exists"}]}`))
	}))
	defer server.Close()
	root := t.TempDir()
	remotePath := filepath.Join(root, "repos", "billing.git")
	if err := os.MkdirAll(filepath.Dir(remotePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "--bare", remotePath).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}

	result, err := (&GitExecutor{HTTPClient: server.Client(), WorkDir: filepath.Join(root, "work")}).ProvisionTemplateRepository(context.Background(),
		map[string]any{"id": "repo-1", "repo_key": "billing-service", "default_branch": "main"},
		[]map[string]any{{
			"id":            "remote-1",
			"provider_type": "github",
			"remote_url":    remotePath,
			"metadata": map[string]any{
				"api_base_url":                   server.URL,
				"owner":                          "acme",
				"token_env":                      "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_TEST",
				"allow_existing_repository_push": true,
			},
		}},
		[]map[string]any{{"path": "README.md", "content": "# Billing\n"}},
	)
	if err != nil {
		t.Fatalf("existing repository opt-in should push starter files: %v\nstdout=%s\nstderr=%s", err, result.Stdout, result.Stderr)
	}
	if result.Details["provisioned"] != true || result.Details["starter_push_skipped"] == true {
		t.Fatalf("existing repository opt-in details = %#v", result.Details)
	}
	out, err := exec.Command("git", "--git-dir", remotePath, "show", "refs/heads/main:README.md").CombinedOutput()
	if err != nil {
		t.Fatalf("git show pushed README: %v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "# Billing" {
		t.Fatalf("README content = %q", out)
	}
}

func TestProviderErrorSuffixTruncatesLongMessages(t *testing.T) {
	longMessage := strings.Repeat("x", providerDiagnosticErrorLimit+20)
	suffix := providerErrorSuffix([]byte(`{"message":"` + longMessage + `"}`))
	if !strings.HasSuffix(suffix, "...") {
		t.Fatalf("suffix should be truncated: %q", suffix)
	}
	if len(strings.TrimPrefix(suffix, ": ")) != providerDiagnosticErrorLimit+3 {
		t.Fatalf("suffix length = %d", len(strings.TrimPrefix(suffix, ": ")))
	}
}

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

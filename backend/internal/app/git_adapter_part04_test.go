package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderReviewAdapterRehearsalReadinessVariants(t *testing.T) {
	t.Run("mixed operation readiness", func(t *testing.T) {
		apiPlan := map[string]any{
			"status":        "ready",
			"source_branch": "assops/template/demo-main",
			"target_branch": "main",
			"file_count":    0,
		}
		envelopes := providerReviewAdapterRequestEnvelopes("github", "pull_request", apiPlan, map[string]any{})
		rehearsal := providerReviewAdapterRehearsal("github", "pull_request", "planned", map[string]any{
			"token_env_configured": true,
			"token_env_present":    true,
		}, envelopes)
		if rehearsal["status"] != "blocked" ||
			rehearsal["operation_count"] != 3 ||
			rehearsal["ready_operation_count"] != 2 ||
			rehearsal["blocked_operation_count"] != 1 ||
			rehearsal["mutation_arming_candidate"] != false {
			t.Fatalf("mixed readiness rehearsal = %#v", rehearsal)
		}
		reasons := stringSliceFromAny(rehearsal["blocked_reasons"])
		if !containsString(reasons, "starter_file_payload_staged") ||
			containsString(reasons, "provider_credential_configured") ||
			containsString(reasons, "provider_token_env_present") {
			t.Fatalf("mixed readiness reasons = %#v", reasons)
		}
	})
	t.Run("credential only blocking", func(t *testing.T) {
		apiPlan := templateProviderReviewAPIRequestPlan("github", "pull_request", "assops/template/demo-main", "main", map[string]any{
			"status":           "ready",
			"file_count":       1,
			"content_included": false,
		})
		envelopes := providerReviewAdapterRequestEnvelopes("github", "pull_request", apiPlan, map[string]any{
			"status":           "ready",
			"file_count":       1,
			"content_included": false,
		})
		rehearsal := providerReviewAdapterRehearsal("github", "pull_request", "planned", map[string]any{}, envelopes)
		if rehearsal["status"] != "blocked" ||
			rehearsal["operation_count"] != 3 ||
			rehearsal["ready_operation_count"] != 3 ||
			rehearsal["blocked_operation_count"] != 0 ||
			rehearsal["mutation_arming_candidate"] != false {
			t.Fatalf("credential-only blocking rehearsal = %#v", rehearsal)
		}
		reasons := stringSliceFromAny(rehearsal["blocked_reasons"])
		if !containsString(reasons, "provider_credential_configured") ||
			!containsString(reasons, "provider_token_env_present") ||
			containsString(reasons, "starter_file_payload_staged") {
			t.Fatalf("credential-only reasons = %#v", reasons)
		}
	})
}

func TestProviderReviewAdapterBuilderAndHandlerNamesUseSharedContract(t *testing.T) {
	for _, tt := range []struct {
		operation string
		builder   string
		handler   string
	}{
		{
			operation: "create_branch_ref",
			builder:   "build_redacted_branch_ref_request",
			handler:   "handle_branch_ref_response",
		},
		{
			operation: "commit_starter_files",
			builder:   "build_redacted_file_batch_request",
			handler:   "handle_commit_files_response",
		},
		{
			operation: "open_review_request",
			builder:   "build_redacted_review_request",
			handler:   "handle_review_request_response",
		},
		{
			operation: "raw_operation",
			builder:   "build_redacted_provider_request",
			handler:   "handle_provider_response",
		},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			if got := providerReviewPayloadBuilderName(tt.operation); got != tt.builder {
				t.Fatalf("providerReviewPayloadBuilderName(%q) = %q, want %q", tt.operation, got, tt.builder)
			}
			if got := providerReviewResponseHandlerName(tt.operation); got != tt.handler {
				t.Fatalf("providerReviewResponseHandlerName(%q) = %q, want %q", tt.operation, got, tt.handler)
			}
		})
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
	credential := templateProviderReviewCredentialStrategy("github", map[string]any{
		"provider_type":     "github",
		"source_account_id": "account-1",
		"metadata": map[string]any{
			"provider_account_id": "account-1",
			"token_env":           "ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_ACCOUNT",
		},
	})
	if credential["mode"] != "provider_account_token_env" ||
		credential["provider_account_attached"] != true ||
		credential["token_env_configured"] != true ||
		credential["token_env_present"] != true ||
		credential["token_stored"] != false ||
		credential["external_call_made"] != false {
		t.Fatalf("credential strategy = %#v", credential)
	}
	encoded, _ := json.Marshal(credential)
	for _, leak := range []string{"ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_ACCOUNT", "account-token"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("credential strategy leaked %q: %s", leak, encoded)
		}
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

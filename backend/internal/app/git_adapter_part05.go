package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func (e *GitExecutor) provisionExternalTemplateRepository(ctx context.Context, result *gitExecutionResult, repo, remote map[string]any) error {
	spec, ok := buildExternalTemplateProviderSpec(repo, remote)
	if !ok {
		result.Details["reason"] = "external template remote is missing provider configuration"
		return nil
	}
	if spec.Token == "" {
		result.Details["reason"] = "external template provider token is not configured"
		result.Details["provider_type"] = spec.Provider
		result.Details["token_configured"] = false
		result.Details["repository_reconciliation"] = templateRepositoryReconciliation("missing_token", repo, remote, defaultBranchFromRow(repo), 0)
		return nil
	}
	if err := validateTemplateProviderURL(ctx, spec.CreateURL); err != nil {
		return fmt.Errorf("unsafe %s provider API URL: %w", spec.Provider, err)
	}
	setTemplateProviderDiagnostics(result, spec, 0, "")
	client := e.HTTPClient
	if client == nil {
		client = newTemplateProviderHTTPClient()
	}
	requestBody := map[string]any{
		"name":        spec.RepositoryName,
		"description": spec.Description,
		"private":     spec.Private,
		"auto_init":   false,
	}
	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spec.CreateURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	switch spec.Provider {
	case "github":
		req.Header.Set("Authorization", "Bearer "+spec.Token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	case "gitea":
		req.Header.Set("Authorization", "token "+spec.Token)
		req.Header.Set("Accept", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		setTemplateProviderDiagnostics(result, spec, 0, err.Error())
		return fmt.Errorf("creating %s repository: %w", spec.Provider, err)
	}
	defer res.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if templateProviderAlreadyExists(res.StatusCode, responseBody) {
		setTemplateProviderDiagnostics(result, spec, res.StatusCode, "already exists")
		result.Details["provisioned"] = true
		result.Details["already_provisioned"] = true
		result.Details["provider_type"] = spec.Provider
		result.Details["remote_id"] = remote["id"]
		result.Details["repository_name"] = spec.RepositoryName
		result.Details["owner"] = spec.Owner
		result.Details["remote_url"] = remoteURLFromRow(remote)
		return nil
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		setTemplateProviderDiagnostics(result, spec, res.StatusCode, providerErrorMessage(responseBody))
		return fmt.Errorf("creating %s repository returned %s%s", spec.Provider, res.Status, providerErrorSuffix(responseBody))
	}
	payload := map[string]any{}
	_ = json.Unmarshal(responseBody, &payload)
	remoteURL := firstNonEmptyString(stringFromMap(payload, "ssh_url"), stringFromMap(payload, "clone_url"), remoteURLFromRow(remote))
	result.Details["provisioned"] = true
	result.Details["provider_type"] = spec.Provider
	result.Details["remote_id"] = remote["id"]
	result.Details["repository_name"] = spec.RepositoryName
	result.Details["owner"] = spec.Owner
	result.Details["remote_url"] = remoteURL
	result.Details["web_url"] = firstNonEmptyString(stringFromMap(payload, "html_url"), stringFromMap(payload, "web_url"))
	return nil
}

func setTemplateProviderDiagnostics(result *gitExecutionResult, spec externalTemplateProviderConfig, status int, message string) {
	if result == nil {
		return
	}
	if result.Details == nil {
		result.Details = map[string]any{}
	}
	result.Details["provider_type"] = spec.Provider
	result.Details["repository_name"] = spec.RepositoryName
	result.Details["owner"] = spec.Owner
	result.Details["token_configured"] = spec.Token != ""
	if status > 0 {
		result.Details["provider_status"] = status
	}
	if message = truncateProviderError(message, providerDiagnosticErrorLimit); message != "" {
		result.Details["provider_error"] = message
	}
}

func templateRepositoryReconciliation(kind string, repo, remote map[string]any, defaultBranch string, fileCount int) map[string]any {
	provider := strings.ToLower(strings.TrimSpace(firstNonEmptyString(
		stringFromMap(remote, "provider_type"),
		stringFromMap(remote, "kind"),
	)))
	branchStrategy := templateProtectedBranchStrategy(repo, remote, defaultBranch)
	credentialStrategy := templateProviderReviewCredentialStrategy(provider, remote)
	reviewReadiness := templateProviderReviewReadiness(kind, provider, branchStrategy, credentialStrategy)
	summary := map[string]any{
		"kind":                      kind,
		"provider_type":             provider,
		"remote_id":                 remote["id"],
		"repository_key":            repo["repo_key"],
		"default_branch":            defaultBranch,
		"file_count":                fileCount,
		"starter_push_state":        "skipped",
		"credential_strategy":       credentialStrategy,
		"provider_review_readiness": reviewReadiness,
	}
	switch kind {
	case "existing_repository":
		summary["guardrail"] = "existing_repository_push_blocked"
		summary["action_required"] = "Review the existing repository contents before allowing ASSOPS to push starter files."
		summary["retry_after"] = "Set allow_existing_repository_push only after the repository is confirmed safe to overwrite or extend."
	case "protected_branch":
		summary["guardrail"] = "protected_branch_push_blocked"
		summary["action_required"] = "Review provider branch protection and choose a provider-specific reconciliation path."
		summary["retry_after"] = "Configure a branch strategy or set allow_protected_branch_push only after branch protection is approved."
		if stringFromMap(branchStrategy, "strategy_status") == "planned" {
			summary["branch_strategy"] = branchStrategy
			summary["action_required"] = templateBranchStrategyActionRequired(branchStrategy, defaultBranch)
			summary["retry_after"] = "Retry after the proposed branch is reviewed and merged, or enable allow_protected_branch_push after approval."
		} else if len(branchStrategy) > 0 {
			summary["branch_strategy"] = branchStrategy
		}
	case "missing_token":
		summary["guardrail"] = "provider_token_missing"
		summary["action_required"] = "Rotate the provider account to a configured token environment and run the provider health check."
		summary["retry_after"] = "Retry after the provider account check succeeds."
	default:
		summary["guardrail"] = "manual_reconciliation_required"
		summary["action_required"] = "Review template remote metadata before retrying repository provisioning."
		summary["retry_after"] = "Retry after the missing provider condition is fixed."
	}
	return summary
}

func templateProviderReviewCredentialStrategy(provider string, remote map[string]any) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	metadata := mapFromAny(remote["metadata"])
	tokenEnv := firstNonEmptyString(stringFromMap(metadata, "token_env"), stringFromMap(metadata, "provider_account_env"), defaultTemplateProviderTokenEnv(provider))
	tokenEnvConfigured := strings.TrimSpace(tokenEnv) != "" && safeTemplateProviderTokenEnv(provider, tokenEnv)
	return map[string]any{
		"mode":                      map[bool]string{true: "provider_account_token_env", false: "template_remote_token_env"}[templateRemoteUsesProviderAccount(remote, metadata)],
		"provider_account_attached": templateRemoteUsesProviderAccount(remote, metadata),
		"token_env_configured":      tokenEnvConfigured,
		"token_env_present":         tokenEnvConfigured && strings.TrimSpace(os.Getenv(tokenEnv)) != "",
		"token_stored":              false,
		"external_call_made":        false,
	}
}

func templateProviderReviewReadiness(kind, provider string, branchStrategy map[string]any, credentialStrategies ...map[string]any) map[string]any {
	credentialStrategy := firstProviderReviewCredentialStrategy(credentialStrategies...)
	readiness := map[string]any{
		"status":             "blocked",
		"provider_type":      provider,
		"execution_enabled":  false,
		"external_call_made": false,
		"branch_creation":    "disabled",
		"review_request":     "disabled",
		"message":            "Provider branch and review execution are disabled in this first version.",
	}
	switch kind {
	case "protected_branch":
		if stringFromMap(branchStrategy, "strategy_status") == "planned" {
			readiness["status"] = "planned"
			readiness["mode"] = branchStrategy["mode"]
			readiness["proposed_branch"] = branchStrategy["proposed_branch"]
			readiness["target_branch"] = branchStrategy["target_branch"]
			readiness["branch_creation"] = "locally_planned"
			readiness["review_request"] = "locally_planned"
			readiness["provider_next_action"] = branchStrategy["provider_next_action"]
			readiness["execution_plan"] = templateProviderReviewExecutionPlan(provider, branchStrategy, credentialStrategy)
			readiness["message"] = "Local branch/review plan is ready; provider API-backed branch creation and PR/MR execution remain disabled."
			return readiness
		}
		readiness["message"] = "Configure a supported branch strategy before provider review execution can be planned."
	case "existing_repository":
		readiness["message"] = "Review existing repository contents before planning provider branch/review execution."
	case "missing_token":
		readiness["message"] = "Provider token readiness is blocked; rotate and health-check the provider account before review execution."
	default:
		readiness["message"] = "Manual repository reconciliation is required before provider review execution can be planned."
	}
	return readiness
}

const templateProviderReviewExecuteApprovalAction = "project_template.provider_review.execute"

func templateProviderReviewExecutionPlan(provider string, branchStrategy map[string]any, credentialStrategies ...map[string]any) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(firstNonEmptyString(provider, stringFromMap(branchStrategy, "provider_type"))))
	credentialStrategy := firstProviderReviewCredentialStrategy(credentialStrategies...)
	mode := strings.ToLower(strings.TrimSpace(fmt.Sprint(branchStrategy["mode"])))
	sourceBranch := strings.TrimSpace(fmt.Sprint(branchStrategy["proposed_branch"]))
	targetBranch := strings.TrimSpace(fmt.Sprint(branchStrategy["target_branch"]))
	reviewKind := templateProviderReviewKind(provider, mode)
	executionRequest := templateProviderReviewExecutionRequest(provider, reviewKind, sourceBranch, targetBranch)
	guardrail := templateProviderReviewExecutionGuardrail(provider, reviewKind, sourceBranch, targetBranch, false)
	// Starter files are staged later when the approval payload is built.
	apiRequestPlan := templateProviderReviewAPIRequestPlan(provider, reviewKind, sourceBranch, targetBranch, nil)
	reconciliation := templateProviderReviewExecutionReconciliation(provider, reviewKind, nil, guardrail, apiRequestPlan, credentialStrategy)
	targetSummary := providerReviewExecutionTargetSummary(provider, reviewKind, apiRequestPlan, nil, reconciliation)
	steps := []map[string]any{
		{
			"name":      "create_branch",
			"status":    "planned",
			"provider":  provider,
			"from":      targetBranch,
			"to":        sourceBranch,
			"api_call":  false,
			"guardrail": "provider API execution disabled",
		},
		{
			"name":       "commit_starter_files",
			"status":     "planned",
			"branch":     sourceBranch,
			"api_call":   false,
			"repository": "external provider repository",
			"guardrail":  "external repository mutation disabled",
		},
		{
			"name":          "open_review",
			"status":        "planned",
			"provider":      provider,
			"review_kind":   reviewKind,
			"source_branch": sourceBranch,
			"target_branch": targetBranch,
			"api_call":      false,
			"guardrail":     "provider review request execution disabled",
		},
	}
	return map[string]any{
		"mode":                           "dry_run",
		"provider_type":                  provider,
		"strategy_mode":                  mode,
		"review_kind":                    reviewKind,
		"source_branch":                  sourceBranch,
		"target_branch":                  targetBranch,
		"execution_enabled":              false,
		"external_call_made":             false,
		"requires_approval":              true,
		"approval_action":                templateProviderReviewExecuteApprovalAction,
		"provider_api_mutation":          "disabled",
		"execution_request":              executionRequest,
		"execution_guardrail":            guardrail,
		"credential_strategy":            credentialStrategy,
		"provider_api_request_plan":      apiRequestPlan,
		"provider_review_reconciliation": reconciliation,
		"provider_review_target_summary": targetSummary,
		"steps":                          steps,
		"message":                        "Provider review execution request is prepared for approval, but branch creation, starter-file commits, and PR/MR creation remain disabled.",
	}
}

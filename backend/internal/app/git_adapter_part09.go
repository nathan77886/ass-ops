package app

import (
	"strings"
)

func readyStatus(ready bool) string {
	if ready {
		return "ready"
	}
	return "blocked"
}

func providerReviewAdapterResponseDiagnostics(provider, reviewKind string) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	return map[string]any{
		"status":                 "pending",
		"mode":                   "redacted_response_diagnostics",
		"provider_type":          provider,
		"review_kind":            reviewKind,
		"adapter_status":         providerReviewAdapterStatus(provider, reviewKind),
		"external_call_made":     false,
		"provider_api_call_made": false,
		"provider_api_mutation":  "disabled",
		"response_body_included": false,
		"headers_included":       false,
		"contains_token":         false,
		"contains_provider_url":  false,
		"diagnostic_fields": []string{
			"status_code_class",
			"provider_request_id_present",
			"rate_limit_state",
			"retryable",
			"sanitized_error_code",
		},
		"operations": providerReviewAdapterResponseDiagnosticOperations(provider, reviewKind),
	}
}

func providerReviewAdapterResponseDiagnosticOperations(provider, reviewKind string) []map[string]any {
	return []map[string]any{
		providerReviewAdapterResponseDiagnosticOperation(provider, "create_branch_ref", "create_branch_ref", "2xx_or_already_exists"),
		providerReviewAdapterResponseDiagnosticOperation(provider, "commit_starter_files", "commit_files", "2xx"),
		providerReviewAdapterResponseDiagnosticOperation(provider, "open_review_request", "open_review", "2xx_or_already_exists"),
	}
}

func providerReviewAdapterResponseDiagnosticOperation(provider, name, endpointOperation, successClass string) map[string]any {
	return map[string]any{
		"name":                     name,
		"endpoint_key":             providerReviewEndpointKey(provider, endpointOperation),
		"status":                   "pending",
		"success_status_class":     successClass,
		"retryable_status_classes": []string{"429", "5xx"},
		"response_body_included":   false,
		"headers_included":         false,
		"contains_token":           false,
		"contains_provider_url":    false,
		"external_call_made":       false,
		"provider_api_mutation":    "disabled",
	}
}

func providerReviewAdapterIdempotencyPlan(provider, reviewKind string) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	return map[string]any{
		"status":                     "planned",
		"mode":                       "redacted_idempotency_plan",
		"provider_type":              provider,
		"review_kind":                reviewKind,
		"adapter_status":             providerReviewAdapterStatus(provider, reviewKind),
		"external_call_made":         false,
		"provider_api_call_made":     false,
		"provider_api_mutation":      "disabled",
		"contains_token":             false,
		"contains_provider_url":      false,
		"contains_repository_ref":    false,
		"contains_branch_name":       false,
		"contains_file_content":      false,
		"idempotency_key_included":   false,
		"idempotency_key_material":   "redacted_required_material_only",
		"requires_persisted_attempt": true,
		"retry_after_diagnostics":    true,
		"operations":                 providerReviewAdapterIdempotencyOperations(provider),
	}
}

func providerReviewAdapterIdempotencyOperations(provider string) []map[string]any {
	return []map[string]any{
		providerReviewAdapterIdempotencyOperation(
			provider,
			"create_branch_ref",
			"create_branch_ref",
			"detect_existing_branch_ref",
			"treat_existing_matching_ref_as_success",
		),
		providerReviewAdapterIdempotencyOperation(
			provider,
			"commit_starter_files",
			"commit_files",
			"detect_existing_commit_batch",
			"block_on_content_or_parent_conflict",
		),
		providerReviewAdapterIdempotencyOperation(
			provider,
			"open_review_request",
			"open_review",
			"detect_existing_open_review",
			"reuse_existing_review_request",
		),
	}
}

func providerReviewAdapterIdempotencyOperation(provider, name, endpointOperation, replayCheck, conflictPolicy string) map[string]any {
	return map[string]any{
		"name":                          name,
		"endpoint_key":                  providerReviewEndpointKey(provider, endpointOperation),
		"status":                        "planned",
		"idempotency_key_kind":          "operation_scope_hash",
		"idempotency_key_included":      false,
		"idempotency_key_material":      "redacted_required_material_only",
		"replay_check":                  replayCheck,
		"conflict_policy":               conflictPolicy,
		"retry_policy":                  "retry_only_after_response_diagnostics",
		"requires_persisted_attempt":    true,
		"contains_token":                false,
		"contains_provider_url":         false,
		"contains_repository_ref":       false,
		"contains_branch_name":          false,
		"contains_file_content":         false,
		"external_call_made":            false,
		"provider_api_mutation":         "disabled",
		"provider_api_call_made":        false,
		"response_diagnostics_required": true,
	}
}

func providerReviewAdapterContractOperations(provider, reviewKind string) []map[string]any {
	adapterStatus := providerReviewAdapterStatus(provider, reviewKind)
	scope := "contents:write"
	reviewScope := "pull_requests:write"
	if provider == "gitea" {
		scope = "repository:write"
		reviewScope = "repository:write"
	}
	return []map[string]any{
		{
			"name":                  "create_branch_ref",
			"endpoint_key":          providerReviewEndpointKey(provider, "create_branch_ref"),
			"required_capability":   "branch_ref_write",
			"required_scope":        scope,
			"payload_shape":         "ref_from_target_branch",
			"adapter_status":        adapterStatus,
			"execution_status":      "blocked",
			"external_call_made":    false,
			"provider_api_mutation": "disabled",
			"payload_redacted":      true,
			"contains_token":        false,
			"contains_file_content": false,
		},
		{
			"name":                  "commit_starter_files",
			"endpoint_key":          providerReviewEndpointKey(provider, "commit_files"),
			"required_capability":   "file_content_write",
			"required_scope":        scope,
			"payload_shape":         "content_redacted_file_batch",
			"adapter_status":        adapterStatus,
			"execution_status":      "blocked",
			"external_call_made":    false,
			"provider_api_mutation": "disabled",
			"payload_redacted":      true,
			"contains_token":        false,
			"contains_file_content": false,
		},
		{
			"name":                  "open_review_request",
			"endpoint_key":          providerReviewEndpointKey(provider, "open_review"),
			"required_capability":   "review_request_write",
			"required_scope":        reviewScope,
			"payload_shape":         reviewKind,
			"adapter_status":        adapterStatus,
			"execution_status":      "blocked",
			"external_call_made":    false,
			"provider_api_mutation": "disabled",
			"payload_redacted":      true,
			"contains_token":        false,
			"contains_file_content": false,
		},
	}
}

func firstProviderReviewCredentialStrategy(items ...map[string]any) map[string]any {
	for _, item := range items {
		if len(item) > 0 {
			return item
		}
	}
	return map[string]any{
		"mode":                      "unknown",
		"provider_account_attached": false,
		"token_env_configured":      false,
		"token_env_present":         false,
		"token_stored":              false,
		"external_call_made":        false,
	}
}

func sanitizedProviderReviewCredentialStrategy(value map[string]any) map[string]any {
	if len(value) == 0 {
		return firstProviderReviewCredentialStrategy()
	}
	return map[string]any{
		"mode":                      cleanOptionalText(stringFromMap(value, "mode")),
		"provider_account_attached": boolOnlyFromAny(value["provider_account_attached"]),
		"token_env_configured":      boolOnlyFromAny(value["token_env_configured"]),
		"token_env_present":         boolOnlyFromAny(value["token_env_present"]),
		"token_stored":              false,
		"external_call_made":        false,
	}
}

func boolOnlyFromAny(value any) bool {
	typed, ok := value.(bool)
	return ok && typed
}

func providerReviewEndpointKey(provider, operation string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch provider {
	case "github":
		return "github." + operation
	case "gitea":
		return "gitea." + operation
	default:
		return "provider." + operation
	}
}

func templateProviderReviewExecutionGuardrail(provider, reviewKind, sourceBranch, targetBranch string, enableRequested bool) map[string]any {
	return templateProviderReviewExecutionGuardrailWithStaging(provider, reviewKind, sourceBranch, targetBranch, enableRequested, false, false)
}

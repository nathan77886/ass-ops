package app

import (
	"fmt"
)

func providerReviewAttemptRequestSummary(operation, executionBlueprintOperation map[string]any) map[string]any {
	return map[string]any{
		"mode":                        "redacted_attempt_request_summary",
		"operation_name":              safeProviderReviewAttemptOperationName(stringFromMap(operation, "name")),
		"endpoint_key":                cleanOptionalText(stringFromMap(operation, "endpoint_key")),
		"payload_builder":             safeProviderReviewPayloadBuilderName(stringFromMap(executionBlueprintOperation, "payload_builder")),
		"response_handler":            safeProviderReviewResponseHandlerName(stringFromMap(executionBlueprintOperation, "response_handler")),
		"execution_status":            safeProviderReviewAdapterExecutionStatus(stringFromMap(executionBlueprintOperation, "execution_status")),
		"request_body_included":       false,
		"headers_included":            false,
		"idempotency_key_kind":        "operation_scope_hash",
		"idempotency_key_included":    false,
		"requires_provider_client":    true,
		"requires_request_builder":    true,
		"requires_response_handler":   true,
		"requires_idempotency_ledger": true,
		"provider_api_call_made":      false,
		"provider_api_mutation":       "disabled",
		"external_call_made":          false,
		"payload_redacted":            true,
		"contains_token":              false,
		"contains_provider_url":       false,
		"contains_repository_ref":     false,
		"contains_branch_name":        false,
		"contains_file_content":       false,
	}
}

func providerReviewAttemptResponseDiagnostics(reconciliation map[string]any, endpointKey string) map[string]any {
	responseDiagnostics := mapFromAny(reconciliation["response_diagnostics"])
	for _, operation := range mapSliceFromAny(responseDiagnostics["operations"]) {
		if cleanOptionalText(stringFromMap(operation, "endpoint_key")) == endpointKey {
			return map[string]any{
				"mode":                     "redacted_attempt_response_diagnostics",
				"endpoint_key":             safeProviderReviewEndpointKey(endpointKey),
				"status":                   safeProviderReviewAttemptResponseStatus(stringFromMap(operation, "status")),
				"success_status_class":     safeProviderReviewStatusClass(stringFromMap(operation, "success_status_class")),
				"retryable_status_classes": safeProviderReviewStatusClasses(stringSliceFromAny(operation["retryable_status_classes"])),
				"response_body_included":   false,
				"headers_included":         false,
				"contains_token":           false,
				"contains_provider_url":    false,
				"provider_api_call_made":   false,
				"provider_api_mutation":    "disabled",
				"external_call_made":       false,
			}
		}
	}
	return map[string]any{
		"mode":                     "redacted_attempt_response_diagnostics",
		"endpoint_key":             safeProviderReviewEndpointKey(endpointKey),
		"status":                   "pending",
		"success_status_class":     "",
		"retryable_status_classes": []string{},
		"response_body_included":   false,
		"headers_included":         false,
		"contains_token":           false,
		"contains_provider_url":    false,
		"provider_api_call_made":   false,
		"provider_api_mutation":    "disabled",
		"external_call_made":       false,
	}
}

func sanitizedProviderReviewAttemptOrchestration(value map[string]any, operations []map[string]any) map[string]any {
	summary := providerReviewAttemptOrchestrationSummary(operations)
	if len(value) > 0 {
		summary["status"] = safeProviderReviewAttemptOrchestrationStatus(stringFromMap(value, "status"))
	}
	return summary
}

func providerReviewAttemptLedgerSummary(attempts []map[string]any) map[string]any {
	operations := make([]map[string]any, 0, len(attempts))
	for _, attempt := range attempts {
		claimedAt := cleanOptionalText(fmt.Sprint(attempt["claimed_at"]))
		if claimedAt == "<nil>" {
			claimedAt = ""
		}
		executedAt := cleanOptionalText(fmt.Sprint(attempt["executed_at"]))
		if executedAt == "<nil>" {
			executedAt = ""
		}
		externalCallMade := boolOnlyFromAny(attempt["external_call_made"])
		providerAPICallMade := boolOnlyFromAny(attempt["provider_api_call_made"])
		providerAPIMutation := safeProviderReviewProviderAPIMutation(stringFromMap(attempt, "provider_api_mutation"))
		responseDiagnostics := sanitizedProviderReviewAttemptResponseDiagnostics(mapFromAny(attempt["response_diagnostics"]))
		responseDiagnostics["external_call_made"] = externalCallMade
		responseDiagnostics["provider_api_call_made"] = providerAPICallMade
		responseDiagnostics["provider_api_mutation"] = providerAPIMutation
		responseDiagnostics["provider_status_class"] = safeProviderReviewStatusClass(stringFromMap(attempt, "provider_status_class"))
		responseDiagnostics["live_execution_phase"] = safeProviderReviewLiveExecutionPhase(stringFromMap(attempt, "live_execution_phase"))
		responseDiagnostics["live_execution_retryable"] = boolOnlyFromAny(attempt["live_execution_retryable"])
		responseDiagnostics["manual_cleanup_hint"] = safeProviderReviewManualCleanupHint(stringFromMap(attempt, "live_execution_manual_cleanup_hint"))
		operations = append(operations, map[string]any{
			"id":                           cleanOptionalID(fmt.Sprint(attempt["id"])),
			"name":                         cleanOptionalText(stringFromMap(attempt, "operation_name")),
			"endpoint_key":                 cleanOptionalText(stringFromMap(attempt, "endpoint_key")),
			"status":                       safeProviderReviewAttemptStatus(stringFromMap(attempt, "status")),
			"replay_check":                 safeProviderReviewReplayCheck(stringFromMap(attempt, "replay_check")),
			"conflict_policy":              safeProviderReviewConflictPolicy(stringFromMap(attempt, "conflict_policy")),
			"retry_policy":                 safeProviderReviewRetryPolicy(stringFromMap(attempt, "retry_policy")),
			"operation_order":              intFromAny(attempt["operation_order"], 0),
			"depends_on_operation":         safeProviderReviewAttemptDependencyName(stringFromMap(attempt, "depends_on_operation")),
			"dependency_status":            safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(attempt, "dependency_status")),
			"request_summary":              sanitizedProviderReviewAttemptRequestSummary(mapFromAny(attempt["request_summary"])),
			"response_diagnostics":         responseDiagnostics,
			"result_recording_plan":        providerReviewAttemptLedgerResultRecordingPlan(attempt),
			"claim_recorded":               providerReviewAttemptClaimRecorded(attempt),
			"claimed_at":                   claimedAt,
			"executed_at":                  executedAt,
			"external_call_made":           externalCallMade,
			"provider_api_call_made":       providerAPICallMade,
			"provider_api_mutation":        providerAPIMutation,
			"provider_status_class":        safeProviderReviewStatusClass(stringFromMap(attempt, "provider_status_class")),
			"provider_review_url":          sanitizeProviderReviewURLForResponse(stringFromMap(attempt, "provider_review_url")),
			"provider_review_url_included": sanitizeProviderReviewURLForResponse(stringFromMap(attempt, "provider_review_url")) != "",
			"live_execution_phase":         safeProviderReviewLiveExecutionPhase(stringFromMap(attempt, "live_execution_phase")),
			"live_execution_retryable":     boolOnlyFromAny(attempt["live_execution_retryable"]),
			"manual_cleanup_hint":          safeProviderReviewManualCleanupHint(stringFromMap(attempt, "live_execution_manual_cleanup_hint")),
			"cleanup_attempted":            boolOnlyFromAny(attempt["cleanup_attempted"]),
			"cleanup_succeeded":            boolOnlyFromAny(attempt["cleanup_succeeded"]),
			"cleanup_required":             boolOnlyFromAny(attempt["cleanup_required"]),
			"idempotency_key_included":     false,
		})
	}
	status := "not_recorded"
	if len(operations) > 0 {
		status = "recorded"
	}
	orchestration := providerReviewAttemptOrchestrationSummary(operations)
	return map[string]any{
		"status":                   status,
		"mode":                     "redacted_attempt_ledger",
		"attempt_count":            len(operations),
		"operations":               operations,
		"orchestration":            orchestration,
		"external_call_made":       providerReviewAttemptAnyBool(operations, "external_call_made"),
		"provider_api_call_made":   providerReviewAttemptAnyBool(operations, "provider_api_call_made"),
		"provider_api_mutation":    providerReviewAttemptLedgerMutation(operations),
		"idempotency_key_included": false,
		"contains_token":           false,
		"contains_provider_url":    providerReviewAttemptAnyBool(operations, "provider_review_url_included"),
		"contains_repository_ref":  false,
		"contains_branch_name":     false,
		"contains_file_content":    false,
	}
}

func providerReviewAttemptAnyBool(operations []map[string]any, key string) bool {
	for _, operation := range operations {
		if boolOnlyFromAny(operation[key]) {
			return true
		}
	}
	return false
}

func providerReviewAttemptLedgerMutation(operations []map[string]any) string {
	for _, operation := range operations {
		if safeProviderReviewProviderAPIMutation(stringFromMap(operation, "provider_api_mutation")) == "enabled" {
			return "enabled"
		}
	}
	return "disabled"
}

func sanitizedProviderReviewAttemptRequestSummary(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"mode":                        "redacted_attempt_request_summary",
		"operation_name":              safeProviderReviewAttemptOperationName(stringFromMap(value, "operation_name")),
		"endpoint_key":                cleanOptionalText(stringFromMap(value, "endpoint_key")),
		"payload_builder":             safeProviderReviewPayloadBuilderName(stringFromMap(value, "payload_builder")),
		"response_handler":            safeProviderReviewResponseHandlerName(stringFromMap(value, "response_handler")),
		"execution_status":            safeProviderReviewAdapterExecutionStatus(stringFromMap(value, "execution_status")),
		"request_body_included":       false,
		"headers_included":            false,
		"idempotency_key_kind":        "operation_scope_hash",
		"idempotency_key_included":    false,
		"requires_provider_client":    true,
		"requires_request_builder":    true,
		"requires_response_handler":   true,
		"requires_idempotency_ledger": true,
		"provider_api_call_made":      false,
		"provider_api_mutation":       "disabled",
		"external_call_made":          false,
		"payload_redacted":            true,
		"contains_token":              false,
		"contains_provider_url":       false,
		"contains_repository_ref":     false,
		"contains_branch_name":        false,
		"contains_file_content":       false,
	}
}

func sanitizedProviderReviewAttemptResponseDiagnostics(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"mode":                     "redacted_attempt_response_diagnostics",
		"endpoint_key":             safeProviderReviewEndpointKey(stringFromMap(value, "endpoint_key")),
		"status":                   safeProviderReviewAttemptResponseStatus(stringFromMap(value, "status")),
		"success_status_class":     safeProviderReviewStatusClass(stringFromMap(value, "success_status_class")),
		"retryable_status_classes": safeProviderReviewStatusClasses(stringSliceFromAny(value["retryable_status_classes"])),
		"response_body_included":   false,
		"headers_included":         false,
		"contains_token":           false,
		"contains_provider_url":    false,
		"provider_api_call_made":   false,
		"provider_api_mutation":    "disabled",
		"external_call_made":       false,
	}
}

func providerReviewAttemptLedgerResultRecordingPlan(attempt map[string]any) map[string]any {
	plan := providerReviewAttemptLocalResultPlanFromAttempt(attempt, "success")
	plan["plan_context"] = "ledger_metadata_readiness"
	plan["result_status_probe"] = "success"
	plan["accepted_result_statuses"] = []string{"success", "retryable", "failed"}
	return plan
}

func safeProviderReviewAttemptResponseStatus(value string) string {
	switch cleanOptionalText(value) {
	case "pending", "success", "retryable", "failed", "blocked":
		return cleanOptionalText(value)
	default:
		return "blocked"
	}
}

func safeProviderReviewAttemptStatus(value string) string {
	switch cleanOptionalText(value) {
	case "planned", "running", "completed", "failed", "blocked":
		return cleanOptionalText(value)
	default:
		return "blocked"
	}
}

func safeProviderReviewReplayCheck(value string) string {
	switch cleanOptionalText(value) {
	case "detect_existing_branch_ref", "detect_existing_commit_batch", "detect_existing_open_review":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func safeProviderReviewConflictPolicy(value string) string {
	switch cleanOptionalText(value) {
	case "treat_existing_matching_ref_as_success", "block_on_content_or_parent_conflict", "reuse_existing_review_request":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func safeProviderReviewRetryPolicy(value string) string {
	switch cleanOptionalText(value) {
	case "retry_only_after_response_diagnostics":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

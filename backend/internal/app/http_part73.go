package app

import (
	"fmt"
	"strings"
)

func sanitizedProviderReviewAttemptLedger(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	operations := make([]map[string]any, 0, len(mapSliceFromAny(value["operations"])))
	for _, operation := range mapSliceFromAny(value["operations"]) {
		operations = append(operations, map[string]any{
			"id":                           cleanOptionalID(fmt.Sprint(operation["id"])),
			"name":                         cleanOptionalText(stringFromMap(operation, "name")),
			"endpoint_key":                 cleanOptionalText(stringFromMap(operation, "endpoint_key")),
			"status":                       cleanOptionalText(stringFromMap(operation, "status")),
			"replay_check":                 cleanOptionalText(stringFromMap(operation, "replay_check")),
			"conflict_policy":              cleanOptionalText(stringFromMap(operation, "conflict_policy")),
			"retry_policy":                 cleanOptionalText(stringFromMap(operation, "retry_policy")),
			"operation_order":              intFromAny(operation["operation_order"], 0),
			"depends_on_operation":         safeProviderReviewAttemptDependencyName(stringFromMap(operation, "depends_on_operation")),
			"dependency_status":            safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(operation, "dependency_status")),
			"request_summary":              sanitizedProviderReviewAttemptRequestSummary(mapFromAny(operation["request_summary"])),
			"response_diagnostics":         sanitizedProviderReviewAttemptResponseDiagnostics(mapFromAny(operation["response_diagnostics"])),
			"external_call_made":           false,
			"provider_api_call_made":       false,
			"provider_api_mutation":        "disabled",
			"provider_status_class":        "",
			"provider_review_url":          "",
			"provider_review_url_included": false,
			"cleanup_attempted":            false,
			"cleanup_succeeded":            false,
			"cleanup_required":             false,
			"idempotency_key_included":     false,
		})
	}
	return map[string]any{
		"status":                   cleanOptionalText(stringFromMap(value, "status")),
		"mode":                     "redacted_attempt_ledger",
		"attempt_count":            len(operations),
		"operations":               operations,
		"orchestration":            sanitizedProviderReviewAttemptOrchestration(mapFromAny(value["orchestration"]), operations),
		"external_call_made":       false,
		"provider_api_call_made":   false,
		"provider_api_mutation":    "disabled",
		"idempotency_key_included": false,
		"contains_token":           false,
		"contains_provider_url":    false,
		"contains_repository_ref":  false,
		"contains_branch_name":     false,
		"contains_file_content":    false,
	}
}

func boolValueFromAny(value any) bool {
	if typed, ok := value.(bool); ok {
		return typed
	}
	text := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	return text == "true" || text == "1" || text == "yes" || text == "on"
}

func sanitizedProviderReviewGates(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"gate":              cleanOptionalText(stringFromMap(item, "gate")),
			"status":            cleanOptionalText(stringFromMap(item, "status")),
			"required_config":   cleanOptionalText(stringFromMap(item, "required_config")),
			"provider_type":     cleanOptionalText(stringFromMap(item, "provider_type")),
			"review_kind":       cleanOptionalText(stringFromMap(item, "review_kind")),
			"adapter_status":    safeProviderReviewAdapterStatus(stringFromMap(item, "adapter_status")),
			"source_branch":     cleanOptionalText(stringFromMap(item, "source_branch")),
			"target_branch":     cleanOptionalText(stringFromMap(item, "target_branch")),
			"message":           cleanOptionalText(stringFromMap(item, "message")),
			"sensitive_payload": false,
		})
	}
	return out
}

func sanitizedProviderAPIRequestPlan(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"status":                 cleanOptionalText(stringFromMap(value, "status")),
		"mode":                   "redacted_request_plan",
		"provider_type":          cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":            cleanOptionalText(stringFromMap(value, "review_kind")),
		"source_branch":          cleanOptionalText(stringFromMap(value, "source_branch")),
		"target_branch":          cleanOptionalText(stringFromMap(value, "target_branch")),
		"file_count":             intFromAny(value["file_count"], 0),
		"payload_redacted":       true,
		"contains_token":         false,
		"contains_file_content":  false,
		"provider_api_call_made": false,
		"provider_api_mutation":  "disabled",
		"blocked_reasons":        stringSliceFromAny(value["blocked_reasons"]),
		"operations":             sanitizedProviderAPIRequestOperations(mapSliceFromAny(value["operations"])),
	}
}

func sanitizedProviderAPIRequestOperations(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"name":                  cleanOptionalText(stringFromMap(item, "name")),
			"method":                cleanOptionalText(stringFromMap(item, "method")),
			"endpoint_key":          cleanOptionalText(stringFromMap(item, "endpoint_key")),
			"payload_shape":         cleanOptionalText(stringFromMap(item, "payload_shape")),
			"file_count":            intFromAny(item["file_count"], 0),
			"payload_redacted":      true,
			"contains_token":        false,
			"contains_file_content": false,
			"api_call":              false,
		})
	}
	return out
}

func sanitizedProviderReviewReconciliation(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"status":                 cleanOptionalText(stringFromMap(value, "status")),
		"mode":                   "preflight_reconciliation",
		"provider_type":          cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":            cleanOptionalText(stringFromMap(value, "review_kind")),
		"credential_strategy":    sanitizedProviderReviewCredentialStrategy(mapFromAny(value["credential_strategy"])),
		"adapter_contract":       sanitizedProviderReviewAdapterContract(mapFromAny(value["adapter_contract"])),
		"request_envelopes":      sanitizedProviderReviewAdapterRequestEnvelopes(mapSliceFromAny(value["request_envelopes"])),
		"adapter_rehearsal":      sanitizedProviderReviewAdapterRehearsal(mapFromAny(value["adapter_rehearsal"])),
		"mutation_arming_plan":   sanitizedProviderReviewMutationArmingPlan(mapFromAny(value["mutation_arming_plan"])),
		"execution_blueprint":    sanitizedProviderReviewAdapterExecutionBlueprint(mapFromAny(value["execution_blueprint"])),
		"response_diagnostics":   sanitizedProviderReviewAdapterResponseDiagnostics(mapFromAny(value["response_diagnostics"])),
		"idempotency_plan":       sanitizedProviderReviewAdapterIdempotencyPlan(mapFromAny(value["idempotency_plan"])),
		"adapter_status":         cleanOptionalText(stringFromMap(value, "adapter_status")),
		"external_call_made":     false,
		"provider_api_call_made": false,
		"provider_api_mutation":  "disabled",
		"blocked_reasons":        stringSliceFromAny(value["blocked_reasons"]),
		"gates":                  sanitizedProviderReviewGates(mapSliceFromAny(value["gates"])),
		"operations":             sanitizedProviderReviewReconciliationOperations(mapSliceFromAny(value["operations"])),
		"next_step":              cleanOptionalText(stringFromMap(value, "next_step")),
	}
}

func sanitizedProviderReviewAdapterExecutionBlueprint(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	operations := sanitizedProviderReviewAdapterExecutionBlueprintOperations(mapSliceFromAny(value["operations"]))
	status := safeProviderReviewAdapterExecutionBlueprintStatus(stringFromMap(value, "status"))
	if len(operations) == 0 {
		status = "not_recorded"
	}
	return map[string]any{
		"status":                         status,
		"mode":                           "redacted_adapter_execution_blueprint",
		"provider_type":                  cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":                    cleanOptionalText(stringFromMap(value, "review_kind")),
		"adapter_status":                 safeProviderReviewAdapterStatus(stringFromMap(value, "adapter_status")),
		"operation_count":                len(operations),
		"operations":                     operations,
		"execution_stage":                "adapter_implementation_required",
		"live_adapter_implemented":       false,
		"requires_provider_client":       true,
		"requires_request_builder":       true,
		"requires_response_handler":      true,
		"requires_idempotency_ledger":    true,
		"requires_operator_review":       true,
		"requires_mutation_arming":       true,
		"external_call_made":             false,
		"provider_api_call_made":         false,
		"provider_api_mutation":          "disabled",
		"payload_redacted":               true,
		"contains_token":                 false,
		"contains_provider_url":          false,
		"contains_repository_ref":        false,
		"contains_branch_name":           false,
		"contains_file_content":          false,
		"adapter_mutation_currently_off": true,
		"next_step":                      cleanOptionalText(stringFromMap(value, "next_step")),
	}
}

func sanitizedProviderReviewAdapterExecutionBlueprintOperations(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"name":                        safeProviderReviewAttemptOperationName(stringFromMap(item, "name")),
			"endpoint_key":                cleanOptionalText(stringFromMap(item, "endpoint_key")),
			"method":                      cleanOptionalText(stringFromMap(item, "method")),
			"payload_shape":               cleanOptionalText(stringFromMap(item, "payload_shape")),
			"execution_status":            safeProviderReviewAdapterExecutionStatus(stringFromMap(item, "execution_status")),
			"payload_builder":             safeProviderReviewPayloadBuilderName(stringFromMap(item, "payload_builder")),
			"response_handler":            safeProviderReviewResponseHandlerName(stringFromMap(item, "response_handler")),
			"idempotency_scope":           "operation_scope_hash",
			"request_body_included":       false,
			"response_body_included":      false,
			"headers_included":            false,
			"payload_redacted":            true,
			"contains_token":              false,
			"contains_provider_url":       false,
			"contains_repository_ref":     false,
			"contains_branch_name":        false,
			"contains_file_content":       false,
			"api_call":                    false,
			"external_call_made":          false,
			"provider_api_call_made":      false,
			"provider_api_mutation":       "disabled",
			"requires_provider_client":    true,
			"requires_request_builder":    true,
			"requires_response_handler":   true,
			"requires_idempotency_ledger": true,
		})
	}
	return out
}

func safeProviderReviewAdapterExecutionBlueprintStatus(value string) string {
	switch cleanOptionalText(value) {
	case "blocked", "ready_for_adapter_implementation":
		return cleanOptionalText(value)
	default:
		return "blocked"
	}
}

func safeProviderReviewAdapterExecutionStatus(value string) string {
	switch cleanOptionalText(value) {
	case "blocked", "ready_for_adapter_implementation":
		return cleanOptionalText(value)
	default:
		return "blocked"
	}
}

func safeProviderReviewPayloadBuilderName(value string) string {
	switch cleanOptionalText(value) {
	case "build_redacted_branch_ref_request", "build_redacted_file_batch_request", "build_redacted_review_request", "build_redacted_provider_request":
		return cleanOptionalText(value)
	default:
		return "build_redacted_provider_request"
	}
}

func safeProviderReviewResponseHandlerName(value string) string {
	switch cleanOptionalText(value) {
	case "handle_branch_ref_response", "handle_commit_files_response", "handle_review_request_response", "handle_provider_response":
		return cleanOptionalText(value)
	default:
		return "handle_provider_response"
	}
}

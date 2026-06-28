package app

func sanitizedProviderReviewMutationArmingPlan(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	executionEnabled := boolOnlyFromAny(value["execution_enabled_config"])
	rehearsalReady := boolOnlyFromAny(value["adapter_rehearsal_ready"])
	status := safeProviderReviewMutationArmingStatus(stringFromMap(value, "status"))
	if status == "armed" {
		status = "ready_to_arm"
	}
	if !executionEnabled || !rehearsalReady {
		status = "blocked"
	}
	return map[string]any{
		"status":                         status,
		"mode":                           "redacted_mutation_arming_plan",
		"provider_type":                  cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":                    cleanOptionalText(stringFromMap(value, "review_kind")),
		"required_config":                "ASSOPS_ARM_PROVIDER_REVIEW_MUTATION",
		"execution_enabled_config":       executionEnabled,
		"adapter_rehearsal_ready":        rehearsalReady,
		"mutation_armed_config":          boolOnlyFromAny(value["mutation_armed_config"]),
		"mutation_armed":                 false,
		"blocked_reasons":                safeProviderReviewBlockedReasons(stringSliceFromAny(value["blocked_reasons"])),
		"external_call_made":             false,
		"provider_api_call_made":         false,
		"provider_api_mutation":          "disabled",
		"contains_token":                 false,
		"contains_provider_url":          false,
		"contains_repository_ref":        false,
		"contains_file_content":          false,
		"requires_operator_review":       true,
		"requires_adapter_rehearsal":     true,
		"adapter_mutation_currently_off": true,
		"next_step":                      cleanOptionalText(stringFromMap(value, "next_step")),
	}
}

func safeProviderReviewMutationArmingStatus(value string) string {
	switch cleanOptionalText(value) {
	case "blocked", "ready_to_arm", "armed":
		return cleanOptionalText(value)
	default:
		return "blocked"
	}
}

func sanitizedProviderReviewAdapterRehearsal(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	operations := sanitizedProviderReviewAdapterRehearsalOperations(mapSliceFromAny(value["operations"]))
	readyCount := 0
	blockedCount := 0
	for _, operation := range operations {
		if operation["status"] == "ready" {
			readyCount++
		} else {
			blockedCount++
		}
	}
	status := "not_recorded"
	if len(operations) > 0 {
		status = "ready"
	}
	if blockedCount > 0 {
		status = "blocked"
	}
	return map[string]any{
		"status":                         status,
		"mode":                           "redacted_adapter_rehearsal",
		"provider_type":                  cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":                    cleanOptionalText(stringFromMap(value, "review_kind")),
		"adapter_status":                 safeProviderReviewAdapterStatus(stringFromMap(value, "adapter_status")),
		"operation_count":                len(operations),
		"ready_operation_count":          readyCount,
		"blocked_operation_count":        blockedCount,
		"blocked_reasons":                safeProviderReviewBlockedReasons(stringSliceFromAny(value["blocked_reasons"])),
		"operations":                     operations,
		"mutation_arming_candidate":      status == "ready" && blockedCount == 0 && len(operations) > 0,
		"external_call_made":             false,
		"provider_api_call_made":         false,
		"provider_api_mutation":          "disabled",
		"payload_redacted":               true,
		"contains_token":                 false,
		"contains_provider_url":          false,
		"contains_repository_ref":        false,
		"contains_file_content":          false,
		"requires_operator_review":       true,
		"requires_mutation_arming":       true,
		"adapter_mutation_currently_off": true,
	}
}

func sanitizedProviderReviewAdapterRehearsalOperations(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"name":                   cleanOptionalText(stringFromMap(item, "name")),
			"endpoint_key":           cleanOptionalText(stringFromMap(item, "endpoint_key")),
			"status":                 safeProviderReviewRehearsalStatus(stringFromMap(item, "status")),
			"blocked_reasons":        safeProviderReviewBlockedReasons(stringSliceFromAny(item["blocked_reasons"])),
			"external_call_made":     false,
			"provider_api_call_made": false,
			"provider_api_mutation":  "disabled",
		})
	}
	return out
}

func safeProviderReviewRehearsalStatus(value string) string {
	switch cleanOptionalText(value) {
	case "ready", "blocked", "not_recorded":
		return cleanOptionalText(value)
	default:
		return "blocked"
	}
}

func sanitizedProviderReviewAdapterContract(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"status":                cleanOptionalText(stringFromMap(value, "status")),
		"adapter_status":        cleanOptionalText(stringFromMap(value, "adapter_status")),
		"contract_version":      cleanOptionalText(stringFromMap(value, "contract_version")),
		"provider_type":         cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":           cleanOptionalText(stringFromMap(value, "review_kind")),
		"external_call_made":    false,
		"provider_api_mutation": "disabled",
		"contains_token":        false,
		"contains_file_content": false,
		"operations":            sanitizedProviderReviewAdapterContractOperations(mapSliceFromAny(value["operations"])),
		"request_envelopes":     sanitizedProviderReviewAdapterRequestEnvelopes(mapSliceFromAny(value["request_envelopes"])),
		"response_diagnostics":  sanitizedProviderReviewAdapterResponseDiagnostics(mapFromAny(value["response_diagnostics"])),
		"idempotency_plan":      sanitizedProviderReviewAdapterIdempotencyPlan(mapFromAny(value["idempotency_plan"])),
		"next_step":             cleanOptionalText(stringFromMap(value, "next_step")),
	}
}

func sanitizedProviderReviewAdapterIdempotencyPlan(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"status":                     cleanOptionalText(stringFromMap(value, "status")),
		"mode":                       "redacted_idempotency_plan",
		"provider_type":              cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":                cleanOptionalText(stringFromMap(value, "review_kind")),
		"adapter_status":             cleanOptionalText(stringFromMap(value, "adapter_status")),
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
		"requires_persisted_attempt": boolOnlyFromAny(value["requires_persisted_attempt"]),
		"retry_after_diagnostics":    boolOnlyFromAny(value["retry_after_diagnostics"]),
		"operations":                 sanitizedProviderReviewAdapterIdempotencyOperations(mapSliceFromAny(value["operations"])),
	}
}

func sanitizedProviderReviewAdapterIdempotencyOperations(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"name":                          cleanOptionalText(stringFromMap(item, "name")),
			"endpoint_key":                  cleanOptionalText(stringFromMap(item, "endpoint_key")),
			"status":                        cleanOptionalText(stringFromMap(item, "status")),
			"idempotency_key_kind":          "operation_scope_hash",
			"idempotency_key_included":      false,
			"idempotency_key_material":      "redacted_required_material_only",
			"replay_check":                  cleanOptionalText(stringFromMap(item, "replay_check")),
			"conflict_policy":               cleanOptionalText(stringFromMap(item, "conflict_policy")),
			"retry_policy":                  cleanOptionalText(stringFromMap(item, "retry_policy")),
			"requires_persisted_attempt":    boolOnlyFromAny(item["requires_persisted_attempt"]),
			"contains_token":                false,
			"contains_provider_url":         false,
			"contains_repository_ref":       false,
			"contains_branch_name":          false,
			"contains_file_content":         false,
			"external_call_made":            false,
			"provider_api_call_made":        false,
			"provider_api_mutation":         "disabled",
			"response_diagnostics_required": boolOnlyFromAny(item["response_diagnostics_required"]),
		})
	}
	return out
}

func sanitizedProviderReviewAdapterResponseDiagnostics(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"status":                 cleanOptionalText(stringFromMap(value, "status")),
		"mode":                   "redacted_response_diagnostics",
		"provider_type":          cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":            cleanOptionalText(stringFromMap(value, "review_kind")),
		"adapter_status":         cleanOptionalText(stringFromMap(value, "adapter_status")),
		"external_call_made":     false,
		"provider_api_call_made": false,
		"provider_api_mutation":  "disabled",
		"response_body_included": false,
		"headers_included":       false,
		"contains_token":         false,
		"contains_provider_url":  false,
		"diagnostic_fields":      stringSliceFromAny(value["diagnostic_fields"]),
		"operations":             sanitizedProviderReviewAdapterResponseDiagnosticOperations(mapSliceFromAny(value["operations"])),
	}
}

func sanitizedProviderReviewAdapterResponseDiagnosticOperations(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"name":                     cleanOptionalText(stringFromMap(item, "name")),
			"endpoint_key":             cleanOptionalText(stringFromMap(item, "endpoint_key")),
			"status":                   cleanOptionalText(stringFromMap(item, "status")),
			"success_status_class":     cleanOptionalText(stringFromMap(item, "success_status_class")),
			"retryable_status_classes": stringSliceFromAny(item["retryable_status_classes"]),
			"response_body_included":   false,
			"headers_included":         false,
			"contains_token":           false,
			"contains_provider_url":    false,
			"external_call_made":       false,
			"provider_api_mutation":    "disabled",
		})
	}
	return out
}

func sanitizedProviderReviewAdapterRequestEnvelopes(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"name":                    cleanOptionalText(stringFromMap(item, "name")),
			"method":                  cleanOptionalText(stringFromMap(item, "method")),
			"endpoint_key":            cleanOptionalText(stringFromMap(item, "endpoint_key")),
			"payload_shape":           cleanOptionalText(stringFromMap(item, "payload_shape")),
			"file_count":              intFromAny(item["file_count"], 0),
			"payload_redacted":        true,
			"contains_token":          false,
			"contains_file_content":   false,
			"contains_provider_url":   false,
			"contains_repository_ref": false,
			"api_call":                false,
			"provider_api_mutation":   "disabled",
			"execution_status":        cleanOptionalText(stringFromMap(item, "execution_status")),
			"blocked_reason":          cleanOptionalText(stringFromMap(item, "blocked_reason")),
			"readiness":               sanitizedProviderReviewAdapterRequestReadiness(mapSliceFromAny(item["readiness"])),
		})
	}
	return out
}

func sanitizedProviderReviewAdapterRequestReadiness(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"evidence": cleanOptionalText(stringFromMap(item, "evidence")),
			"status":   cleanOptionalText(stringFromMap(item, "status")),
		})
	}
	return out
}

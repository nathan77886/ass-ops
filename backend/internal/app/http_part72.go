package app

func providerReviewAttemptStatusForLocalResult(resultStatus string) string {
	switch safeProviderReviewAttemptLocalResultStatus(resultStatus) {
	case "success":
		return "completed"
	case "retryable":
		return "planned"
	case "failed":
		return "failed"
	default:
		return "blocked"
	}
}

func providerReviewAttemptDependencyStatusForLocalResult(resultStatus string) string {
	switch safeProviderReviewAttemptLocalResultStatus(resultStatus) {
	case "success":
		return "dependency_satisfied"
	case "failed":
		return "dependency_failed"
	default:
		return ""
	}
}

func providerReviewAttemptClaimPlanFromAttempt(attempt map[string]any) map[string]any {
	operation := map[string]any{
		"name":                  stringFromMap(attempt, "operation_name"),
		"endpoint_key":          stringFromMap(attempt, "endpoint_key"),
		"status":                stringFromMap(attempt, "status"),
		"dependency_status":     stringFromMap(attempt, "dependency_status"),
		"replay_check":          stringFromMap(attempt, "replay_check"),
		"conflict_policy":       stringFromMap(attempt, "conflict_policy"),
		"retry_policy":          stringFromMap(attempt, "retry_policy"),
		"operation_order":       attempt["operation_order"],
		"request_summary":       attempt["request_summary"],
		"response_diagnostics":  attempt["response_diagnostics"],
		"claimed_at":            attempt["claimed_at"],
		"claimed_by_user_id":    attempt["claimed_by_user_id"],
		"provider_api_mutation": "disabled",
	}
	requestSummary := mapFromAny(attempt["request_summary"])
	responseDiagnostics := mapFromAny(attempt["response_diagnostics"])
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	claimPlan := providerReviewAttemptClaimPlanForOperation(operation, requestSummary, responseDiagnostics, operationName, endpointKey)
	return claimPlan
}

func providerReviewAttemptClaimPlanForOperation(operation, requestSummary, responseDiagnostics map[string]any, operationName, endpointKey string) map[string]any {
	requestSummaryReady := providerReviewAttemptRequestSummaryReadyForOperation(requestSummary, operationName, endpointKey)
	responseDiagnosticsReady := providerReviewAttemptResponseDiagnosticsReadyForEndpoint(responseDiagnostics, endpointKey)
	claimPlan := providerReviewAttemptExecutionClaimPlan(operation, boolOnlyFromAny(requestSummary["requires_idempotency_ledger"]), responseDiagnosticsReady)
	if !requestSummaryReady {
		claimPlan["claim_metadata_ready"] = false
	}
	claimPlan["request_summary_matches_operation"] = requestSummaryReady
	claimPlan["response_diagnostics_match_endpoint"] = responseDiagnosticsReady
	if !requestSummaryReady {
		blockedReasons := stringSliceFromAny(claimPlan["blocked_reasons"])
		if !providerReviewStringSliceContains(blockedReasons, "provider_review_request_summary_mismatch") {
			blockedReasons = append([]string{"provider_review_request_summary_mismatch"}, blockedReasons...)
		}
		claimPlan["blocked_reasons"] = blockedReasons
	}
	if !responseDiagnosticsReady {
		blockedReasons := stringSliceFromAny(claimPlan["blocked_reasons"])
		if !providerReviewStringSliceContains(blockedReasons, "provider_review_response_diagnostics_endpoint_mismatch") {
			blockedReasons = append([]string{"provider_review_response_diagnostics_endpoint_mismatch"}, blockedReasons...)
		}
		claimPlan["blocked_reasons"] = blockedReasons
	}
	return claimPlan
}

func providerReviewAttemptRequestSummaryReadyForOperation(summary map[string]any, operationName, endpointKey string) bool {
	operationName = safeProviderReviewAttemptOperationName(operationName)
	endpointKey = safeProviderReviewEndpointKey(endpointKey)
	return operationName != "" &&
		endpointKey != "" &&
		stringFromMap(summary, "mode") == "redacted_attempt_request_summary" &&
		safeProviderReviewAttemptOperationName(stringFromMap(summary, "operation_name")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(summary, "endpoint_key")) == endpointKey &&
		providerReviewAttemptPayloadBuilderMatchesOperation(operationName, stringFromMap(summary, "payload_builder")) &&
		providerReviewAttemptResponseHandlerMatchesOperation(operationName, stringFromMap(summary, "response_handler"))
}

func providerReviewAttemptResponseDiagnosticsReadyForEndpoint(diagnostics map[string]any, endpointKey string) bool {
	endpointKey = safeProviderReviewEndpointKey(endpointKey)
	return endpointKey != "" &&
		stringFromMap(diagnostics, "mode") == "redacted_attempt_response_diagnostics" &&
		safeProviderReviewEndpointKey(stringFromMap(diagnostics, "endpoint_key")) == endpointKey
}

func providerReviewStringSliceContains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func sanitizedProviderReviewExecutionGuardrail(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"execution_mode":           cleanOptionalText(stringFromMap(value, "execution_mode")),
		"execution_enabled":        false,
		"execution_enabled_config": boolValueFromAny(value["execution_enabled_config"]),
		"mutation_armed_config":    boolOnlyFromAny(value["mutation_armed_config"]),
		"provider_type":            cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":              cleanOptionalText(stringFromMap(value, "review_kind")),
		"source_branch":            cleanOptionalText(stringFromMap(value, "source_branch")),
		"target_branch":            cleanOptionalText(stringFromMap(value, "target_branch")),
		"provider_api_call_made":   false,
		"provider_api_mutation":    "disabled",
		"branch_creation_allowed":  false,
		"review_request_allowed":   false,
		"blocked_reasons":          stringSliceFromAny(value["blocked_reasons"]),
		"gates":                    sanitizedProviderReviewGates(mapSliceFromAny(value["gates"])),
		"next_step":                cleanOptionalText(stringFromMap(value, "next_step")),
	}
}

func sanitizedProviderReviewTargetSummary(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	sourceBranch := cleanOptionalText(stringFromMap(value, "source_branch"))
	if !isSafeGitRefPart(sourceBranch) {
		sourceBranch = ""
	}
	targetBranch := cleanOptionalText(stringFromMap(value, "target_branch"))
	if !isSafeGitRefPart(targetBranch) {
		targetBranch = ""
	}
	operations := make([]map[string]any, 0, len(mapSliceFromAny(value["operations"])))
	for _, operation := range mapSliceFromAny(value["operations"]) {
		operations = append(operations, map[string]any{
			"name":                  cleanOptionalText(stringFromMap(operation, "name")),
			"endpoint_key":          cleanOptionalText(stringFromMap(operation, "endpoint_key")),
			"payload_shape":         cleanOptionalText(stringFromMap(operation, "payload_shape")),
			"status":                cleanOptionalText(stringFromMap(operation, "status")),
			"api_call":              false,
			"provider_api_mutation": "disabled",
			"payload_redacted":      true,
			"contains_token":        false,
			"contains_file_content": false,
		})
	}
	return map[string]any{
		"status":                          cleanOptionalText(stringFromMap(value, "status")),
		"mode":                            "redacted_execution_target_summary",
		"provider_type":                   cleanOptionalText(stringFromMap(value, "provider_type")),
		"review_kind":                     cleanOptionalText(stringFromMap(value, "review_kind")),
		"source_branch":                   sourceBranch,
		"target_branch":                   targetBranch,
		"branch_refs_ready":               boolOnlyFromAny(value["branch_refs_ready"]),
		"starter_file_payload_ready":      boolOnlyFromAny(value["starter_file_payload_ready"]),
		"provider_api_request_ready":      boolOnlyFromAny(value["provider_api_request_ready"]),
		"file_count":                      intFromAny(value["file_count"], 0),
		"operation_count":                 len(operations),
		"operations":                      operations,
		"adapter_status":                  safeProviderReviewAdapterStatus(stringFromMap(value, "adapter_status")),
		"blocked_reasons":                 safeProviderReviewBlockedReasons(stringSliceFromAny(value["blocked_reasons"])),
		"external_call_made":              false,
		"provider_api_call_made":          false,
		"provider_api_mutation":           "disabled",
		"payload_redacted":                true,
		"contains_token":                  false,
		"contains_provider_url":           false,
		"contains_repository_ref":         false,
		"contains_file_content":           false,
		"idempotency_key_included":        false,
		"requires_persisted_attempt":      true,
		"requires_response_diagnostics":   true,
		"requires_provider_api_adapter":   true,
		"requires_adapter_mutation_armed": true,
		"requires_operator_review":        true,
		"future_adapter_input_boundary":   "branch_ref_commit_review_request",
		"adapter_mutation_currently_off":  true,
	}
}

func safeProviderReviewAdapterStatus(value string) string {
	switch cleanOptionalText(value) {
	case "missing", "planned", "ready", "blocked", "unsupported":
		return cleanOptionalText(value)
	default:
		return "missing"
	}
}

func safeProviderReviewBlockedReasons(items []string) []string {
	allowed := map[string]bool{
		"provider_supported":                               true,
		"starter_file_payload_staged":                      true,
		"provider_api_request_plan_ready":                  true,
		"provider_review_execution_enabled":                true,
		"provider_credential_configured":                   true,
		"provider_token_env_present":                       true,
		"provider_review_api_adapter":                      true,
		"provider_review_adapter_rehearsal":                true,
		"provider_review_claim_metadata":                   true,
		"provider_review_adapter_contract":                 true,
		"provider_review_request_materialization":          true,
		"provider_review_branch_policy":                    true,
		"provider_review_credential_binding":               true,
		"provider_review_adapter_runtime":                  true,
		"provider_review_transport_metadata":               true,
		"provider_review_response_recording":               true,
		"provider_review_transaction_boundary":             true,
		"provider_review_mutation_armed":                   true,
		"review_branches_valid":                            true,
		"review_target_summary_ready":                      true,
		"provider_review_target_summary_safe":              true,
		"provider_review_execution_approval_action":        true,
		"operation_approval_not_approved":                  true,
		"provider_review_attempt_claim_not_recorded":       true,
		"provider_review_dependency_not_ready":             true,
		"provider_review_attempt_already_executed":         true,
		"project_template_run_missing":                     true,
		"provider_review_attempt_asset_missing":            true,
		"provider_review_attempt_live_execution_readiness": true,
		"provider_review_mutation_arming_review":           true,
		"provider_review_target_remote_missing":            true,
		"provider_review_github_target_missing":            true,
		"provider_review_attempt_execution_conflict":       true,
	}
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		item = cleanOptionalText(item)
		if item == "" || len(item) > 128 || !allowed[item] || seen[item] {
			continue
		}
		out = append(out, item)
		seen[item] = true
	}
	return out
}

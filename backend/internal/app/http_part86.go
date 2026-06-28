package app

func providerReviewAttemptAdapterProviderSendPlan(operation, requestPlan, credentialPlan, runtimePlan, transportPlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	providerType := providerReviewProviderFromEndpointKey(endpointKey)
	if operationName == "" || endpointKey == "" || providerType == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) {
		return map[string]any{}
	}
	retryBackoffPlan := providerReviewAttemptAdapterRetryBackoffPlan(operation, transportPlan)
	requestReady := providerReviewAttemptRequestPlanReadyForOperation(requestPlan, operationName, endpointKey)
	credentialReady := providerReviewAttemptCredentialPlanReadyForOperation(credentialPlan, operationName, endpointKey)
	runtimeReady := providerReviewAttemptRuntimePlanReadyForOperation(runtimePlan, operationName, endpointKey)
	transportReady := providerReviewAttemptTransportPlanReadyForOperation(transportPlan, operationName, endpointKey)
	metadataReady := requestReady &&
		credentialReady &&
		runtimeReady &&
		transportReady
	return map[string]any{
		"mode":                              "redacted_attempt_adapter_provider_send_plan",
		"provider_send_state":               "blocked",
		"provider_send_ready":               false,
		"provider_send_ready_reason":        "provider_request_send_not_armed",
		"provider_send_metadata_ready":      metadataReady,
		"provider_type":                     providerType,
		"operation_name":                    operationName,
		"endpoint_key":                      endpointKey,
		"operation_order":                   intFromAny(operation["operation_order"], 0),
		"method":                            providerReviewMethodForOperation(operationName),
		"payload_shape":                     providerReviewPayloadShapeForOperation(operationName),
		"auth_scheme":                       providerReviewAuthSchemeForProvider(providerType),
		"content_type":                      "application/json",
		"timeout_seconds":                   intFromAny(transportPlan["timeout_seconds"], 15),
		"retry_backoff_plan":                retryBackoffPlan,
		"requires_request_materialization":  true,
		"requires_credential_binding":       true,
		"requires_adapter_runtime":          true,
		"requires_transport":                true,
		"requires_retry_backoff_plan":       true,
		"requires_mutation_arming":          true,
		"request_materialization_ready":     requestReady,
		"credential_binding_ready":          credentialReady,
		"adapter_runtime_ready":             runtimeReady,
		"transport_metadata_ready":          transportReady,
		"request_path_materialized":         false,
		"request_url_materialized":          false,
		"request_body_materialized":         false,
		"headers_materialized":              false,
		"authorization_header_materialized": false,
		"provider_client_bound":             false,
		"credential_bound":                  false,
		"runtime_bound":                     false,
		"mutation_armed":                    false,
		"send_attempted":                    false,
		"provider_request_sent":             false,
		"provider_response_received":        false,
		"external_call_made":                false,
		"provider_api_call_made":            false,
		"provider_api_mutation":             "disabled",
		"request_body_included":             false,
		"response_body_included":            false,
		"headers_included":                  false,
		"authorization_header_included":     false,
		"provider_url_included":             false,
		"idempotency_key_included":          false,
		"provider_request_id_included":      false,
		"contains_token":                    false,
		"contains_provider_url":             false,
		"contains_repository_ref":           false,
		"contains_branch_name":              false,
		"contains_file_content":             false,
		"provider_send_boundary_redacted":   true,
		"provider_send_sequence":            []string{"bind_provider_client", "apply_redacted_transport_metadata", "verify_mutation_arming", "stage_provider_request", "send_provider_request", "handoff_to_response_handler"},
		"provider_send_suppressed_fields":   []string{"request_url", "request_path", "request_body", "request_headers", "authorization_header", "token", "idempotency_key", "repository_ref", "branch_name", "file_content"},
		"blocked_reasons": []string{
			"provider_request_send_not_armed",
			"provider_request_not_materialized",
			"provider_credential_runtime_binding_not_armed",
			"provider_review_adapter_runtime_not_bound",
			"provider_review_mutation_not_armed",
		},
	}
}

func providerReviewAttemptAdapterRetryBackoffPlan(operation, transportPlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	providerType := providerReviewProviderFromEndpointKey(endpointKey)
	if operationName == "" || endpointKey == "" || providerType == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) || !providerReviewAttemptPlanMatchesOperation(transportPlan, "redacted_attempt_adapter_transport_plan", operationName, endpointKey) {
		return map[string]any{}
	}
	return map[string]any{
		"mode":                               "redacted_attempt_adapter_retry_backoff_plan",
		"retry_backoff_state":                "blocked",
		"retry_backoff_ready":                false,
		"retry_backoff_ready_reason":         "provider_retry_backoff_not_armed",
		"retry_backoff_metadata_ready":       true,
		"operation_name":                     operationName,
		"endpoint_key":                       endpointKey,
		"operation_order":                    intFromAny(operation["operation_order"], 0),
		"retry_policy":                       "retry_only_after_response_diagnostics",
		"max_attempts":                       3,
		"initial_backoff_seconds":            30,
		"max_backoff_seconds":                300,
		"jitter":                             "full",
		"retryable_status_classes":           providerReviewExpectedRetryClassesForOperation(operationName),
		"transport_retryable_status_classes": safeProviderReviewStatusClasses(stringSliceFromAny(transportPlan["retryable_status_classes"])),
		"requires_response_diagnostics":      true,
		"requires_idempotency_ledger":        true,
		"requires_attempt_ledger":            true,
		"requires_mutation_arming":           true,
		"retry_scheduled":                    false,
		"retry_attempt_recorded":             false,
		"retry_after_value_recorded":         false,
		"retry_after_header_included":        false,
		"provider_rate_limit_value_included": false,
		"provider_error_code_included":       false,
		"external_call_made":                 false,
		"provider_api_call_made":             false,
		"provider_api_mutation":              "disabled",
		"request_body_included":              false,
		"response_body_included":             false,
		"headers_included":                   false,
		"authorization_header_included":      false,
		"provider_url_included":              false,
		"idempotency_key_included":           false,
		"contains_token":                     false,
		"contains_provider_url":              false,
		"contains_repository_ref":            false,
		"contains_branch_name":               false,
		"contains_file_content":              false,
		"retry_backoff_boundary_redacted":    true,
		"retry_backoff_sequence":             []string{"classify_retryable_response", "verify_idempotency_ledger", "record_retry_decision", "schedule_backoff_retry"},
		"retry_backoff_suppressed_fields":    []string{"retry_after_value", "rate_limit_remaining", "provider_error_code", "response_headers", "response_body", "provider_url", "authorization_header", "token", "idempotency_key", "repository_ref", "branch_name", "file_content"},
		"blocked_reasons": []string{
			"provider_retry_backoff_not_armed",
			"provider_response_diagnostics_not_recorded",
			"provider_idempotency_ledger_not_claimed",
			"provider_review_mutation_not_armed",
		},
	}
}

func providerReviewAttemptInvocationReadyReason(ready bool, blockedReason string) string {
	if ready {
		return "ready"
	}
	return blockedReason
}

const (
	providerReviewAttemptAdapterResponsePlanMode = "redacted_attempt_adapter_response_plan"
)

func providerReviewAttemptAdapterTransactionPlan(operation, claimPlan, responsePlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	if operationName == "" || endpointKey == "" {
		return map[string]any{}
	}
	providerCallBoundaryPlan := providerReviewAttemptAdapterProviderCallBoundaryPlan(operation, claimPlan, responsePlan)
	return map[string]any{
		"mode":                               "redacted_attempt_adapter_transaction_plan",
		"transaction_state":                  "blocked",
		"transaction_ready":                  false,
		"transaction_ready_reason":           "provider_review_transaction_not_armed",
		"transaction_metadata_ready":         providerReviewAttemptClaimPlanReadyForOperation(claimPlan, operationName, endpointKey) && providerReviewAttemptResponsePlanReadyForOperation(responsePlan, operationName, endpointKey),
		"operation_name":                     operationName,
		"endpoint_key":                       endpointKey,
		"operation_order":                    intFromAny(operation["operation_order"], 0),
		"transaction_sequence":               []string{"verify_attempt_claim", "verify_idempotency_claim", "record_provider_call_boundary", "classify_provider_response", "update_attempt_status", "update_dependency_status"},
		"claim_status_from":                  "planned",
		"claim_status_to":                    "running",
		"success_attempt_status":             safeProviderReviewAttemptStatus(stringFromMap(responsePlan, "success_attempt_status")),
		"retry_attempt_status":               safeProviderReviewAttemptStatus(stringFromMap(responsePlan, "retry_attempt_status")),
		"failure_attempt_status":             safeProviderReviewAttemptStatus(stringFromMap(responsePlan, "failure_attempt_status")),
		"dependency_unlocks_operation":       safeProviderReviewAttemptOperationName(stringFromMap(responsePlan, "dependency_unlocks_operation")),
		"dependency_update_status":           safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(responsePlan, "dependency_update_status")),
		"requires_database_transaction":      true,
		"requires_attempt_status_planned":    true,
		"requires_attempt_status_running":    true,
		"requires_optimistic_lock":           true,
		"requires_idempotency_ledger":        true,
		"requires_provider_call_boundary":    true,
		"requires_response_diagnostics":      true,
		"requires_dependency_update":         boolOnlyFromAny(responsePlan["requires_dependency_update"]),
		"requires_mutation_arming":           true,
		"provider_call_boundary_plan":        providerCallBoundaryPlan,
		"transaction_opened":                 false,
		"attempt_claim_verified":             false,
		"idempotency_claim_verified":         false,
		"provider_call_boundary_recorded":    false,
		"provider_response_classified":       false,
		"attempt_status_updated":             false,
		"response_recorded":                  false,
		"dependency_update_recorded":         false,
		"provider_request_id_recorded":       false,
		"provider_response_body_recorded":    false,
		"provider_response_headers_recorded": false,
		"adapter_implemented":                false,
		"mutation_armed":                     false,
		"external_call_made":                 false,
		"provider_api_call_made":             false,
		"provider_api_mutation":              "disabled",
		"request_body_included":              false,
		"response_body_included":             false,
		"headers_included":                   false,
		"authorization_header_included":      false,
		"provider_url_included":              false,
		"idempotency_key_included":           false,
		"contains_token":                     false,
		"contains_provider_url":              false,
		"contains_repository_ref":            false,
		"contains_branch_name":               false,
		"contains_file_content":              false,
		"transaction_boundary_redacted":      true,
		"blocked_reasons": []string{
			"provider_review_attempt_claim_not_recorded",
			"provider_review_transaction_not_armed",
			"provider_api_call_not_made",
			"provider_review_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
	}
}

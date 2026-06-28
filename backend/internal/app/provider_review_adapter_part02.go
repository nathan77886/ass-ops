package app

func (m disabledProviderReviewAttemptExecuteMethod) BuildPlan(input providerReviewAttemptExecuteMethodInput) map[string]any {
	providerType := safeProviderReviewProviderType(input.ProviderType)
	operationName := safeProviderReviewAttemptOperationName(input.OperationName)
	endpointKey := safeProviderReviewEndpointKey(input.EndpointKey)
	if providerType == "" || operationName == "" || endpointKey == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) {
		return map[string]any{}
	}
	return map[string]any{
		"mode":                                "redacted_attempt_adapter_execute_method_plan",
		"execute_method_state":                "blocked",
		"execute_method_ready":                false,
		"execute_method_ready_reason":         "provider_review_execute_method_not_armed",
		"provider_type":                       providerType,
		"operation_name":                      operationName,
		"endpoint_key":                        endpointKey,
		"method_name":                         m.MethodName(),
		"http_method":                         providerReviewMethodForOperation(operationName),
		"execute_method_interface_registered": true,
		"execute_method_registered":           true,
		"execute_method_implemented":          false,
		"execute_method_bound":                false,
		"requires_attempt_claim":              true,
		"requires_idempotency_claim":          true,
		"requires_credential_binding":         true,
		"requires_provider_client":            true,
		"requires_request_builder":            true,
		"requires_transport":                  true,
		"requires_response_handler":           true,
		"requires_transaction_handler":        true,
		"requires_mutation_arming":            true,
		"provider_client_constructed":         false,
		"request_materialized":                false,
		"provider_request_sent":               false,
		"response_handled":                    false,
		"transaction_recorded":                false,
		"dependency_update_recorded":          false,
		"execute_method_boundary_redacted":    true,
		"external_call_made":                  false,
		"provider_api_call_made":              false,
		"provider_api_mutation":               "disabled",
		"request_body_included":               false,
		"response_body_included":              false,
		"headers_included":                    false,
		"authorization_header_included":       false,
		"provider_url_included":               false,
		"idempotency_key_included":            false,
		"contains_token":                      false,
		"contains_provider_url":               false,
		"contains_repository_ref":             false,
		"contains_branch_name":                false,
		"contains_file_content":               false,
		"execution_sequence": []string{
			"verify_attempt_claim",
			"verify_idempotency_claim",
			"bind_credential",
			"construct_provider_client",
			"build_request",
			"stage_provider_request_send",
			"handle_response",
			"record_attempt_transaction",
		},
		"blocked_reasons": []string{
			"provider_review_execute_method_not_armed",
			"provider_review_live_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
	}
}

func (h disabledProviderReviewAttemptResponseHandler) HandlerName() string {
	return h.handlerName
}

func (h disabledProviderReviewAttemptResponseHandler) BuildPlan(input providerReviewAttemptResponseHandlerInput) map[string]any {
	providerType := safeProviderReviewProviderType(input.ProviderType)
	operationName := safeProviderReviewAttemptOperationName(input.OperationName)
	endpointKey := safeProviderReviewEndpointKey(input.EndpointKey)
	if providerType == "" || operationName == "" || endpointKey == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) {
		return map[string]any{}
	}
	unlockOperation := providerReviewAttemptDependencyUnlockOperation(operationName)
	return map[string]any{
		"mode":                               "redacted_attempt_adapter_response_handler_plan",
		"response_handler_state":             "blocked",
		"response_handler_ready":             false,
		"response_handler_ready_reason":      "provider_review_response_handler_not_armed",
		"provider_type":                      providerType,
		"operation_name":                     operationName,
		"endpoint_key":                       endpointKey,
		"handler_name":                       h.HandlerName(),
		"response_status":                    "pending",
		"expected_success_classes":           providerReviewExpectedSuccessClassesForOperation(operationName),
		"retryable_status_classes":           providerReviewExpectedRetryClassesForOperation(operationName),
		"terminal_failure_status_classes":    providerReviewTerminalFailureClassesForOperation(operationName),
		"success_attempt_status":             "completed",
		"retry_attempt_status":               "planned",
		"failure_attempt_status":             "failed",
		"dependency_unlocks_operation":       unlockOperation,
		"dependency_update_status":           providerReviewAttemptDependencyUnlockStatus(unlockOperation),
		"requires_response_diagnostics":      true,
		"requires_idempotency_ledger":        true,
		"requires_dependency_update":         unlockOperation != "",
		"requires_transaction_handler":       true,
		"requires_mutation_arming":           true,
		"handler_interface_registered":       true,
		"handler_registered":                 true,
		"handler_implemented":                false,
		"provider_response_classified":       false,
		"attempt_status_selected":            false,
		"dependency_update_selected":         false,
		"provider_request_id_recorded":       false,
		"response_body_recorded":             false,
		"response_headers_recorded":          false,
		"response_handler_boundary_redacted": true,
		"external_call_made":                 false,
		"provider_api_call_made":             false,
		"provider_api_mutation":              "disabled",
		"response_body_included":             false,
		"headers_included":                   false,
		"provider_request_id_included":       false,
		"contains_token":                     false,
		"contains_provider_url":              false,
		"contains_repository_ref":            false,
		"contains_branch_name":               false,
		"contains_file_content":              false,
		"blocked_reasons": []string{
			"provider_review_response_handler_not_armed",
			"provider_review_live_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
	}
}

func (a disabledProviderReviewAttemptLiveAdapter) AdapterName() string {
	return a.adapterName
}

func (a disabledProviderReviewAttemptLiveAdapter) BuildPlan(input providerReviewAttemptLiveAdapterInput) map[string]any {
	providerType := safeProviderReviewProviderType(input.ProviderType)
	operationName := safeProviderReviewAttemptOperationName(input.OperationName)
	endpointKey := safeProviderReviewEndpointKey(input.EndpointKey)
	if providerType == "" || operationName == "" || endpointKey == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) {
		return map[string]any{}
	}
	contractPlan := providerReviewAttemptLiveAdapterContractPlan(providerType, operationName, endpointKey, a.AdapterName())
	return map[string]any{
		"mode":                               "redacted_attempt_live_adapter_plan",
		"live_adapter_state":                 "blocked",
		"live_adapter_ready":                 false,
		"live_adapter_ready_reason":          "provider_review_live_adapter_not_implemented",
		"provider_type":                      providerType,
		"operation_name":                     operationName,
		"endpoint_key":                       endpointKey,
		"adapter_name":                       a.AdapterName(),
		"contract_plan":                      contractPlan,
		"adapter_interface_registered":       true,
		"live_adapter_registered":            true,
		"live_adapter_implemented":           false,
		"live_adapter_contract_registered":   len(contractPlan) > 0,
		"live_adapter_contract_implemented":  false,
		"requires_activation_plan":           true,
		"requires_attempt_claim":             true,
		"requires_execution_lock":            true,
		"requires_contract_plan":             true,
		"requires_provider_client":           true,
		"requires_request_builder":           true,
		"requires_execute_method":            true,
		"requires_response_handler":          true,
		"requires_transaction_handler":       true,
		"requires_mutation_arming":           true,
		"activation_plan_verified":           false,
		"attempt_claim_verified":             false,
		"execution_lock_verified":            false,
		"provider_client_constructed":        false,
		"request_built":                      false,
		"execute_method_invoked":             false,
		"response_handler_invoked":           false,
		"transaction_recorded":               false,
		"provider_request_sent":              false,
		"external_call_made":                 false,
		"provider_api_call_made":             false,
		"provider_api_mutation":              "disabled",
		"request_body_included":              false,
		"response_body_included":             false,
		"headers_included":                   false,
		"authorization_header_included":      false,
		"provider_url_included":              false,
		"idempotency_key_included":           false,
		"provider_request_id_included":       false,
		"contains_token":                     false,
		"contains_provider_url":              false,
		"contains_repository_ref":            false,
		"contains_branch_name":               false,
		"contains_file_content":              false,
		"live_adapter_boundary_redacted":     true,
		"live_adapter_required_interfaces":   []string{"providerReviewAttemptAdapterRuntime", "providerReviewAttemptRequestBuilder", "providerReviewAttemptProviderClientFactory", "providerReviewAttemptExecuteMethod", "providerReviewAttemptResponseHandler"},
		"live_adapter_required_methods":      []string{"verify_activation", "claim_execution", "build_request", "send_provider_request", "handle_response", "record_attempt_transaction"},
		"live_adapter_suppressed_fields":     []string{"provider_url", "authorization_header", "token", "request_body", "response_body", "repository_ref", "branch_name", "file_content", "idempotency_key", "lock_key"},
		"live_adapter_required_capabilities": providerReviewClientRequiredCapabilitiesForOperation(operationName),
		"blocked_reasons": []string{
			"provider_review_live_adapter_not_implemented",
			"provider_review_adapter_activation_not_armed",
			"provider_review_mutation_not_armed",
		},
	}
}

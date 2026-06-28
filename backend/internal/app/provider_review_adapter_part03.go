package app

import (
	"context"
)

func providerReviewAttemptLiveAdapterContractPlan(providerType, operationName, endpointKey, adapterName string) map[string]any {
	providerType = safeProviderReviewProviderType(providerType)
	operationName = safeProviderReviewAttemptOperationName(operationName)
	endpointKey = safeProviderReviewEndpointKey(endpointKey)
	adapterName = cleanOptionalText(adapterName)
	if providerType == "" || operationName == "" || endpointKey == "" || adapterName == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) {
		return map[string]any{}
	}
	builder := providerReviewAttemptRequestBuilderForOperation(operationName)
	clientFactory := providerReviewAttemptProviderClientFactoryForProvider(providerType)
	executeMethod := providerReviewAttemptExecuteMethodForOperation(operationName)
	responseHandler := providerReviewAttemptResponseHandlerForOperation(operationName)
	if builder == nil || clientFactory == nil || executeMethod == nil || responseHandler == nil {
		return map[string]any{}
	}
	unlockOperation := providerReviewAttemptDependencyUnlockOperation(operationName)
	return map[string]any{
		"mode":                                    "redacted_attempt_live_adapter_contract_plan",
		"contract_state":                          "blocked",
		"contract_ready":                          false,
		"contract_ready_reason":                   "provider_review_live_adapter_contract_not_armed",
		"provider_type":                           providerType,
		"operation_name":                          operationName,
		"endpoint_key":                            endpointKey,
		"adapter_name":                            adapterName,
		"http_method":                             providerReviewMethodForOperation(operationName),
		"endpoint_path_template_key":              providerReviewEndpointPathTemplateKeyForOperation(providerType, operationName),
		"payload_shape":                           providerReviewPayloadShapeForOperation(operationName),
		"auth_scheme":                             providerReviewAuthSchemeForProvider(providerType),
		"builder_name":                            builder.BuilderName(),
		"client_kind":                             clientFactory.ClientKind(),
		"execute_method_name":                     executeMethod.MethodName(),
		"response_handler_name":                   responseHandler.HandlerName(),
		"required_capabilities":                   providerReviewClientRequiredCapabilitiesForOperation(operationName),
		"success_attempt_status":                  "completed",
		"retry_attempt_status":                    "planned",
		"failure_attempt_status":                  "failed",
		"dependency_unlocks_operation":            unlockOperation,
		"dependency_update_status":                providerReviewAttemptDependencyUnlockStatus(unlockOperation),
		"requires_activation_plan":                true,
		"requires_attempt_claim":                  true,
		"requires_execution_lock":                 true,
		"requires_credential_binding":             true,
		"requires_provider_client":                true,
		"requires_request_builder":                true,
		"requires_transport":                      true,
		"requires_response_handler":               true,
		"requires_transaction_handler":            true,
		"requires_mutation_arming":                true,
		"contract_registered":                     true,
		"contract_implemented":                    false,
		"request_contract_materialized":           false,
		"response_contract_materialized":          false,
		"error_contract_materialized":             false,
		"result_contract_materialized":            false,
		"provider_request_sent":                   false,
		"external_call_made":                      false,
		"provider_api_call_made":                  false,
		"provider_api_mutation":                   "disabled",
		"request_body_included":                   false,
		"response_body_included":                  false,
		"headers_included":                        false,
		"authorization_header_included":           false,
		"provider_url_included":                   false,
		"idempotency_key_included":                false,
		"provider_request_id_included":            false,
		"contains_token":                          false,
		"contains_provider_url":                   false,
		"contains_repository_ref":                 false,
		"contains_branch_name":                    false,
		"contains_file_content":                   false,
		"live_adapter_contract_boundary_redacted": true,
		"contract_input_fields":                   []string{"activation_plan", "attempt_claim", "execution_lock", "credential_binding", "provider_client", "request_builder", "transport", "mutation_arming"},
		"contract_output_fields":                  []string{"attempt_status", "response_status_class", "retry_class", "dependency_update_status"},
		"contract_error_classes":                  []string{"retryable_provider_error", "terminal_provider_error", "credential_binding_error", "request_materialization_error", "mutation_guard_error"},
		"contract_persisted_fields":               []string{"attempt_status", "dependency_status", "response_status_class", "retry_class"},
		"contract_suppressed_fields":              []string{"provider_url", "authorization_header", "token", "request_body", "response_body", "response_headers", "repository_ref", "branch_name", "file_content", "idempotency_key", "lock_key"},
		"contract_sequence":                       []string{"verify_activation_contract", "verify_claim_contract", "verify_request_contract", "execute_provider_request", "classify_response_contract", "record_result_contract"},
		"blocked_reasons": []string{
			"provider_review_live_adapter_contract_not_armed",
			"provider_review_live_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
	}
}

func providerReviewAttemptAdapterRuntimeForProvider(providerType string) providerReviewAttemptAdapterRuntime {
	switch safeProviderReviewProviderType(providerType) {
	case "github":
		return disabledProviderReviewAdapterRuntime{adapterKind: "github_provider_review_adapter"}
	case "gitea":
		return disabledProviderReviewAdapterRuntime{adapterKind: "gitea_provider_review_adapter"}
	default:
		return nil
	}
}

func providerReviewAttemptLiveAdapterForProvider(providerType string) providerReviewAttemptLiveAdapter {
	switch safeProviderReviewProviderType(providerType) {
	case "github":
		return disabledProviderReviewAttemptLiveAdapter{adapterName: "github_live_provider_review_adapter"}
	case "gitea":
		return disabledProviderReviewAttemptLiveAdapter{adapterName: "gitea_live_provider_review_adapter"}
	default:
		return nil
	}
}

func providerReviewAttemptProviderClientFactoryForProvider(providerType string) providerReviewAttemptProviderClientFactory {
	switch safeProviderReviewProviderType(providerType) {
	case "github":
		return disabledProviderReviewAttemptProviderClientFactory{clientKind: "github_provider_review_api_client"}
	case "gitea":
		return disabledProviderReviewAttemptProviderClientFactory{clientKind: "gitea_provider_review_api_client"}
	default:
		return nil
	}
}

func providerReviewAttemptRequestBuilderForOperation(operationName string) providerReviewAttemptRequestBuilder {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref":
		return disabledProviderReviewAttemptRequestBuilder{builderName: "build_redacted_branch_ref_request"}
	case "commit_starter_files":
		return disabledProviderReviewAttemptRequestBuilder{builderName: providerReviewExpectedPayloadBuilderName(operationName)}
	case "open_review_request":
		return disabledProviderReviewAttemptRequestBuilder{builderName: "build_redacted_review_request"}
	default:
		return nil
	}
}

func providerReviewAttemptExecuteMethodForOperation(operationName string) providerReviewAttemptExecuteMethod {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref":
		return disabledProviderReviewAttemptExecuteMethod{methodName: "execute_branch_ref_creation"}
	case "commit_starter_files":
		return disabledProviderReviewAttemptExecuteMethod{methodName: "execute_starter_file_commit"}
	case "open_review_request":
		return disabledProviderReviewAttemptExecuteMethod{methodName: "execute_review_request_open"}
	default:
		return nil
	}
}

func providerReviewAttemptResponseHandlerForOperation(operationName string) providerReviewAttemptResponseHandler {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref":
		return disabledProviderReviewAttemptResponseHandler{handlerName: "handle_branch_ref_response"}
	case "commit_starter_files":
		return disabledProviderReviewAttemptResponseHandler{handlerName: "handle_commit_files_response"}
	case "open_review_request":
		return disabledProviderReviewAttemptResponseHandler{handlerName: "handle_review_request_response"}
	default:
		return nil
	}
}

func providerReviewAttemptLiveAdapterPlan(providerType, operationName, endpointKey string) map[string]any {
	providerType = safeProviderReviewProviderType(providerType)
	operationName = safeProviderReviewAttemptOperationName(operationName)
	endpointKey = safeProviderReviewEndpointKey(endpointKey)
	adapter := providerReviewAttemptLiveAdapterForProvider(providerType)
	if adapter == nil || operationName == "" || endpointKey == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) {
		return map[string]any{}
	}
	return adapter.BuildPlan(providerReviewAttemptLiveAdapterInput{
		ProviderType:  providerType,
		OperationName: operationName,
		EndpointKey:   endpointKey,
	})
}

func providerReviewAttemptAdapterRuntimePlan(providerType, operationName, endpointKey string) map[string]any {
	providerType = safeProviderReviewProviderType(providerType)
	operationName = safeProviderReviewAttemptOperationName(operationName)
	endpointKey = safeProviderReviewEndpointKey(endpointKey)
	runtime := providerReviewAttemptAdapterRuntimeForProvider(providerType)
	if runtime == nil || operationName == "" || endpointKey == "" {
		return map[string]any{}
	}
	result := runtime.PrepareInvocation(context.Background(), providerReviewAttemptAdapterRuntimeInput{
		ProviderType:  providerType,
		OperationName: operationName,
		EndpointKey:   endpointKey,
	})
	if !result.OperationSupported {
		return map[string]any{}
	}
	builderPlan := providerReviewAttemptAdapterRequestBuilderPlan(providerType, operationName, endpointKey)
	providerClientPlan := providerReviewAttemptAdapterProviderClientPlan(providerType, operationName, endpointKey)
	executeMethodPlan := providerReviewAttemptAdapterExecuteMethodPlan(providerType, operationName, endpointKey)
	responseHandlerPlan := providerReviewAttemptAdapterResponseHandlerPlan(providerType, operationName, endpointKey)
	return map[string]any{
		"mode":                          "redacted_attempt_adapter_runtime_plan",
		"runtime_state":                 "blocked",
		"runtime_ready":                 false,
		"runtime_ready_reason":          "provider_review_adapter_runtime_not_armed",
		"provider_type":                 result.ProviderType,
		"adapter_kind":                  result.AdapterKind,
		"operation_name":                result.OperationName,
		"endpoint_key":                  result.EndpointKey,
		"adapter_interface_registered":  true,
		"adapter_dispatch_registered":   true,
		"runtime_adapter_selected":      true,
		"operation_supported":           true,
		"live_adapter_implemented":      false,
		"provider_client_constructed":   false,
		"provider_client_plan":          providerClientPlan,
		"execute_method_bound":          false,
		"execute_method_plan":           executeMethodPlan,
		"request_builder_bound":         false,
		"request_builder_plan":          builderPlan,
		"response_handler_bound":        false,
		"response_handler_plan":         responseHandlerPlan,
		"transaction_handler_bound":     false,
		"requires_provider_client":      true,
		"requires_request_builder":      true,
		"requires_response_handler":     true,
		"requires_transaction_handler":  true,
		"requires_mutation_arming":      true,
		"runtime_boundary_redacted":     true,
		"external_call_made":            false,
		"provider_api_call_made":        false,
		"provider_api_mutation":         "disabled",
		"request_body_included":         false,
		"response_body_included":        false,
		"headers_included":              false,
		"authorization_header_included": false,
		"provider_url_included":         false,
		"idempotency_key_included":      false,
		"contains_token":                false,
		"contains_provider_url":         false,
		"contains_repository_ref":       false,
		"contains_branch_name":          false,
		"contains_file_content":         false,
		"required_runtime_methods":      []string{"build_request", "send_provider_request", "handle_response", "record_attempt_transaction"},
		"blocked_reasons": []string{
			"provider_review_live_adapter_not_implemented",
			"provider_review_adapter_runtime_not_armed",
			"provider_review_mutation_not_armed",
		},
	}
}

func providerReviewAttemptAdapterRequestBuilderPlan(providerType, operationName, endpointKey string) map[string]any {
	providerType = safeProviderReviewProviderType(providerType)
	operationName = safeProviderReviewAttemptOperationName(operationName)
	endpointKey = safeProviderReviewEndpointKey(endpointKey)
	builder := providerReviewAttemptRequestBuilderForOperation(operationName)
	if builder == nil || providerType == "" || endpointKey == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) {
		return map[string]any{}
	}
	return builder.BuildPlan(providerReviewAttemptRequestBuilderInput{
		ProviderType:  providerType,
		OperationName: operationName,
		EndpointKey:   endpointKey,
	})
}

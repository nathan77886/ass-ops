package app

import "context"

type providerReviewAttemptAdapterRuntime interface {
	AdapterKind() string
	SupportsOperation(operationName string) bool
	PrepareInvocation(context.Context, providerReviewAttemptAdapterRuntimeInput) providerReviewAttemptAdapterRuntimeResult
}

type providerReviewAttemptRequestBuilder interface {
	BuilderName() string
	BuildPlan(providerReviewAttemptRequestBuilderInput) map[string]any
}

type providerReviewAttemptProviderClientFactory interface {
	ClientKind() string
	BuildPlan(providerReviewAttemptProviderClientInput) map[string]any
}

type providerReviewAttemptResponseHandler interface {
	HandlerName() string
	BuildPlan(providerReviewAttemptResponseHandlerInput) map[string]any
}

type providerReviewAttemptAdapterRuntimeInput struct {
	ProviderType  string
	OperationName string
	EndpointKey   string
}

type providerReviewAttemptAdapterRuntimeResult struct {
	ProviderType       string
	AdapterKind        string
	OperationName      string
	EndpointKey        string
	OperationSupported bool
}

type providerReviewAttemptRequestBuilderInput struct {
	ProviderType  string
	OperationName string
	EndpointKey   string
}

type providerReviewAttemptProviderClientInput struct {
	ProviderType  string
	OperationName string
	EndpointKey   string
}

type providerReviewAttemptResponseHandlerInput struct {
	ProviderType  string
	OperationName string
	EndpointKey   string
}

type disabledProviderReviewAdapterRuntime struct {
	adapterKind string
}

type disabledProviderReviewAttemptRequestBuilder struct {
	builderName string
}

type disabledProviderReviewAttemptProviderClientFactory struct {
	clientKind string
}

type disabledProviderReviewAttemptResponseHandler struct {
	handlerName string
}

func (a disabledProviderReviewAdapterRuntime) AdapterKind() string {
	return a.adapterKind
}

func (a disabledProviderReviewAdapterRuntime) SupportsOperation(operationName string) bool {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref", "commit_starter_files", "open_review_request":
		return true
	default:
		return false
	}
}

func (a disabledProviderReviewAdapterRuntime) PrepareInvocation(_ context.Context, input providerReviewAttemptAdapterRuntimeInput) providerReviewAttemptAdapterRuntimeResult {
	operationName := safeProviderReviewAttemptOperationName(input.OperationName)
	endpointKey := safeProviderReviewEndpointKey(input.EndpointKey)
	return providerReviewAttemptAdapterRuntimeResult{
		ProviderType:       safeProviderReviewProviderType(input.ProviderType),
		AdapterKind:        a.AdapterKind(),
		OperationName:      operationName,
		EndpointKey:        endpointKey,
		OperationSupported: a.SupportsOperation(operationName) && providerReviewProviderFromEndpointKey(endpointKey) == safeProviderReviewProviderType(input.ProviderType),
	}
}

func (b disabledProviderReviewAttemptRequestBuilder) BuilderName() string {
	return b.builderName
}

func (b disabledProviderReviewAttemptRequestBuilder) BuildPlan(input providerReviewAttemptRequestBuilderInput) map[string]any {
	providerType := safeProviderReviewProviderType(input.ProviderType)
	operationName := safeProviderReviewAttemptOperationName(input.OperationName)
	endpointKey := safeProviderReviewEndpointKey(input.EndpointKey)
	endpointTemplateKey := providerReviewEndpointPathTemplateKeyForOperation(providerType, operationName)
	if providerType == "" || operationName == "" || endpointKey == "" || endpointTemplateKey == "" || providerReviewProviderFromEndpointKey(endpointKey) != providerType {
		return map[string]any{}
	}
	return map[string]any{
		"mode":                                 "redacted_attempt_adapter_request_builder_plan",
		"request_builder_state":                "blocked",
		"request_builder_ready":                false,
		"request_builder_ready_reason":         "provider_review_request_builder_not_armed",
		"provider_type":                        providerType,
		"operation_name":                       operationName,
		"endpoint_key":                         endpointKey,
		"builder_name":                         b.BuilderName(),
		"method":                               providerReviewMethodForOperation(operationName),
		"endpoint_path_template_key":           endpointTemplateKey,
		"payload_shape":                        providerReviewPayloadShapeForOperation(operationName),
		"requires_provider_repository_context": true,
		"requires_redacted_payload_summary":    true,
		"requires_starter_file_manifest":       operationName == "commit_starter_files",
		"builder_interface_registered":         true,
		"builder_registered":                   true,
		"builder_implemented":                  false,
		"provider_repository_context_resolved": false,
		"request_path_materialized":            false,
		"request_url_materialized":             false,
		"request_body_materialized":            false,
		"payload_materialized":                 false,
		"headers_materialized":                 false,
		"starter_file_manifest_materialized":   false,
		"authorization_header_materialized":    false,
		"request_builder_boundary_redacted":    true,
		"external_call_made":                   false,
		"provider_api_call_made":               false,
		"provider_api_mutation":                "disabled",
		"request_body_included":                false,
		"headers_included":                     false,
		"provider_url_included":                false,
		"repository_ref_included":              false,
		"branch_name_included":                 false,
		"file_content_included":                false,
		"idempotency_key_included":             false,
		"contains_token":                       false,
		"contains_provider_url":                false,
		"contains_repository_ref":              false,
		"contains_branch_name":                 false,
		"contains_file_content":                false,
		"blocked_reasons": []string{
			"provider_review_request_builder_not_armed",
			"provider_review_live_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
	}
}

func (f disabledProviderReviewAttemptProviderClientFactory) ClientKind() string {
	return f.clientKind
}

func (f disabledProviderReviewAttemptProviderClientFactory) BuildPlan(input providerReviewAttemptProviderClientInput) map[string]any {
	providerType := safeProviderReviewProviderType(input.ProviderType)
	operationName := safeProviderReviewAttemptOperationName(input.OperationName)
	endpointKey := safeProviderReviewEndpointKey(input.EndpointKey)
	authScheme := providerReviewAuthSchemeForProvider(providerType)
	if providerType == "" || operationName == "" || endpointKey == "" || authScheme == "" || providerReviewProviderFromEndpointKey(endpointKey) != providerType {
		return map[string]any{}
	}
	return map[string]any{
		"mode":                                "redacted_attempt_adapter_provider_client_plan",
		"provider_client_state":               "blocked",
		"provider_client_ready":               false,
		"provider_client_ready_reason":        "provider_review_provider_client_not_armed",
		"provider_type":                       providerType,
		"operation_name":                      operationName,
		"endpoint_key":                        endpointKey,
		"client_kind":                         f.ClientKind(),
		"auth_scheme":                         authScheme,
		"base_url_source":                     "provider_account_api_base_url",
		"credential_source_kind":              "provider_account_token_env",
		"timeout_seconds":                     15,
		"retry_policy":                        "retry_5xx_with_backoff",
		"required_capabilities":               providerReviewClientRequiredCapabilitiesForOperation(operationName),
		"client_factory_interface_registered": true,
		"client_factory_registered":           true,
		"client_implemented":                  false,
		"provider_client_constructed":         false,
		"provider_account_resolved":           false,
		"base_url_validated":                  false,
		"base_url_materialized":               false,
		"token_env_allowed":                   false,
		"runtime_token_loaded":                false,
		"authorization_header_materialized":   false,
		"http_client_configured":              false,
		"provider_client_boundary_redacted":   true,
		"external_call_made":                  false,
		"provider_api_call_made":              false,
		"provider_api_mutation":               "disabled",
		"base_url_included":                   false,
		"token_env_name_included":             false,
		"token_value_included":                false,
		"authorization_header_included":       false,
		"provider_url_included":               false,
		"request_body_included":               false,
		"response_body_included":              false,
		"headers_included":                    false,
		"contains_token":                      false,
		"contains_provider_url":               false,
		"contains_repository_ref":             false,
		"contains_branch_name":                false,
		"contains_file_content":               false,
		"blocked_reasons": []string{
			"provider_review_provider_client_not_armed",
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
	if providerType == "" || operationName == "" || endpointKey == "" || providerReviewProviderFromEndpointKey(endpointKey) != providerType {
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
		return disabledProviderReviewAttemptRequestBuilder{builderName: "build_redacted_commit_files_request"}
	case "open_review_request":
		return disabledProviderReviewAttemptRequestBuilder{builderName: "build_redacted_review_request"}
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
	if builder == nil || providerType == "" || endpointKey == "" || providerReviewProviderFromEndpointKey(endpointKey) != providerType {
		return map[string]any{}
	}
	return builder.BuildPlan(providerReviewAttemptRequestBuilderInput{
		ProviderType:  providerType,
		OperationName: operationName,
		EndpointKey:   endpointKey,
	})
}

func providerReviewAttemptAdapterProviderClientPlan(providerType, operationName, endpointKey string) map[string]any {
	providerType = safeProviderReviewProviderType(providerType)
	operationName = safeProviderReviewAttemptOperationName(operationName)
	endpointKey = safeProviderReviewEndpointKey(endpointKey)
	factory := providerReviewAttemptProviderClientFactoryForProvider(providerType)
	if factory == nil || operationName == "" || endpointKey == "" || providerReviewProviderFromEndpointKey(endpointKey) != providerType {
		return map[string]any{}
	}
	return factory.BuildPlan(providerReviewAttemptProviderClientInput{
		ProviderType:  providerType,
		OperationName: operationName,
		EndpointKey:   endpointKey,
	})
}

func providerReviewClientRequiredCapabilitiesForOperation(operationName string) []string {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref":
		return []string{"repository_ref_write"}
	case "commit_starter_files":
		return []string{"repository_contents_write"}
	case "open_review_request":
		return []string{"review_request_write"}
	default:
		return []string{}
	}
}

func providerReviewAttemptAdapterResponseHandlerPlan(providerType, operationName, endpointKey string) map[string]any {
	providerType = safeProviderReviewProviderType(providerType)
	operationName = safeProviderReviewAttemptOperationName(operationName)
	endpointKey = safeProviderReviewEndpointKey(endpointKey)
	handler := providerReviewAttemptResponseHandlerForOperation(operationName)
	if handler == nil || providerType == "" || endpointKey == "" || providerReviewProviderFromEndpointKey(endpointKey) != providerType {
		return map[string]any{}
	}
	return handler.BuildPlan(providerReviewAttemptResponseHandlerInput{
		ProviderType:  providerType,
		OperationName: operationName,
		EndpointKey:   endpointKey,
	})
}

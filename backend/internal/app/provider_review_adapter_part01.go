package app

import (
	"context"
)

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

type providerReviewAttemptExecuteMethod interface {
	MethodName() string
	BuildPlan(providerReviewAttemptExecuteMethodInput) map[string]any
}

type providerReviewAttemptResponseHandler interface {
	HandlerName() string
	BuildPlan(providerReviewAttemptResponseHandlerInput) map[string]any
}

type providerReviewAttemptLiveAdapter interface {
	AdapterName() string
	BuildPlan(providerReviewAttemptLiveAdapterInput) map[string]any
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

type providerReviewAttemptExecuteMethodInput struct {
	ProviderType  string
	OperationName string
	EndpointKey   string
}

type providerReviewAttemptResponseHandlerInput struct {
	ProviderType  string
	OperationName string
	EndpointKey   string
}

type providerReviewAttemptLiveAdapterInput struct {
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

type disabledProviderReviewAttemptExecuteMethod struct {
	methodName string
}

type disabledProviderReviewAttemptResponseHandler struct {
	handlerName string
}

type disabledProviderReviewAttemptLiveAdapter struct {
	adapterName string
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
		OperationSupported: a.SupportsOperation(operationName) && providerReviewAttemptEndpointMatchesOperation(input.ProviderType, operationName, endpointKey),
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
	if providerType == "" || operationName == "" || endpointKey == "" || endpointTemplateKey == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) {
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
	if providerType == "" || operationName == "" || endpointKey == "" || authScheme == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) {
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

func (m disabledProviderReviewAttemptExecuteMethod) MethodName() string {
	return m.methodName
}

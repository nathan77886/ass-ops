package app

func providerReviewAttemptAdapterExecuteMethodPlan(providerType, operationName, endpointKey string) map[string]any {
	providerType = safeProviderReviewProviderType(providerType)
	operationName = safeProviderReviewAttemptOperationName(operationName)
	endpointKey = safeProviderReviewEndpointKey(endpointKey)
	method := providerReviewAttemptExecuteMethodForOperation(operationName)
	if method == nil || providerType == "" || endpointKey == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) {
		return map[string]any{}
	}
	return method.BuildPlan(providerReviewAttemptExecuteMethodInput{
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
	if factory == nil || operationName == "" || endpointKey == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) {
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
	if handler == nil || providerType == "" || endpointKey == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) {
		return map[string]any{}
	}
	return handler.BuildPlan(providerReviewAttemptResponseHandlerInput{
		ProviderType:  providerType,
		OperationName: operationName,
		EndpointKey:   endpointKey,
	})
}

func providerReviewAttemptAdapterRequestEnvelopePlan(providerType, operationName, endpointKey string, requestPlan, branchPolicyPlan, credentialPlan, transportPlan map[string]any) map[string]any {
	providerType = safeProviderReviewProviderType(providerType)
	operationName = safeProviderReviewAttemptOperationName(operationName)
	endpointKey = safeProviderReviewEndpointKey(endpointKey)
	if providerType == "" || operationName == "" || endpointKey == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) {
		return map[string]any{}
	}
	requestContractReady := providerReviewAttemptPlanMatchesOperation(requestPlan, providerReviewAttemptAdapterRequestMaterializationPlanMode, operationName, endpointKey)
	requestMaterializationReady := providerReviewAttemptRequestPlanReadyForOperation(requestPlan, operationName, endpointKey)
	branchPolicyContractReady := providerReviewAttemptPlanMatchesOperation(branchPolicyPlan, "redacted_attempt_branch_policy_plan", operationName, endpointKey)
	branchPolicyMetadataReady := providerReviewAttemptBranchPolicyPlanReadyForOperation(branchPolicyPlan, operationName, endpointKey)
	credentialContractReady := providerReviewAttemptPlanMatchesOperation(credentialPlan, "redacted_attempt_adapter_credential_binding_plan", operationName, endpointKey)
	credentialBindingReady := providerReviewAttemptCredentialPlanReadyForOperation(credentialPlan, operationName, endpointKey)
	transportContractReady := providerReviewAttemptPlanMatchesOperation(transportPlan, "redacted_attempt_adapter_transport_plan", operationName, endpointKey)
	transportMetadataReady := providerReviewAttemptTransportPlanReadyForOperation(transportPlan, operationName, endpointKey)
	contractReady := requestContractReady && branchPolicyContractReady && credentialContractReady && transportContractReady
	return map[string]any{
		"mode":                                   "redacted_attempt_adapter_request_envelope_plan",
		"envelope_state":                         "blocked",
		"envelope_ready":                         false,
		"envelope_ready_reason":                  "provider_review_request_envelope_not_armed",
		"envelope_contract_ready":                contractReady,
		"envelope_metadata_ready":                false,
		"operation_name":                         operationName,
		"endpoint_key":                           endpointKey,
		"provider_type":                          providerType,
		"method":                                 providerReviewMethodForOperation(operationName),
		"payload_shape":                          providerReviewPayloadShapeForOperation(operationName),
		"payload_builder":                        safeProviderReviewPayloadBuilderName(stringFromMap(requestPlan, "payload_builder")),
		"endpoint_path_template_key":             cleanOptionalText(stringFromMap(requestPlan, "endpoint_path_template_key")),
		"auth_scheme":                            providerReviewAuthSchemeForProvider(providerType),
		"request_materialization_contract_ready": requestContractReady,
		"request_materialization_ready":          requestMaterializationReady,
		"branch_policy_contract_ready":           branchPolicyContractReady,
		"branch_policy_metadata_ready":           branchPolicyMetadataReady,
		"credential_binding_contract_ready":      credentialContractReady,
		"credential_binding_ready":               credentialBindingReady,
		"transport_contract_ready":               transportContractReady,
		"transport_metadata_ready":               transportMetadataReady,
		"branch_safety_summary":                  sanitizedProviderReviewAttemptBranchSafetySummary(mapFromAny(branchPolicyPlan["branch_safety_summary"])),
		"requires_request_materialization":       true,
		"requires_branch_policy":                 true,
		"requires_credential_binding":            true,
		"requires_transport_metadata":            true,
		"requires_mutation_arming":               true,
		"request_path_materialized":              false,
		"request_url_materialized":               false,
		"request_body_materialized":              false,
		"headers_materialized":                   false,
		"authorization_header_materialized":      false,
		"idempotency_metadata_materialized":      false,
		"protected_branch_policy_verified":       false,
		"token_env_bound":                        false,
		"provider_request_sent":                  false,
		"external_call_made":                     false,
		"provider_api_call_made":                 false,
		"provider_api_mutation":                  "disabled",
		"request_body_included":                  false,
		"headers_included":                       false,
		"authorization_header_included":          false,
		"provider_url_included":                  false,
		"repository_ref_included":                false,
		"branch_name_included":                   false,
		"file_content_included":                  false,
		"idempotency_key_included":               false,
		"contains_token":                         false,
		"contains_provider_url":                  false,
		"contains_repository_ref":                false,
		"contains_branch_name":                   false,
		"contains_file_content":                  false,
		"request_envelope_boundary_redacted":     true,
		"request_envelope_sequence":              []string{"verify_request_materialization", "verify_branch_policy", "bind_credential_metadata", "verify_transport_metadata", "stage_redacted_request_envelope"},
		"request_envelope_suppressed_fields":     []string{"provider_url", "authorization_header", "token", "request_body", "request_headers", "repository_ref", "branch_name", "file_content", "idempotency_key"},
		"blocked_reasons": []string{
			"provider_review_request_envelope_not_armed",
			"provider_request_not_materialized",
			"provider_credential_runtime_binding_not_armed",
			"provider_review_mutation_not_armed",
		},
	}
}

func sanitizedProviderReviewAttemptBranchSafetySummary(summary map[string]any) map[string]any {
	if len(summary) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(summary, "operation_name"))
	if operationName == "" {
		return map[string]any{}
	}
	return providerReviewAttemptBranchSafetySummary(operationName)
}

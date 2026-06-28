package app

func providerReviewAttemptCandidateAdapterContract(operation, requestSummary, responseDiagnostics map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"mode":                            "redacted_attempt_adapter_contract",
		"operation_name":                  safeProviderReviewAttemptOperationName(stringFromMap(operation, "name")),
		"endpoint_key":                    safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key")),
		"operation_order":                 intFromAny(operation["operation_order"], 0),
		"payload_builder":                 safeProviderReviewPayloadBuilderName(stringFromMap(requestSummary, "payload_builder")),
		"response_handler":                safeProviderReviewResponseHandlerName(stringFromMap(requestSummary, "response_handler")),
		"idempotency_key_kind":            "operation_scope_hash",
		"response_status":                 safeProviderReviewAttemptResponseStatus(stringFromMap(responseDiagnostics, "status")),
		"success_status_class":            safeProviderReviewStatusClass(stringFromMap(responseDiagnostics, "success_status_class")),
		"retryable_status_classes":        safeProviderReviewStatusClasses(stringSliceFromAny(responseDiagnostics["retryable_status_classes"])),
		"adapter_call_state":              "blocked",
		"requires_provider_client":        true,
		"requires_request_builder":        true,
		"requires_response_handler":       true,
		"requires_idempotency_ledger":     true,
		"requires_response_diagnostics":   true,
		"requires_mutation_arming":        true,
		"adapter_implemented":             false,
		"mutation_armed":                  false,
		"request_body_included":           false,
		"response_body_included":          false,
		"headers_included":                false,
		"idempotency_key_included":        false,
		"external_call_made":              false,
		"provider_api_call_made":          false,
		"provider_api_mutation":           "disabled",
		"contains_token":                  false,
		"contains_provider_url":           false,
		"contains_repository_ref":         false,
		"contains_branch_name":            false,
		"contains_file_content":           false,
		"activation_requirements":         []string{"provider_api_adapter_implemented", "provider_review_mutation_armed", "operator_approval_still_valid", "idempotency_ledger_claim"},
		"adapter_input_boundary_redacted": true,
	}
}

func providerReviewAttemptAdapterDispatchPlan(operation, requestSummary, responseDiagnostics, adapterContract, claimPlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	providerType := providerReviewProviderFromEndpointKey(endpointKey)
	claimMetadataReady := providerReviewAttemptClaimPlanReadyForOperation(claimPlan, operationName, endpointKey)
	idempotencyMetadataReady := providerReviewAttemptClaimPlanIdempotencyReadyForOperation(claimPlan, operationName, endpointKey)
	adapterContractReady := providerReviewAttemptPlanMatchesOperation(adapterContract, "redacted_attempt_adapter_contract", operationName, endpointKey)
	metadataReady := claimMetadataReady &&
		adapterContractReady &&
		operationName != "" &&
		endpointKey != "" &&
		providerType != ""
	blockedReasons := []string{
		"provider_review_attempt_claim_not_recorded",
		"provider_review_adapter_not_implemented",
		"provider_review_mutation_not_armed",
	}
	if !metadataReady {
		blockedReasons = append([]string{"provider_review_dispatch_metadata_not_ready"}, blockedReasons...)
	}
	if providerType == "" {
		blockedReasons = append([]string{"provider_review_dispatch_provider_unknown"}, blockedReasons...)
	}
	requestPlan := providerReviewAttemptAdapterRequestMaterializationPlan(operation, requestSummary, providerType)
	transportPlan := providerReviewAttemptAdapterTransportPlan(providerType, operationName)
	responsePlan := providerReviewAttemptAdapterResponsePlan(operation, requestSummary, responseDiagnostics)
	credentialPlan := providerReviewAttemptAdapterCredentialBindingPlan(providerType, operationName)
	runtimePlan := providerReviewAttemptAdapterRuntimePlan(providerType, operationName, endpointKey)
	transactionPlan := providerReviewAttemptAdapterTransactionPlan(operation, claimPlan, responsePlan)
	branchPolicyPlan := providerReviewAttemptBranchPolicyPlan(operation, requestPlan)
	requestEnvelopePlan := providerReviewAttemptAdapterRequestEnvelopePlan(providerType, operationName, endpointKey, requestPlan, branchPolicyPlan, credentialPlan, transportPlan)
	requestValidationPreflight := map[string]any{}
	if operationName != "" && endpointKey != "" && providerType != "" {
		requestReady := providerReviewAttemptRequestPlanReadyForOperation(requestPlan, operationName, endpointKey)
		branchPolicyReady := providerReviewAttemptBranchPolicyPlanReadyForOperation(branchPolicyPlan, operationName, endpointKey)
		credentialReady := providerReviewAttemptCredentialPlanReadyForOperation(credentialPlan, operationName, endpointKey)
		transportReady := providerReviewAttemptTransportPlanReadyForOperation(transportPlan, operationName, endpointKey)
		requestEnvelopeContractReady := providerReviewAttemptPlanMatchesOperation(requestEnvelopePlan, "redacted_attempt_adapter_request_envelope_plan", operationName, endpointKey) && boolOnlyFromAny(requestEnvelopePlan["envelope_contract_ready"])
		responseReady := providerReviewAttemptResponseRecordingReadyForOperation(responsePlan, operationName, endpointKey)
		transactionReady := providerReviewAttemptTransactionPlanReadyForOperation(transactionPlan, operationName, endpointKey)
		requestValidationPreflight = map[string]any{
			"mode":                                "redacted_attempt_adapter_request_validation_preflight",
			"preflight_state":                     "blocked",
			"preflight_ready":                     false,
			"preflight_ready_reason":              "provider_review_request_validation_not_armed",
			"operation_name":                      operationName,
			"endpoint_key":                        endpointKey,
			"operation_order":                     intFromAny(operation["operation_order"], 0),
			"provider_type":                       providerType,
			"dispatch_metadata_ready":             metadataReady,
			"attempt_claim_metadata_ready":        claimMetadataReady,
			"idempotency_metadata_ready":          idempotencyMetadataReady,
			"request_materialization_ready":       requestReady,
			"branch_policy_metadata_ready":        branchPolicyReady,
			"credential_binding_ready":            credentialReady,
			"transport_metadata_ready":            transportReady,
			"request_envelope_contract_ready":     requestEnvelopeContractReady,
			"request_envelope_metadata_ready":     false,
			"response_recording_ready":            responseReady,
			"transaction_metadata_ready":          transactionReady,
			"protected_branch_policy_check":       false,
			"token_env_check":                     false,
			"request_validated":                   false,
			"request_body_included":               false,
			"headers_included":                    false,
			"authorization_header_included":       false,
			"provider_url_included":               false,
			"repository_ref_included":             false,
			"branch_name_included":                false,
			"file_content_included":               false,
			"external_call_made":                  false,
			"provider_api_call_made":              false,
			"provider_api_mutation":               "disabled",
			"contains_token":                      false,
			"contains_provider_url":               false,
			"contains_repository_ref":             false,
			"contains_branch_name":                false,
			"contains_file_content":               false,
			"preflight_boundary_redacted":         true,
			"requires_request_materialization":    true,
			"requires_branch_policy_verification": true,
			"requires_credential_binding":         true,
			"requires_transport_metadata":         true,
			"requires_response_recording":         true,
			"requires_transaction_boundary":       true,
			"requires_mutation_arming":            true,
			"blocked_reasons": []string{
				"provider_review_request_validation_not_armed",
				"provider_review_adapter_not_implemented",
				"provider_review_mutation_not_armed",
			},
		}
	}
	return map[string]any{
		"mode":                         "redacted_attempt_adapter_dispatch_plan",
		"dispatch_state":               "blocked",
		"dispatch_ready":               false,
		"dispatch_ready_reason":        "provider_api_adapter_dispatch_not_armed",
		"dispatch_metadata_ready":      metadataReady,
		"attempt_claim_metadata_ready": claimMetadataReady,
		"adapter_contract_ready":       adapterContractReady,
		"provider_type":                providerType,
		"adapter_kind":                 providerReviewAdapterKindForProvider(providerType),
		"operation_name":               operationName,
		"endpoint_key":                 endpointKey,
		"operation_order":              intFromAny(operation["operation_order"], 0),
		"method":                       providerReviewMethodForOperation(operationName),
		"payload_shape":                providerReviewPayloadShapeForOperation(operationName),
		"payload_builder":              safeProviderReviewPayloadBuilderName(stringFromMap(requestSummary, "payload_builder")),
		"response_handler":             safeProviderReviewResponseHandlerName(stringFromMap(requestSummary, "response_handler")),
		"request_materialization_plan": requestPlan,
		"transport_plan":               transportPlan,
		"response_plan":                responsePlan,
		"credential_binding_plan":      credentialPlan,
		"adapter_runtime_plan":         runtimePlan,
		"branch_policy_plan":           branchPolicyPlan,
		"request_envelope_plan":        requestEnvelopePlan,
		"transaction_plan":             transactionPlan,
		"request_validation_preflight": requestValidationPreflight,
		"invocation_plan":              providerReviewAttemptAdapterInvocationPlan(operation, claimPlan, requestPlan, credentialPlan, runtimePlan, branchPolicyPlan, transportPlan, responsePlan, transactionPlan),
		"idempotency_key_kind":         "operation_scope_hash",
		"requires_attempt_claim":       true,
		"requires_idempotency_claim":   true,
		"requires_provider_client":     true,
		"requires_request_builder":     true,
		"requires_response_handler":    true,
		"requires_mutation_arming":     true,
		"claim_recorded":               false,
		"idempotency_claim_recorded":   false,
		"adapter_implemented":          false,
		"mutation_armed":               false,
		"external_call_made":           false,
		"provider_api_call_made":       false,
		"provider_api_mutation":        "disabled",
		"request_body_included":        false,
		"response_body_included":       false,
		"headers_included":             false,
		"idempotency_key_included":     false,
		"contains_token":               false,
		"contains_provider_url":        false,
		"contains_repository_ref":      false,
		"contains_branch_name":         false,
		"contains_file_content":        false,
		"blocked_reasons":              blockedReasons,
		"dispatch_boundary_redacted":   true,
		"provider_request_id_included": false,
	}
}

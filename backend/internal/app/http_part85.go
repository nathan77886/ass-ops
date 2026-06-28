package app

func providerReviewAttemptAdapterActivationPlan(operation, claimPlan, executionLockPlan, credentialPlan, runtimePlan, requestPlan, transportPlan, providerSendPlan, responsePlan, transactionPlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	if operationName == "" || endpointKey == "" {
		return map[string]any{}
	}
	providerType := providerReviewProviderFromEndpointKey(endpointKey)
	if providerType == "" {
		return map[string]any{}
	}
	liveAdapterPlan := providerReviewAttemptLiveAdapterPlan(providerType, operationName, endpointKey)
	claimReady := providerReviewAttemptClaimPlanReadyForOperation(claimPlan, operationName, endpointKey)
	executionLockReady := providerReviewAttemptExecutionLockPlanReadyForOperation(executionLockPlan, operationName, endpointKey)
	credentialReady := providerReviewAttemptCredentialPlanReadyForOperation(credentialPlan, operationName, endpointKey)
	runtimeReady := providerReviewAttemptRuntimePlanReadyForOperation(runtimePlan, operationName, endpointKey)
	requestReady := providerReviewAttemptRequestPlanReadyForOperation(requestPlan, operationName, endpointKey)
	transportReady := providerReviewAttemptTransportPlanReadyForOperation(transportPlan, operationName, endpointKey)
	providerSendReady := providerReviewAttemptProviderSendPlanReadyForOperation(providerSendPlan, operationName, endpointKey)
	responseReady := providerReviewAttemptResponseRecordingReadyForOperation(responsePlan, operationName, endpointKey)
	transactionReady := providerReviewAttemptTransactionPlanReadyForOperation(transactionPlan, operationName, endpointKey)
	metadataReady := claimReady &&
		executionLockReady &&
		credentialReady &&
		runtimeReady &&
		requestReady &&
		transportReady &&
		providerSendReady &&
		responseReady &&
		transactionReady
	return map[string]any{
		"mode":                                      "redacted_attempt_adapter_activation_plan",
		"adapter_activation_state":                  "blocked",
		"adapter_activation_ready":                  false,
		"adapter_activation_ready_reason":           "provider_review_adapter_activation_not_armed",
		"adapter_activation_metadata_ready":         metadataReady,
		"adapter_activation_metadata_ready_reason":  providerReviewAttemptAdapterActivationMetadataReadyReason(claimReady, executionLockReady, credentialReady, runtimeReady, requestReady, transportReady, providerSendReady, responseReady, transactionReady),
		"operation_name":                            operationName,
		"endpoint_key":                              endpointKey,
		"operation_order":                           intFromAny(operation["operation_order"], 0),
		"live_adapter_plan":                         liveAdapterPlan,
		"activation_scope":                          "provider_review_attempt_operation",
		"activation_policy":                         "require_all_redacted_subplans_and_mutation_gate",
		"requires_live_adapter":                     true,
		"requires_attempt_claim":                    true,
		"requires_execution_lock":                   true,
		"requires_credential_binding":               true,
		"requires_adapter_runtime":                  true,
		"requires_request_materialization":          true,
		"requires_transport":                        true,
		"requires_provider_send_plan":               true,
		"requires_response_recording":               true,
		"requires_transaction_boundary":             true,
		"requires_mutation_arming":                  true,
		"claim_metadata_ready":                      claimReady,
		"execution_lock_metadata_ready":             executionLockReady,
		"credential_binding_ready":                  credentialReady,
		"adapter_runtime_ready":                     runtimeReady,
		"request_materialization_ready":             requestReady,
		"transport_metadata_ready":                  transportReady,
		"provider_send_metadata_ready":              providerSendReady,
		"response_recording_ready":                  responseReady,
		"transaction_metadata_ready":                transactionReady,
		"live_adapter_registered":                   boolOnlyFromAny(liveAdapterPlan["live_adapter_registered"]),
		"adapter_implemented":                       false,
		"live_adapter_implemented":                  boolOnlyFromAny(liveAdapterPlan["live_adapter_implemented"]),
		"adapter_activation_approved":               false,
		"mutation_gate_armed":                       false,
		"provider_request_sent":                     false,
		"external_call_made":                        false,
		"provider_api_call_made":                    false,
		"provider_api_mutation":                     "disabled",
		"request_body_included":                     false,
		"response_body_included":                    false,
		"headers_included":                          false,
		"authorization_header_included":             false,
		"provider_url_included":                     false,
		"idempotency_key_included":                  false,
		"provider_request_id_included":              false,
		"contains_token":                            false,
		"contains_provider_url":                     false,
		"contains_repository_ref":                   false,
		"contains_branch_name":                      false,
		"contains_file_content":                     false,
		"adapter_activation_boundary_redacted":      true,
		"adapter_activation_sequence":               []string{"verify_live_adapter_registry", "verify_claim_metadata", "verify_execution_lock_metadata", "verify_credential_binding", "verify_runtime_contract", "verify_request_materialization", "verify_transport_contract", "verify_provider_send_contract", "verify_response_recording", "verify_transaction_boundary", "verify_mutation_arming"},
		"adapter_activation_suppressed_fields":      []string{"provider_url", "authorization_header", "token", "request_body", "response_body", "repository_ref", "branch_name", "file_content", "idempotency_key", "lock_key"},
		"adapter_activation_required_config_gates":  []string{"ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION", "ASSOPS_ARM_PROVIDER_REVIEW_MUTATION"},
		"adapter_activation_required_interfaces":    []string{"providerReviewAttemptLiveAdapter", "providerReviewAttemptAdapterRuntime", "providerReviewAttemptRequestBuilder", "providerReviewAttemptProviderClientFactory", "providerReviewAttemptExecuteMethod", "providerReviewAttemptResponseHandler"},
		"adapter_activation_required_capabilities":  providerReviewClientRequiredCapabilitiesForOperation(operationName),
		"adapter_activation_required_status_inputs": []string{"claim_metadata_ready", "execution_lock_metadata_ready", "credential_binding_ready", "runtime_ready", "request_materialization_ready", "transport_ready", "provider_send_metadata_ready", "response_recording_ready", "transaction_metadata_ready"},
		"blocked_reasons": []string{
			"provider_review_adapter_activation_not_armed",
			"provider_review_activation_metadata_not_ready",
			"provider_review_live_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
	}
}

func providerReviewAttemptAdapterActivationMetadataReadyReason(claimReady, executionLockReady, credentialReady, runtimeReady, requestReady, transportReady, providerSendReady, responseReady, transactionReady bool) string {
	switch {
	case !claimReady:
		return "provider_review_activation_claim_metadata_not_ready"
	case !executionLockReady:
		return "provider_review_activation_execution_lock_not_ready"
	case !credentialReady:
		return "provider_review_activation_credential_binding_not_ready"
	case !runtimeReady:
		return "provider_review_activation_adapter_runtime_not_ready"
	case !requestReady:
		return "provider_review_activation_request_materialization_not_ready"
	case !transportReady:
		return "provider_review_activation_transport_not_ready"
	case !providerSendReady:
		return "provider_review_activation_provider_send_not_ready"
	case !responseReady:
		return "provider_review_activation_response_recording_not_ready"
	case !transactionReady:
		return "provider_review_activation_transaction_not_ready"
	default:
		return "ready"
	}
}

func providerReviewAttemptAdapterExecutionLockPlan(operation, claimPlan, transactionPlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	if operationName == "" || endpointKey == "" {
		return map[string]any{}
	}
	claimMetadataReady := providerReviewAttemptClaimPlanReadyForOperation(claimPlan, operationName, endpointKey)
	transactionMetadataReady := providerReviewAttemptTransactionPlanReadyForOperation(transactionPlan, operationName, endpointKey)
	metadataReady := claimMetadataReady && transactionMetadataReady
	return map[string]any{
		"mode":                                  "redacted_attempt_adapter_execution_lock_plan",
		"execution_lock_state":                  "blocked",
		"execution_lock_ready":                  false,
		"execution_lock_ready_reason":           "provider_review_execution_lock_not_armed",
		"execution_lock_metadata_ready":         metadataReady,
		"execution_lock_metadata_ready_reason":  providerReviewAttemptExecutionLockMetadataReadyReason(claimMetadataReady, transactionMetadataReady),
		"operation_name":                        operationName,
		"endpoint_key":                          endpointKey,
		"operation_order":                       intFromAny(operation["operation_order"], 0),
		"claim_status_from":                     "planned",
		"claim_status_to":                       "running",
		"lock_scope":                            "provider_review_attempt_operation",
		"lock_key_kind":                         "attempt_operation_hash",
		"duplicate_send_policy":                 "block_duplicate_provider_send",
		"stale_running_policy":                  "manual_recovery_required",
		"requires_database_transaction":         true,
		"requires_attempt_claim":                true,
		"requires_attempt_status_planned":       true,
		"requires_dependency_ready":             true,
		"requires_optimistic_lock":              true,
		"requires_idempotency_claim":            true,
		"requires_mutation_arming":              true,
		"claim_metadata_ready":                  claimMetadataReady,
		"transaction_metadata_ready":            transactionMetadataReady,
		"attempt_claim_recorded":                false,
		"idempotency_claim_recorded":            false,
		"execution_lock_acquired":               false,
		"optimistic_lock_verified":              false,
		"duplicate_send_detected":               false,
		"stale_running_recovered":               false,
		"provider_request_sent":                 false,
		"external_call_made":                    false,
		"provider_api_call_made":                false,
		"provider_api_mutation":                 "disabled",
		"request_body_included":                 false,
		"response_body_included":                false,
		"headers_included":                      false,
		"authorization_header_included":         false,
		"provider_url_included":                 false,
		"idempotency_key_included":              false,
		"provider_request_id_included":          false,
		"contains_token":                        false,
		"contains_provider_url":                 false,
		"contains_repository_ref":               false,
		"contains_branch_name":                  false,
		"contains_file_content":                 false,
		"execution_lock_boundary_redacted":      true,
		"execution_lock_sequence":               []string{"verify_attempt_status_planned", "verify_dependency_ready", "claim_attempt_running", "claim_idempotency_scope", "mark_duplicate_send_guard", "release_lock_after_transaction"},
		"execution_lock_suppressed_fields":      []string{"lock_key", "idempotency_key", "provider_request_id", "provider_url", "authorization_header", "token", "repository_ref", "branch_name", "file_content"},
		"execution_lock_transaction_boundaries": []string{"claim_attempt_start", "duplicate_send_guard", "provider_call_boundary", "attempt_status_update"},
		"blocked_reasons": []string{
			"provider_review_execution_lock_not_armed",
			"provider_review_attempt_claim_not_recorded",
			"provider_idempotency_ledger_not_claimed",
			"provider_review_mutation_not_armed",
		},
	}
}

func providerReviewAttemptExecutionLockMetadataReadyReason(claimMetadataReady, transactionMetadataReady bool) string {
	if !claimMetadataReady {
		return "provider_review_execution_lock_claim_metadata_not_ready"
	}
	if !transactionMetadataReady {
		return "provider_review_execution_lock_transaction_metadata_not_ready"
	}
	return "ready"
}

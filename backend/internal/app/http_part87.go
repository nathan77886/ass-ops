package app

func providerReviewAttemptAdapterProviderCallBoundaryPlan(operation, claimPlan, responsePlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	if operationName == "" || endpointKey == "" {
		return map[string]any{}
	}
	metadataReady := providerReviewAttemptClaimPlanReadyForOperation(claimPlan, operationName, endpointKey) &&
		providerReviewAttemptResponsePlanReadyForOperation(responsePlan, operationName, endpointKey)
	return map[string]any{
		"mode":                                  "redacted_attempt_adapter_provider_call_boundary_plan",
		"provider_call_boundary_state":          "blocked",
		"provider_call_boundary_ready":          false,
		"provider_call_boundary_ready_reason":   "provider_review_provider_call_boundary_not_armed",
		"provider_call_boundary_metadata_ready": metadataReady,
		"operation_name":                        operationName,
		"endpoint_key":                          endpointKey,
		"operation_order":                       intFromAny(operation["operation_order"], 0),
		"idempotency_key_kind":                  "operation_scope_hash",
		"requires_database_transaction":         true,
		"requires_attempt_claim":                true,
		"requires_idempotency_claim":            true,
		"requires_response_diagnostics":         true,
		"requires_mutation_arming":              true,
		"transaction_opened":                    false,
		"attempt_claim_verified":                false,
		"idempotency_claim_verified":            false,
		"provider_call_boundary_opened":         false,
		"provider_call_boundary_recorded":       false,
		"provider_call_started_recorded":        false,
		"provider_call_finished_recorded":       false,
		"provider_request_sent":                 false,
		"provider_response_received":            false,
		"provider_request_id_recorded":          false,
		"provider_response_status_recorded":     false,
		"provider_response_body_recorded":       false,
		"provider_response_headers_recorded":    false,
		"provider_call_boundary_redacted":       true,
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
		"boundary_sequence": []string{
			"verify_attempt_claim",
			"verify_idempotency_claim",
			"open_database_transaction",
			"record_provider_call_started",
			"stage_provider_request_send",
			"record_provider_call_finished",
			"commit_database_transaction",
		},
		"blocked_reasons": []string{
			"provider_review_provider_call_boundary_not_armed",
			"provider_api_call_not_made",
			"provider_review_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
	}
}

const providerReviewAttemptAdapterRequestMaterializationPlanMode = "redacted_attempt_adapter_request_materialization_plan"

func providerReviewAttemptAdapterRequestMaterializationPlan(operation, requestSummary map[string]any, providerType string) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	providerType = safeProviderReviewProviderType(providerType)
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	endpointTemplateKey := providerReviewEndpointPathTemplateKeyForOperation(providerType, operationName)
	payloadBuilder := safeProviderReviewPayloadBuilderName(stringFromMap(requestSummary, "payload_builder"))
	if providerType == "" || operationName == "" || endpointKey == "" || endpointTemplateKey == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) || !providerReviewAttemptPayloadBuilderMatchesOperation(operationName, payloadBuilder) {
		return map[string]any{}
	}
	return map[string]any{
		"mode":                                      providerReviewAttemptAdapterRequestMaterializationPlanMode,
		"request_materialization_state":             "blocked",
		"request_materialization_ready":             false,
		"request_materialization_ready_reason":      "provider_request_materialization_not_armed",
		"provider_type":                             providerType,
		"operation_name":                            operationName,
		"endpoint_key":                              endpointKey,
		"operation_order":                           intFromAny(operation["operation_order"], 0),
		"method":                                    providerReviewMethodForOperation(operationName),
		"endpoint_path_template_key":                endpointTemplateKey,
		"payload_shape":                             providerReviewPayloadShapeForOperation(operationName),
		"payload_builder":                           payloadBuilder,
		"requires_request_builder":                  true,
		"requires_provider_repository_context":      true,
		"requires_redacted_payload_summary":         true,
		"requires_starter_file_manifest":            operationName == "commit_starter_files",
		"requires_mutation_arming":                  true,
		"request_builder_implemented":               false,
		"provider_repository_context_resolved":      false,
		"request_path_materialized":                 false,
		"request_url_materialized":                  false,
		"request_body_materialized":                 false,
		"payload_materialized":                      false,
		"headers_materialized":                      false,
		"starter_file_manifest_materialized":        false,
		"authorization_header_materialized":         false,
		"external_call_made":                        false,
		"provider_api_call_made":                    false,
		"provider_api_mutation":                     "disabled",
		"request_body_included":                     false,
		"headers_included":                          false,
		"provider_url_included":                     false,
		"repository_ref_included":                   false,
		"branch_name_included":                      false,
		"file_content_included":                     false,
		"contains_token":                            false,
		"contains_provider_url":                     false,
		"contains_repository_ref":                   false,
		"contains_branch_name":                      false,
		"contains_file_content":                     false,
		"request_materialization_boundary_redacted": true,
		"blocked_reasons": []string{
			"provider_request_not_materialized",
			"provider_review_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
	}
}

func providerReviewAttemptAdapterCredentialBindingPlan(providerType, operationName string) map[string]any {
	providerType = safeProviderReviewProviderType(providerType)
	operationName = safeProviderReviewAttemptOperationName(operationName)
	authScheme := providerReviewAuthSchemeForProvider(providerType)
	if providerType == "" || operationName == "" || authScheme == "" {
		return map[string]any{}
	}
	return map[string]any{
		"mode":                              "redacted_attempt_adapter_credential_binding_plan",
		"credential_binding_state":          "blocked",
		"credential_binding_ready":          false,
		"credential_binding_ready_reason":   "provider_credential_runtime_binding_not_armed",
		"provider_type":                     providerType,
		"operation_name":                    operationName,
		"endpoint_key":                      providerReviewEndpointKey(providerType, providerReviewEndpointOperationForAttempt(operationName)),
		"auth_scheme":                       authScheme,
		"credential_source_kind":            "provider_account_token_env",
		"requires_provider_account":         true,
		"requires_allowed_token_env":        true,
		"requires_runtime_token_present":    true,
		"requires_mutation_arming":          true,
		"credential_bound":                  false,
		"authorization_header_materialized": false,
		"token_env_name_included":           false,
		"token_value_included":              false,
		"token_stored":                      false,
		"headers_included":                  false,
		"external_call_made":                false,
		"provider_api_call_made":            false,
		"provider_api_mutation":             "disabled",
		"contains_token":                    false,
		"contains_provider_url":             false,
		"contains_repository_ref":           false,
		"contains_branch_name":              false,
		"contains_file_content":             false,
		"blocked_reasons": []string{
			"provider_credential_runtime_binding_not_armed",
			"provider_review_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
		"credential_boundary_redacted": true,
	}
}

func providerReviewAttemptAdapterResponsePlan(operation, requestSummary, responseDiagnostics map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	providerType := providerReviewProviderFromEndpointKey(endpointKey)
	responseHandler := safeProviderReviewResponseHandlerName(stringFromMap(requestSummary, "response_handler"))
	if operationName == "" || endpointKey == "" || providerType == "" || !providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey) || !providerReviewAttemptResponseHandlerMatchesOperation(operationName, responseHandler) {
		return map[string]any{}
	}
	unlockOperation := providerReviewAttemptDependencyUnlockOperation(operationName)
	plan := map[string]any{
		"mode":                            providerReviewAttemptAdapterResponsePlanMode,
		"response_recording_state":        "blocked",
		"response_recording_ready":        false,
		"response_recording_ready_reason": "provider_api_adapter_response_not_recorded",
		"operation_name":                  operationName,
		"endpoint_key":                    endpointKey,
		"operation_order":                 intFromAny(operation["operation_order"], 0),
		"response_handler":                responseHandler,
		"response_status":                 safeProviderReviewAttemptResponseStatus(stringFromMap(responseDiagnostics, "status")),
		"expected_success_classes":        providerReviewExpectedSuccessClassesForOperation(operationName),
		"retryable_status_classes":        providerReviewExpectedRetryClassesForOperation(operationName),
		"terminal_failure_status_classes": providerReviewTerminalFailureClassesForOperation(operationName),
		"success_attempt_status":          "completed",
		"retry_attempt_status":            "planned",
		"failure_attempt_status":          "failed",
		"dependency_unlocks_operation":    unlockOperation,
		"dependency_update_status":        providerReviewAttemptDependencyUnlockStatus(unlockOperation),
		"requires_response_handler":       true,
		"requires_response_diagnostics":   true,
		"requires_idempotency_ledger":     true,
		"requires_dependency_update":      unlockOperation != "",
		"adapter_implemented":             false,
		"mutation_armed":                  false,
		"response_recorded":               false,
		"dependency_update_recorded":      false,
		"external_call_made":              false,
		"provider_api_call_made":          false,
		"provider_api_mutation":           "disabled",
		"response_body_included":          false,
		"headers_included":                false,
		"provider_request_id_included":    false,
		"contains_token":                  false,
		"contains_provider_url":           false,
		"contains_repository_ref":         false,
		"contains_branch_name":            false,
		"contains_file_content":           false,
		"blocked_reasons": []string{
			"provider_api_call_not_made",
			"provider_review_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
		"response_boundary_redacted": true,
	}
	plan["result_recording_plan"] = providerReviewAttemptAdapterResultRecordingPlan(operation, plan)
	return plan
}

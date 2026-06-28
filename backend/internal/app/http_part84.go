package app

func providerReviewAttemptAdapterInvocationPlan(operation, claimPlan, requestPlan, credentialPlan, runtimePlan, branchPolicyPlan, transportPlan, responsePlan, transactionPlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	if operationName == "" || endpointKey == "" {
		return map[string]any{}
	}
	executionLockPlan := providerReviewAttemptAdapterExecutionLockPlan(operation, claimPlan, transactionPlan)
	providerSendPlan := providerReviewAttemptAdapterProviderSendPlan(operation, requestPlan, credentialPlan, runtimePlan, transportPlan)
	activationPlan := providerReviewAttemptAdapterActivationPlan(operation, claimPlan, executionLockPlan, credentialPlan, runtimePlan, requestPlan, transportPlan, providerSendPlan, responsePlan, transactionPlan)
	claimMetadataReady := providerReviewAttemptClaimPlanReadyForOperation(claimPlan, operationName, endpointKey)
	executionLockReady := providerReviewAttemptExecutionLockPlanReadyForOperation(executionLockPlan, operationName, endpointKey)
	adapterActivationReady := providerReviewAttemptActivationPlanReadyForOperation(activationPlan, operationName, endpointKey)
	credentialReady := providerReviewAttemptCredentialPlanReadyForOperation(credentialPlan, operationName, endpointKey)
	runtimeReady := providerReviewAttemptRuntimePlanReadyForOperation(runtimePlan, operationName, endpointKey)
	branchPolicyReady := providerReviewAttemptBranchPolicyPlanReadyForOperation(branchPolicyPlan, operationName, endpointKey)
	requestReady := providerReviewAttemptRequestPlanReadyForOperation(requestPlan, operationName, endpointKey)
	transportReady := providerReviewAttemptTransportPlanReadyForOperation(transportPlan, operationName, endpointKey)
	providerSendReady := providerReviewAttemptProviderSendPlanReadyForOperation(providerSendPlan, operationName, endpointKey)
	responseReady := providerReviewAttemptResponseRecordingReadyForOperation(responsePlan, operationName, endpointKey)
	transactionReady := providerReviewAttemptTransactionPlanReadyForOperation(transactionPlan, operationName, endpointKey)
	return map[string]any{
		"mode":                              "redacted_attempt_adapter_invocation_plan",
		"invocation_state":                  "blocked",
		"invocation_ready":                  false,
		"invocation_ready_reason":           "provider_api_invocation_not_armed",
		"operation_name":                    operationName,
		"endpoint_key":                      endpointKey,
		"operation_order":                   intFromAny(operation["operation_order"], 0),
		"invocation_sequence":               []string{"claim_attempt", "claim_idempotency", "claim_execution_lock", "evaluate_adapter_activation", "bind_credential", "select_adapter_runtime", "verify_branch_policy", "materialize_request", "send_provider_request", "record_response", "record_transaction_boundary", "unlock_dependency"},
		"required_subplans":                 []string{"claim_plan", "execution_lock_plan", "adapter_activation_plan", "credential_binding_plan", "adapter_runtime_plan", "branch_policy_plan", "request_materialization_plan", "transport_plan", "provider_send_plan", "response_plan", "transaction_plan"},
		"execution_lock_plan":               executionLockPlan,
		"adapter_activation_plan":           activationPlan,
		"provider_send_plan":                providerSendPlan,
		"claim_metadata_ready":              claimMetadataReady,
		"execution_lock_metadata_ready":     executionLockReady,
		"adapter_activation_metadata_ready": adapterActivationReady,
		"credential_binding_ready":          credentialReady,
		"adapter_runtime_ready":             runtimeReady,
		"branch_policy_metadata_ready":      branchPolicyReady,
		"request_materialization_ready":     requestReady,
		"transport_metadata_ready":          transportReady,
		"provider_send_metadata_ready":      providerSendReady,
		"response_recording_ready":          responseReady,
		"transaction_metadata_ready":        transactionReady,
		"claim_metadata_ready_reason":       providerReviewAttemptInvocationReadyReason(claimMetadataReady, "provider_review_claim_metadata_not_ready"),
		"execution_lock_ready_reason":       stringFromMap(executionLockPlan, "execution_lock_metadata_ready_reason"),
		"adapter_activation_ready_reason":   stringFromMap(activationPlan, "adapter_activation_metadata_ready_reason"),
		"adapter_runtime_ready_reason":      providerReviewAttemptInvocationReadyReason(runtimeReady, "provider_review_adapter_runtime_not_ready"),
		"branch_policy_ready_reason":        stringFromMap(branchPolicyPlan, "branch_policy_ready_reason"),
		"transport_metadata_ready_reason":   providerReviewAttemptInvocationReadyReason(transportReady, "provider_review_transport_metadata_not_ready"),
		"provider_send_ready_reason":        providerReviewAttemptInvocationReadyReason(providerSendReady, "provider_request_send_not_armed"),
		"transaction_metadata_ready_reason": providerReviewAttemptInvocationReadyReason(transactionReady, "provider_review_transaction_metadata_not_ready"),
		"requires_attempt_claim":            true,
		"requires_idempotency_claim":        true,
		"requires_execution_lock":           true,
		"requires_adapter_activation":       true,
		"requires_credential_binding":       true,
		"requires_adapter_runtime":          true,
		"requires_branch_policy":            true,
		"requires_request_materialization":  true,
		"requires_transport":                true,
		"requires_response_recording":       true,
		"requires_transaction_boundary":     true,
		"requires_mutation_arming":          true,
		"attempt_claim_recorded":            false,
		"idempotency_claim_recorded":        false,
		"execution_lock_acquired":           false,
		"adapter_activation_approved":       false,
		"duplicate_send_detected":           false,
		"credential_bound":                  false,
		"adapter_runtime_bound":             false,
		"branch_policy_verified":            false,
		"request_materialized":              false,
		"provider_request_sent":             false,
		"response_recorded":                 false,
		"transaction_recorded":              false,
		"dependency_update_recorded":        false,
		"adapter_implemented":               false,
		"mutation_armed":                    false,
		"external_call_made":                false,
		"provider_api_call_made":            false,
		"provider_api_mutation":             "disabled",
		"request_body_included":             false,
		"response_body_included":            false,
		"headers_included":                  false,
		"authorization_header_included":     false,
		"provider_url_included":             false,
		"idempotency_key_included":          false,
		"contains_token":                    false,
		"contains_provider_url":             false,
		"contains_repository_ref":           false,
		"contains_branch_name":              false,
		"contains_file_content":             false,
		"invocation_boundary_redacted":      true,
		"blocked_reasons": []string{
			"provider_review_attempt_claim_not_recorded",
			"provider_review_execution_lock_not_acquired",
			"provider_review_adapter_activation_not_armed",
			"provider_credential_runtime_binding_not_armed",
			"provider_review_adapter_runtime_not_bound",
			"provider_branch_policy_not_armed",
			"provider_request_not_materialized",
			"provider_api_call_not_made",
			"provider_review_transaction_not_recorded",
			"provider_review_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
	}
}

func providerReviewAttemptBranchPolicyPlan(operation, requestPlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	if operationName == "" || endpointKey == "" {
		return map[string]any{}
	}
	metadataReady := providerReviewAttemptPlanMatchesOperation(requestPlan, providerReviewAttemptAdapterRequestMaterializationPlanMode, operationName, endpointKey)
	return map[string]any{
		"mode":                                  "redacted_attempt_branch_policy_plan",
		"branch_policy_state":                   "blocked",
		"branch_policy_ready":                   false,
		"branch_policy_ready_reason":            "provider_branch_policy_not_armed",
		"branch_policy_metadata_ready":          metadataReady,
		"operation_name":                        operationName,
		"endpoint_key":                          endpointKey,
		"operation_order":                       intFromAny(operation["operation_order"], 0),
		"policy_scope":                          "provider_review_attempt_operation",
		"target_branch_policy":                  "protected_default_branch_no_direct_write",
		"review_branch_policy":                  "required_before_starter_file_commit",
		"requires_review_branch":                true,
		"requires_default_branch_protection":    true,
		"requires_review_request":               true,
		"requires_operator_policy_review":       true,
		"requires_mutation_arming":              true,
		"default_branch_direct_write_allowed":   false,
		"protected_branch_direct_write_allowed": false,
		"starter_file_commit_to_default":        false,
		"review_branch_materialized":            false,
		"default_branch_materialized":           false,
		"protected_branch_rules_materialized":   false,
		"branch_policy_verified":                false,
		"branch_ref_created":                    false,
		"review_request_created":                false,
		"external_call_made":                    false,
		"provider_api_call_made":                false,
		"provider_api_mutation":                 "disabled",
		"repository_ref_included":               false,
		"branch_name_included":                  false,
		"protected_branch_rules_included":       false,
		"contains_token":                        false,
		"contains_provider_url":                 false,
		"contains_repository_ref":               false,
		"contains_branch_name":                  false,
		"contains_file_content":                 false,
		"branch_policy_boundary_redacted":       true,
		"branch_safety_summary":                 providerReviewAttemptBranchSafetySummary(operationName),
		"branch_policy_sequence":                []string{"verify_target_branch_policy", "require_review_branch_strategy", "block_default_branch_direct_write", "require_review_request", "handoff_to_provider_adapter"},
		"branch_policy_suppressed_fields":       []string{"default_branch", "target_branch", "review_branch", "branch_ref", "repository_ref", "protected_branch_rules", "provider_url", "authorization_header", "token", "file_content"},
		"blocked_reasons":                       []string{"provider_branch_policy_not_armed", "protected_default_branch_direct_write_disabled", "provider_review_adapter_not_implemented", "provider_review_mutation_not_armed"},
	}
}

func providerReviewAttemptBranchSafetySummary(operationName string) map[string]any {
	// Keep this allowlist here too so direct callers cannot leak a raw operation name.
	operationName = safeProviderReviewAttemptOperationName(operationName)
	summary := map[string]any{
		"mode":                                  "redacted_provider_review_branch_safety_summary",
		"operation_name":                        operationName,
		"target_branch_policy":                  "protected_default_branch_no_direct_write",
		"review_branch_policy":                  "required_before_provider_review",
		"requires_review_branch":                true,
		"requires_protected_branch_check":       true,
		"requires_existing_branch_replay_check": operationName == "create_branch_ref",
		"requires_review_request_before_merge":  true,
		"default_branch_direct_write_allowed":   false,
		"protected_branch_direct_write_allowed": false,
		"starter_file_commit_to_default":        false,
		"branch_ref_created":                    false,
		"commit_written":                        false,
		"review_request_created":                false,
		"provider_api_call_made":                false,
		"provider_api_mutation":                 "disabled",
		"repository_ref_included":               false,
		"branch_name_included":                  false,
		"protected_branch_rules_included":       false,
		"contains_token":                        false,
		"contains_provider_url":                 false,
		"contains_repository_ref":               false,
		"contains_branch_name":                  false,
		"contains_file_content":                 false,
		"summary_boundary_redacted":             true,
	}
	switch operationName {
	case "create_branch_ref":
		summary["operation_intent"] = "create_review_branch_ref_only"
		summary["guardrail_focus"] = "review_branch_must_not_replace_protected_default"
	case "commit_starter_files":
		summary["operation_intent"] = "commit_starter_files_to_review_branch_only"
		summary["guardrail_focus"] = "starter_file_commit_requires_review_branch"
	case "open_review_request":
		summary["operation_intent"] = "open_provider_review_from_review_branch"
		summary["guardrail_focus"] = "operator_review_required_before_merge"
	default:
		summary["operation_intent"] = ""
		summary["guardrail_focus"] = "unknown_operation_blocked"
	}
	return summary
}

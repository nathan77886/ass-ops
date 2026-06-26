package app

import "fmt"

type reviewBranchAttemptLedgerAdapterInput struct {
	ProviderType           string
	OperationName          string
	EndpointKey            string
	AttemptStatus          string
	DependencyStatus       string
	FileCount              int
	ClaimRecorded          bool
	PreflightMetadataReady bool
}

func reviewBranchAttemptLedgerAdapterPlanFromAttempt(attempt, preflight map[string]any) map[string]any {
	requestSummary := mapFromAny(attempt["request_summary"])
	preflightPayload := mapFromAny(preflight["preflight"])
	return reviewBranchAttemptLedgerAdapterPlan(reviewBranchAttemptLedgerAdapterInput{
		ProviderType:           stringFromMap(attempt, "provider_type"),
		OperationName:          stringFromMap(attempt, "operation_name"),
		EndpointKey:            stringFromMap(attempt, "endpoint_key"),
		AttemptStatus:          stringFromMap(attempt, "status"),
		DependencyStatus:       stringFromMap(attempt, "dependency_status"),
		FileCount:              intFromAny(requestSummary["file_count"], 0),
		ClaimRecorded:          providerReviewAttemptClaimRecorded(attempt),
		PreflightMetadataReady: boolOnlyFromAny(preflightPayload["live_execution_preflight_metadata_ready"]),
	})
}

func reviewBranchAttemptLedgerAdapterPlan(input reviewBranchAttemptLedgerAdapterInput) map[string]any {
	providerType := safeProviderReviewProviderType(input.ProviderType)
	operationName := safeProviderReviewAttemptOperationName(input.OperationName)
	endpointKey := safeProviderReviewEndpointKey(input.EndpointKey)
	attemptStatus := safeProviderReviewAttemptStatus(input.AttemptStatus)
	dependencyStatus := safeProviderReviewAttemptClaimDependencyStatus(input.DependencyStatus)
	operationSupported := providerType == "github" &&
		operationName != "" &&
		endpointKey != "" &&
		providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey)
	metadataReady := operationSupported &&
		attemptStatus == "running" &&
		input.ClaimRecorded &&
		input.PreflightMetadataReady &&
		providerReviewAttemptClaimDependencyReady(dependencyStatus)
	// This is an internal consistency signal for dry-run ledger planning only.
	// It must not be used as an execution gate; live_execution_ready remains the gate-facing field.
	blockedReasons := reviewBranchAttemptLedgerAdapterBlockedReasons(operationSupported, metadataReady)
	return map[string]any{
		"mode":                                "redacted_review_branch_attempt_ledger_adapter_plan",
		"adapter_name":                        "github_review_branch_ledger_adapter",
		"adapter_scope":                       "atomic_executor_to_stepwise_attempt_ledger",
		"adapter_plan_ready":                  operationSupported,
		"adapter_plan_state":                  map[bool]string{true: "review_branch_ledger_adapter_planned", false: "blocked"}[operationSupported],
		"live_execution_ready":                false,
		"live_execution_state":                "provider_review_live_execution_blocked",
		"provider_type":                       providerType,
		"operation_name":                      operationName,
		"endpoint_key":                        endpointKey,
		"attempt_status":                      attemptStatus,
		"dependency_status":                   dependencyStatus,
		"claim_recorded":                      input.ClaimRecorded,
		"preflight_metadata_ready":            input.PreflightMetadataReady,
		"ledger_metadata_ready":               metadataReady,
		"operation_supported":                 operationSupported,
		"atomic_executor_available":           true,
		"atomic_executor_bound":               false,
		"stepwise_ledger_execution_required":  true,
		"stepwise_ledger_execution_available": false,
		"schema_allows_provider_call":         false,
		"requires_db_schema_unlock":           true,
		"requires_attempt_claim":              true,
		"requires_dependency_order":           true,
		"requires_mutation_arming":            true,
		"requires_provider_send":              true,
		"requires_sanitized_result_allowlist": true,
		"requires_cleanup_recording":          true,
		"operation_count":                     3,
		"file_count":                          maxInt(input.FileCount, 0),
		"pipeline":                            reviewBranchAttemptLedgerAdapterPipeline(providerType, maxInt(input.FileCount, 0)),
		"external_call_made":                  false,
		"provider_api_call_made":              false,
		"provider_api_mutation":               "disabled",
		"provider_request_materialized":       false,
		"provider_request_sent":               false,
		"provider_response_received":          false,
		"transaction_recorded":                false,
		"request_body_included":               false,
		"response_body_included":              false,
		"headers_included":                    false,
		"authorization_header_included":       false,
		"provider_url_included":               false,
		"token_env_name_included":             false,
		"token_value_included":                false,
		"file_content_included":               false,
		"idempotency_key_included":            false,
		"contains_token":                      false,
		"contains_provider_url":               false,
		"contains_repository_ref":             false,
		"contains_branch_name":                false,
		"contains_file_content":               false,
		"blocked_reasons":                     blockedReasons,
	}
}

func reviewBranchAttemptLedgerAdapterBlockedReasons(operationSupported, metadataReady bool) []string {
	reasons := []string{}
	if !operationSupported {
		reasons = append(reasons, "provider_review_attempt_operation_endpoint_invalid")
	}
	if !metadataReady {
		reasons = append(reasons, "provider_review_attempt_ledger_metadata_not_ready")
	}
	return append(reasons,
		"provider_review_attempt_live_schema_locked",
		"provider_review_stepwise_ledger_execution_not_implemented",
		"provider_review_mutation_not_armed",
		"provider_request_send_not_armed",
	)
}

func reviewBranchAttemptLedgerAdapterPipeline(providerType string, fileCount int) []map[string]any {
	providerType = safeProviderReviewProviderType(providerType)
	operations := []struct {
		name              string
		endpointOperation string
		method            string
		payloadShape      string
		dependsOn         string
		unlocks           string
	}{
		{
			name:              "create_branch_ref",
			endpointOperation: "create_branch_ref",
			method:            "POST",
			payloadShape:      "ref_from_target_branch",
			unlocks:           "commit_starter_files",
		},
		{
			name:              "commit_starter_files",
			endpointOperation: "commit_files",
			method:            "PUT",
			payloadShape:      "content_redacted_file_batch",
			dependsOn:         "create_branch_ref",
			unlocks:           "open_review_request",
		},
		{
			name:              "open_review_request",
			endpointOperation: "open_review",
			method:            "POST",
			payloadShape:      "review_request",
			dependsOn:         "commit_starter_files",
		},
	}
	pipeline := make([]map[string]any, 0, len(operations))
	for index, operation := range operations {
		pipeline = append(pipeline, map[string]any{
			"name":                          operation.name,
			"operation_order":               (index + 1) * 10,
			"endpoint_key":                  providerReviewEndpointKey(providerType, operation.endpointOperation),
			"method":                        operation.method,
			"payload_shape":                 operation.payloadShape,
			"file_count":                    map[bool]int{true: fileCount, false: 0}[operation.name == "commit_starter_files"],
			"depends_on_operation":          operation.dependsOn,
			"unlocks_operation":             operation.unlocks,
			"request_body_included":         false,
			"response_body_included":        false,
			"headers_included":              false,
			"authorization_header_included": false,
			"provider_url_included":         false,
			"contains_token":                false,
			"contains_provider_url":         false,
			"contains_repository_ref":       false,
			"contains_branch_name":          false,
			"contains_file_content":         false,
			"external_call_made":            false,
			"provider_api_call_made":        false,
			"provider_api_mutation":         "disabled",
		})
	}
	return pipeline
}

func maxInt(value, floor int) int {
	if value < floor {
		return floor
	}
	return value
}

func reviewBranchAttemptLedgerAdapterPlanMatchesAttempt(plan map[string]any, operationName, endpointKey string) bool {
	return stringFromMap(plan, "mode") == "redacted_review_branch_attempt_ledger_adapter_plan" &&
		safeProviderReviewAttemptOperationName(stringFromMap(plan, "operation_name")) == safeProviderReviewAttemptOperationName(operationName) &&
		safeProviderReviewEndpointKey(stringFromMap(plan, "endpoint_key")) == safeProviderReviewEndpointKey(endpointKey)
}

func reviewBranchAttemptLedgerAdapterPlanSummary(plan map[string]any) string {
	if len(plan) == 0 {
		return ""
	}
	return fmt.Sprintf("%s:%s", cleanOptionalText(stringFromMap(plan, "adapter_name")), cleanOptionalText(stringFromMap(plan, "live_execution_state")))
}

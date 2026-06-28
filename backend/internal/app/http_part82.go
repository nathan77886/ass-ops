package app

func safeProviderReviewStatusClass(value string) string {
	switch cleanOptionalText(value) {
	case "2xx", "4xx", "5xx", "unknown":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func safeProviderReviewEndpointKey(value string) string {
	switch cleanOptionalText(value) {
	case "github.create_branch_ref", "github.commit_files", "github.open_review", "gitea.create_branch_ref", "gitea.commit_files", "gitea.open_review":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func safeProviderReviewProviderType(value string) string {
	switch cleanOptionalText(value) {
	case "github", "gitea":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func safeProviderReviewStatusClasses(items []string) []string {
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		item = safeProviderReviewStatusClass(item)
		if item == "" || seen[item] {
			continue
		}
		out = append(out, item)
		seen[item] = true
	}
	return out
}

func providerReviewAttemptOrchestrationSummary(operations []map[string]any) map[string]any {
	summary := map[string]any{
		"status":                     "not_recorded",
		"mode":                       "redacted_attempt_orchestration",
		"next_operation":             "",
		"ready_count":                0,
		"waiting_count":              0,
		"blocked_count":              0,
		"completed_count":            0,
		"dependency_chain_status":    "not_recorded",
		"external_call_made":         false,
		"provider_api_call_made":     false,
		"provider_api_mutation":      "disabled",
		"idempotency_key_included":   false,
		"requires_operator_review":   true,
		"requires_adapter_execution": true,
		"dependency_chain_plan":      providerReviewAttemptDependencyChainPlan(nil, "not_recorded", "", 0, 0, 0, 0),
		"execution_candidate":        providerReviewAttemptExecutionCandidate(nil, ""),
	}
	if len(operations) == 0 {
		return summary
	}
	nextOperation := ""
	nextOperationSet := false
	readyCount := 0
	waitingCount := 0
	blockedCount := 0
	completedCount := 0
	failed := false
	for _, operation := range operations {
		name := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
		status := safeProviderReviewAttemptStatus(stringFromMap(operation, "status"))
		dependencyStatus := safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(operation, "dependency_status"))
		switch {
		case dependencyStatus == "dependency_failed" || status == "failed" || status == "blocked":
			blockedCount++
			failed = true
		case status == "completed":
			completedCount++
		case dependencyStatus == "waiting_for_dependency" || status == "running":
			waitingCount++
		case status == "planned" && (dependencyStatus == "independent" || dependencyStatus == "dependency_satisfied"):
			readyCount++
			if !nextOperationSet && name != "" {
				nextOperation = name
				nextOperationSet = true
			}
		default:
			blockedCount++
			failed = true
		}
	}
	chainStatus := "ready"
	if failed {
		chainStatus = "blocked"
	} else if waitingCount > 0 {
		chainStatus = "waiting_for_dependency"
	} else if completedCount == len(operations) {
		chainStatus = "completed"
	}
	summary["status"] = "planned"
	summary["next_operation"] = nextOperation
	summary["ready_count"] = readyCount
	summary["waiting_count"] = waitingCount
	summary["blocked_count"] = blockedCount
	summary["completed_count"] = completedCount
	summary["dependency_chain_status"] = chainStatus
	summary["dependency_chain_plan"] = providerReviewAttemptDependencyChainPlan(operations, chainStatus, nextOperation, readyCount, waitingCount, blockedCount, completedCount)
	summary["execution_candidate"] = providerReviewAttemptExecutionCandidate(operations, nextOperation)
	return summary
}

func providerReviewAttemptDependencyChainPlan(operations []map[string]any, chainStatus, nextOperation string, readyCount, waitingCount, blockedCount, completedCount int) map[string]any {
	orderedOperations := make([]map[string]any, 0, len(operations))
	for _, operation := range operations {
		name := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
		dependsOn := safeProviderReviewAttemptDependencyName(stringFromMap(operation, "depends_on_operation"))
		orderedOperations = append(orderedOperations, map[string]any{
			"name":                         name,
			"endpoint_key":                 safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key")),
			"operation_order":              intFromAny(operation["operation_order"], 0),
			"depends_on_operation":         dependsOn,
			"dependency_status":            safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(operation, "dependency_status")),
			"attempt_status":               safeProviderReviewAttemptStatus(stringFromMap(operation, "status")),
			"dependency_unlocks_operation": providerReviewAttemptDependencyUnlockOperation(name),
			"requires_dependency_update":   providerReviewAttemptDependencyUnlockOperation(name) != "",
			"external_call_made":           false,
			"provider_api_call_made":       false,
			"provider_api_mutation":        "disabled",
			"contains_token":               false,
			"contains_provider_url":        false,
			"contains_repository_ref":      false,
			"contains_branch_name":         false,
			"contains_file_content":        false,
		})
	}
	chainStatus = safeProviderReviewAttemptDependencyChainStatus(chainStatus)
	nextOperation = safeProviderReviewAttemptOperationName(nextOperation)
	return map[string]any{
		"mode":                          "redacted_attempt_dependency_chain_plan",
		"status":                        chainStatus,
		"next_operation":                nextOperation,
		"operation_count":               len(orderedOperations),
		"ready_count":                   readyCount,
		"waiting_count":                 waitingCount,
		"blocked_count":                 blockedCount,
		"completed_count":               completedCount,
		"ordered_operations":            orderedOperations,
		"chain_ready_for_next_attempt":  chainStatus == "ready" && nextOperation != "",
		"requires_ordered_claim":        true,
		"requires_dependency_update":    true,
		"requires_response_plan":        true,
		"requires_transaction_boundary": true,
		"requires_operator_review":      true,
		"requires_adapter_execution":    true,
		"dependency_updates_recorded":   false,
		"attempt_claim_recorded":        false,
		"provider_request_sent":         false,
		"provider_response_recorded":    false,
		"external_call_made":            false,
		"provider_api_call_made":        false,
		"provider_api_mutation":         "disabled",
		"idempotency_key_included":      false,
		"contains_token":                false,
		"contains_provider_url":         false,
		"contains_repository_ref":       false,
		"contains_branch_name":          false,
		"contains_file_content":         false,
		"suppressed_fields":             []string{"provider_url", "repository_ref", "branch_name", "file_content", "request_body", "response_body", "headers", "authorization_header", "idempotency_key", "token"},
		"disabled_backends":             []string{"provider_api_branch_create", "provider_api_file_commit", "provider_api_review_create", "provider_request_send", "provider_response_record"},
	}
}

func providerReviewAttemptExecutionCandidate(operations []map[string]any, nextOperation string) map[string]any {
	candidate := map[string]any{
		"mode":                          "redacted_attempt_execution_candidate",
		"status":                        "blocked",
		"next_operation":                "",
		"endpoint_key":                  "",
		"operation_order":               0,
		"requires_provider_client":      true,
		"requires_idempotency_ledger":   true,
		"requires_response_diagnostics": true,
		"requires_mutation_arming":      true,
		"adapter_contract":              map[string]any{},
		"claim_plan":                    map[string]any{},
		"dispatch_plan":                 map[string]any{},
		"adapter_implemented":           false,
		"mutation_armed":                false,
		"external_call_made":            false,
		"provider_api_call_made":        false,
		"provider_api_mutation":         "disabled",
		"idempotency_key_included":      false,
		"contains_token":                false,
		"contains_provider_url":         false,
		"contains_repository_ref":       false,
		"contains_branch_name":          false,
		"contains_file_content":         false,
		"blocked_reasons": []string{
			"provider_review_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
		"gates": providerReviewAttemptExecutionCandidateGates(false, false, false),
	}
	nextOperation = safeProviderReviewAttemptOperationName(nextOperation)
	if nextOperation == "" {
		candidate["blocked_reasons"] = []string{"provider_review_attempt_not_ready"}
		return candidate
	}
	for _, operation := range operations {
		if safeProviderReviewAttemptOperationName(stringFromMap(operation, "name")) != nextOperation {
			continue
		}
		requestSummary := mapFromAny(operation["request_summary"])
		responseDiagnostics := mapFromAny(operation["response_diagnostics"])
		endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
		adapterContract := providerReviewAttemptCandidateAdapterContract(operation, requestSummary, responseDiagnostics)
		claimPlan := providerReviewAttemptClaimPlanForOperation(operation, requestSummary, responseDiagnostics, nextOperation, endpointKey)
		candidate["next_operation"] = nextOperation
		candidate["endpoint_key"] = endpointKey
		candidate["operation_order"] = intFromAny(operation["operation_order"], 0)
		candidate["status"] = "blocked"
		candidate["adapter_contract"] = adapterContract
		candidate["claim_plan"] = claimPlan
		candidate["dispatch_plan"] = providerReviewAttemptAdapterDispatchPlan(operation, requestSummary, responseDiagnostics, adapterContract, claimPlan)
		candidate["gates"] = providerReviewAttemptExecutionCandidateGates(true, boolOnlyFromAny(claimPlan["idempotency_metadata_ready"]), boolOnlyFromAny(claimPlan["response_diagnostics_ready"]))
		return candidate
	}
	candidate["blocked_reasons"] = []string{"provider_review_attempt_not_found"}
	return candidate
}

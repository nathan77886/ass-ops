package app

import (
	"fmt"
)

func providerReviewAttemptAdapterResultRecordingPlan(operation, responsePlan map[string]any) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	if operationName == "" || endpointKey == "" || !providerReviewAttemptResponsePlanReadyForOperation(responsePlan, operationName, endpointKey) {
		return map[string]any{}
	}
	dependencyUnlockOperation := safeProviderReviewAttemptOperationName(stringFromMap(responsePlan, "dependency_unlocks_operation"))
	dependencyUpdateStatus := ""
	if dependencyUnlockOperation != "" {
		dependencyUpdateStatus = safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(responsePlan, "dependency_update_status"))
	}
	return map[string]any{
		"mode":                               "redacted_attempt_adapter_result_recording_plan",
		"result_recording_state":             "blocked",
		"result_recording_ready":             false,
		"result_recording_ready_reason":      "provider_review_result_recording_not_armed",
		"result_recording_metadata_ready":    true,
		"operation_name":                     operationName,
		"endpoint_key":                       endpointKey,
		"operation_order":                    intFromAny(operation["operation_order"], 0),
		"response_status":                    safeProviderReviewAttemptResponseStatus(stringFromMap(responsePlan, "response_status")),
		"success_attempt_status":             safeProviderReviewAttemptStatus(stringFromMap(responsePlan, "success_attempt_status")),
		"retry_attempt_status":               safeProviderReviewAttemptStatus(stringFromMap(responsePlan, "retry_attempt_status")),
		"failure_attempt_status":             safeProviderReviewAttemptStatus(stringFromMap(responsePlan, "failure_attempt_status")),
		"dependency_unlocks_operation":       dependencyUnlockOperation,
		"dependency_update_status":           dependencyUpdateStatus,
		"requires_response_handler":          true,
		"requires_response_diagnostics":      true,
		"requires_transaction_boundary":      true,
		"requires_dependency_update":         boolOnlyFromAny(responsePlan["requires_dependency_update"]),
		"requires_mutation_arming":           true,
		"result_recorded":                    false,
		"response_classified":                false,
		"attempt_status_mapped":              false,
		"attempt_result_persisted":           false,
		"dependency_update_staged":           false,
		"provider_request_id_recorded":       false,
		"provider_response_status_recorded":  false,
		"provider_response_body_recorded":    false,
		"provider_response_headers_recorded": false,
		"external_call_made":                 false,
		"provider_api_call_made":             false,
		"provider_api_mutation":              "disabled",
		"response_body_included":             false,
		"headers_included":                   false,
		"provider_request_id_included":       false,
		"provider_response_status_included":  false,
		"provider_url_included":              false,
		"idempotency_key_included":           false,
		"contains_token":                     false,
		"contains_provider_url":              false,
		"contains_repository_ref":            false,
		"contains_branch_name":               false,
		"contains_file_content":              false,
		"result_recording_boundary_redacted": true,
		"result_recording_sequence":          []string{"classify_provider_response", "map_attempt_status", "stage_dependency_update", "record_redacted_result", "persist_attempt_result"},
		"result_recording_diagnostic_fields": []string{"status_class", "retry_class", "dependency_update_required", "provider_request_id_present"},
		"result_recording_persisted_fields":  []string{"attempt_status", "dependency_status", "response_status_class", "retry_class"},
		"result_recording_suppressed_fields": []string{"provider_request_id", "response_body", "response_headers", "provider_url", "authorization_header", "token", "repository_ref", "branch_name", "file_content"},
		"blocked_reasons": []string{
			"provider_review_result_recording_not_armed",
			"provider_api_call_not_made",
			"provider_review_adapter_not_implemented",
			"provider_review_mutation_not_armed",
		},
	}
}

func providerReviewAttemptAdapterTransportPlan(providerType, operationName string) map[string]any {
	providerType = providerReviewProviderFromEndpointKey(providerReviewEndpointKey(providerType, providerReviewEndpointOperationForAttempt(operationName)))
	operationName = safeProviderReviewAttemptOperationName(operationName)
	if providerType == "" || operationName == "" {
		return map[string]any{}
	}
	return map[string]any{
		"mode":                          "redacted_attempt_adapter_transport_plan",
		"transport_ready":               true,
		"transport_ready_reason":        "ready",
		"provider_type":                 providerType,
		"operation_name":                operationName,
		"method":                        providerReviewMethodForOperation(operationName),
		"endpoint_key":                  providerReviewEndpointKey(providerType, providerReviewEndpointOperationForAttempt(operationName)),
		"payload_shape":                 providerReviewPayloadShapeForOperation(operationName),
		"auth_scheme":                   providerReviewAuthSchemeForProvider(providerType),
		"accept_header":                 providerReviewAcceptHeaderForProvider(providerType),
		"content_type":                  "application/json",
		"timeout_seconds":               15,
		"expected_success_classes":      providerReviewExpectedSuccessClassesForOperation(operationName),
		"retryable_status_classes":      []string{"5xx"},
		"diagnostic_fields":             []string{"status_code_class", "provider_request_id_present", "rate_limit_remaining_present", "retry_after_present", "provider_error_code_present"},
		"request_body_included":         false,
		"response_body_included":        false,
		"headers_included":              false,
		"authorization_header_included": false,
		"auth_header_redacted":          true,
		"provider_url_included":         false,
		"external_call_made":            false,
		"provider_api_call_made":        false,
		"provider_api_mutation":         "disabled",
		"contains_token":                false,
		"contains_provider_url":         false,
		"contains_repository_ref":       false,
		"contains_branch_name":          false,
		"contains_file_content":         false,
	}
}

func providerReviewAttemptExecutionClaimPlan(operation map[string]any, idempotencyReady, responseDiagnosticsReady bool) map[string]any {
	if len(operation) == 0 {
		return map[string]any{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	providerType := providerReviewProviderFromEndpointKey(endpointKey)
	operationEndpointReady := providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey)
	status := safeProviderReviewAttemptStatus(stringFromMap(operation, "status"))
	rawDependencyStatus := cleanOptionalText(stringFromMap(operation, "dependency_status"))
	dependencyStatus := safeProviderReviewAttemptClaimDependencyStatus(rawDependencyStatus)
	dependencyReady := providerReviewAttemptClaimDependencyReady(dependencyStatus)
	claimRecorded := providerReviewAttemptClaimRecorded(operation)
	claimMetadataReady := status == "planned" && dependencyReady && idempotencyReady && responseDiagnosticsReady && operationEndpointReady
	claimState := "blocked"
	if claimRecorded {
		claimState = "claimed"
	}
	blockedReasons := []string{
		"provider_review_adapter_not_implemented",
		"provider_review_mutation_not_armed",
	}
	if status != "planned" && !claimRecorded {
		blockedReasons = append([]string{"provider_review_attempt_status_not_planned"}, blockedReasons...)
	}
	if !dependencyReady {
		blockedReasons = append([]string{"provider_review_dependency_not_ready"}, blockedReasons...)
	}
	if !idempotencyReady {
		blockedReasons = append([]string{"provider_review_idempotency_metadata_missing"}, blockedReasons...)
	}
	if !responseDiagnosticsReady {
		blockedReasons = append([]string{"provider_review_response_diagnostics_missing"}, blockedReasons...)
	}
	if !operationEndpointReady {
		blockedReasons = append([]string{"provider_review_attempt_operation_endpoint_invalid"}, blockedReasons...)
	}
	return map[string]any{
		"mode":                            "redacted_attempt_execution_claim_plan",
		"claim_state":                     claimState,
		"claim_ready":                     false,
		"claim_metadata_ready":            claimMetadataReady,
		"operation_name":                  operationName,
		"endpoint_key":                    endpointKey,
		"provider_type":                   providerType,
		"operation_order":                 intFromAny(operation["operation_order"], 0),
		"attempt_status":                  status,
		"dependency_status":               dependencyStatus,
		"dependency_ready":                dependencyReady,
		"operation_endpoint_ready":        operationEndpointReady,
		"claim_status_from":               "planned",
		"claim_status_to":                 "running",
		"replay_check":                    safeProviderReviewReplayCheck(stringFromMap(operation, "replay_check")),
		"conflict_policy":                 safeProviderReviewConflictPolicy(stringFromMap(operation, "conflict_policy")),
		"retry_policy":                    safeProviderReviewRetryPolicy(stringFromMap(operation, "retry_policy")),
		"requires_attempt_status_planned": true,
		"requires_dependency_ready":       true,
		"requires_idempotency_ledger":     true,
		"requires_response_diagnostics":   true,
		"requires_optimistic_lock":        true,
		"requires_provider_adapter":       true,
		"requires_mutation_arming":        true,
		"idempotency_metadata_ready":      idempotencyReady,
		"response_diagnostics_ready":      responseDiagnosticsReady,
		"claim_recorded":                  claimRecorded,
		"claimed_at_recorded":             claimRecorded,
		"idempotency_claim_recorded":      false,
		"adapter_implemented":             false,
		"mutation_armed":                  false,
		"external_call_made":              false,
		"provider_api_call_made":          false,
		"provider_api_mutation":           "disabled",
		"idempotency_key_included":        false,
		"contains_token":                  false,
		"contains_provider_url":           false,
		"contains_repository_ref":         false,
		"contains_branch_name":            false,
		"contains_file_content":           false,
		"blocked_reasons":                 blockedReasons,
	}
}

func providerReviewAttemptClaimRecorded(operation map[string]any) bool {
	value, ok := operation["claimed_at"]
	if !ok || value == nil {
		return false
	}
	claimedAt := cleanOptionalText(fmt.Sprint(value))
	return claimedAt != "" && claimedAt != "<nil>"
}

func safeProviderReviewAttemptClaimDependencyStatus(value string) string {
	switch cleanOptionalText(value) {
	case "independent", "waiting_for_dependency", "dependency_satisfied", "dependency_failed":
		return cleanOptionalText(value)
	default:
		return "blocked"
	}
}

func providerReviewAttemptClaimDependencyReady(status string) bool {
	switch safeProviderReviewAttemptClaimDependencyStatus(status) {
	case "independent", "dependency_satisfied":
		return true
	default:
		return false
	}
}

func providerReviewProviderFromEndpointKey(endpointKey string) string {
	switch safeProviderReviewEndpointKey(endpointKey) {
	case "github.create_branch_ref", "github.commit_files", "github.open_review":
		return "github"
	case "gitea.create_branch_ref", "gitea.commit_files", "gitea.open_review":
		return "gitea"
	default:
		return ""
	}
}

func providerReviewAdapterKindForProvider(provider string) string {
	switch cleanOptionalText(provider) {
	case "github":
		return "github_provider_review_adapter"
	case "gitea":
		return "gitea_provider_review_adapter"
	default:
		return ""
	}
}

func providerReviewMethodForOperation(operationName string) string {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref", "open_review_request":
		return "POST"
	case "commit_starter_files":
		return "PUT"
	default:
		return ""
	}
}

func providerReviewPayloadShapeForOperation(operationName string) string {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref":
		return "ref_from_target_branch"
	case "commit_starter_files":
		return "content_redacted_file_batch"
	case "open_review_request":
		return "review_request"
	default:
		return ""
	}
}

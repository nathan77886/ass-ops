package app

import (
	"strings"
)

func providerReviewEndpointOperationForAttempt(operationName string) string {
	// Attempt operation names describe ledger steps; endpoint operation names describe provider API routes.
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref":
		return "create_branch_ref"
	case "commit_starter_files":
		return "commit_files"
	case "open_review_request":
		return "open_review"
	default:
		return ""
	}
}

func providerReviewAttemptEndpointMatchesOperation(providerType, operationName, endpointKey string) bool {
	providerType = safeProviderReviewProviderType(providerType)
	operationName = safeProviderReviewAttemptOperationName(operationName)
	endpointKey = safeProviderReviewEndpointKey(endpointKey)
	endpointOperation := providerReviewEndpointOperationForAttempt(operationName)
	return providerType != "" &&
		operationName != "" &&
		endpointKey != "" &&
		endpointOperation != "" &&
		endpointKey == providerReviewEndpointKey(providerType, endpointOperation)
}

func providerReviewAttemptPlanMatchesOperation(plan map[string]any, mode, operationName, endpointKey string) bool {
	operationName = safeProviderReviewAttemptOperationName(operationName)
	endpointKey = safeProviderReviewEndpointKey(endpointKey)
	return operationName != "" &&
		endpointKey != "" &&
		stringFromMap(plan, "mode") == mode &&
		safeProviderReviewAttemptOperationName(stringFromMap(plan, "operation_name")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(plan, "endpoint_key")) == endpointKey
}

func providerReviewAttemptClaimPlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["claim_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_execution_claim_plan", operationName, endpointKey)
}

func providerReviewAttemptClaimPlanIdempotencyReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["idempotency_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_execution_claim_plan", operationName, endpointKey)
}

func providerReviewAttemptResponsePlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	if !providerReviewAttemptPlanMatchesOperation(plan, providerReviewAttemptAdapterResponsePlanMode, operationName, endpointKey) {
		return false
	}
	expectedUnlockOperation := providerReviewAttemptDependencyUnlockOperation(operationName)
	expectedDependencyStatus := providerReviewAttemptDependencyUnlockStatus(expectedUnlockOperation)
	dependencyUnlockReady := cleanOptionalText(stringFromMap(plan, "dependency_unlocks_operation")) == expectedUnlockOperation
	if expectedUnlockOperation != "" {
		dependencyUnlockReady = safeProviderReviewAttemptOperationName(stringFromMap(plan, "dependency_unlocks_operation")) == expectedUnlockOperation
	}
	dependencyUpdateStatusReady := safeProviderReviewAttemptResponseDependencyStatus(stringFromMap(plan, "dependency_update_status")) == expectedDependencyStatus
	requiresDependencyUpdate, hasRequiresDependencyUpdate := plan["requires_dependency_update"]
	return safeProviderReviewAttemptStatus(stringFromMap(plan, "success_attempt_status")) == "completed" &&
		safeProviderReviewAttemptStatus(stringFromMap(plan, "retry_attempt_status")) == "planned" &&
		safeProviderReviewAttemptStatus(stringFromMap(plan, "failure_attempt_status")) == "failed" &&
		dependencyUnlockReady &&
		dependencyUpdateStatusReady &&
		hasRequiresDependencyUpdate &&
		boolOnlyFromAny(requiresDependencyUpdate) == (expectedUnlockOperation != "")
}

func safeProviderReviewAttemptResponseDependencyStatus(value string) string {
	switch cleanOptionalText(value) {
	case "", "dependency_satisfied":
		return cleanOptionalText(value)
	default:
		return "blocked"
	}
}

func providerReviewAttemptRequestPlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["request_materialization_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, providerReviewAttemptAdapterRequestMaterializationPlanMode, operationName, endpointKey)
}

func providerReviewAttemptBranchPolicyPlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["branch_policy_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_branch_policy_plan", operationName, endpointKey)
}

func providerReviewAttemptCredentialPlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["credential_binding_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_adapter_credential_binding_plan", operationName, endpointKey)
}

func providerReviewAttemptRuntimePlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["runtime_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_adapter_runtime_plan", operationName, endpointKey)
}

func providerReviewAttemptTransportPlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["transport_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_adapter_transport_plan", operationName, endpointKey)
}

func providerReviewAttemptProviderSendPlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["provider_send_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_adapter_provider_send_plan", operationName, endpointKey)
}

func providerReviewAttemptResponseRecordingReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["response_recording_ready"]) &&
		providerReviewAttemptResponsePlanReadyForOperation(plan, operationName, endpointKey)
}

func providerReviewAttemptTransactionPlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["transaction_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_adapter_transaction_plan", operationName, endpointKey)
}

func providerReviewAttemptExecutionLockPlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["execution_lock_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_adapter_execution_lock_plan", operationName, endpointKey)
}

func providerReviewAttemptActivationPlanReadyForOperation(plan map[string]any, operationName, endpointKey string) bool {
	return boolOnlyFromAny(plan["adapter_activation_metadata_ready"]) &&
		providerReviewAttemptPlanMatchesOperation(plan, "redacted_attempt_adapter_activation_plan", operationName, endpointKey)
}

func providerReviewAttemptPayloadBuilderMatchesOperation(operationName, payloadBuilder string) bool {
	operationName = safeProviderReviewAttemptOperationName(operationName)
	payloadBuilder = safeProviderReviewPayloadBuilderName(payloadBuilder)
	expectedBuilder := providerReviewExpectedPayloadBuilderName(operationName)
	return operationName != "" && expectedBuilder != "" && payloadBuilder == expectedBuilder
}

func providerReviewAttemptResponseHandlerMatchesOperation(operationName, responseHandler string) bool {
	operationName = safeProviderReviewAttemptOperationName(operationName)
	responseHandler = safeProviderReviewResponseHandlerName(responseHandler)
	expectedHandler := providerReviewExpectedResponseHandlerName(operationName)
	return operationName != "" && expectedHandler != "" && responseHandler == expectedHandler
}

func providerReviewExpectedPayloadBuilderName(operationName string) string {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref":
		return "build_redacted_branch_ref_request"
	case "commit_starter_files":
		return "build_redacted_file_batch_request"
	case "open_review_request":
		return "build_redacted_review_request"
	default:
		return ""
	}
}

func providerReviewExpectedResponseHandlerName(operationName string) string {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref":
		return "handle_branch_ref_response"
	case "commit_starter_files":
		return "handle_commit_files_response"
	case "open_review_request":
		return "handle_review_request_response"
	default:
		return ""
	}
}

func providerReviewEndpointPathTemplateKeyForOperation(providerType, operationName string) string {
	providerType = safeProviderReviewProviderType(providerType)
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref":
		switch providerType {
		case "github":
			return "github_git_refs_path_template"
		case "gitea":
			return "gitea_git_refs_path_template"
		}
	case "commit_starter_files":
		switch providerType {
		case "github":
			return "github_repository_contents_path_template"
		case "gitea":
			return "gitea_repository_contents_path_template"
		}
	case "open_review_request":
		switch providerType {
		case "github":
			return "github_pull_request_path_template"
		case "gitea":
			return "gitea_merge_request_path_template"
		}
	}
	return ""
}

func providerReviewAuthSchemeForProvider(provider string) string {
	switch strings.ToLower(cleanOptionalText(provider)) {
	case "github":
		return "bearer_token"
	case "gitea":
		return "token"
	default:
		return ""
	}
}

func providerReviewAcceptHeaderForProvider(provider string) string {
	switch strings.ToLower(cleanOptionalText(provider)) {
	case "github":
		return "application/vnd.github+json"
	case "gitea":
		return "application/json"
	default:
		return ""
	}
}

func providerReviewExpectedSuccessClassesForOperation(operationName string) []string {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref", "commit_starter_files", "open_review_request":
		return []string{"2xx"}
	default:
		return []string{}
	}
}

func providerReviewExpectedRetryClassesForOperation(operationName string) []string {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref", "commit_starter_files", "open_review_request":
		return []string{"5xx"}
	default:
		return []string{}
	}
}

func providerReviewTerminalFailureClassesForOperation(operationName string) []string {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref", "commit_starter_files", "open_review_request":
		return []string{"4xx"}
	default:
		return []string{}
	}
}

func providerReviewAttemptDependencyUnlockOperation(operationName string) string {
	switch safeProviderReviewAttemptOperationName(operationName) {
	case "create_branch_ref":
		return "commit_starter_files"
	case "commit_starter_files":
		return "open_review_request"
	default:
		return ""
	}
}

func providerReviewAttemptDependencyUnlockStatus(operationName string) string {
	if safeProviderReviewAttemptOperationName(operationName) == "" {
		return ""
	}
	return "dependency_satisfied"
}

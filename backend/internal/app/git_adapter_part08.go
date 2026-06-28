package app

import (
	"fmt"
	"strings"
)

func providerReviewPayloadBuilderName(operation string) string {
	if expected := providerReviewExpectedPayloadBuilderName(operation); expected != "" {
		return expected
	}
	return "build_redacted_provider_request"
}

func providerReviewResponseHandlerName(operation string) string {
	if expected := providerReviewExpectedResponseHandlerName(operation); expected != "" {
		return expected
	}
	return "handle_provider_response"
}

func providerReviewAdapterRehearsal(provider, reviewKind, adapterStatus string, credentialStrategy map[string]any, requestEnvelopes []map[string]any) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	credentialConfigured := boolValueFromAny(credentialStrategy["token_env_configured"])
	credentialPresent := boolValueFromAny(credentialStrategy["token_env_present"])
	readyCount := 0
	blockedCount := 0
	blockedReasons := []string{}
	seenBlockedReasons := map[string]bool{}
	addBlockedReason := func(reason string) {
		reason = providerReviewRehearsalBlockedReason(reason)
		if reason == "" || seenBlockedReasons[reason] {
			return
		}
		seenBlockedReasons[reason] = true
		blockedReasons = append(blockedReasons, reason)
	}
	if adapterStatus != "planned" {
		addBlockedReason("provider_review_api_adapter")
	}
	if !credentialConfigured {
		addBlockedReason("provider_credential_configured")
	}
	if !credentialPresent {
		addBlockedReason("provider_token_env_present")
	}
	operations := make([]map[string]any, 0, len(requestEnvelopes))
	for _, envelope := range requestEnvelopes {
		status := "ready"
		operationBlockedReasons := []string{}
		operationSeen := map[string]bool{}
		for _, readiness := range mapSliceFromAny(envelope["readiness"]) {
			if stringFromMap(readiness, "status") == "ready" {
				continue
			}
			reason := providerReviewRehearsalBlockedReason(stringFromMap(readiness, "evidence"))
			if reason == "" || operationSeen[reason] {
				continue
			}
			operationSeen[reason] = true
			operationBlockedReasons = append(operationBlockedReasons, reason)
			addBlockedReason(reason)
		}
		if len(operationBlockedReasons) > 0 {
			status = "blocked"
			blockedCount++
		} else {
			readyCount++
		}
		operations = append(operations, map[string]any{
			"name":                   cleanOptionalText(stringFromMap(envelope, "name")),
			"endpoint_key":           cleanOptionalText(stringFromMap(envelope, "endpoint_key")),
			"status":                 status,
			"blocked_reasons":        operationBlockedReasons,
			"external_call_made":     false,
			"provider_api_call_made": false,
			"provider_api_mutation":  "disabled",
		})
	}
	status := "blocked"
	if adapterStatus == "planned" && credentialConfigured && credentialPresent && blockedCount == 0 && len(requestEnvelopes) > 0 {
		status = "ready"
	}
	return map[string]any{
		"status":                         status,
		"mode":                           "redacted_adapter_rehearsal",
		"provider_type":                  provider,
		"review_kind":                    reviewKind,
		"adapter_status":                 adapterStatus,
		"operation_count":                len(operations),
		"ready_operation_count":          readyCount,
		"blocked_operation_count":        blockedCount,
		"blocked_reasons":                blockedReasons,
		"operations":                     operations,
		"mutation_arming_candidate":      status == "ready",
		"external_call_made":             false,
		"provider_api_call_made":         false,
		"provider_api_mutation":          "disabled",
		"payload_redacted":               true,
		"contains_token":                 false,
		"contains_provider_url":          false,
		"contains_repository_ref":        false,
		"contains_file_content":          false,
		"requires_operator_review":       true,
		"requires_mutation_arming":       true,
		"adapter_mutation_currently_off": true,
	}
}

func providerReviewRehearsalBlockedReason(value string) string {
	switch strings.TrimSpace(value) {
	case "provider_review_api_adapter":
		return "provider_review_api_adapter"
	case "provider_credential_configured":
		return "provider_credential_configured"
	case "provider_token_env_present":
		return "provider_token_env_present"
	case "provider_api_request_plan_ready":
		return "provider_api_request_plan_ready"
	case "review_branch_refs_valid", "review_branches_valid":
		return "review_branches_valid"
	case "starter_file_payload_staged":
		return "starter_file_payload_staged"
	default:
		return ""
	}
}

func providerReviewAdapterContract(provider, reviewKind string, requestInputs ...map[string]any) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	supported := provider == "github" || provider == "gitea"
	adapterStatus := providerReviewAdapterStatus(provider, reviewKind)
	apiRequestPlan := map[string]any{}
	starterFilePayload := map[string]any{}
	if len(requestInputs) > 0 {
		apiRequestPlan = requestInputs[0]
	}
	if len(requestInputs) > 1 {
		starterFilePayload = requestInputs[1]
	}
	return map[string]any{
		"status":                map[bool]string{true: "planned", false: "unsupported"}[supported],
		"adapter_status":        adapterStatus,
		"contract_version":      "provider-review-v1",
		"provider_type":         provider,
		"review_kind":           reviewKind,
		"external_call_made":    false,
		"provider_api_mutation": "disabled",
		"contains_token":        false,
		"contains_file_content": false,
		"operations":            providerReviewAdapterContractOperations(provider, reviewKind),
		"request_envelopes":     providerReviewAdapterRequestEnvelopes(provider, reviewKind, apiRequestPlan, starterFilePayload),
		"response_diagnostics":  providerReviewAdapterResponseDiagnostics(provider, reviewKind),
		"idempotency_plan":      providerReviewAdapterIdempotencyPlan(provider, reviewKind),
		"next_step":             "Rehearse and arm operation adapters only after provider credentials, approval, payload staging, and protected-branch rules pass preflight.",
	}
}

func providerReviewAdapterStatus(provider, reviewKind string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	switch provider {
	case "github":
		if reviewKind == "pull_request" {
			return "planned"
		}
	case "gitea":
		if reviewKind == "merge_request" {
			return "planned"
		}
	}
	return "missing"
}

func providerReviewAdapterRequestEnvelopes(provider, reviewKind string, apiRequestPlan, starterFilePayload map[string]any) []map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	sourceBranch := cleanOptionalText(stringFromMap(apiRequestPlan, "source_branch"))
	targetBranch := cleanOptionalText(stringFromMap(apiRequestPlan, "target_branch"))
	fileCount := intFromAny(starterFilePayload["file_count"], intFromAny(apiRequestPlan["file_count"], 0))
	planReady := fmt.Sprint(apiRequestPlan["status"]) == "ready"
	starterReady := starterFilePayloadReady(starterFilePayload)
	branchRefsReady := sourceBranch != "" &&
		targetBranch != "" &&
		isSafeGitRefPart(sourceBranch) &&
		isSafeGitRefPart(targetBranch)

	return []map[string]any{
		providerReviewAdapterRequestEnvelope(
			provider,
			"create_branch_ref",
			"create_branch_ref",
			"POST",
			"ref_from_target_branch",
			0,
			branchRefsReady,
			planReady,
			starterReady,
			false,
		),
		providerReviewAdapterRequestEnvelope(
			provider,
			"commit_starter_files",
			"commit_files",
			"PUT",
			"content_redacted_file_batch",
			fileCount,
			branchRefsReady,
			planReady,
			starterReady,
			true,
		),
		providerReviewAdapterRequestEnvelope(
			provider,
			"open_review_request",
			"open_review",
			"POST",
			reviewKind,
			0,
			branchRefsReady,
			planReady,
			starterReady,
			false,
		),
	}
}

func providerReviewAdapterRequestEnvelope(
	provider,
	operation,
	endpointOperation,
	method,
	payloadShape string,
	fileCount int,
	branchRefsReady,
	planReady,
	starterReady,
	requiresStarterFiles bool,
) map[string]any {
	readiness := []map[string]any{
		{"evidence": "provider_api_request_plan_ready", "status": readyStatus(planReady)},
		{"evidence": "review_branch_refs_valid", "status": readyStatus(branchRefsReady)},
	}
	if requiresStarterFiles {
		readiness = append(readiness, map[string]any{
			"evidence": "starter_file_payload_staged",
			"status":   readyStatus(starterReady),
		})
	}
	return map[string]any{
		"name":                    operation,
		"method":                  method,
		"endpoint_key":            providerReviewEndpointKey(provider, endpointOperation),
		"payload_shape":           payloadShape,
		"file_count":              fileCount,
		"payload_redacted":        true,
		"contains_token":          false,
		"contains_file_content":   false,
		"contains_provider_url":   false,
		"contains_repository_ref": false,
		"api_call":                false,
		"provider_api_mutation":   "disabled",
		"execution_status":        "blocked",
		"blocked_reason":          "provider_review_mutation_armed",
		"readiness":               readiness,
	}
}

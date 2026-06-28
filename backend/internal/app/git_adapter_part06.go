package app

import (
	"fmt"
	"strings"
)

func providerReviewExecutionTargetSummary(provider, reviewKind string, apiRequestPlan, starterFilePayload, reconciliation map[string]any) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	sourceBranch := cleanOptionalText(stringFromMap(apiRequestPlan, "source_branch"))
	targetBranch := cleanOptionalText(stringFromMap(apiRequestPlan, "target_branch"))
	branchRefsReady := sourceBranch != "" &&
		targetBranch != "" &&
		isSafeGitRefPart(sourceBranch) &&
		isSafeGitRefPart(targetBranch)
	starterReady := starterFilePayloadReady(starterFilePayload)
	planReady := fmt.Sprint(apiRequestPlan["status"]) == "ready"
	fileCount := intFromAny(starterFilePayload["file_count"], intFromAny(apiRequestPlan["file_count"], 0))
	operations := make([]map[string]any, 0, len(mapSliceFromAny(apiRequestPlan["operations"])))
	for _, operation := range mapSliceFromAny(apiRequestPlan["operations"]) {
		operations = append(operations, map[string]any{
			"name":                  cleanOptionalText(stringFromMap(operation, "name")),
			"endpoint_key":          cleanOptionalText(stringFromMap(operation, "endpoint_key")),
			"payload_shape":         cleanOptionalText(stringFromMap(operation, "payload_shape")),
			"status":                "planned",
			"api_call":              false,
			"provider_api_mutation": "disabled",
			"payload_redacted":      true,
			"contains_token":        false,
			"contains_file_content": false,
		})
	}
	blockedReasons := append([]string{}, stringSliceFromAny(apiRequestPlan["blocked_reasons"])...)
	blockedSeen := map[string]bool{}
	for _, reason := range blockedReasons {
		blockedSeen[reason] = true
	}
	for _, reason := range stringSliceFromAny(reconciliation["blocked_reasons"]) {
		if reason != "" && !blockedSeen[reason] {
			blockedReasons = append(blockedReasons, reason)
			blockedSeen[reason] = true
		}
	}
	status := "blocked"
	if branchRefsReady && starterReady && planReady {
		status = "adapter_blocked"
		if stringFromMap(reconciliation, "adapter_status") == "planned" {
			status = "mutation_blocked"
		}
	}
	return map[string]any{
		"status":                          status,
		"mode":                            "redacted_execution_target_summary",
		"provider_type":                   provider,
		"review_kind":                     reviewKind,
		"source_branch":                   sourceBranch,
		"target_branch":                   targetBranch,
		"branch_refs_ready":               branchRefsReady,
		"starter_file_payload_ready":      starterReady,
		"provider_api_request_ready":      planReady,
		"file_count":                      fileCount,
		"operation_count":                 len(operations),
		"operations":                      operations,
		"adapter_status":                  cleanOptionalText(stringFromMap(reconciliation, "adapter_status")),
		"blocked_reasons":                 blockedReasons,
		"external_call_made":              false,
		"provider_api_call_made":          false,
		"provider_api_mutation":           "disabled",
		"payload_redacted":                true,
		"contains_token":                  false,
		"contains_provider_url":           false,
		"contains_repository_ref":         false,
		"contains_file_content":           false,
		"idempotency_key_included":        false,
		"requires_persisted_attempt":      true,
		"requires_response_diagnostics":   true,
		"requires_provider_api_adapter":   true,
		"requires_adapter_mutation_armed": true,
		"requires_operator_review":        true,
		"future_adapter_input_boundary":   "branch_ref_commit_review_request",
		"adapter_mutation_currently_off":  true,
	}
}

func templateProviderReviewAPIRequestPlan(provider, reviewKind, sourceBranch, targetBranch string, starterFilePayload map[string]any) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	sourceBranch = strings.TrimSpace(sourceBranch)
	targetBranch = strings.TrimSpace(targetBranch)
	fileCount := intFromAny(starterFilePayload["file_count"], 0)
	ready := sourceBranch != "" &&
		targetBranch != "" &&
		isSafeGitRefPart(sourceBranch) &&
		isSafeGitRefPart(targetBranch) &&
		starterFilePayloadReady(starterFilePayload)
	status := "blocked"
	blockedReasons := []string{}
	if sourceBranch == "" || targetBranch == "" || !isSafeGitRefPart(sourceBranch) || !isSafeGitRefPart(targetBranch) {
		blockedReasons = append(blockedReasons, "review_branches_valid")
	}
	if !starterFilePayloadReady(starterFilePayload) {
		blockedReasons = append(blockedReasons, "starter_file_payload_staged")
	}
	if ready {
		status = "ready"
	}
	return map[string]any{
		"status":                 status,
		"mode":                   "redacted_request_plan",
		"provider_type":          provider,
		"review_kind":            reviewKind,
		"source_branch":          sourceBranch,
		"target_branch":          targetBranch,
		"file_count":             fileCount,
		"payload_redacted":       true,
		"contains_token":         false,
		"contains_file_content":  false,
		"provider_api_call_made": false,
		"provider_api_mutation":  "disabled",
		"blocked_reasons":        blockedReasons,
		"operations": []map[string]any{
			{
				"name":                  "create_branch_ref",
				"method":                "POST",
				"endpoint_key":          providerReviewEndpointKey(provider, "create_branch_ref"),
				"payload_shape":         "ref_from_target_branch",
				"payload_redacted":      true,
				"contains_token":        false,
				"contains_file_content": false,
				"api_call":              false,
			},
			{
				"name":                  "commit_starter_files",
				"method":                "PUT",
				"endpoint_key":          providerReviewEndpointKey(provider, "commit_files"),
				"payload_shape":         "content_redacted_file_batch",
				"file_count":            fileCount,
				"payload_redacted":      true,
				"contains_token":        false,
				"contains_file_content": false,
				"api_call":              false,
			},
			{
				"name":                  "open_review_request",
				"method":                "POST",
				"endpoint_key":          providerReviewEndpointKey(provider, "open_review"),
				"payload_shape":         reviewKind,
				"payload_redacted":      true,
				"contains_token":        false,
				"contains_file_content": false,
				"api_call":              false,
			},
		},
	}
}

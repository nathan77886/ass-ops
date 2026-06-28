package app

import (
	"fmt"
	"strings"
)

func templateProviderReviewExecutionReconciliation(provider, reviewKind string, starterFilePayload, guardrail, apiRequestPlan map[string]any, credentialStrategies ...map[string]any) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	credentialStrategy := firstProviderReviewCredentialStrategy(credentialStrategies...)
	adapterContract := providerReviewAdapterContract(provider, reviewKind, apiRequestPlan, starterFilePayload)
	providerSupported := provider == "github" || provider == "gitea"
	starterReady := starterFilePayloadReady(starterFilePayload)
	planReady := fmt.Sprint(apiRequestPlan["status"]) == "ready"
	credentialConfigured := boolValueFromAny(credentialStrategy["token_env_configured"])
	credentialPresent := boolValueFromAny(credentialStrategy["token_env_present"])
	adapterStatus := providerReviewAdapterStatus(provider, reviewKind)
	adapterReady := adapterStatus == "planned"
	mutationArmed := boolValueFromAny(guardrail["mutation_armed_config"])
	executionEnabledConfig := boolValueFromAny(guardrail["execution_enabled_config"])
	requestEnvelopes := providerReviewAdapterRequestEnvelopes(provider, reviewKind, apiRequestPlan, starterFilePayload)
	adapterRehearsal := providerReviewAdapterRehearsal(provider, reviewKind, adapterStatus, credentialStrategy, requestEnvelopes)
	mutationArmingPlan := providerReviewMutationArmingPlan(provider, reviewKind, executionEnabledConfig, mutationArmed, adapterRehearsal)
	executionBlueprint := providerReviewAdapterExecutionBlueprint(provider, reviewKind, adapterStatus, requestEnvelopes, mutationArmingPlan)
	gates := []map[string]any{
		{
			"gate":              "provider_supported",
			"status":            map[bool]string{true: "ready", false: "blocked"}[providerSupported],
			"provider_type":     provider,
			"message":           "Provider review adapters are only planned for GitHub and Gitea.",
			"sensitive_payload": false,
		},
		{
			"gate":              "starter_file_payload_staged",
			"status":            map[bool]string{true: "ready", false: "blocked"}[starterReady],
			"message":           "Starter-file payload must be staged as a content-redacted summary before provider review execution.",
			"sensitive_payload": false,
		},
		{
			"gate":              "provider_api_request_plan_ready",
			"status":            map[bool]string{true: "ready", false: "blocked"}[planReady],
			"message":           "Provider API request plan must have valid branches and staged starter-file payload metadata.",
			"sensitive_payload": false,
		},
		{
			"gate":              "provider_review_execution_enabled",
			"status":            map[bool]string{true: "ready", false: "blocked"}[executionEnabledConfig],
			"required_config":   "ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION",
			"message":           "Provider review execution must be explicitly enabled before provider API mutation can be considered.",
			"sensitive_payload": false,
		},
		{
			"gate":              "provider_credential_configured",
			"status":            map[bool]string{true: "ready", false: "blocked"}[credentialConfigured],
			"mode":              cleanOptionalText(stringFromMap(credentialStrategy, "mode")),
			"message":           "Provider account token environment must be configured using an allowed ASSOPS provider token env name.",
			"sensitive_payload": false,
		},
		{
			"gate":              "provider_token_env_present",
			"status":            map[bool]string{true: "ready", false: "blocked"}[credentialPresent],
			"message":           "Provider token environment variable must be present at runtime before provider API mutation can be enabled.",
			"sensitive_payload": false,
		},
		{
			"gate":              "provider_review_api_adapter",
			"status":            map[bool]string{true: "ready", false: "blocked"}[adapterReady],
			"provider_type":     provider,
			"review_kind":       reviewKind,
			"adapter_status":    adapterStatus,
			"message":           "Provider branch creation, starter-file commit, and PR/MR adapter contract is registered for supported providers.",
			"sensitive_payload": false,
		},
		{
			"gate":              "provider_review_mutation_armed",
			"status":            map[bool]string{true: "ready", false: "blocked"}[mutationArmed],
			"required_config":   "ASSOPS_ARM_PROVIDER_REVIEW_MUTATION",
			"provider_type":     provider,
			"review_kind":       reviewKind,
			"adapter_status":    adapterStatus,
			"message":           "Provider API mutation remains disabled until the execution adapter is explicitly armed after rehearsal.",
			"sensitive_payload": false,
		},
	}
	blocked := make([]string, 0, len(gates))
	for _, gate := range gates {
		if gate["status"] != "ready" {
			blocked = append(blocked, stringFromMap(gate, "gate"))
		}
	}
	return map[string]any{
		"status":                 map[bool]string{true: "ready", false: "blocked"}[executionEnabledConfig && providerSupported && starterReady && planReady && credentialConfigured && credentialPresent && adapterReady && mutationArmed],
		"mode":                   "preflight_reconciliation",
		"provider_type":          provider,
		"review_kind":            reviewKind,
		"credential_strategy":    sanitizedProviderReviewCredentialStrategy(credentialStrategy),
		"adapter_contract":       adapterContract,
		"request_envelopes":      requestEnvelopes,
		"adapter_rehearsal":      adapterRehearsal,
		"mutation_arming_plan":   mutationArmingPlan,
		"execution_blueprint":    executionBlueprint,
		"response_diagnostics":   providerReviewAdapterResponseDiagnostics(provider, reviewKind),
		"idempotency_plan":       providerReviewAdapterIdempotencyPlan(provider, reviewKind),
		"adapter_status":         adapterStatus,
		"external_call_made":     false,
		"provider_api_call_made": false,
		"provider_api_mutation":  "disabled",
		"blocked_reasons":        blocked,
		"gates":                  gates,
		"operations": []map[string]any{
			{
				"name":               "create_branch_ref",
				"endpoint_key":       providerReviewEndpointKey(provider, "create_branch_ref"),
				"status":             "planned",
				"blocked_reason":     "provider_review_mutation_armed",
				"external_call_made": false,
			},
			{
				"name":               "commit_starter_files",
				"endpoint_key":       providerReviewEndpointKey(provider, "commit_files"),
				"status":             "planned",
				"blocked_reason":     "provider_review_mutation_armed",
				"external_call_made": false,
			},
			{
				"name":               "open_review_request",
				"endpoint_key":       providerReviewEndpointKey(provider, "open_review"),
				"status":             "planned",
				"blocked_reason":     "provider_review_mutation_armed",
				"external_call_made": false,
			},
		},
		"next_step": "Rehearse and arm the provider review execution adapter before enabling provider API mutation.",
	}
}

func providerReviewMutationArmingPlan(provider, reviewKind string, executionEnabledConfig, mutationArmed bool, adapterRehearsal map[string]any) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	rehearsalReady := stringFromMap(adapterRehearsal, "status") == "ready" && boolValueFromAny(adapterRehearsal["mutation_arming_candidate"])
	blockedReasons := []string{}
	if !executionEnabledConfig {
		blockedReasons = append(blockedReasons, "provider_review_execution_enabled")
	}
	if !rehearsalReady {
		blockedReasons = append(blockedReasons, "provider_review_adapter_rehearsal")
	}
	if !mutationArmed {
		blockedReasons = append(blockedReasons, "provider_review_mutation_armed")
	}
	status := "blocked"
	if executionEnabledConfig && rehearsalReady && !mutationArmed {
		status = "ready_to_arm"
	}
	if mutationArmed && executionEnabledConfig && rehearsalReady {
		status = "armed"
	}
	return map[string]any{
		"status":                         status,
		"mode":                           "redacted_mutation_arming_plan",
		"provider_type":                  provider,
		"review_kind":                    reviewKind,
		"required_config":                "ASSOPS_ARM_PROVIDER_REVIEW_MUTATION",
		"execution_enabled_config":       executionEnabledConfig,
		"adapter_rehearsal_ready":        rehearsalReady,
		"mutation_armed_config":          mutationArmed,
		"mutation_armed":                 mutationArmed,
		"blocked_reasons":                blockedReasons,
		"external_call_made":             false,
		"provider_api_call_made":         false,
		"provider_api_mutation":          "disabled",
		"contains_token":                 false,
		"contains_provider_url":          false,
		"contains_repository_ref":        false,
		"contains_file_content":          false,
		"requires_operator_review":       true,
		"requires_adapter_rehearsal":     true,
		"adapter_mutation_currently_off": true,
		"next_step":                      "Only arm provider review mutation after rehearsal evidence, operator approval, and environment-specific rollout controls are reviewed.",
	}
}

func providerReviewAdapterExecutionBlueprint(provider, reviewKind, adapterStatus string, requestEnvelopes []map[string]any, mutationArmingPlan map[string]any) map[string]any {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reviewKind = strings.ToLower(strings.TrimSpace(reviewKind))
	mutationArmed := stringFromMap(mutationArmingPlan, "status") == "armed" && boolValueFromAny(mutationArmingPlan["mutation_armed"])
	status := "blocked"
	if adapterStatus == "planned" && mutationArmed {
		status = "ready_for_adapter_implementation"
	}
	operations := make([]map[string]any, 0, len(requestEnvelopes))
	for _, envelope := range requestEnvelopes {
		ready := providerReviewAdapterRequestEnvelopeReady(envelope) && mutationArmed
		operations = append(operations, providerReviewAdapterExecutionBlueprintOperation(envelope, ready))
	}
	return map[string]any{
		"status":                         status,
		"mode":                           "redacted_adapter_execution_blueprint",
		"provider_type":                  provider,
		"review_kind":                    reviewKind,
		"adapter_status":                 adapterStatus,
		"operation_count":                len(operations),
		"operations":                     operations,
		"execution_stage":                "adapter_implementation_required",
		"live_adapter_implemented":       false,
		"requires_provider_client":       true,
		"requires_request_builder":       true,
		"requires_response_handler":      true,
		"requires_idempotency_ledger":    true,
		"requires_operator_review":       true,
		"requires_mutation_arming":       true,
		"external_call_made":             false,
		"provider_api_call_made":         false,
		"provider_api_mutation":          "disabled",
		"payload_redacted":               true,
		"contains_token":                 false,
		"contains_provider_url":          false,
		"contains_repository_ref":        false,
		"contains_branch_name":           false,
		"contains_file_content":          false,
		"adapter_mutation_currently_off": true,
		"next_step":                      "Implement provider-specific request builder, response handler, and persisted attempt execution before live API mutation is enabled.",
	}
}

func providerReviewAdapterRequestEnvelopeReady(envelope map[string]any) bool {
	for _, item := range mapSliceFromAny(envelope["readiness"]) {
		if cleanOptionalText(stringFromMap(item, "status")) != "ready" {
			return false
		}
	}
	return len(mapSliceFromAny(envelope["readiness"])) > 0
}

func providerReviewAdapterExecutionBlueprintOperation(envelope map[string]any, ready bool) map[string]any {
	name := cleanOptionalText(stringFromMap(envelope, "name"))
	endpointKey := cleanOptionalText(stringFromMap(envelope, "endpoint_key"))
	payloadShape := cleanOptionalText(stringFromMap(envelope, "payload_shape"))
	executionStatus := "blocked"
	if ready {
		executionStatus = "ready_for_adapter_implementation"
	}
	return map[string]any{
		"name":                        name,
		"endpoint_key":                endpointKey,
		"method":                      cleanOptionalText(stringFromMap(envelope, "method")),
		"payload_shape":               payloadShape,
		"execution_status":            executionStatus,
		"payload_builder":             providerReviewPayloadBuilderName(name),
		"response_handler":            providerReviewResponseHandlerName(name),
		"idempotency_scope":           "operation_scope_hash",
		"request_body_included":       false,
		"response_body_included":      false,
		"headers_included":            false,
		"payload_redacted":            true,
		"contains_token":              false,
		"contains_provider_url":       false,
		"contains_repository_ref":     false,
		"contains_branch_name":        false,
		"contains_file_content":       false,
		"api_call":                    false,
		"external_call_made":          false,
		"provider_api_call_made":      false,
		"provider_api_mutation":       "disabled",
		"requires_provider_client":    true,
		"requires_request_builder":    true,
		"requires_response_handler":   true,
		"requires_idempotency_ledger": true,
	}
}

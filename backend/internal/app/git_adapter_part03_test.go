package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestTemplateProviderReviewExecutionPlanUsesProviderTerms(t *testing.T) {
	githubPlan := templateProviderReviewExecutionPlan("github", map[string]any{
		"mode":            "pull_request",
		"provider_type":   "github",
		"proposed_branch": "assops/template/demo-main",
		"target_branch":   "main",
	})
	if githubPlan["review_kind"] != "pull_request" || githubPlan["provider_api_mutation"] != "disabled" {
		t.Fatalf("github execution plan = %#v", githubPlan)
	}
	githubGuardrail := mapFromAny(githubPlan["execution_guardrail"])
	if githubGuardrail["execution_mode"] != "disabled" || githubGuardrail["branch_creation_allowed"] != false || githubGuardrail["review_request_allowed"] != false {
		t.Fatalf("github execution guardrail = %#v", githubGuardrail)
	}
	if githubGuardrail["execution_enabled_config"] != false {
		t.Fatalf("github execution guardrail should record disabled config: %#v", githubGuardrail)
	}
	githubAPIPlan := mapFromAny(githubPlan["provider_api_request_plan"])
	if githubAPIPlan["status"] != "blocked" ||
		githubAPIPlan["payload_redacted"] != true ||
		githubAPIPlan["contains_token"] != false ||
		githubAPIPlan["contains_file_content"] != false ||
		githubAPIPlan["provider_api_call_made"] != false ||
		githubAPIPlan["provider_api_mutation"] != "disabled" {
		t.Fatalf("github provider api request plan = %#v", githubAPIPlan)
	}
	if !containsString(stringSliceFromAny(githubAPIPlan["blocked_reasons"]), "starter_file_payload_staged") {
		t.Fatalf("github provider api request plan blocked reasons = %#v", githubAPIPlan)
	}
	apiOperations := sliceOfMapsFromAny(githubAPIPlan["operations"])
	if len(apiOperations) != 3 || apiOperations[0]["endpoint_key"] != "github.create_branch_ref" {
		t.Fatalf("github provider api request operations = %#v", apiOperations)
	}
	reconciliation := mapFromAny(githubPlan["provider_review_reconciliation"])
	if reconciliation["status"] != "blocked" ||
		reconciliation["mode"] != "preflight_reconciliation" ||
		reconciliation["adapter_status"] != "planned" ||
		reconciliation["external_call_made"] != false ||
		reconciliation["provider_api_mutation"] != "disabled" {
		t.Fatalf("github provider review reconciliation = %#v", reconciliation)
	}
	if containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_review_api_adapter") ||
		!containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_review_mutation_armed") ||
		!containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "starter_file_payload_staged") ||
		!containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_api_request_plan_ready") ||
		!containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_review_execution_enabled") ||
		!containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_credential_configured") ||
		!containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_token_env_present") {
		t.Fatalf("github provider review reconciliation blocked reasons = %#v", reconciliation)
	}
	reconcileOperations := sliceOfMapsFromAny(reconciliation["operations"])
	if len(reconcileOperations) != 3 || reconcileOperations[0]["endpoint_key"] != "github.create_branch_ref" {
		t.Fatalf("github provider review reconciliation operations = %#v", reconcileOperations)
	}
	adapterContract := mapFromAny(reconciliation["adapter_contract"])
	if adapterContract["status"] != "planned" ||
		adapterContract["adapter_status"] != "planned" ||
		adapterContract["contract_version"] != "provider-review-v1" ||
		adapterContract["external_call_made"] != false ||
		adapterContract["provider_api_mutation"] != "disabled" {
		t.Fatalf("github provider review adapter contract = %#v", adapterContract)
	}
	adapterOperations := sliceOfMapsFromAny(adapterContract["operations"])
	if len(adapterOperations) != 3 ||
		adapterOperations[0]["required_capability"] != "branch_ref_write" ||
		adapterOperations[0]["adapter_status"] != "planned" ||
		adapterOperations[1]["required_capability"] != "file_content_write" ||
		adapterOperations[2]["required_capability"] != "review_request_write" {
		t.Fatalf("github provider review adapter operations = %#v", adapterOperations)
	}
	requestEnvelopes := sliceOfMapsFromAny(reconciliation["request_envelopes"])
	if len(requestEnvelopes) != 3 ||
		requestEnvelopes[0]["endpoint_key"] != "github.create_branch_ref" ||
		requestEnvelopes[1]["endpoint_key"] != "github.commit_files" ||
		requestEnvelopes[2]["endpoint_key"] != "github.open_review" {
		t.Fatalf("github provider review request envelopes = %#v", requestEnvelopes)
	}
	if requestEnvelopes[1]["contains_token"] != false ||
		requestEnvelopes[1]["contains_file_content"] != false ||
		requestEnvelopes[1]["contains_provider_url"] != false ||
		requestEnvelopes[1]["provider_api_mutation"] != "disabled" {
		t.Fatalf("github provider review request envelope should be redacted/no-call: %#v", requestEnvelopes[1])
	}
	requestReadiness := sliceOfMapsFromAny(requestEnvelopes[1]["readiness"])
	if len(requestReadiness) != 3 || requestReadiness[2]["evidence"] != "starter_file_payload_staged" {
		t.Fatalf("github provider review request envelope readiness = %#v", requestReadiness)
	}
	adapterRehearsal := mapFromAny(reconciliation["adapter_rehearsal"])
	if adapterRehearsal["status"] != "blocked" ||
		adapterRehearsal["adapter_status"] != "planned" ||
		adapterRehearsal["operation_count"] != 3 ||
		adapterRehearsal["ready_operation_count"] != 0 ||
		adapterRehearsal["blocked_operation_count"] != 3 ||
		adapterRehearsal["mutation_arming_candidate"] != false ||
		adapterRehearsal["provider_api_mutation"] != "disabled" ||
		adapterRehearsal["provider_api_call_made"] != false {
		t.Fatalf("github provider review adapter rehearsal = %#v", adapterRehearsal)
	}
	rehearsalReasons := stringSliceFromAny(adapterRehearsal["blocked_reasons"])
	if !containsString(rehearsalReasons, "starter_file_payload_staged") ||
		!containsString(rehearsalReasons, "provider_api_request_plan_ready") ||
		!containsString(rehearsalReasons, "provider_credential_configured") ||
		!containsString(rehearsalReasons, "provider_token_env_present") ||
		containsString(rehearsalReasons, "provider_review_mutation_armed") {
		t.Fatalf("github provider review adapter rehearsal reasons = %#v", rehearsalReasons)
	}
	mutationArmingPlan := mapFromAny(reconciliation["mutation_arming_plan"])
	if mutationArmingPlan["status"] != "blocked" ||
		mutationArmingPlan["mode"] != "redacted_mutation_arming_plan" ||
		mutationArmingPlan["required_config"] != "ASSOPS_ARM_PROVIDER_REVIEW_MUTATION" ||
		mutationArmingPlan["execution_enabled_config"] != false ||
		mutationArmingPlan["adapter_rehearsal_ready"] != false ||
		mutationArmingPlan["mutation_armed"] != false ||
		mutationArmingPlan["provider_api_mutation"] != "disabled" ||
		mutationArmingPlan["provider_api_call_made"] != false {
		t.Fatalf("github provider review mutation arming plan = %#v", mutationArmingPlan)
	}
	armingReasons := stringSliceFromAny(mutationArmingPlan["blocked_reasons"])
	if !containsString(armingReasons, "provider_review_execution_enabled") ||
		!containsString(armingReasons, "provider_review_adapter_rehearsal") ||
		!containsString(armingReasons, "provider_review_mutation_armed") {
		t.Fatalf("github provider review mutation arming reasons = %#v", armingReasons)
	}
	responseDiagnostics := mapFromAny(reconciliation["response_diagnostics"])
	if responseDiagnostics["status"] != "pending" ||
		responseDiagnostics["mode"] != "redacted_response_diagnostics" ||
		responseDiagnostics["response_body_included"] != false ||
		responseDiagnostics["headers_included"] != false ||
		responseDiagnostics["contains_token"] != false ||
		responseDiagnostics["contains_provider_url"] != false {
		t.Fatalf("github provider review response diagnostics = %#v", responseDiagnostics)
	}
	responseDiagnosticOperations := sliceOfMapsFromAny(responseDiagnostics["operations"])
	if len(responseDiagnosticOperations) != 3 ||
		responseDiagnosticOperations[0]["endpoint_key"] != "github.create_branch_ref" ||
		responseDiagnosticOperations[1]["endpoint_key"] != "github.commit_files" ||
		responseDiagnosticOperations[2]["endpoint_key"] != "github.open_review" {
		t.Fatalf("github provider review response diagnostic operations = %#v", responseDiagnosticOperations)
	}
	idempotencyPlan := mapFromAny(reconciliation["idempotency_plan"])
	if idempotencyPlan["status"] != "planned" ||
		idempotencyPlan["adapter_status"] != "planned" ||
		idempotencyPlan["mode"] != "redacted_idempotency_plan" ||
		idempotencyPlan["idempotency_key_included"] != false ||
		idempotencyPlan["contains_repository_ref"] != false ||
		idempotencyPlan["contains_branch_name"] != false ||
		idempotencyPlan["provider_api_mutation"] != "disabled" {
		t.Fatalf("github provider review idempotency plan = %#v", idempotencyPlan)
	}
	idempotencyOperations := sliceOfMapsFromAny(idempotencyPlan["operations"])
	if len(idempotencyOperations) != 3 ||
		idempotencyOperations[0]["replay_check"] != "detect_existing_branch_ref" ||
		idempotencyOperations[1]["conflict_policy"] != "block_on_content_or_parent_conflict" ||
		idempotencyOperations[2]["replay_check"] != "detect_existing_open_review" {
		t.Fatalf("github provider review idempotency operations = %#v", idempotencyOperations)
	}
	gates := sliceOfMapsFromAny(githubGuardrail["gates"])
	if len(gates) != 5 || gates[0]["gate"] != "provider_review_execution_enabled" || gates[1]["status"] != "ready" || gates[2]["gate"] != "provider_review_mutation_armed" || gates[3]["status"] != "ready" {
		t.Fatalf("github execution guardrail gates = %#v", gates)
	}
	githubRequest := mapFromAny(githubPlan["execution_request"])
	if githubRequest["status"] != "approval_ready" || githubRequest["review_kind"] != "pull_request" {
		t.Fatalf("github execution request = %#v", githubRequest)
	}
	if _, ok := githubRequest["blocked_reason"]; ok {
		t.Fatalf("approval-ready execution request should not include blocked_reason: %#v", githubRequest)
	}
	giteaPlan := templateProviderReviewExecutionPlan("gitea", map[string]any{
		"mode":            "merge_request",
		"provider_type":   "gitea",
		"proposed_branch": "assops/template/demo-main",
		"target_branch":   "main",
	})
	if giteaPlan["review_kind"] != "merge_request" || giteaPlan["execution_enabled"] != false {
		t.Fatalf("gitea execution plan = %#v", giteaPlan)
	}
	enabledGuardrail := templateProviderReviewExecutionGuardrail("github", "pull_request", "assops/template/demo-main", "main", true)
	if enabledGuardrail["execution_mode"] != "mutation_blocked" || enabledGuardrail["execution_enabled"] != false {
		t.Fatalf("enabled guardrail should remain mutation-blocked: %#v", enabledGuardrail)
	}
	if containsString(stringSliceFromAny(enabledGuardrail["blocked_reasons"]), "provider_review_api_adapter") ||
		!containsString(stringSliceFromAny(enabledGuardrail["blocked_reasons"]), "provider_review_mutation_armed") {
		t.Fatalf("enabled guardrail should still require mutation arming: %#v", enabledGuardrail)
	}
	armingOnlyGuardrail := templateProviderReviewExecutionGuardrailWithStaging("github", "pull_request", "assops/template/demo-main", "main", false, true, true)
	if armingOnlyGuardrail["execution_mode"] != "disabled" ||
		armingOnlyGuardrail["mutation_armed_config"] != false ||
		!containsString(stringSliceFromAny(armingOnlyGuardrail["blocked_reasons"]), "provider_review_execution_enabled") ||
		!containsString(stringSliceFromAny(armingOnlyGuardrail["blocked_reasons"]), "provider_review_mutation_armed") {
		t.Fatalf("arming config should require execution config before becoming ready: %#v", armingOnlyGuardrail)
	}
	armedGuardrail := templateProviderReviewExecutionGuardrailWithStaging("github", "pull_request", "assops/template/demo-main", "main", true, true, true)
	if armedGuardrail["execution_mode"] != "mutation_armed_audit_only" ||
		armedGuardrail["execution_enabled"] != false ||
		armedGuardrail["execution_enabled_config"] != true ||
		armedGuardrail["mutation_armed_config"] != true ||
		armedGuardrail["provider_api_mutation"] != "disabled" ||
		containsString(stringSliceFromAny(armedGuardrail["blocked_reasons"]), "provider_review_mutation_armed") {
		t.Fatalf("armed guardrail should expose arming config while staying audit-only: %#v", armedGuardrail)
	}
	readyAPIPlan := templateProviderReviewAPIRequestPlan("github", "pull_request", "assops/template/demo-main", "main", map[string]any{
		"status":           "ready",
		"file_count":       2,
		"content_included": false,
	})
	if readyAPIPlan["status"] != "ready" || readyAPIPlan["file_count"] != 2 {
		t.Fatalf("ready provider api request plan = %#v", readyAPIPlan)
	}
	for _, operation := range sliceOfMapsFromAny(readyAPIPlan["operations"]) {
		if operation["api_call"] != false || operation["payload_redacted"] != true || operation["contains_token"] != false || operation["contains_file_content"] != false {
			t.Fatalf("ready api operation should be redacted/no-call: %#v", operation)
		}
	}
	armedReconciliation := templateProviderReviewExecutionReconciliation(
		"github",
		"pull_request",
		map[string]any{"status": "ready", "file_count": 2, "content_included": false},
		armedGuardrail,
		readyAPIPlan,
		map[string]any{
			"mode":                 "provider_account_token_env",
			"token_env_configured": true,
			"token_env_present":    true,
		},
	)
	armedMutationPlan := mapFromAny(armedReconciliation["mutation_arming_plan"])
	if armedMutationPlan["status"] != "armed" ||
		armedMutationPlan["mutation_armed_config"] != true ||
		armedMutationPlan["mutation_armed"] != true ||
		armedMutationPlan["provider_api_call_made"] != false ||
		armedMutationPlan["provider_api_mutation"] != "disabled" {
		t.Fatalf("armed mutation plan should remain no-call: %#v", armedMutationPlan)
	}
	if armedReconciliation["status"] != "ready" || armedReconciliation["provider_api_mutation"] != "disabled" {
		t.Fatalf("armed reconciliation should be preflight-ready but mutation-disabled: %#v", armedReconciliation)
	}
	executionBlueprint := mapFromAny(armedReconciliation["execution_blueprint"])
	if executionBlueprint["status"] != "ready_for_adapter_implementation" ||
		executionBlueprint["mode"] != "redacted_adapter_execution_blueprint" ||
		executionBlueprint["live_adapter_implemented"] != false ||
		executionBlueprint["requires_provider_client"] != true ||
		executionBlueprint["requires_request_builder"] != true ||
		executionBlueprint["requires_response_handler"] != true ||
		executionBlueprint["requires_idempotency_ledger"] != true ||
		executionBlueprint["provider_api_call_made"] != false ||
		executionBlueprint["provider_api_mutation"] != "disabled" ||
		executionBlueprint["contains_token"] != false ||
		executionBlueprint["contains_provider_url"] != false ||
		executionBlueprint["contains_repository_ref"] != false ||
		executionBlueprint["contains_file_content"] != false {
		t.Fatalf("armed execution blueprint should be ready for implementation but no-call: %#v", executionBlueprint)
	}
	blueprintOperations := sliceOfMapsFromAny(executionBlueprint["operations"])
	if len(blueprintOperations) != 3 ||
		blueprintOperations[0]["payload_builder"] != "build_redacted_branch_ref_request" ||
		blueprintOperations[1]["payload_builder"] != "build_redacted_file_batch_request" ||
		blueprintOperations[2]["response_handler"] != "handle_review_request_response" ||
		blueprintOperations[0]["execution_status"] != "ready_for_adapter_implementation" ||
		blueprintOperations[1]["api_call"] != false ||
		blueprintOperations[1]["contains_file_content"] != false ||
		blueprintOperations[1]["provider_api_mutation"] != "disabled" {
		t.Fatalf("armed execution blueprint operations = %#v", blueprintOperations)
	}
	for _, operation := range sliceOfMapsFromAny(armedReconciliation["operations"]) {
		if operation["external_call_made"] != false {
			t.Fatalf("armed reconciliation operation should remain local-only: %#v", operation)
		}
	}
	for _, tt := range []struct {
		name   string
		source string
		target string
	}{
		{name: "missing source", source: "", target: "main"},
		{name: "missing target", source: "assops/template/demo-main", target: ""},
		{name: "missing both", source: "", target: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			blockedRequest := templateProviderReviewExecutionRequest("github", "pull_request", tt.source, tt.target)
			if blockedRequest["status"] != "blocked" || strings.TrimSpace(fmt.Sprint(blockedRequest["blocked_reason"])) == "" {
				t.Fatalf("blocked execution request = %#v", blockedRequest)
			}
		})
	}
	encoded, _ := json.Marshal(githubPlan)
	for _, leak := range []string{"ASSOPS_TEMPLATE_PROVIDER_TOKEN", "secret-token", "api_base_url"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("provider review execution plan leaked %q: %s", leak, encoded)
		}
	}
}

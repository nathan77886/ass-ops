package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectTemplateProviderReviewApprovalPayload(t *testing.T) {
	t.Setenv("ASSOPS_TEMPLATE_PROVIDER_TOKEN_GITHUB_ACCOUNT", "account-token")
	plan := templateProviderReviewExecutionPlan("github", map[string]any{
		"mode":            "pull_request",
		"provider_type":   "github",
		"proposed_branch": "assops/template/demo-main",
		"target_branch":   "main",
	})
	run := map[string]any{
		"id":         "11111111-1111-1111-1111-111111111111",
		"project_id": "22222222-2222-2222-2222-222222222222",
		"result": map[string]any{
			"template_files": []map[string]any{
				{"id": "33333333-3333-3333-3333-333333333333", "path": "README.md", "kind": "text", "status": "planned", "content": "do-not-include"},
			},
			"details": map[string]any{
				"repository_reconciliation": map[string]any{
					"credential_strategy": map[string]any{
						"mode":                      "provider_account_token_env",
						"provider_account_attached": true,
						"token_env_configured":      true,
						"token_env_present":         true,
						"token_stored":              false,
						"external_call_made":        false,
					},
					"provider_review_readiness": map[string]any{
						"execution_plan": plan,
					},
				},
			},
		},
	}
	payload, err := projectTemplateProviderReviewApprovalPayload(run)
	if err != nil {
		t.Fatalf("projectTemplateProviderReviewApprovalPayload: %v", err)
	}
	if payload["kind"] != "project_template_provider_review_execute" ||
		payload["project_template_run_id"] != "11111111-1111-1111-1111-111111111111" ||
		payload["provider_api_call_made"] != false ||
		payload["provider_api_mutation"] != "disabled" {
		t.Fatalf("payload = %#v", payload)
	}
	guardrail := mapFromAny(payload["execution_guardrail"])
	if guardrail["execution_mode"] != "disabled" ||
		guardrail["execution_enabled"] != false ||
		guardrail["execution_enabled_config"] != false ||
		guardrail["provider_api_call_made"] != false {
		t.Fatalf("payload guardrail = %#v", guardrail)
	}
	if !containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "provider_review_execution_enabled") {
		t.Fatalf("payload guardrail blocked reasons = %#v", guardrail)
	}
	if containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "starter_file_payload_staged") {
		t.Fatalf("starter file staging should be ready in approval payload: %#v", guardrail)
	}
	credential := mapFromAny(payload["credential_strategy"])
	if credential["mode"] != "provider_account_token_env" ||
		credential["provider_account_attached"] != true ||
		credential["token_env_configured"] != true ||
		credential["token_env_present"] != true ||
		credential["token_stored"] != false {
		t.Fatalf("credential strategy = %#v", credential)
	}
	starterPayload := mapFromAny(payload["starter_file_payload"])
	if starterPayload["status"] != "ready" || starterPayload["file_count"] != 1 || starterPayload["content_included"] != false {
		t.Fatalf("starter file payload = %#v", starterPayload)
	}
	starterFiles := sliceOfMapsFromAny(starterPayload["files"])
	if len(starterFiles) != 1 || starterFiles[0]["path"] != "README.md" {
		t.Fatalf("starter file summaries = %#v", starterFiles)
	}
	apiPlan := mapFromAny(payload["provider_api_request_plan"])
	if apiPlan["status"] != "ready" ||
		apiPlan["payload_redacted"] != true ||
		apiPlan["contains_token"] != false ||
		apiPlan["contains_file_content"] != false ||
		apiPlan["provider_api_call_made"] != false ||
		apiPlan["provider_api_mutation"] != "disabled" {
		t.Fatalf("provider api request plan = %#v", apiPlan)
	}
	operations := sliceOfMapsFromAny(apiPlan["operations"])
	if len(operations) != 3 || operations[0]["name"] != "create_branch_ref" || operations[1]["name"] != "commit_starter_files" || operations[2]["name"] != "open_review_request" {
		t.Fatalf("provider api request plan operations = %#v", operations)
	}
	for _, operation := range operations {
		if operation["api_call"] != false || operation["contains_token"] != false || operation["contains_file_content"] != false {
			t.Fatalf("provider api request plan operation should be redacted/no-call: %#v", operation)
		}
	}
	reconciliation := mapFromAny(payload["provider_review_reconciliation"])
	if reconciliation["status"] != "blocked" ||
		reconciliation["adapter_status"] != "planned" ||
		reconciliation["external_call_made"] != false ||
		reconciliation["provider_api_mutation"] != "disabled" {
		t.Fatalf("provider review reconciliation = %#v", reconciliation)
	}
	if containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_review_api_adapter") ||
		!containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_review_mutation_armed") {
		t.Fatalf("provider review reconciliation blocked reasons = %#v", reconciliation)
	}
	if containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "starter_file_payload_staged") ||
		containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_api_request_plan_ready") ||
		containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_credential_configured") ||
		containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_token_env_present") {
		t.Fatalf("provider review reconciliation should see staged payload, ready request plan, and credential preflight: %#v", reconciliation)
	}
	reconcileOperations := sliceOfMapsFromAny(reconciliation["operations"])
	if len(reconcileOperations) != 3 || reconcileOperations[0]["endpoint_key"] != "github.create_branch_ref" {
		t.Fatalf("provider review reconciliation operations = %#v", reconcileOperations)
	}
	adapterRehearsal := mapFromAny(reconciliation["adapter_rehearsal"])
	if adapterRehearsal["status"] != "ready" ||
		adapterRehearsal["adapter_status"] != "planned" ||
		adapterRehearsal["operation_count"] != 3 ||
		adapterRehearsal["ready_operation_count"] != 3 ||
		adapterRehearsal["blocked_operation_count"] != 0 ||
		adapterRehearsal["mutation_arming_candidate"] != true ||
		adapterRehearsal["provider_api_call_made"] != false ||
		adapterRehearsal["provider_api_mutation"] != "disabled" {
		t.Fatalf("provider review adapter rehearsal = %#v", adapterRehearsal)
	}
	mutationArmingPlan := mapFromAny(reconciliation["mutation_arming_plan"])
	if mutationArmingPlan["status"] != "blocked" ||
		mutationArmingPlan["execution_enabled_config"] != false ||
		mutationArmingPlan["adapter_rehearsal_ready"] != true ||
		mutationArmingPlan["mutation_armed_config"] != false ||
		mutationArmingPlan["mutation_armed"] != false ||
		mutationArmingPlan["provider_api_call_made"] != false ||
		mutationArmingPlan["provider_api_mutation"] != "disabled" {
		t.Fatalf("provider review mutation arming plan = %#v", mutationArmingPlan)
	}
	armingReasons := stringSliceFromAny(mutationArmingPlan["blocked_reasons"])
	if !containsString(armingReasons, "provider_review_execution_enabled") ||
		!containsString(armingReasons, "provider_review_mutation_armed") ||
		containsString(armingReasons, "provider_review_adapter_rehearsal") {
		t.Fatalf("provider review mutation arming reasons = %#v", armingReasons)
	}
	targetSummary := mapFromAny(payload["provider_review_target_summary"])
	if targetSummary["status"] != "mutation_blocked" ||
		targetSummary["mode"] != "redacted_execution_target_summary" ||
		targetSummary["branch_refs_ready"] != true ||
		targetSummary["starter_file_payload_ready"] != true ||
		targetSummary["provider_api_request_ready"] != true ||
		targetSummary["provider_api_mutation"] != "disabled" ||
		targetSummary["contains_token"] != false ||
		targetSummary["contains_provider_url"] != false ||
		targetSummary["contains_repository_ref"] != false ||
		targetSummary["contains_file_content"] != false {
		t.Fatalf("provider review target summary = %#v", targetSummary)
	}
	targetOperations := sliceOfMapsFromAny(targetSummary["operations"])
	if len(targetOperations) != 3 || targetOperations[0]["endpoint_key"] != "github.create_branch_ref" || targetOperations[1]["contains_file_content"] != false {
		t.Fatalf("provider review target operations = %#v", targetOperations)
	}
	request := mapFromAny(payload["execution_request"])
	if request["status"] != "approval_ready" ||
		request["approval_action"] != templateProviderReviewExecuteApprovalAction ||
		request["payload_redacted"] != true ||
		request["contains_token"] != false {
		t.Fatalf("execution request = %#v", request)
	}
	encoded, _ := json.Marshal(payload)
	for _, leak := range []string{"ASSOPS_TEMPLATE_PROVIDER_TOKEN", "secret-token", "api_base_url", "do-not-include"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("provider review approval payload leaked %q: %s", leak, encoded)
		}
	}

	blockedRun := map[string]any{
		"id": "11111111-1111-1111-1111-111111111111",
		"result": map[string]any{
			"details": map[string]any{
				"repository_reconciliation": map[string]any{
					"provider_review_readiness": map[string]any{
						"execution_plan": map[string]any{
							"execution_request": map[string]any{"status": "blocked"},
						},
					},
				},
			},
		},
	}
	if _, err := projectTemplateProviderReviewApprovalPayload(blockedRun); err == nil {
		t.Fatal("blocked provider review execution request should not build an approval payload")
	}
}

func TestProjectTemplateProviderReviewApprovalPayloadUsesRuntimeGuardrailConfig(t *testing.T) {
	plan := templateProviderReviewExecutionPlan("github", map[string]any{
		"mode":            "pull_request",
		"provider_type":   "github",
		"proposed_branch": "assops/template/demo-main",
		"target_branch":   "main",
	})
	payload, err := projectTemplateProviderReviewApprovalPayloadForConfig(map[string]any{
		"id":         "11111111-1111-1111-1111-111111111111",
		"project_id": "22222222-2222-2222-2222-222222222222",
		"result": map[string]any{
			"template_files": []map[string]any{
				{"id": "33333333-3333-3333-3333-333333333333", "path": "README.md", "kind": "text", "status": "planned"},
			},
			"details": map[string]any{
				"repository_reconciliation": map[string]any{
					"provider_review_readiness": map[string]any{"execution_plan": plan},
				},
			},
		},
	}, true, false)
	if err != nil {
		t.Fatalf("projectTemplateProviderReviewApprovalPayloadForConfig: %v", err)
	}
	guardrail := mapFromAny(payload["execution_guardrail"])
	if guardrail["execution_mode"] != "mutation_blocked" || guardrail["execution_enabled_config"] != true || guardrail["execution_enabled"] != false {
		t.Fatalf("runtime guardrail should reflect enabled config while staying blocked: %#v", guardrail)
	}
	apiPlan := mapFromAny(payload["provider_api_request_plan"])
	if apiPlan["status"] != "ready" || apiPlan["file_count"] != 1 {
		t.Fatalf("runtime api request plan = %#v", apiPlan)
	}
	if containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "provider_review_api_adapter") ||
		!containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "provider_review_mutation_armed") {
		t.Fatalf("runtime guardrail should remain mutation-blocked: %#v", guardrail)
	}
	reconciliation := mapFromAny(payload["provider_review_reconciliation"])
	mutationArmingPlan := mapFromAny(reconciliation["mutation_arming_plan"])
	if mutationArmingPlan["status"] != "blocked" ||
		mutationArmingPlan["execution_enabled_config"] != true ||
		mutationArmingPlan["adapter_rehearsal_ready"] != false ||
		mutationArmingPlan["mutation_armed_config"] != false ||
		mutationArmingPlan["mutation_armed"] != false ||
		mutationArmingPlan["provider_api_mutation"] != "disabled" {
		t.Fatalf("runtime mutation arming plan should still require rehearsal and stay mutation-off: %#v", mutationArmingPlan)
	}
}

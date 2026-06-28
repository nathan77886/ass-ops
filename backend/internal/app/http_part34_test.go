package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectTemplateProviderReviewApprovalPayloadUsesRuntimeArmingConfig(t *testing.T) {
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
					"credential_strategy": map[string]any{
						"mode":                      "provider_account_token_env",
						"provider_account_attached": true,
						"token_env_configured":      true,
						"token_env_present":         true,
						"token_stored":              false,
						"external_call_made":        false,
					},
					"provider_review_readiness": map[string]any{"execution_plan": plan},
				},
			},
		},
	}, true, true)
	if err != nil {
		t.Fatalf("projectTemplateProviderReviewApprovalPayloadForConfig: %v", err)
	}
	if payload["provider_api_call_made"] != false || payload["provider_api_mutation"] != "disabled" {
		t.Fatalf("armed approval payload should remain no-call: %#v", payload)
	}
	guardrail := mapFromAny(payload["execution_guardrail"])
	if guardrail["execution_mode"] != "mutation_armed_audit_only" ||
		guardrail["execution_enabled"] != false ||
		guardrail["execution_enabled_config"] != true ||
		guardrail["mutation_armed_config"] != true ||
		guardrail["provider_api_mutation"] != "disabled" ||
		containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "provider_review_mutation_armed") {
		t.Fatalf("armed approval guardrail should expose config while staying audit-only: %#v", guardrail)
	}
	reconciliation := mapFromAny(payload["provider_review_reconciliation"])
	if reconciliation["status"] != "ready" ||
		reconciliation["provider_api_call_made"] != false ||
		reconciliation["provider_api_mutation"] != "disabled" {
		t.Fatalf("armed approval reconciliation should be ready but no-call: %#v", reconciliation)
	}
	mutationArmingPlan := mapFromAny(reconciliation["mutation_arming_plan"])
	if mutationArmingPlan["status"] != "armed" ||
		mutationArmingPlan["execution_enabled_config"] != true ||
		mutationArmingPlan["adapter_rehearsal_ready"] != true ||
		mutationArmingPlan["mutation_armed_config"] != true ||
		mutationArmingPlan["mutation_armed"] != true ||
		mutationArmingPlan["provider_api_call_made"] != false ||
		mutationArmingPlan["provider_api_mutation"] != "disabled" {
		t.Fatalf("armed approval mutation arming plan should remain no-call: %#v", mutationArmingPlan)
	}
}

func TestProjectTemplateStarterFilePayloadSummaryBlocked(t *testing.T) {
	missing := projectTemplateStarterFilePayloadSummary(map[string]any{"result": map[string]any{}})
	if missing["status"] != "blocked" || starterFilePayloadReady(missing) {
		t.Fatalf("missing files should block starter payload: %#v", missing)
	}
	unsafe := projectTemplateStarterFilePayloadSummary(map[string]any{
		"result": map[string]any{
			"template_files": []map[string]any{
				{"path": "../secret.txt", "kind": "text", "content": "do-not-include"},
				{"path": "", "kind": "text", "content": "do-not-include"},
			},
		},
	})
	if unsafe["status"] != "blocked" || starterFilePayloadReady(unsafe) {
		t.Fatalf("unsafe files should block starter payload: %#v", unsafe)
	}
	encoded, _ := json.Marshal(unsafe)
	if strings.Contains(string(encoded), "do-not-include") {
		t.Fatalf("blocked starter payload leaked content: %s", encoded)
	}
}

func TestExecuteApprovedOperationProviderReviewIsAuditOnly(t *testing.T) {
	server := &Server{cfg: Config{ProviderReviewExecutionEnabled: true}}
	result, operationID, err := server.executeApprovedOperation(context.Background(), nil, map[string]any{
		"requested_by": "11111111-1111-1111-1111-111111111111",
		"request_payload": map[string]any{
			"kind":                    "project_template_provider_review_execute",
			"project_template_run_id": "22222222-2222-2222-2222-222222222222",
			"execution_request": map[string]any{
				"status":                "approval_ready",
				"provider_type":         "github",
				"review_kind":           "pull_request",
				"source_branch":         "assops/template/demo-main",
				"target_branch":         "main",
				"provider_api_mutation": "disabled",
			},
			"credential_strategy": map[string]any{
				"mode":                      "provider_account_token_env",
				"provider_account_attached": true,
				"token_env_configured":      true,
				"token_env_present":         true,
				"token_stored":              false,
				"external_call_made":        false,
			},
			"starter_file_payload": map[string]any{
				"status":           "ready",
				"file_count":       1,
				"content_included": false,
				"payload_redacted": true,
				"files": []map[string]any{
					{"id": "33333333-3333-3333-3333-333333333333", "path": "README.md", "kind": "text", "status": "planned", "content": "forged-content"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("executeApprovedOperation: %v", err)
	}
	if operationID != "" {
		t.Fatalf("provider review approval should not create an operation id, got %q", operationID)
	}
	if result["provider_api_call_made"] != false ||
		result["provider_api_mutation"] != "disabled" ||
		result["execution_enabled"] != false {
		t.Fatalf("provider review approval result should remain audit-only: %#v", result)
	}
	guardrail := mapFromAny(result["execution_guardrail"])
	if guardrail["execution_mode"] != "mutation_blocked" ||
		guardrail["execution_enabled_config"] != true ||
		guardrail["branch_creation_allowed"] != false ||
		guardrail["review_request_allowed"] != false {
		t.Fatalf("provider review execution guardrail should stay blocked: %#v", guardrail)
	}
	if containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "provider_review_api_adapter") ||
		!containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "provider_review_mutation_armed") ||
		containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "starter_file_payload_staged") {
		t.Fatalf("provider review execution blocked reasons = %#v", guardrail)
	}
	starterPayload := mapFromAny(result["starter_file_payload"])
	if starterPayload["status"] != "ready" || starterPayload["content_included"] != false {
		t.Fatalf("provider review execution starter file payload = %#v", starterPayload)
	}
	apiPlan := mapFromAny(result["provider_api_request_plan"])
	if apiPlan["status"] != "ready" ||
		apiPlan["provider_api_call_made"] != false ||
		apiPlan["provider_api_mutation"] != "disabled" ||
		apiPlan["contains_file_content"] != false {
		t.Fatalf("provider review execution api request plan = %#v", apiPlan)
	}
	reconciliation := mapFromAny(result["provider_review_reconciliation"])
	if reconciliation["status"] != "blocked" ||
		reconciliation["adapter_status"] != "planned" ||
		reconciliation["external_call_made"] != false ||
		reconciliation["provider_api_mutation"] != "disabled" {
		t.Fatalf("provider review execution reconciliation = %#v", reconciliation)
	}
	if containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_review_api_adapter") ||
		!containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_review_mutation_armed") {
		t.Fatalf("provider review execution reconciliation blocked reasons = %#v", reconciliation)
	}
	if containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_credential_configured") ||
		containsString(stringSliceFromAny(reconciliation["blocked_reasons"]), "provider_token_env_present") {
		t.Fatalf("provider review execution reconciliation should preserve credential preflight: %#v", reconciliation)
	}
	targetSummary := mapFromAny(result["provider_review_target_summary"])
	if targetSummary["status"] != "mutation_blocked" ||
		targetSummary["provider_api_call_made"] != false ||
		targetSummary["provider_api_mutation"] != "disabled" ||
		targetSummary["requires_provider_api_adapter"] != true ||
		targetSummary["contains_token"] != false ||
		targetSummary["contains_provider_url"] != false ||
		targetSummary["contains_repository_ref"] != false ||
		targetSummary["contains_file_content"] != false {
		t.Fatalf("provider review execution target summary = %#v", targetSummary)
	}
	encoded, _ := json.Marshal(result)
	for _, leak := range []string{"forged-content", "api_base_url", "secret-token"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("provider review execution result leaked %q: %s", leak, encoded)
		}
	}
}

func TestExecuteApprovedOperationProviderReviewArmedStillNoCall(t *testing.T) {
	server := &Server{cfg: Config{ProviderReviewExecutionEnabled: true, ProviderReviewMutationArmed: true}}
	result, operationID, err := server.executeApprovedOperation(context.Background(), nil, map[string]any{
		"requested_by": "11111111-1111-1111-1111-111111111111",
		"request_payload": map[string]any{
			"kind":                    "project_template_provider_review_execute",
			"project_template_run_id": "22222222-2222-2222-2222-222222222222",
			"execution_request": map[string]any{
				"status":                "approval_ready",
				"provider_type":         "github",
				"review_kind":           "pull_request",
				"source_branch":         "assops/template/demo-main",
				"target_branch":         "main",
				"provider_api_mutation": "disabled",
			},
			"credential_strategy": map[string]any{
				"mode":                      "provider_account_token_env",
				"provider_account_attached": true,
				"token_env_configured":      true,
				"token_env_present":         true,
				"token_stored":              false,
				"external_call_made":        false,
			},
			"starter_file_payload": map[string]any{
				"status":           "ready",
				"file_count":       1,
				"content_included": false,
				"payload_redacted": true,
				"files": []map[string]any{
					{"id": "33333333-3333-3333-3333-333333333333", "path": "README.md", "kind": "text", "status": "planned", "content": "forged-content"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("executeApprovedOperation: %v", err)
	}
	if operationID != "" {
		t.Fatalf("provider review approval should not create an operation id, got %q", operationID)
	}
	if result["provider_api_call_made"] != false ||
		result["provider_api_mutation"] != "disabled" ||
		result["execution_enabled"] != false {
		t.Fatalf("armed provider review approval result should remain audit-only: %#v", result)
	}
	guardrail := mapFromAny(result["execution_guardrail"])
	if guardrail["execution_mode"] != "mutation_armed_audit_only" ||
		guardrail["execution_enabled"] != false ||
		guardrail["execution_enabled_config"] != true ||
		guardrail["mutation_armed_config"] != true ||
		guardrail["branch_creation_allowed"] != false ||
		guardrail["review_request_allowed"] != false ||
		containsString(stringSliceFromAny(guardrail["blocked_reasons"]), "provider_review_mutation_armed") {
		t.Fatalf("armed provider review execution guardrail should stay no-call: %#v", guardrail)
	}
	reconciliation := mapFromAny(result["provider_review_reconciliation"])
	if reconciliation["status"] != "ready" ||
		reconciliation["external_call_made"] != false ||
		reconciliation["provider_api_call_made"] != false ||
		reconciliation["provider_api_mutation"] != "disabled" {
		t.Fatalf("armed provider review execution reconciliation = %#v", reconciliation)
	}
	mutationArmingPlan := mapFromAny(reconciliation["mutation_arming_plan"])
	if mutationArmingPlan["status"] != "armed" ||
		mutationArmingPlan["mutation_armed_config"] != true ||
		mutationArmingPlan["mutation_armed"] != true ||
		mutationArmingPlan["provider_api_call_made"] != false ||
		mutationArmingPlan["provider_api_mutation"] != "disabled" {
		t.Fatalf("armed provider review mutation arming plan = %#v", mutationArmingPlan)
	}
	encoded, _ := json.Marshal(result)
	for _, leak := range []string{"forged-content", "api_base_url", "secret-token"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("armed provider review execution result leaked %q: %s", leak, encoded)
		}
	}
}

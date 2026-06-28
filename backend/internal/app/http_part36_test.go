package app

import (
	"testing"
)

func TestOperationApprovalPayloadAuditProviderReviewAllowsMissingResponseDiagnostics(t *testing.T) {
	approval := map[string]any{
		"request_payload": map[string]any{
			"kind":                    "project_template_provider_review_execute",
			"project_template_run_id": "22222222-2222-2222-2222-222222222222",
			"project_id":              "11111111-1111-1111-1111-111111111111",
			"execution_request": map[string]any{
				"status":          "approval_ready",
				"approval_action": templateProviderReviewExecuteApprovalAction,
				"resource_type":   "project_template_run",
				"provider_type":   "github",
				"review_kind":     "pull_request",
			},
			"provider_review_reconciliation": map[string]any{
				"status":        "blocked",
				"mode":          "preflight_reconciliation",
				"provider_type": "github",
				"review_kind":   "pull_request",
				"adapter_contract": map[string]any{
					"status":           "planned",
					"adapter_status":   "missing",
					"contract_version": "provider-review-v1",
				},
				"adapter_status":         "missing",
				"external_call_made":     true,
				"provider_api_call_made": true,
				"provider_api_mutation":  "enabled",
			},
		},
	}
	audit := operationApprovalPayloadAudit(approval)
	reconciliation := mapFromAny(audit["provider_review_reconciliation"])
	if reconciliation["external_call_made"] != false ||
		reconciliation["provider_api_call_made"] != false ||
		reconciliation["provider_api_mutation"] != "disabled" {
		t.Fatalf("reconciliation should still be sanitized when response diagnostics are missing: %#v", reconciliation)
	}
	responseDiagnostics := mapFromAny(reconciliation["response_diagnostics"])
	if len(responseDiagnostics) != 0 {
		t.Fatalf("missing response diagnostics should remain empty: %#v", responseDiagnostics)
	}
	adapterContract := mapFromAny(reconciliation["adapter_contract"])
	contractResponseDiagnostics := mapFromAny(adapterContract["response_diagnostics"])
	if len(contractResponseDiagnostics) != 0 {
		t.Fatalf("missing contract response diagnostics should remain empty: %#v", contractResponseDiagnostics)
	}
	idempotencyPlan := mapFromAny(reconciliation["idempotency_plan"])
	if len(idempotencyPlan) != 0 {
		t.Fatalf("missing idempotency plan should remain empty: %#v", idempotencyPlan)
	}
	contractIdempotencyPlan := mapFromAny(adapterContract["idempotency_plan"])
	if len(contractIdempotencyPlan) != 0 {
		t.Fatalf("missing contract idempotency plan should remain empty: %#v", contractIdempotencyPlan)
	}
}

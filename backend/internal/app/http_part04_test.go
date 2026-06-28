package app

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestRollbackExecutionPlanReadOnly(t *testing.T) {
	plan := rollbackExecutionPlan("previewable", "read_only_preview")
	if plan["mode"] != "redacted_rollback_execution_plan" ||
		plan["plan_state"] != "blocked" ||
		plan["prerequisite_state"] != "metadata_available" ||
		plan["plan_ready"] != false ||
		plan["plan_ready_reason"] != "rollback_execution_backend_disabled" ||
		plan["execution_enabled"] != false ||
		plan["execution_mode"] != "read_only_preview" ||
		plan["requires_approval"] != true ||
		plan["approval_action"] != "deployment.rollback" ||
		plan["requires_environment_review"] != true ||
		plan["requires_kubeconfig_binding"] != true ||
		plan["requires_revision_verification"] != true ||
		plan["requires_manifest_diff"] != true ||
		plan["requires_dry_run_preflight"] != true ||
		plan["requires_operator_confirmation"] != true ||
		plan["rollback_request_materialized"] != false ||
		plan["revision_verified"] != false ||
		plan["manifest_diff_rendered"] != false ||
		plan["dry_run_performed"] != false ||
		plan["kubernetes_client_constructed"] != false ||
		plan["helm_rollback_invoked"] != false ||
		plan["kubectl_rollout_invoked"] != false ||
		plan["argocd_rollback_invoked"] != false ||
		plan["rollback_started"] != false ||
		plan["external_call_made"] != false ||
		plan["kubernetes_api_call_made"] != false ||
		plan["helm_command_invoked"] != false ||
		plan["rollback_mutation"] != "disabled" ||
		plan["kubeconfig_included"] != false ||
		plan["secret_included"] != false ||
		plan["manifest_body_included"] != false ||
		plan["helm_values_included"] != false ||
		plan["cluster_credential_included"] != false ||
		plan["revision_value_included"] != false ||
		plan["contains_token"] != false ||
		plan["contains_kubeconfig"] != false ||
		plan["contains_secret"] != false ||
		plan["contains_manifest_body"] != false ||
		plan["rollback_boundary_redacted"] != true {
		t.Fatalf("rollback execution plan = %#v", plan)
	}
	controls := stringSliceFromAny(plan["required_controls"])
	if len(controls) != 7 || controls[0] != "operation_approval" || controls[6] != "operator_confirmation" {
		t.Fatalf("rollback execution controls = %#v", controls)
	}
	disabledBackends := stringSliceFromAny(plan["disabled_backends"])
	if len(disabledBackends) != 4 || disabledBackends[0] != "helm_rollback" || disabledBackends[3] != "rollback_execute" {
		t.Fatalf("rollback execution disabled backends = %#v", disabledBackends)
	}
	suppressedFields := stringSliceFromAny(plan["suppressed_fields"])
	for _, field := range []string{"kubeconfig", "cluster_token", "authorization_header", "secret_manifest", "rendered_manifest", "helm_values", "image_pull_secret", "environment_secret", "revision_value"} {
		if !slices.Contains(suppressedFields, field) {
			t.Fatalf("rollback execution suppressed fields missing %q: %#v", field, suppressedFields)
		}
	}
	blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
	if len(blockedReasons) != 2 || blockedReasons[0] != "rollback_execution_backend_disabled" || blockedReasons[1] != "rollback_mutation_not_armed" {
		t.Fatalf("rollback execution blocked reasons = %#v", blockedReasons)
	}
	executionSequence := stringSliceFromAny(plan["execution_sequence"])
	if len(executionSequence) != 8 || executionSequence[0] != "request_approval" || executionSequence[7] != "start_rollback" {
		t.Fatalf("rollback execution sequence = %#v", executionSequence)
	}
	planEncoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal rollback execution plan: %v", err)
	}
	for _, leak := range []string{"apiVersion:", "kind: Secret", "Bearer ", "kubeconfig-data", "helm-values-secret", "revision-sha-secret"} {
		if strings.Contains(string(planEncoded), leak) {
			t.Fatalf("rollback execution plan leaked %q: %s", leak, planEncoded)
		}
	}

	blocked := rollbackExecutionPlan("blocked", "")
	if blocked["prerequisite_state"] != "metadata_blocked" ||
		blocked["execution_mode"] != "read_only_preview" ||
		blocked["plan_state"] != "blocked" ||
		blocked["rollback_mutation"] != "disabled" {
		t.Fatalf("blocked rollback execution plan = %#v", blocked)
	}
}

func TestRollbackExecutionPlanMatchesSQLPreviewContract(t *testing.T) {
	plan := rollbackExecutionPlan("previewable", "read_only_preview")
	for _, tt := range []struct {
		name  string
		value string
	}{
		{"mode", "redacted_rollback_execution_plan"},
		{"plan_state", "blocked"},
		{"plan_ready_reason", "rollback_execution_backend_disabled"},
		{"execution_mode", "read_only_preview"},
		{"approval_action", "deployment.rollback"},
		{"rollback_mutation", "disabled"},
	} {
		if plan[tt.name] != tt.value {
			t.Fatalf("rollbackExecutionPlan[%s] = %#v, want %q", tt.name, plan[tt.name], tt.value)
		}
	}
	for _, name := range []string{
		"plan_ready",
		"execution_enabled",
		"rollback_request_materialized",
		"revision_verified",
		"manifest_diff_rendered",
		"dry_run_performed",
		"kubernetes_client_constructed",
		"helm_rollback_invoked",
		"kubectl_rollout_invoked",
		"argocd_rollback_invoked",
		"rollback_started",
		"external_call_made",
		"kubernetes_api_call_made",
		"helm_command_invoked",
		"kubeconfig_included",
		"secret_included",
		"manifest_body_included",
		"helm_values_included",
		"cluster_credential_included",
		"revision_value_included",
		"contains_token",
		"contains_kubeconfig",
		"contains_secret",
		"contains_manifest_body",
	} {
		if plan[name] != false {
			t.Fatalf("rollbackExecutionPlan[%s] = %#v, want false", name, plan[name])
		}
	}
	for _, name := range []string{
		"requires_approval",
		"requires_environment_review",
		"requires_kubeconfig_binding",
		"requires_revision_verification",
		"requires_manifest_diff",
		"requires_dry_run_preflight",
		"requires_operator_confirmation",
		"rollback_boundary_redacted",
	} {
		if plan[name] != true {
			t.Fatalf("rollbackExecutionPlan[%s] = %#v, want true", name, plan[name])
		}
	}
	for _, tt := range []struct {
		name string
		want []string
	}{
		{"blocked_reasons", []string{"rollback_execution_backend_disabled", "rollback_mutation_not_armed"}},
		{"required_controls", []string{"operation_approval", "environment_review", "kubeconfig_binding", "revision_verification", "manifest_diff", "server_side_dry_run", "operator_confirmation"}},
		{"disabled_backends", []string{"helm_rollback", "kubectl_rollout_undo", "argocd_rollback", "rollback_execute"}},
		{"suppressed_fields", []string{"kubeconfig", "cluster_token", "authorization_header", "secret_manifest", "rendered_manifest", "helm_values", "image_pull_secret", "environment_secret", "revision_value"}},
		{"execution_sequence", []string{"request_approval", "bind_environment", "bind_kubeconfig", "verify_revision", "render_manifest_diff", "run_server_side_dry_run", "record_rollback_audit", "start_rollback"}},
	} {
		got := stringSliceFromAny(plan[tt.name])
		if !slices.Equal(got, tt.want) {
			t.Fatalf("rollbackExecutionPlan[%s] = %#v, want %#v", tt.name, got, tt.want)
		}
	}
}

func TestRollbackGuardrailSummary(t *testing.T) {
	empty := rollbackGuardrailSummary(nil)
	if empty["execution_enabled"] != false || empty["execution_mode"] != "read_only_preview" || empty["total"] != 0 {
		t.Fatalf("empty summary = %#v", empty)
	}

	summary := rollbackGuardrailSummary([]map[string]any{
		{
			"rollback_readiness":        "previewable",
			"rollback_executable":       false,
			"rollback_execution_mode":   "read_only_preview",
			"rollback_readiness_reason": "revision metadata is available",
		},
		{
			"rollback_readiness":  "blocked",
			"rollback_executable": false,
		},
	})
	if summary["execution_enabled"] != false || summary["execution_mode"] != "read_only_preview" {
		t.Fatalf("summary execution fields = %#v", summary)
	}
	if summary["previewable_count"] != 1 || summary["executable_count"] != 0 || summary["total"] != 2 {
		t.Fatalf("summary counts = %#v", summary)
	}
	if !strings.Contains(fmt.Sprint(summary["message"]), "disabled") {
		t.Fatalf("summary message = %#v", summary["message"])
	}

	executable := rollbackGuardrailSummary([]map[string]any{
		{
			"rollback_readiness":      "previewable",
			"rollback_executable":     true,
			"rollback_execution_mode": "approval_required",
		},
	})
	if executable["execution_enabled"] != true || executable["executable_count"] != 1 {
		t.Fatalf("executable summary = %#v", executable)
	}
	if !strings.Contains(fmt.Sprint(executable["message"]), "explicit approval") {
		t.Fatalf("executable summary message = %#v", executable["message"])
	}

	mixed := rollbackGuardrailSummary([]map[string]any{
		{"rollback_execution_mode": "read_only_preview"},
		{"rollback_execution_mode": "approval_required"},
	})
	if mixed["execution_mode"] != "mixed" {
		t.Fatalf("mixed execution mode = %#v", mixed)
	}
}

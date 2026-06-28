package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestDeploymentExecutionReadinessDryRun(t *testing.T) {
	ready := deploymentExecutionReadiness(map[string]any{
		"name":           "prod",
		"status":         "healthy",
		"cluster_name":   "prod-cluster",
		"namespace":      "billing",
		"argo_app_count": int64(2),
	})
	if ready["status"] != "planned" || ready["mode"] != "dry_run" || ready["execution_enabled"] != false || ready["external_call_made"] != false {
		t.Fatalf("ready deployment execution readiness = %#v", ready)
	}
	if ready["requires_approval"] != true || ready["execution_backend"] != "disabled" {
		t.Fatalf("ready deployment execution guardrails = %#v", ready)
	}
	plan := mapFromAny(ready["execution_plan"])
	if plan["mode"] != "redacted_deployment_execution_plan" ||
		plan["plan_state"] != "blocked" ||
		plan["prerequisite_state"] != "planned" ||
		plan["plan_ready"] != false ||
		plan["plan_ready_reason"] != "deployment_execution_backend_disabled" ||
		plan["execution_enabled"] != false ||
		plan["execution_backend"] != "disabled" ||
		plan["requires_approval"] != true ||
		plan["approval_action"] != "deployment.execute" ||
		plan["requires_environment_review"] != true ||
		plan["requires_kubeconfig_binding"] != true ||
		plan["requires_manifest_render"] != true ||
		plan["requires_dry_run_preflight"] != true ||
		plan["requires_rollback_plan"] != true ||
		plan["requires_operator_confirmation"] != true ||
		plan["target_metadata_ready"] != true ||
		plan["deployment_request_materialized"] != false ||
		plan["manifest_rendered"] != false ||
		plan["dry_run_performed"] != false ||
		plan["helm_release_bound"] != false ||
		plan["kubernetes_client_constructed"] != false ||
		plan["rollout_started"] != false ||
		plan["rollback_point_selected"] != false ||
		plan["external_call_made"] != false ||
		plan["kubernetes_api_call_made"] != false ||
		plan["helm_command_invoked"] != false ||
		plan["deployment_mutation"] != "disabled" ||
		plan["kubeconfig_included"] != false ||
		plan["secret_included"] != false ||
		plan["manifest_body_included"] != false ||
		plan["helm_values_included"] != false ||
		plan["cluster_credential_included"] != false ||
		plan["contains_token"] != false ||
		plan["contains_kubeconfig"] != false ||
		plan["contains_secret"] != false ||
		plan["contains_manifest_body"] != false ||
		plan["execution_boundary_redacted"] != true {
		t.Fatalf("ready deployment execution plan = %#v", plan)
	}
	controls := stringSliceFromAny(plan["required_controls"])
	if len(controls) != 7 || controls[0] != "operation_approval" || controls[6] != "operator_confirmation" {
		t.Fatalf("deployment execution controls = %#v", controls)
	}
	disabledBackends := stringSliceFromAny(plan["disabled_backends"])
	if len(disabledBackends) != 5 || disabledBackends[0] != "helm_upgrade" || disabledBackends[4] != "rollback_execute" {
		t.Fatalf("deployment execution disabled backends = %#v", disabledBackends)
	}
	suppressedFields := stringSliceFromAny(plan["suppressed_fields"])
	for _, field := range []string{"kubeconfig", "cluster_token", "authorization_header", "secret_manifest", "rendered_manifest", "helm_values", "image_pull_secret", "environment_secret"} {
		if !slices.Contains(suppressedFields, field) {
			t.Fatalf("deployment execution suppressed fields missing %q: %#v", field, suppressedFields)
		}
	}
	executionSequence := stringSliceFromAny(plan["execution_sequence"])
	if len(executionSequence) != 7 || executionSequence[0] != "request_approval" || executionSequence[6] != "start_rollout" {
		t.Fatalf("deployment execution sequence = %#v", executionSequence)
	}
	planEncoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal deployment execution plan: %v", err)
	}
	for _, leak := range []string{"apiVersion:", "kind: Secret", "Bearer ", "kubeconfig-data", "helm-values-secret"} {
		if strings.Contains(string(planEncoded), leak) {
			t.Fatalf("deployment execution plan leaked %q: %s", leak, planEncoded)
		}
	}
	steps := sliceOfMapsFromAny(ready["steps"])
	if len(steps) != 4 {
		t.Fatalf("ready deployment execution steps = %#v", steps)
	}
	for _, step := range steps {
		if step["execution"] != false {
			t.Fatalf("deployment execution step should be disabled: %#v", step)
		}
	}

	blocked := deploymentExecutionReadiness(map[string]any{
		"name":           "broken",
		"status":         "degraded",
		"cluster_name":   "",
		"namespace":      "",
		"argo_app_count": int64(0),
	})
	if blocked["status"] != "blocked" || blocked["execution_enabled"] != false {
		t.Fatalf("blocked deployment execution readiness = %#v", blocked)
	}
	blockedPlan := mapFromAny(blocked["execution_plan"])
	if blockedPlan["plan_state"] != "blocked" ||
		blockedPlan["prerequisite_state"] != "blocked" ||
		blockedPlan["target_metadata_ready"] != false ||
		blockedPlan["deployment_mutation"] != "disabled" ||
		blockedPlan["kubernetes_api_call_made"] != false ||
		blockedPlan["helm_command_invoked"] != false {
		t.Fatalf("blocked deployment execution plan = %#v", blockedPlan)
	}
	blockedPlanReasons := stringSliceFromAny(blockedPlan["blocked_reasons"])
	if len(blockedPlanReasons) < 4 || blockedPlanReasons[0] != "deployment_execution_backend_disabled" {
		t.Fatalf("blocked deployment execution plan reasons = %#v", blockedPlanReasons)
	}
	reasons := stringSliceFromAny(blocked["blocked_reasons"])
	for _, want := range []string{"status needs review", "cluster name is missing", "namespace is missing", "no Argo apps"} {
		found := false
		for _, reason := range reasons {
			if strings.Contains(reason, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("blocked reasons missing %q in %#v", want, reasons)
		}
	}
}

func TestArgoPodLogQueryPreviewIsReadOnlyAndRedacted(t *testing.T) {
	preview := argoPodLogQueryPreview("api-7d9f", "web", 5000, 999999999, map[string]any{
		"id":           "target-1",
		"name":         "prod",
		"environment":  "prod",
		"cluster_name": "prod-cluster",
		"namespace":    "billing",
		"status":       "Healthy",
	})
	if preview["mode"] != "read_only_preview" ||
		preview["query_state"] != "ready_for_approval" ||
		preview["execution_enabled"] != false ||
		preview["operation_request_enabled"] != true ||
		preview["external_call_made"] != false ||
		preview["kubernetes_api_call"] != false ||
		preview["argocd_api_call"] != false ||
		preview["log_body_included"] != false ||
		preview["contains_secret"] != false ||
		preview["contains_token"] != false {
		t.Fatalf("pod log preview guardrails = %#v", preview)
	}
	query := mapFromAny(preview["query"])
	if query["pod_name"] != "api-7d9f" || query["container_name"] != "web" || query["namespace"] != "billing" || query["tail_lines"] != 1000 || query["since_seconds"] != 86400 {
		t.Fatalf("pod log query = %#v", query)
	}
	target := mapFromAny(preview["deployment_target"])
	if target["name"] != "prod" || target["cluster_name"] != "prod-cluster" || target["namespace"] != "billing" {
		t.Fatalf("pod log target = %#v", target)
	}
	disabledBackends := stringSliceFromAny(preview["disabled_backends"])
	if len(disabledBackends) != 3 || disabledBackends[0] != "kubectl_logs" || disabledBackends[2] != "argocd_pod_logs" {
		t.Fatalf("pod log disabled backends = %#v", disabledBackends)
	}
	suppressed := stringSliceFromAny(preview["suppressed_fields"])
	if len(suppressed) != 7 || suppressed[0] != "kubeconfig" || suppressed[3] != "log_body" {
		t.Fatalf("pod log suppressed fields = %#v", suppressed)
	}
	plan := mapFromAny(preview["retrieval_plan"])
	if plan["mode"] != "pod_log_retrieval_plan_preview" ||
		plan["plan_state"] != "ready_for_approval" ||
		plan["execution_enabled"] != false ||
		plan["operation_request_enabled"] != true ||
		plan["external_call_made"] != false ||
		plan["kubernetes_api_call"] != false ||
		plan["argocd_api_call"] != false ||
		plan["log_body_included"] != false ||
		plan["kubeconfig_included"] != false ||
		plan["contains_secret"] != false {
		t.Fatalf("pod log retrieval plan guardrails = %#v", plan)
	}
	executionPlan := mapFromAny(plan["execution_plan"])
	if executionPlan["mode"] != "pod_log_execution_plan_preview" ||
		executionPlan["execution_state"] != "ready_for_approval" ||
		executionPlan["prerequisite_state"] != "metadata_available" ||
		executionPlan["planned_step_count"] != 4 ||
		executionPlan["blocked_step_count"] != 2 {
		t.Fatalf("pod log execution plan = %#v", executionPlan)
	}
	assertPodLogExecutionPlanSafe(t, executionPlan)
	steps := sliceOfMapsFromAny(plan["steps"])
	if len(steps) != 6 ||
		statusByKind(steps, "operation_approval") != "planned" ||
		statusByKind(steps, "kubeconfig_binding") != "blocked" ||
		statusByKind(steps, "target_scope_check") != "planned" ||
		statusByKind(steps, "pod_identity_confirmation") != "planned" ||
		statusByKind(steps, "live_log_stream") != "blocked" {
		t.Fatalf("pod log retrieval steps = %#v", steps)
	}
}

func TestArgoPodLogQueryPreviewUsesKubernetesEnvironmentReadinessMetadata(t *testing.T) {
	preview := argoPodLogQueryPreview("api-7d9f", "web", 500, 30, map[string]any{
		"id":                            "target-1",
		"name":                          "prod",
		"environment":                   "prod",
		"cluster_name":                  "prod-cluster",
		"namespace":                     "billing",
		"status":                        "Healthy",
		"kubernetes_environment_id":     "kube-env-1",
		"kubernetes_environment_name":   "prod billing",
		"kubeconfig_secret_ref_present": true,
		"service_account_present":       true,
		"token_subject_review_status":   "reviewed",
		"rbac_read_logs_status":         "reviewed",
		"kubernetes_environment_status": "ready",
	}, []map[string]any{
		{"id": "op-pod-logs", "status": "completed", "operation_log_count": int64(1)},
	})
	target := mapFromAny(preview["deployment_target"])
	if target["kubernetes_environment_id"] != "kube-env-1" ||
		target["kubeconfig_secret_ref_present"] != true ||
		target["service_account_present"] != true ||
		target["log_access_metadata_ready"] != true ||
		target["kubeconfig_secret_ref_included"] != false {
		t.Fatalf("kubernetes environment target readiness = %#v", target)
	}
	retrievalPlan := mapFromAny(preview["retrieval_plan"])
	executionPlan := mapFromAny(retrievalPlan["execution_plan"])
	readiness := mapFromAny(executionPlan["kubeconfig_readiness_plan"])
	if readiness["kubernetes_environment_bound"] != true ||
		readiness["namespace_scoped_kubeconfig_bound"] != true ||
		readiness["token_subject_review_performed"] != true ||
		readiness["rbac_read_logs_review_performed"] != true ||
		readiness["log_access_metadata_ready"] != true ||
		readiness["kubernetes_client_created"] != false ||
		readiness["kubernetes_api_call"] != false ||
		readiness["log_stream_opened"] != false ||
		readiness["contains_kubeconfig"] != false ||
		readiness["contains_log_body"] != false {
		t.Fatalf("kubernetes environment readiness plan = %#v", readiness)
	}
	encoded, _ := json.Marshal(preview)
	for _, forbidden := range []string{"apiVersion:", "Bearer secret", "client-key-data", "actual log line", "kubeconfig-data"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("pod log kubernetes readiness preview leaked %q: %s", forbidden, encoded)
		}
	}
}

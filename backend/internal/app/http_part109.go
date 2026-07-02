package app

import (
	"fmt"
	"strings"
)

func argoPodLogQueryPreviewWithConfig(cfg Config, podName, containerName string, tailLines, sinceSeconds int, target map[string]any, auditRows ...[]map[string]any) map[string]any {
	if tailLines <= 0 {
		tailLines = 200
	}
	if tailLines > 1000 {
		tailLines = 1000
	}
	if sinceSeconds < 0 {
		sinceSeconds = 0
	}
	if sinceSeconds > 86400 {
		sinceSeconds = 86400
	}
	namespace := strings.TrimSpace(fmt.Sprint(target["namespace"]))
	clusterName := strings.TrimSpace(fmt.Sprint(target["cluster_name"]))
	status := strings.TrimSpace(fmt.Sprint(target["status"]))
	if namespace == "<nil>" {
		namespace = ""
	}
	if clusterName == "<nil>" {
		clusterName = ""
	}
	if status == "<nil>" {
		status = ""
	}
	liveBackendPlan := kubernetesPodLogBackendPlan(cfg, target)
	liveBackendReady := boolOnlyFromAny(liveBackendPlan["ready"])
	blockedReasons := []string{}
	if !liveBackendReady {
		blockedReasons = append(blockedReasons, "pod_log_backend_disabled", "kubernetes_client_not_bound")
	}
	if namespace == "" {
		blockedReasons = append(blockedReasons, "namespace_missing")
	}
	if clusterName == "" {
		blockedReasons = append(blockedReasons, "cluster_name_missing")
	}
	metadataReady := namespace != "" && clusterName != "" && strings.TrimSpace(podName) != ""
	queryState := "blocked"
	if metadataReady {
		queryState = "ready_for_approval"
	}
	query := map[string]any{
		"pod_name":       podName,
		"container_name": containerName,
		"namespace":      namespace,
		"tail_lines":     tailLines,
		"since_seconds":  sinceSeconds,
	}
	deploymentTarget := map[string]any{
		"id":                             target["id"],
		"name":                           target["name"],
		"environment":                    target["environment"],
		"cluster_name":                   clusterName,
		"namespace":                      namespace,
		"status":                         status,
		"kubernetes_environment_id":      cleanOptionalID(fmt.Sprint(target["kubernetes_environment_id"])),
		"kubernetes_environment_name":    cleanOptionalText(fmt.Sprint(target["kubernetes_environment_name"])),
		"kubeconfig_secret_ref_present":  boolOnlyFromAny(target["kubeconfig_secret_ref_present"]),
		"service_account_present":        boolOnlyFromAny(target["service_account_present"]),
		"token_subject_review_status":    cleanOptionalText(fmt.Sprint(target["token_subject_review_status"])),
		"rbac_read_logs_status":          cleanOptionalText(fmt.Sprint(target["rbac_read_logs_status"])),
		"rbac_restart_pods_status":       cleanOptionalText(fmt.Sprint(target["rbac_restart_pods_status"])),
		"kubernetes_environment_status":  cleanOptionalText(fmt.Sprint(target["kubernetes_environment_status"])),
		"log_access_metadata_ready":      kubernetesLogAccessMetadataReady(target),
		"kubeconfig_secret_ref_included": false,
	}
	var auditEvidenceRows []map[string]any
	if len(auditRows) > 0 {
		auditEvidenceRows = auditRows[0]
	}
	auditEvidence := argoPodLogAuditEvidenceSummary(auditEvidenceRows)
	if !boolOnlyFromAny(liveBackendPlan["ready"]) {
		for _, reason := range stringSliceFromAny(liveBackendPlan["blocked_reasons"]) {
			if !stringListContains(blockedReasons, reason) {
				blockedReasons = append(blockedReasons, reason)
			}
		}
	}
	retrievalPlan := argoPodLogRetrievalPlan(query, deploymentTarget, blockedReasons, auditEvidence, liveBackendPlan)
	disabledBackends := argoPodLogDisabledBackends(liveBackendReady)
	return map[string]any{
		"mode":                      "read_only_preview",
		"query_state":               queryState,
		"execution_enabled":         false,
		"operation_request_enabled": metadataReady,
		"external_call_made":        false,
		"kubernetes_api_call":       false,
		"argocd_api_call":           false,
		"log_body_included":         false,
		"contains_secret":           false,
		"contains_token":            false,
		"deployment_target":         deploymentTarget,
		"query":                     query,
		"audit_evidence":            auditEvidence,
		"retrieval_plan":            retrievalPlan,
		"required_controls":         []string{"deployment_target_review", "kubeconfig_binding", "namespace_confirmation", "pod_name_confirmation", "operator_confirmation"},
		"disabled_backends":         disabledBackends,
		"suppressed_fields":         []string{"kubeconfig", "cluster_token", "authorization_header", "log_body", "pod_env", "secret_env", "volume_secret"},
		"blocked_reasons":           blockedReasons,
		"live_backend_plan":         liveBackendPlan,
		"next_step":                 argoPodLogNextStep(liveBackendReady),
	}
}

func argoPodLogRetrievalPlan(query, target map[string]any, blockedReasons []string, auditEvidence map[string]any, liveBackendPlans ...map[string]any) map[string]any {
	metadataReady := strings.TrimSpace(fmt.Sprint(target["cluster_name"])) != "" && strings.TrimSpace(fmt.Sprint(target["namespace"])) != "" && strings.TrimSpace(fmt.Sprint(query["pod_name"])) != ""
	approvalStatus := "blocked"
	if metadataReady {
		approvalStatus = "planned"
	}
	var liveBackendPlan map[string]any
	if len(liveBackendPlans) > 0 {
		liveBackendPlan = liveBackendPlans[0]
	}
	liveBackendReady := boolOnlyFromAny(liveBackendPlan["ready"])
	liveLogStreamStatus := "blocked"
	liveLogStreamMessage := "Kubernetes/Argo pod log backends are not ready for this target"
	if liveBackendReady {
		liveLogStreamStatus = "planned"
		liveLogStreamMessage = "approved audit jobs can invoke the Kubernetes API and record sanitized metadata without storing log bodies"
	}
	steps := []map[string]any{
		{
			"kind":    "operation_approval",
			"status":  approvalStatus,
			"message": "pod log retrieval requires an approval-gated audit operation before any live backend can be enabled",
		},
		{
			"kind":    "kubeconfig_binding",
			"status":  "blocked",
			"message": "bind a reviewed namespace-scoped kubeconfig outside the preview response",
		},
		{
			"kind":    "target_scope_check",
			"status":  podLogPlanStatus(strings.TrimSpace(fmt.Sprint(target["cluster_name"])) != "" && strings.TrimSpace(fmt.Sprint(target["namespace"])) != ""),
			"message": "deployment target must carry cluster and namespace metadata",
		},
		{
			"kind":    "pod_identity_confirmation",
			"status":  podLogPlanStatus(strings.TrimSpace(fmt.Sprint(query["pod_name"])) != ""),
			"message": "operator must provide an explicit pod name",
		},
		{
			"kind":    "container_scope_confirmation",
			"status":  "planned",
			"message": "empty container name means provider default; explicit container narrows scope",
		},
		{
			"kind":    "live_log_stream",
			"status":  liveLogStreamStatus,
			"message": liveLogStreamMessage,
		},
	}
	planned, blocked := 0, 0
	for _, step := range steps {
		step["external_call_made"] = false
		step["secret_included"] = false
		if step["status"] == "planned" {
			planned++
		} else {
			blocked++
		}
	}
	executionPlan := argoPodLogExecutionPlan(query, target, steps, blockedReasons, auditEvidence, liveBackendPlan)
	planState := "blocked"
	if metadataReady {
		planState = "ready_for_approval"
	}
	return map[string]any{
		"mode":                         "pod_log_retrieval_plan_preview",
		"plan_state":                   planState,
		"execution_enabled":            false,
		"operation_request_enabled":    metadataReady,
		"external_call_made":           false,
		"kubernetes_api_call":          false,
		"argocd_api_call":              false,
		"log_body_included":            false,
		"kubeconfig_included":          false,
		"contains_secret":              false,
		"planned_count":                planned,
		"blocked_count":                blocked,
		"step_count":                   len(steps),
		"steps":                        steps,
		"blocked_reasons":              blockedReasons,
		"audit_evidence":               auditEvidence,
		"execution_plan":               executionPlan,
		"required_live_controls":       []string{"operation_approval", "environment_review", "kubeconfig_binding", "namespace_confirmation", "pod_name_confirmation", "operator_confirmation"},
		"disabled_backends":            argoPodLogDisabledBackends(boolOnlyFromAny(liveBackendPlan["ready"])),
		"suppressed_fields":            []string{"kubeconfig", "cluster_token", "authorization_header", "log_body", "pod_env", "secret_env", "volume_secret"},
		"required_operator_action":     argoPodLogOperatorAction(liveBackendReady),
		"future_execution_result_type": "sanitized_live_log_metadata",
	}
}

func argoPodLogDisabledBackends(liveBackendReady bool) []string {
	if liveBackendReady {
		return []string{"kubernetes_pod_log_api", "argocd_pod_logs"}
	}
	return []string{"kubernetes_client_logs", "kubernetes_pod_log_api", "argocd_pod_logs"}
}

func argoPodLogExecutionDisabledBackends(liveBackendReady bool) []string {
	if liveBackendReady {
		return []string{"kubernetes_pod_log_api", "argocd_pod_logs", "raw_log_body_recording"}
	}
	return []string{"kubeconfig_binding", "kubernetes_pod_log_api", "kubernetes_client_logs", "argocd_pod_logs", "raw_log_body_recording"}
}

func argoPodLogNextStep(liveBackendReady bool) string {
	if liveBackendReady {
		return "Request an approval-gated pod log audit job; the worker can invoke the Kubernetes API and will record sanitized metadata without storing log bodies."
	}
	return "Configure the namespace-scoped Kubernetes environment and enable the opt-in pod log backend, then request an approval-gated audit job."
}

func argoPodLogOperatorAction(liveBackendReady bool) string {
	if liveBackendReady {
		return "Review the target and pod identity, then request an approval-gated audit job; log bodies and kubeconfig data remain suppressed."
	}
	return "Review the target and pod identity, configure the live backend, then request an approval-gated audit job."
}

func argoPodLogLiveStreamMessage(liveBackendReady bool) string {
	if liveBackendReady {
		return "Approved worker jobs can invoke the Kubernetes API for sanitized metadata only; this preview does not read kubeconfig, open a stream, or return log bodies."
	}
	return "Live pod-log stream review is metadata-only until the opt-in backend and reviewed namespace kubeconfig are ready."
}

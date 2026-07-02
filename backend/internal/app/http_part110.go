package app

import (
	"fmt"
	"strings"
)

func argoPodLogExecutionPlan(query, target map[string]any, steps []map[string]any, blockedReasons []string, auditEvidence map[string]any, liveBackendPlan map[string]any) map[string]any {
	planned, blocked := 0, 0
	for _, step := range steps {
		if step["status"] == "planned" {
			planned++
		} else {
			blocked++
		}
	}
	namespaceReady := strings.TrimSpace(fmt.Sprint(target["namespace"])) != ""
	clusterReady := strings.TrimSpace(fmt.Sprint(target["cluster_name"])) != ""
	podReady := strings.TrimSpace(fmt.Sprint(query["pod_name"])) != ""
	prerequisiteState := "metadata_blocked"
	if namespaceReady && clusterReady && podReady {
		prerequisiteState = "metadata_available"
	}
	auditReady := prerequisiteState == "metadata_available"
	executionState := "blocked"
	if auditReady {
		executionState = "ready_for_approval"
	}
	liveBackendEnabled := boolOnlyFromAny(liveBackendPlan["enabled"])
	liveBackendReady := boolOnlyFromAny(liveBackendPlan["ready"])
	liveBackend := "disabled"
	if liveBackendEnabled {
		liveBackend = "kubernetes_client_logs"
	}
	kubeconfigBindingPlan := argoPodLogKubeconfigBindingPlan(prerequisiteState, namespaceReady, clusterReady)
	kubeconfigReadinessPlan := argoPodLogNamespaceKubeconfigReadinessPlan(query, target, prerequisiteState, auditEvidence)
	podScopePlan := argoPodLogPodScopePlan(query, target, prerequisiteState)
	logCapturePlan := argoPodLogCapturePlan(prerequisiteState, liveBackendReady)
	resultRecordingPlan := argoPodLogResultRecordingPlan(auditReady, auditEvidence, query, target, prerequisiteState)
	liveLogStreamPlan := argoPodLogLiveLogStreamReviewPlan(query, target, prerequisiteState, auditEvidence, kubeconfigReadinessPlan, podScopePlan, logCapturePlan, resultRecordingPlan, liveBackendPlan)
	message := "Pod log preview does not call Kubernetes; metadata-ready requests can create an approval-gated audit job with sanitized result only."
	if liveBackendReady {
		message = "Pod log backend is ready for approved audit jobs; the Kubernetes API can be invoked by the worker, while log bodies and kubeconfig data stay suppressed."
	}
	return map[string]any{
		"mode":                          "pod_log_execution_plan_preview",
		"execution_state":               executionState,
		"prerequisite_state":            prerequisiteState,
		"approval_request_plan":         argoPodLogApprovalRequestPlan(query, target, prerequisiteState),
		"execution_enabled":             false,
		"operation_request_enabled":     auditReady,
		"audit_worker_job_enabled":      auditReady,
		"external_call_made":            false,
		"operation_enqueued":            false,
		"worker_job_created":            false,
		"live_log_backend":              liveBackend,
		"live_backend_ready":            liveBackendReady,
		"live_backend_plan":             liveBackendPlan,
		"audit_operation_observed":      boolOnlyFromAny(auditEvidence["has_audit_operations"]),
		"sanitized_result_observed":     boolOnlyFromAny(auditEvidence["sanitized_result_recorded"]),
		"kubeconfig_bound":              false,
		"kubernetes_client_created":     false,
		"kubernetes_api_call":           false,
		"argocd_api_call":               false,
		"kubectl_command_invoked":       false,
		"log_stream_opened":             false,
		"log_body_included":             false,
		"redacted_log_body_included":    false,
		"result_written":                false,
		"secret_included":               false,
		"kubeconfig_included":           false,
		"authorization_header_included": false,
		"planned_step_count":            planned,
		"blocked_step_count":            blocked,
		"blocked_reasons":               blockedReasons,
		"audit_evidence":                auditEvidence,
		"required_controls":             []string{"operation_approval", "environment_review", "kubeconfig_binding", "namespace_confirmation", "pod_name_confirmation", "container_scope_confirmation", "operator_confirmation", "result_redaction_review"},
		"disabled_backends":             argoPodLogExecutionDisabledBackends(liveBackendReady),
		"suppressed_fields":             []string{"kubeconfig", "cluster_token", "authorization_header", "log_body", "redacted_log_body", "pod_env", "secret_env", "volume_secret"},
		"execution_sequence":            []string{"request_operation_approval", "bind_namespace_scoped_kubeconfig", "verify_target_scope", "confirm_pod_identity", "open_pod_log_stream", "redact_log_body", "record_sanitized_result"},
		"kubeconfig_binding_plan":       kubeconfigBindingPlan,
		"kubeconfig_readiness_plan":     kubeconfigReadinessPlan,
		"pod_scope_plan":                podScopePlan,
		"log_capture_plan":              logCapturePlan,
		"live_log_stream_plan":          liveLogStreamPlan,
		"result_recording_plan":         resultRecordingPlan,
		"message":                       message,
	}
}

func argoPodLogLiveLogStreamReviewPlan(query, target map[string]any, prerequisiteState string, auditEvidence, kubeconfigReadinessPlan, podScopePlan, logCapturePlan, resultRecordingPlan, liveBackendPlan map[string]any) map[string]any {
	evidenceState := cleanPreviewString(auditEvidence["evidence_state"])
	if evidenceState == "" {
		evidenceState = "not_requested"
	}
	liveBackendReady := boolOnlyFromAny(liveBackendPlan["ready"])
	streamState := "metadata_blocked"
	if prerequisiteState == "metadata_available" {
		streamState = "ready_for_approval"
	}
	switch evidenceState {
	case "waiting_for_worker", "failed", "canceled", "unknown":
		streamState = "audit_" + evidenceState
	case "recorded":
		if prerequisiteState == "metadata_available" {
			streamState = "ready_for_operator_review"
			if intFromAny(auditEvidence["operation_log_count"], 0) == 0 {
				streamState = "audit_log_missing"
			}
		}
	}
	streamReadyForReview := streamState == "ready_for_operator_review" &&
		boolOnlyFromAny(auditEvidence["sanitized_result_recorded"]) &&
		kubeconfigReadinessPlan["readiness_state"] == "audit_result_ready_for_binding_review" &&
		podScopePlan["scope_state"] == "planned" &&
		logCapturePlan["capture_state"] == "planned" &&
		boolOnlyFromAny(resultRecordingPlan["result_written"])
	blockedReasons := []string{"namespace_scoped_kubeconfig_not_bound", "live_log_stream_not_opened"}
	if !liveBackendReady {
		blockedReasons = append([]string{"live_log_backend_disabled"}, blockedReasons...)
	}
	if prerequisiteState != "metadata_available" {
		blockedReasons = append(blockedReasons, "metadata_incomplete")
	}
	if evidenceState == "not_requested" {
		blockedReasons = append(blockedReasons, "pod_log_audit_not_requested")
	}
	if evidenceState == "waiting_for_worker" {
		blockedReasons = append(blockedReasons, "pod_log_audit_worker_still_running")
	}
	if evidenceState == "failed" {
		blockedReasons = append(blockedReasons, "pod_log_audit_worker_failed")
	}
	if evidenceState == "canceled" {
		blockedReasons = append(blockedReasons, "pod_log_audit_worker_canceled")
	}
	if evidenceState == "unknown" {
		blockedReasons = append(blockedReasons, "pod_log_audit_worker_status_unknown")
	}
	if !boolOnlyFromAny(auditEvidence["sanitized_result_recorded"]) {
		blockedReasons = append(blockedReasons, "sanitized_log_result_not_recorded")
	}
	if evidenceState == "recorded" && intFromAny(auditEvidence["operation_log_count"], 0) == 0 {
		blockedReasons = append(blockedReasons, "sanitized_result_operation_log_missing")
	}
	disabledBackends := []string{"kubeconfig_secret_binding", "kubernetes_client_create", "kubernetes_pod_log_api", "argocd_pod_logs", "live_log_stream_open", "log_body_storage", "redacted_log_body_storage"}
	if !liveBackendReady {
		disabledBackends = append(disabledBackends, "kubernetes_client_logs")
	}
	executionBlockers := []string{"namespace_scoped_kubeconfig_not_bound", "pod_scope_not_verified", "result_redaction_review_not_approved"}
	if !liveBackendReady {
		executionBlockers = append([]string{"live_log_backend_disabled"}, executionBlockers...)
	}
	return map[string]any{
		"mode":                              "pod_log_live_log_stream_review_plan",
		"stream_state":                      streamState,
		"stream_ready_for_review":           streamReadyForReview,
		"metadata_ready":                    prerequisiteState == "metadata_available",
		"audit_operation_observed":          boolOnlyFromAny(auditEvidence["has_audit_operations"]),
		"sanitized_result_observed":         boolOnlyFromAny(auditEvidence["sanitized_result_recorded"]),
		"kubeconfig_binding_review_ready":   kubeconfigReadinessPlan["readiness_ready"] == true,
		"namespace_scope_ready":             kubeconfigReadinessPlan["namespace_scope_ready"] == true,
		"pod_identity_present":              kubeconfigReadinessPlan["pod_identity_present"] == true,
		"pod_scope_review_ready":            podScopePlan["scope_state"] == "planned",
		"log_capture_review_ready":          logCapturePlan["capture_state"] == "planned",
		"result_recording_observed":         boolOnlyFromAny(resultRecordingPlan["result_written"]),
		"live_backend_ready":                liveBackendReady,
		"live_backend_plan":                 liveBackendPlan,
		"namespace_scoped_kubeconfig_bound": false,
		"kubeconfig_secret_read":            false,
		"kubeconfig_bound":                  false,
		"kubernetes_client_created":         false,
		"token_subject_review_performed":    false,
		"rbac_read_logs_review_performed":   false,
		"kubernetes_api_call":               false,
		"argocd_api_call":                   false,
		"kubectl_command_invoked":           false,
		"live_log_stream_opened":            false,
		"log_stream_opened":                 false,
		"log_body_included":                 false,
		"redacted_log_body_included":        false,
		"raw_response_recorded":             false,
		"result_write_enabled":              false,
		"external_call_made":                false,
		"contains_kubeconfig":               false,
		"contains_cluster_token":            false,
		"contains_authorization_header":     false,
		"contains_log_body":                 false,
		"contains_redacted_log_body":        false,
		"contains_raw_kubernetes_response":  false,
		"required_review_fields":            []string{"operation_run_id", "approval_request_id", "deployment_target_id", "cluster_name", "namespace", "pod_name", "container_name", "tail_lines", "since_seconds", "kubeconfig_binding_status", "pod_scope_status", "log_redaction_status", "operator_review_status"},
		"required_controls":                 []string{"operation_approval", "namespace_scoped_kubeconfig_secret", "token_subject_review", "rbac_read_logs_review", "namespace_confirmation", "pod_identity_confirmation", "container_scope_confirmation", "log_line_redaction", "result_redaction_review"},
		"disabled_backends":                 disabledBackends,
		"suppressed_fields":                 []string{"kubeconfig", "cluster_token", "authorization_header", "client_certificate", "client_key", "log_body", "redacted_log_body", "raw_kubernetes_response", "pod_env", "secret_env", "volume_secret", "pod_annotations"},
		"blocked_reasons":                   blockedReasons,
		"execution_blockers":                executionBlockers,
		"target_ref":                        map[string]any{"deployment_target_id": target["id"], "cluster_name": target["cluster_name"], "namespace": target["namespace"], "pod_name": query["pod_name"], "container_name": query["container_name"], "tail_lines": query["tail_lines"], "since_seconds": query["since_seconds"]},
		"message":                           argoPodLogLiveStreamMessage(liveBackendReady),
	}
}

func argoPodLogKubeconfigBindingPlan(prerequisiteState string, namespaceReady, clusterReady bool) map[string]any {
	bindingState := "blocked"
	if prerequisiteState == "metadata_available" {
		bindingState = "planned"
	}
	blockedReasons := []string{"kubeconfig_binding_not_performed"}
	if !clusterReady {
		blockedReasons = append(blockedReasons, "cluster_name_missing")
	}
	if !namespaceReady {
		blockedReasons = append(blockedReasons, "namespace_missing")
	}
	return map[string]any{
		"mode":                          "pod_log_kubeconfig_binding_plan",
		"binding_state":                 bindingState,
		"metadata_ready":                prerequisiteState == "metadata_available",
		"namespace_scoped_required":     true,
		"kubeconfig_bound":              false,
		"kubernetes_client_created":     false,
		"token_subject_reviewed":        false,
		"external_call_made":            false,
		"contains_kubeconfig":           false,
		"contains_cluster_token":        false,
		"contains_authorization_header": false,
		"required_controls":             []string{"environment_review", "namespace_scoped_kubeconfig", "token_subject_review", "rbac_read_logs_review"},
		"disabled_backends":             []string{"kubeconfig_binding", "kubernetes_client_create", "token_subject_review"},
		"suppressed_fields":             []string{"kubeconfig", "cluster_token", "authorization_header", "client_certificate", "client_key"},
		"blocked_reasons":               blockedReasons,
		"execution_blockers":            []string{"kubeconfig_binding_not_approved", "kubeconfig_binding_not_performed"},
		"message":                       "Kubeconfig binding is planned only; no kubeconfig, cluster token, client certificate, or Kubernetes client is created.",
	}
}

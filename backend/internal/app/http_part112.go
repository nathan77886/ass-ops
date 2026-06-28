package app

import (
	"fmt"
	"strings"
)

func argoPodLogResultRecordingPlan(auditReady bool, evidence, query, target map[string]any, prerequisiteState string) map[string]any {
	recordingState := "blocked"
	readyReason := "pod_log_metadata_incomplete"
	resultObserved := auditReady && boolOnlyFromAny(evidence["sanitized_result_recorded"])
	if auditReady {
		recordingState = "planned"
		readyReason = "pod_log_audit_worker_not_observed"
	}
	if auditReady && boolOnlyFromAny(evidence["has_audit_operations"]) {
		recordingState = cleanPreviewString(evidence["evidence_state"])
		if recordingState == "waiting_for_worker" {
			readyReason = "pod_log_audit_worker_still_running"
		} else if recordingState == "recorded" {
			if resultObserved {
				readyReason = "sanitized_pod_log_audit_result_recorded"
			} else {
				readyReason = "pod_log_audit_result_missing_operation_log"
			}
		} else if recordingState == "failed" {
			readyReason = "pod_log_audit_worker_failed"
		} else if recordingState == "canceled" {
			readyReason = "pod_log_audit_worker_canceled"
		} else if recordingState == "unknown" {
			readyReason = "pod_log_audit_worker_status_unknown"
		}
	}
	blockedReasons := []string{"live_log_backend_disabled", "sanitized_log_result_not_recorded"}
	if resultObserved {
		blockedReasons = []string{"live_log_backend_disabled"}
	}
	statusSnapshotWriteEligible := resultObserved
	return map[string]any{
		"mode":                           "pod_log_result_recording_plan",
		"recording_state":                recordingState,
		"recording_ready":                resultObserved,
		"recording_ready_reason":         readyReason,
		"recording_enabled":              resultObserved,
		"result_written":                 resultObserved,
		"operation_log_written":          resultObserved,
		"canonical_asset_sync_queued":    resultObserved,
		"status_snapshot_write_eligible": statusSnapshotWriteEligible,
		"status_snapshot_written":        statusSnapshotWriteEligible,
		"audit_operation_observed":       boolOnlyFromAny(evidence["has_audit_operations"]),
		"sanitized_result_observed":      resultObserved,
		"kubeconfig_binding_recorded":    false,
		"pod_scope_recorded":             false,
		"log_capture_recorded":           false,
		"log_body_included":              false,
		"redacted_log_body_included":     false,
		"raw_response_included":          false,
		"kubeconfig_included":            false,
		"authorization_header_included":  false,
		"audit_evidence":                 evidence,
		"kubeconfig_readiness_plan":      argoPodLogNamespaceKubeconfigReadinessPlan(query, target, prerequisiteState, evidence),
		"required_result_fields":         []string{"operation_run_id", "approval_request_id", "deployment_target_id", "pod_name", "container_name", "status", "line_count", "truncated", "started_at", "finished_at", "kubeconfig_binding_status", "pod_scope_status", "log_capture_status", "redaction_status"},
		"suppressed_fields":              []string{"kubeconfig", "cluster_token", "authorization_header", "log_body", "redacted_log_body", "pod_env", "secret_env", "volume_secret", "raw_kubernetes_response"},
		"blocked_reasons":                blockedReasons,
		"message":                        "Preview does not write results; the audit worker records sanitized metadata only and never stores kubeconfig, raw response, or log bodies.",
	}
}

func argoPodLogNamespaceKubeconfigReadinessPlan(query, target map[string]any, prerequisiteState string, evidence map[string]any) map[string]any {
	namespaceReady := strings.TrimSpace(fmt.Sprint(target["namespace"])) != ""
	clusterReady := strings.TrimSpace(fmt.Sprint(target["cluster_name"])) != ""
	podReady := strings.TrimSpace(fmt.Sprint(query["pod_name"])) != ""
	kubernetesEnvironmentID := cleanOptionalID(fmt.Sprint(target["kubernetes_environment_id"]))
	kubeconfigRefPresent := boolOnlyFromAny(target["kubeconfig_secret_ref_present"])
	tokenSubjectReviewed := cleanPreviewString(target["token_subject_review_status"]) == "reviewed"
	rbacReadLogsReviewed := cleanPreviewString(target["rbac_read_logs_status"]) == "reviewed"
	logAccessMetadataReady := kubernetesEnvironmentID != "" && kubeconfigRefPresent && tokenSubjectReviewed && rbacReadLogsReviewed
	metadataReady := prerequisiteState == "metadata_available" || (namespaceReady && clusterReady && podReady)
	evidenceState := cleanPreviewString(evidence["evidence_state"])
	resultObserved := boolOnlyFromAny(evidence["sanitized_result_recorded"])
	hasAudit := boolOnlyFromAny(evidence["has_audit_operations"])
	readinessState := "metadata_blocked"
	readinessReason := "pod_log_target_metadata_incomplete"
	if metadataReady {
		readinessState = "ready_for_approval"
		readinessReason = "namespace_scoped_kubeconfig_binding_ready_for_operator_approval"
	}
	if metadataReady && hasAudit {
		switch evidenceState {
		case "waiting_for_worker":
			readinessState = "waiting_for_worker"
			readinessReason = "pod_log_audit_worker_still_running"
		case "failed":
			readinessState = "audit_failed"
			readinessReason = "pod_log_audit_worker_failed"
		case "canceled":
			readinessState = "audit_canceled"
			readinessReason = "pod_log_audit_worker_canceled"
		case "recorded":
			readinessState = "audit_result_ready_for_binding_review"
			readinessReason = "sanitized_pod_log_audit_result_ready_for_kubeconfig_binding_review"
		case "unknown":
			readinessState = "audit_unknown"
			readinessReason = "pod_log_audit_worker_status_unknown"
		}
	}
	readinessReady := readinessState == "ready_for_approval" || readinessState == "audit_result_ready_for_binding_review" || logAccessMetadataReady
	bindingBlockers := []string{"live_log_backend_disabled"}
	if kubernetesEnvironmentID == "" {
		bindingBlockers = append(bindingBlockers, "kubernetes_environment_not_configured")
	}
	if !kubeconfigRefPresent {
		bindingBlockers = append(bindingBlockers, "kubeconfig_secret_ref_missing")
	}
	if !tokenSubjectReviewed {
		bindingBlockers = append(bindingBlockers, "token_subject_review_not_performed")
	}
	if !rbacReadLogsReviewed {
		bindingBlockers = append(bindingBlockers, "rbac_read_logs_review_not_performed")
	}
	return map[string]any{
		"mode":                              "pod_log_namespace_kubeconfig_binding_readiness_plan",
		"readiness_state":                   readinessState,
		"readiness_ready":                   readinessReady,
		"readiness_ready_reason":            readinessReason,
		"metadata_ready":                    metadataReady,
		"namespace_scope_ready":             namespaceReady && clusterReady,
		"pod_identity_present":              podReady,
		"kubernetes_environment_id":         kubernetesEnvironmentID,
		"kubernetes_environment_bound":      kubernetesEnvironmentID != "",
		"kubernetes_environment_status":     cleanOptionalText(fmt.Sprint(target["kubernetes_environment_status"])),
		"kubeconfig_secret_ref_present":     kubeconfigRefPresent,
		"service_account_present":           boolOnlyFromAny(target["service_account_present"]),
		"audit_operation_observed":          hasAudit,
		"sanitized_audit_result_observed":   resultObserved,
		"kubeconfig_binding_performed":      false,
		"namespace_scoped_kubeconfig_bound": logAccessMetadataReady,
		"kubernetes_client_created":         false,
		"token_subject_review_performed":    tokenSubjectReviewed,
		"rbac_read_logs_review_performed":   rbacReadLogsReviewed,
		"log_access_metadata_ready":         logAccessMetadataReady,
		"kubernetes_api_call":               false,
		"argocd_api_call":                   false,
		"kubectl_command_invoked":           false,
		"log_stream_opened":                 false,
		"log_body_included":                 false,
		"redacted_log_body_included":        false,
		"external_call_made":                false,
		"contains_kubeconfig":               false,
		"contains_cluster_token":            false,
		"contains_authorization_header":     false,
		"contains_log_body":                 false,
		"contains_raw_kubernetes_response":  false,
		"required_controls":                 []string{"operator_approval", "namespace_scoped_kubeconfig_secret", "token_subject_review", "rbac_read_logs_review", "namespace_confirmation", "pod_identity_confirmation", "result_redaction_review"},
		"disabled_backends":                 []string{"kubeconfig_secret_binding", "kubernetes_client_create", "token_subject_review", "rbac_review", "kubernetes_pod_log_api", "kubectl_logs", "argocd_pod_logs"},
		"binding_blockers":                  bindingBlockers,
		"readiness_sequence":                []string{"review_deployment_target_namespace", "approve_pod_log_audit_request", "bind_namespace_scoped_kubeconfig_secret", "review_token_subject", "review_rbac_logs_permission", "create_kubernetes_client", "open_live_log_stream", "record_redacted_log_result"},
		"suppressed_fields":                 []string{"kubeconfig", "cluster_token", "authorization_header", "client_certificate", "client_key", "log_body", "redacted_log_body", "raw_kubernetes_response", "pod_env", "secret_env", "volume_secret"},
		"message":                           "Namespace-scoped kubeconfig binding is readiness metadata only; no kubeconfig secret is read, no Kubernetes client is created, and no pod log stream is opened.",
	}
}

func kubernetesLogAccessMetadataReady(target map[string]any) bool {
	return cleanOptionalID(fmt.Sprint(target["kubernetes_environment_id"])) != "" &&
		boolOnlyFromAny(target["kubeconfig_secret_ref_present"]) &&
		cleanPreviewString(target["token_subject_review_status"]) == "reviewed" &&
		cleanPreviewString(target["rbac_read_logs_status"]) == "reviewed"
}

func cleanKubernetesReviewStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "reviewed", "approved":
		return "reviewed"
	case "failed", "rejected":
		return "failed"
	case "waived":
		return "waived"
	default:
		return "not_reviewed"
	}
}

func cleanKubernetesEnvironmentStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ready", "reviewed":
		return "ready"
	case "disabled":
		return "disabled"
	default:
		return "metadata_only"
	}
}

func containsSecretLikeMaterial(value string) bool {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" {
		return false
	}
	lower := strings.ToLower(cleaned)
	return strings.Contains(cleaned, "\n") ||
		strings.Contains(lower, "apiversion:") ||
		strings.Contains(lower, `"apiversion"`) ||
		strings.Contains(lower, "kind: config") ||
		strings.Contains(lower, `"kind":"config"`) ||
		strings.Contains(lower, "bearer ") ||
		strings.Contains(lower, "token:") ||
		strings.Contains(lower, "client-key-data") ||
		strings.Contains(lower, "client-certificate-data")
}

func podLogPlanStatus(ready bool) string {
	if ready {
		return "planned"
	}
	return "blocked"
}

func enrichDeploymentTargetsWithExecutionReadiness(rows []map[string]any) {
	for _, row := range rows {
		row["deployment_execution_readiness"] = deploymentExecutionReadiness(row)
	}
}

func deploymentExecutionReadiness(row map[string]any) map[string]any {
	healthStatus := strings.ToLower(strings.TrimSpace(fmt.Sprint(row["status"])))
	cluster := strings.TrimSpace(fmt.Sprint(row["cluster_name"]))
	namespace := strings.TrimSpace(fmt.Sprint(row["namespace"]))
	appCount := intFromAny(row["argo_app_count"], -1)
	blockedReasons := make([]string, 0)
	if deploymentTargetStatusBlocksExecution(healthStatus) {
		blockedReasons = append(blockedReasons, "deployment target status needs review before execution")
	}
	if cluster == "" || cluster == "<nil>" {
		blockedReasons = append(blockedReasons, "cluster name is missing")
	}
	if namespace == "" || namespace == "<nil>" {
		blockedReasons = append(blockedReasons, "namespace is missing")
	}
	if appCount == 0 {
		blockedReasons = append(blockedReasons, "no Argo apps are linked to this deployment target")
	}
	readiness := "planned"
	message := "Deployment execution dry-run plan is ready; Helm/k8s execution remains disabled."
	if len(blockedReasons) > 0 {
		readiness = "blocked"
		message = "Deployment execution cannot be planned until target metadata and health are reviewed."
	}
	executionPlan := deploymentExecutionPlan(readiness, blockedReasons)
	return map[string]any{
		"status":             readiness,
		"mode":               "dry_run",
		"execution_enabled":  false,
		"external_call_made": false,
		"requires_approval":  true,
		"approval_action":    "deployment.execute",
		"execution_backend":  "disabled",
		"blocked_reasons":    blockedReasons,
		"execution_plan":     executionPlan,
		"steps": []map[string]any{
			{"name": "validate_target", "status": "planned", "execution": false},
			{"name": "render_manifest", "status": "planned", "execution": false},
			{"name": "helm_or_kubectl_preflight", "status": "planned", "execution": false},
			{"name": "rollout", "status": "planned", "execution": false},
		},
		"message": message,
	}
}

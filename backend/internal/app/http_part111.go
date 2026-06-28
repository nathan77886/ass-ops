package app

import (
	"fmt"
	"strings"
)

func argoPodLogPodScopePlan(query, target map[string]any, prerequisiteState string) map[string]any {
	podName := strings.TrimSpace(fmt.Sprint(query["pod_name"]))
	containerName := strings.TrimSpace(fmt.Sprint(query["container_name"]))
	namespaceReady := strings.TrimSpace(fmt.Sprint(target["namespace"])) != ""
	clusterReady := strings.TrimSpace(fmt.Sprint(target["cluster_name"])) != ""
	podReady := podName != ""
	scopeState := "blocked"
	if prerequisiteState == "metadata_available" {
		scopeState = "planned"
	}
	blockedReasons := []string{"pod_scope_not_verified"}
	if !clusterReady {
		blockedReasons = append(blockedReasons, "cluster_name_missing")
	}
	if !namespaceReady {
		blockedReasons = append(blockedReasons, "namespace_missing")
	}
	if !podReady {
		blockedReasons = append(blockedReasons, "pod_name_missing")
	}
	return map[string]any{
		"mode":                      "pod_log_pod_scope_plan",
		"scope_state":               scopeState,
		"metadata_ready":            prerequisiteState == "metadata_available",
		"pod_name_present":          podReady,
		"container_name_present":    containerName != "",
		"default_container_allowed": containerName == "",
		"target_scope_verified":     false,
		"pod_identity_confirmed":    false,
		"container_scope_confirmed": false,
		"external_call_made":        false,
		"contains_pod_env":          false,
		"contains_secret_env":       false,
		"required_controls":         []string{"namespace_confirmation", "pod_name_confirmation", "container_scope_confirmation", "operator_confirmation"},
		"disabled_backends":         []string{"kubernetes_pod_lookup", "argocd_pod_lookup"},
		"suppressed_fields":         []string{"pod_env", "secret_env", "volume_secret", "owner_references", "pod_annotations"},
		"blocked_reasons":           blockedReasons,
		"execution_blockers":        []string{"pod_scope_not_verified", "pod_identity_not_confirmed"},
		"message":                   "Pod and container scope verification is planned only; no pod lookup, env, annotation, or secret material is read.",
	}
}

func argoPodLogCapturePlan(prerequisiteState string, liveBackendReady bool) map[string]any {
	captureState := "blocked"
	if prerequisiteState == "metadata_available" {
		captureState = "planned"
	}
	disabledBackends := []string{"kubernetes_pod_log_api", "argocd_pod_logs", "log_stream_result_write"}
	if !liveBackendReady {
		disabledBackends = append(disabledBackends, "kubectl_logs")
	}
	executionBlockers := []string{"log_stream_result_write_disabled"}
	if !liveBackendReady {
		executionBlockers = append([]string{"pod_log_backend_disabled"}, executionBlockers...)
	}
	message := "Pod log capture is planned only; no log stream, raw body, redacted body, Kubernetes response, or result row is produced."
	if liveBackendReady {
		message = "Pod log capture is planned for approved worker jobs; kubectl logs may be invoked for sanitized metadata only, with no raw body, redacted body, Kubernetes response, or result row produced by this preview."
	}
	return map[string]any{
		"mode":                       "pod_log_capture_plan",
		"capture_state":              captureState,
		"metadata_ready":             prerequisiteState == "metadata_available",
		"kubernetes_api_call":        false,
		"argocd_api_call":            false,
		"kubectl_command_invoked":    false,
		"log_stream_opened":          false,
		"log_body_included":          false,
		"redacted_log_body_included": false,
		"redaction_performed":        false,
		"result_write_planned":       captureState == "planned",
		"external_call_made":         false,
		"contains_log_body":          false,
		"contains_redacted_log_body": false,
		"contains_raw_response":      false,
		"required_controls":          []string{"operation_approval", "log_line_redaction", "tail_limit_enforcement", "result_redaction_review"},
		"disabled_backends":          disabledBackends,
		"suppressed_fields":          []string{"log_body", "redacted_log_body", "raw_kubernetes_response", "pod_env", "secret_env", "volume_secret"},
		"blocked_reasons":            []string{"pod_log_execution_not_performed", "log_stream_not_opened", "sanitized_log_result_not_recorded"},
		"execution_blockers":         executionBlockers,
		"message":                    message,
	}
}

func argoPodLogApprovalRequestPlan(query, target map[string]any, prerequisiteState string) map[string]any {
	namespaceReady := strings.TrimSpace(fmt.Sprint(target["namespace"])) != ""
	clusterReady := strings.TrimSpace(fmt.Sprint(target["cluster_name"])) != ""
	podReady := strings.TrimSpace(fmt.Sprint(query["pod_name"])) != ""
	metadataReady := namespaceReady && clusterReady && podReady && prerequisiteState == "metadata_available"
	requestState := "blocked"
	if metadataReady {
		requestState = "planned"
	}
	requestReadyReason := "pod_log_metadata_incomplete"
	if metadataReady {
		requestReadyReason = "pod_log_audit_operation_ready"
	}
	metadataBlockedReasons := []string{}
	if !clusterReady {
		metadataBlockedReasons = append(metadataBlockedReasons, "cluster_name_missing")
	}
	if !namespaceReady {
		metadataBlockedReasons = append(metadataBlockedReasons, "namespace_missing")
	}
	if !podReady {
		metadataBlockedReasons = append(metadataBlockedReasons, "pod_name_missing")
	}
	return map[string]any{
		"mode":                         "pod_log_approval_request_plan",
		"request_state":                requestState,
		"request_ready":                metadataReady,
		"request_ready_reason":         requestReadyReason,
		"metadata_ready":               metadataReady,
		"operation_created":            false,
		"approval_request_created":     false,
		"worker_job_created":           false,
		"kubeconfig_binding_requested": false,
		"external_call_made":           false,
		"required_action":              "Create a high-risk operation approval request before any pod log audit worker can run.",
		"required_approval_fields":     []string{"operation_run_id", "deployment_target_id", "cluster_name", "namespace", "pod_name", "container_name", "tail_lines", "since_seconds", "requested_by", "reason"},
		"suppressed_fields":            []string{"kubeconfig", "cluster_token", "authorization_header", "log_body", "pod_env", "secret_env", "volume_secret", "approval_reason_detail"},
		"blocked_reasons":              metadataBlockedReasons,
		"execution_blockers":           []string{"pod_log_operation_not_created", "approval_policy_not_applied", "live_log_backend_disabled"},
	}
}

func argoPodLogAuditEvidenceSummary(rows []map[string]any) map[string]any {
	queued, running, completed, failed, canceled, unknown, logCount := 0, 0, 0, 0, 0, 0, 0
	latestPreview := ""
	latestPreviewLineCount := 0
	latestPreviewTruncated := false
	latestPreviewOperationID := ""
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		status := cleanPreviewString(row["status"])
		if status == "" {
			status = "unknown"
		}
		switch status {
		case "queued":
			queued++
		case "running":
			running++
		case "completed":
			completed++
		case "failed":
			failed++
		case "canceled":
			canceled++
		default:
			unknown++
		}
		rowLogCount := intFromAny(row["operation_log_count"], 0)
		logCount += rowLogCount
		preview, previewLineCount, previewTruncated := safePodLogPreviewFromOperationResult(row["result"])
		if latestPreview == "" && status == "completed" && preview != "" {
			latestPreview = preview
			latestPreviewLineCount = previewLineCount
			latestPreviewTruncated = previewTruncated
			latestPreviewOperationID = cleanOptionalID(fmt.Sprint(row["id"]))
		}
		items = append(items, map[string]any{
			"operation_run_id":           row["id"],
			"status":                     status,
			"created_at":                 row["created_at"],
			"updated_at":                 row["updated_at"],
			"finished_at":                row["finished_at"],
			"operation_log_count":        rowLogCount,
			"redacted_preview_available": preview != "",
			"preview_line_count":         previewLineCount,
			"preview_truncated":          previewTruncated,
			"raw_input_included":         false,
			"log_body_included":          false,
			"kubeconfig_included":        false,
			"raw_response_included":      false,
			"secret_included":            false,
		})
	}
	operationCount := len(rows)
	activeCount := queued + running
	evidenceState := "not_requested"
	if operationCount > 0 {
		evidenceState = "waiting_for_worker"
		if activeCount == 0 {
			if failed > 0 {
				evidenceState = "failed"
			} else if canceled > 0 {
				evidenceState = "canceled"
			} else if completed > 0 {
				evidenceState = "recorded"
			} else if unknown > 0 {
				evidenceState = "unknown"
			}
		}
	}
	sanitizedRecorded := evidenceState == "recorded" && logCount > 0
	return map[string]any{
		"mode":                       "pod_log_audit_evidence_summary",
		"operation_count":            operationCount,
		"queued_count":               queued,
		"running_count":              running,
		"completed_count":            completed,
		"failed_count":               failed,
		"canceled_count":             canceled,
		"unknown_count":              unknown,
		"active_count":               activeCount,
		"operation_log_count":        logCount,
		"evidence_state":             evidenceState,
		"has_audit_operations":       operationCount > 0,
		"sanitized_result_recorded":  sanitizedRecorded,
		"has_failures":               failed > 0,
		"has_cancellations":          canceled > 0,
		"has_unknown_status":         unknown > 0,
		"redacted_preview_available": latestPreview != "",
		"redacted_log_preview":       latestPreview,
		"preview_line_count":         latestPreviewLineCount,
		"preview_truncated":          latestPreviewTruncated,
		"preview_operation_run_id":   latestPreviewOperationID,
		"items":                      items,
		"external_call_made":         false,
		"kubernetes_api_call":        false,
		"argocd_api_call":            false,
		"kubeconfig_included":        false,
		"log_body_included":          false,
		"redacted_log_body_included": false,
		"raw_response_included":      false,
		"secret_included":            false,
		"suppressed_fields":          []string{"operation_input", "kubeconfig", "cluster_token", "authorization_header", "log_body", "redacted_log_body", "raw_kubernetes_response", "pod_env", "secret_env", "volume_secret"},
	}
}

func safePodLogPreviewFromOperationResult(value any) (string, int, bool) {
	result := mapFromAny(value)
	if !boolOnlyFromAny(result["redacted_log_body_included"]) ||
		boolOnlyFromAny(result["log_body_included"]) ||
		boolOnlyFromAny(result["raw_response_included"]) ||
		boolOnlyFromAny(result["secret_included"]) {
		return "", 0, false
	}
	preview := cleanOptionalText(fmt.Sprint(result["redacted_log_preview"]))
	if preview == "" || strings.Contains(preview, "\x00") {
		return "", 0, false
	}
	preview, truncated := sanitizedKubernetesLogPreview(preview, kubernetesLogPreviewMaxBytes)
	if preview == "" {
		return "", 0, false
	}
	if boolOnlyFromAny(result["preview_truncated"]) {
		truncated = true
	}
	lineCount := intFromAny(result["preview_line_count"], 0)
	if lineCount <= 0 {
		lineCount = countNonEmptyLines(preview)
	}
	return preview, lineCount, truncated
}

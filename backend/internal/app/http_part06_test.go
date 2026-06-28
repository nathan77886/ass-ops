package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestKubernetesEnvironmentInputGuardsRejectCredentialMaterial(t *testing.T) {
	for _, value := range []string{
		"apiVersion: v1",
		`{"apiVersion":"v1","kind":"Config"}`,
		"client-key-data: secret",
		"Bearer secret",
	} {
		if !containsSecretLikeMaterial(value) {
			t.Fatalf("credential-like kubernetes metadata was not rejected: %q", value)
		}
	}
	if containsSecretLikeMaterial("assops/test/billing-reader") {
		t.Fatalf("secret reference name should be allowed")
	}
	if got := cleanKubernetesReviewStatus("ready"); got != "not_reviewed" {
		t.Fatalf("ready should not be accepted as review status, got %q", got)
	}
	if got := cleanKubernetesReviewStatus("approved"); got != "reviewed" {
		t.Fatalf("approved should normalize to reviewed, got %q", got)
	}
}

func TestArgoPodLogQueryPreviewReportsMissingTargetMetadata(t *testing.T) {
	preview := argoPodLogQueryPreview("", "", 0, 60, map[string]any{"id": "target-1", "name": "prod"})
	query := mapFromAny(preview["query"])
	if query["tail_lines"] != 200 || query["since_seconds"] != 60 {
		t.Fatalf("pod log default query = %#v", query)
	}
	blockedReasons := stringSliceFromAny(preview["blocked_reasons"])
	if !containsString(blockedReasons, "namespace_missing") || !containsString(blockedReasons, "cluster_name_missing") {
		t.Fatalf("pod log blocked reasons = %#v", blockedReasons)
	}
	plan := mapFromAny(preview["retrieval_plan"])
	steps := sliceOfMapsFromAny(plan["steps"])
	if statusByKind(steps, "target_scope_check") != "blocked" ||
		statusByKind(steps, "pod_identity_confirmation") != "blocked" ||
		plan["blocked_count"] != 5 {
		t.Fatalf("pod log retrieval plan should block missing target metadata: %#v", plan)
	}
	executionPlan := mapFromAny(plan["execution_plan"])
	if executionPlan["prerequisite_state"] != "metadata_blocked" ||
		executionPlan["planned_step_count"] != 1 ||
		executionPlan["blocked_step_count"] != 5 {
		t.Fatalf("metadata-blocked pod log execution plan = %#v", executionPlan)
	}
	approvalPlan := mapFromAny(executionPlan["approval_request_plan"])
	if approvalPlan["request_state"] != "blocked" ||
		approvalPlan["metadata_ready"] != false ||
		!containsString(stringSliceFromAny(approvalPlan["blocked_reasons"]), "namespace_missing") ||
		!containsString(stringSliceFromAny(approvalPlan["blocked_reasons"]), "cluster_name_missing") ||
		!containsString(stringSliceFromAny(approvalPlan["blocked_reasons"]), "pod_name_missing") {
		t.Fatalf("metadata-blocked pod log approval plan = %#v", approvalPlan)
	}
	assertPodLogExecutionPlanSafe(t, executionPlan)
}

func TestArgoPodLogQueryPreviewKeepsRecordedEvidenceMetadataBlocked(t *testing.T) {
	preview := argoPodLogQueryPreview("", "", 0, 60, map[string]any{"id": "target-1", "name": "prod"}, []map[string]any{
		{
			"id":                  "op-pod-logs",
			"status":              "completed",
			"operation_log_count": int64(1),
		},
	})
	executionPlan := mapFromAny(mapFromAny(preview["retrieval_plan"])["execution_plan"])
	liveStreamPlan := mapFromAny(executionPlan["live_log_stream_plan"])
	if executionPlan["prerequisite_state"] != "metadata_blocked" ||
		liveStreamPlan["stream_state"] != "metadata_blocked" ||
		liveStreamPlan["stream_ready_for_review"] != false ||
		liveStreamPlan["sanitized_result_observed"] != true ||
		!containsString(stringSliceFromAny(liveStreamPlan["blocked_reasons"]), "metadata_incomplete") {
		t.Fatalf("recorded evidence with missing metadata should not make live stream review ready: execution=%#v live=%#v", executionPlan, liveStreamPlan)
	}
	assertPodLogExecutionPlanSafe(t, executionPlan)
}

func TestArgoPodLogQueryPreviewReconcilesAuditEvidence(t *testing.T) {
	preview := argoPodLogQueryPreview("api-7d9f", "web", 500, 30, map[string]any{
		"id":           "target-1",
		"name":         "prod",
		"environment":  "prod",
		"cluster_name": "prod-cluster",
		"namespace":    "billing",
		"status":       "Healthy",
	}, []map[string]any{
		{
			"id":                  "op-pod-logs",
			"status":              "completed",
			"operation_log_count": int64(2),
			"input":               map[string]any{"kubeconfig": "secret kubeconfig", "log_body": "actual log line"},
		},
	})
	evidence := mapFromAny(preview["audit_evidence"])
	if evidence["evidence_state"] != "recorded" ||
		evidence["has_audit_operations"] != true ||
		evidence["sanitized_result_recorded"] != true ||
		intFromAny(evidence["operation_count"], 0) != 1 ||
		intFromAny(evidence["operation_log_count"], 0) != 2 ||
		evidence["log_body_included"] != false ||
		evidence["kubeconfig_included"] != false ||
		evidence["raw_response_included"] != false {
		t.Fatalf("unexpected pod log audit evidence: %#v", evidence)
	}
	executionPlan := mapFromAny(mapFromAny(preview["retrieval_plan"])["execution_plan"])
	if executionPlan["audit_operation_observed"] != true ||
		executionPlan["sanitized_result_observed"] != true ||
		executionPlan["operation_enqueued"] != false ||
		executionPlan["worker_job_created"] != false ||
		executionPlan["kubernetes_api_call"] != false ||
		executionPlan["log_body_included"] != false {
		t.Fatalf("unexpected pod log execution evidence: %#v", executionPlan)
	}
	resultPlan := mapFromAny(executionPlan["result_recording_plan"])
	if resultPlan["recording_state"] != "recorded" ||
		resultPlan["recording_ready"] != true ||
		resultPlan["recording_enabled"] != true ||
		resultPlan["result_written"] != true ||
		resultPlan["operation_log_written"] != true ||
		resultPlan["canonical_asset_sync_queued"] != true ||
		resultPlan["status_snapshot_written"] != true ||
		resultPlan["audit_operation_observed"] != true ||
		resultPlan["sanitized_result_observed"] != true ||
		resultPlan["kubeconfig_binding_recorded"] != false ||
		resultPlan["log_capture_recorded"] != false ||
		resultPlan["log_body_included"] != false ||
		resultPlan["raw_response_included"] != false {
		t.Fatalf("unexpected pod log result plan: %#v", resultPlan)
	}
	liveStreamPlan := mapFromAny(executionPlan["live_log_stream_plan"])
	if liveStreamPlan["stream_state"] != "ready_for_operator_review" ||
		liveStreamPlan["stream_ready_for_review"] != true ||
		liveStreamPlan["audit_operation_observed"] != true ||
		liveStreamPlan["sanitized_result_observed"] != true ||
		liveStreamPlan["result_recording_observed"] != true ||
		liveStreamPlan["live_log_stream_opened"] != false ||
		liveStreamPlan["log_body_included"] != false ||
		liveStreamPlan["redacted_log_body_included"] != false ||
		liveStreamPlan["contains_kubeconfig"] != false {
		t.Fatalf("unexpected pod log live stream review plan: %#v", liveStreamPlan)
	}
	assertPodLogExecutionPlanSafe(t, executionPlan)
	encoded, _ := json.Marshal(preview)
	for _, forbidden := range []string{"secret kubeconfig", "actual log line", "apiVersion:", "Bearer secret"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("pod log evidence leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestArgoPodLogQueryPreviewDoesNotRecordResultWhileAuditWorkerRuns(t *testing.T) {
	preview := argoPodLogQueryPreview("api-7d9f", "web", 500, 30, map[string]any{
		"id":           "target-1",
		"name":         "prod",
		"environment":  "prod",
		"cluster_name": "prod-cluster",
		"namespace":    "billing",
		"status":       "Healthy",
	}, []map[string]any{
		{"id": "op-pod-logs-1", "status": "completed", "operation_log_count": int64(2), "input": map[string]any{"log_body": "actual log line"}},
		{"id": "op-pod-logs-2", "status": "running", "operation_log_count": int64(1), "input": map[string]any{"kubeconfig": "secret kubeconfig"}},
	})
	evidence := mapFromAny(preview["audit_evidence"])
	if evidence["evidence_state"] != "waiting_for_worker" ||
		evidence["sanitized_result_recorded"] != false ||
		intFromAny(evidence["completed_count"], 0) != 1 ||
		intFromAny(evidence["running_count"], 0) != 1 ||
		intFromAny(evidence["active_count"], 0) != 1 ||
		intFromAny(evidence["operation_log_count"], 0) != 3 {
		t.Fatalf("mixed active pod log audit evidence should wait for terminal result: %#v", evidence)
	}
	executionPlan := mapFromAny(mapFromAny(preview["retrieval_plan"])["execution_plan"])
	resultPlan := mapFromAny(executionPlan["result_recording_plan"])
	if resultPlan["recording_state"] != "waiting_for_worker" ||
		resultPlan["result_written"] != false ||
		resultPlan["operation_log_written"] != false ||
		resultPlan["canonical_asset_sync_queued"] != false ||
		resultPlan["status_snapshot_written"] != false ||
		resultPlan["sanitized_result_observed"] != false {
		t.Fatalf("pod log result plan should not write while an audit worker is active: %#v", resultPlan)
	}
	liveStreamPlan := mapFromAny(executionPlan["live_log_stream_plan"])
	if liveStreamPlan["stream_ready_for_review"] != false ||
		liveStreamPlan["sanitized_result_observed"] != false ||
		liveStreamPlan["result_recording_observed"] != false {
		t.Fatalf("active pod log audit should not make live stream review ready: %#v", liveStreamPlan)
	}
	kubeconfigReadiness := mapFromAny(executionPlan["kubeconfig_readiness_plan"])
	if kubeconfigReadiness["readiness_state"] != "waiting_for_worker" ||
		kubeconfigReadiness["readiness_ready"] != false ||
		kubeconfigReadiness["sanitized_audit_result_observed"] != false {
		t.Fatalf("active pod log audit should not make kubeconfig binding review ready: %#v", kubeconfigReadiness)
	}
	encoded, _ := json.Marshal(preview)
	for _, forbidden := range []string{"secret kubeconfig", "actual log line"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("mixed active pod log evidence leaked %q: %s", forbidden, encoded)
		}
	}
}

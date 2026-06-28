package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArgoPodLogQueryPreviewDoesNotRecordResultForUnsuccessfulTerminalAudit(t *testing.T) {
	tests := []struct {
		name       string
		rows       []map[string]any
		wantState  string
		wantReason string
	}{
		{
			name:       "failed only",
			rows:       []map[string]any{{"id": "op-pod-logs-failed", "status": "failed", "operation_log_count": int64(2), "input": map[string]any{"log_body": "actual log line"}}},
			wantState:  "failed",
			wantReason: "pod_log_audit_worker_failed",
		},
		{
			name:       "canceled only",
			rows:       []map[string]any{{"id": "op-pod-logs-canceled", "status": "canceled", "operation_log_count": int64(2), "input": map[string]any{"kubeconfig": "secret kubeconfig"}}},
			wantState:  "canceled",
			wantReason: "pod_log_audit_worker_canceled",
		},
		{
			name: "failed takes priority over completed",
			rows: []map[string]any{
				{"id": "op-pod-logs-completed", "status": "completed", "operation_log_count": int64(2), "input": map[string]any{"log_body": "actual log line"}},
				{"id": "op-pod-logs-failed", "status": "failed", "operation_log_count": int64(1), "input": map[string]any{"kubeconfig": "secret kubeconfig"}},
			},
			wantState:  "failed",
			wantReason: "pod_log_audit_worker_failed",
		},
		{
			name:       "unknown status only",
			rows:       []map[string]any{{"id": "op-pod-logs-unknown", "status": "bogus_status", "operation_log_count": int64(0), "input": map[string]any{"log_body": "actual log line"}}},
			wantState:  "unknown",
			wantReason: "pod_log_audit_worker_status_unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preview := argoPodLogQueryPreview("api-7d9f", "web", 500, 30, map[string]any{
				"id":           "target-1",
				"name":         "prod",
				"environment":  "prod",
				"cluster_name": "prod-cluster",
				"namespace":    "billing",
				"status":       "Healthy",
			}, tt.rows)
			evidence := mapFromAny(preview["audit_evidence"])
			if evidence["evidence_state"] != tt.wantState ||
				evidence["sanitized_result_recorded"] != false ||
				intFromAny(evidence["active_count"], 0) != 0 {
				t.Fatalf("unsuccessful terminal pod log audit should not record sanitized result: %#v", evidence)
			}
			executionPlan := mapFromAny(mapFromAny(preview["retrieval_plan"])["execution_plan"])
			resultPlan := mapFromAny(executionPlan["result_recording_plan"])
			if resultPlan["recording_state"] != tt.wantState ||
				resultPlan["recording_ready"] != false ||
				resultPlan["recording_enabled"] != false ||
				resultPlan["recording_ready_reason"] != tt.wantReason ||
				resultPlan["result_written"] != false ||
				resultPlan["operation_log_written"] != false ||
				resultPlan["canonical_asset_sync_queued"] != false ||
				resultPlan["status_snapshot_written"] != false ||
				resultPlan["sanitized_result_observed"] != false {
				t.Fatalf("unsuccessful terminal pod log result plan should not write: %#v", resultPlan)
			}
			liveStreamPlan := mapFromAny(executionPlan["live_log_stream_plan"])
			if liveStreamPlan["stream_ready_for_review"] != false ||
				liveStreamPlan["sanitized_result_observed"] != false ||
				liveStreamPlan["result_recording_observed"] != false {
				t.Fatalf("unsuccessful terminal pod log audit should not make live stream review ready: %#v", liveStreamPlan)
			}
			encoded, _ := json.Marshal(preview)
			for _, forbidden := range []string{"secret kubeconfig", "actual log line"} {
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("unsuccessful terminal pod log evidence leaked %q: %s", forbidden, encoded)
				}
			}
		})
	}
}

func TestArgoPodLogAuditSnapshotPayloadIsSafeAndReadinessGated(t *testing.T) {
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
	snapshot := argoPodLogAuditSnapshotPayload(preview, true)
	ready, state, missing := argoPodLogAuditSnapshotReadiness(preview, snapshot)
	if !ready || state != "ready_to_record" || len(missing) != 0 {
		t.Fatalf("recorded pod log audit should be snapshot-ready: ready=%v state=%s missing=%#v snapshot=%#v", ready, state, missing, snapshot)
	}
	if snapshot["mode"] != "pod_log_audit_snapshot" ||
		snapshot["deployment_target_asset_observed"] != true ||
		snapshot["audit_evidence_state"] != "recorded" ||
		snapshot["sanitized_result_recorded"] != true ||
		snapshot["live_stream_review_ready"] != true ||
		snapshot["log_body_included"] != false ||
		snapshot["redacted_log_body_included"] != false ||
		snapshot["kubeconfig_included"] != false ||
		snapshot["raw_response_included"] != false ||
		snapshot["secret_included"] != false {
		t.Fatalf("unexpected pod log audit snapshot: %#v", snapshot)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, forbidden := range []string{"secret kubeconfig", "actual log line", "apiVersion:", "Bearer secret", "kubeconfig-data"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("pod log snapshot leaked %q: %s", forbidden, encoded)
		}
	}

	waitingPreview := argoPodLogQueryPreview("api-7d9f", "web", 500, 30, map[string]any{
		"id":           "target-1",
		"name":         "prod",
		"environment":  "prod",
		"cluster_name": "prod-cluster",
		"namespace":    "billing",
		"status":       "Healthy",
	}, []map[string]any{
		{"id": "op-pod-logs", "status": "running", "operation_log_count": int64(1)},
	})
	waitingSnapshot := argoPodLogAuditSnapshotPayload(waitingPreview, true)
	ready, state, missing = argoPodLogAuditSnapshotReadiness(waitingPreview, waitingSnapshot)
	if ready || state != "waiting_for_worker" ||
		!containsString(missing, "pod_log_audit_worker_still_running") ||
		!containsString(missing, "sanitized_pod_log_audit_result_not_recorded") {
		t.Fatalf("running pod log audit should not be snapshot-ready: ready=%v state=%s missing=%#v", ready, state, missing)
	}
}

func newArgoPodLogSnapshotRequest() *http.Request {
	body := strings.NewReader(`{"deployment_target_id":"target-1","pod_name":"api-7d9f","container_name":"web","tail_lines":500,"since_seconds":30}`)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/project-1/argo/pod-log-audit-snapshot", body)
	req = withRouteParam(req, "id", "project-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func TestArgoPodLogAuditEvidenceStatusCombinations(t *testing.T) {
	tests := []struct {
		name          string
		statuses      []string
		logCounts     []int
		wantState     string
		wantSanitized bool
	}{
		{name: "completed with audit log records sanitized result", statuses: []string{"completed"}, logCounts: []int{1}, wantState: "recorded", wantSanitized: true},
		{name: "completed without audit log is not sanitized result", statuses: []string{"completed"}, wantState: "recorded", wantSanitized: false},
		{name: "running waits", statuses: []string{"running", "completed"}, logCounts: []int{1, 1}, wantState: "waiting_for_worker", wantSanitized: false},
		{name: "failed terminal", statuses: []string{"failed", "completed"}, logCounts: []int{1, 1}, wantState: "failed", wantSanitized: false},
		{name: "canceled terminal", statuses: []string{"canceled"}, wantState: "canceled"},
		{name: "unknown terminal", statuses: []string{"mystery"}, wantState: "unknown"},
		{name: "empty not requested", wantState: "not_requested"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := make([]map[string]any, 0, len(tt.statuses))
			for index, status := range tt.statuses {
				row := map[string]any{"id": fmt.Sprintf("op-%d", index), "status": status}
				if index < len(tt.logCounts) {
					row["operation_log_count"] = tt.logCounts[index]
				}
				rows = append(rows, row)
			}
			evidence := argoPodLogAuditEvidenceSummary(rows)
			if evidence["evidence_state"] != tt.wantState {
				t.Fatalf("evidence_state=%#v want %s; evidence=%#v", evidence["evidence_state"], tt.wantState, evidence)
			}
			if evidence["sanitized_result_recorded"] != tt.wantSanitized {
				t.Fatalf("sanitized_result_recorded=%#v want %v; evidence=%#v", evidence["sanitized_result_recorded"], tt.wantSanitized, evidence)
			}
			encoded, _ := json.Marshal(evidence)
			for _, forbidden := range []string{"log line", "secret kubeconfig", "Bearer secret"} {
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("pod log evidence leaked %q: %s", forbidden, encoded)
				}
			}
		})
	}
}

func TestArgoPodLogLiveStreamReviewPlanReportsMissingAuditLog(t *testing.T) {
	preview := argoPodLogQueryPreview("api-7d9f", "web", 500, 30, map[string]any{
		"id":           "target-1",
		"name":         "prod",
		"environment":  "prod",
		"cluster_name": "prod-cluster",
		"namespace":    "billing",
		"status":       "Healthy",
	}, []map[string]any{
		{"id": "op-pod-logs", "status": "completed", "operation_log_count": int64(0)},
	})
	executionPlan := mapFromAny(mapFromAny(preview["retrieval_plan"])["execution_plan"])
	liveStreamPlan := mapFromAny(executionPlan["live_log_stream_plan"])
	if liveStreamPlan["stream_state"] != "audit_log_missing" ||
		liveStreamPlan["stream_ready_for_review"] != false ||
		!containsString(stringSliceFromAny(liveStreamPlan["blocked_reasons"]), "sanitized_result_operation_log_missing") ||
		!containsString(stringSliceFromAny(liveStreamPlan["blocked_reasons"]), "sanitized_log_result_not_recorded") {
		t.Fatalf("live stream plan should report missing sanitized operation log: %#v", liveStreamPlan)
	}
	resultPlan := mapFromAny(executionPlan["result_recording_plan"])
	if resultPlan["recording_state"] != "recorded" ||
		resultPlan["recording_ready"] != false ||
		resultPlan["recording_enabled"] != false ||
		resultPlan["result_written"] != false ||
		resultPlan["operation_log_written"] != false ||
		resultPlan["recording_ready_reason"] != "pod_log_audit_result_missing_operation_log" ||
		!containsString(stringSliceFromAny(resultPlan["blocked_reasons"]), "sanitized_log_result_not_recorded") {
		t.Fatalf("pod log result recording should not be ready without sanitized operation log: %#v", resultPlan)
	}
}

func TestCleanArgoPodLogRequestClampsRanges(t *testing.T) {
	req, err := cleanArgoPodLogRequest(argoPodLogRequest{
		DeploymentTargetID: " target-1 ",
		PodName:            " api-7d9f ",
		ContainerName:      " web ",
		TailLines:          999999,
		SinceSeconds:       -30,
	})
	if err != nil {
		t.Fatalf("cleanArgoPodLogRequest: %v", err)
	}
	if req.DeploymentTargetID != "target-1" ||
		req.PodName != "api-7d9f" ||
		req.ContainerName != "web" ||
		req.TailLines != 1000 ||
		req.SinceSeconds != 0 {
		t.Fatalf("cleaned pod log request = %#v", req)
	}
	req, err = cleanArgoPodLogRequest(argoPodLogRequest{DeploymentTargetID: "target-1", PodName: "api", TailLines: -1, SinceSeconds: 999999})
	if err != nil {
		t.Fatalf("cleanArgoPodLogRequest default: %v", err)
	}
	if req.TailLines != 200 || req.SinceSeconds != 86400 {
		t.Fatalf("cleaned pod log range defaults = %#v", req)
	}
}

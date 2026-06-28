package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebhookCallbackRehearsalReadinessReconcilesFailedEvidence(t *testing.T) {
	got := webhookCallbackRehearsalReadiness(map[string]any{
		"provider":                   "github",
		"webhook_url":                "https://assops.example.com/api/webhooks/github/hook-1",
		"enabled":                    true,
		"source_remote_id":           "remote-1",
		"event_types":                []any{"workflow_run"},
		"deliveries_7d":              int64(1),
		"failures_7d":                int64(1),
		"replayed_7d":                int64(1),
		"signature_valid_7d":         int64(0),
		"last_event_status":          "rejected",
		"last_event_type":            "workflow_run",
		"last_event_signature_valid": false,
		"secret_token":               "secret-token",
		"payload":                    map[string]any{"body": "payload-body"},
		"result":                     map[string]any{"provider_response": "provider-response"},
		"last_error_message":         "password should not leak",
		"last_delivery_error":        "Bearer secret",
		"delivery_id":                "delivery-secret",
		"provider_url":               "https://provider.example.com/hook",
		"request_headers":            "Authorization: Bearer secret",
		"request_body":               "payload-body",
	}, "https://assops.example.com")

	evidence := mapFromAny(got["callback_evidence"])
	if evidence["evidence_state"] != "failed" ||
		intFromAny(evidence["failed_count_7d"], 0) != 1 ||
		evidence["webhook_event_recorded"] != true ||
		evidence["signature_validation_observed"] != false {
		t.Fatalf("unexpected failed callback evidence: %#v", evidence)
	}
	replayProof := mapFromAny(evidence["operator_replay_proof"])
	if replayProof["proof_state"] != "failed" ||
		replayProof["manual_replay_required"] != false ||
		replayProof["operator_replay_observed"] != true ||
		replayProof["sanitized_replay_result_recorded"] != true ||
		replayProof["signature_validation_observed"] != false ||
		replayProof["provider_api_called"] != false ||
		replayProof["source_delivery_id_recorded"] != false {
		t.Fatalf("unexpected failed operator replay proof: %#v", replayProof)
	}
	plan := mapFromAny(got["provider_rehearsal_plan"])
	thresholdPlan := mapFromAny(plan["threshold_tuning_plan"])
	volumeEvidence := mapFromAny(thresholdPlan["volume_evidence"])
	if thresholdPlan["live_volume_observed"] != true ||
		thresholdPlan["threshold_review_state"] != "review_failed_volume" ||
		thresholdPlan["threshold_review_ready"] != false ||
		volumeEvidence["webhook_failure_volume_observed"] != true ||
		intFromAny(volumeEvidence["failed_count_7d"], 0) != 1 {
		t.Fatalf("unexpected failed threshold volume evidence: threshold=%#v volume=%#v", thresholdPlan, volumeEvidence)
	}
	if !containsString(stringSliceFromAny(thresholdPlan["execution_blockers"]), "webhook_failures_need_operator_threshold_review") {
		t.Fatalf("failed threshold volume should expose failure review blocker: %#v", thresholdPlan["execution_blockers"])
	}
	metricsComparison := mapFromAny(thresholdPlan["provider_metrics_comparison_plan"])
	if metricsComparison["comparison_state"] != "needs_failure_review" ||
		metricsComparison["comparison_ready_for_review"] != false ||
		metricsComparison["local_volume_observed"] != true ||
		metricsComparison["provider_metrics_fetched"] != false ||
		metricsComparison["provider_pair_limits_compared"] != false ||
		metricsComparison["external_call_made"] != false ||
		intFromAny(metricsComparison["failed_count_7d"], 0) != 1 {
		t.Fatalf("failed provider metrics comparison should require failure review without provider calls: %#v", metricsComparison)
	}
	if !containsString(stringSliceFromAny(metricsComparison["blocked_reasons"]), "webhook_failures_need_operator_threshold_review") {
		t.Fatalf("failed provider metrics comparison blocked reasons missing failure review: %#v", metricsComparison["blocked_reasons"])
	}
	configurationPlan := mapFromAny(thresholdPlan["threshold_configuration_plan"])
	if configurationPlan["configuration_state"] != "needs_failure_review" ||
		configurationPlan["configuration_review_ready"] != false ||
		configurationPlan["threshold_configuration_written"] != false ||
		configurationPlan["configuration_write_enabled"] != false ||
		configurationPlan["external_call_made"] != false {
		t.Fatalf("failed threshold configuration should require failure review without writes: %#v", configurationPlan)
	}
	if mapFromAny(configurationPlan["provider_metrics_comparison_plan"])["comparison_state"] != "needs_failure_review" {
		t.Fatalf("failed threshold configuration should carry metrics comparison: %#v", configurationPlan["provider_metrics_comparison_plan"])
	}
	if !containsString(stringSliceFromAny(configurationPlan["blocked_reasons"]), "webhook_failures_need_operator_threshold_review") {
		t.Fatalf("failed threshold configuration blocked reasons missing failure indicator: %#v", configurationPlan["blocked_reasons"])
	}
	decisionAuditPlan := mapFromAny(configurationPlan["threshold_decision_audit_plan"])
	if decisionAuditPlan["decision_state"] != "needs_failure_review" ||
		decisionAuditPlan["decision_ready_for_review"] != false ||
		decisionAuditPlan["threshold_configuration_audit_inserted"] != false ||
		decisionAuditPlan["audit_insert_enabled"] != false ||
		intFromAny(decisionAuditPlan["failed_count_7d"], 0) != 1 ||
		!containsString(stringSliceFromAny(decisionAuditPlan["blocked_reasons"]), "webhook_failures_need_operator_threshold_review") {
		t.Fatalf("failed threshold decision audit should require failure review without writes: %#v", decisionAuditPlan)
	}
	assertWebhookThresholdDecisionAuditPlanSafe(t, decisionAuditPlan)
	resultPlan := mapFromAny(plan["result_recording_plan"])
	if resultPlan["result_recording_state"] != "failed" ||
		resultPlan["result_recording_ready_reason"] != "provider_callback_delivery_failed" ||
		resultPlan["result_written"] != true ||
		resultPlan["webhook_event_recorded"] != true ||
		resultPlan["operator_replay_proof_state"] != "failed" ||
		resultPlan["operator_replay_proof_recorded"] != false ||
		resultPlan["repo_sync_result_recorded"] != false {
		t.Fatalf("unexpected failed callback result plan: %#v", resultPlan)
	}
	encoded, _ := json.Marshal(got)
	for _, forbidden := range []string{"secret-token", "payload-body", "provider-response", "password", "Bearer secret", "delivery-secret", "provider.example.com"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("failed callback rehearsal evidence leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestAnnotateWebhookThresholdDecisionAuditEvidenceMarksPersistedAudit(t *testing.T) {
	item := map[string]any{
		"source_remote_id":           "remote-1",
		"deliveries_7d":              int64(4),
		"failures_7d":                int64(0),
		"processed_7d":               int64(2),
		"operation_run_7d":           int64(1),
		"matched_repo_sync_asset_7d": int64(1),
		"callback_rehearsal": map[string]any{
			"provider_rehearsal_plan": map[string]any{
				"threshold_tuning_plan": map[string]any{
					"threshold_configuration_plan": map[string]any{
						"operator_threshold_review_recorded": false,
						"threshold_decision_audit_plan": map[string]any{
							"threshold_configuration_audit_inserted": false,
							"operator_threshold_review_recorded":     false,
						},
					},
				},
			},
		},
		"threshold_decision_audit_count":     int64(2),
		"last_threshold_decision_audit_at":   "2026-06-24T10:00:00Z",
		"raw_request_body_should_not_appear": "payload-body",
	}
	annotateWebhookThresholdDecisionAuditEvidence([]map[string]any{item})
	readiness := mapFromAny(item["callback_rehearsal"])
	providerPlan := mapFromAny(readiness["provider_rehearsal_plan"])
	thresholdPlan := mapFromAny(providerPlan["threshold_tuning_plan"])
	configurationPlan := mapFromAny(thresholdPlan["threshold_configuration_plan"])
	decisionAuditPlan := mapFromAny(configurationPlan["threshold_decision_audit_plan"])
	if configurationPlan["operator_threshold_review_recorded"] != true ||
		decisionAuditPlan["threshold_configuration_audit_inserted"] != true ||
		decisionAuditPlan["operator_threshold_review_recorded"] != true ||
		intFromAny(decisionAuditPlan["threshold_decision_audit_count"], 0) != 2 ||
		decisionAuditPlan["last_threshold_decision_audit_at"] != "2026-06-24T10:00:00Z" {
		t.Fatalf("persisted threshold audit evidence was not merged into readiness: %#v", readiness)
	}
	encoded, _ := json.Marshal(readiness)
	if strings.Contains(string(encoded), "payload-body") {
		t.Fatalf("annotated threshold audit evidence leaked unrelated raw field: %s", encoded)
	}
}

func TestAnnotateWebhookThresholdConfigurationEvidenceMarksPersistedConfig(t *testing.T) {
	item := map[string]any{
		"source_remote_id":           "remote-1",
		"deliveries_7d":              int64(4),
		"failures_7d":                int64(0),
		"processed_7d":               int64(2),
		"operation_run_7d":           int64(1),
		"matched_repo_sync_asset_7d": int64(1),
		"callback_rehearsal": map[string]any{
			"provider_rehearsal_plan": map[string]any{
				"threshold_tuning_plan": map[string]any{
					"threshold_configuration_written": false,
					"threshold_configuration_plan": map[string]any{
						"configuration_state":                "ready_for_operator_review",
						"configuration_write_enabled":        false,
						"threshold_configuration_written":    false,
						"operator_threshold_review_recorded": true,
						"blocked_reasons":                    []string{"operator_threshold_review_not_recorded", "threshold_configuration_write_disabled"},
						"threshold_decision_audit_plan": map[string]any{
							"operator_threshold_review_recorded": true,
							"configuration_write_enabled":        false,
							"threshold_configuration_written":    false,
							"threshold_decision_audit_count":     1,
							"blocked_reasons":                    []string{"operator_threshold_review_not_recorded", "threshold_configuration_write_disabled"},
						},
					},
				},
			},
		},
		"threshold_configuration_count":     int64(6),
		"last_threshold_configuration_at":   "2026-06-24T10:30:00Z",
		"raw_provider_response_should_hide": "provider-response",
	}
	annotateWebhookThresholdConfigurationEvidence([]map[string]any{item})
	readiness := mapFromAny(item["callback_rehearsal"])
	providerPlan := mapFromAny(readiness["provider_rehearsal_plan"])
	thresholdPlan := mapFromAny(providerPlan["threshold_tuning_plan"])
	configurationPlan := mapFromAny(thresholdPlan["threshold_configuration_plan"])
	decisionAuditPlan := mapFromAny(configurationPlan["threshold_decision_audit_plan"])
	if thresholdPlan["threshold_configuration_written"] != true ||
		thresholdPlan["provider_pair_thresholds_tuned"] != true ||
		configurationPlan["threshold_configuration_written"] != true ||
		configurationPlan["configuration_state"] != "recorded" ||
		configurationPlan["configuration_write_enabled"] != true ||
		configurationPlan["capacity_signals_recomputed"] != true ||
		configurationPlan["capacity_signal_recompute_mode"] != "read_time_repo_sync_asset_detail" ||
		intFromAny(configurationPlan["threshold_configuration_count"], 0) != 6 ||
		decisionAuditPlan["threshold_configuration_written"] != true ||
		decisionAuditPlan["capacity_signals_recomputed"] != true ||
		decisionAuditPlan["capacity_signal_recompute_mode"] != "read_time_repo_sync_asset_detail" ||
		decisionAuditPlan["configuration_write_enabled"] != true {
		t.Fatalf("persisted threshold configuration evidence was not merged into readiness: %#v", readiness)
	}
	recomputeEvidence := mapFromAny(configurationPlan["capacity_signal_recompute_evidence"])
	if recomputeEvidence["mode"] != "webhook_threshold_capacity_signal_recompute_evidence" ||
		recomputeEvidence["capacity_signals_recomputed"] != true ||
		recomputeEvidence["recompute_mode"] != "read_time_repo_sync_asset_detail" ||
		recomputeEvidence["source_remote_id"] != "remote-1" ||
		recomputeEvidence["external_call_made"] != false ||
		recomputeEvidence["raw_provider_response_recorded"] != false {
		t.Fatalf("capacity recompute evidence = %#v", recomputeEvidence)
	}
	if containsString(stringSliceFromAny(configurationPlan["blocked_reasons"]), "operator_threshold_review_not_recorded") ||
		containsString(stringSliceFromAny(decisionAuditPlan["blocked_reasons"]), "operator_threshold_review_not_recorded") {
		t.Fatalf("persisted threshold configuration should remove operator-review blocker: %#v %#v", configurationPlan, decisionAuditPlan)
	}
	encoded, _ := json.Marshal(readiness)
	if strings.Contains(string(encoded), "provider-response") {
		t.Fatalf("annotated threshold configuration evidence leaked unrelated raw field: %s", encoded)
	}
}

func newProviderCallbackSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/webhook-connections/conn-1/provider-callback-rehearsal-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "conn-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func TestValidWebhookEvidenceWindow(t *testing.T) {
	for _, value := range []string{"1h", "7d", "2w", "12m"} {
		if !validWebhookEvidenceWindow(value) {
			t.Fatalf("validWebhookEvidenceWindow(%q) = false", value)
		}
	}
	for _, value := range []string{"", "0d", "-1d", "7", "abc123", "1y", "1 d"} {
		if validWebhookEvidenceWindow(value) {
			t.Fatalf("validWebhookEvidenceWindow(%q) = true", value)
		}
	}
}

package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWebhookCallbackRehearsalReadinessReconcilesObservedEvidence(t *testing.T) {
	got := webhookCallbackRehearsalReadiness(map[string]any{
		"provider":                       "gitea",
		"webhook_url":                    "https://assops.example.com/api/webhooks/gitea/hook-1",
		"enabled":                        true,
		"source_remote_id":               "remote-1",
		"event_types":                    []any{"push"},
		"deliveries_7d":                  int64(2),
		"processed_7d":                   int64(1),
		"ignored_7d":                     int64(1),
		"signature_valid_7d":             int64(2),
		"matched_repo_sync_asset_7d":     int64(1),
		"operation_run_7d":               int64(1),
		"replayed_7d":                    int64(1),
		"last_event_status":              "processed",
		"last_event_type":                "push",
		"last_event_signature_valid":     true,
		"secret_token":                   "secret-token",
		"payload":                        map[string]any{"body": "payload-body"},
		"result":                         map[string]any{"provider_response": "provider-response"},
		"last_error_message":             "password should not leak",
		"last_delivery_error":            "Bearer secret",
		"last_delivery_status":           "processed",
		"delivery_id":                    "delivery-secret",
		"provider_url":                   "https://provider.example.com/hook",
		"request_headers":                "Authorization: Bearer secret",
		"request_body":                   "payload-body",
		"provider_response_body":         "provider-response",
		"provider_response_headers":      "provider-response-headers",
		"matched_repo_sync_asset_secret": "asset-secret",
	}, "https://assops.example.com")

	evidence := mapFromAny(got["callback_evidence"])
	if evidence["mode"] != "provider_callback_rehearsal_evidence" ||
		evidence["evidence_state"] != "recorded" ||
		intFromAny(evidence["delivery_count_7d"], 0) != 2 ||
		intFromAny(evidence["processed_count_7d"], 0) != 1 ||
		intFromAny(evidence["signature_valid_count_7d"], 0) != 2 ||
		intFromAny(evidence["matched_repo_sync_asset_count_7d"], 0) != 1 ||
		intFromAny(evidence["operation_run_count_7d"], 0) != 1 ||
		intFromAny(evidence["replayed_count_7d"], 0) != 1 ||
		evidence["webhook_event_recorded"] != true ||
		evidence["provider_delivery_observed"] != true ||
		evidence["signature_validation_observed"] != true ||
		evidence["webhook_event_replay_observed"] != true ||
		evidence["repo_sync_enqueue_observed"] != true ||
		evidence["external_call_made_by_assops"] != false ||
		evidence["provider_settings_written"] != false ||
		evidence["provider_test_delivery_sent"] != false ||
		evidence["contains_token"] != false ||
		evidence["contains_secret"] != false ||
		evidence["contains_payload"] != false ||
		evidence["contains_provider_url"] != false {
		t.Fatalf("unexpected callback evidence: %#v", evidence)
	}
	replayProof := mapFromAny(evidence["operator_replay_proof"])
	if replayProof["mode"] != "operator_guided_webhook_replay_proof" ||
		replayProof["proof_state"] != "recorded" ||
		replayProof["proof_source"] != "webhook_events_aggregate" ||
		replayProof["manual_replay_required"] != false ||
		replayProof["operator_replay_observed"] != true ||
		replayProof["sanitized_replay_result_recorded"] != true ||
		intFromAny(replayProof["replayed_event_count_7d"], 0) != 1 ||
		replayProof["signature_validation_observed"] != true ||
		replayProof["repo_sync_binding_observed"] != true ||
		replayProof["operation_binding_observed"] != true ||
		replayProof["external_call_made_by_assops"] != false ||
		replayProof["provider_api_called"] != false ||
		replayProof["provider_test_delivery_sent"] != false ||
		replayProof["source_delivery_id_recorded"] != false ||
		replayProof["replay_source_delivery_id_recorded"] != false ||
		replayProof["contains_token"] != false ||
		replayProof["contains_secret"] != false ||
		replayProof["contains_payload"] != false ||
		replayProof["contains_provider_url"] != false {
		t.Fatalf("unexpected operator replay proof: %#v", replayProof)
	}
	plan := mapFromAny(got["provider_rehearsal_plan"])
	if plan["provider_delivery_received"] != true ||
		plan["webhook_event_created"] != true ||
		plan["webhook_event_replayed"] != true ||
		plan["operator_replay_proof_recorded"] != true ||
		plan["repo_sync_enqueued"] != true ||
		plan["result_written"] != true ||
		plan["external_call_made"] != false ||
		plan["provider_settings_written"] != false ||
		plan["provider_test_delivery_sent"] != false {
		t.Fatalf("unexpected provider rehearsal plan evidence: %#v", plan)
	}
	resultPlan := mapFromAny(plan["result_recording_plan"])
	if resultPlan["result_recording_state"] != "recorded" ||
		resultPlan["result_recording_ready"] != true ||
		resultPlan["result_written"] != true ||
		resultPlan["webhook_event_recorded"] != true ||
		resultPlan["operator_replay_proof_state"] != "recorded" ||
		resultPlan["operator_replay_proof_recorded"] != true ||
		resultPlan["repo_sync_result_recorded"] != true ||
		resultPlan["github_actions_result_recorded"] != false ||
		resultPlan["raw_request_headers_recorded"] != false ||
		resultPlan["raw_request_body_recorded"] != false ||
		resultPlan["raw_provider_response_recorded"] != false {
		t.Fatalf("unexpected callback result plan: %#v", resultPlan)
	}
	thresholdPlan := mapFromAny(plan["threshold_tuning_plan"])
	volumeEvidence := mapFromAny(thresholdPlan["volume_evidence"])
	if thresholdPlan["live_volume_observed"] != true ||
		thresholdPlan["threshold_review_state"] != "ready_for_review" ||
		thresholdPlan["threshold_review_ready"] != true ||
		thresholdPlan["threshold_configuration_written"] != false ||
		volumeEvidence["local_volume_observed"] != true ||
		volumeEvidence["repo_sync_volume_observed"] != true ||
		volumeEvidence["provider_volume_observed"] != false ||
		volumeEvidence["provider_metrics_fetched"] != false ||
		intFromAny(volumeEvidence["delivery_count_7d"], 0) != 2 ||
		intFromAny(volumeEvidence["operation_run_count_7d"], 0) != 1 {
		t.Fatalf("unexpected threshold volume evidence: threshold=%#v volume=%#v", thresholdPlan, volumeEvidence)
	}
	if !containsString(stringSliceFromAny(thresholdPlan["execution_blockers"]), "operator_threshold_review_not_recorded") {
		t.Fatalf("ready threshold volume should wait for operator threshold review: %#v", thresholdPlan["execution_blockers"])
	}
	metricsComparison := mapFromAny(thresholdPlan["provider_metrics_comparison_plan"])
	if metricsComparison["comparison_state"] != "ready_for_operator_review" ||
		metricsComparison["comparison_ready_for_review"] != true ||
		metricsComparison["local_volume_observed"] != true ||
		metricsComparison["provider_metrics_fetched"] != false ||
		metricsComparison["provider_pair_limits_compared"] != false ||
		metricsComparison["external_call_made"] != false ||
		intFromAny(metricsComparison["delivery_count_7d"], 0) != 2 ||
		intFromAny(metricsComparison["operation_run_count_7d"], 0) != 1 {
		t.Fatalf("ready provider metrics comparison should use local volume only: %#v", metricsComparison)
	}
	if !containsString(stringSliceFromAny(metricsComparison["blocked_reasons"]), "provider_metrics_fetch_disabled") {
		t.Fatalf("ready provider metrics comparison should keep fetch disabled: %#v", metricsComparison["blocked_reasons"])
	}
	configurationPlan := mapFromAny(thresholdPlan["threshold_configuration_plan"])
	if configurationPlan["configuration_state"] != "ready_for_operator_review" ||
		configurationPlan["configuration_review_ready"] != true ||
		configurationPlan["threshold_configuration_written"] != false ||
		configurationPlan["configuration_write_enabled"] != false ||
		configurationPlan["provider_metrics_fetched"] != false ||
		configurationPlan["provider_pair_limits_compared"] != false ||
		configurationPlan["external_call_made"] != false {
		t.Fatalf("ready threshold configuration should wait for operator review without writes: %#v", configurationPlan)
	}
	if mapFromAny(configurationPlan["provider_metrics_comparison_plan"])["comparison_state"] != "ready_for_operator_review" {
		t.Fatalf("ready threshold configuration should carry metrics comparison: %#v", configurationPlan["provider_metrics_comparison_plan"])
	}
	if !containsString(stringSliceFromAny(configurationPlan["blocked_reasons"]), "threshold_configuration_write_disabled") {
		t.Fatalf("ready threshold configuration should keep write disabled: %#v", configurationPlan["blocked_reasons"])
	}
	decisionAuditPlan := mapFromAny(configurationPlan["threshold_decision_audit_plan"])
	if decisionAuditPlan["decision_state"] != "metadata_review_ready" ||
		decisionAuditPlan["decision_ready_for_review"] != true ||
		decisionAuditPlan["threshold_configuration_audit_inserted"] != false ||
		decisionAuditPlan["audit_insert_enabled"] != true ||
		decisionAuditPlan["capacity_signals_recomputed"] != false ||
		intFromAny(decisionAuditPlan["delivery_count_7d"], 0) != 2 ||
		intFromAny(decisionAuditPlan["operation_run_count_7d"], 0) != 1 {
		t.Fatalf("ready threshold decision audit should expose metadata review only: %#v", decisionAuditPlan)
	}
	assertWebhookThresholdDecisionAuditPlanSafe(t, decisionAuditPlan)
	encoded, _ := json.Marshal(got)
	for _, forbidden := range []string{"secret-token", "payload-body", "provider-response", "password", "Bearer secret", "delivery-secret", "provider.example.com", "asset-secret"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("callback rehearsal evidence leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestWebhookCallbackRehearsalObservedDeliveryDoesNotRecordTerminalResult(t *testing.T) {
	got := webhookCallbackRehearsalReadiness(map[string]any{
		"provider":                   "gitea",
		"webhook_url":                "https://assops.example.com/api/webhooks/gitea/hook-1",
		"enabled":                    true,
		"source_remote_id":           "remote-1",
		"event_types":                []any{"push"},
		"deliveries_7d":              int64(1),
		"processed_7d":               int64(0),
		"ignored_7d":                 int64(0),
		"failures_7d":                int64(0),
		"signature_valid_7d":         int64(1),
		"matched_repo_sync_asset_7d": int64(0),
		"operation_run_7d":           int64(0),
		"replayed_7d":                int64(0),
		"last_event_status":          "received",
		"last_event_type":            "push",
		"last_delivery_status":       "received",
	}, "https://assops.example.com")

	evidence := mapFromAny(got["callback_evidence"])
	if evidence["evidence_state"] != "observed" ||
		evidence["webhook_event_recorded"] != true ||
		evidence["provider_delivery_observed"] != true ||
		evidence["sanitized_result_recorded"] != false {
		t.Fatalf("observed-only callback delivery should not claim terminal result recording: %#v", evidence)
	}
	plan := mapFromAny(got["provider_rehearsal_plan"])
	if plan["provider_delivery_received"] != true ||
		plan["webhook_event_created"] != true ||
		plan["result_written"] != false {
		t.Fatalf("observed-only callback plan should expose delivery without result write: %#v", plan)
	}
	resultPlan := mapFromAny(plan["result_recording_plan"])
	if resultPlan["result_recording_state"] != "observed" ||
		resultPlan["result_recording_ready"] != false ||
		resultPlan["result_written"] != false ||
		resultPlan["result_recording_ready_reason"] != "provider_callback_delivery_observed_without_terminal_result" ||
		resultPlan["webhook_event_recorded"] != true {
		t.Fatalf("observed-only callback result plan should wait for terminal result: %#v", resultPlan)
	}
}

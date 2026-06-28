package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWebhookCallbackThresholdDecisionAuditUsesMetadataNotReadyReason(t *testing.T) {
	got := webhookCallbackRehearsalReadiness(map[string]any{
		"provider":                   "gitea",
		"webhook_url":                "https://assops.example.com/api/webhooks/gitea/hook-1",
		"enabled":                    true,
		"source_remote_id":           "remote-1",
		"event_types":                []any{"push"},
		"deliveries_7d":              int64(1),
		"processed_7d":               int64(0),
		"matched_repo_sync_asset_7d": int64(0),
		"operation_run_7d":           int64(0),
		"last_event_status":          "received",
	}, "https://assops.example.com")

	plan := mapFromAny(got["provider_rehearsal_plan"])
	thresholdPlan := mapFromAny(plan["threshold_tuning_plan"])
	volumeEvidence := mapFromAny(thresholdPlan["volume_evidence"])
	if volumeEvidence["threshold_review_state"] != "volume_observed" ||
		volumeEvidence["threshold_review_ready"] != false {
		t.Fatalf("threshold volume should be observed but not ready: %#v", volumeEvidence)
	}
	configurationPlan := mapFromAny(thresholdPlan["threshold_configuration_plan"])
	decisionAuditPlan := mapFromAny(configurationPlan["threshold_decision_audit_plan"])
	if decisionAuditPlan["decision_state"] != "blocked" ||
		decisionAuditPlan["decision_ready_for_review"] != false ||
		!containsString(stringSliceFromAny(decisionAuditPlan["blocked_reasons"]), "threshold_review_metadata_not_ready") ||
		containsString(stringSliceFromAny(decisionAuditPlan["blocked_reasons"]), "operator_threshold_review_not_recorded") {
		t.Fatalf("metadata-not-ready threshold decision audit should avoid operator-review blocker: %#v", decisionAuditPlan)
	}
	assertWebhookThresholdDecisionAuditPlanSafe(t, decisionAuditPlan)
}

func assertWebhookThresholdDecisionAuditPlanSafe(t *testing.T, plan map[string]any) {
	t.Helper()
	if plan["mode"] != "provider_callback_threshold_decision_audit_plan" ||
		plan["threshold_configuration_written"] != false ||
		plan["threshold_configuration_audit_inserted"] != false ||
		plan["configuration_write_enabled"] != false ||
		plan["capacity_signals_recomputed"] != false ||
		plan["provider_metrics_fetched"] != false ||
		plan["provider_pair_limits_compared"] != false ||
		plan["proposed_threshold_delta_persisted"] != false ||
		plan["operator_threshold_review_recorded"] != false ||
		plan["external_call_made"] != false ||
		plan["contains_token"] != false ||
		plan["contains_secret"] != false ||
		plan["contains_payload"] != false ||
		plan["contains_provider_url"] != false {
		t.Fatalf("threshold decision audit plan should stay disabled and redacted: %#v", plan)
	}
	for _, field := range []string{"provider_pair", "threshold_key", "current_warning_at", "current_danger_at", "proposed_warning_at", "proposed_danger_at", "evidence_window", "operator_decision", "reviewed_at"} {
		if !containsString(stringSliceFromAny(plan["required_decision_fields"]), field) {
			t.Fatalf("threshold decision audit required_decision_fields missing %q: %#v", field, plan["required_decision_fields"])
		}
	}
	for _, control := range []string{"operator_threshold_review", "provider_metrics_comparison_review", "threshold_delta_schema_review", "audit_row_redaction_review", "capacity_signal_recompute_review"} {
		if !containsString(stringSliceFromAny(plan["required_controls"]), control) {
			t.Fatalf("threshold decision audit required_controls missing %q: %#v", control, plan["required_controls"])
		}
	}
	for _, backend := range []string{"threshold_configuration_write", "threshold_delta_persist", "provider_metrics_fetch"} {
		if !containsString(stringSliceFromAny(plan["disabled_backends"]), backend) {
			t.Fatalf("threshold decision audit disabled_backends missing %q: %#v", backend, plan["disabled_backends"])
		}
	}
	if containsString(stringSliceFromAny(plan["disabled_backends"]), "sync_capacity_signal_recompute") {
		t.Fatalf("local read-time capacity recompute should not be listed as disabled: %#v", plan["disabled_backends"])
	}
	if plan["audit_insert_enabled"] != true && !containsString(stringSliceFromAny(plan["disabled_backends"]), "threshold_configuration_audit_insert") {
		t.Fatalf("threshold decision audit disabled_backends missing audit insert while disabled: %#v", plan["disabled_backends"])
	}
	for _, field := range []string{"operator_identity", "operator_notes", "provider_token", "provider_url", "authorization_header", "request_headers", "provider_response_body", "provider_response_headers", "delivery_id", "payload"} {
		if !containsString(stringSliceFromAny(plan["suppressed_fields"]), field) {
			t.Fatalf("threshold decision audit suppressed_fields missing %q: %#v", field, plan["suppressed_fields"])
		}
	}
	for _, reason := range []string{"threshold_configuration_write_disabled"} {
		if !containsString(stringSliceFromAny(plan["blocked_reasons"]), reason) {
			t.Fatalf("threshold decision audit blocked_reasons missing %q: %#v", reason, plan["blocked_reasons"])
		}
	}
	if plan["audit_insert_enabled"] != true && !containsString(stringSliceFromAny(plan["blocked_reasons"]), "threshold_configuration_audit_insert_disabled") {
		t.Fatalf("threshold decision audit blocked_reasons missing audit insert while disabled: %#v", plan["blocked_reasons"])
	}
	encoded, _ := json.Marshal(plan)
	for _, forbidden := range []string{"secret-token", "Bearer secret", "payload-body", "provider-response", "provider.example.com", "operator@example.com"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("threshold decision audit plan leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestWebhookProviderCallbackOperatorReplayProofResultRecordedBoundaries(t *testing.T) {
	tests := []struct {
		name               string
		failures           int
		processed          int
		replayed           int
		matchedRepoSync    int
		operationRuns      int
		wantState          string
		wantResultRecorded bool
	}{
		{
			name:               "waiting without replay aggregate",
			wantState:          "waiting_for_operator_replay",
			wantResultRecorded: false,
		},
		{
			name:               "observed replay without terminal result or binding",
			replayed:           1,
			wantState:          "observed",
			wantResultRecorded: false,
		},
		{
			name:               "recorded replay with processed delivery",
			processed:          1,
			replayed:           1,
			wantState:          "recorded",
			wantResultRecorded: true,
		},
		{
			name:               "recorded replay with repo sync binding",
			replayed:           1,
			matchedRepoSync:    1,
			wantState:          "recorded",
			wantResultRecorded: true,
		},
		{
			name:               "recorded replay with operation binding",
			replayed:           1,
			operationRuns:      1,
			wantState:          "recorded",
			wantResultRecorded: true,
		},
		{
			name:               "failed terminal replay",
			failures:           1,
			replayed:           1,
			wantState:          "failed",
			wantResultRecorded: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proof := webhookProviderCallbackOperatorReplayProof(1, tt.failures, tt.processed, tt.replayed, 0, tt.matchedRepoSync, tt.operationRuns)
			if proof["proof_state"] != tt.wantState ||
				proof["sanitized_replay_result_recorded"] != tt.wantResultRecorded {
				t.Fatalf("replay proof = %#v, want state %s and result recorded %v", proof, tt.wantState, tt.wantResultRecorded)
			}
		})
	}
}

func TestWebhookCallbackRehearsalEvidenceStateBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		row        map[string]any
		wantState  string
		wantResult string
		wantReady  bool
	}{
		{
			name:       "observed without terminal classification",
			row:        map[string]any{"provider": "gitea", "deliveries_7d": int64(1), "last_event_status": "received"},
			wantState:  "observed",
			wantResult: "observed",
			wantReady:  false,
		},
		{
			name:       "ignored",
			row:        map[string]any{"provider": "gitea", "deliveries_7d": int64(1), "ignored_7d": int64(1), "last_event_status": "ignored"},
			wantState:  "ignored",
			wantResult: "ignored",
			wantReady:  true,
		},
		{
			name:       "github processed without operation run is not actions refresh evidence",
			row:        map[string]any{"provider": "github", "deliveries_7d": int64(1), "processed_7d": int64(1), "operation_run_7d": int64(0), "last_event_status": "processed"},
			wantState:  "recorded",
			wantResult: "recorded",
			wantReady:  true,
		},
		{
			name:       "operator replay still waiting when no replay aggregate exists",
			row:        map[string]any{"provider": "gitea", "deliveries_7d": int64(1), "processed_7d": int64(1), "last_event_status": "processed"},
			wantState:  "recorded",
			wantResult: "recorded",
			wantReady:  true,
		},
		{
			name:       "operator replay observed before processing or repo sync binding",
			row:        map[string]any{"provider": "gitea", "deliveries_7d": int64(1), "replayed_7d": int64(1), "last_event_status": "received"},
			wantState:  "observed",
			wantResult: "observed",
			wantReady:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evidence := webhookProviderCallbackRehearsalEvidence(tt.row)
			if evidence["evidence_state"] != tt.wantState {
				t.Fatalf("evidence_state = %v, want %s: %#v", evidence["evidence_state"], tt.wantState, evidence)
			}
			resultPlan := webhookProviderCallbackRehearsalResultRecordingPlan(evidence)
			if resultPlan["result_recording_state"] != tt.wantResult {
				t.Fatalf("result_recording_state = %v, want %s: %#v", resultPlan["result_recording_state"], tt.wantResult, resultPlan)
			}
			if resultPlan["result_recording_ready"] != tt.wantReady || resultPlan["result_written"] != tt.wantReady {
				t.Fatalf("result recording ready/written = %v/%v, want %v: %#v", resultPlan["result_recording_ready"], resultPlan["result_written"], tt.wantReady, resultPlan)
			}
			if strings.Contains(tt.name, "github processed") && evidence["github_actions_refresh_observed"] != false {
				t.Fatalf("github actions refresh should require operation_run evidence: %#v", evidence)
			}
			if strings.Contains(tt.name, "operator replay still waiting") {
				replayProof := mapFromAny(evidence["operator_replay_proof"])
				if replayProof["proof_state"] != "waiting_for_operator_replay" || replayProof["manual_replay_required"] != true || replayProof["operator_replay_observed"] != false {
					t.Fatalf("operator replay proof should stay waiting without replay aggregate: %#v", replayProof)
				}
			}
			if strings.Contains(tt.name, "operator replay observed") {
				replayProof := mapFromAny(evidence["operator_replay_proof"])
				if replayProof["proof_state"] != "observed" || replayProof["manual_replay_required"] != false || replayProof["operator_replay_observed"] != true || replayProof["sanitized_replay_result_recorded"] != false {
					t.Fatalf("operator replay proof should be observed without claiming sanitized result before processing or repo sync binding: %#v", replayProof)
				}
			}
		})
	}
}

func TestWebhookCallbackRehearsalResultRecordingPlanDefaultsMissingReplayProof(t *testing.T) {
	resultPlan := webhookProviderCallbackRehearsalResultRecordingPlan(map[string]any{
		"evidence_state":            "recorded",
		"webhook_event_recorded":    true,
		"sanitized_result_recorded": true,
	})
	if resultPlan["operator_replay_proof_state"] != "waiting_for_operator_replay" ||
		resultPlan["operator_replay_proof_recorded"] != false {
		t.Fatalf("missing replay proof should default to waiting without <nil> leak: %#v", resultPlan)
	}
	encoded, _ := json.Marshal(resultPlan)
	if strings.Contains(string(encoded), "<nil>") {
		t.Fatalf("missing replay proof leaked nil sentinel: %s", encoded)
	}
}

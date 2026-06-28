package app

import (
	"fmt"
	"strings"
)

func webhookCallbackRehearsalReadiness(row map[string]any, baseURL string) map[string]any {
	reasons := make([]string, 0)
	origin := strings.TrimSpace(baseURL)
	if !isPlausiblePublicWebhookOrigin(origin) {
		reasons = append(reasons, "set ASSOPS_GATEWAY_URL to a public HTTP(S) origin before provider callback rehearsal")
	}
	if enabled, ok := row["enabled"].(bool); ok && !enabled {
		reasons = append(reasons, "webhook connection is disabled")
	}
	if !hasNonZeroValue(row["source_remote_id"]) {
		reasons = append(reasons, "source remote is missing")
	}
	if len(stringSliceFromAny(row["event_types"])) == 0 {
		reasons = append(reasons, "event types are missing")
	}
	if failures := intFromAny(row["failures_7d"], 0); failures > 0 {
		reasons = append(reasons, fmt.Sprintf("%d failed or rejected deliveries in 7d should be reviewed before rehearsal", failures))
	}
	switch strings.TrimSpace(fmt.Sprint(row["last_delivery_status"])) {
	case "failed", "rejected":
		reasons = append(reasons, "last delivery was "+fmt.Sprint(row["last_delivery_status"]))
	}
	status := "ready"
	message := "local prerequisites are ready; complete provider callback rehearsal in Gitea/GitHub"
	if len(reasons) > 0 {
		status = "blocked"
		message = strings.Join(reasons, "; ")
	}
	evidence := webhookProviderCallbackRehearsalEvidence(row)
	return map[string]any{
		"status":                  status,
		"public_origin":           origin,
		"provider":                strings.TrimSpace(fmt.Sprint(row["provider"])),
		"webhook_url":             strings.TrimSpace(fmt.Sprint(row["webhook_url"])),
		"required_provider":       "gitea_or_github_webhook_settings",
		"external_call_made":      false,
		"reasons":                 reasons,
		"callback_evidence":       evidence,
		"provider_rehearsal_plan": webhookProviderCallbackRehearsalPlan(status, reasons, evidence),
		"message":                 message,
	}
}

func webhookProviderCallbackRehearsalEvidence(row map[string]any) map[string]any {
	deliveries := intFromAny(row["deliveries_7d"], 0)
	failures := intFromAny(row["failures_7d"], 0)
	processed := intFromAny(row["processed_7d"], 0)
	ignored := intFromAny(row["ignored_7d"], 0)
	replayed := intFromAny(row["replayed_7d"], 0)
	signatureValid := intFromAny(row["signature_valid_7d"], 0)
	matchedRepoSyncAsset := intFromAny(row["matched_repo_sync_asset_7d"], 0)
	operationRuns := intFromAny(row["operation_run_7d"], 0)
	lastStatus := strings.ToLower(strings.TrimSpace(fmt.Sprint(row["last_event_status"])))
	if lastStatus == "" || lastStatus == "<nil>" {
		lastStatus = strings.ToLower(strings.TrimSpace(fmt.Sprint(row["last_delivery_status"])))
	}
	lastEventType := strings.ToLower(strings.TrimSpace(fmt.Sprint(row["last_event_type"])))
	if lastEventType == "<nil>" {
		lastEventType = ""
	}
	state := "not_observed"
	switch {
	case deliveries == 0:
		state = "not_observed"
	case failures > 0:
		state = "failed"
	case processed > 0:
		state = "recorded"
	case ignored > 0:
		state = "ignored"
	default:
		state = "observed"
	}
	sanitizedResultRecorded := deliveries > 0 && (processed > 0 || ignored > 0 || failures > 0)
	return map[string]any{
		"mode":                             "provider_callback_rehearsal_evidence",
		"evidence_state":                   state,
		"delivery_count_7d":                deliveries,
		"processed_count_7d":               processed,
		"failed_count_7d":                  failures,
		"ignored_count_7d":                 ignored,
		"replayed_count_7d":                replayed,
		"signature_valid_count_7d":         signatureValid,
		"matched_repo_sync_asset_count_7d": matchedRepoSyncAsset,
		"operation_run_count_7d":           operationRuns,
		"last_event_at":                    row["last_event_at"],
		"last_event_status":                lastStatus,
		"last_event_type":                  lastEventType,
		"last_event_signature_valid":       boolOnlyFromAny(row["last_event_signature_valid"]),
		"provider":                         strings.TrimSpace(fmt.Sprint(row["provider"])),
		"webhook_event_recorded":           deliveries > 0,
		"provider_delivery_observed":       deliveries > 0,
		"signature_validation_observed":    signatureValid > 0,
		"webhook_event_replay_observed":    replayed > 0,
		"repo_sync_enqueue_observed":       operationRuns > 0 || matchedRepoSyncAsset > 0,
		"github_actions_refresh_observed":  strings.EqualFold(strings.TrimSpace(fmt.Sprint(row["provider"])), "github") && processed > 0 && operationRuns > 0,
		"sanitized_result_recorded":        sanitizedResultRecorded,
		"operator_replay_proof":            webhookProviderCallbackOperatorReplayProof(deliveries, failures, processed, replayed, signatureValid, matchedRepoSyncAsset, operationRuns),
		"external_call_made_by_assops":     false,
		"provider_settings_written":        false,
		"provider_test_delivery_sent":      false,
		"raw_request_headers_recorded":     false,
		"raw_request_body_recorded":        false,
		"raw_provider_response_recorded":   false,
		"contains_token":                   false,
		"contains_secret":                  false,
		"contains_payload":                 false,
		"contains_provider_url":            false,
		"suppressed_fields":                []string{"secret_token", "shared_secret", "signature_header", "provider_token", "provider_url", "request_headers", "request_body", "delivery_payload", "delivery_response", "provider_response_body", "provider_response_headers", "delivery_id", "payload", "result", "error_message"},
	}
}

func webhookProviderCallbackOperatorReplayProof(deliveries, failures, processed, replayed, signatureValid, matchedRepoSyncAsset, operationRuns int) map[string]any {
	proofState := "waiting_for_operator_replay"
	switch {
	case replayed > 0 && failures > 0 && processed == 0:
		proofState = "failed"
	case replayed > 0 && (operationRuns > 0 || matchedRepoSyncAsset > 0 || processed > 0):
		proofState = "recorded"
	case replayed > 0:
		proofState = "observed"
	}
	replayResultRecorded := proofState == "recorded" || proofState == "failed"
	return map[string]any{
		"mode":                               "operator_guided_webhook_replay_proof",
		"proof_state":                        proofState,
		"proof_source":                       "webhook_events_aggregate",
		"manual_replay_required":             replayed == 0,
		"operator_replay_observed":           replayed > 0,
		"sanitized_replay_result_recorded":   replayResultRecorded,
		"delivery_evidence_count_7d":         deliveries,
		"replayed_event_count_7d":            replayed,
		"processed_delivery_count_7d":        processed,
		"failed_delivery_count_7d":           failures,
		"signature_valid_delivery_count_7d":  signatureValid,
		"repo_sync_binding_count_7d":         matchedRepoSyncAsset,
		"operation_binding_count_7d":         operationRuns,
		"signature_validation_observed":      replayed > 0 && signatureValid > 0,
		"repo_sync_binding_observed":         replayed > 0 && matchedRepoSyncAsset > 0,
		"operation_binding_observed":         replayed > 0 && operationRuns > 0,
		"external_call_made_by_assops":       false,
		"provider_api_called":                false,
		"provider_test_delivery_sent":        false,
		"raw_request_headers_recorded":       false,
		"raw_request_body_recorded":          false,
		"raw_provider_response_recorded":     false,
		"source_delivery_id_recorded":        false,
		"replay_source_delivery_id_recorded": false,
		"contains_token":                     false,
		"contains_secret":                    false,
		"contains_payload":                   false,
		"contains_provider_url":              false,
		"suppressed_fields":                  []string{"delivery_id", "source_delivery_id", "replay_source_delivery_id", "secret_token", "shared_secret", "signature_header", "authorization_header", "provider_token", "provider_url", "request_headers", "request_body", "payload", "delivery_payload", "result", "error_message", "provider_response_body", "provider_response_headers"},
	}
}

func webhookProviderCallbackRehearsalPlan(readinessStatus string, readinessReasons []string, evidence map[string]any) map[string]any {
	planState := "blocked"
	if readinessStatus == "ready" {
		planState = "planned"
	}
	blockedReasons := append([]string{}, readinessReasons...)
	executionBlockers := []string{"provider_callback_rehearsal_not_performed"}
	if planState == "planned" {
		blockedReasons = []string{}
	}
	deliveryObserved := boolOnlyFromAny(evidence["provider_delivery_observed"])
	resultWritten := boolOnlyFromAny(evidence["sanitized_result_recorded"])
	replayProof := mapFromAny(evidence["operator_replay_proof"])
	replayProofState := cleanPreviewString(replayProof["proof_state"])
	return map[string]any{
		"mode":                           "provider_callback_rehearsal_plan",
		"plan_state":                     planState,
		"execution_enabled":              false,
		"external_call_made":             false,
		"provider_settings_written":      false,
		"provider_test_delivery_sent":    false,
		"provider_delivery_received":     deliveryObserved,
		"webhook_event_created":          deliveryObserved,
		"webhook_event_replayed":         boolOnlyFromAny(evidence["webhook_event_replay_observed"]),
		"operator_replay_proof_recorded": replayProofState == "recorded",
		"repo_sync_enqueued":             boolOnlyFromAny(evidence["repo_sync_enqueue_observed"]),
		"github_actions_refresh_started": boolOnlyFromAny(evidence["github_actions_refresh_observed"]),
		"result_written":                 resultWritten,
		"contains_token":                 false,
		"contains_secret":                false,
		"contains_payload":               false,
		"contains_provider_url":          false,
		"contains_delivery_body":         false,
		"required_controls": []string{
			"public_gateway_origin",
			"provider_webhook_settings_review",
			"webhook_secret_rotation_review",
			"delivery_id_deduplication",
			"provider_test_delivery",
			"sanitized_result_recording",
		},
		"callback_execution_sequence": []string{
			"verify_public_staging_origin",
			"review_provider_webhook_settings",
			"send_provider_test_delivery",
			"observe_sanitized_callback_event",
			"replay_event_to_repo_sync",
			"refresh_provider_actions_state",
			"record_redacted_rehearsal_result",
			"review_provider_pair_thresholds",
		},
		"disabled_backends": []string{
			"provider_webhook_settings_write",
			"provider_test_delivery",
			"external_callback_wait",
			"webhook_event_insert",
			"webhook_event_replay",
			"repo_sync_enqueue",
			"github_actions_api_sync",
		},
		"suppressed_fields": []string{
			"secret_token",
			"shared_secret",
			"signature_header",
			"provider_token",
			"provider_url",
			"request_headers",
			"request_body",
			"delivery_payload",
			"delivery_response",
		},
		"blocked_reasons":        blockedReasons,
		"execution_blockers":     executionBlockers,
		"public_endpoint_plan":   webhookProviderCallbackPublicEndpointPlan(planState, readinessReasons),
		"provider_delivery_plan": webhookProviderCallbackDeliveryPlan(planState),
		"operator_replay_proof":  replayProof,
		"threshold_tuning_plan":  webhookProviderCallbackThresholdTuningPlan(planState, evidence),
		"result_recording_plan":  webhookProviderCallbackRehearsalResultRecordingPlan(evidence),
		"message":                "Provider callback rehearsal is audit-only; no provider settings write or provider test delivery is performed, while existing webhook event evidence is reconciled as sanitized metadata.",
	}
}

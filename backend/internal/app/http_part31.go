package app

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

func webhookProviderCallbackProviderMetricsComparisonPlan(volumeEvidence map[string]any) map[string]any {
	reviewState := cleanPreviewString(volumeEvidence["threshold_review_state"])
	if reviewState == "" {
		reviewState = "waiting_for_volume"
	}
	volumeObserved := boolOnlyFromAny(volumeEvidence["local_volume_observed"])
	reviewReady := boolOnlyFromAny(volumeEvidence["threshold_review_ready"])
	comparisonState := "waiting_for_volume"
	switch {
	case reviewState == "review_failed_volume":
		comparisonState = "needs_failure_review"
	case reviewReady:
		comparisonState = "ready_for_operator_review"
	case volumeObserved:
		comparisonState = "local_volume_observed"
	}
	blockedReasons := []string{"provider_metrics_fetch_disabled", "provider_pair_limits_compare_disabled"}
	if !volumeObserved {
		blockedReasons = append(blockedReasons, "real_provider_volume_not_observed")
	}
	if reviewState == "review_failed_volume" {
		blockedReasons = append(blockedReasons, "webhook_failures_need_operator_threshold_review")
	} else if volumeObserved && !reviewReady {
		blockedReasons = append(blockedReasons, "processed_or_repo_sync_volume_not_observed")
	}
	return map[string]any{
		"mode":                               "provider_callback_provider_metrics_comparison_plan",
		"comparison_state":                   comparisonState,
		"threshold_review_state":             reviewState,
		"comparison_ready_for_review":        reviewReady,
		"local_volume_observed":              volumeObserved,
		"provider_volume_observed":           false,
		"provider_metrics_fetched":           false,
		"provider_pair_limits_compared":      false,
		"external_call_made":                 false,
		"contains_token":                     false,
		"contains_secret":                    false,
		"contains_payload":                   false,
		"contains_provider_url":              false,
		"delivery_count_7d":                  intFromAny(volumeEvidence["delivery_count_7d"], 0),
		"processed_count_7d":                 intFromAny(volumeEvidence["processed_count_7d"], 0),
		"failed_count_7d":                    intFromAny(volumeEvidence["failed_count_7d"], 0),
		"operation_run_count_7d":             intFromAny(volumeEvidence["operation_run_count_7d"], 0),
		"matched_repo_sync_asset_count_7d":   intFromAny(volumeEvidence["matched_repo_sync_asset_count_7d"], 0),
		"repo_sync_volume_observed":          boolOnlyFromAny(volumeEvidence["repo_sync_volume_observed"]),
		"processed_or_bound_volume_observed": boolOnlyFromAny(volumeEvidence["processed_or_bound_volume_observed"]),
		"current_thresholds": []map[string]any{
			{"key": "webhook_delivery_failure_7d", "warning_at": repoSyncCapacityWebhookWarningThreshold, "danger_at": repoSyncCapacityWebhookDangerThreshold, "unit": "failed_events"},
			{"key": "provider_pair_active_24h", "warning_at": repoSyncCapacityPairActiveWarningThreshold, "danger_at": repoSyncCapacityPairActiveDangerThreshold, "unit": "active_runs"},
			{"key": "provider_pair_failure_24h", "warning_at": repoSyncCapacityPairFailureWarningThreshold, "danger_at": repoSyncCapacityPairFailureDangerThreshold, "unit": "failures"},
			{"key": "github_actions_volume_24h", "warning_at": repoSyncCapacityGitHubVolumeWarningThreshold, "danger_at": repoSyncCapacityGitHubVolumeDangerThreshold, "unit": "runs"},
		},
		"required_provider_metrics": []string{
			"provider_delivery_attempts",
			"provider_delivery_failures",
			"provider_rate_limit_remaining",
			"provider_actions_run_volume",
		},
		"comparison_sequence": []string{
			"collect_local_webhook_volume",
			"fetch_provider_delivery_metrics",
			"compare_provider_pair_limits",
			"review_threshold_delta",
			"record_operator_threshold_decision",
		},
		"disabled_backends": []string{
			"provider_metrics_fetch",
			"provider_pair_limits_compare",
			"threshold_delta_persist",
			"operator_review_audit_insert",
		},
		"suppressed_fields": []string{
			"provider_token",
			"provider_url",
			"authorization_header",
			"request_headers",
			"provider_response_body",
			"provider_response_headers",
			"delivery_id",
			"payload",
		},
		"blocked_reasons": blockedReasons,
		"message":         "Provider metrics comparison is review-only; ASSOPS uses local webhook counters here and does not fetch provider metrics, compare live provider limits, or persist threshold deltas.",
	}
}

func webhookProviderCallbackThresholdVolumeEvidence(evidence map[string]any) map[string]any {
	if evidence == nil {
		evidence = map[string]any{}
	}
	deliveries := intFromAny(evidence["delivery_count_7d"], 0)
	failures := intFromAny(evidence["failed_count_7d"], 0)
	processed := intFromAny(evidence["processed_count_7d"], 0)
	replayed := intFromAny(evidence["replayed_count_7d"], 0)
	operationRuns := intFromAny(evidence["operation_run_count_7d"], 0)
	matchedRepoSyncAsset := intFromAny(evidence["matched_repo_sync_asset_count_7d"], 0)
	localVolumeObserved := deliveries > 0 || replayed > 0 || operationRuns > 0 || matchedRepoSyncAsset > 0
	processedOrBoundVolume := operationRuns > 0 || matchedRepoSyncAsset > 0 || processed > 0
	reviewState := "waiting_for_volume"
	var blockedReasons []string
	switch {
	case !localVolumeObserved:
		reviewState = "waiting_for_volume"
		blockedReasons = []string{"real_provider_volume_not_observed"}
	case failures > 0:
		reviewState = "review_failed_volume"
		blockedReasons = []string{"webhook_failures_need_operator_threshold_review"}
	case processedOrBoundVolume:
		reviewState = "ready_for_review"
		blockedReasons = []string{"operator_threshold_review_not_recorded"}
	default:
		reviewState = "volume_observed"
		blockedReasons = []string{"processed_or_repo_sync_volume_not_observed"}
	}
	return map[string]any{
		"mode":                               "provider_callback_threshold_volume_evidence",
		"threshold_review_state":             reviewState,
		"threshold_review_ready":             localVolumeObserved && failures == 0 && processedOrBoundVolume,
		"local_volume_observed":              localVolumeObserved,
		"provider_volume_observed":           false,
		"provider_metrics_fetched":           false,
		"provider_pair_limits_compared":      false,
		"threshold_configuration_written":    false,
		"delivery_count_7d":                  deliveries,
		"processed_count_7d":                 processed,
		"failed_count_7d":                    failures,
		"replayed_count_7d":                  replayed,
		"operation_run_count_7d":             operationRuns,
		"matched_repo_sync_asset_count_7d":   matchedRepoSyncAsset,
		"repo_sync_volume_observed":          operationRuns > 0 || matchedRepoSyncAsset > 0,
		"processed_or_bound_volume_observed": processedOrBoundVolume,
		"webhook_failure_volume_observed":    failures > 0,
		"external_call_made":                 false,
		"contains_token":                     false,
		"contains_secret":                    false,
		"contains_payload":                   false,
		"contains_provider_url":              false,
		"suppressed_fields":                  []string{"delivery_id", "source_delivery_id", "provider_token", "provider_url", "request_headers", "request_body", "payload", "provider_response_body", "provider_response_headers", "repo_url", "branch_name"},
		"blocked_reasons":                    blockedReasons,
	}
}

func webhookProviderCallbackRehearsalResultRecordingPlan(evidence map[string]any) map[string]any {
	evidenceState := strings.TrimSpace(fmt.Sprint(evidence["evidence_state"]))
	evidenceObserved := boolOnlyFromAny(evidence["webhook_event_recorded"])
	resultReady := boolOnlyFromAny(evidence["sanitized_result_recorded"])
	replayProof := mapFromAny(evidence["operator_replay_proof"])
	replayProofState := cleanPreviewString(replayProof["proof_state"])
	if replayProofState == "" {
		replayProofState = "waiting_for_operator_replay"
	}
	recordingState := "blocked"
	recordingReason := "provider_callback_rehearsal_execution_not_performed"
	switch evidenceState {
	case "recorded":
		recordingState = "recorded"
		recordingReason = "sanitized_provider_callback_result_recorded"
	case "observed":
		recordingState = "observed"
		recordingReason = "provider_callback_delivery_observed_without_terminal_result"
	case "failed":
		recordingState = "failed"
		recordingReason = "provider_callback_delivery_failed"
	case "ignored":
		recordingState = "ignored"
		recordingReason = "provider_callback_delivery_ignored"
	}
	return map[string]any{
		"mode":                             "provider_callback_rehearsal_result_recording_plan",
		"result_recording_state":           recordingState,
		"result_recording_ready":           resultReady,
		"result_recording_ready_reason":    recordingReason,
		"recording_enabled":                resultReady,
		"result_written":                   resultReady,
		"webhook_connection_updated":       false,
		"webhook_event_recorded":           evidenceObserved,
		"operator_replay_proof_state":      replayProofState,
		"operator_replay_proof_recorded":   replayProofState == "recorded",
		"operation_log_written":            false,
		"repo_sync_result_recorded":        boolOnlyFromAny(evidence["repo_sync_enqueue_observed"]),
		"github_actions_result_recorded":   boolOnlyFromAny(evidence["github_actions_refresh_observed"]),
		"threshold_tuning_result_recorded": false,
		"raw_request_headers_recorded":     false,
		"raw_request_body_recorded":        false,
		"raw_provider_response_recorded":   false,
		"contains_token":                   false,
		"contains_secret":                  false,
		"contains_payload":                 false,
		"contains_provider_url":            false,
		"result_recording_sequence": []string{
			"classify_provider_delivery",
			"record_sanitized_delivery_summary",
			"record_webhook_event_status",
			"record_repo_sync_rehearsal_summary",
			"record_github_actions_refresh_summary",
			"persist_redacted_rehearsal_result",
		},
		"result_diagnostic_fields": []string{
			"provider",
			"public_origin_valid",
			"delivery_status",
			"signature_valid",
			"event_type",
			"repo_sync_enqueued",
			"github_actions_refresh_status",
			"provider_pair_threshold_state",
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
			"provider_response_body",
			"provider_response_headers",
		},
		"blocked_reasons": []string{
			"provider_callback_rehearsal_execution_not_performed",
			"webhook_event_result_update_not_wired",
			"repo_sync_rehearsal_result_not_recorded",
			"github_actions_refresh_not_performed",
		},
		"message": "Provider callback rehearsal result recording is planned only; raw request, provider response, secret, and payload material are never persisted.",
	}
}

func isPlausiblePublicWebhookOrigin(baseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), "[]")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return isPublicIP(ip)
	}
	if !strings.Contains(host, ".") {
		return false
	}
	for _, suffix := range []string{".local", ".internal", ".cluster.local", ".svc", ".svc.cluster.local"} {
		if strings.HasSuffix(host, suffix) {
			return false
		}
	}
	return true
}

func hasNonZeroValue(value any) bool {
	text := strings.TrimSpace(fmt.Sprint(value))
	return text != "" && text != "<nil>" && text != "0"
}

package app

import (
	"strings"
)

func webhookProviderCallbackPublicEndpointPlan(planState string, readinessReasons []string) map[string]any {
	publicOriginReady := true
	blockedReasons := make([]string, 0)
	for _, reason := range readinessReasons {
		if strings.Contains(reason, "ASSOPS_GATEWAY_URL") || strings.Contains(reason, "public HTTP(S) origin") {
			publicOriginReady = false
			blockedReasons = append(blockedReasons, reason)
		}
	}
	endpointState := "planned"
	if planState != "planned" || !publicOriginReady {
		endpointState = "blocked"
	}
	return map[string]any{
		"mode":                    "provider_callback_public_endpoint_plan",
		"endpoint_state":          endpointState,
		"public_origin_ready":     publicOriginReady,
		"public_staging_required": true,
		"dns_probe_performed":     false,
		"tls_probe_performed":     false,
		"provider_ping_performed": false,
		"external_call_made":      false,
		"contains_provider_url":   false,
		"contains_token":          false,
		"required_controls":       []string{"public_https_origin", "dns_review", "tls_review", "provider_callback_path_review"},
		"disabled_backends":       []string{"dns_probe", "tls_probe", "provider_callback_ping"},
		"suppressed_fields":       []string{"provider_url", "request_headers", "provider_token", "shared_secret"},
		"blocked_reasons":         blockedReasons,
		"execution_blockers":      []string{"public_staging_hostname_not_verified"},
		"message":                 "Public endpoint verification is planned only; no DNS, TLS, or provider callback probe is performed.",
	}
}

func webhookProviderCallbackDeliveryPlan(planState string) map[string]any {
	deliveryState := "blocked"
	if planState == "planned" {
		deliveryState = "planned"
	}
	return map[string]any{
		"mode":                         "provider_callback_delivery_plan",
		"delivery_state":               deliveryState,
		"provider_settings_written":    false,
		"provider_test_delivery_sent":  false,
		"provider_delivery_received":   false,
		"delivery_signature_validated": false,
		"delivery_deduplicated":        false,
		"webhook_event_created":        false,
		"external_call_made":           false,
		"contains_token":               false,
		"contains_secret":              false,
		"contains_payload":             false,
		"required_controls": []string{
			"provider_settings_operator_review",
			"webhook_secret_rotation_review",
			"test_delivery_id_capture",
			"signature_validation",
			"delivery_id_deduplication",
		},
		"disabled_backends": []string{
			"provider_webhook_settings_write",
			"provider_test_delivery",
			"external_callback_wait",
			"webhook_event_insert",
		},
		"suppressed_fields":  []string{"secret_token", "shared_secret", "signature_header", "provider_token", "request_headers", "request_body", "delivery_payload", "delivery_response"},
		"blocked_reasons":    []string{"real_provider_test_delivery_not_performed"},
		"execution_blockers": []string{"provider_callback_rehearsal_not_performed"},
		"message":            "Provider delivery rehearsal is planned only; no provider settings are written and no test delivery is sent.",
	}
}

func webhookProviderCallbackThresholdTuningPlan(planState string, evidence map[string]any) map[string]any {
	thresholdState := "blocked"
	if planState == "planned" {
		thresholdState = "planned"
	}
	volumeEvidence := webhookProviderCallbackThresholdVolumeEvidence(evidence)
	metricsComparisonPlan := webhookProviderCallbackProviderMetricsComparisonPlan(volumeEvidence)
	thresholdReviewReady := boolOnlyFromAny(volumeEvidence["threshold_review_ready"])
	executionBlockers := []string{"provider_pair_thresholds_need_live_volume_tuning"}
	switch cleanPreviewString(volumeEvidence["threshold_review_state"]) {
	case "ready_for_review":
		executionBlockers = []string{"operator_threshold_review_not_recorded"}
	case "review_failed_volume":
		executionBlockers = []string{"webhook_failures_need_operator_threshold_review"}
	case "volume_observed":
		executionBlockers = []string{"processed_or_repo_sync_volume_not_observed"}
	}
	return map[string]any{
		"mode":                              "provider_callback_threshold_tuning_plan",
		"threshold_state":                   thresholdState,
		"live_volume_observed":              boolOnlyFromAny(volumeEvidence["local_volume_observed"]),
		"threshold_review_state":            volumeEvidence["threshold_review_state"],
		"threshold_review_ready":            thresholdReviewReady,
		"provider_pair_thresholds_tuned":    false,
		"sync_capacity_thresholds_tuned":    false,
		"webhook_delivery_thresholds_tuned": false,
		"github_actions_thresholds_tuned":   false,
		"threshold_configuration_written":   false,
		"external_call_made":                false,
		"volume_evidence":                   volumeEvidence,
		"provider_metrics_comparison_plan":  metricsComparisonPlan,
		"threshold_configuration_plan":      webhookProviderCallbackThresholdConfigurationPlan(volumeEvidence, metricsComparisonPlan),
		"required_observations":             []string{"provider_pair_active_runs", "provider_pair_recent_failures", "webhook_delivery_failures", "github_actions_run_volume"},
		"threshold_review_sequence":         []string{"collect_live_sync_volume", "compare_provider_limits", "adjust_warning_thresholds", "adjust_danger_thresholds", "record_threshold_review"},
		"disabled_backends":                 []string{"provider_metrics_fetch", "threshold_configuration_write", "sync_capacity_backfill"},
		"suppressed_fields":                 []string{"provider_token", "provider_url", "request_headers", "provider_response_body"},
		"blocked_reasons":                   stringSliceFromAny(volumeEvidence["blocked_reasons"]),
		"execution_blockers":                executionBlockers,
		"message":                           "Provider-pair threshold tuning is planned only; local webhook volume evidence is redacted and current thresholds stay unchanged until an operator reviews real rehearsal volume.",
	}
}

func webhookProviderCallbackThresholdConfigurationPlan(volumeEvidence, metricsComparisonPlan map[string]any) map[string]any {
	reviewState := cleanPreviewString(volumeEvidence["threshold_review_state"])
	reviewReady := boolOnlyFromAny(volumeEvidence["threshold_review_ready"])
	configurationState := "blocked"
	blockedReasons := append([]string{}, stringSliceFromAny(volumeEvidence["blocked_reasons"])...)
	switch {
	case reviewState == "waiting_for_volume":
		configurationState = "waiting_for_volume"
	case reviewState == "review_failed_volume":
		configurationState = "needs_failure_review"
	case reviewReady:
		configurationState = "ready_for_operator_review"
		blockedReasons = []string{"operator_threshold_review_not_recorded", "threshold_configuration_write_disabled"}
	}
	decisionAuditPlan := webhookProviderCallbackThresholdDecisionAuditPlan(volumeEvidence, metricsComparisonPlan, configurationState)
	return map[string]any{
		"mode":                               "provider_callback_threshold_configuration_plan",
		"configuration_state":                configurationState,
		"configuration_review_ready":         reviewReady,
		"threshold_review_state":             reviewState,
		"threshold_configuration_written":    false,
		"configuration_write_enabled":        false,
		"operator_threshold_review_recorded": false,
		"threshold_decision_audit_plan":      decisionAuditPlan,
		"provider_metrics_fetched":           false,
		"provider_pair_limits_compared":      false,
		"provider_metrics_comparison_plan":   metricsComparisonPlan,
		"external_call_made":                 false,
		"contains_token":                     false,
		"contains_secret":                    false,
		"contains_payload":                   false,
		"contains_provider_url":              false,
		"current_thresholds":                 webhookProviderCallbackCurrentThresholds(),
		"required_persisted_fields": []string{
			"provider_pair",
			"threshold_key",
			"warning_at",
			"danger_at",
			"unit",
			"reviewed_by",
			"reviewed_at",
			"evidence_window",
		},
		"configuration_sequence": []string{
			"collect_live_volume_evidence",
			"compare_current_thresholds",
			"record_operator_threshold_review",
			"persist_threshold_configuration",
			"recompute_repo_sync_capacity_signals",
		},
		"disabled_backends": []string{
			"provider_metrics_fetch",
			"threshold_configuration_write",
			"threshold_configuration_audit_insert",
		},
		"capacity_signal_recompute_mode": "read_time_repo_sync_asset_detail",
		"capacity_signals_recomputed":    false,
		"suppressed_fields": []string{
			"provider_token",
			"provider_url",
			"request_headers",
			"provider_response_body",
			"operator_identity",
			"operator_notes",
		},
		"blocked_reasons": blockedReasons,
		"message":         "Threshold configuration persistence is review-only until an operator audit is recorded; after configuration write, repo sync capacity signals recompute from local rows without provider metrics.",
	}
}

func webhookProviderCallbackCurrentThresholds() []map[string]any {
	return []map[string]any{
		{"key": "sync_capacity_active", "warning_at": repoSyncCapacityActiveWarningThreshold, "danger_at": repoSyncCapacityActiveDangerThreshold, "unit": "active_runs"},
		{"key": "sync_failure_7d", "warning_at": repoSyncCapacityFailure7dWarningThreshold, "danger_at": repoSyncCapacityFailure7dDangerThreshold, "unit": "failures"},
		{"key": "webhook_delivery_failure_7d", "warning_at": repoSyncCapacityWebhookWarningThreshold, "danger_at": repoSyncCapacityWebhookDangerThreshold, "unit": "failed_events"},
		{"key": "github_actions_volume_24h", "warning_at": repoSyncCapacityGitHubVolumeWarningThreshold, "danger_at": repoSyncCapacityGitHubVolumeDangerThreshold, "unit": "runs"},
		{"key": "provider_pair_active_24h", "warning_at": repoSyncCapacityPairActiveWarningThreshold, "danger_at": repoSyncCapacityPairActiveDangerThreshold, "unit": "active_runs"},
		{"key": "provider_pair_failure_24h", "warning_at": repoSyncCapacityPairFailureWarningThreshold, "danger_at": repoSyncCapacityPairFailureDangerThreshold, "unit": "failures"},
	}
}

func webhookProviderCallbackThresholdDecisionAuditPlan(volumeEvidence, metricsComparisonPlan map[string]any, configurationState string) map[string]any {
	reviewState := cleanPreviewString(volumeEvidence["threshold_review_state"])
	if reviewState == "" {
		reviewState = "waiting_for_volume"
	}
	decisionState := "blocked"
	switch {
	case reviewState == "waiting_for_volume":
		decisionState = "waiting_for_volume"
	case reviewState == "review_failed_volume":
		decisionState = "needs_failure_review"
	case configurationState == "ready_for_operator_review" && boolOnlyFromAny(volumeEvidence["threshold_review_ready"]):
		decisionState = "metadata_review_ready"
	}
	decisionReady := decisionState == "metadata_review_ready" &&
		boolOnlyFromAny(metricsComparisonPlan["comparison_ready_for_review"])
	blockedReasons := []string{"threshold_configuration_write_disabled"}
	if !decisionReady {
		blockedReasons = append(blockedReasons, "threshold_configuration_audit_insert_disabled")
	}
	if reviewState == "waiting_for_volume" {
		blockedReasons = append(blockedReasons, "real_provider_volume_not_observed")
	}
	if reviewState == "review_failed_volume" {
		blockedReasons = append(blockedReasons, "webhook_failures_need_operator_threshold_review")
	}
	if decisionState == "blocked" {
		blockedReasons = append(blockedReasons, "threshold_review_metadata_not_ready")
	} else if !decisionReady {
		blockedReasons = append(blockedReasons, "operator_threshold_review_not_recorded")
	}
	if !boolOnlyFromAny(metricsComparisonPlan["comparison_ready_for_review"]) {
		blockedReasons = append(blockedReasons, "provider_metrics_comparison_not_review_ready")
	}
	disabledBackends := []string{"threshold_configuration_write", "threshold_delta_persist", "provider_metrics_fetch"}
	if !decisionReady {
		disabledBackends = append([]string{"threshold_configuration_audit_insert"}, disabledBackends...)
	}
	return map[string]any{
		"mode":                                   "provider_callback_threshold_decision_audit_plan",
		"decision_state":                         decisionState,
		"decision_ready_for_review":              decisionReady,
		"threshold_review_state":                 reviewState,
		"configuration_state":                    configurationState,
		"threshold_configuration_written":        false,
		"threshold_configuration_audit_inserted": false,
		"configuration_write_enabled":            false,
		"audit_insert_enabled":                   decisionReady,
		"capacity_signals_recomputed":            false,
		"provider_metrics_fetched":               false,
		"provider_pair_limits_compared":          false,
		"proposed_threshold_delta_persisted":     false,
		"operator_threshold_review_recorded":     false,
		"external_call_made":                     false,
		"contains_token":                         false,
		"contains_secret":                        false,
		"contains_payload":                       false,
		"contains_provider_url":                  false,
		"delivery_count_7d":                      intFromAny(volumeEvidence["delivery_count_7d"], 0),
		"failed_count_7d":                        intFromAny(volumeEvidence["failed_count_7d"], 0),
		"operation_run_count_7d":                 intFromAny(volumeEvidence["operation_run_count_7d"], 0),
		"matched_repo_sync_asset_count_7d":       intFromAny(volumeEvidence["matched_repo_sync_asset_count_7d"], 0),
		"required_decision_fields":               []string{"provider_pair", "threshold_key", "current_warning_at", "current_danger_at", "proposed_warning_at", "proposed_danger_at", "evidence_window", "operator_decision", "reviewed_at"},
		"required_controls":                      []string{"operator_threshold_review", "provider_metrics_comparison_review", "threshold_delta_schema_review", "audit_row_redaction_review", "capacity_signal_recompute_review"},
		"disabled_backends":                      disabledBackends,
		"suppressed_fields":                      []string{"operator_identity", "operator_notes", "provider_token", "provider_url", "authorization_header", "request_headers", "provider_response_body", "provider_response_headers", "delivery_id", "payload"},
		"blocked_reasons":                        blockedReasons,
		"message":                                "Threshold decision audit can record sanitized local callback-volume metadata when review-ready; no threshold configuration, provider metric, URL, token, payload, raw provider response, or operator note is written.",
	}
}

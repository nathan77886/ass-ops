package app

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

type WebhookProviderCallbackRehearsalSnapshotOptions struct {
	ConnectionID string
	DryRun       bool
	Connection   map[string]any
}

func RecordWebhookProviderCallbackRehearsalSnapshot(ctx context.Context, store *Store, opts WebhookProviderCallbackRehearsalSnapshotOptions) (map[string]any, error) {
	connectionID := strings.TrimSpace(opts.ConnectionID)
	if connectionID == "" {
		return nil, fmt.Errorf("webhook connection id is required")
	}
	connection := opts.Connection
	if len(connection) == 0 {
		return nil, fmt.Errorf("webhook connection readiness is required")
	}
	assetID, assetErr := webhookConnectionAssetID(ctx, store.Gorm, connectionID)
	if assetErr != nil && !strings.Contains(assetErr.Error(), "not found") {
		return nil, assetErr
	}
	readiness := mapFromAny(connection["callback_rehearsal"])
	providerPlan := mapFromAny(readiness["provider_rehearsal_plan"])
	resultPlan := mapFromAny(providerPlan["result_recording_plan"])
	snapshot := webhookProviderCallbackRehearsalSnapshotPayload(connection, readiness, assetErr == nil)
	ready, state, missing := webhookProviderCallbackRehearsalSnapshotReadiness(resultPlan, snapshot)
	projectID := strings.TrimSpace(fmt.Sprint(connection["project_id"]))
	result := map[string]any{
		"mode":                              "provider_callback_rehearsal_snapshot_recording",
		"recording_state":                   state,
		"recording_ready":                   ready,
		"recording_enabled":                 ready && !opts.DryRun,
		"dry_run":                           opts.DryRun,
		"project_id":                        projectID,
		"webhook_connection_id":             connectionID,
		"webhook_connection_asset_observed": assetErr == nil,
		"snapshot":                          snapshot,
		"result_recording_plan":             resultPlan,
		"snapshots_written":                 0,
		"snapshots_skipped_as_duplicate":    0,
		"provider_callback_rehearsal_snapshot_written": false,
		"asset_status_snapshot_written":                false,
		"operation_log_written":                        false,
		"external_call_made":                           false,
		"provider_api_called":                          false,
		"provider_settings_written":                    false,
		"provider_test_delivery_sent":                  false,
		"provider_metrics_fetched":                     false,
		"provider_pair_limits_compared":                false,
		"raw_request_headers_recorded":                 false,
		"raw_request_body_recorded":                    false,
		"raw_provider_response_recorded":               false,
		"contains_token":                               false,
		"contains_secret":                              false,
		"contains_payload":                             false,
		"contains_provider_url":                        false,
		"sanitized_result_recorded":                    boolOnlyFromAny(snapshot["sanitized_result_recorded"]),
		"webhook_event_recorded":                       boolOnlyFromAny(snapshot["webhook_event_recorded"]),
		"operator_replay_proof_recorded":               boolOnlyFromAny(snapshot["operator_replay_proof_recorded"]),
		"repo_sync_result_recorded":                    boolOnlyFromAny(snapshot["repo_sync_result_recorded"]),
		"github_actions_result_recorded":               boolOnlyFromAny(snapshot["github_actions_result_recorded"]),
		"canonical_asset_status_snapshot_attempted":    false,
		"snapshot_commit_attempted":                    false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"webhook_connection_asset_missing"}
		result["message"] = "Provider callback rehearsal snapshot is derived, but the canonical webhook_connection asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider callback rehearsal snapshot is waiting for sanitized local callback evidence; no provider call was made and no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["recording_state"] = "ready_to_record"
		result["message"] = "Dry run only; sanitized provider callback rehearsal snapshot was not written."
		return result, nil
	}
	const status = "provider_callback_rehearsal_recorded"
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, webhookProviderCallbackRehearsalSnapshotHealth(snapshot), "provider callback rehearsal snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording provider callback rehearsal snapshot: %w", err)
	}
	result["recording_state"] = "recorded"
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["provider_callback_rehearsal_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["canonical_asset_status_snapshot_attempted"] = true
	result["message"] = "Sanitized provider callback rehearsal snapshot recorded from local webhook evidence."
	return result, nil
}

func webhookConnectionAssetID(ctx context.Context, db *gorm.DB, connectionID string) (string, error) {
	if db == nil {
		return "", fmt.Errorf("gorm database is not configured")
	}
	var asset GormAsset
	if err := db.WithContext(ctx).
		Where(&GormAsset{AssetType: "webhook_connection", SourceTable: "webhook_connections", SourceID: validNullString(connectionID)}).
		First(&asset).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return "", fmt.Errorf("webhook_connection asset for %s not found; run db sync-assets first", connectionID)
		}
		return "", err
	}
	assetID := strings.TrimSpace(asset.ID)
	if assetID == "" || assetID == "<nil>" {
		return "", fmt.Errorf("webhook_connection asset for %s has empty id", connectionID)
	}
	return assetID, nil
}

func webhookProviderCallbackRehearsalSnapshotPayload(connection, readiness map[string]any, assetObserved bool) map[string]any {
	providerPlan := mapFromAny(readiness["provider_rehearsal_plan"])
	resultPlan := mapFromAny(providerPlan["result_recording_plan"])
	callbackEvidence := mapFromAny(readiness["callback_evidence"])
	replayProof := mapFromAny(callbackEvidence["operator_replay_proof"])
	thresholdPlan := mapFromAny(providerPlan["threshold_tuning_plan"])
	volumeEvidence := mapFromAny(thresholdPlan["volume_evidence"])
	metricsComparison := mapFromAny(thresholdPlan["provider_metrics_comparison_plan"])
	configurationPlan := mapFromAny(thresholdPlan["threshold_configuration_plan"])
	decisionAuditPlan := mapFromAny(configurationPlan["threshold_decision_audit_plan"])
	return map[string]any{
		"mode":                                "provider_callback_rehearsal_snapshot",
		"webhook_connection_id":               cleanPreviewString(connection["id"]),
		"provider":                            cleanPreviewString(connection["provider"]),
		"rehearsal_status":                    cleanPreviewString(readiness["status"]),
		"result_recording_state":              cleanPreviewString(resultPlan["result_recording_state"]),
		"result_recording_ready":              boolOnlyFromAny(resultPlan["result_recording_ready"]),
		"sanitized_result_recorded":           boolOnlyFromAny(resultPlan["result_written"]),
		"webhook_connection_asset_observed":   assetObserved,
		"webhook_event_recorded":              boolOnlyFromAny(resultPlan["webhook_event_recorded"]),
		"operator_replay_proof_state":         cleanPreviewString(resultPlan["operator_replay_proof_state"]),
		"operator_replay_proof_recorded":      boolOnlyFromAny(resultPlan["operator_replay_proof_recorded"]),
		"repo_sync_result_recorded":           boolOnlyFromAny(resultPlan["repo_sync_result_recorded"]),
		"github_actions_result_recorded":      boolOnlyFromAny(resultPlan["github_actions_result_recorded"]),
		"threshold_tuning_result_recorded":    boolOnlyFromAny(resultPlan["threshold_tuning_result_recorded"]),
		"evidence_state":                      cleanPreviewString(callbackEvidence["evidence_state"]),
		"delivery_count_7d":                   intFromAny(callbackEvidence["delivery_count_7d"], 0),
		"processed_count_7d":                  intFromAny(callbackEvidence["processed_count_7d"], 0),
		"failed_count_7d":                     intFromAny(callbackEvidence["failed_count_7d"], 0),
		"ignored_count_7d":                    intFromAny(callbackEvidence["ignored_count_7d"], 0),
		"replayed_count_7d":                   intFromAny(callbackEvidence["replayed_count_7d"], 0),
		"signature_valid_count_7d":            intFromAny(callbackEvidence["signature_valid_count_7d"], 0),
		"matched_repo_sync_asset_count_7d":    intFromAny(callbackEvidence["matched_repo_sync_asset_count_7d"], 0),
		"operation_run_count_7d":              intFromAny(callbackEvidence["operation_run_count_7d"], 0),
		"last_event_status":                   cleanPreviewString(callbackEvidence["last_event_status"]),
		"last_event_type":                     cleanPreviewString(callbackEvidence["last_event_type"]),
		"last_event_signature_valid":          boolOnlyFromAny(callbackEvidence["last_event_signature_valid"]),
		"operator_replay_observed":            boolOnlyFromAny(replayProof["operator_replay_observed"]),
		"threshold_review_state":              cleanPreviewString(volumeEvidence["threshold_review_state"]),
		"threshold_review_ready":              boolOnlyFromAny(volumeEvidence["threshold_review_ready"]),
		"provider_metrics_comparison_state":   cleanPreviewString(metricsComparison["comparison_state"]),
		"provider_metrics_comparison_ready":   boolOnlyFromAny(metricsComparison["comparison_ready_for_review"]),
		"threshold_configuration_state":       cleanPreviewString(configurationPlan["configuration_state"]),
		"threshold_decision_state":            cleanPreviewString(decisionAuditPlan["decision_state"]),
		"threshold_decision_ready_for_review": boolOnlyFromAny(decisionAuditPlan["decision_ready_for_review"]),
		"threshold_configuration_written":     boolOnlyFromAny(configurationPlan["threshold_configuration_written"]),
		"capacity_signals_recomputed":         boolOnlyFromAny(configurationPlan["capacity_signals_recomputed"]),
		"external_call_made":                  false,
		"provider_api_called":                 false,
		"provider_settings_written":           false,
		"provider_test_delivery_sent":         false,
		"provider_metrics_fetched":            false,
		"provider_pair_limits_compared":       false,
		"operation_log_written":               false,
		"webhook_connection_updated":          false,
		"raw_request_headers_recorded":        false,
		"raw_request_body_recorded":           false,
		"raw_provider_response_recorded":      false,
		"contains_token":                      false,
		"contains_secret":                     false,
		"contains_payload":                    false,
		"contains_provider_url":               false,
		"suppressed_fields":                   resultPlan["suppressed_fields"],
	}
}

func webhookProviderCallbackRehearsalSnapshotReadiness(resultPlan, snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	if !boolOnlyFromAny(snapshot["webhook_connection_asset_observed"]) {
		missing = append(missing, "webhook_connection_asset_missing")
	}
	if !boolOnlyFromAny(resultPlan["result_recording_ready"]) {
		missing = append(missing, "sanitized_callback_result_not_ready")
	}
	if !boolOnlyFromAny(resultPlan["webhook_event_recorded"]) {
		missing = append(missing, "webhook_event_not_observed")
	}
	if len(missing) > 0 {
		return false, "waiting_for_evidence", missing
	}
	return true, "ready_to_record", nil
}

func webhookProviderCallbackRehearsalSnapshotHealth(snapshot map[string]any) string {
	switch cleanPreviewString(snapshot["result_recording_state"]) {
	case "failed":
		return "high"
	case "ignored":
		return "warning"
	default:
		return "normal"
	}
}

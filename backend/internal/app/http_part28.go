package app

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func webhookConnectionMap(connection GormWebhookConnection, webhookURL, webhookPath string) map[string]any {
	return map[string]any{
		"id":                   connection.ID,
		"project_id":           connection.ProjectID,
		"provider":             connection.Provider,
		"name":                 connection.Name,
		"source_remote_id":     nullableStringValue(connection.SourceRemoteID),
		"enabled":              connection.Enabled,
		"event_types":          stringSliceFromAny(connection.EventTypes.Data),
		"last_delivery_status": connection.LastDeliveryStatus,
		"last_delivery_error":  connection.LastDeliveryError,
		"metadata":             mapFromAny(connection.Metadata.Data),
		"created_at":           connection.CreatedAt,
		"updated_at":           connection.UpdatedAt,
		"webhook_path":         webhookPath,
		"webhook_url":          webhookURL,
	}
}

type webhookConnectionStats struct {
	Deliveries7d            int
	Failures7d              int
	Processed7d             int
	Ignored7d               int
	Replayed7d              int
	SignatureValid7d        int
	MatchedRepoSyncAsset7d  int
	OperationRun7d          int
	LastEventAt             time.Time
	LastEventStatus         string
	LastEventType           string
	LastEventSignatureValid bool
}

func (s *Server) webhookConnectionStatsGorm(ctx context.Context, connectionIDs []string) (map[string]webhookConnectionStats, error) {
	stats := map[string]webhookConnectionStats{}
	if len(connectionIDs) == 0 {
		return stats, nil
	}
	var events []GormWebhookEvent
	if err := s.store.Gorm.WithContext(ctx).Where(gormField("webhook_connection_id", connectionIDs)).Order(gormOrderDesc("received_at")).Find(&events).Error; err != nil {
		return nil, err
	}
	since := time.Now().Add(-7 * 24 * time.Hour)
	for _, event := range events {
		connectionID := cleanOptionalID(event.WebhookConnectionID.String)
		stat := stats[connectionID]
		if stat.LastEventAt.IsZero() || event.ReceivedAt.After(stat.LastEventAt) {
			stat.LastEventAt = event.ReceivedAt
			stat.LastEventStatus = event.Status
			stat.LastEventType = event.EventType
			stat.LastEventSignatureValid = event.SignatureValid
		}
		if !event.ReceivedAt.Before(since) {
			stat.Deliveries7d++
			if isWebhookFailureStatus(event.Status) {
				stat.Failures7d++
			}
			if event.Status == "processed" {
				stat.Processed7d++
			}
			if event.Status == "ignored" {
				stat.Ignored7d++
			}
			if strings.Contains(strings.ToLower(event.DeliveryID), ":replay:") {
				stat.Replayed7d++
			}
			if event.SignatureValid {
				stat.SignatureValid7d++
			}
			if cleanOptionalID(event.MatchedRepoSyncAssetID.String) != "" {
				stat.MatchedRepoSyncAsset7d++
			}
			if cleanOptionalID(event.OperationRunID.String) != "" {
				stat.OperationRun7d++
			}
		}
		stats[connectionID] = stat
	}
	return stats, nil
}

type webhookThresholdStats struct {
	Count  int
	LastAt time.Time
}

func (s *Server) webhookThresholdAuditStatsGorm(ctx context.Context, connectionIDs []string) (map[string]webhookThresholdStats, error) {
	stats := map[string]webhookThresholdStats{}
	if len(connectionIDs) == 0 {
		return stats, nil
	}
	var audits []GormWebhookThresholdDecisionAudit
	if err := s.store.Gorm.WithContext(ctx).Where(gormField("webhook_connection_id", connectionIDs)).Find(&audits).Error; err != nil {
		return nil, err
	}
	for _, audit := range audits {
		stat := stats[audit.WebhookConnectionID]
		stat.Count++
		if audit.CreatedAt.After(stat.LastAt) {
			stat.LastAt = audit.CreatedAt
		}
		stats[audit.WebhookConnectionID] = stat
	}
	return stats, nil
}

func (s *Server) webhookThresholdConfigurationStatsGorm(ctx context.Context, connectionIDs []string) (map[string]webhookThresholdStats, error) {
	stats := map[string]webhookThresholdStats{}
	if len(connectionIDs) == 0 {
		return stats, nil
	}
	var configs []GormWebhookThresholdConfiguration
	if err := s.store.Gorm.WithContext(ctx).Where(gormField("webhook_connection_id", connectionIDs)).Find(&configs).Error; err != nil {
		return nil, err
	}
	for _, config := range configs {
		stat := stats[config.WebhookConnectionID]
		stat.Count++
		if config.AppliedAt.After(stat.LastAt) {
			stat.LastAt = config.AppliedAt
		}
		stats[config.WebhookConnectionID] = stat
	}
	return stats, nil
}

func annotateWebhookConnectionHealth(items []map[string]any) {
	for _, item := range items {
		health, summary := webhookConnectionHealth(item)
		item["webhook_health"] = health
		item["webhook_summary"] = summary
	}
}

func webhookConnectionHealth(row map[string]any) (string, string) {
	if enabled, ok := row["enabled"].(bool); ok && !enabled {
		return "warning", "disabled"
	}
	failures := intFromAny(row["failures_7d"], 0)
	if failures >= 3 {
		return "danger", fmt.Sprintf("%d failed or rejected deliveries in 7d", failures)
	}
	lastStatus := strings.TrimSpace(fmt.Sprint(row["last_delivery_status"]))
	switch lastStatus {
	case "failed", "rejected":
		return "danger", "last delivery " + lastStatus
	}
	if failures > 0 {
		return "warning", fmt.Sprintf("%d failed or rejected deliveries in 7d", failures)
	}
	deliveries := intFromAny(row["deliveries_7d"], 0)
	if deliveries == 0 {
		return "unknown", "no deliveries in 7d"
	}
	return "ok", fmt.Sprintf("%d deliveries in 7d", deliveries)
}

func annotateWebhookCallbackReadiness(items []map[string]any, baseURL string) {
	for _, item := range items {
		item["callback_rehearsal"] = webhookCallbackRehearsalReadiness(item, baseURL)
	}
}

func annotateWebhookThresholdDecisionAuditEvidence(items []map[string]any) {
	for _, item := range items {
		count := intFromAny(item["threshold_decision_audit_count"], 0)
		if count <= 0 {
			continue
		}
		readiness := mapFromAny(item["callback_rehearsal"])
		providerPlan := mapFromAny(readiness["provider_rehearsal_plan"])
		thresholdPlan := mapFromAny(providerPlan["threshold_tuning_plan"])
		configurationPlan := mapFromAny(thresholdPlan["threshold_configuration_plan"])
		decisionAuditPlan := mapFromAny(configurationPlan["threshold_decision_audit_plan"])
		decisionAuditPlan["threshold_configuration_audit_inserted"] = true
		decisionAuditPlan["operator_threshold_review_recorded"] = true
		decisionAuditPlan["threshold_decision_audit_count"] = count
		decisionAuditPlan["last_threshold_decision_audit_at"] = item["last_threshold_decision_audit_at"]
		configurationPlan["operator_threshold_review_recorded"] = true
		configurationPlan["threshold_decision_audit_plan"] = decisionAuditPlan
		thresholdPlan["threshold_configuration_plan"] = configurationPlan
		providerPlan["threshold_tuning_plan"] = thresholdPlan
		readiness["provider_rehearsal_plan"] = providerPlan
		item["callback_rehearsal"] = readiness
	}
}

func annotateWebhookThresholdConfigurationEvidence(items []map[string]any) {
	for _, item := range items {
		count := intFromAny(item["threshold_configuration_count"], 0)
		readiness := mapFromAny(item["callback_rehearsal"])
		providerPlan := mapFromAny(readiness["provider_rehearsal_plan"])
		thresholdPlan := mapFromAny(providerPlan["threshold_tuning_plan"])
		configurationPlan := mapFromAny(thresholdPlan["threshold_configuration_plan"])
		decisionAuditPlan := mapFromAny(configurationPlan["threshold_decision_audit_plan"])
		auditRecorded := boolOnlyFromAny(decisionAuditPlan["operator_threshold_review_recorded"]) ||
			intFromAny(decisionAuditPlan["threshold_decision_audit_count"], 0) > 0
		if auditRecorded {
			configurationPlan["configuration_write_enabled"] = true
			configurationPlan["blocked_reasons"] = removeStringFromSlice(stringSliceFromAny(configurationPlan["blocked_reasons"]), "operator_threshold_review_not_recorded")
			decisionAuditPlan["configuration_write_enabled"] = true
			decisionAuditPlan["blocked_reasons"] = removeStringFromSlice(stringSliceFromAny(decisionAuditPlan["blocked_reasons"]), "operator_threshold_review_not_recorded")
		}
		if count > 0 {
			recomputeEvidence := webhookThresholdCapacityRecomputeEvidence(item, count)
			configurationPlan["threshold_configuration_written"] = true
			configurationPlan["configuration_state"] = "recorded"
			configurationPlan["threshold_configuration_count"] = count
			configurationPlan["last_threshold_configuration_at"] = item["last_threshold_configuration_at"]
			configurationPlan["capacity_signals_recomputed"] = true
			configurationPlan["capacity_signal_recompute_mode"] = recomputeEvidence["recompute_mode"]
			configurationPlan["capacity_signal_recompute_evidence"] = recomputeEvidence
			configurationPlan["provider_metrics_fetched"] = false
			configurationPlan["external_call_made"] = false
			decisionAuditPlan["threshold_configuration_written"] = true
			decisionAuditPlan["threshold_configuration_count"] = count
			decisionAuditPlan["last_threshold_configuration_at"] = item["last_threshold_configuration_at"]
			decisionAuditPlan["capacity_signals_recomputed"] = true
			decisionAuditPlan["capacity_signal_recompute_mode"] = recomputeEvidence["recompute_mode"]
			decisionAuditPlan["capacity_signal_recompute_evidence"] = recomputeEvidence
			thresholdPlan["threshold_configuration_written"] = true
			thresholdPlan["provider_pair_thresholds_tuned"] = true
			thresholdPlan["capacity_signals_recomputed"] = true
			thresholdPlan["capacity_signal_recompute_mode"] = recomputeEvidence["recompute_mode"]
		}
		configurationPlan["threshold_decision_audit_plan"] = decisionAuditPlan
		thresholdPlan["threshold_configuration_plan"] = configurationPlan
		providerPlan["threshold_tuning_plan"] = thresholdPlan
		readiness["provider_rehearsal_plan"] = providerPlan
		item["callback_rehearsal"] = readiness
	}
}

func removeStringFromSlice(values []string, remove string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == remove {
			continue
		}
		result = append(result, value)
	}
	return result
}

func webhookThresholdCapacityRecomputeEvidence(item map[string]any, configurationCount int) map[string]any {
	return map[string]any{
		"mode":                             "webhook_threshold_capacity_signal_recompute_evidence",
		"capacity_signals_recomputed":      configurationCount > 0,
		"recompute_mode":                   "read_time_repo_sync_asset_detail",
		"threshold_configuration_count":    configurationCount,
		"threshold_source":                 "webhook_threshold_configuration",
		"source_remote_id":                 cleanOptionalID(fmt.Sprint(item["source_remote_id"])),
		"delivery_count_7d":                intFromAny(item["deliveries_7d"], 0),
		"failed_count_7d":                  intFromAny(item["failures_7d"], 0),
		"processed_count_7d":               intFromAny(item["processed_7d"], 0),
		"operation_run_count_7d":           intFromAny(item["operation_run_7d"], 0),
		"matched_repo_sync_asset_count_7d": intFromAny(item["matched_repo_sync_asset_7d"], 0),
		"provider_metrics_fetched":         false,
		"provider_pair_limits_compared":    false,
		"external_call_made":               false,
		"raw_provider_response_recorded":   false,
		"raw_request_or_payload_recorded":  false,
		"contains_token":                   false,
		"contains_secret":                  false,
		"contains_payload":                 false,
		"contains_provider_url":            false,
		"suppressed_fields":                []string{"provider_token", "provider_url", "authorization_header", "request_headers", "provider_response_body", "provider_response_headers", "delivery_id", "payload"},
		"message":                          "Repo sync capacity signals are recomputed on read from local webhook threshold configuration rows and local counters only.",
	}
}

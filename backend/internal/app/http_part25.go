package app

import (
	"context"
	"fmt"
	"strings"
)

func repoSyncCapacitySignalsWithThresholds(asset, raw map[string]any, sourceID, targetID string, thresholds map[string]webhookThresholdConfiguration) []map[string]any {
	signals := []map[string]any{
		{
			"name":     "source provider",
			"status":   signalStatusFromSync(raw["source_last_sync_status"]),
			"severity": signalSeverityFromSync(raw["source_last_sync_status"]),
			"detail":   fmt.Sprintf("%s remote %s", firstNonEmptyString(strings.TrimSpace(fmt.Sprint(raw["source_provider"])), "unknown"), sourceID),
		},
		{
			"name":     "target provider",
			"status":   signalStatusFromSync(raw["target_last_sync_status"]),
			"severity": signalSeverityFromSync(raw["target_last_sync_status"]),
			"detail":   fmt.Sprintf("%s remote %s", firstNonEmptyString(strings.TrimSpace(fmt.Sprint(raw["target_provider"])), "unknown"), targetID),
		},
	}
	activeRuns := intFromAny(raw["active_runs"], 0)
	activeThreshold := thresholdForKey(thresholds, "sync_capacity_active", repoSyncCapacityActiveWarningThreshold, repoSyncCapacityActiveDangerThreshold, "active_runs")
	signals = append(signals, map[string]any{
		"name":                            "sync capacity",
		"status":                          activeRuns,
		"severity":                        severityForCount(activeRuns, activeThreshold.WarningAt, activeThreshold.DangerAt),
		"threshold":                       thresholdDetail(activeThreshold.WarningAt, activeThreshold.DangerAt, humanThresholdUnit(activeThreshold.Unit)),
		"threshold_key":                   "sync_capacity_active",
		"threshold_source":                activeThreshold.Source,
		"threshold_configuration_applied": activeThreshold.Applied,
		"capacity_signals_recomputed":     activeThreshold.Applied,
		"detail":                          fmt.Sprintf("%d queued or running sync runs", activeRuns),
	})
	failedRuns := intFromAny(raw["failed_runs_7d"], 0)
	failureThreshold := thresholdForKey(thresholds, "sync_failure_7d", repoSyncCapacityFailure7dWarningThreshold, repoSyncCapacityFailure7dDangerThreshold, "failures")
	signals = append(signals, map[string]any{
		"name":                            "7d sync failures",
		"status":                          failedRuns,
		"severity":                        severityForCount(failedRuns, failureThreshold.WarningAt, failureThreshold.DangerAt),
		"threshold":                       thresholdDetail(failureThreshold.WarningAt, failureThreshold.DangerAt, humanThresholdUnit(failureThreshold.Unit)),
		"threshold_key":                   "sync_failure_7d",
		"threshold_source":                failureThreshold.Source,
		"threshold_configuration_applied": failureThreshold.Applied,
		"capacity_signals_recomputed":     failureThreshold.Applied,
		"detail":                          fmt.Sprintf("%d failed sync runs in the last 7 days", failedRuns),
	})
	webhookFailures := intFromAny(raw["webhook_failures_7d"], 0)
	lastWebhookError := strings.TrimSpace(fmt.Sprint(raw["last_webhook_error"]))
	if lastWebhookError == "<nil>" {
		lastWebhookError = ""
	}
	detail := fmt.Sprintf("%d failed or rejected webhook events in the last 7 days", webhookFailures)
	if lastWebhookError != "" {
		detail = detail + ": " + truncateText(lastWebhookError, 160)
	}
	webhookThreshold := thresholdForKey(thresholds, "webhook_delivery_failure_7d", repoSyncCapacityWebhookWarningThreshold, repoSyncCapacityWebhookDangerThreshold, "failed_events")
	signals = append(signals, map[string]any{
		"name":                            "webhook delivery",
		"status":                          webhookFailures,
		"severity":                        severityForCount(webhookFailures, webhookThreshold.WarningAt, webhookThreshold.DangerAt),
		"threshold":                       thresholdDetail(webhookThreshold.WarningAt, webhookThreshold.DangerAt, humanThresholdUnit(webhookThreshold.Unit)),
		"threshold_key":                   "webhook_delivery_failure_7d",
		"threshold_source":                webhookThreshold.Source,
		"threshold_configuration_applied": webhookThreshold.Applied,
		"capacity_signals_recomputed":     webhookThreshold.Applied,
		"detail":                          detail,
	})
	githubRuns := intFromAny(raw["github_runs_24h"], 0)
	githubThreshold := thresholdForKey(thresholds, "github_actions_volume_24h", repoSyncCapacityGitHubVolumeWarningThreshold, repoSyncCapacityGitHubVolumeDangerThreshold, "runs")
	signals = append(signals, map[string]any{
		"name":                            "GitHub Actions volume",
		"status":                          githubRuns,
		"severity":                        severityForCount(githubRuns, githubThreshold.WarningAt, githubThreshold.DangerAt),
		"threshold":                       thresholdDetail(githubThreshold.WarningAt, githubThreshold.DangerAt, humanThresholdUnit(githubThreshold.Unit)),
		"threshold_key":                   "github_actions_volume_24h",
		"threshold_source":                githubThreshold.Source,
		"threshold_configuration_applied": githubThreshold.Applied,
		"capacity_signals_recomputed":     githubThreshold.Applied,
		"detail":                          fmt.Sprintf("%d action runs observed on source/target remotes in the last 24 hours", githubRuns),
	})
	pairActive := intFromAny(raw["provider_pair_active_runs"], 0)
	pairRuns24h := intFromAny(raw["provider_pair_runs_24h"], 0)
	pairFailures24h := intFromAny(raw["provider_pair_failed_runs_24h"], 0)
	pairActiveThreshold := thresholdForKey(thresholds, "provider_pair_active_24h", repoSyncCapacityPairActiveWarningThreshold, repoSyncCapacityPairActiveDangerThreshold, "active_runs")
	pairFailureThreshold := thresholdForKey(thresholds, "provider_pair_failure_24h", repoSyncCapacityPairFailureWarningThreshold, repoSyncCapacityPairFailureDangerThreshold, "failures")
	pairSeverity := severityForCount(pairActive, pairActiveThreshold.WarningAt, pairActiveThreshold.DangerAt)
	if failureSeverity := severityForCount(pairFailures24h, pairFailureThreshold.WarningAt, pairFailureThreshold.DangerAt); failureSeverity == "danger" || (failureSeverity == "warning" && pairSeverity == "ok") {
		pairSeverity = failureSeverity
	}
	pairThresholdApplied := pairActiveThreshold.Applied || pairFailureThreshold.Applied
	signals = append(signals, map[string]any{
		"name":     "provider pair pressure",
		"status":   pairActive,
		"severity": pairSeverity,
		"threshold": fmt.Sprintf(
			"active warning >= %d / danger >= %d; failures warning >= %d / danger >= %d",
			pairActiveThreshold.WarningAt,
			pairActiveThreshold.DangerAt,
			pairFailureThreshold.WarningAt,
			pairFailureThreshold.DangerAt,
		),
		"threshold_key":                   "provider_pair_active_24h,provider_pair_failure_24h",
		"threshold_source":                thresholdSourceForPair(pairActiveThreshold, pairFailureThreshold),
		"threshold_configuration_applied": pairThresholdApplied,
		"capacity_signals_recomputed":     pairThresholdApplied,
		"detail": fmt.Sprintf(
			"%d active and %d total sync runs in 24h for %s -> %s providers (%d failed)",
			pairActive,
			pairRuns24h,
			firstNonEmptyString(strings.TrimSpace(fmt.Sprint(raw["source_provider"])), "unknown"),
			firstNonEmptyString(strings.TrimSpace(fmt.Sprint(raw["target_provider"])), "unknown"),
			pairFailures24h,
		),
	})
	if enabled, ok := asset["enabled"].(bool); ok && !enabled {
		signals = append(signals, map[string]any{"name": "asset state", "status": "disabled", "severity": "warning", "detail": "disabled sync assets do not enqueue manual or webhook runs"})
	}
	if repoSyncAssetArchived(asset) {
		signals = append(signals, map[string]any{"name": "asset state", "status": "archived", "severity": "warning", "detail": "archived sync assets are hidden and cannot run until restored"})
	}
	return signals
}

type webhookThresholdConfiguration struct {
	WarningAt      int
	DangerAt       int
	Unit           string
	Source         string
	EvidenceWindow string
	Applied        bool
}

func (s *Server) queryWebhookThresholdConfigurationOverridesGorm(ctx context.Context, sourceRemoteID, evidenceWindow string) (map[string]webhookThresholdConfiguration, error) {
	sourceRemoteID = strings.TrimSpace(sourceRemoteID)
	if sourceRemoteID == "" || sourceRemoteID == "<nil>" {
		return nil, nil
	}
	evidenceWindow = strings.TrimSpace(evidenceWindow)
	if evidenceWindow == "" {
		evidenceWindow = "7d"
	}
	var connections []GormWebhookConnection
	if err := s.store.Gorm.WithContext(ctx).Where(gormField("source_remote_id", sourceRemoteID)).Find(&connections).Error; err != nil {
		return nil, err
	}
	connectionIDs := make([]string, 0, len(connections))
	for _, connection := range connections {
		connectionIDs = append(connectionIDs, connection.ID)
	}
	if len(connectionIDs) == 0 {
		return map[string]webhookThresholdConfiguration{}, nil
	}
	var configs []GormWebhookThresholdConfiguration
	if err := s.store.Gorm.WithContext(ctx).
		Where("webhook_connection_id IN ?", connectionIDs).
		Where("evidence_window = ?", evidenceWindow).
		Order("threshold_key ASC").
		Order("applied_at DESC").
		Find(&configs).Error; err != nil {
		return nil, err
	}
	thresholds := make(map[string]webhookThresholdConfiguration, len(configs))
	for _, config := range configs {
		key := cleanPreviewString(config.ThresholdKey)
		if key == "" {
			continue
		}
		if _, exists := thresholds[key]; exists {
			continue
		}
		thresholds[key] = webhookThresholdConfiguration{
			WarningAt:      config.WarningAt,
			DangerAt:       config.DangerAt,
			Unit:           cleanPreviewString(config.Unit),
			Source:         "webhook_threshold_configuration",
			EvidenceWindow: cleanPreviewString(config.EvidenceWindow),
			Applied:        true,
		}
	}
	return thresholds, nil
}

func thresholdForKey(thresholds map[string]webhookThresholdConfiguration, key string, defaultWarning, defaultDanger int, defaultUnit string) webhookThresholdConfiguration {
	if thresholds != nil {
		if threshold, ok := thresholds[key]; ok {
			if threshold.WarningAt < 0 {
				threshold.WarningAt = 0
			}
			if threshold.DangerAt < threshold.WarningAt {
				threshold.DangerAt = threshold.WarningAt
			}
			if threshold.Unit == "" {
				threshold.Unit = defaultUnit
			}
			if expected := expectedWebhookThresholdUnit(key); expected != "" && threshold.Unit != expected {
				threshold.Unit = defaultUnit
			}
			if threshold.Source == "" {
				threshold.Source = "webhook_threshold_configuration"
			}
			threshold.Applied = true
			return threshold
		}
	}
	return webhookThresholdConfiguration{
		WarningAt: defaultWarning,
		DangerAt:  defaultDanger,
		Unit:      defaultUnit,
		Source:    "default_static_threshold",
	}
}

func expectedWebhookThresholdUnit(key string) string {
	switch key {
	case "sync_capacity_active", "provider_pair_active_24h":
		return "active_runs"
	case "sync_failure_7d", "provider_pair_failure_24h":
		return "failures"
	case "webhook_delivery_failure_7d":
		return "failed_events"
	case "github_actions_volume_24h":
		return "runs"
	default:
		return ""
	}
}

func humanThresholdUnit(unit string) string {
	switch strings.TrimSpace(unit) {
	case "active_runs":
		return "active runs"
	case "failed_events":
		return "failed events"
	default:
		return strings.ReplaceAll(strings.TrimSpace(unit), "_", " ")
	}
}

func thresholdSourceForPair(activeThreshold, failureThreshold webhookThresholdConfiguration) string {
	if activeThreshold.Applied && failureThreshold.Applied {
		return "webhook_threshold_configuration"
	}
	if activeThreshold.Applied {
		return "webhook_threshold_configuration_active_only"
	}
	if failureThreshold.Applied {
		return "webhook_threshold_configuration_failure_only"
	}
	return "default_static_threshold"
}

func thresholdDetail(warningAt, dangerAt int, unit string) string {
	return fmt.Sprintf("warning >= %d %s / danger >= %d %s", warningAt, unit, dangerAt, unit)
}

func signalStatusFromSync(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return "unknown"
	}
	return text
}

func signalSeverityFromSync(value any) string {
	switch signalStatusFromSync(value) {
	case "failed", "error", "rejected":
		return "danger"
	case "running", "queued", "provisioning":
		return "warning"
	default:
		return "ok"
	}
}

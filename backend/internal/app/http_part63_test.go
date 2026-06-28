package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderReviewAttemptTransactionSnapshotPayloadRejectsProviderCallRecorded(t *testing.T) {
	attempt := providerReviewActivationSnapshotAttempt("planned", "independent")
	ledger := providerReviewActivationSnapshotLedger(attempt)
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	transactionPlan := mapFromAny(dispatchPlan["transaction_plan"])
	boundaryPlan := mapFromAny(transactionPlan["provider_call_boundary_plan"])
	boundaryPlan["provider_call_boundary_recorded"] = true
	boundaryPlan["contains_token"] = true
	boundaryPlan["provider_call_boundary_ready_reason"] = "secret-token"

	snapshot := providerReviewAttemptTransactionSnapshotPayload(attempt, ledger, true)
	ready, state, missing := providerReviewAttemptTransactionSnapshotReadiness(snapshot)
	if ready ||
		state != "transaction_metadata_ready" ||
		snapshot["provider_call_boundary_recorded"] != true ||
		snapshot["contains_token"] != true ||
		snapshot["provider_call_boundary_ready_reason"] != "" ||
		snapshot["status_snapshot_write_eligible"] != false ||
		snapshot["status_snapshot_written"] != false ||
		!containsString(missing, "provider_review_transaction_not_no_call") {
		t.Fatalf("transaction snapshot with recorded provider call = snapshot %#v, ready %v, state %s, missing %#v", snapshot, ready, state, missing)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "request body", "response body", "idempotency_key_material"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("transaction snapshot with provider call marker leaked %q: %s", leak, encoded)
		}
	}
}

func TestRepoSyncRunFiltersFromRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/repo-sync-runs?asset_id=%20asset-1%20&status=%20failed%20&ref=%20refs/heads/main%20&since=2026-01-01T00:00:00Z&until=2026-01-02T00:00:00Z", nil)
	got, err := repoSyncRunFiltersFromRequest(req)
	if err != nil {
		t.Fatalf("repoSyncRunFiltersFromRequest: %v", err)
	}
	if got.AssetID != "asset-1" || got.Status != "failed" || got.Ref != "refs/heads/main" {
		t.Fatalf("filters = %#v", got)
	}
	if got.Since != "2026-01-01T00:00:00Z" || got.Until != "2026-01-02T00:00:00Z" {
		t.Fatalf("date filters = %#v", got)
	}
}

func TestRepoSyncRunFiltersRejectInvalidTime(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/repo-sync-runs?since=yesterday", nil)
	_, err := repoSyncRunFiltersFromRequest(req)
	if err == nil || !strings.Contains(err.Error(), "since must be RFC3339") {
		t.Fatalf("error = %v, want RFC3339 error", err)
	}
}

func TestRepoSyncAssetAnalyticsSQLIncludesCoreMetrics(t *testing.T) {
	t.Skip("repo sync asset analytics now use GORM models and Go aggregation; replace SQL-shape assertion with GORM fixture coverage")
}

func TestRepoSyncAssetTrendSQLIncludesDailyMetrics(t *testing.T) {
	t.Skip("repo sync asset trend now uses GORM models and Go aggregation; replace SQL-shape assertion with GORM fixture coverage")
}

func TestRepoSyncCapacitySignals(t *testing.T) {
	signals := repoSyncCapacitySignals(
		map[string]any{"id": "asset-1", "enabled": false},
		map[string]any{
			"source_provider":               "gitea",
			"target_provider":               "github",
			"source_last_sync_status":       "completed",
			"target_last_sync_status":       "failed",
			"active_runs":                   int64(2),
			"failed_runs_7d":                int64(6),
			"webhook_failures_7d":           int64(1),
			"github_runs_24h":               int64(55),
			"provider_pair_active_runs":     int64(4),
			"provider_pair_runs_24h":        int64(20),
			"provider_pair_failed_runs_24h": int64(2),
			"last_webhook_error":            "bad signature",
		},
		"source-1",
		"target-1",
	)
	byName := map[string]map[string]any{}
	for _, signal := range signals {
		byName[fmt.Sprint(signal["name"])] = signal
	}
	if byName["target provider"]["severity"] != "danger" {
		t.Fatalf("target provider severity = %v", byName["target provider"]["severity"])
	}
	if byName["sync capacity"]["severity"] != "warning" {
		t.Fatalf("sync capacity severity = %v", byName["sync capacity"]["severity"])
	}
	if !strings.Contains(fmt.Sprint(byName["sync capacity"]["threshold"]), "warning >= 1 active runs") {
		t.Fatalf("sync capacity threshold = %#v", byName["sync capacity"]["threshold"])
	}
	if byName["7d sync failures"]["severity"] != "danger" {
		t.Fatalf("7d sync failures severity = %v", byName["7d sync failures"]["severity"])
	}
	if !strings.Contains(fmt.Sprint(byName["7d sync failures"]["threshold"]), "warning >= 1 failures") {
		t.Fatalf("7d sync failures threshold = %#v", byName["7d sync failures"]["threshold"])
	}
	if byName["webhook delivery"]["severity"] != "warning" || !strings.Contains(fmt.Sprint(byName["webhook delivery"]["detail"]), "bad signature") {
		t.Fatalf("webhook signal = %#v", byName["webhook delivery"])
	}
	if !strings.Contains(fmt.Sprint(byName["webhook delivery"]["threshold"]), "danger >= 3 failed events") {
		t.Fatalf("webhook threshold = %#v", byName["webhook delivery"]["threshold"])
	}
	if byName["GitHub Actions volume"]["severity"] != "warning" {
		t.Fatalf("GitHub Actions volume severity = %v", byName["GitHub Actions volume"]["severity"])
	}
	if !strings.Contains(fmt.Sprint(byName["GitHub Actions volume"]["threshold"]), "warning >= 50 runs") {
		t.Fatalf("GitHub Actions volume threshold = %#v", byName["GitHub Actions volume"]["threshold"])
	}
	if byName["provider pair pressure"]["severity"] != "warning" || !strings.Contains(fmt.Sprint(byName["provider pair pressure"]["detail"]), "gitea -> github") {
		t.Fatalf("provider pair pressure signal = %#v", byName["provider pair pressure"])
	}
	if !strings.Contains(fmt.Sprint(byName["provider pair pressure"]["threshold"]), "active warning >= 3") {
		t.Fatalf("provider pair pressure threshold = %#v", byName["provider pair pressure"]["threshold"])
	}
	if byName["asset state"]["status"] != "disabled" {
		t.Fatalf("asset state signal = %#v", byName["asset state"])
	}
}

func TestRepoSyncCapacitySignalsUseWebhookThresholdConfigurations(t *testing.T) {
	signals := repoSyncCapacitySignalsWithThresholds(
		map[string]any{"id": "asset-1", "enabled": true},
		map[string]any{
			"source_provider":               "gitea",
			"target_provider":               "github",
			"source_last_sync_status":       "completed",
			"target_last_sync_status":       "completed",
			"active_runs":                   int64(2),
			"failed_runs_7d":                int64(2),
			"webhook_failures_7d":           int64(2),
			"github_runs_24h":               int64(75),
			"provider_pair_active_runs":     int64(6),
			"provider_pair_runs_24h":        int64(12),
			"provider_pair_failed_runs_24h": int64(1),
		},
		"source-1",
		"target-1",
		map[string]webhookThresholdConfiguration{
			"sync_capacity_active":        {WarningAt: 3, DangerAt: 5, Unit: "active_runs", Source: "webhook_threshold_configuration", Applied: true},
			"sync_failure_7d":             {WarningAt: 3, DangerAt: 6, Unit: "failures", Source: "webhook_threshold_configuration", Applied: true},
			"webhook_delivery_failure_7d": {WarningAt: 3, DangerAt: 6, Unit: "failed_events", Source: "webhook_threshold_configuration", Applied: true},
			"github_actions_volume_24h":   {WarningAt: 80, DangerAt: 250, Unit: "runs", Source: "webhook_threshold_configuration", Applied: true},
			"provider_pair_active_24h":    {WarningAt: 7, DangerAt: 12, Unit: "active_runs", Source: "webhook_threshold_configuration", Applied: true},
			"provider_pair_failure_24h":   {WarningAt: 2, DangerAt: 4, Unit: "failures", Source: "webhook_threshold_configuration", Applied: true},
		},
	)
	byName := map[string]map[string]any{}
	for _, signal := range signals {
		byName[fmt.Sprint(signal["name"])] = signal
	}
	for _, name := range []string{"sync capacity", "7d sync failures", "webhook delivery", "GitHub Actions volume", "provider pair pressure"} {
		if byName[name]["threshold_configuration_applied"] != true ||
			byName[name]["capacity_signals_recomputed"] != true ||
			!strings.Contains(fmt.Sprint(byName[name]["threshold_source"]), "webhook_threshold_configuration") {
			t.Fatalf("%s should use configured threshold: %#v", name, byName[name])
		}
		if byName[name]["severity"] != "ok" {
			t.Fatalf("%s severity = %v, want ok after configured thresholds: %#v", name, byName[name]["severity"], byName[name])
		}
	}
	if !strings.Contains(fmt.Sprint(byName["sync capacity"]["threshold"]), "warning >= 3 active runs") {
		t.Fatalf("sync capacity threshold did not use configured value: %#v", byName["sync capacity"])
	}
}

func TestRepoSyncCapacityThresholdDetail(t *testing.T) {
	got := thresholdDetail(2, 4, "items")
	if got != "warning >= 2 items / danger >= 4 items" {
		t.Fatalf("thresholdDetail = %q", got)
	}
}

func TestRepoSyncProviderPairPressureSeverity(t *testing.T) {
	cases := []struct {
		name        string
		active      int64
		failures24h int64
		want        string
	}{
		{name: "empty", want: "ok"},
		{name: "failure warning", failures24h: int64(repoSyncCapacityPairFailureWarningThreshold), want: "warning"},
		{name: "failure danger", failures24h: int64(repoSyncCapacityPairFailureDangerThreshold), want: "danger"},
		{name: "active danger", active: int64(repoSyncCapacityPairActiveDangerThreshold), want: "danger"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			signals := repoSyncCapacitySignals(
				map[string]any{"id": "asset-1", "enabled": true},
				map[string]any{
					"source_provider":               "gitea",
					"target_provider":               "github",
					"source_last_sync_status":       "completed",
					"target_last_sync_status":       "completed",
					"provider_pair_active_runs":     tc.active,
					"provider_pair_runs_24h":        tc.active + tc.failures24h,
					"provider_pair_failed_runs_24h": tc.failures24h,
				},
				"source-1",
				"target-1",
			)
			byName := map[string]map[string]any{}
			for _, signal := range signals {
				byName[fmt.Sprint(signal["name"])] = signal
			}
			if byName["provider pair pressure"]["severity"] != tc.want {
				t.Fatalf("provider pair pressure severity = %v, want %s", byName["provider pair pressure"]["severity"], tc.want)
			}
		})
	}
}

func TestRepoSyncCapacitySignalsSQLIncludesProviderPairPressure(t *testing.T) {
	t.Skip("repo sync capacity now uses GORM models and Go aggregation; replace SQL-shape assertion with GORM fixture coverage")
}

func TestWebhookThresholdConfigurationOverrideSQLUsesSourceRemote(t *testing.T) {
	t.Skip("webhook threshold overrides now use GORM models; replace SQL-shape assertion with GORM fixture coverage")
}

func TestThresholdForKeyFallsBackToExpectedUnit(t *testing.T) {
	threshold := thresholdForKey(map[string]webhookThresholdConfiguration{
		"sync_capacity_active": {WarningAt: 2, DangerAt: 4, Unit: "failures", Applied: true},
	}, "sync_capacity_active", 1, 3, "active_runs")
	if threshold.Unit != "active_runs" || threshold.WarningAt != 2 || threshold.DangerAt != 4 || !threshold.Applied {
		t.Fatalf("threshold should preserve configured counts but fallback unit: %#v", threshold)
	}
}

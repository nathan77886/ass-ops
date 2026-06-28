package app

import (
	"strings"
	"testing"
)

func TestRepoSyncAssetRisk(t *testing.T) {
	cases := []struct {
		name        string
		asset       map[string]any
		wantRisk    string
		wantSummary string
	}{
		{
			name:        "archived",
			asset:       map[string]any{"archived_at": "2026-01-01T00:00:00Z", "enabled": true},
			wantRisk:    "warning",
			wantSummary: "archived",
		},
		{
			name:        "last sync failed",
			asset:       map[string]any{"enabled": true, "last_sync_status": "failed"},
			wantRisk:    "danger",
			wantSummary: "last sync failed",
		},
		{
			name:        "queue saturated",
			asset:       map[string]any{"enabled": true, "running_runs": int64(3)},
			wantRisk:    "danger",
			wantSummary: "3 active runs",
		},
		{
			name:        "low success rate",
			asset:       map[string]any{"enabled": true, "total_runs": int64(8), "success_rate": "42.5"},
			wantRisk:    "danger",
			wantSummary: "42% success rate",
		},
		{
			name:        "healthy",
			asset:       map[string]any{"enabled": true, "total_runs": int64(4), "success_rate": 100.0},
			wantRisk:    "ok",
			wantSummary: "healthy",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRisk, gotSummary := repoSyncAssetRisk(tc.asset)
			if gotRisk != tc.wantRisk || !strings.Contains(gotSummary, tc.wantSummary) {
				t.Fatalf("repoSyncAssetRisk = %q, %q; want %q containing %q", gotRisk, gotSummary, tc.wantRisk, tc.wantSummary)
			}
		})
	}
}

func TestWebhookConnectionHealth(t *testing.T) {
	cases := []struct {
		name        string
		row         map[string]any
		wantHealth  string
		wantSummary string
	}{
		{
			name:        "disabled",
			row:         map[string]any{"enabled": false},
			wantHealth:  "warning",
			wantSummary: "disabled",
		},
		{
			name:        "many failures",
			row:         map[string]any{"enabled": true, "failures_7d": int64(3)},
			wantHealth:  "danger",
			wantSummary: "3 failed",
		},
		{
			name:        "last rejected",
			row:         map[string]any{"enabled": true, "last_delivery_status": "rejected", "last_error_message": "invalid signature"},
			wantHealth:  "danger",
			wantSummary: "last delivery rejected",
		},
		{
			name:        "some failures",
			row:         map[string]any{"enabled": true, "failures_7d": int64(1), "deliveries_7d": int64(5)},
			wantHealth:  "warning",
			wantSummary: "1 failed",
		},
		{
			name:        "healthy",
			row:         map[string]any{"enabled": true, "deliveries_7d": int64(4), "failures_7d": int64(0)},
			wantHealth:  "ok",
			wantSummary: "4 deliveries",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotHealth, gotSummary := webhookConnectionHealth(tc.row)
			if gotHealth != tc.wantHealth || !strings.Contains(gotSummary, tc.wantSummary) {
				t.Fatalf("webhookConnectionHealth = %q, %q; want %q containing %q", gotHealth, gotSummary, tc.wantHealth, tc.wantSummary)
			}
		})
	}
}

func TestWebhookConnectionHealthRedactsErrorMessages(t *testing.T) {
	health, summary := webhookConnectionHealth(map[string]any{
		"enabled":              true,
		"last_delivery_status": "failed",
		"last_error_message":   "Bearer secret-token payload-body https://provider.example.com/hook",
		"last_delivery_error":  "password=secret",
	})
	if health != "danger" || summary != "last delivery failed" {
		t.Fatalf("webhookConnectionHealth = %q, %q", health, summary)
	}
	for _, forbidden := range []string{"Bearer", "secret-token", "payload-body", "provider.example.com", "password"} {
		if strings.Contains(summary, forbidden) {
			t.Fatalf("webhook health summary leaked %q: %s", forbidden, summary)
		}
	}
}

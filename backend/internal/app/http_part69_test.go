package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebhookProviderCallbackThresholdVolumeEvidenceBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		evidence   map[string]any
		wantState  string
		wantReady  bool
		wantReason string
	}{
		{
			name:       "delivery volume without processing is visible but not review ready",
			evidence:   map[string]any{"delivery_count_7d": int64(3)},
			wantState:  "volume_observed",
			wantReady:  false,
			wantReason: "processed_or_repo_sync_volume_not_observed",
		},
		{
			name:       "failures override operation volume",
			evidence:   map[string]any{"delivery_count_7d": int64(5), "failed_count_7d": int64(1), "operation_run_count_7d": int64(3)},
			wantState:  "review_failed_volume",
			wantReady:  false,
			wantReason: "webhook_failures_need_operator_threshold_review",
		},
		{
			name:       "processed volume is review ready",
			evidence:   map[string]any{"delivery_count_7d": int64(5), "processed_count_7d": int64(5)},
			wantState:  "ready_for_review",
			wantReady:  true,
			wantReason: "operator_threshold_review_not_recorded",
		},
		{
			name:       "operation run volume is review ready",
			evidence:   map[string]any{"operation_run_count_7d": int64(2)},
			wantState:  "ready_for_review",
			wantReady:  true,
			wantReason: "operator_threshold_review_not_recorded",
		},
		{
			name:       "matched repo sync asset volume is review ready",
			evidence:   map[string]any{"matched_repo_sync_asset_count_7d": int64(2)},
			wantState:  "ready_for_review",
			wantReady:  true,
			wantReason: "operator_threshold_review_not_recorded",
		},
		{
			name:       "replay only volume is visible but not review ready",
			evidence:   map[string]any{"replayed_count_7d": int64(1)},
			wantState:  "volume_observed",
			wantReady:  false,
			wantReason: "processed_or_repo_sync_volume_not_observed",
		},
		{
			name:       "nil evidence waits for volume",
			evidence:   nil,
			wantState:  "waiting_for_volume",
			wantReady:  false,
			wantReason: "real_provider_volume_not_observed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := webhookProviderCallbackThresholdVolumeEvidence(tt.evidence)
			if got["threshold_review_state"] != tt.wantState ||
				got["threshold_review_ready"] != tt.wantReady ||
				!containsString(stringSliceFromAny(got["blocked_reasons"]), tt.wantReason) {
				t.Fatalf("threshold volume evidence = %#v, want state=%s ready=%v reason=%s", got, tt.wantState, tt.wantReady, tt.wantReason)
			}
			if got["external_call_made"] != false ||
				got["provider_metrics_fetched"] != false ||
				got["threshold_configuration_written"] != false ||
				got["contains_token"] != false ||
				got["contains_payload"] != false ||
				got["contains_provider_url"] != false {
				t.Fatalf("threshold volume evidence must stay no-call and redacted: %#v", got)
			}
			metricsComparison := webhookProviderCallbackProviderMetricsComparisonPlan(got)
			if metricsComparison["comparison_ready_for_review"] != tt.wantReady ||
				metricsComparison["provider_metrics_fetched"] != false ||
				metricsComparison["provider_pair_limits_compared"] != false ||
				metricsComparison["external_call_made"] != false ||
				metricsComparison["contains_token"] != false ||
				metricsComparison["contains_payload"] != false ||
				metricsComparison["contains_provider_url"] != false {
				t.Fatalf("provider metrics comparison plan must stay no-call and redacted: %#v", metricsComparison)
			}
			configurationPlan := webhookProviderCallbackThresholdConfigurationPlan(got, metricsComparison)
			wantConfigState := "blocked"
			wantComparisonState := "local_volume_observed"
			if tt.wantState == "waiting_for_volume" {
				wantConfigState = "waiting_for_volume"
				wantComparisonState = "waiting_for_volume"
			} else if tt.wantState == "review_failed_volume" {
				wantConfigState = "needs_failure_review"
				wantComparisonState = "needs_failure_review"
			} else if tt.wantReady {
				wantConfigState = "ready_for_operator_review"
				wantComparisonState = "ready_for_operator_review"
			}
			if metricsComparison["comparison_state"] != wantComparisonState {
				t.Fatalf("provider metrics comparison = %#v, want state=%s", metricsComparison, wantComparisonState)
			}
			if configurationPlan["configuration_state"] != wantConfigState ||
				configurationPlan["configuration_review_ready"] != tt.wantReady ||
				configurationPlan["threshold_configuration_written"] != false ||
				configurationPlan["configuration_write_enabled"] != false ||
				configurationPlan["provider_metrics_fetched"] != false ||
				configurationPlan["external_call_made"] != false {
				t.Fatalf("threshold configuration plan = %#v, want state=%s ready=%v", configurationPlan, wantConfigState, tt.wantReady)
			}
			if mapFromAny(configurationPlan["provider_metrics_comparison_plan"])["comparison_state"] != wantComparisonState {
				t.Fatalf("configuration should carry provider metrics comparison plan: %#v", configurationPlan["provider_metrics_comparison_plan"])
			}
		})
	}
}

func TestAnnotateWebhookCallbackReadinessAllowsNilItems(t *testing.T) {
	annotateWebhookCallbackReadiness(nil, "https://assops.example.com")
}

func TestOperationApprovalFiltersFromRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/operation-approvals?status=%20pending%20&action=ssh.exec&resource_type=ssh_machine&q=%20deploy%20&requested_by=%20ops@example.com%20&since=2026-01-01T00:00:00Z&until=2026-01-02T00:00:00Z", nil)
	got, err := operationApprovalFiltersFromRequest(req)
	if err != nil {
		t.Fatalf("operationApprovalFiltersFromRequest: %v", err)
	}
	if got.Status != "pending" || got.Action != "ssh.exec" || got.ResourceType != "ssh_machine" {
		t.Fatalf("filters = %#v", got)
	}
	if got.Query != "deploy" || got.RequestedBy != "ops@example.com" {
		t.Fatalf("text filters = %#v", got)
	}
}

func TestOperationApprovalSummarySQLIncludesVisibilityAndMetrics(t *testing.T) {
	t.Skip("operation approval summary now uses GORM models and Go aggregation; replace SQL-shape assertion with GORM fixture coverage")
}

func TestOperationApprovalReminderCandidatesSQLIncludesSLAAndVisibility(t *testing.T) {
	t.Skip("operation approval reminder candidates now use GORM models and Go aggregation; replace SQL-shape assertion with GORM fixture coverage")
}

func TestDueOperationApprovalRemindersSQLIncludesThrottleAndLocking(t *testing.T) {
	t.Skip("due approval reminders now use GORM models and Go due filtering; replace SQL-shape assertion with GORM fixture coverage")
}

func TestDueOperationApprovalEscalationsSQLIncludesThrottleAndLocking(t *testing.T) {
	t.Skip("due approval escalations now use GORM models and Go due filtering; replace SQL-shape assertion with GORM fixture coverage")
}

func TestOperationApprovalRulesSQLIncludesPolicyFields(t *testing.T) {
	t.Skip("operation approval rules now use GORM models and Go mapping; replace SQL-shape assertion with GORM fixture coverage")
}

func TestApprovalChannelDestinationsPreviewKinds(t *testing.T) {
	destinations := approvalChannelDestinations([]string{"ui", "webhook", "email:ops@example.com", "slack:#deploys", "pagerduty"})
	if len(destinations) != 5 {
		t.Fatalf("destinations = %#v", destinations)
	}
	if destinations[0]["kind"] != "ui" || destinations[0]["label"] != "Operations UI" || destinations[0]["needs_config"] != false {
		t.Fatalf("ui destination = %#v", destinations[0])
	}
	if destinations[0]["adapter"] != "operations_ui" || destinations[0]["adapter_status"] != "enabled" || destinations[0]["delivery_mode"] != "in_app" {
		t.Fatalf("ui adapter readiness = %#v", destinations[0])
	}
	if destinations[1]["kind"] != "webhook" || destinations[1]["target"] != "" || destinations[1]["needs_config"] != false {
		t.Fatalf("webhook destination = %#v", destinations[1])
	}
	if destinations[1]["adapter"] != "approval_webhook" || destinations[1]["adapter_status"] != "environment_backed" || destinations[1]["delivery_mode"] != "http_post" || destinations[1]["requires_external_call"] != true {
		t.Fatalf("webhook adapter readiness = %#v", destinations[1])
	}
	if destinations[2]["kind"] != "email" || destinations[2]["target"] != "ops@example.com" || destinations[2]["needs_config"] != true {
		t.Fatalf("email destination = %#v", destinations[2])
	}
	if destinations[3]["kind"] != "slack" || destinations[3]["target"] != "#deploys" || destinations[3]["needs_config"] != true {
		t.Fatalf("slack destination = %#v", destinations[3])
	}
	if destinations[4]["kind"] != "pagerduty" || destinations[4]["needs_config"] != true {
		t.Fatalf("pagerduty destination = %#v", destinations[4])
	}
	for _, index := range []int{2, 3, 4} {
		if destinations[index]["adapter_status"] != "planned" || destinations[index]["delivery_mode"] != "preview_only" || destinations[index]["requires_external_call"] != true {
			t.Fatalf("future adapter should be preview-only: %#v", destinations[index])
		}
	}
	for _, kind := range []string{"ui", "webhook", "email", "slack", "pagerduty"} {
		if !approvalDestinationKnownKind(kind) || approvalDestinationAdapterReadiness(kind, "")["adapter_status"] == "unknown" {
			t.Fatalf("known destination kind missing adapter readiness: %s", kind)
		}
	}
}

func TestApprovalChannelDestinationsHideUnknownTargets(t *testing.T) {
	destinations := approvalChannelDestinations([]string{" sms:+1234567890 ", "custom:target:extra"})
	if len(destinations) != 2 {
		t.Fatalf("destinations = %#v", destinations)
	}
	for _, destination := range destinations {
		if destination["needs_config"] != true {
			t.Fatalf("destination should need config: %#v", destination)
		}
		label := fmt.Sprint(destination["label"])
		if strings.Contains(label, "+1234567890") || strings.Contains(label, "target") || strings.Contains(label, "extra") {
			t.Fatalf("unknown destination label leaked target: %#v", destination)
		}
		if fmt.Sprint(destination["target"]) != "" || destination["redacted_target"] != true {
			t.Fatalf("unknown destination should redact target: %#v", destination)
		}
		if destination["adapter_status"] != "unknown" || destination["delivery_mode"] != "preview_only" || destination["requires_external_call"] != true {
			t.Fatalf("unknown destination should remain preview-only: %#v", destination)
		}
	}
	if len(approvalChannelDestinations(nil)) != 0 {
		t.Fatal("nil channel list should produce no destinations")
	}
}

func TestEnrichOperationApprovalRuleDoesNotExposeWebhookSecretConfig(t *testing.T) {
	t.Setenv("ASSOPS_APPROVAL_WEBHOOK_URL", "https://example.test/secret-hook")
	t.Setenv("ASSOPS_APPROVAL_WEBHOOK_TOKEN", "secret-token")
	item := enrichOperationApprovalRule(map[string]any{
		"notification_channels": []string{"ui", "webhook"},
		"escalation_channels":   []string{"email:ops@example.com"},
	})
	encoded, _ := json.Marshal(item)
	if strings.Contains(string(encoded), "secret-hook") || strings.Contains(string(encoded), "secret-token") {
		t.Fatalf("enriched approval rule leaked webhook config: %s", encoded)
	}
	if _, ok := item["notification_destinations"]; !ok {
		t.Fatalf("notification_destinations missing: %#v", item)
	}
	notifications := sliceOfMapsFromAny(item["notification_destinations"])
	if len(notifications) != 2 ||
		notifications[1]["adapter"] != "approval_webhook" ||
		notifications[1]["adapter_status"] != "environment_backed" ||
		notifications[1]["delivery_mode"] != "http_post" ||
		notifications[1]["requires_external_call"] != true {
		t.Fatalf("notification destination adapter readiness missing: %#v", notifications)
	}
	if _, ok := item["escalation_destinations"]; !ok {
		t.Fatalf("escalation_destinations missing: %#v", item)
	}
	escalation := sliceOfMapsFromAny(item["escalation_destinations"])
	if len(escalation) != 1 || escalation[0]["adapter"] != "email" || escalation[0]["adapter_status"] != "planned" {
		t.Fatalf("escalation destination adapter readiness missing: %#v", escalation)
	}
}

func TestNormalizeRuleStringList(t *testing.T) {
	got := normalizeRuleStringList([]string{" Admin ", "admin", "OWNER", ""}, []string{"fallback"})
	want := []string{"admin", "owner"}
	if len(got) != len(want) {
		t.Fatalf("roles = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("roles = %#v, want %#v", got, want)
		}
	}
	fallback := normalizeRuleStringList(nil, []string{"admin"})
	if len(fallback) != 1 || fallback[0] != "admin" {
		t.Fatalf("fallback = %#v", fallback)
	}
}

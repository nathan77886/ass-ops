package app

import (
	"reflect"
	"slices"
	"testing"
)

func TestProviderReviewAttemptAdapterRetryBackoffPlan(t *testing.T) {
	for _, tt := range []struct {
		name      string
		operation string
		endpoint  string
		order     int
	}{
		{
			name:      "branch ref",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
			order:     10,
		},
		{
			name:      "starter files",
			operation: "commit_starter_files",
			endpoint:  "github.commit_files",
			order:     20,
		},
		{
			name:      "review request",
			operation: "open_review_request",
			endpoint:  "gitea.open_review",
			order:     30,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			operation := map[string]any{
				"name":            tt.operation,
				"endpoint_key":    tt.endpoint,
				"operation_order": tt.order,
			}
			transportPlan := map[string]any{
				"mode":                     "redacted_attempt_adapter_transport_plan",
				"operation_name":           tt.operation,
				"endpoint_key":             tt.endpoint,
				"retryable_status_classes": []string{"5xx"},
			}
			plan := providerReviewAttemptAdapterRetryBackoffPlan(operation, transportPlan)
			if plan["mode"] != "redacted_attempt_adapter_retry_backoff_plan" ||
				plan["retry_backoff_state"] != "blocked" ||
				plan["retry_backoff_ready"] != false ||
				plan["retry_backoff_ready_reason"] != "provider_retry_backoff_not_armed" ||
				plan["retry_backoff_metadata_ready"] != true ||
				plan["operation_name"] != tt.operation ||
				plan["endpoint_key"] != tt.endpoint ||
				plan["operation_order"] != tt.order ||
				plan["retry_policy"] != "retry_only_after_response_diagnostics" ||
				plan["max_attempts"] != 3 ||
				plan["initial_backoff_seconds"] != 30 ||
				plan["max_backoff_seconds"] != 300 ||
				plan["jitter"] != "full" ||
				plan["requires_response_diagnostics"] != true ||
				plan["requires_idempotency_ledger"] != true ||
				plan["requires_attempt_ledger"] != true ||
				plan["requires_mutation_arming"] != true ||
				plan["retry_scheduled"] != false ||
				plan["retry_attempt_recorded"] != false ||
				plan["retry_after_value_recorded"] != false ||
				plan["retry_after_header_included"] != false ||
				plan["provider_rate_limit_value_included"] != false ||
				plan["provider_error_code_included"] != false ||
				plan["external_call_made"] != false ||
				plan["provider_api_call_made"] != false ||
				plan["provider_api_mutation"] != "disabled" ||
				plan["request_body_included"] != false ||
				plan["response_body_included"] != false ||
				plan["headers_included"] != false ||
				plan["authorization_header_included"] != false ||
				plan["provider_url_included"] != false ||
				plan["idempotency_key_included"] != false ||
				plan["contains_token"] != false ||
				plan["contains_provider_url"] != false ||
				plan["contains_repository_ref"] != false ||
				plan["contains_branch_name"] != false ||
				plan["contains_file_content"] != false ||
				plan["retry_backoff_boundary_redacted"] != true {
				t.Fatalf("retry backoff plan = %#v", plan)
			}
			if got := stringSliceFromAny(plan["retryable_status_classes"]); !reflect.DeepEqual(got, []string{"5xx"}) {
				t.Fatalf("retry classes = %#v", got)
			}
			if got := stringSliceFromAny(plan["transport_retryable_status_classes"]); !reflect.DeepEqual(got, []string{"5xx"}) {
				t.Fatalf("transport retry classes = %#v", got)
			}
			if got := stringSliceFromAny(plan["retry_backoff_sequence"]); !reflect.DeepEqual(got, []string{"classify_retryable_response", "verify_idempotency_ledger", "record_retry_decision", "schedule_backoff_retry"}) {
				t.Fatalf("retry sequence = %#v", got)
			}
			suppressedFields := stringSliceFromAny(plan["retry_backoff_suppressed_fields"])
			for _, field := range []string{"retry_after_value", "rate_limit_remaining", "provider_error_code", "response_headers", "response_body", "provider_url", "authorization_header", "token", "idempotency_key", "repository_ref", "branch_name", "file_content"} {
				if !slices.Contains(suppressedFields, field) {
					t.Fatalf("retry suppressed fields missing %q: %#v", field, suppressedFields)
				}
			}
			blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
			if !reflect.DeepEqual(blockedReasons, []string{"provider_retry_backoff_not_armed", "provider_response_diagnostics_not_recorded", "provider_idempotency_ledger_not_claimed", "provider_review_mutation_not_armed"}) {
				t.Fatalf("retry blocked reasons = %#v", blockedReasons)
			}
		})
	}

	if got := providerReviewAttemptAdapterRetryBackoffPlan(nil, map[string]any{"mode": "redacted_attempt_adapter_transport_plan"}); len(got) != 0 {
		t.Fatalf("empty operation retry backoff plan = %#v", got)
	}
	if got := providerReviewAttemptAdapterRetryBackoffPlan(
		map[string]any{"name": "create_branch_ref", "endpoint_key": "github.create_branch_ref"},
		map[string]any{"mode": "raw_transport_plan"},
	); len(got) != 0 {
		t.Fatalf("raw transport retry backoff plan = %#v", got)
	}
	if got := providerReviewAttemptAdapterRetryBackoffPlan(
		map[string]any{"name": "create_branch_ref", "endpoint_key": "github.create_branch_ref"},
		map[string]any{
			"mode":           "redacted_attempt_adapter_transport_plan",
			"operation_name": "commit_starter_files",
			"endpoint_key":   "github.commit_files",
		},
	); len(got) != 0 {
		t.Fatalf("mismatched transport identity retry backoff plan = %#v", got)
	}
	if got := providerReviewAttemptAdapterRetryBackoffPlan(
		map[string]any{"name": "create_branch_ref", "endpoint_key": "github.commit_files"},
		map[string]any{"mode": "redacted_attempt_adapter_transport_plan"},
	); len(got) != 0 {
		t.Fatalf("mismatched operation endpoint retry backoff plan = %#v", got)
	}
	if got := providerReviewAttemptAdapterRetryBackoffPlan(
		map[string]any{"name": "commit_starter_files", "endpoint_key": "github.open_review"},
		map[string]any{"mode": "redacted_attempt_adapter_transport_plan"},
	); len(got) != 0 {
		t.Fatalf("commit operation review endpoint retry backoff plan = %#v", got)
	}
	if got := providerReviewAttemptAdapterRetryBackoffPlan(
		map[string]any{"name": "commit_starter_files", "endpoint_key": "gitea.create_branch_ref"},
		map[string]any{"mode": "redacted_attempt_adapter_transport_plan"},
	); len(got) != 0 {
		t.Fatalf("gitea commit operation branch endpoint retry backoff plan = %#v", got)
	}
	if got := providerReviewAttemptAdapterRetryBackoffPlan(
		map[string]any{"name": "create_branch_ref", "endpoint_key": "unknown.create_branch_ref"},
		map[string]any{"mode": "redacted_attempt_adapter_transport_plan"},
	); len(got) != 0 {
		t.Fatalf("unknown endpoint retry backoff plan = %#v", got)
	}
}

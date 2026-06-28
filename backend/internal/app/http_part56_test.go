package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderReviewAttemptAdapterCredentialBindingPlan(t *testing.T) {
	for _, item := range []struct {
		name         string
		provider     string
		operation    string
		endpoint     string
		authScheme   string
		wantNonEmpty bool
	}{
		{
			name:         "github branch ref uses bearer token",
			provider:     "github",
			operation:    "create_branch_ref",
			endpoint:     "github.create_branch_ref",
			authScheme:   "bearer_token",
			wantNonEmpty: true,
		},
		{
			name:         "gitea review request uses token auth",
			provider:     "gitea",
			operation:    "open_review_request",
			endpoint:     "gitea.open_review",
			authScheme:   "token",
			wantNonEmpty: true,
		},
		{
			name:         "unknown provider returns empty plan",
			provider:     "raw_provider",
			operation:    "create_branch_ref",
			wantNonEmpty: false,
		},
		{
			name:         "unknown operation returns empty plan",
			provider:     "github",
			operation:    "raw_operation",
			wantNonEmpty: false,
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			plan := providerReviewAttemptAdapterCredentialBindingPlan(item.provider, item.operation)
			if !item.wantNonEmpty {
				if len(plan) != 0 {
					t.Fatalf("credential binding plan should be empty: %#v", plan)
				}
				return
			}
			if plan["mode"] != "redacted_attempt_adapter_credential_binding_plan" ||
				plan["credential_binding_state"] != "blocked" ||
				plan["credential_binding_ready"] != false ||
				plan["credential_binding_ready_reason"] != "provider_credential_runtime_binding_not_armed" ||
				plan["provider_type"] != item.provider ||
				plan["operation_name"] != item.operation ||
				plan["endpoint_key"] != item.endpoint ||
				plan["auth_scheme"] != item.authScheme ||
				plan["credential_source_kind"] != "provider_account_token_env" ||
				plan["requires_provider_account"] != true ||
				plan["requires_allowed_token_env"] != true ||
				plan["requires_runtime_token_present"] != true ||
				plan["credential_bound"] != false ||
				plan["authorization_header_materialized"] != false ||
				plan["token_env_name_included"] != false ||
				plan["token_value_included"] != false ||
				plan["token_stored"] != false ||
				plan["headers_included"] != false ||
				plan["external_call_made"] != false ||
				plan["provider_api_call_made"] != false ||
				plan["provider_api_mutation"] != "disabled" ||
				plan["contains_token"] != false ||
				plan["contains_provider_url"] != false ||
				plan["contains_repository_ref"] != false ||
				plan["contains_branch_name"] != false ||
				plan["contains_file_content"] != false ||
				plan["credential_boundary_redacted"] != true {
				t.Fatalf("credential binding plan = %#v", plan)
			}
			blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
			if len(blockedReasons) != 3 ||
				blockedReasons[0] != "provider_credential_runtime_binding_not_armed" ||
				blockedReasons[1] != "provider_review_adapter_not_implemented" ||
				blockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("credential binding blocked reasons = %#v", blockedReasons)
			}
			encoded, _ := json.Marshal(plan)
			for _, leak := range []string{"ASSOPS_TEMPLATE_PROVIDER_TOKEN", "secret-token", "Authorization", "raw_provider"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("credential binding plan leaked %q: %s", leak, encoded)
				}
			}
		})
	}
}

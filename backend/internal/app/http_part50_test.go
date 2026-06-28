package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderReviewAttemptAdapterProviderClientPlan(t *testing.T) {
	for _, item := range []struct {
		name         string
		provider     string
		operation    string
		endpoint     string
		clientKind   string
		authScheme   string
		capability   string
		wantNonEmpty bool
	}{
		{
			name:         "github branch ref client",
			provider:     "github",
			operation:    "create_branch_ref",
			endpoint:     "github.create_branch_ref",
			clientKind:   "github_provider_review_api_client",
			authScheme:   "bearer_token",
			capability:   "repository_ref_write",
			wantNonEmpty: true,
		},
		{
			name:         "github commit starter files client",
			provider:     "github",
			operation:    "commit_starter_files",
			endpoint:     "github.commit_files",
			clientKind:   "github_provider_review_api_client",
			authScheme:   "bearer_token",
			capability:   "repository_contents_write",
			wantNonEmpty: true,
		},
		{
			name:         "gitea review request client",
			provider:     "gitea",
			operation:    "open_review_request",
			endpoint:     "gitea.open_review",
			clientKind:   "gitea_provider_review_api_client",
			authScheme:   "token",
			capability:   "review_request_write",
			wantNonEmpty: true,
		},
		{
			name:      "unknown provider returns empty provider client plan",
			provider:  "raw_provider",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "empty operation returns empty provider client plan",
			provider:  "github",
			operation: "",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "mismatched endpoint returns empty provider client plan",
			provider:  "github",
			operation: "create_branch_ref",
			endpoint:  "gitea.create_branch_ref",
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			plan := providerReviewAttemptAdapterProviderClientPlan(item.provider, item.operation, item.endpoint)
			if !item.wantNonEmpty {
				if len(plan) != 0 {
					t.Fatalf("provider client plan should be empty: %#v", plan)
				}
				return
			}
			if plan["mode"] != "redacted_attempt_adapter_provider_client_plan" ||
				plan["provider_client_state"] != "blocked" ||
				plan["provider_client_ready"] != false ||
				plan["provider_client_ready_reason"] != "provider_review_provider_client_not_armed" ||
				plan["provider_type"] != item.provider ||
				plan["operation_name"] != item.operation ||
				plan["endpoint_key"] != item.endpoint ||
				plan["client_kind"] != item.clientKind ||
				plan["auth_scheme"] != item.authScheme ||
				plan["base_url_source"] != "provider_account_api_base_url" ||
				plan["credential_source_kind"] != "provider_account_token_env" ||
				plan["timeout_seconds"] != 15 ||
				plan["retry_policy"] != "retry_5xx_with_backoff" ||
				plan["client_factory_interface_registered"] != true ||
				plan["client_factory_registered"] != true ||
				plan["client_implemented"] != false ||
				plan["provider_client_constructed"] != false ||
				plan["provider_account_resolved"] != false ||
				plan["base_url_validated"] != false ||
				plan["base_url_materialized"] != false ||
				plan["token_env_allowed"] != false ||
				plan["runtime_token_loaded"] != false ||
				plan["authorization_header_materialized"] != false ||
				plan["http_client_configured"] != false ||
				plan["provider_client_boundary_redacted"] != true ||
				plan["external_call_made"] != false ||
				plan["provider_api_call_made"] != false ||
				plan["provider_api_mutation"] != "disabled" ||
				plan["base_url_included"] != false ||
				plan["token_env_name_included"] != false ||
				plan["token_value_included"] != false ||
				plan["authorization_header_included"] != false ||
				plan["provider_url_included"] != false ||
				plan["request_body_included"] != false ||
				plan["response_body_included"] != false ||
				plan["headers_included"] != false ||
				plan["contains_token"] != false ||
				plan["contains_provider_url"] != false ||
				plan["contains_repository_ref"] != false ||
				plan["contains_branch_name"] != false ||
				plan["contains_file_content"] != false {
				t.Fatalf("provider client plan = %#v", plan)
			}
			capabilities := stringSliceFromAny(plan["required_capabilities"])
			if len(capabilities) != 1 || capabilities[0] != item.capability {
				t.Fatalf("provider client capabilities = %#v", capabilities)
			}
			blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
			if len(blockedReasons) != 3 ||
				blockedReasons[0] != "provider_review_provider_client_not_armed" ||
				blockedReasons[1] != "provider_review_live_adapter_not_implemented" ||
				blockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("provider client blocked reasons = %#v", blockedReasons)
			}
			encoded, _ := json.Marshal(plan)
			for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "ASSOPS_TEMPLATE_PROVIDER_TOKEN"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("provider client plan leaked %q: %s", leak, encoded)
				}
			}
		})
	}
}

package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderReviewAttemptAdapterExecuteMethodPlan(t *testing.T) {
	for _, item := range []struct {
		name         string
		provider     string
		operation    string
		endpoint     string
		methodName   string
		httpMethod   string
		wantNonEmpty bool
	}{
		{
			name:         "github branch ref execute method",
			provider:     "github",
			operation:    "create_branch_ref",
			endpoint:     "github.create_branch_ref",
			methodName:   "execute_branch_ref_creation",
			httpMethod:   "POST",
			wantNonEmpty: true,
		},
		{
			name:         "github commit starter files execute method",
			provider:     "github",
			operation:    "commit_starter_files",
			endpoint:     "github.commit_files",
			methodName:   "execute_starter_file_commit",
			httpMethod:   "PUT",
			wantNonEmpty: true,
		},
		{
			name:         "gitea review request execute method",
			provider:     "gitea",
			operation:    "open_review_request",
			endpoint:     "gitea.open_review",
			methodName:   "execute_review_request_open",
			httpMethod:   "POST",
			wantNonEmpty: true,
		},
		{
			name:      "unknown provider returns empty execute method plan",
			provider:  "raw_provider",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "empty operation returns empty execute method plan",
			provider:  "github",
			operation: "",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "mismatched endpoint returns empty execute method plan",
			provider:  "github",
			operation: "create_branch_ref",
			endpoint:  "gitea.create_branch_ref",
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			plan := providerReviewAttemptAdapterExecuteMethodPlan(item.provider, item.operation, item.endpoint)
			if !item.wantNonEmpty {
				if len(plan) != 0 {
					t.Fatalf("execute method plan should be empty: %#v", plan)
				}
				return
			}
			if plan["mode"] != "redacted_attempt_adapter_execute_method_plan" ||
				plan["execute_method_state"] != "blocked" ||
				plan["execute_method_ready"] != false ||
				plan["execute_method_ready_reason"] != "provider_review_execute_method_not_armed" ||
				plan["provider_type"] != item.provider ||
				plan["operation_name"] != item.operation ||
				plan["endpoint_key"] != item.endpoint ||
				plan["method_name"] != item.methodName ||
				plan["http_method"] != item.httpMethod ||
				plan["execute_method_interface_registered"] != true ||
				plan["execute_method_registered"] != true ||
				plan["execute_method_implemented"] != false ||
				plan["execute_method_bound"] != false ||
				plan["requires_attempt_claim"] != true ||
				plan["requires_idempotency_claim"] != true ||
				plan["requires_credential_binding"] != true ||
				plan["requires_provider_client"] != true ||
				plan["requires_request_builder"] != true ||
				plan["requires_transport"] != true ||
				plan["requires_response_handler"] != true ||
				plan["requires_transaction_handler"] != true ||
				plan["requires_mutation_arming"] != true ||
				plan["provider_client_constructed"] != false ||
				plan["request_materialized"] != false ||
				plan["provider_request_sent"] != false ||
				plan["response_handled"] != false ||
				plan["transaction_recorded"] != false ||
				plan["dependency_update_recorded"] != false ||
				plan["execute_method_boundary_redacted"] != true ||
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
				plan["contains_file_content"] != false {
				t.Fatalf("execute method plan = %#v", plan)
			}
			sequence := stringSliceFromAny(plan["execution_sequence"])
			if len(sequence) != 8 ||
				sequence[0] != "verify_attempt_claim" ||
				sequence[1] != "verify_idempotency_claim" ||
				sequence[2] != "bind_credential" ||
				sequence[3] != "construct_provider_client" ||
				sequence[4] != "build_request" ||
				sequence[5] != "stage_provider_request_send" ||
				sequence[6] != "handle_response" ||
				sequence[7] != "record_attempt_transaction" {
				t.Fatalf("execute method sequence = %#v", sequence)
			}
			blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
			if len(blockedReasons) != 3 ||
				blockedReasons[0] != "provider_review_execute_method_not_armed" ||
				blockedReasons[1] != "provider_review_live_adapter_not_implemented" ||
				blockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("execute method blocked reasons = %#v", blockedReasons)
			}
			encoded, _ := json.Marshal(plan)
			for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "ASSOPS_TEMPLATE_PROVIDER_TOKEN"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("execute method plan leaked %q: %s", leak, encoded)
				}
			}
		})
	}
}

func TestDisabledProviderReviewAttemptExecuteMethodRejectsMismatchedEndpoint(t *testing.T) {
	method := disabledProviderReviewAttemptExecuteMethod{methodName: "execute_branch_ref_creation"}
	plan := method.BuildPlan(providerReviewAttemptExecuteMethodInput{
		ProviderType:  "github",
		OperationName: "create_branch_ref",
		EndpointKey:   "gitea.create_branch_ref",
	})
	if len(plan) != 0 {
		t.Fatalf("mismatched endpoint direct execute method plan should be empty: %#v", plan)
	}
}

func TestDisabledProviderReviewAttemptProviderClientRejectsMismatchedEndpoint(t *testing.T) {
	factory := disabledProviderReviewAttemptProviderClientFactory{clientKind: "github_provider_review_api_client"}
	plan := factory.BuildPlan(providerReviewAttemptProviderClientInput{
		ProviderType:  "github",
		OperationName: "create_branch_ref",
		EndpointKey:   "gitea.create_branch_ref",
	})
	if len(plan) != 0 {
		t.Fatalf("mismatched endpoint direct provider client plan should be empty: %#v", plan)
	}
}

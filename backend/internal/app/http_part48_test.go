package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestProviderReviewAttemptLiveAdapterPlan(t *testing.T) {
	for _, tt := range []struct {
		name            string
		provider        string
		operation       string
		endpoint        string
		adapterName     string
		builderName     string
		clientKind      string
		executeMethod   string
		responseHandler string
		capability      string
		expectEmpty     bool
	}{
		{
			name:            "github branch ref",
			provider:        "github",
			operation:       "create_branch_ref",
			endpoint:        "github.create_branch_ref",
			adapterName:     "github_live_provider_review_adapter",
			builderName:     "build_redacted_branch_ref_request",
			clientKind:      "github_provider_review_api_client",
			executeMethod:   "execute_branch_ref_creation",
			responseHandler: "handle_branch_ref_response",
			capability:      "repository_ref_write",
		},
		{
			name:            "github starter files",
			provider:        "github",
			operation:       "commit_starter_files",
			endpoint:        "github.commit_files",
			adapterName:     "github_live_provider_review_adapter",
			builderName:     "build_redacted_file_batch_request",
			clientKind:      "github_provider_review_api_client",
			executeMethod:   "execute_starter_file_commit",
			responseHandler: "handle_commit_files_response",
			capability:      "repository_contents_write",
		},
		{
			name:            "gitea review request",
			provider:        "gitea",
			operation:       "open_review_request",
			endpoint:        "gitea.open_review",
			adapterName:     "gitea_live_provider_review_adapter",
			builderName:     "build_redacted_review_request",
			clientKind:      "gitea_provider_review_api_client",
			executeMethod:   "execute_review_request_open",
			responseHandler: "handle_review_request_response",
			capability:      "review_request_write",
		},
		{
			name:        "unknown provider",
			provider:    "raw",
			operation:   "create_branch_ref",
			endpoint:    "github.create_branch_ref",
			expectEmpty: true,
		},
		{
			name:        "provider endpoint mismatch",
			provider:    "github",
			operation:   "create_branch_ref",
			endpoint:    "gitea.create_branch_ref",
			expectEmpty: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan := providerReviewAttemptLiveAdapterPlan(tt.provider, tt.operation, tt.endpoint)
			if tt.expectEmpty {
				if len(plan) != 0 {
					t.Fatalf("providerReviewAttemptLiveAdapterPlan() = %#v, want empty", plan)
				}
				return
			}
			if plan["mode"] != "redacted_attempt_live_adapter_plan" ||
				plan["live_adapter_state"] != "blocked" ||
				plan["live_adapter_ready"] != false ||
				plan["live_adapter_ready_reason"] != "provider_review_live_adapter_not_implemented" ||
				plan["provider_type"] != tt.provider ||
				plan["operation_name"] != tt.operation ||
				plan["endpoint_key"] != tt.endpoint ||
				plan["adapter_name"] != tt.adapterName ||
				len(mapFromAny(plan["contract_plan"])) == 0 ||
				plan["adapter_interface_registered"] != true ||
				plan["live_adapter_registered"] != true ||
				plan["live_adapter_implemented"] != false ||
				plan["live_adapter_contract_registered"] != true ||
				plan["live_adapter_contract_implemented"] != false ||
				plan["requires_activation_plan"] != true ||
				plan["requires_attempt_claim"] != true ||
				plan["requires_execution_lock"] != true ||
				plan["requires_contract_plan"] != true ||
				plan["requires_provider_client"] != true ||
				plan["requires_request_builder"] != true ||
				plan["requires_execute_method"] != true ||
				plan["requires_response_handler"] != true ||
				plan["requires_transaction_handler"] != true ||
				plan["requires_mutation_arming"] != true ||
				plan["activation_plan_verified"] != false ||
				plan["attempt_claim_verified"] != false ||
				plan["execution_lock_verified"] != false ||
				plan["provider_client_constructed"] != false ||
				plan["request_built"] != false ||
				plan["execute_method_invoked"] != false ||
				plan["response_handler_invoked"] != false ||
				plan["transaction_recorded"] != false ||
				plan["provider_request_sent"] != false ||
				plan["external_call_made"] != false ||
				plan["provider_api_call_made"] != false ||
				plan["provider_api_mutation"] != "disabled" ||
				plan["request_body_included"] != false ||
				plan["response_body_included"] != false ||
				plan["headers_included"] != false ||
				plan["authorization_header_included"] != false ||
				plan["provider_url_included"] != false ||
				plan["idempotency_key_included"] != false ||
				plan["provider_request_id_included"] != false ||
				plan["contains_token"] != false ||
				plan["contains_provider_url"] != false ||
				plan["contains_repository_ref"] != false ||
				plan["contains_branch_name"] != false ||
				plan["contains_file_content"] != false ||
				plan["live_adapter_boundary_redacted"] != true {
				t.Fatalf("live adapter plan = %#v", plan)
			}
			methods := stringSliceFromAny(plan["live_adapter_required_methods"])
			if len(methods) != 6 ||
				methods[0] != "verify_activation" ||
				methods[3] != "send_provider_request" ||
				methods[5] != "record_attempt_transaction" {
				t.Fatalf("live adapter methods = %#v", methods)
			}
			interfaces := stringSliceFromAny(plan["live_adapter_required_interfaces"])
			if len(interfaces) != 5 ||
				interfaces[0] != "providerReviewAttemptAdapterRuntime" ||
				interfaces[4] != "providerReviewAttemptResponseHandler" {
				t.Fatalf("live adapter interfaces = %#v", interfaces)
			}
			suppressedFields := stringSliceFromAny(plan["live_adapter_suppressed_fields"])
			if len(suppressedFields) != 10 ||
				suppressedFields[0] != "provider_url" ||
				suppressedFields[9] != "lock_key" {
				t.Fatalf("live adapter suppressed fields = %#v", suppressedFields)
			}
			capabilities := stringSliceFromAny(plan["live_adapter_required_capabilities"])
			if len(capabilities) != 1 || capabilities[0] != tt.capability {
				t.Fatalf("live adapter capabilities = %#v", capabilities)
			}
			contractPlan := mapFromAny(plan["contract_plan"])
			if contractPlan["mode"] != "redacted_attempt_live_adapter_contract_plan" ||
				contractPlan["contract_state"] != "blocked" ||
				contractPlan["contract_ready"] != false ||
				contractPlan["contract_ready_reason"] != "provider_review_live_adapter_contract_not_armed" ||
				contractPlan["provider_type"] != tt.provider ||
				contractPlan["operation_name"] != tt.operation ||
				contractPlan["endpoint_key"] != tt.endpoint ||
				contractPlan["adapter_name"] != tt.adapterName ||
				contractPlan["http_method"] != providerReviewMethodForOperation(tt.operation) ||
				contractPlan["endpoint_path_template_key"] != providerReviewEndpointPathTemplateKeyForOperation(tt.provider, tt.operation) ||
				contractPlan["payload_shape"] != providerReviewPayloadShapeForOperation(tt.operation) ||
				contractPlan["auth_scheme"] != providerReviewAuthSchemeForProvider(tt.provider) ||
				contractPlan["builder_name"] != tt.builderName ||
				contractPlan["client_kind"] != tt.clientKind ||
				contractPlan["execute_method_name"] != tt.executeMethod ||
				contractPlan["response_handler_name"] != tt.responseHandler ||
				contractPlan["success_attempt_status"] != "completed" ||
				contractPlan["retry_attempt_status"] != "planned" ||
				contractPlan["failure_attempt_status"] != "failed" ||
				contractPlan["requires_activation_plan"] != true ||
				contractPlan["requires_attempt_claim"] != true ||
				contractPlan["requires_execution_lock"] != true ||
				contractPlan["requires_credential_binding"] != true ||
				contractPlan["requires_provider_client"] != true ||
				contractPlan["requires_request_builder"] != true ||
				contractPlan["requires_transport"] != true ||
				contractPlan["requires_response_handler"] != true ||
				contractPlan["requires_transaction_handler"] != true ||
				contractPlan["requires_mutation_arming"] != true ||
				contractPlan["contract_registered"] != true ||
				contractPlan["contract_implemented"] != false ||
				contractPlan["request_contract_materialized"] != false ||
				contractPlan["response_contract_materialized"] != false ||
				contractPlan["error_contract_materialized"] != false ||
				contractPlan["result_contract_materialized"] != false ||
				contractPlan["provider_request_sent"] != false ||
				contractPlan["external_call_made"] != false ||
				contractPlan["provider_api_call_made"] != false ||
				contractPlan["provider_api_mutation"] != "disabled" ||
				contractPlan["request_body_included"] != false ||
				contractPlan["response_body_included"] != false ||
				contractPlan["headers_included"] != false ||
				contractPlan["authorization_header_included"] != false ||
				contractPlan["provider_url_included"] != false ||
				contractPlan["idempotency_key_included"] != false ||
				contractPlan["provider_request_id_included"] != false ||
				contractPlan["contains_token"] != false ||
				contractPlan["contains_provider_url"] != false ||
				contractPlan["contains_repository_ref"] != false ||
				contractPlan["contains_branch_name"] != false ||
				contractPlan["contains_file_content"] != false ||
				contractPlan["live_adapter_contract_boundary_redacted"] != true {
				t.Fatalf("live adapter contract plan = %#v", contractPlan)
			}
			contractCapabilities := stringSliceFromAny(contractPlan["required_capabilities"])
			if len(contractCapabilities) != 1 || contractCapabilities[0] != tt.capability {
				t.Fatalf("live adapter contract capabilities = %#v", contractCapabilities)
			}
			contractInputs := stringSliceFromAny(contractPlan["contract_input_fields"])
			if len(contractInputs) != 8 ||
				contractInputs[0] != "activation_plan" ||
				contractInputs[7] != "mutation_arming" {
				t.Fatalf("live adapter contract inputs = %#v", contractInputs)
			}
			contractOutputs := stringSliceFromAny(contractPlan["contract_output_fields"])
			if len(contractOutputs) != 4 ||
				contractOutputs[0] != "attempt_status" ||
				contractOutputs[3] != "dependency_update_status" {
				t.Fatalf("live adapter contract outputs = %#v", contractOutputs)
			}
			contractErrors := stringSliceFromAny(contractPlan["contract_error_classes"])
			if len(contractErrors) != 5 ||
				contractErrors[0] != "retryable_provider_error" ||
				contractErrors[4] != "mutation_guard_error" {
				t.Fatalf("live adapter contract errors = %#v", contractErrors)
			}
			contractPersistedFields := stringSliceFromAny(contractPlan["contract_persisted_fields"])
			if len(contractPersistedFields) != 4 ||
				contractPersistedFields[0] != "attempt_status" ||
				contractPersistedFields[3] != "retry_class" {
				t.Fatalf("live adapter contract persisted fields = %#v", contractPersistedFields)
			}
			contractSuppressedFields := stringSliceFromAny(contractPlan["contract_suppressed_fields"])
			for _, field := range []string{"provider_url", "authorization_header", "token", "request_body", "response_body", "response_headers", "repository_ref", "branch_name", "file_content", "idempotency_key", "lock_key"} {
				if !slices.Contains(contractSuppressedFields, field) {
					t.Fatalf("live adapter contract suppressed fields missing %q: %#v", field, contractSuppressedFields)
				}
			}
			contractSequence := stringSliceFromAny(contractPlan["contract_sequence"])
			if len(contractSequence) != 6 ||
				contractSequence[0] != "verify_activation_contract" ||
				contractSequence[5] != "record_result_contract" {
				t.Fatalf("live adapter contract sequence = %#v", contractSequence)
			}
			contractBlockedReasons := stringSliceFromAny(contractPlan["blocked_reasons"])
			if len(contractBlockedReasons) != 3 ||
				contractBlockedReasons[0] != "provider_review_live_adapter_contract_not_armed" ||
				contractBlockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("live adapter contract blocked reasons = %#v", contractBlockedReasons)
			}
			blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
			if len(blockedReasons) != 3 ||
				blockedReasons[0] != "provider_review_live_adapter_not_implemented" ||
				blockedReasons[1] != "provider_review_adapter_activation_not_armed" ||
				blockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("live adapter blocked reasons = %#v", blockedReasons)
			}
			encoded, _ := json.Marshal(plan)
			for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("live adapter plan leaked %q: %s", leak, encoded)
				}
			}
		})
	}
}

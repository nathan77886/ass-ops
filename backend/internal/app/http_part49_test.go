package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderReviewAttemptAdapterRuntimePlan(t *testing.T) {
	for _, item := range []struct {
		name         string
		provider     string
		operation    string
		endpoint     string
		adapterKind  string
		builderName  string
		clientKind   string
		methodName   string
		authScheme   string
		capability   string
		handlerName  string
		templateKey  string
		payloadShape string
		wantNonEmpty bool
	}{
		{
			name:         "github branch ref selects github runtime",
			provider:     "github",
			operation:    "create_branch_ref",
			endpoint:     "github.create_branch_ref",
			adapterKind:  "github_provider_review_adapter",
			builderName:  "build_redacted_branch_ref_request",
			clientKind:   "github_provider_review_api_client",
			methodName:   "execute_branch_ref_creation",
			authScheme:   "bearer_token",
			capability:   "repository_ref_write",
			handlerName:  "handle_branch_ref_response",
			templateKey:  "github_git_refs_path_template",
			payloadShape: "ref_from_target_branch",
			wantNonEmpty: true,
		},
		{
			name:         "gitea review request selects gitea runtime",
			provider:     "gitea",
			operation:    "open_review_request",
			endpoint:     "gitea.open_review",
			adapterKind:  "gitea_provider_review_adapter",
			builderName:  "build_redacted_review_request",
			clientKind:   "gitea_provider_review_api_client",
			methodName:   "execute_review_request_open",
			authScheme:   "token",
			capability:   "review_request_write",
			handlerName:  "handle_review_request_response",
			templateKey:  "gitea_merge_request_path_template",
			payloadShape: "review_request",
			wantNonEmpty: true,
		},
		{
			name:         "github commit starter files selects github runtime",
			provider:     "github",
			operation:    "commit_starter_files",
			endpoint:     "github.commit_files",
			adapterKind:  "github_provider_review_adapter",
			builderName:  "build_redacted_file_batch_request",
			clientKind:   "github_provider_review_api_client",
			methodName:   "execute_starter_file_commit",
			authScheme:   "bearer_token",
			capability:   "repository_contents_write",
			handlerName:  "handle_commit_files_response",
			templateKey:  "github_repository_contents_path_template",
			payloadShape: "content_redacted_file_batch",
			wantNonEmpty: true,
		},
		{
			name:      "unknown provider returns empty plan",
			provider:  "raw_provider",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "unknown operation returns empty plan",
			provider:  "github",
			operation: "raw_operation",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "empty operation name returns empty plan",
			provider:  "github",
			operation: "",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "empty endpoint key returns empty plan",
			provider:  "github",
			operation: "create_branch_ref",
			endpoint:  "",
		},
		{
			name:      "provider endpoint mismatch returns empty plan",
			provider:  "github",
			operation: "create_branch_ref",
			endpoint:  "gitea.create_branch_ref",
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			plan := providerReviewAttemptAdapterRuntimePlan(item.provider, item.operation, item.endpoint)
			if !item.wantNonEmpty {
				if len(plan) != 0 {
					t.Fatalf("runtime plan should be empty: %#v", plan)
				}
				return
			}
			if plan["mode"] != "redacted_attempt_adapter_runtime_plan" ||
				plan["runtime_state"] != "blocked" ||
				plan["runtime_ready"] != false ||
				plan["runtime_ready_reason"] != "provider_review_adapter_runtime_not_armed" ||
				plan["provider_type"] != item.provider ||
				plan["adapter_kind"] != item.adapterKind ||
				plan["operation_name"] != item.operation ||
				plan["endpoint_key"] != item.endpoint ||
				plan["adapter_interface_registered"] != true ||
				plan["adapter_dispatch_registered"] != true ||
				plan["runtime_adapter_selected"] != true ||
				plan["operation_supported"] != true ||
				plan["live_adapter_implemented"] != false ||
				plan["provider_client_constructed"] != false ||
				len(mapFromAny(plan["provider_client_plan"])) == 0 ||
				plan["execute_method_bound"] != false ||
				len(mapFromAny(plan["execute_method_plan"])) == 0 ||
				plan["request_builder_bound"] != false ||
				len(mapFromAny(plan["request_builder_plan"])) == 0 ||
				plan["response_handler_bound"] != false ||
				len(mapFromAny(plan["response_handler_plan"])) == 0 ||
				plan["transaction_handler_bound"] != false ||
				plan["requires_provider_client"] != true ||
				plan["requires_request_builder"] != true ||
				plan["requires_response_handler"] != true ||
				plan["requires_transaction_handler"] != true ||
				plan["requires_mutation_arming"] != true ||
				plan["runtime_boundary_redacted"] != true ||
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
				t.Fatalf("runtime plan = %#v", plan)
			}
			methods := stringSliceFromAny(plan["required_runtime_methods"])
			if len(methods) != 4 ||
				methods[0] != "build_request" ||
				methods[1] != "send_provider_request" ||
				methods[2] != "handle_response" ||
				methods[3] != "record_attempt_transaction" {
				t.Fatalf("runtime methods = %#v", methods)
			}
			clientPlan := mapFromAny(plan["provider_client_plan"])
			if clientPlan["mode"] != "redacted_attempt_adapter_provider_client_plan" ||
				clientPlan["provider_client_state"] != "blocked" ||
				clientPlan["provider_client_ready"] != false ||
				clientPlan["provider_client_ready_reason"] != "provider_review_provider_client_not_armed" ||
				clientPlan["provider_type"] != item.provider ||
				clientPlan["operation_name"] != item.operation ||
				clientPlan["endpoint_key"] != item.endpoint ||
				clientPlan["client_kind"] != item.clientKind ||
				clientPlan["auth_scheme"] != item.authScheme ||
				clientPlan["base_url_source"] != "provider_account_api_base_url" ||
				clientPlan["credential_source_kind"] != "provider_account_token_env" ||
				clientPlan["timeout_seconds"] != 15 ||
				clientPlan["retry_policy"] != "retry_5xx_with_backoff" ||
				clientPlan["client_factory_interface_registered"] != true ||
				clientPlan["client_factory_registered"] != true ||
				clientPlan["client_implemented"] != false ||
				clientPlan["provider_client_constructed"] != false ||
				clientPlan["provider_account_resolved"] != false ||
				clientPlan["base_url_validated"] != false ||
				clientPlan["base_url_materialized"] != false ||
				clientPlan["token_env_allowed"] != false ||
				clientPlan["runtime_token_loaded"] != false ||
				clientPlan["authorization_header_materialized"] != false ||
				clientPlan["http_client_configured"] != false ||
				clientPlan["provider_client_boundary_redacted"] != true ||
				clientPlan["external_call_made"] != false ||
				clientPlan["provider_api_call_made"] != false ||
				clientPlan["provider_api_mutation"] != "disabled" ||
				clientPlan["base_url_included"] != false ||
				clientPlan["token_env_name_included"] != false ||
				clientPlan["token_value_included"] != false ||
				clientPlan["authorization_header_included"] != false ||
				clientPlan["provider_url_included"] != false ||
				clientPlan["request_body_included"] != false ||
				clientPlan["response_body_included"] != false ||
				clientPlan["headers_included"] != false ||
				clientPlan["contains_token"] != false ||
				clientPlan["contains_provider_url"] != false ||
				clientPlan["contains_repository_ref"] != false ||
				clientPlan["contains_branch_name"] != false ||
				clientPlan["contains_file_content"] != false {
				t.Fatalf("runtime provider client plan = %#v", clientPlan)
			}
			clientCapabilities := stringSliceFromAny(clientPlan["required_capabilities"])
			if len(clientCapabilities) != 1 || clientCapabilities[0] != item.capability {
				t.Fatalf("runtime provider client capabilities = %#v", clientCapabilities)
			}
			clientBlockedReasons := stringSliceFromAny(clientPlan["blocked_reasons"])
			if len(clientBlockedReasons) != 3 ||
				clientBlockedReasons[0] != "provider_review_provider_client_not_armed" ||
				clientBlockedReasons[1] != "provider_review_live_adapter_not_implemented" ||
				clientBlockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("runtime provider client blocked reasons = %#v", clientBlockedReasons)
			}
			executePlan := mapFromAny(plan["execute_method_plan"])
			if executePlan["mode"] != "redacted_attempt_adapter_execute_method_plan" ||
				executePlan["execute_method_state"] != "blocked" ||
				executePlan["execute_method_ready"] != false ||
				executePlan["execute_method_ready_reason"] != "provider_review_execute_method_not_armed" ||
				executePlan["provider_type"] != item.provider ||
				executePlan["operation_name"] != item.operation ||
				executePlan["endpoint_key"] != item.endpoint ||
				executePlan["method_name"] != item.methodName ||
				executePlan["http_method"] != providerReviewMethodForOperation(item.operation) ||
				executePlan["execute_method_interface_registered"] != true ||
				executePlan["execute_method_registered"] != true ||
				executePlan["execute_method_implemented"] != false ||
				executePlan["execute_method_bound"] != false ||
				executePlan["provider_client_constructed"] != false ||
				executePlan["request_materialized"] != false ||
				executePlan["provider_request_sent"] != false ||
				executePlan["response_handled"] != false ||
				executePlan["transaction_recorded"] != false ||
				executePlan["dependency_update_recorded"] != false ||
				executePlan["execute_method_boundary_redacted"] != true ||
				executePlan["external_call_made"] != false ||
				executePlan["provider_api_call_made"] != false ||
				executePlan["provider_api_mutation"] != "disabled" ||
				executePlan["request_body_included"] != false ||
				executePlan["response_body_included"] != false ||
				executePlan["headers_included"] != false ||
				executePlan["authorization_header_included"] != false ||
				executePlan["provider_url_included"] != false ||
				executePlan["idempotency_key_included"] != false ||
				executePlan["contains_token"] != false ||
				executePlan["contains_provider_url"] != false ||
				executePlan["contains_repository_ref"] != false ||
				executePlan["contains_branch_name"] != false ||
				executePlan["contains_file_content"] != false {
				t.Fatalf("runtime execute method plan = %#v", executePlan)
			}
			executeSequence := stringSliceFromAny(executePlan["execution_sequence"])
			if len(executeSequence) != 8 ||
				executeSequence[0] != "verify_attempt_claim" ||
				executeSequence[5] != "stage_provider_request_send" ||
				executeSequence[7] != "record_attempt_transaction" {
				t.Fatalf("runtime execute method sequence = %#v", executeSequence)
			}
			executeBlockedReasons := stringSliceFromAny(executePlan["blocked_reasons"])
			if len(executeBlockedReasons) != 3 ||
				executeBlockedReasons[0] != "provider_review_execute_method_not_armed" ||
				executeBlockedReasons[1] != "provider_review_live_adapter_not_implemented" ||
				executeBlockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("runtime execute method blocked reasons = %#v", executeBlockedReasons)
			}
			builderPlan := mapFromAny(plan["request_builder_plan"])
			if builderPlan["mode"] != "redacted_attempt_adapter_request_builder_plan" ||
				builderPlan["request_builder_state"] != "blocked" ||
				builderPlan["request_builder_ready"] != false ||
				builderPlan["request_builder_ready_reason"] != "provider_review_request_builder_not_armed" ||
				builderPlan["provider_type"] != item.provider ||
				builderPlan["operation_name"] != item.operation ||
				builderPlan["endpoint_key"] != item.endpoint ||
				builderPlan["builder_name"] != item.builderName ||
				builderPlan["endpoint_path_template_key"] != item.templateKey ||
				builderPlan["payload_shape"] != item.payloadShape ||
				builderPlan["builder_interface_registered"] != true ||
				builderPlan["builder_registered"] != true ||
				builderPlan["builder_implemented"] != false ||
				builderPlan["request_url_materialized"] != false ||
				builderPlan["request_body_materialized"] != false ||
				builderPlan["headers_materialized"] != false ||
				builderPlan["authorization_header_materialized"] != false ||
				builderPlan["provider_api_call_made"] != false ||
				builderPlan["provider_api_mutation"] != "disabled" ||
				builderPlan["contains_token"] != false ||
				builderPlan["contains_provider_url"] != false ||
				builderPlan["contains_repository_ref"] != false ||
				builderPlan["contains_branch_name"] != false ||
				builderPlan["contains_file_content"] != false ||
				builderPlan["request_builder_boundary_redacted"] != true {
				t.Fatalf("runtime request builder plan = %#v", builderPlan)
			}
			responseHandlerPlan := mapFromAny(plan["response_handler_plan"])
			if responseHandlerPlan["mode"] != "redacted_attempt_adapter_response_handler_plan" ||
				responseHandlerPlan["response_handler_state"] != "blocked" ||
				responseHandlerPlan["response_handler_ready"] != false ||
				responseHandlerPlan["response_handler_ready_reason"] != "provider_review_response_handler_not_armed" ||
				responseHandlerPlan["provider_type"] != item.provider ||
				responseHandlerPlan["operation_name"] != item.operation ||
				responseHandlerPlan["endpoint_key"] != item.endpoint ||
				responseHandlerPlan["handler_name"] != item.handlerName ||
				responseHandlerPlan["response_status"] != "pending" ||
				responseHandlerPlan["handler_interface_registered"] != true ||
				responseHandlerPlan["handler_registered"] != true ||
				responseHandlerPlan["handler_implemented"] != false ||
				responseHandlerPlan["provider_response_classified"] != false ||
				responseHandlerPlan["attempt_status_selected"] != false ||
				responseHandlerPlan["dependency_update_selected"] != false ||
				responseHandlerPlan["provider_request_id_recorded"] != false ||
				responseHandlerPlan["response_body_recorded"] != false ||
				responseHandlerPlan["response_headers_recorded"] != false ||
				responseHandlerPlan["provider_api_call_made"] != false ||
				responseHandlerPlan["provider_api_mutation"] != "disabled" ||
				responseHandlerPlan["response_body_included"] != false ||
				responseHandlerPlan["headers_included"] != false ||
				responseHandlerPlan["provider_request_id_included"] != false ||
				responseHandlerPlan["contains_token"] != false ||
				responseHandlerPlan["contains_provider_url"] != false ||
				responseHandlerPlan["contains_repository_ref"] != false ||
				responseHandlerPlan["contains_branch_name"] != false ||
				responseHandlerPlan["contains_file_content"] != false ||
				responseHandlerPlan["response_handler_boundary_redacted"] != true {
				t.Fatalf("runtime response handler plan = %#v", responseHandlerPlan)
			}
			blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
			if len(blockedReasons) != 3 ||
				blockedReasons[0] != "provider_review_live_adapter_not_implemented" ||
				blockedReasons[1] != "provider_review_adapter_runtime_not_armed" ||
				blockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("runtime blocked reasons = %#v", blockedReasons)
			}
			encoded, _ := json.Marshal(plan)
			for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("runtime plan leaked %q: %s", leak, encoded)
				}
			}
		})
	}
}

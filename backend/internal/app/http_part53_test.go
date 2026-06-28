package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderReviewAttemptAdapterResponseHandlerPlan(t *testing.T) {
	for _, item := range []struct {
		name            string
		provider        string
		operation       string
		endpoint        string
		handlerName     string
		unlockOperation string
		unlockStatus    string
		requiresUpdate  bool
		wantNonEmpty    bool
	}{
		{
			name:            "github branch ref handler",
			provider:        "github",
			operation:       "create_branch_ref",
			endpoint:        "github.create_branch_ref",
			handlerName:     "handle_branch_ref_response",
			unlockOperation: "commit_starter_files",
			unlockStatus:    "dependency_satisfied",
			requiresUpdate:  true,
			wantNonEmpty:    true,
		},
		{
			name:            "github commit starter files handler",
			provider:        "github",
			operation:       "commit_starter_files",
			endpoint:        "github.commit_files",
			handlerName:     "handle_commit_files_response",
			unlockOperation: "open_review_request",
			unlockStatus:    "dependency_satisfied",
			requiresUpdate:  true,
			wantNonEmpty:    true,
		},
		{
			name:         "gitea review request handler",
			provider:     "gitea",
			operation:    "open_review_request",
			endpoint:     "gitea.open_review",
			handlerName:  "handle_review_request_response",
			wantNonEmpty: true,
		},
		{
			name:            "gitea branch ref handler",
			provider:        "gitea",
			operation:       "create_branch_ref",
			endpoint:        "gitea.create_branch_ref",
			handlerName:     "handle_branch_ref_response",
			wantNonEmpty:    true,
			unlockOperation: "commit_starter_files",
			unlockStatus:    "dependency_satisfied",
			requiresUpdate:  true,
		},
		{
			name:      "unknown provider returns empty response handler plan",
			provider:  "raw_provider",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "empty operation returns empty response handler plan",
			provider:  "github",
			operation: "",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "mismatched endpoint returns empty response handler plan",
			provider:  "github",
			operation: "create_branch_ref",
			endpoint:  "gitea.create_branch_ref",
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			plan := providerReviewAttemptAdapterResponseHandlerPlan(item.provider, item.operation, item.endpoint)
			if !item.wantNonEmpty {
				if len(plan) != 0 {
					t.Fatalf("response handler plan should be empty: %#v", plan)
				}
				return
			}
			if plan["mode"] != "redacted_attempt_adapter_response_handler_plan" ||
				plan["response_handler_state"] != "blocked" ||
				plan["response_handler_ready"] != false ||
				plan["response_handler_ready_reason"] != "provider_review_response_handler_not_armed" ||
				plan["provider_type"] != item.provider ||
				plan["operation_name"] != item.operation ||
				plan["endpoint_key"] != item.endpoint ||
				plan["handler_name"] != item.handlerName ||
				plan["response_status"] != "pending" ||
				plan["success_attempt_status"] != "completed" ||
				plan["retry_attempt_status"] != "planned" ||
				plan["failure_attempt_status"] != "failed" ||
				plan["dependency_unlocks_operation"] != item.unlockOperation ||
				plan["dependency_update_status"] != item.unlockStatus ||
				plan["requires_response_diagnostics"] != true ||
				plan["requires_idempotency_ledger"] != true ||
				plan["requires_dependency_update"] != item.requiresUpdate ||
				plan["requires_transaction_handler"] != true ||
				plan["requires_mutation_arming"] != true ||
				plan["handler_interface_registered"] != true ||
				plan["handler_registered"] != true ||
				plan["handler_implemented"] != false ||
				plan["provider_response_classified"] != false ||
				plan["attempt_status_selected"] != false ||
				plan["dependency_update_selected"] != false ||
				plan["provider_request_id_recorded"] != false ||
				plan["response_body_recorded"] != false ||
				plan["response_headers_recorded"] != false ||
				plan["response_handler_boundary_redacted"] != true ||
				plan["external_call_made"] != false ||
				plan["provider_api_call_made"] != false ||
				plan["provider_api_mutation"] != "disabled" ||
				plan["response_body_included"] != false ||
				plan["headers_included"] != false ||
				plan["provider_request_id_included"] != false ||
				plan["contains_token"] != false ||
				plan["contains_provider_url"] != false ||
				plan["contains_repository_ref"] != false ||
				plan["contains_branch_name"] != false ||
				plan["contains_file_content"] != false {
				t.Fatalf("response handler plan = %#v", plan)
			}
			if successClasses := stringSliceFromAny(plan["expected_success_classes"]); len(successClasses) != 1 || successClasses[0] != "2xx" {
				t.Fatalf("response handler success classes = %#v", successClasses)
			}
			if retryClasses := stringSliceFromAny(plan["retryable_status_classes"]); len(retryClasses) != 1 || retryClasses[0] != "5xx" {
				t.Fatalf("response handler retry classes = %#v", retryClasses)
			}
			if failureClasses := stringSliceFromAny(plan["terminal_failure_status_classes"]); len(failureClasses) != 1 || failureClasses[0] != "4xx" {
				t.Fatalf("response handler failure classes = %#v", failureClasses)
			}
			blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
			if len(blockedReasons) != 3 ||
				blockedReasons[0] != "provider_review_response_handler_not_armed" ||
				blockedReasons[1] != "provider_review_live_adapter_not_implemented" ||
				blockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("response handler blocked reasons = %#v", blockedReasons)
			}
			encoded, _ := json.Marshal(plan)
			for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("response handler plan leaked %q: %s", leak, encoded)
				}
			}
		})
	}
}

func TestDisabledProviderReviewAttemptResponseHandlerRejectsMismatchedEndpoint(t *testing.T) {
	handler := disabledProviderReviewAttemptResponseHandler{handlerName: "handle_branch_ref_response"}
	plan := handler.BuildPlan(providerReviewAttemptResponseHandlerInput{
		ProviderType:  "github",
		OperationName: "create_branch_ref",
		EndpointKey:   "gitea.create_branch_ref",
	})
	if len(plan) != 0 {
		t.Fatalf("mismatched endpoint direct response handler plan should be empty: %#v", plan)
	}
}

func TestDisabledProviderReviewAttemptRequestBuilderRejectsMismatchedEndpoint(t *testing.T) {
	builder := disabledProviderReviewAttemptRequestBuilder{builderName: "build_redacted_branch_ref_request"}
	plan := builder.BuildPlan(providerReviewAttemptRequestBuilderInput{
		ProviderType:  "github",
		OperationName: "create_branch_ref",
		EndpointKey:   "gitea.create_branch_ref",
	})
	if len(plan) != 0 {
		t.Fatalf("mismatched endpoint direct builder plan should be empty: %#v", plan)
	}
}

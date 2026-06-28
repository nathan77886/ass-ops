package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestProviderReviewAttemptAdapterBuilderAndHandlerMatchOperation(t *testing.T) {
	for _, tt := range []struct {
		name            string
		operation       string
		payloadBuilder  string
		responseHandler string
		builderMatches  bool
		handlerMatches  bool
	}{
		{
			name:            "branch ref",
			operation:       "create_branch_ref",
			payloadBuilder:  "build_redacted_branch_ref_request",
			responseHandler: "handle_branch_ref_response",
			builderMatches:  true,
			handlerMatches:  true,
		},
		{
			name:            "starter files",
			operation:       "commit_starter_files",
			payloadBuilder:  "build_redacted_file_batch_request",
			responseHandler: "handle_commit_files_response",
			builderMatches:  true,
			handlerMatches:  true,
		},
		{
			name:            "review request",
			operation:       "open_review_request",
			payloadBuilder:  "build_redacted_review_request",
			responseHandler: "handle_review_request_response",
			builderMatches:  true,
			handlerMatches:  true,
		},
		{
			name:            "builder and handler mismatch",
			operation:       "commit_starter_files",
			payloadBuilder:  "build_redacted_branch_ref_request",
			responseHandler: "handle_branch_ref_response",
		},
		{
			name:            "generic sanitized defaults do not match concrete operation",
			operation:       "create_branch_ref",
			payloadBuilder:  "raw_builder",
			responseHandler: "raw_handler",
		},
		{
			name:            "generic sanitized defaults do not match commit operation",
			operation:       "commit_starter_files",
			payloadBuilder:  "raw_builder",
			responseHandler: "raw_handler",
		},
		{
			name:            "generic sanitized defaults do not match review operation",
			operation:       "open_review_request",
			payloadBuilder:  "raw_builder",
			responseHandler: "raw_handler",
		},
		{
			name:            "unknown operation never matches",
			operation:       "raw_operation",
			payloadBuilder:  "build_redacted_branch_ref_request",
			responseHandler: "handle_branch_ref_response",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := providerReviewAttemptPayloadBuilderMatchesOperation(tt.operation, tt.payloadBuilder); got != tt.builderMatches {
				t.Fatalf("providerReviewAttemptPayloadBuilderMatchesOperation(%q, %q) = %v, want %v", tt.operation, tt.payloadBuilder, got, tt.builderMatches)
			}
			if got := providerReviewAttemptResponseHandlerMatchesOperation(tt.operation, tt.responseHandler); got != tt.handlerMatches {
				t.Fatalf("providerReviewAttemptResponseHandlerMatchesOperation(%q, %q) = %v, want %v", tt.operation, tt.responseHandler, got, tt.handlerMatches)
			}
		})
	}
}

func TestProviderReviewAttemptBranchPolicyPlan(t *testing.T) {
	validOperation := map[string]any{
		"name":            "create_branch_ref",
		"endpoint_key":    "github.create_branch_ref",
		"operation_order": 10,
	}
	for _, tt := range []struct {
		name              string
		operation         map[string]any
		requestPlan       map[string]any
		wantEmpty         bool
		wantMetadataReady bool
	}{
		{name: "nil operation", operation: nil, wantEmpty: true},
		{name: "empty operation", operation: map[string]any{}, wantEmpty: true},
		{name: "invalid operation", operation: map[string]any{"name": "raw_operation", "endpoint_key": "github.create_branch_ref"}, wantEmpty: true},
		{name: "invalid endpoint", operation: map[string]any{"name": "create_branch_ref", "endpoint_key": "github.secret"}, wantEmpty: true},
		{name: "valid operation without request metadata", operation: validOperation, requestPlan: nil, wantMetadataReady: false},
		{name: "valid operation with request metadata", operation: validOperation, requestPlan: map[string]any{
			"mode":           providerReviewAttemptAdapterRequestMaterializationPlanMode,
			"operation_name": "create_branch_ref",
			"endpoint_key":   "github.create_branch_ref",
		}, wantMetadataReady: true},
		{name: "request metadata for different operation is not ready", operation: validOperation, requestPlan: map[string]any{
			"mode":           providerReviewAttemptAdapterRequestMaterializationPlanMode,
			"operation_name": "commit_starter_files",
			"endpoint_key":   "github.commit_files",
		}, wantMetadataReady: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := providerReviewAttemptBranchPolicyPlan(tt.operation, tt.requestPlan)
			if tt.wantEmpty {
				if len(got) != 0 {
					t.Fatalf("branch policy plan should be empty: %#v", got)
				}
				return
			}
			if got["mode"] != "redacted_attempt_branch_policy_plan" ||
				got["branch_policy_state"] != "blocked" ||
				got["branch_policy_ready"] != false ||
				got["branch_policy_ready_reason"] != "provider_branch_policy_not_armed" ||
				got["branch_policy_metadata_ready"] != tt.wantMetadataReady ||
				got["default_branch_direct_write_allowed"] != false ||
				got["protected_branch_direct_write_allowed"] != false ||
				got["starter_file_commit_to_default"] != false ||
				got["provider_api_call_made"] != false ||
				got["provider_api_mutation"] != "disabled" ||
				got["repository_ref_included"] != false ||
				got["branch_name_included"] != false ||
				got["protected_branch_rules_included"] != false ||
				got["contains_token"] != false ||
				got["contains_provider_url"] != false ||
				got["contains_repository_ref"] != false ||
				got["contains_branch_name"] != false ||
				got["contains_file_content"] != false ||
				got["branch_policy_boundary_redacted"] != true {
				t.Fatalf("branch policy plan = %#v", got)
			}
			summary := mapFromAny(got["branch_safety_summary"])
			if summary["mode"] != "redacted_provider_review_branch_safety_summary" ||
				summary["operation_name"] != "create_branch_ref" ||
				summary["operation_intent"] != "create_review_branch_ref_only" ||
				summary["guardrail_focus"] != "review_branch_must_not_replace_protected_default" ||
				summary["requires_existing_branch_replay_check"] != true ||
				summary["default_branch_direct_write_allowed"] != false ||
				summary["protected_branch_direct_write_allowed"] != false ||
				summary["provider_api_call_made"] != false ||
				summary["provider_api_mutation"] != "disabled" ||
				summary["repository_ref_included"] != false ||
				summary["branch_name_included"] != false ||
				summary["contains_token"] != false ||
				summary["contains_repository_ref"] != false ||
				summary["contains_branch_name"] != false ||
				summary["contains_file_content"] != false ||
				summary["summary_boundary_redacted"] != true {
				t.Fatalf("branch safety summary = %#v", summary)
			}
			for _, reason := range []string{
				"provider_branch_policy_not_armed",
				"protected_default_branch_direct_write_disabled",
				"provider_review_adapter_not_implemented",
				"provider_review_mutation_not_armed",
			} {
				if !slices.Contains(stringSliceFromAny(got["blocked_reasons"]), reason) {
					t.Fatalf("branch policy blocked reasons missing %q: %#v", reason, got["blocked_reasons"])
				}
			}
			encoded, _ := json.Marshal(got)
			for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "main", "file content", "Authorization"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("branch policy plan leaked %q: %s", leak, encoded)
				}
			}
		})
	}
}

func TestProviderReviewAttemptBranchSafetySummaryByOperation(t *testing.T) {
	tests := []struct {
		name            string
		operation       string
		wantIntent      string
		wantFocus       string
		wantReplayCheck bool
	}{
		{
			name:            "create review branch ref",
			operation:       "create_branch_ref",
			wantIntent:      "create_review_branch_ref_only",
			wantFocus:       "review_branch_must_not_replace_protected_default",
			wantReplayCheck: true,
		},
		{
			name:            "commit starter files",
			operation:       "commit_starter_files",
			wantIntent:      "commit_starter_files_to_review_branch_only",
			wantFocus:       "starter_file_commit_requires_review_branch",
			wantReplayCheck: false,
		},
		{
			name:            "open provider review",
			operation:       "open_review_request",
			wantIntent:      "open_provider_review_from_review_branch",
			wantFocus:       "operator_review_required_before_merge",
			wantReplayCheck: false,
		},
		{
			name:            "unknown operation stays blocked and redacted",
			operation:       "bad_op",
			wantIntent:      "",
			wantFocus:       "unknown_operation_blocked",
			wantReplayCheck: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := providerReviewAttemptBranchSafetySummary(tt.operation)
			wantOperation := safeProviderReviewAttemptOperationName(tt.operation)
			if got["mode"] != "redacted_provider_review_branch_safety_summary" ||
				got["operation_name"] != wantOperation ||
				got["operation_intent"] != tt.wantIntent ||
				got["guardrail_focus"] != tt.wantFocus ||
				got["requires_existing_branch_replay_check"] != tt.wantReplayCheck ||
				got["requires_review_branch"] != true ||
				got["requires_protected_branch_check"] != true ||
				got["requires_review_request_before_merge"] != true ||
				got["default_branch_direct_write_allowed"] != false ||
				got["protected_branch_direct_write_allowed"] != false ||
				got["starter_file_commit_to_default"] != false ||
				got["branch_ref_created"] != false ||
				got["commit_written"] != false ||
				got["review_request_created"] != false ||
				got["provider_api_call_made"] != false ||
				got["provider_api_mutation"] != "disabled" ||
				got["repository_ref_included"] != false ||
				got["branch_name_included"] != false ||
				got["protected_branch_rules_included"] != false ||
				got["contains_token"] != false ||
				got["contains_provider_url"] != false ||
				got["contains_repository_ref"] != false ||
				got["contains_branch_name"] != false ||
				got["contains_file_content"] != false ||
				got["summary_boundary_redacted"] != true {
				t.Fatalf("branch safety summary = %#v", got)
			}
			encoded, _ := json.Marshal(got)
			for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("branch safety summary leaked %q: %s", leak, encoded)
				}
			}
		})
	}
}

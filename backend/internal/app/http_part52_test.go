package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderReviewAttemptAdapterRequestBuilderPlan(t *testing.T) {
	for _, item := range []struct {
		name             string
		provider         string
		operation        string
		endpoint         string
		builderName      string
		method           string
		templateKey      string
		payloadShape     string
		requiresManifest bool
		wantNonEmpty     bool
	}{
		{
			name:         "github branch ref builder",
			provider:     "github",
			operation:    "create_branch_ref",
			endpoint:     "github.create_branch_ref",
			builderName:  "build_redacted_branch_ref_request",
			method:       "POST",
			templateKey:  "github_git_refs_path_template",
			payloadShape: "ref_from_target_branch",
			wantNonEmpty: true,
		},
		{
			name:             "github commit starter files builder",
			provider:         "github",
			operation:        "commit_starter_files",
			endpoint:         "github.commit_files",
			builderName:      "build_redacted_file_batch_request",
			method:           "PUT",
			templateKey:      "github_repository_contents_path_template",
			payloadShape:     "content_redacted_file_batch",
			requiresManifest: true,
			wantNonEmpty:     true,
		},
		{
			name:         "gitea review request builder",
			provider:     "gitea",
			operation:    "open_review_request",
			endpoint:     "gitea.open_review",
			builderName:  "build_redacted_review_request",
			method:       "POST",
			templateKey:  "gitea_merge_request_path_template",
			payloadShape: "review_request",
			wantNonEmpty: true,
		},
		{
			name:         "gitea branch ref builder",
			provider:     "gitea",
			operation:    "create_branch_ref",
			endpoint:     "gitea.create_branch_ref",
			builderName:  "build_redacted_branch_ref_request",
			method:       "POST",
			templateKey:  "gitea_git_refs_path_template",
			payloadShape: "ref_from_target_branch",
			wantNonEmpty: true,
		},
		{
			name:         "github review request builder",
			provider:     "github",
			operation:    "open_review_request",
			endpoint:     "github.open_review",
			builderName:  "build_redacted_review_request",
			method:       "POST",
			templateKey:  "github_pull_request_path_template",
			payloadShape: "review_request",
			wantNonEmpty: true,
		},
		{
			name:      "unknown provider returns empty builder plan",
			provider:  "raw_provider",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "empty operation returns empty builder plan",
			provider:  "github",
			operation: "",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "mismatched endpoint returns empty builder plan",
			provider:  "github",
			operation: "create_branch_ref",
			endpoint:  "gitea.create_branch_ref",
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			plan := providerReviewAttemptAdapterRequestBuilderPlan(item.provider, item.operation, item.endpoint)
			if !item.wantNonEmpty {
				if len(plan) != 0 {
					t.Fatalf("request builder plan should be empty: %#v", plan)
				}
				return
			}
			if plan["mode"] != "redacted_attempt_adapter_request_builder_plan" ||
				plan["request_builder_state"] != "blocked" ||
				plan["request_builder_ready"] != false ||
				plan["request_builder_ready_reason"] != "provider_review_request_builder_not_armed" ||
				plan["provider_type"] != item.provider ||
				plan["operation_name"] != item.operation ||
				plan["endpoint_key"] != item.endpoint ||
				plan["builder_name"] != item.builderName ||
				plan["method"] != item.method ||
				plan["endpoint_path_template_key"] != item.templateKey ||
				plan["payload_shape"] != item.payloadShape ||
				plan["requires_provider_repository_context"] != true ||
				plan["requires_redacted_payload_summary"] != true ||
				plan["requires_starter_file_manifest"] != item.requiresManifest ||
				plan["builder_interface_registered"] != true ||
				plan["builder_registered"] != true ||
				plan["builder_implemented"] != false ||
				plan["provider_repository_context_resolved"] != false ||
				plan["request_path_materialized"] != false ||
				plan["request_url_materialized"] != false ||
				plan["request_body_materialized"] != false ||
				plan["payload_materialized"] != false ||
				plan["headers_materialized"] != false ||
				plan["starter_file_manifest_materialized"] != false ||
				plan["authorization_header_materialized"] != false ||
				plan["request_builder_boundary_redacted"] != true ||
				plan["external_call_made"] != false ||
				plan["provider_api_call_made"] != false ||
				plan["provider_api_mutation"] != "disabled" ||
				plan["request_body_included"] != false ||
				plan["headers_included"] != false ||
				plan["provider_url_included"] != false ||
				plan["repository_ref_included"] != false ||
				plan["branch_name_included"] != false ||
				plan["file_content_included"] != false ||
				plan["idempotency_key_included"] != false ||
				plan["contains_token"] != false ||
				plan["contains_provider_url"] != false ||
				plan["contains_repository_ref"] != false ||
				plan["contains_branch_name"] != false ||
				plan["contains_file_content"] != false {
				t.Fatalf("request builder plan = %#v", plan)
			}
			blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
			if len(blockedReasons) != 3 ||
				blockedReasons[0] != "provider_review_request_builder_not_armed" ||
				blockedReasons[1] != "provider_review_live_adapter_not_implemented" ||
				blockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("request builder blocked reasons = %#v", blockedReasons)
			}
			encoded, _ := json.Marshal(plan)
			for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("request builder plan leaked %q: %s", leak, encoded)
				}
			}
		})
	}
}

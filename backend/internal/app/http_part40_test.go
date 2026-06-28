package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderReviewAttemptAdapterRequestMaterializationPlan(t *testing.T) {
	for _, item := range []struct {
		name                string
		provider            string
		operationName       string
		endpointKey         string
		order               int
		method              string
		endpointTemplateKey string
		payloadShape        string
		payloadBuilder      string
		requiresManifest    bool
		wantNonEmpty        bool
	}{
		{
			name:                "github branch ref request stays redacted",
			provider:            "github",
			operationName:       "create_branch_ref",
			endpointKey:         "github.create_branch_ref",
			order:               10,
			method:              "POST",
			endpointTemplateKey: "github_git_refs_path_template",
			payloadShape:        "ref_from_target_branch",
			payloadBuilder:      "build_redacted_branch_ref_request",
			requiresManifest:    false,
			wantNonEmpty:        true,
		},
		{
			name:                "github commit request requires file manifest without content",
			provider:            "github",
			operationName:       "commit_starter_files",
			endpointKey:         "github.commit_files",
			order:               20,
			method:              "PUT",
			endpointTemplateKey: "github_repository_contents_path_template",
			payloadShape:        "content_redacted_file_batch",
			payloadBuilder:      "build_redacted_file_batch_request",
			requiresManifest:    true,
			wantNonEmpty:        true,
		},
		{
			name:                "gitea review request uses merge request template",
			provider:            "gitea",
			operationName:       "open_review_request",
			endpointKey:         "gitea.open_review",
			order:               30,
			method:              "POST",
			endpointTemplateKey: "gitea_merge_request_path_template",
			payloadShape:        "review_request",
			payloadBuilder:      "build_redacted_review_request",
			requiresManifest:    false,
			wantNonEmpty:        true,
		},
		{
			name:                "gitea branch ref request stays redacted",
			provider:            "gitea",
			operationName:       "create_branch_ref",
			endpointKey:         "gitea.create_branch_ref",
			order:               10,
			method:              "POST",
			endpointTemplateKey: "gitea_git_refs_path_template",
			payloadShape:        "ref_from_target_branch",
			payloadBuilder:      "build_redacted_branch_ref_request",
			requiresManifest:    false,
			wantNonEmpty:        true,
		},
		{
			name:                "gitea commit request requires file manifest without content",
			provider:            "gitea",
			operationName:       "commit_starter_files",
			endpointKey:         "gitea.commit_files",
			order:               20,
			method:              "PUT",
			endpointTemplateKey: "gitea_repository_contents_path_template",
			payloadShape:        "content_redacted_file_batch",
			payloadBuilder:      "build_redacted_file_batch_request",
			requiresManifest:    true,
			wantNonEmpty:        true,
		},
		{
			name:                "gitea review request stays redacted",
			provider:            "gitea",
			operationName:       "open_review_request",
			endpointKey:         "gitea.open_review",
			order:               30,
			method:              "POST",
			endpointTemplateKey: "gitea_merge_request_path_template",
			payloadShape:        "review_request",
			payloadBuilder:      "build_redacted_review_request",
			requiresManifest:    false,
			wantNonEmpty:        true,
		},
		{
			name:          "unknown provider returns empty plan",
			provider:      "raw_provider",
			operationName: "create_branch_ref",
			endpointKey:   "github.create_branch_ref",
		},
		{
			name:          "unknown operation returns empty plan",
			provider:      "github",
			operationName: "raw_operation",
			endpointKey:   "github.create_branch_ref",
		},
		{
			name:          "operation endpoint mismatch returns empty plan",
			provider:      "github",
			operationName: "create_branch_ref",
			endpointKey:   "github.commit_files",
		},
		{
			name:          "cross provider endpoint mismatch returns empty plan",
			provider:      "github",
			operationName: "create_branch_ref",
			endpointKey:   "gitea.create_branch_ref",
		},
		{
			name:          "commit operation review endpoint mismatch returns empty plan",
			provider:      "github",
			operationName: "commit_starter_files",
			endpointKey:   "github.open_review",
		},
		{
			name:          "unknown endpoint returns empty plan",
			provider:      "github",
			operationName: "create_branch_ref",
			endpointKey:   "unknown.create_branch_ref",
		},
		{
			name:           "payload builder mismatch returns empty plan",
			provider:       "github",
			operationName:  "commit_starter_files",
			endpointKey:    "github.commit_files",
			payloadBuilder: "build_redacted_branch_ref_request",
		},
		{
			name:           "generic payload builder returns empty plan",
			provider:       "github",
			operationName:  "create_branch_ref",
			endpointKey:    "github.create_branch_ref",
			payloadBuilder: "build_redacted_provider_request",
		},
		{
			name:           "generic commit payload builder returns empty plan",
			provider:       "github",
			operationName:  "commit_starter_files",
			endpointKey:    "github.commit_files",
			payloadBuilder: "build_redacted_provider_request",
		},
		{
			name:           "generic review payload builder returns empty plan",
			provider:       "github",
			operationName:  "open_review_request",
			endpointKey:    "github.open_review",
			payloadBuilder: "build_redacted_provider_request",
		},
		{
			name:           "gitea payload builder mismatch returns empty plan",
			provider:       "gitea",
			operationName:  "commit_starter_files",
			endpointKey:    "gitea.commit_files",
			payloadBuilder: "build_redacted_branch_ref_request",
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			plan := providerReviewAttemptAdapterRequestMaterializationPlan(
				map[string]any{
					"name":            item.operationName,
					"endpoint_key":    item.endpointKey,
					"operation_order": item.order,
				},
				map[string]any{
					"payload_builder": item.payloadBuilder,
				},
				item.provider,
			)
			if !item.wantNonEmpty {
				if len(plan) != 0 {
					t.Fatalf("request materialization plan should be empty: %#v", plan)
				}
				return
			}
			if plan["mode"] != "redacted_attempt_adapter_request_materialization_plan" ||
				plan["request_materialization_state"] != "blocked" ||
				plan["request_materialization_ready"] != false ||
				plan["request_materialization_ready_reason"] != "provider_request_materialization_not_armed" ||
				plan["provider_type"] != item.provider ||
				plan["operation_name"] != item.operationName ||
				plan["endpoint_key"] != item.endpointKey ||
				plan["operation_order"] != item.order ||
				plan["method"] != item.method ||
				plan["endpoint_path_template_key"] != item.endpointTemplateKey ||
				plan["payload_shape"] != item.payloadShape ||
				plan["payload_builder"] != item.payloadBuilder ||
				plan["requires_request_builder"] != true ||
				plan["requires_provider_repository_context"] != true ||
				plan["requires_redacted_payload_summary"] != true ||
				plan["requires_starter_file_manifest"] != item.requiresManifest ||
				plan["request_builder_implemented"] != false ||
				plan["provider_repository_context_resolved"] != false ||
				plan["request_path_materialized"] != false ||
				plan["request_url_materialized"] != false ||
				plan["request_body_materialized"] != false ||
				plan["payload_materialized"] != false ||
				plan["headers_materialized"] != false ||
				plan["starter_file_manifest_materialized"] != false ||
				plan["authorization_header_materialized"] != false ||
				plan["external_call_made"] != false ||
				plan["provider_api_call_made"] != false ||
				plan["provider_api_mutation"] != "disabled" ||
				plan["request_body_included"] != false ||
				plan["headers_included"] != false ||
				plan["provider_url_included"] != false ||
				plan["repository_ref_included"] != false ||
				plan["branch_name_included"] != false ||
				plan["file_content_included"] != false ||
				plan["contains_token"] != false ||
				plan["contains_provider_url"] != false ||
				plan["contains_repository_ref"] != false ||
				plan["contains_branch_name"] != false ||
				plan["contains_file_content"] != false ||
				plan["request_materialization_boundary_redacted"] != true {
				t.Fatalf("request materialization plan = %#v", plan)
			}
			blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
			if len(blockedReasons) != 3 ||
				blockedReasons[0] != "provider_request_not_materialized" ||
				blockedReasons[1] != "provider_review_adapter_not_implemented" ||
				blockedReasons[2] != "provider_review_mutation_not_armed" {
				t.Fatalf("request materialization blocked reasons = %#v", blockedReasons)
			}
			encoded, _ := json.Marshal(plan)
			for _, leak := range []string{"https://", "api.github.example.test", "secret-token", "secret-repo", "feature/secret", "file content"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("request materialization plan leaked %q: %s", leak, encoded)
				}
			}
		})
	}
	t.Run("nil request summary returns empty plan", func(t *testing.T) {
		got := providerReviewAttemptAdapterRequestMaterializationPlan(
			map[string]any{
				"name":         "create_branch_ref",
				"endpoint_key": "github.create_branch_ref",
			},
			nil,
			"github",
		)
		if len(got) != 0 {
			t.Fatalf("nil request summary materialization plan should be empty: %#v", got)
		}
	})
	t.Run("empty request summary returns empty plan", func(t *testing.T) {
		got := providerReviewAttemptAdapterRequestMaterializationPlan(
			map[string]any{
				"name":         "create_branch_ref",
				"endpoint_key": "github.create_branch_ref",
			},
			map[string]any{},
			"github",
		)
		if len(got) != 0 {
			t.Fatalf("empty request summary materialization plan should be empty: %#v", got)
		}
	})
}

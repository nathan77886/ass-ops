package app

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"
)

func TestProviderReviewAttemptAdapterRequestEnvelopePlan(t *testing.T) {
	for _, item := range []struct {
		name        string
		provider    string
		operation   string
		endpoint    string
		method      string
		payload     string
		builder     string
		templateKey string
		authScheme  string
	}{
		{
			name:        "github branch ref envelope",
			provider:    "github",
			operation:   "create_branch_ref",
			endpoint:    "github.create_branch_ref",
			method:      "POST",
			payload:     "ref_from_target_branch",
			builder:     "build_redacted_branch_ref_request",
			templateKey: "github_git_refs_path_template",
			authScheme:  "bearer_token",
		},
		{
			name:        "github commit starter files envelope",
			provider:    "github",
			operation:   "commit_starter_files",
			endpoint:    "github.commit_files",
			method:      "PUT",
			payload:     "content_redacted_file_batch",
			builder:     "build_redacted_file_batch_request",
			templateKey: "github_repository_contents_path_template",
			authScheme:  "bearer_token",
		},
		{
			name:        "gitea review request envelope",
			provider:    "gitea",
			operation:   "open_review_request",
			endpoint:    "gitea.open_review",
			method:      "POST",
			payload:     "review_request",
			builder:     "build_redacted_review_request",
			templateKey: "gitea_merge_request_path_template",
			authScheme:  "token",
		},
	} {
		t.Run(item.name, func(t *testing.T) {
			operation := map[string]any{
				"name":            item.operation,
				"endpoint_key":    item.endpoint,
				"operation_order": 10,
			}
			requestPlan := providerReviewAttemptAdapterRequestMaterializationPlan(operation, map[string]any{
				"payload_builder": item.builder,
			}, item.provider)
			branchPolicyPlan := providerReviewAttemptBranchPolicyPlan(operation, requestPlan)
			credentialPlan := providerReviewAttemptAdapterCredentialBindingPlan(item.provider, item.operation)
			transportPlan := providerReviewAttemptAdapterTransportPlan(item.provider, item.operation)
			plan := providerReviewAttemptAdapterRequestEnvelopePlan(item.provider, item.operation, item.endpoint, requestPlan, branchPolicyPlan, credentialPlan, transportPlan)
			if plan["mode"] != "redacted_attempt_adapter_request_envelope_plan" ||
				plan["envelope_state"] != "blocked" ||
				plan["envelope_ready"] != false ||
				plan["envelope_ready_reason"] != "provider_review_request_envelope_not_armed" ||
				plan["envelope_contract_ready"] != true ||
				plan["envelope_metadata_ready"] != false ||
				plan["provider_type"] != item.provider ||
				plan["operation_name"] != item.operation ||
				plan["endpoint_key"] != item.endpoint ||
				plan["method"] != item.method ||
				plan["payload_shape"] != item.payload ||
				plan["payload_builder"] != item.builder ||
				plan["endpoint_path_template_key"] != item.templateKey ||
				plan["auth_scheme"] != item.authScheme ||
				plan["request_materialization_contract_ready"] != true ||
				plan["request_materialization_ready"] != false ||
				plan["branch_policy_contract_ready"] != true ||
				plan["branch_policy_metadata_ready"] != true ||
				plan["credential_binding_contract_ready"] != true ||
				plan["credential_binding_ready"] != false ||
				plan["transport_contract_ready"] != true ||
				plan["transport_metadata_ready"] != true ||
				plan["requires_request_materialization"] != true ||
				plan["requires_branch_policy"] != true ||
				plan["requires_credential_binding"] != true ||
				plan["requires_transport_metadata"] != true ||
				plan["requires_mutation_arming"] != true ||
				plan["request_path_materialized"] != false ||
				plan["request_url_materialized"] != false ||
				plan["request_body_materialized"] != false ||
				plan["headers_materialized"] != false ||
				plan["authorization_header_materialized"] != false ||
				plan["idempotency_metadata_materialized"] != false ||
				plan["protected_branch_policy_verified"] != false ||
				plan["token_env_bound"] != false ||
				plan["provider_request_sent"] != false ||
				plan["external_call_made"] != false ||
				plan["provider_api_call_made"] != false ||
				plan["provider_api_mutation"] != "disabled" ||
				plan["request_body_included"] != false ||
				plan["headers_included"] != false ||
				plan["authorization_header_included"] != false ||
				plan["provider_url_included"] != false ||
				plan["repository_ref_included"] != false ||
				plan["branch_name_included"] != false ||
				plan["file_content_included"] != false ||
				plan["idempotency_key_included"] != false ||
				plan["contains_token"] != false ||
				plan["contains_provider_url"] != false ||
				plan["contains_repository_ref"] != false ||
				plan["contains_branch_name"] != false ||
				plan["contains_file_content"] != false ||
				plan["request_envelope_boundary_redacted"] != true {
				t.Fatalf("request envelope plan = %#v", plan)
			}
			safetySummary := mapFromAny(plan["branch_safety_summary"])
			if safetySummary["mode"] != "redacted_provider_review_branch_safety_summary" ||
				safetySummary["operation_name"] != item.operation ||
				safetySummary["contains_branch_name"] != false ||
				safetySummary["contains_repository_ref"] != false ||
				safetySummary["contains_file_content"] != false ||
				safetySummary["summary_boundary_redacted"] != true {
				t.Fatalf("request envelope branch safety summary = %#v", safetySummary)
			}
			sequence := stringSliceFromAny(plan["request_envelope_sequence"])
			if len(sequence) != 5 ||
				sequence[0] != "verify_request_materialization" ||
				sequence[1] != "verify_branch_policy" ||
				sequence[2] != "bind_credential_metadata" ||
				sequence[3] != "verify_transport_metadata" ||
				sequence[4] != "stage_redacted_request_envelope" {
				t.Fatalf("request envelope sequence = %#v", sequence)
			}
			suppressedFields := stringSliceFromAny(plan["request_envelope_suppressed_fields"])
			if len(suppressedFields) != 9 ||
				suppressedFields[0] != "provider_url" ||
				suppressedFields[1] != "authorization_header" ||
				suppressedFields[8] != "idempotency_key" {
				t.Fatalf("request envelope suppressed fields = %#v", suppressedFields)
			}
			blockedReasons := stringSliceFromAny(plan["blocked_reasons"])
			if len(blockedReasons) != 4 ||
				blockedReasons[0] != "provider_review_request_envelope_not_armed" ||
				blockedReasons[1] != "provider_request_not_materialized" ||
				blockedReasons[2] != "provider_credential_runtime_binding_not_armed" ||
				blockedReasons[3] != "provider_review_mutation_not_armed" {
				t.Fatalf("request envelope blocked reasons = %#v", blockedReasons)
			}
			encoded, _ := json.Marshal(plan)
			for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization"} {
				if strings.Contains(string(encoded), leak) {
					t.Fatalf("request envelope plan leaked %q: %s", leak, encoded)
				}
			}
		})
	}

	operation := map[string]any{
		"name":            "create_branch_ref",
		"endpoint_key":    "github.create_branch_ref",
		"operation_order": 10,
	}
	requestPlan := providerReviewAttemptAdapterRequestMaterializationPlan(operation, map[string]any{
		"payload_builder": "build_redacted_branch_ref_request",
	}, "github")
	branchPolicyPlan := providerReviewAttemptBranchPolicyPlan(operation, requestPlan)
	credentialPlan := providerReviewAttemptAdapterCredentialBindingPlan("github", "create_branch_ref")
	transportPlan := providerReviewAttemptAdapterTransportPlan("github", "create_branch_ref")

	if got := providerReviewAttemptAdapterRequestEnvelopePlan("raw_provider", "create_branch_ref", "github.create_branch_ref", requestPlan, branchPolicyPlan, credentialPlan, transportPlan); len(got) != 0 {
		t.Fatalf("unknown provider request envelope plan should be empty: %#v", got)
	}
	if got := providerReviewAttemptAdapterRequestEnvelopePlan("github", "raw_operation", "github.create_branch_ref", requestPlan, branchPolicyPlan, credentialPlan, transportPlan); len(got) != 0 {
		t.Fatalf("unknown operation request envelope plan should be empty: %#v", got)
	}
	if got := providerReviewAttemptAdapterRequestEnvelopePlan("github", "create_branch_ref", "gitea.create_branch_ref", requestPlan, branchPolicyPlan, credentialPlan, transportPlan); len(got) != 0 {
		t.Fatalf("mismatched endpoint request envelope plan should be empty: %#v", got)
	}
	mismatchedRequestPlan := providerReviewAttemptAdapterRequestEnvelopePlan("github", "create_branch_ref", "github.create_branch_ref", map[string]any{
		"mode":           providerReviewAttemptAdapterRequestMaterializationPlanMode,
		"operation_name": "commit_starter_files",
		"endpoint_key":   "github.commit_files",
	}, branchPolicyPlan, credentialPlan, transportPlan)
	if mismatchedRequestPlan["envelope_contract_ready"] != false ||
		mismatchedRequestPlan["request_materialization_contract_ready"] != false ||
		mismatchedRequestPlan["branch_policy_metadata_ready"] != true ||
		mismatchedRequestPlan["credential_binding_contract_ready"] != true ||
		mismatchedRequestPlan["transport_metadata_ready"] != true {
		t.Fatalf("mismatched request envelope contract = %#v", mismatchedRequestPlan)
	}
	branchPolicyNotReadyPlan := maps.Clone(branchPolicyPlan)
	branchPolicyNotReadyPlan["branch_policy_metadata_ready"] = false
	branchPolicyNotReadyEnvelope := providerReviewAttemptAdapterRequestEnvelopePlan("github", "create_branch_ref", "github.create_branch_ref", requestPlan, branchPolicyNotReadyPlan, credentialPlan, transportPlan)
	if branchPolicyNotReadyEnvelope["envelope_contract_ready"] != true ||
		branchPolicyNotReadyEnvelope["branch_policy_contract_ready"] != true ||
		branchPolicyNotReadyEnvelope["branch_policy_metadata_ready"] != false {
		t.Fatalf("branch-policy-not-ready request envelope contract = %#v", branchPolicyNotReadyEnvelope)
	}
	transportNotReadyPlan := maps.Clone(transportPlan)
	transportNotReadyPlan["transport_ready"] = false
	transportNotReadyEnvelope := providerReviewAttemptAdapterRequestEnvelopePlan("github", "create_branch_ref", "github.create_branch_ref", requestPlan, branchPolicyPlan, credentialPlan, transportNotReadyPlan)
	if transportNotReadyEnvelope["envelope_contract_ready"] != true ||
		transportNotReadyEnvelope["transport_contract_ready"] != true ||
		transportNotReadyEnvelope["transport_metadata_ready"] != false {
		t.Fatalf("transport-not-ready request envelope contract = %#v", transportNotReadyEnvelope)
	}
	nilRequestEnvelope := providerReviewAttemptAdapterRequestEnvelopePlan("github", "create_branch_ref", "github.create_branch_ref", nil, branchPolicyPlan, credentialPlan, transportPlan)
	if nilRequestEnvelope["envelope_contract_ready"] != false ||
		nilRequestEnvelope["request_materialization_contract_ready"] != false ||
		nilRequestEnvelope["request_materialization_ready"] != false {
		t.Fatalf("nil request request envelope contract = %#v", nilRequestEnvelope)
	}
	nilCredentialEnvelope := providerReviewAttemptAdapterRequestEnvelopePlan("github", "create_branch_ref", "github.create_branch_ref", requestPlan, branchPolicyPlan, nil, transportPlan)
	if nilCredentialEnvelope["envelope_contract_ready"] != false ||
		nilCredentialEnvelope["credential_binding_contract_ready"] != false ||
		nilCredentialEnvelope["credential_binding_ready"] != false {
		t.Fatalf("nil credential request envelope contract = %#v", nilCredentialEnvelope)
	}
	branchPolicyPlan["branch_safety_summary"] = map[string]any{
		"operation_name":       "raw_operation",
		"contains_branch_name": true,
	}
	redactedPlan := providerReviewAttemptAdapterRequestEnvelopePlan("github", "create_branch_ref", "github.create_branch_ref", requestPlan, branchPolicyPlan, credentialPlan, transportPlan)
	if safetySummary := mapFromAny(redactedPlan["branch_safety_summary"]); len(safetySummary) != 0 {
		t.Fatalf("raw branch safety summary should be rejected: %#v", safetySummary)
	}
	encoded, _ := json.Marshal(redactedPlan)
	for _, leak := range []string{"raw_operation", "contains_branch_name\":true"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("request envelope leaked raw branch safety summary value %q: %s", leak, encoded)
		}
	}
}

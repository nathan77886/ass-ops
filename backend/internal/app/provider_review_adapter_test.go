package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderReviewAttemptAdapterSurfaceConsistency(t *testing.T) {
	for _, tt := range []struct {
		name             string
		provider         string
		operation        string
		endpoint         string
		adapterKind      string
		clientKind       string
		authScheme       string
		builderName      string
		executeMethod    string
		responseHandler  string
		capability       string
		templateKey      string
		payloadShape     string
		httpMethod       string
		unlockOperation  string
		requiresManifest bool
	}{
		{
			name:            "github branch ref",
			provider:        "github",
			operation:       "create_branch_ref",
			endpoint:        "github.create_branch_ref",
			adapterKind:     "github_provider_review_adapter",
			clientKind:      "github_provider_review_api_client",
			authScheme:      "bearer_token",
			builderName:     "build_redacted_branch_ref_request",
			executeMethod:   "execute_branch_ref_creation",
			responseHandler: "handle_branch_ref_response",
			capability:      "repository_ref_write",
			templateKey:     "github_git_refs_path_template",
			payloadShape:    "ref_from_target_branch",
			httpMethod:      "POST",
			unlockOperation: "commit_starter_files",
		},
		{
			name:             "github starter file commit",
			provider:         "github",
			operation:        "commit_starter_files",
			endpoint:         "github.commit_files",
			adapterKind:      "github_provider_review_adapter",
			clientKind:       "github_provider_review_api_client",
			authScheme:       "bearer_token",
			builderName:      "build_redacted_commit_files_request",
			executeMethod:    "execute_starter_file_commit",
			responseHandler:  "handle_commit_files_response",
			capability:       "repository_contents_write",
			templateKey:      "github_repository_contents_path_template",
			payloadShape:     "content_redacted_file_batch",
			httpMethod:       "PUT",
			unlockOperation:  "open_review_request",
			requiresManifest: true,
		},
		{
			name:            "github review request",
			provider:        "github",
			operation:       "open_review_request",
			endpoint:        "github.open_review",
			adapterKind:     "github_provider_review_adapter",
			clientKind:      "github_provider_review_api_client",
			authScheme:      "bearer_token",
			builderName:     "build_redacted_review_request",
			executeMethod:   "execute_review_request_open",
			responseHandler: "handle_review_request_response",
			capability:      "review_request_write",
			templateKey:     "github_pull_request_path_template",
			payloadShape:    "review_request",
			httpMethod:      "POST",
		},
		{
			name:            "gitea branch ref",
			provider:        "gitea",
			operation:       "create_branch_ref",
			endpoint:        "gitea.create_branch_ref",
			adapterKind:     "gitea_provider_review_adapter",
			clientKind:      "gitea_provider_review_api_client",
			authScheme:      "token",
			builderName:     "build_redacted_branch_ref_request",
			executeMethod:   "execute_branch_ref_creation",
			responseHandler: "handle_branch_ref_response",
			capability:      "repository_ref_write",
			templateKey:     "gitea_git_refs_path_template",
			payloadShape:    "ref_from_target_branch",
			httpMethod:      "POST",
			unlockOperation: "commit_starter_files",
		},
		{
			name:             "gitea starter file commit",
			provider:         "gitea",
			operation:        "commit_starter_files",
			endpoint:         "gitea.commit_files",
			adapterKind:      "gitea_provider_review_adapter",
			clientKind:       "gitea_provider_review_api_client",
			authScheme:       "token",
			builderName:      "build_redacted_commit_files_request",
			executeMethod:    "execute_starter_file_commit",
			responseHandler:  "handle_commit_files_response",
			capability:       "repository_contents_write",
			templateKey:      "gitea_repository_contents_path_template",
			payloadShape:     "content_redacted_file_batch",
			httpMethod:       "PUT",
			unlockOperation:  "open_review_request",
			requiresManifest: true,
		},
		{
			name:            "gitea review request",
			provider:        "gitea",
			operation:       "open_review_request",
			endpoint:        "gitea.open_review",
			adapterKind:     "gitea_provider_review_adapter",
			clientKind:      "gitea_provider_review_api_client",
			authScheme:      "token",
			builderName:     "build_redacted_review_request",
			executeMethod:   "execute_review_request_open",
			responseHandler: "handle_review_request_response",
			capability:      "review_request_write",
			templateKey:     "gitea_merge_request_path_template",
			payloadShape:    "review_request",
			httpMethod:      "POST",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runtimePlan := providerReviewAttemptAdapterRuntimePlan(tt.provider, tt.operation, tt.endpoint)
			builderPlan := providerReviewAttemptAdapterRequestBuilderPlan(tt.provider, tt.operation, tt.endpoint)
			clientPlan := providerReviewAttemptAdapterProviderClientPlan(tt.provider, tt.operation, tt.endpoint)
			executePlan := providerReviewAttemptAdapterExecuteMethodPlan(tt.provider, tt.operation, tt.endpoint)
			handlerPlan := providerReviewAttemptAdapterResponseHandlerPlan(tt.provider, tt.operation, tt.endpoint)
			liveAdapterPlan := providerReviewAttemptLiveAdapterPlan(tt.provider, tt.operation, tt.endpoint)
			contractPlan := mapFromAny(liveAdapterPlan["contract_plan"])

			if runtimePlan["provider_type"] != tt.provider ||
				runtimePlan["adapter_kind"] != tt.adapterKind ||
				runtimePlan["operation_name"] != tt.operation ||
				runtimePlan["endpoint_key"] != tt.endpoint ||
				runtimePlan["operation_supported"] != true ||
				runtimePlan["provider_api_mutation"] != "disabled" {
				t.Fatalf("runtime plan inconsistent: %#v", runtimePlan)
			}
			if builderPlan["provider_type"] != tt.provider ||
				builderPlan["operation_name"] != tt.operation ||
				builderPlan["endpoint_key"] != tt.endpoint ||
				builderPlan["builder_name"] != tt.builderName ||
				builderPlan["method"] != tt.httpMethod ||
				builderPlan["endpoint_path_template_key"] != tt.templateKey ||
				builderPlan["payload_shape"] != tt.payloadShape ||
				builderPlan["requires_starter_file_manifest"] != tt.requiresManifest ||
				builderPlan["provider_api_mutation"] != "disabled" {
				t.Fatalf("request builder plan inconsistent: %#v", builderPlan)
			}
			if clientPlan["provider_type"] != tt.provider ||
				clientPlan["operation_name"] != tt.operation ||
				clientPlan["endpoint_key"] != tt.endpoint ||
				clientPlan["client_kind"] != tt.clientKind ||
				clientPlan["auth_scheme"] != tt.authScheme ||
				clientPlan["provider_api_mutation"] != "disabled" {
				t.Fatalf("provider client plan inconsistent: %#v", clientPlan)
			}
			if executePlan["provider_type"] != tt.provider ||
				executePlan["operation_name"] != tt.operation ||
				executePlan["endpoint_key"] != tt.endpoint ||
				executePlan["method_name"] != tt.executeMethod ||
				executePlan["http_method"] != tt.httpMethod ||
				executePlan["provider_api_mutation"] != "disabled" {
				t.Fatalf("execute method plan inconsistent: %#v", executePlan)
			}
			if handlerPlan["provider_type"] != tt.provider ||
				handlerPlan["operation_name"] != tt.operation ||
				handlerPlan["endpoint_key"] != tt.endpoint ||
				handlerPlan["handler_name"] != tt.responseHandler ||
				handlerPlan["dependency_unlocks_operation"] != tt.unlockOperation ||
				handlerPlan["provider_api_mutation"] != "disabled" {
				t.Fatalf("response handler plan inconsistent: %#v", handlerPlan)
			}
			if liveAdapterPlan["provider_type"] != tt.provider ||
				liveAdapterPlan["operation_name"] != tt.operation ||
				liveAdapterPlan["endpoint_key"] != tt.endpoint ||
				liveAdapterPlan["live_adapter_registered"] != true ||
				liveAdapterPlan["live_adapter_implemented"] != false ||
				liveAdapterPlan["provider_api_mutation"] != "disabled" {
				t.Fatalf("live adapter plan inconsistent: %#v", liveAdapterPlan)
			}
			if contractPlan["provider_type"] != tt.provider ||
				contractPlan["operation_name"] != tt.operation ||
				contractPlan["endpoint_key"] != tt.endpoint ||
				contractPlan["adapter_name"] != liveAdapterPlan["adapter_name"] ||
				contractPlan["http_method"] != tt.httpMethod ||
				contractPlan["endpoint_path_template_key"] != tt.templateKey ||
				contractPlan["payload_shape"] != tt.payloadShape ||
				contractPlan["auth_scheme"] != tt.authScheme ||
				contractPlan["builder_name"] != tt.builderName ||
				contractPlan["client_kind"] != tt.clientKind ||
				contractPlan["execute_method_name"] != tt.executeMethod ||
				contractPlan["response_handler_name"] != tt.responseHandler ||
				contractPlan["dependency_unlocks_operation"] != tt.unlockOperation ||
				contractPlan["provider_api_mutation"] != "disabled" {
				t.Fatalf("live adapter contract plan inconsistent: %#v", contractPlan)
			}
			contractCapabilities := stringSliceFromAny(contractPlan["required_capabilities"])
			if len(contractCapabilities) != 1 || contractCapabilities[0] != tt.capability {
				t.Fatalf("live adapter contract capabilities mismatch for %s/%s: %#v", tt.provider, tt.operation, contractCapabilities)
			}

			for name, plan := range map[string]map[string]any{
				"runtime":       runtimePlan,
				"builder":       builderPlan,
				"client":        clientPlan,
				"execute":       executePlan,
				"handler":       handlerPlan,
				"live_adapter":  liveAdapterPlan,
				"live_contract": contractPlan,
			} {
				encoded, _ := json.Marshal(plan)
				for _, leak := range []string{"https://", "secret-token", "secret-repo", "feature/secret", "file content", "Authorization", "ASSOPS_TEMPLATE_PROVIDER_TOKEN"} {
					if strings.Contains(string(encoded), leak) {
						t.Fatalf("%s plan leaked %q: %s", name, leak, encoded)
					}
				}
			}

			for _, capability := range [][]string{
				stringSliceFromAny(clientPlan["required_capabilities"]),
				stringSliceFromAny(liveAdapterPlan["live_adapter_required_capabilities"]),
			} {
				if len(capability) != 1 || capability[0] != tt.capability {
					t.Fatalf("capability mismatch for %s/%s: %#v", tt.provider, tt.operation, capability)
				}
			}
		})
	}
}

func TestProviderReviewAttemptAdapterSurfaceRejectsInvalidCombinations(t *testing.T) {
	for _, tt := range []struct {
		name      string
		provider  string
		operation string
		endpoint  string
	}{
		{name: "unknown provider", provider: "raw_provider", operation: "create_branch_ref", endpoint: "github.create_branch_ref"},
		{name: "unknown operation", provider: "github", operation: "raw_operation", endpoint: "github.create_branch_ref"},
		{name: "mismatched endpoint provider", provider: "github", operation: "create_branch_ref", endpoint: "gitea.create_branch_ref"},
		{name: "unknown endpoint", provider: "github", operation: "create_branch_ref", endpoint: "github.secret_endpoint"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for name, plan := range map[string]map[string]any{
				"runtime":      providerReviewAttemptAdapterRuntimePlan(tt.provider, tt.operation, tt.endpoint),
				"builder":      providerReviewAttemptAdapterRequestBuilderPlan(tt.provider, tt.operation, tt.endpoint),
				"client":       providerReviewAttemptAdapterProviderClientPlan(tt.provider, tt.operation, tt.endpoint),
				"execute":      providerReviewAttemptAdapterExecuteMethodPlan(tt.provider, tt.operation, tt.endpoint),
				"handler":      providerReviewAttemptAdapterResponseHandlerPlan(tt.provider, tt.operation, tt.endpoint),
				"contract":     providerReviewAttemptLiveAdapterContractPlan(tt.provider, tt.operation, tt.endpoint, "github_live_provider_review_adapter"),
				"live_adapter": providerReviewAttemptLiveAdapterPlan(tt.provider, tt.operation, tt.endpoint),
			} {
				if len(plan) != 0 {
					t.Fatalf("%s plan should reject invalid combination: %#v", name, plan)
				}
			}
		})
	}
}

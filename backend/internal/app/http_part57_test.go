package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProviderReviewAttemptOrchestrationSummaryHandlesEdgeStates(t *testing.T) {
	t.Run("returns empty dispatch plan for empty operation", func(t *testing.T) {
		if got := providerReviewAttemptAdapterDispatchPlan(nil, nil, nil, nil, nil); len(got) != 0 {
			t.Fatalf("empty operation dispatch plan = %#v", got)
		}
	})
	t.Run("redacts unknown adapter dispatch plan fields", func(t *testing.T) {
		dispatchPlan := providerReviewAttemptAdapterDispatchPlan(
			map[string]any{
				"name":            "raw_operation",
				"endpoint_key":    "github.secret_endpoint",
				"operation_order": 99,
			},
			map[string]any{
				"payload_builder":  "raw_builder",
				"response_handler": "raw_handler",
			},
			map[string]any{
				"status": "raw_status",
			},
			map[string]any{
				"mode": "raw_adapter_contract",
			},
			map[string]any{
				"claim_metadata_ready": true,
			},
		)
		if dispatchPlan["mode"] != "redacted_attempt_adapter_dispatch_plan" ||
			dispatchPlan["dispatch_state"] != "blocked" ||
			dispatchPlan["dispatch_ready"] != false ||
			dispatchPlan["dispatch_ready_reason"] != "provider_api_adapter_dispatch_not_armed" ||
			dispatchPlan["dispatch_metadata_ready"] != false ||
			dispatchPlan["attempt_claim_metadata_ready"] != false ||
			dispatchPlan["adapter_contract_ready"] != false ||
			dispatchPlan["provider_type"] != "" ||
			dispatchPlan["adapter_kind"] != "" ||
			dispatchPlan["operation_name"] != "" ||
			dispatchPlan["endpoint_key"] != "" ||
			dispatchPlan["method"] != "" ||
			dispatchPlan["payload_shape"] != "" ||
			dispatchPlan["payload_builder"] != "build_redacted_provider_request" ||
			dispatchPlan["response_handler"] != "handle_provider_response" ||
			dispatchPlan["provider_api_call_made"] != false ||
			dispatchPlan["provider_api_mutation"] != "disabled" ||
			dispatchPlan["contains_token"] != false ||
			dispatchPlan["contains_provider_url"] != false ||
			dispatchPlan["contains_repository_ref"] != false ||
			dispatchPlan["contains_branch_name"] != false ||
			dispatchPlan["contains_file_content"] != false {
			t.Fatalf("unknown operation dispatch plan should be redacted: %#v", dispatchPlan)
		}
		if transportPlan := mapFromAny(dispatchPlan["transport_plan"]); len(transportPlan) != 0 {
			t.Fatalf("unknown operation transport plan should be empty: %#v", transportPlan)
		}
		if requestPlan := mapFromAny(dispatchPlan["request_materialization_plan"]); len(requestPlan) != 0 {
			t.Fatalf("unknown operation request materialization plan should be empty: %#v", requestPlan)
		}
		if responsePlan := mapFromAny(dispatchPlan["response_plan"]); len(responsePlan) != 0 {
			t.Fatalf("unknown operation response plan should be empty: %#v", responsePlan)
		}
		if credentialPlan := mapFromAny(dispatchPlan["credential_binding_plan"]); len(credentialPlan) != 0 {
			t.Fatalf("unknown operation credential binding plan should be empty: %#v", credentialPlan)
		}
		if runtimePlan := mapFromAny(dispatchPlan["adapter_runtime_plan"]); len(runtimePlan) != 0 {
			t.Fatalf("unknown operation runtime plan should be empty: %#v", runtimePlan)
		}
		if branchPolicyPlan := mapFromAny(dispatchPlan["branch_policy_plan"]); len(branchPolicyPlan) != 0 {
			t.Fatalf("unknown operation branch policy plan should be empty: %#v", branchPolicyPlan)
		}
		if requestEnvelopePlan := mapFromAny(dispatchPlan["request_envelope_plan"]); len(requestEnvelopePlan) != 0 {
			t.Fatalf("unknown operation request envelope plan should be empty: %#v", requestEnvelopePlan)
		}
		if transactionPlan := mapFromAny(dispatchPlan["transaction_plan"]); len(transactionPlan) != 0 {
			t.Fatalf("unknown operation transaction plan should be empty: %#v", transactionPlan)
		}
		if requestValidationPreflight := mapFromAny(dispatchPlan["request_validation_preflight"]); len(requestValidationPreflight) != 0 {
			t.Fatalf("unknown operation request validation preflight should be empty: %#v", requestValidationPreflight)
		}
		if invocationPlan := mapFromAny(dispatchPlan["invocation_plan"]); len(invocationPlan) != 0 {
			t.Fatalf("unknown operation invocation plan should be empty: %#v", invocationPlan)
		}
		blockedReasons := stringSliceFromAny(dispatchPlan["blocked_reasons"])
		if len(blockedReasons) != 5 ||
			blockedReasons[0] != "provider_review_dispatch_provider_unknown" ||
			blockedReasons[1] != "provider_review_dispatch_metadata_not_ready" ||
			blockedReasons[2] != "provider_review_attempt_claim_not_recorded" ||
			blockedReasons[3] != "provider_review_adapter_not_implemented" ||
			blockedReasons[4] != "provider_review_mutation_not_armed" {
			t.Fatalf("unknown operation dispatch blocked reasons = %#v", blockedReasons)
		}
		encoded, _ := json.Marshal(dispatchPlan)
		for _, leak := range []string{"raw_operation", "github.secret_endpoint", "raw_builder", "raw_handler", "raw_adapter_contract"} {
			if strings.Contains(string(encoded), leak) {
				t.Fatalf("unknown operation dispatch plan leaked %q: %s", leak, encoded)
			}
		}
	})
	t.Run("keeps dispatch metadata blocked when claim identity mismatches", func(t *testing.T) {
		operation := map[string]any{
			"name":            "create_branch_ref",
			"endpoint_key":    "github.create_branch_ref",
			"operation_order": 10,
		}
		requestSummary := map[string]any{
			"payload_builder":                 "build_redacted_branch_ref_request",
			"response_handler":                "handle_branch_ref_response",
			"requires_idempotency_ledger":     true,
			"requires_response_diagnostics":   true,
			"request_envelope_ready":          true,
			"credential_preflight_ready":      true,
			"provider_review_preflight_ready": true,
		}
		responseDiagnostics := map[string]any{
			"mode":                     "redacted_attempt_response_diagnostics",
			"status":                   "pending",
			"success_status_class":     "2xx",
			"retryable_status_classes": []string{"5xx"},
		}
		adapterContract := providerReviewAttemptCandidateAdapterContract(operation, requestSummary, responseDiagnostics)
		claimPlan := map[string]any{
			"mode":                       "redacted_attempt_execution_claim_plan",
			"operation_name":             "commit_starter_files",
			"endpoint_key":               "github.commit_files",
			"claim_metadata_ready":       true,
			"idempotency_metadata_ready": true,
		}
		dispatchPlan := providerReviewAttemptAdapterDispatchPlan(operation, requestSummary, responseDiagnostics, adapterContract, claimPlan)
		if dispatchPlan["dispatch_metadata_ready"] != false ||
			dispatchPlan["attempt_claim_metadata_ready"] != false ||
			dispatchPlan["adapter_contract_ready"] != true {
			t.Fatalf("mismatched claim identity dispatch plan = %#v", dispatchPlan)
		}
		preflight := mapFromAny(dispatchPlan["request_validation_preflight"])
		if preflight["dispatch_metadata_ready"] != false ||
			preflight["attempt_claim_metadata_ready"] != false ||
			preflight["idempotency_metadata_ready"] != false {
			t.Fatalf("mismatched claim identity preflight = %#v", preflight)
		}
	})
	t.Run("keeps dispatch metadata blocked when adapter contract identity mismatches", func(t *testing.T) {
		operation := map[string]any{
			"name":              "create_branch_ref",
			"endpoint_key":      "github.create_branch_ref",
			"operation_order":   10,
			"status":            "planned",
			"dependency_status": "independent",
			"replay_check":      "not_seen",
			"conflict_policy":   "block_on_conflict",
			"retry_policy":      "retry_on_retryable_status",
		}
		requestSummary := map[string]any{
			"payload_builder":                 "build_redacted_branch_ref_request",
			"response_handler":                "handle_branch_ref_response",
			"requires_idempotency_ledger":     true,
			"requires_response_diagnostics":   true,
			"request_envelope_ready":          true,
			"credential_preflight_ready":      true,
			"provider_review_preflight_ready": true,
		}
		responseDiagnostics := map[string]any{
			"mode":                     "redacted_attempt_response_diagnostics",
			"status":                   "pending",
			"success_status_class":     "2xx",
			"retryable_status_classes": []string{"5xx"},
		}
		claimPlan := providerReviewAttemptExecutionClaimPlan(operation, true, true)
		adapterContract := map[string]any{
			"mode":           "redacted_attempt_adapter_contract",
			"operation_name": "commit_starter_files",
			"endpoint_key":   "github.commit_files",
		}
		dispatchPlan := providerReviewAttemptAdapterDispatchPlan(operation, requestSummary, responseDiagnostics, adapterContract, claimPlan)
		if dispatchPlan["dispatch_metadata_ready"] != false ||
			dispatchPlan["attempt_claim_metadata_ready"] != true ||
			dispatchPlan["adapter_contract_ready"] != false {
			t.Fatalf("mismatched adapter contract identity dispatch plan = %#v", dispatchPlan)
		}
		preflight := mapFromAny(dispatchPlan["request_validation_preflight"])
		if preflight["dispatch_metadata_ready"] != false ||
			preflight["attempt_claim_metadata_ready"] != true ||
			preflight["idempotency_metadata_ready"] != true {
			t.Fatalf("mismatched adapter contract identity preflight = %#v", preflight)
		}
	})
	t.Run("keeps dispatch metadata blocked when claim and adapter contract identities mismatch", func(t *testing.T) {
		operation := map[string]any{
			"name":            "create_branch_ref",
			"endpoint_key":    "github.create_branch_ref",
			"operation_order": 10,
		}
		requestSummary := map[string]any{
			"payload_builder":                 "build_redacted_branch_ref_request",
			"response_handler":                "handle_branch_ref_response",
			"requires_idempotency_ledger":     true,
			"requires_response_diagnostics":   true,
			"request_envelope_ready":          true,
			"credential_preflight_ready":      true,
			"provider_review_preflight_ready": true,
		}
		responseDiagnostics := map[string]any{
			"mode":                     "redacted_attempt_response_diagnostics",
			"status":                   "pending",
			"success_status_class":     "2xx",
			"retryable_status_classes": []string{"5xx"},
		}
		claimPlan := map[string]any{
			"mode":                       "redacted_attempt_execution_claim_plan",
			"operation_name":             "commit_starter_files",
			"endpoint_key":               "github.commit_files",
			"claim_metadata_ready":       true,
			"idempotency_metadata_ready": true,
		}
		adapterContract := map[string]any{
			"mode":           "redacted_attempt_adapter_contract",
			"operation_name": "open_review_request",
			"endpoint_key":   "github.open_review",
		}
		dispatchPlan := providerReviewAttemptAdapterDispatchPlan(operation, requestSummary, responseDiagnostics, adapterContract, claimPlan)
		if dispatchPlan["dispatch_metadata_ready"] != false ||
			dispatchPlan["attempt_claim_metadata_ready"] != false ||
			dispatchPlan["adapter_contract_ready"] != false {
			t.Fatalf("mismatched claim and adapter contract identities dispatch plan = %#v", dispatchPlan)
		}
		preflight := mapFromAny(dispatchPlan["request_validation_preflight"])
		if preflight["dispatch_metadata_ready"] != false ||
			preflight["attempt_claim_metadata_ready"] != false ||
			preflight["idempotency_metadata_ready"] != false {
			t.Fatalf("mismatched claim and adapter contract identities preflight = %#v", preflight)
		}
	})
	t.Run("keeps request validation preflight blocked when dispatch metadata is not ready", func(t *testing.T) {
		operation := map[string]any{
			"name":            "create_branch_ref",
			"endpoint_key":    "github.create_branch_ref",
			"operation_order": 10,
		}
		requestSummary := map[string]any{
			"payload_builder":                 "build_redacted_branch_ref_request",
			"response_handler":                "handle_branch_ref_response",
			"requires_idempotency_ledger":     true,
			"requires_response_diagnostics":   true,
			"request_envelope_ready":          true,
			"credential_preflight_ready":      true,
			"provider_review_preflight_ready": true,
		}
		responseDiagnostics := map[string]any{
			"mode":                 "redacted_attempt_response_diagnostics",
			"status":               "pending",
			"success_status_class": "2xx",
			"retryable_status_classes": []string{
				"5xx",
			},
		}
		adapterContract := providerReviewAttemptCandidateAdapterContract(operation, requestSummary, responseDiagnostics)
		claimPlan := providerReviewAttemptExecutionClaimPlan(operation, false, true)
		dispatchPlan := providerReviewAttemptAdapterDispatchPlan(operation, requestSummary, responseDiagnostics, adapterContract, claimPlan)
		preflight := mapFromAny(dispatchPlan["request_validation_preflight"])
		if preflight["mode"] != "redacted_attempt_adapter_request_validation_preflight" ||
			preflight["preflight_state"] != "blocked" ||
			preflight["preflight_ready"] != false ||
			preflight["dispatch_metadata_ready"] != false ||
			preflight["attempt_claim_metadata_ready"] != false ||
			preflight["idempotency_metadata_ready"] != false ||
			preflight["request_envelope_contract_ready"] != true ||
			preflight["request_envelope_metadata_ready"] != false ||
			preflight["request_validated"] != false ||
			preflight["provider_api_call_made"] != false ||
			preflight["provider_api_mutation"] != "disabled" ||
			preflight["contains_token"] != false ||
			preflight["contains_provider_url"] != false ||
			preflight["contains_repository_ref"] != false ||
			preflight["contains_branch_name"] != false ||
			preflight["contains_file_content"] != false ||
			preflight["preflight_boundary_redacted"] != true {
			t.Fatalf("metadata-not-ready request validation preflight = %#v", preflight)
		}
	})
	t.Run("redacts unknown attempt claim plan fields", func(t *testing.T) {
		claimPlan := providerReviewAttemptExecutionClaimPlan(
			map[string]any{
				"name":              "raw_operation",
				"endpoint_key":      "github.secret_endpoint",
				"status":            "retrying",
				"dependency_status": "raw_dependency",
				"replay_check":      "raw_replay",
				"conflict_policy":   "raw_conflict",
				"retry_policy":      "raw_retry",
				"operation_order":   99,
			},
			false,
			false,
		)
		if claimPlan["mode"] != "redacted_attempt_execution_claim_plan" ||
			claimPlan["claim_state"] != "blocked" ||
			claimPlan["claim_ready"] != false ||
			claimPlan["claim_metadata_ready"] != false ||
			claimPlan["operation_name"] != "" ||
			claimPlan["endpoint_key"] != "" ||
			claimPlan["provider_type"] != "" ||
			claimPlan["operation_endpoint_ready"] != false ||
			claimPlan["attempt_status"] != "blocked" ||
			claimPlan["dependency_status"] != "blocked" ||
			claimPlan["dependency_ready"] != false ||
			claimPlan["replay_check"] != "" ||
			claimPlan["conflict_policy"] != "" ||
			claimPlan["retry_policy"] != "" ||
			claimPlan["idempotency_metadata_ready"] != false ||
			claimPlan["response_diagnostics_ready"] != false ||
			claimPlan["provider_api_call_made"] != false ||
			claimPlan["provider_api_mutation"] != "disabled" ||
			claimPlan["contains_token"] != false {
			t.Fatalf("unknown operation claim plan should be redacted: %#v", claimPlan)
		}
		blockedReasons := stringSliceFromAny(claimPlan["blocked_reasons"])
		if len(blockedReasons) != 7 ||
			blockedReasons[0] != "provider_review_attempt_operation_endpoint_invalid" ||
			blockedReasons[1] != "provider_review_response_diagnostics_missing" ||
			blockedReasons[2] != "provider_review_idempotency_metadata_missing" ||
			blockedReasons[3] != "provider_review_dependency_not_ready" ||
			blockedReasons[4] != "provider_review_attempt_status_not_planned" ||
			blockedReasons[5] != "provider_review_adapter_not_implemented" ||
			blockedReasons[6] != "provider_review_mutation_not_armed" {
			t.Fatalf("unknown operation claim blocked reasons = %#v", blockedReasons)
		}
		encoded, _ := json.Marshal(claimPlan)
		for _, leak := range []string{"raw_operation", "github.secret_endpoint", "retrying", "raw_dependency", "raw_replay", "raw_conflict", "raw_retry"} {
			if strings.Contains(string(encoded), leak) {
				t.Fatalf("unknown operation claim plan leaked %q: %s", leak, encoded)
			}
		}
	})
	t.Run("blocks claim metadata when operation and endpoint mismatch", func(t *testing.T) {
		for _, tt := range []struct {
			name      string
			operation string
			endpoint  string
		}{
			{name: "branch with commit endpoint", operation: "create_branch_ref", endpoint: "github.commit_files"},
			{name: "commit with branch endpoint", operation: "commit_starter_files", endpoint: "github.create_branch_ref"},
			{name: "review with commit endpoint", operation: "open_review_request", endpoint: "gitea.commit_files"},
		} {
			t.Run(tt.name, func(t *testing.T) {
				claimPlan := providerReviewAttemptExecutionClaimPlan(
					map[string]any{
						"name":              tt.operation,
						"endpoint_key":      tt.endpoint,
						"status":            "planned",
						"dependency_status": "independent",
						"operation_order":   10,
					},
					true,
					true,
				)
				if claimPlan["claim_metadata_ready"] != false ||
					claimPlan["operation_endpoint_ready"] != false ||
					claimPlan["operation_name"] != tt.operation ||
					claimPlan["endpoint_key"] != tt.endpoint ||
					!containsString(stringSliceFromAny(claimPlan["blocked_reasons"]), "provider_review_attempt_operation_endpoint_invalid") {
					t.Fatalf("mismatched endpoint claim plan should stay blocked: %#v", claimPlan)
				}
			})
		}
	})
	t.Run("execution candidate blocks request summary mismatch", func(t *testing.T) {
		summary := providerReviewAttemptOrchestrationSummary([]map[string]any{{
			"name":              "create_branch_ref",
			"endpoint_key":      "github.create_branch_ref",
			"status":            "planned",
			"dependency_status": "independent",
			"operation_order":   10,
			"request_summary": map[string]any{
				"mode":                        "redacted_attempt_request_summary",
				"operation_name":              "commit_starter_files",
				"endpoint_key":                "github.commit_files",
				"payload_builder":             "build_redacted_file_batch_request",
				"response_handler":            "handle_commit_files_response",
				"requires_idempotency_ledger": true,
			},
			"response_diagnostics": map[string]any{
				"mode":         "redacted_attempt_response_diagnostics",
				"endpoint_key": "github.create_branch_ref",
			},
		}})
		candidate := mapFromAny(mapFromAny(summary["execution_candidate"]))
		claimPlan := mapFromAny(candidate["claim_plan"])
		if claimPlan["claim_metadata_ready"] != false ||
			claimPlan["request_summary_matches_operation"] != false ||
			containsString(stringSliceFromAny(claimPlan["blocked_reasons"]), "provider_review_idempotency_metadata_missing") ||
			!containsString(stringSliceFromAny(claimPlan["blocked_reasons"]), "provider_review_request_summary_mismatch") {
			t.Fatalf("execution candidate should block mismatched request summary without idempotency noise: %#v", claimPlan)
		}
		dispatchPlan := mapFromAny(candidate["dispatch_plan"])
		if dispatchPlan["dispatch_metadata_ready"] != false ||
			dispatchPlan["attempt_claim_metadata_ready"] != false {
			t.Fatalf("execution candidate dispatch should inherit claim metadata block: %#v", dispatchPlan)
		}
	})
	t.Run("execution candidate blocks response diagnostics mismatch", func(t *testing.T) {
		summary := providerReviewAttemptOrchestrationSummary([]map[string]any{{
			"name":              "create_branch_ref",
			"endpoint_key":      "github.create_branch_ref",
			"status":            "planned",
			"dependency_status": "independent",
			"operation_order":   10,
			"request_summary": map[string]any{
				"mode":                        "redacted_attempt_request_summary",
				"operation_name":              "create_branch_ref",
				"endpoint_key":                "github.create_branch_ref",
				"payload_builder":             "build_redacted_branch_ref_request",
				"response_handler":            "handle_branch_ref_response",
				"requires_idempotency_ledger": true,
			},
			"response_diagnostics": map[string]any{
				"mode":         "redacted_attempt_response_diagnostics",
				"endpoint_key": "github.commit_files",
			},
		}})
		candidate := mapFromAny(mapFromAny(summary["execution_candidate"]))
		claimPlan := mapFromAny(candidate["claim_plan"])
		if claimPlan["claim_metadata_ready"] != false ||
			claimPlan["response_diagnostics_match_endpoint"] != false ||
			claimPlan["response_diagnostics_ready"] != false ||
			!containsString(stringSliceFromAny(claimPlan["blocked_reasons"]), "provider_review_response_diagnostics_endpoint_mismatch") {
			t.Fatalf("execution candidate should block mismatched response diagnostics: %#v", claimPlan)
		}
		dispatchPlan := mapFromAny(candidate["dispatch_plan"])
		if dispatchPlan["dispatch_metadata_ready"] != false ||
			dispatchPlan["attempt_claim_metadata_ready"] != false {
			t.Fatalf("execution candidate dispatch should inherit response diagnostics block: %#v", dispatchPlan)
		}
	})
	t.Run("execution candidate accepts matching claim metadata", func(t *testing.T) {
		summary := providerReviewAttemptOrchestrationSummary([]map[string]any{{
			"name":              "create_branch_ref",
			"endpoint_key":      "github.create_branch_ref",
			"status":            "planned",
			"dependency_status": "independent",
			"operation_order":   10,
			"request_summary": map[string]any{
				"mode":                        "redacted_attempt_request_summary",
				"operation_name":              "create_branch_ref",
				"endpoint_key":                "github.create_branch_ref",
				"payload_builder":             "build_redacted_branch_ref_request",
				"response_handler":            "handle_branch_ref_response",
				"requires_idempotency_ledger": true,
			},
			"response_diagnostics": map[string]any{
				"mode":         "redacted_attempt_response_diagnostics",
				"endpoint_key": "github.create_branch_ref",
			},
		}})
		candidate := mapFromAny(mapFromAny(summary["execution_candidate"]))
		claimPlan := mapFromAny(candidate["claim_plan"])
		if claimPlan["claim_metadata_ready"] != true ||
			claimPlan["request_summary_matches_operation"] != true ||
			claimPlan["response_diagnostics_match_endpoint"] != true {
			t.Fatalf("execution candidate should accept matching claim metadata: %#v", claimPlan)
		}
		dispatchPlan := mapFromAny(candidate["dispatch_plan"])
		if dispatchPlan["dispatch_metadata_ready"] != true ||
			dispatchPlan["attempt_claim_metadata_ready"] != true {
			t.Fatalf("execution candidate dispatch should accept matching claim metadata: %#v", dispatchPlan)
		}
		gates := mapSliceFromAny(candidate["gates"])
		if len(gates) != 5 ||
			gates[0]["gate"] != "attempt_operation_ready" ||
			gates[0]["status"] != "ready" ||
			gates[1]["gate"] != "idempotency_metadata" ||
			gates[1]["status"] != "ready" ||
			gates[2]["gate"] != "response_diagnostics_metadata" ||
			gates[2]["status"] != "ready" ||
			gates[3]["category"] != "execution_blocker" ||
			gates[3]["status"] != "blocked" ||
			gates[4]["category"] != "execution_blocker" ||
			gates[4]["status"] != "blocked" {
			t.Fatalf("execution candidate gates should keep metadata ready and provider execution blocked: %#v", gates)
		}
	})
	t.Run("redacts unknown adapter contract operation name", func(t *testing.T) {
		contract := providerReviewAttemptCandidateAdapterContract(
			map[string]any{
				"name":            "raw_operation",
				"endpoint_key":    "github.secret_endpoint",
				"operation_order": 99,
			},
			map[string]any{
				"payload_builder":  "raw_builder",
				"response_handler": "raw_handler",
			},
			map[string]any{
				"status":                   "raw_status",
				"success_status_class":     "3xx",
				"retryable_status_classes": []any{"5xx", "secret-token", "4xx"},
			},
		)
		if contract["operation_name"] != "" ||
			contract["endpoint_key"] != "" ||
			contract["payload_builder"] != "build_redacted_provider_request" ||
			contract["response_handler"] != "handle_provider_response" ||
			contract["response_status"] != "blocked" ||
			contract["success_status_class"] != "" ||
			contract["provider_api_call_made"] != false ||
			contract["provider_api_mutation"] != "disabled" ||
			contract["contains_token"] != false {
			t.Fatalf("unknown operation adapter contract should be redacted: %#v", contract)
		}
		retryable := stringSliceFromAny(contract["retryable_status_classes"])
		if len(retryable) != 2 || retryable[0] != "5xx" || retryable[1] != "4xx" {
			t.Fatalf("unknown operation retryable classes should be allowlisted: %#v", retryable)
		}
		encoded, _ := json.Marshal(contract)
		for _, leak := range []string{"raw_operation", "github.secret_endpoint", "raw_builder", "raw_handler", "raw_status", "secret-token"} {
			if strings.Contains(string(encoded), leak) {
				t.Fatalf("unknown operation adapter contract leaked %q: %s", leak, encoded)
			}
		}
	})
	t.Run("uses first known ready operation name", func(t *testing.T) {
		summary := providerReviewAttemptOrchestrationSummary([]map[string]any{
			{
				"name":              "unknown_operation",
				"status":            "planned",
				"dependency_status": "independent",
			},
			{
				"name":              "commit_starter_files",
				"status":            "planned",
				"dependency_status": "dependency_satisfied",
			},
		})
		if summary["ready_count"] != 2 || summary["next_operation"] != "commit_starter_files" || summary["dependency_chain_status"] != "ready" {
			t.Fatalf("mixed operation name orchestration summary = %#v", summary)
		}
		candidate := mapFromAny(summary["execution_candidate"])
		if candidate["next_operation"] != "commit_starter_files" || candidate["endpoint_key"] != "" {
			t.Fatalf("mixed operation name execution candidate = %#v", candidate)
		}
		claimPlan := mapFromAny(candidate["claim_plan"])
		if claimPlan["claim_ready"] != false || claimPlan["claim_metadata_ready"] != false || claimPlan["operation_name"] != "commit_starter_files" || claimPlan["endpoint_key"] != "" {
			t.Fatalf("mixed operation name claim plan = %#v", claimPlan)
		}
	})
	t.Run("dependency failure wins over completed status", func(t *testing.T) {
		summary := providerReviewAttemptOrchestrationSummary([]map[string]any{{
			"name":              "commit_starter_files",
			"status":            "completed",
			"dependency_status": "dependency_failed",
		}})
		if summary["completed_count"] != 0 ||
			summary["blocked_count"] != 1 ||
			summary["dependency_chain_status"] != "blocked" {
			t.Fatalf("conflicting dependency orchestration summary = %#v", summary)
		}
	})
	t.Run("running attempt records local claim without provider call", func(t *testing.T) {
		claimPlan := providerReviewAttemptExecutionClaimPlan(
			map[string]any{
				"name":              "create_branch_ref",
				"endpoint_key":      "github.create_branch_ref",
				"status":            "running",
				"dependency_status": "independent",
				"claimed_at":        time.Now(),
				"replay_check":      "detect_existing_branch_ref",
				"conflict_policy":   "treat_existing_matching_ref_as_success",
				"retry_policy":      "retry_only_after_response_diagnostics",
				"operation_order":   10,
			},
			true,
			true,
		)
		if claimPlan["claim_state"] != "claimed" ||
			claimPlan["claim_recorded"] != true ||
			claimPlan["claim_metadata_ready"] != false ||
			claimPlan["provider_api_call_made"] != false ||
			claimPlan["provider_api_mutation"] != "disabled" ||
			claimPlan["contains_token"] != false ||
			claimPlan["contains_provider_url"] != false ||
			claimPlan["contains_repository_ref"] != false ||
			claimPlan["contains_branch_name"] != false ||
			claimPlan["contains_file_content"] != false {
			t.Fatalf("running claim plan should record only local claim: %#v", claimPlan)
		}
		reasons := stringSliceFromAny(claimPlan["blocked_reasons"])
		if containsString(reasons, "provider_review_attempt_status_not_planned") {
			t.Fatalf("running claim should not be marked as not-planned blocker: %#v", reasons)
		}
	})
	t.Run("running attempt without claim timestamp is not recorded", func(t *testing.T) {
		claimPlan := providerReviewAttemptExecutionClaimPlan(
			map[string]any{
				"name":              "create_branch_ref",
				"endpoint_key":      "github.create_branch_ref",
				"status":            "running",
				"dependency_status": "independent",
			},
			true,
			true,
		)
		if claimPlan["claim_state"] != "blocked" ||
			claimPlan["claim_recorded"] != false ||
			!containsString(stringSliceFromAny(claimPlan["blocked_reasons"]), "provider_review_attempt_status_not_planned") {
			t.Fatalf("running attempt without timestamp should not be recorded: %#v", claimPlan)
		}
	})
}

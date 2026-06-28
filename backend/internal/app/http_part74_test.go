package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
)

func newProviderReviewAttemptCredentialSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/credential-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptRequestEnvelopeSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/request-envelope-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptRequestValidationSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/request-validation-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptRequestMaterializationSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/request-materialization-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptBranchPolicySnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/branch-policy-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptRuntimeSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/runtime-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptAdapterRehearsalSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/adapter-rehearsal-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptAdapterBlueprintSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/adapter-blueprint-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptLiveAdapterContractSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/live-adapter-contract-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptInvocationSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/invocation-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptExecutionLockSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/execution-lock-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptSendSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/send-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptTransportSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/transport-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptRetryBackoffSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/retry-backoff-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptResponseSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/response-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptResultRecordingSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/result-recording-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptProviderCallBoundarySnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/provider-call-boundary-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newProviderReviewAttemptTransactionSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/provider-review-attempts/attempt-1/transaction-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "attempt-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

func newAgentCodeAuditSnapshotRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/agent/tasks/task-1/code-audit-snapshot", strings.NewReader(body))
	req = withRouteParam(req, "id", "task-1")
	return req.WithContext(context.WithValue(req.Context(), userContextKey{}, &User{ID: "admin-1", Role: "admin"}))
}

type providerReviewAttemptMockRow struct {
	ID         string
	Operation  string
	Endpoint   string
	Status     string
	Dependency string
	Order      int
	DependsOn  string
	ClaimedAt  any
}

func providerReviewAttemptRequestSummaryJSON(operation, endpoint string) []byte {
	data, _ := json.Marshal(map[string]any{
		"mode":                          "redacted_attempt_request_summary",
		"operation_name":                operation,
		"endpoint_key":                  endpoint,
		"payload_builder":               providerReviewExpectedPayloadBuilderName(operation),
		"response_handler":              providerReviewExpectedResponseHandlerName(operation),
		"execution_status":              "ready_for_adapter_implementation",
		"requires_idempotency_ledger":   true,
		"requires_response_diagnostics": true,
		"provider_api_call_made":        false,
		"provider_api_mutation":         "disabled",
		"external_call_made":            false,
		"contains_token":                false,
		"contains_provider_url":         false,
		"contains_repository_ref":       false,
		"contains_branch_name":          false,
		"contains_file_content":         false,
	})
	return data
}

func providerReviewAttemptResponseDiagnosticsJSON(endpoint string) []byte {
	data, _ := json.Marshal(map[string]any{
		"mode":                     "redacted_attempt_response_diagnostics",
		"endpoint_key":             endpoint,
		"status":                   "pending",
		"success_status_class":     "2xx",
		"retryable_status_classes": []string{"5xx"},
		"provider_api_call_made":   false,
		"provider_api_mutation":    "disabled",
		"external_call_made":       false,
		"contains_token":           false,
		"contains_provider_url":    false,
	})
	return data
}

func providerReviewAttemptAllRequiredLiveExecutionStatusesForTest() map[string]bool {
	out := map[string]bool{}
	for _, item := range providerReviewAttemptLiveExecutionRequiredEvidence() {
		out[item.Status] = true
	}
	return out
}

type providerReviewAttemptClaimSelectOptions struct {
	Status           string
	Dependency       string
	Operation        string
	Endpoint         string
	RequestOperation string
	RequestEndpoint  string
	ResponseEndpoint string
	ApprovalAction   string
	ApprovalStatus   string
	ClaimedAt        any
}

func providerReviewArmingSnapshotApproval(approvalStatus, armingStatus string, executionEnabled, rehearsalReady, mutationConfig bool) map[string]any {
	return map[string]any{
		"id":          "approval-1",
		"project_id":  "project-1",
		"action":      templateProviderReviewExecuteApprovalAction,
		"status":      approvalStatus,
		"title":       "Provider review execution",
		"resource_id": "run-1",
		"request_payload": map[string]any{
			"kind":                    "project_template_provider_review_execute",
			"project_id":              "project-1",
			"project_template_run_id": "run-1",
			"execution_request": map[string]any{
				"status":          "approval_ready",
				"approval_action": templateProviderReviewExecuteApprovalAction,
				"resource_type":   "project_template_run",
				"provider_type":   "github",
				"review_kind":     "pull_request",
				"source_branch":   "secret-source-branch",
				"target_branch":   "secret-target-branch",
			},
			"provider_review_reconciliation": map[string]any{
				"status":        "ready",
				"provider_type": "github",
				"review_kind":   "pull_request",
				"adapter_rehearsal": map[string]any{
					"status":                         "ready",
					"operation_count":                3,
					"ready_operation_count":          3,
					"blocked_operation_count":        0,
					"mutation_arming_candidate":      true,
					"external_call_made":             false,
					"provider_api_call_made":         false,
					"provider_api_mutation":          "disabled",
					"adapter_mutation_currently_off": true,
				},
				"mutation_arming_plan": map[string]any{
					"status":                         armingStatus,
					"provider_type":                  "github",
					"review_kind":                    "pull_request",
					"execution_enabled_config":       executionEnabled,
					"adapter_rehearsal_ready":        rehearsalReady,
					"mutation_armed_config":          mutationConfig,
					"mutation_armed":                 mutationConfig,
					"blocked_reasons":                []string{"provider_review_mutation_armed"},
					"external_call_made":             false,
					"provider_api_call_made":         false,
					"provider_api_mutation":          "disabled",
					"adapter_mutation_currently_off": true,
				},
				"execution_blueprint": map[string]any{
					"status":                   "ready_for_adapter_implementation",
					"provider_type":            "github",
					"review_kind":              "pull_request",
					"live_adapter_implemented": false,
				},
			},
		},
	}
}

func providerReviewArmingSnapshotLedger(attemptCount int) map[string]any {
	operations := make([]map[string]any, 0, attemptCount)
	for i := 1; i <= attemptCount; i++ {
		operation := "create_branch_ref"
		endpoint := "github.create_branch_ref"
		dependency := "independent"
		dependsOn := ""
		if i == 2 {
			operation = "commit_starter_files"
			endpoint = "github.commit_files"
			dependency = "waiting_for_dependency"
			dependsOn = "create_branch_ref"
		}
		if i >= 3 {
			operation = "open_review_request"
			endpoint = "github.open_review"
			dependency = "waiting_for_dependency"
			dependsOn = "commit_starter_files"
		}
		operations = append(operations, map[string]any{
			"id":                   fmt.Sprintf("attempt-%d", i),
			"name":                 operation,
			"endpoint_key":         endpoint,
			"status":               "planned",
			"operation_order":      i * 10,
			"depends_on_operation": dependsOn,
			"dependency_status":    dependency,
		})
	}
	return map[string]any{
		"status":        "recorded",
		"attempt_count": attemptCount,
		"operations":    operations,
		"orchestration": map[string]any{
			"next_operation":          "create_branch_ref",
			"dependency_chain_status": "ready",
		},
	}
}

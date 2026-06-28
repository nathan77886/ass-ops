package app

import (
	"testing"
)

func TestProviderReviewAttemptEndpointMatchesOperation(t *testing.T) {
	for _, tt := range []struct {
		name      string
		provider  string
		operation string
		endpoint  string
		want      bool
	}{
		{
			name:      "github branch ref",
			provider:  "github",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
			want:      true,
		},
		{
			name:      "github starter files maps to commit files endpoint",
			provider:  "github",
			operation: "commit_starter_files",
			endpoint:  "github.commit_files",
			want:      true,
		},
		{
			name:      "gitea review request",
			provider:  "gitea",
			operation: "open_review_request",
			endpoint:  "gitea.open_review",
			want:      true,
		},
		{
			name:      "operation endpoint mismatch",
			provider:  "github",
			operation: "create_branch_ref",
			endpoint:  "github.commit_files",
		},
		{
			name:      "cross provider mismatch",
			provider:  "github",
			operation: "create_branch_ref",
			endpoint:  "gitea.create_branch_ref",
		},
		{
			name:      "unknown provider",
			provider:  "raw_provider",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "unknown operation",
			provider:  "github",
			operation: "raw_operation",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "unknown endpoint",
			provider:  "github",
			operation: "create_branch_ref",
			endpoint:  "unknown.create_branch_ref",
		},
		{
			name:      "empty endpoint",
			provider:  "github",
			operation: "create_branch_ref",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := providerReviewAttemptEndpointMatchesOperation(tt.provider, tt.operation, tt.endpoint); got != tt.want {
				t.Fatalf("providerReviewAttemptEndpointMatchesOperation(%q, %q, %q) = %v, want %v", tt.provider, tt.operation, tt.endpoint, got, tt.want)
			}
		})
	}
}

func TestProviderReviewAttemptPlanMatchesOperation(t *testing.T) {
	for _, tt := range []struct {
		name      string
		plan      map[string]any
		mode      string
		operation string
		endpoint  string
		want      bool
	}{
		{
			name: "matching plan",
			plan: map[string]any{
				"mode":           "redacted_attempt_adapter_response_plan",
				"operation_name": "create_branch_ref",
				"endpoint_key":   "github.create_branch_ref",
			},
			mode:      "redacted_attempt_adapter_response_plan",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
			want:      true,
		},
		{
			name: "mode mismatch",
			plan: map[string]any{
				"mode":           "raw_plan",
				"operation_name": "create_branch_ref",
				"endpoint_key":   "github.create_branch_ref",
			},
			mode:      "redacted_attempt_adapter_response_plan",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name: "operation mismatch",
			plan: map[string]any{
				"mode":           "redacted_attempt_adapter_response_plan",
				"operation_name": "commit_starter_files",
				"endpoint_key":   "github.commit_files",
			},
			mode:      "redacted_attempt_adapter_response_plan",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name: "endpoint mismatch",
			plan: map[string]any{
				"mode":           "redacted_attempt_adapter_response_plan",
				"operation_name": "create_branch_ref",
				"endpoint_key":   "gitea.create_branch_ref",
			},
			mode:      "redacted_attempt_adapter_response_plan",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "missing plan",
			mode:      "redacted_attempt_adapter_response_plan",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := providerReviewAttemptPlanMatchesOperation(tt.plan, tt.mode, tt.operation, tt.endpoint); got != tt.want {
				t.Fatalf("providerReviewAttemptPlanMatchesOperation() = %v, want %v", got, tt.want)
			}
		})
	}
}

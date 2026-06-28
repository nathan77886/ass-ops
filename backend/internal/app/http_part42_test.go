package app

import (
	"testing"
)

func TestProviderReviewAttemptResponsePlanReadyForOperation(t *testing.T) {
	validPlan := func(operation, endpoint string) map[string]any {
		unlockOperation := providerReviewAttemptDependencyUnlockOperation(operation)
		return map[string]any{
			"mode":                         providerReviewAttemptAdapterResponsePlanMode,
			"operation_name":               operation,
			"endpoint_key":                 endpoint,
			"success_attempt_status":       "completed",
			"retry_attempt_status":         "planned",
			"failure_attempt_status":       "failed",
			"dependency_unlocks_operation": unlockOperation,
			"dependency_update_status":     providerReviewAttemptDependencyUnlockStatus(unlockOperation),
			"requires_dependency_update":   unlockOperation != "",
		}
	}

	tests := []struct {
		name      string
		plan      map[string]any
		operation string
		endpoint  string
		want      bool
	}{
		{
			name:      "branch response contract ready",
			plan:      validPlan("create_branch_ref", "github.create_branch_ref"),
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
			want:      true,
		},
		{
			name:      "commit response contract ready",
			plan:      validPlan("commit_starter_files", "github.commit_files"),
			operation: "commit_starter_files",
			endpoint:  "github.commit_files",
			want:      true,
		},
		{
			name:      "terminal review response contract ready",
			plan:      validPlan("open_review_request", "gitea.open_review"),
			operation: "open_review_request",
			endpoint:  "gitea.open_review",
			want:      true,
		},
		{
			name:      "nil plan",
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:      "empty plan",
			plan:      map[string]any{},
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name:     "empty operation",
			plan:     validPlan("create_branch_ref", "github.create_branch_ref"),
			endpoint: "github.create_branch_ref",
		},
		{
			name:      "empty endpoint",
			plan:      validPlan("create_branch_ref", "github.create_branch_ref"),
			operation: "create_branch_ref",
		},
		{
			name: "success status mismatch",
			plan: map[string]any{
				"mode":                         providerReviewAttemptAdapterResponsePlanMode,
				"operation_name":               "create_branch_ref",
				"endpoint_key":                 "github.create_branch_ref",
				"success_attempt_status":       "planned",
				"retry_attempt_status":         "planned",
				"failure_attempt_status":       "failed",
				"dependency_unlocks_operation": "commit_starter_files",
				"dependency_update_status":     "dependency_satisfied",
				"requires_dependency_update":   true,
			},
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name: "retry status mismatch",
			plan: map[string]any{
				"mode":                         providerReviewAttemptAdapterResponsePlanMode,
				"operation_name":               "create_branch_ref",
				"endpoint_key":                 "github.create_branch_ref",
				"success_attempt_status":       "completed",
				"retry_attempt_status":         "completed",
				"failure_attempt_status":       "failed",
				"dependency_unlocks_operation": "commit_starter_files",
				"dependency_update_status":     "dependency_satisfied",
				"requires_dependency_update":   true,
			},
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name: "failure status mismatch",
			plan: map[string]any{
				"mode":                         providerReviewAttemptAdapterResponsePlanMode,
				"operation_name":               "create_branch_ref",
				"endpoint_key":                 "github.create_branch_ref",
				"success_attempt_status":       "completed",
				"retry_attempt_status":         "planned",
				"failure_attempt_status":       "planned",
				"dependency_unlocks_operation": "commit_starter_files",
				"dependency_update_status":     "dependency_satisfied",
				"requires_dependency_update":   true,
			},
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name: "dependency unlock mismatch",
			plan: map[string]any{
				"mode":                         providerReviewAttemptAdapterResponsePlanMode,
				"operation_name":               "create_branch_ref",
				"endpoint_key":                 "github.create_branch_ref",
				"success_attempt_status":       "completed",
				"retry_attempt_status":         "planned",
				"failure_attempt_status":       "failed",
				"dependency_unlocks_operation": "open_review_request",
				"dependency_update_status":     "dependency_satisfied",
				"requires_dependency_update":   true,
			},
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name: "dependency update status mismatch",
			plan: map[string]any{
				"mode":                         providerReviewAttemptAdapterResponsePlanMode,
				"operation_name":               "create_branch_ref",
				"endpoint_key":                 "github.create_branch_ref",
				"success_attempt_status":       "completed",
				"retry_attempt_status":         "planned",
				"failure_attempt_status":       "failed",
				"dependency_unlocks_operation": "commit_starter_files",
				"dependency_update_status":     "independent",
				"requires_dependency_update":   true,
			},
			operation: "create_branch_ref",
			endpoint:  "github.create_branch_ref",
		},
		{
			name: "terminal operation rejects raw unlock",
			plan: map[string]any{
				"mode":                         providerReviewAttemptAdapterResponsePlanMode,
				"operation_name":               "open_review_request",
				"endpoint_key":                 "github.open_review",
				"success_attempt_status":       "completed",
				"retry_attempt_status":         "planned",
				"failure_attempt_status":       "failed",
				"dependency_unlocks_operation": "raw-operation-secret",
				"dependency_update_status":     "",
				"requires_dependency_update":   false,
			},
			operation: "open_review_request",
			endpoint:  "github.open_review",
		},
		{
			name: "requires dependency update mismatch",
			plan: map[string]any{
				"mode":                         providerReviewAttemptAdapterResponsePlanMode,
				"operation_name":               "commit_starter_files",
				"endpoint_key":                 "github.commit_files",
				"success_attempt_status":       "completed",
				"retry_attempt_status":         "planned",
				"failure_attempt_status":       "failed",
				"dependency_unlocks_operation": "open_review_request",
				"dependency_update_status":     "dependency_satisfied",
				"requires_dependency_update":   false,
			},
			operation: "commit_starter_files",
			endpoint:  "github.commit_files",
		},
		{
			name: "requires dependency update missing",
			plan: map[string]any{
				"mode":                         providerReviewAttemptAdapterResponsePlanMode,
				"operation_name":               "open_review_request",
				"endpoint_key":                 "github.open_review",
				"success_attempt_status":       "completed",
				"retry_attempt_status":         "planned",
				"failure_attempt_status":       "failed",
				"dependency_unlocks_operation": "",
				"dependency_update_status":     "",
			},
			operation: "open_review_request",
			endpoint:  "github.open_review",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := providerReviewAttemptResponsePlanReadyForOperation(tt.plan, tt.operation, tt.endpoint); got != tt.want {
				t.Fatalf("providerReviewAttemptResponsePlanReadyForOperation() = %v, want %v", got, tt.want)
			}
		})
	}
}

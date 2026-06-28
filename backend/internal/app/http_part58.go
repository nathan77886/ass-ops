package app

import (
	"context"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
)

func (s *Server) providerReviewCurrentLiveExecutionGate(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "update") {
		return
	}
	approvalID := cleanOptionalID(chi.URLParam(r, "id"))
	if approvalID == "" {
		writeError(w, http.StatusBadRequest, "operation approval id is required")
		return
	}
	approval, err := providerReviewApprovalForArmingSnapshot(r.Context(), s.store, approvalID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireApprovalRead(w, r, approval) {
		return
	}
	if stringFromMap(approval, "action") != templateProviderReviewExecuteApprovalAction {
		writeError(w, http.StatusConflict, "operation approval is not tied to provider review execution")
		return
	}
	ledger, err := providerReviewAttemptLedgerForApprovalSnapshot(r.Context(), s.store, approvalID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := ProviderReviewCurrentLiveExecutionGate(r.Context(), s.store, ProviderReviewCurrentLiveExecutionGateOptions{
		OperationApprovalID: approvalID,
		Approval:            approval,
		AttemptLedger:       ledger,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "provider review current live execution gate failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func operationApprovalPayloadAudit(approval map[string]any) map[string]any {
	payload := mapFromAny(approval["request_payload"])
	switch stringFromMap(payload, "kind") {
	case "config_git_commit":
		out := map[string]any{
			"kind":                  "config_git_commit",
			"project_id":            cleanOptionalID(stringFromMap(payload, "project_id")),
			"repo_id":               cleanOptionalID(stringFromMap(payload, "repo_id")),
			"scaffold_file_count":   intFromAny(payload["scaffold_file_count"], 0),
			"project_version_count": intFromAny(payload["project_version_count"], 0),
			"payload_redacted":      true,
			"external_call_made":    false,
			"git_write_performed":   false,
			"file_content_included": false,
			"secret_included":       false,
		}
		input := mapFromAny(payload["input"])
		if len(input) > 0 {
			out["input"] = map[string]any{
				"project_git_repository_id": cleanOptionalID(fmt.Sprint(input["project_git_repository_id"])),
				"config_remote_id":          cleanOptionalID(fmt.Sprint(input["config_remote_id"])),
				"provider_type":             cleanOptionalText(fmt.Sprint(input["provider_type"])),
				"default_branch_configured": boolOnlyFromAny(input["default_branch_configured"]),
				"scaffold_file_count":       intFromAny(input["scaffold_file_count"], 0),
				"remote_count":              intFromAny(input["remote_count"], 0),
				"mode":                      cleanOptionalText(fmt.Sprint(input["mode"])),
				"file_content_included":     false,
				"secret_included":           false,
				"external_call_made":        false,
				"git_write_performed":       false,
			}
		}
		if result := mapFromAny(payload["approval_result"]); len(result) > 0 {
			out["approval_result"] = map[string]any{
				"operation_request_result": mapFromAny(result["operation_request_result"]),
				"external_call_made":       false,
				"git_write_performed":      false,
				"file_content_included":    false,
				"secret_included":          false,
			}
		}
		return out
	case "project_template_provider_review_execute":
		out := map[string]any{
			"kind":                    "project_template_provider_review_execute",
			"project_template_run_id": cleanOptionalID(stringFromMap(payload, "project_template_run_id")),
			"project_id":              cleanOptionalID(stringFromMap(payload, "project_id")),
			"provider_api_call_made":  false,
			"provider_api_mutation":   "disabled",
			"payload_redacted":        true,
			"contains_token":          false,
			"contains_file_content":   false,
		}
		request := mapFromAny(payload["execution_request"])
		if len(request) > 0 {
			out["execution_request"] = map[string]any{
				"status":                   request["status"],
				"approval_action":          request["approval_action"],
				"resource_type":            request["resource_type"],
				"provider_type":            request["provider_type"],
				"review_kind":              request["review_kind"],
				"source_branch":            request["source_branch"],
				"target_branch":            request["target_branch"],
				"payload_redacted":         true,
				"contains_token":           false,
				"provider_api_mutation":    "disabled",
				"requires_operator_review": true,
			}
		}
		out["execution_guardrail"] = sanitizedProviderReviewExecutionGuardrail(mapFromAny(payload["execution_guardrail"]))
		out["credential_strategy"] = sanitizedProviderReviewCredentialStrategy(mapFromAny(payload["credential_strategy"]))
		out["starter_file_payload"] = sanitizedStarterFilePayloadSummary(mapFromAny(payload["starter_file_payload"]))
		out["provider_api_request_plan"] = sanitizedProviderAPIRequestPlan(mapFromAny(payload["provider_api_request_plan"]))
		out["provider_review_reconciliation"] = sanitizedProviderReviewReconciliation(mapFromAny(payload["provider_review_reconciliation"]))
		out["provider_review_target_summary"] = sanitizedProviderReviewTargetSummary(mapFromAny(payload["provider_review_target_summary"]))
		if result := mapFromAny(payload["approval_result"]); len(result) > 0 {
			out["approval_result"] = map[string]any{
				"project_template_run_id":        cleanOptionalID(stringFromMap(result, "project_template_run_id")),
				"execution_request":              out["execution_request"],
				"execution_guardrail":            sanitizedProviderReviewExecutionGuardrail(mapFromAny(result["execution_guardrail"])),
				"credential_strategy":            sanitizedProviderReviewCredentialStrategy(mapFromAny(result["credential_strategy"])),
				"starter_file_payload":           sanitizedStarterFilePayloadSummary(mapFromAny(result["starter_file_payload"])),
				"provider_api_request_plan":      sanitizedProviderAPIRequestPlan(mapFromAny(result["provider_api_request_plan"])),
				"provider_review_reconciliation": sanitizedProviderReviewReconciliation(mapFromAny(result["provider_review_reconciliation"])),
				"provider_review_target_summary": sanitizedProviderReviewTargetSummary(mapFromAny(result["provider_review_target_summary"])),
				"provider_review_attempt_ledger": sanitizedProviderReviewAttemptLedger(mapFromAny(result["provider_review_attempt_ledger"])),
				"provider_api_call_made":         false,
				"provider_api_mutation":          "disabled",
				"execution_enabled":              false,
			}
		}
		return out
	default:
		return map[string]any{}
	}
}

func (s *Server) providerReviewAttemptLedgerForApproval(ctx context.Context, approvalID string) (map[string]any, error) {
	approvalID = cleanOptionalID(approvalID)
	if approvalID == "" {
		return providerReviewAttemptLedgerSummary(nil), nil
	}
	return providerReviewAttemptLedgerForApprovalGorm(ctx, s.store.Gorm, approvalID)
}

func providerReviewAttemptLedgerForApprovalGorm(ctx context.Context, db *gorm.DB, approvalID string) (map[string]any, error) {
	approvalID = cleanOptionalID(approvalID)
	if approvalID == "" {
		return providerReviewAttemptLedgerSummary(nil), nil
	}
	var models []GormProviderReviewAttempt
	if err := db.WithContext(ctx).
		Where(&GormProviderReviewAttempt{OperationApprovalID: approvalID}).
		Order("operation_order ASC").
		Order("created_at ASC").
		Order("operation_name ASC").
		Find(&models).Error; err != nil {
		return nil, err
	}
	attempts := make([]map[string]any, 0, len(models))
	for _, model := range models {
		attempts = append(attempts, providerReviewAttemptMap(model, nil))
	}
	return providerReviewAttemptLedgerSummary(attempts), nil
}

func providerReviewAttemptMap(attempt GormProviderReviewAttempt, approval *GormOperationApproval) map[string]any {
	item := map[string]any{
		"id":                                 attempt.ID,
		"operation_approval_id":              attempt.OperationApprovalID,
		"project_template_run_id":            nullableStringValue(attempt.ProjectTemplateRunID),
		"provider_type":                      attempt.ProviderType,
		"review_kind":                        attempt.ReviewKind,
		"operation_name":                     attempt.OperationName,
		"endpoint_key":                       attempt.EndpointKey,
		"status":                             attempt.Status,
		"replay_check":                       attempt.ReplayCheck,
		"conflict_policy":                    attempt.ConflictPolicy,
		"retry_policy":                       attempt.RetryPolicy,
		"operation_order":                    attempt.OperationOrder,
		"depends_on_operation":               attempt.DependsOnOperation,
		"dependency_status":                  attempt.DependencyStatus,
		"request_summary":                    mapFromAny(attempt.RequestSummary.Data),
		"response_diagnostics":               mapFromAny(attempt.ResponseDiagnostics.Data),
		"provider_api_call_made":             attempt.ProviderAPICallMade,
		"provider_api_mutation":              attempt.ProviderAPIMutation,
		"external_call_made":                 attempt.ExternalCallMade,
		"provider_status_class":              attempt.ProviderStatusClass,
		"provider_review_url":                attempt.ProviderReviewURL,
		"executed_at":                        nullableTimeAny(attempt.ExecutedAt),
		"live_execution_phase":               attempt.LiveExecutionPhase,
		"live_execution_retryable":           attempt.LiveExecutionRetryable,
		"live_execution_manual_cleanup_hint": attempt.LiveExecutionManualCleanupHint,
		"cleanup_attempted":                  attempt.CleanupAttempted,
		"cleanup_succeeded":                  attempt.CleanupSucceeded,
		"cleanup_required":                   attempt.CleanupRequired,
		"claimed_at":                         nullableTimeAny(attempt.ClaimedAt),
		"claimed_by_user_id":                 nullableStringValue(attempt.ClaimedByUserID),
		"created_at":                         attempt.CreatedAt,
		"updated_at":                         attempt.UpdatedAt,
	}
	if approval != nil {
		item["approval_id"] = approval.ID
		item["approval_project_id"] = nullableStringValue(approval.ProjectID)
		item["approval_action"] = approval.Action
		item["approval_status"] = approval.Status
		item["approval_request_payload"] = mapFromAny(approval.RequestPayload.Data)
	}
	return item
}

func providerReviewAttemptWithApprovalGorm(ctx context.Context, tx *gorm.DB, attemptID string, lock bool) (GormProviderReviewAttempt, GormOperationApproval, map[string]any, error) {
	db := tx.WithContext(ctx)
	if lock {
		db = db.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var attempt GormProviderReviewAttempt
	if err := db.First(&attempt, &GormProviderReviewAttempt{GormBase: GormBase{ID: attemptID}}).Error; err != nil {
		return GormProviderReviewAttempt{}, GormOperationApproval{}, nil, gormNotFoundAsErrNotFound(err)
	}
	var approval GormOperationApproval
	if err := tx.WithContext(ctx).First(&approval, &GormOperationApproval{GormBase: GormBase{ID: attempt.OperationApprovalID}}).Error; err != nil {
		return GormProviderReviewAttempt{}, GormOperationApproval{}, nil, gormNotFoundAsErrNotFound(err)
	}
	return attempt, approval, providerReviewAttemptMap(attempt, &approval), nil
}

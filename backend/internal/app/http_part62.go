package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Server) recordProviderReviewAttemptLiveCleanupResult(ctx context.Context, attemptID string, attempt map[string]any, result reviewBranchExecutionResult, cleanupErr error) (map[string]any, error) {
	responseStatus := "success"
	if cleanupErr != nil {
		responseStatus = "failed"
	}
	statusClass := safeProviderReviewStatusClass(result.ProviderStatusClass)
	if statusClass == "" && cleanupErr != nil {
		statusClass = safeProviderReviewStatusClass(providerStatusClassFromError(cleanupErr))
	}
	if statusClass == "" {
		statusClass = "unknown"
	}
	diagnostics := sanitizedProviderReviewAttemptResponseDiagnostics(mapFromAny(attempt["response_diagnostics"]))
	diagnostics["cleanup_result_recorded"] = true
	diagnostics["cleanup_result_source"] = "atomic_github_review_branch_cleanup"
	diagnostics["cleanup_status"] = safeProviderReviewAttemptResponseStatus(responseStatus)
	diagnostics["provider_status_class"] = statusClass
	diagnostics["live_execution_phase"] = safeProviderReviewLiveExecutionPhase(result.ExecutionPhase)
	diagnostics["live_execution_retryable"] = result.Retryable
	diagnostics["manual_cleanup_hint"] = safeProviderReviewManualCleanupHint(result.ManualCleanupHint)
	diagnostics["cleanup_attempted"] = result.CleanupAttempted
	diagnostics["cleanup_succeeded"] = result.CleanupSucceeded
	diagnostics["cleanup_required"] = result.CleanupRequired
	diagnostics["provider_api_call_made"] = true
	diagnostics["provider_api_mutation"] = "enabled"
	diagnostics["external_call_made"] = true
	diagnostics["provider_response_status_included"] = false
	diagnostics["provider_request_id_included"] = false
	diagnostics["response_body_included"] = false
	diagnostics["headers_included"] = false
	diagnostics["provider_url_included"] = false
	diagnostics["idempotency_key_included"] = false
	diagnostics["contains_token"] = false
	diagnostics["contains_provider_url"] = false
	diagnostics["contains_repository_ref"] = false
	diagnostics["contains_branch_name"] = false
	diagnostics["contains_file_content"] = false
	approvalID := cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"]))
	var recorded map[string]any
	var ledger map[string]any
	err := s.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model GormProviderReviewAttempt
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(&GormProviderReviewAttempt{GormBase: GormBase{ID: attemptID}, Status: "failed", CleanupRequired: true}).
			First(&model).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		model.ResponseDiagnostics = JSONValue{Data: diagnostics}
		model.ProviderAPIMutation = "enabled"
		model.ProviderStatusClass = statusClass
		model.LiveExecutionPhase = safeProviderReviewLiveExecutionPhase(result.ExecutionPhase)
		model.LiveExecutionRetryable = result.Retryable
		model.LiveExecutionManualCleanupHint = safeProviderReviewManualCleanupHint(result.ManualCleanupHint)
		model.CleanupAttempted = true
		model.CleanupSucceeded = result.CleanupSucceeded
		model.CleanupRequired = result.CleanupRequired
		if err := tx.Save(&model).Error; err != nil {
			return err
		}
		recorded = providerReviewAttemptMap(model, nil)
		var err error
		ledger, err = providerReviewAttemptLedgerForApprovalGorm(ctx, tx, approvalID)
		if err != nil {
			return err
		}
		syncResult, err := syncCanonicalAssetsGorm(ctx, tx)
		if err != nil {
			return fmt.Errorf("syncing canonical assets for provider review live cleanup: %w", err)
		}
		if s.log != nil {
			s.log.Debug("canonical assets synced in transaction", "reason", "provider_review_attempt.live_cleanup", "synced_assets", syncResult.SyncedAssets, "inserted_relations", syncResult.InsertedRelations, "pruned_relations", syncResult.PrunedRelations, "inserted_status_snapshots", syncResult.InsertedStatusSnapshots)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return providerReviewAttemptLiveCleanupResponse(recorded, ledger, cleanupErr == nil, responseStatus, statusClass, result), nil
}

func providerReviewAttemptLiveExecutionDiagnostics(attempt map[string]any, responseStatus, statusClass string, result reviewBranchExecutionResult) map[string]any {
	diagnostics := sanitizedProviderReviewAttemptResponseDiagnostics(mapFromAny(attempt["response_diagnostics"]))
	diagnostics["status"] = safeProviderReviewAttemptResponseStatus(responseStatus)
	diagnostics["live_result_recorded"] = true
	diagnostics["live_result_source"] = "atomic_github_review_branch_executor"
	diagnostics["provider_status_class"] = safeProviderReviewStatusClass(statusClass)
	diagnostics["provider_api_call_made"] = result.ExternalCallMade
	diagnostics["provider_api_mutation"] = "enabled"
	diagnostics["external_call_made"] = result.ExternalCallMade
	diagnostics["live_execution_phase"] = safeProviderReviewLiveExecutionPhase(result.ExecutionPhase)
	diagnostics["live_execution_retryable"] = result.Retryable
	diagnostics["manual_cleanup_hint"] = safeProviderReviewManualCleanupHint(result.ManualCleanupHint)
	diagnostics["cleanup_attempted"] = result.CleanupAttempted
	diagnostics["cleanup_succeeded"] = result.CleanupSucceeded
	diagnostics["cleanup_required"] = result.CleanupRequired
	diagnostics["provider_response_status_included"] = false
	diagnostics["provider_request_id_included"] = false
	diagnostics["response_body_included"] = false
	diagnostics["headers_included"] = false
	diagnostics["provider_url_included"] = false
	diagnostics["idempotency_key_included"] = false
	diagnostics["contains_token"] = false
	diagnostics["contains_provider_url"] = false
	diagnostics["contains_repository_ref"] = false
	diagnostics["contains_branch_name"] = false
	diagnostics["contains_file_content"] = false
	return diagnostics
}

func providerReviewAttemptLiveExecutionResponse(attempt, ledger map[string]any, ok bool, responseStatus, statusClass, reviewURL string, result reviewBranchExecutionResult) map[string]any {
	state := "completed"
	if !ok {
		state = "failed"
	}
	return map[string]any{
		"live_execution_state":          state,
		"live_execution_recorded":       true,
		"live_execution_success":        ok,
		"executed":                      ok,
		"result_status":                 safeProviderReviewAttemptResponseStatus(responseStatus),
		"provider_review_attempt_id":    cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":         cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"operation_name":                safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name")),
		"endpoint_key":                  safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key")),
		"provider_status_class":         safeProviderReviewStatusClass(statusClass),
		"provider_review_url":           reviewURL,
		"provider_review_url_included":  reviewURL != "",
		"attempt":                       providerReviewAttemptLedgerSummary([]map[string]any{attempt})["operations"].([]map[string]any)[0],
		"ledger":                        ledger,
		"live_execution_phase":          safeProviderReviewLiveExecutionPhase(result.ExecutionPhase),
		"live_execution_retryable":      result.Retryable,
		"manual_cleanup_hint":           safeProviderReviewManualCleanupHint(result.ManualCleanupHint),
		"external_call_made":            result.ExternalCallMade,
		"provider_api_call_made":        result.ExternalCallMade,
		"provider_api_mutation":         "enabled",
		"cleanup_attempted":             result.CleanupAttempted,
		"cleanup_succeeded":             result.CleanupSucceeded,
		"cleanup_required":              result.CleanupRequired,
		"request_body_included":         false,
		"response_body_included":        false,
		"headers_included":              false,
		"authorization_header_included": false,
		"idempotency_key_included":      false,
		"contains_token":                false,
		"contains_repository_ref":       false,
		"contains_branch_name":          false,
		"contains_file_content":         false,
	}
}

func providerReviewAttemptLiveCleanupResponse(attempt, ledger map[string]any, ok bool, responseStatus, statusClass string, result reviewBranchExecutionResult) map[string]any {
	state := "cleanup_completed"
	if !ok {
		state = "cleanup_failed"
	}
	return map[string]any{
		"live_cleanup_state":            state,
		"live_cleanup_recorded":         true,
		"live_cleanup_success":          ok,
		"cleanup_status":                safeProviderReviewAttemptResponseStatus(responseStatus),
		"provider_review_attempt_id":    cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":         cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"operation_name":                safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name")),
		"endpoint_key":                  safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key")),
		"provider_status_class":         safeProviderReviewStatusClass(statusClass),
		"attempt":                       providerReviewAttemptLedgerSummary([]map[string]any{attempt})["operations"].([]map[string]any)[0],
		"ledger":                        ledger,
		"live_execution_phase":          safeProviderReviewLiveExecutionPhase(result.ExecutionPhase),
		"live_execution_retryable":      result.Retryable,
		"manual_cleanup_hint":           safeProviderReviewManualCleanupHint(result.ManualCleanupHint),
		"external_call_made":            result.ExternalCallMade,
		"provider_api_call_made":        result.ExternalCallMade,
		"provider_api_mutation":         "enabled",
		"cleanup_attempted":             result.CleanupAttempted,
		"cleanup_succeeded":             result.CleanupSucceeded,
		"cleanup_required":              result.CleanupRequired,
		"request_body_included":         false,
		"response_body_included":        false,
		"headers_included":              false,
		"authorization_header_included": false,
		"idempotency_key_included":      false,
		"contains_token":                false,
		"contains_repository_ref":       false,
		"contains_branch_name":          false,
		"contains_file_content":         false,
	}
}

func safeProviderReviewLiveExecutionPhase(value string) string {
	switch cleanOptionalText(value) {
	case "input_validation", "read_base_ref", "create_review_branch", "commit_starter_files", "open_review_request", "cleanup_review_branch", "completed":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func safeProviderReviewManualCleanupHint(value string) string {
	switch cleanOptionalText(value) {
	case "review_branch_delete_required":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

func providerReviewAttemptLiveExecutionBlockedResponse(attempt map[string]any, state string, missing []string) map[string]any {
	return map[string]any{
		"live_execution_state":       cleanOptionalText(state),
		"live_execution_ready":       false,
		"live_execution_recorded":    false,
		"executed":                   false,
		"provider_review_attempt_id": cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":      cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"operation_name":             safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name")),
		"endpoint_key":               safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key")),
		"missing_evidence":           safeProviderReviewBlockedReasons(missing),
		"external_call_made":         false,
		"provider_api_call_made":     false,
		"provider_api_mutation":      safeProviderReviewProviderAPIMutation(stringFromMap(attempt, "provider_api_mutation")),
		"provider_request_sent":      false,
		"provider_response_received": false,
		"contains_token":             false,
		"contains_repository_ref":    false,
		"contains_branch_name":       false,
		"contains_file_content":      false,
	}
}

func providerReviewAttemptLiveCleanupBlockedResponse(attempt map[string]any, state string, missing []string) map[string]any {
	return map[string]any{
		"live_cleanup_state":         cleanOptionalText(state),
		"live_cleanup_ready":         false,
		"live_cleanup_recorded":      false,
		"provider_review_attempt_id": cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":      cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"operation_name":             safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name")),
		"endpoint_key":               safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key")),
		"missing_evidence":           safeProviderReviewBlockedReasons(missing),
		"external_call_made":         false,
		"provider_api_call_made":     false,
		"provider_api_mutation":      safeProviderReviewProviderAPIMutation(stringFromMap(attempt, "provider_api_mutation")),
		"provider_request_sent":      false,
		"provider_response_received": false,
		"contains_token":             false,
		"contains_repository_ref":    false,
		"contains_branch_name":       false,
		"contains_file_content":      false,
	}
}

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"time"
)

func (s *Server) acquireProviderReviewLiveExecutionLock(ctx context.Context, attemptID string) (bool, func(), error) {
	if err := ctx.Err(); err != nil {
		return false, nil, err
	}
	key := "provider_review_live_execution:" + cleanOptionalID(attemptID)
	if key == "provider_review_live_execution:" {
		return false, nil, fmt.Errorf("provider review live execution lock requires an attempt id")
	}
	if _, loaded := providerReviewLiveExecutionLocks.LoadOrStore(key, struct{}{}); loaded {
		return false, nil, nil
	}
	unlock := func() {
		providerReviewLiveExecutionLocks.Delete(key)
	}
	return true, unlock, nil
}

func providerReviewStarterFilesForLiveExecutionGorm(ctx context.Context, db *gorm.DB, runID, repoID string) (map[string]string, error) {
	var models []GormProjectTemplateFile
	if err := db.WithContext(ctx).
		Where(&GormProjectTemplateFile{ProjectTemplateRunID: validNullString(runID), ProjectGitRepositoryID: validNullString(repoID)}).
		Order("created_at").
		Order("path").
		Find(&models).Error; err != nil {
		return nil, err
	}
	files := make(map[string]string, len(models))
	for _, model := range models {
		path := safeTemplateFilePath(model.Path)
		if path == "" {
			continue
		}
		if len([]byte(model.Content)) > reviewBranchExecutorMaxFileBytes {
			return nil, fmt.Errorf("provider review starter file is too large")
		}
		files[path] = model.Content
	}
	return files, nil
}

func (s *Server) markProviderReviewAttemptLiveExecutionArmed(ctx context.Context, attemptID, idempotencyHash, idempotencyMaterial string) (map[string]any, error) {
	material := map[string]any{}
	if strings.TrimSpace(idempotencyMaterial) != "" {
		_ = json.Unmarshal([]byte(idempotencyMaterial), &material)
	}
	result := s.store.Gorm.WithContext(ctx).Model(&GormProviderReviewAttempt{}).
		Where(&GormProviderReviewAttempt{GormBase: GormBase{ID: attemptID}, Status: "running", OperationName: "create_branch_ref", EndpointKey: "github.create_branch_ref", ProviderAPICallMade: false, ExternalCallMade: false}).
		Where("claimed_at IS NOT NULL").
		Updates(map[string]any{"provider_api_mutation": "enabled", "idempotency_key_hash": idempotencyHash, "idempotency_key_material": JSONValue{Data: material}, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return map[string]any{
			"live_execution_state":       "provider_review_attempt_execution_conflict",
			"live_execution_ready":       false,
			"executed":                   false,
			"provider_review_attempt_id": attemptID,
			"missing_evidence":           []string{"provider_review_attempt_execution_conflict"},
			"external_call_made":         false,
			"provider_api_call_made":     false,
			"provider_api_mutation":      "disabled",
			"contains_token":             false,
			"contains_file_content":      false,
		}, nil
	}
	return nil, nil
}

func (s *Server) recordProviderReviewAttemptLiveExecutionResult(ctx context.Context, attemptID string, attempt map[string]any, result reviewBranchExecutionResult, execErr error) (map[string]any, error) {
	status := "completed"
	responseStatus := "success"
	if execErr != nil {
		status = "failed"
		responseStatus = "failed"
	}
	statusClass := safeProviderReviewStatusClass(result.ProviderStatusClass)
	if statusClass == "" && execErr != nil {
		statusClass = safeProviderReviewStatusClass(providerStatusClassFromError(execErr))
	}
	if statusClass == "" {
		statusClass = "unknown"
	}
	reviewURL := sanitizeProviderReviewURLForResponse(result.ReviewURL)
	approvalID := cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"]))
	var recorded map[string]any
	var ledger map[string]any
	err := s.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model GormProviderReviewAttempt
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&model, &GormProviderReviewAttempt{GormBase: GormBase{ID: attemptID}, Status: "running"}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		now := time.Now().UTC()
		model.Status = status
		model.ResponseDiagnostics = JSONValue{Data: providerReviewAttemptLiveExecutionDiagnostics(attempt, responseStatus, statusClass, result)}
		model.ProviderAPICallMade = result.ExternalCallMade
		model.ProviderAPIMutation = "enabled"
		model.ExternalCallMade = result.ExternalCallMade
		model.ProviderStatusClass = statusClass
		model.ProviderReviewURL = reviewURL
		if !model.ExecutedAt.Valid {
			model.ExecutedAt = validNullTime(now)
		}
		model.LiveExecutionPhase = safeProviderReviewLiveExecutionPhase(result.ExecutionPhase)
		model.LiveExecutionRetryable = result.Retryable
		model.LiveExecutionManualCleanupHint = safeProviderReviewManualCleanupHint(result.ManualCleanupHint)
		model.CleanupAttempted = result.CleanupAttempted
		model.CleanupSucceeded = result.CleanupSucceeded
		model.CleanupRequired = result.CleanupRequired
		if err := tx.Save(&model).Error; err != nil {
			return err
		}
		recorded = providerReviewAttemptMap(model, nil)
		if status == "completed" {
			if err := completeProviderReviewDependentAttemptsGorm(ctx, tx, approvalID, attemptID, responseStatus, statusClass, reviewURL, result); err != nil {
				return err
			}
		} else {
			if err := failProviderReviewDependentAttemptsGorm(ctx, tx, approvalID, safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))); err != nil {
				return err
			}
		}
		var err error
		ledger, err = providerReviewAttemptLedgerForApprovalGorm(ctx, tx, approvalID)
		if err != nil {
			return err
		}
		syncResult, err := syncCanonicalAssetsGorm(ctx, tx)
		if err != nil {
			return fmt.Errorf("syncing canonical assets for provider review live execution: %w", err)
		}
		if s.log != nil {
			s.log.Debug("canonical assets synced in transaction", "reason", "provider_review_attempt.live_execute", "synced_assets", syncResult.SyncedAssets, "inserted_relations", syncResult.InsertedRelations, "pruned_relations", syncResult.PrunedRelations, "inserted_status_snapshots", syncResult.InsertedStatusSnapshots)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return providerReviewAttemptLiveExecutionResponse(recorded, ledger, execErr == nil, responseStatus, statusClass, reviewURL, result), nil
}

func completeProviderReviewDependentAttemptsGorm(ctx context.Context, tx *gorm.DB, approvalID, attemptID, responseStatus, statusClass, reviewURL string, result reviewBranchExecutionResult) error {
	var attempts []GormProviderReviewAttempt
	if err := tx.WithContext(ctx).
		Where(&GormProviderReviewAttempt{OperationApprovalID: approvalID}).
		Where("id <> ?", attemptID).
		Where("operation_name IN ?", []string{"commit_starter_files", "open_review_request"}).
		Where("status IN ?", []string{"planned", "running"}).
		Find(&attempts).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, attempt := range attempts {
		diagnostics := mapFromAny(attempt.ResponseDiagnostics.Data)
		for key, value := range providerReviewAttemptLiveExecutionDiagnostics(providerReviewAttemptMap(attempt, nil), responseStatus, statusClass, result) {
			diagnostics[key] = value
		}
		attempt.Status = "completed"
		attempt.DependencyStatus = "dependency_satisfied"
		attempt.ResponseDiagnostics = JSONValue{Data: diagnostics}
		attempt.ProviderAPICallMade = result.ExternalCallMade
		attempt.ProviderAPIMutation = "enabled"
		attempt.ExternalCallMade = result.ExternalCallMade
		attempt.ProviderStatusClass = statusClass
		attempt.ProviderReviewURL = reviewURL
		attempt.LiveExecutionPhase = safeProviderReviewLiveExecutionPhase(result.ExecutionPhase)
		attempt.LiveExecutionRetryable = result.Retryable
		attempt.LiveExecutionManualCleanupHint = safeProviderReviewManualCleanupHint(result.ManualCleanupHint)
		if !attempt.ExecutedAt.Valid {
			attempt.ExecutedAt = validNullTime(now)
		}
		attempt.CleanupAttempted = result.CleanupAttempted
		attempt.CleanupSucceeded = result.CleanupSucceeded
		attempt.CleanupRequired = result.CleanupRequired
		if err := tx.Save(&attempt).Error; err != nil {
			return err
		}
	}
	return nil
}

func failProviderReviewDependentAttemptsGorm(ctx context.Context, tx *gorm.DB, approvalID, operationName string) error {
	return tx.WithContext(ctx).Model(&GormProviderReviewAttempt{}).
		Where(&GormProviderReviewAttempt{OperationApprovalID: approvalID, DependsOnOperation: operationName, DependencyStatus: "waiting_for_dependency"}).
		Updates(map[string]any{"dependency_status": "dependency_failed", "updated_at": time.Now().UTC()}).Error
}

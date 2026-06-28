package app

import (
	"context"
	"fmt"
	"gorm.io/gorm/clause"
	"strings"
)

type ProviderReviewMutationArmingSnapshotOptions struct {
	OperationApprovalID           string
	DryRun                        bool
	Approval                      map[string]any
	AttemptLedger                 map[string]any
	AttemptLiveExecutionReadiness map[string]bool
}

func RecordProviderReviewMutationArmingSnapshot(ctx context.Context, store *Store, opts ProviderReviewMutationArmingSnapshotOptions) (map[string]any, error) {
	if store == nil || store.Gorm == nil {
		return nil, fmt.Errorf("store is required")
	}
	approvalID := cleanOptionalID(opts.OperationApprovalID)
	if approvalID == "" {
		return nil, fmt.Errorf("operation approval id is required")
	}
	approval := opts.Approval
	var err error
	if len(approval) == 0 {
		approval, err = providerReviewApprovalForArmingSnapshot(ctx, store, approvalID)
		if err != nil {
			return nil, err
		}
	}
	ledger := opts.AttemptLedger
	if len(ledger) == 0 {
		ledger, err = providerReviewAttemptLedgerForApprovalSnapshot(ctx, store, approvalID)
		if err != nil {
			return nil, err
		}
	}
	liveReadiness := opts.AttemptLiveExecutionReadiness
	if liveReadiness == nil && providerReviewAttemptLedgerAttemptCount(ledger) > 0 {
		liveReadiness, err = providerReviewAttemptLiveExecutionReadinessForArmingSnapshot(ctx, store, ledger)
		if err != nil {
			return nil, err
		}
	}
	assetID, assetErr := operationApprovalAssetID(ctx, store, approvalID)
	snapshot := providerReviewMutationArmingSnapshotPayload(approval, ledger, assetErr == nil, liveReadiness)
	ready, state, missing := providerReviewMutationArmingSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                              "provider_review_mutation_arming_snapshot_recording",
		"recording_state":                   state,
		"recording_ready":                   ready,
		"recording_enabled":                 ready && !opts.DryRun,
		"dry_run":                           opts.DryRun,
		"operation_approval_id":             approvalID,
		"project_id":                        cleanOptionalID(fmt.Sprint(approval["project_id"])),
		"operation_approval_asset_observed": assetErr == nil,
		"snapshot":                          snapshot,
		"snapshots_written":                 0,
		"snapshots_skipped_as_duplicate":    0,
		"provider_review_mutation_arming_snapshot_written": false,
		"asset_status_snapshot_written":                    false,
		"operation_log_written":                            false,
		"external_call_made":                               false,
		"provider_api_call_made":                           false,
		"provider_api_mutation":                            "disabled",
		"mutation_armed":                                   false,
		"contains_token":                                   false,
		"contains_provider_url":                            false,
		"contains_repository_ref":                          false,
		"contains_branch_name":                             false,
		"contains_file_content":                            false,
		"canonical_asset_status_snapshot_try":              false,
		"snapshot_commit_attempted":                        false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"operation_approval_asset_missing"}
		result["message"] = "Provider review mutation arming snapshot is derived, but the canonical operation_approval asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review mutation arming snapshot is waiting for an approved provider-review execution approval, rehearsal-ready arming plan, and persisted attempt ledger; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review mutation arming snapshot was not written."
		return result, nil
	}
	status, health := providerReviewMutationArmingSnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "provider review mutation arming snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording provider review mutation arming snapshot: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["provider_review_mutation_arming_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized provider review mutation arming snapshot recorded from local approval and attempt-ledger evidence."
	return result, nil
}

func providerReviewApprovalForArmingSnapshot(ctx context.Context, store *Store, approvalID string) (map[string]any, error) {
	var approval GormOperationApproval
	if err := store.Gorm.WithContext(ctx).First(&approval, &GormOperationApproval{GormBase: GormBase{ID: approvalID}}).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return map[string]any{
		"id":               approval.ID,
		"project_id":       nullableStringValue(approval.ProjectID),
		"operation_run_id": nullableStringValue(approval.OperationRunID),
		"resource_type":    approval.ResourceType,
		"resource_id":      approval.ResourceID,
		"action":           approval.Action,
		"title":            approval.Title,
		"status":           approval.Status,
		"request_payload":  approval.RequestPayload,
		"created_at":       approval.CreatedAt,
		"updated_at":       approval.UpdatedAt,
	}, nil
}

func providerReviewAttemptLedgerForApprovalSnapshot(ctx context.Context, store *Store, approvalID string) (map[string]any, error) {
	var rows []GormProviderReviewAttempt
	if err := store.Gorm.WithContext(ctx).
		Where(&GormProviderReviewAttempt{OperationApprovalID: approvalID}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "operation_order"}}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "operation_name"}}).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	attempts := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		attempts = append(attempts, map[string]any{
			"id":                     row.ID,
			"operation_name":         row.OperationName,
			"endpoint_key":           row.EndpointKey,
			"status":                 row.Status,
			"replay_check":           row.ReplayCheck,
			"conflict_policy":        row.ConflictPolicy,
			"retry_policy":           row.RetryPolicy,
			"operation_order":        row.OperationOrder,
			"depends_on_operation":   row.DependsOnOperation,
			"dependency_status":      row.DependencyStatus,
			"request_summary":        row.RequestSummary,
			"response_diagnostics":   row.ResponseDiagnostics,
			"provider_api_call_made": row.ProviderAPICallMade,
			"provider_api_mutation":  row.ProviderAPIMutation,
			"external_call_made":     row.ExternalCallMade,
			"claimed_at":             nullableTimeAny(row.ClaimedAt),
			"claimed_by_user_id":     nullableStringValue(row.ClaimedByUserID),
		})
	}
	return providerReviewAttemptLedgerSummary(attempts), nil
}

func providerReviewAttemptLiveExecutionReadinessForArmingSnapshot(ctx context.Context, store *Store, ledger map[string]any) (map[string]bool, error) {
	attemptIDs := providerReviewAttemptLedgerAttemptIDs(ledger)
	if len(attemptIDs) == 0 {
		return map[string]bool{}, nil
	}
	var assets []GormAsset
	if err := store.Gorm.WithContext(ctx).
		Where(&GormAsset{AssetType: "provider_review_attempt", SourceTable: "provider_review_attempts"}).
		Find(&assets).Error; err != nil {
		return nil, err
	}
	attemptSet := map[string]bool{}
	for _, id := range attemptIDs {
		attemptSet[id] = true
	}
	observed := map[string]bool{}
	for _, asset := range assets {
		id := ""
		if asset.SourceID.Valid {
			id = cleanOptionalID(asset.SourceID.String)
		}
		if id == "" || !attemptSet[id] {
			continue
		}
		var count int64
		if err := store.Gorm.WithContext(ctx).
			Model(&GormAssetStatusSnapshot{}).
			Where(&GormAssetStatusSnapshot{AssetID: asset.ID, Status: "provider_review_attempt_live_execution_review_ready"}).
			Count(&count).Error; err != nil {
			return nil, err
		}
		if count > 0 {
			observed[id] = true
		}
	}
	return observed, nil
}

func operationApprovalAssetID(ctx context.Context, store *Store, approvalID string) (string, error) {
	var asset GormAsset
	if err := store.Gorm.WithContext(ctx).
		Where(&GormAsset{AssetType: "operation_approval", SourceTable: "operation_approvals", SourceID: validNullString(approvalID)}).
		First(&asset).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return "", fmt.Errorf("operation_approval asset for %s not found; run db sync-assets first", approvalID)
		}
		return "", err
	}
	assetID := strings.TrimSpace(asset.ID)
	if assetID == "" || assetID == "<nil>" {
		return "", fmt.Errorf("operation_approval asset for %s has empty id", approvalID)
	}
	return assetID, nil
}

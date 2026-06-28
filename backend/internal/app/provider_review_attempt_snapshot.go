package app

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

type ProviderReviewAttemptSnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
}

func RecordProviderReviewAttemptSnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptSnapshotOptions) (map[string]any, error) {
	if store == nil || store.Gorm == nil {
		return nil, fmt.Errorf("store is required")
	}
	attemptID := cleanOptionalID(opts.AttemptID)
	if attemptID == "" {
		return nil, fmt.Errorf("provider review attempt id is required")
	}
	attempt := opts.Attempt
	var err error
	if len(attempt) == 0 {
		attempt, err = providerReviewAttemptForSnapshot(ctx, store.Gorm, attemptID)
		if err != nil {
			return nil, err
		}
	}
	assetID, assetErr := providerReviewAttemptAssetID(ctx, store.Gorm, attemptID)
	snapshot := providerReviewAttemptSnapshotPayload(attempt, assetErr == nil)
	ready, state, missing := providerReviewAttemptSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                     "provider_review_attempt_snapshot_recording",
		"recording_state":                          state,
		"recording_ready":                          ready,
		"recording_enabled":                        ready && !opts.DryRun,
		"dry_run":                                  opts.DryRun,
		"provider_review_attempt_id":               attemptID,
		"operation_approval_id":                    cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"project_template_run_id":                  cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed":   assetErr == nil,
		"snapshot":                                 snapshot,
		"snapshots_written":                        0,
		"snapshots_skipped_as_duplicate":           0,
		"provider_review_attempt_snapshot_written": false,
		"asset_status_snapshot_written":            false,
		"operation_log_written":                    false,
		"external_call_made":                       false,
		"provider_api_call_made":                   false,
		"provider_api_mutation":                    "disabled",
		"request_body_included":                    false,
		"response_body_included":                   false,
		"headers_included":                         false,
		"idempotency_key_included":                 false,
		"contains_token":                           false,
		"contains_provider_url":                    false,
		"contains_repository_ref":                  false,
		"contains_branch_name":                     false,
		"contains_file_content":                    false,
		"canonical_asset_status_snapshot_try":      false,
		"snapshot_commit_attempted":                false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt snapshot is waiting for a valid no-call provider review attempt state; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptSnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "provider review attempt snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording provider review attempt snapshot: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["provider_review_attempt_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized provider review attempt snapshot recorded from local no-call ledger evidence."
	return result, nil
}

func providerReviewAttemptForSnapshot(ctx context.Context, db *gorm.DB, attemptID string) (map[string]any, error) {
	if db == nil {
		return nil, fmt.Errorf("gorm database is not configured")
	}
	var attempt GormProviderReviewAttempt
	if err := db.WithContext(ctx).First(&attempt, &GormProviderReviewAttempt{GormBase: GormBase{ID: attemptID}}).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var approval GormOperationApproval
	if err := db.WithContext(ctx).First(&approval, &GormOperationApproval{GormBase: GormBase{ID: attempt.OperationApprovalID}}).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return map[string]any{
		"id":                      attempt.ID,
		"operation_approval_id":   attempt.OperationApprovalID,
		"project_template_run_id": nullableStringValue(attempt.ProjectTemplateRunID),
		"provider_type":           attempt.ProviderType,
		"review_kind":             attempt.ReviewKind,
		"operation_name":          attempt.OperationName,
		"endpoint_key":            attempt.EndpointKey,
		"status":                  attempt.Status,
		"replay_check":            attempt.ReplayCheck,
		"conflict_policy":         attempt.ConflictPolicy,
		"retry_policy":            attempt.RetryPolicy,
		"operation_order":         attempt.OperationOrder,
		"depends_on_operation":    attempt.DependsOnOperation,
		"dependency_status":       attempt.DependencyStatus,
		"request_summary":         attempt.RequestSummary,
		"response_diagnostics":    attempt.ResponseDiagnostics,
		"provider_api_call_made":  attempt.ProviderAPICallMade,
		"provider_api_mutation":   attempt.ProviderAPIMutation,
		"external_call_made":      attempt.ExternalCallMade,
		"claimed_at":              nullableTimeAny(attempt.ClaimedAt),
		"approval_id":             approval.ID,
		"approval_project_id":     nullableStringValue(approval.ProjectID),
		"approval_action":         approval.Action,
		"approval_status":         approval.Status,
	}, nil
}

func providerReviewAttemptAssetID(ctx context.Context, db *gorm.DB, attemptID string) (string, error) {
	if db == nil {
		return "", fmt.Errorf("gorm database is not configured")
	}
	var asset GormAsset
	if err := db.WithContext(ctx).
		Where(&GormAsset{AssetType: "provider_review_attempt", SourceTable: "provider_review_attempts", SourceID: validNullString(attemptID)}).
		First(&asset).Error; err != nil {
		if errorsIsRecordNotFound(err) {
			return "", fmt.Errorf("provider_review_attempt asset for %s not found; run db sync-assets first", attemptID)
		}
		return "", err
	}
	assetID := strings.TrimSpace(asset.ID)
	if assetID == "" || assetID == "<nil>" {
		return "", fmt.Errorf("provider_review_attempt asset for %s has empty id", attemptID)
	}
	return assetID, nil
}

func providerReviewAttemptSnapshotPayload(attempt map[string]any, assetObserved bool) map[string]any {
	status := safeProviderReviewAttemptStatus(stringFromMap(attempt, "status"))
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	dependencyStatus := safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(attempt, "dependency_status"))
	claimRecorded := providerReviewAttemptClaimRecorded(attempt)
	statusSnapshotWriteEligible := assetObserved && operationName != "" && endpointKey != "" && status != ""
	return map[string]any{
		"mode":                                   "provider_review_attempt_snapshot",
		"provider_review_attempt_id":             cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":                  cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"project_template_run_id":                cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed": assetObserved,
		"provider_type":                          cleanOptionalText(stringFromMap(attempt, "provider_type")),
		"review_kind":                            cleanOptionalText(stringFromMap(attempt, "review_kind")),
		"operation_name":                         operationName,
		"endpoint_key":                           endpointKey,
		"attempt_status":                         status,
		"dependency_status":                      dependencyStatus,
		"operation_order":                        intFromAny(attempt["operation_order"], 0),
		"depends_on_operation":                   safeProviderReviewAttemptDependencyName(stringFromMap(attempt, "depends_on_operation")),
		"replay_check":                           cleanOptionalText(stringFromMap(attempt, "replay_check")),
		"conflict_policy":                        cleanOptionalText(stringFromMap(attempt, "conflict_policy")),
		"retry_policy":                           cleanOptionalText(stringFromMap(attempt, "retry_policy")),
		"claim_recorded":                         claimRecorded,
		"provider_api_call_made":                 false,
		"provider_api_mutation":                  "disabled",
		"external_call_made":                     false,
		"operation_log_written":                  false,
		"request_body_included":                  false,
		"response_body_included":                 false,
		"headers_included":                       false,
		"idempotency_key_included":               false,
		"contains_token":                         false,
		"contains_provider_url":                  false,
		"contains_repository_ref":                false,
		"contains_branch_name":                   false,
		"contains_file_content":                  false,
		"status_snapshot_write_eligible":         statusSnapshotWriteEligible,
		"status_snapshot_written":                statusSnapshotWriteEligible,
	}
}

func providerReviewAttemptSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	if !boolOnlyFromAny(snapshot["provider_review_attempt_asset_observed"]) {
		missing = append(missing, "provider_review_attempt_asset_missing")
	}
	if cleanOptionalID(fmt.Sprint(snapshot["provider_review_attempt_id"])) == "" {
		missing = append(missing, "provider_review_attempt_id_missing")
	}
	if safeProviderReviewAttemptOperationName(stringFromMap(snapshot, "operation_name")) == "" {
		missing = append(missing, "provider_review_attempt_operation_missing")
	}
	if safeProviderReviewEndpointKey(stringFromMap(snapshot, "endpoint_key")) == "" {
		missing = append(missing, "provider_review_attempt_endpoint_missing")
	}
	state := safeProviderReviewAttemptStatus(stringFromMap(snapshot, "attempt_status"))
	if state == "" {
		missing = append(missing, "provider_review_attempt_status_missing")
		state = "unknown"
	}
	// These flags are intentionally rechecked even though the snapshot payload
	// hard-codes them to no-call values; keep the readiness contract defensive.
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" {
		missing = append(missing, "provider_review_attempt_not_no_call")
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptSnapshotStatusHealth(state string) (string, string) {
	status := "provider_review_attempt_" + safeProviderReviewAttemptStatus(state)
	if status == "provider_review_attempt_" {
		status = "provider_review_attempt_unknown"
	}
	health := "warning"
	switch safeProviderReviewAttemptStatus(state) {
	case "completed":
		health = "low"
	case "failed", "blocked":
		health = "high"
	}
	return status, health
}

package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptBranchPolicySnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
	Ledger    map[string]any
}

func RecordProviderReviewAttemptBranchPolicySnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptBranchPolicySnapshotOptions) (map[string]any, error) {
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
		attempt, err = providerReviewAttemptForActivationSnapshot(ctx, store, attemptID)
		if err != nil {
			return nil, err
		}
	}
	approvalID := cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"]))
	ledger := opts.Ledger
	if len(ledger) == 0 {
		ledger, err = providerReviewAttemptLedgerForApprovalSnapshot(ctx, store, approvalID)
		if err != nil {
			return nil, err
		}
	}
	assetID, assetErr := providerReviewAttemptAssetID(ctx, store.Gorm, attemptID)
	snapshot := providerReviewAttemptBranchPolicySnapshotPayload(attempt, ledger, assetErr == nil)
	ready, state, missing := providerReviewAttemptBranchPolicySnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_branch_policy_snapshot_recording",
		"recording_state":                        state,
		"recording_ready":                        ready,
		"recording_enabled":                      ready && !opts.DryRun,
		"dry_run":                                opts.DryRun,
		"provider_review_attempt_id":             attemptID,
		"operation_approval_id":                  approvalID,
		"project_template_run_id":                cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed": assetErr == nil,
		"snapshot":                               snapshot,
		"snapshots_written":                      0,
		"snapshots_skipped_as_duplicate":         0,
		"provider_review_attempt_branch_policy_snapshot_written": false,
		"asset_status_snapshot_written":                          false,
		"operation_log_written":                                  false,
		"external_call_made":                                     false,
		"provider_api_call_made":                                 false,
		"provider_api_mutation":                                  "disabled",
		"mutation_armed":                                         false,
		"branch_policy_verified":                                 false,
		"branch_ref_created":                                     false,
		"review_request_created":                                 false,
		"contains_token":                                         false,
		"contains_provider_url":                                  false,
		"contains_repository_ref":                                false,
		"contains_branch_name":                                   false,
		"contains_file_content":                                  false,
		"canonical_asset_status_snapshot_try":                    false,
		"snapshot_commit_attempted":                              false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt branch policy snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt branch policy snapshot is waiting for the current execution candidate and branch policy metadata; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt branch policy snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptBranchPolicySnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "provider review attempt branch policy snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording provider review attempt branch policy snapshot recorded: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["provider_review_attempt_branch_policy_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized provider review attempt branch policy snapshot recorded from local branch policy metadata."
	return result, nil
}

func providerReviewAttemptBranchPolicySnapshotPayload(attempt, ledger map[string]any, assetObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	branchPolicyPlan := mapFromAny(dispatchPlan["branch_policy_plan"])
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	branchPolicyContractReady := providerReviewAttemptPlanMatchesOperation(branchPolicyPlan, "redacted_attempt_branch_policy_plan", operationName, endpointKey)
	branchPolicyMetadataReady := providerReviewAttemptBranchPolicyPlanReadyForOperation(branchPolicyPlan, operationName, endpointKey)
	// Structural write eligibility is intentionally weaker than recording readiness; readiness also requires the contract and metadata checks below.
	statusSnapshotWriteEligible := assetObserved && candidateMatches && len(branchPolicyPlan) > 0
	return map[string]any{
		"mode":                                       "provider_review_attempt_branch_policy_snapshot",
		"provider_review_attempt_id":                 cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":                      cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"project_template_run_id":                    cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed":     assetObserved,
		"operation_name":                             operationName,
		"endpoint_key":                               endpointKey,
		"attempt_status":                             safeProviderReviewAttemptStatus(stringFromMap(attempt, "status")),
		"dependency_status":                          safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(attempt, "dependency_status")),
		"operation_order":                            intFromAny(attempt["operation_order"], 0),
		"candidate_observed":                         len(candidate) > 0,
		"candidate_matches_attempt":                  candidateMatches,
		"candidate_status":                           cleanOptionalText(stringFromMap(candidate, "status")),
		"dispatch_plan_observed":                     len(dispatchPlan) > 0,
		"branch_policy_plan_observed":                len(branchPolicyPlan) > 0,
		"branch_policy_contract_ready":               branchPolicyContractReady,
		"branch_policy_metadata_ready":               branchPolicyMetadataReady,
		"branch_policy_state":                        cleanOptionalText(stringFromMap(branchPolicyPlan, "branch_policy_state")),
		"branch_policy_ready":                        boolOnlyFromAny(branchPolicyPlan["branch_policy_ready"]),
		"branch_policy_ready_reason":                 cleanOptionalText(stringFromMap(branchPolicyPlan, "branch_policy_ready_reason")),
		"policy_scope":                               cleanOptionalText(stringFromMap(branchPolicyPlan, "policy_scope")),
		"target_branch_policy":                       cleanOptionalText(stringFromMap(branchPolicyPlan, "target_branch_policy")),
		"review_branch_policy":                       cleanOptionalText(stringFromMap(branchPolicyPlan, "review_branch_policy")),
		"requires_review_branch":                     boolOnlyFromAny(branchPolicyPlan["requires_review_branch"]),
		"requires_default_branch_protection":         boolOnlyFromAny(branchPolicyPlan["requires_default_branch_protection"]),
		"requires_review_request":                    boolOnlyFromAny(branchPolicyPlan["requires_review_request"]),
		"requires_operator_policy_review":            boolOnlyFromAny(branchPolicyPlan["requires_operator_policy_review"]),
		"requires_mutation_arming":                   boolOnlyFromAny(branchPolicyPlan["requires_mutation_arming"]),
		"default_branch_direct_write_allowed":        false,
		"protected_branch_direct_write_allowed":      false,
		"starter_file_commit_to_default":             false,
		"review_branch_materialized":                 false,
		"default_branch_materialized":                false,
		"protected_branch_rules_materialized":        false,
		"branch_policy_verified":                     false,
		"branch_ref_created":                         false,
		"review_request_created":                     false,
		"external_call_made":                         false,
		"provider_api_call_made":                     false,
		"provider_api_mutation":                      "disabled",
		"repository_ref_included":                    false,
		"branch_name_included":                       false,
		"protected_branch_rules_included":            false,
		"contains_token":                             false,
		"contains_provider_url":                      false,
		"contains_repository_ref":                    false,
		"contains_branch_name":                       false,
		"contains_file_content":                      false,
		"status_snapshot_write_eligible":             statusSnapshotWriteEligible,
		"status_snapshot_written":                    statusSnapshotWriteEligible,
		"branch_policy_boundary_redacted":            true,
		"future_live_branch_policy_still_blocked":    true,
		"future_live_provider_request_still_blocked": true,
	}
}

func providerReviewAttemptBranchPolicySnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "branch_policy_blocked"
	if boolOnlyFromAny(snapshot["branch_policy_metadata_ready"]) {
		state = "branch_policy_metadata_ready"
	}
	if !boolOnlyFromAny(snapshot["provider_review_attempt_asset_observed"]) {
		missing = append(missing, "provider_review_attempt_asset_missing")
	}
	if cleanOptionalID(fmt.Sprint(snapshot["provider_review_attempt_id"])) == "" {
		missing = append(missing, "provider_review_attempt_id_missing")
	}
	if !boolOnlyFromAny(snapshot["candidate_observed"]) {
		missing = append(missing, "provider_review_execution_candidate_missing")
	}
	if !boolOnlyFromAny(snapshot["candidate_matches_attempt"]) {
		missing = append(missing, "provider_review_attempt_not_current_candidate")
	}
	if !boolOnlyFromAny(snapshot["branch_policy_plan_observed"]) {
		missing = append(missing, "provider_review_branch_policy_plan_missing")
	}
	if !boolOnlyFromAny(snapshot["branch_policy_contract_ready"]) {
		missing = append(missing, "provider_review_branch_policy_contract_not_ready")
	}
	if !boolOnlyFromAny(snapshot["branch_policy_metadata_ready"]) {
		missing = append(missing, "provider_review_branch_policy_metadata_not_ready")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["branch_policy_verified"]) ||
		boolOnlyFromAny(snapshot["branch_ref_created"]) ||
		boolOnlyFromAny(snapshot["review_request_created"]) ||
		boolOnlyFromAny(snapshot["review_branch_materialized"]) ||
		boolOnlyFromAny(snapshot["default_branch_materialized"]) ||
		boolOnlyFromAny(snapshot["protected_branch_rules_materialized"]) ||
		boolOnlyFromAny(snapshot["repository_ref_included"]) ||
		boolOnlyFromAny(snapshot["branch_name_included"]) ||
		boolOnlyFromAny(snapshot["protected_branch_rules_included"]) {
		missing = append(missing, "provider_review_branch_policy_not_redacted")
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptBranchPolicySnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "branch_policy_metadata_ready":
		return "provider_review_attempt_branch_policy_metadata_ready", "low"
	case "branch_policy_blocked":
		return "provider_review_attempt_branch_policy_blocked", "warning"
	default:
		return "provider_review_attempt_branch_policy_unknown", "warning"
	}
}

package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptLiveExecutionReadinessSnapshotOptions struct {
	AttemptID                string
	DryRun                   bool
	Attempt                  map[string]any
	Ledger                   map[string]any
	ObservedSnapshotStatuses map[string]bool
}

type providerReviewAttemptReadinessEvidence struct {
	Name   string
	Status string
}

func RecordProviderReviewAttemptLiveExecutionReadinessSnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptLiveExecutionReadinessSnapshotOptions) (map[string]any, error) {
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
	assetObserved := assetErr == nil && assetID != ""
	observed := opts.ObservedSnapshotStatuses
	if observed == nil && assetObserved {
		observed, err = providerReviewAttemptObservedSnapshotStatuses(ctx, store, assetID, providerReviewAttemptLiveExecutionRequiredEvidence())
		if err != nil {
			return nil, err
		}
	}
	snapshot := providerReviewAttemptLiveExecutionReadinessSnapshotPayload(attempt, ledger, assetObserved, observed)
	ready, state, missing := providerReviewAttemptLiveExecutionReadinessSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_live_execution_readiness_snapshot_recording",
		"recording_state":                        state,
		"recording_ready":                        ready,
		"recording_enabled":                      ready && !opts.DryRun,
		"dry_run":                                opts.DryRun,
		"provider_review_attempt_id":             attemptID,
		"operation_approval_id":                  approvalID,
		"project_template_run_id":                cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed": assetObserved,
		"snapshot":                               snapshot,
		"status_snapshot_write_eligible":         boolOnlyFromAny(snapshot["status_snapshot_write_eligible"]),
		"snapshots_written":                      0,
		"snapshots_skipped_as_duplicate":         0,
		"provider_review_attempt_live_execution_readiness_snapshot_written": false,
		"asset_status_snapshot_written":                                     false,
		"operation_log_written":                                             false,
		"external_call_made":                                                false,
		"provider_api_call_made":                                            false,
		"provider_api_mutation":                                             "disabled",
		"provider_request_sent":                                             false,
		"provider_response_received":                                        false,
		"idempotency_claim_recorded":                                        false,
		"idempotency_key_included":                                          false,
		"mutation_armed":                                                    false,
		"live_adapter_implemented":                                          false,
		"future_live_execution_still_blocked":                               true,
		"contains_token":                                                    false,
		"contains_provider_url":                                             false,
		"contains_repository_ref":                                           false,
		"contains_branch_name":                                              false,
		"contains_file_content":                                             false,
		"canonical_asset_status_snapshot_try":                               false,
		"snapshot_commit_attempted":                                         false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if !assetObserved {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt live execution readiness snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt live execution readiness snapshot is waiting for required no-call snapshot evidence; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt live execution readiness snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptLiveExecutionReadinessSnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "provider review attempt live execution readiness snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording provider review attempt live execution readiness snapshot recorded: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["provider_review_attempt_live_execution_readiness_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized provider review attempt live execution readiness snapshot recorded from local no-call evidence."
	return result, nil
}

func providerReviewAttemptObservedSnapshotStatuses(ctx context.Context, store *Store, assetID string, evidence []providerReviewAttemptReadinessEvidence) (map[string]bool, error) {
	observed := map[string]bool{}
	for _, item := range evidence {
		seen, err := providerReviewAttemptStatusObserved(ctx, store, assetID, item.Status)
		if err != nil {
			return nil, err
		}
		if seen {
			observed[item.Status] = true
		}
	}
	return observed, nil
}

func providerReviewAttemptLiveExecutionReadinessSnapshotPayload(attempt, ledger map[string]any, assetObserved bool, observed map[string]bool) map[string]any {
	if observed == nil {
		observed = map[string]bool{}
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	evidence := providerReviewAttemptLiveExecutionRequiredEvidence()
	observedEvidence := make([]map[string]any, 0, len(evidence))
	missingEvidence := []string{}
	for _, item := range evidence {
		seen := observed[item.Status]
		observedEvidence = append(observedEvidence, map[string]any{
			"name":     item.Name,
			"status":   item.Status,
			"observed": seen,
		})
		if !seen {
			missingEvidence = append(missingEvidence, item.Status)
		}
	}
	allEvidenceObserved := len(missingEvidence) == 0
	return map[string]any{
		"mode":                                       "provider_review_attempt_live_execution_readiness_snapshot",
		"provider_review_attempt_id":                 cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":                      cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"project_template_run_id":                    cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed":     assetObserved,
		"operation_name":                             operationName,
		"endpoint_key":                               endpointKey,
		"attempt_status":                             safeProviderReviewAttemptStatus(stringFromMap(attempt, "status")),
		"dependency_status":                          safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(attempt, "dependency_status")),
		"candidate_observed":                         len(candidate) > 0,
		"candidate_matches_attempt":                  candidateMatches,
		"candidate_status":                           cleanOptionalText(stringFromMap(candidate, "status")),
		"required_snapshot_count":                    len(evidence),
		"observed_snapshot_count":                    len(evidence) - len(missingEvidence),
		"required_snapshot_evidence":                 observedEvidence,
		"missing_snapshot_statuses":                  missingEvidence,
		"all_required_snapshot_evidence_observed":    allEvidenceObserved,
		"live_execution_review_ready":                assetObserved && candidateMatches && allEvidenceObserved,
		"future_live_execution_still_blocked":        true,
		"requires_operator_review":                   true,
		"requires_live_adapter_implementation":       true,
		"requires_mutation_arming":                   true,
		"live_adapter_implemented":                   false,
		"mutation_armed":                             false,
		"idempotency_claim_recorded":                 false,
		"idempotency_key_included":                   false,
		"provider_request_sent":                      false,
		"provider_response_received":                 false,
		"provider_api_call_made":                     false,
		"external_call_made":                         false,
		"provider_api_mutation":                      "disabled",
		"request_body_included":                      false,
		"response_body_included":                     false,
		"headers_included":                           false,
		"provider_url_included":                      false,
		"provider_request_id_included":               false,
		"contains_token":                             false,
		"contains_provider_url":                      false,
		"contains_repository_ref":                    false,
		"contains_branch_name":                       false,
		"contains_file_content":                      false,
		"status_snapshot_write_eligible":             assetObserved && len(candidate) > 0 && candidateMatches && allEvidenceObserved,
		"status_snapshot_written":                    false,
		"live_execution_readiness_boundary_redacted": true,
	}
}

func providerReviewAttemptLiveExecutionReadinessSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	for _, item := range []struct {
		field  string
		reason string
	}{
		{"provider_review_attempt_asset_observed", "provider_review_attempt_asset_missing"},
		{"candidate_observed", "provider_review_execution_candidate_missing"},
		{"candidate_matches_attempt", "provider_review_attempt_not_current_candidate"},
		{"all_required_snapshot_evidence_observed", "provider_review_required_snapshot_evidence_missing"},
		{"future_live_execution_still_blocked", "provider_review_live_execution_boundary_not_blocked"},
		{"requires_operator_review", "provider_review_operator_review_requirement_missing"},
		{"requires_live_adapter_implementation", "provider_review_live_adapter_requirement_missing"},
		{"requires_mutation_arming", "provider_review_mutation_arming_requirement_missing"},
	} {
		if !boolOnlyFromAny(snapshot[item.field]) {
			missing = append(missing, item.reason)
		}
	}
	if cleanOptionalID(fmt.Sprint(snapshot["provider_review_attempt_id"])) == "" {
		missing = append(missing, "provider_review_attempt_id_missing")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["live_adapter_implemented"]) ||
		boolOnlyFromAny(snapshot["provider_request_sent"]) ||
		boolOnlyFromAny(snapshot["provider_response_received"]) ||
		boolOnlyFromAny(snapshot["idempotency_claim_recorded"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_included"]) ||
		boolOnlyFromAny(snapshot["request_body_included"]) ||
		boolOnlyFromAny(snapshot["response_body_included"]) ||
		boolOnlyFromAny(snapshot["headers_included"]) ||
		boolOnlyFromAny(snapshot["provider_url_included"]) ||
		boolOnlyFromAny(snapshot["provider_request_id_included"]) ||
		boolOnlyFromAny(snapshot["contains_token"]) ||
		boolOnlyFromAny(snapshot["contains_provider_url"]) ||
		boolOnlyFromAny(snapshot["contains_repository_ref"]) ||
		boolOnlyFromAny(snapshot["contains_branch_name"]) ||
		boolOnlyFromAny(snapshot["contains_file_content"]) {
		missing = append(missing, "provider_review_live_execution_not_no_call")
	}
	state := "live_execution_review_blocked"
	if len(missing) == 0 {
		state = "live_execution_review_ready"
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptLiveExecutionRequiredEvidence() []providerReviewAttemptReadinessEvidence {
	return []providerReviewAttemptReadinessEvidence{
		{Name: "attempt_snapshot", Status: "provider_review_attempt_planned"},
		{Name: "activation", Status: "provider_review_attempt_activation_blocked"},
		{Name: "credential", Status: "provider_review_attempt_credential_contract_ready"},
		{Name: "request_envelope", Status: "provider_review_attempt_request_envelope_contract_ready"},
		{Name: "idempotency", Status: "provider_review_attempt_idempotency_metadata_ready"},
		{Name: "request_validation", Status: "provider_review_attempt_request_validation_metadata_ready"},
		{Name: "request_materialization", Status: "provider_review_attempt_request_materialization_contract_ready"},
		{Name: "branch_policy", Status: "provider_review_attempt_branch_policy_metadata_ready"},
		{Name: "runtime", Status: "provider_review_attempt_runtime_contract_ready"},
		{Name: "adapter_rehearsal", Status: "provider_review_attempt_adapter_rehearsal_contract_ready"},
		{Name: "adapter_blueprint", Status: "provider_review_attempt_adapter_blueprint_contract_ready"},
		{Name: "live_adapter_contract", Status: "provider_review_attempt_live_adapter_contract_metadata_ready"},
		{Name: "invocation", Status: "provider_review_attempt_invocation_contract_ready"},
		{Name: "execution_lock", Status: "provider_review_attempt_execution_lock_metadata_ready"},
		{Name: "send", Status: "provider_review_attempt_send_blocked"},
		{Name: "transport", Status: "provider_review_attempt_transport_metadata_ready"},
		{Name: "retry_backoff", Status: "provider_review_attempt_retry_backoff_metadata_ready"},
		{Name: "response", Status: "provider_review_attempt_response_metadata_ready"},
		{Name: "result_recording", Status: "provider_review_attempt_result_recording_metadata_ready"},
		{Name: "provider_call_boundary", Status: "provider_review_attempt_provider_call_boundary_metadata_ready"},
		{Name: "transaction", Status: "provider_review_attempt_transaction_metadata_ready"},
	}
}

func providerReviewAttemptLiveExecutionReadinessSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "live_execution_review_ready":
		return "provider_review_attempt_live_execution_review_ready", "low"
	case "live_execution_review_blocked":
		return "provider_review_attempt_live_execution_review_blocked", "warning"
	default:
		return "provider_review_attempt_live_execution_review_unknown", "warning"
	}
}

package app

import (
	"context"
	"fmt"
)

type ProviderReviewAttemptAdapterRehearsalSnapshotOptions struct {
	AttemptID string
	DryRun    bool
	Attempt   map[string]any
	Ledger    map[string]any
}

func RecordProviderReviewAttemptAdapterRehearsalSnapshot(ctx context.Context, store *Store, opts ProviderReviewAttemptAdapterRehearsalSnapshotOptions) (map[string]any, error) {
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
	snapshot := providerReviewAttemptAdapterRehearsalSnapshotPayload(attempt, ledger, assetErr == nil)
	ready, state, missing := providerReviewAttemptAdapterRehearsalSnapshotReadiness(snapshot)
	result := map[string]any{
		"mode":                                   "provider_review_attempt_adapter_rehearsal_snapshot_recording",
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
		"provider_review_attempt_adapter_rehearsal_snapshot_written": false,
		"asset_status_snapshot_written":                              false,
		"operation_log_written":                                      false,
		"external_call_made":                                         false,
		"provider_api_call_made":                                     false,
		"provider_api_mutation":                                      "disabled",
		"mutation_armed":                                             false,
		"live_adapter_implemented":                                   false,
		"provider_client_constructed":                                false,
		"contains_token":                                             false,
		"contains_provider_url":                                      false,
		"contains_repository_ref":                                    false,
		"contains_branch_name":                                       false,
		"contains_file_content":                                      false,
		"canonical_asset_status_snapshot_try":                        false,
		"snapshot_commit_attempted":                                  false,
	}
	if len(missing) > 0 {
		result["missing_evidence"] = missing
	}
	if assetErr != nil {
		result["recording_state"] = "asset_missing"
		result["recording_ready"] = false
		result["recording_enabled"] = false
		result["missing_evidence"] = []string{"provider_review_attempt_asset_missing"}
		result["message"] = "Provider review attempt adapter rehearsal snapshot is derived, but the canonical provider_review_attempt asset is missing; run db sync-assets before recording."
		return result, nil
	}
	if !ready {
		result["message"] = "Provider review attempt adapter rehearsal snapshot is waiting for the current execution candidate and redacted adapter rehearsal contract; no snapshot was written."
		return result, nil
	}
	if opts.DryRun {
		result["message"] = "Dry run only; sanitized provider review attempt adapter rehearsal snapshot was not written."
		return result, nil
	}
	status, health := providerReviewAttemptAdapterRehearsalSnapshotStatusHealth(state)
	written, err := recordAssetStatusSnapshotIfChanged(ctx, store.Gorm, assetID, status, health, "provider review attempt adapter rehearsal snapshot recorded", snapshot)
	if err != nil {
		return nil, fmt.Errorf("recording provider review attempt adapter rehearsal snapshot recorded: %w", err)
	}
	result["recording_state"] = state
	result["snapshot_status"] = status
	result["snapshot_health"] = health
	result["snapshots_written"] = written
	result["snapshot_commit_attempted"] = true
	result["canonical_asset_status_snapshot_try"] = true
	result["snapshots_skipped_as_duplicate"] = 1 - written
	result["provider_review_attempt_adapter_rehearsal_snapshot_written"] = written > 0
	result["asset_status_snapshot_written"] = written > 0
	result["message"] = "Sanitized provider review attempt adapter rehearsal snapshot recorded from local redacted rehearsal metadata."
	return result, nil
}

func providerReviewAttemptAdapterRehearsalSnapshotPayload(attempt, ledger map[string]any, assetObserved bool) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	orchestration := mapFromAny(ledger["orchestration"])
	candidate := mapFromAny(orchestration["execution_candidate"])
	dispatchPlan := mapFromAny(candidate["dispatch_plan"])
	rehearsal := mapFromAny(dispatchPlan["adapter_rehearsal"])
	if len(rehearsal) == 0 {
		rehearsal = providerReviewAttemptAdapterRehearsalPlanForSnapshot(attempt, dispatchPlan)
	}
	mutationArming := mapFromAny(dispatchPlan["mutation_arming_plan"])
	if len(mutationArming) == 0 {
		mutationArming = providerReviewAttemptAdapterRehearsalMutationArmingPlanForSnapshot(rehearsal)
	}
	candidateMatches := safeProviderReviewAttemptOperationName(stringFromMap(candidate, "next_operation")) == operationName &&
		safeProviderReviewEndpointKey(stringFromMap(candidate, "endpoint_key")) == endpointKey
	rehearsalContractReady := cleanOptionalText(stringFromMap(rehearsal, "mode")) == "redacted_adapter_rehearsal" &&
		safeProviderReviewProviderType(stringFromMap(rehearsal, "provider_type")) != "" &&
		len(safeProviderReviewAttemptRehearsalOperations(mapSliceFromAny(rehearsal["operations"]))) > 0
	mutationArmingContractReady := cleanOptionalText(stringFromMap(mutationArming, "mode")) == "redacted_mutation_arming_plan"
	statusSnapshotWriteEligible := assetObserved && candidateMatches && len(rehearsal) > 0
	operations := safeProviderReviewAttemptRehearsalOperations(mapSliceFromAny(rehearsal["operations"]))
	return map[string]any{
		"mode":                                        "provider_review_attempt_adapter_rehearsal_snapshot",
		"provider_review_attempt_id":                  cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":                       cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"project_template_run_id":                     cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
		"provider_review_attempt_asset_observed":      assetObserved,
		"operation_name":                              operationName,
		"endpoint_key":                                endpointKey,
		"attempt_status":                              safeProviderReviewAttemptStatus(stringFromMap(attempt, "status")),
		"dependency_status":                           safeProviderReviewAttemptClaimDependencyStatus(stringFromMap(attempt, "dependency_status")),
		"operation_order":                             intFromAny(attempt["operation_order"], 0),
		"candidate_observed":                          len(candidate) > 0,
		"candidate_matches_attempt":                   candidateMatches,
		"candidate_status":                            cleanOptionalText(stringFromMap(candidate, "status")),
		"dispatch_plan_observed":                      len(dispatchPlan) > 0,
		"adapter_rehearsal_observed":                  len(rehearsal) > 0,
		"adapter_rehearsal_contract_ready":            rehearsalContractReady,
		"mutation_arming_contract_ready":              mutationArmingContractReady,
		"provider_type":                               safeProviderReviewProviderType(stringFromMap(rehearsal, "provider_type")),
		"review_kind":                                 cleanOptionalText(stringFromMap(rehearsal, "review_kind")),
		"adapter_status":                              cleanOptionalText(stringFromMap(rehearsal, "adapter_status")),
		"adapter_rehearsal_status":                    cleanOptionalText(stringFromMap(rehearsal, "status")),
		"adapter_rehearsal_ready":                     stringFromMap(rehearsal, "status") == "ready",
		"mutation_arming_candidate":                   boolOnlyFromAny(rehearsal["mutation_arming_candidate"]),
		"operation_count":                             intFromAny(rehearsal["operation_count"], len(operations)),
		"ready_operation_count":                       intFromAny(rehearsal["ready_operation_count"], 0),
		"blocked_operation_count":                     intFromAny(rehearsal["blocked_operation_count"], 0),
		"operations":                                  operations,
		"blocked_reasons":                             safeProviderReviewBlockedReasons(stringSliceFromAny(rehearsal["blocked_reasons"])),
		"mutation_arming_status":                      safeProviderReviewMutationArmingStatus(stringFromMap(mutationArming, "status")),
		"execution_enabled_config":                    boolOnlyFromAny(mutationArming["execution_enabled_config"]),
		"mutation_armed_config":                       boolOnlyFromAny(mutationArming["mutation_armed_config"]),
		"requires_operator_review":                    true,
		"requires_mutation_arming":                    true,
		"adapter_mutation_currently_off":              true,
		"live_adapter_implemented":                    false,
		"provider_client_constructed":                 false,
		"request_body_materialized":                   false,
		"response_body_materialized":                  false,
		"headers_materialized":                        false,
		"mutation_armed":                              false,
		"external_call_made":                          false,
		"provider_api_call_made":                      false,
		"provider_api_mutation":                       "disabled",
		"request_body_included":                       false,
		"response_body_included":                      false,
		"headers_included":                            false,
		"authorization_header_included":               false,
		"provider_url_included":                       false,
		"idempotency_key_included":                    false,
		"contains_token":                              false,
		"contains_provider_url":                       false,
		"contains_repository_ref":                     false,
		"contains_branch_name":                        false,
		"contains_file_content":                       false,
		"status_snapshot_write_eligible":              statusSnapshotWriteEligible,
		"status_snapshot_written":                     statusSnapshotWriteEligible,
		"adapter_rehearsal_boundary_redacted":         true,
		"future_live_adapter_execution_still_blocked": true,
	}
}

func providerReviewAttemptAdapterRehearsalPlanForSnapshot(attempt, dispatchPlan map[string]any) map[string]any {
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key"))
	providerType := safeProviderReviewProviderType(stringFromMap(attempt, "provider_type"))
	if operationName == "" || endpointKey == "" || providerType == "" || len(dispatchPlan) == 0 {
		return map[string]any{}
	}
	contractReady := providerReviewAttemptPlanMatchesOperation(dispatchPlan, "redacted_attempt_adapter_dispatch_plan", operationName, endpointKey)
	subplans := []struct {
		evidence string
		ready    bool
	}{
		{"attempt_claim_metadata", boolOnlyFromAny(dispatchPlan["attempt_claim_metadata_ready"])},
		{"adapter_contract", boolOnlyFromAny(dispatchPlan["adapter_contract_ready"])},
		{"request_materialization", providerReviewAttemptRequestPlanReadyForOperation(mapFromAny(dispatchPlan["request_materialization_plan"]), operationName, endpointKey)},
		{"branch_policy", providerReviewAttemptBranchPolicyPlanReadyForOperation(mapFromAny(dispatchPlan["branch_policy_plan"]), operationName, endpointKey)},
		{"credential_binding", providerReviewAttemptCredentialPlanReadyForOperation(mapFromAny(dispatchPlan["credential_binding_plan"]), operationName, endpointKey)},
		{"adapter_runtime", providerReviewAttemptRuntimePlanReadyForOperation(mapFromAny(dispatchPlan["adapter_runtime_plan"]), operationName, endpointKey)},
		{"transport_metadata", providerReviewAttemptTransportPlanReadyForOperation(mapFromAny(dispatchPlan["transport_plan"]), operationName, endpointKey)},
		{"response_recording", providerReviewAttemptResponseRecordingReadyForOperation(mapFromAny(dispatchPlan["response_plan"]), operationName, endpointKey)},
		{"transaction_boundary", providerReviewAttemptTransactionPlanReadyForOperation(mapFromAny(dispatchPlan["transaction_plan"]), operationName, endpointKey)},
	}
	blockedReasons := []string{}
	for _, subplan := range subplans {
		if subplan.ready {
			continue
		}
		blockedReasons = append(blockedReasons, providerReviewAttemptAdapterRehearsalBlockedReason(subplan.evidence))
	}
	status := "blocked"
	if contractReady && len(blockedReasons) == 0 {
		status = "ready"
	}
	return map[string]any{
		"mode":                           "redacted_adapter_rehearsal",
		"status":                         status,
		"provider_type":                  providerType,
		"review_kind":                    cleanOptionalText(stringFromMap(attempt, "review_kind")),
		"adapter_status":                 cleanOptionalText(stringFromMap(dispatchPlan, "adapter_kind")),
		"operation_count":                1,
		"ready_operation_count":          map[bool]int{true: 1, false: 0}[status == "ready"],
		"blocked_operation_count":        map[bool]int{true: 0, false: 1}[status == "ready"],
		"blocked_reasons":                blockedReasons,
		"operations":                     []map[string]any{providerReviewAttemptAdapterRehearsalOperationForSnapshot(operationName, endpointKey, status, blockedReasons)},
		"mutation_arming_candidate":      status == "ready",
		"external_call_made":             false,
		"provider_api_call_made":         false,
		"provider_api_mutation":          "disabled",
		"payload_redacted":               true,
		"contains_token":                 false,
		"contains_provider_url":          false,
		"contains_repository_ref":        false,
		"contains_branch_name":           false,
		"contains_file_content":          false,
		"requires_operator_review":       true,
		"requires_mutation_arming":       true,
		"adapter_mutation_currently_off": true,
	}
}

func providerReviewAttemptAdapterRehearsalOperationForSnapshot(operationName, endpointKey, status string, blockedReasons []string) map[string]any {
	return map[string]any{
		"name":                    operationName,
		"endpoint_key":            endpointKey,
		"status":                  status,
		"blocked_reasons":         blockedReasons,
		"external_call_made":      false,
		"provider_api_call_made":  false,
		"provider_api_mutation":   "disabled",
		"contains_token":          false,
		"contains_provider_url":   false,
		"contains_repository_ref": false,
		"contains_branch_name":    false,
		"contains_file_content":   false,
	}
}

func providerReviewAttemptAdapterRehearsalBlockedReason(evidence string) string {
	switch cleanOptionalText(evidence) {
	case "attempt_claim_metadata":
		return "provider_review_claim_metadata"
	case "adapter_contract":
		return "provider_review_adapter_contract"
	case "request_materialization":
		return "provider_review_request_materialization"
	case "branch_policy":
		return "provider_review_branch_policy"
	case "credential_binding":
		return "provider_review_credential_binding"
	case "adapter_runtime":
		return "provider_review_adapter_runtime"
	case "transport_metadata":
		return "provider_review_transport_metadata"
	case "response_recording":
		return "provider_review_response_recording"
	case "transaction_boundary":
		return "provider_review_transaction_boundary"
	default:
		return "provider_review_adapter_rehearsal"
	}
}

func providerReviewAttemptAdapterRehearsalMutationArmingPlanForSnapshot(rehearsal map[string]any) map[string]any {
	rehearsalReady := cleanOptionalText(stringFromMap(rehearsal, "status")) == "ready" && boolOnlyFromAny(rehearsal["mutation_arming_candidate"])
	status := "blocked"
	if rehearsalReady {
		status = "ready_to_arm"
	}
	return map[string]any{
		"mode":                           "redacted_mutation_arming_plan",
		"status":                         status,
		"provider_type":                  safeProviderReviewProviderType(stringFromMap(rehearsal, "provider_type")),
		"review_kind":                    cleanOptionalText(stringFromMap(rehearsal, "review_kind")),
		"execution_enabled_config":       false,
		"adapter_rehearsal_ready":        rehearsalReady,
		"mutation_armed_config":          false,
		"mutation_armed":                 false,
		"blocked_reasons":                []string{"provider_review_execution_enabled", "provider_review_mutation_armed"},
		"external_call_made":             false,
		"provider_api_call_made":         false,
		"provider_api_mutation":          "disabled",
		"contains_token":                 false,
		"contains_provider_url":          false,
		"contains_repository_ref":        false,
		"contains_file_content":          false,
		"requires_operator_review":       true,
		"requires_adapter_rehearsal":     true,
		"adapter_mutation_currently_off": true,
	}
}

func providerReviewAttemptAdapterRehearsalSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := "adapter_rehearsal_blocked"
	if boolOnlyFromAny(snapshot["adapter_rehearsal_contract_ready"]) {
		state = "adapter_rehearsal_contract_ready"
	}
	if boolOnlyFromAny(snapshot["adapter_rehearsal_ready"]) && boolOnlyFromAny(snapshot["mutation_arming_candidate"]) {
		state = "adapter_rehearsal_ready"
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
	if !boolOnlyFromAny(snapshot["adapter_rehearsal_observed"]) {
		missing = append(missing, "provider_review_adapter_rehearsal_missing")
	}
	if !boolOnlyFromAny(snapshot["adapter_rehearsal_contract_ready"]) {
		missing = append(missing, "provider_review_adapter_rehearsal_contract_not_ready")
	}
	if !boolOnlyFromAny(snapshot["mutation_arming_contract_ready"]) {
		missing = append(missing, "provider_review_mutation_arming_contract_not_ready")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) ||
		boolOnlyFromAny(snapshot["live_adapter_implemented"]) ||
		boolOnlyFromAny(snapshot["provider_client_constructed"]) ||
		boolOnlyFromAny(snapshot["request_body_materialized"]) ||
		boolOnlyFromAny(snapshot["response_body_materialized"]) ||
		boolOnlyFromAny(snapshot["headers_materialized"]) ||
		boolOnlyFromAny(snapshot["provider_url_included"]) ||
		boolOnlyFromAny(snapshot["authorization_header_included"]) ||
		boolOnlyFromAny(snapshot["idempotency_key_included"]) {
		missing = append(missing, "provider_review_adapter_rehearsal_not_no_call")
	}
	return len(missing) == 0, state, missing
}

func providerReviewAttemptAdapterRehearsalSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "adapter_rehearsal_ready":
		return "provider_review_attempt_adapter_rehearsal_ready", "low"
	case "adapter_rehearsal_contract_ready":
		return "provider_review_attempt_adapter_rehearsal_contract_ready", "low"
	case "adapter_rehearsal_blocked":
		return "provider_review_attempt_adapter_rehearsal_blocked", "warning"
	default:
		return "provider_review_attempt_adapter_rehearsal_unknown", "warning"
	}
}

func safeProviderReviewAttemptRehearsalOperations(values []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(values))
	for _, value := range values {
		name := safeProviderReviewAttemptOperationName(stringFromMap(value, "name"))
		endpointKey := safeProviderReviewEndpointKey(stringFromMap(value, "endpoint_key"))
		if name == "" || endpointKey == "" {
			continue
		}
		out = append(out, map[string]any{
			"name":                    name,
			"endpoint_key":            endpointKey,
			"status":                  cleanOptionalText(stringFromMap(value, "status")),
			"blocked_reasons":         safeProviderReviewBlockedReasons(stringSliceFromAny(value["blocked_reasons"])),
			"external_call_made":      false,
			"provider_api_call_made":  false,
			"provider_api_mutation":   "disabled",
			"contains_token":          false,
			"contains_provider_url":   false,
			"contains_repository_ref": false,
			"contains_branch_name":    false,
			"contains_file_content":   false,
		})
	}
	return out
}

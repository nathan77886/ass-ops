package app

import (
	"fmt"
)

func providerReviewMutationArmingSnapshotPayload(approval, ledger map[string]any, assetObserved bool, liveReadiness map[string]bool) map[string]any {
	audit := operationApprovalPayloadAudit(approval)
	reconciliation := mapFromAny(audit["provider_review_reconciliation"])
	if len(reconciliation) == 0 {
		reconciliation = mapFromAny(mapFromAny(audit["approval_result"])["provider_review_reconciliation"])
	}
	armingPlan := sanitizedProviderReviewMutationArmingPlan(mapFromAny(reconciliation["mutation_arming_plan"]))
	rehearsal := mapFromAny(reconciliation["adapter_rehearsal"])
	blueprint := mapFromAny(reconciliation["execution_blueprint"])
	executionRequest := mapFromAny(audit["execution_request"])
	attemptCount := providerReviewAttemptLedgerAttemptCount(ledger)
	liveReadinessEvidence, liveReadinessObserved := providerReviewAttemptLiveExecutionReadinessEvidence(ledger, liveReadiness)
	liveReadinessComplete := attemptCount > 0 && liveReadinessObserved == attemptCount
	statusSnapshotWriteEligible := assetObserved && attemptCount > 0 && liveReadinessComplete
	return map[string]any{
		"mode":                                      "provider_review_mutation_arming_snapshot",
		"operation_approval_id":                     cleanOptionalID(fmt.Sprint(approval["id"])),
		"project_id":                                cleanOptionalID(fmt.Sprint(approval["project_id"])),
		"project_template_run_id":                   cleanOptionalID(fmt.Sprint(audit["project_template_run_id"])),
		"operation_approval_asset_observed":         assetObserved,
		"operation_approval_action":                 cleanOptionalText(stringFromMap(approval, "action")),
		"operation_approval_status":                 cleanOptionalText(stringFromMap(approval, "status")),
		"provider_type":                             cleanOptionalText(stringFromMap(armingPlan, "provider_type")),
		"review_kind":                               cleanOptionalText(stringFromMap(armingPlan, "review_kind")),
		"execution_request_status":                  cleanOptionalText(stringFromMap(executionRequest, "status")),
		"arming_status":                             safeProviderReviewMutationArmingStatus(stringFromMap(armingPlan, "status")),
		"required_config":                           "ASSOPS_ARM_PROVIDER_REVIEW_MUTATION",
		"execution_enabled_config":                  boolOnlyFromAny(armingPlan["execution_enabled_config"]),
		"adapter_rehearsal_ready":                   boolOnlyFromAny(armingPlan["adapter_rehearsal_ready"]),
		"mutation_armed_config":                     boolOnlyFromAny(armingPlan["mutation_armed_config"]),
		"mutation_armed":                            false,
		"requires_operator_review":                  true,
		"requires_adapter_rehearsal":                true,
		"adapter_mutation_currently_off":            true,
		"adapter_rehearsal_status":                  cleanOptionalText(stringFromMap(rehearsal, "status")),
		"adapter_rehearsal_operation_count":         intFromAny(rehearsal["operation_count"], 0),
		"adapter_rehearsal_ready_operation_count":   intFromAny(rehearsal["ready_operation_count"], 0),
		"adapter_rehearsal_blocked_operation_count": intFromAny(rehearsal["blocked_operation_count"], 0),
		"execution_blueprint_status":                cleanOptionalText(stringFromMap(blueprint, "status")),
		"live_adapter_implemented":                  false,
		"attempt_ledger_observed":                   attemptCount > 0,
		"attempt_count":                             attemptCount,
		"attempt_live_execution_readiness_required": true,
		"attempt_live_execution_readiness_complete": liveReadinessComplete,
		"attempt_live_execution_readiness_count":    liveReadinessObserved,
		"attempt_live_execution_readiness_evidence": liveReadinessEvidence,
		"next_attempt_operation":                    cleanOptionalText(stringFromMap(mapFromAny(ledger["orchestration"]), "next_operation")),
		"attempt_dependency_chain_status":           cleanOptionalText(stringFromMap(mapFromAny(ledger["orchestration"]), "dependency_chain_status")),
		"blocked_reasons":                           safeProviderReviewBlockedReasons(stringSliceFromAny(armingPlan["blocked_reasons"])),
		"external_call_made":                        false,
		"provider_api_call_made":                    false,
		"provider_api_mutation":                     "disabled",
		"operation_log_written":                     false,
		"request_body_included":                     false,
		"response_body_included":                    false,
		"headers_included":                          false,
		"idempotency_key_included":                  false,
		"contains_token":                            false,
		"contains_provider_url":                     false,
		"contains_repository_ref":                   false,
		"contains_branch_name":                      false,
		"contains_file_content":                     false,
		"status_snapshot_write_eligible":            statusSnapshotWriteEligible,
		"status_snapshot_written":                   statusSnapshotWriteEligible,
	}
}

func providerReviewMutationArmingSnapshotReadiness(snapshot map[string]any) (bool, string, []string) {
	missing := []string{}
	state := safeProviderReviewMutationArmingStatus(stringFromMap(snapshot, "arming_status"))
	if state == "" {
		state = "blocked"
	}
	if !boolOnlyFromAny(snapshot["operation_approval_asset_observed"]) {
		missing = append(missing, "operation_approval_asset_missing")
	}
	if cleanOptionalID(fmt.Sprint(snapshot["operation_approval_id"])) == "" {
		missing = append(missing, "operation_approval_id_missing")
	}
	if stringFromMap(snapshot, "operation_approval_action") != templateProviderReviewExecuteApprovalAction {
		missing = append(missing, "provider_review_execution_approval_action")
	}
	if stringFromMap(snapshot, "operation_approval_status") != "approved" {
		missing = append(missing, "operation_approval_not_approved")
	}
	if state != "ready_to_arm" {
		missing = append(missing, "provider_review_mutation_not_ready_to_arm")
	}
	if !boolOnlyFromAny(snapshot["execution_enabled_config"]) {
		missing = append(missing, "provider_review_execution_enabled")
	}
	if !boolOnlyFromAny(snapshot["adapter_rehearsal_ready"]) {
		missing = append(missing, "provider_review_adapter_rehearsal")
	}
	if !boolOnlyFromAny(snapshot["attempt_ledger_observed"]) {
		missing = append(missing, "provider_review_attempt_ledger")
	}
	if !boolOnlyFromAny(snapshot["attempt_live_execution_readiness_complete"]) {
		missing = append(missing, "provider_review_attempt_live_execution_readiness")
	}
	if boolOnlyFromAny(snapshot["provider_api_call_made"]) ||
		boolOnlyFromAny(snapshot["external_call_made"]) ||
		stringFromMap(snapshot, "provider_api_mutation") != "disabled" ||
		boolOnlyFromAny(snapshot["mutation_armed"]) {
		missing = append(missing, "provider_review_mutation_not_no_call")
	}
	if len(missing) > 0 {
		return false, state, missing
	}
	return true, "ready_for_operator_review", nil
}

func providerReviewAttemptLedgerAttemptCount(ledger map[string]any) int {
	if count := intFromAny(ledger["attempt_count"], 0); count > 0 {
		return count
	}
	// If callers omit attempt_count, fall back to operations; mismatched count vs operations blocks readiness.
	return len(providerReviewAttemptLedgerOperationsFromAny(ledger["operations"]))
}

func providerReviewAttemptLedgerAttemptIDs(ledger map[string]any) []string {
	operations := providerReviewAttemptLedgerOperationsFromAny(ledger["operations"])
	ids := make([]string, 0, len(operations))
	seen := map[string]bool{}
	for _, operation := range operations {
		id := cleanOptionalID(fmt.Sprint(operation["id"]))
		if id == "" || seen[id] {
			continue
		}
		ids = append(ids, id)
		seen[id] = true
	}
	return ids
}

func providerReviewAttemptLiveExecutionReadinessEvidence(ledger map[string]any, observed map[string]bool) ([]map[string]any, int) {
	if observed == nil {
		observed = map[string]bool{}
	}
	operations := providerReviewAttemptLedgerOperationsFromAny(ledger["operations"])
	evidence := make([]map[string]any, 0, len(operations))
	observedCount := 0
	for _, operation := range operations {
		id := cleanOptionalID(fmt.Sprint(operation["id"]))
		ready := id != "" && observed[id]
		if ready {
			observedCount++
		}
		evidence = append(evidence, map[string]any{
			"operation_name": cleanOptionalText(stringFromMap(operation, "name")),
			"endpoint_key":   cleanOptionalText(stringFromMap(operation, "endpoint_key")),
			"status":         cleanOptionalText(stringFromMap(operation, "status")),
			"observed":       ready,
		})
	}
	return evidence, observedCount
}

func providerReviewAttemptLedgerOperationsFromAny(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if row := mapFromAny(item); len(row) > 0 {
				out = append(out, row)
			}
		}
		return out
	default:
		return nil
	}
}

func providerReviewMutationArmingSnapshotStatusHealth(state string) (string, string) {
	switch state {
	case "ready_for_operator_review":
		return "provider_review_mutation_arming_review_ready", "low"
	case "blocked":
		return "provider_review_mutation_arming_blocked", "warning"
	default:
		return "provider_review_mutation_arming_" + cleanOptionalText(state), "warning"
	}
}

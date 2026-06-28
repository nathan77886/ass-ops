package app

import (
	"fmt"
	"github.com/go-chi/chi/v5"
	"log/slog"
	"net/http"
)

func (s *Server) providerReviewAttemptLiveExecutionLaunchPlan(w http.ResponseWriter, r *http.Request) {
	attemptID := cleanOptionalID(chi.URLParam(r, "id"))
	if attemptID == "" {
		writeError(w, http.StatusBadRequest, "provider review attempt id is required")
		return
	}
	if !s.requireProviderReviewAttemptUpdatePolicy(w, r, attemptID) {
		return
	}
	attempt, err := providerReviewAttemptForActivationSnapshot(r.Context(), s.store, attemptID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if stringFromMap(attempt, "approval_action") != templateProviderReviewExecuteApprovalAction {
		writeError(w, http.StatusConflict, "provider review attempt is not tied to provider review execution approval")
		return
	}
	if stringFromMap(attempt, "approval_status") != "approved" {
		writeJSON(w, http.StatusOK, map[string]any{
			"mode":                                "provider_review_attempt_live_execution_launch_plan",
			"launch_plan_state":                   "operation_approval_not_approved",
			"launch_plan_ready":                   false,
			"provider_review_attempt_id":          attemptID,
			"operation_approval_id":               cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
			"project_template_run_id":             cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
			"live_execution_preflight_ready":      false,
			"live_execution_preflight_state":      "operation_approval_not_approved",
			"launch_plan":                         nil,
			"external_call_made":                  false,
			"provider_api_call_made":              false,
			"provider_api_mutation":               "disabled",
			"provider_request_materialized":       false,
			"provider_request_sent":               false,
			"provider_response_received":          false,
			"provider_client_constructed":         false,
			"live_adapter_invoked":                false,
			"execute_method_invoked":              false,
			"response_handler_invoked":            false,
			"transaction_recorded":                false,
			"operation_log_written":               false,
			"asset_status_snapshot_written":       false,
			"contains_token":                      false,
			"contains_provider_url":               false,
			"contains_repository_ref":             false,
			"contains_branch_name":                false,
			"contains_file_content":               false,
			"future_live_execution_still_blocked": true,
			"missing_evidence":                    []string{"operation_approval_not_approved"},
			"message":                             "Provider review live execution launch plan is waiting for an approved provider review execution approval.",
		})
		return
	}
	result, err := ProviderReviewAttemptLiveExecutionLaunchPlan(r.Context(), s.store, ProviderReviewAttemptLiveExecutionLaunchPlanOptions{
		AttemptID: attemptID,
		Attempt:   attempt,
	})
	if err != nil {
		log := s.log
		if log == nil {
			log = slog.Default()
		}
		log.Warn("provider review attempt live execution launch plan failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "provider review attempt live execution launch plan failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func providerReviewAttemptClaimBlockedResponse(attempt map[string]any, reason string, claimPlan map[string]any) map[string]any {
	if len(claimPlan) == 0 {
		claimPlan = providerReviewAttemptClaimPlanFromAttempt(attempt)
	}
	return providerReviewAttemptClaimResponse(attempt, providerReviewAttemptLedgerSummary([]map[string]any{attempt}), false, reason, claimPlan)
}

func providerReviewAttemptClaimResponse(attempt map[string]any, ledger map[string]any, claimed bool, state string, claimPlanOverride ...map[string]any) map[string]any {
	claimPlan := providerReviewAttemptClaimPlanFromAttempt(attempt)
	if len(claimPlanOverride) > 0 && len(claimPlanOverride[0]) > 0 {
		claimPlan = claimPlanOverride[0]
	}
	return map[string]any{
		"claim_state":                cleanOptionalText(state),
		"claimed":                    claimed,
		"attempt":                    providerReviewAttemptLedgerSummary([]map[string]any{attempt})["operations"].([]map[string]any)[0],
		"provider_review_attempt_id": cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":      cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"operation_name":             safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name")),
		"endpoint_key":               safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key")),
		"claim_plan":                 claimPlan,
		"ledger":                     ledger,
		"external_call_made":         false,
		"provider_api_call_made":     false,
		"provider_api_mutation":      "disabled",
		"idempotency_key_included":   false,
		"contains_token":             false,
		"contains_provider_url":      false,
		"contains_repository_ref":    false,
		"contains_branch_name":       false,
		"contains_file_content":      false,
	}
}

func providerReviewAttemptLocalResultBlockedResponse(attempt map[string]any, reason, resultStatus string, resultPlan map[string]any) map[string]any {
	if len(resultPlan) == 0 {
		resultPlan = providerReviewAttemptLocalResultPlanFromAttempt(attempt, resultStatus)
	}
	return providerReviewAttemptLocalResultResponse(attempt, providerReviewAttemptLedgerSummary([]map[string]any{attempt}), false, reason, resultStatus, resultPlan)
}

func (s *Server) requireProviderReviewAttemptUpdatePolicy(w http.ResponseWriter, r *http.Request, attemptID string) bool {
	_, approval, _, err := providerReviewAttemptWithApprovalGorm(r.Context(), s.store.Gorm, attemptID, false)
	if err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return false
	}
	approvalID := approval.ID
	projectID := cleanOptionalID(approval.ProjectID.String)
	if projectID == "" {
		return s.requirePolicy(w, r, PolicyResource{Type: "operation_approval", ID: approvalID}, "update")
	}
	return s.requireProjectPolicy(w, r, PolicyResource{Type: "operation_approval", ID: approvalID, ProjectID: projectID}, "update")
}

func providerReviewAttemptLocalResultResponse(attempt map[string]any, ledger map[string]any, recorded bool, state, resultStatus string, resultPlanOverride ...map[string]any) map[string]any {
	resultPlan := providerReviewAttemptLocalResultPlanFromAttempt(attempt, resultStatus)
	if len(resultPlanOverride) > 0 && len(resultPlanOverride[0]) > 0 {
		resultPlan = resultPlanOverride[0]
	}
	return map[string]any{
		"result_state":               cleanOptionalText(state),
		"result_recorded":            recorded,
		"result_status":              safeProviderReviewAttemptLocalResultStatus(resultStatus),
		"attempt":                    providerReviewAttemptLedgerSummary([]map[string]any{attempt})["operations"].([]map[string]any)[0],
		"provider_review_attempt_id": cleanOptionalID(fmt.Sprint(attempt["id"])),
		"operation_approval_id":      cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
		"operation_name":             safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name")),
		"endpoint_key":               safeProviderReviewEndpointKey(stringFromMap(attempt, "endpoint_key")),
		"result_recording_plan":      resultPlan,
		"ledger":                     ledger,
		"external_call_made":         boolOnlyFromAny(attempt["external_call_made"]),
		"provider_api_call_made":     boolOnlyFromAny(attempt["provider_api_call_made"]),
		"provider_api_mutation":      safeProviderReviewProviderAPIMutation(stringFromMap(attempt, "provider_api_mutation")),
		"provider_status_class":      safeProviderReviewStatusClass(stringFromMap(attempt, "provider_status_class")),
		"provider_review_url":        sanitizeProviderReviewURLForResponse(stringFromMap(attempt, "provider_review_url")),
		"live_execution_phase":       safeProviderReviewLiveExecutionPhase(stringFromMap(attempt, "live_execution_phase")),
		"live_execution_retryable":   boolOnlyFromAny(attempt["live_execution_retryable"]),
		"manual_cleanup_hint":        safeProviderReviewManualCleanupHint(stringFromMap(attempt, "live_execution_manual_cleanup_hint")),
		"cleanup_attempted":          boolOnlyFromAny(attempt["cleanup_attempted"]),
		"cleanup_succeeded":          boolOnlyFromAny(attempt["cleanup_succeeded"]),
		"cleanup_required":           boolOnlyFromAny(attempt["cleanup_required"]),
		"response_body_included":     false,
		"headers_included":           false,
		"idempotency_key_included":   false,
		"contains_token":             false,
		"contains_provider_url":      false,
		"contains_repository_ref":    false,
		"contains_branch_name":       false,
		"contains_file_content":      false,
	}
}

func providerReviewAttemptLocalResultPlanFromAttempt(attempt map[string]any, resultStatus string) map[string]any {
	providerAPIMutation := safeProviderReviewProviderAPIMutation(stringFromMap(attempt, "provider_api_mutation"))
	operation := map[string]any{
		"name":                  stringFromMap(attempt, "operation_name"),
		"endpoint_key":          stringFromMap(attempt, "endpoint_key"),
		"status":                stringFromMap(attempt, "status"),
		"dependency_status":     stringFromMap(attempt, "dependency_status"),
		"operation_order":       attempt["operation_order"],
		"request_summary":       attempt["request_summary"],
		"response_diagnostics":  attempt["response_diagnostics"],
		"claimed_at":            attempt["claimed_at"],
		"claimed_by_user_id":    attempt["claimed_by_user_id"],
		"provider_api_mutation": providerAPIMutation,
	}
	operationName := safeProviderReviewAttemptOperationName(stringFromMap(operation, "name"))
	endpointKey := safeProviderReviewEndpointKey(stringFromMap(operation, "endpoint_key"))
	responsePlan := providerReviewAttemptAdapterResponsePlan(operation, mapFromAny(attempt["request_summary"]), mapFromAny(attempt["response_diagnostics"]))
	resultPlan := mapFromAny(responsePlan["result_recording_plan"])
	claimRecorded := providerReviewAttemptClaimRecorded(attempt)
	resultStatus = safeProviderReviewAttemptLocalResultStatus(resultStatus)
	metadataReady := claimRecorded &&
		safeProviderReviewAttemptStatus(stringFromMap(attempt, "status")) == "running" &&
		resultStatus != "" &&
		providerReviewAttemptResponsePlanReadyForOperation(responsePlan, operationName, endpointKey)
	blockedReasons := []string{}
	if !claimRecorded {
		blockedReasons = append(blockedReasons, "provider_review_attempt_claim_not_recorded")
	}
	if safeProviderReviewAttemptStatus(stringFromMap(attempt, "status")) != "running" {
		blockedReasons = append(blockedReasons, "provider_review_attempt_status_not_running")
	}
	if resultStatus == "" {
		blockedReasons = append(blockedReasons, "provider_review_result_status_invalid")
	}
	if !providerReviewAttemptResponsePlanReadyForOperation(responsePlan, operationName, endpointKey) {
		blockedReasons = append(blockedReasons, "provider_review_response_plan_not_ready")
	}
	if len(blockedReasons) == 0 {
		blockedReasons = []string{"provider_api_call_not_made", "provider_review_adapter_not_implemented", "provider_review_mutation_not_armed"}
	}
	resultPlan["result_recording_state"] = "local_recordable"
	if !metadataReady {
		resultPlan["result_recording_state"] = "blocked"
	}
	resultPlan["result_recording_ready"] = false
	resultPlan["result_recording_metadata_ready"] = metadataReady
	resultPlan["result_status"] = resultStatus
	resultPlan["mapped_attempt_status"] = providerReviewAttemptStatusForLocalResult(resultStatus)
	resultPlan["mapped_dependency_status"] = providerReviewAttemptDependencyStatusForLocalResult(resultStatus)
	resultPlan["claim_recorded"] = claimRecorded
	resultPlan["requires_attempt_status_running"] = true
	resultPlan["attempt_status"] = safeProviderReviewAttemptStatus(stringFromMap(attempt, "status"))
	resultPlan["response_recorded"] = false
	resultPlan["local_result_recording_enabled"] = metadataReady
	resultPlan["external_call_made"] = boolOnlyFromAny(attempt["external_call_made"])
	resultPlan["provider_api_call_made"] = boolOnlyFromAny(attempt["provider_api_call_made"])
	resultPlan["provider_api_mutation"] = providerAPIMutation
	resultPlan["response_body_included"] = false
	resultPlan["headers_included"] = false
	resultPlan["contains_token"] = false
	resultPlan["contains_provider_url"] = false
	resultPlan["contains_repository_ref"] = false
	resultPlan["contains_branch_name"] = false
	resultPlan["contains_file_content"] = false
	resultPlan["blocked_reasons"] = blockedReasons
	return resultPlan
}

func providerReviewAttemptLocalResultDiagnostics(attempt map[string]any, resultStatus string) map[string]any {
	existing := sanitizedProviderReviewAttemptResponseDiagnostics(mapFromAny(attempt["response_diagnostics"]))
	existing["status"] = safeProviderReviewAttemptLocalResultStatus(resultStatus)
	existing["local_result_recorded"] = true
	existing["local_result_source"] = "operator_no_call_result"
	existing["provider_response_status_included"] = false
	existing["provider_request_id_included"] = false
	existing["response_body_included"] = false
	existing["headers_included"] = false
	existing["provider_url_included"] = false
	existing["idempotency_key_included"] = false
	existing["contains_token"] = false
	existing["contains_provider_url"] = false
	existing["contains_repository_ref"] = false
	existing["contains_branch_name"] = false
	existing["contains_file_content"] = false
	existing["provider_api_call_made"] = false
	existing["provider_api_mutation"] = "disabled"
	existing["external_call_made"] = false
	return existing
}

func safeProviderReviewAttemptLocalResultStatus(value string) string {
	switch cleanOptionalText(value) {
	case "success", "retryable", "failed":
		return cleanOptionalText(value)
	default:
		return ""
	}
}

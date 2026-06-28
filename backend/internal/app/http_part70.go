package app

import (
	"fmt"
	"github.com/go-chi/chi/v5"
	"log/slog"
	"net/http"
)

func (s *Server) recordProviderReviewAttemptLiveExecutionReadinessSnapshot(w http.ResponseWriter, r *http.Request) {
	attemptID := cleanOptionalID(chi.URLParam(r, "id"))
	if attemptID == "" {
		writeError(w, http.StatusBadRequest, "provider review attempt id is required")
		return
	}
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
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
			"mode":                                   "provider_review_attempt_live_execution_readiness_snapshot_recording",
			"recording_state":                        "operation_approval_not_approved",
			"recording_ready":                        false,
			"recording_enabled":                      false,
			"dry_run":                                req.DryRun,
			"provider_review_attempt_id":             attemptID,
			"operation_approval_id":                  cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
			"project_template_run_id":                cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
			"provider_review_attempt_asset_observed": false,
			"snapshot":                               nil,
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
			"status_snapshot_write_eligible":                                    false,
			"message":                                                           "Provider review attempt live execution readiness snapshot is waiting for an approved provider review execution approval.",
		})
		return
	}
	ledger, err := s.providerReviewAttemptLedgerForApproval(r.Context(), cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := RecordProviderReviewAttemptLiveExecutionReadinessSnapshot(r.Context(), s.store, ProviderReviewAttemptLiveExecutionReadinessSnapshotOptions{
		AttemptID: attemptID,
		DryRun:    req.DryRun,
		Attempt:   attempt,
		Ledger:    ledger,
	})
	if err != nil {
		log := s.log
		if log == nil {
			log = slog.Default()
		}
		log.Warn("provider review attempt live execution readiness snapshot failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review attempt live execution readiness snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordProviderReviewAttemptLiveExecutionGuardSnapshot(w http.ResponseWriter, r *http.Request) {
	attemptID := cleanOptionalID(chi.URLParam(r, "id"))
	if attemptID == "" {
		writeError(w, http.StatusBadRequest, "provider review attempt id is required")
		return
	}
	var req struct {
		DryRun bool `json:"dry_run"`
	}
	if !decodeJSON(w, r, &req) {
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
			"mode":                                   "provider_review_attempt_live_execution_guard_snapshot_recording",
			"recording_state":                        "operation_approval_not_approved",
			"recording_ready":                        false,
			"recording_enabled":                      false,
			"dry_run":                                req.DryRun,
			"provider_review_attempt_id":             attemptID,
			"operation_approval_id":                  cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
			"project_template_run_id":                cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
			"provider_review_attempt_asset_observed": false,
			"snapshot":                               nil,
			"snapshots_written":                      0,
			"snapshots_skipped_as_duplicate":         0,
			"provider_review_attempt_live_execution_guard_written": false,
			"asset_status_snapshot_written":                        false,
			"operation_log_written":                                false,
			"external_call_made":                                   false,
			"provider_api_call_made":                               false,
			"provider_api_mutation":                                "disabled",
			"provider_request_sent":                                false,
			"provider_response_received":                           false,
			"mutation_armed":                                       false,
			"live_adapter_implemented":                             false,
			"future_live_execution_still_blocked":                  true,
			"contains_token":                                       false,
			"contains_provider_url":                                false,
			"contains_repository_ref":                              false,
			"contains_branch_name":                                 false,
			"contains_file_content":                                false,
			"status_snapshot_write_eligible":                       false,
			"message":                                              "Provider review attempt live execution guard snapshot is waiting for an approved provider review execution approval.",
		})
		return
	}
	result, err := RecordProviderReviewAttemptLiveExecutionGuardSnapshot(r.Context(), s.store, ProviderReviewAttemptLiveExecutionGuardSnapshotOptions{
		AttemptID: attemptID,
		DryRun:    req.DryRun,
		Attempt:   attempt,
	})
	if err != nil {
		log := s.log
		if log == nil {
			log = slog.Default()
		}
		log.Warn("provider review attempt live execution guard snapshot failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review attempt live execution guard snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) providerReviewAttemptLiveExecutionPreflight(w http.ResponseWriter, r *http.Request) {
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
			"mode":                                   "provider_review_attempt_live_execution_preflight",
			"preflight_state":                        "operation_approval_not_approved",
			"preflight_ready":                        false,
			"provider_review_attempt_id":             attemptID,
			"operation_approval_id":                  cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
			"project_template_run_id":                cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
			"provider_review_attempt_asset_observed": false,
			"preflight":                              nil,
			"external_call_made":                     false,
			"provider_api_call_made":                 false,
			"provider_api_mutation":                  "disabled",
			"provider_request_sent":                  false,
			"provider_response_received":             false,
			"mutation_armed":                         false,
			"live_adapter_implemented":               false,
			"future_live_execution_still_blocked":    true,
			"operation_log_written":                  false,
			"asset_status_snapshot_written":          false,
			"contains_token":                         false,
			"contains_provider_url":                  false,
			"contains_repository_ref":                false,
			"contains_branch_name":                   false,
			"contains_file_content":                  false,
			"missing_evidence":                       []string{"operation_approval_not_approved"},
			"message":                                "Provider review live execution preflight is waiting for an approved provider review execution approval.",
		})
		return
	}
	result, err := ProviderReviewAttemptLiveExecutionPreflight(r.Context(), s.store, ProviderReviewAttemptLiveExecutionPreflightOptions{
		AttemptID: attemptID,
		Attempt:   attempt,
	})
	if err != nil {
		log := s.log
		if log == nil {
			log = slog.Default()
		}
		log.Warn("provider review attempt live execution preflight failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "provider review attempt live execution preflight failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

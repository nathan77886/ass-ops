package app

import (
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (s *Server) recordProviderReviewAttemptResultRecordingSnapshot(w http.ResponseWriter, r *http.Request) {
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
			"mode":                                   "provider_review_attempt_result_recording_snapshot_recording",
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
			"provider_review_attempt_result_recording_snapshot_written": false,
			"asset_status_snapshot_written":                             false,
			"operation_log_written":                                     false,
			"external_call_made":                                        false,
			"provider_api_call_made":                                    false,
			"provider_api_mutation":                                     "disabled",
			"provider_request_sent":                                     false,
			"provider_response_received":                                false,
			"response_recorded":                                         false,
			"result_recorded":                                           false,
			"attempt_result_persisted":                                  false,
			"dependency_update_recorded":                                false,
			"transaction_recorded":                                      false,
			"contains_token":                                            false,
			"contains_provider_url":                                     false,
			"contains_repository_ref":                                   false,
			"contains_branch_name":                                      false,
			"contains_file_content":                                     false,
			"status_snapshot_write_eligible":                            false,
			"message":                                                   "Provider review attempt result-recording snapshot is waiting for an approved provider review execution approval.",
		})
		return
	}
	ledger, err := s.providerReviewAttemptLedgerForApproval(r.Context(), cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := RecordProviderReviewAttemptResultRecordingSnapshot(r.Context(), s.store, ProviderReviewAttemptResultRecordingSnapshotOptions{
		AttemptID: attemptID,
		DryRun:    req.DryRun,
		Attempt:   attempt,
		Ledger:    ledger,
	})
	if err != nil {
		s.log.Warn("provider review attempt result-recording snapshot failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review attempt result-recording snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordProviderReviewAttemptProviderCallBoundarySnapshot(w http.ResponseWriter, r *http.Request) {
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
			"mode":                                   "provider_review_attempt_provider_call_boundary_snapshot_recording",
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
			"provider_review_attempt_provider_call_boundary_snapshot_written": false,
			"asset_status_snapshot_written":                                   false,
			"operation_log_written":                                           false,
			"external_call_made":                                              false,
			"provider_api_call_made":                                          false,
			"provider_api_mutation":                                           "disabled",
			"provider_request_sent":                                           false,
			"provider_response_received":                                      false,
			"provider_call_boundary_opened":                                   false,
			"provider_call_boundary_recorded":                                 false,
			"provider_call_started_recorded":                                  false,
			"provider_call_finished_recorded":                                 false,
			"provider_request_id_recorded":                                    false,
			"provider_response_status_recorded":                               false,
			"provider_response_body_recorded":                                 false,
			"provider_response_headers_recorded":                              false,
			"contains_token":                                                  false,
			"contains_provider_url":                                           false,
			"contains_repository_ref":                                         false,
			"contains_branch_name":                                            false,
			"contains_file_content":                                           false,
			"status_snapshot_write_eligible":                                  false,
			"message":                                                         "Provider review attempt provider-call-boundary snapshot is waiting for an approved provider review execution approval.",
		})
		return
	}
	ledger, err := s.providerReviewAttemptLedgerForApproval(r.Context(), cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := RecordProviderReviewAttemptProviderCallBoundarySnapshot(r.Context(), s.store, ProviderReviewAttemptProviderCallBoundarySnapshotOptions{
		AttemptID: attemptID,
		DryRun:    req.DryRun,
		Attempt:   attempt,
		Ledger:    ledger,
	})
	if err != nil {
		s.log.Warn("provider review attempt provider-call-boundary snapshot failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review attempt provider-call-boundary snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordProviderReviewAttemptTransactionSnapshot(w http.ResponseWriter, r *http.Request) {
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
			"mode":                                   "provider_review_attempt_transaction_snapshot_recording",
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
			"provider_review_attempt_transaction_snapshot_written": false,
			"asset_status_snapshot_written":                        false,
			"operation_log_written":                                false,
			"external_call_made":                                   false,
			"provider_api_call_made":                               false,
			"provider_api_mutation":                                "disabled",
			"provider_request_sent":                                false,
			"provider_response_received":                           false,
			"transaction_opened":                                   false,
			"transaction_recorded":                                 false,
			"provider_call_boundary_opened":                        false,
			"provider_call_boundary_recorded":                      false,
			"response_recorded":                                    false,
			"dependency_update_recorded":                           false,
			"contains_token":                                       false,
			"contains_provider_url":                                false,
			"contains_repository_ref":                              false,
			"contains_branch_name":                                 false,
			"contains_file_content":                                false,
			"status_snapshot_write_eligible":                       false,
			"message":                                              "Provider review attempt transaction snapshot is waiting for an approved provider review execution approval.",
		})
		return
	}
	ledger, err := s.providerReviewAttemptLedgerForApproval(r.Context(), cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := RecordProviderReviewAttemptTransactionSnapshot(r.Context(), s.store, ProviderReviewAttemptTransactionSnapshotOptions{
		AttemptID: attemptID,
		DryRun:    req.DryRun,
		Attempt:   attempt,
		Ledger:    ledger,
	})
	if err != nil {
		s.log.Warn("provider review attempt transaction snapshot failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review attempt transaction snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

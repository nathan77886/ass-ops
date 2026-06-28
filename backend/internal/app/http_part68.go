package app

import (
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (s *Server) recordProviderReviewAttemptTransportSnapshot(w http.ResponseWriter, r *http.Request) {
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
			"mode":                                   "provider_review_attempt_transport_snapshot_recording",
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
			"provider_review_attempt_transport_snapshot_written": false,
			"asset_status_snapshot_written":                      false,
			"operation_log_written":                              false,
			"external_call_made":                                 false,
			"provider_api_call_made":                             false,
			"provider_api_mutation":                              "disabled",
			"provider_request_sent":                              false,
			"provider_response_received":                         false,
			"provider_client_bound":                              false,
			"request_path_materialized":                          false,
			"request_url_materialized":                           false,
			"request_body_materialized":                          false,
			"headers_materialized":                               false,
			"authorization_header_materialized":                  false,
			"mutation_armed":                                     false,
			"contains_token":                                     false,
			"contains_provider_url":                              false,
			"contains_repository_ref":                            false,
			"contains_branch_name":                               false,
			"contains_file_content":                              false,
			"canonical_asset_status_snapshot_try":                false,
			"snapshot_commit_attempted":                          false,
			"status_snapshot_write_eligible":                     false,
			"message":                                            "Provider review attempt transport snapshot is waiting for an approved provider review execution approval.",
		})
		return
	}
	ledger, err := s.providerReviewAttemptLedgerForApproval(r.Context(), cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := RecordProviderReviewAttemptTransportSnapshot(r.Context(), s.store, ProviderReviewAttemptTransportSnapshotOptions{
		AttemptID: attemptID,
		DryRun:    req.DryRun,
		Attempt:   attempt,
		Ledger:    ledger,
	})
	if err != nil {
		s.log.Warn("provider review attempt transport snapshot failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review attempt transport snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordProviderReviewAttemptRetryBackoffSnapshot(w http.ResponseWriter, r *http.Request) {
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
			"mode":                                   "provider_review_attempt_retry_backoff_snapshot_recording",
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
			"provider_review_attempt_retry_backoff_snapshot_written": false,
			"asset_status_snapshot_written":                          false,
			"operation_log_written":                                  false,
			"external_call_made":                                     false,
			"provider_api_call_made":                                 false,
			"provider_api_mutation":                                  "disabled",
			"provider_request_sent":                                  false,
			"provider_response_received":                             false,
			"retry_scheduled":                                        false,
			"retry_attempt_recorded":                                 false,
			"retry_after_value_recorded":                             false,
			"retry_after_header_included":                            false,
			"provider_rate_limit_value_included":                     false,
			"provider_error_code_included":                           false,
			"mutation_armed":                                         false,
			"contains_token":                                         false,
			"contains_provider_url":                                  false,
			"contains_repository_ref":                                false,
			"contains_branch_name":                                   false,
			"contains_file_content":                                  false,
			"canonical_asset_status_snapshot_try":                    false,
			"snapshot_commit_attempted":                              false,
			"status_snapshot_write_eligible":                         false,
			"message":                                                "Provider review attempt retry/backoff snapshot is waiting for an approved provider review execution approval.",
		})
		return
	}
	ledger, err := s.providerReviewAttemptLedgerForApproval(r.Context(), cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := RecordProviderReviewAttemptRetryBackoffSnapshot(r.Context(), s.store, ProviderReviewAttemptRetryBackoffSnapshotOptions{
		AttemptID: attemptID,
		DryRun:    req.DryRun,
		Attempt:   attempt,
		Ledger:    ledger,
	})
	if err != nil {
		s.log.Warn("provider review attempt retry/backoff snapshot failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review attempt retry/backoff snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordProviderReviewAttemptResponseSnapshot(w http.ResponseWriter, r *http.Request) {
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
			"mode":                           "provider_review_attempt_response_snapshot_recording",
			"recording_state":                "operation_approval_not_approved",
			"recording_ready":                false,
			"recording_enabled":              false,
			"dry_run":                        req.DryRun,
			"provider_review_attempt_id":     attemptID,
			"operation_approval_id":          cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
			"project_template_run_id":        cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
			"snapshots_written":              0,
			"snapshots_skipped_as_duplicate": 0,
			"provider_review_attempt_response_snapshot_written": false,
			"asset_status_snapshot_written":                     false,
			"external_call_made":                                false,
			"provider_api_call_made":                            false,
			"provider_api_mutation":                             "disabled",
			"provider_request_sent":                             false,
			"provider_response_received":                        false,
			"response_recorded":                                 false,
			"transaction_recorded":                              false,
			"message":                                           "Provider review attempt response snapshot is waiting for an approved provider review execution approval.",
		})
		return
	}
	ledger, err := s.providerReviewAttemptLedgerForApproval(r.Context(), cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := RecordProviderReviewAttemptResponseSnapshot(r.Context(), s.store, ProviderReviewAttemptResponseSnapshotOptions{
		AttemptID: attemptID,
		DryRun:    req.DryRun,
		Attempt:   attempt,
		Ledger:    ledger,
	})
	if err != nil {
		s.log.Warn("provider review attempt response snapshot failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review attempt response snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

package app

import (
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (s *Server) recordProviderReviewAttemptLiveAdapterContractSnapshot(w http.ResponseWriter, r *http.Request) {
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
			"mode":                                   "provider_review_attempt_live_adapter_contract_snapshot_recording",
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
			"provider_review_attempt_live_adapter_contract_snapshot_written": false,
			"asset_status_snapshot_written":                                  false,
			"operation_log_written":                                          false,
			"external_call_made":                                             false,
			"provider_api_call_made":                                         false,
			"provider_api_mutation":                                          "disabled",
			"live_adapter_implemented":                                       false,
			"provider_request_sent":                                          false,
			"provider_response_received":                                     false,
			"mutation_armed":                                                 false,
			"request_contract_materialized":                                  false,
			"response_contract_materialized":                                 false,
			"error_contract_materialized":                                    false,
			"result_contract_materialized":                                   false,
			"contains_token":                                                 false,
			"contains_provider_url":                                          false,
			"contains_repository_ref":                                        false,
			"contains_branch_name":                                           false,
			"contains_file_content":                                          false,
			"canonical_asset_status_snapshot_try":                            false,
			"snapshot_commit_attempted":                                      false,
			"status_snapshot_write_eligible":                                 false,
			"message":                                                        "Provider review attempt live-adapter contract snapshot is waiting for an approved provider review execution approval.",
		})
		return
	}
	ledger, err := s.providerReviewAttemptLedgerForApproval(r.Context(), cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := RecordProviderReviewAttemptLiveAdapterContractSnapshot(r.Context(), s.store, ProviderReviewAttemptLiveAdapterContractSnapshotOptions{
		AttemptID: attemptID,
		DryRun:    req.DryRun,
		Attempt:   attempt,
		Ledger:    ledger,
	})
	if err != nil {
		s.log.Warn("provider review attempt live-adapter contract snapshot failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review attempt live-adapter contract snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordProviderReviewAttemptCredentialSnapshot(w http.ResponseWriter, r *http.Request) {
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
			"mode":                           "provider_review_attempt_credential_snapshot_recording",
			"recording_state":                "operation_approval_not_approved",
			"recording_ready":                false,
			"recording_enabled":              false,
			"dry_run":                        req.DryRun,
			"provider_review_attempt_id":     attemptID,
			"operation_approval_id":          cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
			"project_template_run_id":        cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
			"snapshots_written":              0,
			"snapshots_skipped_as_duplicate": 0,
			"provider_review_attempt_credential_snapshot_written": false,
			"asset_status_snapshot_written":                       false,
			"external_call_made":                                  false,
			"provider_api_call_made":                              false,
			"provider_api_mutation":                               "disabled",
			"credential_bound":                                    false,
			"authorization_header_materialized":                   false,
			"message":                                             "Provider review attempt credential snapshot is waiting for an approved provider review execution approval.",
		})
		return
	}
	ledger, err := s.providerReviewAttemptLedgerForApproval(r.Context(), cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := RecordProviderReviewAttemptCredentialSnapshot(r.Context(), s.store, ProviderReviewAttemptCredentialSnapshotOptions{
		AttemptID: attemptID,
		DryRun:    req.DryRun,
		Attempt:   attempt,
		Ledger:    ledger,
	})
	if err != nil {
		s.log.Warn("provider review attempt credential snapshot failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review attempt credential snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordProviderReviewAttemptSendSnapshot(w http.ResponseWriter, r *http.Request) {
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
			"mode":                           "provider_review_attempt_send_snapshot_recording",
			"recording_state":                "operation_approval_not_approved",
			"recording_ready":                false,
			"recording_enabled":              false,
			"dry_run":                        req.DryRun,
			"provider_review_attempt_id":     attemptID,
			"operation_approval_id":          cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
			"project_template_run_id":        cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
			"snapshots_written":              0,
			"snapshots_skipped_as_duplicate": 0,
			"provider_review_attempt_send_snapshot_written": false,
			"asset_status_snapshot_written":                 false,
			"external_call_made":                            false,
			"provider_api_call_made":                        false,
			"provider_api_mutation":                         "disabled",
			"provider_request_sent":                         false,
			"send_attempted":                                false,
			"message":                                       "Provider review attempt send snapshot is waiting for an approved provider review execution approval.",
		})
		return
	}
	ledger, err := s.providerReviewAttemptLedgerForApproval(r.Context(), cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := RecordProviderReviewAttemptSendSnapshot(r.Context(), s.store, ProviderReviewAttemptSendSnapshotOptions{
		AttemptID: attemptID,
		DryRun:    req.DryRun,
		Attempt:   attempt,
		Ledger:    ledger,
	})
	if err != nil {
		s.log.Warn("provider review attempt send snapshot failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review attempt send snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

package app

import (
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (s *Server) recordProviderReviewAttemptRuntimeSnapshot(w http.ResponseWriter, r *http.Request) {
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
			"mode":                           "provider_review_attempt_runtime_snapshot_recording",
			"recording_state":                "operation_approval_not_approved",
			"recording_ready":                false,
			"recording_enabled":              false,
			"dry_run":                        req.DryRun,
			"provider_review_attempt_id":     attemptID,
			"operation_approval_id":          cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
			"project_template_run_id":        cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
			"snapshots_written":              0,
			"snapshots_skipped_as_duplicate": 0,
			"provider_review_attempt_runtime_snapshot_written": false,
			"asset_status_snapshot_written":                    false,
			"external_call_made":                               false,
			"provider_api_call_made":                           false,
			"provider_api_mutation":                            "disabled",
			"live_adapter_implemented":                         false,
			"provider_client_constructed":                      false,
			"runtime_bound":                                    false,
			"contains_token":                                   false,
			"contains_provider_url":                            false,
			"contains_repository_ref":                          false,
			"contains_branch_name":                             false,
			"contains_file_content":                            false,
			"status_snapshot_write_eligible":                   false,
			"message":                                          "Provider review attempt runtime snapshot is waiting for an approved provider review execution approval.",
		})
		return
	}
	ledger, err := s.providerReviewAttemptLedgerForApproval(r.Context(), cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := RecordProviderReviewAttemptRuntimeSnapshot(r.Context(), s.store, ProviderReviewAttemptRuntimeSnapshotOptions{
		AttemptID: attemptID,
		DryRun:    req.DryRun,
		Attempt:   attempt,
		Ledger:    ledger,
	})
	if err != nil {
		s.log.Warn("provider review attempt runtime snapshot failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review attempt runtime snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordProviderReviewAttemptAdapterRehearsalSnapshot(w http.ResponseWriter, r *http.Request) {
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
			"mode":                           "provider_review_attempt_adapter_rehearsal_snapshot_recording",
			"recording_state":                "operation_approval_not_approved",
			"recording_ready":                false,
			"recording_enabled":              false,
			"dry_run":                        req.DryRun,
			"provider_review_attempt_id":     attemptID,
			"operation_approval_id":          cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
			"project_template_run_id":        cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
			"snapshots_written":              0,
			"snapshots_skipped_as_duplicate": 0,
			"provider_review_attempt_adapter_rehearsal_snapshot_written": false,
			"asset_status_snapshot_written":                              false,
			"external_call_made":                                         false,
			"provider_api_call_made":                                     false,
			"provider_api_mutation":                                      "disabled",
			"live_adapter_implemented":                                   false,
			"provider_client_constructed":                                false,
			"mutation_armed":                                             false,
			"contains_token":                                             false,
			"contains_provider_url":                                      false,
			"contains_repository_ref":                                    false,
			"contains_branch_name":                                       false,
			"contains_file_content":                                      false,
			"status_snapshot_write_eligible":                             false,
			"message":                                                    "Provider review attempt adapter rehearsal snapshot is waiting for an approved provider review execution approval.",
		})
		return
	}
	ledger, err := s.providerReviewAttemptLedgerForApproval(r.Context(), cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := RecordProviderReviewAttemptAdapterRehearsalSnapshot(r.Context(), s.store, ProviderReviewAttemptAdapterRehearsalSnapshotOptions{
		AttemptID: attemptID,
		DryRun:    req.DryRun,
		Attempt:   attempt,
		Ledger:    ledger,
	})
	if err != nil {
		s.log.Warn("provider review attempt adapter rehearsal snapshot failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review attempt adapter rehearsal snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordProviderReviewAttemptAdapterBlueprintSnapshot(w http.ResponseWriter, r *http.Request) {
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
			"mode":                           "provider_review_attempt_adapter_blueprint_snapshot_recording",
			"recording_state":                "operation_approval_not_approved",
			"recording_ready":                false,
			"recording_enabled":              false,
			"dry_run":                        req.DryRun,
			"provider_review_attempt_id":     attemptID,
			"operation_approval_id":          cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
			"project_template_run_id":        cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
			"snapshots_written":              0,
			"snapshots_skipped_as_duplicate": 0,
			"provider_review_attempt_adapter_blueprint_snapshot_written": false,
			"asset_status_snapshot_written":                              false,
			"external_call_made":                                         false,
			"provider_api_call_made":                                     false,
			"provider_api_mutation":                                      "disabled",
			"adapter_implemented":                                        false,
			"live_adapter_implemented":                                   false,
			"provider_request_sent":                                      false,
			"mutation_armed":                                             false,
			"contains_token":                                             false,
			"contains_provider_url":                                      false,
			"contains_repository_ref":                                    false,
			"contains_branch_name":                                       false,
			"contains_file_content":                                      false,
			"status_snapshot_write_eligible":                             false,
			"message":                                                    "Provider review attempt adapter blueprint snapshot is waiting for an approved provider review execution approval.",
		})
		return
	}
	ledger, err := s.providerReviewAttemptLedgerForApproval(r.Context(), cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := RecordProviderReviewAttemptAdapterBlueprintSnapshot(r.Context(), s.store, ProviderReviewAttemptAdapterBlueprintSnapshotOptions{
		AttemptID: attemptID,
		DryRun:    req.DryRun,
		Attempt:   attempt,
		Ledger:    ledger,
	})
	if err != nil {
		s.log.Warn("provider review attempt adapter blueprint snapshot failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review attempt adapter blueprint snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

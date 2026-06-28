package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/url"
	"strings"
)

func providerReviewAttemptLiveExecutionConflictResponse(attemptID string) map[string]any {
	return map[string]any{
		"live_execution_state":       "provider_review_attempt_execution_conflict",
		"live_execution_ready":       false,
		"live_execution_recorded":    false,
		"executed":                   false,
		"provider_review_attempt_id": cleanOptionalID(attemptID),
		"missing_evidence":           []string{"provider_review_attempt_execution_conflict"},
		"external_call_made":         false,
		"provider_api_call_made":     false,
		"provider_api_mutation":      "disabled",
		"provider_request_sent":      false,
		"provider_response_received": false,
		"contains_token":             false,
		"contains_repository_ref":    false,
		"contains_branch_name":       false,
		"contains_file_content":      false,
	}
}

func providerReviewLiveExecutionHash(attemptID, operationName, approvalID string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		cleanOptionalID(attemptID),
		safeProviderReviewAttemptOperationName(operationName),
		cleanOptionalID(approvalID),
		"atomic_github_review_branch_executor",
	}, ":")))
	return hex.EncodeToString(sum[:])
}

func safeProviderReviewExecutionBranch(value string) string {
	value = strings.TrimSpace(value)
	if !isSafeGitRefPart(value) {
		return ""
	}
	return value
}

func safeProviderReviewProviderAPIMutation(value string) string {
	switch cleanOptionalText(value) {
	case "enabled":
		return "enabled"
	default:
		return "disabled"
	}
}

func sanitizeProviderReviewURLForResponse(value string) string {
	value = strings.TrimSpace(sanitizeURLUserInfo(value))
	if len(value) > 512 {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil || parsed.User != nil {
		return ""
	}
	switch parsed.Scheme {
	case "http", "https":
		if parsed.Host == "" {
			return ""
		}
		return parsed.String()
	default:
		return ""
	}
}

func (s *Server) recordProviderReviewAttemptSnapshot(w http.ResponseWriter, r *http.Request) {
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
	attempt, err := providerReviewAttemptForSnapshot(r.Context(), s.store.Gorm, attemptID)
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
			"mode":                                     "provider_review_attempt_snapshot_recording",
			"recording_state":                          "operation_approval_not_approved",
			"recording_ready":                          false,
			"recording_enabled":                        false,
			"dry_run":                                  req.DryRun,
			"provider_review_attempt_id":               attemptID,
			"operation_approval_id":                    cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
			"project_template_run_id":                  cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
			"snapshots_written":                        0,
			"snapshots_skipped_as_duplicate":           0,
			"provider_review_attempt_snapshot_written": false,
			"asset_status_snapshot_written":            false,
			"external_call_made":                       false,
			"provider_api_call_made":                   false,
			"provider_api_mutation":                    "disabled",
			"message":                                  "Provider review attempt snapshot is waiting for an approved provider review execution approval.",
		})
		return
	}
	result, err := RecordProviderReviewAttemptSnapshot(r.Context(), s.store, ProviderReviewAttemptSnapshotOptions{
		AttemptID: attemptID,
		DryRun:    req.DryRun,
		Attempt:   attempt,
	})
	if err != nil {
		s.log.Warn("provider review attempt snapshot failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review attempt snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordProviderReviewAttemptActivationSnapshot(w http.ResponseWriter, r *http.Request) {
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
			"mode":                           "provider_review_attempt_activation_snapshot_recording",
			"recording_state":                "operation_approval_not_approved",
			"recording_ready":                false,
			"recording_enabled":              false,
			"dry_run":                        req.DryRun,
			"provider_review_attempt_id":     attemptID,
			"operation_approval_id":          cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
			"project_template_run_id":        cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
			"snapshots_written":              0,
			"snapshots_skipped_as_duplicate": 0,
			"provider_review_attempt_activation_snapshot_written": false,
			"asset_status_snapshot_written":                       false,
			"external_call_made":                                  false,
			"provider_api_call_made":                              false,
			"provider_api_mutation":                               "disabled",
			"message":                                             "Provider review attempt activation snapshot is waiting for an approved provider review execution approval.",
		})
		return
	}
	ledger, err := providerReviewAttemptLedgerForApprovalSnapshot(r.Context(), s.store, cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := RecordProviderReviewAttemptActivationSnapshot(r.Context(), s.store, ProviderReviewAttemptActivationSnapshotOptions{
		AttemptID: attemptID,
		DryRun:    req.DryRun,
		Attempt:   attempt,
		Ledger:    ledger,
	})
	if err != nil {
		s.log.Warn("provider review attempt activation snapshot failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review attempt activation snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) recordProviderReviewAttemptRequestEnvelopeSnapshot(w http.ResponseWriter, r *http.Request) {
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
			"mode":                           "provider_review_attempt_request_envelope_snapshot_recording",
			"recording_state":                "operation_approval_not_approved",
			"recording_ready":                false,
			"recording_enabled":              false,
			"dry_run":                        req.DryRun,
			"provider_review_attempt_id":     attemptID,
			"operation_approval_id":          cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])),
			"project_template_run_id":        cleanOptionalID(fmt.Sprint(attempt["project_template_run_id"])),
			"snapshots_written":              0,
			"snapshots_skipped_as_duplicate": 0,
			"provider_review_attempt_request_envelope_snapshot_written": false,
			"asset_status_snapshot_written":                             false,
			"external_call_made":                                        false,
			"provider_api_call_made":                                    false,
			"provider_api_mutation":                                     "disabled",
			"provider_request_sent":                                     false,
			"request_envelope_materialized":                             false,
			"message":                                                   "Provider review attempt request envelope snapshot is waiting for an approved provider review execution approval.",
		})
		return
	}
	ledger, err := s.providerReviewAttemptLedgerForApproval(r.Context(), cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider review attempts")
		return
	}
	result, err := RecordProviderReviewAttemptRequestEnvelopeSnapshot(r.Context(), s.store, ProviderReviewAttemptRequestEnvelopeSnapshotOptions{
		AttemptID: attemptID,
		DryRun:    req.DryRun,
		Attempt:   attempt,
		Ledger:    ledger,
	})
	if err != nil {
		s.log.Warn("provider review attempt request envelope snapshot failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "record provider review attempt request envelope snapshot failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

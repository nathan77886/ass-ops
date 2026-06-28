package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"io"
	"log/slog"
	"net/http"
	"time"
)

func (s *Server) claimProviderReviewAttempt(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "update") {
		return
	}
	attemptID := cleanOptionalID(chi.URLParam(r, "id"))
	if attemptID == "" {
		writeError(w, http.StatusBadRequest, "provider review attempt id is required")
		return
	}
	if !s.requireProviderReviewAttemptUpdatePolicy(w, r, attemptID) {
		return
	}
	var claimed map[string]any
	var ledger map[string]any
	var claimPlan map[string]any
	var blocked map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		attemptModel, _, attempt, err := providerReviewAttemptWithApprovalGorm(r.Context(), tx, attemptID, true)
		if err != nil {
			return err
		}
		if stringFromMap(attempt, "approval_action") != templateProviderReviewExecuteApprovalAction {
			blocked = map[string]any{"http_status": http.StatusConflict, "error": "provider review attempt is not tied to provider review execution approval"}
			return nil
		}
		if stringFromMap(attempt, "approval_status") != "approved" {
			blocked = providerReviewAttemptClaimBlockedResponse(attempt, "operation_approval_not_approved", nil)
			return nil
		}
		claimPlan = providerReviewAttemptClaimPlanFromAttempt(attempt)
		if claimPlan["claim_metadata_ready"] != true {
			blocked = providerReviewAttemptClaimBlockedResponse(attempt, "provider_review_attempt_claim_metadata_not_ready", claimPlan)
			return nil
		}
		updates := map[string]any{
			"status":     "running",
			"claimed_at": sql.NullTime{Time: time.Now().UTC(), Valid: true},
			"updated_at": time.Now().UTC(),
		}
		if user := currentUser(r); user != nil && cleanOptionalID(user.ID) != "" {
			updates["claimed_by_user_id"] = validNullString(user.ID)
		}
		result := tx.Model(&GormProviderReviewAttempt{}).
			Where(&GormProviderReviewAttempt{GormBase: GormBase{ID: attemptModel.ID}, Status: "planned", DependencyStatus: attemptModel.DependencyStatus, ProviderAPICallMade: false, ExternalCallMade: false, ProviderAPIMutation: "disabled"}).
			Where("dependency_status IN ?", []string{"independent", "dependency_satisfied"}).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			blocked = providerReviewAttemptClaimBlockedResponse(attempt, "provider_review_attempt_claim_conflict", claimPlan)
			return nil
		}
		var reloaded GormProviderReviewAttempt
		if err := tx.First(&reloaded, &GormProviderReviewAttempt{GormBase: GormBase{ID: attemptModel.ID}}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		claimed = providerReviewAttemptMap(reloaded, nil)
		ledger, err = providerReviewAttemptLedgerForApprovalGorm(r.Context(), tx, attemptModel.OperationApprovalID)
		if err != nil {
			return err
		}
		_, err = syncCanonicalAssetsGorm(r.Context(), tx)
		if err != nil {
			return fmt.Errorf("syncing canonical assets for provider_review_attempt.claim: %w", err)
		}
		return nil
	}); err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if blocked != nil {
		if status := intFromAny(blocked["http_status"], 0); status >= 400 {
			writeError(w, status, cleanOptionalText(stringFromMap(blocked, "error")))
			return
		}
		writeJSON(w, http.StatusOK, blocked)
		return
	}
	writeJSON(w, http.StatusOK, providerReviewAttemptClaimResponse(claimed, ledger, true, "claimed"))
}

type providerReviewAttemptLocalResultRequest struct {
	Result string `json:"result"`
}

func (s *Server) recordProviderReviewAttemptLocalResult(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "update") {
		return
	}
	attemptID := cleanOptionalID(chi.URLParam(r, "id"))
	if attemptID == "" {
		writeError(w, http.StatusBadRequest, "provider review attempt id is required")
		return
	}
	var req providerReviewAttemptLocalResultRequest
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid provider review attempt result payload")
			return
		}
	}
	resultStatus := safeProviderReviewAttemptLocalResultStatus(req.Result)
	if resultStatus == "" {
		writeError(w, http.StatusBadRequest, "result must be success, retryable, or failed")
		return
	}
	if !s.requireProviderReviewAttemptUpdatePolicy(w, r, attemptID) {
		return
	}
	var recorded map[string]any
	var ledger map[string]any
	var resultPlan map[string]any
	var blocked map[string]any
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		attemptModel, _, attempt, err := providerReviewAttemptWithApprovalGorm(r.Context(), tx, attemptID, true)
		if err != nil {
			return err
		}
		if stringFromMap(attempt, "approval_action") != templateProviderReviewExecuteApprovalAction {
			blocked = map[string]any{"http_status": http.StatusConflict, "error": "provider review attempt is not tied to provider review execution approval"}
			return nil
		}
		if stringFromMap(attempt, "approval_status") != "approved" {
			blocked = providerReviewAttemptLocalResultBlockedResponse(attempt, "operation_approval_not_approved", resultStatus, nil)
			return nil
		}
		resultPlan = providerReviewAttemptLocalResultPlanFromAttempt(attempt, resultStatus)
		if resultPlan["result_recording_metadata_ready"] != true {
			blocked = providerReviewAttemptLocalResultBlockedResponse(attempt, "provider_review_attempt_result_metadata_not_ready", resultStatus, resultPlan)
			return nil
		}
		attemptStatus := providerReviewAttemptStatusForLocalResult(resultStatus)
		updates := map[string]any{
			"status":               attemptStatus,
			"response_diagnostics": JSONValue{Data: providerReviewAttemptLocalResultDiagnostics(attempt, resultStatus)},
			"updated_at":           time.Now().UTC(),
		}
		if attemptStatus == "planned" {
			updates["claimed_at"] = sql.NullTime{}
			updates["claimed_by_user_id"] = sql.NullString{}
		}
		result := tx.Model(&GormProviderReviewAttempt{}).
			Where(&GormProviderReviewAttempt{GormBase: GormBase{ID: attemptModel.ID}, Status: "running", ProviderAPICallMade: false, ExternalCallMade: false, ProviderAPIMutation: "disabled"}).
			Where("claimed_at IS NOT NULL").
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			blocked = providerReviewAttemptLocalResultBlockedResponse(attempt, "provider_review_attempt_result_conflict", resultStatus, resultPlan)
			return nil
		}
		if dependencyStatus := providerReviewAttemptDependencyStatusForLocalResult(resultStatus); dependencyStatus != "" {
			if err := tx.Model(&GormProviderReviewAttempt{}).
				Where(&GormProviderReviewAttempt{OperationApprovalID: attemptModel.OperationApprovalID, DependsOnOperation: safeProviderReviewAttemptOperationName(attemptModel.OperationName), DependencyStatus: "waiting_for_dependency", ProviderAPICallMade: false, ExternalCallMade: false, ProviderAPIMutation: "disabled"}).
				Updates(map[string]any{"dependency_status": dependencyStatus, "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		var reloaded GormProviderReviewAttempt
		if err := tx.First(&reloaded, &GormProviderReviewAttempt{GormBase: GormBase{ID: attemptModel.ID}}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		recorded = providerReviewAttemptMap(reloaded, nil)
		ledger, err = providerReviewAttemptLedgerForApprovalGorm(r.Context(), tx, attemptModel.OperationApprovalID)
		if err != nil {
			return err
		}
		_, err = syncCanonicalAssetsGorm(r.Context(), tx)
		if err != nil {
			return fmt.Errorf("syncing canonical assets for provider_review_attempt.local_result: %w", err)
		}
		return nil
	}); err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if blocked != nil {
		if status := intFromAny(blocked["http_status"], 0); status >= 400 {
			writeError(w, status, cleanOptionalText(stringFromMap(blocked, "error")))
			return
		}
		writeJSON(w, http.StatusOK, blocked)
		return
	}
	writeJSON(w, http.StatusOK, providerReviewAttemptLocalResultResponse(recorded, ledger, true, "recorded", resultStatus, resultPlan))
}

func (s *Server) executeProviderReviewAttemptLive(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "update") {
		return
	}
	attemptID := cleanOptionalID(chi.URLParam(r, "id"))
	if attemptID == "" {
		writeError(w, http.StatusBadRequest, "provider review attempt id is required")
		return
	}
	if !s.requireProviderReviewAttemptUpdatePolicy(w, r, attemptID) {
		return
	}
	locked, unlock, err := s.acquireProviderReviewLiveExecutionLock(r.Context(), attemptID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not lock provider review live execution")
		return
	}
	if !locked {
		writeJSON(w, http.StatusOK, providerReviewAttemptLiveExecutionConflictResponse(attemptID))
		return
	}
	defer unlock()
	input, attempt, blocked, err := s.providerReviewLiveExecutionInput(r.Context(), attemptID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not prepare provider review live execution")
		return
	}
	if blocked != nil {
		writeJSON(w, http.StatusOK, blocked)
		return
	}
	idempotencyHash := providerReviewLiveExecutionHash(attemptID, stringFromMap(attempt, "operation_name"), cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])))
	idempotencyMaterial, err := jsonParam(map[string]any{
		"material":                      "redacted_live_execution_attempt_scope",
		"provider_review_attempt_id":    attemptID,
		"operation_name":                safeProviderReviewAttemptOperationName(stringFromMap(attempt, "operation_name")),
		"operation_approval_id_present": cleanOptionalID(fmt.Sprint(attempt["operation_approval_id"])) != "",
		"branch_name_included":          false,
		"repository_ref_included":       false,
		"provider_url_included":         false,
		"file_content_included":         false,
		"token_included":                false,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not encode provider review idempotency material")
		return
	}
	if blocked, err := s.markProviderReviewAttemptLiveExecutionArmed(r.Context(), attemptID, idempotencyHash, idempotencyMaterial); err != nil {
		writeError(w, http.StatusInternalServerError, "could not arm provider review live execution")
		return
	} else if blocked != nil {
		writeJSON(w, http.StatusOK, blocked)
		return
	}
	result, execErr := (reviewBranchExecutor{HTTPClient: newTemplateProviderHTTPClient()}).Execute(r.Context(), input)
	recorded, err := s.recordProviderReviewAttemptLiveExecutionResult(r.Context(), attemptID, attempt, result, execErr)
	if err != nil {
		log := s.log
		if log == nil {
			log = slog.Default()
		}
		log.Warn("provider review live execution result recording failed", "provider_review_attempt_id", attemptID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not record provider review live execution result")
		return
	}
	writeJSON(w, http.StatusOK, recorded)
}

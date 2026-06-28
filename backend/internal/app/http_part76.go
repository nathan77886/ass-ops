package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"strings"
	"time"
)

func (s *Server) approveOperationApproval(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	expired, err := s.expireOperationApprovalByIDGorm(r.Context(), s.store.Gorm, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not expire approval")
		return
	}
	if expired != nil {
		s.dispatchApprovalNotification(r.Context(), expired, "expired")
		writeError(w, http.StatusConflict, "approval is not pending")
		return
	}
	var approvalModel GormOperationApproval
	if err := s.store.Gorm.WithContext(r.Context()).First(&approvalModel, &GormOperationApproval{GormBase: GormBase{ID: chi.URLParam(r, "id")}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	approval := operationApprovalMap(approvalModel, nil, nil, nil, nil)
	if approvalModel.Status != "pending" {
		writeError(w, http.StatusConflict, "approval is not pending")
		return
	}
	if !s.canDecideOperationApproval(r.Context(), currentUser(r), approval) {
		writeError(w, http.StatusForbidden, "approval decision requires one of the configured approver roles")
		return
	}
	approvedCount := 0
	requiredCount := requiredApprovalCount(approval["required_approval_count"])
	var item map[string]any
	var shouldNotify bool
	err = s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&approvalModel, &GormOperationApproval{GormBase: GormBase{ID: chi.URLParam(r, "id")}}).Error; err != nil {
			return gormNotFoundAsErrNotFound(err)
		}
		if approvalModel.Status != "pending" {
			return errApprovalNotPending
		}
		if err := upsertOperationApprovalDecisionGorm(r.Context(), tx, chi.URLParam(r, "id"), currentUser(r).ID, "approved", strings.TrimSpace(req.Reason)); err != nil {
			return fmt.Errorf("recording approval decision: %w", err)
		}
		var err error
		approvedCount, err = operationApprovalApprovedCountGorm(r.Context(), tx, chi.URLParam(r, "id"))
		if err != nil {
			return fmt.Errorf("counting approval decisions: %w", err)
		}
		approval = operationApprovalMap(approvalModel, nil, nil, nil, nil)
		if approvedCount < requiredCount {
			approvalModel.DecidedBy = validNullString(currentUser(r).ID)
			approvalModel.DecisionReason = strings.TrimSpace(req.Reason)
			if err := tx.Save(&approvalModel).Error; err != nil {
				return err
			}
			item = operationApprovalMap(approvalModel, nil, nil, nil, nil)
			_, err = syncCanonicalAssetsGorm(r.Context(), tx)
			return err
		}
		result, operationRunID, err := s.executeApprovedOperation(r.Context(), tx, approval)
		if err != nil {
			return err
		}
		payload := mapFromAny(approvalModel.RequestPayload.Data)
		payload["approval_result"] = result
		approvalModel.Status = "approved"
		approvalModel.OperationRunID = validNullString(operationRunID)
		approvalModel.DecidedBy = validNullString(currentUser(r).ID)
		approvalModel.DecisionReason = strings.TrimSpace(req.Reason)
		approvalModel.DecidedAt = validNullTime(time.Now().UTC())
		approvalModel.RequestPayload = JSONValue{Data: payload}
		if err := tx.Save(&approvalModel).Error; err != nil {
			return err
		}
		item = operationApprovalMap(approvalModel, nil, nil, nil, nil)
		shouldNotify = true
		_, err = syncCanonicalAssetsGorm(r.Context(), tx)
		return err
	})
	if err != nil {
		if errors.Is(err, errApprovalNotPending) {
			writeError(w, http.StatusConflict, "approval is not pending")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	delete(item, "request_payload")
	item["approved_count"] = approvedCount
	item["required_approval_count"] = requiredCount
	if shouldNotify {
		item = s.dispatchApprovalNotification(r.Context(), item, "approved")
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) rejectOperationApproval(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	expired, err := s.expireOperationApprovalByIDGorm(r.Context(), s.store.Gorm, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not expire approval")
		return
	}
	if expired != nil {
		s.dispatchApprovalNotification(r.Context(), expired, "expired")
		writeError(w, http.StatusConflict, "approval is not pending")
		return
	}
	var approvalModel GormOperationApproval
	if err := s.store.Gorm.WithContext(r.Context()).First(&approvalModel, &GormOperationApproval{GormBase: GormBase{ID: chi.URLParam(r, "id")}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	approval := operationApprovalMap(approvalModel, nil, nil, nil, nil)
	if approvalModel.Status != "pending" {
		writeError(w, http.StatusConflict, "approval is not pending")
		return
	}
	if !s.canDecideOperationApproval(r.Context(), currentUser(r), approval) {
		writeError(w, http.StatusForbidden, "approval decision requires one of the configured approver roles")
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := upsertOperationApprovalDecisionGorm(r.Context(), tx, chi.URLParam(r, "id"), currentUser(r).ID, "rejected", reason); err != nil {
			return err
		}
		now := time.Now().UTC()
		approvalModel.Status = "rejected"
		approvalModel.DecidedBy = validNullString(currentUser(r).ID)
		approvalModel.DecisionReason = reason
		approvalModel.DecidedAt = validNullTime(now)
		if err := tx.Save(&approvalModel).Error; err != nil {
			return err
		}
		_, err := syncCanonicalAssetsGorm(r.Context(), tx)
		if err != nil {
			return fmt.Errorf("syncing canonical assets for operation_approval.reject: %w", err)
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit approval")
		return
	}
	item := operationApprovalMap(approvalModel, nil, nil, nil, nil)
	delete(item, "request_payload")
	item = s.dispatchApprovalNotification(r.Context(), item, "rejected")
	writeQueryOne(w, item, nil)
}

func (s *Server) remindOperationApproval(w http.ResponseWriter, r *http.Request) {
	expired, err := s.expireOperationApprovalByIDGorm(r.Context(), s.store.Gorm, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not expire approval")
		return
	}
	if expired != nil {
		s.dispatchApprovalNotification(r.Context(), expired, "expired")
		writeError(w, http.StatusConflict, "approval is not pending")
		return
	}
	var approvalModel GormOperationApproval
	if err := s.store.Gorm.WithContext(r.Context()).First(&approvalModel, &GormOperationApproval{GormBase: GormBase{ID: chi.URLParam(r, "id")}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	approval := operationApprovalMap(approvalModel, nil, nil, nil, nil)
	if !s.requireApprovalRead(w, r, approval) {
		return
	}
	if approvalModel.Status != "pending" {
		writeError(w, http.StatusConflict, "approval is not pending")
		return
	}
	if !s.canDecideOperationApproval(r.Context(), currentUser(r), approval) {
		writeError(w, http.StatusForbidden, "approval reminder requires one of the configured approver roles")
		return
	}
	approvedCount, err := operationApprovalApprovedCountGorm(r.Context(), s.store.Gorm, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not count approval decisions")
		return
	}
	approval["approved_count"] = approvedCount
	approval["required_approval_count"] = requiredApprovalCount(approval["required_approval_count"])
	delete(approval, "request_payload")
	approvalModel.LastReminderAt = validNullTime(time.Now().UTC())
	approvalModel.ReminderCount++
	if err := s.store.Gorm.WithContext(r.Context()).Save(&approvalModel).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "could not record approval reminder")
		return
	}
	approval = operationApprovalMap(approvalModel, nil, nil, nil, nil)
	approval["approved_count"] = approvedCount
	approval["required_approval_count"] = requiredApprovalCount(approval["required_approval_count"])
	delete(approval, "request_payload")
	item := s.dispatchApprovalNotification(r.Context(), approval, "reminder")
	delete(item, "request_payload")
	item["approved_count"] = approvedCount
	item["required_approval_count"] = approval["required_approval_count"]
	writeJSON(w, http.StatusOK, item)
}

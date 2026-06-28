package app

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"strings"
	"time"
)

func (s *Server) createOperationApprovalDelegation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ToEmail string `json:"to_email"`
		Reason  string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.ToEmail = strings.ToLower(strings.TrimSpace(req.ToEmail))
	if req.ToEmail == "" {
		writeError(w, http.StatusBadRequest, "to_email is required")
		return
	}
	if s == nil || s.store == nil || s.store.Gorm == nil {
		writeError(w, http.StatusInternalServerError, "approval store is not configured")
		return
	}
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
		writeError(w, http.StatusForbidden, "approval delegation requires one of the configured approver roles")
		return
	}
	var target GormUser
	if err := s.store.Gorm.WithContext(r.Context()).Where(&GormUser{Email: req.ToEmail}).First(&target).Error; err != nil {
		writeError(w, http.StatusNotFound, "delegate user not found")
		return
	}
	if target.ID == currentUser(r).ID {
		writeError(w, http.StatusBadRequest, "cannot delegate approval to yourself")
		return
	}
	delegation := GormOperationApprovalDelegation{OperationApprovalID: chi.URLParam(r, "id"), FromUserID: validNullString(currentUser(r).ID), ToUserID: target.ID, Reason: strings.TrimSpace(req.Reason), RevokedAt: sql.NullTime{}}
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "operation_approval_id"}, {Name: "to_user_id"}},
			DoUpdates: clause.Assignments(map[string]any{"from_user_id": delegation.FromUserID, "reason": delegation.Reason, "revoked_at": sql.NullTime{}}),
		}).Create(&delegation).Error; err != nil {
			return err
		}
		if err := tx.Where(&GormOperationApprovalDelegation{OperationApprovalID: chi.URLParam(r, "id"), ToUserID: target.ID}).First(&delegation).Error; err != nil {
			return err
		}
		_, err := syncCanonicalAssetsGorm(r.Context(), tx)
		if err != nil {
			return fmt.Errorf("syncing canonical assets for operation_approval.delegation.create: %w", err)
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create approval delegation")
		return
	}
	item := operationApprovalDelegationMap(delegation, map[string]GormUser{target.ID: target, currentUser(r).ID: {GormBase: GormBase{ID: currentUser(r).ID}, Email: currentUser(r).Email}})
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) revokeOperationApprovalDelegation(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.store == nil || s.store.Gorm == nil {
		writeError(w, http.StatusInternalServerError, "approval store is not configured")
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
	var delegation GormOperationApprovalDelegation
	if err := s.store.Gorm.WithContext(r.Context()).First(&delegation, &GormOperationApprovalDelegation{ID: chi.URLParam(r, "delegationID"), OperationApprovalID: chi.URLParam(r, "id")}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	if delegation.RevokedAt.Valid {
		writeError(w, http.StatusConflict, "approval delegation is already revoked")
		return
	}
	delegationMap := operationApprovalDelegationMap(delegation, nil)
	if !s.canRevokeOperationApprovalDelegation(r.Context(), currentUser(r), approval, delegationMap) {
		writeError(w, http.StatusForbidden, "approval delegation revoke requires the delegator, an active approver, or an operator role")
		return
	}
	delegation.RevokedAt = validNullTime(time.Now().UTC())
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&delegation).Error; err != nil {
			return err
		}
		_, err := syncCanonicalAssetsGorm(r.Context(), tx)
		if err != nil {
			return fmt.Errorf("syncing canonical assets for operation_approval.delegation.revoke: %w", err)
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit approval delegation revoke")
		return
	}
	writeJSON(w, http.StatusOK, operationApprovalDelegationMap(delegation, nil))
}

func operationApprovalDelegationMap(delegation GormOperationApprovalDelegation, users map[string]GormUser) map[string]any {
	fromID := cleanOptionalID(delegation.FromUserID.String)
	return map[string]any{"id": delegation.ID, "operation_approval_id": delegation.OperationApprovalID, "from_user_id": nullableStringValue(delegation.FromUserID), "from_user_email": users[fromID].Email, "to_user_id": delegation.ToUserID, "to_user_email": users[delegation.ToUserID].Email, "reason": delegation.Reason, "revoked_at": nullableTimeAny(delegation.RevokedAt), "created_at": delegation.CreatedAt}
}

func (s *Server) canRevokeOperationApprovalDelegation(ctx context.Context, user *User, approval, delegation map[string]any) bool {
	if user == nil {
		return false
	}
	role := strings.ToLower(strings.TrimSpace(user.Role))
	if role == "admin" || role == "owner" {
		return true
	}
	if cleanOptionalID(fmt.Sprint(delegation["from_user_id"])) == user.ID {
		return true
	}
	return canDecideOperationApproval(user, approval)
}

func (s *Server) canDecideOperationApproval(ctx context.Context, user *User, approval map[string]any) bool {
	if canDecideOperationApproval(user, approval) {
		return true
	}
	if user == nil || s == nil || s.store == nil || s.store.Gorm == nil {
		return false
	}
	var count int64
	err := s.store.Gorm.WithContext(ctx).Model(&GormOperationApprovalDelegation{}).Where(map[string]any{
		"operation_approval_id": cleanOptionalID(fmt.Sprint(approval["id"])),
		"to_user_id":            user.ID,
		"revoked_at":            nil,
	}).Count(&count).Error
	return err == nil && count > 0
}

func canDecideOperationApproval(user *User, approval map[string]any) bool {
	if user == nil {
		return false
	}
	userRole := strings.ToLower(strings.TrimSpace(user.Role))
	roles := approvalRolesFromAny(approval["required_approver_roles"])
	if len(roles) == 0 {
		roles = []string{"admin", "owner"}
	}
	for _, role := range roles {
		if userRole == strings.ToLower(strings.TrimSpace(role)) {
			return true
		}
	}
	return false
}

func upsertOperationApprovalDecisionGorm(ctx context.Context, tx *gorm.DB, approvalID, userID, decision, reason string) error {
	if strings.TrimSpace(userID) == "" {
		return fmt.Errorf("approval decision requires a user")
	}
	if decision != "approved" && decision != "rejected" {
		return fmt.Errorf("approval decision is invalid")
	}
	row := GormOperationApprovalDecision{OperationApprovalID: approvalID, UserID: validNullString(userID), Decision: decision, Reason: reason, DecidedAt: time.Now().UTC()}
	return tx.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "operation_approval_id"}, {Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{"decision": decision, "reason": reason, "decided_at": row.DecidedAt}),
	}).Create(&row).Error
}

func operationApprovalApprovedCountGorm(ctx context.Context, db *gorm.DB, approvalID string) (int, error) {
	var count int64
	if err := db.WithContext(ctx).Model(&GormOperationApprovalDecision{}).Where(&GormOperationApprovalDecision{OperationApprovalID: approvalID, Decision: "approved"}).Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

func requiredApprovalCount(value any) int {
	count := intFromAny(value, 1)
	if count < 1 {
		return 1
	}
	return count
}

func (s *Server) requireProjectPolicyOrApproval(w http.ResponseWriter, r *http.Request, resource PolicyResource, action, title string, payload map[string]any) bool {
	if !s.requireProjectMembershipForPolicy(w, r, resource) {
		return false
	}
	return s.requirePolicyOrApproval(w, r, resource, action, title, payload)
}

func (s *Server) requirePolicyOrApproval(w http.ResponseWriter, r *http.Request, resource PolicyResource, action, title string, payload map[string]any) bool {
	decision := NewPolicyChecker().Check(currentUser(r), resource, action)
	switch decision.Effect {
	case PolicyAllow:
		return true
	case PolicyRequireConfirm:
		approval, err := s.createOperationApproval(r.Context(), resource, action, title, payload, currentUser(r).ID)
		if err != nil {
			if isUniqueViolation(err, "idx_operation_approvals_pending_once") {
				writeError(w, http.StatusConflict, "approval request is already pending")
				return false
			}
			writeError(w, http.StatusInternalServerError, "could not create approval request")
			return false
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"approval": approval, "decision": decision})
		return false
	default:
		writeJSON(w, http.StatusForbidden, decision)
		return false
	}
}

func (s *Server) requireProjectMembershipForPolicy(w http.ResponseWriter, r *http.Request, resource PolicyResource) bool {
	user := currentUser(r)
	if user != nil && resource.ProjectID != "" && resource.ProjectID != "<nil>" && user.Role != "admin" && user.Role != "owner" {
		var count int64
		if err := s.store.Gorm.WithContext(r.Context()).Model(&GormProjectMember{}).Where(&GormProjectMember{ProjectID: resource.ProjectID, UserID: user.ID}).Count(&count).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "could not check project membership")
			return false
		}
		if count == 0 {
			writeJSON(w, http.StatusForbidden, PolicyDecision{Effect: PolicyDeny, Reason: "user is not a member of this project"})
			return false
		}
	}
	return true
}

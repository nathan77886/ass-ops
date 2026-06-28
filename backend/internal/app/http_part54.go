package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"sort"
	"time"
)

func (s *Server) dueOperationApprovalEscalationsGorm(ctx context.Context) ([]map[string]any, error) {
	var items []map[string]any
	now := time.Now().UTC()
	err := s.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		approvals, counts, err := pendingOperationApprovalsWithDecisionCounts(ctx, tx)
		if err != nil {
			return err
		}
		due := make([]GormOperationApproval, 0, len(approvals))
		for _, approval := range approvals {
			escalation := mapFromAny(approval.EscalationPolicy.Data)
			afterMinutes := intFromAny(escalation["after_minutes"], 0)
			if afterMinutes <= 0 || counts[approval.ID] >= requiredApprovalCount(approval.RequiredApprovalCount) {
				continue
			}
			if approval.CreatedAt.After(now.Add(-time.Duration(afterMinutes) * time.Minute)) {
				continue
			}
			if approval.EscalatedAt.Valid && approval.EscalatedAt.Time.After(now.Add(-120*time.Minute)) {
				continue
			}
			due = append(due, approval)
		}
		sort.Slice(due, func(i, j int) bool { return due[i].CreatedAt.Before(due[j].CreatedAt) })
		if len(due) > 20 {
			due = due[:20]
		}
		items = make([]map[string]any, 0, len(due))
		for _, approval := range due {
			escalation := mapFromAny(approval.EscalationPolicy.Data)
			escalation["count"] = intFromAny(escalation["count"], 0) + 1
			approval.EscalationPolicy = JSONValue{Data: escalation}
			approval.EscalatedAt = validNullTime(now)
			if err := tx.WithContext(ctx).Save(&approval).Error; err != nil {
				return err
			}
			item := operationApprovalMap(approval, nil, nil, map[string]map[string]int{approval.ID: {"approved": counts[approval.ID]}}, nil)
			item["approved_count"] = counts[approval.ID]
			items = append(items, item)
		}
		return nil
	})
	return items, err
}

func pendingOperationApprovalsWithDecisionCounts(ctx context.Context, db *gorm.DB) ([]GormOperationApproval, map[string]int, error) {
	var approvals []GormOperationApproval
	if err := db.WithContext(ctx).Where(&GormOperationApproval{Status: "pending"}).Find(&approvals).Error; err != nil {
		return nil, nil, err
	}
	var decisions []GormOperationApprovalDecision
	if err := db.WithContext(ctx).Where(&GormOperationApprovalDecision{Decision: "approved"}).Find(&decisions).Error; err != nil {
		return nil, nil, err
	}
	approvalIDs := map[string]bool{}
	for _, approval := range approvals {
		approvalIDs[approval.ID] = true
	}
	counts := map[string]int{}
	for _, decision := range decisions {
		if approvalIDs[decision.OperationApprovalID] {
			counts[decision.OperationApprovalID]++
		}
	}
	return approvals, counts, nil
}

func operationApprovalDueReminderRank(approval GormOperationApproval, approved int, now time.Time) int {
	switch {
	case approval.NotificationStatus == "failed":
		return 0
	case approval.ExpiresAt.Valid && (approval.ExpiresAt.Time.Before(now.Add(15*time.Minute)) || approval.ExpiresAt.Time.Equal(now.Add(15*time.Minute))):
		return 1
	case approval.CreatedAt.Before(now.Add(-30*time.Minute)) && approved < requiredApprovalCount(approval.RequiredApprovalCount):
		return 2
	default:
		return 3
	}
}

func (s *Server) listOperationApprovalRules(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval_rule"}, "read") {
		return
	}
	var rules []GormOperationApprovalRule
	err := s.store.Gorm.WithContext(r.Context()).
		Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{
			{Column: clause.Column{Name: "enabled"}, Desc: true},
			{Column: clause.Column{Name: "priority"}},
			{Column: clause.Column{Name: "resource_type"}},
			{Column: clause.Column{Name: "action"}},
		}}).
		Find(&rules).Error
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	items := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		items = append(items, operationApprovalRuleMap(rule))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": enrichOperationApprovalRules(items)})
}

type operationApprovalRuleRequest struct {
	ResourceType           string         `json:"resource_type"`
	Action                 string         `json:"action"`
	RequiredApproverRoles  []string       `json:"required_approver_roles"`
	RequiredApprovalCount  int            `json:"required_approval_count"`
	ExpiresAfterMinutes    int            `json:"expires_after_minutes"`
	NotificationChannels   []string       `json:"notification_channels"`
	EscalationAfterMinutes int            `json:"escalation_after_minutes"`
	EscalationChannels     []string       `json:"escalation_channels"`
	Priority               int            `json:"priority"`
	Enabled                *bool          `json:"enabled"`
	Metadata               map[string]any `json:"metadata"`
}

func (s *Server) createOperationApprovalRule(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval_rule"}, "create") {
		return
	}
	req, ok := decodeOperationApprovalRuleRequest(w, r, true)
	if !ok {
		return
	}
	var item map[string]any
	err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		rule := operationApprovalRuleModelFromRequest(req)
		if err := tx.Create(&rule).Error; err != nil {
			return err
		}
		item = operationApprovalRuleMap(rule)
		if err := s.recordOperationApprovalRuleAuditGorm(r.Context(), tx, rule.ID, currentUser(r), "create", nil, item); err != nil {
			return fmt.Errorf("recording approval rule audit: %w", err)
		}
		if _, err := syncCanonicalAssetsGorm(r.Context(), tx); err != nil {
			return fmt.Errorf("syncing canonical assets for approval rule create: %w", err)
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not create approval rule")
		return
	}
	writeJSON(w, http.StatusCreated, enrichOperationApprovalRule(item))
}

func (s *Server) updateOperationApprovalRule(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval_rule", ID: chi.URLParam(r, "id")}, "update") {
		return
	}
	req, ok := decodeOperationApprovalRuleRequest(w, r, false)
	if !ok {
		return
	}
	var item map[string]any
	err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var rule GormOperationApprovalRule
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(&GormOperationApprovalRule{GormBase: GormBase{ID: chi.URLParam(r, "id")}}).First(&rule).Error; err != nil {
			return err
		}
		before := operationApprovalRuleMap(rule)
		applyOperationApprovalRuleRequest(&rule, req)
		if err := tx.Save(&rule).Error; err != nil {
			return err
		}
		item = operationApprovalRuleMap(rule)
		if err := s.recordOperationApprovalRuleAuditGorm(r.Context(), tx, rule.ID, currentUser(r), "update", before, item); err != nil {
			return fmt.Errorf("recording approval rule audit: %w", err)
		}
		if _, err := syncCanonicalAssetsGorm(r.Context(), tx); err != nil {
			return fmt.Errorf("syncing canonical assets for approval rule update: %w", err)
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update approval rule")
		return
	}
	writeJSON(w, http.StatusOK, enrichOperationApprovalRule(item))
}

func operationApprovalRuleModelFromRequest(req operationApprovalRuleRequest) GormOperationApprovalRule {
	rule := GormOperationApprovalRule{}
	applyOperationApprovalRuleRequest(&rule, req)
	return rule
}

func applyOperationApprovalRuleRequest(rule *GormOperationApprovalRule, req operationApprovalRuleRequest) {
	rule.ResourceType = req.ResourceType
	rule.Action = req.Action
	rule.RequiredApproverRoles = pq.StringArray(req.RequiredApproverRoles)
	rule.RequiredApprovalCount = req.RequiredApprovalCount
	rule.ExpiresAfterMinutes = req.ExpiresAfterMinutes
	rule.NotificationChannels = pq.StringArray(req.NotificationChannels)
	rule.EscalationPolicy = JSONValue{Data: map[string]any{
		"after_minutes": req.EscalationAfterMinutes,
		"channels":      req.EscalationChannels,
	}}
	rule.Priority = req.Priority
	rule.Enabled = *req.Enabled
	rule.Metadata = JSONValue{Data: nonNilMap(req.Metadata)}
}

func operationApprovalRuleMap(rule GormOperationApprovalRule) map[string]any {
	escalation := mapFromAny(rule.EscalationPolicy.Data)
	return map[string]any{
		"id":                       rule.ID,
		"resource_type":            rule.ResourceType,
		"action":                   rule.Action,
		"required_approver_roles":  []string(rule.RequiredApproverRoles),
		"required_approval_count":  rule.RequiredApprovalCount,
		"expires_after_minutes":    rule.ExpiresAfterMinutes,
		"notification_channels":    []string(rule.NotificationChannels),
		"escalation_after_minutes": intFromAny(escalation["after_minutes"], 0),
		"escalation_channels":      stringSliceFromAny(escalation["channels"]),
		"priority":                 rule.Priority,
		"enabled":                  rule.Enabled,
		"metadata":                 mapFromAny(rule.Metadata.Data),
		"created_at":               rule.CreatedAt,
		"updated_at":               rule.UpdatedAt,
	}
}

func enrichOperationApprovalRules(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, enrichOperationApprovalRule(item))
	}
	return out
}

func enrichOperationApprovalRule(item map[string]any) map[string]any {
	if item == nil {
		return nil
	}
	out := make(map[string]any, len(item)+2)
	for key, value := range item {
		out[key] = value
	}
	out["notification_destinations"] = approvalChannelDestinations(approvalRolesFromAny(item["notification_channels"]))
	out["escalation_destinations"] = approvalChannelDestinations(approvalRolesFromAny(item["escalation_channels"]))
	return out
}

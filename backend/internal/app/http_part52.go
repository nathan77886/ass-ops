package app

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

func queueByTool(counts map[string]int) []map[string]any {
	items := make([]map[string]any, 0, len(counts))
	for tool, count := range counts {
		items = append(items, map[string]any{"tool_name": tool, "queued": count})
	}
	sort.Slice(items, func(i, j int) bool {
		left := intFromAny(items[i]["queued"], 0)
		right := intFromAny(items[j]["queued"], 0)
		if left != right {
			return left > right
		}
		return fmt.Sprint(items[i]["tool_name"]) < fmt.Sprint(items[j]["tool_name"])
	})
	if len(items) > 8 {
		items = items[:8]
	}
	return items
}

func (s *Server) listOperationApprovals(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "read") {
		return
	}
	if err := s.expirePendingOperationApprovalsGorm(r.Context(), s.store.Gorm); err != nil {
		writeError(w, http.StatusInternalServerError, "could not expire approvals")
		return
	}
	user := currentUser(r)
	filters, err := operationApprovalFiltersFromRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.operationApprovalListGorm(r.Context(), user, filters)
	writeQueryResult(w, items, err)
}

func (s *Server) getOperationApprovalSummary(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "read") {
		return
	}
	if err := s.expirePendingOperationApprovalsGorm(r.Context(), s.store.Gorm); err != nil {
		writeError(w, http.StatusInternalServerError, "could not expire approvals")
		return
	}
	user := currentUser(r)
	summary, err := s.operationApprovalSummaryGorm(r.Context(), user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load approval summary")
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) listOperationApprovalReminderCandidates(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval"}, "read") {
		return
	}
	if err := s.expirePendingOperationApprovalsGorm(r.Context(), s.store.Gorm); err != nil {
		writeError(w, http.StatusInternalServerError, "could not expire approvals")
		return
	}
	user := currentUser(r)
	items, err := s.operationApprovalReminderCandidatesGorm(r.Context(), user)
	writeQueryResult(w, items, err)
}

func (s *Server) operationApprovalListGorm(ctx context.Context, user *User, filters operationApprovalFilters) ([]map[string]any, error) {
	approvals, err := s.visibleOperationApprovalsGorm(ctx, user)
	if err != nil {
		return nil, err
	}
	usersByID, projectsByID, decisions, delegated, err := s.operationApprovalAnnotationDataGorm(ctx, approvals, user)
	if err != nil {
		return nil, err
	}
	since, _ := time.Parse(time.RFC3339, filters.Since)
	until, _ := time.Parse(time.RFC3339, filters.Until)
	query := strings.ToLower(strings.TrimSpace(filters.Query))
	requestedBy := strings.ToLower(strings.TrimSpace(filters.RequestedBy))
	items := make([]map[string]any, 0, len(approvals))
	for _, approval := range approvals {
		requesterEmail := strings.ToLower(usersByID[cleanOptionalID(approval.RequestedBy.String)].Email)
		if filters.Status != "" && approval.Status != filters.Status {
			continue
		}
		if filters.Action != "" && approval.Action != filters.Action {
			continue
		}
		if filters.ResourceType != "" && approval.ResourceType != filters.ResourceType {
			continue
		}
		if query != "" && !strings.Contains(requesterEmail, query) && !strings.Contains(strings.ToLower(approval.Title), query) && !strings.Contains(strings.ToLower(approval.ResourceID), query) {
			continue
		}
		if requestedBy != "" && !strings.Contains(requesterEmail, requestedBy) {
			continue
		}
		if !since.IsZero() && approval.CreatedAt.Before(since) {
			continue
		}
		if !until.IsZero() && approval.CreatedAt.After(until) {
			continue
		}
		item := operationApprovalMap(approval, usersByID, projectsByID, decisions, delegated)
		items = append(items, item)
		if len(items) >= 100 {
			break
		}
	}
	return items, nil
}

func (s *Server) operationApprovalSummaryGorm(ctx context.Context, user *User) (map[string]any, error) {
	approvals, err := s.visibleOperationApprovalsGorm(ctx, user)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	byStatus := map[string]int{}
	byAction := map[string]int{}
	summary := map[string]any{"total": len(approvals), "pending": 0, "approved": 0, "rejected": 0, "expired": 0, "expiring_soon": 0, "notification_failed": 0}
	for _, approval := range approvals {
		byStatus[approval.Status]++
		byAction[approval.Action]++
		switch approval.Status {
		case "pending":
			summary["pending"] = intFromAny(summary["pending"], 0) + 1
			if approval.ExpiresAt.Valid && !approval.ExpiresAt.Time.After(now.Add(time.Hour)) {
				summary["expiring_soon"] = intFromAny(summary["expiring_soon"], 0) + 1
			}
		case "approved":
			summary["approved"] = intFromAny(summary["approved"], 0) + 1
		case "rejected":
			summary["rejected"] = intFromAny(summary["rejected"], 0) + 1
		case "expired":
			summary["expired"] = intFromAny(summary["expired"], 0) + 1
		}
		if approval.NotificationStatus == "failed" {
			summary["notification_failed"] = intFromAny(summary["notification_failed"], 0) + 1
		}
	}
	summary["by_status"] = byStatus
	summary["by_action"] = approvalActionCounts(byAction)
	return summary, nil
}

func (s *Server) operationApprovalReminderCandidatesGorm(ctx context.Context, user *User) ([]map[string]any, error) {
	approvals, err := s.visibleOperationApprovalsGorm(ctx, user)
	if err != nil {
		return nil, err
	}
	usersByID, projectsByID, decisions, delegated, err := s.operationApprovalAnnotationDataGorm(ctx, approvals, user)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	items := []map[string]any{}
	for _, approval := range approvals {
		if approval.Status != "pending" {
			continue
		}
		approvedCount := decisions[approval.ID]["approved"]
		ageMinutes := int(now.Sub(approval.CreatedAt).Minutes())
		minutesUntilExpiry := any(nil)
		if approval.ExpiresAt.Valid {
			minutesUntilExpiry = int(approval.ExpiresAt.Time.Sub(now).Minutes())
		}
		escalationMinutes := intFromAny(mapFromAny(approval.EscalationPolicy.Data)["after_minutes"], 0)
		reminderReason, level := operationApprovalReminderReason(approval, approvedCount, ageMinutes, minutesUntilExpiry, escalationMinutes)
		if reminderReason == "watch" {
			continue
		}
		item := operationApprovalMap(approval, usersByID, projectsByID, decisions, delegated)
		item["approved_count"] = approvedCount
		item["last_reminded_at"] = nullableTimeAny(approval.LastReminderAt)
		item["reminder_count"] = approval.ReminderCount
		item["escalation_after_minutes"] = escalationMinutes
		item["escalation_channels"] = stringSliceFromAny(mapFromAny(approval.EscalationPolicy.Data)["channels"])
		item["last_escalated_at"] = nullableTimeAny(approval.EscalatedAt)
		item["escalation_count"] = intFromAny(mapFromAny(approval.EscalationPolicy.Data)["count"], 0)
		item["age_minutes"] = ageMinutes
		item["minutes_until_expiry"] = minutesUntilExpiry
		item["reminder_reason"] = reminderReason
		item["escalation_level"] = level
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return operationApprovalReminderRank(items[i]) < operationApprovalReminderRank(items[j])
	})
	if len(items) > 50 {
		items = items[:50]
	}
	return items, nil
}

func (s *Server) visibleOperationApprovalsGorm(ctx context.Context, user *User) ([]GormOperationApproval, error) {
	var approvals []GormOperationApproval
	if err := s.store.Gorm.WithContext(ctx).Order(gormOrderDesc("created_at")).Find(&approvals).Error; err != nil {
		return nil, err
	}
	if userCanReadAllProjects(user) {
		return approvals, nil
	}
	allowed, err := s.projectMembershipSetGorm(ctx, user)
	if err != nil {
		return nil, err
	}
	filtered := approvals[:0]
	for _, approval := range approvals {
		projectID := cleanOptionalID(approval.ProjectID.String)
		if projectID == "" || allowed[projectID] {
			filtered = append(filtered, approval)
		}
	}
	return filtered, nil
}

package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"sort"
	"time"
)

func (s *Server) operationApprovalAnnotationDataGorm(ctx context.Context, approvals []GormOperationApproval, user *User) (map[string]GormUser, map[string]GormProject, map[string]map[string]int, map[string]bool, error) {
	userIDs := []string{}
	projectIDs := []string{}
	approvalIDs := []string{}
	seenUsers := map[string]bool{}
	seenProjects := map[string]bool{}
	for _, approval := range approvals {
		approvalIDs = append(approvalIDs, approval.ID)
		for _, id := range []string{cleanOptionalID(approval.RequestedBy.String), cleanOptionalID(approval.DecidedBy.String)} {
			if id != "" && !seenUsers[id] {
				seenUsers[id] = true
				userIDs = append(userIDs, id)
			}
		}
		if id := cleanOptionalID(approval.ProjectID.String); id != "" && !seenProjects[id] {
			seenProjects[id] = true
			projectIDs = append(projectIDs, id)
		}
	}
	usersByID := map[string]GormUser{}
	if len(userIDs) > 0 {
		var users []GormUser
		if err := s.store.Gorm.WithContext(ctx).Where(gormField("id", userIDs)).Find(&users).Error; err != nil {
			return nil, nil, nil, nil, err
		}
		for _, item := range users {
			usersByID[item.ID] = item
		}
	}
	projectsByID := map[string]GormProject{}
	if len(projectIDs) > 0 {
		var projects []GormProject
		if err := s.store.Gorm.WithContext(ctx).Where(gormField("id", projectIDs)).Find(&projects).Error; err != nil {
			return nil, nil, nil, nil, err
		}
		for _, item := range projects {
			projectsByID[item.ID] = item
		}
	}
	decisions := map[string]map[string]int{}
	if len(approvalIDs) > 0 {
		var rows []GormOperationApprovalDecision
		if err := s.store.Gorm.WithContext(ctx).Where(gormField("operation_approval_id", approvalIDs)).Find(&rows).Error; err != nil {
			return nil, nil, nil, nil, err
		}
		for _, row := range rows {
			if decisions[row.OperationApprovalID] == nil {
				decisions[row.OperationApprovalID] = map[string]int{}
			}
			decisions[row.OperationApprovalID][row.Decision]++
		}
	}
	delegated := map[string]bool{}
	if user != nil && cleanOptionalID(user.ID) != "" && len(approvalIDs) > 0 {
		var delegations []GormOperationApprovalDelegation
		if err := s.store.Gorm.WithContext(ctx).Where(gormField("operation_approval_id", approvalIDs)).Where(&GormOperationApprovalDelegation{ToUserID: user.ID}).Find(&delegations).Error; err != nil {
			return nil, nil, nil, nil, err
		}
		for _, delegation := range delegations {
			delegated[delegation.OperationApprovalID] = true
		}
	}
	return usersByID, projectsByID, decisions, delegated, nil
}

func operationApprovalMap(approval GormOperationApproval, users map[string]GormUser, projects map[string]GormProject, decisions map[string]map[string]int, delegated map[string]bool) map[string]any {
	escalation := mapFromAny(approval.EscalationPolicy.Data)
	return map[string]any{
		"id":                       approval.ID,
		"project_id":               nullableStringValue(approval.ProjectID),
		"operation_run_id":         nullableStringValue(approval.OperationRunID),
		"approval_rule_id":         nullableStringValue(approval.ApprovalRuleID),
		"resource_type":            approval.ResourceType,
		"resource_id":              approval.ResourceID,
		"action":                   approval.Action,
		"title":                    approval.Title,
		"status":                   approval.Status,
		"required_approver_roles":  []string(approval.RequiredApproverRoles),
		"required_approval_count":  approval.RequiredApprovalCount,
		"notification_channels":    []string(approval.NotificationChannels),
		"escalation_after_minutes": intFromAny(escalation["after_minutes"], 0),
		"escalation_channels":      stringSliceFromAny(escalation["channels"]),
		"last_escalated_at":        nullableTimeAny(approval.EscalatedAt),
		"escalation_count":         intFromAny(escalation["count"], 0),
		"notification_status":      approval.NotificationStatus,
		"notification_last_error":  approval.NotificationLastError,
		"requested_by":             nullableStringValue(approval.RequestedBy),
		"decided_by":               nullableStringValue(approval.DecidedBy),
		"decision_reason":          approval.DecisionReason,
		"decided_at":               nullableTimeAny(approval.DecidedAt),
		"expires_at":               nullableTimeAny(approval.ExpiresAt),
		"expired_at":               nullableTimeAny(approval.ExpiredAt),
		"created_at":               approval.CreatedAt,
		"updated_at":               approval.UpdatedAt,
		"requested_by_email":       users[cleanOptionalID(approval.RequestedBy.String)].Email,
		"decided_by_email":         users[cleanOptionalID(approval.DecidedBy.String)].Email,
		"project_name":             projects[cleanOptionalID(approval.ProjectID.String)].Name,
		"approved_count":           decisions[approval.ID]["approved"],
		"rejected_count":           decisions[approval.ID]["rejected"],
		"can_current_user_decide":  delegated[approval.ID],
	}
}

func approvalActionCounts(counts map[string]int) []map[string]any {
	items := make([]map[string]any, 0, len(counts))
	for action, count := range counts {
		items = append(items, map[string]any{"action": action, "count": count})
	}
	sort.Slice(items, func(i, j int) bool {
		left := intFromAny(items[i]["count"], 0)
		right := intFromAny(items[j]["count"], 0)
		if left != right {
			return left > right
		}
		return fmt.Sprint(items[i]["action"]) < fmt.Sprint(items[j]["action"])
	})
	if len(items) > 8 {
		items = items[:8]
	}
	return items
}

func operationApprovalReminderReason(approval GormOperationApproval, approvedCount, ageMinutes int, minutesUntilExpiry any, escalationMinutes int) (string, string) {
	expiryMinutes, hasExpiry := minutesUntilExpiry.(int)
	switch {
	case approval.NotificationStatus == "failed":
		return "notification_failed", "danger"
	case escalationMinutes > 0 && ageMinutes >= escalationMinutes && approvedCount < approval.RequiredApprovalCount:
		return "escalation_due", "danger"
	case hasExpiry && expiryMinutes <= 15:
		return "expires_soon", "danger"
	case ageMinutes >= 30 && approvedCount < approval.RequiredApprovalCount:
		return "waiting_for_approvers", "warning"
	case hasExpiry && expiryMinutes <= 60:
		return "approaching_expiry", "warning"
	default:
		return "watch", "info"
	}
}

func operationApprovalReminderRank(item map[string]any) int {
	switch fmt.Sprint(item["reminder_reason"]) {
	case "notification_failed":
		return 0
	case "escalation_due":
		return 1
	case "expires_soon":
		return 2
	case "waiting_for_approvers":
		return 3
	default:
		return 4
	}
}

func (s *Server) dispatchDueOperationApprovalReminders(ctx context.Context) error {
	items, err := s.dueOperationApprovalRemindersGorm(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		item["required_approval_count"] = requiredApprovalCount(item["required_approval_count"])
		delete(item, "request_payload")
		s.dispatchApprovalNotification(ctx, item, "reminder")
	}
	return nil
}

func (s *Server) dispatchDueOperationApprovalEscalations(ctx context.Context) error {
	items, err := s.dueOperationApprovalEscalationsGorm(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		item["required_approval_count"] = requiredApprovalCount(item["required_approval_count"])
		delete(item, "request_payload")
		s.dispatchApprovalNotification(ctx, item, "escalation")
	}
	return nil
}

func (s *Server) dueOperationApprovalRemindersGorm(ctx context.Context) ([]map[string]any, error) {
	var items []map[string]any
	now := time.Now().UTC()
	err := s.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		approvals, counts, err := pendingOperationApprovalsWithDecisionCounts(ctx, tx)
		if err != nil {
			return err
		}
		due := make([]GormOperationApproval, 0, len(approvals))
		for _, approval := range approvals {
			approved := counts[approval.ID]
			if !approval.LastReminderAt.Valid || approval.LastReminderAt.Time.Before(now.Add(-60*time.Minute)) || approval.LastReminderAt.Time.Equal(now.Add(-60*time.Minute)) {
				createdOld := approval.CreatedAt.Before(now.Add(-30*time.Minute)) || approval.CreatedAt.Equal(now.Add(-30*time.Minute))
				expiresSoon := approval.ExpiresAt.Valid && (approval.ExpiresAt.Time.Before(now.Add(time.Hour)) || approval.ExpiresAt.Time.Equal(now.Add(time.Hour)))
				needsApprover := createdOld && approved < requiredApprovalCount(approval.RequiredApprovalCount)
				if approval.NotificationStatus == "failed" || expiresSoon || needsApprover {
					due = append(due, approval)
				}
			}
		}
		sort.Slice(due, func(i, j int) bool {
			leftRank := operationApprovalDueReminderRank(due[i], counts[due[i].ID], now)
			rightRank := operationApprovalDueReminderRank(due[j], counts[due[j].ID], now)
			if leftRank != rightRank {
				return leftRank < rightRank
			}
			if due[i].ExpiresAt.Valid != due[j].ExpiresAt.Valid {
				return due[i].ExpiresAt.Valid
			}
			if due[i].ExpiresAt.Valid && !due[i].ExpiresAt.Time.Equal(due[j].ExpiresAt.Time) {
				return due[i].ExpiresAt.Time.Before(due[j].ExpiresAt.Time)
			}
			return due[i].CreatedAt.Before(due[j].CreatedAt)
		})
		if len(due) > 20 {
			due = due[:20]
		}
		items = make([]map[string]any, 0, len(due))
		for _, approval := range due {
			approval.LastReminderAt = validNullTime(now)
			approval.ReminderCount++
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

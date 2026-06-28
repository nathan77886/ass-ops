package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/lib/pq"
	"gorm.io/gorm"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

func (s *Server) createOperationApproval(ctx context.Context, resource PolicyResource, action, title string, payload map[string]any, requestedBy string) (map[string]any, error) {
	rule, err := s.operationApprovalRule(ctx, s.store.Gorm, resource, action)
	if err != nil {
		return nil, err
	}
	expiresAfter := rule.ExpiresAfterMinutes
	if expiresAfter <= 0 {
		expiresAfter = 1440
	}
	approval := GormOperationApproval{
		ProjectID:             validNullString(cleanOptionalID(resource.ProjectID)),
		ApprovalRuleID:        validNullString(rule.ID),
		ResourceType:          resource.Type,
		ResourceID:            resource.ID,
		Action:                action,
		Title:                 title,
		RequestPayload:        JSONValue{Data: nonNilMap(payload)},
		RequiredApproverRoles: pq.StringArray(rule.RequiredApproverRoles),
		RequiredApprovalCount: rule.RequiredApprovalCount,
		NotificationChannels:  pq.StringArray(rule.NotificationChannels),
		EscalationPolicy: JSONValue{Data: map[string]any{
			"after_minutes": rule.EscalationAfterMinutes,
			"channels":      rule.EscalationChannels,
			"count":         0,
		}},
		ExpiresAt:   validNullTime(time.Now().Add(time.Duration(expiresAfter) * time.Minute)),
		RequestedBy: validNullString(requestedBy),
	}
	err = s.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&approval).Error; err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for operation approval create: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.dispatchApprovalNotification(ctx, operationApprovalMap(approval, nil, nil, nil, nil), "pending"), nil
}

type operationApprovalRule struct {
	ID                     string
	RequiredApproverRoles  []string
	RequiredApprovalCount  int
	ExpiresAfterMinutes    int
	NotificationChannels   []string
	EscalationAfterMinutes int
	EscalationChannels     []string
}

func defaultOperationApprovalRule() operationApprovalRule {
	return operationApprovalRule{
		RequiredApproverRoles:  []string{"admin", "owner"},
		RequiredApprovalCount:  1,
		ExpiresAfterMinutes:    1440,
		NotificationChannels:   []string{"ui"},
		EscalationAfterMinutes: 0,
		EscalationChannels:     []string{},
	}
}

func (s *Server) operationApprovalRule(ctx context.Context, db *gorm.DB, resource PolicyResource, action string) (operationApprovalRule, error) {
	rule := defaultOperationApprovalRule()
	var candidates []GormOperationApprovalRule
	if err := db.WithContext(ctx).Where(&GormOperationApprovalRule{Enabled: true}).Find(&candidates).Error; err != nil {
		return rule, err
	}
	matched := make([]GormOperationApprovalRule, 0, len(candidates))
	for _, candidate := range candidates {
		if (candidate.Action == action || candidate.Action == "*") && (candidate.ResourceType == resource.Type || candidate.ResourceType == "") {
			matched = append(matched, candidate)
		}
	}
	if len(matched) == 0 {
		return rule, nil
	}
	sort.Slice(matched, func(i, j int) bool {
		left := matched[i]
		right := matched[j]
		if left.Priority != right.Priority {
			return left.Priority < right.Priority
		}
		leftScore := approvalRuleSpecificity(left, resource.Type, action)
		rightScore := approvalRuleSpecificity(right, resource.Type, action)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if left.ResourceType != right.ResourceType {
			return left.ResourceType == resource.Type
		}
		if left.Action != right.Action {
			return left.Action == action
		}
		return left.UpdatedAt.After(right.UpdatedAt)
	})
	selected := matched[0]
	rule.ID = selected.ID
	if roles := approvalRolesFromAny([]string(selected.RequiredApproverRoles)); len(roles) > 0 {
		rule.RequiredApproverRoles = roles
	}
	rule.RequiredApprovalCount = requiredApprovalCount(selected.RequiredApprovalCount)
	rule.ExpiresAfterMinutes = selected.ExpiresAfterMinutes
	if channels := approvalRolesFromAny([]string(selected.NotificationChannels)); len(channels) > 0 {
		rule.NotificationChannels = channels
	}
	escalation := mapFromAny(selected.EscalationPolicy.Data)
	rule.EscalationAfterMinutes = intFromAny(escalation["after_minutes"], 0)
	if rule.EscalationAfterMinutes < 0 {
		rule.EscalationAfterMinutes = 0
	}
	rule.EscalationChannels = approvalRolesFromAny(stringSliceFromAny(escalation["channels"]))
	return rule, nil
}

func approvalRuleSpecificity(rule GormOperationApprovalRule, resourceType, action string) int {
	score := 0
	if rule.ResourceType == resourceType {
		score++
	}
	if rule.Action == action {
		score++
	}
	return score
}

func (s *Server) expirePendingOperationApprovalsGorm(ctx context.Context, db *gorm.DB) error {
	var approvals []GormOperationApproval
	if err := db.WithContext(ctx).Where(&GormOperationApproval{Status: "pending"}).Find(&approvals).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	expired := make([]map[string]any, 0)
	for _, approval := range approvals {
		if !approval.ExpiresAt.Valid || approval.ExpiresAt.Time.After(now) {
			continue
		}
		approval.Status = "expired"
		approval.ExpiredAt = validNullTime(now)
		if strings.TrimSpace(approval.DecisionReason) == "" {
			approval.DecisionReason = "approval expired"
		}
		if err := db.WithContext(ctx).Save(&approval).Error; err != nil {
			return err
		}
		expired = append(expired, operationApprovalMap(approval, nil, nil, nil, nil))
	}
	if len(expired) > 0 {
		if _, err := syncCanonicalAssetsGorm(ctx, db); err != nil {
			return fmt.Errorf("syncing canonical assets for expired operation approvals: %w", err)
		}
	}
	for _, item := range expired {
		s.dispatchApprovalNotification(ctx, item, "expired")
	}
	return nil
}

func (s *Server) expireOperationApprovalByIDGorm(ctx context.Context, db *gorm.DB, approvalID string) (map[string]any, error) {
	var approval GormOperationApproval
	err := db.WithContext(ctx).First(&approval, &GormOperationApproval{GormBase: GormBase{ID: approvalID}}).Error
	if errorsIsRecordNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if approval.Status != "pending" || !approval.ExpiresAt.Valid || approval.ExpiresAt.Time.After(now) {
		return nil, nil
	}
	approval.Status = "expired"
	approval.ExpiredAt = validNullTime(now)
	if strings.TrimSpace(approval.DecisionReason) == "" {
		approval.DecisionReason = "approval expired"
	}
	if err := db.WithContext(ctx).Save(&approval).Error; err != nil {
		return nil, err
	}
	return operationApprovalMap(approval, nil, nil, nil, nil), nil
}

func (s *Server) dispatchApprovalNotification(ctx context.Context, approval map[string]any, event string) map[string]any {
	if approval == nil {
		return nil
	}
	status, lastError := s.approvalNotificationStatus(ctx, approval, event)
	approvalID := cleanOptionalID(fmt.Sprint(approval["id"]))
	var updatedApproval GormOperationApproval
	err := s.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where(&GormOperationApproval{GormBase: GormBase{ID: approvalID}}).First(&updatedApproval).Error; err != nil {
			return err
		}
		updatedApproval.NotificationStatus = status
		updatedApproval.NotificationLastError = lastError
		return tx.Save(&updatedApproval).Error
	})
	if err != nil {
		return approval
	}
	if s.store != nil && s.store.Gorm != nil {
		// Notification dispatch happens after the approval transaction commits, so this
		// refresh is intentionally best-effort; the next canonical sync will repair it.
		if _, syncErr := syncCanonicalAssetsGorm(ctx, s.store.Gorm); syncErr != nil {
			s.log.Debug("could not sync canonical assets after approval notification", "error", syncErr)
		}
	}
	return operationApprovalMap(updatedApproval, nil, nil, nil, nil)
}

func (s *Server) approvalNotificationStatus(ctx context.Context, approval map[string]any, event string) (string, string) {
	if strings.TrimSpace(s.cfg.ApprovalWebhookURL) == "" {
		return "delivered", ""
	}
	if err := s.postApprovalWebhook(ctx, approval, event); err != nil {
		return "failed", truncateText(err.Error(), 500)
	}
	return "delivered", ""
}

func (s *Server) postApprovalWebhook(ctx context.Context, approval map[string]any, event string) error {
	endpoint := strings.TrimSpace(s.cfg.ApprovalWebhookURL)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("parsing approval webhook url: %w", err)
	}
	if parsed.Host == "" {
		return fmt.Errorf("approval webhook url must include a host")
	}
	if !validPublicHTTPURL(ctx, endpoint) {
		return fmt.Errorf("approval webhook url must be a public http or https URL")
	}
	payload := approvalWebhookPayload(approval, event)
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling approval webhook payload: %w", err)
	}
	notifyCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(notifyCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating approval webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(s.cfg.ApprovalWebhookToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := approvalWebhookHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("posting approval webhook: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("approval webhook returned status %d", resp.StatusCode)
	}
	return nil
}

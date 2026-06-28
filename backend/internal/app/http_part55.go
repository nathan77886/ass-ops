package app

import (
	"context"
	"database/sql"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"strings"
)

func approvalChannelDestinations(channels []string) []map[string]any {
	destinations := make([]map[string]any, 0, len(channels))
	for _, channel := range channels {
		raw := strings.ToLower(strings.TrimSpace(channel))
		if raw == "" {
			continue
		}
		kind, target, _ := strings.Cut(raw, ":")
		if target == "" {
			kind = raw
		}
		known := approvalDestinationKnownKind(kind)
		exposedTarget := target
		if !known {
			exposedTarget = ""
		}
		readiness := approvalDestinationAdapterReadiness(kind, target)
		destination := map[string]any{
			"channel":      raw,
			"kind":         kind,
			"target":       exposedTarget,
			"label":        approvalDestinationLabel(kind, exposedTarget),
			"needs_config": approvalDestinationNeedsConfig(kind, target),
		}
		for key, value := range readiness {
			destination[key] = value
		}
		destinations = append(destinations, destination)
	}
	return destinations
}

func approvalDestinationKnownKind(kind string) bool {
	switch kind {
	case "ui", "webhook", "email", "slack", "pagerduty":
		return true
	default:
		return false
	}
}

func approvalDestinationLabel(kind, target string) string {
	switch kind {
	case "ui":
		return "Operations UI"
	case "webhook":
		if target != "" {
			return "Approval webhook: " + target
		}
		return "Approval webhook"
	case "email":
		if target != "" {
			return "Email: " + target
		}
		return "Email"
	case "slack":
		if target != "" {
			return "Slack: " + target
		}
		return "Slack"
	case "pagerduty":
		if target != "" {
			return "PagerDuty: " + target
		}
		return "PagerDuty"
	default:
		return "Unknown channel: " + kind
	}
}

func approvalDestinationAdapterReadiness(kind, target string) map[string]any {
	switch kind {
	case "ui":
		return map[string]any{
			"adapter":                "operations_ui",
			"adapter_status":         "enabled",
			"delivery_mode":          "in_app",
			"requires_external_call": false,
			"blocked_reason":         "",
			"configuration_scope":    "built_in",
		}
	case "webhook":
		return map[string]any{
			"adapter":                "approval_webhook",
			"adapter_status":         "environment_backed",
			"delivery_mode":          "http_post",
			"requires_external_call": true,
			"blocked_reason":         "",
			"configuration_scope":    "ASSOPS_APPROVAL_WEBHOOK_URL",
		}
	case "email", "slack", "pagerduty":
		return map[string]any{
			"adapter":                kind,
			"adapter_status":         "planned",
			"delivery_mode":          "preview_only",
			"requires_external_call": true,
			"blocked_reason":         "adapter delivery is not implemented yet",
			"configuration_scope":    "future_connector",
		}
	default:
		return map[string]any{
			"adapter":                "custom",
			"adapter_status":         "unknown",
			"delivery_mode":          "preview_only",
			"requires_external_call": target != "",
			"blocked_reason":         "unknown approval destination adapter",
			"configuration_scope":    "custom",
			"redacted_target":        target != "",
		}
	}
}

func approvalDestinationNeedsConfig(kind, target string) bool {
	switch kind {
	case "ui":
		return false
	case "webhook":
		return false
	case "email", "slack", "pagerduty":
		return true
	default:
		return true
	}
}

func (s *Server) listOperationApprovalRuleAudits(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "operation_approval_rule", ID: chi.URLParam(r, "id")}, "read") {
		return
	}
	var audits []GormOperationApprovalRuleAudit
	if err := s.store.Gorm.WithContext(r.Context()).
		Where(&GormOperationApprovalRuleAudit{OperationApprovalRuleID: validNullString(chi.URLParam(r, "id"))}).
		Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "created_at"}, Desc: true}}}).
		Limit(100).
		Find(&audits).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	usersByID, err := operationApprovalRuleAuditUsers(r.Context(), s.store.Gorm, audits)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	items := make([]map[string]any, 0, len(audits))
	for _, audit := range audits {
		items = append(items, operationApprovalRuleAuditMap(audit, usersByID))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func operationApprovalRuleAuditUsers(ctx context.Context, db *gorm.DB, audits []GormOperationApprovalRuleAudit) (map[string]GormUser, error) {
	ids := make([]string, 0, len(audits))
	seen := map[string]bool{}
	for _, audit := range audits {
		id := cleanOptionalID(audit.ActorUserID.String)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return map[string]GormUser{}, nil
	}
	var users []GormUser
	if err := db.WithContext(ctx).Find(&users, ids).Error; err != nil {
		return nil, err
	}
	usersByID := make(map[string]GormUser, len(users))
	for _, user := range users {
		usersByID[user.ID] = user
	}
	return usersByID, nil
}

func operationApprovalRuleAuditMap(audit GormOperationApprovalRuleAudit, users map[string]GormUser) map[string]any {
	actorID := cleanOptionalID(audit.ActorUserID.String)
	return map[string]any{
		"id":                         audit.ID,
		"operation_approval_rule_id": nullableStringValue(audit.OperationApprovalRuleID),
		"actor_user_id":              nullableStringValue(audit.ActorUserID),
		"actor_email":                users[actorID].Email,
		"action":                     audit.Action,
		"before_state":               mapFromAny(audit.Before.Data),
		"after_state":                mapFromAny(audit.After.Data),
		"created_at":                 audit.CreatedAt,
	}
}

func (s *Server) recordOperationApprovalRuleAuditGorm(ctx context.Context, db *gorm.DB, ruleID string, actor *User, action string, before, after map[string]any) error {
	actorID := sql.NullString{}
	if actor != nil {
		actorID = validNullString(actor.ID)
	}
	audit := GormOperationApprovalRuleAudit{
		OperationApprovalRuleID: validNullString(ruleID),
		ActorUserID:             actorID,
		Action:                  action,
		Before:                  JSONValue{Data: nonNilMap(before)},
		After:                   JSONValue{Data: nonNilMap(after)},
	}
	return db.WithContext(ctx).Create(&audit).Error
}

func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func decodeOperationApprovalRuleRequest(w http.ResponseWriter, r *http.Request, create bool) (operationApprovalRuleRequest, bool) {
	var req operationApprovalRuleRequest
	if !decodeJSON(w, r, &req) {
		return req, false
	}
	req.ResourceType = strings.TrimSpace(req.ResourceType)
	req.Action = strings.TrimSpace(req.Action)
	if req.Action == "" {
		writeError(w, http.StatusBadRequest, "action is required")
		return req, false
	}
	if len(req.Action) > 80 || len(req.ResourceType) > 80 {
		writeError(w, http.StatusBadRequest, "rule key is too long")
		return req, false
	}
	req.RequiredApproverRoles = normalizeRuleStringList(req.RequiredApproverRoles, []string{"admin", "owner"})
	req.NotificationChannels = normalizeRuleStringList(req.NotificationChannels, []string{"ui"})
	req.EscalationChannels = normalizeRuleStringList(req.EscalationChannels, nil)
	if req.RequiredApprovalCount < 1 {
		req.RequiredApprovalCount = 1
	}
	if req.RequiredApprovalCount > len(req.RequiredApproverRoles) {
		writeError(w, http.StatusBadRequest, "required_approval_count cannot exceed approver role count")
		return req, false
	}
	if req.ExpiresAfterMinutes <= 0 {
		req.ExpiresAfterMinutes = 1440
	}
	if req.ExpiresAfterMinutes > 43200 {
		writeError(w, http.StatusBadRequest, "expires_after_minutes must be <= 43200")
		return req, false
	}
	if req.EscalationAfterMinutes < 0 {
		writeError(w, http.StatusBadRequest, "escalation_after_minutes must be >= 0")
		return req, false
	}
	if req.EscalationAfterMinutes > 43200 {
		writeError(w, http.StatusBadRequest, "escalation_after_minutes must be <= 43200")
		return req, false
	}
	if req.Priority == 0 && create {
		req.Priority = 100
	}
	if req.Enabled == nil {
		enabled := true
		req.Enabled = &enabled
	}
	if req.Metadata == nil {
		req.Metadata = map[string]any{}
	}
	return req, true
}

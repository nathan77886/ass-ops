package app

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lib/pq"
	"strings"
)

func approvalWebhookPayload(approval map[string]any, event string) map[string]any {
	// Approval notifications intentionally use an allowlist so external
	// destinations never receive raw request payloads, secrets, or rule metadata.
	return map[string]any{
		"event": event,
		"approval": map[string]any{
			"id":               approval["id"],
			"project_id":       approval["project_id"],
			"operation_run_id": approval["operation_run_id"],
			"resource_type":    approval["resource_type"],
			"resource_id":      approval["resource_id"],
			"action":           approval["action"],
			"title":            approval["title"],
			"status":           approval["status"],
			"approved_count":   approval["approved_count"],
			"rejected_count":   approval["rejected_count"],
			"requested_by":     approval["requested_by"],
			"decided_by":       approval["decided_by"],
			"expires_at":       approval["expires_at"],
			"expired_at":       approval["expired_at"],
			"created_at":       approval["created_at"],
			"updated_at":       approval["updated_at"],
		},
	}
}

func truncateText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
}

func approvalRolesFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return cleanStringList(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, fmt.Sprint(item))
		}
		return cleanStringList(out)
	case string:
		text := strings.TrimSpace(typed)
		if text == "" || text == "<nil>" {
			return nil
		}
		if strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}") {
			return parsePostgresTextArray(text)
		}
		var parsed []string
		if json.Unmarshal([]byte(text), &parsed) == nil {
			return cleanStringList(parsed)
		}
		return cleanStringList([]string{text})
	default:
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" || text == "<nil>" {
			return nil
		}
		return cleanStringList([]string{text})
	}
}

func parsePostgresTextArray(value string) []string {
	text := strings.TrimSpace(value)
	if !strings.HasPrefix(text, "{") || !strings.HasSuffix(text, "}") {
		return cleanStringList([]string{text})
	}
	text = strings.TrimSuffix(strings.TrimPrefix(text, "{"), "}")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	reader := csv.NewReader(strings.NewReader(text))
	reader.TrimLeadingSpace = true
	reader.LazyQuotes = true
	items, err := reader.Read()
	if err != nil {
		return cleanStringList(strings.Split(text, ","))
	}
	for i, item := range items {
		items[i] = strings.ReplaceAll(item, `\"`, `"`)
	}
	return cleanStringList(items)
}

func cleanStringList(items []string) []string {
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		item = strings.ToLower(strings.Trim(item, ` "'`))
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func isUniqueViolation(err error, constraint string) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	return string(pqErr.Code) == "23505" && (constraint == "" || pqErr.Constraint == constraint)
}

func cleanOptionalID(value string) string {
	value = strings.TrimSpace(value)
	if value == "<nil>" {
		return ""
	}
	return value
}

func nullableIDArg(value string) any {
	value = cleanOptionalID(value)
	if value == "" {
		return nil
	}
	return value
}

func cleanOptionalText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "<nil>" {
		return ""
	}
	return value
}

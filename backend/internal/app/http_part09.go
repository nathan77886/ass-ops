package app

import (
	"fmt"
	"math"
	"strings"
	"time"
)

func providerAccountTokenRotationPlanSummary(items []map[string]any, now time.Time) map[string]any {
	counts := map[string]int{
		"fresh":   0,
		"soon":    0,
		"due":     0,
		"missing": 0,
		"unknown": 0,
	}
	nextAction := "No provider accounts configured."
	for _, item := range items {
		status := providerAccountTokenRotationStatus(item, now)
		key := strings.TrimSpace(fmt.Sprint(status["status"]))
		if _, ok := counts[key]; !ok {
			key = "unknown"
		}
		counts[key]++
	}
	actionRequired := counts["due"] + counts["missing"]
	if len(items) > 0 {
		switch {
		case counts["due"] > 0 && counts["missing"] > 0:
			nextAction = "Rotate due or missing provider token env values before external template provisioning."
		case counts["missing"] > 0:
			nextAction = "Configure missing provider token env values before external template provisioning."
		case counts["due"] > 0:
			nextAction = "Rotate due provider token env values before external template provisioning."
		case counts["soon"] > 0:
			nextAction = "Schedule provider token env rotation before the next due window."
		case counts["unknown"] > 0:
			nextAction = "Run a provider account check or rotate token env to establish rotation evidence."
		default:
			nextAction = "Provider token rotation evidence is fresh."
		}
	}
	return map[string]any{
		"total":           len(items),
		"fresh":           counts["fresh"],
		"soon":            counts["soon"],
		"due":             counts["due"],
		"missing":         counts["missing"],
		"unknown":         counts["unknown"],
		"action_required": actionRequired,
		"next_action":     nextAction,
	}
}

func providerAccountAutomatedRotationPlan(items []map[string]any, now time.Time) map[string]any {
	planItems := make([]map[string]any, 0, len(items))
	counts := map[string]int{
		"ready":      0,
		"blocked":    0,
		"not_needed": 0,
	}
	for _, item := range items {
		entry := providerAccountAutomatedRotationPlanItem(item, now)
		status := strings.TrimSpace(fmt.Sprint(entry["status"]))
		if _, ok := counts[status]; !ok {
			status = "blocked"
		}
		counts[status]++
		planItems = append(planItems, entry)
	}
	nextAction := "No provider accounts configured."
	if len(items) > 0 {
		switch {
		case counts["ready"] > 0:
			nextAction = "Review ready provider token rotation candidates, then execute the ready rotation plan or rotate manually."
		case counts["blocked"] > 0:
			nextAction = "Add safe rotation candidate token env metadata before automated rotation can be enabled."
		default:
			nextAction = "No provider token rotation is currently due."
		}
	}
	return map[string]any{
		"mode":                "dry_run",
		"automation_enabled":  false,
		"execution_available": counts["ready"] > 0,
		"external_call_made":  false,
		"total":               len(items),
		"ready":               counts["ready"],
		"blocked":             counts["blocked"],
		"not_needed":          counts["not_needed"],
		"next_action":         nextAction,
		"items":               planItems,
	}
}

func providerAccountAutomatedRotationPlanItem(item map[string]any, now time.Time) map[string]any {
	status := providerAccountTokenRotationStatus(item, now)
	accountID := rawStringFromMap(item, "id")
	tokenStatus := strings.TrimSpace(fmt.Sprint(status["status"]))
	entry := map[string]any{
		"provider_account_id": accountID,
		"name":                rawStringFromMap(item, "name"),
		"provider_type":       rawStringFromMap(item, "provider_type"),
		"rotation_status":     tokenStatus,
		"status":              "blocked",
		"automation_enabled":  false,
		"external_call_made":  false,
		"masked_current_env":  maskProviderTokenEnv(rawStringFromMap(item, "token_env")),
	}
	for _, key := range []string{"last_rotated_at", "next_rotation_due_at", "days_since_rotation", "days_until_due"} {
		if value, ok := status[key]; ok {
			entry[key] = value
		}
	}
	if rawStringFromMap(item, "token_env") == "" {
		entry["blocked_reason"] = "current token env is missing"
		entry["next_action"] = "configure a current provider token env before planning automated rotation"
		return entry
	}
	if tokenStatus != "due" && tokenStatus != "soon" {
		entry["status"] = "not_needed"
		entry["next_action"] = "no rotation required in the current window"
		return entry
	}
	candidate := providerAccountRotationCandidate(item)
	entry["candidate_present"] = candidate["present"]
	entry["masked_candidate_env"] = candidate["masked_token_env"]
	if candidate["safe"] != true {
		if candidate["present"] == true {
			entry["blocked_reason"] = "rotation candidate token env is not allowed for this provider type"
			entry["next_action"] = "set provider account metadata rotation_candidate_token_env to an allowed provider-scoped env name"
		} else {
			entry["blocked_reason"] = "safe rotation candidate token env metadata is missing"
			entry["next_action"] = "set provider account metadata rotation_candidate_token_env to an allowed env name"
		}
		return entry
	}
	if candidate["same_as_current"] == true {
		entry["blocked_reason"] = "candidate token env matches the current token env"
		entry["next_action"] = "provide a different allowed candidate token env"
		return entry
	}
	entry["status"] = "ready"
	entry["next_action"] = "ready for operator-triggered token-env rotation execution"
	return entry
}

type providerAccountRotationExecutionCandidate struct {
	account  map[string]any
	tokenEnv string
}

func providerAccountAutomatedRotationExecutionCandidates(items []map[string]any, now time.Time) []providerAccountRotationExecutionCandidate {
	candidates := make([]providerAccountRotationExecutionCandidate, 0)
	for _, item := range items {
		planItem := providerAccountAutomatedRotationPlanItem(item, now)
		if planItem["status"] != "ready" {
			continue
		}
		tokenEnv := providerAccountRotationCandidateEnv(item)
		if tokenEnv == "" ||
			!safeTemplateProviderTokenEnv(rawStringFromMap(item, "provider_type"), tokenEnv) ||
			tokenEnv == rawStringFromMap(item, "token_env") {
			continue
		}
		candidates = append(candidates, providerAccountRotationExecutionCandidate{
			account:  item,
			tokenEnv: tokenEnv,
		})
	}
	return candidates
}

var providerTokenRotationCandidateKeys = []string{
	"rotation_candidate_token_env",
	"next_token_env",
	"candidate_token_env",
	"automated_rotation_token_env",
}

func providerAccountRotationCandidateEnv(item map[string]any) string {
	metadata := mapFromAny(item["metadata"])
	for _, key := range providerTokenRotationCandidateKeys {
		if value := strings.TrimSpace(fmt.Sprint(metadata[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func providerAccountRotationCandidate(item map[string]any) map[string]any {
	providerType := rawStringFromMap(item, "provider_type")
	current := rawStringFromMap(item, "token_env")
	candidate := providerAccountRotationCandidateEnv(item)
	out := map[string]any{
		"present":          candidate != "",
		"safe":             false,
		"same_as_current":  false,
		"masked_token_env": maskProviderTokenEnv(candidate),
	}
	if candidate == "" {
		return out
	}
	out["safe"] = safeTemplateProviderTokenEnv(providerType, candidate)
	out["same_as_current"] = current != "" && candidate == current
	return out
}

const (
	providerTokenRotationDueDays  = 90
	providerTokenRotationSoonDays = 75
)

func providerAccountTokenRotationStatus(item map[string]any, now time.Time) map[string]any {
	status := map[string]any{
		"status": "unknown",
		"source": "unknown",
	}
	if rawStringFromMap(item, "token_env") == "" {
		status["status"] = "missing"
		return status
	}
	metadata := mapFromAny(item["metadata"])
	rotation := mapFromAny(metadata["token_rotation"])
	lastRotatedAt, source := providerAccountRotationTime(item, rotation)
	if lastRotatedAt.IsZero() {
		return status
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	lastRotatedAt = lastRotatedAt.UTC()
	daysSince := int(now.Sub(lastRotatedAt).Hours() / 24)
	if daysSince < 0 {
		daysSince = 0
	}
	dueAt := lastRotatedAt.Add(providerTokenRotationDueDays * 24 * time.Hour)
	daysUntilDue := int(math.Ceil(dueAt.Sub(now).Hours() / 24))
	tokenStatus := "fresh"
	if !now.Before(dueAt) {
		tokenStatus = "due"
		daysUntilDue = 0
	} else if daysSince >= providerTokenRotationSoonDays {
		tokenStatus = "soon"
	}
	status["status"] = tokenStatus
	status["source"] = source
	status["last_rotated_at"] = lastRotatedAt.Format(time.RFC3339)
	status["next_rotation_due_at"] = dueAt.Format(time.RFC3339)
	status["days_since_rotation"] = daysSince
	status["days_until_due"] = daysUntilDue
	return status
}

func providerAccountRotationTime(item, rotation map[string]any) (time.Time, string) {
	if rotatedAt := parseProviderAccountTime(rotation["rotated_at"]); !rotatedAt.IsZero() {
		return rotatedAt, "token_rotation"
	}
	if createdAt := parseProviderAccountTime(item["created_at"]); !createdAt.IsZero() {
		return createdAt, "created_at"
	}
	return time.Time{}, "unknown"
}

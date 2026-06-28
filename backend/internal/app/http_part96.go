package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"sort"
	"strings"
	"time"
)

func rollbackGuardrailSummary(rollbackPoints []map[string]any) map[string]any {
	previewable := 0
	executable := 0
	mode := ""
	for _, row := range rollbackPoints {
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(row["rollback_readiness"])), "previewable") {
			previewable++
		}
		if value, ok := row["rollback_executable"].(bool); ok && value {
			executable++
		}
		if rowMode := strings.TrimSpace(fmt.Sprint(row["rollback_execution_mode"])); rowMode != "" && rowMode != "<nil>" {
			if mode == "" {
				mode = rowMode
			} else if mode != rowMode {
				mode = "mixed"
			}
		}
	}
	if mode == "" {
		mode = "read_only_preview"
	}
	executionEnabled := executable > 0
	message := "Rollback execution is disabled in this first version; rollback points are preview-only evidence."
	if executionEnabled {
		message = "Rollback execution appears enabled for at least one rollback point; require explicit approval and environment review before action."
	}
	return map[string]any{
		"total":             len(rollbackPoints),
		"previewable_count": previewable,
		"executable_count":  executable,
		"execution_enabled": executionEnabled,
		"execution_mode":    mode,
		"message":           message,
	}
}

func rollbackPointReadiness(row map[string]any) (string, string) {
	status := strings.TrimSpace(fmt.Sprint(row["status"]))
	revision := cleanOptionalText(fmt.Sprint(row["revision"]))
	switch {
	case status == "expired":
		return "blocked", "rollback point is expired"
	case revision == "":
		return "incomplete", "rollback point has no captured revision"
	case status == "available":
		return "previewable", "rollback point has revision metadata; execution remains disabled in this first version"
	default:
		return "blocked", "rollback point is not available"
	}
}

// rollbackExecutionPlan builds the redacted JSON contract for unit tests and fallback callers.
func rollbackExecutionPlan(readiness, mode string) map[string]any {
	prerequisiteState := "metadata_blocked"
	if strings.EqualFold(strings.TrimSpace(readiness), "previewable") {
		prerequisiteState = "metadata_available"
	}
	mode = strings.TrimSpace(mode)
	if mode == "" || mode == "<nil>" {
		mode = "read_only_preview"
	}
	return map[string]any{
		"mode":                           "redacted_rollback_execution_plan",
		"plan_state":                     "blocked",
		"prerequisite_state":             prerequisiteState,
		"plan_ready":                     false,
		"plan_ready_reason":              "rollback_execution_backend_disabled",
		"execution_enabled":              false,
		"execution_mode":                 mode,
		"requires_approval":              true,
		"approval_action":                "deployment.rollback",
		"requires_environment_review":    true,
		"requires_kubeconfig_binding":    true,
		"requires_revision_verification": true,
		"requires_manifest_diff":         true,
		"requires_dry_run_preflight":     true,
		"requires_operator_confirmation": true,
		"rollback_request_materialized":  false,
		"revision_verified":              false,
		"manifest_diff_rendered":         false,
		"dry_run_performed":              false,
		"kubernetes_client_constructed":  false,
		"helm_rollback_invoked":          false,
		"kubectl_rollout_invoked":        false,
		"argocd_rollback_invoked":        false,
		"rollback_started":               false,
		"external_call_made":             false,
		"kubernetes_api_call_made":       false,
		"helm_command_invoked":           false,
		"rollback_mutation":              "disabled",
		"kubeconfig_included":            false,
		"secret_included":                false,
		"manifest_body_included":         false,
		"helm_values_included":           false,
		"cluster_credential_included":    false,
		"revision_value_included":        false,
		"contains_token":                 false,
		"contains_kubeconfig":            false,
		"contains_secret":                false,
		"contains_manifest_body":         false,
		"rollback_boundary_redacted":     true,
		"blocked_reasons":                []string{"rollback_execution_backend_disabled", "rollback_mutation_not_armed"},
		"required_controls":              []string{"operation_approval", "environment_review", "kubeconfig_binding", "revision_verification", "manifest_diff", "server_side_dry_run", "operator_confirmation"},
		"disabled_backends":              []string{"helm_rollback", "kubectl_rollout_undo", "argocd_rollback", "rollback_execute"},
		"suppressed_fields":              []string{"kubeconfig", "cluster_token", "authorization_header", "secret_manifest", "rendered_manifest", "helm_values", "image_pull_secret", "environment_secret", "revision_value"},
		"execution_sequence":             []string{"request_approval", "bind_environment", "bind_kubeconfig", "verify_revision", "render_manifest_diff", "run_server_side_dry_run", "record_rollback_audit", "start_rollback"},
	}
}

func countByStringField(rows []map[string]any, field string) map[string]int {
	counts := make(map[string]int)
	for _, row := range rows {
		key := strings.TrimSpace(fmt.Sprint(row[field]))
		if key == "" || key == "<nil>" {
			continue
		}
		counts[key]++
	}
	return counts
}

func countNestedStringField(rows []map[string]any, outer, inner string) map[string]int {
	counts := make(map[string]int)
	for _, row := range rows {
		nested := mapFromAny(row[outer])
		key := strings.TrimSpace(fmt.Sprint(nested[inner]))
		if key == "" || key == "<nil>" {
			continue
		}
		counts[key]++
	}
	return counts
}

func sanitizeContextRowsMetadata(rows []map[string]any) {
	for _, row := range rows {
		metadata, ok := row["metadata"].(map[string]any)
		if !ok {
			continue
		}
		// Keep AI/context snapshots explicitly sanitized even if callers change query normalization.
		row["metadata"] = sanitizeMetadata(metadata)
	}
}

func formatCountMap(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, ", ")
}

func (s *Server) approvePlan(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	task, err := agentTaskByID(r.Context(), s.store.Gorm, taskID)
	if err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	projectID := strings.TrimSpace(task.ProjectID)
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "agent_task", ID: taskID, ProjectID: projectID}, "agent.approve_plan") {
		return
	}
	var plan GormAgentPlan
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		plan, err = latestAgentPlanModel(r.Context(), tx, taskID)
		if err != nil {
			return err
		}
		plan.Status = "approved"
		plan.ApprovedAt = validNullTime(time.Now().UTC())
		if err := tx.Save(&plan).Error; err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(r.Context(), tx); err != nil {
			return fmt.Errorf("syncing canonical assets for agent plan approve: %w", err)
		}
		return nil
	}); err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	writeJSON(w, http.StatusOK, agentPlanMap(plan))
}

func latestAgentPlanModel(ctx context.Context, db *gorm.DB, taskID string) (GormAgentPlan, error) {
	var plans []GormAgentPlan
	if err := db.WithContext(ctx).Where(&GormAgentPlan{AgentTaskID: taskID}).Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "created_at"}, Desc: true}}}).Limit(1).Find(&plans).Error; err != nil {
		return GormAgentPlan{}, err
	}
	if len(plans) == 0 {
		return GormAgentPlan{}, gorm.ErrRecordNotFound
	}
	return plans[0], nil
}

func (s *Server) executePlan(w http.ResponseWriter, r *http.Request) {
	task, err := agentTaskByID(r.Context(), s.store.Gorm, chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	payload := map[string]any{"kind": "agent_execute", "agent_task_id": chi.URLParam(r, "id")}
	if !s.requireProjectPolicyOrApproval(w, r, PolicyResource{Type: "agent_task", ID: chi.URLParam(r, "id"), ProjectID: task.ProjectID}, "agent.execute", "execute agent task "+task.Title, payload) {
		return
	}
	var op map[string]any
	err = s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		op, err = s.enqueueAgentTaskExecutionGorm(r.Context(), tx, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(r.Context(), tx); err != nil {
			return fmt.Errorf("syncing canonical assets for agent task execute: %w", err)
		}
		return nil
	})
	if errors.Is(err, errAgentPlanNotApproved) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeCreatedOne(w, op, err)
		return
	}
	writeJSON(w, http.StatusCreated, op)
}

func (s *Server) enqueueAgentTaskExecutionGorm(ctx context.Context, tx *gorm.DB, taskID string) (map[string]any, error) {
	var task GormAgentTask
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&task, &GormAgentTask{GormBase: GormBase{ID: taskID}}).Error; err != nil {
		return nil, gormNotFoundAsErrNotFound(err)
	}
	plan, err := latestApprovedAgentPlanGorm(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	op, err := enqueueOperationGorm(ctx, tx, task.ProjectID, "", "agent.execute", "execute agent task "+task.Title, map[string]any{"agent_task_id": task.ID}, []string{"ai"}, "")
	if err != nil {
		return nil, err
	}
	if err = enqueueAgentToolCallAuditGorm(ctx, tx, task, plan, op); err != nil {
		return nil, err
	}
	task.Status = "queued"
	if err := tx.WithContext(ctx).Save(&task).Error; err != nil {
		return nil, err
	}
	return op, nil
}

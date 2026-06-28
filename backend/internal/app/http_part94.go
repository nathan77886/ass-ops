package app

import (
	"context"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"strings"
)

func applyGitRemotePatch(remote *GormGitRemote, req gitRemotePatchRequest) {
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		remote.Name = strings.TrimSpace(*req.Name)
	}
	if req.Kind != nil && strings.TrimSpace(*req.Kind) != "" {
		remote.Kind = strings.TrimSpace(*req.Kind)
	}
	if req.RemoteKey != nil && strings.TrimSpace(*req.RemoteKey) != "" {
		remote.RemoteKey = strings.TrimSpace(*req.RemoteKey)
	}
	if req.ProviderType != nil && strings.TrimSpace(*req.ProviderType) != "" {
		remote.ProviderType = strings.TrimSpace(*req.ProviderType)
	}
	if req.RemoteURL != nil {
		remote.RemoteURL = strings.TrimSpace(*req.RemoteURL)
	}
	if req.WebURL != nil {
		remote.WebURL = strings.TrimSpace(*req.WebURL)
	}
	if req.RemoteRole != nil && strings.TrimSpace(*req.RemoteRole) != "" {
		remote.RemoteRole = strings.TrimSpace(*req.RemoteRole)
	}
	if req.IsPrimary != nil {
		remote.IsPrimary = *req.IsPrimary
	}
	if req.SyncEnabled != nil {
		remote.SyncEnabled = *req.SyncEnabled
	}
	if req.Protected != nil {
		remote.Protected = *req.Protected
	}
	if req.LatestSHA != nil {
		remote.LatestSHA = strings.TrimSpace(*req.LatestSHA)
	}
	if req.LastSyncStatus != nil && strings.TrimSpace(*req.LastSyncStatus) != "" {
		remote.LastSyncStatus = strings.TrimSpace(*req.LastSyncStatus)
	}
	if req.URLs != nil {
		remote.URLs = JSONValue{Data: *req.URLs}
	}
	if req.DefaultBranch != nil && strings.TrimSpace(*req.DefaultBranch) != "" {
		remote.DefaultBranch = strings.TrimSpace(*req.DefaultBranch)
	}
	if req.Metadata != nil {
		remote.Metadata = JSONValue{Data: *req.Metadata}
	}
}

func (s *Server) connectionCredentialByID(ctx context.Context, id string) (*GormConnectionCredential, error) {
	id = cleanOptionalID(id)
	if id == "" {
		return nil, nil
	}
	var credential GormConnectionCredential
	if err := s.store.Gorm.WithContext(ctx).Where(map[string]any{"id": id}).Take(&credential).Error; err != nil {
		return nil, err
	}
	return &credential, nil
}

func (s *Server) connectionCredentialForProjectOrGlobal(ctx context.Context, projectID, credentialID, kind string) (*GormConnectionCredential, error) {
	credential, err := s.connectionCredentialByID(ctx, credentialID)
	if err != nil {
		return nil, err
	}
	if credential == nil || credential.Kind != kind || credential.SecretCiphertext == "" {
		return nil, ErrNotFound
	}
	projectID = cleanOptionalID(projectID)
	if credential.ProjectID.Valid && cleanOptionalID(credential.ProjectID.String) != projectID {
		return nil, ErrNotFound
	}
	return credential, nil
}

func (s *Server) createAgentTask(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "agent_task", ProjectID: projectID}, "create") {
		return
	}
	var req struct {
		Title  string `json:"title"`
		Prompt string `json:"prompt"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	task := GormAgentTask{ProjectID: projectID, Title: req.Title, Prompt: req.Prompt, CreatedBy: validNullString(currentUser(r).ID)}
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		if _, err := syncCanonicalAssetsGorm(r.Context(), tx); err != nil {
			return fmt.Errorf("syncing canonical assets for agent task create: %w", err)
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusBadRequest, "could not create agent task")
		return
	}
	writeJSON(w, http.StatusCreated, agentTaskMap(task, nil))
}

func (s *Server) listAgentTasks(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "agent_task", ProjectID: projectID}, "read") {
		return
	}
	var tasks []GormAgentTask
	if err := s.store.Gorm.WithContext(r.Context()).Where(&GormAgentTask{ProjectID: projectID}).Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "created_at"}, Desc: true}}}).Limit(100).Find(&tasks).Error; err != nil {
		writeQueryResult(w, nil, err)
		return
	}
	plans, err := latestAgentPlansByTaskID(r.Context(), s.store.Gorm, agentTaskIDs(tasks))
	if err != nil {
		writeQueryResult(w, nil, err)
		return
	}
	items := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, agentTaskMap(task, plans[task.ID]))
	}
	writeQueryResult(w, items, err)
}

func (s *Server) getAgentTask(w http.ResponseWriter, r *http.Request) {
	taskModel, err := agentTaskByID(r.Context(), s.store.Gorm, chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	projectID := strings.TrimSpace(taskModel.ProjectID)
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "agent_task", ID: chi.URLParam(r, "id"), ProjectID: projectID}, "read") {
		return
	}
	task := agentTaskMap(taskModel, nil)
	plans, _ := agentPlanMaps(r.Context(), s.store.Gorm, chi.URLParam(r, "id"))
	task["plans"] = plans
	toolCalls, _ := agentToolCallMaps(r.Context(), s.store.Gorm, chi.URLParam(r, "id"))
	task["tool_calls"] = toolCalls
	toolCallEvidence := agentToolCallAuditEvidence(toolCalls)
	task["tool_call_audit_evidence"] = toolCallEvidence
	task["code_modification_evidence"] = agentCodeModificationEvidence(toolCallEvidence)
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) listAgentTaskToolCalls(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	task, err := agentTaskByID(r.Context(), s.store.Gorm, taskID)
	if err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	projectID := strings.TrimSpace(task.ProjectID)
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "agent_task", ID: taskID, ProjectID: projectID}, "read") {
		return
	}
	items, err := agentToolCallMaps(r.Context(), s.store.Gorm, taskID)
	writeQueryResult(w, items, err)
}

func agentTaskByID(ctx context.Context, db *gorm.DB, taskID string) (GormAgentTask, error) {
	var task GormAgentTask
	if err := db.WithContext(ctx).First(&task, &GormAgentTask{GormBase: GormBase{ID: taskID}}).Error; err != nil {
		return GormAgentTask{}, err
	}
	return task, nil
}

func agentTaskIDs(tasks []GormAgentTask) []string {
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func agentTaskMap(task GormAgentTask, latestPlan *GormAgentPlan) map[string]any {
	item := map[string]any{
		"id":         task.ID,
		"project_id": task.ProjectID,
		"title":      task.Title,
		"prompt":     task.Prompt,
		"status":     task.Status,
		"created_by": nullableStringValue(task.CreatedBy),
		"created_at": task.CreatedAt,
		"updated_at": task.UpdatedAt,
	}
	if latestPlan != nil {
		item["latest_plan_id"] = latestPlan.ID
		item["latest_plan_status"] = latestPlan.Status
		item["latest_plan_created_at"] = latestPlan.CreatedAt
	}
	return item
}

func latestAgentPlansByTaskID(ctx context.Context, db *gorm.DB, taskIDs []string) (map[string]*GormAgentPlan, error) {
	if len(taskIDs) == 0 {
		return map[string]*GormAgentPlan{}, nil
	}
	wanted := map[string]bool{}
	for _, id := range taskIDs {
		wanted[id] = true
	}
	var plans []GormAgentPlan
	if err := db.WithContext(ctx).Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "created_at"}, Desc: true}}}).Find(&plans).Error; err != nil {
		return nil, err
	}
	out := map[string]*GormAgentPlan{}
	for i := range plans {
		if !wanted[plans[i].AgentTaskID] {
			continue
		}
		if _, exists := out[plans[i].AgentTaskID]; !exists {
			out[plans[i].AgentTaskID] = &plans[i]
		}
	}
	return out, nil
}

func agentPlanMaps(ctx context.Context, db *gorm.DB, taskID string) ([]map[string]any, error) {
	var plans []GormAgentPlan
	if err := db.WithContext(ctx).Where(&GormAgentPlan{AgentTaskID: taskID}).Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "created_at"}, Desc: true}}}).Find(&plans).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(plans))
	for _, plan := range plans {
		items = append(items, agentPlanMap(plan))
	}
	return items, nil
}

func agentPlanMap(plan GormAgentPlan) map[string]any {
	return map[string]any{"id": plan.ID, "agent_task_id": plan.AgentTaskID, "status": plan.Status, "content": plan.Content, "created_at": plan.CreatedAt, "approved_at": nullableTimeAny(plan.ApprovedAt)}
}

func agentToolCallMaps(ctx context.Context, db *gorm.DB, taskID string) ([]map[string]any, error) {
	var calls []GormAgentToolCall
	if err := db.WithContext(ctx).Where(&GormAgentToolCall{AgentTaskID: taskID}).Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "created_at"}, Desc: true}, {Column: clause.Column{Name: "id"}, Desc: true}}}).Limit(100).Find(&calls).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		items = append(items, agentToolCallMap(call))
	}
	return items, nil
}

func agentToolCallMap(call GormAgentToolCall) map[string]any {
	return map[string]any{"id": call.ID, "agent_task_id": call.AgentTaskID, "operation_run_id": nullableStringValue(call.OperationRunID), "project_id": nullableStringValue(call.ProjectID), "tool_name": call.ToolName, "input": mapFromAny(call.Input.Data), "output": mapFromAny(call.Output.Data), "status": call.Status, "started_at": nullableTimeAny(call.StartedAt), "finished_at": nullableTimeAny(call.FinishedAt), "error_message": call.ErrorMessage, "metadata": mapFromAny(call.Metadata.Data), "created_at": call.CreatedAt}
}

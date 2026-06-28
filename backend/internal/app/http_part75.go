package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"net/http"
)

func sanitizedProviderReviewAdapterContractOperations(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"name":                  cleanOptionalText(stringFromMap(item, "name")),
			"endpoint_key":          cleanOptionalText(stringFromMap(item, "endpoint_key")),
			"required_capability":   cleanOptionalText(stringFromMap(item, "required_capability")),
			"required_scope":        cleanOptionalText(stringFromMap(item, "required_scope")),
			"payload_shape":         cleanOptionalText(stringFromMap(item, "payload_shape")),
			"adapter_status":        cleanOptionalText(stringFromMap(item, "adapter_status")),
			"execution_status":      cleanOptionalText(stringFromMap(item, "execution_status")),
			"external_call_made":    false,
			"provider_api_mutation": "disabled",
			"payload_redacted":      true,
			"contains_token":        false,
			"contains_file_content": false,
		})
	}
	return out
}

func sanitizedProviderReviewReconciliationOperations(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"name":                  cleanOptionalText(stringFromMap(item, "name")),
			"endpoint_key":          cleanOptionalText(stringFromMap(item, "endpoint_key")),
			"status":                cleanOptionalText(stringFromMap(item, "status")),
			"blocked_reason":        cleanOptionalText(stringFromMap(item, "blocked_reason")),
			"external_call_made":    false,
			"provider_api_mutation": "disabled",
		})
	}
	return out
}

func (s *Server) operationApprovalDecisions(ctx context.Context, approvalID string) ([]map[string]any, error) {
	var decisions []GormOperationApprovalDecision
	if err := s.store.Gorm.WithContext(ctx).Where(&GormOperationApprovalDecision{OperationApprovalID: approvalID}).Order(gormOrderDesc("decided_at")).Find(&decisions).Error; err != nil {
		return nil, err
	}
	userIDs := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		if id := cleanOptionalID(decision.UserID.String); id != "" {
			userIDs = append(userIDs, id)
		}
	}
	users, err := usersByIDGorm(ctx, s.store.Gorm, userIDs)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(decisions))
	for _, decision := range decisions {
		userID := cleanOptionalID(decision.UserID.String)
		items = append(items, map[string]any{"id": decision.ID, "operation_approval_id": decision.OperationApprovalID, "user_id": nullableStringValue(decision.UserID), "user_email": users[userID].Email, "decision": decision.Decision, "reason": decision.Reason, "decided_at": decision.DecidedAt})
	}
	return items, nil
}

func (s *Server) operationApprovalDelegations(ctx context.Context, approvalID string) ([]map[string]any, error) {
	var delegations []GormOperationApprovalDelegation
	if err := s.store.Gorm.WithContext(ctx).Where(&GormOperationApprovalDelegation{OperationApprovalID: approvalID}).Order(gormOrderDesc("created_at")).Find(&delegations).Error; err != nil {
		return nil, err
	}
	userIDs := make([]string, 0, len(delegations)*2)
	for _, delegation := range delegations {
		if id := cleanOptionalID(delegation.FromUserID.String); id != "" {
			userIDs = append(userIDs, id)
		}
		userIDs = append(userIDs, delegation.ToUserID)
	}
	users, err := usersByIDGorm(ctx, s.store.Gorm, userIDs)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(delegations))
	for _, delegation := range delegations {
		fromID := cleanOptionalID(delegation.FromUserID.String)
		items = append(items, map[string]any{"id": delegation.ID, "operation_approval_id": delegation.OperationApprovalID, "from_user_id": nullableStringValue(delegation.FromUserID), "from_user_email": users[fromID].Email, "to_user_id": delegation.ToUserID, "to_user_email": users[delegation.ToUserID].Email, "reason": delegation.Reason, "revoked_at": nullableTimeAny(delegation.RevokedAt), "created_at": delegation.CreatedAt})
	}
	return items, nil
}

func usersByIDGorm(ctx context.Context, db *gorm.DB, ids []string) (map[string]GormUser, error) {
	out := map[string]GormUser{}
	ids = uniqueCleanIDs(ids)
	if len(ids) == 0 {
		return out, nil
	}
	var users []GormUser
	if err := db.WithContext(ctx).Find(&users, ids).Error; err != nil {
		return nil, err
	}
	for _, user := range users {
		out[user.ID] = user
	}
	return out, nil
}

func uniqueCleanIDs(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = cleanOptionalID(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func (s *Server) requireApprovalRead(w http.ResponseWriter, r *http.Request, approval map[string]any) bool {
	projectID := cleanOptionalID(fmt.Sprint(approval["project_id"]))
	if projectID == "" {
		return s.requirePolicy(w, r, PolicyResource{Type: "operation_approval", ID: fmt.Sprint(approval["id"])}, "read")
	}
	return s.requireProjectPolicy(w, r, PolicyResource{Type: "operation_approval", ID: fmt.Sprint(approval["id"]), ProjectID: projectID}, "read")
}

func safeOperationForAudit(operation map[string]any) map[string]any {
	if operation == nil {
		return nil
	}
	return map[string]any{
		"id":             operation["id"],
		"project_id":     operation["project_id"],
		"git_remote_id":  operation["git_remote_id"],
		"operation_type": operation["operation_type"],
		"status":         operation["status"],
		"title":          operation["title"],
		"error":          operation["error"],
		"started_at":     operation["started_at"],
		"finished_at":    operation["finished_at"],
		"created_at":     operation["created_at"],
		"updated_at":     operation["updated_at"],
	}
}

func (s *Server) operationRunRecords(ctx context.Context, opID string, canReadSSHOutput bool) (map[string]any, error) {
	records := map[string]any{}
	var syncRuns []GormRepoSyncRun
	if err := s.store.Gorm.WithContext(ctx).Where(&GormRepoSyncRun{OperationRunID: opID}).Order(gormOrderAsc("created_at")).Find(&syncRuns).Error; err != nil {
		return nil, err
	}
	syncItems := make([]map[string]any, 0, len(syncRuns))
	for _, run := range syncRuns {
		item := repoSyncRunMap(run)
		delete(item, "git_remote_id")
		delete(item, "actor_user_id")
		delete(item, "stdout")
		delete(item, "stderr")
		syncItems = append(syncItems, item)
	}
	records["repo_sync_runs"] = syncItems

	var tagRuns []GormRepoTagRun
	if err := s.store.Gorm.WithContext(ctx).Where(&GormRepoTagRun{OperationRunID: opID}).Order(gormOrderAsc("created_at")).Find(&tagRuns).Error; err != nil {
		return nil, err
	}
	tagItems := make([]map[string]any, 0, len(tagRuns))
	for _, run := range tagRuns {
		tagItems = append(tagItems, repoTagRunRecordMap(run))
	}
	records["repo_tag_runs"] = tagItems

	var templateRuns []GormProjectTemplateRun
	if err := s.store.Gorm.WithContext(ctx).Where(&GormProjectTemplateRun{OperationRunID: validNullString(opID)}).Order(gormOrderAsc("created_at")).Find(&templateRuns).Error; err != nil {
		return nil, err
	}
	templateItems := make([]map[string]any, 0, len(templateRuns))
	for _, run := range templateRuns {
		templateItems = append(templateItems, projectTemplateRunRecordMap(run))
	}
	records["project_template_runs"] = templateItems

	var events []GormWebhookEvent
	if err := s.store.Gorm.WithContext(ctx).Where(&GormWebhookEvent{OperationRunID: validNullString(opID)}).Order(gormOrderAsc("received_at")).Find(&events).Error; err != nil {
		return nil, err
	}
	eventItems := make([]map[string]any, 0, len(events))
	for _, event := range events {
		item := webhookEventMap(event)
		delete(item, "payload")
		delete(item, "result")
		eventItems = append(eventItems, item)
	}
	records["webhook_events"] = eventItems

	var toolCalls []GormAgentToolCall
	if err := s.store.Gorm.WithContext(ctx).Where(&GormAgentToolCall{OperationRunID: validNullString(opID)}).Order(gormOrderAsc("created_at")).Find(&toolCalls).Error; err != nil {
		return nil, err
	}
	toolItems := make([]map[string]any, 0, len(toolCalls))
	for _, call := range toolCalls {
		item := agentToolCallMap(call)
		delete(item, "metadata")
		toolItems = append(toolItems, item)
	}
	records["agent_tool_calls"] = toolItems

	var sshRuns []GormSSHCommandRun
	if err := s.store.Gorm.WithContext(ctx).Where(&GormSSHCommandRun{OperationRunID: validNullString(opID)}).Order(gormOrderAsc("created_at")).Find(&sshRuns).Error; err != nil {
		return nil, err
	}
	sshItems := make([]map[string]any, 0, len(sshRuns))
	for _, run := range sshRuns {
		item := sshCommandRunMap(run, "")
		delete(item, "operation_type")
		if !canReadSSHOutput {
			delete(item, "command")
			delete(item, "stdout")
			delete(item, "stderr")
		}
		sshItems = append(sshItems, item)
	}
	records["ssh_command_runs"] = sshItems
	return records, nil
}

func repoTagRunRecordMap(run GormRepoTagRun) map[string]any {
	return map[string]any{"id": run.ID, "operation_run_id": run.OperationRunID, "project_id": nullableStringValue(run.ProjectID), "project_git_repository_id": nullableStringValue(run.ProjectGitRepositoryID), "target_remote_id": nullableStringValue(run.TargetRemoteID), "tag_name": run.TagName, "target_sha": run.TargetSHA, "status": run.Status, "error_message": run.ErrorMessage, "started_at": nullableTimeAny(run.StartedAt), "finished_at": nullableTimeAny(run.FinishedAt), "created_at": run.CreatedAt}
}

func projectTemplateRunRecordMap(run GormProjectTemplateRun) map[string]any {
	return map[string]any{"id": run.ID, "operation_run_id": nullableStringValue(run.OperationRunID), "project_template_id": nullableStringValue(run.ProjectTemplateID), "requested_by": nullableStringValue(run.RequestedBy), "project_id": nullableStringValue(run.ProjectID), "project_name": run.ProjectName, "project_slug": run.ProjectSlug, "status": run.Status, "steps": mapSliceFromAny(run.Steps.Data), "error_message": run.ErrorMessage, "started_at": nullableTimeAny(run.StartedAt), "finished_at": nullableTimeAny(run.FinishedAt), "created_at": run.CreatedAt, "updated_at": run.UpdatedAt}
}

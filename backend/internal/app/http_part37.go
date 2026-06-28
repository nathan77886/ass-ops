package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"net/http"
	"strings"
	"time"
)

func (s *Server) upsertGitHubWorkflowRunFromWebhookGorm(ctx context.Context, tx *gorm.DB, connection map[string]any, payload map[string]any) (map[string]any, error) {
	remoteID := cleanOptionalID(fmt.Sprint(connection["source_remote_id"]))
	if remoteID == "" {
		return nil, fmt.Errorf("webhook connection has no GitHub remote")
	}
	remote, projectID, err := s.gitRemoteWithProjectGormTx(ctx, tx, remoteID)
	if err != nil {
		return nil, err
	}
	if projectID != cleanOptionalID(fmt.Sprint(connection["project_id"])) {
		return nil, fmt.Errorf("GitHub remote does not belong to webhook project")
	}
	var event githubWorkflowRunPayload
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, fmt.Errorf("decoding GitHub workflow_run payload: %w", err)
	}
	if event.WorkflowRun.ID == 0 {
		return nil, fmt.Errorf("GitHub workflow_run payload is missing workflow_run.id")
	}
	remoteMap := gitRemoteMap(remote, nil, projectID)
	owner, repo, err := gitHubRepositoryFromRemote(remoteMap)
	if err != nil {
		return nil, err
	}
	if event.Repository.FullName != "" && !strings.EqualFold(event.Repository.FullName, owner+"/"+repo) {
		return nil, fmt.Errorf("GitHub workflow_run repository does not match remote")
	}
	name := event.WorkflowRun.Name
	if name == "" {
		name = event.WorkflowRun.DisplayTitle
	}
	runID := fmt.Sprint(event.WorkflowRun.ID)
	run := GormGitHubActionRun{GitRemoteID: remoteID, ExternalRunID: runID}
	updates := GormGitHubActionRun{
		GitRemoteID:   remoteID,
		ExternalRunID: runID,
		WorkflowName:  name,
		RunID:         runID,
		Branch:        event.WorkflowRun.HeadBranch,
		CommitSHA:     event.WorkflowRun.HeadSHA,
		Status:        event.WorkflowRun.Status,
		Conclusion:    event.WorkflowRun.Conclusion,
		HTMLURL:       event.WorkflowRun.HTMLURL,
		Metadata: JSONValue{Data: map[string]any{
			"action":     event.Action,
			"event":      event.WorkflowRun.Event,
			"repository": event.Repository.FullName,
			"run_number": event.WorkflowRun.RunNumber,
		}},
		StartedAt: nullableTimeFromPointer(event.WorkflowRun.RunStartedAt),
		UpdatedAt: nullableTimeFromPointer(event.WorkflowRun.UpdatedAt),
		SyncedAt:  sql.NullTime{Time: time.Now().UTC(), Valid: true},
	}
	if err := tx.WithContext(ctx).Where(&run).Assign(updates).FirstOrCreate(&run).Error; err != nil {
		return nil, err
	}
	return map[string]any{
		"status":               "processed",
		"git_remote_id":        remoteID,
		"github_action_run_id": run.ID,
		"external_run_id":      runID,
		"workflow_name":        name,
		"branch":               event.WorkflowRun.HeadBranch,
		"conclusion":           event.WorkflowRun.Conclusion,
	}, nil
}

func nullableTimeFromPointer(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *value, Valid: true}
}

func (s *Server) gitRemoteWithProjectGormTx(ctx context.Context, tx *gorm.DB, remoteID string) (GormGitRemote, string, error) {
	var remote GormGitRemote
	if err := tx.WithContext(ctx).Where(&GormGitRemote{GormBase: GormBase{ID: remoteID}}).First(&remote).Error; err != nil {
		return remote, "", gormNotFoundAsErrNotFound(err)
	}
	var repo GormProjectGitRepository
	if err := tx.WithContext(ctx).Where(&GormProjectGitRepository{GormBase: GormBase{ID: remote.ProjectGitRepositoryID}}).First(&repo).Error; err != nil {
		return remote, "", gormNotFoundAsErrNotFound(err)
	}
	return remote, repo.ProjectID, nil
}

func repoSyncAssetMatchesWebhookRef(refs map[string]any, ref string) bool {
	kind, name := splitGitRef(ref)
	if kind == "" || name == "" {
		return false
	}
	switch kind {
	case "branch":
		return refListMatches(stringSliceFromAny(refs["branches"]), name)
	case "tag":
		return refListMatches(stringSliceFromAny(refs["tags"]), name)
	default:
		return false
	}
}

func refsForWebhookRef(ref string) map[string]any {
	kind, name := splitGitRef(ref)
	switch kind {
	case "branch":
		return map[string]any{"branches": []string{name}, "tags": []string{}}
	case "tag":
		return map[string]any{"branches": []string{}, "tags": []string{name}}
	default:
		return map[string]any{}
	}
}

func splitGitRef(ref string) (string, string) {
	ref = strings.TrimSpace(ref)
	switch {
	case strings.HasPrefix(ref, "refs/heads/"):
		return "branch", strings.TrimPrefix(ref, "refs/heads/")
	case strings.HasPrefix(ref, "refs/tags/"):
		return "tag", strings.TrimPrefix(ref, "refs/tags/")
	default:
		return "", ""
	}
}

func refListMatches(values []string, name string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "*" || value == name {
			return true
		}
	}
	return false
}

func (s *Server) recordWebhookEvent(ctx context.Context, connection map[string]any, eventType, deliveryID string, signatureValid bool, status, errorMessage string, payload, result map[string]any) (map[string]any, error) {
	var event map[string]any
	err := s.store.Gorm.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		created, err := s.recordWebhookEventTxGorm(ctx, tx, connection, eventType, deliveryID, signatureValid, status, errorMessage, payload, result)
		if err != nil {
			return err
		}
		event = created
		if _, err := syncCanonicalAssetsGorm(ctx, tx); err != nil {
			return fmt.Errorf("syncing canonical assets for webhook event: %w", err)
		}
		return nil
	})
	return event, err
}

func (s *Server) recordWebhookDiagnosticEvent(ctx context.Context, connection map[string]any, eventType, deliveryID string, signatureValid bool, status, errorMessage string, payload, result map[string]any) {
	if _, err := s.recordWebhookEvent(ctx, connection, eventType, deliveryID, signatureValid, status, errorMessage, payload, result); err != nil && s.log != nil {
		s.log.Warn(
			"failed to record webhook diagnostic event",
			"webhook_connection_id", connection["id"],
			"provider", connection["provider"],
			"event_type", eventType,
			"delivery_id", deliveryID,
			"status", status,
			"error", err,
		)
	}
}

func (s *Server) recordWebhookEventTxGorm(ctx context.Context, tx *gorm.DB, connection map[string]any, eventType, deliveryID string, signatureValid bool, status, errorMessage string, payload, result map[string]any) (map[string]any, error) {
	var matchedAssetID sql.NullString
	var operationRunID sql.NullString
	if result != nil {
		if value := cleanOptionalID(fmt.Sprint(result["matched_repo_sync_asset"])); value != "" {
			matchedAssetID = validNullString(value)
		}
		if runs, ok := result["queued_runs"].([]map[string]any); ok && len(runs) > 0 {
			operationRunID = validNullString(cleanOptionalID(fmt.Sprint(runs[0]["operation_run_id"])))
		}
	}
	now := time.Now().UTC()
	event := GormWebhookEvent{
		WebhookConnectionID:    validNullString(cleanOptionalID(fmt.Sprint(connection["id"]))),
		ProjectID:              validNullString(cleanOptionalID(fmt.Sprint(connection["project_id"]))),
		Provider:               strings.TrimSpace(fmt.Sprint(connection["provider"])),
		EventType:              eventType,
		DeliveryID:             deliveryID,
		SignatureValid:         signatureValid,
		MatchedRepoSyncAssetID: matchedAssetID,
		OperationRunID:         operationRunID,
		Status:                 status,
		ErrorMessage:           errorMessage,
		Payload:                JSONValue{Data: payload},
		Result:                 JSONValue{Data: result},
		ReceivedAt:             now,
		ProcessedAt:            sql.NullTime{Time: now, Valid: true},
	}
	if err := tx.WithContext(ctx).Create(&event).Error; err != nil {
		return nil, err
	}
	if err := tx.WithContext(ctx).Model(&GormWebhookConnection{}).
		Where(&GormWebhookConnection{GormBase: GormBase{ID: cleanOptionalID(fmt.Sprint(connection["id"]))}}).
		Updates(map[string]any{"last_delivery_status": status, "last_delivery_error": errorMessage}).Error; err != nil {
		return nil, err
	}
	return webhookEventMap(event), nil
}

func webhookEventMap(event GormWebhookEvent) map[string]any {
	return map[string]any{
		"id":                         event.ID,
		"webhook_connection_id":      nullableStringValue(event.WebhookConnectionID),
		"project_id":                 nullableStringValue(event.ProjectID),
		"provider":                   event.Provider,
		"event_type":                 event.EventType,
		"delivery_id":                event.DeliveryID,
		"signature_valid":            event.SignatureValid,
		"matched_repo_sync_asset_id": nullableStringValue(event.MatchedRepoSyncAssetID),
		"operation_run_id":           nullableStringValue(event.OperationRunID),
		"status":                     event.Status,
		"error_message":              event.ErrorMessage,
		"payload":                    mapFromAny(event.Payload.Data),
		"result":                     mapFromAny(event.Result.Data),
		"received_at":                event.ReceivedAt,
		"processed_at":               nullableTimeAny(event.ProcessedAt),
	}
}

func findProcessedWebhookDeliveryGorm(ctx context.Context, tx *gorm.DB, connectionID, deliveryID string) (map[string]any, bool, error) {
	var event GormWebhookEvent
	err := tx.WithContext(ctx).
		Where(&GormWebhookEvent{WebhookConnectionID: validNullString(connectionID), DeliveryID: deliveryID, SignatureValid: true}).
		Order("received_at DESC").
		First(&event).Error
	if errorsIsRecordNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return webhookEventMap(event), true, nil
}

func webhookEventType(r *http.Request) string {
	for _, header := range []string{"X-Gitea-Event", "X-GitHub-Event"} {
		if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
			return value
		}
	}
	return "push"
}

func webhookDeliveryID(r *http.Request) string {
	for _, header := range []string{"X-Gitea-Delivery", "X-GitHub-Delivery"} {
		if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
			return value
		}
	}
	return ""
}

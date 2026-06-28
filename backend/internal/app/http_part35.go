package app

import (
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"io"
	"net/http"
	"strings"
	"time"
)

func (s *Server) listWebhookEvents(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "webhook_event", ProjectID: projectID}, "read") {
		return
	}
	var events []GormWebhookEvent
	if err := s.store.Gorm.WithContext(r.Context()).Where(&GormWebhookEvent{ProjectID: validNullString(projectID)}).Order(gormOrderDesc("received_at")).Limit(100).Find(&events).Error; err != nil {
		writeQueryResult(w, nil, err)
		return
	}
	connectionIDs := make([]string, 0, len(events))
	for _, event := range events {
		if id := cleanOptionalID(event.WebhookConnectionID.String); id != "" {
			connectionIDs = append(connectionIDs, id)
		}
	}
	connectionNames := map[string]string{}
	if len(connectionIDs) > 0 {
		var connections []GormWebhookConnection
		if err := s.store.Gorm.WithContext(r.Context()).Find(&connections, connectionIDs).Error; err != nil {
			writeQueryResult(w, nil, err)
			return
		}
		for _, connection := range connections {
			connectionNames[connection.ID] = connection.Name
		}
	}
	items := make([]map[string]any, 0, len(events))
	for _, event := range events {
		item := webhookEventMap(event)
		delete(item, "payload")
		delete(item, "result")
		item["webhook_connection_name"] = connectionNames[cleanOptionalID(event.WebhookConnectionID.String)]
		items = append(items, item)
	}
	writeQueryResult(w, items, nil)
}

func (s *Server) replayWebhookEvent(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	var eventModel GormWebhookEvent
	if err := s.store.Gorm.WithContext(r.Context()).First(&eventModel, &GormWebhookEvent{ID: eventID}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	event := webhookEventMap(eventModel)
	projectID := strings.TrimSpace(fmt.Sprint(event["project_id"]))
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "webhook_event", ID: eventID, ProjectID: projectID}, "repo.sync") {
		return
	}
	if signatureValid, ok := event["signature_valid"].(bool); !ok || !signatureValid {
		writeError(w, http.StatusConflict, "only verified webhook events can be replayed")
		return
	}
	connectionID := strings.TrimSpace(fmt.Sprint(event["webhook_connection_id"]))
	if connectionID == "" || connectionID == "<nil>" {
		writeError(w, http.StatusBadRequest, "webhook event has no connection")
		return
	}
	provider := fmt.Sprint(event["provider"])
	eventType := fmt.Sprint(event["event_type"])
	if (provider != "gitea" || eventType != "push") && (provider != "github" || eventType != "workflow_run") {
		writeError(w, http.StatusBadRequest, "only gitea push or github workflow_run webhook events can be replayed")
		return
	}
	connection, err := s.webhookConnectionForDeliveryGorm(r.Context(), s.store.Gorm, connectionID, provider)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if enabled, ok := connection["enabled"].(bool); ok && !enabled {
		writeError(w, http.StatusConflict, "webhook connection is disabled")
		return
	}
	payload := mapFromAny(event["payload"])
	tx := s.store.Gorm.WithContext(r.Context()).Begin()
	if tx.Error != nil {
		writeError(w, http.StatusInternalServerError, "could not start webhook replay transaction")
		return
	}
	defer tx.Rollback()
	var result map[string]any
	if provider == "github" {
		result, err = s.upsertGitHubWorkflowRunFromWebhookGorm(r.Context(), tx, connection, payload)
	} else {
		push := parseGiteaPushPayload(payload)
		if push.Ref == "" {
			writeError(w, http.StatusBadRequest, "webhook event payload has no push ref")
			return
		}
		result, err = s.enqueueWebhookRepoSyncRunsGorm(r.Context(), tx, connection, push)
	}
	status := stringFromMap(result, "status")
	if status == "" {
		status = "processed"
	}
	errorMessage := ""
	if err != nil {
		status = "failed"
		errorMessage = err.Error()
	}
	if result == nil {
		result = map[string]any{}
	}
	result["replayed_from_event_id"] = eventID
	replayDeliveryID := replayWebhookDeliveryID(strings.TrimSpace(fmt.Sprint(event["delivery_id"])), eventID)
	if err != nil {
		_ = tx.Rollback()
		replayEvent, eventErr := s.recordWebhookEvent(r.Context(), connection, eventType, replayDeliveryID, false, status, errorMessage, payload, result)
		if eventErr != nil {
			writeError(w, http.StatusInternalServerError, "could not record webhook replay")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"event": replayEvent, "result": result, "error": errorMessage})
		return
	}
	replayEvent, eventErr := s.recordWebhookEventTxGorm(r.Context(), tx, connection, eventType, replayDeliveryID, false, status, errorMessage, payload, result)
	if eventErr != nil {
		writeError(w, http.StatusInternalServerError, "could not record webhook replay")
		return
	}
	if !s.syncCanonicalAssetsInGormTransaction(w, r, tx, "webhook_event.replay") {
		return
	}
	if err := tx.Commit().Error; err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit webhook replay")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"event": replayEvent, "result": result, "error": errorMessage})
}

func (s *Server) receiveGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	connectionID := chi.URLParam(r, "id")
	if !s.webhookLimiter.allow(webhookRateLimitKey(r, connectionID), time.Now()) {
		writeError(w, http.StatusTooManyRequests, "webhook rate limit exceeded")
		return
	}
	connection, err := s.webhookConnectionForDeliveryGorm(r.Context(), s.store.Gorm, connectionID, "github")
	if err != nil {
		writeError(w, http.StatusNotFound, "webhook connection not found")
		return
	}
	eventType := webhookEventType(r)
	deliveryID := webhookDeliveryID(r)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "webhook payload is too large")
		return
	}
	if enabled, ok := connection["enabled"].(bool); ok && !enabled {
		s.recordWebhookDiagnosticEvent(r.Context(), connection, eventType, deliveryID, false, "disabled", "webhook connection is disabled", nil, nil)
		writeError(w, http.StatusGone, "webhook connection is disabled")
		return
	}
	secret, err := s.webhookSecretFromConnection(connection)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read webhook secret")
		return
	}
	if !verifyWebhookSignature(r.Header, secret, body) {
		s.recordWebhookDiagnosticEvent(r.Context(), connection, eventType, deliveryID, false, "rejected", "invalid signature", nil, nil)
		writeError(w, http.StatusUnauthorized, "invalid webhook signature")
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		s.recordWebhookDiagnosticEvent(r.Context(), connection, eventType, deliveryID, true, "failed", "invalid JSON payload", nil, nil)
		writeError(w, http.StatusBadRequest, "invalid webhook JSON")
		return
	}
	if eventType != "workflow_run" {
		result := map[string]any{"event_type": eventType, "message": "ignored unsupported event"}
		s.recordWebhookDiagnosticEvent(r.Context(), connection, eventType, deliveryID, true, "ignored", "unsupported event type", payload, result)
		writeJSON(w, http.StatusAccepted, result)
		return
	}
	tx := s.store.Gorm.WithContext(r.Context()).Begin()
	if tx.Error != nil {
		writeError(w, http.StatusInternalServerError, "could not start GitHub webhook transaction")
		return
	}
	defer tx.Rollback()
	if deliveryID != "" {
		existing, exists, err := findProcessedWebhookDeliveryGorm(r.Context(), tx, connectionID, deliveryID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not check webhook delivery")
			return
		}
		if exists {
			if err := tx.Commit().Error; err != nil {
				writeError(w, http.StatusInternalServerError, "could not commit webhook delivery check")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"duplicate": true, "event": existing})
			return
		}
	}
	result, err := s.upsertGitHubWorkflowRunFromWebhookGorm(r.Context(), tx, connection, payload)
	status := "processed"
	errorMessage := ""
	if err != nil {
		status = "failed"
		errorMessage = err.Error()
	}
	if result == nil {
		result = map[string]any{}
	}
	if err != nil {
		_ = tx.Rollback()
		event, eventErr := s.recordWebhookEvent(r.Context(), connection, "workflow_run", deliveryID, true, status, errorMessage, payload, result)
		if eventErr != nil {
			writeError(w, http.StatusInternalServerError, "could not record GitHub webhook event")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"event": event, "result": result, "error": errorMessage})
		return
	}
	event, eventErr := s.recordWebhookEventTxGorm(r.Context(), tx, connection, "workflow_run", deliveryID, true, status, errorMessage, payload, result)
	if eventErr != nil {
		writeError(w, http.StatusInternalServerError, "could not record GitHub webhook event")
		return
	}
	if !s.syncCanonicalAssetsInGormTransaction(w, r, tx, "webhook_event.github_workflow_run") {
		return
	}
	if err := tx.Commit().Error; err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit GitHub webhook event")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"event": event, "result": result})
}

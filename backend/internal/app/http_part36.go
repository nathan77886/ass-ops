package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"io"
	"net/http"
	"strings"
	"time"
)

func (s *Server) receiveGiteaWebhook(w http.ResponseWriter, r *http.Request) {
	connectionID := chi.URLParam(r, "id")
	if !s.webhookLimiter.allow(webhookRateLimitKey(r, connectionID), time.Now()) {
		writeError(w, http.StatusTooManyRequests, "webhook rate limit exceeded")
		return
	}
	connection, err := s.webhookConnectionForDeliveryGorm(r.Context(), s.store.Gorm, connectionID, "gitea")
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
	if eventType != "" && eventType != "push" {
		result := map[string]any{"event_type": eventType, "message": "ignored unsupported event"}
		s.recordWebhookDiagnosticEvent(r.Context(), connection, eventType, deliveryID, true, "ignored", "unsupported event type", nil, result)
		writeJSON(w, http.StatusAccepted, result)
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		s.recordWebhookDiagnosticEvent(r.Context(), connection, eventType, deliveryID, true, "failed", "invalid JSON payload", nil, nil)
		writeError(w, http.StatusBadRequest, "invalid webhook JSON")
		return
	}
	push := parseGiteaPushPayload(payload)
	if push.Ref == "" {
		s.recordWebhookDiagnosticEvent(r.Context(), connection, "push", deliveryID, true, "failed", "push ref is required", payload, nil)
		writeError(w, http.StatusBadRequest, "push ref is required")
		return
	}
	tx := s.store.Gorm.WithContext(r.Context()).Begin()
	if tx.Error != nil {
		writeError(w, http.StatusInternalServerError, "could not start webhook transaction")
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
	result, err := s.enqueueWebhookRepoSyncRunsGorm(r.Context(), tx, connection, push)
	status := stringFromMap(result, "status")
	if status == "" {
		status = "processed"
	}
	errorMessage := ""
	if err != nil {
		status = "failed"
		errorMessage = err.Error()
	}
	if err != nil {
		_ = tx.Rollback()
		event, eventErr := s.recordWebhookEvent(r.Context(), connection, "push", deliveryID, true, status, errorMessage, payload, result)
		if eventErr != nil {
			writeError(w, http.StatusInternalServerError, "could not record webhook event")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"event": event, "result": result, "error": errorMessage})
		return
	}
	event, eventErr := s.recordWebhookEventTxGorm(r.Context(), tx, connection, "push", deliveryID, true, status, errorMessage, payload, result)
	if eventErr != nil {
		writeError(w, http.StatusInternalServerError, "could not record webhook event")
		return
	}
	if !s.syncCanonicalAssetsInGormTransaction(w, r, tx, "webhook_event.gitea_push") {
		return
	}
	if err := tx.Commit().Error; err != nil {
		writeError(w, http.StatusInternalServerError, "could not commit webhook event")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"event": event, "result": result})
}

func (s *Server) webhookConnectionForDeliveryGorm(ctx context.Context, db *gorm.DB, connectionID, provider string) (map[string]any, error) {
	var connection GormWebhookConnection
	if err := db.WithContext(ctx).Where(&GormWebhookConnection{GormBase: GormBase{ID: connectionID}, Provider: provider}).First(&connection).Error; err != nil {
		return nil, gormNotFoundAsErrNotFound(err)
	}
	return webhookConnectionDeliveryMap(connection), nil
}

func webhookConnectionDeliveryMap(connection GormWebhookConnection) map[string]any {
	return map[string]any{
		"id":                connection.ID,
		"project_id":        connection.ProjectID,
		"provider":          connection.Provider,
		"source_remote_id":  nullableStringValue(connection.SourceRemoteID),
		"secret_ciphertext": connection.SecretCiphertext,
		"enabled":           connection.Enabled,
	}
}

type giteaPushPayload struct {
	Ref       string
	BeforeSHA string
	AfterSHA  string
}

func parseGiteaPushPayload(payload map[string]any) giteaPushPayload {
	return giteaPushPayload{
		Ref:       strings.TrimSpace(fmt.Sprint(payload["ref"])),
		BeforeSHA: strings.TrimSpace(fmt.Sprint(payload["before"])),
		AfterSHA:  strings.TrimSpace(fmt.Sprint(payload["after"])),
	}
}

func (s *Server) enqueueWebhookRepoSyncRunsGorm(ctx context.Context, tx *gorm.DB, connection map[string]any, push giteaPushPayload) (map[string]any, error) {
	sourceRemoteID := cleanOptionalID(fmt.Sprint(connection["source_remote_id"]))
	if sourceRemoteID == "" {
		return nil, fmt.Errorf("webhook connection has no source remote")
	}
	projectID := cleanOptionalID(fmt.Sprint(connection["project_id"]))
	var assets []GormRepoSyncAsset
	if err := tx.WithContext(ctx).
		Where(&GormRepoSyncAsset{SourceRemoteID: sourceRemoteID, ProjectID: projectID, Enabled: true}).
		Where("archived_at IS NULL").
		Where("trigger_mode IN ?", []string{"webhook", "push", "manual_or_webhook"}).
		Order("created_at").
		Find(&assets).Error; err != nil {
		return nil, err
	}
	var runs []map[string]any
	var matchedAssetID string
	eventRefs := refsForWebhookRef(push.Ref)
	for _, asset := range assets {
		if !repoSyncAssetMatchesWebhookRef(mapFromAny(asset.Refs.Data), push.Ref) {
			continue
		}
		var repo GormProjectGitRepository
		if err := tx.WithContext(ctx).Where(&GormProjectGitRepository{GormBase: GormBase{ID: asset.ProjectGitRepositoryID}}).First(&repo).Error; err != nil {
			return nil, gormNotFoundAsErrNotFound(err)
		}
		var source GormGitRemote
		if err := tx.WithContext(ctx).Where(&GormGitRemote{GormBase: GormBase{ID: asset.SourceRemoteID}, ProjectGitRepositoryID: asset.ProjectGitRepositoryID}).First(&source).Error; err != nil {
			return nil, gormNotFoundAsErrNotFound(err)
		}
		var target GormGitRemote
		if err := tx.WithContext(ctx).Where(&GormGitRemote{GormBase: GormBase{ID: asset.TargetRemoteID}, ProjectGitRepositoryID: asset.ProjectGitRepositoryID}).First(&target).Error; err != nil {
			return nil, gormNotFoundAsErrNotFound(err)
		}
		run, err := s.enqueueRepoSyncRunGorm(ctx, tx, gitRepositoryMap(repo), gitRemoteMap(source, nil, repo.ProjectID), gitRemoteMap(target, nil, repo.ProjectID), eventRefs, false, "", asset.ID)
		if err != nil {
			return nil, err
		}
		if err := tx.WithContext(ctx).Model(&GormRepoSyncAsset{}).Where(&GormRepoSyncAsset{GormBase: GormBase{ID: asset.ID}}).Updates(map[string]any{"last_sync_status": "queued", "last_sync_run_id": validNullString(cleanOptionalID(fmt.Sprint(run["id"])))}).Error; err != nil {
			return nil, err
		}
		if matchedAssetID == "" {
			matchedAssetID = asset.ID
		}
		runs = append(runs, run)
	}
	status := "queued"
	if len(runs) == 0 {
		status = "ignored"
	}
	return map[string]any{
		"status":                  status,
		"ref":                     push.Ref,
		"before":                  push.BeforeSHA,
		"after":                   push.AfterSHA,
		"matched_repo_sync_asset": matchedAssetID,
		"queued_runs":             runs,
		"queued_count":            len(runs),
	}, nil
}

type githubWorkflowRunPayload struct {
	Action     string `json:"action"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	WorkflowRun struct {
		ID           int64      `json:"id"`
		Name         string     `json:"name"`
		DisplayTitle string     `json:"display_title"`
		RunNumber    int64      `json:"run_number"`
		HeadBranch   string     `json:"head_branch"`
		HeadSHA      string     `json:"head_sha"`
		Status       string     `json:"status"`
		Conclusion   string     `json:"conclusion"`
		HTMLURL      string     `json:"html_url"`
		RunStartedAt *time.Time `json:"run_started_at"`
		UpdatedAt    *time.Time `json:"updated_at"`
		Event        string     `json:"event"`
	} `json:"workflow_run"`
}

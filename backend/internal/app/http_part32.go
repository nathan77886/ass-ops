package app

import (
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"net/http"
	"strings"
)

func (s *Server) createWebhookConnection(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "webhook_connection", ProjectID: projectID}, "create") {
		return
	}
	var req struct {
		Name           string         `json:"name"`
		Provider       string         `json:"provider"`
		SourceRemoteID string         `json:"source_remote_id"`
		SecretToken    string         `json:"secret_token"`
		Enabled        *bool          `json:"enabled"`
		EventTypes     []string       `json:"event_types"`
		Metadata       map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Provider == "" {
		req.Provider = "gitea"
	}
	if req.Provider != "gitea" && req.Provider != "github" {
		writeError(w, http.StatusBadRequest, "provider must be gitea or github")
		return
	}
	if req.SourceRemoteID == "" {
		writeError(w, http.StatusBadRequest, "source_remote_id is required")
		return
	}
	remoteModel, remoteProjectID, err := s.gitRemoteWithProjectGorm(r.Context(), req.SourceRemoteID)
	if err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	if remoteProjectID != projectID {
		writeError(w, http.StatusBadRequest, "source remote must belong to the project")
		return
	}
	if req.Name == "" {
		req.Name = req.Provider + " webhook for " + remoteModel.Name
	}
	secret := strings.TrimSpace(req.SecretToken)
	generated := false
	if secret == "" {
		var err error
		secret, err = randomWebhookSecret()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not generate webhook secret")
			return
		}
		generated = true
	} else if len(secret) < 16 {
		writeError(w, http.StatusBadRequest, "secret_token must be at least 16 characters")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if len(req.EventTypes) == 0 {
		req.EventTypes = []string{"push"}
		if req.Provider == "github" {
			req.EventTypes = []string{"workflow_run"}
		}
	}
	secretCiphertext, err := s.encryptWebhookSecret(secret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not encrypt webhook secret")
		return
	}
	connection := GormWebhookConnection{
		ProjectID:        projectID,
		Provider:         req.Provider,
		Name:             req.Name,
		SourceRemoteID:   validNullString(req.SourceRemoteID),
		SecretCiphertext: secretCiphertext,
		Enabled:          enabled,
		EventTypes:       JSONValue{Data: req.EventTypes},
		Metadata:         JSONValue{Data: nonNilMap(req.Metadata)},
	}
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&connection).Error; err != nil {
			return err
		}
		_, err := syncCanonicalAssetsGorm(r.Context(), tx)
		if err != nil {
			return fmt.Errorf("syncing canonical assets for webhook_connection.create: %w", err)
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create webhook connection")
		return
	}
	path := "/api/webhooks/" + connection.Provider + "/" + connection.ID
	item := webhookConnectionMap(connection, s.publicBaseURL()+path, path)
	if generated {
		item["secret_token_once"] = secret
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) rotateWebhookConnectionSecret(w http.ResponseWriter, r *http.Request) {
	connectionID := chi.URLParam(r, "id")
	var connection GormWebhookConnection
	if err := s.store.Gorm.WithContext(r.Context()).First(&connection, &GormWebhookConnection{GormBase: GormBase{ID: connectionID}}).Error; err != nil {
		writeQueryOne(w, nil, gormNotFoundAsErrNotFound(err))
		return
	}
	projectID := connection.ProjectID
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "webhook_connection", ID: connectionID, ProjectID: projectID}, "update") {
		return
	}
	var req struct {
		SecretToken string `json:"secret_token"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	secret := strings.TrimSpace(req.SecretToken)
	generated := false
	if secret == "" {
		var err error
		secret, err = randomWebhookSecret()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not generate webhook secret")
			return
		}
		generated = true
	} else if len(secret) < 16 {
		writeError(w, http.StatusBadRequest, "secret_token must be at least 16 characters")
		return
	}
	secretCiphertext, err := s.encryptWebhookSecret(secret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not encrypt webhook secret")
		return
	}
	connection.SecretCiphertext = secretCiphertext
	if err := s.store.Gorm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&connection).Error; err != nil {
			return err
		}
		_, err := syncCanonicalAssetsGorm(r.Context(), tx)
		if err != nil {
			return fmt.Errorf("syncing canonical assets for webhook_connection.rotate_secret: %w", err)
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not rotate webhook secret")
		return
	}
	path := "/api/webhooks/" + connection.Provider + "/" + connection.ID
	item := webhookConnectionMap(connection, s.publicBaseURL()+path, path)
	if generated {
		item["secret_token_once"] = secret
	}
	writeJSON(w, http.StatusOK, item)
}

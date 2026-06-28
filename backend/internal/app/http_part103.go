package app

import (
	"context"
	"fmt"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm/clause"
	"net/http"
	"strings"
)

func (s *Server) createConnectionCredentialForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	var req struct {
		Name        string         `json:"name"`
		Kind        string         `json:"kind"`
		SecretValue string         `json:"secret_value"`
		PublicValue string         `json:"public_value"`
		Metadata    map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = cleanOptionalText(req.Name)
	req.Kind = cleanConnectionCredentialKind(req.Kind)
	req.SecretValue = strings.TrimSpace(req.SecretValue)
	req.PublicValue = strings.TrimSpace(req.PublicValue)
	if req.Name == "" || req.Kind == "" {
		writeError(w, http.StatusBadRequest, "name and kind are required")
		return
	}
	if req.SecretValue == "" {
		writeError(w, http.StatusBadRequest, "secret_value is required")
		return
	}
	if len(req.Name) > 253 || len(req.PublicValue) > 4096 || len(req.SecretValue) > 128*1024 {
		writeError(w, http.StatusBadRequest, "credential fields exceed allowed length")
		return
	}
	if req.Kind == "ssh_key" && !strings.Contains(req.SecretValue, "PRIVATE KEY") {
		writeError(w, http.StatusBadRequest, "ssh_key secret_value must be a private key")
		return
	}
	if (req.Kind == "git_https_password" || req.Kind == "git_https_token") && req.PublicValue == "" {
		writeError(w, http.StatusBadRequest, "git HTTPS credentials require public_value username")
		return
	}
	if (req.Kind == "git_https_password" || req.Kind == "git_https_token") && strings.Contains(req.SecretValue, "PRIVATE KEY") {
		writeError(w, http.StatusBadRequest, "git HTTPS secret_value must not be a private key")
		return
	}
	if projectID == "" && req.Kind != "provider_token" && req.Kind != "ai_provider_api_key" {
		writeError(w, http.StatusBadRequest, "global credentials must use provider_token or ai_provider_api_key kind")
		return
	}
	secretCiphertext, err := s.encryptWebhookSecret(req.SecretValue)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not encrypt credential secret")
		return
	}
	credential := GormConnectionCredential{
		ProjectID:        validNullString(projectID),
		Name:             req.Name,
		Kind:             req.Kind,
		SecretCiphertext: secretCiphertext,
		PublicValue:      req.PublicValue,
		Metadata:         JSONValue{Data: req.Metadata},
	}
	if err := s.store.Gorm.WithContext(r.Context()).Create(&credential).Error; err != nil {
		writeCreatedOne(w, nil, err)
		return
	}
	writeJSON(w, http.StatusCreated, connectionCredentialMap(credential))
}

func (s *Server) listConnectionCredentials(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "connection_credential", ProjectID: projectID}, "read") {
		return
	}
	var credentials []GormConnectionCredential
	err := s.store.Gorm.WithContext(r.Context()).
		Where(map[string]any{"project_id": projectID}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}, Desc: true}).
		Find(&credentials).Error
	writeQueryResult(w, connectionCredentialMaps(credentials), err)
}

func (s *Server) listGlobalConnectionCredentials(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "connection_credential"}, "read") {
		return
	}
	var credentials []GormConnectionCredential
	err := s.store.Gorm.WithContext(r.Context()).
		Where(map[string]any{"project_id": nil}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}, Desc: true}).
		Find(&credentials).Error
	writeQueryResult(w, connectionCredentialMaps(credentials), err)
}

func connectionCredentialMaps(credentials []GormConnectionCredential) []map[string]any {
	out := make([]map[string]any, 0, len(credentials))
	for _, credential := range credentials {
		out = append(out, connectionCredentialMap(credential))
	}
	return out
}

func connectionCredentialMap(credential GormConnectionCredential) map[string]any {
	return map[string]any{
		"id":                credential.ID,
		"project_id":        nullableStringValue(credential.ProjectID),
		"name":              credential.Name,
		"kind":              credential.Kind,
		"public_value":      credential.PublicValue,
		"metadata":          mapFromAny(credential.Metadata.Data),
		"created_at":        credential.CreatedAt,
		"updated_at":        credential.UpdatedAt,
		"secret_configured": credential.SecretCiphertext != "",
	}
}

func cleanConnectionCredentialKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case "ssh_key", "ssh_password", "git_https_password", "git_https_token", "argo_token", "provider_token", "ai_provider_api_key":
		return strings.TrimSpace(kind)
	default:
		return ""
	}
}

func isGitRemoteCredentialKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "ssh_key", "git_https_password", "git_https_token":
		return true
	default:
		return false
	}
}

func (s *Server) gitRemoteConnectionCredential(ctx context.Context, projectID, credentialID string) (*GormConnectionCredential, error) {
	credential, err := s.connectionCredentialByID(ctx, credentialID)
	if err != nil {
		return nil, err
	}
	if credential == nil || credential.SecretCiphertext == "" {
		return nil, ErrNotFound
	}
	if !isGitRemoteCredentialKind(credential.Kind) {
		return nil, ErrNotFound
	}
	projectID = cleanOptionalID(projectID)
	if credential.ProjectID.Valid && cleanOptionalID(credential.ProjectID.String) != projectID {
		return nil, ErrNotFound
	}
	return credential, nil
}

func connectionCredentialKindForSSHAuth(authType string) string {
	switch strings.TrimSpace(authType) {
	case "key":
		return "ssh_key"
	case "password":
		return "ssh_password"
	default:
		return ""
	}
}

func credentialText(credential map[string]any, key string) string {
	if credential == nil {
		return ""
	}
	value := strings.TrimSpace(fmt.Sprint(credential[key]))
	if value == "<nil>" {
		return ""
	}
	return value
}

func (s *Server) createArgoConnection(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "argo_connection", ProjectID: projectID}, "create") {
		return
	}
	var req struct {
		Name         string         `json:"name"`
		ServerURL    string         `json:"server_url"`
		AuthType     string         `json:"auth_type"`
		CredentialID string         `json:"credential_id"`
		Config       map[string]any `json:"config"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !validPublicHTTPURL(r.Context(), req.ServerURL) {
		writeError(w, http.StatusBadRequest, "server_url must be a public http or https URL")
		return
	}
	if (boolConfig(req.Config, "insecure_skip_verify") || boolConfig(req.Config, "use_env_token")) && !canUseSensitiveArgoConfig(currentUser(r)) {
		writeError(w, http.StatusForbidden, "sensitive Argo connection config requires an owner role")
		return
	}
	if req.AuthType == "" {
		req.AuthType = "token"
	}
	if req.AuthType != "token" {
		writeError(w, http.StatusBadRequest, "auth_type must be token")
		return
	}
	credential, err := s.connectionCredentialForProjectOrGlobal(r.Context(), projectID, req.CredentialID, "argo_token")
	if err != nil {
		writeError(w, http.StatusBadRequest, "credential_id must reference an Argo token credential in this project")
		return
	}
	connection := GormArgoConnection{
		ProjectID:    projectID,
		Name:         req.Name,
		ServerURL:    req.ServerURL,
		AuthType:     req.AuthType,
		CredentialID: validNullString(req.CredentialID),
		Config:       JSONValue{Data: req.Config},
	}
	if err := s.store.Gorm.WithContext(r.Context()).Create(&connection).Error; err != nil {
		writeError(w, http.StatusBadRequest, "could not create resource")
		return
	}
	if _, err := s.store.SyncCanonicalAssets(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync argo connection asset")
		return
	}
	writeJSON(w, http.StatusCreated, argoConnectionMap(connection, credential))
}

func (s *Server) listArgoConnections(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "argo_connection", ProjectID: projectID}, "read") {
		return
	}
	var connections []GormArgoConnection
	err := s.store.Gorm.WithContext(r.Context()).
		Where(map[string]any{"project_id": projectID}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}, Desc: true}).
		Find(&connections).Error
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	credentials, err := s.connectionCredentialsForArgoConnections(r.Context(), connections)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": argoConnectionMaps(connections, credentials)})
}

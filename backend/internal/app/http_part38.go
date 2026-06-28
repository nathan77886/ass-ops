package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"gorm.io/gorm/clause"
	"net/http"
	"net/url"
	"strings"
)

func verifyWebhookSignature(header http.Header, secret string, body []byte) bool {
	if secret == "" {
		return false
	}
	expectedMAC := hmac.New(sha256.New, []byte(secret))
	_, _ = expectedMAC.Write(body)
	expected := expectedMAC.Sum(nil)
	for _, candidate := range []string{
		stripSignaturePrefix(header.Get("X-Gitea-Signature")),
		stripSignaturePrefix(header.Get("X-Hub-Signature-256")),
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		got, err := hex.DecodeString(candidate)
		if err == nil && hmac.Equal(got, expected) {
			return true
		}
	}
	return false
}

func (s *Server) encryptWebhookSecret(secret string) (string, error) {
	block, err := aes.NewCipher(s.webhookSecretKey())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, []byte(secret), nil)
	return "v1:" + hex.EncodeToString(nonce) + ":" + hex.EncodeToString(sealed), nil
}

func (s *Server) decryptWebhookSecret(ciphertext string) (string, error) {
	parts := strings.Split(strings.TrimSpace(ciphertext), ":")
	if len(parts) == 3 && parts[0] == "v1" {
		parts = parts[1:]
	} else if len(parts) != 2 {
		return "", fmt.Errorf("invalid webhook secret ciphertext")
	}
	nonce, err := hex.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("decoding webhook secret nonce: %w", err)
	}
	sealed, err := hex.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decoding webhook secret ciphertext: %w", err)
	}
	block, err := aes.NewCipher(s.webhookSecretKey())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypting webhook secret: %w", err)
	}
	return string(plain), nil
}

func (s *Server) webhookSecretFromConnection(connection map[string]any) (string, error) {
	ciphertext := strings.TrimSpace(fmt.Sprint(connection["secret_ciphertext"]))
	if ciphertext != "" && ciphertext != "<nil>" {
		return s.decryptWebhookSecret(ciphertext)
	}
	return "", fmt.Errorf("webhook connection has no secret configured")
}

func (s *Server) webhookSecretKey() []byte {
	material := strings.TrimSpace(s.cfg.WebhookSecretKey)
	if material == "" {
		material = s.cfg.JWTSecret
	}
	sum := sha256.Sum256([]byte("assops:webhook-secret-encryption:" + material))
	return sum[:]
}

func (s *Server) publicBaseURL() string {
	base := strings.TrimSpace(s.cfg.GatewayURL)
	if base == "" {
		base = "http://localhost:8080"
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "http://localhost:8080"
	}
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

func replayWebhookDeliveryID(deliveryID, eventID string) string {
	if deliveryID == "" || deliveryID == "<nil>" {
		deliveryID = eventID
	}
	return deliveryID + ":replay:" + uuid.NewString()
}

func stripSignaturePrefix(value string) string {
	value = strings.TrimSpace(value)
	return strings.TrimPrefix(value, "sha256=")
}

func randomWebhookSecret() (string, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func (s *Server) createGitRemote(w http.ResponseWriter, r *http.Request) {
	projectID, err := s.projectIDForRepositoryGorm(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_remote", ProjectID: projectID}, "create") {
		return
	}
	var req struct {
		Name           string         `json:"name"`
		Kind           string         `json:"kind"`
		RemoteKey      string         `json:"remote_key"`
		ProviderType   string         `json:"provider_type"`
		CredentialID   string         `json:"credential_id"`
		RemoteURL      string         `json:"remote_url"`
		WebURL         string         `json:"web_url"`
		RemoteRole     string         `json:"remote_role"`
		IsPrimary      bool           `json:"is_primary"`
		SyncEnabled    *bool          `json:"sync_enabled"`
		Protected      bool           `json:"protected"`
		LatestSHA      string         `json:"latest_sha"`
		LastSyncStatus string         `json:"last_sync_status"`
		URLs           []string       `json:"urls"`
		DefaultBranch  string         `json:"default_branch"`
		Metadata       map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Kind == "" {
		req.Kind = "github"
	}
	if req.ProviderType == "" {
		req.ProviderType = req.Kind
	}
	if req.RemoteKey == "" {
		req.RemoteKey = req.Name
	}
	if req.RemoteRole == "" {
		req.RemoteRole = "mirror"
	}
	syncEnabled := true
	if req.SyncEnabled != nil {
		syncEnabled = *req.SyncEnabled
	}
	if req.LastSyncStatus == "" {
		req.LastSyncStatus = "never"
	}
	if req.DefaultBranch == "" {
		req.DefaultBranch = "main"
	}
	if req.RemoteURL == "" && len(req.URLs) > 0 {
		req.RemoteURL = req.URLs[0]
	}
	credentialID := cleanOptionalID(req.CredentialID)
	var credential *GormConnectionCredential
	if credentialID != "" {
		credential, err = s.gitRemoteConnectionCredential(r.Context(), projectID, credentialID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "credential_id must reference a Git remote credential in this project")
			return
		}
	}
	remote := GormGitRemote{
		ProjectGitRepositoryID: chi.URLParam(r, "id"),
		Name:                   req.Name,
		Kind:                   req.Kind,
		RemoteKey:              req.RemoteKey,
		ProviderType:           req.ProviderType,
		RemoteURL:              req.RemoteURL,
		WebURL:                 req.WebURL,
		RemoteRole:             req.RemoteRole,
		IsPrimary:              req.IsPrimary,
		SyncEnabled:            syncEnabled,
		Protected:              req.Protected,
		LatestSHA:              req.LatestSHA,
		LastSyncStatus:         req.LastSyncStatus,
		URLs:                   JSONValue{Data: req.URLs},
		DefaultBranch:          req.DefaultBranch,
		CredentialID:           validNullString(credentialID),
		Metadata:               JSONValue{Data: req.Metadata},
	}
	if err := s.store.Gorm.WithContext(r.Context()).Create(&remote).Error; err != nil {
		writeError(w, http.StatusBadRequest, "could not create resource")
		return
	}
	if _, err := s.store.SyncCanonicalAssets(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync git remote asset")
		return
	}
	writeJSON(w, http.StatusCreated, gitRemoteMap(remote, credential, projectID))
}

type gitRemotePatchRequest struct {
	Name           *string         `json:"name"`
	Kind           *string         `json:"kind"`
	RemoteKey      *string         `json:"remote_key"`
	ProviderType   *string         `json:"provider_type"`
	RemoteURL      *string         `json:"remote_url"`
	WebURL         *string         `json:"web_url"`
	RemoteRole     *string         `json:"remote_role"`
	IsPrimary      *bool           `json:"is_primary"`
	SyncEnabled    *bool           `json:"sync_enabled"`
	Protected      *bool           `json:"protected"`
	LatestSHA      *string         `json:"latest_sha"`
	LastSyncStatus *string         `json:"last_sync_status"`
	URLs           *[]string       `json:"urls"`
	DefaultBranch  *string         `json:"default_branch"`
	Metadata       *map[string]any `json:"metadata"`
}

func (s *Server) listGitRemotes(w http.ResponseWriter, r *http.Request) {
	projectID, err := s.projectIDForRepositoryGorm(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	if !s.requireProjectPolicy(w, r, PolicyResource{Type: "git_remote", ProjectID: projectID}, "read") {
		return
	}
	var remotes []GormGitRemote
	err = s.store.Gorm.WithContext(r.Context()).
		Where(map[string]any{"project_git_repository_id": chi.URLParam(r, "id")}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "created_at"}, Desc: true}).
		Find(&remotes).Error
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	credentials, err := s.connectionCredentialsForGitRemotes(r.Context(), remotes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": gitRemoteMaps(remotes, credentials, projectID)})
}

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func runProviderAccountCheck(ctx context.Context, account providerAccountConfig, client *http.Client) map[string]any {
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	check := map[string]any{
		"checked_at":        checkedAt,
		"provider_type":     account.ProviderType,
		"token_env_present": false,
		"status":            "error",
	}
	token := strings.TrimSpace(os.Getenv(account.TokenEnv))
	if token == "" && strings.TrimSpace(account.CredentialCiphertext) != "" {
		plain, err := decryptArgoCredentialSecret(account.CredentialCiphertext, argoCredentialSecretKeyMaterial())
		if err != nil {
			check["message"] = "provider token credential could not be decrypted"
			return check
		}
		token = strings.TrimSpace(plain)
	}
	if token == "" {
		check["message"] = "provider token environment variable is not set"
		return check
	}
	check["token_env_present"] = true
	checkURL, ok := providerAccountCheckURL(account)
	if !ok {
		check["message"] = "provider account check endpoint is not configured"
		return check
	}
	if err := validateTemplateProviderURL(ctx, checkURL); err != nil {
		check["message"] = "provider check URL is unsafe: " + truncateProviderError(err.Error(), 240)
		return check
	}
	if client == nil {
		client = newTemplateProviderHTTPClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
	if err != nil {
		check["message"] = truncateProviderError(err.Error(), 240)
		return check
	}
	switch account.ProviderType {
	case "github":
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	case "gitea":
		req.Header.Set("Authorization", "token "+token)
		req.Header.Set("Accept", "application/json")
	default:
		check["message"] = "unsupported provider type"
		return check
	}
	res, err := client.Do(req)
	if err != nil {
		check["message"] = truncateProviderError(err.Error(), 240)
		return check
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	check["http_status"] = res.StatusCode
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		message := providerErrorMessage(body)
		if message == "" {
			message = res.Status
		}
		check["message"] = truncateProviderError(message, 240)
		return check
	}
	payload := map[string]any{}
	_ = json.Unmarshal(body, &payload)
	check["status"] = "ok"
	check["message"] = "provider token verified"
	if actor := firstNonEmptyString(stringFromMap(payload, "login"), stringFromMap(payload, "username"), stringFromMap(payload, "user_name"), stringFromMap(payload, "name")); actor != "" {
		check["actor"] = actor
	}
	return check
}

func providerAccountCheckURL(account providerAccountConfig) (string, bool) {
	apiBase := strings.TrimRight(strings.TrimSpace(account.APIBaseURL), "/")
	if apiBase == "" {
		return "", false
	}
	switch account.ProviderType {
	case "github", "gitea":
		return apiBase + "/user", true
	default:
		return "", false
	}
}

func cloneMap(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func mergeMaps(base, overlay map[string]any) map[string]any {
	if base == nil {
		base = map[string]any{}
	}
	for key, value := range overlay {
		base[key] = value
	}
	return base
}

type providerAccountConfig struct {
	ID                   string
	Name                 string
	ProviderType         string
	APIBaseURL           string
	WebBaseURL           string
	TokenEnv             string
	CredentialID         string
	CredentialCiphertext string
	DefaultOwner         string
	Visibility           string
	Enabled              bool
	Metadata             map[string]any
}

func (s *Server) loadProviderAccountByID(ctx context.Context, id string) (GormProviderAccount, error) {
	return s.loadProviderAccount(ctx, "id", id)
}

func (s *Server) loadProviderAccountByName(ctx context.Context, name string) (GormProviderAccount, error) {
	return s.loadProviderAccount(ctx, "name", name)
}

func (s *Server) loadProviderAccount(ctx context.Context, field, value string) (GormProviderAccount, error) {
	var account GormProviderAccount
	err := s.store.Gorm.WithContext(ctx).Where(map[string]any{field: value}).First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return account, ErrNotFound
	}
	return account, err
}

func sanitizeProviderAccounts(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, sanitizeProviderAccount(item))
	}
	return out
}

func sanitizeProviderAccount(item map[string]any) map[string]any {
	if item == nil {
		return nil
	}
	out := make(map[string]any, len(item)+2)
	for key, value := range item {
		if key == "token_env" {
			continue
		}
		if key == "metadata" {
			out[key] = sanitizeProviderAccountMetadata(mapFromAny(value))
			continue
		}
		out[key] = value
	}
	tokenEnv := rawStringFromMap(item, "token_env")
	out["token_configured"] = tokenEnv != "" || boolOnlyFromAny(item["credential_configured"])
	out["masked_token_env"] = maskProviderTokenEnv(tokenEnv)
	out["token_rotation_status"] = providerAccountTokenRotationStatus(item, time.Now().UTC())
	out["token_rotation_candidate"] = providerAccountRotationCandidate(item)
	return out
}

func providerAccountMap(account GormProviderAccount, credential *GormConnectionCredential) map[string]any {
	item := map[string]any{
		"id":            account.ID,
		"name":          account.Name,
		"provider_type": account.ProviderType,
		"api_base_url":  account.APIBaseURL,
		"web_base_url":  account.WebBaseURL,
		"token_env":     account.TokenEnv,
		"credential_id": nullableStringValue(account.CredentialID),
		"default_owner": account.DefaultOwner,
		"visibility":    account.Visibility,
		"enabled":       account.Enabled,
		"metadata":      mapFromAny(account.Metadata.Data),
		"created_at":    account.CreatedAt,
		"updated_at":    account.UpdatedAt,
	}
	if credential != nil {
		item["credential_name"] = credential.Name
		item["credential_kind"] = credential.Kind
		item["credential_configured"] = credential.SecretCiphertext != ""
	} else {
		item["credential_configured"] = false
	}
	return item
}

func providerAccountConfigFromGorm(account GormProviderAccount) providerAccountConfig {
	return providerAccountConfig{
		ID:           account.ID,
		Name:         account.Name,
		ProviderType: account.ProviderType,
		APIBaseURL:   account.APIBaseURL,
		WebBaseURL:   account.WebBaseURL,
		TokenEnv:     account.TokenEnv,
		CredentialID: nullableStringFromNull(account.CredentialID),
		DefaultOwner: account.DefaultOwner,
		Visibility:   account.Visibility,
		Enabled:      account.Enabled,
		Metadata:     mapFromAny(account.Metadata.Data),
	}
}

func nullableStringFromNull(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return cleanOptionalText(value.String)
}

func (s *Server) connectionCredentialsForProviderAccounts(ctx context.Context, accounts []GormProviderAccount) (map[string]*GormConnectionCredential, error) {
	ids := make([]string, 0, len(accounts))
	seen := map[string]bool{}
	for _, account := range accounts {
		id := cleanOptionalID(account.CredentialID.String)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return map[string]*GormConnectionCredential{}, nil
	}
	var credentials []GormConnectionCredential
	if err := s.store.Gorm.WithContext(ctx).Find(&credentials, ids).Error; err != nil {
		return nil, err
	}
	out := make(map[string]*GormConnectionCredential, len(credentials))
	for i := range credentials {
		credential := credentials[i]
		out[credential.ID] = &credential
	}
	return out, nil
}

func sanitizeProviderAccountMetadata(metadata map[string]any) map[string]any {
	out := cloneMap(metadata)
	for _, key := range providerTokenRotationCandidateKeys {
		delete(out, key)
	}
	delete(out, "token")
	delete(out, "token_env")
	delete(out, "secret")
	return out
}

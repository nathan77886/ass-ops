package app

import (
	"context"
	"fmt"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strings"
	"time"
)

func (s *Server) rotateProviderAccountTokenEnv(w http.ResponseWriter, r *http.Request) {
	accountID := chi.URLParam(r, "id")
	if !s.requirePolicy(w, r, PolicyResource{Type: "provider_account", ID: accountID}, "update") {
		return
	}
	accountModel, err := s.loadProviderAccountByID(r.Context(), accountID)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	account := providerAccountConfigFromGorm(accountModel)
	var req struct {
		TokenEnv string `json:"token_env"`
		Reason   string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	tokenEnv := strings.TrimSpace(req.TokenEnv)
	if tokenEnv == "" {
		writeError(w, http.StatusBadRequest, "token_env is required")
		return
	}
	if !safeTemplateProviderTokenEnv(account.ProviderType, tokenEnv) {
		writeError(w, http.StatusBadRequest, "token_env is not allowed for provider_type")
		return
	}
	next := providerAccountInput{
		Name:         account.Name,
		ProviderType: account.ProviderType,
		APIBaseURL:   account.APIBaseURL,
		WebBaseURL:   account.WebBaseURL,
		TokenEnv:     tokenEnv,
		DefaultOwner: account.DefaultOwner,
		Visibility:   account.Visibility,
		Metadata:     cloneMap(account.Metadata),
	}
	next.Metadata = providerAccountRotationMetadata(next.Metadata, account.TokenEnv, tokenEnv, strings.TrimSpace(req.Reason), currentUser(r))
	result := s.store.Gorm.WithContext(r.Context()).Model(&GormProviderAccount{}).
		Where(map[string]any{"id": accountID, "token_env": account.TokenEnv}).
		Updates(map[string]any{"token_env": tokenEnv, "metadata": JSONValue{Data: next.Metadata}})
	if result.Error != nil {
		writeError(w, http.StatusInternalServerError, "could not rotate provider token env")
		return
	}
	if result.RowsAffected == 0 {
		writeError(w, http.StatusConflict, "provider account changed during token rotation; retry")
		return
	}
	if err := s.refreshGitRemotesForProviderAccountGorm(r.Context(), next, accountID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not refresh provider account remotes")
		return
	}
	accountModel.TokenEnv = tokenEnv
	accountModel.Metadata = JSONValue{Data: next.Metadata}
	if _, err := s.store.SyncCanonicalAssets(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync provider account asset")
		return
	}
	writeJSON(w, http.StatusOK, sanitizeProviderAccount(providerAccountMap(accountModel, nil)))
}

func (s *Server) executeProviderAccountTokenRotationPlan(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "provider_account"}, "update") {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "automated rotation plan execution"
	}
	now := time.Now().UTC()
	var accounts []GormProviderAccount
	if err := s.store.Gorm.WithContext(r.Context()).Order(gormOrderAsc("provider_type")).Order(gormOrderAsc("name")).Find(&accounts).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "could not load provider token rotation plan")
		return
	}
	items := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		items = append(items, providerAccountMap(account, nil))
	}
	plan := providerAccountAutomatedRotationPlan(items, now)
	candidates := providerAccountAutomatedRotationExecutionCandidates(items, now)
	if len(candidates) == 0 {
		writeError(w, http.StatusConflict, "no provider token rotation candidates are ready")
		return
	}

	accountsByID := make(map[string]GormProviderAccount, len(accounts))
	for _, account := range accounts {
		accountsByID[account.ID] = account
	}
	rotated := make([]map[string]any, 0, len(candidates))
	for _, candidate := range candidates {
		account := candidate.account
		accountID := rawStringFromMap(account, "id")
		currentTokenEnv := rawStringFromMap(account, "token_env")
		accountModel, ok := accountsByID[accountID]
		if !ok {
			writeError(w, http.StatusConflict, "provider account changed during token rotation execution; retry")
			return
		}
		next := providerAccountInput{
			Name:         rawStringFromMap(account, "name"),
			ProviderType: rawStringFromMap(account, "provider_type"),
			APIBaseURL:   rawStringFromMap(account, "api_base_url"),
			WebBaseURL:   rawStringFromMap(account, "web_base_url"),
			TokenEnv:     candidate.tokenEnv,
			DefaultOwner: rawStringFromMap(account, "default_owner"),
			Visibility:   rawStringFromMap(account, "visibility"),
			Metadata:     cloneMap(mapFromAny(account["metadata"])),
		}
		next.Metadata = providerAccountRotationMetadata(next.Metadata, currentTokenEnv, candidate.tokenEnv, reason, currentUser(r))
		result := s.store.Gorm.WithContext(r.Context()).Model(&GormProviderAccount{}).
			Where(map[string]any{"id": accountID, "token_env": currentTokenEnv}).
			Updates(map[string]any{"token_env": candidate.tokenEnv, "metadata": JSONValue{Data: next.Metadata}})
		if result.Error != nil {
			writeError(w, http.StatusInternalServerError, "could not execute provider token rotation")
			return
		}
		if result.RowsAffected == 0 {
			writeError(w, http.StatusConflict, "provider account changed during token rotation execution; retry")
			return
		}
		if err := s.refreshGitRemotesForProviderAccountGorm(r.Context(), next, accountID); err != nil {
			writeError(w, http.StatusInternalServerError, "could not refresh provider account remotes")
			return
		}
		accountModel.TokenEnv = candidate.tokenEnv
		accountModel.Metadata = JSONValue{Data: next.Metadata}
		rotated = append(rotated, providerAccountMap(accountModel, nil))
	}
	if _, err := s.store.SyncCanonicalAssets(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync provider account asset")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":                   "executed",
		"automation_enabled":     true,
		"provider_api_call_made": false,
		"rotated_count":          len(rotated),
		"skipped_count":          len(items) - len(rotated),
		"plan_before":            plan,
		"items":                  sanitizeProviderAccounts(rotated),
	})
}

type providerAccountInput struct {
	Name         string
	ProviderType string
	APIBaseURL   string
	WebBaseURL   string
	TokenEnv     string
	DefaultOwner string
	Visibility   string
	Metadata     map[string]any
}

func validateProviderAccountInput(ctx context.Context, name, providerType, apiBaseURL, webBaseURL, tokenEnv, defaultOwner, visibility string, metadata map[string]any) (providerAccountInput, error) {
	input := providerAccountInput{
		Name:         strings.TrimSpace(name),
		ProviderType: strings.ToLower(strings.TrimSpace(providerType)),
		APIBaseURL:   strings.TrimRight(strings.TrimSpace(apiBaseURL), "/"),
		WebBaseURL:   strings.TrimRight(strings.TrimSpace(webBaseURL), "/"),
		TokenEnv:     strings.TrimSpace(tokenEnv),
		DefaultOwner: strings.TrimSpace(defaultOwner),
		Visibility:   strings.ToLower(strings.TrimSpace(visibility)),
		Metadata:     metadata,
	}
	if input.Name == "" {
		return input, fmt.Errorf("name is required")
	}
	if input.ProviderType != "github" && input.ProviderType != "gitea" {
		return input, fmt.Errorf("provider_type must be github or gitea")
	}
	if input.APIBaseURL == "" {
		input.APIBaseURL = defaultProviderAccountAPIBase(input.ProviderType)
	}
	if err := validateTemplateProviderURL(ctx, input.APIBaseURL); err != nil {
		return input, fmt.Errorf("api_base_url is unsafe: %w", err)
	}
	if input.WebBaseURL != "" {
		if err := validateTemplateProviderURL(ctx, input.WebBaseURL); err != nil {
			return input, fmt.Errorf("web_base_url is unsafe: %w", err)
		}
	}
	if input.TokenEnv == "" {
		input.TokenEnv = defaultTemplateProviderTokenEnv(input.ProviderType)
	}
	if !safeTemplateProviderTokenEnv(input.ProviderType, input.TokenEnv) {
		return input, fmt.Errorf("token_env is not allowed for provider_type")
	}
	if input.Visibility == "" {
		input.Visibility = "private"
	}
	if input.Visibility != "public" && input.Visibility != "private" && input.Visibility != "internal" {
		return input, fmt.Errorf("visibility must be public, private, or internal")
	}
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	delete(input.Metadata, "token")
	delete(input.Metadata, "token_env")
	delete(input.Metadata, "secret")
	return input, nil
}

func providerAccountRotationMetadata(metadata map[string]any, oldEnv, newEnv, reason string, user *User) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	event := map[string]any{
		"rotated_at":             time.Now().UTC().Format(time.RFC3339),
		"previous_token_present": strings.TrimSpace(oldEnv) != "",
		"new_token_present":      strings.TrimSpace(newEnv) != "",
	}
	if reason != "" {
		event["reason"] = reason
	}
	if user != nil {
		event["rotated_by"] = user.ID
	}
	metadata["token_rotation"] = event
	delete(metadata, "token")
	delete(metadata, "token_env")
	delete(metadata, "secret")
	return metadata
}

func defaultProviderAccountAPIBase(provider string) string {
	if provider == "github" {
		return "https://api.github.com"
	}
	return ""
}

func (s *Server) refreshGitRemotesForProviderAccountGorm(ctx context.Context, input providerAccountInput, accountID string) error {
	metadataPatch := map[string]any{
		"provider_account_id": accountID,
		"api_base_url":        input.APIBaseURL,
		"token_env":           input.TokenEnv,
		"visibility":          input.Visibility,
	}
	if input.DefaultOwner != "" {
		metadataPatch["owner"] = input.DefaultOwner
	}
	var remotes []GormGitRemote
	if err := s.store.Gorm.WithContext(ctx).Where(map[string]any{"source_account_id": accountID}).Find(&remotes).Error; err != nil {
		return err
	}
	for _, remote := range remotes {
		metadata := mapFromAny(remote.Metadata.Data)
		for key, value := range metadataPatch {
			metadata[key] = value
		}
		if err := s.store.Gorm.WithContext(ctx).Model(&remote).Update("metadata", JSONValue{Data: metadata}).Error; err != nil {
			return err
		}
	}
	return nil
}

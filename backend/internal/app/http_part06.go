package app

import (
	"errors"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"strings"
	"time"
)

func (s *Server) listProviderAccounts(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "provider_account"}, "read") {
		return
	}
	var accounts []GormProviderAccount
	err := s.store.Gorm.WithContext(r.Context()).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "provider_type"}}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: "name"}}).
		Find(&accounts).Error
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	credentials, err := s.connectionCredentialsForProviderAccounts(r.Context(), accounts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	items := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		items = append(items, providerAccountMap(account, credentials[account.CredentialID.String]))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":                  sanitizeProviderAccounts(items),
		"token_rotation_summary": providerAccountTokenRotationPlanSummary(items, time.Now().UTC()),
		"token_rotation_plan":    providerAccountAutomatedRotationPlan(items, time.Now().UTC()),
	})
}

func (s *Server) getProviderAccount(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "provider_account", ID: chi.URLParam(r, "id")}, "read") {
		return
	}
	var account GormProviderAccount
	if err := s.store.Gorm.WithContext(r.Context()).Where(map[string]any{"id": chi.URLParam(r, "id")}).First(&account).Error; err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	credential, err := s.connectionCredentialByID(r.Context(), account.CredentialID.String)
	if err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, sanitizeProviderAccount(providerAccountMap(account, credential)))
}

func (s *Server) createProviderAccount(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "provider_account"}, "create") {
		return
	}
	var req struct {
		Name         string         `json:"name"`
		ProviderType string         `json:"provider_type"`
		APIBaseURL   string         `json:"api_base_url"`
		WebBaseURL   string         `json:"web_base_url"`
		TokenEnv     string         `json:"token_env"`
		CredentialID string         `json:"credential_id"`
		DefaultOwner string         `json:"default_owner"`
		Visibility   string         `json:"visibility"`
		Enabled      *bool          `json:"enabled"`
		Metadata     map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	input, err := validateProviderAccountInput(r.Context(), req.Name, req.ProviderType, req.APIBaseURL, req.WebBaseURL, req.TokenEnv, req.DefaultOwner, req.Visibility, req.Metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	credentialID := cleanOptionalID(req.CredentialID)
	if credentialID != "" && strings.TrimSpace(req.TokenEnv) == "" {
		input.TokenEnv = ""
	}
	var credential *GormConnectionCredential
	if credentialID != "" {
		credential, err = s.connectionCredentialForProjectOrGlobal(r.Context(), "", credentialID, "provider_token")
		if err != nil {
			writeError(w, http.StatusBadRequest, "credential_id must reference a global provider token credential")
			return
		}
	}
	account := GormProviderAccount{
		Name:         input.Name,
		ProviderType: input.ProviderType,
		APIBaseURL:   input.APIBaseURL,
		WebBaseURL:   input.WebBaseURL,
		TokenEnv:     input.TokenEnv,
		CredentialID: validNullString(credentialID),
		DefaultOwner: input.DefaultOwner,
		Visibility:   input.Visibility,
		Enabled:      enabled,
		Metadata:     JSONValue{Data: input.Metadata},
	}
	if err := s.store.Gorm.WithContext(r.Context()).Create(&account).Error; err != nil {
		writeError(w, http.StatusBadRequest, "could not create provider account")
		return
	}
	if _, err := s.store.SyncCanonicalAssets(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync provider account asset")
		return
	}
	writeJSON(w, http.StatusCreated, sanitizeProviderAccount(providerAccountMap(account, credential)))
}

func (s *Server) updateProviderAccount(w http.ResponseWriter, r *http.Request) {
	if !s.requirePolicy(w, r, PolicyResource{Type: "provider_account", ID: chi.URLParam(r, "id")}, "update") {
		return
	}
	var currentAccount GormProviderAccount
	if err := s.store.Gorm.WithContext(r.Context()).Where(map[string]any{"id": chi.URLParam(r, "id")}).First(&currentAccount).Error; err != nil {
		writeQueryOne(w, nil, err)
		return
	}
	current := providerAccountConfigFromGorm(currentAccount)
	var req struct {
		Name         *string         `json:"name"`
		ProviderType *string         `json:"provider_type"`
		APIBaseURL   *string         `json:"api_base_url"`
		WebBaseURL   *string         `json:"web_base_url"`
		TokenEnv     *string         `json:"token_env"`
		CredentialID *string         `json:"credential_id"`
		DefaultOwner *string         `json:"default_owner"`
		Visibility   *string         `json:"visibility"`
		Enabled      *bool           `json:"enabled"`
		Metadata     *map[string]any `json:"metadata"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := firstNonEmptyString(stringPtrValue(req.Name), current.Name)
	providerType := firstNonEmptyString(stringPtrValue(req.ProviderType), current.ProviderType)
	apiBaseURL := firstNonEmptyString(stringPtrValue(req.APIBaseURL), current.APIBaseURL)
	webBaseURL := firstNonEmptyString(stringPtrValue(req.WebBaseURL), current.WebBaseURL)
	tokenEnv := firstNonEmptyString(stringPtrValue(req.TokenEnv), current.TokenEnv)
	defaultOwner := firstNonEmptyString(stringPtrValue(req.DefaultOwner), current.DefaultOwner)
	visibility := firstNonEmptyString(stringPtrValue(req.Visibility), current.Visibility)
	metadata := cloneMap(current.Metadata)
	if req.Metadata != nil {
		metadata = mergeMaps(metadata, *req.Metadata)
	}
	input, err := validateProviderAccountInput(r.Context(), name, providerType, apiBaseURL, webBaseURL, tokenEnv, defaultOwner, visibility, metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	enabled := current.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	credentialID := stringPtrValue(req.CredentialID)
	if req.CredentialID == nil {
		credentialID = current.CredentialID
	}
	credentialID = cleanOptionalID(credentialID)
	var credential *GormConnectionCredential
	if credentialID != "" {
		credential, err = s.connectionCredentialForProjectOrGlobal(r.Context(), "", credentialID, "provider_token")
		if err != nil {
			writeError(w, http.StatusBadRequest, "credential_id must reference a global provider token credential")
			return
		}
	}
	updated := map[string]any{
		"name":          input.Name,
		"provider_type": input.ProviderType,
		"api_base_url":  input.APIBaseURL,
		"web_base_url":  input.WebBaseURL,
		"token_env":     input.TokenEnv,
		"credential_id": validNullString(credentialID),
		"default_owner": input.DefaultOwner,
		"visibility":    input.Visibility,
		"enabled":       enabled,
		"metadata":      JSONValue{Data: input.Metadata},
	}
	result := s.store.Gorm.WithContext(r.Context()).Model(&GormProviderAccount{}).
		Where(map[string]any{"id": chi.URLParam(r, "id"), "token_env": current.TokenEnv}).
		Updates(updated)
	if result.Error != nil {
		writeError(w, http.StatusBadRequest, "could not update provider account")
		return
	}
	if result.RowsAffected == 0 {
		writeError(w, http.StatusConflict, "provider account changed during update; retry")
		return
	}
	if err := s.refreshGitRemotesForProviderAccountGorm(r.Context(), input, chi.URLParam(r, "id")); err != nil {
		writeError(w, http.StatusInternalServerError, "could not refresh provider account remotes")
		return
	}
	currentAccount.Name = input.Name
	currentAccount.ProviderType = input.ProviderType
	currentAccount.APIBaseURL = input.APIBaseURL
	currentAccount.WebBaseURL = input.WebBaseURL
	currentAccount.TokenEnv = input.TokenEnv
	currentAccount.CredentialID = validNullString(credentialID)
	currentAccount.DefaultOwner = input.DefaultOwner
	currentAccount.Visibility = input.Visibility
	currentAccount.Enabled = enabled
	currentAccount.Metadata = JSONValue{Data: input.Metadata}
	if _, err := s.store.SyncCanonicalAssets(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync provider account asset")
		return
	}
	writeJSON(w, http.StatusOK, sanitizeProviderAccount(providerAccountMap(currentAccount, credential)))
}

func (s *Server) checkProviderAccount(w http.ResponseWriter, r *http.Request) {
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
	if account.CredentialID != "" {
		credential, err := s.connectionCredentialForProjectOrGlobal(r.Context(), "", account.CredentialID, "provider_token")
		if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, http.StatusInternalServerError, "could not load provider account credential")
			return
		}
		if credential != nil {
			account.CredentialCiphertext = credential.SecretCiphertext
		}
	}
	check := runProviderAccountCheck(r.Context(), account, newTemplateProviderHTTPClient())
	metadata := cloneMap(account.Metadata)
	metadata["provider_check"] = check
	result := s.store.Gorm.WithContext(r.Context()).Model(&GormProviderAccount{}).
		Where(map[string]any{"id": accountID, "token_env": account.TokenEnv}).
		Updates(map[string]any{"metadata": JSONValue{Data: metadata}})
	if result.Error != nil {
		writeError(w, http.StatusInternalServerError, "could not store provider account check")
		return
	}
	if result.RowsAffected == 0 {
		writeError(w, http.StatusConflict, "provider account changed during check; retry")
		return
	}
	accountModel.Metadata = JSONValue{Data: metadata}
	if _, err := s.store.SyncCanonicalAssets(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync provider account asset")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"check": check, "account": sanitizeProviderAccount(providerAccountMap(accountModel, nil))})
}

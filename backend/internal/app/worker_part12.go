package app

import (
	"context"
	"database/sql"
	"fmt"
	"gorm.io/gorm"
	"strings"
)

func createTemplateRemoteGorm(ctx context.Context, store *Store, tx *gorm.DB, repo, item map[string]any) (map[string]any, error) {
	remoteKey := firstNonEmptyString(stringFromMap(item, "remote_key"), stringFromMap(item, "name"))
	name := firstNonEmptyString(stringFromMap(item, "name"), remoteKey)
	kind := firstNonEmptyString(stringFromMap(item, "kind"), stringFromMap(item, "provider_type"), "git")
	providerType := firstNonEmptyString(stringFromMap(item, "provider_type"), kind)
	remoteRole := firstNonEmptyString(stringFromMap(item, "remote_role"), stringFromMap(item, "role"), "mirror")
	defaultBranch := firstNonEmptyString(stringFromMap(item, "default_branch"), fmt.Sprint(repo["default_branch"]), "main")
	remoteURL := stringFromMap(item, "remote_url")
	urls := stringSliceFromAny(item["urls"])
	if remoteURL == "" && len(urls) > 0 {
		remoteURL = urls[0]
	}
	account, hasAccount, err := resolveTemplateProviderAccount(ctx, store, item, providerType)
	if err != nil {
		return nil, err
	}
	metadata := mapFromAny(item["metadata"])
	metadata["source"] = "project_template"
	metadata["template_placeholder"] = true
	sourceAccountID := sql.NullString{}
	if hasAccount {
		sourceAccountID = validNullString(account.ID)
		metadata["provider_account_id"] = account.ID
		metadata["provider_account_name"] = account.Name
		metadata["api_base_url"] = account.APIBaseURL
		metadata["token_env"] = account.TokenEnv
		if stringFromMap(metadata, "owner", "org") == "" && account.DefaultOwner != "" {
			metadata["owner"] = account.DefaultOwner
		}
		if stringFromMap(metadata, "visibility") == "" && account.Visibility != "" {
			metadata["visibility"] = account.Visibility
		}
	}
	remote := GormGitRemote{
		ProjectGitRepositoryID: cleanOptionalID(fmt.Sprint(repo["id"])),
		Name:                   name,
		Kind:                   kind,
		RemoteKey:              remoteKey,
		ProviderType:           providerType,
		RemoteURL:              remoteURL,
		WebURL:                 stringFromMap(item, "web_url"),
		RemoteRole:             remoteRole,
		IsPrimary:              boolFromMap(item, "is_primary"),
		SyncEnabled:            boolDefaultFromMap(item, "sync_enabled", true),
		Protected:              boolFromMap(item, "protected"),
		LatestSHA:              stringFromMap(item, "latest_sha"),
		LastSyncStatus:         "never",
		SourceAccountID:        sourceAccountID,
		URLs:                   JSONValue{Data: urls},
		DefaultBranch:          defaultBranch,
		Metadata:               JSONValue{Data: metadata},
	}
	if err := tx.WithContext(ctx).Create(&remote).Error; err != nil {
		return nil, err
	}
	return gitRemoteMap(remote, nil, ""), nil
}

func createTemplateRepoSyncAssetGorm(ctx context.Context, tx *gorm.DB, opID string, project, repo map[string]any, remotes []map[string]any, defaults, parameters map[string]any) (map[string]any, error) {
	syncParams := mapFromAny(parameters["repo_sync"])
	syncDefaults := mapFromAny(defaults["repo_sync"])
	sourceRemoteID := firstNonEmptyString(stringFromMap(syncParams, "source_remote_id"), remoteIDByKey(remotes, firstNonEmptyString(stringFromMap(syncParams, "source_remote_key"), stringFromMap(syncDefaults, "source_remote_key"))))
	targetRemoteID := firstNonEmptyString(stringFromMap(syncParams, "target_remote_id"), remoteIDByKey(remotes, firstNonEmptyString(stringFromMap(syncParams, "target_remote_key"), stringFromMap(syncDefaults, "target_remote_key"))))
	if sourceRemoteID == "" || targetRemoteID == "" {
		if err := logTemplateRepoSyncSkippedGorm(ctx, tx, opID, map[string]any{"reason": "source and target remotes are required", "source_remote_id": sourceRemoteID, "target_remote_id": targetRemoteID}); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if sourceRemoteID == targetRemoteID {
		if err := logTemplateRepoSyncSkippedGorm(ctx, tx, opID, map[string]any{"reason": "source and target remotes must differ", "remote_id": sourceRemoteID}); err != nil {
			return nil, err
		}
		return nil, nil
	}
	repoID := cleanOptionalID(fmt.Sprint(repo["id"]))
	if ok, err := verifyTemplateRemoteForRepositoryGorm(ctx, tx, opID, repoID, sourceRemoteID, "source_remote_id"); err != nil || !ok {
		return nil, err
	}
	if ok, err := verifyTemplateRemoteForRepositoryGorm(ctx, tx, opID, repoID, targetRemoteID, "target_remote_id"); err != nil || !ok {
		return nil, err
	}
	enabled := false
	if value, ok := syncParams["enabled"].(bool); ok {
		enabled = value
	} else if value, ok := syncDefaults["enabled"].(bool); ok {
		enabled = value
	}
	asset := GormRepoSyncAsset{
		ProjectID:              cleanOptionalID(fmt.Sprint(project["id"])),
		ProjectGitRepositoryID: repoID,
		Name:                   firstNonEmptyString(stringFromMap(syncParams, "name"), stringFromMap(syncDefaults, "name"), "default mirror"),
		SourceRemoteID:         sourceRemoteID,
		TargetRemoteID:         targetRemoteID,
		TriggerMode:            firstNonEmptyString(stringFromMap(syncParams, "trigger_mode"), stringFromMap(syncDefaults, "trigger_mode"), "manual"),
		SyncMode:               firstNonEmptyString(stringFromMap(syncParams, "sync_mode"), stringFromMap(syncDefaults, "sync_mode"), "selected_refs"),
		Transport:              firstNonEmptyString(stringFromMap(syncParams, "transport"), stringFromMap(syncDefaults, "transport"), "ssh"),
		Driver:                 firstNonEmptyString(stringFromMap(syncParams, "driver"), stringFromMap(syncDefaults, "driver"), "projectops_worker_git_ssh"),
		Refs:                   JSONValue{Data: mapFromAny(syncParams["refs"])},
		Enabled:                enabled,
		Metadata:               JSONValue{Data: map[string]any{"source": "project_template", "template_placeholder": true}},
	}
	if err := tx.WithContext(ctx).Create(&asset).Error; err != nil {
		return nil, err
	}
	return repoSyncAssetMap(asset), nil
}

func logTemplateRepoSyncSkippedGorm(ctx context.Context, tx *gorm.DB, opID string, fields map[string]any) error {
	return tx.WithContext(ctx).Create(&GormOperationLog{OperationRunID: validNullString(opID), Level: "warn", Message: "template repo sync asset was not created", Fields: JSONValue{Data: fields}}).Error
}

func createTemplateFilesGorm(ctx context.Context, tx *gorm.DB, run, project, repo, defaults, parameters map[string]any) ([]map[string]any, error) {
	items := templateFileItems(defaults, parameters)
	files := make([]map[string]any, 0, len(items))
	for _, item := range items {
		file, err := createTemplateFileGorm(ctx, tx, run, project, repo, item)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func createTemplateFileGorm(ctx context.Context, tx *gorm.DB, run, project, repo, item map[string]any) (map[string]any, error) {
	path := safeTemplateFilePath(stringFromMap(item, "path"))
	if path == "" {
		return nil, fmt.Errorf("template file path is required")
	}
	metadata := mapFromAny(item["metadata"])
	metadata["source"] = "project_template"
	metadata["template_placeholder"] = true
	file := GormProjectTemplateFile{
		ProjectTemplateRunID:   validNullString(cleanOptionalID(fmt.Sprint(run["id"]))),
		ProjectTemplateID:      validNullString(cleanOptionalID(fmt.Sprint(run["project_template_id"]))),
		ProjectID:              validNullString(cleanOptionalID(fmt.Sprint(project["id"]))),
		ProjectGitRepositoryID: validNullString(cleanOptionalID(fmt.Sprint(repo["id"]))),
		Path:                   path,
		Kind:                   firstNonEmptyString(stringFromMap(item, "kind"), "text"),
		Content:                renderTemplateFileContent(stringFromMap(item, "content"), run, project, repo),
		Status:                 "planned",
		Metadata:               JSONValue{Data: metadata},
	}
	if err := tx.WithContext(ctx).Create(&file).Error; err != nil {
		return nil, err
	}
	return projectTemplateFileMap(file), nil
}

func verifyTemplateRemoteForRepositoryGorm(ctx context.Context, tx *gorm.DB, opID, repoID, remoteID, field string) (bool, error) {
	var remote GormGitRemote
	err := tx.WithContext(ctx).Where(&GormGitRemote{ProjectGitRepositoryID: repoID}).Where(map[string]any{"id": remoteID}).First(&remote).Error
	if err == nil {
		return true, nil
	}
	if !errorsIsRecordNotFound(err) {
		return false, err
	}
	fields := map[string]any{"field": field, "remote_id": remoteID, "repo_id": repoID}
	if logErr := tx.WithContext(ctx).Create(&GormOperationLog{OperationRunID: validNullString(opID), Level: "warn", Message: "template repo sync remote does not belong to the created repository", Fields: JSONValue{Data: fields}}).Error; logErr != nil {
		return false, logErr
	}
	return false, nil
}

func templateRemoteItems(defaults, parameters map[string]any) []map[string]any {
	items := mapSliceFromAny(parameters["remotes"])
	if len(items) == 0 {
		items = mapSliceFromAny(defaults["remotes"])
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if firstNonEmptyString(stringFromMap(item, "remote_key"), stringFromMap(item, "name")) == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func resolveTemplateProviderAccount(ctx context.Context, store *Store, item map[string]any, providerType string) (providerAccountConfig, bool, error) {
	accountID := strings.TrimSpace(stringFromMap(item, "provider_account_id"))
	accountName := strings.TrimSpace(stringFromMap(item, "provider_account_name"))
	if accountID == "" && accountName == "" {
		return providerAccountConfig{}, false, nil
	}
	if store == nil || store.Gorm == nil {
		return providerAccountConfig{}, false, fmt.Errorf("gorm store is not initialized")
	}
	var accountModel GormProviderAccount
	query := store.Gorm.WithContext(ctx)
	var err error
	if accountID != "" {
		err = query.Where(map[string]any{"id": accountID}).First(&accountModel).Error
	} else {
		err = query.Where(map[string]any{"name": accountName}).First(&accountModel).Error
	}
	account := providerAccountConfigFromGorm(accountModel)
	if err != nil {
		return account, false, fmt.Errorf("loading provider account for template remote: %w", err)
	}
	if !account.Enabled {
		return account, false, fmt.Errorf("provider account %q is disabled", account.Name)
	}
	provider := strings.ToLower(strings.TrimSpace(providerType))
	if provider != account.ProviderType {
		return account, false, fmt.Errorf("provider account %q is %s but template remote is %s", account.Name, account.ProviderType, provider)
	}
	if !safeTemplateProviderTokenEnv(account.ProviderType, account.TokenEnv) {
		return account, false, fmt.Errorf("provider account %q has an unsafe token_env", account.Name)
	}
	return account, true, nil
}

func remoteIDByKey(remotes []map[string]any, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	for _, remote := range remotes {
		if stringFromMap(remote, "remote_key") == key || stringFromMap(remote, "name") == key {
			return strings.TrimSpace(fmt.Sprint(remote["id"]))
		}
	}
	return ""
}

func templateFileItems(defaults, parameters map[string]any) []map[string]any {
	items := mapSliceFromAny(parameters["files"])
	if len(items) == 0 {
		items = mapSliceFromAny(defaults["files"])
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if safeTemplateFilePath(stringFromMap(item, "path")) == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func renderTemplateFileContent(content string, run, project, repo map[string]any) string {
	replacements := map[string]string{
		"{{project_name}}":   strings.TrimSpace(fmt.Sprint(project["name"])),
		"{{project_slug}}":   strings.TrimSpace(fmt.Sprint(project["slug"])),
		"{{template_slug}}":  strings.TrimSpace(fmt.Sprint(run["template_slug"])),
		"{{repository_key}}": strings.TrimSpace(fmt.Sprint(repo["repo_key"])),
	}
	for token, value := range replacements {
		content = strings.ReplaceAll(content, token, value)
	}
	return content
}

func safeTemplateFilePath(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	path = strings.TrimPrefix(path, "/")
	if path == "" || path == "." || strings.Contains(path, "\x00") {
		return ""
	}
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return ""
		}
	}
	return path
}

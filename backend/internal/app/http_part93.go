package app

import (
	"context"
	"database/sql"
	"errors"
	"gorm.io/gorm"
	"strings"
)

func applyGitRepositoryPatch(repo *GormProjectGitRepository, req struct {
	Name          *string `json:"name"`
	RepoKey       *string `json:"repo_key"`
	DisplayName   *string `json:"display_name"`
	RepoRole      *string `json:"repo_role"`
	Status        *string `json:"status"`
	Description   *string `json:"description"`
	DefaultBranch *string `json:"default_branch"`
}) {
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		repo.Name = strings.TrimSpace(*req.Name)
	}
	if req.RepoKey != nil && strings.TrimSpace(*req.RepoKey) != "" {
		repo.RepoKey = strings.TrimSpace(*req.RepoKey)
	}
	if req.DisplayName != nil {
		repo.DisplayName = *req.DisplayName
	}
	if req.RepoRole != nil && strings.TrimSpace(*req.RepoRole) != "" {
		repo.RepoRole = strings.TrimSpace(*req.RepoRole)
	}
	if req.Status != nil && strings.TrimSpace(*req.Status) != "" {
		repo.Status = strings.TrimSpace(*req.Status)
	}
	if req.Description != nil {
		repo.Description = *req.Description
	}
	if req.DefaultBranch != nil && strings.TrimSpace(*req.DefaultBranch) != "" {
		repo.DefaultBranch = strings.TrimSpace(*req.DefaultBranch)
	}
}

func (s *Server) projectIDForRepositoryGorm(ctx context.Context, repoID string) (string, error) {
	repoID = cleanOptionalID(repoID)
	if repoID == "" {
		return "", ErrNotFound
	}
	var repo GormProjectGitRepository
	err := s.store.Gorm.WithContext(ctx).Where(map[string]any{"id": repoID}).First(&repo).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if cleanOptionalID(repo.ProjectID) == "" {
		return "", ErrNotFound
	}
	return repo.ProjectID, nil
}

func (s *Server) gitRemoteWithProjectGorm(ctx context.Context, remoteID string) (GormGitRemote, string, error) {
	remoteID = cleanOptionalID(remoteID)
	if remoteID == "" {
		return GormGitRemote{}, "", ErrNotFound
	}
	var remote GormGitRemote
	err := s.store.Gorm.WithContext(ctx).Where(map[string]any{"id": remoteID}).First(&remote).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return remote, "", ErrNotFound
	}
	if err != nil {
		return remote, "", err
	}
	projectID, err := s.projectIDForRepositoryGorm(ctx, remote.ProjectGitRepositoryID)
	return remote, projectID, err
}

func (s *Server) connectionCredentialsForGitRemotes(ctx context.Context, remotes []GormGitRemote) (map[string]*GormConnectionCredential, error) {
	ids := make([]string, 0, len(remotes))
	seen := map[string]bool{}
	for _, remote := range remotes {
		id := cleanOptionalID(remote.CredentialID.String)
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
		if !isGitRemoteCredentialKind(credential.Kind) {
			continue
		}
		out[credential.ID] = &credential
	}
	return out, nil
}

func (s *Server) connectionCredentialsForArgoConnections(ctx context.Context, connections []GormArgoConnection) (map[string]*GormConnectionCredential, error) {
	ids := make([]string, 0, len(connections))
	seen := map[string]bool{}
	for _, connection := range connections {
		id := cleanOptionalID(connection.CredentialID.String)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return s.connectionCredentialsByIDs(ctx, ids)
}

func argoConnectionMaps(connections []GormArgoConnection, credentials map[string]*GormConnectionCredential) []map[string]any {
	items := make([]map[string]any, 0, len(connections))
	for _, connection := range connections {
		items = append(items, argoConnectionMap(connection, credentials[connection.CredentialID.String]))
	}
	return items
}

func argoConnectionMap(connection GormArgoConnection, credential *GormConnectionCredential) map[string]any {
	item := map[string]any{
		"id":                    connection.ID,
		"project_id":            connection.ProjectID,
		"name":                  connection.Name,
		"server_url":            connection.ServerURL,
		"auth_type":             connection.AuthType,
		"credential_id":         nullableStringValue(connection.CredentialID),
		"config":                mapFromAny(connection.Config.Data),
		"last_sync_status":      connection.LastSyncStatus,
		"last_sync_error":       connection.LastSyncError,
		"created_at":            connection.CreatedAt,
		"updated_at":            connection.UpdatedAt,
		"credential_configured": false,
	}
	if credential != nil {
		item["credential_name"] = credential.Name
		item["credential_kind"] = credential.Kind
		item["credential_configured"] = credential.SecretCiphertext != ""
	}
	return item
}

func (s *Server) connectionCredentialsForSSHMachine(ctx context.Context, machines []GormSSHMachine) (map[string]*GormConnectionCredential, error) {
	ids := make([]string, 0, len(machines))
	seen := map[string]bool{}
	for _, machine := range machines {
		id := cleanOptionalID(machine.CredentialID.String)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return s.connectionCredentialsByIDs(ctx, ids)
}

func sshMachineMaps(machines []GormSSHMachine, credentials map[string]*GormConnectionCredential) []map[string]any {
	items := make([]map[string]any, 0, len(machines))
	for _, machine := range machines {
		items = append(items, sshMachineMap(machine, credentials[machine.CredentialID.String]))
	}
	return items
}

func sshMachineMap(machine GormSSHMachine, credential *GormConnectionCredential) map[string]any {
	item := map[string]any{
		"id":                    machine.ID,
		"project_id":            machine.ProjectID,
		"name":                  machine.Name,
		"host":                  machine.Host,
		"port":                  machine.Port,
		"username":              machine.Username,
		"auth_type":             machine.AuthType,
		"credential_id":         nullableStringValue(machine.CredentialID),
		"metadata":              mapFromAny(machine.Metadata.Data),
		"created_at":            machine.CreatedAt,
		"updated_at":            machine.UpdatedAt,
		"credential_configured": false,
	}
	if credential != nil {
		item["credential_name"] = credential.Name
		item["credential_kind"] = credential.Kind
		item["credential_configured"] = credential.SecretCiphertext != ""
	}
	return item
}

func (s *Server) connectionCredentialsByIDs(ctx context.Context, ids []string) (map[string]*GormConnectionCredential, error) {
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

func gitRemoteMaps(remotes []GormGitRemote, credentials map[string]*GormConnectionCredential, projectID string) []map[string]any {
	items := make([]map[string]any, 0, len(remotes))
	for _, remote := range remotes {
		items = append(items, gitRemoteMap(remote, credentials[remote.CredentialID.String], projectID))
	}
	return items
}

func gitRemoteMap(remote GormGitRemote, credential *GormConnectionCredential, projectID string) map[string]any {
	item := map[string]any{
		"id":                        remote.ID,
		"project_id":                nullableStringValue(sql.NullString{String: projectID, Valid: cleanOptionalID(projectID) != ""}),
		"project_git_repository_id": remote.ProjectGitRepositoryID,
		"name":                      remote.Name,
		"kind":                      remote.Kind,
		"remote_key":                remote.RemoteKey,
		"provider_type":             remote.ProviderType,
		"source_provider_id":        nullableStringValue(remote.SourceProviderID),
		"source_account_id":         nullableStringValue(remote.SourceAccountID),
		"credential_id":             nullableStringValue(remote.CredentialID),
		"remote_url":                remote.RemoteURL,
		"web_url":                   remote.WebURL,
		"remote_role":               remote.RemoteRole,
		"is_primary":                remote.IsPrimary,
		"sync_enabled":              remote.SyncEnabled,
		"protected":                 remote.Protected,
		"latest_sha":                remote.LatestSHA,
		"last_sync_status":          remote.LastSyncStatus,
		"urls":                      stringSliceFromAny(remote.URLs.Data),
		"default_branch":            remote.DefaultBranch,
		"metadata":                  mapFromAny(remote.Metadata.Data),
		"created_at":                remote.CreatedAt,
		"updated_at":                remote.UpdatedAt,
		"credential_configured":     false,
		"credential_name":           "",
		"credential_kind":           "",
	}
	if credential != nil {
		item["credential_name"] = credential.Name
		item["credential_kind"] = credential.Kind
		item["credential_configured"] = credential.SecretCiphertext != ""
	}
	return item
}

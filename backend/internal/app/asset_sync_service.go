package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

type AssetSyncResult struct {
	SyncedAssets            int `json:"synced_assets"`
	InsertedRelations       int `json:"inserted_relations"`
	PrunedRelations         int `json:"pruned_relations"`
	InsertedStatusSnapshots int `json:"inserted_status_snapshots"`
}

type AssetGraphRepairReport struct {
	TotalRelations           int    `json:"total_relations"`
	DerivedRelations         int    `json:"derived_relations"`
	ManualRelations          int    `json:"manual_relations"`
	DanglingRelations        int    `json:"dangling_relations"`
	DanglingDerivedRelations int    `json:"dangling_derived_relations"`
	DanglingManualRelations  int    `json:"dangling_manual_relations"`
	ReportError              string `json:"report_error,omitempty"`
}

type AssetSyncReport struct {
	AssetSyncResult
	GraphRepair AssetGraphRepairReport `json:"graph_repair"`
}

func (s *Store) SyncCanonicalAssets(ctx context.Context) (AssetSyncResult, error) {
	if s == nil || s.Gorm == nil {
		return AssetSyncResult{}, fmt.Errorf("gorm store is not initialized")
	}
	return syncCanonicalAssetsGorm(ctx, s.Gorm)
}

func (s *Store) SyncCanonicalAssetsReport(ctx context.Context) (AssetSyncReport, error) {
	result, err := s.SyncCanonicalAssets(ctx)
	if err != nil {
		return AssetSyncReport{AssetSyncResult: result}, err
	}
	graphRepair, err := assetGraphRepairReportGorm(ctx, s.Gorm)
	if err != nil {
		graphRepair.ReportError = err.Error()
		return AssetSyncReport{AssetSyncResult: result, GraphRepair: graphRepair}, nil
	}
	return AssetSyncReport{AssetSyncResult: result, GraphRepair: graphRepair}, nil
}

type canonicalAssetSpec struct {
	ProjectID   string
	AssetType   string
	SourceTable string
	SourceID    string
	Name        string
	DisplayName string
	Description string
	Source      string
	ExternalID  string
	Status      string
	RiskLevel   string
	Metadata    map[string]any
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type canonicalRelationSpec struct {
	ProjectID     string
	FromAssetType string
	FromTable     string
	FromID        string
	ToAssetType   string
	ToTable       string
	ToID          string
	RelationType  string
	Metadata      map[string]any
	CreatedAt     time.Time
}

func syncCanonicalAssetsGorm(ctx context.Context, db *gorm.DB) (AssetSyncResult, error) {
	if db == nil {
		return AssetSyncResult{}, fmt.Errorf("gorm store is not initialized")
	}
	assets, relations := canonicalAssetSpecs(ctx, db)
	return syncCanonicalAssetSpecs(ctx, db, assets, relations)
}

func syncWorkerNodeCanonicalAssetGorm(ctx context.Context, db *gorm.DB, workerNodeID string) (AssetSyncResult, error) {
	if db == nil {
		return AssetSyncResult{}, fmt.Errorf("gorm store is not initialized")
	}
	var node GormWorkerNode
	if err := db.WithContext(ctx).Where(&GormWorkerNode{GormBase: GormBase{ID: workerNodeID}}).First(&node).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AssetSyncResult{}, nil
		}
		return AssetSyncResult{}, fmt.Errorf("loading worker node asset: %w", err)
	}
	assets := []canonicalAssetSpec{workerNodeAssetSpec(node)}
	return syncCanonicalAssetSpecs(ctx, db, assets, nil)
}

func syncCanonicalAssetSpecs(ctx context.Context, db *gorm.DB, specs []canonicalAssetSpec, relationSpecs ...[]canonicalRelationSpec) (AssetSyncResult, error) {
	result := AssetSyncResult{}
	assetByKey := map[string]GormAsset{}
	for _, spec := range specs {
		asset, insertedSnapshot, err := upsertCanonicalAsset(ctx, db, spec)
		if err != nil {
			return result, err
		}
		result.SyncedAssets++
		if insertedSnapshot {
			result.InsertedStatusSnapshots++
		}
		assetByKey[assetKey(spec.AssetType, spec.SourceTable, spec.SourceID)] = asset
	}

	if len(relationSpecs) == 0 {
		return result, nil
	}
	desired := map[string]canonicalRelationSpec{}
	for _, spec := range relationSpecs[0] {
		fromAsset, ok := assetByKey[assetKey(spec.FromAssetType, spec.FromTable, spec.FromID)]
		if !ok {
			continue
		}
		toAsset, ok := assetByKey[assetKey(spec.ToAssetType, spec.ToTable, spec.ToID)]
		if !ok {
			continue
		}
		key := relationKey(fromAsset.ID, toAsset.ID, spec.RelationType)
		desired[key] = spec
		inserted, err := upsertCanonicalRelation(ctx, db, spec, fromAsset.ID, toAsset.ID)
		if err != nil {
			return result, err
		}
		if inserted {
			result.InsertedRelations++
		}
	}
	pruned, err := pruneDerivedRelations(ctx, db, desired, assetByKey)
	if err != nil {
		return result, err
	}
	result.PrunedRelations = pruned
	return result, nil
}

func canonicalAssetSpecs(ctx context.Context, db *gorm.DB) ([]canonicalAssetSpec, []canonicalRelationSpec) {
	assets := []canonicalAssetSpec{}
	relations := []canonicalRelationSpec{}
	var projects []GormProject
	_ = db.WithContext(ctx).Find(&projects).Error
	for _, project := range projects {
		assets = append(assets, canonicalAssetSpec{ProjectID: project.ID, AssetType: "project", SourceTable: "projects", SourceID: project.ID, Name: project.Slug, DisplayName: project.Name, Description: project.Description, Source: "local", ExternalID: project.Slug, Status: "active", RiskLevel: "normal", Metadata: map[string]any{"slug": project.Slug}, CreatedAt: project.CreatedAt, UpdatedAt: project.UpdatedAt})
	}

	var repos []GormProjectGitRepository
	_ = db.WithContext(ctx).Find(&repos).Error
	for _, repo := range repos {
		assets = append(assets, canonicalAssetSpec{ProjectID: repo.ProjectID, AssetType: "repository", SourceTable: "project_git_repositories", SourceID: repo.ID, Name: repo.RepoKey, DisplayName: firstNonEmpty(repo.DisplayName, repo.Name), Description: repo.Description, Source: "local", ExternalID: repo.RepoKey, Status: repo.Status, RiskLevel: "normal", Metadata: map[string]any{"repo_role": repo.RepoRole, "default_branch": repo.DefaultBranch}, CreatedAt: repo.CreatedAt, UpdatedAt: repo.UpdatedAt})
		relations = append(relations, canonicalRelationSpec{ProjectID: repo.ProjectID, FromAssetType: "project", FromTable: "projects", FromID: repo.ProjectID, ToAssetType: "repository", ToTable: "project_git_repositories", ToID: repo.ID, RelationType: "owns", Metadata: map[string]any{"source": "canonical_asset_sync"}, CreatedAt: repo.CreatedAt})
	}

	repoProject := map[string]string{}
	for _, repo := range repos {
		repoProject[repo.ID] = repo.ProjectID
	}
	var remotes []GormGitRemote
	_ = db.WithContext(ctx).Find(&remotes).Error
	for _, remote := range remotes {
		projectID := repoProject[remote.ProjectGitRepositoryID]
		assets = append(assets, canonicalAssetSpec{ProjectID: projectID, AssetType: "git_remote", SourceTable: "git_remotes", SourceID: remote.ID, Name: remote.RemoteKey, DisplayName: remote.Name, Description: remote.RemoteRole, Source: remote.ProviderType, ExternalID: firstNonEmpty(remote.WebURL, remote.RemoteURL), Status: firstNonEmpty(remote.LastSyncStatus, "unknown"), RiskLevel: boolRisk(remote.Protected), Metadata: map[string]any{"remote_role": remote.RemoteRole, "sync_enabled": remote.SyncEnabled, "is_primary": remote.IsPrimary, "default_branch": remote.DefaultBranch}, CreatedAt: remote.CreatedAt, UpdatedAt: remote.UpdatedAt})
		relations = append(relations, canonicalRelationSpec{ProjectID: projectID, FromAssetType: "repository", FromTable: "project_git_repositories", FromID: remote.ProjectGitRepositoryID, ToAssetType: "git_remote", ToTable: "git_remotes", ToID: remote.ID, RelationType: "has_remote", Metadata: map[string]any{"source": "canonical_asset_sync"}, CreatedAt: remote.CreatedAt})
	}

	var argoConnections []GormArgoConnection
	_ = db.WithContext(ctx).Find(&argoConnections).Error
	for _, item := range argoConnections {
		assets = append(assets, canonicalAssetSpec{ProjectID: item.ProjectID, AssetType: "argo_connection", SourceTable: "argo_connections", SourceID: item.ID, Name: item.Name, DisplayName: item.Name, Description: item.AuthType, Source: "argocd", ExternalID: item.ServerURL, Status: firstNonEmpty(item.LastSyncStatus, "unknown"), RiskLevel: "normal", Metadata: map[string]any{"auth_type": item.AuthType, "last_sync_error_present": item.LastSyncError != ""}, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt})
		relations = append(relations, canonicalRelationSpec{ProjectID: item.ProjectID, FromAssetType: "project", FromTable: "projects", FromID: item.ProjectID, ToAssetType: "argo_connection", ToTable: "argo_connections", ToID: item.ID, RelationType: "deploys_to", Metadata: map[string]any{"source": "canonical_asset_sync"}, CreatedAt: item.CreatedAt})
	}

	var sshMachines []GormSSHMachine
	_ = db.WithContext(ctx).Find(&sshMachines).Error
	for _, item := range sshMachines {
		assets = append(assets, canonicalAssetSpec{ProjectID: item.ProjectID, AssetType: "ssh_machine", SourceTable: "ssh_machines", SourceID: item.ID, Name: item.Name, DisplayName: item.Name, Description: item.Host, Source: "ssh", ExternalID: item.Host, Status: "configured", RiskLevel: "high", Metadata: map[string]any{"auth_type": item.AuthType, "port": item.Port, "username": item.Username}, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt})
		relations = append(relations, canonicalRelationSpec{ProjectID: item.ProjectID, FromAssetType: "project", FromTable: "projects", FromID: item.ProjectID, ToAssetType: "ssh_machine", ToTable: "ssh_machines", ToID: item.ID, RelationType: "targets", Metadata: map[string]any{"source": "canonical_asset_sync"}, CreatedAt: item.CreatedAt})
	}

	var runtimes []GormAIRuntime
	_ = db.WithContext(ctx).Find(&runtimes).Error
	for _, item := range runtimes {
		projectID := nullStringValue(item.ProjectID)
		assets = append(assets, canonicalAssetSpec{ProjectID: projectID, AssetType: "ai_runtime", SourceTable: "ai_runtimes", SourceID: item.ID, Name: item.Name, DisplayName: item.Name, Description: item.RuntimeType, Source: firstNonEmpty(item.ProviderType, "ai"), ExternalID: item.Model, Status: item.Status, RiskLevel: "normal", Metadata: map[string]any{"runtime_type": item.RuntimeType, "model": item.Model, "credential_configured": item.CredentialID.Valid}, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt})
		if projectID != "" {
			relations = append(relations, canonicalRelationSpec{ProjectID: projectID, FromAssetType: "project", FromTable: "projects", FromID: projectID, ToAssetType: "ai_runtime", ToTable: "ai_runtimes", ToID: item.ID, RelationType: "uses", Metadata: map[string]any{"source": "canonical_asset_sync"}, CreatedAt: item.CreatedAt})
		}
	}

	var nodes []GormWorkerNode
	_ = db.WithContext(ctx).Find(&nodes).Error
	for _, node := range nodes {
		assets = append(assets, workerNodeAssetSpec(node))
	}
	return assets, relations
}

func workerNodeAssetSpec(node GormWorkerNode) canonicalAssetSpec {
	return canonicalAssetSpec{AssetType: "node_agent", SourceTable: "worker_nodes", SourceID: node.ID, Name: node.Name, DisplayName: node.Name, Description: node.Kind, Source: "local", ExternalID: node.Name, Status: node.Status, RiskLevel: "normal", Metadata: map[string]any{"kind": node.Kind, "capabilities": []string(node.Capabilities), "last_heartbeat_at": node.LastHeartbeatAt}, CreatedAt: node.CreatedAt, UpdatedAt: node.UpdatedAt}
}

func upsertCanonicalAsset(ctx context.Context, db *gorm.DB, spec canonicalAssetSpec) (GormAsset, bool, error) {
	asset := GormAsset{AssetType: spec.AssetType, SourceTable: spec.SourceTable, SourceID: validNullString(spec.SourceID)}
	updates := GormAsset{ProjectID: validNullString(spec.ProjectID), Name: spec.Name, DisplayName: spec.DisplayName, Description: spec.Description, Source: firstNonEmpty(spec.Source, "local"), ExternalID: spec.ExternalID, Status: firstNonEmpty(spec.Status, "unknown"), RiskLevel: firstNonEmpty(spec.RiskLevel, "normal"), Metadata: JSONValue{Data: spec.Metadata}}
	if spec.CreatedAt.IsZero() {
		spec.CreatedAt = time.Now().UTC()
	}
	if spec.UpdatedAt.IsZero() {
		spec.UpdatedAt = spec.CreatedAt
	}
	updates.CreatedAt = spec.CreatedAt
	updates.UpdatedAt = spec.UpdatedAt
	if err := db.WithContext(ctx).Where(&asset).Assign(updates).FirstOrCreate(&asset).Error; err != nil {
		return asset, false, fmt.Errorf("upserting canonical asset %s/%s/%s: %w", spec.AssetType, spec.SourceTable, spec.SourceID, err)
	}
	inserted, err := insertStatusSnapshotIfChanged(ctx, db, asset, spec)
	return asset, inserted, err
}

func insertStatusSnapshotIfChanged(ctx context.Context, db *gorm.DB, asset GormAsset, spec canonicalAssetSpec) (bool, error) {
	raw := map[string]any{"asset_type": spec.AssetType, "source_table": spec.SourceTable, "source_id": spec.SourceID, "name": spec.Name, "metadata": spec.Metadata}
	var snapshots []GormAssetStatusSnapshot
	if err := db.WithContext(ctx).Where(&GormAssetStatusSnapshot{AssetID: asset.ID}).Find(&snapshots).Error; err != nil {
		return false, fmt.Errorf("loading asset status snapshots: %w", err)
	}
	var latest *GormAssetStatusSnapshot
	for i := range snapshots {
		if latest == nil || snapshots[i].CollectedAt.After(latest.CollectedAt) {
			latest = &snapshots[i]
		}
	}
	if latest != nil && latest.Status == asset.Status && latest.Health == asset.RiskLevel && jsonValuesEqual(latest.Raw.Data, raw) {
		return false, nil
	}
	snapshot := GormAssetStatusSnapshot{AssetID: asset.ID, Status: asset.Status, Health: asset.RiskLevel, Summary: fmt.Sprintf("%s %s is %s", asset.AssetType, asset.Name, asset.Status), Raw: JSONValue{Data: raw}, CollectedAt: time.Now().UTC()}
	if err := db.WithContext(ctx).Create(&snapshot).Error; err != nil {
		return false, fmt.Errorf("inserting asset status snapshot: %w", err)
	}
	return true, nil
}

func upsertCanonicalRelation(ctx context.Context, db *gorm.DB, spec canonicalRelationSpec, fromAssetID, toAssetID string) (bool, error) {
	relation := GormAssetRelation{FromAssetID: fromAssetID, ToAssetID: toAssetID, RelationType: spec.RelationType}
	createdAt := spec.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updates := GormAssetRelation{ProjectID: validNullString(spec.ProjectID), Metadata: JSONValue{Data: spec.Metadata}, CreatedAt: createdAt}
	if err := db.WithContext(ctx).Where(&relation).Assign(updates).FirstOrCreate(&relation).Error; err != nil {
		return false, fmt.Errorf("upserting canonical relation: %w", err)
	}
	return relation.CreatedAt.Equal(createdAt), nil
}

func pruneDerivedRelations(ctx context.Context, db *gorm.DB, desired map[string]canonicalRelationSpec, assetByKey map[string]GormAsset) (int, error) {
	var relations []GormAssetRelation
	if err := db.WithContext(ctx).Find(&relations).Error; err != nil {
		return 0, fmt.Errorf("loading asset relations: %w", err)
	}
	pruned := 0
	for _, relation := range relations {
		metadata := mapFromAny(relation.Metadata.Data)
		if metadata["source"] == "manual" {
			continue
		}
		if _, ok := desired[relationKey(relation.FromAssetID, relation.ToAssetID, relation.RelationType)]; ok {
			continue
		}
		if err := db.WithContext(ctx).Delete(&relation).Error; err != nil {
			return pruned, fmt.Errorf("pruning derived asset relation: %w", err)
		}
		pruned++
	}
	return pruned, nil
}

func assetGraphRepairReportGorm(ctx context.Context, db *gorm.DB) (AssetGraphRepairReport, error) {
	if db == nil {
		return AssetGraphRepairReport{}, fmt.Errorf("gorm store is not initialized")
	}
	var assets []GormAsset
	if err := db.WithContext(ctx).Find(&assets).Error; err != nil {
		return AssetGraphRepairReport{}, fmt.Errorf("loading assets: %w", err)
	}
	assetIDs := map[string]bool{}
	for _, asset := range assets {
		assetIDs[asset.ID] = true
	}
	var relations []GormAssetRelation
	if err := db.WithContext(ctx).Find(&relations).Error; err != nil {
		return AssetGraphRepairReport{}, fmt.Errorf("loading asset relations: %w", err)
	}
	report := AssetGraphRepairReport{TotalRelations: len(relations)}
	for _, relation := range relations {
		manual := mapFromAny(relation.Metadata.Data)["source"] == "manual"
		dangling := !assetIDs[relation.FromAssetID] || !assetIDs[relation.ToAssetID]
		if manual {
			report.ManualRelations++
		} else {
			report.DerivedRelations++
		}
		if dangling {
			report.DanglingRelations++
			if manual {
				report.DanglingManualRelations++
			} else {
				report.DanglingDerivedRelations++
			}
		}
	}
	return report, nil
}

func nullStringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func boolRisk(protected bool) string {
	if protected {
		return "high"
	}
	return "normal"
}

func assetKey(assetType, table, id string) string {
	return assetType + "|" + table + "|" + id
}

func relationKey(fromID, toID, relationType string) string {
	return fromID + "|" + toID + "|" + relationType
}

func jsonValuesEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

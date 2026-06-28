package app

import (
	"fmt"
	"sort"
	"strings"
)

func canonicalDemoReadinessPayload(assets []demoReadinessAssetRow, relations []demoReadinessRelationRow) (map[string]any, demoReadinessSnapshotPath) {
	graphIDByDBID := map[string]string{}
	dbIDByGraphID := map[string]string{}
	nodes := make([]any, 0, len(assets))
	for _, asset := range assets {
		graphID := demoReadinessGraphID(asset)
		if graphID == "" {
			continue
		}
		graphIDByDBID[asset.ID] = graphID
		dbIDByGraphID[graphID] = asset.ID
		nodes = append(nodes, map[string]any{"id": graphID, "asset_type": asset.AssetType, "status": asset.Status})
	}
	edges := make([]any, 0, len(relations))
	remotesByRepository := map[string][]string{}
	projectByRepository := map[string]string{}
	for _, relation := range relations {
		from := graphIDByDBID[relation.FromAssetID]
		to := graphIDByDBID[relation.ToAssetID]
		if from == "" || to == "" {
			continue
		}
		edges = append(edges, map[string]any{"from_asset_id": from, "to_asset_id": to, "relation_type": relation.RelationType})
		switch relation.RelationType {
		case "owns":
			if strings.HasPrefix(from, "project:") && strings.HasPrefix(to, "repository:") {
				projectByRepository[to] = from
			}
		case "has_remote":
			if strings.HasPrefix(from, "repository:") && strings.HasPrefix(to, "git_remote:") {
				remotesByRepository[from] = append(remotesByRepository[from], to)
			}
		}
	}
	path := demoReadinessSnapshotPath{}
	for graphID, dbID := range dbIDByGraphID {
		if strings.HasPrefix(graphID, "project:") {
			path.ProjectAssetDBID = dbID
			path.ProjectAssetID = graphID
			break
		}
	}
	repositories := make([]string, 0, len(remotesByRepository))
	for repository := range remotesByRepository {
		repositories = append(repositories, repository)
	}
	sort.Strings(repositories)
	for _, repository := range repositories {
		project := projectByRepository[repository]
		remotes := append([]string{}, remotesByRepository[repository]...)
		sort.Strings(remotes)
		if project == "" || len(remotes) < 2 {
			if path.RepositoryAssetID == "" && project != "" {
				path.ProjectAssetDBID = dbIDByGraphID[project]
				path.ProjectAssetID = project
				path.RepositoryAssetDBID = dbIDByGraphID[repository]
				path.RepositoryAssetID = repository
				path.RemoteAssetIDs = firstNStrings(remotes, 2)
				for _, remote := range path.RemoteAssetIDs {
					path.RemoteAssetDBIDs = append(path.RemoteAssetDBIDs, dbIDByGraphID[remote])
				}
			}
			continue
		}
		path = demoReadinessSnapshotPath{
			ProjectAssetDBID:    dbIDByGraphID[project],
			ProjectAssetID:      project,
			RepositoryAssetDBID: dbIDByGraphID[repository],
			RepositoryAssetID:   repository,
			RemoteAssetDBIDs:    []string{dbIDByGraphID[remotes[0]], dbIDByGraphID[remotes[1]]},
			RemoteAssetIDs:      []string{remotes[0], remotes[1]},
		}
		break
	}
	return map[string]any{"nodes": nodes, "edges": edges}, path
}

func demoReadinessGraphID(asset demoReadinessAssetRow) string {
	typ := strings.TrimSpace(asset.AssetType)
	sourceID := strings.TrimSpace(asset.SourceID)
	if typ == "" {
		return ""
	}
	if sourceID == "" {
		sourceID = strings.TrimSpace(asset.ID)
	}
	if sourceID == "" {
		return ""
	}
	return typ + ":" + sourceID
}

func combinedDemoReadinessStatus(projectStatus, repositoryStatus string) string {
	if projectStatus == "ready" && repositoryStatus == "ready" {
		return "ready"
	}
	if projectStatus == "missing" && repositoryStatus == "missing" {
		return "missing"
	}
	return "partial"
}

func graphProofStatusFromPath(path demoReadinessSnapshotPath) string {
	if path.ProjectAssetID != "" && path.RepositoryAssetID != "" && len(path.RemoteAssetIDs) >= 2 {
		return "observed"
	}
	if path.ProjectAssetID != "" || path.RepositoryAssetID != "" || len(path.RemoteAssetIDs) > 0 {
		return "partial"
	}
	return "missing"
}

func firstNStrings(values []string, n int) []string {
	if len(values) <= n {
		return append([]string{}, values...)
	}
	return append([]string{}, values[:n]...)
}

func demoReadinessSnapshotMissingEvidence(projectStatus, repositoryStatus string, path demoReadinessSnapshotPath) []string {
	missing := []string{}
	if projectStatus != "ready" {
		missing = append(missing, "project_readiness_not_ready")
	}
	if repositoryStatus != "ready" {
		missing = append(missing, "repository_readiness_not_ready")
	}
	if path.ProjectAssetID == "" {
		missing = append(missing, "project_asset_path_missing")
	}
	if path.RepositoryAssetID == "" {
		missing = append(missing, "repository_asset_path_missing")
	}
	if len(path.RemoteAssetIDs) < 2 {
		missing = append(missing, "two_remote_asset_path_missing")
	}
	return missing
}

func countDemoAssetsByType(assets []demoReadinessAssetRow, assetType string) int {
	count := 0
	for _, asset := range assets {
		if asset.AssetType == assetType {
			count++
		}
	}
	return count
}

func countDemoRelationsByType(relations []demoReadinessRelationRow, relationType string) int {
	count := 0
	for _, relation := range relations {
		if relation.RelationType == relationType {
			count++
		}
	}
	return count
}

func countCompleteDemoRepositoryPaths(graph map[string]any) int {
	type repositoryLinks struct {
		project bool
		remotes map[string]bool
	}
	byRepository := map[string]*repositoryLinks{}
	repositoryEntry := func(assetID string) *repositoryLinks {
		entry := byRepository[assetID]
		if entry == nil {
			entry = &repositoryLinks{remotes: map[string]bool{}}
			byRepository[assetID] = entry
		}
		return entry
	}
	for _, edge := range demoSliceOfMapsFromAny(graph["edges"]) {
		from := fmt.Sprint(edge["from_asset_id"])
		to := fmt.Sprint(edge["to_asset_id"])
		switch fmt.Sprint(edge["relation_type"]) {
		case "owns":
			if strings.HasPrefix(from, "project:") && strings.HasPrefix(to, "repository:") {
				repositoryEntry(to).project = true
			}
		case "has_remote":
			if strings.HasPrefix(from, "repository:") && strings.HasPrefix(to, "git_remote:") {
				repositoryEntry(from).remotes[to] = true
			}
		}
	}
	count := 0
	for _, entry := range byRepository {
		if entry.project && len(entry.remotes) >= 2 {
			count++
		}
	}
	return count
}

func demoSliceOfMapsFromAny(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if mapped, ok := item.(map[string]any); ok {
			out = append(out, mapped)
		}
	}
	return out
}

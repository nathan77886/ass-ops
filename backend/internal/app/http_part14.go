package app

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"sort"
	"strings"
)

func canonicalAssetForRelationGorm(ctx context.Context, db *gorm.DB, assetID string) (map[string]any, error) {
	assetID = strings.TrimSpace(assetID)
	if assetID == "" {
		return nil, ErrNotFound
	}
	var asset GormAsset
	if err := db.WithContext(ctx).First(&asset, &GormAsset{GormBase: GormBase{ID: assetID}}).Error; err != nil {
		return nil, gormNotFoundAsErrNotFound(err)
	}
	return assetMap(asset), nil
}

func assetMap(asset GormAsset) map[string]any {
	return map[string]any{
		"id":            asset.ID,
		"project_id":    nullableStringValue(asset.ProjectID),
		"asset_type":    asset.AssetType,
		"source_table":  asset.SourceTable,
		"source_id":     nullableStringValue(asset.SourceID),
		"name":          asset.Name,
		"display_name":  asset.DisplayName,
		"description":   asset.Description,
		"source":        asset.Source,
		"external_id":   asset.ExternalID,
		"status":        asset.Status,
		"risk_level":    asset.RiskLevel,
		"owner_user_id": nullableStringValue(asset.OwnerUserID),
		"metadata":      mapFromAny(asset.Metadata.Data),
		"created_at":    asset.CreatedAt,
		"updated_at":    asset.UpdatedAt,
	}
}

func assetMaps(assets []GormAsset) []map[string]any {
	items := make([]map[string]any, 0, len(assets))
	for _, asset := range assets {
		items = append(items, assetMap(asset))
	}
	return items
}

func (s *Server) visibleAssetsGorm(ctx context.Context, user *User, projectID, assetType, queryText string, limit int) ([]GormAsset, error) {
	allowedProjects, err := s.projectMembershipSetGorm(ctx, user)
	if err != nil {
		return nil, err
	}
	projectID = cleanOptionalID(projectID)
	assetType = strings.TrimSpace(assetType)
	queryText = strings.ToLower(strings.TrimSpace(queryText))
	var assets []GormAsset
	if err := s.store.Gorm.WithContext(ctx).Find(&assets).Error; err != nil {
		return nil, err
	}
	filtered := make([]GormAsset, 0, len(assets))
	for _, asset := range assets {
		assetProjectID := cleanOptionalID(asset.ProjectID.String)
		if projectID != "" && assetProjectID != projectID {
			continue
		}
		if assetType != "" && asset.AssetType != assetType {
			continue
		}
		if queryText != "" && !assetMatchesQuery(asset, queryText) {
			continue
		}
		if !userCanReadAllProjects(user) && assetProjectID != "" && !allowedProjects[assetProjectID] {
			continue
		}
		filtered = append(filtered, asset)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if !filtered[i].UpdatedAt.Equal(filtered[j].UpdatedAt) {
			return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
		}
		return filtered[i].Name < filtered[j].Name
	})
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

func (s *Server) visibleAssetRelationsGorm(ctx context.Context, user *User, assetID, projectID string, limit int) ([]GormAssetRelation, error) {
	allowedProjects, err := s.projectMembershipSetGorm(ctx, user)
	if err != nil {
		return nil, err
	}
	assetID = cleanOptionalID(assetID)
	projectID = cleanOptionalID(projectID)
	var relations []GormAssetRelation
	if err := s.store.Gorm.WithContext(ctx).Find(&relations).Error; err != nil {
		return nil, err
	}
	filtered := make([]GormAssetRelation, 0, len(relations))
	for _, relation := range relations {
		relationProjectID := cleanOptionalID(relation.ProjectID.String)
		if assetID != "" && relation.FromAssetID != assetID && relation.ToAssetID != assetID {
			continue
		}
		if projectID != "" && relationProjectID != projectID {
			continue
		}
		if !userCanReadAllProjects(user) && relationProjectID != "" && !allowedProjects[relationProjectID] {
			continue
		}
		filtered = append(filtered, relation)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].RelationType != filtered[j].RelationType {
			return filtered[i].RelationType < filtered[j].RelationType
		}
		if filtered[i].FromAssetID != filtered[j].FromAssetID {
			return filtered[i].FromAssetID < filtered[j].FromAssetID
		}
		return filtered[i].ToAssetID < filtered[j].ToAssetID
	})
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

func (s *Server) assetGraphFromModels(ctx context.Context, assets []GormAsset, limit int) ([]map[string]any, []map[string]any, error) {
	assetIDs := make(map[string]bool, len(assets))
	for _, asset := range assets {
		assetIDs[asset.ID] = true
	}
	var relations []GormAssetRelation
	if len(assetIDs) > 0 {
		if err := s.store.Gorm.WithContext(ctx).Find(&relations).Error; err != nil {
			return nil, nil, err
		}
	}
	outgoing := map[string]int{}
	incoming := map[string]int{}
	edges := make([]map[string]any, 0, len(relations))
	for _, relation := range relations {
		if !assetIDs[relation.FromAssetID] || !assetIDs[relation.ToAssetID] {
			continue
		}
		outgoing[relation.FromAssetID]++
		incoming[relation.ToAssetID]++
		edges = append(edges, assetRelationMap(relation))
		if len(edges) >= 500 {
			break
		}
	}
	nodes := make([]map[string]any, 0, len(assets))
	for _, asset := range assets {
		node := assetMap(asset)
		relationCount := outgoing[asset.ID] + incoming[asset.ID]
		node["outgoing_relation_count"] = outgoing[asset.ID]
		node["incoming_relation_count"] = incoming[asset.ID]
		node["relation_count"] = relationCount
		node["graph_rank"] = assetRiskRank(asset.RiskLevel) + minInt(relationCount, 99)
		nodes = append(nodes, node)
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		if intFromAny(nodes[i]["graph_rank"], 0) != intFromAny(nodes[j]["graph_rank"], 0) {
			return intFromAny(nodes[i]["graph_rank"], 0) > intFromAny(nodes[j]["graph_rank"], 0)
		}
		if intFromAny(nodes[i]["relation_count"], 0) != intFromAny(nodes[j]["relation_count"], 0) {
			return intFromAny(nodes[i]["relation_count"], 0) > intFromAny(nodes[j]["relation_count"], 0)
		}
		return projectVersionTimeFromAny(nodes[i]["updated_at"]).After(projectVersionTimeFromAny(nodes[j]["updated_at"]))
	})
	if limit > 0 && len(nodes) > limit+1 {
		nodes = nodes[:limit+1]
	}
	return nodes, edges, nil
}

func (s *Server) assetDependencyPathsGorm(ctx context.Context, user *User, assetID, projectID, direction string, maxDepth, limit int) ([]map[string]any, error) {
	relations, err := s.visibleAssetRelationsGorm(ctx, user, "", projectID, 0)
	if err != nil {
		return nil, err
	}
	startAssetID := cleanOptionalID(assetID)
	if startAssetID == "" {
		return []map[string]any{}, nil
	}
	startField := func(relation GormAssetRelation) string { return relation.FromAssetID }
	nextField := func(relation GormAssetRelation) string { return relation.ToAssetID }
	if direction == "upstream" {
		startField = func(relation GormAssetRelation) string { return relation.ToAssetID }
		nextField = func(relation GormAssetRelation) string { return relation.FromAssetID }
	}
	byStart := map[string][]GormAssetRelation{}
	for _, relation := range relations {
		byStart[startField(relation)] = append(byStart[startField(relation)], relation)
	}
	type pathState struct {
		relation GormAssetRelation
		depth    int
		current  string
		assets   []string
		text     string
	}
	queue := []pathState{}
	for _, relation := range byStart[startAssetID] {
		next := nextField(relation)
		queue = append(queue, pathState{relation: relation, depth: 1, current: next, assets: []string{relation.FromAssetID, relation.ToAssetID}, text: assetRelationPathText(relation)})
	}
	items := []map[string]any{}
	for len(queue) > 0 && (limit <= 0 || len(items) < limit) {
		state := queue[0]
		queue = queue[1:]
		items = append(items, map[string]any{
			"id":               state.relation.ID,
			"project_id":       nullableStringValue(state.relation.ProjectID),
			"from_asset_id":    state.relation.FromAssetID,
			"to_asset_id":      state.relation.ToAssetID,
			"relation_type":    state.relation.RelationType,
			"depth":            state.depth,
			"path_assets":      append([]string(nil), state.assets...),
			"current_asset_id": state.current,
			"path_text":        state.text,
			"created_at":       state.relation.CreatedAt,
			"metadata":         mapFromAny(state.relation.Metadata.Data),
		})
		if state.depth >= maxDepth {
			continue
		}
		seen := map[string]bool{}
		for _, id := range state.assets {
			seen[id] = true
		}
		for _, relation := range byStart[state.current] {
			next := nextField(relation)
			if seen[next] {
				continue
			}
			nextAssets := append(append([]string(nil), state.assets...), next)
			queue = append(queue, pathState{relation: relation, depth: state.depth + 1, current: next, assets: nextAssets, text: state.text + " | " + assetRelationPathText(relation)})
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if intFromAny(items[i]["depth"], 0) != intFromAny(items[j]["depth"], 0) {
			return intFromAny(items[i]["depth"], 0) < intFromAny(items[j]["depth"], 0)
		}
		return fmt.Sprint(items[i]["path_text"]) < fmt.Sprint(items[j]["path_text"])
	})
	return items, nil
}

func assetRelationPathText(relation GormAssetRelation) string {
	return relation.FromAssetID + " --" + relation.RelationType + "--> " + relation.ToAssetID
}

func (s *Server) projectMembershipSetGorm(ctx context.Context, user *User) (map[string]bool, error) {
	items := map[string]bool{}
	if userCanReadAllProjects(user) || user == nil {
		return items, nil
	}
	var memberships []GormProjectMember
	if err := s.store.Gorm.WithContext(ctx).Where(&GormProjectMember{UserID: user.ID}).Find(&memberships).Error; err != nil {
		return nil, err
	}
	for _, membership := range memberships {
		items[membership.ProjectID] = true
	}
	return items, nil
}

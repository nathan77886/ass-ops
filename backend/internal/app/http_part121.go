package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"net/http"
	"os"
	"sort"
	"strings"
)

func contextDeploymentTargetMaps(targets []GormDeploymentTarget) []map[string]any {
	items := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		items = append(items, map[string]any{"id": target.ID, "name": target.Name, "environment": target.Environment, "cluster_name": target.ClusterName, "namespace": target.Namespace, "source": target.Source, "status": target.Status, "updated_at": target.UpdatedAt})
	}
	return items
}

func contextDeploymentRecordMaps(records []GormDeploymentRecord) []map[string]any {
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, map[string]any{"id": record.ID, "deployment_target_id": nullableStringValue(record.DeploymentTargetID), "name": record.Name, "environment": record.Environment, "namespace": record.Namespace, "cluster_name": record.ClusterName, "source": record.Source, "status": record.Status, "revision": record.Revision, "observed_at": record.ObservedAt})
	}
	return items
}

func contextRollbackPointMaps(ctx context.Context, db *gorm.DB, projectID string, limit int) ([]map[string]any, error) {
	var points []GormRollbackPoint
	if err := db.WithContext(ctx).Where(&GormRollbackPoint{ProjectID: projectID}).Order(gormOrderDesc("captured_at")).Limit(limit).Find(&points).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(points))
	for _, point := range points {
		item := map[string]any{"id": point.ID, "project_id": point.ProjectID, "deployment_record_id": nullableStringValue(point.DeploymentRecordID), "deployment_target_id": nullableStringValue(point.DeploymentTargetID), "name": point.Name, "environment": point.Environment, "revision": point.Revision, "image_refs": mapFromAny(point.ImageRefs.Data), "source": point.Source, "status": point.Status, "metadata": sanitizeMetadata(mapFromAny(point.Metadata.Data)), "captured_at": point.CapturedAt, "created_at": point.CreatedAt}
		readiness, reason := rollbackPointReadiness(item)
		item["rollback_readiness"] = readiness
		item["rollback_readiness_reason"] = reason
		item["rollback_execution_mode"] = "read_only_preview"
		item["rollback_executable"] = false
		item["rollback_execution_plan"] = rollbackExecutionPlan(readiness, "read_only_preview")
		items = append(items, item)
	}
	return items, nil
}

func contextAssetRelationMaps(ctx context.Context, db *gorm.DB, projectID string, assets map[string]GormAsset, limit int) ([]map[string]any, error) {
	var relations []GormAssetRelation
	if err := db.WithContext(ctx).Where(&GormAssetRelation{ProjectID: validNullString(projectID)}).Order(gormOrderAsc("relation_type")).Limit(limit).Find(&relations).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(relations))
	for _, relation := range relations {
		fromAsset, fromOK := assets[relation.FromAssetID]
		toAsset, toOK := assets[relation.ToAssetID]
		if !fromOK || !toOK {
			continue
		}
		items = append(items, map[string]any{"id": relation.ID, "project_id": nullableStringValue(relation.ProjectID), "relation_type": relation.RelationType, "metadata": mapFromAny(relation.Metadata.Data), "created_at": relation.CreatedAt, "from_asset_id": fromAsset.ID, "from_asset_type": fromAsset.AssetType, "from_asset_name": fromAsset.Name, "from_source_table": fromAsset.SourceTable, "from_source_id": nullableStringValue(fromAsset.SourceID), "to_asset_id": toAsset.ID, "to_asset_type": toAsset.AssetType, "to_asset_name": toAsset.Name, "to_source_table": toAsset.SourceTable, "to_source_id": nullableStringValue(toAsset.SourceID)})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if fmt.Sprint(items[i]["relation_type"]) != fmt.Sprint(items[j]["relation_type"]) {
			return fmt.Sprint(items[i]["relation_type"]) < fmt.Sprint(items[j]["relation_type"])
		}
		if fmt.Sprint(items[i]["from_asset_name"]) != fmt.Sprint(items[j]["from_asset_name"]) {
			return fmt.Sprint(items[i]["from_asset_name"]) < fmt.Sprint(items[j]["from_asset_name"])
		}
		return fmt.Sprint(items[i]["to_asset_name"]) < fmt.Sprint(items[j]["to_asset_name"])
	})
	return items, nil
}

func contextAssetStatusSnapshotMaps(ctx context.Context, db *gorm.DB, assets map[string]GormAsset, assetIDs []string, limit int) ([]map[string]any, error) {
	if len(assetIDs) == 0 {
		return []map[string]any{}, nil
	}
	var snapshots []GormAssetStatusSnapshot
	if err := db.WithContext(ctx).Where(gormField("asset_id", assetIDs)).Order(gormOrder(gormOrderColumn("collected_at", true), gormOrderColumn("id", true))).Limit(limit).Find(&snapshots).Error; err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(snapshots))
	for _, snapshot := range snapshots {
		asset := assets[snapshot.AssetID]
		items = append(items, map[string]any{"id": snapshot.ID, "asset_id": snapshot.AssetID, "asset_type": asset.AssetType, "asset_name": asset.Name, "status": snapshot.Status, "health": snapshot.Health, "summary": snapshot.Summary, "collected_at": snapshot.CollectedAt})
	}
	return items, nil
}

func writeJSONFile(path string, value any) error {
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, bytes, contextFileMode)
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return false
	}
	return true
}

func writeQueryResult(w http.ResponseWriter, items []map[string]any, err error) {
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func writeQueryOne(w http.ResponseWriter, item map[string]any, err error) {
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func writeCreatedOne(w http.ResponseWriter, item map[string]any, err error) {
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not create resource")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) syncCanonicalAssetsInGormTransaction(w http.ResponseWriter, r *http.Request, tx *gorm.DB, reason string) bool {
	result, err := syncCanonicalAssetsGorm(r.Context(), tx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not sync canonical assets")
		return false
	}
	if s.log != nil {
		s.log.Debug("canonical assets synced in transaction", "reason", reason, "synced_assets", result.SyncedAssets, "inserted_relations", result.InsertedRelations, "pruned_relations", result.PrunedRelations, "inserted_status_snapshots", result.InsertedStatusSnapshots)
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"strings"
	"time"
)

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

package app

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type SnapshotCleanupResult struct {
	DeletedRows int64
	Cutoff      time.Time
}

func cleanupAssetStatusSnapshots(ctx context.Context, db *gorm.DB, cfg SnapshotCleanupScheduleConfig, now time.Time) (SnapshotCleanupResult, error) {
	if db == nil {
		return SnapshotCleanupResult{}, fmt.Errorf("gorm store is not initialized")
	}
	if err := validateSchedulerConfig(SchedulerConfig{SnapshotCleanup: cfg}); err != nil {
		return SnapshotCleanupResult{}, err
	}
	cutoff := now.UTC().AddDate(0, 0, -cfg.RetentionDays)
	result := SnapshotCleanupResult{Cutoff: cutoff}
	for {
		deleted, err := deleteAssetStatusSnapshotBatch(ctx, db, cutoff, cfg.BatchSize, cfg.KeepLatestPerAsset)
		if err != nil {
			return result, err
		}
		result.DeletedRows += deleted
		if deleted < int64(cfg.BatchSize) {
			return result, nil
		}
	}
}

func deleteAssetStatusSnapshotBatch(ctx context.Context, db *gorm.DB, cutoff time.Time, batchSize int, keepLatestPerAsset bool) (int64, error) {
	query := `
DELETE FROM asset_status_snapshots
WHERE id IN (
  SELECT id FROM (
    SELECT s.id
    FROM asset_status_snapshots s
    WHERE s.collected_at < ?
    ORDER BY s.collected_at ASC, s.id ASC
    LIMIT ?
  ) victims
)`
	if keepLatestPerAsset {
		query = `
DELETE FROM asset_status_snapshots
WHERE id IN (
  SELECT id FROM (
    SELECT s.id
    FROM asset_status_snapshots s
    WHERE s.collected_at < ?
      AND EXISTS (
        SELECT 1
        FROM asset_status_snapshots newer
        WHERE newer.asset_id = s.asset_id
          AND (newer.collected_at > s.collected_at OR (newer.collected_at = s.collected_at AND newer.id > s.id))
      )
    ORDER BY s.collected_at ASC, s.id ASC
    LIMIT ?
  ) victims
)`
	}
	res := db.WithContext(ctx).Exec(query, cutoff, batchSize)
	if res.Error != nil {
		return 0, fmt.Errorf("deleting asset status snapshot batch: %w", res.Error)
	}
	return res.RowsAffected, nil
}

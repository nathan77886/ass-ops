package app

import (
	"testing"
	"time"
)

func TestCleanupAssetStatusSnapshotsKeepsLatestPerAsset(t *testing.T) {
	store := newGormFixtureStore(t)
	migrateGormFixture(t, store, &sqliteAssetStatusSnapshotFixture{})

	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	createSnapshot := func(id, assetID string, ageDays int) {
		t.Helper()
		snapshot := GormAssetStatusSnapshot{
			ID:          id,
			AssetID:     assetID,
			Status:      "active",
			Health:      "normal",
			Raw:         JSONValue{Data: map[string]any{}},
			CollectedAt: now.AddDate(0, 0, -ageDays),
		}
		if err := store.Gorm.Create(&snapshot).Error; err != nil {
			t.Fatalf("create snapshot %s: %v", id, err)
		}
	}
	createSnapshot("00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-0000000000a1", 40)
	createSnapshot("00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-0000000000a1", 35)
	createSnapshot("00000000-0000-0000-0000-000000000003", "00000000-0000-0000-0000-0000000000a1", 1)
	createSnapshot("00000000-0000-0000-0000-000000000004", "00000000-0000-0000-0000-0000000000b1", 40)
	createSnapshot("00000000-0000-0000-0000-000000000005", "00000000-0000-0000-0000-0000000000c1", 50)
	createSnapshot("00000000-0000-0000-0000-000000000006", "00000000-0000-0000-0000-0000000000c1", 45)

	result, err := cleanupAssetStatusSnapshots(t.Context(), store.Gorm, SnapshotCleanupScheduleConfig{
		Enabled:            true,
		RunAt:              defaultSnapshotCleanupRunAt,
		RetentionDays:      30,
		BatchSize:          2,
		KeepLatestPerAsset: true,
	}, now)
	if err != nil {
		t.Fatalf("cleanupAssetStatusSnapshots: %v", err)
	}
	if result.DeletedRows != 3 {
		t.Fatalf("deleted rows = %d, want 3", result.DeletedRows)
	}
	for _, id := range []string{
		"00000000-0000-0000-0000-000000000003",
		"00000000-0000-0000-0000-000000000004",
		"00000000-0000-0000-0000-000000000006",
	} {
		var count int64
		if err := store.Gorm.Model(&GormAssetStatusSnapshot{}).Where(&GormAssetStatusSnapshot{ID: id}).Count(&count).Error; err != nil {
			t.Fatalf("count snapshot %s: %v", id, err)
		}
		if count != 1 {
			t.Fatalf("snapshot %s count = %d, want 1", id, count)
		}
	}
}

func TestCleanupAssetStatusSnapshotsCanDeleteAllExpired(t *testing.T) {
	store := newGormFixtureStore(t)
	migrateGormFixture(t, store, &sqliteAssetStatusSnapshotFixture{})

	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	if err := store.Gorm.Create(&GormAssetStatusSnapshot{AssetID: "00000000-0000-0000-0000-0000000000a1", Status: "active", Health: "normal", Raw: JSONValue{Data: map[string]any{}}, CollectedAt: now.AddDate(0, 0, -40)}).Error; err != nil {
		t.Fatalf("create old snapshot: %v", err)
	}
	if err := store.Gorm.Create(&GormAssetStatusSnapshot{AssetID: "00000000-0000-0000-0000-0000000000a1", Status: "active", Health: "normal", Raw: JSONValue{Data: map[string]any{}}, CollectedAt: now.AddDate(0, 0, -1)}).Error; err != nil {
		t.Fatalf("create fresh snapshot: %v", err)
	}

	result, err := cleanupAssetStatusSnapshots(t.Context(), store.Gorm, SnapshotCleanupScheduleConfig{
		Enabled:            true,
		RunAt:              defaultSnapshotCleanupRunAt,
		RetentionDays:      30,
		BatchSize:          100,
		KeepLatestPerAsset: false,
	}, now)
	if err != nil {
		t.Fatalf("cleanupAssetStatusSnapshots: %v", err)
	}
	if result.DeletedRows != 1 {
		t.Fatalf("deleted rows = %d, want 1", result.DeletedRows)
	}
	var count int64
	if err := store.Gorm.Model(&GormAssetStatusSnapshot{}).Count(&count).Error; err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	if count != 1 {
		t.Fatalf("snapshot count = %d, want 1", count)
	}
}

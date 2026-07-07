package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadSchedulerConfigDefaults(t *testing.T) {
	cfg, err := LoadSchedulerConfig("")
	if err != nil {
		t.Fatalf("LoadSchedulerConfig: %v", err)
	}
	if !cfg.SnapshotCleanup.Enabled ||
		cfg.SnapshotCleanup.RunAt != defaultSnapshotCleanupRunAt ||
		cfg.SnapshotCleanup.RetentionDays != 30 ||
		cfg.SnapshotCleanup.BatchSize != 1000 ||
		!cfg.SnapshotCleanup.KeepLatestPerAsset {
		t.Fatalf("unexpected defaults: %#v", cfg.SnapshotCleanup)
	}
}

func TestLoadSchedulerConfigYAMLAndEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedule.yaml")
	if err := os.WriteFile(path, []byte(`
scheduler:
  snapshot_cleanup:
    enabled: false
    run_at: "01:15"
    retention_days: 45
    batch_size: 25
    keep_latest_per_asset: false
`), 0o644); err != nil {
		t.Fatalf("write schedule yaml: %v", err)
	}
	t.Setenv("ASSOPS_SCHEDULER_SNAPSHOT_CLEANUP_RETENTION_DAYS", "60")

	cfg, err := LoadSchedulerConfig(path)
	if err != nil {
		t.Fatalf("LoadSchedulerConfig: %v", err)
	}
	if cfg.SnapshotCleanup.Enabled ||
		cfg.SnapshotCleanup.RunAt != "01:15" ||
		cfg.SnapshotCleanup.RetentionDays != 60 ||
		cfg.SnapshotCleanup.BatchSize != 25 ||
		cfg.SnapshotCleanup.KeepLatestPerAsset {
		t.Fatalf("unexpected config: %#v", cfg.SnapshotCleanup)
	}
}

func TestLoadSchedulerConfigEnvOnly(t *testing.T) {
	t.Setenv("ASSOPS_SCHEDULER_SNAPSHOT_CLEANUP_ENABLED", "false")
	t.Setenv("ASSOPS_SCHEDULER_SNAPSHOT_CLEANUP_RUN_AT", "04:45")
	t.Setenv("ASSOPS_SCHEDULER_SNAPSHOT_CLEANUP_BATCH_SIZE", "50")

	cfg, err := LoadSchedulerConfig("")
	if err != nil {
		t.Fatalf("LoadSchedulerConfig: %v", err)
	}
	if cfg.SnapshotCleanup.Enabled ||
		cfg.SnapshotCleanup.RunAt != "04:45" ||
		cfg.SnapshotCleanup.BatchSize != 50 ||
		cfg.SnapshotCleanup.RetentionDays != 30 {
		t.Fatalf("unexpected env-only config: %#v", cfg.SnapshotCleanup)
	}
}

func TestNextDailyRunAfter(t *testing.T) {
	runAt, err := parseDailyRunAt("03:30")
	if err != nil {
		t.Fatalf("parseDailyRunAt: %v", err)
	}
	now := time.Date(2026, 7, 7, 2, 0, 0, 0, time.UTC)
	want := time.Date(2026, 7, 7, 3, 30, 0, 0, time.UTC)
	if got := nextDailyRunAfter(now, runAt); !got.Equal(want) {
		t.Fatalf("next before run_at = %s, want %s", got, want)
	}
	now = time.Date(2026, 7, 7, 4, 0, 0, 0, time.UTC)
	want = time.Date(2026, 7, 8, 3, 30, 0, 0, time.UTC)
	if got := nextDailyRunAfter(now, runAt); !got.Equal(want) {
		t.Fatalf("next after run_at = %s, want %s", got, want)
	}
}

package app

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const defaultSnapshotCleanupRunAt = "03:30"

type SchedulerConfig struct {
	SnapshotCleanup SnapshotCleanupScheduleConfig `mapstructure:"snapshot_cleanup"`
}

type SnapshotCleanupScheduleConfig struct {
	Enabled            bool   `mapstructure:"enabled"`
	RunAt              string `mapstructure:"run_at"`
	RetentionDays      int    `mapstructure:"retention_days"`
	BatchSize          int    `mapstructure:"batch_size"`
	KeepLatestPerAsset bool   `mapstructure:"keep_latest_per_asset"`
}

func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{SnapshotCleanup: SnapshotCleanupScheduleConfig{
		Enabled:            true,
		RunAt:              defaultSnapshotCleanupRunAt,
		RetentionDays:      30,
		BatchSize:          1000,
		KeepLatestPerAsset: true,
	}}
}

func LoadSchedulerConfig(path string) (SchedulerConfig, error) {
	v := viper.New()
	v.SetEnvPrefix("ASSOPS")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	setSchedulerDefaults(v)
	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			var notFound viper.ConfigFileNotFoundError
			if !errors.As(err, &notFound) && !errors.Is(err, os.ErrNotExist) {
				return SchedulerConfig{}, fmt.Errorf("reading scheduler config: %w", err)
			}
			return SchedulerConfig{}, fmt.Errorf("reading scheduler config: %w", err)
		}
	}

	cfg := SchedulerConfig{SnapshotCleanup: SnapshotCleanupScheduleConfig{
		Enabled:            v.GetBool("scheduler.snapshot_cleanup.enabled"),
		RunAt:              v.GetString("scheduler.snapshot_cleanup.run_at"),
		RetentionDays:      v.GetInt("scheduler.snapshot_cleanup.retention_days"),
		BatchSize:          v.GetInt("scheduler.snapshot_cleanup.batch_size"),
		KeepLatestPerAsset: v.GetBool("scheduler.snapshot_cleanup.keep_latest_per_asset"),
	}}
	if err := validateSchedulerConfig(cfg); err != nil {
		return SchedulerConfig{}, err
	}
	return cfg, nil
}

func setSchedulerDefaults(v *viper.Viper) {
	defaults := DefaultSchedulerConfig()
	v.SetDefault("scheduler.snapshot_cleanup.enabled", defaults.SnapshotCleanup.Enabled)
	v.SetDefault("scheduler.snapshot_cleanup.run_at", defaults.SnapshotCleanup.RunAt)
	v.SetDefault("scheduler.snapshot_cleanup.retention_days", defaults.SnapshotCleanup.RetentionDays)
	v.SetDefault("scheduler.snapshot_cleanup.batch_size", defaults.SnapshotCleanup.BatchSize)
	v.SetDefault("scheduler.snapshot_cleanup.keep_latest_per_asset", defaults.SnapshotCleanup.KeepLatestPerAsset)
}

func validateSchedulerConfig(cfg SchedulerConfig) error {
	cleanup := cfg.SnapshotCleanup
	if cleanup.RetentionDays < 1 {
		return fmt.Errorf("scheduler snapshot cleanup retention_days must be positive")
	}
	if cleanup.BatchSize < 1 {
		return fmt.Errorf("scheduler snapshot cleanup batch_size must be positive")
	}
	if _, err := parseDailyRunAt(cleanup.RunAt); err != nil {
		return err
	}
	return nil
}

func parseDailyRunAt(value string) (time.Duration, error) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("scheduler run_at must use HH:MM: %w", err)
	}
	return time.Duration(parsed.Hour())*time.Hour + time.Duration(parsed.Minute())*time.Minute, nil
}

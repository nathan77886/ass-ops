package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"
)

type Scheduler struct {
	store *Store
	cfg   SchedulerConfig
	log   *slog.Logger
	now   func() time.Time
}

func NewScheduler(store *Store, cfg SchedulerConfig, log *slog.Logger) *Scheduler {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Scheduler{store: store, cfg: cfg, log: log, now: time.Now}
}

func (s *Scheduler) Run(ctx context.Context) error {
	cleanup := s.cfg.SnapshotCleanup
	if !cleanup.Enabled {
		s.log.Info("snapshot cleanup scheduler disabled")
		<-ctx.Done()
		return ctx.Err()
	}
	runAt, err := parseDailyRunAt(cleanup.RunAt)
	if err != nil {
		return err
	}
	for {
		next := nextDailyRunAfter(s.now(), runAt)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
		result, err := cleanupAssetStatusSnapshots(ctx, s.store.Gorm, cleanup, s.now())
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			s.log.Warn("snapshot cleanup failed", "error", err)
			continue
		}
		s.log.Info("snapshot cleanup finished", "deleted_rows", result.DeletedRows, "cutoff", result.Cutoff.Format(time.RFC3339))
	}
}

func nextDailyRunAfter(now time.Time, runAt time.Duration) time.Time {
	base := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Add(runAt)
	if base.After(now) {
		return base
	}
	return base.Add(24 * time.Hour)
}

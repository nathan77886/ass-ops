package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"assops/backend/internal/app"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg := app.LoadConfig()
	schedulerCfg, err := app.LoadSchedulerConfig(cfg.ScheduleConfigPath)
	if err != nil {
		log.Error("load scheduler config failed", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := app.OpenStore(ctx, cfg)
	if err != nil {
		log.Error("open store failed", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	if err := store.AutoMigrate(ctx); err != nil {
		log.Error("apply schema failed", "error", err)
		os.Exit(1)
	}
	if err := store.SeedAdmin(ctx, cfg); err != nil {
		log.Error("seed admin failed", "error", err)
		os.Exit(1)
	}

	var wg sync.WaitGroup
	server := app.NewServer(cfg, store, log)
	if cfg.LocalWorkerEnabled {
		worker := app.NewControlWorker(store, cfg, log)
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Info("local control worker started")
			if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Error("local control worker stopped", "error", err)
				stop()
			}
		}()
	}
	scheduler := app.NewScheduler(store, schedulerCfg, log)
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info("scheduler started")
		if err := scheduler.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("scheduler stopped", "error", err)
			stop()
		}
	}()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("gateway listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("gateway failed", "error", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	wg.Wait()
}

package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"assops/backend/internal/app"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg := app.LoadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, err := app.OpenStore(ctx, cfg)
	if err != nil {
		log.Error("open store failed", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	log.Info("control worker started")
	if err := app.NewControlWorker(store, cfg.WorkerInterval, log).Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}

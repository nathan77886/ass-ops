package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	if err := store.AutoMigrate(ctx); err != nil {
		log.Error("apply schema failed", "error", err)
		os.Exit(1)
	}
	if err := store.SeedAdmin(ctx, cfg); err != nil {
		log.Error("seed admin failed", "error", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.NewServer(cfg, store, log).Handler(),
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
}

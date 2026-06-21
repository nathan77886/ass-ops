package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"assops/backend/internal/app"
)

func main() {
	name := flag.String("name", "local-node", "worker node name")
	kind := flag.String("kind", "local", "worker node kind")
	caps := flag.String("capabilities", "echo,git,ssh,ai", "comma-separated capabilities")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg := app.LoadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	worker := app.NewNodeWorker(cfg, *name, *kind, splitCSV(*caps), log)
	if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("node worker stopped", "error", err)
		os.Exit(1)
	}
}

func splitCSV(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

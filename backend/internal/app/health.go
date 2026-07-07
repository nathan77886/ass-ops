package app

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func HealthPayload(component string) map[string]any {
	return map[string]any{
		"ok":         true,
		"component":  component,
		"version":    envDefault("ASSOPS_VERSION", "dev"),
		"commit":     envDefault("ASSOPS_COMMIT", "local"),
		"build_time": envDefault("ASSOPS_BUILD_TIME", "unknown"),
	}
}

func HealthHandler(component string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, HealthPayload(component))
	})
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}

func envDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func StartHealthServer(ctx context.Context, addr, component string, log *slog.Logger) error {
	if addr == "" {
		return nil
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           HealthHandler(component),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go func() {
		log.Info("health server listening", "component", component, "addr", addr)
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("health server failed", "component", component, "error", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	return nil
}

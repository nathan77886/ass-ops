package app

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Addr           string
	DatabaseURL    string
	JWTSecret      string
	AdminEmail     string
	AdminPassword  string
	ContextDir     string
	GatewayURL     string
	WorkerInterval time.Duration
}

func LoadConfig() Config {
	return Config{
		Addr:           env("ASSOPS_ADDR", ":8080"),
		DatabaseURL:    env("DATABASE_URL", "postgres://assops:assops@localhost:5432/assops?sslmode=disable"),
		JWTSecret:      env("ASSOPS_JWT_SECRET", "dev-assops-change-me"),
		AdminEmail:     env("ASSOPS_ADMIN_EMAIL", "admin@assops.local"),
		AdminPassword:  env("ASSOPS_ADMIN_PASSWORD", "admin1234"),
		ContextDir:     env("ASSOPS_CONTEXT_DIR", ".assops/context"),
		GatewayURL:     env("ASSOPS_GATEWAY_URL", "http://localhost:8080"),
		WorkerInterval: time.Duration(envInt("ASSOPS_WORKER_INTERVAL_SECONDS", 3)) * time.Second,
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

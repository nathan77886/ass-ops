package app

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr                            string
	WorkerHealthAddr                string
	NodeWorkerHealthAddr            string
	DatabaseURL                     string
	JWTSecret                       string
	WebhookSecretKey                string
	ApprovalWebhookURL              string
	ApprovalWebhookToken            string
	ProviderReviewExecutionEnabled  bool
	ProviderReviewMutationArmed     bool
	KubernetesPodLogsEnabled        bool
	KubernetesLogPreviewEnabled     bool
	KubernetesRestartsEnabled       bool
	ConfigGitLocalBareWritesEnabled bool
	LocalWorkerEnabled              bool
	CloudflareQueuesEnabled         bool
	CloudflareAccountID             string
	CloudflareQueuesAPIToken        string
	CloudflareQueuesAPIBase         string
	CloudflareWorkerEventsQueueID   string
	CloudflareTaskQueueID           string
	CloudflareQueuePullBatchSize    int
	CloudflareQueueVisibilityMS     int
	AdminEmail                      string
	AdminPassword                   string
	ContextDir                      string
	WorkerMetricsPath               string
	GatewayURL                      string
	LocalBareBaseDirs               []string
	WorkerInterval                  time.Duration
}

func LoadConfig() Config {
	return Config{
		Addr:                            env("ASSOPS_ADDR", ":8080"),
		WorkerHealthAddr:                env("ASSOPS_WORKER_HEALTH_ADDR", ":8081"),
		NodeWorkerHealthAddr:            env("ASSOPS_NODE_WORKER_HEALTH_ADDR", ":8082"),
		DatabaseURL:                     env("DATABASE_URL", "postgres://assops:assops@localhost:5432/assops?sslmode=disable"),
		JWTSecret:                       env("ASSOPS_JWT_SECRET", "dev-assops-change-me"),
		WebhookSecretKey:                env("ASSOPS_WEBHOOK_SECRET_KEY", "dev-assops-webhook-change-me"),
		ApprovalWebhookURL:              env("ASSOPS_APPROVAL_WEBHOOK_URL", ""),
		ApprovalWebhookToken:            env("ASSOPS_APPROVAL_WEBHOOK_TOKEN", ""),
		ProviderReviewExecutionEnabled:  envBool("ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION", false),
		ProviderReviewMutationArmed:     envBool("ASSOPS_ARM_PROVIDER_REVIEW_MUTATION", false),
		KubernetesPodLogsEnabled:        envBool("ASSOPS_KUBERNETES_LOGS_ENABLED", false),
		KubernetesLogPreviewEnabled:     envBool("ASSOPS_KUBERNETES_LOG_PREVIEW_ENABLED", false),
		KubernetesRestartsEnabled:       envBool("ASSOPS_KUBERNETES_RESTARTS_ENABLED", false),
		ConfigGitLocalBareWritesEnabled: envBool("ASSOPS_CONFIG_GIT_LOCAL_BARE_WRITES_ENABLED", false),
		LocalWorkerEnabled:              envBool("ASSOPS_LOCAL_WORKER_ENABLED", true),
		CloudflareQueuesEnabled:         envBool("ASSOPS_CLOUDFLARE_QUEUES_ENABLED", false),
		CloudflareAccountID:             env("ASSOPS_CLOUDFLARE_ACCOUNT_ID", ""),
		CloudflareQueuesAPIToken:        env("ASSOPS_CLOUDFLARE_QUEUES_API_TOKEN", ""),
		CloudflareQueuesAPIBase:         env("ASSOPS_CLOUDFLARE_QUEUES_API_BASE", "https://api.cloudflare.com/client/v4"),
		CloudflareWorkerEventsQueueID:   env("ASSOPS_CLOUDFLARE_WORKER_EVENTS_QUEUE_ID", ""),
		CloudflareTaskQueueID:           env("ASSOPS_CLOUDFLARE_TASK_QUEUE_ID", ""),
		CloudflareQueuePullBatchSize:    envInt("ASSOPS_CLOUDFLARE_QUEUE_PULL_BATCH_SIZE", 10),
		CloudflareQueueVisibilityMS:     envInt("ASSOPS_CLOUDFLARE_QUEUE_VISIBILITY_TIMEOUT_MS", 300000),
		AdminEmail:                      env("ASSOPS_ADMIN_EMAIL", "admin@assops.local"),
		AdminPassword:                   env("ASSOPS_ADMIN_PASSWORD", "admin1234"),
		ContextDir:                      env("ASSOPS_CONTEXT_DIR", ".assops/context"),
		WorkerMetricsPath:               env("ASSOPS_WORKER_METRICS_PATH", ""),
		GatewayURL:                      env("ASSOPS_GATEWAY_URL", "http://localhost:8080"),
		LocalBareBaseDirs:               envList("ASSOPS_LOCAL_BARE_BASE_DIRS", ""),
		WorkerInterval:                  time.Duration(envInt("ASSOPS_WORKER_INTERVAL_SECONDS", 3)) * time.Second,
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

func envBool(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envList(key, fallback string) []string {
	value := env(key, fallback)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, string(os.PathListSeparator))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

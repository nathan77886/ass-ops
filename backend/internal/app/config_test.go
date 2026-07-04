package app

import "testing"

func TestLoadConfigIncludesApprovalWebhook(t *testing.T) {
	t.Setenv("ASSOPS_APPROVAL_WEBHOOK_URL", "https://hooks.example.test/approval")
	t.Setenv("ASSOPS_APPROVAL_WEBHOOK_TOKEN", "approval-token")
	t.Setenv("ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION", "true")
	t.Setenv("ASSOPS_ARM_PROVIDER_REVIEW_MUTATION", "true")

	cfg := LoadConfig()
	if cfg.ApprovalWebhookURL != "https://hooks.example.test/approval" {
		t.Fatalf("ApprovalWebhookURL = %q", cfg.ApprovalWebhookURL)
	}
	if cfg.ApprovalWebhookToken != "approval-token" {
		t.Fatalf("ApprovalWebhookToken = %q", cfg.ApprovalWebhookToken)
	}
	if !cfg.ProviderReviewExecutionEnabled {
		t.Fatal("ProviderReviewExecutionEnabled should be true")
	}
	if !cfg.ProviderReviewMutationArmed {
		t.Fatal("ProviderReviewMutationArmed should be true")
	}
}

func TestLoadConfigIncludesWorkerHealthAddresses(t *testing.T) {
	t.Setenv("ASSOPS_WORKER_HEALTH_ADDR", ":18081")
	t.Setenv("ASSOPS_NODE_WORKER_HEALTH_ADDR", ":18082")
	t.Setenv("ASSOPS_LOCAL_WORKER_ENABLED", "false")
	t.Setenv("ASSOPS_CLOUDFLARE_QUEUES_ENABLED", "true")
	t.Setenv("ASSOPS_CLOUDFLARE_ACCOUNT_ID", "account-1")
	t.Setenv("ASSOPS_CLOUDFLARE_QUEUES_API_TOKEN", "queue-token")
	t.Setenv("ASSOPS_CLOUDFLARE_WORKER_EVENTS_QUEUE_ID", "events-queue")
	t.Setenv("ASSOPS_CLOUDFLARE_TASK_QUEUE_ID", "task-queue")
	t.Setenv("ASSOPS_CLOUDFLARE_QUEUE_PULL_BATCH_SIZE", "9")
	t.Setenv("ASSOPS_CLOUDFLARE_QUEUE_VISIBILITY_TIMEOUT_MS", "12000")

	cfg := LoadConfig()
	if cfg.WorkerHealthAddr != ":18081" {
		t.Fatalf("WorkerHealthAddr = %q", cfg.WorkerHealthAddr)
	}
	if cfg.NodeWorkerHealthAddr != ":18082" {
		t.Fatalf("NodeWorkerHealthAddr = %q", cfg.NodeWorkerHealthAddr)
	}
	if cfg.LocalWorkerEnabled {
		t.Fatal("LocalWorkerEnabled should be false")
	}
	if !cfg.CloudflareQueuesEnabled ||
		cfg.CloudflareAccountID != "account-1" ||
		cfg.CloudflareQueuesAPIToken != "queue-token" ||
		cfg.CloudflareWorkerEventsQueueID != "events-queue" ||
		cfg.CloudflareTaskQueueID != "task-queue" ||
		cfg.CloudflareQueuePullBatchSize != 9 ||
		cfg.CloudflareQueueVisibilityMS != 12000 {
		t.Fatalf("cloudflare queue config not loaded: %#v", cfg)
	}
}

func TestLoadConfigEnablesLocalWorkerByDefault(t *testing.T) {
	cfg := LoadConfig()
	if !cfg.LocalWorkerEnabled {
		t.Fatal("LocalWorkerEnabled should default to true")
	}
}

func TestNewControlWorkerUsesConfigInterval(t *testing.T) {
	cfg := Config{WorkerInterval: 42}
	worker := NewControlWorker(nil, cfg, nil)
	if worker.interval != cfg.WorkerInterval {
		t.Fatalf("worker interval = %v, want %v", worker.interval, cfg.WorkerInterval)
	}
	if worker.server == nil || worker.server.webhookLimiter == nil {
		t.Fatal("control worker should use full server construction")
	}
}

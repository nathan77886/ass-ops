package app

import "testing"

func TestLoadConfigIncludesApprovalWebhook(t *testing.T) {
	t.Setenv("ASSOPS_APPROVAL_WEBHOOK_URL", "https://hooks.example.test/approval")
	t.Setenv("ASSOPS_APPROVAL_WEBHOOK_TOKEN", "approval-token")
	t.Setenv("ASSOPS_ENABLE_PROVIDER_REVIEW_EXECUTION", "true")

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
}

func TestLoadConfigIncludesWorkerHealthAddresses(t *testing.T) {
	t.Setenv("ASSOPS_WORKER_HEALTH_ADDR", ":18081")
	t.Setenv("ASSOPS_NODE_WORKER_HEALTH_ADDR", ":18082")

	cfg := LoadConfig()
	if cfg.WorkerHealthAddr != ":18081" {
		t.Fatalf("WorkerHealthAddr = %q", cfg.WorkerHealthAddr)
	}
	if cfg.NodeWorkerHealthAddr != ":18082" {
		t.Fatalf("NodeWorkerHealthAddr = %q", cfg.NodeWorkerHealthAddr)
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

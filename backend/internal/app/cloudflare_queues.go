package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type CloudflareQueueTaskEvent struct {
	EventID              string         `json:"event_id"`
	EventType            string         `json:"event_type"`
	OperationID          string         `json:"operation_id"`
	JobID                string         `json:"job_id"`
	ToolName             string         `json:"tool_name"`
	TargetWorkerKind     string         `json:"target_worker_kind"`
	RequiredCapabilities []string       `json:"required_capabilities"`
	Payload              map[string]any `json:"payload"`
	CreatedAt            time.Time      `json:"created_at"`
}

type CloudflareQueueMessage struct {
	Body        json.RawMessage `json:"body"`
	ID          string          `json:"id"`
	TimestampMS int64           `json:"timestamp_ms"`
	Attempts    int             `json:"attempts"`
	Metadata    map[string]any  `json:"metadata"`
	LeaseID     string          `json:"lease_id"`
}

type CloudflareQueuesClient struct {
	cfg    Config
	client *http.Client
}

func NewCloudflareQueuesClient(cfg Config, client *http.Client) *CloudflareQueuesClient {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &CloudflareQueuesClient{cfg: cfg, client: client}
}

func (c *CloudflareQueuesClient) PullConfigured(queueID string) bool {
	return c != nil &&
		c.cfg.CloudflareQueuesEnabled &&
		strings.TrimSpace(c.cfg.CloudflareAccountID) != "" &&
		strings.TrimSpace(c.cfg.CloudflareQueuesAPIToken) != "" &&
		strings.TrimSpace(queueID) != ""
}

func (c *CloudflareQueuesClient) WorkerEventsConfigured() bool {
	return c.PullConfigured(c.cfg.CloudflareWorkerEventsQueueID)
}

func (c *CloudflareQueuesClient) TaskPullConfigured() bool {
	return c.PullConfigured(c.cfg.CloudflareTaskQueueID)
}

func (c *CloudflareQueuesClient) TaskPublishConfigured() bool {
	return c.PullConfigured(c.cfg.CloudflareTaskQueueID)
}

func (c *CloudflareQueuesClient) PublishTask(ctx context.Context, event CloudflareQueueTaskEvent) error {
	if !c.TaskPublishConfigured() {
		return nil
	}
	reqBody := map[string]any{
		"body":         event,
		"content_type": "json",
	}
	var resp struct {
		Success bool `json:"success"`
	}
	if err := c.queuePost(ctx, c.cfg.CloudflareTaskQueueID, "", reqBody, &resp); err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("cloudflare queue task publish failed")
	}
	return nil
}

func (c *CloudflareQueuesClient) PullMessages(ctx context.Context, queueID string) ([]CloudflareQueueMessage, error) {
	if !c.PullConfigured(queueID) {
		return nil, nil
	}
	reqBody := map[string]any{
		"visibility_timeout_ms": c.cfg.CloudflareQueueVisibilityMS,
		"batch_size":            c.cfg.CloudflareQueuePullBatchSize,
	}
	var resp struct {
		Success bool             `json:"success"`
		Errors  []map[string]any `json:"errors"`
		Result  struct {
			Messages []CloudflareQueueMessage `json:"messages"`
		} `json:"result"`
	}
	if err := c.queuePost(ctx, queueID, "pull", reqBody, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("cloudflare queue pull failed")
	}
	return resp.Result.Messages, nil
}

func (c *CloudflareQueuesClient) AckMessages(ctx context.Context, queueID string, acks, retries []string) error {
	if !c.PullConfigured(queueID) {
		return nil
	}
	body := map[string]any{
		"acks":    queueLeaseItems(acks),
		"retries": queueLeaseItems(retries),
	}
	var resp struct {
		Success bool `json:"success"`
	}
	if err := c.queuePost(ctx, queueID, "ack", body, &resp); err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("cloudflare queue ack failed")
	}
	return nil
}

func (c *CloudflareQueuesClient) queuePost(ctx context.Context, queueID, action string, body any, dst any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.queueMessagesURL(queueID, action), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.CloudflareQueuesAPIToken)
	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1024))
		return fmt.Errorf("cloudflare queue %s returned %s", action, res.Status)
	}
	return json.NewDecoder(res.Body).Decode(dst)
}

func (c *CloudflareQueuesClient) queueMessagesURL(queueID, action string) string {
	base := strings.TrimRight(c.cfg.CloudflareQueuesAPIBase, "/")
	accountID := url.PathEscape(strings.TrimSpace(c.cfg.CloudflareAccountID))
	queueID = url.PathEscape(strings.TrimSpace(queueID))
	action = url.PathEscape(strings.TrimSpace(action))
	path := base + "/accounts/" + accountID + "/queues/" + queueID + "/messages"
	if action != "" {
		path += "/" + action
	}
	return path
}

func queueLeaseItems(leases []string) []map[string]string {
	items := make([]map[string]string, 0, len(leases))
	for _, lease := range leases {
		lease = strings.TrimSpace(lease)
		if lease != "" {
			items = append(items, map[string]string{"lease_id": lease})
		}
	}
	return items
}

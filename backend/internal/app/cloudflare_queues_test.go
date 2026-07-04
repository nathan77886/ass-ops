package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCloudflareQueuesClientPullAndAck(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer queue-token" {
			t.Fatalf("Authorization header = %q", r.Header.Get("Authorization"))
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/pull"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode pull body: %v", err)
			}
			if body["batch_size"].(float64) != 7 || body["visibility_timeout_ms"].(float64) != 9000 {
				t.Fatalf("pull body = %#v", body)
			}
			_, _ = w.Write([]byte(`{"success":true,"result":{"messages":[{"body":{"ok":true},"id":"m1","timestamp_ms":1,"attempts":1,"metadata":{"CF-Content-Type":"json"},"lease_id":"lease-1"}]}}`))
		case strings.HasSuffix(r.URL.Path, "/ack"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode ack body: %v", err)
			}
			if len(body["acks"].([]any)) != 1 || len(body["retries"].([]any)) != 1 {
				t.Fatalf("ack body = %#v", body)
			}
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewCloudflareQueuesClient(Config{
		CloudflareQueuesEnabled:      true,
		CloudflareAccountID:          "acct",
		CloudflareQueuesAPIToken:     "queue-token",
		CloudflareQueuesAPIBase:      server.URL,
		CloudflareQueuePullBatchSize: 7,
		CloudflareQueueVisibilityMS:  9000,
	}, server.Client())
	messages, err := client.PullMessages(t.Context(), "queue-id")
	if err != nil {
		t.Fatalf("PullMessages: %v", err)
	}
	if len(messages) != 1 || messages[0].LeaseID != "lease-1" {
		t.Fatalf("messages = %#v", messages)
	}
	if err := client.AckMessages(t.Context(), "queue-id", []string{"lease-1"}, []string{"lease-2"}); err != nil {
		t.Fatalf("AckMessages: %v", err)
	}
	if len(paths) != 2 || paths[0] != "/accounts/acct/queues/queue-id/messages/pull" || paths[1] != "/accounts/acct/queues/queue-id/messages/ack" {
		t.Fatalf("paths = %#v", paths)
	}
}

func TestCloudflareQueuesClientPublishTaskUsesProducerBridge(t *testing.T) {
	var auth string
	var event CloudflareQueueTaskEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := NewCloudflareQueuesClient(Config{
		CloudflareQueuesEnabled:     true,
		CloudflareTaskProducerURL:   server.URL,
		CloudflareTaskProducerToken: "producer-token",
	}, server.Client())
	if err := client.PublishTask(t.Context(), CloudflareQueueTaskEvent{EventID: "evt-1", JobID: "job-1"}); err != nil {
		t.Fatalf("PublishTask: %v", err)
	}
	if auth != "Bearer producer-token" || event.EventID != "evt-1" || event.JobID != "job-1" {
		t.Fatalf("auth=%q event=%#v", auth, event)
	}
}

package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestApprovalWebhookPayloadUsesMetadataAllowlist(t *testing.T) {
	payload := approvalWebhookPayload(map[string]any{
		"id":                       "approval-1",
		"project_id":               "project-1",
		"operation_run_id":         "run-1",
		"resource_type":            "agent_task",
		"resource_id":              "task-1",
		"action":                   "agent.execute",
		"title":                    "Execute agent task",
		"status":                   "pending",
		"approved_count":           1,
		"rejected_count":           0,
		"required_approver_roles":  []string{"admin", "owner"},
		"required_approval_count":  2,
		"escalation_after_minutes": 30,
		"escalation_channels":      []string{"email:ops@example.com", "slack:#deploys", "pagerduty"},
		"last_escalated_at":        "2026-01-01T00:00:00Z",
		"escalation_count":         1,
		"request_payload":          map[string]any{"prompt": "secret prompt", "token": "secret-token"},
		"result_payload":           map[string]any{"diff": "secret diff"},
		"decision_reason":          "contains private operational detail",
		"notification_last_error":  "Bearer secret-token",
		"metadata":                 map[string]any{"kubeconfig": "secret kubeconfig"},
	}, "escalation")
	if payload["event"] != "escalation" {
		t.Fatalf("event = %#v", payload["event"])
	}
	approval := mapFromAny(payload["approval"])
	for _, field := range []string{
		"id",
		"project_id",
		"operation_run_id",
		"resource_type",
		"resource_id",
		"action",
		"title",
		"status",
		"approved_count",
		"rejected_count",
	} {
		if _, ok := approval[field]; !ok {
			t.Fatalf("approval payload missing allowlisted field %q: %#v", field, approval)
		}
	}
	for _, field := range []string{
		"request_payload",
		"result_payload",
		"decision_reason",
		"notification_last_error",
		"required_approver_roles",
		"required_approval_count",
		"escalation_after_minutes",
		"escalation_channels",
		"last_escalated_at",
		"escalation_count",
		"metadata",
		"token",
		"kubeconfig",
		"secret",
	} {
		if _, ok := approval[field]; ok {
			t.Fatalf("approval payload included suppressed field %q: %#v", field, approval)
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal approval webhook payload: %v", err)
	}
	for _, leaked := range []string{"secret prompt", "secret-token", "secret diff", "private operational detail", "secret kubeconfig", "ops@example.com", "#deploys", "pagerduty"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("approval webhook payload leaked %q: %s", leaked, encoded)
		}
	}
}

func TestPostApprovalWebhookReminderUsesSafePayload(t *testing.T) {
	var gotPayload map[string]any
	previousClient := approvalWebhookHTTPClient
	approvalWebhookHTTPClient = &http.Client{Transport: approvalRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode webhook payload: %v", err)
		}
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	defer func() { approvalWebhookHTTPClient = previousClient }()

	server := &Server{cfg: Config{ApprovalWebhookURL: "https://93.184.216.34/approval"}}
	err := server.postApprovalWebhook(context.Background(), map[string]any{
		"id":                       "approval-1",
		"action":                   "agent.execute",
		"title":                    "Execute agent task",
		"status":                   "pending",
		"required_approver_roles":  []string{"admin", "owner"},
		"required_approval_count":  2,
		"approved_count":           1,
		"escalation_after_minutes": 30,
		"escalation_channels":      []string{"email:ops@example.com", "slack:#deploys", "pagerduty"},
		"last_escalated_at":        "2026-01-01T00:00:00Z",
		"escalation_count":         1,
		"request_payload":          map[string]any{"private": "context"},
	}, "reminder")
	if err != nil {
		t.Fatalf("postApprovalWebhook reminder: %v", err)
	}
	if gotPayload["event"] != "reminder" {
		t.Fatalf("event = %v, want reminder", gotPayload["event"])
	}
	approval, ok := gotPayload["approval"].(map[string]any)
	if !ok {
		t.Fatalf("approval payload = %#v", gotPayload["approval"])
	}
	if _, ok := approval["request_payload"]; ok {
		t.Fatal("reminder webhook must not include request_payload")
	}
	if approval["approved_count"] != float64(1) {
		t.Fatalf("approval progress = %#v", approval)
	}
	if _, ok := approval["required_approval_count"]; ok {
		t.Fatalf("reminder webhook must not include rule approval count: %#v", approval)
	}
	if _, ok := approval["required_approver_roles"]; ok {
		t.Fatalf("reminder webhook must not include approver roles: %#v", approval)
	}
	for _, field := range []string{"escalation_after_minutes", "escalation_channels", "last_escalated_at", "escalation_count"} {
		if _, ok := approval[field]; ok {
			t.Fatalf("reminder webhook must not include escalation metadata field %q: %#v", field, approval)
		}
	}
	encoded, _ := json.Marshal(gotPayload)
	for _, leaked := range []string{"private", "ops@example.com", "#deploys", "pagerduty"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("reminder webhook leaked %q: %s", leaked, encoded)
		}
	}
}

func TestPostApprovalWebhookRejectsUnsupportedScheme(t *testing.T) {
	server := &Server{cfg: Config{ApprovalWebhookURL: "ftp://example.com/hook"}}
	err := server.postApprovalWebhook(context.Background(), map[string]any{"id": "approval-1"}, "pending")
	if err == nil || !strings.Contains(err.Error(), "public http or https") {
		t.Fatalf("postApprovalWebhook error = %v, want scheme error", err)
	}
}

func TestPostApprovalWebhookRejectsMissingHost(t *testing.T) {
	server := &Server{cfg: Config{ApprovalWebhookURL: "http://"}}
	err := server.postApprovalWebhook(context.Background(), map[string]any{"id": "approval-1"}, "pending")
	if err == nil || !strings.Contains(err.Error(), "include a host") {
		t.Fatalf("postApprovalWebhook error = %v, want host error", err)
	}
}

func TestPostApprovalWebhookRejectsLocalhost(t *testing.T) {
	server := &Server{cfg: Config{ApprovalWebhookURL: "http://127.0.0.1:8080/approval"}}
	err := server.postApprovalWebhook(context.Background(), map[string]any{"id": "approval-1"}, "pending")
	if err == nil || !strings.Contains(err.Error(), "public http or https") {
		t.Fatalf("postApprovalWebhook error = %v, want public URL error", err)
	}
}

func TestApprovalExpirySQLOnlyExpiresPendingDueRows(t *testing.T) {
	t.Skip("approval expiry now uses GORM models and Go time filtering; replace SQL-shape assertion with GORM fixture coverage")
}

func TestApprovalNotificationStatusSuccessAndFailure(t *testing.T) {
	previousClient := approvalWebhookHTTPClient
	defer func() { approvalWebhookHTTPClient = previousClient }()

	approval := map[string]any{"id": "approval-1", "action": "ssh.exec"}
	server := &Server{cfg: Config{ApprovalWebhookURL: "https://93.184.216.34/approval"}}

	approvalWebhookHTTPClient = &http.Client{Transport: approvalRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	status, lastError := server.approvalNotificationStatus(context.Background(), approval, "expired")
	if status != "delivered" || lastError != "" {
		t.Fatalf("success status = %q error = %q", status, lastError)
	}

	approvalWebhookHTTPClient = &http.Client{Transport: approvalRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	status, lastError = server.approvalNotificationStatus(context.Background(), approval, "expired")
	if status != "failed" || !strings.Contains(lastError, "status 502") {
		t.Fatalf("failure status = %q error = %q", status, lastError)
	}
}

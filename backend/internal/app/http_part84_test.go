package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
)

func TestProjectTemplatePreviewFlagsSameRemoteIDs(t *testing.T) {
	preview := projectTemplatePreview(map[string]any{}, "Billing", "billing", "", map[string]any{
		"repo_sync": map[string]any{
			"source_remote_id": "remote-1",
			"target_remote_id": "remote-1",
		},
	})
	sync := mapFromAny(preview["repo_sync"])
	if sync["status"] != "planned" {
		t.Fatalf("repo_sync status = %v, want planned", sync["status"])
	}
	if sync["reason"] != "source_remote_id and target_remote_id must be different" {
		t.Fatalf("repo_sync reason = %v", sync["reason"])
	}
	repo := mapFromAny(preview["repository"])
	if repo["repo_key"] != "billing-service" {
		t.Fatalf("repo_key = %v, want billing-service", repo["repo_key"])
	}
}

func TestVerifyWebhookSignature(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	secret := "top-secret"
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))

	header := make(http.Header)
	header.Set("X-Gitea-Signature", signature)
	if !verifyWebhookSignature(header, secret, body) {
		t.Fatal("expected X-Gitea-Signature to verify")
	}
	header = make(http.Header)
	header.Set("X-Hub-Signature-256", "sha256="+signature)
	if !verifyWebhookSignature(header, secret, body) {
		t.Fatal("expected X-Hub-Signature-256 to verify")
	}
	header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	if verifyWebhookSignature(header, secret, body) {
		t.Fatal("invalid signature should fail")
	}
}

func TestWebhookSecretEncryptionRequiresCiphertext(t *testing.T) {
	server := &Server{cfg: Config{JWTSecret: "jwt-secret", WebhookSecretKey: "webhook-key"}}
	ciphertext, err := server.encryptWebhookSecret("shared-secret")
	if err != nil {
		t.Fatalf("encryptWebhookSecret error: %v", err)
	}
	if !strings.HasPrefix(ciphertext, "v1:") || strings.Contains(ciphertext, "shared-secret") {
		t.Fatalf("ciphertext should not contain plaintext secret: %q", ciphertext)
	}
	got, err := server.webhookSecretFromConnection(map[string]any{"secret_ciphertext": ciphertext})
	if err != nil {
		t.Fatalf("webhookSecretFromConnection error: %v", err)
	}
	if got != "shared-secret" {
		t.Fatalf("secret = %q, want shared-secret", got)
	}
	if _, err := server.webhookSecretFromConnection(map[string]any{"secret_token": "plaintext-secret"}); err == nil {
		t.Fatal("plaintext webhook secret should not be accepted")
	}
	if _, err := server.webhookSecretFromConnection(map[string]any{}); err == nil {
		t.Fatal("empty webhook connection secret should return an error")
	}
}

func TestPublicBaseURLTrimsTrailingSlash(t *testing.T) {
	server := &Server{cfg: Config{GatewayURL: "https://assops.example.com/"}}
	if got := server.publicBaseURL(); got != "https://assops.example.com" {
		t.Fatalf("publicBaseURL = %q, want https://assops.example.com", got)
	}
}

func TestPublicBaseURLKeepsOnlyHTTPOrigin(t *testing.T) {
	server := &Server{cfg: Config{GatewayURL: "https://assops.example.com/nested/path?token=bad#fragment"}}
	if got := server.publicBaseURL(); got != "https://assops.example.com" {
		t.Fatalf("publicBaseURL = %q, want https://assops.example.com", got)
	}
	for _, input := range []string{"ftp://assops.example.com", "https://", "://bad", "assops.example.com"} {
		server.cfg.GatewayURL = input
		if got := server.publicBaseURL(); got != "http://localhost:8080" {
			t.Fatalf("publicBaseURL(%q) = %q, want localhost fallback", input, got)
		}
	}
}

func TestWebhookDeliveryIDIgnoresRequestIDFallback(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/api/webhooks/github/id", nil)
	req.Header.Set("X-Request-Id", "request-id")
	if got := webhookDeliveryID(req); got != "" {
		t.Fatalf("webhookDeliveryID with only X-Request-Id = %q, want empty", got)
	}
	req.Header.Set("X-GitHub-Delivery", "delivery-id")
	if got := webhookDeliveryID(req); got != "delivery-id" {
		t.Fatalf("webhookDeliveryID = %q, want delivery-id", got)
	}
}

func TestRepoSyncAssetMatchesWebhookRef(t *testing.T) {
	tests := []struct {
		name string
		refs map[string]any
		ref  string
		want bool
	}{
		{name: "matching branch", refs: map[string]any{"branches": []any{"main"}}, ref: "refs/heads/main", want: true},
		{name: "wildcard tag", refs: map[string]any{"tags": []any{"*"}}, ref: "refs/tags/v1.0.0", want: true},
		{name: "wrong branch", refs: map[string]any{"branches": []any{"develop"}}, ref: "refs/heads/main", want: false},
		{name: "empty refs", refs: nil, ref: "refs/heads/main", want: false},
		{name: "unsupported ref", refs: map[string]any{"branches": []any{"main"}}, ref: "main", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := repoSyncAssetMatchesWebhookRef(tt.refs, tt.ref); got != tt.want {
				t.Fatalf("repoSyncAssetMatchesWebhookRef = %v, want %v", got, tt.want)
			}
		})
	}
}

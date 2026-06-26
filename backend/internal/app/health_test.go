package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthHandler(t *testing.T) {
	t.Setenv("ASSOPS_VERSION", "v0.1.0")
	t.Setenv("ASSOPS_COMMIT", "abc1234")
	t.Setenv("ASSOPS_BUILD_TIME", "2026-06-26T00:00:00Z")

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	HealthHandler("control-worker").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["ok"] != true || body["component"] != "control-worker" {
		t.Fatalf("body = %#v", body)
	}
	if body["version"] != "v0.1.0" || body["commit"] != "abc1234" || body["build_time"] != "2026-06-26T00:00:00Z" {
		t.Fatalf("build metadata = %#v", body)
	}
}

func TestHealthPayloadDefaultsBuildMetadata(t *testing.T) {
	body := HealthPayload("gateway")

	if body["ok"] != true || body["component"] != "gateway" {
		t.Fatalf("body = %#v", body)
	}
	if body["version"] != "dev" || body["commit"] != "local" || body["build_time"] != "unknown" {
		t.Fatalf("default build metadata = %#v", body)
	}
}

func TestStartHealthServerReturnsListenError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	err = StartHealthServer(context.Background(), listener.Addr().String(), "control-worker", log)
	if err == nil {
		t.Fatal("expected bind error")
	}
}

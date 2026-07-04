package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkerNodeMetricsFileStoresLatestByNode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.json")
	if err := writeWorkerNodeMetrics(path, "node-1", map[string]any{"hostname": "host-a", "secret": "nope"}); err != nil {
		t.Fatalf("writeWorkerNodeMetrics: %v", err)
	}
	if err := writeWorkerNodeMetrics(path, "node-1", map[string]any{"hostname": "host-b"}); err != nil {
		t.Fatalf("writeWorkerNodeMetrics second: %v", err)
	}
	got := readWorkerNodeMetrics(path)
	if got["node-1"]["hostname"] != "host-b" {
		t.Fatalf("hostname = %v, want host-b", got["node-1"]["hostname"])
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat metrics file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestSanitizeWorkerNodeMetricsKeepsAllowlistedKeys(t *testing.T) {
	got := sanitizeWorkerNodeMetrics(map[string]any{
		"cpu_load_1m": 1.2,
		"hostname":    "worker-a",
		"secret":      "hidden",
	})
	if got["cpu_load_1m"] != 1.2 || got["hostname"] != "worker-a" {
		t.Fatalf("allowlisted metrics missing: %#v", got)
	}
	if _, ok := got["secret"]; ok {
		t.Fatalf("secret key should be dropped: %#v", got)
	}
}

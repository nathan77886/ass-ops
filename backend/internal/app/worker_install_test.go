package app

import (
	"strings"
	"testing"
)

func TestBuildWorkerInstallCommandRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name string
		req  workerInstallRequest
	}{
		{
			name: "shell in node name",
			req:  workerInstallRequest{NodeName: "bad;name", Kind: "remote", Capabilities: []string{"exec"}, GatewayURL: "https://gateway.example", DownloadBaseURL: "https://github.com/acme/assops/releases/latest/download", NodeWorkerPath: "/usr/local/bin/node-worker"},
		},
		{
			name: "local kind",
			req:  workerInstallRequest{NodeName: "node-a", Kind: "local", Capabilities: []string{"exec"}, GatewayURL: "https://gateway.example", DownloadBaseURL: "https://github.com/acme/assops/releases/latest/download", NodeWorkerPath: "/usr/local/bin/node-worker"},
		},
		{
			name: "bad url",
			req:  workerInstallRequest{NodeName: "node-a", Kind: "remote", Capabilities: []string{"exec"}, GatewayURL: "file:///tmp/socket", DownloadBaseURL: "https://github.com/acme/assops/releases/latest/download", NodeWorkerPath: "/usr/local/bin/node-worker"},
		},
		{
			name: "bad download url",
			req:  workerInstallRequest{NodeName: "node-a", Kind: "remote", Capabilities: []string{"exec"}, GatewayURL: "https://gateway.example", DownloadBaseURL: "file:///tmp/node-worker", NodeWorkerPath: "/usr/local/bin/node-worker"},
		},
		{
			name: "http download url",
			req:  workerInstallRequest{NodeName: "node-a", Kind: "remote", Capabilities: []string{"exec"}, GatewayURL: "https://gateway.example", DownloadBaseURL: "http://example.com/releases", NodeWorkerPath: "/usr/local/bin/node-worker"},
		},
		{
			name: "download url with query",
			req:  workerInstallRequest{NodeName: "node-a", Kind: "remote", Capabilities: []string{"exec"}, GatewayURL: "https://gateway.example", DownloadBaseURL: "https://example.com/releases?token=redacted", NodeWorkerPath: "/usr/local/bin/node-worker"},
		},
		{
			name: "bad path",
			req:  workerInstallRequest{NodeName: "node-a", Kind: "remote", Capabilities: []string{"exec"}, GatewayURL: "https://gateway.example", DownloadBaseURL: "https://github.com/acme/assops/releases/latest/download", NodeWorkerPath: "/tmp/node worker"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := buildWorkerInstallCommand(tt.req); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestBuildWorkerInstallCommandUsesSystemdService(t *testing.T) {
	cmd, err := buildWorkerInstallCommand(workerInstallRequest{
		NodeName:        "node-a",
		Kind:            "remote",
		Capabilities:    []string{"k8s", "exec", "k8s"},
		GatewayURL:      "https://gateway.example",
		DownloadBaseURL: "https://github.com/acme/assops/releases/latest/download",
		NodeWorkerPath:  "/usr/local/bin/node-worker",
	})
	if err != nil {
		t.Fatalf("buildWorkerInstallCommand: %v", err)
	}
	for _, want := range []string{"systemctl enable --now assops-node-worker.service", "base64 -d", "node-worker-linux-${assops_arch}", "curl -fsSL", "trap 'rm -f", "sha256sum -c", "sudo install -m 0755 \"$bin_tmp\" '/usr/local/bin/node-worker'"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("command missing %q:\n%s", want, cmd)
		}
	}
	if strings.Contains(cmd, "k8s,k8s") {
		t.Fatalf("capabilities were not deduped:\n%s", cmd)
	}
}

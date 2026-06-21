package app

import (
	"strings"
	"testing"
)

func TestSSHCommandArgs(t *testing.T) {
	t.Setenv("ASSOPS_SSH_KEY_DIR", "/keys")
	t.Setenv("ASSOPS_SSH_KNOWN_HOSTS_DIR", "/known_hosts")
	args, err := sshCommandArgs(map[string]any{
		"host":     "10.0.0.10",
		"port":     int64(2222),
		"username": "deploy",
		"metadata": map[string]any{
			"key_path":                 "/keys/deploy",
			"known_hosts_path":         "/known_hosts",
			"strict_host_key_checking": "yes",
		},
	}, "uptime")
	if err != nil {
		t.Fatalf("sshCommandArgs returned error: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"BatchMode=yes",
		"ConnectTimeout=10",
		"-p 2222",
		"UserKnownHostsFile=/known_hosts",
		"StrictHostKeyChecking=yes",
		"-i /keys/deploy",
		"deploy@10.0.0.10",
		"uptime",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
}

func TestSSHCommandArgsRejectsInvalidPort(t *testing.T) {
	_, err := sshCommandArgs(map[string]any{
		"host":     "10.0.0.10",
		"port":     "not-a-port",
		"username": "deploy",
	}, "uptime")
	if err == nil {
		t.Fatal("expected invalid port to fail")
	}
}

func TestSSHCommandArgsRejectsPathOutsideAllowedDir(t *testing.T) {
	t.Setenv("ASSOPS_SSH_KEY_DIR", "/keys")
	_, err := sshCommandArgs(map[string]any{
		"host":     "10.0.0.10",
		"port":     22,
		"username": "deploy",
		"metadata": map[string]any{"key_path": "/etc/shadow"},
	}, "uptime")
	if err == nil {
		t.Fatal("expected key path outside allowed dir to fail")
	}
}

func TestTruncateOutput(t *testing.T) {
	got := truncateOutput("abcdef", 3)
	if got != "abc\n[truncated]" {
		t.Fatalf("truncateOutput = %q", got)
	}
}

func TestSanitizeSSHOutput(t *testing.T) {
	got := sanitizeSSHOutput("AWS_SECRET_ACCESS_KEY=abc123\nAuthorization: Bearer token-value\nPASSWORD=hunter2\ncurl -u user:pass --password pass2\njwt=eyJhbGciOiJIUzI1NiJ9.abc.def")
	for _, leaked := range []string{"abc123", "token-value", "hunter2", "user:pass", "pass2", "eyJhbGciOiJIUzI1NiJ9.abc.def"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("sanitizeSSHOutput leaked %q in %q", leaked, got)
		}
	}
}

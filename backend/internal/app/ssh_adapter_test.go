package app

import (
	"context"
	"strings"
	"testing"
)

type fakeSSHRunner struct {
	name string
	args []string
}

func (r *fakeSSHRunner) Run(_ context.Context, name string, args ...string) (string, string, int, error) {
	r.name = name
	r.args = append([]string{}, args...)
	return "", "", 0, nil
}

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

func TestSSHExecutorForcesVerifyCommand(t *testing.T) {
	command, timeout, verify, err := sshExecutionCommand(map[string]any{"verify": true, "command": "rm -rf /", "timeout_seconds": 99}, "rm -rf /")
	if err != nil {
		t.Fatalf("sshExecutionCommand: %v", err)
	}
	if command != "true" {
		t.Fatalf("command = %q, want true", command)
	}
	if strings.Contains(command, "rm -rf") {
		t.Fatalf("command leaked stored verify command: %q", command)
	}
	if !verify {
		t.Fatalf("verify = %v, want true", verify)
	}
	if timeout != 15 {
		t.Fatalf("timeout = %d, want 15", timeout)
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

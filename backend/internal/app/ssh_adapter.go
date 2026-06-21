package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

type SSHExecutor struct {
	Runner sshRunner
}

type sshRunner interface {
	Run(ctx context.Context, name string, args ...string) (string, string, int, error)
}

type execSSHRunner struct{}

type SSHExecutionResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Details  map[string]any
}

func NewSSHExecutor() *SSHExecutor {
	return &SSHExecutor{Runner: execSSHRunner{}}
}

func (execSSHRunner) Run(ctx context.Context, name string, args ...string) (string, string, int, error) {
	stdout, stderr, err := execCommandRunner{}.Run(ctx, "", name, args...)
	exitCode := 0
	if err != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	return stdout, stderr, exitCode, err
}

func (e *SSHExecutor) Execute(ctx context.Context, db sqlx.ExtContext, opID string) (*SSHExecutionResult, error) {
	run, err := queryOne(ctx, db, `
		SELECT scr.*, opr.input
		FROM ssh_command_runs scr
		JOIN operation_runs opr ON opr.id=scr.operation_run_id
		WHERE scr.operation_run_id=$1
		LIMIT 1`, opID)
	if err != nil {
		return nil, err
	}
	machine, err := queryOne(ctx, db, "SELECT * FROM ssh_machines WHERE id=$1", run["ssh_machine_id"])
	if err != nil {
		return nil, fmt.Errorf("loading SSH machine: %w", err)
	}
	input := mapFromAny(run["input"])
	command := strings.TrimSpace(fmt.Sprint(input["command"]))
	if command == "" || command == "<nil>" {
		command = strings.TrimSpace(fmt.Sprint(run["command"]))
	}
	if command == "" || command == "<nil>" {
		return nil, fmt.Errorf("command is required")
	}
	timeout := intFromAny(input["timeout_seconds"], 60)
	if timeout <= 0 || timeout > 300 {
		timeout = 60
	}
	args, err := sshCommandArgs(machine, command)
	if err != nil {
		return nil, err
	}
	runner := e.Runner
	if runner == nil {
		runner = execSSHRunner{}
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	stdout, stderr, exitCode, err := runner.Run(runCtx, "ssh", args...)
	result := &SSHExecutionResult{
		Stdout:   truncateOutput(sanitizeSSHOutput(stdout), 64*1024),
		Stderr:   truncateOutput(sanitizeSSHOutput(stderr), 64*1024),
		ExitCode: exitCode,
		Details: map[string]any{
			"ssh_machine_id":  run["ssh_machine_id"],
			"host":            machine["host"],
			"port":            machine["port"],
			"timeout_seconds": timeout,
		},
	}
	if err != nil {
		return result, fmt.Errorf("ssh command failed with exit code %d: %w", exitCode, err)
	}
	return result, nil
}

func sshCommandArgs(machine map[string]any, command string) ([]string, error) {
	host := strings.TrimSpace(fmt.Sprint(machine["host"]))
	username := strings.TrimSpace(fmt.Sprint(machine["username"]))
	if host == "" || host == "<nil>" || username == "" || username == "<nil>" {
		return nil, fmt.Errorf("SSH machine host and username are required")
	}
	port := strings.TrimSpace(fmt.Sprint(machine["port"]))
	if port == "" || port == "<nil>" {
		port = "22"
	}
	if _, err := strconv.Atoi(port); err != nil {
		return nil, fmt.Errorf("invalid SSH port")
	}
	metadata := mapFromAny(machine["metadata"])
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-p", port,
	}
	if knownHosts := strings.TrimSpace(fmt.Sprint(metadata["known_hosts_path"])); knownHosts != "" && knownHosts != "<nil>" {
		if err := validateSSHPath(knownHosts, "ASSOPS_SSH_KNOWN_HOSTS_DIR", "/etc/assops/ssh"); err != nil {
			return nil, fmt.Errorf("invalid known_hosts_path: %w", err)
		}
		args = append(args, "-o", "UserKnownHostsFile="+knownHosts)
	}
	strict := strings.TrimSpace(fmt.Sprint(metadata["strict_host_key_checking"]))
	if strict == "" || strict == "<nil>" {
		strict = "accept-new"
	}
	args = append(args, "-o", "StrictHostKeyChecking="+strict)
	if keyPath := strings.TrimSpace(fmt.Sprint(metadata["key_path"])); keyPath != "" && keyPath != "<nil>" {
		if err := validateSSHPath(keyPath, "ASSOPS_SSH_KEY_DIR", "/etc/assops/ssh"); err != nil {
			return nil, fmt.Errorf("invalid key_path: %w", err)
		}
		args = append(args, "-i", keyPath)
	}
	args = append(args, username+"@"+host, command)
	return args, nil
}

func validateSSHPath(pathValue, envKey, fallbackDir string) error {
	if strings.Contains(pathValue, "\x00") {
		return fmt.Errorf("path contains null byte")
	}
	allowedDir := strings.TrimSpace(os.Getenv(envKey))
	if allowedDir == "" {
		allowedDir = fallbackDir
	}
	absPath, err := filepath.Abs(pathValue)
	if err != nil {
		return err
	}
	absAllowed, err := filepath.Abs(allowedDir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absAllowed, absPath)
	if err != nil {
		return err
	}
	if rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)) {
		return nil
	}
	return fmt.Errorf("path must be under %s", absAllowed)
}

func truncateOutput(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "\n[truncated]"
}

var sshAssignmentSecretPattern = regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|API[_-]?KEY|ACCESS[_-]?KEY|PRIVATE[_-]?KEY|CREDENTIAL)[A-Z0-9_]*)\s*=\s*([^\s]+)`)
var sshBearerSecretPattern = regexp.MustCompile(`(?i)\b(Bearer)\s+[A-Za-z0-9._~+/=-]+`)
var sshCLISecretPattern = regexp.MustCompile(`(?i)(--?(?:password|passwd|token|secret|api-key|access-key|private-key)\s+)([^\s]+)`)
var sshBasicAuthPattern = regexp.MustCompile(`(?i)(-u\s+[^:\s]+:)([^\s]+)`)
var sshJWTSecretPattern = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)

func sanitizeSSHOutput(output string) string {
	output = sanitizeGitOutput(output)
	output = sshAssignmentSecretPattern.ReplaceAllString(output, `$1=<redacted>`)
	output = sshBearerSecretPattern.ReplaceAllString(output, `$1 <redacted>`)
	output = sshCLISecretPattern.ReplaceAllString(output, `$1<redacted>`)
	output = sshBasicAuthPattern.ReplaceAllString(output, `$1<redacted>`)
	output = sshJWTSecretPattern.ReplaceAllString(output, `<redacted-jwt>`)
	return output
}

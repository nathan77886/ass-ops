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

	"gorm.io/gorm"
)

type SSHExecutor struct {
	Runner sshRunner
}

type sshRunner interface {
	Run(ctx context.Context, name string, args ...string) (string, string, int, error)
}

type sshEnvRunner interface {
	RunWithEnv(ctx context.Context, env []string, name string, args ...string) (string, string, int, error)
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
	return runSSHCommand(ctx, nil, name, args...)
}

func (execSSHRunner) RunWithEnv(ctx context.Context, env []string, name string, args ...string) (string, string, int, error) {
	return runSSHCommand(ctx, env, name, args...)
}

func runSSHCommand(ctx context.Context, env []string, name string, args ...string) (string, string, int, error) {
	if name == "sshpass" {
		if _, err := exec.LookPath("sshpass"); err != nil {
			return "", "", 127, fmt.Errorf("sshpass is required for SSH password authentication but is not installed")
		}
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = os.Environ()
	if len(env) > 0 {
		cmd.Env = append(cmd.Env, env...)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	return stdout.String(), stderr.String(), exitCode, err
}

func (e *SSHExecutor) Execute(ctx context.Context, db *gorm.DB, opID string) (*SSHExecutionResult, error) {
	if db == nil {
		return nil, fmt.Errorf("gorm store is not initialized")
	}
	var op GormOperationRun
	if err := db.WithContext(ctx).Where(&GormOperationRun{GormBase: GormBase{ID: opID}}).First(&op).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("loading SSH operation: %w", err)
	}
	var run GormSSHCommandRun
	if err := db.WithContext(ctx).Where(&GormSSHCommandRun{OperationRunID: validNullString(opID)}).First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("loading SSH command run: %w", err)
	}
	var machine GormSSHMachine
	if err := db.WithContext(ctx).Where(&GormSSHMachine{GormBase: GormBase{ID: nullStringValue(run.SSHMachineID)}}).First(&machine).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("loading SSH machine: %w", err)
	}
	input := mapFromAny(op.Input.Data)
	command, timeout, verify, err := sshExecutionCommand(input, run.Command)
	if err != nil {
		return nil, err
	}
	machineInput := sshMachineMap(machine, nil)
	args, env, err := sshCommandInvocation(ctx, db, machine, machineInput, command)
	if err != nil {
		return nil, err
	}
	runner := e.Runner
	if runner == nil {
		runner = execSSHRunner{}
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	name := "ssh"
	if len(env) > 0 {
		name = "sshpass"
		args = append([]string{"-e", "ssh"}, args...)
	}
	var stdout, stderr string
	var exitCode int
	if envRunner, ok := runner.(sshEnvRunner); ok {
		stdout, stderr, exitCode, err = envRunner.RunWithEnv(runCtx, env, name, args...)
	} else {
		stdout, stderr, exitCode, err = runner.Run(runCtx, name, args...)
	}
	result := &SSHExecutionResult{
		Stdout:   truncateOutput(sanitizeSSHOutput(stdout), 64*1024),
		Stderr:   truncateOutput(sanitizeSSHOutput(stderr), 64*1024),
		ExitCode: exitCode,
		Details: map[string]any{
			"ssh_machine_id":  nullStringValue(run.SSHMachineID),
			"host":            machine.Host,
			"port":            machine.Port,
			"timeout_seconds": timeout,
			"verify":          verify,
		},
	}
	if err != nil {
		return result, fmt.Errorf("ssh command failed with exit code %d: %w", exitCode, err)
	}
	return result, nil
}

func sshCommandInvocation(ctx context.Context, db *gorm.DB, machine GormSSHMachine, machineInput map[string]any, command string) ([]string, []string, error) {
	args, err := sshCommandArgs(machineInput, command)
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(machine.AuthType) != "password" {
		return args, nil, nil
	}
	if db == nil {
		return nil, nil, fmt.Errorf("database is not configured")
	}
	credentialID := cleanOptionalID(machine.CredentialID.String)
	if credentialID == "" {
		return nil, nil, fmt.Errorf("SSH password credential is not configured")
	}
	var credential GormConnectionCredential
	if err := db.WithContext(ctx).
		Where(&GormConnectionCredential{Kind: "ssh_password"}).
		Where("id = ? AND project_id = ?", credentialID, machine.ProjectID).
		First(&credential).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, fmt.Errorf("SSH password credential is not configured")
		}
		return nil, nil, fmt.Errorf("loading SSH password credential: %w", err)
	}
	if strings.TrimSpace(credential.SecretCiphertext) == "" {
		return nil, nil, fmt.Errorf("SSH password credential is not configured")
	}
	password, err := decryptArgoCredentialSecret(credential.SecretCiphertext, argoCredentialSecretKeyMaterial())
	if err != nil {
		return nil, nil, fmt.Errorf("decrypting SSH password credential failed")
	}
	if strings.TrimSpace(password) == "" {
		return nil, nil, fmt.Errorf("SSH password credential is empty")
	}
	return args, []string{"SSHPASS=" + password}, nil
}

func sshExecutionCommand(input map[string]any, storedCommand string) (string, int, bool, error) {
	command := strings.TrimSpace(fmt.Sprint(input["command"]))
	if command == "" || command == "<nil>" {
		command = strings.TrimSpace(storedCommand)
	}
	if command == "" || command == "<nil>" {
		return "", 0, false, fmt.Errorf("command is required")
	}
	timeout := intFromAny(input["timeout_seconds"], 60)
	verify := boolOnlyFromAny(input["verify"])
	if verify {
		command = "true"
		if timeout <= 0 || timeout > 15 {
			timeout = 15
		}
	}
	if timeout <= 0 || timeout > 300 {
		timeout = 60
	}
	return command, timeout, verify, nil
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
	authType := strings.TrimSpace(fmt.Sprint(machine["auth_type"]))
	batchMode := "yes"
	if authType == "password" {
		batchMode = "no"
	}
	args := []string{
		"-o", "BatchMode=" + batchMode,
		"-o", "ConnectTimeout=10",
		"-p", port,
	}
	if authType == "password" {
		args = append(args, "-o", "PreferredAuthentications=password,keyboard-interactive")
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

var sshAssignmentSecretPattern = regexp.MustCompile(`(?i)\b(SSHPASS|[A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|API[_-]?KEY|ACCESS[_-]?KEY|PRIVATE[_-]?KEY|CREDENTIAL)[A-Z0-9_]*)\s*=\s*([^\s]+)`)
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

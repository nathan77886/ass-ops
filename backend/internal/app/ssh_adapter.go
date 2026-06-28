package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	sshagent "golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
	"gorm.io/gorm"
)

type SSHExecutor struct {
	Runner sshRunner
}

type sshRunner interface {
	Run(ctx context.Context, request sshRunRequest) (string, string, int, error)
}

type nativeSSHRunner struct{}

type sshRunRequest struct {
	Host                  string
	Port                  int
	Username              string
	Command               string
	AuthType              string
	Password              string
	KeyPath               string
	KnownHostsPath        string
	StrictHostKeyChecking string
	ConnectTimeout        time.Duration
}

type SSHExecutionResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Details  map[string]any
}

func NewSSHExecutor() *SSHExecutor {
	return &SSHExecutor{Runner: nativeSSHRunner{}}
}

func (nativeSSHRunner) Run(ctx context.Context, request sshRunRequest) (string, string, int, error) {
	authMethods, cleanupAuth, err := sshAuthMethods(request)
	if err != nil {
		return "", "", 1, err
	}
	defer cleanupAuth()
	hostKeyCallback, err := sshHostKeyCallback(request.KnownHostsPath, request.StrictHostKeyChecking)
	if err != nil {
		return "", "", 1, err
	}
	timeout := request.ConnectTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	config := &ssh.ClientConfig{
		User:            request.Username,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         timeout,
	}
	address := net.JoinHostPort(request.Host, strconv.Itoa(request.Port))
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return "", "", 1, err
	}
	if err := conn.SetDeadline(sshHandshakeDeadline(ctx, timeout)); err != nil {
		_ = conn.Close()
		return "", "", 1, err
	}
	clientConn, chans, reqs, err := ssh.NewClientConn(conn, address, config)
	if err != nil {
		_ = conn.Close()
		return "", "", 1, err
	}
	_ = conn.SetDeadline(time.Time{})
	client := ssh.NewClient(clientConn, chans, reqs)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return "", "", 1, err
	}
	defer session.Close()
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	done := make(chan error, 1)
	go func() {
		done <- session.Run(request.Command)
	}()
	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		return stdout.String(), stderr.String(), 1, ctx.Err()
	case err := <-done:
		exitCode := 0
		if err != nil {
			exitCode = 1
			var exitErr *ssh.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitStatus()
			}
		}
		return stdout.String(), stderr.String(), exitCode, err
	}
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
	request, err := sshCommandInvocation(ctx, db, machine, machineInput, command)
	if err != nil {
		return nil, err
	}
	runner := e.Runner
	if runner == nil {
		runner = nativeSSHRunner{}
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	stdout, stderr, exitCode, err := runner.Run(runCtx, request)
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

func sshCommandInvocation(ctx context.Context, db *gorm.DB, machine GormSSHMachine, machineInput map[string]any, command string) (sshRunRequest, error) {
	request, err := sshCommandRequest(machineInput, command)
	if err != nil {
		return sshRunRequest{}, err
	}
	if strings.TrimSpace(machine.AuthType) != "password" {
		return request, nil
	}
	if db == nil {
		return sshRunRequest{}, fmt.Errorf("database is not configured")
	}
	credentialID := cleanOptionalID(machine.CredentialID.String)
	if credentialID == "" {
		return sshRunRequest{}, fmt.Errorf("SSH password credential is not configured")
	}
	var credential GormConnectionCredential
	if err := db.WithContext(ctx).
		Where(&GormConnectionCredential{Kind: "ssh_password"}).
		Where("id = ? AND project_id = ?", credentialID, machine.ProjectID).
		First(&credential).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return sshRunRequest{}, fmt.Errorf("SSH password credential is not configured")
		}
		return sshRunRequest{}, fmt.Errorf("loading SSH password credential: %w", err)
	}
	if strings.TrimSpace(credential.SecretCiphertext) == "" {
		return sshRunRequest{}, fmt.Errorf("SSH password credential is not configured")
	}
	password, err := decryptArgoCredentialSecret(credential.SecretCiphertext, argoCredentialSecretKeyMaterial())
	if err != nil {
		return sshRunRequest{}, fmt.Errorf("decrypting SSH password credential failed")
	}
	if strings.TrimSpace(password) == "" {
		return sshRunRequest{}, fmt.Errorf("SSH password credential is empty")
	}
	request.Password = password
	return request, nil
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

func sshCommandRequest(machine map[string]any, command string) (sshRunRequest, error) {
	host := strings.TrimSpace(fmt.Sprint(machine["host"]))
	username := strings.TrimSpace(fmt.Sprint(machine["username"]))
	if host == "" || host == "<nil>" || username == "" || username == "<nil>" {
		return sshRunRequest{}, fmt.Errorf("SSH machine host and username are required")
	}
	port := strings.TrimSpace(fmt.Sprint(machine["port"]))
	if port == "" || port == "<nil>" {
		port = "22"
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber <= 0 || portNumber > 65535 {
		return sshRunRequest{}, fmt.Errorf("invalid SSH port")
	}
	metadata := mapFromAny(machine["metadata"])
	authType := strings.TrimSpace(fmt.Sprint(machine["auth_type"]))
	request := sshRunRequest{
		Host:                  host,
		Port:                  portNumber,
		Username:              username,
		Command:               command,
		AuthType:              authType,
		StrictHostKeyChecking: "accept-new",
		ConnectTimeout:        10 * time.Second,
	}
	if knownHosts := strings.TrimSpace(fmt.Sprint(metadata["known_hosts_path"])); knownHosts != "" && knownHosts != "<nil>" {
		if err := validateSSHPath(knownHosts, "ASSOPS_SSH_KNOWN_HOSTS_DIR", "/etc/assops/ssh"); err != nil {
			return sshRunRequest{}, fmt.Errorf("invalid known_hosts_path: %w", err)
		}
		request.KnownHostsPath = knownHosts
	}
	strict := strings.TrimSpace(fmt.Sprint(metadata["strict_host_key_checking"]))
	if strict != "" && strict != "<nil>" {
		request.StrictHostKeyChecking = strict
	}
	if keyPath := strings.TrimSpace(fmt.Sprint(metadata["key_path"])); keyPath != "" && keyPath != "<nil>" {
		if err := validateSSHPath(keyPath, "ASSOPS_SSH_KEY_DIR", "/etc/assops/ssh"); err != nil {
			return sshRunRequest{}, fmt.Errorf("invalid key_path: %w", err)
		}
		request.KeyPath = keyPath
	}
	return request, nil
}

func sshAuthMethods(request sshRunRequest) ([]ssh.AuthMethod, func(), error) {
	var methods []ssh.AuthMethod
	var cleanup []func()
	if strings.TrimSpace(request.Password) != "" {
		methods = append(methods, ssh.Password(request.Password), ssh.KeyboardInteractive(func(_ string, _ string, questions []string, _ []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range answers {
				answers[i] = request.Password
			}
			return answers, nil
		}))
	}
	if strings.TrimSpace(request.KeyPath) != "" {
		method, ok, err := sshPrivateKeyAuthMethod(request.KeyPath, true)
		if err != nil {
			return nil, noop, err
		}
		if ok {
			methods = append(methods, method)
		}
	}
	if agentMethod, agentCleanup := sshAgentAuthMethod(); agentMethod != nil {
		methods = append(methods, agentMethod)
		cleanup = append(cleanup, agentCleanup)
	}
	for _, identityPath := range defaultSSHIdentityPaths() {
		method, ok, err := sshPrivateKeyAuthMethod(identityPath, false)
		if err != nil {
			return nil, joinedCleanup(cleanup), err
		}
		if ok {
			methods = append(methods, method)
		}
	}
	if len(methods) == 0 && request.AuthType == "password" {
		return nil, joinedCleanup(cleanup), fmt.Errorf("SSH password credential is not configured")
	}
	if len(methods) == 0 {
		return nil, joinedCleanup(cleanup), fmt.Errorf("SSH key_path, SSH agent, or default SSH identity is required for key authentication")
	}
	return methods, joinedCleanup(cleanup), nil
}

func sshPrivateKeyAuthMethod(path string, explicit bool) (ssh.AuthMethod, bool, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		if explicit {
			return nil, false, fmt.Errorf("reading SSH key failed: %w", err)
		}
		return nil, false, nil
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		var passphraseErr *ssh.PassphraseMissingError
		if explicit && errors.As(err, &passphraseErr) {
			return nil, false, fmt.Errorf("SSH key passphrase is required for encrypted private key")
		}
		if errors.As(err, &passphraseErr) {
			return nil, false, nil
		}
		if explicit {
			return nil, false, fmt.Errorf("parsing SSH key failed: %w", err)
		}
		return nil, false, nil
	}
	return ssh.PublicKeys(signer), true, nil
}

func sshAgentAuthMethod() (ssh.AuthMethod, func()) {
	socket := strings.TrimSpace(os.Getenv("SSH_AUTH_SOCK"))
	if socket == "" {
		return nil, nil
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, nil
	}
	return ssh.PublicKeysCallback(sshagent.NewClient(conn).Signers), func() { _ = conn.Close() }
}

func defaultSSHIdentityPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".ssh", "id_ecdsa"),
		filepath.Join(home, ".ssh", "id_rsa"),
	}
}

func joinedCleanup(cleanups []func()) func() {
	if len(cleanups) == 0 {
		return noop
	}
	return func() {
		for _, cleanup := range cleanups {
			if cleanup != nil {
				cleanup()
			}
		}
	}
}

func noop() {
}

func sshHostKeyCallback(knownHostsPath, strict string) (ssh.HostKeyCallback, error) {
	strict = strings.ToLower(strings.TrimSpace(strict))
	if strict == "" || strict == "<nil>" {
		strict = "accept-new"
	}
	if strict == "no" || strict == "false" {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	if knownHostsPath == "" {
		knownHostsPath = defaultKnownHostsPath()
	}
	if knownHostsPath == "" {
		if strict == "yes" || strict == "true" {
			return nil, fmt.Errorf("known_hosts_path is required when strict host key checking is enabled")
		}
		return ssh.InsecureIgnoreHostKey(), nil
	}
	callback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && strict == "accept-new" {
			return func(hostname string, _ net.Addr, key ssh.PublicKey) error {
				return appendKnownHostKey(knownHostsPath, hostname, key)
			}, nil
		}
		return nil, fmt.Errorf("loading known_hosts failed: %w", err)
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := callback(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) && len(keyErr.Want) == 0 && strict == "accept-new" {
			return appendKnownHostKey(knownHostsPath, hostname, key)
		}
		return err
	}, nil
}

func defaultKnownHostsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".ssh", "known_hosts")
}

func appendKnownHostKey(path, hostname string, key ssh.PublicKey) error {
	if strings.TrimSpace(hostname) == "" {
		return fmt.Errorf("known_hosts hostname is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating known_hosts directory failed: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("opening known_hosts failed: %w", err)
	}
	defer file.Close()
	if _, err := file.WriteString(knownhosts.Line([]string{hostname}, key) + "\n"); err != nil {
		return fmt.Errorf("writing known_hosts failed: %w", err)
	}
	return nil
}

func sshHandshakeDeadline(ctx context.Context, timeout time.Duration) time.Time {
	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		return ctxDeadline
	}
	return deadline
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

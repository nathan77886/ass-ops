package app

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/crypto/ssh"
	sshagent "golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

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

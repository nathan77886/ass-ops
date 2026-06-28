package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"golang.org/x/crypto/ssh"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeSSHRunner struct {
	request sshRunRequest
}

func (r *fakeSSHRunner) Run(_ context.Context, request sshRunRequest) (string, string, int, error) {
	r.request = request
	return "", "", 0, nil
}

func TestSSHCommandRequest(t *testing.T) {
	t.Setenv("ASSOPS_SSH_KEY_DIR", "/keys")
	t.Setenv("ASSOPS_SSH_KNOWN_HOSTS_DIR", "/known_hosts")
	request, err := sshCommandRequest(map[string]any{
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
		t.Fatalf("sshCommandRequest returned error: %v", err)
	}
	if request.Host != "10.0.0.10" ||
		request.Port != 2222 ||
		request.Username != "deploy" ||
		request.Command != "uptime" ||
		request.KeyPath != "/keys/deploy" ||
		request.KnownHostsPath != "/known_hosts" ||
		request.StrictHostKeyChecking != "yes" {
		t.Fatalf("unexpected SSH request: %#v", request)
	}
	if request.ConnectTimeout != 10*time.Second {
		t.Fatalf("connect timeout = %s, want 10s", request.ConnectTimeout)
	}
}

func TestSSHCommandRequestPasswordAuth(t *testing.T) {
	request, err := sshCommandRequest(map[string]any{
		"host":      "10.0.0.10",
		"port":      22,
		"username":  "deploy",
		"auth_type": "password",
	}, "uptime")
	if err != nil {
		t.Fatalf("sshCommandRequest returned error: %v", err)
	}
	if request.AuthType != "password" ||
		request.Host != "10.0.0.10" ||
		request.Port != 22 ||
		request.Username != "deploy" ||
		request.Command != "uptime" {
		t.Fatalf("unexpected password SSH request: %#v", request)
	}
}

func TestSSHExecutorUsesPasswordCredentialWithNativeRunner(t *testing.T) {
	t.Setenv("ASSOPS_WEBHOOK_SECRET_KEY", "test-secret-material")
	store := newGormFixtureStore(t)
	migrateGormFixture(t, store, &GormOperationRun{}, &GormSSHCommandRun{}, &GormSSHMachine{}, &GormConnectionCredential{})
	ciphertext, err := (&Server{cfg: Config{WebhookSecretKey: "test-secret-material"}}).encryptWebhookSecret("correct horse battery staple")
	if err != nil {
		t.Fatalf("encryptWebhookSecret: %v", err)
	}
	projectID := "11111111-1111-1111-1111-111111111111"
	machineID := "22222222-2222-2222-2222-222222222222"
	credentialID := "33333333-3333-3333-3333-333333333333"
	opID := "44444444-4444-4444-4444-444444444444"
	runID := "55555555-5555-5555-5555-555555555555"
	if err := store.Gorm.Create(&GormConnectionCredential{
		GormBase:         GormBase{ID: credentialID},
		ProjectID:        validNullString(projectID),
		Name:             "prod password",
		Kind:             "ssh_password",
		SecretCiphertext: ciphertext,
	}).Error; err != nil {
		t.Fatalf("create credential: %v", err)
	}
	if err := store.Gorm.Create(&GormSSHMachine{
		GormBase:     GormBase{ID: machineID},
		ProjectID:    projectID,
		Name:         "prod",
		Host:         "10.0.0.10",
		Port:         8090,
		Username:     "root",
		AuthType:     "password",
		CredentialID: validNullString(credentialID),
		Metadata:     JSONValue{Data: map[string]any{}},
	}).Error; err != nil {
		t.Fatalf("create machine: %v", err)
	}
	if err := store.Gorm.Create(&GormOperationRun{
		GormBase:      GormBase{ID: opID},
		ProjectID:     validNullString(projectID),
		OperationType: "ssh.verify",
		Status:        "running",
		Title:         "verify ssh prod",
		Input:         JSONValue{Data: map[string]any{"command": "true", "timeout_seconds": 15, "verify": true}},
		Result:        JSONValue{Data: map[string]any{}},
	}).Error; err != nil {
		t.Fatalf("create operation: %v", err)
	}
	if err := store.Gorm.Create(&GormSSHCommandRun{
		ID:             runID,
		OperationRunID: validNullString(opID),
		SSHMachineID:   validNullString(machineID),
		ProjectID:      validNullString(projectID),
		Command:        "true",
		Status:         "running",
	}).Error; err != nil {
		t.Fatalf("create run: %v", err)
	}
	runner := &fakeSSHRunner{}
	result, err := (&SSHExecutor{Runner: runner}).Execute(context.Background(), store.Gorm, opID)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", result.ExitCode)
	}
	if runner.request.Host != "10.0.0.10" ||
		runner.request.Port != 8090 ||
		runner.request.Username != "root" ||
		runner.request.AuthType != "password" ||
		runner.request.Command != "true" ||
		runner.request.Password != "correct horse battery staple" {
		t.Fatalf("unexpected SSH runner request: %#v", runner.request)
	}
	for _, leaked := range []string{"correct horse battery staple", "SSHPASS"} {
		if strings.Contains(fmt.Sprint(result.Details), leaked) {
			t.Fatalf("password leaked %q details=%#v", leaked, result.Details)
		}
	}
}

func TestSSHCommandRequestRejectsInvalidPort(t *testing.T) {
	_, err := sshCommandRequest(map[string]any{
		"host":     "10.0.0.10",
		"port":     "not-a-port",
		"username": "deploy",
	}, "uptime")
	if err == nil {
		t.Fatal("expected invalid port to fail")
	}
}

func TestSSHCommandRequestRejectsPathOutsideAllowedDir(t *testing.T) {
	t.Setenv("ASSOPS_SSH_KEY_DIR", "/keys")
	_, err := sshCommandRequest(map[string]any{
		"host":     "10.0.0.10",
		"port":     22,
		"username": "deploy",
		"metadata": map[string]any{"key_path": "/etc/shadow"},
	}, "uptime")
	if err == nil {
		t.Fatal("expected key path outside allowed dir to fail")
	}
}

func TestSSHHostKeyCallbackAcceptNewCreatesAndChecksKnownHosts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	key := testSSHPublicKey(t)
	callback, err := sshHostKeyCallback(path, "accept-new")
	if err != nil {
		t.Fatalf("sshHostKeyCallback: %v", err)
	}
	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 22}
	if err := callback("example.com:22", addr, key); err != nil {
		t.Fatalf("accept-new callback: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	if !strings.Contains(string(body), "example.com") || !strings.Contains(string(body), key.Type()) {
		t.Fatalf("known_hosts was not persisted correctly: %q", string(body))
	}
	callback, err = sshHostKeyCallback(path, "yes")
	if err != nil {
		t.Fatalf("strict callback: %v", err)
	}
	if err := callback("example.com:22", addr, key); err != nil {
		t.Fatalf("strict callback should accept persisted key: %v", err)
	}
	if err := callback("example.com:22", addr, testSSHPublicKey(t)); err == nil {
		t.Fatal("strict callback should reject changed host key")
	}
}

func TestSSHHostKeyCallbackStrictRequiresKnownHosts(t *testing.T) {
	_, err := sshHostKeyCallback(filepath.Join(t.TempDir(), "missing_known_hosts"), "yes")
	if err == nil {
		t.Fatal("expected missing known_hosts to fail in strict mode")
	}
}

func TestSSHAuthMethodsRejectsEncryptedExplicitKey(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(keyPath, testEncryptedPrivateKeyPEM(t), 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	_, cleanup, err := sshAuthMethods(sshRunRequest{AuthType: "key", KeyPath: keyPath})
	if cleanup != nil {
		cleanup()
	}
	if err == nil || !strings.Contains(err.Error(), "passphrase is required") {
		t.Fatalf("sshAuthMethods error = %v, want passphrase error", err)
	}
}

func TestNativeSSHRunnerReturnsConnectionFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	_, _, exitCode, err := nativeSSHRunner{}.Run(context.Background(), sshRunRequest{
		Host:                  "127.0.0.1",
		Port:                  port,
		Username:              "deploy",
		Command:               "true",
		AuthType:              "password",
		Password:              "secret",
		StrictHostKeyChecking: "no",
		ConnectTimeout:        100 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected connection failure")
	}
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
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

func testSSHPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	publicKey, err := ssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("new ssh public key: %v", err)
	}
	return publicKey
}

package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type fakeSSHRunner struct {
	name string
	args []string
	env  []string
}

func (r *fakeSSHRunner) Run(_ context.Context, name string, args ...string) (string, string, int, error) {
	r.name = name
	r.args = append([]string{}, args...)
	return "", "", 0, nil
}

func (r *fakeSSHRunner) RunWithEnv(_ context.Context, env []string, name string, args ...string) (string, string, int, error) {
	r.name = name
	r.args = append([]string{}, args...)
	r.env = append([]string{}, env...)
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

func TestSSHCommandArgsPasswordAuthAllowsPasswordPrompt(t *testing.T) {
	args, err := sshCommandArgs(map[string]any{
		"host":      "10.0.0.10",
		"port":      22,
		"username":  "deploy",
		"auth_type": "password",
	}, "uptime")
	if err != nil {
		t.Fatalf("sshCommandArgs returned error: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"BatchMode=no",
		"PreferredAuthentications=password,keyboard-interactive",
		"deploy@10.0.0.10",
		"uptime",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "BatchMode=yes") {
		t.Fatalf("password auth args still disable password prompting: %q", joined)
	}
}

func TestSSHExecutorUsesPasswordCredentialViaSSHPassEnv(t *testing.T) {
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
	if runner.name != "sshpass" {
		t.Fatalf("runner name = %q, want sshpass", runner.name)
	}
	joinedArgs := strings.Join(runner.args, " ")
	for _, want := range []string{"-e ssh", "BatchMode=no", "-p 8090", "root@10.0.0.10", "true"} {
		if !strings.Contains(joinedArgs, want) {
			t.Fatalf("args %q missing %q", joinedArgs, want)
		}
	}
	joinedEnv := strings.Join(runner.env, "\n")
	if !strings.Contains(joinedEnv, "SSHPASS=correct horse battery staple") {
		t.Fatalf("SSHPASS env missing: %q", joinedEnv)
	}
	for _, leaked := range []string{"correct horse battery staple", "SSHPASS"} {
		if strings.Contains(joinedArgs, leaked) || strings.Contains(fmt.Sprint(result.Details), leaked) {
			t.Fatalf("password leaked %q args=%q details=%#v", leaked, joinedArgs, result.Details)
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
	got := sanitizeSSHOutput("AWS_SECRET_ACCESS_KEY=abc123\nAuthorization: Bearer token-value\nPASSWORD=hunter2\nSSHPASS=secret-pass\ncurl -u user:pass --password pass2\njwt=eyJhbGciOiJIUzI1NiJ9.abc.def")
	for _, leaked := range []string{"abc123", "token-value", "hunter2", "secret-pass", "user:pass", "pass2", "eyJhbGciOiJIUzI1NiJ9.abc.def"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("sanitizeSSHOutput leaked %q in %q", leaked, got)
		}
	}
}

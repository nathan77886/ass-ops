package app

import (
	"context"
	"database/sql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type fakeGitCommandRunner struct {
	stdout string
	stderr string
	err    error
	calls  []fakeGitCommandCall
}

type fakeGitCommandCall struct {
	dir  string
	name string
	args []string
}

func (r *fakeGitCommandRunner) Run(ctx context.Context, dir, name string, args ...string) (string, string, error) {
	r.calls = append(r.calls, fakeGitCommandCall{dir: dir, name: name, args: append([]string(nil), args...)})
	return r.stdout, r.stderr, r.err
}

func newGitAdapterMockGorm(t *testing.T, sqlDB *sql.DB) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB, PreferSimpleProtocol: true}), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	return db
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	runGitOutput(t, dir, "git", args...)
}

func runGitOutput(t *testing.T, dir, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=ASSOPS",
		"GIT_AUTHOR_EMAIL=assops@local",
		"GIT_COMMITTER_NAME=ASSOPS",
		"GIT_COMMITTER_EMAIL=assops@local",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v: %s", name, strings.Join(args, " "), err, out)
	}
	return out
}

func TestGitRefsFromInput(t *testing.T) {
	tests := []struct {
		name          string
		input         any
		defaultBranch string
		wantBranches  []string
		wantTags      []string
	}{
		{
			name:          "nested refs",
			input:         map[string]any{"refs": map[string]any{"branches": []any{"main", "release"}, "tags": []any{"v1.0.0"}}},
			defaultBranch: "main",
			wantBranches:  []string{"main", "release"},
			wantTags:      []string{"v1.0.0"},
		},
		{
			name:          "default branch",
			input:         map[string]any{},
			defaultBranch: "develop",
			wantBranches:  []string{"develop"},
		},
		{
			name:          "scalar tag",
			input:         map[string]any{"refs": map[string]any{"tags": "v1.0.1"}},
			defaultBranch: "main",
			wantTags:      []string{"v1.0.1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gitRefsFromInput(tt.input, tt.defaultBranch)
			assertStringSlice(t, got.Branches, tt.wantBranches)
			assertStringSlice(t, got.Tags, tt.wantTags)
		})
	}
}

func TestRemoteURLFromRow(t *testing.T) {
	got := remoteURLFromRow(map[string]any{
		"remote_url": "",
		"urls":       []any{"git@example.com:org/repo.git"},
	})
	if got != "git@example.com:org/repo.git" {
		t.Fatalf("remoteURLFromRow = %q", got)
	}
}

func TestSafeLocalBareRemotePath(t *testing.T) {
	base := t.TempDir()
	if !safeLocalBareRemotePath(filepath.Join(base, "repo.git"), []string{base}) {
		t.Fatal("absolute local path should be accepted")
	}
	for _, path := range []string{"", "relative/repo.git", "https://example.com/repo.git", "git@example.com:org/repo.git", "/tmp/repo\x00.git"} {
		if safeLocalBareRemotePath(path, []string{base}) {
			t.Fatalf("safeLocalBareRemotePath(%q) = true, want false", path)
		}
	}
	if safeLocalBareRemotePath(filepath.Join(t.TempDir(), "repo.git"), []string{base}) {
		t.Fatal("path outside configured base dir should be rejected")
	}
	if safeLocalBareRemotePath(filepath.Join(base, "repo.git"), []string{string(filepath.Separator)}) {
		t.Fatal("root base dir should be rejected")
	}
}

func TestSafeResolvedLocalBareRemotePathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if safeResolvedLocalBareRemotePath(filepath.Join(link, "repo.git"), []string{base}) {
		t.Fatal("resolved path outside base should be rejected")
	}
	if err := os.MkdirAll(filepath.Join(base, "repos"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !safeResolvedLocalBareRemotePath(filepath.Join(base, "repos", "repo.git"), []string{base}) {
		t.Fatal("resolved path inside base should be accepted")
	}
}

func TestEnsureBareRepositoryHandlesNilResultAndSanitizesLocalPath(t *testing.T) {
	gitDir := filepath.Join(t.TempDir(), "repos", "repo.git")
	runner := &fakeGitCommandRunner{
		stdout: "true\n",
		stderr: "checking " + gitDir + "\n",
	}
	executor := &GitExecutor{Runner: runner}
	if err := executor.ensureBareRepository(context.Background(), nil, gitDir); err != nil {
		t.Fatalf("ensureBareRepository nil result: %v", err)
	}
	result := &gitExecutionResult{}
	if err := executor.ensureBareRepository(context.Background(), result, gitDir); err != nil {
		t.Fatalf("ensureBareRepository: %v", err)
	}
	if strings.Contains(result.Stderr, gitDir) || !strings.Contains(result.Stderr, "<local_bare>") {
		t.Fatalf("local path should be sanitized, stderr=%q", result.Stderr)
	}
}

func TestProvisionTemplateRepositoryCreatesLocalBareRepoAndPushesFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary is required for template repository provisioning test")
	}
	root := t.TempDir()
	remotePath := filepath.Join(root, "repos", "billing.git")
	executor := &GitExecutor{WorkDir: filepath.Join(root, "work"), LocalBareBaseDirs: []string{filepath.Join(root, "repos")}}
	result, err := executor.ProvisionTemplateRepository(context.Background(),
		map[string]any{"id": "repo-1", "repo_key": "billing-service", "default_branch": "main"},
		[]map[string]any{{"id": "remote-1", "provider_type": "local_bare", "remote_url": remotePath}},
		[]map[string]any{
			{"path": "README.md", "content": "# Billing\n"},
			{"path": "docs/context.md", "content": "asset graph\n"},
		},
	)
	if err != nil {
		t.Fatalf("ProvisionTemplateRepository: %v\nstdout=%s\nstderr=%s", err, result.Stdout, result.Stderr)
	}
	if result.AfterSHA == "" {
		t.Fatal("AfterSHA should be populated")
	}
	if result.Details["provisioned"] != true {
		t.Fatalf("provisioned = %v, want true", result.Details["provisioned"])
	}
	cmd := exec.Command("git", "--git-dir", remotePath, "show", "refs/heads/main:README.md")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git show pushed README: %v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "# Billing" {
		t.Fatalf("README content = %q", out)
	}
}

func TestSafeResolvedLocalBareRemotePathRejectsFinalSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "repos")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "config.git")
	target := filepath.Join(outside, "config.git")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if safeResolvedLocalBareRemotePath(link, []string{base}) {
		t.Fatal("final symlink escaping outside base should be rejected")
	}
}

func TestProvisionTemplateRepositoryIdempotentWhenBareRepoExists(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary is required for template repository provisioning test")
	}
	root := t.TempDir()
	remotePath := filepath.Join(root, "repos", "billing.git")
	executor := &GitExecutor{WorkDir: filepath.Join(root, "work"), LocalBareBaseDirs: []string{filepath.Join(root, "repos")}}
	repo := map[string]any{"id": "repo-1", "repo_key": "billing-service", "default_branch": "main"}
	remotes := []map[string]any{{"id": "remote-1", "provider_type": "local_bare", "remote_url": remotePath}}
	files := []map[string]any{{"path": "README.md", "content": "# Billing\n"}}
	first, err := executor.ProvisionTemplateRepository(context.Background(), repo, remotes, files)
	if err != nil {
		t.Fatalf("first ProvisionTemplateRepository: %v", err)
	}
	second, err := executor.ProvisionTemplateRepository(context.Background(), repo, remotes, []map[string]any{{"path": "README.md", "content": "# Changed\n"}})
	if err != nil {
		t.Fatalf("second ProvisionTemplateRepository: %v", err)
	}
	if first.AfterSHA == "" || second.AfterSHA != first.AfterSHA {
		t.Fatalf("second SHA = %q, want first SHA %q", second.AfterSHA, first.AfterSHA)
	}
	if second.Details["already_provisioned"] != true {
		t.Fatalf("already_provisioned = %v, want true", second.Details["already_provisioned"])
	}
	cmd := exec.Command("git", "--git-dir", remotePath, "show", "refs/heads/main:README.md")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git show pushed README: %v: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "# Billing" {
		t.Fatalf("README content changed on idempotent provisioning: %q", out)
	}
}

func TestProvisionTemplateRepositorySkipsWhenNoLocalBareRemote(t *testing.T) {
	result, err := (&GitExecutor{}).ProvisionTemplateRepository(context.Background(),
		map[string]any{"id": "repo-1", "repo_key": "billing-service", "default_branch": "main"},
		[]map[string]any{{"id": "remote-1", "provider_type": "github", "remote_url": "git@example.com:org/repo.git"}},
		nil,
	)
	if err != nil {
		t.Fatalf("ProvisionTemplateRepository: %v", err)
	}
	if result.Details["provisioned"] != false {
		t.Fatalf("provisioned = %v, want false", result.Details["provisioned"])
	}
	if result.Details["reason"] == "" {
		t.Fatal("skip result should include a reason")
	}
}

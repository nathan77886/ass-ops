package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostgresProcessDatabaseURLStripsPassword(t *testing.T) {
	got, env, secrets, err := postgresProcessDatabaseURL("postgres://assops:secret-pass@postgres:5432/assops?sslmode=disable")
	if err != nil {
		t.Fatalf("postgresProcessDatabaseURL: %v", err)
	}
	if strings.Contains(got, "secret-pass") {
		t.Fatalf("database URL leaked password: %q", got)
	}
	if got != "postgres://assops@postgres:5432/assops?sslmode=disable" {
		t.Fatalf("database URL = %q", got)
	}
	if len(env) != 1 || env[0] != "PGPASSWORD=secret-pass" {
		t.Fatalf("env = %#v", env)
	}
	if len(secrets) != 2 {
		t.Fatalf("secrets = %#v", secrets)
	}
}

func TestPostgresProcessDatabaseURLRejectsKeywordPassword(t *testing.T) {
	_, _, _, err := postgresProcessDatabaseURL("host=localhost user=assops password=secret dbname=assops")
	if err == nil {
		t.Fatal("expected keyword DSN with password to be rejected")
	}
}

func TestValidateRestoreRehearsalTarget(t *testing.T) {
	current := "postgres://assops:secret@localhost:5432/assops?sslmode=disable"
	if err := validateRestoreRehearsalTarget(current, "postgres://assops:secret@localhost:5432/assops?sslmode=disable", false); err == nil {
		t.Fatal("same target should be rejected")
	}
	if err := validateRestoreRehearsalTarget(current, "postgres://assops:secret@127.0.0.1:5432/assops?sslmode=disable", true); err == nil {
		t.Fatal("localhost and 127.0.0.1 same database should be rejected")
	}
	if err := validateRestoreRehearsalTarget(current, "postgres://assops:secret@0.0.0.0:5432/assops?sslmode=disable", true); err == nil {
		t.Fatal("localhost and 0.0.0.0 same database should be rejected")
	}
	if err := validateRestoreRehearsalTarget(current, "postgres://assops:secret@localhost:5432/assops?sslmode=require", true); err == nil {
		t.Fatal("same database with different query params should be rejected")
	}
	if err := validateRestoreRehearsalTarget(current, "postgres://assops:secret@localhost:5432/assops_prod?sslmode=disable", false); err == nil {
		t.Fatal("non-disposable target should be rejected without override")
	}
	if err := validateRestoreRehearsalTarget(current, "postgres://assops:secret@localhost:5432/assops_restore_test?sslmode=disable", false); err != nil {
		t.Fatalf("restore test target should be accepted: %v", err)
	}
	if err := validateRestoreRehearsalTarget("", "postgres://assops:secret@localhost:5432/assops_restore_test?sslmode=disable", false); err != nil {
		t.Fatalf("empty current database URL should not block disposable target: %v", err)
	}
	if err := validateRestoreRehearsalTarget(current, "postgres://assops:secret@localhost:5432/assops_prod?sslmode=disable", true); err != nil {
		t.Fatalf("override should accept target: %v", err)
	}
	if err := validateRestoreRehearsalTarget(current, "host=localhost dbname=assops_restore_test", true); err == nil || !strings.Contains(err.Error(), "URL-style") {
		t.Fatalf("keyword DSN should get URL-style error, got %v", err)
	}
}

func TestConfirmDestructiveRestore(t *testing.T) {
	dbURL := "postgres://assops:secret@localhost:5432/assops?sslmode=disable"
	if err := confirmDestructiveRestore(dbURL, ""); err == nil {
		t.Fatal("restore should require explicit database confirmation")
	}
	if err := confirmDestructiveRestore(dbURL, "wrong"); err == nil {
		t.Fatal("wrong confirmation should be rejected")
	}
	if err := confirmDestructiveRestore(dbURL, "assops"); err != nil {
		t.Fatalf("matching confirmation should be accepted: %v", err)
	}
}

func TestRedactedDatabaseURL(t *testing.T) {
	got := redactedDatabaseURL("postgres://assops:secret-pass@localhost:5432/assops_restore_test?sslmode=disable")
	if strings.Contains(got, "secret-pass") {
		t.Fatalf("redactedDatabaseURL leaked password: %q", got)
	}
	if !strings.Contains(got, "assops_restore_test") {
		t.Fatalf("redactedDatabaseURL should retain database name: %q", got)
	}
}

func TestWriteJSONReportCreatesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release-notes", "restore-rehearsal.json")
	if err := writeJSONReport(path, map[string]any{"target_database": "postgres://assops@db:5432/assops_restore_test"}); err != nil {
		t.Fatalf("writeJSONReport: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat report: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("report mode = %#o, want 0600", info.Mode().Perm())
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(bytes), "assops_restore_test") || !strings.HasSuffix(string(bytes), "\n") {
		t.Fatalf("report content = %q", bytes)
	}
}

func TestValidateReleaseBundleAcceptsCompleteArtifacts(t *testing.T) {
	artifactDir := t.TempDir()
	files := map[string]string{
		"assops-tool-v1.0.0-linux-amd64.tar.gz": "binary",
		"assops-web-v1.0.0.tar.gz":              "web",
		"assops-0.1.0.tgz":                      "helm",
	}
	writeSHA256SUMS(t, artifactDir, files)
	reportPath := writeValidRehearsalReport(t, artifactDir, "postgres://assops@postgres:5432/assops_restore_test?sslmode=disable")

	result, err := validateReleaseBundle(artifactDir, reportPath)
	if err != nil {
		t.Fatalf("validateReleaseBundle: %v", err)
	}
	if result["valid"] != true {
		t.Fatalf("result valid = %#v", result["valid"])
	}
	if result["checksum_entries"] != len(files) || result["checksum_verified"] != len(files) {
		t.Fatalf("checksum result = %#v", result)
	}
}

func TestValidateReleaseBundleRejectsBadChecksum(t *testing.T) {
	artifactDir := t.TempDir()
	files := map[string]string{
		"assops-tool-v1.0.0-linux-amd64.tar.gz": "binary",
		"assops-web-v1.0.0.tar.gz":              "web",
		"assops-0.1.0.tgz":                      "helm",
	}
	writeSHA256SUMS(t, artifactDir, files)
	if err := os.WriteFile(filepath.Join(artifactDir, "assops-web-v1.0.0.tar.gz"), []byte("changed"), 0o600); err != nil {
		t.Fatalf("tamper artifact: %v", err)
	}
	reportPath := writeValidRehearsalReport(t, artifactDir, "postgres://assops@postgres:5432/assops_restore_test?sslmode=disable")

	err := expectReleaseBundleError(artifactDir, reportPath)
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestValidateReleaseBundleRejectsLeakyRehearsalTarget(t *testing.T) {
	artifactDir := t.TempDir()
	files := map[string]string{
		"assops-tool-v1.0.0-linux-amd64.tar.gz": "binary",
		"assops-web-v1.0.0.tar.gz":              "web",
		"assops-0.1.0.tgz":                      "helm",
	}
	writeSHA256SUMS(t, artifactDir, files)
	reportPath := writeValidRehearsalReport(t, artifactDir, "postgres://assops:secret@postgres:5432/assops_restore_test?sslmode=disable")

	err := expectReleaseBundleError(artifactDir, reportPath)
	if !strings.Contains(err.Error(), "must not include a password") {
		t.Fatalf("expected password leak error, got %v", err)
	}
}

func TestReadChecksumFileRejectsUnsafePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SHA256SUMS")
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("evil")))
	if err := os.WriteFile(path, []byte(hash+"  ../evil.tar.gz\n"), 0o600); err != nil {
		t.Fatalf("write SHA256SUMS: %v", err)
	}
	if _, err := readChecksumFile(path); err == nil {
		t.Fatal("expected unsafe checksum path to be rejected")
	}
}

func TestReleaseHelmValuesGeneratesGHCRImages(t *testing.T) {
	values, err := releaseHelmValues("Nathan77886", "v0.1.0")
	if err != nil {
		t.Fatalf("releaseHelmValues: %v", err)
	}
	for _, want := range []string{
		"registry: ghcr.io",
		"repository: nathan77886/assops-gateway",
		"repository: nathan77886/assops-worker",
		"repository: nathan77886/assops-node-worker",
		"repository: nathan77886/assops-web",
		"tag: v0.1.0",
		`version: "v0.1.0"`,
		`commit: "release-commit-not-set"`,
		`buildTime: "release-build-time-not-set"`,
	} {
		if !strings.Contains(values, want) {
			t.Fatalf("releaseHelmValues missing %q in:\n%s", want, values)
		}
	}
}

func TestReleaseHelmValuesIncludesReleaseMetadataFromEnvironment(t *testing.T) {
	t.Setenv("ASSOPS_RELEASE_COMMIT", "abc123def456")
	t.Setenv("ASSOPS_RELEASE_BUILD_TIME", "2026-06-26T12:34:56Z")

	values, err := releaseHelmValues("nathan77886", "v0.1.0")
	if err != nil {
		t.Fatalf("releaseHelmValues: %v", err)
	}
	for _, want := range []string{
		`version: "v0.1.0"`,
		`commit: "abc123def456"`,
		`buildTime: "2026-06-26T12:34:56Z"`,
	} {
		if !strings.Contains(values, want) {
			t.Fatalf("releaseHelmValues missing %q in:\n%s", want, values)
		}
	}
}

func TestReleaseHelmValuesRejectsRepositoryPath(t *testing.T) {
	if _, err := releaseHelmValues("owner/repo", "v0.1.0"); err == nil {
		t.Fatal("expected owner/repo to be rejected")
	}
	if _, err := releaseHelmValues("owner", "v0.1.0 bad"); err == nil {
		t.Fatal("expected version with whitespace to be rejected")
	}
}

func TestWriteTextFileCreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release", "values.yaml")
	if err := writeTextFile(path, "image:\n"); err != nil {
		t.Fatalf("writeTextFile: %v", err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read values: %v", err)
	}
	if string(bytes) != "image:\n" {
		t.Fatalf("values content = %q", bytes)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat values: %v", err)
	}
	if info.Mode().Perm()&0o111 != 0 || info.Mode().Perm()&0o400 == 0 {
		t.Fatalf("values mode = %#o, want owner-readable non-executable file", info.Mode().Perm())
	}
}

func TestWriteTextFileRejectsParentTraversal(t *testing.T) {
	for _, path := range []string{"../values.yaml", "release/../values.yaml", `release\..\values.yaml`} {
		if err := writeTextFile(path, "image:\n"); err == nil || !strings.Contains(err.Error(), "parent directory traversal") {
			t.Fatalf("writeTextFile(%q) error = %v, want traversal rejection", path, err)
		}
	}
}

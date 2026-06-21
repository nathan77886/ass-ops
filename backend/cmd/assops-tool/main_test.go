package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestReleasePromotionPlanIncludesVerificationAndRollout(t *testing.T) {
	artifactDir := t.TempDir()
	files := map[string]string{
		"assops-v0.1.0-linux-amd64.tar.gz": "binary",
		"assops-web-v0.1.0.tar.gz":         "web",
		"assops-0.1.0.tgz":                 "helm",
	}
	writeSHA256SUMS(t, artifactDir, files)
	reportPath := writeValidRehearsalReport(t, artifactDir, "postgres://assops@postgres:5432/assops_restore_test?sslmode=disable")
	valuesPath := filepath.Join(artifactDir, "helm-values.yaml")
	values, err := releaseHelmValues("Nathan77886", "v0.1.0")
	if err != nil {
		t.Fatalf("releaseHelmValues: %v", err)
	}
	if err := writeTextFile(valuesPath, values); err != nil {
		t.Fatalf("write values: %v", err)
	}

	plan, err := releasePromotionPlan("nathan77886/ass-ops", "Nathan77886", "v0.1.0", artifactDir, reportPath, valuesPath)
	if err != nil {
		t.Fatalf("releasePromotionPlan: %v", err)
	}
	for _, want := range []string{
		"# ASSOPS Promotion Plan v0.1.0",
		"Helm values sha256:",
		"assops-tool release validate-bundle",
		"gh attestation verify",
		"oci://ghcr.io/nathan77886/assops-gateway:v0.1.0",
		"helm template assops deploy/helm/assops",
		"helm upgrade --install assops deploy/helm/assops",
	} {
		if !strings.Contains(plan, want) {
			t.Fatalf("promotion plan missing %q in:\n%s", want, plan)
		}
	}
}

func TestReleasePromotionPlanRejectsInvalidRepo(t *testing.T) {
	if _, err := releasePromotionPlan("owner", "owner", "v0.1.0", "missing", "missing", "missing"); err == nil {
		t.Fatal("expected invalid owner/repo to be rejected")
	}
}

func TestReleasePromotionPlanRejectsMismatchedHelmValues(t *testing.T) {
	artifactDir := t.TempDir()
	files := map[string]string{
		"assops-v0.1.0-linux-amd64.tar.gz": "binary",
		"assops-web-v0.1.0.tar.gz":         "web",
		"assops-0.1.0.tgz":                 "helm",
	}
	writeSHA256SUMS(t, artifactDir, files)
	reportPath := writeValidRehearsalReport(t, artifactDir, "postgres://assops@postgres:5432/assops_restore_test?sslmode=disable")
	valuesPath := filepath.Join(artifactDir, "helm-values.yaml")
	values, err := releaseHelmValues("nathan77886", "v0.2.0")
	if err != nil {
		t.Fatalf("releaseHelmValues: %v", err)
	}
	if err := writeTextFile(valuesPath, values); err != nil {
		t.Fatalf("write values: %v", err)
	}

	_, err = releasePromotionPlan("nathan77886/ass-ops", "nathan77886", "v0.1.0", artifactDir, reportPath, valuesPath)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected mismatched helm values error, got %v", err)
	}
}

func TestCountNonEmptyLines(t *testing.T) {
	if got := countNonEmptyLines("one\n\n two \n"); got != 2 {
		t.Fatalf("countNonEmptyLines = %d, want 2", got)
	}
}

func TestPGRestoreListObjectCounts(t *testing.T) {
	counts := pgRestoreListObjectCounts(`
;
; Archive created at 2026-06-22
;
1234; 1259 20001 TABLE public projects assops
1235; 1259 20002 TABLE public users assops
1236; 1259 20003 INDEX public projects_pkey assops
1237; 0 0 ACL public projects assops
`)
	if counts["TABLE"] != 2 || counts["INDEX"] != 1 || counts["ACL"] != 1 {
		t.Fatalf("counts = %#v", counts)
	}
}

func TestSanitizeCommandOutput(t *testing.T) {
	got := sanitizeCommandOutput("failed postgres://assops:secret@db/assops password secret", []string{"postgres://assops:secret@db/assops", "secret"})
	if strings.Contains(got, "secret") || strings.Contains(got, "postgres://assops:secret@db/assops") {
		t.Fatalf("output leaked secret: %q", got)
	}
}

func TestNextBackupPathAllocatesSuffixWhenTimestampExists(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 22, 12, 34, 56, 0, time.UTC)
	first, err := nextBackupPath(dir, now)
	if err != nil {
		t.Fatalf("nextBackupPath first: %v", err)
	}
	if filepath.Base(first) != "assops-20260622-123456.dump" {
		t.Fatalf("first path = %s", first)
	}
	if err := os.WriteFile(first, []byte("backup"), 0o600); err != nil {
		t.Fatalf("write first backup: %v", err)
	}
	second, err := nextBackupPath(dir, now)
	if err != nil {
		t.Fatalf("nextBackupPath second: %v", err)
	}
	if filepath.Base(second) != "assops-20260622-123456-001.dump" {
		t.Fatalf("second path = %s", second)
	}
}

func TestPruneBackupsKeepsNewestManagedFilesOnly(t *testing.T) {
	dir := t.TempDir()
	files := []struct {
		name string
		age  time.Duration
	}{
		{name: "assops-20260622-120000.dump", age: 4 * time.Hour},
		{name: "assops-20260622-130000.dump", age: 3 * time.Hour},
		{name: "assops-20260622-140000.dump", age: 2 * time.Hour},
		{name: "assops-20260622-150000.dump", age: 1 * time.Hour},
		{name: "manual.dump", age: 5 * time.Hour},
	}
	now := time.Now()
	for _, file := range files {
		path := filepath.Join(dir, file.name)
		if err := os.WriteFile(path, []byte(file.name), 0o600); err != nil {
			t.Fatalf("write %s: %v", file.name, err)
		}
		ts := now.Add(-file.age)
		if err := os.Chtimes(path, ts, ts); err != nil {
			t.Fatalf("chtimes %s: %v", file.name, err)
		}
	}

	pruned, err := pruneBackups(dir, 2)
	if err != nil {
		t.Fatalf("pruneBackups: %v", err)
	}
	if len(pruned) != 2 {
		t.Fatalf("pruned = %#v, want 2 files", pruned)
	}
	for _, name := range []string{"assops-20260622-140000.dump", "assops-20260622-150000.dump", "manual.dump"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s should remain: %v", name, err)
		}
	}
	for _, name := range []string{"assops-20260622-120000.dump", "assops-20260622-130000.dump"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should be pruned, stat err = %v", name, err)
		}
	}
}

func TestAcquireBackupDirLockPreventsConcurrentLock(t *testing.T) {
	dir := t.TempDir()
	unlock, err := acquireBackupDirLock(dir)
	if err != nil {
		t.Fatalf("acquireBackupDirLock first: %v", err)
	}
	if _, err := acquireBackupDirLock(dir); err == nil {
		t.Fatal("expected second lock acquisition to fail")
	}
	unlock()
	unlockAgain, err := acquireBackupDirLock(dir)
	if err != nil {
		t.Fatalf("acquireBackupDirLock after unlock: %v", err)
	}
	unlockAgain()
}

func TestListManagedBackupsSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	backupPath := filepath.Join(dir, "assops-20260622-150000.dump")
	if err := os.WriteFile(backupPath, []byte("backup"), 0o600); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	targetPath := filepath.Join(dir, "manual-target.dump")
	if err := os.WriteFile(targetPath, []byte("manual"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(targetPath, filepath.Join(dir, "assops-manual-snapshot.dump")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	backups, err := listManagedBackups(dir)
	if err != nil {
		t.Fatalf("listManagedBackups: %v", err)
	}
	if len(backups) != 1 || backups[0].name != filepath.Base(backupPath) {
		t.Fatalf("backups = %#v", backups)
	}
}

func writeSHA256SUMS(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	var lines []string
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write artifact %s: %v", name, err)
		}
		lines = append(lines, fmt.Sprintf("%x  %s", sha256.Sum256([]byte(content)), name))
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write SHA256SUMS: %v", err)
	}
}

func writeValidRehearsalReport(t *testing.T, dir, targetDatabase string) string {
	t.Helper()
	path := filepath.Join(dir, "restore-rehearsal.json")
	if err := writeJSONReport(path, map[string]any{
		"backup":               "/backups/assops-20260622-120000.dump",
		"target_database":      targetDatabase,
		"backup_object_counts": map[string]int{"TABLE": 2},
		"migrations":           []map[string]string{{"filename": "001_init.sql"}},
		"rehearsed_at":         time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("write rehearsal report: %v", err)
	}
	return path
}

func expectReleaseBundleError(artifactDir, reportPath string) error {
	_, err := validateReleaseBundle(artifactDir, reportPath)
	if err == nil {
		return fmt.Errorf("expected validateReleaseBundle to fail")
	}
	return err
}

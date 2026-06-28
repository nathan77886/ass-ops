package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReleaseBackupSchedulePlanRejectsUnsafeInputs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "invalid repo",
			args: []string{"owner", "production", "ubuntu-latest", "17 3 * * 1", "artifact:backup", "14"},
			want: "owner/repo",
		},
		{
			name: "unsafe cron",
			args: []string{"owner/repo", "production", "ubuntu-latest", "17 3 * * 1; curl bad", "artifact:backup", "14"},
			want: "five-field",
		},
		{
			name: "timezone cron prefix rejected",
			args: []string{"owner/repo", "production", "ubuntu-latest", "CRON_TZ=Asia/Shanghai 17 3 * * 1", "artifact:backup", "14"},
			want: "five-field",
		},
		{
			name: "empty environment",
			args: []string{"owner/repo", "", "ubuntu-latest", "17 3 * * 1", "artifact:backup", "14"},
			want: "environment",
		},
		{
			name: "empty runner",
			args: []string{"owner/repo", "production", "", "17 3 * * 1", "artifact:backup", "14"},
			want: "runner",
		},
		{
			name: "empty source",
			args: []string{"owner/repo", "production", "ubuntu-latest", "17 3 * * 1", "", "14"},
			want: "backup source",
		},
		{
			name: "artifact source without value",
			args: []string{"owner/repo", "production", "ubuntu-latest", "17 3 * * 1", "artifact:", "14"},
			want: "value is required",
		},
		{
			name: "artifact source with path characters",
			args: []string{"owner/repo", "production", "ubuntu-latest", "17 3 * * 1", "artifact:/mnt/backup.dump", "14"},
			want: "artifact name",
		},
		{
			name: "path source needs self hosted",
			args: []string{"owner/repo", "production", "ubuntu-latest", "17 3 * * 1", "path:/mnt/backups/assops.dump", "14"},
			want: "self-hosted",
		},
		{
			name: "unsafe path",
			args: []string{"owner/repo", "production", "self-hosted", "17 3 * * 1", "path:/mnt/../secret.dump", "14"},
			want: "unsupported",
		},
		{
			name: "retention too long",
			args: []string{"owner/repo", "production", "ubuntu-latest", "17 3 * * 1", "artifact:backup", "120"},
			want: "between 1 and 90",
		},
		{
			name: "retention zero",
			args: []string{"owner/repo", "production", "ubuntu-latest", "17 3 * * 1", "artifact:backup", "0"},
			want: "between 1 and 90",
		},
		{
			name: "retention negative",
			args: []string{"owner/repo", "production", "ubuntu-latest", "17 3 * * 1", "artifact:backup", "-1"},
			want: "between 1 and 90",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := releaseBackupSchedulePlan(tc.args[0], tc.args[1], tc.args[2], tc.args[3], tc.args[4], tc.args[5])
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("releaseBackupSchedulePlan error = %v, want containing %q", err, tc.want)
			}
		})
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

func TestBackupAndRestoreCommandsUseNoPasswordPrompt(t *testing.T) {
	source := readSourceGlob(t, "main_part*.go")
	if got := strings.Count(source, `"pg_dump", "-Fc", "--no-owner", "--no-password", "--file"`); got < 2 {
		t.Fatalf("backup pg_dump commands should use --no-password, found %d guarded invocations", got)
	}
	if got := strings.Count(source, `"pg_restore", "--clean", "--if-exists", "--no-owner", "--no-password", "--dbname"`); got < 2 {
		t.Fatalf("restore pg_restore commands should use --no-password, found %d guarded invocations", got)
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

func readinessByKey(t *testing.T, report map[string]any, key string) readinessRow {
	t.Helper()
	items, ok := report["items"].([]readinessRow)
	if !ok {
		t.Fatalf("report items type = %T", report["items"])
	}
	for _, item := range items {
		if item.Key == key {
			return item
		}
	}
	t.Fatalf("readiness item %q not found", key)
	return readinessRow{}
}

func mapFromAny(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	default:
		return nil
	}
}

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func validateRehearsalReport(path string) (map[string]any, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading restore rehearsal report: %w", err)
	}
	var report map[string]any
	if err := json.Unmarshal(bytes, &report); err != nil {
		return nil, fmt.Errorf("parsing restore rehearsal report: %w", err)
	}
	backup, err := reportString(report, "backup")
	if err != nil {
		return nil, err
	}
	targetDatabase, err := reportString(report, "target_database")
	if err != nil {
		return nil, err
	}
	if err := validateReportDatabaseURL(targetDatabase); err != nil {
		return nil, err
	}
	rehearsedAt, err := reportString(report, "rehearsed_at")
	if err != nil {
		return nil, err
	}
	if _, err := time.Parse(time.RFC3339, rehearsedAt); err != nil {
		return nil, fmt.Errorf("restore rehearsal report rehearsed_at must be RFC3339: %w", err)
	}
	migrations, ok := report["migrations"].([]any)
	if !ok || len(migrations) == 0 {
		return nil, fmt.Errorf("restore rehearsal report migrations must be a non-empty array")
	}
	counts, ok := report["backup_object_counts"].(map[string]any)
	if !ok || len(counts) == 0 {
		return nil, fmt.Errorf("restore rehearsal report backup_object_counts must be a non-empty object")
	}
	return map[string]any{
		"path":                path,
		"backup":              backup,
		"target_database":     targetDatabase,
		"migration_count":     len(migrations),
		"backup_object_types": len(counts),
		"rehearsed_at":        rehearsedAt,
	}, nil
}

func reportString(report map[string]any, key string) (string, error) {
	value, ok := report[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("restore rehearsal report %s must be a non-empty string", key)
	}
	return value, nil
}

func validateReportDatabaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return fmt.Errorf("restore rehearsal report target_database must be URL-style")
	}
	if _, hasPassword := parsed.User.Password(); hasPassword {
		return fmt.Errorf("restore rehearsal report target_database must not include a password")
	}
	return nil
}

func validateRestoreRehearsalTarget(currentDatabaseURL, targetDatabaseURL string, allowOverride bool) error {
	current, err := normalizeDatabaseURLForCompare(currentDatabaseURL)
	if err != nil && strings.TrimSpace(currentDatabaseURL) != "" {
		return fmt.Errorf("invalid DATABASE_URL: %w", err)
	}
	target, err := normalizeDatabaseURLForCompare(targetDatabaseURL)
	if err != nil {
		return fmt.Errorf("invalid restore rehearsal target database URL: %w", err)
	}
	if target == "" {
		return fmt.Errorf("restore rehearsal target database URL is required")
	}
	if current != "" && target == current {
		return fmt.Errorf("restore rehearsal target must not equal DATABASE_URL")
	}
	if allowOverride {
		return nil
	}
	dbName, err := databaseNameFromURL(targetDatabaseURL)
	if err != nil {
		return err
	}
	lowerName := strings.ToLower(dbName)
	for _, token := range []string{"rehears", "restore", "test", "tmp", "scratch", "disposable"} {
		if strings.Contains(lowerName, token) {
			return nil
		}
	}
	return fmt.Errorf("restore rehearsal target database name %q must look disposable; include rehearsal/test/tmp/restore/scratch or set ASSOPS_ALLOW_RESTORE_REHEARSAL_TARGET=1", dbName)
}

func confirmDestructiveRestore(databaseURL, confirmation string) error {
	dbName, err := databaseNameFromURL(databaseURL)
	if err != nil {
		return err
	}
	if confirmation != dbName {
		return fmt.Errorf("db restore is destructive; set ASSOPS_CONFIRM_DB_RESTORE=%s to confirm target database", dbName)
	}
	return nil
}

func normalizeDatabaseURLForCompare(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return "", fmt.Errorf("database URL must be URL-style")
	}
	host := canonicalDBHost(parsed.Hostname())
	port := parsed.Port()
	if port == "" {
		port = defaultDatabasePort(parsed.Scheme)
	}
	dbName := strings.Trim(strings.TrimSpace(parsed.Path), "/")
	if dbName == "" {
		return "", fmt.Errorf("database URL must include a database name")
	}
	return strings.Join([]string{strings.ToLower(parsed.Scheme), host, port, dbName}, "|"), nil
}

func canonicalDBHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	switch host {
	case "localhost", "::1", "[::1]", "0.0.0.0":
		return "127.0.0.1"
	default:
		return host
	}
}

func defaultDatabasePort(scheme string) string {
	switch strings.ToLower(scheme) {
	case "postgres", "postgresql":
		return "5432"
	default:
		return ""
	}
}

func databaseNameFromURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return "", fmt.Errorf("database URL must be URL-style")
	}
	name := strings.Trim(strings.TrimSpace(parsed.Path), "/")
	if name == "" {
		return "", fmt.Errorf("database URL must include a database name")
	}
	return name, nil
}

func redactedDatabaseURL(raw string) string {
	redacted, _, _, err := postgresProcessDatabaseURL(raw)
	if err != nil {
		return "<invalid>"
	}
	return redacted
}

func countNonEmptyLines(output string) int {
	count := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func pgRestoreListObjectCounts(output string) map[string]int {
	counts := map[string]int{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		// pg_restore --list TOC lines are stable as:
		// dumpID; catalogOID objectOID OBJECT_TYPE schema name owner
		objectType := fields[3]
		counts[objectType]++
	}
	return counts
}

func acquireBackupDirLock(dir string) (func(), error) {
	lockPath := filepath.Join(dir, ".assops-backup.lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("backup directory is already locked: %s", lockPath)
		}
		return nil, fmt.Errorf("creating backup lock: %w", err)
	}
	_, _ = fmt.Fprintf(file, "pid=%d\n", os.Getpid())
	return func() {
		_ = file.Close()
		_ = os.Remove(lockPath)
	}, nil
}

func nextBackupPath(dir string, now time.Time) (string, error) {
	base := "assops-" + now.UTC().Format("20060102-150405") + ".dump"
	path := filepath.Join(dir, base)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path, nil
	} else if err != nil {
		return "", fmt.Errorf("checking backup path: %w", err)
	}
	for i := 1; i < 1000; i++ {
		path = filepath.Join(dir, fmt.Sprintf("assops-%s-%03d.dump", now.UTC().Format("20060102-150405"), i))
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return path, nil
		} else if err != nil {
			return "", fmt.Errorf("checking backup path: %w", err)
		}
	}
	return "", fmt.Errorf("could not allocate unique backup filename in %s", dir)
}

type backupFile struct {
	path    string
	name    string
	modTime time.Time
}

func pruneBackups(dir string, keep int) ([]string, error) {
	if keep < 1 {
		return nil, fmt.Errorf("backup retention KEEP must be a positive integer")
	}
	backups, err := listManagedBackups(dir)
	if err != nil {
		return nil, err
	}
	if len(backups) <= keep {
		return []string{}, nil
	}
	var pruned []string
	for _, backup := range backups[keep:] {
		if err := os.Remove(backup.path); err != nil {
			return nil, fmt.Errorf("pruning backup %s: %w", backup.path, err)
		}
		pruned = append(pruned, backup.path)
	}
	return pruned, nil
}

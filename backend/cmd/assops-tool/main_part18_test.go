package main

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

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

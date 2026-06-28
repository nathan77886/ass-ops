package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readSourceGlob(t *testing.T, pattern string) string {
	t.Helper()
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	if len(paths) == 0 {
		t.Fatalf("glob %s: no matches", pattern)
	}
	var b strings.Builder
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		b.Write(content)
		b.WriteByte('\n')
	}
	return b.String()
}

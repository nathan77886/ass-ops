package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func isSafeKubernetesNamespace(value string) bool {
	if value == "" || len(value) > 63 {
		return false
	}
	if value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= '0' && char <= '9':
		case char == '-':
		default:
			return false
		}
	}
	return true
}

func hasUppercaseASCII(value string) bool {
	for _, char := range value {
		if char >= 'A' && char <= 'Z' {
			return true
		}
	}
	return false
}

func toolMapFromAny(value any) map[string]any {
	if item, ok := value.(map[string]any); ok {
		return item
	}
	return nil
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	default:
		n, _ := strconv.Atoi(strings.TrimSpace(fmt.Sprint(value)))
		return n
	}
}

func toolContainsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func stringSliceFromAny(value any) []string {
	items, ok := value.([]string)
	if ok {
		return items
	}
	rawItems, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(rawItems))
	for _, item := range rawItems {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func isContainerPathSegment(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= '0' && char <= '9':
		case char == '-', char == '_', char == '.':
		default:
			return false
		}
	}
	return true
}

func validateReleaseBundle(artifactDir, rehearsalReport string) (map[string]any, error) {
	artifactDir = strings.TrimSpace(artifactDir)
	rehearsalReport = strings.TrimSpace(rehearsalReport)
	if artifactDir == "" {
		return nil, fmt.Errorf("release artifact directory is required")
	}
	if rehearsalReport == "" {
		return nil, fmt.Errorf("restore rehearsal report path is required")
	}
	info, err := os.Stat(artifactDir)
	if err != nil {
		return nil, fmt.Errorf("checking release artifact directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("release artifact path is not a directory: %s", artifactDir)
	}
	checksums, err := readChecksumFile(filepath.Join(artifactDir, "SHA256SUMS"))
	if err != nil {
		return nil, err
	}
	for name, expected := range checksums {
		actual, err := sha256File(filepath.Join(artifactDir, name))
		if err != nil {
			return nil, fmt.Errorf("verifying checksum for %s: %w", name, err)
		}
		if actual != expected {
			return nil, fmt.Errorf("checksum mismatch for %s", name)
		}
	}
	artifacts, err := releaseArtifactSummary(artifactDir, checksums)
	if err != nil {
		return nil, err
	}
	report, err := validateRehearsalReport(rehearsalReport)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"valid":             true,
		"artifact_dir":      artifactDir,
		"checksum_file":     filepath.Join(artifactDir, "SHA256SUMS"),
		"checksum_entries":  len(checksums),
		"checksum_verified": len(checksums),
		"artifacts":         artifacts,
		"rehearsal_report":  report,
	}, nil
}

func readChecksumFile(path string) (map[string]string, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading SHA256SUMS: %w", err)
	}
	checksums := map[string]string{}
	for index, rawLine := range strings.Split(string(bytes), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid SHA256SUMS line %d", index+1)
		}
		hash := strings.ToLower(fields[0])
		name := strings.TrimPrefix(fields[1], "*")
		if !isSHA256Hex(hash) {
			return nil, fmt.Errorf("invalid SHA256 hash on line %d", index+1)
		}
		if err := validateChecksumPath(name); err != nil {
			return nil, fmt.Errorf("invalid SHA256SUMS path on line %d: %w", index+1, err)
		}
		if _, exists := checksums[name]; exists {
			return nil, fmt.Errorf("duplicate SHA256SUMS entry for %s", name)
		}
		checksums[name] = hash
	}
	if len(checksums) == 0 {
		return nil, fmt.Errorf("SHA256SUMS has no entries")
	}
	return checksums, nil
}

func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') {
			continue
		}
		return false
	}
	return true
}

func validateChecksumPath(name string) error {
	if name == "" {
		return fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(name) {
		return fmt.Errorf("absolute paths are not allowed")
	}
	clean := filepath.Clean(name)
	if clean == "." || clean != name || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || clean == ".." {
		return fmt.Errorf("path must be a clean relative file path")
	}
	return nil
}

func sha256File(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("symlink artifacts are not allowed")
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("artifact is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func releaseArtifactSummary(artifactDir string, checksums map[string]string) (map[string]any, error) {
	patterns := map[string]string{
		"binaries": "*-linux-amd64.tar.gz",
		"web":      "assops-web-*.tar.gz",
		"helm":     "assops-*.tgz",
	}
	summary := map[string]any{}
	for key, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(artifactDir, pattern))
		if err != nil {
			return nil, err
		}
		var names []string
		for _, match := range matches {
			info, err := os.Lstat(match)
			if err != nil {
				return nil, fmt.Errorf("checking release artifact %s: %w", match, err)
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				continue
			}
			name := filepath.Base(match)
			if _, ok := checksums[name]; !ok {
				return nil, fmt.Errorf("release artifact %s is missing from SHA256SUMS", name)
			}
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) == 0 {
			return nil, fmt.Errorf("release bundle missing %s artifact matching %s", key, pattern)
		}
		summary[key] = names
	}
	return summary, nil
}

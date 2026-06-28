package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func listManagedBackups(dir string) ([]backupFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("listing backup directory: %w", err)
	}
	var backups []backupFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "assops-") || !strings.HasSuffix(name, ".dump") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("reading backup file info: %w", err)
		}
		backups = append(backups, backupFile{path: filepath.Join(dir, name), name: name, modTime: info.ModTime()})
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].modTime.Equal(backups[j].modTime) {
			return backups[i].name > backups[j].name
		}
		return backups[i].modTime.After(backups[j].modTime)
	})
	return backups, nil
}

func postgresProcessDatabaseURL(raw string) (string, []string, []string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		if strings.Contains(strings.ToLower(raw), "password=") {
			return "", nil, nil, fmt.Errorf("database backup/restore requires URL-style DATABASE_URL or PGPASSWORD; keyword DSNs with password= are not supported")
		}
		return raw, nil, []string{raw}, nil
	}
	password, hasPassword := parsed.User.Password()
	if !hasPassword {
		return raw, nil, []string{raw}, nil
	}
	username := parsed.User.Username()
	if username != "" {
		parsed.User = url.User(username)
	} else {
		parsed.User = nil
	}
	return parsed.String(), []string{"PGPASSWORD=" + password}, []string{raw, password}, nil
}

func runExternalDBTool(ctx context.Context, env, secrets []string, name string, args ...string) error {
	output, err := runExternalDBToolOutput(ctx, env, secrets, name, args...)
	if err != nil {
		return err
	}
	if output != "" {
		fmt.Print(output)
	}
	return nil
}

func runExternalDBToolOutput(ctx context.Context, env, secrets []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	output, err := cmd.CombinedOutput()
	sanitized := sanitizeCommandOutput(string(output), secrets)
	if err != nil {
		if len(output) > 0 {
			return "", fmt.Errorf("%s failed: %s", name, sanitized)
		}
		return "", fmt.Errorf("%s failed: %w", name, err)
	}
	return sanitized, nil
}

func sanitizeCommandOutput(output string, secrets []string) string {
	sanitized := output
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		sanitized = strings.ReplaceAll(sanitized, secret, "<redacted>")
	}
	return sanitized
}

func readContextBrief(root string) error {
	path, err := firstContextFile(root, "ASSOPS_CONTEXT.md")
	if err != nil {
		return err
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fmt.Print(string(bytes))
	return nil
}

func readContextKey(root, key string) error {
	path, err := firstContextFile(root, "assops-context.json")
	if err != nil {
		return err
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var data map[string]any
	if err := json.Unmarshal(bytes, &data); err != nil {
		return err
	}
	return printJSON(data[key])
}

func firstContextFile(root, name string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return err
		}
		if !d.IsDir() && d.Name() == name {
			found = path
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("%s not found under %s", name, root)
	}
	return found, nil
}

type readinessRow struct {
	Key                   string `json:"key"`
	Label                 string `json:"label"`
	Status                string `json:"status"`
	Evidence              any    `json:"evidence"`
	Next                  string `json:"next"`
	DemoDataRehearsalPlan any    `json:"demo_data_rehearsal_plan,omitempty"`
}

func getProjectReadiness(base, token string) error {
	assets, err := getAPIJSON(base, token, "/api/assets")
	if err != nil {
		return err
	}
	operations, err := getAPIJSON(base, token, "/api/operations")
	if err != nil {
		return err
	}
	warnings := []string{}
	approvals, err := getAPIJSON(base, token, "/api/operation-approvals/summary")
	if err != nil {
		warnings = append(warnings, "approval summary unavailable: "+err.Error())
		approvals = map[string]any{}
	}
	graph, err := getAPIJSON(base, token, "/api/assets/graph")
	if err != nil {
		warnings = append(warnings, "asset graph unavailable: "+err.Error())
		graph = map[string]any{}
	} else if !assetGraphPayloadAvailable(graph) {
		warnings = append(warnings, "asset graph response missing nodes or edges")
	}
	report := firstVersionReadinessReportWithGraph(apiItems(assets), apiItems(operations), approvals, graph)
	if len(warnings) > 0 {
		report["warnings"] = warnings
	}
	return printJSON(report)
}

func firstVersionReadinessReport(assets, operations []map[string]any, approvals map[string]any) map[string]any {
	return firstVersionReadinessReportWithGraph(assets, operations, approvals, nil)
}

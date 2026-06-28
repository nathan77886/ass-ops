package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func (e *GitExecutor) hasStagedChanges(ctx context.Context, dir string) (bool, error) {
	runner := e.Runner
	if runner == nil {
		runner = execCommandRunner{}
	}
	_, stderr, err := runner.Run(ctx, dir, "git", "diff", "--cached", "--quiet")
	if err == nil {
		return false, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("git diff --cached --quiet failed: %w: %s", err, strings.TrimSpace(sanitizeGitOutput(stderr)))
}

func (e *GitExecutor) newWorkDir(pattern string) (string, func(), error) {
	base := e.WorkDir
	if base == "" {
		base = os.TempDir()
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", nil, fmt.Errorf("creating git work dir: %w", err)
	}
	dir, err := os.MkdirTemp(base, pattern)
	if err != nil {
		return "", nil, fmt.Errorf("creating git temp dir: %w", err)
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

func remoteURLFromRow(row map[string]any) string {
	if bytes, ok := row["remote_url"].(sql.RawBytes); ok {
		if value := strings.TrimSpace(string(bytes)); value != "" {
			return value
		}
	}
	if bytes, ok := row["remote_url"].([]byte); ok {
		if value := strings.TrimSpace(string(bytes)); value != "" {
			return value
		}
	}
	if value := strings.TrimSpace(fmt.Sprint(row["remote_url"])); value != "" && value != "<nil>" {
		return value
	}
	if urls, ok := row["urls"].([]any); ok {
		for _, url := range urls {
			if value := strings.TrimSpace(fmt.Sprint(url)); value != "" {
				return value
			}
		}
	}
	return ""
}

func stripGitRemoteURLUserinfo(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if strings.Contains(value, "://<redacted>@") {
		return strings.Replace(value, "://<redacted>@", "://", 1), true
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil || parsed.User == nil {
		return value, false
	}
	parsed.User = nil
	return parsed.String(), true
}

func parseLsRemoteTagLookup(stdout, tagName string) (string, int) {
	matchedSHA := ""
	matchedCount := 0
	targetRef := "refs/tags/" + tagName
	peeledTargetRef := targetRef + "^{}"
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		if fields[1] != targetRef && fields[1] != peeledTargetRef {
			continue
		}
		matchedCount++
		if matchedSHA == "" && isFullHexSHA(fields[0]) {
			matchedSHA = fields[0]
		}
	}
	return matchedSHA, matchedCount
}

func sanitizeLookupError(err error) string {
	if err == nil {
		return ""
	}
	return truncateProviderError(sanitizeGitOutput(err.Error()), providerRunErrorLimit)
}

func defaultBranchFromRow(row map[string]any) string {
	branch := strings.TrimSpace(fmt.Sprint(row["default_branch"]))
	if branch == "" || branch == "<nil>" {
		return "main"
	}
	return branch
}

func gitRefsFromInput(input any, defaultBranch string) gitRefs {
	refsMap := mapFromAny(input)
	if nested, ok := refsMap["refs"]; ok {
		refsMap = mapFromAny(nested)
	}
	refs := gitRefs{
		Branches: stringSliceFromAny(refsMap["branches"]),
		Tags:     stringSliceFromAny(refsMap["tags"]),
	}
	if len(refs.Branches) == 0 && len(refs.Tags) == 0 && defaultBranch != "" {
		refs.Branches = []string{defaultBranch}
	}
	return refs
}

func mapFromAny(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	if typed, ok := value.([]byte); ok {
		var decoded map[string]any
		if err := json.Unmarshal(typed, &decoded); err == nil && decoded != nil {
			return decoded
		}
		return map[string]any{}
	}
	if typed, ok := value.(string); ok {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(typed), &decoded); err == nil && decoded != nil {
			return decoded
		}
		return map[string]any{}
	}
	return map[string]any{}
}

func stringSliceFromAny(value any) []string {
	if typed, ok := value.([]string); ok {
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	}
	items, ok := value.([]any)
	if !ok {
		if strings.TrimSpace(fmt.Sprint(value)) == "" || fmt.Sprint(value) == "<nil>" {
			return nil
		}
		return []string{strings.TrimSpace(fmt.Sprint(value))}
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		value := strings.TrimSpace(fmt.Sprint(item))
		if value != "" && value != "<nil>" {
			out = append(out, value)
		}
	}
	return out
}

var safeGitRefPartPattern = regexp.MustCompile(`^[A-Za-z0-9._/\-]+$`)

var fullHexSHAPattern = regexp.MustCompile(`^[a-fA-F0-9]{40}([a-fA-F0-9]{24})?$`)

func isSafeGitRefPart(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.Contains(value, "..") {
		return false
	}
	if strings.Contains(value, "//") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".lock") {
		return false
	}
	return safeGitRefPartPattern.MatchString(value)
}

func isFullHexSHA(value string) bool {
	return fullHexSHAPattern.MatchString(value)
}

func redactGitArgs(args []string) []string {
	redacted := make([]string, len(args))
	copy(redacted, args)
	for i, arg := range redacted {
		if strings.Contains(arg, "://") || strings.Contains(arg, "@") && strings.Contains(arg, ":") {
			redacted[i] = "<remote>"
		}
	}
	return redacted
}

var gitURLPattern = regexp.MustCompile(`(?i)((?:https?|ssh|git)://[^\s'"]+|[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+:[^\s'"]+)`)

func sanitizeGitOutput(output string) string {
	return gitURLPattern.ReplaceAllString(output, "<remote>")
}

func sanitizeGitOutputForLocalPath(output, path string) string {
	output = sanitizeGitOutput(output)
	path = strings.TrimSpace(path)
	if path == "" {
		return output
	}
	if cleanPath, err := filepath.Abs(filepath.Clean(path)); err == nil {
		output = strings.ReplaceAll(output, cleanPath, "<local_bare>")
	}
	return strings.ReplaceAll(output, path, "<local_bare>")
}

func appendGitExecutionOutput(result *gitExecutionResult, stdout, stderr string) {
	if result == nil {
		return
	}
	result.Stdout += stdout
	result.Stderr += stderr
}
